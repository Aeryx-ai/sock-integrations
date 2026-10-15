package test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (h *rnHarness) repliedCalls() []string {
	b, _ := os.ReadFile(filepath.Join(h.rec, "replied"))
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// An agent that exits 0 is done only when its sock_send posted in the
// message's conversation since the run started.
func TestRunnerChecksTheReplyPosted(t *testing.T) {
	h := newRunnerHarness(t)
	before := time.Now().Unix()
	if r := h.run(rnOpts{Env: map[string]string{"STUB_REPLIED": "0"}}); r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	calls := h.repliedCalls()
	if len(calls) != 1 {
		t.Fatalf("replied calls %q", calls)
	}
	f := strings.Fields(calls[0])
	if len(f) != 5 || f[0] != "replied" || f[1] != "--conversation" || f[2] != rnConvID || f[3] != "--since" {
		t.Fatalf("replied argv %q", calls[0])
	}
	since, err := strconv.ParseFloat(f[4], 64)
	if err != nil || since < float64(before-1) || since > float64(time.Now().Unix()) {
		t.Fatalf("--since %q, want the run's start", f[4])
	}
	if !strings.Contains(h.log(), " ran") {
		t.Fatalf("log %q", h.log())
	}
}

// Exit 0 without a reply is retried, and after a second run without one the
// message is given up on, so a model that never answers cannot loop.
func TestRunnerRetriesThenGivesUpWithoutAReply(t *testing.T) {
	h := newRunnerHarness(t)
	env := map[string]string{"STUB_REPLIED": "1"}
	if r := h.run(rnOpts{Env: env}); r.Code != 1 {
		t.Fatalf("first run exit %d, want 1", r.Code)
	}
	if !strings.Contains(h.log(), "agent exited 0 without replying") {
		t.Fatalf("log %q", h.log())
	}
	if r := h.run(rnOpts{Env: env}); r.Code != 0 {
		t.Fatalf("second run exit %d, want 0 (given up)", r.Code)
	}
	if !strings.Contains(h.log(), "no reply after 2 runs") {
		t.Fatalf("log %q", h.log())
	}
	if r := h.run(rnOpts{Env: env}); r.Code != 0 || len(h.runs()) != 2 || !strings.Contains(h.log(), "duplicate") {
		t.Fatalf("a given-up message ran again: exit %d, %d runs", r.Code, len(h.runs()))
	}
}

// An agent that posted its reply and then failed or timed out is done: a
// retry would post a second reply.
func TestRunnerFailedAgentThatRepliedIsDone(t *testing.T) {
	h := newRunnerHarness(t)
	replied := map[string]string{"STUB_EXIT": "3", "STUB_REPLIED": "0"}
	if r := h.run(rnOpts{Env: replied}); r.Code != 0 {
		t.Fatalf("exit %d, want 0", r.Code)
	}
	if !strings.Contains(h.log(), "replied, agent exit 3") {
		t.Fatalf("log %q", h.log())
	}
	if r := h.run(rnOpts{Env: replied}); r.Code != 0 || len(h.runs()) != 1 {
		t.Fatalf("rerun after a reply: exit %d, %d runs", r.Code, len(h.runs()))
	}
}

// A failed agent that did not reply is retried, as before.
func TestRunnerFailedAgentWithoutAReplyRetries(t *testing.T) {
	h := newRunnerHarness(t)
	if r := h.run(rnOpts{Env: map[string]string{"STUB_EXIT": "3", "STUB_REPLIED": "1"}}); r.Code != 1 {
		t.Fatalf("exit %d, want 1", r.Code)
	}
}

// A sock-mcp that cannot say (too old for replied, missing) leaves the
// agent's exit status in charge, as before the check existed.
func TestRunnerReplyCheckUnavailableFallsBack(t *testing.T) {
	for _, c := range []struct {
		name, replied, exit string
		want                int
	}{
		{"old sock-mcp, agent ok", "2", "0", 0},
		{"old sock-mcp, agent failed", "2", "3", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newRunnerHarness(t)
			if r := h.run(rnOpts{Env: map[string]string{"STUB_REPLIED": c.replied, "STUB_EXIT": c.exit}}); r.Code != c.want {
				t.Fatalf("exit %d, want %d", r.Code, c.want)
			}
		})
	}
}
