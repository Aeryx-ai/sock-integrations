package test

import (
	"strconv"
	"strings"
	"testing"
)

func bnLongAnswer() string {
	var lines []string
	for i := range 2000 {
		lines = append(lines, strings.Repeat("x", 59)+string(rune('a'+i%26)))
	}
	return strings.Join(lines, "\n")
}

// Each part the bridge posts carries the key <message id>.<part>, the same on
// every rerun of the message, so a send the hub took whose answer was lost
// posts once.
func TestBridgePostsUnderAStableKeyPerPart(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(bnLongAnswer()), idle("end_turn"))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	s := h.sends()
	if len(s) < 2 {
		t.Fatalf("%d sends, want a split answer", len(s))
	}
	for i, m := range s {
		if want := rnMsgID + "." + strconv.Itoa(i+1); m.Key != want {
			t.Fatalf("part %d key %q, want %q", i+1, m.Key, want)
		}
	}
}

// The hub refusing a key for a different text means this message already
// posted that part: the bridge goes on as if the send succeeded.
func TestBridgeKeyReusedCountsAsPosted(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{Env: map[string]string{"STUB_SEND_MISMATCH_N": "0"}})
	if r.Code != 0 || strings.Contains(h.bridgeLog(), "send failed") {
		t.Fatalf("exit %d, log %q", r.Code, h.bridgeLog())
	}
}

// A sock without --idempotency-key still posts, unkeyed.
func TestBridgeOlderSockPostsWithoutAKey(t *testing.T) {
	h := newBridgeHarness(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{Env: map[string]string{"STUB_NO_IDEM": "1"}})
	s := h.sends()
	if r.Code != 0 || len(s) != 1 || s[0].Key != "" || s[0].Text != bnAnswer {
		t.Fatalf("exit %d, sends %+v", r.Code, s)
	}
}
