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
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			runHook(t, s.URL, hookEnv{Env: map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj"}, Stdin: stopInput("s22", c.msg)}, "--stop", "--ask-now")
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
	seds := map[string]string{"system": ""}
	if p, err := exec.LookPath("gsed"); err == nil {
		seds["gnu"] = p
	} else if out, err := exec.Command("sed", "--version").Output(); err == nil && strings.Contains(string(out), "GNU") {
		p, _ := exec.LookPath("sed")
		seds["gnu"] = p
	}
	msg := strings.Repeat("KEY_", 8192) + "?"
	for name, sed := range seds {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			env := map[string]string{"CLAUDE_PROJECT_DIR": "/x/proj", "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8"}
			if sed != "" {
				bin := t.TempDir()
				if err := os.Symlink(sed, filepath.Join(bin, "sed")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(sockBin(t), filepath.Join(bin, "sock")); err != nil {
					t.Fatal(err)
				}
				env["PATH"] = bin + ":" + hookPathTail
			}
			r := runHook(t, s.URL, hookEnv{Env: env, Stdin: stopInput("s26", msg)}, "--stop", "--ask-now")
			if r.Elapsed > 8*time.Second {
				t.Fatalf("hook took %v on 32 KB of KEY_", r.Elapsed)
			}
		})
	}
}
