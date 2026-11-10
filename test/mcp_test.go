package test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mcpSendText = "smoke: mcp"

// TestMCPSend is the real-path check of sock-mcp's sock_send: the real binary,
// over stdio, to a NotifyOwner endpoint. By default the endpoint is the stub
// and the test asserts the stub saw one authenticated request with the text.
// With SOCK_SMOKE=1 it targets SOCK_URL with SOCK_TOKEN from the environment
// (scripts/smoke.sh) and asserts only that the call succeeded. Only a session
// configured to message the owner may: SOCK_MCP_MESSAGE_OWNER, else
// message_owner in the sock config file, else on.
func TestMCPSend(t *testing.T) {
	url, tok := "", token
	var s *stub
	if os.Getenv("SOCK_SMOKE") == "1" {
		url, tok = os.Getenv("SOCK_URL"), os.Getenv("SOCK_TOKEN")
		if url == "" || tok == "" {
			t.Fatal("SOCK_SMOKE=1 needs SOCK_URL and SOCK_TOKEN")
		}
	} else {
		s = newStub(t)
		url = s.URL
	}
	c := startMCP(t, url, tok, "SOCK_MCP_MESSAGE_OWNER=true")
	line := c.request(2, "tools/call", `{"name":"sock_send","arguments":{"text":"`+mcpSendText+`"}}`)
	var resp struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		c.die("sock_send response: %v: %s", err, line)
	}
	if len(resp.Error) > 0 {
		c.die("sock_send returned a JSON-RPC error: %s", line)
	}
	if resp.Result.IsError {
		c.die("sock_send returned isError:true: %s", line)
	}
	if s == nil {
		return
	}
	got := s.Requests()
	if len(got) != 1 || got[0].Auth != "Bearer "+token || got[0].Text != mcpSendText {
		t.Fatalf("stub saw %+v", got)
	}
}

// A session with owner messaging off, by the env or by message_owner = false
// in the config file, has its sock_send without conversation_id refused, and
// it reaches nothing.
func TestMCPSendRefusedOutsideDigestChannel(t *testing.T) {
	cfg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfg, "sock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "sock", "config.toml"), []byte("message_owner = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]string{
		"env":  "SOCK_MCP_MESSAGE_OWNER=off",
		"file": "XDG_CONFIG_HOME=" + cfg,
	} {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			c := startMCP(t, s.URL, token, env)
			line := c.request(2, "tools/call", `{"name":"sock_send","arguments":{"text":"`+mcpSendText+`"}}`)
			var resp struct {
				Result struct {
					IsError bool `json:"isError"`
				} `json:"result"`
			}
			if err := json.Unmarshal(line, &resp); err != nil || !resp.Result.IsError {
				c.die("want an isError refusal, got %v: %s", err, line)
			}
			if got := s.Requests(); len(got) != 0 {
				t.Fatalf("stub saw %+v", got)
			}
		})
	}
}

// TestMCPSendFailsOnBadResponses proves TestMCPSend is not a rubber stamp: it
// must fail against servers that answer id 2 with isError:true or with a
// JSON-RPC error.
func TestMCPSendFailsOnBadResponses(t *testing.T) {
	cases := []struct{ name, resp, want string }{
		{"isError", `{"jsonrpc":"2.0","id":2,"result":{"isError":true,"content":[{"type":"text","text":"permanent: nope"}]}}`, "isError:true"},
		{"rpc error", `{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"bad params"}}`, "JSON-RPC error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := filepath.Join(t.TempDir(), "fake-mcp")
			script := "#!/bin/sh\nread l\necho '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"serverInfo\":{\"name\":\"fake\",\"version\":\"0\"}}}'\nread l\nread l\necho '" + c.resp + "'\nexec sleep 600\n"
			writeFakeExec(t, fake, script)
			cmd := testCmd(t, os.Args[0], "-test.run=^TestMCPSend$")
			cmd.Env = append(os.Environ(), "SOCK_MCP_BIN="+fake, "SOCK_SMOKE=")
			var buf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			err := runCmd(cmd)
			out := buf.Bytes()
			if err == nil {
				t.Fatalf("TestMCPSend passed against a failing server:\n%s", out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Fatalf("want %q in failure:\n%s", c.want, out)
			}
		})
	}
}
