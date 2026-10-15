package test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var rnUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (h *rnHarness) sessionFile() string { return filepath.Join(h.state, "session") }

func (h *rnHarness) storedSession() string {
	h.t.Helper()
	b, err := os.ReadFile(h.sessionFile())
	if err != nil {
		h.t.Fatalf("session file: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// sessionArg is the flag and id a claude run was given: --resume or --session-id.
func sessionArg(t *testing.T, r rnRun) (string, string) {
	t.Helper()
	for i, a := range r.Argv {
		if (a == "--resume" || a == "--session-id") && i+1 < len(r.Argv) {
			return a, r.Argv[i+1]
		}
	}
	t.Fatalf("no session flag in %v", r.Argv)
	return "", ""
}

// Without SOCK_RUNNER_SESSION, the first message starts a fresh claude session
// under a new id, kept in the state directory, and later messages resume it.
func TestRunnerCreatesItsOwnSession(t *testing.T) {
	h := newRunnerHarness(t)
	if r := h.run(rnOpts{Unset: []string{"SOCK_RUNNER_SESSION"}}); r.Code != 0 {
		t.Fatalf("first run exit %d: %q", r.Code, r.Stderr)
	}
	runs := h.runs()
	if len(runs) != 1 {
		t.Fatalf("%d runs", len(runs))
	}
	flag, id := sessionArg(t, runs[0])
	if flag != "--session-id" || !rnUUID.MatchString(id) {
		t.Fatalf("first run %s %q, want --session-id and a lowercase uuid", flag, id)
	}
	if got := h.storedSession(); got != id {
		t.Fatalf("stored %q, want %q", got, id)
	}
	if fi, err := os.Stat(h.sessionFile()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode %v %v", fi, err)
	}

	f := h.fields()
	f["id"] = rnOther
	env := map[string]string{"SOCK_MESSAGE_ID": rnOther}
	if r := h.run(rnOpts{Unset: []string{"SOCK_RUNNER_SESSION"}, Env: env, Stdin: rnMessage(f)}); r.Code != 0 {
		t.Fatalf("second run exit %d: %q", r.Code, r.Stderr)
	}
	runs = h.runs()
	if len(runs) != 2 {
		t.Fatalf("%d runs", len(runs))
	}
	var resumed bool
	for _, r := range runs {
		if flag, got := sessionArg(t, r); flag == "--resume" {
			resumed = got == id
		}
	}
	if !resumed {
		t.Fatalf("second run did not resume %s: %+v", id, runs)
	}
}

// A stored session claude no longer has is replaced in the same run: the
// message runs once, in a new session, and the new id is kept.
func TestRunnerReplacesAGoneSession(t *testing.T) {
	h := newRunnerHarness(t)
	gone := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.sessionFile(), []byte(gone+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := h.run(rnOpts{Unset: []string{"SOCK_RUNNER_SESSION"}, Env: map[string]string{"STUB_GONE": "1"}})
	if r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	var flags []string
	var fresh string
	for _, run := range h.runs() {
		flag, id := sessionArg(t, run)
		flags = append(flags, flag)
		if flag == "--session-id" {
			fresh = id
			if !strings.Contains(run.Stdin, rnText) {
				t.Fatal("the fresh session did not get the message")
			}
		}
	}
	slices.Sort(flags)
	if !slices.Equal(flags, []string{"--resume", "--session-id"}) {
		t.Fatalf("runs %v, want one failed resume then one fresh session", flags)
	}
	if !rnUUID.MatchString(fresh) || fresh == gone || h.storedSession() != fresh {
		t.Fatalf("fresh %q stored %q", fresh, h.storedSession())
	}
	if strings.Contains(h.log(), "No conversation") {
		t.Fatal("agent stderr reached the log")
	}
}

// A session file that does not hold a valid id is replaced, never passed on.
func TestRunnerReplacesACorruptSessionFile(t *testing.T) {
	for _, bad := range []string{"", "../x", "a b", "--resume", strings.Repeat("a", 200)} {
		t.Run(bad[:min(len(bad), 8)], func(t *testing.T) { rnCorruptSession(t, bad) })
	}
}

func rnCorruptSession(t *testing.T, bad string) {
	h := newRunnerHarness(t)
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.sessionFile(), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := h.run(rnOpts{Unset: []string{"SOCK_RUNNER_SESSION"}}); r.Code != 0 {
		t.Fatalf("%q: exit %d: %q", bad, r.Code, r.Stderr)
	}
	runs := h.runs()
	if len(runs) != 1 {
		t.Fatalf("%q: %d runs", bad, len(runs))
	}
	if flag, id := sessionArg(t, runs[0]); flag != "--session-id" || !rnUUID.MatchString(id) {
		t.Fatalf("%q: %s %q", bad, flag, id)
	}
}

// SOCK_RUNNER_SESSION, when set, is used as given and nothing is stored.
func TestRunnerSessionOverrideStoresNothing(t *testing.T) {
	h := newRunnerHarness(t)
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	if _, err := os.Stat(h.sessionFile()); !os.IsNotExist(err) {
		t.Fatalf("session file with an override: %v", err)
	}
}

// Codex prints its own session ids, so it still needs SOCK_RUNNER_SESSION.
func TestRunnerCodexStillNeedsASession(t *testing.T) {
	h := newRunnerHarness(t)
	r := h.run(rnOpts{Unset: []string{"SOCK_RUNNER_SESSION"}, Env: map[string]string{"SOCK_RUNNER_AGENT": "codex", "SOCK_RUNNER_PERMISSION": "read-only"}})
	h.wantNoRun(r, 2)
}
