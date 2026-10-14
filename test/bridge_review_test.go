package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// 1: a line the server has half written is not an event yet
func TestBridgeWaitsForTheEndOfASplitEvent(t *testing.T) {
	h := newBridgeHarness(t)
	h.api.set(func(f *fakeMA) { f.split = true })
	h.script(agentMsg(bnAnswer+" padded so the event is longer than forty bytes"), idle("end_turn"))
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 || !strings.Contains(s[0].Text, "ANSWERSENTINEL-6b2d") {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
}

// 2: a POST that may have landed is never repeated by the bridge
func TestBridgeDoesNotRepeatAPostThatMayHaveLanded(t *testing.T) {
	h := newBridgeHarness(t)
	h.api.set(func(f *fakeMA) { f.dropSend = 1 })
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 1 || len(h.api.sentBodies()) != 1 {
		t.Fatalf("first run: exit %d, %d user.messages sent", r.Code, len(h.api.sentBodies()))
	}
	// the server did record it: the retry finds it in history and finishes
	tag := `id="` + rnMsgID + `"`
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{
			{"type": "user.message", "id": "evt_u1", "content": []any{map[string]any{"type": "text", "text": "<sock-message " + tag + ">x</sock-message>"}}},
			{"type": "agent.message", "id": "evt_a1", "content": []any{map[string]any{"type": "text", "text": bnAnswer}}},
			{"type": "session.status_idle", "id": "evt_i1", "stop_reason": map[string]any{"type": "end_turn"}},
		}
	})
	r = h.run(rnOpts{})
	if s := h.sends(); r.Code != 0 || len(s) != 1 || len(h.api.sentBodies()) != 1 {
		t.Fatalf("retry: exit %d, sends %d, user.messages %d", r.Code, len(h.sends()), len(h.api.sentBodies()))
	}
}

// ...and when it did not land, the retry sends it
func TestBridgeResendsWhenHistoryShowsTheMessageNeverLanded(t *testing.T) {
	h := newBridgeHarness(t)
	h.api.set(func(f *fakeMA) { f.status["send"] = []int{500} })
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	if r := h.run(rnOpts{}); r.Code != 1 {
		t.Fatalf("first run: exit %d", r.Code)
	}
	h.api.set(func(f *fakeMA) { f.status["send"] = nil; f.history = nil })
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 || len(h.sends()) != 1 {
		t.Fatalf("retry: exit %d, sends %d, user.messages %d", r.Code, len(h.sends()), len(h.api.sentBodies()))
	}
}

// 3: temp files of a run that was killed are swept, the key's included
func TestBridgeSweepsTempFilesOfDeadRuns(t *testing.T) {
	h := newBridgeHarness(t)
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	dead := strconv.Itoa(rnDeadPid(t))
	live := strconv.Itoa(rnLivePid(t))
	for _, name := range []string{"curl", "hdr", "resp", "stream", "body", "answer", "prompt"} {
		for _, pid := range []string{dead, live} {
			if err := os.WriteFile(filepath.Join(h.state, name+"."+pid), []byte("x-api-key: "+bnKey), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	h.run(rnOpts{})
	for _, name := range []string{"curl", "hdr", "resp", "stream", "body", "answer", "prompt"} {
		if _, err := os.Stat(filepath.Join(h.state, name+"."+dead)); err == nil {
			t.Fatalf("%s.%s of a dead run was kept", name, dead)
		}
		if _, err := os.Stat(filepath.Join(h.state, name+"."+live)); err != nil {
			t.Fatalf("%s.%s of a live process was swept", name, live)
		}
	}
}

// 4: a flood of events is read in linear time and the deadline is checked
func TestBridgeReadsAFloodOfEventsQuickly(t *testing.T) {
	h := newBridgeHarness(t)
	h.api.set(func(f *fakeMA) { f.flood = 4000 })
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 || r.Elapsed > 12*time.Second {
		t.Fatalf("exit %d after %v", r.Code, r.Elapsed)
	}
	if s := h.sends(); len(s) != 1 || !strings.Contains(s[0].Text, "ANSWERSENTINEL-6b2d") {
		t.Fatalf("sends %v", s)
	}
}

func TestBridgeStopsOnAStreamThatGrowsWithoutBound(t *testing.T) {
	h := newBridgeHarness(t)
	h.api.set(func(f *fakeMA) { f.flood = 4000; f.hold = true })
	h.script(agentMsg("still working"))
	r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_TIMEOUT": "3", "SOCK_BRIDGE_MAX_STREAM": "100000"}})
	if r.Code != 1 || r.Elapsed > 10*time.Second || !strings.Contains(h.bridgeLog(), "stream too large") {
		t.Fatalf("exit %d after %v, log %q", r.Code, r.Elapsed, h.bridgeLog())
	}
}

// 5: a cut never splits a character
func TestBridgeSplitsOnCharacterBoundaries(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(strings.Repeat("日", 25000)), idle("end_turn"))
	r := h.run(rnOpts{})
	s := h.sends()
	if r.Code != 0 || len(s) < 2 {
		t.Fatalf("exit %d, %d sends", r.Code, len(s))
	}
	for i, m := range s {
		if !utf8.ValidString(m.Text) || len(m.Text) > 60000 {
			t.Fatalf("part %d: %d bytes, valid UTF-8 %v", i, len(m.Text), utf8.ValidString(m.Text))
		}
	}
	if joined := s[0].Text + s[1].Text; strings.Count(joined, "日") != 25000 {
		t.Fatalf("characters lost or invented across the cut: %d", strings.Count(joined, "日"))
	}
}

func TestBridgeFivePartCutKeepsCharactersWhole(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(strings.Repeat("日", 120000)), idle("end_turn"))
	h.run(rnOpts{})
	for i, m := range h.sends() {
		if !utf8.ValidString(m.Text) {
			t.Fatalf("part %d is not valid UTF-8", i)
		}
	}
}

// 6: any Retry-After value is survivable
func TestBridgeRetryAfterEdgeValues(t *testing.T) {
	for _, v := range []string{"08", "09", "0", "00000000000000000000", "-1", "soon", "Wed, 21 Oct 2015 07:28:00 GMT"} {
		t.Run(v, func(t *testing.T) {
			h := newBridgeHarness(t)
			h.api.set(func(f *fakeMA) { f.status["send"] = []int{429}; f.retryAft = v })
			h.script(agentMsg(bnAnswer), idle("end_turn"))
			r := h.run(rnOpts{})
			if r.Code != 0 || len(h.sends()) != 1 || r.Elapsed > 15*time.Second {
				t.Fatalf("Retry-After %q: exit %d after %v, %d sends", v, r.Code, r.Elapsed, len(h.sends()))
			}
		})
	}
}

// 7: history pages are followed, and a turn still running is waited for
func TestBridgeFollowsHistoryPages(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg("PARTIAL-before-drop"))
	h.run(rnOpts{})
	tag := `id="` + rnMsgID + `"`
	var filler []map[string]any
	for i := range 3 {
		filler = append(filler, map[string]any{"type": "agent.message", "id": "evt_f" + strconv.Itoa(i), "content": []any{map[string]any{"type": "text", "text": "old"}}})
	}
	h.api.set(func(f *fakeMA) {
		f.pages = [][]map[string]any{
			filler,
			{{"type": "user.message", "id": "evt_u1", "content": []any{map[string]any{"type": "text", "text": "<sock-message " + tag + ">x</sock-message>"}}}},
			{{"type": "agent.message", "id": "evt_a1", "content": []any{map[string]any{"type": "text", "text": bnAnswer}}},
				{"type": "session.status_idle", "id": "evt_i1", "stop_reason": map[string]any{"type": "end_turn"}}},
		}
	})
	r := h.run(rnOpts{})
	if s := h.sends(); r.Code != 0 || len(s) != 1 || !strings.Contains(s[0].Text, "ANSWERSENTINEL") {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
}

func TestBridgeWaitsForARunningTurnInHistory(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg("PARTIAL-before-drop"))
	h.run(rnOpts{})
	tag := `id="` + rnMsgID + `"`
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{{"type": "user.message", "id": "evt_u1", "content": []any{map[string]any{"type": "text", "text": "<sock-message " + tag + ">x</sock-message>"}}}}
	})
	go func() {
		time.Sleep(2500 * time.Millisecond)
		h.api.set(func(f *fakeMA) {
			f.history = append(f.history,
				map[string]any{"type": "agent.message", "id": "evt_a1", "content": []any{map[string]any{"type": "text", "text": bnAnswer}}},
				map[string]any{"type": "session.status_idle", "id": "evt_i1", "stop_reason": map[string]any{"type": "end_turn"}})
		})
	}()
	r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_TIMEOUT": "20"}})
	if s := h.sends(); r.Code != 0 || len(s) != 1 {
		t.Fatalf("exit %d, sends %v", r.Code, s)
	}
}

// 9: what is posted to the owner
func TestBridgeCleansTheAnswerItPosts(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg("clean \x1b[31mred\x1b[0m bell\x07 text"), agentMsg("clean \x1b[31mred\x1b[0m bell\x07 text"), idle("end_turn"))
	h.run(rnOpts{})
	s := h.sends()
	if len(s) != 1 || strings.ContainsAny(s[0].Text, "\x1b\x07") {
		t.Fatalf("sends %q stderr %q log %q", s, "", h.bridgeLog())
	}
}

func TestBridgeAWhitespaceOnlyAnswerIsEmpty(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg("   \n  "), idle("end_turn"))
	h.run(rnOpts{})
	if s := h.sends(); len(s) != 1 || s[0].Text != "the agent finished without a reply" {
		t.Fatalf("sends %q", s)
	}
}

func TestBridgeDuplicateEventsAreCountedOnce(t *testing.T) {
	h := newBridgeHarness(t)
	a := agentMsg(bnAnswer)
	h.script(a, a, idle("end_turn"))
	h.run(rnOpts{})
	s := h.sends()
	if len(s) != 1 || strings.Count(s[0].Text, "ANSWERSENTINEL") != 1 {
		t.Fatalf("sends %q", s)
	}
}

func TestBridgeUnwritableStateDirFailsFast(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_STATE": "/proc/nonexistent/state", "SOCK_BRIDGE_TIMEOUT": "30"}})
	if r.Code != 2 || r.Elapsed > 5*time.Second {
		t.Fatalf("exit %d after %v", r.Code, r.Elapsed)
	}
}

// the key reaches curl only through its config file: never in argv
func TestBridgeCurlNeverSeesTheKeyInItsArguments(t *testing.T) {
	h := newBridgeHarness(t)
	wrapper := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$STUB_DIR/curl.argv\"\nexec /usr/bin/curl \"$@\"\n"
	if err := os.WriteFile(filepath.Join(h.stubs, "curl"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	argv, _ := os.ReadFile(filepath.Join(h.rec, "curl.argv"))
	if r.Code != 0 || len(argv) == 0 || strings.Contains(string(argv), bnKey) || strings.Contains(string(argv), "x-api-key") {
		t.Fatalf("exit %d, curl argv %q", r.Code, argv)
	}
}

// brg 1: the key only goes to https, or to a loopback address for tests
func TestBridgeRefusesToSendTheKeyToAnUnsafeURL(t *testing.T) {
	for _, u := range []string{"http://evil.example", "http://127.0.0.1.evil.example", "ftp://x", "https://", "api.anthropic.com", "http://localhost@evil.example", "https://x/ y"} {
		t.Run(u, func(t *testing.T) {
			h := newBridgeHarness(t)
			r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_API": u}})
			if r.Code != 2 || len(h.api.kinds()) != 0 {
				t.Fatalf("exit %d, %d requests", r.Code, len(h.api.kinds()))
			}
		})
	}
	for _, u := range []string{"http://localhost:1", "http://127.0.0.1:1", "https://api.example.test"} {
		t.Run("accepts "+u, func(t *testing.T) {
			h := newBridgeHarness(t)
			r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_API": u, "SOCK_BRIDGE_TIMEOUT": "3"}})
			if r.Code == 2 {
				t.Fatalf("refused %s: %q", u, r.Stderr)
			}
		})
	}
}

// brg 2: a part that failed is retried alone; the parts already posted stay posted
func TestBridgeRetryPostsOnlyTheRemainingParts(t *testing.T) {
	h := newBridgeHarness(t)
	var b strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&b, "line %05d %s\n", i, strings.Repeat("x", 45)) // two parts of different text
	}
	long := b.String()
	h.script(agentMsg(long), idle("end_turn"))
	r := h.run(rnOpts{Env: map[string]string{"STUB_SEND_FAIL_N": "1"}})
	if r.Code != 1 || len(h.sends()) != 2 {
		t.Fatalf("first run: exit %d, %d sends", r.Code, len(h.sends()))
	}
	tag := `id="` + rnMsgID + `"`
	h.api.set(func(f *fakeMA) {
		f.history = []map[string]any{
			{"type": "user.message", "id": "evt_u1", "content": []any{map[string]any{"type": "text", "text": "<sock-message " + tag + ">x</sock-message>"}}},
			{"type": "agent.message", "id": "evt_a1", "content": []any{map[string]any{"type": "text", "text": long}}},
			{"type": "session.status_idle", "id": "evt_i1", "stop_reason": map[string]any{"type": "end_turn"}},
		}
	})
	r = h.run(rnOpts{})
	s := h.sends()
	// part 1 (posted), part 2 (failed), then only part 2 again
	if r.Code != 0 || len(s) != 3 || s[2].Text != s[1].Text || s[2].Text == s[0].Text {
		t.Fatalf("retry: exit %d, %d sends", r.Code, len(s))
	}
}

// brg 3: an unknown stop reason is named in the log
func TestBridgeLogsAnUnknownStopReason(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(idle("model_context_window_exceeded"))
	h.run(rnOpts{})
	if !strings.Contains(h.bridgeLog(), "agent error (model_context_window_exceeded)") {
		t.Fatalf("log %q", h.bridgeLog())
	}
}

func TestBridgeDoesNotLogAnOddStopReason(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(idle("x; rm -rf / \x1b[31m"))
	h.run(rnOpts{})
	if strings.Contains(h.bridgeLog(), "rm -rf") || strings.ContainsRune(h.bridgeLog(), 0x1b) || !strings.Contains(h.bridgeLog(), "agent error (unknown)") {
		t.Fatalf("log %q", h.bridgeLog())
	}
}

// brg 4: the answer reaches `sock send` on stdin, not in argv (visible in ps)
func TestBridgeSendsTheAnswerOnStdin(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	h.run(rnOpts{})
	argv, err := os.ReadFile(filepath.Join(h.rec, "send.0", "argv"))
	if err != nil || strings.Contains(string(argv), "ANSWERSENTINEL") {
		t.Fatalf("argv %q (%v)", argv, err)
	}
	if s := h.sends(); len(s) != 1 || !strings.Contains(s[0].Text, "ANSWERSENTINEL") {
		t.Fatalf("sends %v", s)
	}
}

// brg 5: the curl config holds the key, so an old one goes even if its pid is alive again
func TestBridgeSweepsAnOldCurlConfigWhateverItsPid(t *testing.T) {
	h := newBridgeHarness(t)
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(h.state, "curl."+strconv.Itoa(rnLivePid(t)))
	if err := os.WriteFile(old, []byte("x-api-key: "+bnKey), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, when, when); err != nil {
		t.Fatal(err)
	}
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	h.run(rnOpts{})
	if _, err := os.Stat(old); err == nil {
		t.Fatal("an hour-old curl config was kept")
	}
}
