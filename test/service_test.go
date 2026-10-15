package test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A sock stub whose listen --help offers --as-puppet unless STUB_NO_AS_PUPPET.
const svcSockStub = `#!/bin/sh
if [ "$1 $2" = "listen --help" ]; then
  echo "Flags:"
  [ -n "$STUB_NO_AS_PUPPET" ] || echo "      --as-puppet   listen as the puppet sock-mcp would"
  echo "      --exec string   shell command run per message"
  exit 0
fi
exit 64
`

// Records each call's argv as one line.
const svcRecordStub = `#!/bin/sh
printf '%s\n' "${0##*/} $*" >> "$STUB_DIR/calls"
`

type svcHarness struct {
	t                         *testing.T
	bin, dir, home, rec, proj string
	env                       map[string]string
}

func newServiceHarness(t *testing.T, osName string) *svcHarness {
	t.Helper()
	t.Parallel()
	dir := t.TempDir()
	h := &svcHarness{t: t, bin: filepath.Join(root, "runner", "sock-runner-service"), dir: dir,
		home: filepath.Join(dir, "home"), rec: filepath.Join(dir, "rec"), proj: filepath.Join(dir, "proj")}
	stubs := filepath.Join(dir, "bin")
	for _, d := range []string{stubs, h.rec, h.home, h.proj} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFakeExec(t, filepath.Join(stubs, "sock"), svcSockStub)
	writeFakeExec(t, filepath.Join(stubs, "uname"), "#!/bin/sh\necho "+osName+"\n")
	for _, n := range []string{"launchctl", "systemctl", "claude", "codex", "jq"} {
		writeFakeExec(t, filepath.Join(stubs, n), svcRecordStub)
	}
	h.env = map[string]string{
		"PATH":     stubs + ":" + rnPathTail,
		"HOME":     h.home,
		"TMPDIR":   dir,
		"STUB_DIR": h.rec,
	}
	return h
}

func (h *svcHarness) run(env map[string]string, args ...string) (int, string) {
	h.t.Helper()
	cmd := testCmd(h.t, h.bin, args...)
	cmd.Dir = h.dir
	for k, v := range h.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := runCmd(cmd)
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		return ee.ExitCode(), out.String()
	} else if err != nil {
		h.t.Fatal(err)
	}
	return 0, out.String()
}

func (h *svcHarness) calls() []string {
	b, _ := os.ReadFile(filepath.Join(h.rec, "calls"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (h *svcHarness) units(sub string) []string {
	m, _ := filepath.Glob(filepath.Join(h.home, sub, "*"))
	return m
}

func (h *svcHarness) physProj() string {
	p, err := filepath.EvalSymlinks(h.proj)
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

const launchAgents = "Library/LaunchAgents"

func TestServiceInstallLaunchd(t *testing.T) {
	h := newServiceHarness(t, "Darwin")
	code, out := h.run(nil, "install", h.proj)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	units := h.units(launchAgents)
	if len(units) != 1 || !strings.HasPrefix(filepath.Base(units[0]), "ai.aeryx.sock-runner.") || filepath.Ext(units[0]) != ".plist" {
		t.Fatalf("units %v", units)
	}
	label := strings.TrimSuffix(filepath.Base(units[0]), ".plist")
	b, _ := os.ReadFile(units[0])
	plist := string(b)
	proj, runner := h.physProj(), filepath.Join(root, "runner", "sock-runner")
	for _, want := range []string{
		"<key>Label</key>\n\t<string>" + label + "</string>",
		"<string>listen</string>\n\t\t<string>--as-puppet</string>\n\t\t<string>--project</string>\n\t\t<string>" + proj + "</string>\n\t\t<string>--exec</string>\n\t\t<string>" + runner + "</string>",
		"<key>KeepAlive</key>\n\t<true/>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>SOCK_RUNNER_WORKDIR</key>\n\t\t<string>" + proj + "</string>",
		"<key>SOCK_RUNNER_AGENT</key>\n\t\t<string>claude</string>",
		"<key>SOCK_RUNNER_PERMISSION</key>\n\t\t<string>default</string>",
		"<key>SOCK_RUNNER_STATE</key>\n\t\t<string>" + filepath.Join(h.home, ".local/state/sock-integrations/runner") + "/",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "SOCK_TOKEN") {
		t.Fatal("a credential in the unit")
	}
	if p, err := exec.LookPath("plutil"); err == nil {
		if o, err := exec.Command(p, "-lint", units[0]).CombinedOutput(); err != nil {
			t.Fatalf("plutil: %s", o)
		}
	}
	gui := "gui/" + strconv.Itoa(os.Getuid())
	want := []string{"launchctl bootout " + gui + "/" + label, "launchctl bootstrap " + gui + " " + units[0]}
	if c := h.calls(); strings.Join(c, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls %q, want %q", c, want)
	}

	// Installing again replaces the one unit; another project gets its own.
	if code, out := h.run(nil, "install", h.proj); code != 0 || len(h.units(launchAgents)) != 1 {
		t.Fatalf("reinstall %d %s %v", code, out, h.units(launchAgents))
	}
	other := filepath.Join(h.dir, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out := h.run(nil, "install", other); code != 0 || len(h.units(launchAgents)) != 2 {
		t.Fatalf("second project %d %s %v", code, out, h.units(launchAgents))
	}

	if code, out := h.run(nil, "uninstall", h.proj); code != 0 {
		t.Fatalf("uninstall %d %s", code, out)
	}
	if _, err := os.Stat(units[0]); !os.IsNotExist(err) {
		t.Fatal("unit left after uninstall")
	}
	if c := h.calls(); c[len(c)-1] != "launchctl bootout "+gui+"/"+label {
		t.Fatalf("last call %q", c[len(c)-1])
	}
}

func TestServiceInstallSystemd(t *testing.T) {
	h := newServiceHarness(t, "Linux")
	code, out := h.run(map[string]string{"SOCK_RUNNER_PERMISSION": "dontAsk"}, "install", h.proj)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	units := h.units(".config/systemd/user")
	if len(units) != 1 || !strings.HasPrefix(filepath.Base(units[0]), "sock-runner-") || filepath.Ext(units[0]) != ".service" {
		t.Fatalf("units %v", units)
	}
	name := filepath.Base(units[0])
	b, _ := os.ReadFile(units[0])
	unit := string(b)
	proj, runner := h.physProj(), filepath.Join(root, "runner", "sock-runner")
	for _, want := range []string{
		"ExecStart=" + filepath.Join(h.dir, "bin", "sock") + " listen --as-puppet --project " + proj + " --exec " + runner + "\n",
		"Restart=always\n",
		"Environment=SOCK_RUNNER_WORKDIR=" + proj + "\n",
		"Environment=SOCK_RUNNER_PERMISSION=dontAsk\n",
		"WantedBy=default.target\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	want := []string{"systemctl --user daemon-reload", "systemctl --user enable --now " + name, "systemctl --user restart " + name}
	if c := h.calls(); strings.Join(c, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls %q, want %q", c, want)
	}
	if code, out := h.run(nil, "uninstall", h.proj); code != 0 {
		t.Fatalf("uninstall %d %s", code, out)
	}
	if _, err := os.Stat(units[0]); !os.IsNotExist(err) {
		t.Fatal("unit left after uninstall")
	}
}

func TestServiceInstallRefuses(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		dir  func(h *svcHarness) string
	}{
		{"listen without --as-puppet", map[string]string{"STUB_NO_AS_PUPPET": "1"}, nil},
		{"missing project", nil, func(h *svcHarness) string { return filepath.Join(h.dir, "nope") }},
		{"space in path", nil, func(h *svcHarness) string {
			d := filepath.Join(h.dir, "a b")
			_ = os.Mkdir(d, 0o755)
			return d
		}},
		{"plan mode", map[string]string{"SOCK_RUNNER_PERMISSION": "plan"}, nil},
		{"unknown agent", map[string]string{"SOCK_RUNNER_AGENT": "bogus"}, nil},
		{"codex without a session", map[string]string{"SOCK_RUNNER_AGENT": "codex", "SOCK_RUNNER_PERMISSION": "read-only"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newServiceHarness(t, "Darwin")
			dir := h.proj
			if c.dir != nil {
				dir = c.dir(h)
			}
			code, out := h.run(c.env, "install", dir)
			if code != 2 || out == "" {
				t.Fatalf("exit %d, output %q; want 2 and a reason", code, out)
			}
			if u := h.units(launchAgents); len(u) != 0 {
				t.Fatalf("unit written: %v", u)
			}
			if c := h.calls(); len(c) != 1 || c[0] != "" {
				t.Fatalf("launchctl ran: %q", c)
			}
		})
	}
}
