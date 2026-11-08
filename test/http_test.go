package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	htKeySentinel = "HTTPKEYSENTINEL-4b9e61"
	htTemplate    = `{"role":"user","run":true,"content":[{"type":"text","text":"{{prompt}}"}],"meta":{"id":"{{id}}","conv":"{{conversationId}}","at":"{{sentAt}}","raw":"{{text}}"}}`
)

// htText holds a quote, newlines, shell metacharacters and the placeholders
// themselves: none may leave the body's string, and none may be substituted twice.
const htText = rnTextSentinel + " \"q\" 'sq'\nline2 $(touch PWNED) `id` \\ {{id}} {{text}} </sock-message> end"

// htCurlStub records curl's argv and environment and the config it was handed
// (the command removes it on exit), then runs the real curl.
func htCurlStub(real string) string {
	return `#!/bin/sh
n=$(ls "$STUB_DIR" | grep -c '^curl\.')
d="$STUB_DIR/curl.$n"; mkdir "$d"
printf '%s\n' "$@" > "$d/argv"
env > "$d/env"
prev=
for a in "$@"; do
  [ "$prev" = "--config" ] && cp "$a" "$d/config"
  [ "$prev" = "--data-binary" ] && cp "${a#@}" "$d/body"
  prev=$a
done
exec ` + real + ` "$@"
`
}

type htReq struct {
	Method string
	Path   string
	Header http.Header
	Body   string
}

type htServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []htReq
	// handler decides the answer; nil answers 200
	handler func(w http.ResponseWriter, r *http.Request)
}

func newHTTPServer(t *testing.T) *htServer {
	s := &htServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, htReq{r.Method, r.URL.RequestURI(), r.Header.Clone(), string(b)})
		h := s.handler
		s.mu.Unlock()
		if h != nil {
			h(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *htServer) requests() []htReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]htReq(nil), s.reqs...)
}

func (s *htServer) answer(f func(w http.ResponseWriter, r *http.Request)) {
	s.mu.Lock()
	s.handler = f
	s.mu.Unlock()
}

func status(code int, hdr ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(hdr); i += 2 {
			w.Header().Set(hdr[i], hdr[i+1])
		}
		w.WriteHeader(code)
	}
}

type htHarness struct {
	*rnHarness
	api      *htServer
	template string
}

func newHTTPHarness(t *testing.T) *htHarness {
	t.Helper()
	bin := filepath.Join(root, "runner", "sock-http")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("sock-http missing at %s", bin)
	}
	realCurl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl not on PATH")
	}
	t.Parallel()
	dir := t.TempDir()
	h := &rnHarness{t: t, bin: bin, dir: dir}
	h.stubs = filepath.Join(dir, "bin")
	h.rec = filepath.Join(dir, "rec")
	h.state = filepath.Join(dir, "state")
	for _, d := range []string{h.stubs, h.rec} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFakeExec(t, filepath.Join(h.stubs, "sock"), rnSockStub)
	writeFakeExec(t, filepath.Join(h.stubs, "curl"), htCurlStub(realCurl))
	api := newHTTPServer(t)
	tpl := filepath.Join(dir, "body.json")
	if err := os.WriteFile(tpl, []byte(htTemplate), 0o600); err != nil {
		t.Fatal(err)
	}
	h.env = map[string]string{
		"PATH":                 h.stubs + ":" + rnPathTail,
		"HOME":                 filepath.Join(dir, "home"),
		"TMPDIR":               dir,
		"SOCK_TOKEN":           rnTokenSentinel,
		"SOCK_URL":             rnURLSentinel,
		"SOCK_MESSAGE_ID":      rnMsgID,
		"SOCK_CONVERSATION_ID": rnConvID,
		"SOCK_SENDER_ID":       rnOwner,
		"SOCK_HTTP_URL":        api.srv.URL + "/api/conversations/c1/events",
		"SOCK_HTTP_HEADERS":    "X-Session-API-Key: " + htKeySentinel,
		"SOCK_HTTP_BODY":       tpl,
		"SOCK_HTTP_STATE":      h.state,
		"SOCK_HTTP_TIMEOUT":    "10",
		"STUB_DIR":             h.rec,
		"STUB_OWNER":           rnOwner,
	}
	return &htHarness{rnHarness: h, api: api, template: tpl}
}

func (h *htHarness) fields() map[string]any {
	f := h.rnHarness.fields()
	f["text"] = htText
	return f
}

func (h *htHarness) send(o rnOpts) rnRes {
	h.t.Helper()
	if o.Stdin == "" {
		o.Stdin = rnMessage(h.fields())
	}
	return h.run(o)
}

func (h *htHarness) readRec(path ...string) string {
	b, _ := os.ReadFile(filepath.Join(append([]string{h.rec}, path...)...))
	return string(b)
}

func (h *htHarness) httpLog() string {
	b, _ := os.ReadFile(filepath.Join(h.state, "http.log"))
	return string(b)
}

func (h *htHarness) ledger() string {
	b, _ := os.ReadFile(filepath.Join(h.state, "done.ids"))
	return string(b)
}

// noLeak: the text, the headers' key and the token appear in no log, output,
// argv or environment of the command or of curl.
func (h *htHarness) noLeak(r rnRes) {
	h.t.Helper()
	for name, s := range map[string]string{"stdout": r.Stdout, "stderr": r.Stderr, "log": h.httpLog()} {
		for _, bad := range []string{rnTextSentinel, htKeySentinel, rnTokenSentinel} {
			if strings.Contains(s, bad) {
				h.t.Errorf("%s leaks %q: %q", name, bad, s)
			}
		}
	}
	ents, _ := os.ReadDir(h.rec)
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), "curl.") {
			continue
		}
		for _, f := range []string{"argv", "env"} {
			s := h.readRec(e.Name(), f)
			for _, bad := range []string{rnTextSentinel, htKeySentinel} {
				if strings.Contains(s, bad) {
					h.t.Errorf("curl %s leaks %q", f, bad)
				}
			}
		}
	}
}

func TestHTTPForwardsTheOwnersMessageOnce(t *testing.T) {
	h := newHTTPHarness(t)
	r := h.send(rnOpts{})
	if r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	reqs := h.api.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	q := reqs[0]
	if q.Method != "POST" || q.Path != "/api/conversations/c1/events" {
		t.Fatalf("%s %s", q.Method, q.Path)
	}
	if q.Header.Get("X-Session-API-Key") != htKeySentinel || q.Header.Get("Idempotency-Key") != rnMsgID ||
		q.Header.Get("Content-Type") != "application/json" || q.Header.Get("User-Agent") != "sock-http" {
		t.Fatalf("headers %v", q.Header)
	}
	var body struct {
		Role    string
		Run     bool
		Content []struct{ Type, Text string }
		Meta    struct{ ID, Conv, At, Raw string }
	}
	if err := json.Unmarshal([]byte(q.Body), &body); err != nil {
		t.Fatalf("body %q: %v", q.Body, err)
	}
	tag := `<sock-message from="owner" conversation="` + rnConvID + `" id="` + rnMsgID + `">`
	if body.Role != "user" || !body.Run || len(body.Content) != 1 || !strings.Contains(body.Content[0].Text, tag+"\n"+rnTextSentinel) {
		t.Fatalf("body %+v", body)
	}
	// the text arrives verbatim, placeholders in it untouched
	if body.Meta.Raw != htText || body.Meta.ID != rnMsgID || body.Meta.Conv != rnConvID || body.Meta.At != "2026-10-13T12:00:00Z" {
		t.Fatalf("meta %+v", body.Meta)
	}
	// a closing tag in the text cannot end the tag the agent reads
	if strings.Count(body.Content[0].Text, "</sock-message>") != 1 {
		t.Fatalf("prompt %q", body.Content[0].Text)
	}
	if !strings.Contains(h.ledger(), rnMsgID) || !strings.Contains(h.httpLog(), "forwarded") {
		t.Fatalf("ledger %q log %q", h.ledger(), h.httpLog())
	}
	h.noLeak(r)
}

func TestHTTPKeepsTheHeadersInTheCurlConfigOnly(t *testing.T) {
	h := newHTTPHarness(t)
	if r := h.send(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	cfg := h.readRec("curl.0", "config")
	if !strings.Contains(cfg, "X-Session-API-Key: "+htKeySentinel) || !strings.Contains(cfg, "Idempotency-Key: "+rnMsgID) {
		t.Fatalf("config %q", cfg)
	}
	if strings.Contains(h.readRec("curl.0", "argv"), "--location") || strings.Contains(h.readRec("curl.0", "argv"), " -L") {
		t.Fatal("redirects are followed")
	}
}

func TestHTTPAHeaderContentTypeIsNotDuplicated(t *testing.T) {
	h := newHTTPHarness(t)
	h.env["SOCK_HTTP_HEADERS"] = "content-type: application/vnd.x+json\nX-A: 1"
	if r := h.send(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	if got := h.api.requests()[0].Header.Values("Content-Type"); len(got) != 1 || got[0] != "application/vnd.x+json" {
		t.Fatalf("content-type %v", got)
	}
}

func TestHTTPMethodPUT(t *testing.T) {
	h := newHTTPHarness(t)
	h.env["SOCK_HTTP_METHOD"] = "PUT"
	if r := h.send(rnOpts{}); r.Code != 0 || h.api.requests()[0].Method != "PUT" {
		t.Fatalf("exit %d", r.Code)
	}
}

func TestHTTPNotTheOwnerIsAckedWithNoRequest(t *testing.T) {
	h := newHTTPHarness(t)
	f := h.fields()
	f["senderId"] = rnOther
	r := h.run(rnOpts{Stdin: rnMessage(f), Env: map[string]string{"SOCK_SENDER_ID": rnOther}})
	if r.Code != 0 || len(h.api.requests()) != 0 || !strings.Contains(h.httpLog(), "dropped") {
		t.Fatalf("exit %d, %d requests, log %q", r.Code, len(h.api.requests()), h.httpLog())
	}
	// stdin and environment must name one sender
	r = h.run(rnOpts{Stdin: rnMessage(f)})
	if r.Code != 0 || len(h.api.requests()) != 0 {
		t.Fatalf("mismatch: exit %d, %d requests", r.Code, len(h.api.requests()))
	}
}

func TestHTTPAPuppetOfTheOwnerIsForwardedAndNamed(t *testing.T) {
	h := newHTTPHarness(t)
	f := h.fields()
	f["senderId"] = rnOther
	env := map[string]string{"SOCK_SENDER_ID": rnOther, "STUB_PUPPETS": rnOther, "STUB_PUPPET_NAME": `Andy <the> pod!`}
	// without the setting the owner's puppet is dropped before any lookup
	r := h.run(rnOpts{Stdin: rnMessage(f), Env: env})
	if r.Code != 0 || len(h.api.requests()) != 0 || h.readRec("accounts-get") != "" || !strings.Contains(h.httpLog(), "dropped: sender "+rnOther) {
		t.Fatalf("without SOCK_ACCEPT_PUPPETS: exit %d, %d requests, lookups %q, log %q", r.Code, len(h.api.requests()), h.readRec("accounts-get"), h.httpLog())
	}
	env["SOCK_ACCEPT_PUPPETS"] = "1"
	r = h.run(rnOpts{Stdin: rnMessage(f), Env: env})
	if r.Code != 0 || len(h.api.requests()) != 1 {
		t.Fatalf("exit %d, %d requests, log %q", r.Code, len(h.api.requests()), h.httpLog())
	}
	var body struct {
		Content []struct{ Text string }
	}
	if err := json.Unmarshal([]byte(h.api.requests()[0].Body), &body); err != nil || len(body.Content) != 1 {
		t.Fatalf("body %q: %v", h.api.requests()[0].Body, err)
	}
	// the name reaches the tag with only letters, digits, space, . _ and -
	tag := `<sock-message from="puppet" name="Andy the pod" conversation="` + rnConvID + `" id="` + rnMsgID + `">`
	if !strings.Contains(body.Content[0].Text, tag+"\n"+rnTextSentinel) || !strings.HasPrefix(body.Content[0].Text, "Andy the pod, one of your owner's puppets") {
		t.Fatalf("prompt %q", body.Content[0].Text)
	}
	// the account is cached: a second message from the same puppet makes no second lookup
	r = h.run(rnOpts{Stdin: rnMessage(f), Env: env})
	lookups := strings.Count(h.readRec("accounts-get"), rnOther)
	if r.Code != 0 || lookups != 1 {
		t.Fatalf("exit %d, %d lookups", r.Code, lookups)
	}
	h.noLeak(r)
}

func TestHTTPAnAccountLookupThatFailsLeavesTheMessagePending(t *testing.T) {
	h := newHTTPHarness(t)
	f := h.fields()
	f["senderId"] = rnOther
	r := h.run(rnOpts{Stdin: rnMessage(f), Env: map[string]string{"SOCK_SENDER_ID": rnOther, "SOCK_ACCEPT_PUPPETS": "1", "STUB_ACCOUNTS": "fail"}})
	if r.Code != 1 || len(h.api.requests()) != 0 || !strings.Contains(h.httpLog(), "account lookup failed") {
		t.Fatalf("exit %d, %d requests, log %q", r.Code, len(h.api.requests()), h.httpLog())
	}
}

// An id the hub does not know, or a human, is dropped and acked, and the verdict is cached.
func TestHTTPAStrangerIsDroppedAfterOneLookup(t *testing.T) {
	for name, mode := range map[string]string{"unknown id": "none", "a human": ""} {
		t.Run(name, func(t *testing.T) {
			h := newHTTPHarness(t)
			f := h.fields()
			f["senderId"] = rnOther
			env := map[string]string{"SOCK_SENDER_ID": rnOther, "SOCK_ACCEPT_PUPPETS": "1", "STUB_ACCOUNTS": mode}
			h.run(rnOpts{Stdin: rnMessage(f), Env: env})
			r := h.run(rnOpts{Stdin: rnMessage(f), Env: env})
			if r.Code != 0 || len(h.api.requests()) != 0 || !strings.Contains(h.httpLog(), "dropped: sender "+rnOther) || strings.Count(h.readRec("accounts-get"), rnOther) != 1 {
				t.Fatalf("exit %d, %d requests, log %q, lookups %q", r.Code, len(h.api.requests()), h.httpLog(), h.readRec("accounts-get"))
			}
		})
	}
}

func TestHTTPAHumanTokenExits2WithNoRequest(t *testing.T) {
	h := newHTTPHarness(t)
	r := h.send(rnOpts{Env: map[string]string{"STUB_WHOAMI": "nopuppet"}})
	if r.Code != 2 || len(h.api.requests()) != 0 {
		t.Fatalf("exit %d, %d requests", r.Code, len(h.api.requests()))
	}
}

func TestHTTPARepeatedIdIsAckedWithoutASecondRequest(t *testing.T) {
	h := newHTTPHarness(t)
	for i := range 2 {
		if r := h.send(rnOpts{}); r.Code != 0 {
			t.Fatalf("run %d exit %d: %q", i, r.Code, r.Stderr)
		}
	}
	if n := len(h.api.requests()); n != 1 {
		t.Fatalf("%d requests", n)
	}
	if !strings.Contains(h.httpLog(), "duplicate") {
		t.Fatalf("log %q", h.httpLog())
	}
}

func TestHTTPAnythingButA2xxLeavesTheMessagePending(t *testing.T) {
	for _, code := range []int{301, 302, 400, 401, 404, 429, 500, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			h := newHTTPHarness(t)
			h.api.answer(status(code, "Location", "/elsewhere"))
			r := h.send(rnOpts{})
			if r.Code != 1 || h.ledger() != "" {
				t.Fatalf("exit %d ledger %q", r.Code, h.ledger())
			}
			if !strings.Contains(h.httpLog(), "http ") {
				t.Fatalf("log %q", h.httpLog())
			}
			h.noLeak(r)
		})
	}
}

func TestHTTPNoRedirectIsFollowed(t *testing.T) {
	other := newHTTPServer(t)
	h := newHTTPHarness(t)
	h.api.answer(status(307, "Location", other.srv.URL+"/stolen"))
	if r := h.send(rnOpts{}); r.Code != 1 || len(other.requests()) != 0 {
		t.Fatalf("exit %d, %d requests to the redirect target", r.Code, len(other.requests()))
	}
}

func TestHTTPNoConnectionLeavesTheMessagePending(t *testing.T) {
	h := newHTTPHarness(t)
	h.api.srv.Close()
	if r := h.send(rnOpts{}); r.Code != 1 || h.ledger() != "" || !strings.Contains(h.httpLog(), "no response") {
		t.Fatalf("exit %d ledger %q log %q", r.Code, h.ledger(), h.httpLog())
	}
}

func TestHTTPATimeoutLeavesTheMessagePending(t *testing.T) {
	h := newHTTPHarness(t)
	h.api.answer(func(w http.ResponseWriter, r *http.Request) { time.Sleep(4 * time.Second) })
	h.env["SOCK_HTTP_TIMEOUT"] = "1"
	r := h.send(rnOpts{})
	if r.Code != 1 || r.Elapsed > 3500*time.Millisecond || h.ledger() != "" {
		t.Fatalf("exit %d after %v ledger %q", r.Code, r.Elapsed, h.ledger())
	}
}

func TestHTTPRetryAfterIsHonoured(t *testing.T) {
	when := func() string { return time.Now().UTC().Add(3 * time.Second).Format(http.TimeFormat) }
	for name, ra := range map[string]func() string{"delta": func() string { return "2" }, "date": when} {
		t.Run(name, func(t *testing.T) {
			h := newHTTPHarness(t)
			h.api.answer(status(429, "Retry-After", ra()))
			r := h.send(rnOpts{})
			if r.Code != 1 || r.Elapsed < 1500*time.Millisecond || r.Elapsed > 8*time.Second {
				t.Fatalf("exit %d after %v", r.Code, r.Elapsed)
			}
		})
	}
	// a past date, garbage and no header wait for nothing
	for name, ra := range map[string]string{"past": time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat), "garbage": "soon", "none": ""} {
		t.Run(name, func(t *testing.T) {
			h := newHTTPHarness(t)
			h.api.answer(status(503, "Retry-After", ra))
			if r := h.send(rnOpts{}); r.Code != 1 || r.Elapsed > 1500*time.Millisecond {
				t.Fatalf("exit %d after %v", r.Code, r.Elapsed)
			}
		})
	}
}

func TestHTTPBadConfigurationExits2BeforeAnyRequest(t *testing.T) {
	bad := t.TempDir()
	notJSON := filepath.Join(bad, "not.json")
	inKey := filepath.Join(bad, "key.json")
	for p, c := range map[string]string{notJSON: `{"text": `, inKey: `{"{{text}}": "x"}`} {
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]map[string]string{
		"no url":          {"SOCK_HTTP_URL": ""},
		"plain http":      {"SOCK_HTTP_URL": "http://example.com/x"},
		"userinfo":        {"SOCK_HTTP_URL": "https://user:pw@example.com/x"},
		"userinfo digits": {"SOCK_HTTP_URL": "https://user:123@example.com/x"},
		"other scheme":    {"SOCK_HTTP_URL": "ftp://example.com/x"},
		"quote in url":    {"SOCK_HTTP_URL": `https://example.com/x"`},
		"long url":        {"SOCK_HTTP_URL": "https://example.com/" + strings.Repeat("a", 2100)},
		"port range":      {"SOCK_HTTP_URL": "https://example.com:99999/x"},
		"empty host":      {"SOCK_HTTP_URL": "https://-/x"},
		"dots host":       {"SOCK_HTTP_URL": "https://../x"},
		"bad method":      {"SOCK_HTTP_METHOD": "DELETE"},
		"quote header":    {"SOCK_HTTP_HEADERS": `X-A: a"b`},
		"slash header":    {"SOCK_HTTP_HEADERS": `X-A: a\b`},
		"header name":     {"SOCK_HTTP_HEADERS": "X A: b"},
		"no colon":        {"SOCK_HTTP_HEADERS": "nocolon"},
		"control header":  {"SOCK_HTTP_HEADERS": "X-A: a\rb"},
		"no template":     {"SOCK_HTTP_BODY": ""},
		"missing file":    {"SOCK_HTTP_BODY": filepath.Join(bad, "nope.json")},
		"template json":   {"SOCK_HTTP_BODY": notJSON},
		"template key":    {"SOCK_HTTP_BODY": inKey},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHTTPHarness(t)
			r := h.send(rnOpts{Env: env})
			if r.Code != 2 || len(h.api.requests()) != 0 {
				t.Fatalf("exit %d, %d requests, stderr %q", r.Code, len(h.api.requests()), r.Stderr)
			}
		})
	}
}

func TestHTTPLoopbackHTTPIsAccepted(t *testing.T) {
	h := newHTTPHarness(t) // the fake server is http://127.0.0.1
	if !strings.HasPrefix(h.env["SOCK_HTTP_URL"], "http://127.0.0.1") {
		t.Skip("test server is not on 127.0.0.1")
	}
	if r := h.send(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
}

func TestHTTPHeadersLeaveTheEnvironmentBeforeCurlRuns(t *testing.T) {
	h := newHTTPHarness(t)
	if r := h.send(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d: %q", r.Code, r.Stderr)
	}
	if strings.Contains(h.readRec("curl.0", "env"), "SOCK_HTTP_HEADERS") {
		t.Fatal("curl inherited SOCK_HTTP_HEADERS")
	}
}

func TestHTTPLoopbackIgnoresTheProxyEnvironment(t *testing.T) {
	proxy := newHTTPServer(t)
	h := newHTTPHarness(t)
	h.env["http_proxy"] = proxy.srv.URL
	h.env["HTTP_PROXY"] = proxy.srv.URL
	h.env["ALL_PROXY"] = proxy.srv.URL
	if r := h.send(rnOpts{}); r.Code != 0 || len(h.api.requests()) != 1 || len(proxy.requests()) != 0 {
		t.Fatalf("exit %d, %d requests to the endpoint, %d to the proxy", r.Code, len(h.api.requests()), len(proxy.requests()))
	}
}
