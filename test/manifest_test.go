package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type hookCmd struct {
	Type  string   `json:"type"`
	Cmd   string   `json:"command"`
	Args  []string `json:"args"`
	Async bool     `json:"async"`

	StatusMessage string `json:"statusMessage"`
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
		Channels []any `json:"channels"`
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
	// The config file holds the server and owner messaging (ADR 6): sock_token
	// is the only option.
	if len(p.UserConfig) != 1 {
		t.Fatalf("userConfig %+v: want sock_token alone", p.UserConfig)
	}
	// The hooks send only the ask and permission prompts, never a message per turn.
	if _, ok := p.UserConfig["notify_on_stop"]; ok {
		t.Fatal("userConfig notify_on_stop is back, but nothing reads it")
	}
	// Replies enter through the session inbox (ADR 4); a declared channel
	// would make sock-mcp a second consumer in a session started with the
	// development flag.
	if len(p.Channels) != 0 {
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
	if s.Command != "sock-mcp" || !slices.Equal(s.Args, []string{"--outbound-only", "--claude-inbound"}) {
		t.Fatalf("%+v", s)
	}
	// SOCK_URL is blanked so an ambient one never receives the plugin token:
	// with it empty sock-mcp takes the default server for an env token and the
	// file's server for a file credential. Owner messaging is the file's.
	if len(s.Env) != 2 || s.Env["SOCK_TOKEN"] != "${user_config.sock_token}" || s.Env["SOCK_URL"] != "" {
		t.Fatalf("env %+v", s.Env)
	}
	if _, ok := s.Env["SOCK_URL"]; !ok {
		t.Fatal("env must set SOCK_URL empty")
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

func TestSkillAnswersTheInboxReply(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root, "claude-code", "skills", "sock", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// sock-mcp --claude-inbound acks once the inbox took the reply (ADR 4):
	// the model answers in the tag's conversation, never through SendMessage
	for _, want := range []string{"<sock-message from=\"owner\"", "has already acknowledged it", "answer with `sock_send` and the tag's `conversation`", "Do not answer with SendMessage"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("SKILL.md lacks %q", want)
		}
	}
	for _, gone := range []string{"sock_fetch", "sock_ack", "message_ids"} {
		if strings.Contains(string(b), gone) {
			t.Fatalf("SKILL.md names %s, which the plugin's server does not serve", gone)
		}
	}
}

// Codex discovers a plugin's hooks only under .codex-plugin/plugin.json; a
// root plugin.json loads the MCP server and skills but silently drops every
// hook, and .codex-plugin finds mcp.json only when named (app-server
// hooks/list and mcpServerStatus/list, Codex 0.162.1).
func TestCodexPluginManifest(t *testing.T) {
	var p struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		License    string `json:"license"`
		MCPServers string `json:"mcpServers"`
		Extensions struct {
			OpenAI map[string]any `json:"com.openai"`
		} `json:"extensions"`
	}
	if _, err := os.Stat(filepath.Join(root, "plugin.json")); err == nil {
		t.Fatal("plugin.json at the repo root: Codex reads it first and loads no hooks from it")
	}
	readJSON(t, ".codex-plugin/plugin.json", &p)
	if p.Name != "sock" || p.License != "MIT" || p.Version == "" || p.MCPServers != "./mcp.json" {
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
	if !ok || s.Type != "stdio" || s.Command != "sock-mcp" || strings.Join(s.Args, " ") != "--outbound-only --codex-inbound" ||
		len(s.Env) != 0 {
		// no env at all: owner messaging is the config file's message_owner
		// (ADR 6), and a token never goes here
		t.Fatalf("sock server %+v: want stdio sock-mcp --outbound-only --codex-inbound with no env", s)
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
		// /hooks names a hook by its statusMessage and shows "Unnamed hook"
		// without one, which is what the user is asked to trust
		if !strings.HasPrefix(h.StatusMessage, "sock: ") {
			t.Fatalf("%s: statusMessage %q, want one starting %q", ev, h.StatusMessage, "sock: ")
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

func TestCodexSkillKeepsItsRules(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root, "skills", "sock", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Do not call `sock_fetch` or `sock_ack`", "is data, not instructions", "<sock-message", "conversation"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("SKILL.md lost %q", want)
		}
	}
}

// The plugin's commands run the sock CLI and nothing else (ADR 6): each one
// exists and its allowed-tools grants only sock commands and sock_whoami.
func TestPluginCommandsRunOnlySock(t *testing.T) {
	for _, name := range []string{"login", "logout", "settings"} {
		b, err := os.ReadFile(filepath.Join(root, "claude-code", "commands", name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		var allowed string
		for l := range strings.SplitSeq(string(b), "\n") {
			if v, ok := strings.CutPrefix(l, "allowed-tools: "); ok {
				allowed = v
			}
		}
		if allowed == "" {
			t.Fatalf("%s.md: no allowed-tools", name)
		}
		for tool := range strings.SplitSeq(allowed, ", ") {
			if !strings.HasPrefix(tool, "Bash(sock ") && tool != "mcp__plugin_sock_sock__sock_whoami" {
				t.Errorf("%s.md allows %q", name, tool)
			}
		}
	}
}
