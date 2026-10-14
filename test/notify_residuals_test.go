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
