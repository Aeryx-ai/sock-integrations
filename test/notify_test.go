package test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// failedSend matches the log line for a sock send that exited nonzero. The
// CLI's own error text is not asserted.
var failedSend = regexp.MustCompile(`sock send exit [1-9][0-9]*:`)

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

func TestPluginOptionTokenWinsOverSockToken(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "SOCK_URL": ""}}, "x")
	if g := s.Requests(); len(g) != 1 || g[0].Auth != "Bearer sock_pt_other_other" {
		t.Fatalf("%+v", g)
	}
}

// The plugin token must go only to the plugin URL, never to an ambient SOCK_URL.
func TestPluginTokenNeverReachesAmbientURL(t *testing.T) {
	plugin, ambient := newStub(t), newStub(t)
	runHook(t, ambient.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": plugin.URL}}, "x")
	if g := ambient.Requests(); len(g) != 0 {
		t.Fatalf("plugin token leaked to ambient SOCK_URL: %+v", g)
	}
	if g := plugin.Requests(); len(g) != 1 || g[0].Auth != "Bearer sock_pt_other_other" {
		t.Fatalf("%+v", g)
	}
}

// With no URL from the token's source, the CLI config applies and the ambient SOCK_URL does not.
func TestNoURLFromSourceUsesCLIConfig(t *testing.T) {
	cfgStub, ambient := newStub(t), newStub(t)
	cfg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfg, "sock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "sock", "config.toml"), []byte("url = \""+cfgStub.URL+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHook(t, ambient.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "XDG_CONFIG_HOME": cfg}}, "x")
	if g := ambient.Requests(); len(g) != 0 {
		t.Fatalf("token leaked to ambient SOCK_URL: %+v", g)
	}
	if g := cfgStub.Requests(); len(g) != 1 || g[0].Auth != "Bearer sock_pt_other_other" {
		t.Fatalf("%+v", g)
	}
}

// An env token must go only to SOCK_URL, never to the plugin URL.
func TestEnvTokenNeverReachesPluginURL(t *testing.T) {
	env, plugin := newStub(t), newStub(t)
	runHook(t, env.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_URL": plugin.URL}}, "x")
	if g := plugin.Requests(); len(g) != 0 {
		t.Fatalf("env token leaked to plugin URL: %+v", g)
	}
	if g := env.Requests(); len(g) != 1 || g[0].Auth != "Bearer "+token {
		t.Fatalf("%+v", g)
	}
}

// An ambient access gate belongs to the env-token deployment and must not go to the plugin URL.
func TestPluginTokenDropsAmbientAccessGate(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "SOCK_ACCESS_GATE": "gate-secret"}}, "x")
	g := s.Requests()
	if len(g) != 1 || g[0].Gate != "" {
		t.Fatalf("access gate leaked to plugin URL: %+v", g)
	}
}

// The env-token path keeps its gate.
func TestEnvTokenKeepsAccessGate(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_ACCESS_GATE": "gate-secret"}}, "x")
	g := s.Requests()
	if len(g) != 1 || g[0].Gate != "gate-secret" {
		t.Fatalf("%+v", g)
	}
}

// access_gate in the CLI config file belongs to the config's own URL, not the plugin URL.
func TestPluginTokenIgnoresConfigFileAccessGate(t *testing.T) {
	s := newStub(t)
	cfg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfg, "sock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "sock", "config.toml"), []byte("access_gate = \"file-gate\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "XDG_CONFIG_HOME": cfg}}, "x")
	g := s.Requests()
	if len(g) != 1 || g[0].Gate != "" {
		t.Fatalf("config file access gate leaked to plugin URL: %+v", g)
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
	if n := len(s.Requests()); n > 4 {
		t.Fatalf("%d requests in %v", n, r.Elapsed)
	}
	if !failedSend.MatchString(r.Log) {
		t.Fatalf("log line: %q", r.Log)
	}
	if r.Elapsed.Seconds() > 2+3 { // watchdog timeout plus slack
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
	if r.Code != 0 || strings.Count(r.Log, "\n") != 1 || !strings.Contains(r.Log, "sock not found") {
		t.Fatalf("%+v", r)
	}
}

func TestStopGate(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{}, "--stop", "finished a turn")
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("sent %d with both gates unset", n)
	}
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

func TestStdinHeldOpenIsBounded(t *testing.T) {
	s := newStub(t)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pw.Close(); _ = pr.Close() })
	r := runHook(t, s.URL, hookEnv{StdinFile: pr}, "x")
	if r.Code != 0 || r.Elapsed.Seconds() < 1.5 || r.Elapsed.Seconds() > 5 {
		t.Fatalf("%+v", r)
	}
	if n := len(s.Requests()); n != 1 {
		t.Fatalf("sent %d", n)
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
	if g[0].Text != "ab31mc: p" {
		t.Fatalf("control chars kept: %q", g[0].Text)
	}
	if want := strings.Repeat("n", 100) + ": p"; g[1].Text != want {
		t.Fatalf("not cut: %d chars", len(g[1].Text))
	}
}

func TestProjectNameAllowlist(t *testing.T) {
	cases := []struct{ name, dir, want string }{
		{"bidi override and C1 control removed", "/x/pro\u202eje\u0085ct", "project: p"},
		{"all non-ASCII becomes project", "/x/\u65e5\u672c\u8a9e", "project: p"},
		{"multibyte mixed with ASCII keeps ASCII", "/x/caf\u00e9-1.2_x y", "caf-1.2_x y: p"},
	}
	for _, c := range cases {
		for _, loc := range []string{"C", "en_US.UTF-8"} {
			s := newStub(t)
			runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": c.dir, "LANG": loc, "LC_ALL": loc}}, "p")
			if g := s.Requests(); len(g) != 1 || g[0].Text != c.want {
				t.Fatalf("%s (%s): %+v, want %q", c.name, loc, g, c.want)
			}
		}
	}
}

// A normal run, including reaping the watchdog, writes nothing to stderr.
func TestNormalRunSilentOnStderr(t *testing.T) {
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{}, "x")
	if r.Code != 0 || len(s.Requests()) != 1 || r.Stderr != "" {
		t.Fatalf("%+v", r)
	}
}

// A fake sock printing terminal escapes must not put control bytes in the log.
func TestLogHasNoControlCharacters(t *testing.T) {
	s := newStub(t)
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "sock")
	script := "#!/bin/sh\nprintf '\\033]0;pwned\\007\\033[2J\\tend\\r' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": tmp + ":/usr/bin:/bin", "SOCK_HOOK_TIMEOUT": "10"}}, "x")
	if !strings.Contains(r.Log, "pwned") {
		t.Fatalf("no log: %q", r.Log)
	}
	body := strings.TrimSuffix(r.Log, "\n")
	for i := range len(body) {
		if body[i] < 0x20 || body[i] == 0x7f {
			t.Fatalf("control byte %#x at %d in %q", body[i], i, r.Log)
		}
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
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": tmp + ":/usr/bin:/bin", "SOCK_HOOK_TIMEOUT": "10"}}, "x")
	if r.Log == "" || strings.Contains(r.Log, token) {
		t.Fatalf("log %q", r.Log)
	}
	if !strings.Contains(r.Log, "[redacted]") {
		t.Fatalf("not redacted: %q", r.Log)
	}
}

func TestLogCreatedWithPrivateModes(t *testing.T) {
	s := newStub(t)
	s.setFail(func(int) error { return unavailable() })
	state := filepath.Join(t.TempDir(), "state")
	runHook(t, s.URL, hookEnv{Env: map[string]string{"XDG_STATE_HOME": state}}, "x")
	dir := filepath.Join(state, "sock-integrations")
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "hook.log"))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("dir %v file %v", di.Mode().Perm(), fi.Mode().Perm())
	}
}

func TestLogTruncatedAboveLimit(t *testing.T) {
	s := newStub(t)
	s.setFail(func(int) error { return unavailable() })
	state := filepath.Join(t.TempDir(), "state")
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
}

// Review Focus 4
func TestUnwritableLogDirStillExitsZero(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := newStub(t)
	s.setFail(func(int) error { return unavailable() })
	r := runHook(t, s.URL, hookEnv{HomeRO: true}, "x")
	if r.Code != 0 || r.Elapsed.Seconds() > 2+3 || r.Log != "" { // watchdog timeout plus slack
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
	if r.Code != 0 || r.Elapsed.Seconds() > 1+3 || strings.Count(r.Log, "\n") != 1 || !failedSend.MatchString(r.Log) { // watchdog timeout plus slack
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

// Token straddling the 300 byte cut must not leave a prefix in the log.
func TestTokenStraddlingCutIsRedacted(t *testing.T) {
	s := newStub(t)
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "sock")
	script := "#!/bin/sh\nprintf '%0290d' 0 >&2\necho \"$SOCK_TOKEN\" >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": tmp + ":/usr/bin:/bin", "SOCK_HOOK_TIMEOUT": "10"}}, "x")
	if r.Log == "" {
		t.Fatal("no log")
	}
	if strings.Contains(r.Log, token[:8]) {
		t.Fatalf("token prefix leaked: %q", r.Log)
	}
}

// A token split by a control byte is only whole after control characters are
// stripped. The redaction must run after the strip, before the 300 byte cut.
func TestTokenSplitByControlByteIsRedacted(t *testing.T) {
	s := newStub(t)
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "sock")
	script := "#!/bin/sh\nprintf '%0290d' 0 >&2\nprintf '%s\\001%s\\n' \"${SOCK_TOKEN%????????}\" \"${SOCK_TOKEN#${SOCK_TOKEN%????????}}\" >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": tmp + ":/usr/bin:/bin", "SOCK_HOOK_TIMEOUT": "10"}}, "x")
	if r.Log == "" {
		t.Fatal("no log")
	}
	if strings.Contains(r.Log, token[:8]) {
		t.Fatalf("token prefix leaked: %q", r.Log)
	}
}

func TestInvalidTimeoutFallsBackToDefault(t *testing.T) {
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_HOOK_TIMEOUT": "abc"}}, "x")
	if r.Code != 0 || len(s.Requests()) != 1 || r.Log != "" {
		t.Fatalf("%+v reqs=%d", r, len(s.Requests()))
	}
}

// --- what the owner reads: the ask, not a fixed phrase ----------------------

func hookJSON(fields map[string]any) string {
	b, _ := json.Marshal(fields)
	return string(b)
}

func stopInput(session, last string) string {
	return hookJSON(map[string]any{"session_id": session, "hook_event_name": "Stop", "last_assistant_message": last, "stop_hook_active": false})
}

func idleInput(session string) string {
	return hookJSON(map[string]any{"session_id": session, "hook_event_name": "Notification", "notification_type": "idle_prompt", "message": "Claude is waiting for your input"})
}

const askMessage = "I ran the tests and they pass.\n\n```sh\nexport SECRET_TOKEN_9f1=abc\n```\n\nShould I push the branch, or wait for the review?\n\n1) push now\n2) wait"

const askExpected = "proj: Should I push the branch, or wait for the review?\n\n1) push now\n2) wait"

func TestPermissionPromptForwardsTheNotificationText(t *testing.T) {
	s := newStub(t)
	in := hookJSON(map[string]any{"hook_event_name": "Notification", "notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: in}, "--permission")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: Claude needs your permission to use Bash" {
		t.Fatalf("%+v", g)
	}
}

func TestStopRecordsTheAskAndSendsNothing(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Stdin: stopInput("s1", askMessage)}, "--stop")
	if g := s.Requests(); len(g) != 0 {
		t.Fatalf("stop sent %+v", g)
	}
}

func TestIdleSendsTheRecordedAskOnce(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s1", askMessage), State: state}, "--stop")
	runHook(t, s.URL, hookEnv{Env: env, Stdin: idleInput("s1"), State: state}, "--idle")
	g := s.Requests()
	if len(g) != 1 || g[0].Text != askExpected {
		t.Fatalf("%+v", g)
	}
	if strings.Contains(g[0].Text, "SECRET_TOKEN_9f1") {
		t.Fatalf("code block forwarded: %q", g[0].Text)
	}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: idleInput("s1"), State: state}, "--idle")
	if g := s.Requests(); len(g) != 1 {
		t.Fatalf("the same ask was reposted: %+v", g)
	}
}

func TestIdleWithNoAskSendsNothing(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	runHook(t, s.URL, hookEnv{Stdin: stopInput("s2", "Done. The tests pass and the branch is pushed."), State: state}, "--stop")
	runHook(t, s.URL, hookEnv{Stdin: idleInput("s2"), State: state}, "--idle")
	if g := s.Requests(); len(g) != 0 {
		t.Fatalf("nothing was pending, yet %+v", g)
	}
}

func TestANewAskAfterTheSameSessionIsSent(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	runHook(t, s.URL, hookEnv{Stdin: stopInput("s3", "First question?"), State: state}, "--stop")
	runHook(t, s.URL, hookEnv{Stdin: idleInput("s3"), State: state}, "--idle")
	runHook(t, s.URL, hookEnv{Stdin: stopInput("s3", "Second question?"), State: state}, "--stop")
	runHook(t, s.URL, hookEnv{Stdin: idleInput("s3"), State: state}, "--idle")
	g := s.Requests()
	if len(g) != 2 || !strings.HasSuffix(g[0].Text, "First question?") || !strings.HasSuffix(g[1].Text, "Second question?") {
		t.Fatalf("%+v", g)
	}
}

func TestAskIsCappedAt600Bytes(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	long := strings.Repeat("Which of these many options would you like me to take for the deployment? ", 20)
	runHook(t, s.URL, hookEnv{Stdin: stopInput("s4", long), State: state}, "--stop")
	runHook(t, s.URL, hookEnv{Stdin: idleInput("s4"), State: state}, "--idle")
	g := s.Requests()
	if len(g) != 1 || len(g[0].Text) > 600+len("test: ") || !strings.HasPrefix(g[0].Text, "test: Which of these") {
		t.Fatalf("len %d %q", len(g[0].Text), g[0].Text)
	}
}

func TestIdleFallsBackToTheTranscript(t *testing.T) {
	s := newStub(t)
	tmp := t.TempDir()
	transcript := filepath.Join(tmp, "s5.jsonl")
	lines := []string{
		hookJSON(map[string]any{"type": "user", "message": map[string]any{"content": "hi"}}),
		hookJSON(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": "Earlier answer."}}}}),
		hookJSON(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{{"type": "tool_use", "name": "Bash"}, {"type": "text", "text": "Deploy to staging first, or straight to production?"}}}}),
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := hookJSON(map[string]any{"session_id": "s5", "hook_event_name": "Notification", "notification_type": "idle_prompt", "transcript_path": transcript})
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: in}, "--idle")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: Deploy to staging first, or straight to production?" {
		t.Fatalf("%+v", g)
	}
}

func TestStopWithAskNowSendsAtOnce(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s6", askMessage)}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 1 || g[0].Text != askExpected {
		t.Fatalf("%+v", g)
	}
}

func TestNotifyOnStopStillSendsFinishedATurn(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj", "CLAUDE_PLUGIN_OPTION_NOTIFY_ON_STOP": "true"}, Stdin: stopInput("s7", "Done.")}, "--stop")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: finished a turn" {
		t.Fatalf("%+v", g)
	}
}

// binWithoutJq builds a PATH directory holding sock and the utilities the hook
// needs, and no jq: macOS ships one in /usr/bin, so the system dirs cannot be
// on the PATH.
func binWithoutJq(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink(sockBin(t), filepath.Join(dir, "sock")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"env", "bash", "sh", "tr", "cut", "basename", "awk", "sed", "head", "tail", "shasum", "sha256sum", "find", "wc", "mktemp", "cat", "mkdir", "date", "sleep", "kill", "pkill", "rm"} {
		for _, d := range []string{"/usr/bin", "/bin"} {
			if _, err := os.Stat(filepath.Join(d, name)); err == nil {
				if err := os.Symlink(filepath.Join(d, name), filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
	}
	return dir
}

func TestWithoutJqPermissionKeepsThePhraseAndIdleStaysQuiet(t *testing.T) {
	s := newStub(t)
	env := map[string]string{"PATH": binWithoutJq(t), "CLAUDE_PROJECT_DIR": "/x/proj"}
	in := hookJSON(map[string]any{"hook_event_name": "Notification", "notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})
	r := runHook(t, s.URL, hookEnv{Env: env, Stdin: in}, "--permission")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: needs permission" {
		t.Fatalf("%+v", g)
	}
	if !strings.Contains(r.Log, "jq") {
		t.Fatalf("log %q", r.Log)
	}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: idleInput("s8")}, "--idle")
	if g := s.Requests(); len(g) != 1 {
		t.Fatalf("idle without jq sent: %+v", g)
	}
}

// --- findings of the fresh-context security review of 9e8a549 -------------

func TestIdleFallbackSkipsToolOnlyEntries(t *testing.T) {
	s := newStub(t)
	tmp := t.TempDir()
	transcript := filepath.Join(tmp, "s9.jsonl")
	lines := []string{
		hookJSON(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": "Deploy now?"}}}}),
		hookJSON(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{{"type": "tool_use", "name": "Bash"}}}}),
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := hookJSON(map[string]any{"session_id": "s9", "hook_event_name": "Notification", "notification_type": "idle_prompt", "transcript_path": transcript})
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: in}, "--idle")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: Deploy now?" {
		t.Fatalf("%+v", g)
	}
}

func TestANewTurnWithTheSameAskIsSentAgain(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	for range 2 {
		runHook(t, s.URL, hookEnv{Stdin: stopInput("s10", "Proceed?"), State: state}, "--stop")
		runHook(t, s.URL, hookEnv{Stdin: idleInput("s10"), State: state}, "--idle")
		runHook(t, s.URL, hookEnv{Stdin: idleInput("s10"), State: state}, "--idle")
	}
	if g := s.Requests(); len(g) != 2 {
		t.Fatalf("two turns asked the same thing, %d sent: %+v", len(g), g)
	}
}

func TestWithoutAHashToolNothingIsSuppressed(t *testing.T) {
	s := newStub(t)
	dir := binWithoutJq(t)
	for _, name := range []string{"shasum", "sha256sum"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"} {
		if _, err := os.Stat(filepath.Join(d, "jq")); err == nil {
			if err := os.Symlink(filepath.Join(d, "jq"), filepath.Join(dir, "jq")); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	state := t.TempDir()
	env := map[string]string{"PATH": dir}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s11", "First?"), State: state}, "--stop", "--ask-now")
	runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s11", "Second?"), State: state}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 2 {
		t.Fatalf("%+v", g)
	}
}

func TestSecretsAreRedactedBeforeSending(t *testing.T) {
	s := newStub(t)
	msg := "Here is what I found: the token is " + token + " and AWS_SECRET_ACCESS_KEY=zzz9 plus ghp_abcdefghijklmnop and https://b.s3/x?X-Amz-Signature=deadbeef. Proceed?"
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s12", msg)}, "--stop", "--ask-now")
	g := s.Requests()
	if len(g) != 1 {
		t.Fatalf("%+v", g)
	}
	for _, bad := range []string{token, "zzz9", "ghp_abcdefghijklmnop", "deadbeef"} {
		if strings.Contains(g[0].Text, bad) {
			t.Fatalf("%q reached the message: %q", bad, g[0].Text)
		}
	}
	if !strings.HasSuffix(g[0].Text, "Proceed?") {
		t.Fatalf("the ask itself was lost: %q", g[0].Text)
	}
}

// The fenced blocks sit after the question, inside the paragraphs the ask
// takes, so only fence handling can keep them out.
func TestOtherFenceShapesAreDropped(t *testing.T) {
	cases := map[string]string{
		"tilde":            "Retry the deploy?\n\n~~~\nTILDE_SECRET=1\n~~~",
		"long tilde":       "Retry the deploy?\n\n~~~~\n~~~\nTILDE_SECRET=1\n~~~~",
		"four backticks":   "Retry the deploy?\n\n````md\n```\nNESTED_SECRET=2\n```\n````",
		"closer with text": "Retry the deploy?\n\n```\n``` not a closer\nNESTED_SECRET=2\n```",
		"tilde after text": "Retry the deploy?\n\n~~~\n```\nTILDE_SECRET=1\n```\n~~~",
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s13", msg)}, "--stop", "--ask-now")
			if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: Retry the deploy?" {
				t.Fatalf("%+v", g)
			}
		})
	}
}

// A question mark inside a fence is code, not the ask.
func TestAQuestionMarkInAFenceIsNotTheAsk(t *testing.T) {
	cases := map[string]string{
		"tilde":          "Retry the deploy?\n\nDone.\n\n~~~\nwhy?=SECRETQ\n~~~",
		"four backticks": "Retry the deploy?\n\nDone.\n\n````\n```\nwhy?=SECRETQ\n```\nmore?\n````",
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s13q", msg)}, "--stop", "--ask-now")
			if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: Retry the deploy?\n\nDone." {
				t.Fatalf("%+v", g)
			}
		})
	}
}

// cutCases are texts whose byte cap lands inside a 2, 3 or 4 byte character
// for some prefix length.
func cutCases(limit int) map[string]string {
	cases := map[string]string{}
	for _, r := range []string{"é", "€", "😀"} {
		for _, prefix := range []string{"Ok?", "Ok? ", "Ok?  "} {
			if (limit-len(prefix))%len(r) != 0 {
				cases[fmt.Sprintf("%d byte after %d", len(r), len(prefix))] = prefix + strings.Repeat(r, limit)
			}
		}
	}
	return cases
}

func TestCapKeepsValidUTF8(t *testing.T) {
	for name, msg := range cutCases(600) {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s14", msg)}, "--stop", "--ask-now")
			g := s.Requests()
			if len(g) != 1 {
				t.Fatalf("nothing sent: %+v", g)
			}
			if n := len(g[0].Text) - len("proj: "); !utf8.ValidString(g[0].Text) || n > 600 || n < 597 {
				t.Fatalf("len %d valid %v", n, utf8.ValidString(g[0].Text))
			}
		})
	}
}

func TestPermissionCapKeepsValidUTF8(t *testing.T) {
	for name, msg := range cutCases(300) {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			in := hookJSON(map[string]any{"hook_event_name": "Notification", "notification_type": "permission_prompt", "message": msg})
			runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: in}, "--permission")
			g := s.Requests()
			if len(g) != 1 {
				t.Fatalf("nothing sent: %+v", g)
			}
			if n := len(g[0].Text) - len("proj: "); !utf8.ValidString(g[0].Text) || n > 300 || n < 297 {
				t.Fatalf("len %d valid %v", n, utf8.ValidString(g[0].Text))
			}
		})
	}
}

// Blank-line separated items each make a paragraph; the ask still starts at
// the paragraph before the list.
func TestALooseListStartsAtTheParagraphBeforeIt(t *testing.T) {
	s := newStub(t)
	msg := "Done.\n\nOptions:\n\n1. alpha\n\n2. beta\n\n3. gamma"
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s16", msg)}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: Options:\n\n1. alpha\n\n2. beta\n\n3. gamma" {
		t.Fatalf("%+v", g)
	}
}

func TestAListFollowedByTextIsNotTrailing(t *testing.T) {
	s := newStub(t)
	msg := "Steps taken:\n\n1. built\n\nAll green."
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s17", msg)}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 0 {
		t.Fatalf("no trailing list, yet %+v", g)
	}
}

func TestIdleIgnoresATranscriptThatIsNotARegularFile(t *testing.T) {
	s := newStub(t)
	in := hookJSON(map[string]any{"session_id": "s15", "hook_event_name": "Notification", "notification_type": "idle_prompt", "transcript_path": "/dev/zero"})
	r := runHook(t, s.URL, hookEnv{Stdin: in}, "--idle")
	if g := s.Requests(); len(g) != 0 || r.Elapsed > 5*time.Second {
		t.Fatalf("%+v %v", g, r.Elapsed)
	}
}

// --- machine key: each project sends as its own puppet (ADR 10) -----------

const machineKey = "sock_mk_11111111-1111-4111-8111-111111111111_" + "MACHINEKEYSENTINELxxxxxxxxxxxxxxxxxxxxxxxx"

// fakeSockForKeys answers `sock puppets ensure <dir>` the way the CLI does:
// it writes a puppet token under the state dir's puppets cache and prints the
// file's path. mode changes that: "fail" exits non-zero with the key on
// stderr, "slow" sleeps 5 s first, "outside" prints a path outside the cache,
// "notpuppet" writes a token that is not a sock_pt_. `sock send` records its
// token, its text and its whole environment.
func fakeSockForKeys(t *testing.T, mode string) (dir, rec string) {
	t.Helper()
	dir = t.TempDir()
	rec = filepath.Join(dir, "rec")
	if err := os.MkdirAll(rec, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside-token")
	if err := os.WriteFile(outside, []byte("sock_pt_outside_secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
rec="` + rec + `"
mode="` + mode + `"
for a in "$@"; do last=$a; done
case "$*" in
*"puppets ensure"*)
	printf '%s\n' "$SOCK_TOKEN" > "$rec/ensure-token"
	printf '%s\n' "$last" > "$rec/ensure-dir"
	case $mode in
	fail) echo "ensure failed for $SOCK_TOKEN" >&2; exit 3 ;;
	slow) sleep 5 ;;
	outside) echo "printed outside for $SOCK_TOKEN" >&2; printf '%s\n' "` + outside + `"; exit 0 ;;
	esac
	# The CLI prints a cleaned path (filepath.Join).
	cache=$(printf '%s' "${XDG_STATE_HOME:-$HOME/.local/state}/sock-integrations/puppets" | tr -s /)
	mkdir -p "$cache"
	tok=sock_pt_projectpuppet_secret
	[ "$mode" = notpuppet ] && tok=sock_mk_not_a_puppet
	printf '%s\n' "$tok" > "$cache/0123456789abcdef0123456789abcdef"
	printf '%s\n' "$cache/0123456789abcdef0123456789abcdef"
	;;
*send*)
	printf '%s\n' "$SOCK_TOKEN" > "$rec/send-token"
	env > "$rec/send-env"
	cat > "$rec/send-text"
	;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "sock"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, rec
}

func readRec(t *testing.T, rec, name string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(rec, name))
	return strings.TrimSpace(string(b))
}

func TestMachineKeySendsAsTheProjectPuppet(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockForKeys(t, "")
	runHook(t, s.URL, hookEnv{Env: machineKeyEnv(bin), Stdin: stopInput("k1", "Ship it?")}, "--stop", "--ask-now")
	if got := readRec(t, rec, "ensure-token"); got != machineKey {
		t.Fatalf("ensure ran with %q", got)
	}
	if got := readRec(t, rec, "ensure-dir"); got != "/x/proj" {
		t.Fatalf("ensure dir %q", got)
	}
	if got := readRec(t, rec, "send-token"); got != "sock_pt_projectpuppet_secret" {
		t.Fatalf("send ran with %q, want the project puppet's token", got)
	}
	if got := readRec(t, rec, "send-text"); got != "proj: Ship it?" {
		t.Fatalf("sent %q", got)
	}
}

func TestMachineKeyEnsureFailureSendsNothingAndHidesTheKey(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockForKeys(t, "fail")
	env := map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_TOKEN": machineKey, "SOCK_HOOK_TIMEOUT": fakeSockTimeout}
	r := runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("k2", "Ship it?")}, "--stop", "--ask-now")
	if _, err := os.Stat(filepath.Join(rec, "send-token")); err == nil {
		t.Fatal("sent after ensure failed")
	}
	if !strings.Contains(r.Log, "puppets ensure") || strings.Contains(r.Log, "MACHINEKEYSENTINEL") {
		t.Fatalf("log %q", r.Log)
	}
	if r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
}

func TestPluginOptionMachineKeyWorksToo(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockForKeys(t, "")
	env := map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_TOKEN": "", "CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": machineKey, "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "CLAUDE_PROJECT_DIR": "/x/proj", "SOCK_HOOK_TIMEOUT": fakeSockTimeout}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: hookJSON(map[string]any{"hook_event_name": "Notification", "notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})}, "--permission")
	if got := readRec(t, rec, "send-token"); got != "sock_pt_projectpuppet_secret" {
		t.Fatalf("send ran with %q", got)
	}
}

// fakeSockTimeout is the watchdog for tests whose sock is a fresh script: its
// first exec can take seconds on a loaded machine, past runHook's default 2.
const fakeSockTimeout = "10"

func machineKeyEnv(bin string) map[string]string {
	return map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_TOKEN": machineKey, "CLAUDE_PROJECT_DIR": "/x/proj", "SOCK_HOOK_TIMEOUT": fakeSockTimeout}
}

func sent(rec string) bool {
	_, err := os.Stat(filepath.Join(rec, "send-token"))
	return err == nil
}

func TestMachineKeyEnsureIsBoundedByTheWatchdog(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockForKeys(t, "slow")
	env := machineKeyEnv(bin)
	env["SOCK_HOOK_TIMEOUT"] = "1"
	r := runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("k4", "Ship it?")}, "--stop", "--ask-now")
	if r.Elapsed > 4*time.Second {
		t.Fatalf("hook took %v with a 1 s timeout", r.Elapsed)
	}
	if sent(rec) {
		t.Fatal("sent after ensure timed out")
	}
}

func TestMachineKeyTokenFileOutsideTheCacheIsRefused(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockForKeys(t, "outside")
	r := runHook(t, s.URL, hookEnv{Env: machineKeyEnv(bin), Stdin: stopInput("k5", "Ship it?")}, "--stop", "--ask-now")
	if sent(rec) {
		t.Fatalf("sent with a token file outside the cache (%q)", readRec(t, rec, "send-token"))
	}
	if !strings.Contains(r.Log, "puppets ensure") {
		t.Fatalf("log %q", r.Log)
	}
}

func TestMachineKeyTokenMustBeAPuppetToken(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockForKeys(t, "notpuppet")
	runHook(t, s.URL, hookEnv{Env: machineKeyEnv(bin), Stdin: stopInput("k6", "Ship it?")}, "--stop", "--ask-now")
	if sent(rec) {
		t.Fatal("sent with a token that is not a puppet token")
	}
}

func TestMachineKeyIsNotInTheSendEnvironment(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockForKeys(t, "")
	env := machineKeyEnv(bin)
	env["SOCK_TOKEN"] = ""
	env["CLAUDE_PLUGIN_OPTION_SOCK_TOKEN"] = machineKey
	env["CLAUDE_PLUGIN_OPTION_SOCK_URL"] = s.URL
	runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("k7", "Ship it?")}, "--stop", "--ask-now")
	if !sent(rec) {
		t.Fatal("nothing sent")
	}
	if e := readRec(t, rec, "send-env"); strings.Contains(e, "MACHINEKEYSENTINEL") {
		t.Fatal("the machine key reached sock send's environment")
	}
}

func TestMachineKeyEnsureFailureLogsItsRedactedStderr(t *testing.T) {
	s := newStub(t)
	bin, _ := fakeSockForKeys(t, "fail")
	r := runHook(t, s.URL, hookEnv{Env: machineKeyEnv(bin), Stdin: stopInput("k8", "Ship it?")}, "--stop", "--ask-now")
	if !strings.Contains(r.Log, "ensure failed for [redacted]") || strings.Contains(r.Log, "MACHINEKEYSENTINEL") {
		t.Fatalf("log %q", r.Log)
	}
}

// The CLI prints a cleaned path, so a state dir with a trailing or doubled
// slash must still match it.
func TestMachineKeyStateDirWithATrailingSlash(t *testing.T) {
	cases := map[string]func(dir string) map[string]string{
		"XDG_STATE_HOME": func(dir string) map[string]string { return map[string]string{"XDG_STATE_HOME": dir + "/"} },
		"HOME":           func(dir string) map[string]string { return map[string]string{"XDG_STATE_HOME": "", "HOME": dir + "/"} },
	}
	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			bin, rec := fakeSockForKeys(t, "")
			env := machineKeyEnv(bin)
			for k, v := range vars(t.TempDir()) {
				env[k] = v
			}
			runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("k10", "Ship it?")}, "--stop", "--ask-now")
			if got := readRec(t, rec, "send-token"); got != "sock_pt_projectpuppet_secret" {
				t.Fatalf("send ran with %q", got)
			}
		})
	}
}

func TestMachineKeyUnusablePathLogsItsRedactedStderr(t *testing.T) {
	s := newStub(t)
	bin, _ := fakeSockForKeys(t, "outside")
	r := runHook(t, s.URL, hookEnv{Env: machineKeyEnv(bin), Stdin: stopInput("k11", "Ship it?")}, "--stop", "--ask-now")
	if !strings.Contains(r.Log, "printed outside for [redacted]") || strings.Contains(r.Log, "MACHINEKEYSENTINEL") {
		t.Fatalf("log %q", r.Log)
	}
}

func TestLeadingDashProjectKeepsItsName(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "-rf"}, Stdin: stopInput("k9", "Ship it?")}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "-rf: Ship it?" {
		t.Fatalf("%+v", g)
	}
}

// --- deferred hook minors (integrations-r3j) --------------------------------

// fakeSock writes a sock that runs body for every call.
func fakeSock(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sock"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWatchdogKillsASockThatIgnoresTERM(t *testing.T) {
	s := newStub(t)
	bin := fakeSock(t, "trap '' TERM\nwhile :; do sleep 1; done")
	env := map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_HOOK_TIMEOUT": "1"}
	r := runHook(t, s.URL, hookEnv{Env: env}, "x")
	if r.Elapsed > 6*time.Second {
		t.Fatalf("hook took %v: a TERM-ignoring sock was never killed", r.Elapsed)
	}
}

func TestExistingHookLogIsMadePrivate(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	dir := filepath.Join(state, "sock-integrations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "hook.log")
	if err := os.WriteFile(log, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(log, 0o644); err != nil {
		t.Fatal(err)
	}
	runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": "/usr/bin:/bin"}, State: state}, "x")
	fi, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("hook.log mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestNULBytesInSockStderrNeverReachTheLog(t *testing.T) {
	s := newStub(t)
	bin := fakeSock(t, "printf 'bad\\000news\\001' >&2; exit 4")
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_HOOK_TIMEOUT": fakeSockTimeout}}, "x")
	if strings.ContainsRune(r.Log, 0) || !strings.Contains(r.Log, "badnews") {
		t.Fatalf("log %q", r.Log)
	}
	if strings.Contains(r.Stderr, "null byte") {
		t.Fatalf("hook stderr %q", r.Stderr)
	}
}

// --- Codex plugin route (integrations-zpe) -----------------------------------

// fakeSockAsPuppet records each call's argv (one per line), stdin and
// environment under rec. With refuse set it fails the way sock v0.2.5 does
// when it finds no puppet credential.
func fakeSockAsPuppet(t *testing.T, refuse bool) (dir, rec string) {
	t.Helper()
	dir = t.TempDir()
	rec = filepath.Join(dir, "rec")
	if err := os.MkdirAll(rec, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `rec="` + rec + `"
echo call >> "$rec/calls"
printf '%s\n' "$@" > "$rec/args"
env > "$rec/env"
cat > "$rec/text"
`
	if refuse {
		body += "echo 'sock: send --as-puppet: no puppet credential; set SOCK_TOKEN, or run sock machine-key use or sock puppets use' >&2\nexit 1"
	}
	return fakeSock(t, body), rec
}

func codexPluginEnv(bin string) map[string]string {
	return map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_TOKEN": "", "PLUGIN_ROOT": "/x/plugin", "CLAUDE_PROJECT_DIR": "/x/proj", "SOCK_ACCESS_GATE": "gate-secret", "SOCK_HOOK_TIMEOUT": fakeSockTimeout}
}

// With no SOCK_TOKEN the hook sends as the puppet the plugin's sock-mcp
// resolves, never with a token of its own and never as the login.
func TestCodexPluginRouteSendsAsThePluginPuppet(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockAsPuppet(t, false)
	r := runHook(t, s.URL, hookEnv{Env: codexPluginEnv(bin), Stdin: stopInput("z1", "Ship it?")}, "--stop", "--ask-now")
	if got := readRec(t, rec, "args"); got != "send\n--as-puppet\n--project\n/x/plugin" {
		t.Fatalf("argv %q", got)
	}
	if got := readRec(t, rec, "text"); got != "proj: Ship it?" {
		t.Fatalf("stdin %q", got)
	}
	env := readRec(t, rec, "env")
	for _, v := range []string{"SOCK_TOKEN=", "SOCK_URL=", "SOCK_ACCESS_GATE=", "CLAUDE_PROJECT_DIR="} {
		if regexp.MustCompile(`(?m)^` + v).MatchString(env) {
			t.Fatalf("%s reached sock send: the config must pick the credential and server, as for sock-mcp", v)
		}
	}
	if r.Log != "" {
		t.Fatalf("log %q", r.Log)
	}
}

func TestCodexPluginRouteRefusalIsLoggedAndNothingElse(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockAsPuppet(t, true)
	r := runHook(t, s.URL, hookEnv{Env: codexPluginEnv(bin)}, "needs permission")
	if r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	if got := readRec(t, rec, "calls"); got != "call" {
		t.Fatalf("sock calls %q, want one", got)
	}
	if strings.Count(r.Log, "\n") != 1 || !strings.Contains(r.Log, "sock send exit 1: sock: send --as-puppet: no puppet credential") {
		t.Fatalf("log %q", r.Log)
	}
	if len(s.Requests()) != 0 {
		t.Fatal("a request reached the server")
	}
}

// The plugin's sock-mcp never sees SOCK_TOKEN, so an exported one must not
// make the hooks send as a different puppet.
func TestCodexPluginRouteIgnoresAnExportedSockToken(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockAsPuppet(t, false)
	env := codexPluginEnv(bin)
	env["SOCK_TOKEN"] = token
	runHook(t, s.URL, hookEnv{Env: env}, "x")
	if got := readRec(t, rec, "args"); got != "send\n--as-puppet\n--project\n/x/plugin" {
		t.Fatalf("argv %q", got)
	}
	if e := readRec(t, rec, "env"); regexp.MustCompile(`(?m)^SOCK_TOKEN=`).MatchString(e) || strings.Contains(e, token) {
		t.Fatal("the exported SOCK_TOKEN reached sock send")
	}
	if len(s.Requests()) != 0 {
		t.Fatal("a request reached the server")
	}
}

func TestClaudePluginWithPluginRootIsUnchanged(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"PLUGIN_ROOT": "/x/plugin", "SOCK_TOKEN": "", "CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL}}, "x")
	if g := s.Requests(); len(g) != 1 || g[0].Auth != "Bearer sock_pt_other_other" {
		t.Fatalf("%+v", g)
	}
}

// --- notify.sh security review fixes -----------------------------------------

// A backtick in a backtick opener's info string makes it text under
// CommonMark; the hook still treats it as an opener so the code stays home.
func TestBacktickInfoStringStillOpensAFence(t *testing.T) {
	s := newStub(t)
	msg := "Which value do you want?\n\n```sh `env`\nDB password hunter2\n```"
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("f1", msg)}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: Which value do you want?" {
		t.Fatalf("%+v", g)
	}
}

// A closing fence takes at most 3 spaces of indent.
func TestAFenceIndentedFourSpacesDoesNotClose(t *testing.T) {
	s := newStub(t)
	msg := "Retry the deploy?\n\n```\nfoo\n    ```\nDB_PASS hunter2 why?\n```\n"
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("f2", msg)}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: Retry the deploy?" {
		t.Fatalf("%+v", g)
	}
	s2 := newStub(t)
	msg = "Retry the deploy?\n\n```\nfoo\n   ```\nShip it?"
	runHook(t, s2.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("f3", msg)}, "--stop", "--ask-now")
	if g := s2.Requests(); len(g) != 1 || g[0].Text != "proj: Ship it?" {
		t.Fatalf("a 3-space closer must close: %+v", g)
	}
}

// A key straddling a byte cap is redacted before the cut, not sent in part.
func TestSecretStraddlingTheCapIsRedacted(t *testing.T) {
	key := " sk-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	t.Run("ask", func(t *testing.T) {
		s := newStub(t)
		runHook(t, s.URL, hookEnv{Stdin: stopInput("c1", "Use it? "+strings.Repeat("a", 580)+key)}, "--stop", "--ask-now")
		if g := s.Requests(); len(g) != 1 || strings.Contains(g[0].Text, "sk-A") {
			t.Fatalf("%+v", g)
		}
	})
	t.Run("permission", func(t *testing.T) {
		s := newStub(t)
		in := hookJSON(map[string]any{"message": strings.Repeat("a", 280) + key})
		runHook(t, s.URL, hookEnv{Stdin: in}, "--permission")
		if g := s.Requests(); len(g) != 1 || strings.Contains(g[0].Text, "sk-A") {
			t.Fatalf("%+v", g)
		}
	})
}
