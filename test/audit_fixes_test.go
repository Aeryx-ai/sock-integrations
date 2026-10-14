package test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

// M3: a Retry-After past 30 s is honored, bounded only by the time left
func TestBridgeRetryAfterIsNotCappedAtThirtySeconds(t *testing.T) {
	h := newBridgeHarness(t)
	h.dropThenHistory(textEvent("agent.message", "evt_a1", bnAnswer),
		map[string]any{"type": "session.status_idle", "id": "evt_i1", "stop_reason": map[string]any{"type": "end_turn"}})
	h.api.set(func(f *fakeMA) { f.status["list"] = []int{429}; f.retryAft = "36" })
	r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_TIMEOUT": "60"}})
	if r.Code != 0 || len(h.sends()) != 1 || r.Elapsed < 35*time.Second {
		t.Fatalf("exit %d after %v, %d sends", r.Code, r.Elapsed, len(h.sends()))
	}
}

// L1: an HTTP-date Retry-After is GMT whatever the local zone
func TestBridgeRetryAfterDateIsReadAsGMT(t *testing.T) {
	h := newBridgeHarness(t)
	when := time.Now().Add(5 * time.Second).UTC().Format(http.TimeFormat)
	h.api.set(func(f *fakeMA) { f.status["send"] = []int{429}; f.retryAft = when })
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{Env: map[string]string{"TZ": "Europe/Berlin", "SOCK_BRIDGE_TIMEOUT": "60"}})
	if r.Code != 0 || len(h.sends()) != 1 || r.Elapsed < 3*time.Second || r.Elapsed > 20*time.Second {
		t.Fatalf("Retry-After %q: exit %d after %v, %d sends", when, r.Code, r.Elapsed, len(h.sends()))
	}
}

func count(xs []string, x string) int {
	n := 0
	for _, v := range xs {
		if v == x {
			n++
		}
	}
	return n
}

// M4: the stream GET retries 429 and 5xx with Retry-After or backoff, a few times
func TestBridgeStreamOpenRetries(t *testing.T) {
	for _, c := range []struct {
		name   string
		codes  []int
		retry  string
		want   int
		opens  int
		sends  int
		atLeast time.Duration
	}{
		{"429 with Retry-After", []int{429}, "2", 0, 2, 1, 2 * time.Second},
		{"503 then 502", []int{503, 502}, "", 0, 3, 1, 3 * time.Second},
		{"persistent 503", []int{503, 503, 503, 503, 503, 503}, "", 1, 4, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newBridgeHarness(t)
			h.api.set(func(f *fakeMA) { f.status["stream"] = c.codes; f.retryAft = c.retry })
			h.script(agentMsg(bnAnswer), idle("end_turn"))
			r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_TIMEOUT": "30"}})
			k := h.api.kinds()
			if r.Code != c.want || count(k, "stream") != c.opens || len(h.sends()) != c.sends || r.Elapsed < c.atLeast {
				t.Fatalf("exit %d after %v, requests %v, %d sends, log %q", r.Code, r.Elapsed, k, len(h.sends()), h.bridgeLog())
			}
			if c.want != 0 && len(h.api.sentBodies()) != 0 {
				t.Fatal("a user.message was posted without a stream")
			}
		})
	}
}

// M6: the history being gathered is a temp file like any other: removed when a
// history read fails, and swept when its run died
func TestBridgeFailedHistoryReadLeavesNoHistory(t *testing.T) {
	h := newBridgeHarness(t)
	h.dropThenHistory(textEvent("agent.message", "evt_a1", bnAnswer))
	h.api.set(func(f *fakeMA) { f.status["list"] = []int{200, 503, 503, 503, 503, 503} })
	h.api.set(func(f *fakeMA) {
		f.pages = [][]map[string]any{f.history, {textEvent("agent.message", "evt_a2", "more")}}
	})
	r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_TIMEOUT": "5"}})
	if r.Code != 1 {
		t.Fatalf("exit %d", r.Code)
	}
	ents, _ := os.ReadDir(h.state)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "hist") {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}

func TestBridgeSweepsTheHistoryAndStreamCutsOfDeadRuns(t *testing.T) {
	h := newBridgeHarness(t)
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	dead, live := strconv.Itoa(rnDeadPid(t)), strconv.Itoa(rnLivePid(t))
	// histp: the history pages; newc: a stream batch cut at its last newline
	names := []string{"histp", "newc"}
	for _, name := range names {
		for _, pid := range []string{dead, live} {
			if err := os.WriteFile(filepath.Join(h.state, name+"."+pid), []byte(bnToolInput), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	h.run(rnOpts{})
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(h.state, name+"."+dead)); err == nil {
			t.Fatalf("%s.%s of a dead run was kept", name, dead)
		}
		if _, err := os.Stat(filepath.Join(h.state, name+"."+live)); err != nil {
			t.Fatalf("%s.%s of a live process was swept", name, live)
		}
	}
}

// L2: CR and the C1 controls go too, and no other character is touched
func TestBridgeCleansCarriageReturnsAndC1Controls(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg("safe\rrm -rf ~ \u0085next \u009b31m csi \u0080\u009f 日本語  nbsp é Ā"), idle("end_turn"))
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
	got := s[0].Text
	if want := "saferm -rf ~ next 31m csi  日本語  nbsp é Ā"; got != want {
		t.Fatalf("posted %q, want %q", got, want)
	}
}

// L3: a split falls on a blank line before it falls between lines
func TestBridgeSplitsOnBlankLinesFirst(t *testing.T) {
	h := newBridgeHarness(t)
	para := func(name string) []string {
		var ls []string
		for i := range 700 {
			ls = append(ls, fmt.Sprintf("%s line %04d %s", name, i, strings.Repeat("x", 45)))
		}
		return ls
	}
	p1, p2 := para("first"), para("second")
	h.script(agentMsg(strings.Join(p1, "\n")+"\n\n"+strings.Join(p2, "\n")), idle("end_turn"))
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 2 {
		t.Fatalf("exit %d, %d sends", r.Code, len(s))
	}
	if a := strings.TrimRight(s[0].Text, "\n"); !strings.HasSuffix(a, p1[len(p1)-1]) {
		t.Fatalf("part 1 ends %q", a[max(0, len(a)-80):])
	}
	if b := strings.TrimLeft(s[1].Text, "\n"); !strings.HasPrefix(b, p2[0]) {
		t.Fatalf("part 2 starts %q", b[:min(80, len(b))])
	}
}

// L5: a stop reason holding a newline is unknown, not a second log line
func TestBridgeAStopReasonWithANewlineIsUnknown(t *testing.T) {
	for _, reason := range []string{"x\nran", "budget\nran\nx", strings.Repeat("a", 41)} {
		t.Run(strconv.Quote(reason), func(t *testing.T) {
			h := newBridgeHarness(t)
			h.script(idle(reason))
			h.run(rnOpts{})
			log := h.bridgeLog()
			if strings.Count(log, "\n") != 1 || !strings.Contains(log, "agent error (unknown)") {
				t.Fatalf("log %q", log)
			}
		})
	}
}

// L9: a relative SOCK_RUNNER_STATE still means the directory it named at start,
// after the runner has moved into the workdir
func TestRunnerRelativeStateDirIsResolvedAtStart(t *testing.T) {
	h := newRunnerHarness(t)
	r := h.run(rnOpts{Env: map[string]string{"SOCK_RUNNER_STATE": "state"}}) // the harness runs in h.dir
	if r.Code != 0 || len(h.runs()) != 1 || !strings.Contains(h.log(), " ran") {
		t.Fatalf("exit %d, %d runs, log %q, stderr %q", r.Code, len(h.runs()), h.log(), r.Stderr)
	}
	if _, err := os.Lstat(h.lockPath()); err == nil {
		t.Fatal("the lock was left behind")
	}
	if _, err := os.Stat(filepath.Join(h.work, "state")); err == nil {
		t.Fatal("state was written under the workdir")
	}
}
