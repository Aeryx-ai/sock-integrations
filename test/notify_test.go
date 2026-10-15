package test

import (
	"encoding/json"
	"errors"
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
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}}, "--permission")
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
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "SOCK_URL": ""}}, "--permission")
	if g := s.Requests(); len(g) != 1 || g[0].Auth != "Bearer sock_pt_other_other" {
		t.Fatalf("%+v", g)
	}
}

// The plugin token must go only to the plugin URL, never to an ambient SOCK_URL.
func TestPluginTokenNeverReachesAmbientURL(t *testing.T) {
	plugin, ambient := newStub(t), newStub(t)
	runHook(t, ambient.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": plugin.URL}}, "--permission")
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
	runHook(t, ambient.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "XDG_CONFIG_HOME": cfg}}, "--permission")
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
	runHook(t, env.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_URL": plugin.URL}}, "--permission")
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
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "SOCK_ACCESS_GATE": "gate-secret"}}, "--permission")
	g := s.Requests()
	if len(g) != 1 || g[0].Gate != "" {
		t.Fatalf("access gate leaked to plugin URL: %+v", g)
	}
}

// The env-token path keeps its gate.
func TestEnvTokenKeepsAccessGate(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_ACCESS_GATE": "gate-secret"}}, "--permission")
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
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "XDG_CONFIG_HOME": cfg}}, "--permission")
	g := s.Requests()
	if len(g) != 1 || g[0].Gate != "" {
		t.Fatalf("config file access gate leaked to plugin URL: %+v", g)
	}
}

func TestPluginOptionTokenUsedWhenSockTokenUnset(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_TOKEN": "", "CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_opt_opt", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "SOCK_URL": ""}}, "--permission")
	if g := s.Requests(); len(g) != 1 || g[0].Auth != "Bearer sock_pt_opt_opt" {
		t.Fatalf("%+v", g)
	}
}

func TestMissingTokenLogsAndExitsZero(t *testing.T) {
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_TOKEN": ""}}, "--permission")
	if r.Code != 0 || len(s.Requests()) != 0 || strings.Count(r.Log, "\n") != 1 {
		t.Fatalf("%+v reqs=%d", r, len(s.Requests()))
	}
}

func TestServerDownExitsZeroLogsOnce(t *testing.T) {
	s := newStub(t)
	s.setFail(func(int) error { return unavailable() })
	r := runHook(t, s.URL, hookEnv{}, "--permission")
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
	r := runHook(t, s.URL, hookEnv{}, "--permission")
	if r.Code != 0 || len(s.Requests()) != 1 || strings.Count(r.Log, "\n") != 1 {
		t.Fatalf("%+v reqs=%d", r, len(s.Requests()))
	}
}

func TestSockMissingFromPath(t *testing.T) {
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": "/usr/bin:/bin"}}, "--permission")
	if r.Code != 0 || strings.Count(r.Log, "\n") != 1 || !strings.Contains(r.Log, "sock not found") {
		t.Fatalf("%+v", r)
	}
}

// Rule 4 of the quiet rule: only the ask and permission prompts send. The
// end of a turn with no ask sends nothing, whatever the old opt-ins say, and so
// does a bare phrase (the v1 shape).
func TestOnlyTheAskAndPermissionPromptsSend(t *testing.T) {
	s := newStub(t)
	for _, env := range []map[string]string{nil, {"CLAUDE_PLUGIN_OPTION_NOTIFY_ON_STOP": "true"}, {"SOCK_NOTIFY_ON_STOP": "1"}} {
		for _, args := range [][]string{{"--stop"}, {"--stop", "--ask-now"}} {
			r := runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s0", "Done.")}, args...)
			if n := len(s.Requests()); n != 0 || r.Log != "" {
				t.Fatalf("%v %v: sent %d, log %q", env, args, n, r.Log)
			}
		}
	}
	for _, phrase := range []string{"needs permission", "finished a turn", "x"} {
		r := runHook(t, s.URL, hookEnv{}, phrase)
		if n := len(s.Requests()); n != 0 || r.Code != 0 || r.Log != "" {
			t.Fatalf("phrase %q: sent %d, %+v", phrase, n, r)
		}
	}
}

func TestStdinShapesDoNotHang(t *testing.T) {
	s := newStub(t)
	for _, in := range []string{"", `{"hook_event_name":"Notification"}`, strings.Repeat("x", 1<<20)} {
		r := runHook(t, s.URL, hookEnv{Stdin: in}, "--permission")
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
	r := runHook(t, s.URL, hookEnv{StdinFile: pr}, "--permission")
	if r.Code != 0 || r.Elapsed.Seconds() < 1.5 || r.Elapsed.Seconds() > 5 {
		t.Fatalf("%+v", r)
	}
	// No input, so no session: quiet (rule 8).
	if n := len(s.Requests()); n != 0 || !strings.Contains(r.Log, "quiet: unknown session") {
		t.Fatalf("sent %d, log %q", n, r.Log)
	}
}

// Review Focus 1
func TestProjectNameLikeFlag(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/-v"}}, "--permission")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "-v: needs permission" {
		t.Fatalf("%+v", g)
	}
}

// Review Focus 2
func TestProjectNameSanitized(t *testing.T) {
	s := newStub(t)
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/a\nb\x1b[31mc"}}, "--permission")
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/" + strings.Repeat("n", 500)}}, "--permission")
	g := s.Requests()
	if len(g) != 2 {
		t.Fatalf("%+v", g)
	}
	if g[0].Text != "ab31mc: needs permission" {
		t.Fatalf("control chars kept: %q", g[0].Text)
	}
	if want := strings.Repeat("n", 100) + ": needs permission"; g[1].Text != want {
		t.Fatalf("not cut: %d chars", len(g[1].Text))
	}
}

func TestProjectNameAllowlist(t *testing.T) {
	cases := []struct{ name, dir, want string }{
		{"bidi override and C1 control removed", "/x/pro\u202eje\u0085ct", "project: needs permission"},
		{"all non-ASCII becomes project", "/x/\u65e5\u672c\u8a9e", "project: needs permission"},
		{"multibyte mixed with ASCII keeps ASCII", "/x/caf\u00e9-1.2_x y", "caf-1.2_x y: needs permission"},
	}
	for _, c := range cases {
		for _, loc := range []string{"C", "en_US.UTF-8"} {
			s := newStub(t)
			runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": c.dir, "LANG": loc, "LC_ALL": loc}}, "--permission")
			if g := s.Requests(); len(g) != 1 || g[0].Text != c.want {
				t.Fatalf("%s (%s): %+v, want %q", c.name, loc, g, c.want)
			}
		}
	}
}

// A normal run, including reaping the watchdog, writes nothing to stderr.
func TestNormalRunSilentOnStderr(t *testing.T) {
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{}, "--permission")
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
	writeFakeExec(t, fake, script)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": tmp + ":/usr/bin:/bin"}}, "--permission")
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
	writeFakeExec(t, fake, "#!/bin/sh\necho \"boom token=$SOCK_TOKEN\" >&2\nexit 1\n")
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": tmp + ":/usr/bin:/bin"}}, "--permission")
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
	runHook(t, s.URL, hookEnv{Env: map[string]string{"XDG_STATE_HOME": state}}, "--permission")
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
	runHook(t, s.URL, hookEnv{Env: map[string]string{"XDG_STATE_HOME": state}}, "--permission")
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
	r := runHook(t, s.URL, hookEnv{HomeRO: true}, "--permission")
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
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_HOOK_TIMEOUT": "1"}}, "--permission")
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
	runHook(t, s.URL, hookEnv{}, "--permission")
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
	writeFakeExec(t, fake, script)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": tmp + ":/usr/bin:/bin"}}, "--permission")
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
	writeFakeExec(t, fake, script)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": tmp + ":/usr/bin:/bin"}}, "--permission")
	if r.Log == "" {
		t.Fatal("no log")
	}
	if strings.Contains(r.Log, token[:8]) {
		t.Fatalf("token prefix leaked: %q", r.Log)
	}
}

func TestInvalidTimeoutFallsBackToDefault(t *testing.T) {
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"SOCK_HOOK_TIMEOUT": "abc"}}, "--permission")
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
	expireThrottle(t, state)
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

// binWithoutJq builds a PATH directory holding sock and the utilities the hook
// needs, and no jq: macOS ships one in /usr/bin, so the system dirs cannot be
// on the PATH.
func binWithoutJq(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink(sockBin(t), filepath.Join(dir, "sock")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"env", "bash", "sh", "tr", "cut", "basename", "awk", "sed", "head", "tail", "shasum", "sha256sum", "find", "wc", "mktemp", "cat", "mkdir", "rmdir", "mv", "date", "sleep", "kill", "pkill", "rm"} {
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

// Without jq the session cannot be read, so a permission prompt is quiet
// (rule 8); the idle prompt was already.
func TestWithoutJqPermissionAndIdleStayQuiet(t *testing.T) {
	s := newStub(t)
	env := map[string]string{"PATH": binWithoutJq(t), "CLAUDE_PROJECT_DIR": "/x/proj"}
	in := hookJSON(map[string]any{"hook_event_name": "Notification", "notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})
	r := runHook(t, s.URL, hookEnv{Env: env, Stdin: in}, "--permission")
	if g := s.Requests(); len(g) != 0 {
		t.Fatalf("%+v", g)
	}
	if !strings.Contains(r.Log, "quiet: unknown session") {
		t.Fatalf("log %q", r.Log)
	}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: idleInput("s8")}, "--idle")
	if g := s.Requests(); len(g) != 0 {
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
		expireThrottle(t, state)
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
	expireThrottle(t, state)
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
// stderr, "slow" sleeps 30 s first, "outside" prints a path outside the cache,
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
	slow) sleep 30 ;;
	outside) echo "printed outside for $SOCK_TOKEN" >&2; printf '%s\n' "` + outside + `"; exit 0 ;;
	linkdir)
		cache=$(printf '%s' "${XDG_STATE_HOME:-$HOME/.local/state}/sock-integrations/puppets" | tr -s /)
		mkdir -p "$cache" "` + dir + `/elsewhere"
		printf 'sock_pt_OUTSIDE\n' > "` + dir + `/elsewhere/tok"
		ln -s "` + dir + `/elsewhere" "$cache/link"
		printf '%s\n' "$cache/link/tok"
		exit 0 ;;
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
	writeFakeExec(t, filepath.Join(dir, "sock"), script)
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
	env := map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_TOKEN": machineKey}
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
	env := map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_TOKEN": "", "CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": machineKey, "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL, "CLAUDE_PROJECT_DIR": "/x/proj"}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: hookJSON(map[string]any{"hook_event_name": "Notification", "notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})}, "--permission")
	if got := readRec(t, rec, "send-token"); got != "sock_pt_projectpuppet_secret" {
		t.Fatalf("send ran with %q", got)
	}
}

func machineKeyEnv(bin string) map[string]string {
	return map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_TOKEN": machineKey, "CLAUDE_PROJECT_DIR": "/x/proj"}
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
	// Unbounded, the hook outlives the fake's 30 s sleep. The margin below
	// that is for notify.sh itself, which takes seconds on a loaded machine.
	if r.Elapsed > 15*time.Second {
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
	writeFakeExec(t, filepath.Join(dir, "sock"), "#!/bin/sh\n"+body+"\n")
	return dir
}

func TestWatchdogKillsASockThatIgnoresTERM(t *testing.T) {
	s := newStub(t)
	bin := fakeSock(t, "trap '' TERM\nwhile :; do sleep 1; done")
	env := map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_HOOK_TIMEOUT": "1"}
	r := runHook(t, s.URL, hookEnv{Env: env}, "--permission")
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
	runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": "/usr/bin:/bin"}, State: state}, "--permission")
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
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": bin + ":" + hookPathTail}}, "--permission")
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
// fakeConversation is the conversation fakeSockAsPuppet's posted message is in.
const fakeConversation = "0d6f9c7e-1111-4222-8333-444455556666"

func fakeSockAsPuppet(t *testing.T, refuse bool) (dir, rec string) {
	t.Helper()
	if refuse {
		return fakeSockAsPuppetBody(t, "echo 'sock: send --as-puppet: no puppet credential; set SOCK_TOKEN, or run sock machine-key use or sock puppets use' >&2\nexit 1")
	}
	return fakeSockAsPuppetPrinting(t, `{"id":"m1","conversationId":"`+fakeConversation+`"}`)
}

// fakeSockAsPuppetPrinting is a sock that prints out as `send --json` does.
func fakeSockAsPuppetPrinting(t *testing.T, out string) (dir, rec string) {
	t.Helper()
	return fakeSockAsPuppetBody(t, "printf '%s\\n' '"+out+"'")
}

func fakeSockAsPuppetBody(t *testing.T, tail string) (dir, rec string) {
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
	body += tail
	return fakeSock(t, body), rec
}

// codexPluginEnv is a Codex plugin hook's environment. Codex sets
// CLAUDE_PLUGIN_ROOT beside PLUGIN_ROOT for compatibility
// (learn.chatgpt.com/docs/hooks, "Plugin-bundled hooks"), so it cannot tell
// the routes apart; the input's turn_id, a Codex-only field, does.
func codexPluginEnv(bin string) map[string]string {
	return map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_TOKEN": "", "PLUGIN_ROOT": "/x/plugin", "CLAUDE_PLUGIN_ROOT": "/x/plugin", "CLAUDE_PROJECT_DIR": "/x/proj", "SOCK_ACCESS_GATE": "gate-secret"}
}

// codexPermission is a Codex PermissionRequest input: no message, a turn_id.
var codexPermission = hookJSON(map[string]any{"hook_event_name": "PermissionRequest", "turn_id": "t1", "tool_name": "Bash"})

// codexStopInput is stopInput as Codex sends it, with a turn_id.
func codexStopInput(session, last string) string {
	return hookJSON(map[string]any{"session_id": session, "turn_id": "t1", "hook_event_name": "Stop", "last_assistant_message": last, "stop_hook_active": false})
}

// Without a turn_id the input is Claude Code's, whatever PLUGIN_ROOT holds.
func TestPluginRootWithoutATurnIDIsNotTheCodexRoute(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockAsPuppet(t, false)
	env := codexPluginEnv(bin)
	env["SOCK_TOKEN"] = token
	runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("z2", "Ship it?")}, "--stop", "--ask-now")
	if got := readRec(t, rec, "args"); strings.Contains(got, "--as-puppet") || !strings.HasSuffix(got, "send") {
		t.Fatalf("argv %q, want the env token route", got)
	}
}

// With no SOCK_TOKEN the hook sends as the puppet the plugin's sock-mcp
// resolves, never with a token of its own and never as the login.
func TestCodexPluginRouteSendsAsThePluginPuppet(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockAsPuppet(t, false)
	r := runHook(t, s.URL, hookEnv{Env: codexPluginEnv(bin), Stdin: codexStopInput("z1", "Ship it?")}, "--stop", "--ask-now")
	if got := readRec(t, rec, "args"); got != "send\n--as-puppet\n--project\n/x/plugin\n--json" {
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
	r := runHook(t, s.URL, hookEnv{Env: codexPluginEnv(bin), Stdin: codexPermission}, "--permission")
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
	runHook(t, s.URL, hookEnv{Env: env, Stdin: codexPermission}, "--permission")
	if got := readRec(t, rec, "args"); got != "send\n--as-puppet\n--project\n/x/plugin\n--json" {
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
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PLUGIN_ROOT": "/x/cplugin", "PLUGIN_ROOT": "/x/plugin", "SOCK_TOKEN": "", "CLAUDE_PLUGIN_OPTION_SOCK_TOKEN": "sock_pt_other_other", "CLAUDE_PLUGIN_OPTION_SOCK_URL": s.URL}}, "--permission")
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

// Logged stderr has every token shape redacted, not only the hook's own token.
func TestLoggedStderrRedactsUnknownTokens(t *testing.T) {
	leak := "sock_pt_00000000-0000-0000-0000-000000000000_BBBBBBBBBBBBBBBBBBBBBBBB"
	pad := strings.Repeat("p", 280)
	cases := map[string]map[string]string{
		"as-puppet": {"SOCK_TOKEN": "", "PLUGIN_ROOT": "/x/plugin"},
		"env token": {},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			bin := fakeSock(t, "cat >/dev/null; echo '"+pad+" "+leak+"' >&2; echo 'boom "+leak+"' >&2; exit 1")
			env := map[string]string{"PATH": bin + ":" + hookPathTail}
			for k, v := range extra {
				env[k] = v
			}
			r := runHook(t, s.URL, hookEnv{Env: env, Stdin: codexPermission}, "--permission")
			if strings.Contains(r.Log, "sock_pt_0") || !strings.Contains(r.Log, "[redacted]") {
				t.Fatalf("log %q", r.Log)
			}
		})
	}
}

// The log drops C1 controls, raw and UTF-8 encoded, and keeps other characters whole.
func TestLogStripsC1Controls(t *testing.T) {
	s := newStub(t)
	bin := fakeSock(t, `cat >/dev/null; printf 'c1:\302\2332J raw:\2332J keep:\303\251\342\202\254\360\237\230\200' >&2; exit 1`)
	r := runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": bin + ":" + hookPathTail}}, "--permission")
	if !strings.Contains(r.Log, "c1:2J raw:2J keep:é€😀") || !utf8.ValidString(r.Log) {
		t.Fatalf("log %q", r.Log)
	}
	for _, c := range r.Log {
		if c >= 0x80 && c <= 0x9f {
			t.Fatalf("C1 control %U in log %q", c, r.Log)
		}
	}
}

// A token file reached through a symlinked directory resolves outside the cache.
func TestMachineKeyTokenFileThroughASymlinkedDirIsRefused(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockForKeys(t, "linkdir")
	runHook(t, s.URL, hookEnv{Env: machineKeyEnv(bin), Stdin: stopInput("k12", "Ship it?")}, "--stop", "--ask-now")
	if sent(rec) {
		t.Fatalf("sent %q from a token file outside the cache", readRec(t, rec, "send-token"))
	}
}

// Claude Code plugin hooks carry CLAUDE_PLUGIN_ROOT; an ambient PLUGIN_ROOT
// there must not turn them into the Codex route.
func TestClaudePluginRootKeepsTheMachineKeyRoute(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockForKeys(t, "")
	env := machineKeyEnv(bin)
	env["CLAUDE_PLUGIN_ROOT"] = "/x/cplugin"
	env["PLUGIN_ROOT"] = "/x/plugin"
	env["CLAUDE_PLUGIN_OPTION_SOCK_TOKEN"] = ""
	runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("r1", "Ship it?")}, "--stop", "--ask-now")
	if got := readRec(t, rec, "send-token"); got != "sock_pt_projectpuppet_secret" {
		t.Fatalf("send ran with %q, want the project puppet", got)
	}
}

// The Codex route is PLUGIN_ROOT without CLAUDE_PLUGIN_ROOT, whatever the option holds.
func TestCodexRouteIgnoresAnAmbientPluginOption(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockAsPuppet(t, false)
	env := codexPluginEnv(bin)
	env["CLAUDE_PLUGIN_OPTION_SOCK_TOKEN"] = "sock_pt_other_other"
	runHook(t, s.URL, hookEnv{Env: env, Stdin: codexPermission}, "--permission")
	if got := readRec(t, rec, "args"); got != "send\n--as-puppet\n--project\n/x/plugin\n--json" {
		t.Fatalf("argv %q", got)
	}
	if e := readRec(t, rec, "env"); strings.Contains(e, "sock_pt_other_other") {
		t.Fatal("the plugin option reached sock send")
	}
}

// --- only an interactive session messages the owner, once per project per hour
// (notify.sh rule 8). The owner got dozens of messages overnight.

const quietAsk = "Deploy the canary now?"

// codexTranscript writes a Codex rollout file whose first line is the
// session_meta Codex writes (live probe, Codex 0.162.1) and returns its path.
func codexTranscript(t *testing.T, sid string, source any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rollout-"+sid+".jsonl")
	meta := hookJSON(map[string]any{"type": "session_meta", "payload": map[string]any{"id": sid, "session_id": sid, "originator": "codex-tui", "source": source, "base_instructions": strings.Repeat("b", 70000)}})
	turn := hookJSON(map[string]any{"type": "turn_context", "payload": map[string]any{"approval_policy": "on-request"}})
	if err := os.WriteFile(p, []byte(meta+"\n"+turn+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// codexStop is a Codex Stop input (live probe, Codex 0.162.1).
func codexStop(sid, transcript string) string {
	return hookJSON(map[string]any{"session_id": sid, "turn_id": "t1", "transcript_path": transcript, "cwd": "/x/proj", "hook_event_name": "Stop", "model": "gpt", "permission_mode": "default", "stop_hook_active": false, "last_assistant_message": quietAsk})
}

// noClaude clears the attended Claude Code session runHook sets up by default.
func noClaude(extra map[string]string) map[string]string {
	env := map[string]string{"CLAUDE_CODE_SESSION_ATTENDED": "", "CLAUDE_CODE_SESSION_ID": "", "CLAUDE_PROJECT_DIR": "/x/proj"}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func permissionInput(sid string, extra map[string]any) string {
	m := map[string]any{"session_id": sid, "hook_event_name": "Notification", "notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"}
	for k, v := range extra {
		m[k] = v
	}
	return hookJSON(m)
}

// assertQuiet fails unless the run sent nothing and logged exactly the outcome,
// never the text.
func assertQuiet(t *testing.T, name string, s *stub, r result, outcome string) {
	t.Helper()
	if g := s.Requests(); len(g) != 0 {
		t.Fatalf("%s: sent %+v", name, g)
	}
	if !strings.HasSuffix(strings.TrimSpace(r.Log), " "+outcome) || strings.Count(r.Log, "\n") != 1 {
		t.Fatalf("%s: log %q, want one line %q", name, r.Log, outcome)
	}
	for _, text := range []string{quietAsk, "needs your permission", "Bash"} {
		if strings.Contains(r.Log, text) {
			t.Fatalf("%s: text %q in the log %q", name, text, r.Log)
		}
	}
}

func TestBackgroundSessionsAreQuiet(t *testing.T) {
	cases := []struct {
		name  string
		env   func(t *testing.T) map[string]string
		stdin func(t *testing.T) string
		args  []string
	}{
		{"claude -p", func(*testing.T) map[string]string {
			return map[string]string{"CLAUDE_CODE_SESSION_ATTENDED": "0", "CLAUDE_CODE_SESSION_ID": "s1", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli"}
		}, func(*testing.T) string { return stopInput("s1", quietAsk) }, []string{"--stop", "--ask-now"}},
		{"claude subagent ask", nil, func(*testing.T) string {
			return hookJSON(map[string]any{"session_id": "s1", "agent_id": "a79f", "agent_type": "general-purpose", "hook_event_name": "Stop", "last_assistant_message": quietAsk})
		}, []string{"--stop", "--ask-now"}},
		{"claude teammate or bg permission prompt", func(*testing.T) map[string]string {
			return map[string]string{"CLAUDE_CODE_SESSION_ATTENDED": "0", "CLAUDE_CODE_SESSION_ID": "s1"}
		}, func(*testing.T) string {
			return permissionInput("s1", map[string]any{"agent_id": "a79f", "agent_type": "general-purpose"})
		}, []string{"--permission"}},
		{"codex subagent permission prompt", func(*testing.T) map[string]string { return noClaude(nil) }, func(t *testing.T) string {
			return permissionInput("c1", map[string]any{"agent_id": "a79f", "turn_id": "t1", "transcript_path": codexTranscript(t, "c1", "cli")})
		}, []string{"--permission"}},
		{"codex exec", func(*testing.T) map[string]string { return noClaude(nil) }, func(t *testing.T) string {
			return codexStop("c1", codexTranscript(t, "c1", "exec"))
		}, []string{"--stop", "--ask-now"}},
		{"codex subagent", func(*testing.T) map[string]string { return noClaude(nil) }, func(t *testing.T) string {
			return codexStop("c1", codexTranscript(t, "c1", map[string]any{"subagent": map[string]any{"other": "guardian"}}))
		}, []string{"--stop", "--ask-now"}},
		{"sock-runner resuming a TUI session", func(*testing.T) map[string]string { return noClaude(map[string]string{"SOCK_HEADLESS": "1"}) }, func(t *testing.T) string {
			return codexStop("c1", codexTranscript(t, "c1", "cli"))
		}, []string{"--stop", "--ask-now"}},
		{"sock-runner under claude", func(*testing.T) map[string]string { return map[string]string{"SOCK_HEADLESS": "1"} }, func(*testing.T) string {
			return stopInput("s1", quietAsk)
		}, []string{"--stop", "--ask-now"}},
	}
	for _, c := range cases {
		s := newStub(t)
		var env map[string]string
		if c.env != nil {
			env = c.env(t)
		}
		r := runHook(t, s.URL, hookEnv{Env: env, Stdin: c.stdin(t)}, c.args...)
		assertQuiet(t, c.name, s, r, "quiet: background")
	}
}

func TestUnknownSessionIsQuiet(t *testing.T) {
	cases := []struct {
		name  string
		env   func(t *testing.T) map[string]string
		stdin func(t *testing.T) string
	}{
		{"no signal at all", func(*testing.T) map[string]string { return noClaude(nil) }, func(*testing.T) string { return stopInput("s1", quietAsk) }},
		{"attended env of another session", func(*testing.T) map[string]string {
			return map[string]string{"CLAUDE_CODE_SESSION_ID": "other"}
		}, func(*testing.T) string { return stopInput("s1", quietAsk) }},
		{"attended not set", func(*testing.T) map[string]string {
			return map[string]string{"CLAUDE_CODE_SESSION_ATTENDED": "", "CLAUDE_CODE_SESSION_ID": "s1"}
		}, func(*testing.T) string { return stopInput("s1", quietAsk) }},
		{"attended not 0 or 1", func(*testing.T) map[string]string {
			return map[string]string{"CLAUDE_CODE_SESSION_ATTENDED": "yes", "CLAUDE_CODE_SESSION_ID": "s1"}
		}, func(*testing.T) string { return stopInput("s1", quietAsk) }},
		{"codex source not recognized", func(*testing.T) map[string]string { return noClaude(nil) }, func(t *testing.T) string {
			return codexStop("c1", codexTranscript(t, "c1", "app-server"))
		}},
		{"codex transcript of another session", func(*testing.T) map[string]string { return noClaude(nil) }, func(t *testing.T) string {
			return codexStop("c1", codexTranscript(t, "c2", "cli"))
		}},
		{"codex transcript missing", func(*testing.T) map[string]string { return noClaude(nil) }, func(t *testing.T) string {
			return codexStop("c1", filepath.Join(t.TempDir(), "gone.jsonl"))
		}},
	}
	for _, c := range cases {
		s := newStub(t)
		r := runHook(t, s.URL, hookEnv{Env: c.env(t), Stdin: c.stdin(t)}, "--stop", "--ask-now")
		assertQuiet(t, c.name, s, r, "quiet: unknown session")
	}
	// Input that is not JSON names no session.
	s := newStub(t)
	r := runHook(t, s.URL, hookEnv{Stdin: "not json"}, "--permission")
	assertQuiet(t, "no session id", s, r, "quiet: unknown session")
	// Without jq the hook cannot read the session: the permission prompt is quiet.
	s = newStub(t)
	r = runHook(t, s.URL, hookEnv{Env: map[string]string{"PATH": binWithoutJq(t)}, Stdin: permissionInput(testSession, nil)}, "--permission")
	if g := s.Requests(); len(g) != 0 || !strings.Contains(r.Log, "quiet: unknown session") {
		t.Fatalf("no jq: sent %+v, log %q", g, r.Log)
	}
}

// A subagent's permission prompt holds up the owner's attended session, so it
// may send, under the same hourly budget.
func TestSubagentPermissionPromptInAnAttendedSessionSends(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}
	in := permissionInput("s1", map[string]any{"agent_id": "a79f", "agent_type": "general-purpose"})
	runHook(t, s.URL, hookEnv{Env: env, Stdin: in, State: state}, "--permission")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: Claude needs your permission to use Bash" {
		t.Fatalf("%+v", g)
	}
	r := runHook(t, s.URL, hookEnv{Env: env, Stdin: in, State: state}, "--permission")
	if g := s.Requests(); len(g) != 1 || !strings.Contains(r.Log, "quiet: throttled") {
		t.Fatalf("second prompt inside the hour: %+v, log %q", g, r.Log)
	}
}

func TestInteractiveCodexSessionsSend(t *testing.T) {
	for _, source := range []string{"cli", "vscode"} {
		s := newStub(t)
		r := runHook(t, s.URL, hookEnv{Env: noClaude(nil), Stdin: codexStop("c1", codexTranscript(t, "c1", source))}, "--stop", "--ask-now")
		if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: "+quietAsk || r.Log != "" {
			t.Fatalf("%s: %+v log %q", source, g, r.Log)
		}
	}
}

// throttleFile is where notify.sh records the last send for a project.
func throttleFile(state, project string) string {
	return filepath.Join(state, "sock-integrations", "throttle", project+".at")
}

// expireThrottle moves every project's last send back past the hour.
func expireThrottle(t *testing.T, state string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(state, "sock-integrations", "throttle", "*.at"))
	for _, f := range files {
		if err := os.WriteFile(f, fmt.Appendf(nil, "%d\n", time.Now().Unix()-3601), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInteractiveSendsOnceAnHour(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s1", "First ask?"), State: state}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 1 || g[0].Text != "proj: First ask?" {
		t.Fatalf("%+v", g)
	}
	r := runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s1", quietAsk), State: state}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 1 {
		t.Fatalf("a second ask inside the hour was sent: %+v", g)
	}
	if !strings.HasSuffix(strings.TrimSpace(r.Log), " quiet: throttled") || strings.Contains(r.Log, quietAsk) {
		t.Fatalf("log %q", r.Log)
	}
	fi, err := os.Stat(throttleFile(state, "proj"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("throttle record %v %v", fi, err)
	}
	di, err := os.Stat(filepath.Dir(throttleFile(state, "proj")))
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("throttle dir %v %v", di, err)
	}
}

func TestPermissionPromptInsideTheHourIsQuiet(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s1", "First ask?"), State: state}, "--stop", "--ask-now")
	r := runHook(t, s.URL, hookEnv{Env: env, Stdin: permissionInput("s1", nil), State: state}, "--permission")
	if g := s.Requests(); len(g) != 1 {
		t.Fatalf("permission prompt inside the hour was sent: %+v", g)
	}
	if !strings.HasSuffix(strings.TrimSpace(r.Log), " quiet: throttled") || strings.Contains(r.Log, "Bash") {
		t.Fatalf("log %q", r.Log)
	}
}

func TestAnotherProjectIsNotThrottled(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s1", quietAsk), State: state}, "--stop", "--ask-now")
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/other"}, Stdin: stopInput("s2", quietAsk), State: state}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 2 || g[1].Text != "other: "+quietAsk {
		t.Fatalf("%+v", g)
	}
}

func TestAfterTheHourItSendsAgain(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}
	send := func(ask string) result {
		return runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s1", ask), State: state}, "--stop", "--ask-now")
	}
	send("First ask?")
	at := func(ago int64) {
		if err := os.WriteFile(throttleFile(state, "proj"), fmt.Appendf(nil, "%d\n", time.Now().Unix()-ago), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	at(3590)
	if r := send("Still inside?"); len(s.Requests()) != 1 || !strings.Contains(r.Log, "quiet: throttled") {
		t.Fatalf("sent inside the hour: %+v", s.Requests())
	}
	at(3601)
	send("Past the hour?")
	if g := s.Requests(); len(g) != 2 || g[1].Text != "proj: Past the hour?" {
		t.Fatalf("%+v", g)
	}
}

// A send that fails does not use up the hour.
func TestAFailedSendKeepsTheHour(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	s.setFail(func(int) error { return tokenInvalid() })
	runHook(t, s.URL, hookEnv{Stdin: stopInput("s1", "First ask?"), State: state}, "--stop", "--ask-now")
	s.setFail(nil)
	runHook(t, s.URL, hookEnv{Stdin: stopInput("s1", "Second ask?"), State: state}, "--stop", "--ask-now")
	if g := s.Requests(); len(g) != 2 || !strings.HasSuffix(g[1].Text, "Second ask?") {
		t.Fatalf("%+v", g)
	}
}

// Rule 8c: a reply to the owner is the model's sock_send through sock-mcp. No
// hook runs on PreToolUse or PostToolUse, where it could block or count one,
// and every hook is async, so none can hold up the turn that replies.
func TestHooksNeverTouchSockSend(t *testing.T) {
	allowed := map[string]bool{"Notification": true, "Stop": true, "PermissionRequest": true}
	for _, rel := range []string{"claude-code/hooks/hooks.json", "hooks/hooks.json", "codex/hooks.json"} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		var f struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Async bool `json:"async"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for event, groups := range f.Hooks {
			if !allowed[event] {
				t.Fatalf("%s: hook on %s", rel, event)
			}
			for _, g := range groups {
				for _, h := range g.Hooks {
					if !h.Async {
						t.Fatalf("%s: %s hook is not async", rel, event)
					}
				}
			}
		}
	}
}

// A Claude Code hook with no token set (plugin option or SOCK_TOKEN) sends as
// the project's puppet that the plugin's sock-mcp resolves from the config
// file, so a plugin installed with nothing configured still reaches the owner.
func TestClaudeHookWithNoTokenSendsAsTheProjectPuppet(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockAsPuppet(t, false)
	env := map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_TOKEN": "", "CLAUDE_PLUGIN_ROOT": "/x/cplugin", "CLAUDE_PROJECT_DIR": "/x/proj", "SOCK_ACCESS_GATE": "gate-secret"}
	runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("z3", "Ship it?")}, "--stop", "--ask-now")
	if got := readRec(t, rec, "args"); got != "send\n--as-puppet\n--project\n/x/proj\n--json" {
		t.Fatalf("argv %q", got)
	}
	if got := readRec(t, rec, "text"); got != "proj: Ship it?" {
		t.Fatalf("stdin %q", got)
	}
	got := readRec(t, rec, "env")
	for _, v := range []string{"SOCK_TOKEN=", "SOCK_URL=", "SOCK_ACCESS_GATE=", "CLAUDE_PROJECT_DIR="} {
		if strings.Contains(got, "\n"+v) || strings.HasPrefix(got, v) {
			t.Fatalf("%s reached sock send", v)
		}
	}
}

// integrations-2ol: a Codex hook that sent records which session asked, from
// the hook's session_id and the posted message's conversation, so the owner's
// reply goes back into that session.
func TestCodexPluginRouteRecordsTheSessionAsk(t *testing.T) {
	s := newStub(t)
	bin, rec := fakeSockAsPuppet(t, false)
	state := t.TempDir()
	runHook(t, s.URL, hookEnv{Env: codexPluginEnv(bin), Stdin: codexStopInput("01a141ab-b265-75d0", "Ship it?"), State: state}, "--stop", "--ask-now")
	if got := readRec(t, rec, "args"); got != "send\n--as-puppet\n--project\n/x/plugin\n--json" {
		t.Fatalf("argv %q", got)
	}
	b, err := os.ReadFile(filepath.Join(state, "sock-integrations", "codex-asks", fakeConversation))
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	if len(f) != 2 || f[0] != "01a141ab-b265-75d0" || !regexp.MustCompile(`^[0-9]{10}$`).MatchString(f[1]) {
		t.Fatalf("ask %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(state, "sock-integrations", "codex-asks", fakeConversation)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

// A conversation id that is not a lowercase UUID never names a file.
func TestCodexPluginRouteIgnoresABadConversationID(t *testing.T) {
	s := newStub(t)
	bin, _ := fakeSockAsPuppetPrinting(t, `{"id":"m1","conversationId":"../../x"}`)
	state := t.TempDir()
	runHook(t, s.URL, hookEnv{Env: codexPluginEnv(bin), Stdin: codexStopInput("s1", "Ship it?"), State: state}, "--stop", "--ask-now")
	if _, err := os.Stat(filepath.Join(state, "sock-integrations", "codex-asks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("asks dir: %v", err)
	}
}
