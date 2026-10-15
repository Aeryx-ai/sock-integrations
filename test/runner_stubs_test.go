package test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	rnOwner  = "11111111-1111-4111-8111-111111111111"
	rnOther  = "22222222-2222-4222-8222-222222222222"
	rnMsgID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	rnConvID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	rnSess   = "sess-1.2_x"

	// Unique sentinels: any appearance in a log, argv, env or runner output is a leak.
	rnTextSentinel  = "TEXTSENTINEL-7f3a9c1e"
	rnTokenSentinel = "sock_pt_RUNNERTOKENSENTINEL_9d41b7"
	rnOutSentinel   = "AGENTOUTSENTINEL-5c0e82"
	rnURLSentinel   = "http://sentinel-url.invalid:9"

	// A message text with shell metacharacters; it must reach the agent verbatim.
	rnText = rnTextSentinel + " line1\nline2 \"q\" 'sq' $HOME `id` $(touch PWNED) \\ end"

	// System tail of PATH after the stubs. jq/python live outside /usr/bin on macOS.
	rnPathTail = "/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin"
)

// Stub agent: records argv (one per line), env, cwd, pid and stdin, then
// behaves as the STUB_* variables say.
const rnAgentStub = `#!/bin/sh
name=$(basename "$0")
r=$(mktemp -d "$STUB_DIR/run.XXXXXX") || exit 99
echo "$name" > "$r/agent"
for a in "$@"; do printf '%s\n' "$a"; done > "$r/argv"
env > "$r/env"
pwd > "$r/pwd"
echo $$ > "$r/pid"
cat > "$r/stdin"
if [ -n "$STUB_GONE" ] && [ "$2" = "--resume" ]; then
  echo "No conversation found with session ID: $3" >&2
  exit 1
fi
[ -n "$STUB_IGNORE_TERM" ] && trap '' TERM
echo start >> "$STUB_DIR/events"
if [ -n "$STUB_OUT" ]; then echo "$STUB_OUT"; fi
if [ -n "$STUB_ERR" ]; then printf '%b\n' "$STUB_ERR" >&2; fi
if [ -n "$STUB_MARKER" ]; then
  ( sleep "${STUB_CHILD_SLEEP:-3}"; echo x > "$STUB_MARKER" ) >/dev/null 2>&1 &
  echo $! > "$r/childpid"
fi
sleep "${STUB_SLEEP:-0}"
echo end >> "$STUB_DIR/events"
exit "${STUB_EXIT:-0}"
`

const rnSockStub = `#!/bin/sh
if [ "$1" = activity ]; then
  printf '%s %s\n' "$(date +%s)" "$*" >> "$STUB_DIR/activity"
  exit "${STUB_ACTIVITY_EXIT:-0}"
fi
echo "$SOCK_TOKEN" >> "$STUB_DIR/sock-token"
if [ "$1 $2" != "whoami --json" ]; then exit 64; fi
case "$STUB_WHOAMI" in
fail) echo "boom token=$SOCK_TOKEN" >&2; exit 1 ;;
nopuppet) printf '{"account":{"id":"%s"}}\n' "$STUB_OWNER" ;;
*) printf '{"account":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","puppet":{"ownerId":"%s","name":"x"}}}\n' "$STUB_OWNER" ;;
esac
`

// rnMessage renders a protojson-like Message with random whitespace between tokens.
func rnMessage(fields map[string]any) string {
	ws := func() string {
		return [...]string{"", " ", "\n", "  ", "\t", "\n  "}[rand.IntN(6)]
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rand.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	var b strings.Builder
	b.WriteString("{" + ws())
	for i, k := range keys {
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(fields[k])
		if i > 0 {
			b.WriteString(ws() + "," + ws())
		}
		b.Write(kb)
		b.WriteString(ws() + ":" + ws())
		b.Write(vb)
	}
	b.WriteString(ws() + "}" + ws())
	return b.String()
}

// rnHarness is a temp world: stub binaries first on PATH, recording dir, workdir, state dir.
type rnHarness struct {
	t     *testing.T
	bin   string // runner under test
	dir   string
	stubs string
	rec   string
	work  string
	state string
	env   map[string]string
}

func rnRunnerPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("SOCK_RUNNER_BIN")
	if p == "" {
		p = filepath.Join(root, "runner", "sock-runner")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("runner not present at %s (set SOCK_RUNNER_BIN): skipping conformance test", p)
	}
	return p
}

func newRunnerHarness(t *testing.T) *rnHarness {
	t.Helper()
	bin := rnRunnerPath(t)
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
	for name, body := range map[string]string{"sock": rnSockStub, "claude": rnAgentStub, "codex": rnAgentStub} {
		writeFakeExec(t, filepath.Join(h.stubs, name), body)
	}
	h.env = map[string]string{
		"PATH":                   h.stubs + ":" + rnPathTail,
		"HOME":                   filepath.Join(dir, "home"),
		"TMPDIR":                 dir,
		"SOCK_TOKEN":             rnTokenSentinel,
		"SOCK_URL":               rnURLSentinel,
		"SOCK_MESSAGE_ID":        rnMsgID,
		"SOCK_CONVERSATION_ID":   rnConvID,
		"SOCK_SENDER_ID":         rnOwner,
		"SOCK_RUNNER_AGENT":      "claude",
		"SOCK_RUNNER_SESSION":    rnSess,
		"SOCK_RUNNER_WORKDIR":    h.work,
		"SOCK_RUNNER_PERMISSION": "default",
		"SOCK_RUNNER_STATE":      h.state,
		"SOCK_RUNNER_TIMEOUT":    "20",
		"STUB_DIR":               h.rec,
		"STUB_OWNER":             rnOwner,
	}
	return h
}

// fields is the default owner message.
func (h *rnHarness) fields() map[string]any {
	return map[string]any{
		"id":             rnMsgID,
		"conversationId": rnConvID,
		"seq":            "42",
		"senderId":       rnOwner,
		"sentAt":         "2026-10-13T12:00:00Z",
		"text":           rnText,
		"evidenceTag":    "AAEC",
	}
}

type rnOpts struct {
	Env   map[string]string // overrides
	Unset []string
	Stdin string // raw stdin; default is the owner message
}

type rnRes struct {
	Code           int
	Elapsed        time.Duration
	Stdout, Stderr string
}

// command builds the runner invocation without starting it. Stdout and
// stderr are collected in buffers reachable through the Cmd.
func (h *rnHarness) command(o rnOpts) *exec.Cmd {
	h.t.Helper()
	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	for k, v := range o.Env {
		env[k] = v
	}
	for _, k := range o.Unset {
		delete(env, k)
	}
	in := o.Stdin
	if in == "" {
		in = rnMessage(h.fields())
	}
	cmd := testCmd(h.t, h.bin)
	cmd.Dir = h.dir
	cmd.Env = make([]string, 0, len(env))
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = strings.NewReader(in)
	cmd.Stdout, cmd.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	return cmd
}

func (h *rnHarness) run(o rnOpts) rnRes {
	h.t.Helper()
	cmd := h.command(o)
	start := time.Now()
	err := runCmd(cmd)
	so, se := cmd.Stdout.(*bytes.Buffer), cmd.Stderr.(*bytes.Buffer)
	r := rnRes{Elapsed: time.Since(start), Stdout: so.String(), Stderr: se.String()}
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		r.Code = ee.ExitCode()
	} else if err != nil {
		h.t.Errorf("run %s: %v", h.bin, err)
		r.Code = -1
	}
	return r
}

type rnRun struct {
	Agent, Pwd, Stdin, Env string
	Argv                   []string
	Dir                    string
}

func (h *rnHarness) runs() []rnRun {
	h.t.Helper()
	ents, err := os.ReadDir(h.rec)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []rnRun
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), "run.") {
			continue
		}
		d := filepath.Join(h.rec, e.Name())
		rd := func(n string) string {
			b, _ := os.ReadFile(filepath.Join(d, n))
			return string(b)
		}
		r := rnRun{Dir: d, Agent: strings.TrimSpace(rd("agent")), Pwd: strings.TrimSpace(rd("pwd")), Stdin: rd("stdin"), Env: rd("env")}
		if a := strings.TrimSuffix(rd("argv"), "\n"); a != "" {
			r.Argv = strings.Split(a, "\n")
		}
		out = append(out, r)
	}
	return out
}

func (h *rnHarness) events() []string {
	b, _ := os.ReadFile(filepath.Join(h.rec, "events"))
	return strings.Fields(string(b))
}

func (h *rnHarness) log() string {
	b, _ := os.ReadFile(filepath.Join(h.state, "runner.log"))
	return string(b)
}

func (h *rnHarness) removeStub(name string) {
	h.t.Helper()
	if err := os.Remove(filepath.Join(h.stubs, name)); err != nil {
		h.t.Fatal(err)
	}
}

// envHas reports whether the recorded env has a variable named name.
func (r rnRun) envHas(name string) bool {
	for _, l := range strings.Split(r.Env, "\n") {
		if strings.HasPrefix(l, name+"=") {
			return true
		}
	}
	return false
}

// envNames lists the recorded variable names.
func (r rnRun) envNames() []string {
	var out []string
	for _, l := range strings.Split(r.Env, "\n") {
		if k, _, ok := strings.Cut(l, "="); ok {
			out = append(out, k)
		}
	}
	return out
}
