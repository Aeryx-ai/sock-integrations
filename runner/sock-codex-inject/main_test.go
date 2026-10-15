package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const (
	conv  = "0b6d3c2a-1f4e-4c8a-9d2b-7e5f6a1b2c3d"
	msgID = "1c7e4d3b-2a5f-4d9b-8e3c-6f7a8b9c0d1e"
)

type thread struct {
	ID           string  `json:"id"`
	Cwd          string  `json:"cwd"`
	Ephemeral    bool    `json:"ephemeral"`
	ThreadSource *string `json:"threadSource"`
	UpdatedAt    int64   `json:"updatedAt"`
}

func user() *string { s := "user"; return &s }

// fakeServer is the Codex app-server's control socket: JSON-RPC over a
// WebSocket upgrade on a Unix socket.
type fakeServer struct {
	threads   []thread
	loaded    []string
	clientIDs map[string][]string // thread id -> user message clientIds
	turnErr   bool

	mu      sync.Mutex
	methods []string
	params  map[string]json.RawMessage // last params per method
}

func (f *fakeServer) handle(method string, params json.RawMessage) (any, *rpcError) {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	f.params[method] = params
	f.mu.Unlock()
	switch method {
	case "initialize":
		return map[string]any{"userAgent": "fake"}, nil
	case "thread/list":
		var p struct {
			Cursor *string `json:"cursor"`
		}
		_ = json.Unmarshal(params, &p)
		// two pages, to prove pagination
		half := len(f.threads) / 2
		if p.Cursor == nil {
			return map[string]any{"data": f.threads[:half], "nextCursor": "page2"}, nil
		}
		return map[string]any{"data": f.threads[half:], "nextCursor": nil}, nil
	case "thread/loaded/list":
		return map[string]any{"data": f.loaded, "nextCursor": nil}, nil
	case "thread/resume":
		return map[string]any{"thread": map[string]any{}}, nil
	case "thread/items/list":
		var p struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(params, &p)
		var data []any
		data = append(data, map[string]any{"turnId": "t0", "item": map[string]any{"type": "agentMessage", "id": "a1", "text": "hi"}})
		for _, id := range f.clientIDs[p.ThreadID] {
			data = append(data, map[string]any{"turnId": "t0", "item": map[string]any{"type": "userMessage", "id": "u-" + id, "clientId": id, "content": []any{}}})
		}
		return map[string]any{"data": data, "nextCursor": nil}, nil
	case "turn/start":
		if f.turnErr {
			return nil, &rpcError{Code: -32600, Message: "no"}
		}
		return map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress", "items": []any{}}}, nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found"}
}

func (f *fakeServer) called(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.methods {
		if m == method {
			return true
		}
	}
	return false
}

func (f *fakeServer) order() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

// serve starts f on a short Unix socket path and returns it.
func serve(t *testing.T, f *fakeServer) string {
	t.Helper()
	f.params = map[string]json.RawMessage{}
	dir, err := os.MkdirTemp("/tmp", "ci")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		c.SetReadLimit(4 << 20)
		ctx := r.Context()
		// a notification first, which the client must skip
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"method":"thread/status/changed","params":{}}`))
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID     *int64          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(b, &req) != nil || req.ID == nil {
				continue
			}
			res, rerr := f.handle(req.Method, req.Params)
			out := map[string]any{"id": *req.ID}
			if rerr != nil {
				out["error"] = rerr
			} else {
				out["result"] = res
			}
			b, _ = json.Marshal(out)
			if c.Write(ctx, websocket.MessageText, b) != nil {
				return
			}
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

type result struct {
	code           int
	stdout, stderr string
}

func inject(t *testing.T, sock, project, asks, prompt string) result {
	t.Helper()
	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	args := []string{"--project", project, "--conversation", conv, "--message-id", msgID, "--socket", sock, "--asks", asks}
	code := run(ctx, args, strings.NewReader(prompt), &out, &errb, func(string) string { return "" })
	return result{code, out.String(), errb.String()}
}

func writeAsk(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, conv), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func project(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const prompt = "This message is your owner's reply.\n<sock-message from=\"owner\" conversation=\"x\" id=\"y\">\nsecret-text ✓ \"quoted\"\n</sock-message>\n"

func turnStart(t *testing.T, f *fakeServer) (threadID, text, clientID string) {
	t.Helper()
	var p struct {
		ThreadID string `json:"threadId"`
		Input    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"input"`
		ClientUserMessageID string `json:"clientUserMessageId"`
	}
	f.mu.Lock()
	raw := f.params["turn/start"]
	f.mu.Unlock()
	if raw == nil {
		t.Fatal("no turn/start")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Input) != 1 || p.Input[0].Type != "text" {
		t.Fatalf("input %s", raw)
	}
	return p.ThreadID, p.Input[0].Text, p.ClientUserMessageID
}

func noPromptLeak(t *testing.T, r result) {
	t.Helper()
	if strings.Contains(r.stdout+r.stderr, "secret-text") {
		t.Fatalf("prompt leaked: stdout %q stderr %q", r.stdout, r.stderr)
	}
}

func TestAcceptedPicksTheAsksSession(t *testing.T) {
	p := project(t)
	f := &fakeServer{
		threads: []thread{
			{ID: "newest", Cwd: p, ThreadSource: user(), UpdatedAt: 300},
			{ID: "asked", Cwd: p, ThreadSource: user(), UpdatedAt: 100},
		},
		loaded: []string{"newest", "asked"},
	}
	r := inject(t, serve(t, f), p, writeAsk(t, "asked 1792100000\n"), prompt)
	if r.code != 0 || r.stdout != "accepted session=asked turn=turn-1\n" {
		t.Fatalf("got %+v", r)
	}
	id, text, cid := turnStart(t, f)
	if id != "asked" || text != prompt || cid != msgID {
		t.Fatalf("turn/start %q %q %q", id, text, cid)
	}
	if f.called("thread/resume") {
		t.Fatal("resumed a loaded thread")
	}
	noPromptLeak(t, r)
}

func TestNewestWhenTheAskedSessionIsGone(t *testing.T) {
	p := project(t)
	f := &fakeServer{
		threads: []thread{
			{ID: "old", Cwd: p, ThreadSource: user(), UpdatedAt: 100},
			{ID: "new", Cwd: p, ThreadSource: user(), UpdatedAt: 200},
		},
		loaded: []string{"old", "new"},
	}
	r := inject(t, serve(t, f), p, writeAsk(t, "vanished 1792100000\n"), prompt)
	if r.code != 0 || r.stdout != "accepted session=new turn=turn-1\n" {
		t.Fatalf("got %+v", r)
	}
}

func TestOnlyInteractiveSessionsInTheProject(t *testing.T) {
	p := project(t)
	sub := filepath.Join(p, "pkg", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	other := project(t)
	exec := "exec"
	f := &fakeServer{
		threads: []thread{
			{ID: "ephemeral", Cwd: p, Ephemeral: true, ThreadSource: user(), UpdatedAt: 900},
			{ID: "title", Cwd: p, ThreadSource: &exec, UpdatedAt: 800},
			{ID: "elsewhere", Cwd: other, ThreadSource: user(), UpdatedAt: 700},
			{ID: "sibling", Cwd: p + "-other", ThreadSource: user(), UpdatedAt: 650},
			{ID: "subdir", Cwd: sub, ThreadSource: user(), UpdatedAt: 10},
		},
		loaded: []string{"ephemeral", "title", "elsewhere", "subdir"},
	}
	r := inject(t, serve(t, f), p, writeAsk(t, "elsewhere 1792100000\n"), prompt)
	if r.code != 0 || r.stdout != "accepted session=subdir turn=turn-1\n" {
		t.Fatalf("got %+v", r)
	}
}

// Live Codex 0.163 thread/list leaves threadSource null on TUI threads (only
// thread/read fills it); the list is already limited to interactive sources.
func TestListedSessionWithoutThreadSourceCounts(t *testing.T) {
	p := project(t)
	f := &fakeServer{threads: []thread{{ID: "tui", Cwd: p, UpdatedAt: 5}, {ID: "x", Cwd: "/nowhere"}}, loaded: []string{"tui"}}
	r := inject(t, serve(t, f), p, writeAsk(t, ""), prompt)
	if r.code != 0 || r.stdout != "accepted session=tui turn=turn-1\n" {
		t.Fatalf("got %+v", r)
	}
}

func TestNotLoadedIsResumedBeforeTheTurn(t *testing.T) {
	p := project(t)
	f := &fakeServer{threads: []thread{{ID: "cold", Cwd: p, ThreadSource: user(), UpdatedAt: 1}, {ID: "x", Cwd: "/nowhere", ThreadSource: user()}}}
	r := inject(t, serve(t, f), p, writeAsk(t, ""), prompt)
	if r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	order := strings.Join(f.order(), ",")
	if !strings.Contains(order, "thread/resume") || strings.Index(order, "thread/resume") > strings.Index(order, "turn/start") {
		t.Fatalf("order %s", order)
	}
	var rp struct {
		ThreadID     string `json:"threadId"`
		ExcludeTurns bool   `json:"excludeTurns"`
	}
	_ = json.Unmarshal(f.params["thread/resume"], &rp)
	if rp.ThreadID != "cold" || !rp.ExcludeTurns {
		t.Fatalf("resume %s", f.params["thread/resume"])
	}
}

func TestDuplicateClientIDIsNotInjectedAgain(t *testing.T) {
	p := project(t)
	f := &fakeServer{
		threads:   []thread{{ID: "s", Cwd: p, ThreadSource: user(), UpdatedAt: 1}, {ID: "x", Cwd: "/nowhere"}},
		loaded:    []string{"s"},
		clientIDs: map[string][]string{"s": {"other", msgID}},
	}
	r := inject(t, serve(t, f), p, writeAsk(t, ""), prompt)
	if r.code != 3 || r.stdout != "duplicate session=s\n" {
		t.Fatalf("got %+v", r)
	}
	if f.called("turn/start") {
		t.Fatal("injected a duplicate")
	}
}

func TestTurnStartErrorIsRefused(t *testing.T) {
	p := project(t)
	f := &fakeServer{threads: []thread{{ID: "s", Cwd: p, ThreadSource: user(), UpdatedAt: 1}, {ID: "x", Cwd: "/nowhere"}}, loaded: []string{"s"}, turnErr: true}
	r := inject(t, serve(t, f), p, writeAsk(t, ""), prompt)
	if r.code != 5 || r.stdout != "refused session=s\n" {
		t.Fatalf("got %+v", r)
	}
	noPromptLeak(t, r)
}

func TestMissingSocketIsDaemonUnavailable(t *testing.T) {
	r := inject(t, filepath.Join(t.TempDir(), "absent.sock"), project(t), writeAsk(t, ""), prompt)
	if r.code != 6 || r.stdout != "daemon-unavailable\n" {
		t.Fatalf("got %+v", r)
	}
}

func TestNoCandidatesIsNoSession(t *testing.T) {
	p := project(t)
	f := &fakeServer{threads: []thread{{ID: "a", Cwd: "/elsewhere", ThreadSource: user()}, {ID: "b", Cwd: "/else", ThreadSource: user()}}}
	r := inject(t, serve(t, f), p, writeAsk(t, ""), prompt)
	if r.code != 4 || r.stdout != "no-session\n" {
		t.Fatalf("got %+v", r)
	}
	if f.called("turn/start") {
		t.Fatal("injected with no session")
	}
}

func TestBadAskFileIsIgnored(t *testing.T) {
	p := project(t)
	for _, content := range []string{"bad id! 1\n", "\n", "a/b 1\n", strings.Repeat("x", 65) + " 1\n", "-- 1\nmore\n"} {
		f := &fakeServer{
			threads: []thread{
				{ID: "new", Cwd: p, ThreadSource: user(), UpdatedAt: 200},
				{ID: "bad", Cwd: p, ThreadSource: user(), UpdatedAt: 100},
			},
			loaded: []string{"new", "bad"},
		}
		r := inject(t, serve(t, f), p, writeAsk(t, content), prompt)
		if r.code != 0 || r.stdout != "accepted session=new turn=turn-1\n" {
			t.Fatalf("%q: got %+v", content, r)
		}
	}
}

func TestPromptOverOneMiBIsUsage(t *testing.T) {
	p := project(t)
	f := &fakeServer{threads: []thread{{ID: "s", Cwd: p, ThreadSource: user()}, {ID: "x", Cwd: "/n"}}, loaded: []string{"s"}}
	r := inject(t, serve(t, f), p, writeAsk(t, ""), strings.Repeat("a", 1<<20+1))
	if r.code != 2 || f.called("turn/start") {
		t.Fatalf("got %+v", r)
	}
	if r := inject(t, serve(t, f), p, writeAsk(t, ""), strings.Repeat("a", 1<<20)); r.code != 0 {
		t.Fatalf("exactly 1 MiB: %+v", r)
	}
}

func TestBadArgumentsAreUsage(t *testing.T) {
	p := project(t)
	for _, args := range [][]string{
		{"--project", p, "--conversation", "nope", "--message-id", msgID},
		{"--project", p, "--conversation", conv, "--message-id", "NOPE"},
		{"--project", filepath.Join(p, "missing"), "--conversation", conv, "--message-id", msgID},
		{"--conversation", conv, "--message-id", msgID},
	} {
		var out, errb bytes.Buffer
		if code := run(t.Context(), args, strings.NewReader("x"), &out, &errb, func(string) string { return "" }); code != 2 {
			t.Fatalf("%v: code %d", args, code)
		}
	}
}
