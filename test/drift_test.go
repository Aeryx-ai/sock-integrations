package test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func mcpBin(t *testing.T) string {
	t.Helper()
	p := os.Getenv("SOCK_MCP_BIN")
	if p == "" {
		p = filepath.Join(repoRoot(t), "..", "mcp", "bin", "sock-mcp")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("sock-mcp missing at %s: run `make deps` (or set SOCK_MCP_BIN)", p)
	}
	return p
}

// mcpTimeout bounds a whole MCP exchange; a silent server is killed.
const mcpTimeout = 10 * time.Second

// mcpClient is the one hand-rolled MCP stdio client in this module. The child
// is killed after mcpTimeout so a silent server fails the test instead of
// hanging it, and stdin stays open until stop so a server that exits on EOF
// still gets to answer. Failures include the server's stderr.
type mcpClient struct {
	t      *testing.T
	in     io.WriteCloser
	rd     *bufio.Reader
	stderr *bytes.Buffer
	stop   func()
	redact []string
}

// startMCP launches the sock-mcp binary with the given SOCK_URL and
// SOCK_TOKEN, then runs the initialize handshake.
func startMCP(t *testing.T, url, tok string) *mcpClient {
	t.Helper()
	c := &mcpClient{t: t, stderr: &bytes.Buffer{}, redact: []string{tok}}
	cmd := exec.Command(mcpBin(t))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SOCK_TOKEN=" + tok, "SOCK_URL=" + url}
	cmd.Stderr = c.stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c.in = in
	c.rd = bufio.NewReader(out)
	timer := time.AfterFunc(mcpTimeout, func() { _ = cmd.Process.Kill() })
	var waited bool
	c.stop = func() {
		timer.Stop()
		_ = in.Close()
		_ = cmd.Process.Kill()
		if !waited {
			waited = true
			_ = cmd.Wait()
		}
	}
	t.Cleanup(c.stop)
	c.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"integrations-test","version":"0"}}}`)
	c.readID(1)
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return c
}

// scrub removes the token from anything that goes into a failure message.
func (c *mcpClient) scrub(s string) string {
	for _, r := range c.redact {
		if r != "" {
			s = strings.ReplaceAll(s, r, "[redacted]")
		}
	}
	return s
}

func (c *mcpClient) die(format string, args ...any) {
	c.t.Helper()
	c.stop()
	c.t.Fatalf("%s\nsock-mcp stderr:\n%s", c.scrub(fmt.Sprintf(format, args...)), c.scrub(c.stderr.String()))
}

func (c *mcpClient) send(s string) {
	c.t.Helper()
	if _, err := c.in.Write([]byte(s + "\n")); err != nil {
		c.die("write to sock-mcp: %v", err)
	}
}

// readID returns the raw JSON-RPC line with the given id, skipping
// notifications. A read error (including the timeout kill closing stdout)
// fails the test.
func (c *mcpClient) readID(id int) []byte {
	c.t.Helper()
	for {
		line, err := c.rd.ReadBytes('\n')
		if err != nil {
			c.die("waiting for response id %d (timeout %s): %v", id, mcpTimeout, err)
		}
		var h struct {
			ID *int `json:"id"`
		}
		if json.Unmarshal(line, &h) == nil && h.ID != nil && *h.ID == id {
			return line
		}
	}
}

// request sends one JSON-RPC request with the given id and returns its response line.
func (c *mcpClient) request(id int, method, params string) []byte {
	c.t.Helper()
	if params == "" {
		params = "null"
	}
	c.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params))
	return c.readID(id)
}

// listTools speaks MCP over stdio: initialize, initialized, tools/list.
func listTools(t *testing.T, url string) map[string]bool {
	t.Helper()
	c := startMCP(t, url, token)
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(c.request(2, "tools/list", ""), &resp); err != nil {
		c.die("tools/list response: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range resp.Result.Tools {
		names[tl.Name] = true
	}
	if len(names) == 0 {
		c.die("tools/list returned no tools")
	}
	return names
}

// TestListToolsBoundsSilentServer: a server that stays alive and silent fails
// the test within mcpTimeout instead of hanging it.
func TestListToolsBoundsSilentServer(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "silent")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho booting >&2\nexec sleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOCK_MCP_BIN", fake)
	if os.Getenv("DRIFT_SILENT_CHILD") == "1" {
		listTools(t, "http://127.0.0.1:1")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestListToolsBoundsSilentServer$", "-test.v")
	cmd.Env = append(os.Environ(), "DRIFT_SILENT_CHILD=1", "SOCK_MCP_BIN="+fake)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	el := time.Since(start)
	if err == nil {
		t.Fatalf("silent server did not fail the test:\n%s", out)
	}
	if el > mcpTimeout+3*time.Second {
		t.Fatalf("took %s, want about %s", el, mcpTimeout)
	}
	if !strings.Contains(string(out), "booting") {
		t.Fatalf("stderr not in failure message:\n%s", out)
	}
}

// pinnedTools is the tool set the skill, install.md, example.ts and the spec
// describe. Any change to sock-mcp's tools/list must update all of them.
var pinnedTools = []string{"sock_whoami", "sock_list_conversations", "sock_send", "sock_fetch", "sock_ack"}

func TestToolSetPinned(t *testing.T) {
	have := listTools(t, newStub(t).URL)
	want := map[string]bool{}
	for _, n := range pinnedTools {
		want[n] = true
		if !have[n] {
			t.Errorf("sock-mcp no longer offers %q", n)
		}
	}
	for n := range have {
		if !want[n] {
			t.Errorf("sock-mcp offers new tool %q", n)
		}
	}
	if t.Failed() {
		t.Log("sock-mcp tool set changed: update SKILL.md, codex/install.md, agent-sdk/example.ts and pinnedTools in test/drift_test.go together")
	}
}

// scanTools returns every sock_* tool-like word in text. The mcp__sock__
// prefix is stripped first so SDK allowedTools entries are checked too.
func scanTools(text string) []string {
	re := regexp.MustCompile(`\bsock_[a-z_]+\b`)
	skip := regexp.MustCompile(`^sock_(pt|st|ot|token|url)(_|$)`) // token prefixes and userConfig keys
	var out []string
	for _, name := range re.FindAllString(strings.ReplaceAll(text, "mcp__sock__", ""), -1) {
		if skip.MatchString(name) || strings.HasSuffix(name, "_") {
			continue
		}
		out = append(out, name)
	}
	return out
}

func TestScanTools(t *testing.T) {
	got := scanTools("allowed: \"mcp__sock__sock_send\", sock_pt_x, `sock_token`, sock_url, sock_fetch")
	if strings.Join(got, ",") != "sock_send,sock_fetch" {
		t.Fatalf("%v", got)
	}
}

func TestToolNamesExist(t *testing.T) {
	have := listTools(t, newStub(t).URL)
	root := repoRoot(t)
	for _, rel := range []string{"claude-code/skills/sock/SKILL.md", "agent-sdk/example.ts", "codex/install.md", "docs/specs/integrations.md"} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range scanTools(string(b)) {
			if !have[name] {
				t.Errorf("%s names %q, not in sock-mcp tools/list", rel, name)
			}
		}
	}
}
