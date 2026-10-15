package test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// activity is the recorded `sock activity` calls, without their timestamps.
func (h *rnHarness) activity() []string {
	b, _ := os.ReadFile(filepath.Join(h.rec, "activity"))
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if _, call, ok := strings.Cut(l, " "); ok {
			out = append(out, call)
		}
	}
	return out
}

func (h *rnHarness) waitActivity(n int) []string {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a := h.activity(); len(a) >= n {
			return a
		}
		time.Sleep(20 * time.Millisecond)
	}
	return h.activity()
}

var (
	rnWorking = "activity --conversation " + rnConvID + " working"
	rnIdle    = "activity --conversation " + rnConvID + " idle"
)

// An owner message shows the owner the agent is working before the agent
// starts, and idle once the run ends.
func TestRunnerSignalsWorkingThenIdle(t *testing.T) {
	h := newRunnerHarness(t)
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	a := h.waitActivity(2)
	if len(a) < 2 || a[0] != rnWorking || a[len(a)-1] != rnIdle {
		t.Fatalf("activity %q, want working first and idle last", a)
	}
}

// While the agent runs, working is refreshed every 10 s, inside its 30 s TTL.
func TestRunnerRefreshesWorking(t *testing.T) {
	h := newRunnerHarness(t)
	if r := h.run(rnOpts{Env: map[string]string{"STUB_SLEEP": "11"}}); r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	a := h.waitActivity(3)
	if n := strings.Count(strings.Join(a, "\n"), rnWorking); n < 2 {
		t.Fatalf("activity %q, want working refreshed", a)
	}
	if a[len(a)-1] != rnIdle {
		t.Fatalf("activity %q, want idle last", a)
	}
}

// A failing `sock activity` (an older CLI, the hub down) never changes the
// run's outcome.
func TestRunnerActivityFailureIsIgnored(t *testing.T) {
	h := newRunnerHarness(t)
	if r := h.run(rnOpts{Env: map[string]string{"STUB_ACTIVITY_EXIT": "64"}}); r.Code != 0 || len(h.runs()) != 1 {
		t.Fatalf("exit %d, %d runs", r.Code, len(h.runs()))
	}
}

// Dropped and duplicate messages signal nothing: the owner sees working only
// for a message the agent will answer.
func TestRunnerNoActivityForDrops(t *testing.T) {
	h := newRunnerHarness(t)
	f := h.fields()
	f["senderId"] = rnOther
	h.run(rnOpts{Env: map[string]string{"SOCK_SENDER_ID": rnOther}, Stdin: rnMessage(f)})
	time.Sleep(200 * time.Millisecond)
	if a := h.activity(); len(a) != 0 {
		t.Fatalf("activity for a non-owner: %q", a)
	}
}

func TestRunnerModel(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		h := newRunnerHarness(t)
		if r := h.run(rnOpts{Env: map[string]string{"SOCK_RUNNER_MODEL": "haiku"}}); r.Code != 0 {
			t.Fatalf("exit %d: %q", r.Code, r.Stderr)
		}
		argv := h.runs()[0].Argv
		if i := slices.Index(argv, "--model"); i < 0 || i+1 >= len(argv) || argv[i+1] != "haiku" {
			t.Fatalf("argv %v, want --model haiku", argv)
		}
	})
	t.Run("codex", func(t *testing.T) {
		h := newRunnerHarness(t)
		if r := h.run(rnOpts{Env: map[string]string{"SOCK_RUNNER_AGENT": "codex", "SOCK_RUNNER_PERMISSION": "read-only", "SOCK_RUNNER_MODEL": "o4-mini"}}); r.Code != 0 {
			t.Fatalf("exit %d: %q", r.Code, r.Stderr)
		}
		argv := h.runs()[0].Argv
		if i := slices.Index(argv, "-m"); i < 0 || i+1 >= len(argv) || argv[i+1] != "o4-mini" || i > slices.Index(argv, "resume") {
			t.Fatalf("argv %v, want -m o4-mini before resume", argv)
		}
	})
	for _, bad := range []string{"a b", "--dangerously-skip-permissions", "x;y", strings.Repeat("m", 65)} {
		t.Run("refuses "+bad[:min(len(bad), 8)], func(t *testing.T) {
			h := newRunnerHarness(t)
			h.wantNoRun(h.run(rnOpts{Env: map[string]string{"SOCK_RUNNER_MODEL": bad}}), 2)
		})
	}
}
