package test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A stub sock-codex-inject: records argv and stdin like the agent stub, prints
// STUB_INJECT_OUT and exits STUB_INJECT_EXIT.
const scInjectStub = `#!/bin/sh
r=$(mktemp -d "$STUB_DIR/run.XXXXXX") || exit 99
echo sock-codex-inject > "$r/agent"
for a in "$@"; do printf '%s\n' "$a"; done > "$r/argv"
env > "$r/env"
pwd > "$r/pwd"
cat > "$r/stdin"
[ -n "$STUB_INJECT_OUT" ] && printf '%s\n' "$STUB_INJECT_OUT"
exit "${STUB_INJECT_EXIT:-0}"
`

// newCodexHarness is the runner harness pointed at runner/sock-codex, with a
// stub injector on PATH.
func newCodexHarness(t *testing.T) *rnHarness {
	t.Helper()
	h := newRunnerHarness(t)
	h.bin = filepath.Join(root, "runner", "sock-codex")
	writeFakeExec(t, filepath.Join(h.stubs, "sock-codex-inject"), scInjectStub)
	for _, k := range []string{"SOCK_RUNNER_AGENT", "SOCK_RUNNER_SESSION", "SOCK_RUNNER_PERMISSION"} {
		delete(h.env, k)
	}
	return h
}

func TestSockCodexInjectsTheOwnersReply(t *testing.T) {
	h := newCodexHarness(t)
	r := h.run(rnOpts{Env: map[string]string{"STUB_INJECT_OUT": "accepted session=s1 turn=t1"}})
	if r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	runs := h.runs()
	if len(runs) != 1 {
		t.Fatalf("%d runs", len(runs))
	}
	a := runs[0]
	want := []string{"--project", h.work, "--conversation", rnConvID, "--message-id", rnMsgID}
	if strings.Join(a.Argv, "\n") != strings.Join(want, "\n") {
		t.Fatalf("argv %q", a.Argv)
	}
	tag := `<sock-message from="owner" conversation="` + rnConvID + `" id="` + rnMsgID + `">`
	if !strings.Contains(a.Stdin, tag+"\n"+rnText+"\n</sock-message>") {
		t.Fatalf("prompt %q", a.Stdin)
	}
	for _, n := range []string{"SOCK_TOKEN", "SOCK_URL", "SOCK_MESSAGE_ID", "SOCK_CONVERSATION_ID", "SOCK_SENDER_ID", "SOCK_RUNNER_WORKDIR"} {
		if a.envHas(n) {
			t.Fatalf("%s reached the injector", n)
		}
	}
	if l := h.log(); !strings.Contains(l, "injected session=s1 turn=t1") {
		t.Fatalf("log %q", l)
	}
	// a redelivery is acknowledged without a second injection
	if r := h.run(rnOpts{}); r.Code != 0 || len(h.runs()) != 1 {
		t.Fatalf("redelivery: exit %d, %d runs", r.Code, len(h.runs()))
	}
	h.noLeak(r)
}

func TestSockCodexOutcomes(t *testing.T) {
	for name, c := range map[string]struct {
		exit, want int
		log        string
	}{
		"duplicate":          {3, 0, "duplicate session=s1"},
		"no session":         {4, 1, "no session"},
		"refused":            {5, 1, "refused"},
		"daemon unavailable": {6, 1, "daemon unavailable"},
		"usage":              {2, 2, "injector config"},
		"crash":              {9, 1, "injector exit 9"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newCodexHarness(t)
			r := h.run(rnOpts{Env: map[string]string{"STUB_INJECT_EXIT": strconv.Itoa(c.exit), "STUB_INJECT_OUT": "x session=s1"}})
			if r.Code != c.want {
				t.Fatalf("exit %d, want %d: %q", r.Code, c.want, r.Stderr)
			}
			if l := h.log(); !strings.Contains(l, c.log) {
				t.Fatalf("log %q", l)
			}
			h.noLeak(r)
		})
	}
}

func TestSockCodexGatesTheSender(t *testing.T) {
	h := newCodexHarness(t)
	f := h.fields()
	f["senderId"] = rnOther
	r := h.run(rnOpts{Stdin: rnMessage(f), Env: map[string]string{"SOCK_SENDER_ID": rnOther}})
	if r.Code != 0 || len(h.runs()) != 0 {
		t.Fatalf("exit %d, %d runs: a non-owner reached the injector", r.Code, len(h.runs()))
	}
}

func TestSockCodexConfig(t *testing.T) {
	for name, o := range map[string]rnOpts{
		"no workdir":      {Unset: []string{"SOCK_RUNNER_WORKDIR"}},
		"missing workdir": {Env: map[string]string{"SOCK_RUNNER_WORKDIR": "/nonexistent/x"}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newCodexHarness(t)
			if r := h.run(o); r.Code != 2 || len(h.runs()) != 0 {
				t.Fatalf("exit %d, %d runs", r.Code, len(h.runs()))
			}
		})
	}
	t.Run("no injector", func(t *testing.T) {
		h := newCodexHarness(t)
		h.removeStub("sock-codex-inject")
		if r := h.run(rnOpts{}); r.Code != 2 {
			t.Fatalf("exit %d", r.Code)
		}
	})
}
