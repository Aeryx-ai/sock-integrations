package test

import (
	"bufio"
	"encoding/json"
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

// listTools speaks MCP over stdio: initialize, initialized, tools/list.
func listTools(t *testing.T, url string) map[string]bool {
	t.Helper()
	cmd := exec.Command(mcpBin(t))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SOCK_TOKEN=" + token, "SOCK_URL=" + url}
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	rd := bufio.NewReader(out)
	send := func(s string) { _, _ = in.Write([]byte(s + "\n")) }
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"drift","version":"0"}}}`)
	readID(t, rd, 1)
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(readID(t, rd, 2), &resp); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range resp.Result.Tools {
		names[tl.Name] = true
	}
	if len(names) == 0 {
		t.Fatal("tools/list returned no tools")
	}
	return names
}

// readID returns the raw JSON-RPC line with the given id, skipping notifications.
func readID(t *testing.T, rd *bufio.Reader, id int) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var h struct {
			ID *int `json:"id"`
		}
		if json.Unmarshal(line, &h) == nil && h.ID != nil && *h.ID == id {
			return line
		}
	}
	t.Fatalf("no response for id %d", id)
	return nil
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
