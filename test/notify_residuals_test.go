package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A Stop that found no ask records that nothing is pending: --idle does not
// fall back to the transcript and repost an older, answered question.
func TestIdleAfterAStopWithNoAskIgnoresTheTranscript(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "s20.jsonl")
	line := hookJSON(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": "Deploy to prod now?"}}}})
	if err := os.WriteFile(transcript, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHook(t, s.URL, hookEnv{Stdin: stopInput("s20", "Done, deployed."), State: state}, "--stop")
	in := hookJSON(map[string]any{"session_id": "s20", "hook_event_name": "Notification", "notification_type": "idle_prompt", "transcript_path": transcript})
	runHook(t, s.URL, hookEnv{Stdin: in, State: state}, "--idle")
	if g := s.Requests(); len(g) != 0 {
		t.Fatalf("Stop found nothing pending, yet idle sent %+v", g)
	}
}

// A send the watchdog killed may have landed: the next idle prompt of the same
// turn does not send the ask again under a new idempotency key.
func TestAnAskWhoseSendTimedOutIsNotReposted(t *testing.T) {
	state := t.TempDir()
	rec := filepath.Join(t.TempDir(), "attempts")
	bin := fakeSock(t, "cat >/dev/null\necho x >>"+rec+"\nsleep 30")
	env := map[string]string{"PATH": bin + ":" + hookPathTail, "SOCK_HOOK_TIMEOUT": "1"}
	runHook(t, "http://127.0.0.1:9", hookEnv{Env: env, Stdin: stopInput("s21", "Proceed?"), State: state}, "--stop")
	runHook(t, "http://127.0.0.1:9", hookEnv{Env: env, Stdin: idleInput("s21"), State: state}, "--idle")
	runHook(t, "http://127.0.0.1:9", hookEnv{Env: env, Stdin: idleInput("s21"), State: state}, "--idle")
	b, _ := os.ReadFile(rec)
	if n := strings.Count(string(b), "x"); n != 1 {
		t.Fatalf("%d send attempts for one ask, want 1", n)
	}
}

// Secret shapes an agent's ask can echo, each reaching the owner before
// redaction learned it (security review, 2026-10-14).
func TestMoreSecretShapesAreRedacted(t *testing.T) {
	cases := map[string]struct{ msg, secret string }{
		"url userinfo":  {"Connect with postgres://admin:Pq7xSecret@db.internal/app now?", "Pq7xSecret"},
		"colon form":    {`The config has "api_key": "Vv9Secret" set. Keep it?`, "Vv9Secret"},
		"tailscale key": {"Use tskey-auth-kAbC123-XYZsecret for the node?", "kAbC123"},
		"space form":    {"Set AWS_SECRET_ACCESS_KEY wJalrXUtnFEMIK7MDENG in CI?", "wJalrXUtnFEMIK7MDENG"},
		"pem body":      {"Should I install this key?\n-----BEGIN RSA PRIVATE KEY-----\nMIIEpQIBAAKCAQEAbody\n-----END RSA PRIVATE KEY-----", "MIIEpQIBAAKCAQEAbody"},
		"one line pem":  {"Install -----BEGIN PRIVATE KEY-----MIIEoneline-----END PRIVATE KEY----- here?", "MIIEoneline"},
		"indented code": {"Run this?\n\n    export PATH=/opt/Zq9code:$PATH", "Zq9code"},
		"jwt":           {"Is eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJKd3RCb2R5In0.c2lnSnd0U2ln the session?", "eyJzdWIiOiJKd3RCb2R5In0"},
		"slack token":   {"Post with xoxb-1234-5678-SlackTokBody?", "SlackTokBody"},
		"slack webhook": {"Use https://hooks.slack.com/services/T000/B000/HookBodyXyz for alerts?", "HookBodyXyz"},
		"basic auth":    {"Send Authorization: Basic dXNlcjpCYXNpY0JvZHk= to the API?", "dXNlcjpCYXNpY0JvZHk="},
		"gitlab":        {"Use glpat-GitLabBody1234567890ab?", "GitLabBody1234567890ab"},
		"google":        {"Use AIzaSyGoogleBody0123456789abcdefghijk?", "SyGoogleBody0123456789"},
		"npm":           {"Publish with npm_NpmBody0123456789abcdefghijklmnopq?", "NpmBody0123456789"},
		"stripe live":   {"Charge with sk_live_StripeBody123 or rk_live_StripeRest456?", "StripeBody123"},
		"stripe restr":  {"Charge with rk_live_StripeRest456?", "StripeRest456"},
		"pgp block":     {"Import this?\n-----BEGIN PGP PRIVATE KEY BLOCK-----\nlQOYBPgpBody\n-----END PGP PRIVATE KEY BLOCK-----", "lQOYBPgpBody"},
		"quoted spaces": {`Keep password = "correct horse QuotedBody" in the file?`, "QuotedBody"},
		"lowercase env": {"Set aws_secret_access_key LowerEnvBody9 in CI?", "LowerEnvBody9"},
		"pass param":    {"Connect with ?x=1 pass=PassBody7 or pwd=PwdBody8 now?", "PassBody7"},
		"pwd param":     {"Connect with pwd=PwdBody8 now?", "PwdBody8"},
	}
	for sedName, sed := range sedPaths() {
		for name, c := range cases {
			t.Run(sedName+" "+name, func(t *testing.T) {
				s := newStub(t)
				env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}
				withSed(t, env, sed)
				runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s22", c.msg)}, "--stop", "--ask-now")
				g := s.Requests()
				if len(g) != 1 {
					t.Fatalf("%+v", g)
				}
				if strings.Contains(g[0].Text, c.secret) {
					t.Fatalf("%q reached the owner: %q", c.secret, g[0].Text)
				}
			})
		}
	}
}

// staleThrottleLock leaves the lock a killed hook would, older than a minute.
func staleThrottleLock(t *testing.T, state string) {
	t.Helper()
	lock := throttleFile(state, "proj") + ".lock"
	if err := os.MkdirAll(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
}

// Hooks that find the same stale throttle lock cannot both take it and both
// send inside the hour: whoever clears it stays quiet this run.
func TestHooksRacingOverAStaleThrottleLockSendOnce(t *testing.T) {
	env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}
	for trial := range 40 {
		s := newStub(t)
		state := t.TempDir()
		staleThrottleLock(t, state)
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runHook(t, s.URL, hookEnv{Env: env, Stdin: permissionInput("s23", nil), State: state}, "--permission")
			}()
		}
		wg.Wait()
		if g := s.Requests(); len(g) > 1 {
			t.Fatalf("trial %d: %d sends inside one hour", trial, len(g))
		}
	}
}

// The hook that clears a stale lock stays quiet; the next one sends.
func TestAStaleThrottleLockIsClearedForTheNextHook(t *testing.T) {
	s := newStub(t)
	state := t.TempDir()
	env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}
	staleThrottleLock(t, state)
	runHook(t, s.URL, hookEnv{Env: env, Stdin: permissionInput("s24", nil), State: state}, "--permission")
	runHook(t, s.URL, hookEnv{Env: env, Stdin: permissionInput("s24", nil), State: state}, "--permission")
	if g := s.Requests(); len(g) != 1 {
		t.Fatalf("%+v", g)
	}
}

// An encrypted PEM has a blank line after its headers; with a trailing list the
// ask starts at the body paragraph, so redaction must see the whole message.
func TestAnEncryptedKeyBodyBeforeATrailingListIsRedacted(t *testing.T) {
	s := newStub(t)
	msg := "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,ABCD\n\nMIIEsecretBODYline1\nMIIEsecretBODYline2\n-----END RSA PRIVATE KEY-----\n\n1. use it\n2. rotate it"
	runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s25", msg)}, "--stop", "--ask-now")
	for _, r := range s.Requests() {
		if strings.Contains(r.Text, "MIIEsecretBODY") {
			t.Fatalf("key body reached the owner: %q", r.Text)
		}
	}
}

// Redaction stays linear: GNU sed in a UTF-8 locale backtracks the env-style
// rule quadratically, so the seds run in the C locale on capped input.
func TestRedactionOfALongNameRunIsFast(t *testing.T) {
	seds := sedPaths()
	msg := strings.Repeat("KEY_", 8192) + "?"
	for name, sed := range seds {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj", "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8"}
			withSed(t, env, sed)
			r := runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s26", msg)}, "--stop", "--ask-now")
			if r.Elapsed > 8*time.Second {
				t.Fatalf("hook took %v on 32 KB of KEY_", r.Elapsed)
			}
		})
	}
}

// sedPaths is the system sed ("") and GNU sed when this machine has it: GNU
// sed comes first on the owner's PATH, the hook tests' PATH has the system one.
func sedPaths() map[string]string {
	seds := map[string]string{"system": ""}
	if p, err := exec.LookPath("gsed"); err == nil {
		seds["gnu"] = p
	} else if out, err := exec.Command("sed", "--version").Output(); err == nil && strings.Contains(string(out), "GNU") {
		p, _ := exec.LookPath("sed")
		seds["gnu"] = p
	}
	return seds
}

// withSed puts sed (and the real sock) first on the hook's PATH.
func withSed(t *testing.T, env map[string]string, sed string) {
	t.Helper()
	if sed == "" {
		return
	}
	bin := t.TempDir()
	if err := os.Symlink(sed, filepath.Join(bin, "sed")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sockBin(t), filepath.Join(bin, "sock")); err != nil {
		t.Fatal(err)
	}
	env["PATH"] = bin + ":" + hookPathTail
}

// A send that failed outright, or never ran sock, did not land: the next idle
// of the same turn sends the ask. Only a send the watchdog killed is held back.
func TestAnAskWhoseSendFailedOutrightIsSentAgain(t *testing.T) {
	state := t.TempDir()
	rec := filepath.Join(t.TempDir(), "attempts")
	bin := fakeSock(t, "cat >/dev/null\necho x >>"+rec+"\nexit 1")
	env := map[string]string{"PATH": bin + ":" + hookPathTail}
	runHook(t, "http://127.0.0.1:9", hookEnv{Env: env, Stdin: stopInput("s27", "Proceed?"), State: state}, "--stop")
	runHook(t, "http://127.0.0.1:9", hookEnv{Env: env, Stdin: idleInput("s27"), State: state}, "--idle")
	runHook(t, "http://127.0.0.1:9", hookEnv{Env: env, Stdin: idleInput("s27"), State: state}, "--idle")
	b, _ := os.ReadFile(rec)
	if n := strings.Count(string(b), "x"); n != 2 {
		t.Fatalf("%d send attempts, want 2: a failed send held the ask back", n)
	}
}

func TestAnAskThatNeverReachedSockIsSentAgain(t *testing.T) {
	state := t.TempDir()
	rec := filepath.Join(t.TempDir(), "attempts")
	bin := fakeSock(t, "cat >/dev/null\necho x >>"+rec)
	env := map[string]string{"PATH": bin + ":" + hookPathTail}
	runHook(t, "http://127.0.0.1:9", hookEnv{Env: env, Stdin: stopInput("s28", "Proceed?"), State: state}, "--stop")
	noToken := map[string]string{"PATH": env["PATH"], "SOCK_TOKEN": ""}
	runHook(t, "http://127.0.0.1:9", hookEnv{Env: noToken, Stdin: idleInput("s28"), State: state}, "--idle")
	runHook(t, "http://127.0.0.1:9", hookEnv{Env: env, Stdin: idleInput("s28"), State: state}, "--idle")
	b, _ := os.ReadFile(rec)
	if n := strings.Count(string(b), "x"); n != 1 {
		t.Fatalf("%d send attempts, want 1: a send that never ran sock held the ask back", n)
	}
}

// Over 64 KiB, extract_ask keeps the tail, and the cut can land inside a secret
// whose marker is then gone: the partial first paragraph is dropped, and so is
// everything through a key's END line whose BEGIN was cut away.
func TestTheTailCutNeverStartsInsideASecret(t *testing.T) {
	const tok = "sock_pt_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef0123456789"
	// the window is the last 65536 bytes of the message plus its newline
	pre := strings.Repeat("a", 1000)
	rest := 65536 - (len(tok) - 10) - 2
	line := pre + tok + strings.Repeat("x", rest) + "?"
	pem := "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: " + strings.Repeat("A", 70000) +
		"\n\nMIIEsecretBODYcut1\nMIIEsecretBODYcut2\n-----END RSA PRIVATE KEY-----\n\n1. use it\n2. rotate it"
	for name, c := range map[string]struct{ msg, secret string }{
		"token": {line, "KLMNOPQRSTUVWXYZabcdef0123456789"},
		"pem":   {pem, "MIIEsecretBODYcut"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s29", c.msg)}, "--stop", "--ask-now")
			for _, r := range s.Requests() {
				if strings.Contains(r.Text, c.secret) {
					t.Fatalf("%q reached the owner: %.80q", c.secret, r.Text)
				}
			}
		})
	}
}
