package test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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

// mcpTimeout bounds a whole tools/list exchange; a silent server is killed.
const mcpTimeout = 10 * time.Second

// listTools speaks MCP over stdio: initialize, initialized, tools/list. The
// process is killed after mcpTimeout so a silent server fails the test
// instead of hanging it. Failures include the server's stderr.
func listTools(t *testing.T, url string) map[string]bool {
	t.Helper()
	cmd := exec.Command(mcpBin(t))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SOCK_TOKEN=" + token, "SOCK_URL=" + url}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
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
	timer := time.AfterFunc(mcpTimeout, func() { _ = cmd.Process.Kill() })
	var waited bool
	stop := func() {
		timer.Stop()
		_ = in.Close()
		_ = cmd.Process.Kill()
		if !waited {
			waited = true
			_ = cmd.Wait()
		}
	}
	t.Cleanup(stop)
	die := func(format string, args ...any) {
		t.Helper()
		stop()
		t.Fatalf("%s\nsock-mcp stderr:\n%s", fmt.Sprintf(format, args...), stderr.String())
	}
	rd := bufio.NewReader(out)
	send := func(s string) {
		t.Helper()
		if _, err := in.Write([]byte(s + "\n")); err != nil {
			die("write to sock-mcp: %v", err)
		}
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"drift","version":"0"}}}`)
	readID(rd, 1, die)
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(readID(rd, 2, die), &resp); err != nil {
		die("tools/list response: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range resp.Result.Tools {
		names[tl.Name] = true
	}
	if len(names) == 0 {
		die("tools/list returned no tools")
	}
	return names
}

// readID returns the raw JSON-RPC line with the given id, skipping
// notifications. A read error (including the timeout kill closing stdout)
// goes to die.
func readID(rd *bufio.Reader, id int, die func(string, ...any)) []byte {
	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			die("waiting for response id %d (timeout %s): %v", id, mcpTimeout, err)
		}
		var h struct {
			ID *int `json:"id"`
		}
		if json.Unmarshal(line, &h) == nil && h.ID != nil && *h.ID == id {
			return line
		}
	}
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
