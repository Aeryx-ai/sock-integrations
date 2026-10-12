package test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	sockv1 "github.com/guygrigsby/sock-contract/gen/go/sock/v1"
	"github.com/guygrigsby/sock-contract/gen/go/sock/v1/sockv1connect"
)

const token = "sock_pt_testdevice_testsecret"

type rec struct{ Auth, Text string }

// stub is a loopback NotifyOwner. fail(n) returns the error for call n (1-based),
// nil to succeed. block, when set, holds every call open until closed.
type stub struct {
	sockv1connect.UnimplementedMessagingServiceHandler
	URL string

	mu    sync.Mutex
	fail  func(n int) error
	block chan struct{}
	reqs  []rec
}

// setFail and setBlock configure the stub under the lock so the handler
// goroutine reads them race-free.
func (s *stub) setFail(f func(n int) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = f
}

func (s *stub) setBlock(c chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.block = c
}

func newStub(t *testing.T) *stub {
	t.Helper()
	s := &stub{}
	mux := http.NewServeMux()
	path, h := sockv1connect.NewMessagingServiceHandler(s)
	mux.Handle(path, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

func (s *stub) NotifyOwner(ctx context.Context, req *connect.Request[sockv1.NotifyOwnerRequest]) (*connect.Response[sockv1.NotifyOwnerResponse], error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, rec{req.Header().Get("Authorization"), req.Msg.GetText()})
	n := len(s.reqs)
	fail, block := s.fail, s.block
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	if fail != nil {
		if err := fail(n); err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&sockv1.NotifyOwnerResponse{Message: &sockv1.Message{Id: "m1"}}), nil
}

func (s *stub) Requests() []rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rec(nil), s.reqs...)
}

func tokenInvalid() error {
	ce := connect.NewError(connect.CodeUnauthenticated, errors.New("token rejected"))
	d, err := connect.NewErrorDetail(&sockv1.ErrorInfo{Reason: sockv1.ErrorReason_ERROR_REASON_TOKEN_INVALID})
	if err != nil {
		panic(err)
	}
	ce.AddDetail(d)
	return ce
}

func unavailable() error { return connect.NewError(connect.CodeUnavailable, errors.New("down")) }

// root is the integrations repo root (test/ is one level down), captured at
// init so tests that t.Chdir still resolve it.
var root = func() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Dir(wd)
}()

func repoRoot(t *testing.T) string {
	t.Helper()
	return root
}

func sockBin(t *testing.T) string {
	t.Helper()
	p := os.Getenv("SOCK_BIN")
	if p == "" {
		p = filepath.Join(repoRoot(t), "..", "cli", "bin", "sock")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("sock binary missing at %s: run `make deps` (or set SOCK_BIN)", p)
	}
	return p
}

type result struct {
	Code    int
	Elapsed time.Duration
	Log     string
}

type hookEnv struct {
	Env       map[string]string // extra or overriding env
	Stdin     string
	StdinFile *os.File // used instead of Stdin when set
	HomeRO    bool     // make HOME and XDG_STATE_HOME unwritable
}

// runHook runs the real notify.sh with a clean environment. The real sock
// binary is on PATH unless env.Env["PATH"] overrides it.
func runHook(t *testing.T, stubURL string, he hookEnv, args ...string) result {
	t.Helper()
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sockBin(t), filepath.Join(binDir, "sock")); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(tmp, "state")
	env := map[string]string{
		"PATH":              binDir + ":/usr/bin:/bin",
		"HOME":              filepath.Join(tmp, "home"),
		"XDG_STATE_HOME":    state,
		"TMPDIR":            tmp,
		"SOCK_URL":          stubURL,
		"SOCK_TOKEN":        token,
		"SOCK_HOOK_TIMEOUT": "2",
	}
	for k, v := range he.Env {
		env[k] = v
	}
	if he.HomeRO {
		_ = os.MkdirAll(state, 0o755)
		_ = os.Chmod(state, 0o500)
		t.Cleanup(func() { _ = os.Chmod(state, 0o700) })
	}
	cmd := exec.Command(filepath.Join(repoRoot(t), "claude-code", "hooks", "notify.sh"), args...)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if he.StdinFile != nil {
		cmd.Stdin = he.StdinFile
	} else if he.Stdin != "" {
		cmd.Stdin = strings.NewReader(he.Stdin)
	}
	start := time.Now()
	err := cmd.Run()
	r := result{Elapsed: time.Since(start)}
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		r.Code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run notify.sh: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(state, "sock-integrations", "hook.log")); err == nil {
		r.Log = string(b)
	}
	return r
}
