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

// An agent that exits 0 as the watchdog fires finished its turn: its status
// wins, the id is recorded and the message is not rerun.
func TestRunnerAgentExitZeroBeatsALateWatchdog(t *testing.T) {
	h := newRunnerHarness(t)
	agent := "#!/bin/sh\ncat >/dev/null\nmkdir -p \"$STUB_DIR/run.x\"\ntrap 'exit 0' TERM\nsleep 60 &\nwait\n"
	if err := os.WriteFile(filepath.Join(h.stubs, "claude"), []byte(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	r := h.run(rnOpts{Env: map[string]string{"SOCK_RUNNER_TIMEOUT": "2"}})
	if r.Code != 0 || !strings.Contains(h.log(), "ran") {
		t.Fatalf("agent exit 0 after the watchdog fired: %+v log %q", r, h.log())
	}
	if r := h.run(rnOpts{}); r.Code != 0 || !strings.Contains(h.log(), "duplicate") {
		t.Fatalf("redelivery reran: %+v log %q", r, h.log())
	}
}
