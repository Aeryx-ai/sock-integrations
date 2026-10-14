package test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// Owner-brokered tool confirmations (docs/specs/integrations.md, "Owner-brokered
// tool confirmations (integrations-0ga)").

const (
	cfTool    = "sevt_01TOOLONE"
	cfTool2   = "sevt_01TOOLTWO"
	cfInput   = "CONFIRMINPUTSENTINEL-4a1c"
	cfName    = "bashsentinel"
	cfReason  = "REASONSENTINEL-77"
	cfLater   = "LATERANSWER-91c3"
	cfGone    = "that approval is no longer pending"
	cfNoCode  = "no approval is pending with that code"
	cfFixed   = "the agent is waiting for approval of a tool call"
	cfPending = "a tool call is waiting"
)

var cfCodeRE = regexp.MustCompile(`"approve ([a-km-z2-9]{6})"`)

func toolUse(id, typ, name, perm string) map[string]any {
	e := map[string]any{"type": typ, "id": id, "name": name, "input": map[string]any{"command": cfInput}}
	if typ == "agent.mcp_tool_use" {
		e["mcp_server_name"] = "github"
	}
	if perm != "" {
		e["evaluated_permission"] = perm
	}
	return e
}

// blocked is the idle of a turn that waits on the calls of those ids.
func blocked(ids ...string) map[string]any {
	return map[string]any{"type": "session.status_idle", "id": "evt_idle_ra", "stop_reason": map[string]any{"type": "requires_action", "event_ids": ids}}
}

// reply is owner message n with that text, in this harness's conversation.
func (h *bnHarness) reply(n int, text string) rnOpts {
	id := fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", n)
	o := rnOpts{Env: map[string]string{"SOCK_MESSAGE_ID": id}}
	f := h.fields()
	f["id"] = id
	f["text"] = text
	o.Stdin = rnMessage(f)
	return o
}

// codes are the approval codes the owner was sent, in order.
func (h *bnHarness) codes() []string {
	var out []string
	for _, s := range h.sends() {
		for _, m := range cfCodeRE.FindAllStringSubmatch(s.Text, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// askOnce plays a turn that ends waiting on one bash call and returns its code.
func (h *bnHarness) askOnce() string {
	h.t.Helper()
	h.script(toolUse(cfTool, "agent.tool_use", cfName, "ask"), agentMsg("PARTIAL-text"), blocked(cfTool))
	r := h.run(rnOpts{})
	c := h.codes()
	if r.Code != 0 || len(c) != 1 {
		h.t.Fatalf("ask: exit %d codes %v sends %v stderr %q log %q", r.Code, c, h.sends(), r.Stderr, h.bridgeLog())
	}
	// the session's history, as the API would list it
	prompt := h.sentPrompt()
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{textEvent("user.message", "evt_u1", prompt), toolUse(cfTool, "agent.tool_use", cfName, "ask"), blocked(cfTool)}
	})
	return c[0]
}

// confirmations are the user.tool_confirmation events posted, in order.
func (f *fakeMA) confirmations() []map[string]any {
	var out []map[string]any
	for _, b := range f.sentBodies() {
		var body struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.Unmarshal([]byte(b), &body)
		for _, e := range body.Events {
			if e["type"] == "user.tool_confirmation" {
				out = append(out, e)
			}
		}
	}
	return out
}

func (f *fakeMA) userMessages() int {
	n := 0
	for _, b := range f.sentBodies() {
		n += strings.Count(b, `"user.message"`)
	}
	return n
}

func (h *bnHarness) pendingFile() string {
	b, _ := os.ReadFile(filepath.Join(h.state, "confirm.pending"))
	return string(b)
}

// noCodeLeak fails when the code, the tool name or the reason is in the log,
// or the code in any request the API got.
func (h *bnHarness) noCodeLeak(code string) {
	h.t.Helper()
	for _, bad := range []string{code, cfName, cfReason, cfInput} {
		if strings.Contains(h.bridgeLog(), bad) {
			h.t.Fatalf("the log holds %q: %q", bad, h.bridgeLog())
		}
	}
	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	for _, b := range append(append(append([]string(nil), h.api.sent...), h.api.creates...), h.api.paths...) {
		if strings.Contains(b, code) {
			h.t.Fatalf("the code reached the API: %q", b)
		}
	}
}

func TestConfirmAsksWithACodeAndWithholdsTheInput(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	s := h.sends()
	all := ""
	for _, m := range s {
		all += m.Text
	}
	if !strings.Contains(all, "the agent wants to run "+cfName) || !strings.Contains(all, `"deny `+code) || !strings.Contains(all, "PARTIAL-text") {
		t.Fatalf("posted %q", all)
	}
	if strings.Contains(all, cfInput) {
		t.Fatal("the tool input reached the owner")
	}
	p := filepath.Join(h.state, "confirm.pending")
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("confirm.pending: %v %v", st, err)
	}
	if f := h.pendingFile(); f != code+" "+rnConvID+" "+bnSession+" "+cfTool+" pending - "+cfName+" ?\n" {
		t.Fatalf("confirm.pending %q", f)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Fatal("confirm.pending.tmp left behind")
	}
	if !strings.Contains(h.bridgeLog(), "approval requested") {
		t.Fatalf("log %q", h.bridgeLog())
	}
	h.noCodeLeak(code)
}

func TestConfirmCodesAreFreshAndInTheAlphabet(t *testing.T) {
	// two calls of one turn, then three fresh asks: all five codes differ
	h := newBridgeHarness(t)
	h.script(toolUse(cfTool, "agent.tool_use", cfName, "ask"), toolUse(cfTool2, "agent.tool_use", cfName, "ask"), blocked(cfTool, cfTool2))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	codes := h.codes()
	for range 3 {
		codes = append(codes, newBridgeHarnessNoParallel(t).askOnce())
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if !regexp.MustCompile(`\A[a-km-z2-9]{6}\z`).MatchString(c) || seen[c] {
			t.Fatalf("codes %v", codes)
		}
		seen[c] = true
	}
	if len(codes) != 5 {
		t.Fatalf("codes %v", codes)
	}
}

func TestConfirmApproveSendsAllowAndFollowsTheTurn(t *testing.T) {
	for _, noEcho := range []bool{false, true} {
		t.Run(map[bool]string{false: "echoed", true: "not echoed"}[noEcho], func(t *testing.T) {
			h := newBridgeHarness(t)
			code := h.askOnce()
			h.api.set(func(f *fakeMA) { f.noEcho = noEcho })
			h.script(agentMsg(cfLater), idle("end_turn"))
			stop := make(chan struct{})
			if noEcho {
				// the stream cannot place the turn: once the confirmation is
				// posted, the history holds it and the answer after it
				go func() {
					for len(h.api.confirmations()) == 0 {
						select {
						case <-stop:
							return
						case <-time.After(20 * time.Millisecond):
						}
					}
					h.api.set(func(f *fakeMA) {
						f.history = append(f.history,
							map[string]any{"type": "user.tool_confirmation", "id": "evt_c1", "tool_use_id": cfTool, "result": "allow"},
							textEvent("agent.message", "evt_l", cfLater),
							map[string]any{"type": "session.status_idle", "id": "evt_i9", "stop_reason": map[string]any{"type": "end_turn"}})
					})
				}()
			}
			before := len(h.sends())
			o := h.reply(2, "  Approve "+code+"\n")
			r := h.run(o)
			close(stop)
			c := h.api.confirmations()
			if r.Code != 0 || len(c) != 1 || c[0]["tool_use_id"] != cfTool || c[0]["result"] != "allow" {
				t.Fatalf("exit %d confirmations %v stderr %q log %q", r.Code, c, r.Stderr, h.bridgeLog())
			}
			if _, ok := c[0]["deny_message"]; ok {
				t.Fatalf("an allow carries a deny_message: %v", c[0])
			}
			s := h.sends()
			if len(s) != before+1 || !strings.Contains(s[before].Text, cfLater) {
				t.Fatalf("sends %v", s[before:])
			}
			if h.api.userMessages() != 1 {
				t.Fatalf("%d user.messages", h.api.userMessages())
			}
			if f := h.pendingFile(); f != "" {
				t.Fatalf("confirm.pending still holds %q", f)
			}
			// a redelivery is a duplicate, a new message with the used code is refused
			if r := h.run(o); r.Code != 0 || len(h.api.confirmations()) != 1 || !strings.Contains(h.bridgeLog(), "duplicate") {
				t.Fatalf("redelivery: exit %d, %d confirmations", r.Code, len(h.api.confirmations()))
			}
			if r := h.run(h.reply(3, "approve "+code)); r.Code != 0 || len(h.api.confirmations()) != 1 || h.sends()[len(h.sends())-1].Text != cfNoCode {
				t.Fatalf("reused code: exit %d, %d confirmations, sends %v", r.Code, len(h.api.confirmations()), h.sends())
			}
			h.noCodeLeak(code)
		})
	}
}

func TestConfirmDenySendsACleanedCutReason(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	h.script(agentMsg(cfLater), idle("end_turn"))
	reason := cfReason + " not " + code + " \x1b[31mred\x07 \u202eflip\u2066 " + strings.Repeat("é", 1500)
	if r := h.run(h.reply(2, "deny "+code+"  "+reason)); r.Code != 0 {
		t.Fatalf("exit %d stderr %q log %q", r.Code, r.Stderr, h.bridgeLog())
	}
	c := h.api.confirmations()
	if len(c) != 1 || c[0]["result"] != "deny" || c[0]["tool_use_id"] != cfTool {
		t.Fatalf("confirmations %v", c)
	}
	m, _ := c[0]["deny_message"].(string)
	if !strings.HasPrefix(m, cfReason+" not ") || len(m) > 1000 || !utf8.ValidString(m) {
		t.Fatalf("deny_message %d bytes %q", len(m), m[:min(len(m), 80)])
	}
	for _, bad := range []string{"\x1b", "\x07", "\u202e", "\u2066", code} {
		if strings.Contains(m, bad) {
			t.Fatalf("deny_message holds %q", bad)
		}
	}
	h.noCodeLeak(code)
}

func TestConfirmDenyWithoutAReasonSendsNoMessage(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	h.script(idle("end_turn"))
	if r := h.run(h.reply(2, "DENY "+code)); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	c := h.api.confirmations()
	if len(c) != 1 || c[0]["result"] != "deny" {
		t.Fatalf("confirmations %v", c)
	}
	if _, ok := c[0]["deny_message"]; ok {
		t.Fatalf("deny_message without a reason: %v", c[0])
	}
}

func TestConfirmOnlyTheOwnerApproves(t *testing.T) {
	for name, sender := range map[string]string{"another account": rnOther, "the bridge puppet": bnPuppet} {
		t.Run(name, func(t *testing.T) {
			h := newBridgeHarness(t)
			code := h.askOnce()
			o := h.reply(2, "approve "+code)
			o.Env["SOCK_SENDER_ID"] = sender
			f := h.fields()
			f["id"] = o.Env["SOCK_MESSAGE_ID"]
			f["senderId"] = sender
			f["text"] = "approve " + code
			o.Stdin = rnMessage(f)
			if r := h.run(o); r.Code != 0 || len(h.api.sentBodies()) != 1 || !strings.Contains(h.bridgeLog(), "dropped: sender") {
				t.Fatalf("exit %d, %d posts, log %q", r.Code, len(h.api.sentBodies()), h.bridgeLog())
			}
			if !strings.Contains(h.pendingFile(), code+" ") {
				t.Fatal("the code is gone")
			}
		})
	}
}

func TestConfirmLookAlikesApproveNothing(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	up := strings.ToUpper(code)
	for i, text := range []string{
		"please approve " + code,
		"approve " + code + " now",
		"approve " + code + " please",
		"> approve " + code,
		"approve  " + code,
		"approve\t" + code,
		"approve " + code + "\napprove " + code,
		"\"approve " + code + "\"",
		"approve " + up,
		"аpprove " + code, // Cyrillic a
		"approve" + code,
		"approves " + code,
		"yes, approve it",
		"approve",
		"approve " + code[:5],
		"I said: reply \"approve " + code + "\" or \"deny " + code + " <optional reason>\"",
	} {
		before := len(h.sends())
		if r := h.run(h.reply(10+i, text)); r.Code != 0 {
			t.Fatalf("%q: exit %d", text, r.Code)
		}
		if len(h.api.confirmations()) != 0 || h.api.userMessages() != 1 {
			t.Fatalf("%q: %d confirmations, %d user.messages", text, len(h.api.confirmations()), h.api.userMessages())
		}
		if s := h.sends(); len(s) != before+1 || !strings.HasPrefix(s[before].Text, cfPending) || !strings.Contains(s[before].Text, code) {
			t.Fatalf("%q: sends %v", text, s[before:])
		}
	}
	if !strings.Contains(h.pendingFile(), code+" ") || !strings.Contains(h.bridgeLog(), "approval pending") {
		t.Fatalf("pending %q log %q", h.pendingFile(), h.bridgeLog())
	}
	h.noCodeLeak(code)
}

func TestConfirmAWrongCodeIsNotPending(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	wrong := "zzzzzz"
	if code == wrong {
		wrong = "yyyyyy"
	}
	if r := h.run(h.reply(2, "approve "+wrong)); r.Code != 0 || len(h.api.confirmations()) != 0 || h.sends()[len(h.sends())-1].Text != cfNoCode {
		t.Fatalf("exit %d sends %v", r.Code, h.sends())
	}
	if !strings.Contains(h.bridgeLog(), "approval not pending") {
		t.Fatalf("log %q", h.bridgeLog())
	}
}

func TestConfirmACodeFromAnotherConversationIsRefused(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	o := h.inConv(2, bnConv2)
	f := h.fields()
	f["id"] = o.Env["SOCK_MESSAGE_ID"]
	f["conversationId"] = bnConv2
	f["text"] = "approve " + code
	o.Stdin = rnMessage(f)
	if r := h.run(o); r.Code != 0 || len(h.api.confirmations()) != 0 || h.sends()[len(h.sends())-1].Text != cfNoCode {
		t.Fatalf("exit %d sends %v", r.Code, h.sends())
	}
	if !strings.Contains(h.pendingFile(), code+" ") {
		t.Fatal("the code is gone")
	}
}

// The session is waiting on a call: an ordinary reply is not sent into it
func TestConfirmAnOrdinaryReplyWhilePendingIsHeld(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{toolUse(cfTool, "agent.tool_use", cfName, "ask"), blocked(cfTool)}
	})
	if r := h.run(h.withID(2, rnOpts{})); r.Code != 0 || h.api.userMessages() != 1 {
		t.Fatalf("exit %d, %d user.messages", r.Code, h.api.userMessages())
	}
	s := h.sends()
	if last := s[len(s)-1].Text; !strings.HasPrefix(last, cfPending) || !strings.Contains(last, `"approve `+code+`"`) {
		t.Fatalf("posted %q", last)
	}
}

// Answered in the console: the history shows the session moved on, the lines
// are dropped and the reply goes in
func TestConfirmStaleLinesDoNotHoldReplies(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{
			toolUse(cfTool, "agent.tool_use", cfName, "ask"), blocked(cfTool),
			{"type": "user.tool_confirmation", "id": "evt_console", "tool_use_id": cfTool, "result": "allow"},
			{"type": "session.status_running", "id": "evt_run"},
			{"type": "session.status_idle", "id": "evt_i2", "stop_reason": map[string]any{"type": "end_turn"}},
		}
	})
	h.script(agentMsg(cfLater), idle("end_turn"))
	if r := h.run(h.withID(2, rnOpts{})); r.Code != 0 || h.api.userMessages() != 2 {
		t.Fatalf("exit %d, %d user.messages, log %q", r.Code, h.api.userMessages(), h.bridgeLog())
	}
	if strings.Contains(h.pendingFile(), code) {
		t.Fatalf("stale line kept: %q", h.pendingFile())
	}
	if r := h.run(h.reply(3, "approve "+code)); r.Code != 0 || len(h.api.confirmations()) != 0 {
		t.Fatalf("exit %d", r.Code)
	}
}

func TestConfirmAnsweredElsewhereIsNotPosted(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{{"type": "user.tool_confirmation", "id": "evt_console", "tool_use_id": cfTool, "result": "deny"}}
	})
	if r := h.run(h.reply(2, "approve "+code)); r.Code != 0 || len(h.api.confirmations()) != 0 || h.sends()[len(h.sends())-1].Text != cfGone {
		t.Fatalf("exit %d sends %v", r.Code, h.sends())
	}
	if h.pendingFile() != "" {
		t.Fatalf("pending %q", h.pendingFile())
	}
}

func TestConfirmA400DropsTheLine(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	h.api.set(func(f *fakeMA) { f.status["send"] = []int{400} })
	r := h.run(h.reply(2, "approve "+code))
	if r.Code != 0 || h.sends()[len(h.sends())-1].Text != cfGone || h.pendingFile() != "" || !strings.Contains(h.bridgeLog(), "approval refused") {
		t.Fatalf("exit %d sends %v pending %q log %q", r.Code, h.sends(), h.pendingFile(), h.bridgeLog())
	}
	if n := strings.Count(strings.Join(h.api.kinds(), " "), "send"); n != 2 {
		t.Fatalf("%d posts", n)
	}
}

// A confirmation that may have landed is not posted again: the retry finds it
// in the history and finishes from there
func TestConfirmACrashAfterThePostRepeatsNothing(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	h.api.set(func(f *fakeMA) { f.dropSend = 1 })
	o := h.reply(2, "approve "+code)
	if r := h.run(o); r.Code != 1 || len(h.api.confirmations()) != 1 {
		t.Fatalf("first: exit %d, %d confirmations", r.Code, len(h.api.confirmations()))
	}
	if !strings.Contains(h.pendingFile(), " sent ") {
		t.Fatalf("not marked sent: %q", h.pendingFile())
	}
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{
			{"type": "user.tool_confirmation", "id": "evt_c1", "tool_use_id": cfTool, "result": "allow"},
			textEvent("agent.message", "evt_l", cfLater),
			{"type": "session.status_idle", "id": "evt_i9", "stop_reason": map[string]any{"type": "end_turn"}},
		}
	})
	before := len(h.sends())
	if r := h.run(o); r.Code != 0 || len(h.api.confirmations()) != 1 {
		t.Fatalf("retry: exit %d, %d confirmations, log %q", r.Code, len(h.api.confirmations()), h.bridgeLog())
	}
	if s := h.sends(); len(s) != before+1 || !strings.Contains(s[before].Text, cfLater) || h.pendingFile() != "" {
		t.Fatalf("sends %v pending %q", s[before:], h.pendingFile())
	}
	// the code, marked sent for that message, approves nothing for another
	h.noCodeLeak(code)
}

func TestConfirmASentCodeIsBoundToItsMessage(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	h.api.set(func(f *fakeMA) { f.dropSend = 1 })
	if r := h.run(h.reply(2, "approve "+code)); r.Code != 1 {
		t.Fatalf("exit %d", r.Code)
	}
	if r := h.run(h.reply(3, "approve "+code)); r.Code != 0 || len(h.api.confirmations()) != 1 || h.sends()[len(h.sends())-1].Text != cfNoCode {
		t.Fatalf("exit %d, %d confirmations, sends %v", r.Code, len(h.api.confirmations()), h.sends())
	}
}

func TestConfirmTwoCallsWaitForBoth(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(toolUse(cfTool, "agent.tool_use", cfName, "ask"), toolUse(cfTool2, "agent.mcp_tool_use", "create_issue", "ask"), blocked(cfTool, cfTool2))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	c := h.codes()
	if len(c) != 2 || c[0] == c[1] || !strings.Contains(h.sends()[0].Text, "the agent wants to run create_issue from github") {
		t.Fatalf("codes %v sends %v", c, h.sends())
	}
	// the session re-emits its idle with the call still waiting
	h.script(blocked(cfTool2))
	if r := h.run(h.reply(2, "approve "+c[0])); r.Code != 0 {
		t.Fatalf("exit %d log %q", r.Code, h.bridgeLog())
	}
	if last := h.sends()[len(h.sends())-1].Text; last != "recorded; 1 more waiting" {
		t.Fatalf("posted %q", last)
	}
	if p := h.pendingFile(); strings.Contains(p, c[0]) || !strings.Contains(p, c[1]+" ") {
		t.Fatalf("pending %q", p)
	}
	h.script(agentMsg(cfLater), idle("end_turn"))
	if r := h.run(h.reply(3, "approve "+c[1])); r.Code != 0 || !strings.Contains(h.sends()[len(h.sends())-1].Text, cfLater) {
		t.Fatalf("exit %d sends %v", r.Code, h.sends())
	}
	if cf := h.api.confirmations(); len(cf) != 2 || cf[0]["tool_use_id"] != cfTool || cf[1]["tool_use_id"] != cfTool2 || h.pendingFile() != "" {
		t.Fatalf("confirmations %v pending %q", cf, h.pendingFile())
	}
}

func TestConfirmWhatTheBridgeCannotAnswerKeepsTheFixedText(t *testing.T) {
	for name, evs := range map[string][]any{
		"custom tool":     {toolUse(cfTool, "agent.tool_use", cfName, "ask"), toolUse(cfTool2, "agent.custom_tool_use", "mine", ""), blocked(cfTool, cfTool2)},
		"unseen id":       {toolUse(cfTool, "agent.tool_use", cfName, "ask"), blocked(cfTool, "sevt_01NEVERSEEN")},
		"not ask":         {toolUse(cfTool, "agent.tool_use", cfName, "allow"), blocked(cfTool)},
		"odd id":          {toolUse("sevt odd", "agent.tool_use", cfName, "ask"), blocked("sevt odd")},
		"long id":         {toolUse(strings.Repeat("s", 129), "agent.tool_use", cfName, "ask"), blocked(strings.Repeat("s", 129))},
		"no event ids":    {toolUse(cfTool, "agent.tool_use", cfName, "ask"), blocked()},
		"before the turn": {blocked(cfTool)},
	} {
		t.Run(name, func(t *testing.T) {
			h := newBridgeHarness(t)
			if name == "before the turn" {
				h.api.set(func(f *fakeMA) { f.pre = []any{toolUse(cfTool, "agent.tool_use", cfName, "ask")} })
			}
			h.script(evs...)
			r := h.run(rnOpts{})
			if s := h.sends(); r.Code != 0 || len(s) != 1 || s[0].Text != cfFixed || h.pendingFile() != "" {
				t.Fatalf("exit %d sends %v pending %q", r.Code, s, h.pendingFile())
			}
		})
	}
}

func TestConfirmAnOddToolNameShowsAsATool(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(toolUse(cfTool, "agent.tool_use", "rm -rf /\x1b[2J", "ask"), blocked(cfTool))
	if r := h.run(rnOpts{}); r.Code != 0 || len(h.codes()) != 1 {
		t.Fatalf("exit %d", r.Code)
	}
	if s := h.sends()[0].Text; !strings.Contains(s, "the agent wants to run a tool:") || strings.Contains(s, "rm -rf") {
		t.Fatalf("posted %q", s)
	}
}

// Asked from history: a turn whose stream dropped asks when the retry reads it
func TestConfirmAsksFromHistory(t *testing.T) {
	h := newBridgeHarness(t)
	h.dropThenHistory(toolUse(cfTool, "agent.tool_use", cfName, "ask"), blocked(cfTool))
	if r := h.run(rnOpts{}); r.Code != 0 || len(h.codes()) != 1 || !strings.Contains(h.pendingFile(), cfTool+" pending") {
		t.Fatalf("exit %d sends %v pending %q", r.Code, h.sends(), h.pendingFile())
	}
}

func TestConfirmPendingHoldsAtMostFiftyLines(t *testing.T) {
	h := newBridgeHarness(t)
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := range 60 {
		b.WriteString("aaaaa" + string(rune('a'+i%10)) + " " + bnConv2 + " sesn_OTHER sevt_old" + string(rune('a'+i%26)) + " pending - x ?\n")
	}
	if err := os.WriteFile(filepath.Join(h.state, "confirm.pending"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	code := h.askOnce()
	p := h.pendingFile()
	if n := strings.Count(p, "\n"); n > 50 || !strings.Contains(p, code+" "+rnConvID) {
		t.Fatalf("%d lines, new line kept: %v", n, strings.Contains(p, code))
	}
}

// awk compares strings that read as numbers as numbers: 2e3333 and 9e9999 are
// both infinite. A code is matched as text.
func TestConfirmCodesMatchAsTextNotNumbers(t *testing.T) {
	h := newBridgeHarness(t)
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	line := "2e3333 " + rnConvID + " " + bnSession + " " + cfTool + " pending - " + cfName + " ?\n"
	if err := os.WriteFile(filepath.Join(h.state, "confirm.pending"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := h.run(h.reply(2, "approve 9e9999")); r.Code != 0 || len(h.api.confirmations()) != 0 || h.sends()[len(h.sends())-1].Text != cfNoCode {
		t.Fatalf("exit %d, %d confirmations, sends %v", r.Code, len(h.api.confirmations()), h.sends())
	}
}

// A call id is matched as text too: 10 and 1e1 are one number to awk
func TestConfirmCallIDsMatchAsTextNotNumbers(t *testing.T) {
	h := newBridgeHarness(t)
	code := h.askOnce()
	old := "gggggg " + rnConvID + " " + bnSession + " 10 pending - " + cfName + " ?\n"
	if err := os.WriteFile(filepath.Join(h.state, "confirm.pending"), []byte(h.pendingFile()+old), 0o600); err != nil {
		t.Fatal(err)
	}
	// the approved call ran, and the agent now waits on a new call 1e1
	h.script(toolUse("1e1", "agent.tool_use", cfName, "ask"), blocked("1e1"))
	if r := h.run(h.reply(2, "approve "+code)); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	last := h.sends()[len(h.sends())-1].Text
	if c := cfCodeRE.FindAllStringSubmatch(last, -1); len(c) != 1 || c[0][1] == "gggggg" || !strings.Contains(h.pendingFile(), " 1e1 pending") || strings.Contains(h.pendingFile(), " 10 ") {
		t.Fatalf("posted %q pending %q", last, h.pendingFile())
	}
}

// L1: the requests go first, in a message of their own, so no cut of the
// agent's text can drop them and they cannot be mistaken for the agent's text
func TestConfirmRequestsComeFirstInTheirOwnMessage(t *testing.T) {
	h := newBridgeHarness(t)
	var paras []string
	for range 7 {
		paras = append(paras, strings.Repeat("x", 30001))
	}
	h.script(toolUse(cfTool, "agent.tool_use", cfName, "ask"), agentMsg(strings.Join(paras, "\n\n")), blocked(cfTool))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	s := h.sends()
	if len(s) < 2 || len(s) > 5 || !strings.HasPrefix(s[0].Text, "the agent wants to run "+cfName) || strings.Contains(s[0].Text, "xxx") || len(h.codes()) != 1 {
		t.Fatalf("%d sends, first %q, codes %v", len(s), s[0].Text[:min(len(s[0].Text), 120)], h.codes())
	}
	if !strings.Contains(s[1].Text, "xxx") {
		t.Fatal("the agent's text does not follow the requests")
	}
}

// fold is s lower-cased with the format characters a reader cannot see taken out
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// L2: a deny reason loses every pending code, in any case and through format
// characters
func TestConfirmDenyScrubsCodesInAnyCaseAndThroughFormatCharacters(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(toolUse(cfTool, "agent.tool_use", cfName, "ask"), toolUse(cfTool2, "agent.tool_use", cfName, "ask"), blocked(cfTool, cfTool2))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	c := h.codes()
	h.script(blocked(cfTool2))
	o := c[1]
	reason := "upper " + strings.ToUpper(o) + " zwsp " + o[:3] + "\u200b" + o[3:] + " soft " + o[:2] + "\u00ad" + o[2:] + " tag " + o[:1] + "\U000e0041" + o[1:] + " plain " + o
	if r := h.run(h.reply(2, "deny "+c[0]+" "+reason)); r.Code != 0 {
		t.Fatalf("exit %d log %q", r.Code, h.bridgeLog())
	}
	cf := h.api.confirmations()
	m, _ := cf[0]["deny_message"].(string)
	if len(cf) != 1 || strings.Contains(fold(m), o) || !strings.HasPrefix(m, "upper [code] zwsp [code]") {
		t.Fatalf("deny_message %q", m)
	}
}

// L3: an ordinary reply that quotes a pending code, in another conversation
// whose session takes it, carries no code into that session
func TestConfirmAnOrdinaryReplyCarriesNoCode(t *testing.T) {
	h := newConvHarness(t)
	code := h.askOnce()
	o := h.inConv(2, bnConv2)
	f := h.fields()
	f["id"] = o.Env["SOCK_MESSAGE_ID"]
	f["conversationId"] = bnConv2
	f["text"] = "approve " + code + " please, " + strings.ToUpper(code) + " and " + code[:3] + "\u200b" + code[3:]
	o.Stdin = rnMessage(f)
	h.script(agentMsg(cfLater), idle("end_turn"))
	if r := h.run(o); r.Code != 0 || h.api.userMessages() != 2 {
		t.Fatalf("exit %d, %d user.messages, log %q", r.Code, h.api.userMessages(), h.bridgeLog())
	}
	b := h.api.sentBodies()
	var body struct {
		Events []struct{ Content []struct{ Text string } }
	}
	mustJSON(t, []byte(b[len(b)-1]), &body)
	text := body.Events[0].Content[0].Text
	if strings.Contains(fold(text), code) || strings.Count(text, "[code]") != 3 {
		t.Fatalf("user.message %q", text)
	}
}

// L4: a code is never in any process's argv, where other local users can read it
func TestConfirmCodesAreInNoArgv(t *testing.T) {
	h := newBridgeHarness(t)
	for _, name := range []string{"awk", "sed", "jq", "grep", "tr", "head", "tail", "cut", "cat", "mv", "chmod", "curl", "date", "find", "wc", "rm", "mkdir", "sleep", "seq", "ps", "readlink", "dirname", "ln"} {
		w := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done >> \"$STUB_DIR/argvlog\"\nPATH=" + rnPathTail + "\nexport PATH\nexec " + name + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(h.stubs, name), []byte(w), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.script(toolUse(cfTool, "agent.tool_use", cfName, "ask"), toolUse(cfTool2, "agent.tool_use", cfName, "ask"), blocked(cfTool, cfTool2))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	c := h.codes()
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{toolUse(cfTool, "agent.tool_use", cfName, "ask"), toolUse(cfTool2, "agent.tool_use", cfName, "ask"), blocked(cfTool, cfTool2)}
	})
	h.run(h.reply(2, "hello "+c[0]))
	h.script(blocked(cfTool2))
	h.run(h.reply(3, "deny "+c[0]+" not "+c[1]))
	h.script(idle("end_turn"))
	h.run(h.reply(4, "approve "+c[1]))
	if len(h.api.confirmations()) != 2 {
		t.Fatalf("%d confirmations, log %q", len(h.api.confirmations()), h.bridgeLog())
	}
	argv := string(readFile(t, filepath.Join(h.rec, "argvlog")))
	ents, _ := os.ReadDir(h.rec)
	for _, e := range ents {
		if b, err := os.ReadFile(filepath.Join(h.rec, e.Name(), "argv")); err == nil {
			argv += string(b)
		}
	}
	for _, code := range c {
		if strings.Contains(argv, code) {
			t.Fatalf("code %s is in an argv", code)
		}
	}
}

// L5: the bound never evicts a sent line or a call of the current turn; calls
// past it are not asked, and the owner is told to answer them in the console
func TestConfirmTheBoundEvictsNoSentLine(t *testing.T) {
	h := newBridgeHarness(t)
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	sent := "sssss2 " + bnConv2 + " sesn_OTHER sevt_sent sent aaaaaaaa-aaaa-4aaa-8aaa-000000000099 x ?\n"
	old := ""
	for i := range 3 {
		old += "oooo" + string(rune('a'+i)) + "2 " + bnConv2 + " sesn_OTHER sevt_old" + string(rune('a'+i)) + " pending - x ?\n"
	}
	if err := os.WriteFile(filepath.Join(h.state, "confirm.pending"), []byte(sent+old), 0o600); err != nil {
		t.Fatal(err)
	}
	var evs []any
	var ids []string
	for i := range 55 {
		id := "sevt_many" + strconv.Itoa(i)
		evs = append(evs, toolUse(id, "agent.tool_use", cfName, "ask"))
		ids = append(ids, id)
	}
	h.script(append(evs, blocked(ids...))...)
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	p := h.pendingFile()
	if n := strings.Count(p, "\n"); n != 50 || !strings.Contains(p, sent) || strings.Contains(p, "sevt_olda") {
		t.Fatalf("%d lines, sent kept %v: %q", n, strings.Contains(p, sent), p)
	}
	if c := h.codes(); len(c) != 49 {
		t.Fatalf("%d codes", len(c))
	}
	if !strings.Contains(h.sends()[0].Text, "6 more tool calls wait for approval: answer them in the Managed Agents console") {
		t.Fatalf("posted %q", h.sends()[0].Text[len(h.sends()[0].Text)-200:])
	}
}

// L6: an id listed twice is one call with one code
func TestConfirmDuplicateEventIDsGetOneCode(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(toolUse(cfTool, "agent.tool_use", cfName, "ask"), blocked(cfTool, cfTool))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	if c := h.codes(); len(c) != 1 || strings.Count(h.pendingFile(), "\n") != 1 {
		t.Fatalf("codes %v pending %q", c, h.pendingFile())
	}
}
