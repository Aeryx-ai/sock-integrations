package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	if r.Code != 0 || strings.Count(r.Log, "\n") != 1 || !strings.Contains(r.Log, "sock") {
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
