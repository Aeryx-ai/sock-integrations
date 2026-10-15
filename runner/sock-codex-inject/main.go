// Command sock-codex-inject hands an owner reply to a Codex session as a turn,
// through the Codex app-server daemon's control socket. It is the
// anti-corruption layer between sock-codex and the app-server protocol: no
// Codex term leaves it except a session id (docs/specs/codex-inbound.md).
//
//	sock-codex-inject --project DIR --conversation UUID --message-id UUID [--socket PATH] [--asks DIR]
//
// The prompt comes on stdin, at most 1 MiB, and is never printed. stdout is one
// line: the outcome, then " session=<id>" when a session was chosen, then
// " turn=<id>" when accepted. Exit 0 accepted, 3 duplicate, 4 no-session,
// 5 refused, 6 daemon-unavailable, 2 usage.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"

	client "github.com/guygrigsby/sock-client"
)

var version = "dev"

const (
	maxPrompt  = 1 << 20
	maxThreads = 500
	maxItems   = 2000 // user-message items scanned for a duplicate
	deadline   = 30 * time.Second
)

var (
	uuidRE    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sessionRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	secondsRE = regexp.MustCompile(`^[0-9]{1,19}$`)
)

// outcome is how one injection ended, with its exit status.
type outcome struct {
	name string
	code int
}

var (
	accepted    = outcome{"accepted", 0}
	duplicate   = outcome{"duplicate", 3}
	noSession   = outcome{"no-session", 4}
	refused     = outcome{"refused", 5}
	unavailable = outcome{"daemon-unavailable", 6}
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)
	cancel()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("sock-codex-inject", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := fs.String("project", "", "the project directory")
	conversation := fs.String("conversation", "", "the reply's conversation id")
	messageID := fs.String("message-id", "", "the reply's message id")
	socket := fs.String("socket", "", "the app-server control socket")
	asks := fs.String("asks", "", "the session-ask directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	usage := func(why string) int {
		_, _ = fmt.Fprintln(stderr, "sock-codex-inject:", why)
		return 2
	}
	if !uuidRE.MatchString(*conversation) || !uuidRE.MatchString(*messageID) {
		return usage("--conversation and --message-id must be lowercase UUIDs")
	}
	if st, err := os.Stat(*projectDir); *projectDir == "" || err != nil || !st.IsDir() {
		return usage("--project must be an existing directory")
	}
	prompt, err := io.ReadAll(io.LimitReader(stdin, maxPrompt+1))
	if err != nil || len(prompt) > maxPrompt {
		return usage("the prompt must be readable and at most 1 MiB")
	}
	if *socket == "" {
		home := getenv("CODEX_HOME")
		if home == "" {
			home = filepath.Join(userHome(getenv), ".codex")
		}
		*socket = filepath.Join(home, "app-server-control", "app-server-control.sock")
	}
	if *asks == "" {
		state := getenv("XDG_STATE_HOME")
		if state == "" {
			state = filepath.Join(userHome(getenv), ".local", "state")
		}
		*asks = filepath.Join(state, "sock-integrations", "codex-asks")
	}

	root, err := client.Project(ctx, *projectDir)
	if err != nil {
		return usage("cannot tell the project of --project: " + err.Error())
	}
	inj := injection{
		root:      root,
		asked:     readAsk(filepath.Join(*asks, *conversation)),
		messageID: *messageID,
		prompt:    string(prompt),
	}
	res, session, turn, why := inj.run(ctx, *socket)
	line := res.name
	if session != "" {
		line += " session=" + session
	}
	if turn != "" {
		line += " turn=" + turn
	}
	_, _ = fmt.Fprintln(stdout, line)
	if why != "" {
		_, _ = fmt.Fprintln(stderr, "sock-codex-inject:", why)
	}
	return res.code
}

func userHome(getenv func(string) string) string {
	if h := getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// readAsk is the session that last posted to the conversation, or "" when the
// file is missing or does not hold exactly one valid "<session> <seconds>" line.
func readAsk(path string) string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 256 {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 1 {
		return ""
	}
	f := strings.Split(lines[0], " ")
	if len(f) != 2 || !sessionRE.MatchString(f[0]) || !secondsRE.MatchString(f[1]) {
		return ""
	}
	return f[0]
}

type injection struct {
	root, asked, messageID, prompt string
}

// run returns the outcome, the chosen session ("" when none), the turn id
// (accepted only) and a reason for stderr carrying ids only.
func (in injection) run(ctx context.Context, socket string) (outcome, string, string, string) {
	c, err := dial(ctx, socket)
	if err != nil {
		return unavailable, "", "", err.Error()
	}
	defer c.close()

	loaded, err := c.loaded(ctx)
	if err != nil {
		return refused, "", "", "thread/loaded/list: " + err.Error()
	}
	sessions, err := in.candidates(ctx, c, loaded)
	if err != nil {
		return refused, "", "", "thread/list: " + err.Error()
	}
	session := in.choose(sessions)
	if session == "" {
		return noSession, "", "", ""
	}
	// every candidate, not only the chosen one: a retry may choose another
	for _, s := range sessions {
		dup, err := c.hasClientID(ctx, s.ID, in.messageID)
		if err != nil {
			return refused, session, "", "thread/items/list: " + err.Error()
		}
		if dup {
			return duplicate, s.ID, "", ""
		}
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	err = c.call(ctx, "turn/start", map[string]any{
		"threadId":            session,
		"input":               []map[string]string{{"type": "text", "text": in.prompt}},
		"clientUserMessageId": in.messageID,
	}, &started)
	if err != nil {
		return refused, session, "", "turn/start: " + err.Error()
	}
	if started.Turn.ID == "" {
		return refused, session, "", "turn/start returned no turn"
	}
	return accepted, session, started.Turn.ID, ""
}

// session is a Codex thread as this command sees it.
type session struct {
	ID           string  `json:"id"`
	Cwd          string  `json:"cwd"`
	Ephemeral    bool    `json:"ephemeral"`
	ThreadSource *string `json:"threadSource"`
	UpdatedAt    int64   `json:"updatedAt"`
}

// interactive are the thread sources a person types into: the TUI and the
// Codex app both report vscode, the CLI cli.
var interactive = []string{"cli", "vscode"}

// candidates are the loaded interactive sessions of the project: not ephemeral
// (title generation), not another thread source, and in the same project as
// the puppet sees it (client.Project), so a nested repository is another
// project and a linked worktree is this one. A session the daemon has not
// loaded is never one: resuming it would not keep the settings it was started
// with.
func (in injection) candidates(ctx context.Context, c *rpc, loaded map[string]bool) ([]session, error) {
	var out []session
	var cursor *string
	projects := map[string]string{}
	seen := 0
	for seen < maxThreads {
		params := map[string]any{"limit": 100, "sortKey": "updated_at", "sortDirection": "desc", "sourceKinds": interactive}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		var page struct {
			Data       []session `json:"data"`
			NextCursor *string   `json:"nextCursor"`
		}
		if err := c.call(ctx, "thread/list", params, &page); err != nil {
			return nil, err
		}
		for _, s := range page.Data {
			seen++
			// thread/list already holds interactive sources only (its sourceKinds
			// default) and leaves threadSource null on TUI threads; any value it
			// does give must be "user"
			if s.Ephemeral || (s.ThreadSource != nil && *s.ThreadSource != "user") || !sessionRE.MatchString(s.ID) || !loaded[s.ID] {
				continue
			}
			if in.sameProject(ctx, projects, s.Cwd) {
				out = append(out, s)
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

// sameProject reports whether cwd is in the project, caching each cwd's
// project in seen. A cwd that is gone or not absolute is in none.
func (in injection) sameProject(ctx context.Context, seen map[string]string, cwd string) bool {
	if !filepath.IsAbs(cwd) {
		return false
	}
	p, ok := seen[cwd]
	if !ok {
		if st, err := os.Stat(cwd); err == nil && st.IsDir() {
			p, _ = client.Project(ctx, cwd)
		}
		seen[cwd] = p
	}
	return p != "" && p == in.root
}

// choose is the asked session when it is a candidate, else the newest.
func (in injection) choose(sessions []session) string {
	newest := ""
	var at int64
	for _, s := range sessions {
		if s.ID == in.asked {
			return s.ID
		}
		if newest == "" || s.UpdatedAt > at {
			newest, at = s.ID, s.UpdatedAt
		}
	}
	return newest
}

// --- JSON-RPC over the control socket's WebSocket ----------------------------

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("json-rpc error %d", e.Code) }

type rpc struct {
	ws *websocket.Conn
	n  int64
}

func dial(ctx context.Context, socket string) (*rpc, error) {
	if _, err := os.Stat(socket); err != nil {
		return nil, errors.New("no control socket")
	}
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
	ws, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		return nil, errors.New("control socket did not accept a WebSocket")
	}
	ws.SetReadLimit(64 << 20)
	c := &rpc{ws: ws}
	if err := c.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "sock-codex-inject", "version": version}}, nil); err != nil {
		_ = ws.CloseNow()
		return nil, errors.New("initialize failed")
	}
	if err := c.send(ctx, map[string]any{"method": "initialized"}); err != nil {
		_ = ws.CloseNow()
		return nil, errors.New("initialized failed")
	}
	return c, nil
}

func (c *rpc) close() { _ = c.ws.Close(websocket.StatusNormalClosure, "") }

func (c *rpc) send(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, b)
}

// call sends one request and waits for its response, skipping notifications
// and requests from the server. A JSON-RPC error is a *rpcError.
func (c *rpc) call(ctx context.Context, method string, params, out any) error {
	c.n++
	id := c.n
	if err := c.send(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		_, b, err := c.ws.Read(ctx)
		if err != nil {
			return err
		}
		var resp struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if json.Unmarshal(b, &resp) != nil || resp.Method != "" || resp.ID == nil || *resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(resp.Result, out)
	}
}

func (c *rpc) loaded(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	var cursor *string
	for range 50 {
		params := map[string]any{}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		var page struct {
			Data       []string `json:"data"`
			NextCursor *string  `json:"nextCursor"`
		}
		if err := c.call(ctx, "thread/loaded/list", params, &page); err != nil {
			return nil, err
		}
		for _, id := range page.Data {
			out[id] = true
		}
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Data) == 0 {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

// hasClientID reports whether the session already holds a user message whose
// clientId is id: an earlier delivery of the same sock message got in.
func (c *rpc) hasClientID(ctx context.Context, threadID, id string) (bool, error) {
	var cursor *string
	scanned := 0
	for scanned < maxItems {
		params := map[string]any{"threadId": threadID, "limit": 200, "sortDirection": "desc"}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		var page struct {
			Data []struct {
				Item struct {
					Type     string  `json:"type"`
					ClientID *string `json:"clientId"`
				} `json:"item"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := c.call(ctx, "thread/items/list", params, &page); err != nil {
			return false, err
		}
		for _, e := range page.Data {
			scanned++
			if e.Item.Type == "userMessage" && e.Item.ClientID != nil && *e.Item.ClientID == id {
				return true, nil
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Data) == 0 {
			break
		}
		cursor = page.NextCursor
	}
	return false, nil
}
