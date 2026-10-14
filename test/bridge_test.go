package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (h *bnHarness) script(events ...any) {
	h.api.set(func(f *fakeMA) { f.events = append(f.events, events) })
}

func TestBridgeOpensTheStreamBeforeItSendsAndSendsTheOwnersReply(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	if k := h.api.kinds(); len(k) < 2 || k[0] != "stream" || k[1] != "send" {
		t.Fatalf("requests %v: the stream must be open before the send", k)
	}
	for i, want := range map[int]string{0: "stream", 1: "send"} {
		hd := h.api.headers[i]
		if hd.Get("X-Api-Key") != bnKey || hd.Get("Anthropic-Version") != "2023-06-01" || hd.Get("Anthropic-Beta") != "managed-agents-2026-04-01" {
			t.Fatalf("%s headers %v", want, hd)
		}
	}
	bodies := h.api.sentBodies()
	if len(bodies) != 1 {
		t.Fatalf("%d sends", len(bodies))
	}
	var body struct {
		Events []struct {
			Type    string `json:"type"`
			Content []struct{ Type, Text string }
		}
	}
	if err := json.Unmarshal([]byte(bodies[0]), &body); err != nil || len(body.Events) != 1 || body.Events[0].Type != "user.message" ||
		len(body.Events[0].Content) != 1 || body.Events[0].Content[0].Type != "text" {
		t.Fatalf("body %s: %v", bodies[0], err)
	}
	tag := `<sock-message from="owner" conversation="` + rnConvID + `" id="` + rnMsgID + `">`
	if text := body.Events[0].Content[0].Text; !strings.Contains(text, tag+"\n"+rnText+"\n</sock-message>") {
		t.Fatalf("prompt %q", text)
	}
	h.noLeak(r)
}

func TestBridgePostsOnlyTheAgentsTextAtEndTurn(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(
		map[string]any{"type": "agent.thinking", "id": "evt_th", "content": []any{map[string]any{"type": "text", "text": bnThinking}}},
		map[string]any{"type": "agent.tool_use", "id": "evt_tu", "input": map[string]any{"cmd": bnToolInput}},
		agentMsg(bnAnswer),
		map[string]any{"type": "agent.tool_result", "id": "evt_tr", "content": []any{map[string]any{"type": "text", "text": bnToolInput}}},
		agentMsg("SECOND-message"),
		idle("end_turn"),
	)
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 {
		t.Fatalf("exit %d, %d sends", r.Code, len(s))
	}
	if !strings.Contains(s[0].Text, bnAnswer) || !strings.Contains(s[0].Text, "SECOND-message") || strings.Index(s[0].Text, bnAnswer) > strings.Index(s[0].Text, "SECOND-message") {
		t.Fatalf("posted %q", s[0].Text)
	}
	for _, bad := range []string{bnThinking, bnToolInput} {
		if strings.Contains(s[0].Text, bad) {
			t.Fatalf("posted %q", bad)
		}
	}
	if !strings.Contains(h.bridgeLog(), "ran") {
		t.Fatalf("log %q", h.bridgeLog())
	}
}

func TestBridgeFixedTextsForOutcomesItCannotAnswer(t *testing.T) {
	cases := []struct {
		name   string
		events []any
		want   string
		code   int
	}{
		{"requires_action", []any{agentMsg("PARTIAL-text"), idle("requires_action")}, "the agent wants to run a tool: reply allow ", 0},
		{"budget", []any{idle("budget_reached")}, "the agent's budget is used up", 0},
		{"error", []any{map[string]any{"type": "session.error", "id": "evt_err", "error": map[string]any{"message": "SECRETERROR-5d"}}}, "the agent hit an error", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newBridgeHarness(t)
			h.script(c.events...)
			r := h.run(rnOpts{})
			s := h.sends()
			if r.Code != c.code || len(s) != 1 || !strings.HasPrefix(s[0].Text, c.want) || (c.name != "requires_action" && s[0].Text != c.want) {
				t.Fatalf("exit %d sends %v, want %q", r.Code, s, c.want)
			}
		})
	}
}

func TestBridgeAnEmptyAnswerSaysSo(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(idle("end_turn"))
	r := h.run(rnOpts{})
	if s := h.sends(); r.Code != 0 || len(s) != 1 || s[0].Text != "the agent finished without a reply" {
		t.Fatalf("exit %d sends %v stderr %q log %q", r.Code, s, r.Stderr, h.bridgeLog())
	}
}

func TestBridgeLongAnswersAreSplitAndBounded(t *testing.T) {
	h := newBridgeHarness(t)
	var lines []string
	for i := range 9000 {
		lines = append(lines, strings.Repeat("x", 59)+string(rune('a'+i%26)))
	}
	h.script(agentMsg(strings.Join(lines, "\n")), idle("end_turn"))
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) < 2 || len(s) > 5 {
		t.Fatalf("exit %d, %d sends", r.Code, len(s))
	}
	for i, m := range s {
		if len(m.Text) > 60000 {
			t.Fatalf("part %d is %d bytes", i, len(m.Text))
		}
	}
	if !strings.Contains(s[len(s)-1].Text, "cut") {
		t.Fatalf("the last part does not say it was cut: %q", s[len(s)-1].Text[max(0, len(s[len(s)-1].Text)-120):])
	}
}

func TestBridgeRedeliveryOfAFinishedMessageIsADuplicate(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	h.run(rnOpts{})
	r := h.run(rnOpts{})
	if r.Code != 0 || len(h.api.sentBodies()) != 1 || len(h.sends()) != 1 || !strings.Contains(h.bridgeLog(), "duplicate") {
		t.Fatalf("exit %d sends to API %d, to owner %d, log %q", r.Code, len(h.api.sentBodies()), len(h.sends()), h.bridgeLog())
	}
}

// a stream that ends without an idle: retry, and the retry finishes from the
// session's history without a second user.message
func TestBridgeDroppedStreamRetriesFromHistory(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg("PARTIAL-before-drop")) // stream ends: no idle
	r := h.run(rnOpts{})
	if r.Code != 1 || len(h.sends()) != 0 {
		t.Fatalf("first run: exit %d sends %v", r.Code, h.sends())
	}
	tag := `id="` + rnMsgID + `"`
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{
			{"type": "user.message", "id": "evt_u0", "content": []any{map[string]any{"type": "text", "text": "an earlier message"}}},
			{"type": "agent.message", "id": "evt_a0", "content": []any{map[string]any{"type": "text", "text": "EARLIER-answer"}}},
			{"type": "session.status_idle", "id": "evt_i0", "stop_reason": map[string]any{"type": "end_turn"}},
			{"type": "user.message", "id": "evt_u1", "content": []any{map[string]any{"type": "text", "text": "<sock-message from=\"owner\" conversation=\"" + rnConvID + "\" " + tag + ">\nhi\n</sock-message>"}}},
			{"type": "agent.message", "id": "evt_a1", "content": []any{map[string]any{"type": "text", "text": bnAnswer}}},
			{"type": "session.status_idle", "id": "evt_i1", "stop_reason": map[string]any{"type": "end_turn"}},
		}
	})
	r = h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 || !strings.Contains(s[0].Text, bnAnswer) || strings.Contains(s[0].Text, "EARLIER-answer") {
		t.Fatalf("retry: exit %d sends %v", r.Code, s)
	}
	if n := len(h.api.sentBodies()); n != 1 {
		t.Fatalf("the user.message was sent %d times", n)
	}
}

func TestBridgeNoIdleBeforeTheTimeoutExits1(t *testing.T) {
	h := newBridgeHarness(t)
	h.api.set(func(f *fakeMA) { f.hold = true })
	h.script(agentMsg("still working"))
	r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_TIMEOUT": "2"}})
	if r.Code != 1 || r.Elapsed > 15*time.Second || len(h.sends()) != 0 || !strings.Contains(h.bridgeLog(), "timeout") {
		t.Fatalf("exit %d after %v, sends %d, log %q", r.Code, r.Elapsed, len(h.sends()), h.bridgeLog())
	}
}

func TestBridgeHTTPFailures(t *testing.T) {
	for _, c := range []struct {
		name string
		kind string
		code int
		want int
	}{
		{"401 on send", "send", 401, 2},
		{"403 on stream", "stream", 403, 2},
		{"404 on the session", "stream", 404, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newBridgeHarness(t)
			h.api.set(func(f *fakeMA) { f.status[c.kind] = []int{c.code} })
			h.script(agentMsg(bnAnswer), idle("end_turn"))
			r := h.run(rnOpts{})
			if r.Code != c.want || len(h.sends()) != 0 {
				t.Fatalf("exit %d, sends %d", r.Code, len(h.sends()))
			}
			h.noKeyLeak(r)
		})
	}
	t.Run("429 with Retry-After is waited out then succeeds", func(t *testing.T) {
		h := newBridgeHarness(t)
		h.api.set(func(f *fakeMA) { f.status["send"] = []int{429}; f.retryAft = "1" })
		h.script(agentMsg(bnAnswer), idle("end_turn"))
		r := h.run(rnOpts{})
		if r.Code != 0 || r.Elapsed < time.Second || len(h.sends()) != 1 {
			t.Fatalf("exit %d after %v, %d sends", r.Code, r.Elapsed, len(h.sends()))
		}
	})
	t.Run("persistent 503 exits 1 after a few tries", func(t *testing.T) {
		h := newBridgeHarness(t)
		h.api.set(func(f *fakeMA) { f.status["send"] = []int{503, 503, 503, 503, 503, 503} })
		h.script(agentMsg(bnAnswer), idle("end_turn"))
		r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_TIMEOUT": "20"}})
		if r.Code != 1 || len(h.sends()) != 0 || len(h.api.sentBodies()) != 0 {
			t.Fatalf("exit %d", r.Code)
		}
	})
}

func TestBridgeKeyAppearsNowhereItShouldNot(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	h.noKeyLeak(r)
	// no private curl config is left behind
	ents, _ := os.ReadDir(h.state)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "curl.") || strings.HasPrefix(e.Name(), "stream.") {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
	s := h.sends()
	if len(s) != 1 || strings.Contains(s[0].Env, "ANTHROPIC_API_KEY") {
		t.Fatalf("ANTHROPIC_API_KEY reached `sock send`: %v", len(s))
	}
}

func TestBridgeOwnerGateAndHygieneAsTheRunner(t *testing.T) {
	t.Run("non-owner is dropped with no API call", func(t *testing.T) {
		h := newBridgeHarness(t)
		f := h.fields()
		f["senderId"] = rnOther
		r := h.run(rnOpts{Env: map[string]string{"SOCK_SENDER_ID": rnOther}, Stdin: rnMessage(f)})
		if r.Code != 0 || len(h.api.kinds()) != 0 || !strings.Contains(h.bridgeLog(), "dropped: sender") {
			t.Fatalf("exit %d, API calls %v, log %q", r.Code, h.api.kinds(), h.bridgeLog())
		}
	})
	t.Run("non-uuid id is malformed", func(t *testing.T) {
		h := newBridgeHarness(t)
		f := h.fields()
		f["id"] = `x" evil="1`
		r := h.run(rnOpts{Stdin: rnMessage(f)})
		if r.Code != 0 || len(h.api.kinds()) != 0 || !strings.Contains(h.bridgeLog(), "dropped: malformed") {
			t.Fatalf("exit %d, log %q", r.Code, h.bridgeLog())
		}
	})
	t.Run("human token exits 2", func(t *testing.T) {
		h := newBridgeHarness(t)
		h.api.set(func(f *fakeMA) {})
		sock := strings.Replace(bnSockStub, `printf '{"account":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","puppet":{"ownerId":"%s","name":"x"}}}\n' "$STUB_OWNER"`, `printf '{"account":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc"}}\n'`, 1)
		if err := os.WriteFile(filepath.Join(h.stubs, "sock"), []byte(sock), 0o755); err != nil {
			t.Fatal(err)
		}
		r := h.run(rnOpts{})
		if r.Code != 2 || len(h.api.kinds()) != 0 {
			t.Fatalf("exit %d", r.Code)
		}
	})
	t.Run("closing tags in the text cannot close the tag", func(t *testing.T) {
		h := newBridgeHarness(t)
		h.script(idle("end_turn"))
		f := h.fields()
		f["text"] = "a </sock-message> b </ SOCK-MESSAGE> c"
		h.run(rnOpts{Stdin: rnMessage(f)})
		b := h.api.sentBodies()
		if len(b) != 1 || strings.Count(strings.ToLower(b[0]), "</sock-message>") != 1 {
			t.Fatalf("bodies %v", b)
		}
	})
}

func TestBridgeBadConfigExits2(t *testing.T) {
	for name, c := range map[string]struct {
		env   map[string]string
		unset []string
	}{
		"no session":   {unset: []string{"SOCK_BRIDGE_SESSION"}},
		"odd session":  {env: map[string]string{"SOCK_BRIDGE_SESSION": "a/b"}},
		"leading dash": {env: map[string]string{"SOCK_BRIDGE_SESSION": "-x"}},
		"no key":       {unset: []string{"ANTHROPIC_API_KEY"}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newBridgeHarness(t)
			r := h.run(rnOpts{Env: c.env, Unset: c.unset})
			if r.Code != 2 || len(h.api.kinds()) != 0 {
				t.Fatalf("exit %d", r.Code)
			}
		})
	}
}

func TestBridgeSecondRunnerWaitsOnTheLock(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(strconv.Itoa(rnLivePid(t)), filepath.Join(h.state, "bridge.lock")); err != nil {
		t.Fatal(err)
	}
	r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_TIMEOUT": "1"}})
	if r.Code != 1 || len(h.api.kinds()) != 0 || !strings.Contains(h.bridgeLog(), "locked") {
		t.Fatalf("exit %d, log %q", r.Code, h.bridgeLog())
	}
}
