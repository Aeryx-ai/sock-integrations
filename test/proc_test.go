package test

import (
	"context"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// cmdDeadline bounds one run of a script under test or a fake. It is far
// above any timeout a test asserts and well under go test's own -timeout,
// whose panic skips every cleanup and would orphan whatever is running.
const cmdDeadline = 3 * time.Minute

// testCmd builds name in a process group of its own. The group gets TERM at
// the deadline, so sock-runner's trap can stop the groups it made itself, and
// KILL after waitCmd and at test cleanup, so nothing the run started outlives
// it. WaitDelay keeps a pipe held by a stray descendant from hanging Wait.
func testCmd(t testing.TB, name string, args ...string) *exec.Cmd {
	return groupCmd(t, cmdDeadline, name, args...)
}

func groupCmd(t testing.TB, deadline time.Duration, name string, args ...string) *exec.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return signalGroup(cmd, syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	t.Cleanup(func() {
		cancel()
		_ = signalGroup(cmd, syscall.SIGKILL)
	})
	return cmd
}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

// waitCmd waits for cmd, then kills whatever is left in its group.
func waitCmd(cmd *exec.Cmd) error {
	err := cmd.Wait()
	_ = signalGroup(cmd, syscall.SIGKILL)
	return err
}

func runCmd(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return waitCmd(cmd)
}

// A script under test that backgrounds a child and exits, or that outlives
// its deadline, must not leave that child running once the helper returns.
func TestCmdLeavesNoDescendant(t *testing.T) {
	for _, c := range []struct {
		name, tail string
		deadline   time.Duration
	}{
		{"exits", "exit 0", cmdDeadline},
		{"deadline", "exec sleep 600", time.Second},
	} {
		t.Run(c.name, func(t *testing.T) {
			pidf := filepath.Join(t.TempDir(), "pid")
			cmd := groupCmd(t, c.deadline, "/bin/sh", "-c", `sleep 600 </dev/null >/dev/null 2>&1 & echo $! >"$1"; `+c.tail, "sh", pidf)
			start := time.Now()
			_ = runCmd(cmd)
			if el := time.Since(start); el > c.deadline+10*time.Second {
				t.Fatalf("took %v", el)
			}
			pid := rnPidFile(t, pidf)
			if !rnGone(pid, 2*time.Second) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				t.Fatalf("grandchild %d outlived the helper", pid)
			}
		})
	}
}
