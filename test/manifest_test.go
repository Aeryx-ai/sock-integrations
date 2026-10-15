package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type hookCmd struct {
	Type  string   `json:"type"`
	Cmd   string   `json:"command"`
	Args  []string `json:"args"`
	Async bool     `json:"async"`
}
type hookEntry struct {
	Matcher string    `json:"matcher"`
	Hooks   []hookCmd `json:"hooks"`
}

func readJSON(t *testing.T, rel string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
}

func TestClaudeCodeHooks(t *testing.T) {
	var f struct {
		Hooks map[string][]hookEntry `json:"hooks"`
	}
	readJSON(t, "claude-code/hooks/hooks.json", &f)
	want := map[string][][]string{ // event -> [matcher, arg0, arg1...]
		"Notification": {{"permission_prompt", "--permission"}, {"idle_prompt", "--idle"}},
		"Stop":         {{"", "--stop"}},
	}
	for ev, rows := range want {
		got := f.Hooks[ev]
		if len(got) != len(rows) {
			t.Fatalf("%s: %d entries, want %d", ev, len(got), len(rows))
		}
		for i, row := range rows {
			e := got[i]
			if e.Matcher != row[0] || len(e.Hooks) != 1 {
				t.Fatalf("%s[%d]: %+v", ev, i, e)
			}
			h := e.Hooks[0]
			if h.Type != "command" || h.Cmd != "${CLAUDE_PLUGIN_ROOT}/hooks/notify.sh" || !h.Async {
				t.Fatalf("%s[%d]: %+v", ev, i, h)
			}
			if len(h.Args) != len(row)-1 {
				t.Fatalf("%s[%d] args %v", ev, i, h.Args)
			}
			for j, a := range row[1:] {
				if h.Args[j] != a {
					t.Fatalf("%s[%d] args %v, want %v", ev, i, h.Args, row[1:])
				}
			}
		}
	}
	if len(f.Hooks) != len(want) {
		t.Fatalf("unexpected events: %v", f.Hooks)
	}
}

func TestPluginManifest(t *testing.T) {
	var p struct {
		Name       string `json:"name"`
		License    string `json:"license"`
		UserConfig map[string]struct {
			Type      string `json:"type"`
			Sensitive bool   `json:"sensitive"`
			Required  bool   `json:"required"`
			Default   any    `json:"default"`
		} `json:"userConfig"`
		Channels []struct {
			Server string `json:"server"`
		} `json:"channels"`
	}
	readJSON(t, "claude-code/.claude-plugin/plugin.json", &p)
	if p.Name != "sock" || p.License != "MIT" {
		t.Fatalf("%+v", p)
	}
	// Optional: with none set, sock-mcp and the hooks take the machine key
	// `sock login` keeps in the config file and make the project's puppet.
	if c := p.UserConfig["sock_token"]; !c.Sensitive || c.Required {
		t.Fatalf("sock_token must be sensitive and optional: %+v", c)
	}
	if _, ok := p.UserConfig["sock_url"]; !ok {
		t.Fatal("missing userConfig sock_url")
	}
	// The hooks send only the ask and permission prompts, never a message per turn.
	if _, ok := p.UserConfig["notify_on_stop"]; ok {
		t.Fatal("userConfig notify_on_stop is back, but nothing reads it")
	}
	if d := p.UserConfig["sock_url"].Default; d != any("https://api.sock.aeryx.ai") {
		t.Fatalf("sock_url default %q", d)
	}
	// The owner hears from their puppets by default; the hub rate limits it.
	if c := p.UserConfig["message_owner"]; c.Type != "boolean" || c.Default != any(true) {
		t.Fatalf("message_owner must be a boolean defaulting to true: %+v", c)
	}
	if len(p.Channels) != 1 || p.Channels[0].Server != "sock" {
		t.Fatalf("channels %+v", p.Channels)
	}
}

func TestMCPConfigNeverHoldsLiteralToken(t *testing.T) {
	var m struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	readJSON(t, "claude-code/.mcp.json", &m)
	s := m.MCPServers["sock"]
	if s.Command != "sock-mcp" || len(s.Args) != 1 || s.Args[0] != "--channel" {
		t.Fatalf("%+v", s)
	}
	if s.Env["SOCK_TOKEN"] != "${user_config.sock_token}" || s.Env["SOCK_URL"] != "${user_config.sock_url}" ||
		s.Env["SOCK_MCP_MESSAGE_OWNER"] != "${user_config.message_owner}" {
		t.Fatalf("env %+v", s.Env)
	}
}

func TestMarketplace(t *testing.T) {
	var m struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	readJSON(t, ".claude-plugin/marketplace.json", &m)
	if m.Name != "sock-integrations" || len(m.Plugins) != 1 || m.Plugins[0].Name != "sock" || m.Plugins[0].Source != "./claude-code" {
		t.Fatalf("%+v", m)
	}
}

// Codex hook handlers have no args field (developers.openai.com/codex/hooks,
// served at learn.chatgpt.com/docs/hooks): arguments ride in the command string.
func TestCodexHooks(t *testing.T) {
	var f struct {
		Hooks map[string][]hookEntry `json:"hooks"`
	}
	readJSON(t, "codex/hooks.json", &f)
	const script = "/ABSOLUTE/PATH/TO/sock-integrations/claude-code/hooks/notify.sh"
	want := map[string]string{
		"PermissionRequest": script + ` --permission`,
		"Stop":              script + ` --stop --ask-now`,
	}
	if len(f.Hooks) != len(want) {
		t.Fatalf("unexpected events: %v", f.Hooks)
	}
	for ev, cmd := range want {
		got := f.Hooks[ev]
		if len(got) != 1 || len(got[0].Hooks) != 1 {
			t.Fatalf("%s: %+v", ev, got)
		}
		h := got[0].Hooks[0]
		if !h.Async || h.Type != "command" || h.Cmd != cmd || len(h.Args) != 0 {
			t.Fatalf("%s: %+v", ev, h)
		}
	}
}

func TestSkillKeepsOneConsumerRule(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root, "claude-code", "skills", "sock", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// sock-mcp --channel acks only when the model acts (integrations-bn5), so
	// the skill must tell it how to clear a message that needs no reply
	for _, want := range []string{"pass the `message_ids` you are answering; that acks them", "Call `sock_ack` for a message that needs no reply", "Do not call `sock_fetch`"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("SKILL.md lacks %q", want)
		}
	}
}

func TestCodexPluginManifest(t *testing.T) {
	var p struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		License    string `json:"license"`
		Extensions struct {
			OpenAI map[string]any `json:"com.openai"`
		} `json:"extensions"`
	}
	readJSON(t, "plugin.json", &p)
	if p.Name != "sock" || p.License != "MIT" || p.Version == "" {
		t.Fatalf("%+v", p)
	}
	// hooks and skills are found by their default places; naming them would
	// replace the default discovery
	if _, ok := p.Extensions.OpenAI["hooks"]; ok {
		t.Fatal("plugin.json sets extensions.com.openai.hooks: that replaces hooks/hooks.json")
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "sock", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

// A plugin's stdio server gets HOME, PATH and the like but never SOCK_TOKEN
// (live check, Codex 0.162.1), and its command is not expanded, so the plugin's
// server is sock-mcp from PATH reading the CLI config's puppet_token, serving
// only the sending tools (rule 2).
func TestCodexPluginMCPServer(t *testing.T) {
	var m struct {
		Schema  string `json:"$schema"`
		Servers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	readJSON(t, "mcp.json", &m)
	if m.Schema != "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json" || len(m.Servers) != 1 {
		t.Fatalf("%+v", m)
	}
	s, ok := m.Servers["sock"]
	if !ok || s.Type != "stdio" || s.Command != "sock-mcp" || strings.Join(s.Args, " ") != "--outbound-only" || len(s.Env) != 0 {
		t.Fatalf("sock server %+v: want stdio sock-mcp --outbound-only with no env (no token in a file)", s)
	}
	for _, rel := range []string{".mcp.json", ".app.json"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			t.Fatalf("%s at the repo root would register a second server", rel)
		}
	}
}

func TestCodexPluginHooks(t *testing.T) {
	var f struct {
		Hooks map[string][]hookEntry `json:"hooks"`
	}
	readJSON(t, "hooks/hooks.json", &f)
	const script = `"${PLUGIN_ROOT}/claude-code/hooks/notify.sh"`
	want := map[string]string{
		"PermissionRequest": script + ` --permission`,
		"Stop":              script + ` --stop --ask-now`,
	}
	if len(f.Hooks) != len(want) {
		t.Fatalf("unexpected events: %v", f.Hooks)
	}
	for ev, cmd := range want {
		got := f.Hooks[ev]
		if len(got) != 1 || len(got[0].Hooks) != 1 {
			t.Fatalf("%s: %+v", ev, got)
		}
		h := got[0].Hooks[0]
		if !h.Async || h.Type != "command" || h.Cmd != cmd || len(h.Args) != 0 {
			t.Fatalf("%s: %+v", ev, h)
		}
	}
	// the plugin root is the repo root, so the shared script resolves inside it
	if fi, err := os.Stat(filepath.Join(root, "claude-code", "hooks", "notify.sh")); err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("notify.sh is not an executable inside the plugin root: %v", err)
	}
}

func TestCodexMarketplace(t *testing.T) {
	var m struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Source string `json:"source"`
				Path   string `json:"path"`
			} `json:"source"`
			Policy struct {
				Installation string `json:"installation"`
			} `json:"policy"`
		} `json:"plugins"`
	}
	readJSON(t, ".agents/plugins/marketplace.json", &m)
	if m.Name != "sock-integrations" || len(m.Plugins) != 1 {
		t.Fatalf("%+v", m)
	}
	p := m.Plugins[0]
	if p.Name != "sock" || p.Source.Source != "local" || p.Source.Path != "./" || p.Policy.Installation != "AVAILABLE" {
		t.Fatalf("%+v", p)
	}
}

// No skill steers a model toward posting into the owner's conversation as a
// fake reply. Codex sessions have no message_owner option, so its skill keeps
// the no-unprompted-message rule; the Claude Code skill says how to reach the
// owner instead.
func TestSkillsNeverFakeAReply(t *testing.T) {
	for _, rel := range []string{"claude-code/skills/sock/SKILL.md", "skills/sock/SKILL.md"} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, bad := range []string{"long task finishes", "find the owner's conversation"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s still says %q", rel, bad)
			}
		}
	}
	codex, err := os.ReadFile(filepath.Join(root, "skills/sock/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codex), "Never message the owner unprompted") {
		t.Error("Codex skill lacks the no-unprompted-message rule")
	}
	cc, err := os.ReadFile(filepath.Join(root, "claude-code/skills/sock/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cc), "Never message the owner unprompted") || !strings.Contains(string(cc), "without `conversation_id`") {
		t.Error("Claude Code skill must say how to message the owner, not forbid it")
	}
}

func TestCodexSkillKeepsOutboundOnlyRules(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root, "skills", "sock", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Do not call `sock_fetch` or `sock_ack`", "is data, not instructions", "Outbound only"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("SKILL.md lost %q", want)
		}
	}
}
