package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
