package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// One deadline covers the lock wait and the run (spec: "Waiting counts against
// the timeout"): a run that waited most of it for the lock gets only the rest.
func TestRunnerLockWaitShortensTheRun(t *testing.T) {
	h := newRunnerHarness(t)
	h.symlinkLock(rnLivePid(t))
	go func() {
		time.Sleep(3 * time.Second)
		_ = os.Remove(h.lockPath())
	}()
	r := h.run(rnOpts{Env: map[string]string{"SOCK_RUNNER_TIMEOUT": rnLoadedTimeout, "STUB_SLEEP": "4"}})
	if r.Code != 1 || !strings.Contains(h.log(), "timeout") {
		t.Fatalf("a 3 s lock wait and a 4 s run fit a %s s timeout: %+v log %q", rnLoadedTimeout, r, h.log())
	}
}

// An agent that exits 0 on the watchdog's TERM did not finish its turn: the
// run is a timeout and the message stays pending for its retry.
func TestRunnerAgentThatExitsZeroOnTermIsATimeout(t *testing.T) {
	h := newRunnerHarness(t)
	// the trap comes first and the timeout leaves room for a slow first exec
	agent := "#!/bin/sh\ntrap 'exit 0' TERM\ncat >/dev/null\nsleep 60 &\nwait\n"
	if err := os.WriteFile(filepath.Join(h.stubs, "claude"), []byte(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	r := h.run(rnOpts{Env: map[string]string{"SOCK_RUNNER_TIMEOUT": rnLoadedTimeout}})
	if r.Code != 1 || !strings.Contains(h.log(), "timeout") {
		t.Fatalf("an agent that exited 0 on TERM was acknowledged: %+v log %q", r, h.log())
	}
	if strings.Contains(h.log(), "duplicate") || strings.Contains(h.log(), "ran") {
		t.Fatalf("log %q", h.log())
	}
}

// The Agent SDK example keeps rule 2 in the tool list, not in a permission
// default, and leaves no token in the environment the spawned Claude Code (and
// any shell tool a user adds) inherits: only sock-mcp's own env carries it.
func TestAgentSDKExampleIsOutboundOnlyAndHoldsNoAmbientToken(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root, "agent-sdk", "example.ts"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, `args: ["--outbound-only"]`) {
		t.Error(`example.ts: sock-mcp runs without args: ["--outbound-only"]`)
	}
	del, q := strings.Index(src, "delete process.env.SOCK_TOKEN"), strings.Index(src, "query({")
	if del < 0 || del > q {
		t.Error("example.ts: SOCK_TOKEN stays in process.env when query() spawns Claude Code")
	}
}
