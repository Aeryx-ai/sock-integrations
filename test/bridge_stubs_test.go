package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	bnSession   = "sesn_01TESTSESSION"
	bnKey       = "sk-ant-KEYSENTINEL-3e8a51"
	bnAnswer    = "ANSWERSENTINEL-6b2d first line\nsecond line"
	bnThinking  = "THINKINGSENTINEL-0c77"
	bnToolInput = "TOOLINPUTSENTINEL-91fe"
)

// bnSockStub records `sock send` argv and environment, and answers whoami like rnSockStub.
const bnSockStub = `#!/bin/sh
echo "$SOCK_TOKEN" >> "$STUB_DIR/sock-token"
case "$1" in
whoami)
  printf '{"account":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","puppet":{"ownerId":"%s","name":"x"}}}\n' "$STUB_OWNER" ;;
send)
  n=$(ls "$STUB_DIR" | grep -c '^send\.')
  d="$STUB_DIR/send.$n"; mkdir "$d"
  shift; [ "$1" = "--" ] && shift
  printf '%s' "$1" > "$d/text"
  env > "$d/env"
  printf '%s\n' "$@" > "$d/argv" ;;
*) exit 64 ;;
esac
`

// fakeMA is a Managed Agents server: it records requests and plays a script.
type fakeMA struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	log      []string         // "stream", "send", "list" in arrival order
	headers  []http.Header    // of each request, in order
	sent     []string         // user.message bodies
	streamed chan struct{}    // closed when a stream request arrives
	events   [][]any          // per send: events the stream plays after it
	history  []map[string]any // what the list endpoint returns
	status   map[string][]int // per "stream"/"send"/"list": statuses to return first
	retryAft string           // Retry-After for the scripted non-200s
	hold     bool             // after the script, keep the stream open and silent
}

func newFakeMA(t *testing.T) *fakeMA {
	f := &fakeMA{t: t, status: map[string][]int{}, streamed: make(chan struct{})}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMA) next(kind string) int {
	if q := f.status[kind]; len(q) > 0 {
		f.status[kind] = q[1:]
		return q[0]
	}
	return 200
}

func (f *fakeMA) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	kind := "list"
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events/stream"):
		kind = "stream"
	case r.Method == http.MethodPost:
		kind = "send"
	}
	f.log = append(f.log, kind)
	f.headers = append(f.headers, r.Header.Clone())
	code := f.next(kind)
	f.mu.Unlock()
	if !strings.HasPrefix(r.URL.Path, "/v1/sessions/"+bnSession+"/events") {
		http.NotFound(w, r)
		return
	}
	if code != 200 {
		if f.retryAft != "" {
			w.Header().Set("Retry-After", f.retryAft)
		}
		http.Error(w, `{"type":"error"}`, code)
		return
	}
	switch kind {
	case "stream":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		f.mu.Lock()
		select {
		case <-f.streamed:
		default:
			close(f.streamed)
		}
		f.mu.Unlock()
		f.play(w, r)
	case "send":
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.sent = append(f.sent, string(b))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	default:
		f.mu.Lock()
		h := f.history
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": h})
	}
}

// play waits for the send, then writes that send's scripted events.
func (f *fakeMA) play(w http.ResponseWriter, r *http.Request) {
	idx := f.streamCount() - 1
	deadline := time.After(15 * time.Second)
	for {
		f.mu.Lock()
		sent := len(f.sent)
		f.mu.Unlock()
		if sent > idx {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline:
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	f.mu.Lock()
	var evs []any
	if idx < len(f.events) {
		evs = f.events[idx]
	}
	hold := f.hold
	f.mu.Unlock()
	for _, e := range evs {
		b, _ := json.Marshal(e)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
		w.(http.Flusher).Flush()
	}
	if hold {
		<-r.Context().Done()
	}
}

func (f *fakeMA) streamCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, k := range f.log {
		if k == "stream" {
			n++
		}
	}
	return n
}

func (f *fakeMA) kinds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func (f *fakeMA) sentBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func (f *fakeMA) set(fn func(*fakeMA)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func agentMsg(text string) map[string]any {
	return map[string]any{"type": "agent.message", "id": "evt_" + text[:3], "content": []any{map[string]any{"type": "text", "text": text}}}
}

func idle(reason string) map[string]any {
	return map[string]any{"type": "session.status_idle", "id": "evt_idle", "stop_reason": map[string]any{"type": reason, "event_ids": []string{"evt_x"}}}
}

// bnHarness: the runner harness, pointed at the bridge and the fake server.
type bnHarness struct {
	*rnHarness
	api *fakeMA
}

func rnBridgePath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("SOCK_BRIDGE_BIN")
	if p == "" {
		p = filepath.Join(root, "runner", "sock-bridge")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("bridge not present at %s (set SOCK_BRIDGE_BIN): skipping conformance test", p)
	}
	return p
}

func newBridgeHarness(t *testing.T) *bnHarness {
	t.Helper()
	bin := rnBridgePath(t)
	t.Parallel()
	dir := t.TempDir()
	h := &rnHarness{t: t, bin: bin, dir: dir}
	h.stubs = filepath.Join(dir, "bin")
	h.rec = filepath.Join(dir, "rec")
	h.work = filepath.Join(dir, "work")
	h.state = filepath.Join(dir, "state")
	for _, d := range []string{h.stubs, h.rec, h.work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(h.stubs, "sock"), []byte(bnSockStub), 0o755); err != nil {
		t.Fatal(err)
	}
	api := newFakeMA(t)
	h.env = map[string]string{
		"PATH":                 h.stubs + ":" + rnPathTail,
		"HOME":                 filepath.Join(dir, "home"),
		"TMPDIR":               dir,
		"SOCK_TOKEN":           rnTokenSentinel,
		"SOCK_URL":             rnURLSentinel,
		"SOCK_MESSAGE_ID":      rnMsgID,
		"SOCK_CONVERSATION_ID": rnConvID,
		"SOCK_SENDER_ID":       rnOwner,
		"ANTHROPIC_API_KEY":    bnKey,
		"SOCK_BRIDGE_SESSION":  bnSession,
		"SOCK_BRIDGE_API":      api.srv.URL,
		"SOCK_BRIDGE_STATE":    h.state,
		"SOCK_BRIDGE_TIMEOUT":  "20",
		"STUB_DIR":             h.rec,
		"STUB_OWNER":           rnOwner,
	}
	return &bnHarness{rnHarness: h, api: api}
}

type bnSend struct{ Text, Env string }

// sends are the `sock send` calls the bridge made, in order.
func (h *bnHarness) sends() []bnSend {
	ents, _ := os.ReadDir(h.rec)
	var out []bnSend
	for i := 0; i < len(ents); i++ {
		d := filepath.Join(h.rec, fmt.Sprintf("send.%d", i))
		tb, err := os.ReadFile(filepath.Join(d, "text"))
		if err != nil {
			break
		}
		eb, _ := os.ReadFile(filepath.Join(d, "env"))
		out = append(out, bnSend{Text: string(tb), Env: string(eb)})
	}
	return out
}

func (h *bnHarness) bridgeLog() string {
	b, _ := os.ReadFile(filepath.Join(h.state, "bridge.log"))
	return string(b)
}

// anywhereKey fails when the key shows up in the log, the state directory, the
// bridge's output or the environment of anything it launched.
func (h *bnHarness) noKeyLeak(r rnRes) {
	h.t.Helper()
	for name, s := range map[string]string{"log": h.bridgeLog(), "stdout": r.Stdout, "stderr": r.Stderr} {
		if strings.Contains(s, bnKey) {
			h.t.Fatalf("%s contains the API key: %q", name, s)
		}
	}
	for _, s := range h.sends() {
		if strings.Contains(s.Env, bnKey) {
			h.t.Fatal("the API key is in the environment of a child")
		}
	}
	_ = filepath.WalkDir(h.state, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, _ := os.ReadFile(p); strings.Contains(string(b), bnKey) {
			h.t.Fatalf("the API key is in %s", p)
		}
		return nil
	})
}
