package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPermissionPrompt(t *testing.T) {
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: `{"message":"secret text"}`}, "needs permission")
	if r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	got := s.Requests()
	if len(got) != 1 || got[0].Auth != "Bearer "+token || got[0].Text != "proj: needs permission" {
		t.Fatalf("requests %+v", got)
	}
}

func TestSockTokenWinsOverPluginOption(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other"}}, "x")
	if g := s.Requests(); len(g) != 1 || g[0].Auth != "Bearer "+token {
		t.Fatalf("%+v", g)
	}
}

func TestPluginOptionTokenUsedWhenSockTokenUnset(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_TOKEN": "", "CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_opt_opt", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "SOCK_URL": ""}}, "x")
	if g := s.Requests(); len(g) != 1 || g[0].Auth != "Bearer sock_pt_opt_opt" {
		t.Fatalf("%+v", g)
	}
}

func TestMissingTokenLogsAndExitsZero(t *testing.T) {
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_TOKEN": ""}}, "x")
	if r.Code != 0 || len(s.Requests()) != 0 || strings.Count(r.Log, "\n") != 1 {
		t.Fatalf("%+v reqs=%d", r, len(s.Requests()))
	}
}

func TestServerDownExitsZeroLogsOnce(t *testing.T) {
	s := newStub(t)
	s.setFail(func(int) error { return unavailable() })
	r := runHook(t, s.URL, hookEnv{}, "x")
	if r.Code != 0 || strings.Count(r.Log, "\n") != 1 {
		t.Fatalf("%+v", r)
	}
	if r.Elapsed.Seconds() > 15 {
		t.Fatalf("took %v", r.Elapsed)
	}
}

func TestTokenInvalidNoRetryStorm(t *testing.T) {
	s := newStub(t)
	s.setFail(func(int) error { return tokenInvalid() })
	r := runHook(t, s.URL, hookEnv{}, "x")
	if r.Code != 0 || len(s.Requests()) != 1 || strings.Count(r.Log, "\n") != 1 {
		t.Fatalf("%+v reqs=%d", r, len(s.Requests()))
	}
}

func TestSockMissingFromPath(t *testing.T) {
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": "/usr/bin:/bin"}}, "x")
	if r.Code != 0 || strings.Count(r.Log, "\n") != 1 || !strings.Contains(r.Log, "sock") {
		t.Fatalf("%+v", r)
	}
}

func TestStopGate(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_NOTIFY_ON_STOP": "false"}}, "--stop", "finished a turn")
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("sent %d with option false", n)
	}
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_NOTIFY_ON_STOP": "true"}}, "--stop", "finished a turn")
	runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_NOTIFY_ON_STOP": "1"}}, "--stop", "finished a turn")
	if n := len(s.Requests()); n != 2 {
		t.Fatalf("sent %d, want 2", n)
	}
}

func TestStdinShapesDoNotHang(t *testing.T) {
	s := newStub(t)
	for _, in := range []string{"", `{"hook_event_name":"Notification"}`, strings.Repeat("x", 1<<20)} {
		r := runHook(t, s.URL, hookEnv{Stdin: in}, "x")
		if r.Code != 0 || r.Elapsed.Seconds() > 8 {
			t.Fatalf("stdin len %d: %+v", len(in), r)
		}
	}
}

// Review Focus 1
func TestProjectNameLikeFlag(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/-v"}}, "needs permission")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "-v: needs permission" {
		t.Fatalf("%+v", g)
	}
}

// Review Focus 2
func TestProjectNameSanitized(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/a\nb\x1b[31mc"}}, "p")
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/" + strings.Repeat("n", 500)}}, "p")
	g := s.Requests()
	if len(g) != 2 {
		t.Fatalf("%+v", g)
	}
	if g[0].Text != "ab[31mc: p" {
		t.Fatalf("control chars kept: %q", g[0].Text)
	}
	if want := strings.Repeat("n", 100) + ": p"; g[1].Text != want {
		t.Fatalf("not cut: %d chars", len(g[1].Text))
	}
}

// Review Focus 3: a fake sock that prints the token to stderr and fails.
func TestTokenNeverInLog(t *testing.T) {
	s := newStub(t)
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "sock")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"boom token=$SOCK_TOKEN\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": tmp + ":/usr/bin:/bin"}}, "x")
	if r.Log == "" || strings.Contains(r.Log, token) {
		t.Fatalf("log %q", r.Log)
	}
	if !strings.Contains(r.Log, "[redacted]") {
		t.Fatalf("not redacted: %q", r.Log)
	}
}

func TestLogModeAndTruncation(t *testing.T) {
	s := newStub(t)
	s.setFail(func(int) error { return unavailable() })
	tmp := t.TempDir()
	state := filepath.Join(tmp, "state")
	dir := filepath.Join(state, "sock-integrations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.log"), []byte(strings.Repeat("y", 102401)), 0o600); err != nil {
		t.Fatal(err)
	}
	runHook(t, s.URL, hookEnv{Env: map[string]string{"XDG_STATE_HOME": state}}, "x")
	fi, err := os.Stat(filepath.Join(dir, "hook.log"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 1024 {
		t.Fatalf("not truncated: %d", fi.Size())
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

// Review Focus 4
func TestUnwritableLogDirStillExitsZero(t *testing.T) {
	s := newStub(t)
	s.setFail(func(int) error { return unavailable() })
	r := runHook(t, s.URL, hookEnv{HomeRO: true}, "x")
	if r.Code != 0 || r.Elapsed.Seconds() > 8 {
		t.Fatalf("%+v", r)
	}
}

// Review Focus 5
func TestHungServerKilledByWatchdog(t *testing.T) {
	s := newStub(t)
	blk := make(chan struct{})
	s.setBlock(blk)
	t.Cleanup(func() { close(blk) })
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_HOOK_TIMEOUT": "1"}}, "x")
	if r.Code != 0 || r.Elapsed.Seconds() > 6 || strings.Count(r.Log, "\n") != 1 {
		t.Fatalf("%+v", r)
	}
}

// Review Focus 6
func TestProjectFallsBackToPWD(t *testing.T) {
	s := newStub(t)
	cwd := filepath.Join(t.TempDir(), "fromcwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	runHook(t, s.URL, hookEnv{}, "needs permission")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "fromcwd: needs permission" {
		t.Fatalf("%+v", g)
	}
}
