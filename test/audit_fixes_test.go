package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wrapTools puts a wrapper for each tool first on PATH: it appends its own
// environment to $STUB_DIR/childenv, then runs the real tool.
func (h *rnHarness) wrapTools(tools ...string) {
	h.t.Helper()
	for _, name := range tools {
		w := "#!/bin/sh\nenv >> \"$STUB_DIR/childenv\"\nPATH=" + rnPathTail + "\nexport PATH\nexec " + name + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(h.stubs, name), []byte(w), 0o755); err != nil {
			h.t.Fatal(err)
		}
	}
}

// M1: the API key is unset before the bridge runs any child, whoami included
func TestBridgeKeyIsInNoChildEnvironment(t *testing.T) {
	h := newBridgeHarness(t)
	h.wrapTools("dirname", "jq", "ps", "find", "cat", "grep", "sed", "tr", "readlink", "curl", "date")
	sock := strings.Replace(bnSockStub, "#!/bin/sh\n", "#!/bin/sh\nenv >> \"$STUB_DIR/childenv\"\n", 1)
	if err := os.WriteFile(filepath.Join(h.stubs, "sock"), []byte(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 || len(h.sends()) != 1 {
		t.Fatalf("exit %d, %d sends, stderr %q", r.Code, len(h.sends()), r.Stderr)
	}
	env := string(readFile(t, filepath.Join(h.rec, "childenv")))
	tok, _ := os.ReadFile(filepath.Join(h.rec, "sock-token"))
	if strings.Count(string(tok), "\n") < 2 {
		t.Fatalf("whoami did not run: %q", tok)
	}
	if strings.Contains(env, bnKey) || strings.Contains(env, "ANTHROPIC_API_KEY") {
		t.Fatal("the API key is in the environment of a child")
	}
}

// sentPrompt is the text of the first user.message the bridge posted.
func (h *bnHarness) sentPrompt() string {
	h.t.Helper()
	b := h.api.sentBodies()
	if len(b) == 0 {
		h.t.Fatal("no user.message was posted")
	}
	var body struct {
		Events []struct{ Content []struct{ Text string } }
	}
	mustJSON(h.t, []byte(b[0]), &body)
	return body.Events[0].Content[0].Text
}

func textEvent(typ, id, text string) map[string]any {
	e := map[string]any{"type": typ, "content": []any{map[string]any{"type": "text", "text": text}}}
	if id != "" {
		e["id"] = id
	}
	return e
}

// dropThenHistory runs once with a stream that ends without idle, so the id is
// in sent.ids, then serves history: the posted user.message, then after.
func (h *bnHarness) dropThenHistory(after ...map[string]any) {
	h.t.Helper()
	h.script(agentMsg("PARTIAL-before-drop"))
	if r := h.run(rnOpts{}); r.Code != 1 {
		h.t.Fatalf("first run: exit %d", r.Code)
	}
	hist := append([]map[string]any{textEvent("user.message", "evt_u1", h.sentPrompt())}, after...)
	h.api.set(func(f *fakeMA) { f.history = hist })
}

// M2: catch-up keeps history order, once per id, and keeps events without one
func TestBridgeCatchUpKeepsHistoryOrder(t *testing.T) {
	h := newBridgeHarness(t)
	h.dropThenHistory(
		textEvent("agent.message", "evt_b", "FIRST-part"),
		textEvent("agent.message", "evt_a", "SECOND-part"),
		textEvent("agent.message", "evt_b", "FIRST-part"),
		textEvent("agent.thinking", "", "THINKING-noid"),
		textEvent("agent.message", "", "THIRD-noid"),
		map[string]any{"type": "session.status_idle", "id": "evt_i1", "stop_reason": map[string]any{"type": "end_turn"}},
	)
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
	if want := "FIRST-part\n\nSECOND-part\n\nTHIRD-noid"; s[0].Text != want {
		t.Fatalf("posted %q, want %q", s[0].Text, want)
	}
}

// L11: catch-up matches only the prompt's own tag line, not a tag the owner
// quoted inside another message's text
func TestBridgeCatchUpIgnoresAQuotedTag(t *testing.T) {
	h := newBridgeHarness(t)
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.state, "sent.ids"), []byte(rnMsgID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	tag := func(id string) string {
		return `<sock-message from="owner" conversation="` + rnConvID + `" id="` + id + `">`
	}
	quoting := "preface line\n" + tag(other) + "\nI pasted this earlier:\n" + tag(rnMsgID) + "\nhi\n</sock-message>\n"
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{
			textEvent("user.message", "evt_u0", quoting),
			textEvent("agent.message", "evt_a0", "OTHER-answer"),
			{"type": "session.status_idle", "id": "evt_i0", "stop_reason": map[string]any{"type": "end_turn"}},
		}
	})
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 || strings.Contains(s[0].Text, "OTHER-answer") || !strings.Contains(s[0].Text, "ANSWERSENTINEL") {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
	if n := len(h.api.sentBodies()); n != 1 {
		t.Fatalf("%d user.messages posted, want 1", n)
	}
}
