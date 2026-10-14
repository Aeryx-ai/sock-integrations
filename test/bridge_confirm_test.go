package test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	bcToolID    = "toolu_01CONFIRM"
	bcToolInput = "TOOLSECRET-7d21-must-not-reach-the-owner"
)

var bcCodeRe = regexp.MustCompile(`allow ([0-9a-f]{6}) or deny`)

func toolUse(id, name string) map[string]any {
	return map[string]any{"type": "agent.tool_use", "id": id, "name": name, "input": map[string]any{"cmd": bcToolInput}, "evaluated_permission": "ask"}
}

func requiresAction(ids ...string) map[string]any {
	return map[string]any{"type": "session.status_idle", "id": "evt_idle_ra", "stop_reason": map[string]any{"type": "requires_action", "event_ids": ids}}
}

// answer runs the bridge on a second owner message with the given text.
func (h *bnHarness) answer(text string) rnRes {
	h.answers++
	id := fmt.Sprintf("bbbbbbbb-bbbb-4bbb-8bbb-%012d", h.answers)
	f := h.fields()
	f["id"] = id
	f["text"] = text
	return h.run(rnOpts{Env: map[string]string{"SOCK_MESSAGE_ID": id}, Stdin: rnMessage(f)})
}

// asked runs the first message until the session blocks, and returns the code.
func (h *bnHarness) asked(t *testing.T) string {
	t.Helper()
	h.script(toolUse(bcToolID, "bash"), requiresAction(bcToolID))
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 {
		t.Fatalf("first run: exit %d, sends %v, log %q", r.Code, s, h.bridgeLog())
	}
	m := bcCodeRe.FindStringSubmatch(s[0].Text)
	if m == nil || !strings.Contains(s[0].Text, "the agent wants to run bash") {
		t.Fatalf("question %q", s[0].Text)
	}
	if strings.Contains(s[0].Text, bcToolInput) {
		t.Fatalf("the tool's input reached the owner: %q", s[0].Text)
	}
	return m[1]
}

func confirmations(t *testing.T, bodies []string) []map[string]string {
	t.Helper()
	var out []map[string]string
	for _, b := range bodies {
		var v struct {
			Events []map[string]any `json:"events"`
		}
		if err := json.Unmarshal([]byte(b), &v); err != nil {
			t.Fatal(err)
		}
		for _, e := range v.Events {
			if e["type"] == "user.tool_confirmation" {
				out = append(out, map[string]string{"id": fmt.Sprint(e["tool_use_id"]), "result": fmt.Sprint(e["result"])})
			}
		}
	}
	return out
}

func TestBridgeAsksTheOwnerAboutABlockedToolCall(t *testing.T) {
	h := newBridgeHarness(t)
	h.asked(t)
	if _, err := os.Stat(filepath.Join(h.state, "pending."+bnSession)); err != nil {
		t.Fatalf("no pending file: %v", err)
	}
}

func TestBridgeAllowSendsTheConfirmationAndRelaysTheAnswer(t *testing.T) {
	for _, verb := range []string{"allow", "deny", "  ALLOW  ", "Deny"} {
		t.Run(strings.TrimSpace(verb), func(t *testing.T) {
			h := newBridgeHarness(t)
			code := h.asked(t)
			h.script(agentMsg(bnAnswer), idle("end_turn"))
			r := h.answer(verb + " " + strings.ToUpper(code))
			s := h.sends()
			if r.Code != 0 || len(s) != 2 || !strings.Contains(s[1].Text, "ANSWERSENTINEL") {
				t.Fatalf("exit %d, sends %v, log %q", r.Code, s, h.bridgeLog())
			}
			c := confirmations(t, h.api.sentBodies())
			want := strings.ToLower(strings.TrimSpace(verb))
			if len(c) != 1 || c[0]["id"] != bcToolID || c[0]["result"] != want {
				t.Fatalf("confirmations %v, want %s", c, want)
			}
			if n := len(h.api.sentBodies()); n != 2 {
				t.Fatalf("%d posts: the decision must not be sent as a user.message", n)
			}
			h.noKeyLeak(r)
		})
	}
}

func TestBridgeAnsweredQuestionIsNotAskedAgain(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.asked(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	h.answer("allow " + code)
	h.api.set(func(f *fakeMA) { f.events = nil })
	r := h.answer("allow " + code) // a second answer to the same code
	if s := h.sends(); r.Code != 0 || !strings.Contains(s[len(s)-1].Text, "nothing is waiting") {
		t.Fatalf("exit %d, last send %v, stderr %q log %q", r.Code, s[len(s)-1].Text, r.Stderr, h.bridgeLog())
	}
	if len(confirmations(t, h.api.sentBodies())) != 1 {
		t.Fatal("the confirmation was sent twice")
	}
}

func TestBridgeOtherTextWhileAskingIsNotForwarded(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.asked(t)
	before := len(h.api.sentBodies())
	r := h.answer("please also delete everything")
	s := h.sends()
	if r.Code != 0 || len(s) != 2 || !strings.Contains(s[1].Text, "waiting for your approval") || !strings.Contains(s[1].Text, code) {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
	if len(h.api.sentBodies()) != before {
		t.Fatal("the text was forwarded to the session")
	}
}

func TestBridgeUnknownCodeChangesNothing(t *testing.T) {
	h := newBridgeHarness(t)
	h.asked(t)
	before := len(h.api.sentBodies())
	r := h.answer("allow 000000")
	s := h.sends()
	if r.Code != 0 || len(s) != 2 || !strings.Contains(s[1].Text, "nothing is waiting for approval under 000000") || len(h.api.sentBodies()) != before {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
}

func TestBridgeAllowWithNothingPending(t *testing.T) {
	h := newBridgeHarness(t)
	r := h.run(rnOpts{Stdin: rnMessage(func() map[string]any { f := h.fields(); f["text"] = "allow abcdef"; return f }())})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 || !strings.Contains(s[0].Text, "nothing is waiting") || len(h.api.kinds()) != 0 {
		t.Fatalf("exit %d, sends %v, api %v", r.Code, s, h.api.kinds())
	}
}

func TestBridgeBlockedCallWithoutAToolEventStillAsks(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(requiresAction("toolu_01UNSEEN"))
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 || !strings.Contains(s[0].Text, "the agent wants to run a tool") {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
}

func TestBridgeToolNameIsSanitizedBeforeTheOwnerSeesIt(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(toolUse(bcToolID, "bash\x1b[31m; rm -rf / $(x)"), requiresAction(bcToolID))
	h.run(rnOpts{})
	s := h.sends()
	if len(s) != 1 || strings.ContainsAny(s[0].Text, "\x1b$();") {
		t.Fatalf("sends %q", s)
	}
}

// the redelivery of the first message finds the blocked turn in the history
func TestBridgeRedeliveryOfABlockedTurnAsksAgain(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(toolUse(bcToolID, "bash")) // the stream ends before an idle: exit 1
	if r := h.run(rnOpts{}); r.Code != 1 {
		t.Fatalf("first run: exit %d", r.Code)
	}
	tag := `id="` + rnMsgID + `"`
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{
			{"type": "user.message", "id": "evt_u1", "content": []any{map[string]any{"type": "text", "text": "<sock-message " + tag + ">x</sock-message>"}}},
			toolUse(bcToolID, "bash"),
			requiresAction(bcToolID),
		}
	})
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 || !bcCodeRe.MatchString(s[0].Text) || !strings.Contains(s[0].Text, "run bash") {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
}
