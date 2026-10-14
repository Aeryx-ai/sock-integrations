package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A session per conversation (docs/specs/integrations.md, "Follow-up: a
// session per conversation (integrations-kzk)").

const (
	bnAgent   = "agent_011CZkYpogX7uDKUyvBTophP"
	bnEnv     = "env_011CZkZ9X2dpNyB7HsEFoRfW"
	bnPuppet  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc" // the stub's account.id
	bnConv2   = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	bnGoneMsg = "the agent's earlier session ended; this reply started a new one"
)

// newConvHarness is a bridge that creates sessions from an agent and an
// environment instead of using SOCK_BRIDGE_SESSION.
func newConvHarness(t *testing.T) *bnHarness {
	t.Helper()
	h := newBridgeHarness(t)
	delete(h.env, "SOCK_BRIDGE_SESSION")
	h.env["SOCK_BRIDGE_AGENT"] = bnAgent
	h.env["SOCK_BRIDGE_ENVIRONMENT"] = bnEnv
	return h
}

// maSession is a stored session of the bridge's agent for that conversation
// and puppet.
func maSession(id, conv, puppet string) map[string]any {
	return map[string]any{"type": "session", "id": id, "status": "idle", "archived_at": nil,
		"metadata":       map[string]any{"bridge_conversation": conv, "bridge_puppet": puppet},
		"agent":          map[string]any{"type": "agent", "id": bnAgent, "version": float64(1)},
		"environment_id": bnEnv, "budget": nil}
}

// sessionsFile is the map as "<conversation> <session>" lines, without the
// config hash each line carries; sessionsRaw is the file as it is.
func (h *bnHarness) sessionsFile() string {
	var out strings.Builder
	for _, l := range strings.SplitAfter(h.sessionsRaw(), "\n") {
		if f := strings.Fields(l); len(f) >= 2 {
			out.WriteString(f[0] + " " + f[1] + "\n")
		} else {
			out.WriteString(l)
		}
	}
	return out.String()
}

func (h *bnHarness) sessionsRaw() string {
	b, _ := os.ReadFile(filepath.Join(h.state, "sessions"))
	return string(b)
}

func (h *bnHarness) writeSessions(body string) {
	h.t.Helper()
	if err := os.MkdirAll(h.state, 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.state, "sessions"), []byte(body), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// inConv is a message of another id in conversation conv.
func (h *bnHarness) inConv(n int, conv string) rnOpts {
	o := h.withID(n, rnOpts{})
	o.Env["SOCK_CONVERSATION_ID"] = conv
	f := h.fields()
	f["id"] = o.Env["SOCK_MESSAGE_ID"]
	f["conversationId"] = conv
	o.Stdin = rnMessage(f)
	return o
}

func (f *fakeMA) createBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.creates...)
}

// sessionPaths are the sessions the events requests (stream, send, list) went to.
func (f *fakeMA) eventSessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.paths {
		rest, ok := strings.CutPrefix(p, "/v1/sessions/")
		if !ok {
			continue
		}
		id, sub, _ := strings.Cut(rest, "/")
		if strings.HasPrefix(sub, "events") {
			out = append(out, id)
		}
	}
	return out
}

func TestBridgeConversationFirstUseCreatesAndRecords(t *testing.T) {
	h := newConvHarness(t)
	h.env["SOCK_BRIDGE_AGENT_VERSION"] = "3"
	h.env["SOCK_BRIDGE_BUDGET"] = "125"
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 {
		t.Fatalf("exit %d: %q log %q", r.Code, r.Stderr, h.bridgeLog())
	}
	if k := h.api.kinds(); !slices.Equal(k, []string{"find", "create", "stream", "send"}) {
		t.Fatalf("requests %v, want find, create, stream, send", k)
	}
	for i, hd := range h.api.headers[:2] {
		if hd.Get("X-Api-Key") != bnKey || hd.Get("Anthropic-Beta") != "managed-agents-2026-04-01" || hd.Get("User-Agent") != "sock-bridge" {
			t.Fatalf("request %d headers %v", i, hd)
		}
	}
	find := h.api.paths[0]
	for _, want := range []string{"agent_id=" + bnAgent, "order=desc", "limit=100", "beta=true"} {
		if !strings.Contains(find, want) {
			t.Fatalf("find %q lacks %q", find, want)
		}
	}
	bodies := h.api.createBodies()
	if len(bodies) != 1 {
		t.Fatalf("%d creates", len(bodies))
	}
	var body map[string]any
	mustJSON(t, []byte(bodies[0]), &body)
	want := map[string]any{
		"agent":          map[string]any{"type": "agent", "id": bnAgent, "version": float64(3)},
		"environment_id": bnEnv,
		"title":          "sock " + rnConvID[:8],
		"metadata":       map[string]any{"bridge_conversation": rnConvID, "bridge_puppet": bnPuppet},
		"budget":         map[string]any{"type": "limit", "max_list_cost": map[string]any{"amount": "125", "currency": "USD"}},
	}
	gotJ, _ := json.Marshal(body)
	wantJ, _ := json.Marshal(want)
	if string(gotJ) != string(wantJ) {
		t.Fatalf("create body\n %s\nwant\n %s", gotJ, wantJ)
	}
	if got := h.sessionsFile(); got != rnConvID+" sesn_NEW1\n" {
		t.Fatalf("sessions %q", got)
	}
	st, err := os.Stat(filepath.Join(h.state, "sessions"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("sessions mode %v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(h.state, "sessions.tmp")); err == nil {
		t.Fatal("sessions.tmp left behind")
	}
	if s := h.api.eventSessions(); !slices.Equal(s, []string{"sesn_NEW1", "sesn_NEW1"}) {
		t.Fatalf("events went to %v", s)
	}
	if s := h.sends(); len(s) != 1 || !strings.Contains(s[0].Text, bnAnswer) {
		t.Fatalf("sends %v", s)
	}
	if lg := h.bridgeLog(); !strings.Contains(lg, "session created") || !strings.Contains(lg, "ran") {
		t.Fatalf("log %q", lg)
	}
	h.noKeyLeak(r)
	h.noLeak(r)
}

func TestBridgeConversationWithoutVersionOrBudget(t *testing.T) {
	h := newConvHarness(t)
	h.script(idle("end_turn"))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	var body map[string]any
	mustJSON(t, []byte(h.api.createBodies()[0]), &body)
	if body["agent"] != bnAgent {
		t.Fatalf("agent %v, want the id string for the latest version", body["agent"])
	}
	for _, k := range []string{"budget", "initial_events"} {
		if _, ok := body[k]; ok {
			t.Fatalf("create has %s: %v", k, body)
		}
	}
}

func TestBridgeConversationReusesItsSession(t *testing.T) {
	h := newConvHarness(t)
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	h.script(agentMsg("SECOND-answer"), idle("end_turn"))
	h.script(agentMsg("THIRD-answer"), idle("end_turn"))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("first: exit %d", r.Code)
	}
	r := h.run(h.inConv(2, rnConvID))
	if r.Code != 0 {
		t.Fatalf("second: exit %d log %q", r.Code, h.bridgeLog())
	}
	if n := len(h.api.createBodies()); n != 1 {
		t.Fatalf("%d creates for one conversation", n)
	}
	k := h.api.kinds()
	if got := k[4:]; !slices.Equal(got, []string{"check", "stream", "send"}) {
		t.Fatalf("second message requests %v, want check, stream, send", got)
	}
	// another conversation gets its own session
	if r := h.run(h.inConv(3, bnConv2)); r.Code != 0 {
		t.Fatalf("third: exit %d", r.Code)
	}
	if n := len(h.api.createBodies()); n != 2 {
		t.Fatalf("%d creates for two conversations", n)
	}
	if got := h.sessionsFile(); got != rnConvID+" sesn_NEW1\n"+bnConv2+" sesn_NEW2\n" {
		t.Fatalf("sessions %q", got)
	}
	s := h.api.eventSessions()
	if !slices.Equal(s, []string{"sesn_NEW1", "sesn_NEW1", "sesn_NEW1", "sesn_NEW1", "sesn_NEW2", "sesn_NEW2"}) {
		t.Fatalf("events went to %v", s)
	}
}

// a create answered by a dropped connection may have landed: it is not
// repeated, and the retry adopts what it made
func TestBridgeConversationAdoptsAfterACrashBetweenCreateAndRecord(t *testing.T) {
	h := newConvHarness(t)
	h.api.set(func(f *fakeMA) { f.dropCreate = 1 })
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 1 || len(h.api.createBodies()) != 1 || h.sessionsFile() != "" || len(h.sends()) != 0 {
		t.Fatalf("first: exit %d, %d creates, sessions %q", r.Code, len(h.api.createBodies()), h.sessionsFile())
	}
	r = h.run(rnOpts{})
	if r.Code != 0 || len(h.api.createBodies()) != 1 {
		t.Fatalf("retry: exit %d, %d creates, log %q", r.Code, len(h.api.createBodies()), h.bridgeLog())
	}
	if got := h.sessionsFile(); got != rnConvID+" sesn_NEW1\n" {
		t.Fatalf("sessions %q", got)
	}
	if !strings.Contains(h.bridgeLog(), "session adopted") {
		t.Fatalf("log %q", h.bridgeLog())
	}
	if s := h.sends(); len(s) != 1 || !strings.Contains(s[0].Text, bnAnswer) {
		t.Fatalf("sends %v", s)
	}
}

// a lost state directory: Find adopts the newest live session of this
// conversation and this puppet, and nothing else
func TestBridgeConversationAdoptsAfterALostStateDir(t *testing.T) {
	h := newConvHarness(t)
	other := maSession("sesn_OTHERPUPPET", rnConvID, rnOther)
	archived := maSession("sesn_ARCHIVED", rnConvID, bnPuppet)
	archived["archived_at"] = "2026-10-01T00:00:00Z"
	archived["status"] = "terminated"
	terminated := maSession("sesn_TERMINATED", rnConvID, bnPuppet)
	terminated["status"] = "terminated"
	wrongConv := maSession("sesn_WRONGCONV", bnConv2, bnPuppet)
	badID := maSession("sesn_BAD/../x", rnConvID, bnPuppet)
	h.api.set(func(f *fakeMA) {
		f.findPage = 2
		// oldest first: the good one is the oldest, so every other is seen first
		f.store = []map[string]any{maSession("sesn_GOOD", rnConvID, bnPuppet), wrongConv, terminated, badID, archived, other}
	})
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 || len(h.api.createBodies()) != 0 {
		t.Fatalf("exit %d, %d creates, log %q", r.Code, len(h.api.createBodies()), h.bridgeLog())
	}
	if got := h.sessionsFile(); got != rnConvID+" sesn_GOOD\n" {
		t.Fatalf("sessions %q", got)
	}
	if s := h.api.eventSessions(); !slices.Equal(s, []string{"sesn_GOOD", "sesn_GOOD"}) {
		t.Fatalf("events went to %v", s)
	}
	if n := count(h.api.kinds(), "find"); n < 3 {
		t.Fatalf("%d find pages, want the pages followed", n)
	}
}

func TestBridgeConversationFindIsBoundedAtTenPages(t *testing.T) {
	h := newConvHarness(t)
	var store []map[string]any
	store = append(store, maSession("sesn_TOOFAR", rnConvID, bnPuppet))
	for range 12 {
		store = append(store, maSession("sesn_NOISE", bnConv2, bnPuppet))
	}
	h.api.set(func(f *fakeMA) { f.findPage = 1; f.store = store })
	h.script(idle("end_turn"))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d log %q", r.Code, h.bridgeLog())
	}
	if n := count(h.api.kinds(), "find"); n != 10 {
		t.Fatalf("%d find pages, want 10", n)
	}
	if n := len(h.api.createBodies()); n != 1 {
		t.Fatalf("%d creates: past the page cap the bridge creates", n)
	}
}

// two bridges on one agent never adopt each other's sessions
func TestBridgeConversationPuppetsDoNotShareSessions(t *testing.T) {
	a := newConvHarness(t)
	a.script(idle("end_turn"))
	if r := a.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("bridge a: exit %d", r.Code)
	}
	// bridge b: another puppet, its own state, the same API and conversation
	rb := *a.rnHarness
	rb.state = filepath.Join(t.TempDir(), "state")
	rb.env = map[string]string{}
	for k, v := range a.env {
		rb.env[k] = v
	}
	rb.env["SOCK_BRIDGE_STATE"] = rb.state
	rb.env["STUB_ACCOUNT"] = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	b := &bnHarness{rnHarness: &rb, api: a.api}
	a.script(idle("end_turn"))
	if r := b.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("bridge b: exit %d log %q", r.Code, b.bridgeLog())
	}
	if n := len(a.api.createBodies()); n != 2 {
		t.Fatalf("%d creates, want one per puppet", n)
	}
	if got := b.sessionsFile(); got != rnConvID+" sesn_NEW2\n" {
		t.Fatalf("bridge b sessions %q", got)
	}
	var body map[string]any
	mustJSON(t, []byte(a.api.createBodies()[1]), &body)
	if m := body["metadata"].(map[string]any); m["bridge_puppet"] != "dddddddd-dddd-4ddd-8ddd-dddddddddddd" {
		t.Fatalf("metadata %v", m)
	}
}

// a mapped session that is gone is replaced, and the owner is told
func TestBridgeConversationReplacesAGoneSession(t *testing.T) {
	for name, mod := range map[string]func(map[string]any) map[string]any{
		"404":        func(map[string]any) map[string]any { return nil },
		"terminated": func(s map[string]any) map[string]any { s["status"] = "terminated"; return s },
		"archived":   func(s map[string]any) map[string]any { s["archived_at"] = "2026-10-01T00:00:00Z"; return s },
	} {
		t.Run(name, func(t *testing.T) {
			h := newConvHarness(t)
			h.writeSessions(bnConv2 + " sesn_KEEP\n" + rnConvID + " sesn_OLD\n")
			old := mod(maSession("sesn_OLD", rnConvID, bnPuppet))
			h.api.set(func(f *fakeMA) {
				f.store = []map[string]any{maSession("sesn_KEEP", bnConv2, bnPuppet)}
				if old != nil {
					f.store = append(f.store, old)
				}
			})
			h.script(agentMsg(bnAnswer), idle("end_turn"))
			r := h.run(rnOpts{})
			if r.Code != 0 || len(h.api.createBodies()) != 1 {
				t.Fatalf("exit %d, %d creates, log %q", r.Code, len(h.api.createBodies()), h.bridgeLog())
			}
			if got := h.sessionsFile(); got != bnConv2+" sesn_KEEP\n"+rnConvID+" sesn_NEW1\n" {
				t.Fatalf("sessions %q", got)
			}
			s := h.sends()
			if len(s) != 2 || s[0].Text != bnGoneMsg || !strings.Contains(s[1].Text, bnAnswer) {
				t.Fatalf("sends %v", s)
			}
			if ev := h.api.eventSessions(); !slices.Equal(ev, []string{"sesn_NEW1", "sesn_NEW1"}) {
				t.Fatalf("events went to %v", ev)
			}
			if lg := h.bridgeLog(); !strings.Contains(lg, "session gone") || !strings.Contains(lg, "session created") {
				t.Fatalf("log %q", lg)
			}
		})
	}
}

// with SOCK_BRIDGE_SESSION the session is the owner's: 404 is a
// configuration error, never a replacement
func TestBridgeFixedSession404StillExits2(t *testing.T) {
	h := newBridgeHarness(t)
	h.api.set(func(f *fakeMA) { f.status["stream"] = []int{404} })
	r := h.run(rnOpts{})
	if r.Code != 2 || len(h.api.createBodies()) != 0 || count(h.api.kinds(), "find") != 0 {
		t.Fatalf("exit %d, kinds %v", r.Code, h.api.kinds())
	}
}

func TestBridgeConversationCreateMayHaveLanded(t *testing.T) {
	for name, c := range map[string]struct {
		set  func(*fakeMA)
		code int
	}{
		"dropped connection": {func(f *fakeMA) { f.dropCreate = 1 }, 1},
		"500":                {func(f *fakeMA) { f.status["create"] = []int{500, 500} }, 1},
		"400":                {func(f *fakeMA) { f.status["create"] = []int{400} }, 2},
		"404":                {func(f *fakeMA) { f.status["create"] = []int{404} }, 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := newConvHarness(t)
			h.api.set(c.set)
			r := h.run(rnOpts{})
			if r.Code != c.code || count(h.api.kinds(), "create") != 1 {
				t.Fatalf("exit %d, kinds %v", r.Code, h.api.kinds())
			}
			if h.sessionsFile() != "" || len(h.api.sentBodies()) != 0 {
				t.Fatalf("sessions %q, %d sends", h.sessionsFile(), len(h.api.sentBodies()))
			}
		})
	}
	t.Run("429 is retried", func(t *testing.T) {
		h := newConvHarness(t)
		h.api.set(func(f *fakeMA) { f.status["create"] = []int{429}; f.retryAft = "0" })
		h.script(idle("end_turn"))
		if r := h.run(rnOpts{}); r.Code != 0 || count(h.api.kinds(), "create") != 2 {
			t.Fatalf("exit %d, kinds %v", r.Code, h.api.kinds())
		}
	})
}

// the session id of a create response is checked before it is recorded or
// used, and one refused is a configuration error: a retry would only create
// another session the bridge cannot adopt
func TestBridgeConversationCreatedIDIsValidated(t *testing.T) {
	for _, id := range []string{"sesn_a/../b", "-sesn", "sesn a", "sesn_a\nb", strings.Repeat("s", 129)} {
		t.Run(id, func(t *testing.T) {
			h := newConvHarness(t)
			h.api.set(func(f *fakeMA) { f.createID = id })
			r := h.run(rnOpts{})
			if r.Code != 2 || h.sessionsFile() != "" || len(h.api.eventSessions()) != 0 || !strings.Contains(h.bridgeLog(), "session id invalid") {
				t.Fatalf("exit %d, sessions %q, events %v, log %q", r.Code, h.sessionsFile(), h.api.eventSessions(), h.bridgeLog())
			}
		})
	}
}

func TestBridgeConversationMetadataHoldsNoSecrets(t *testing.T) {
	h := newConvHarness(t)
	wrapper := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$STUB_DIR/curl.argv\"\nexec /usr/bin/curl \"$@\"\n"
	if err := os.WriteFile(filepath.Join(h.stubs, "curl"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	h.wrapTools("jq", "sed", "awk", "mv", "chmod")
	h.script(idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	argv, _ := os.ReadFile(filepath.Join(h.rec, "curl.argv"))
	if len(argv) == 0 || strings.Contains(string(argv), bnKey) || strings.Contains(string(argv), "x-api-key") {
		t.Fatalf("curl argv %q", argv)
	}
	env, _ := os.ReadFile(filepath.Join(h.rec, "childenv"))
	if strings.Contains(string(env), bnKey) || strings.Contains(string(env), "ANTHROPIC_API_KEY") {
		t.Fatal("the API key is in the environment of a child")
	}
	body := h.api.createBodies()[0]
	for _, bad := range []string{bnKey, rnTokenSentinel, rnTextSentinel, rnOwner} {
		if strings.Contains(body, bad) {
			t.Fatalf("create body holds %q: %s", bad, body)
		}
	}
	h.noKeyLeak(r)
	h.noLeak(r)
}

func TestBridgeConversationConfigExits2(t *testing.T) {
	for name, c := range map[string]struct {
		env   map[string]string
		unset []string
	}{
		"with SOCK_BRIDGE_SESSION": {env: map[string]string{"SOCK_BRIDGE_SESSION": bnSession}},
		"agent without prefix":     {env: map[string]string{"SOCK_BRIDGE_AGENT": "011CZkYpogX7"}},
		"agent with a slash":       {env: map[string]string{"SOCK_BRIDGE_AGENT": "agent_a/b"}},
		"agent with a newline":     {env: map[string]string{"SOCK_BRIDGE_AGENT": "agent_a\nagent_b"}},
		"agent too long":           {env: map[string]string{"SOCK_BRIDGE_AGENT": "agent_" + strings.Repeat("a", 121)}},
		"no environment":           {unset: []string{"SOCK_BRIDGE_ENVIRONMENT"}},
		"environment odd":          {env: map[string]string{"SOCK_BRIDGE_ENVIRONMENT": "env_a&b=c"}},
		"environment newline":      {env: map[string]string{"SOCK_BRIDGE_ENVIRONMENT": "env_a\nenv_b"}},
		"version zero":             {env: map[string]string{"SOCK_BRIDGE_AGENT_VERSION": "0"}},
		"version leading zero":     {env: map[string]string{"SOCK_BRIDGE_AGENT_VERSION": "03"}},
		"version ten digits":       {env: map[string]string{"SOCK_BRIDGE_AGENT_VERSION": "1234567890"}},
		"version not digits":       {env: map[string]string{"SOCK_BRIDGE_AGENT_VERSION": "1e3"}},
		"version newline":          {env: map[string]string{"SOCK_BRIDGE_AGENT_VERSION": "1\n2"}},
		"budget zero":              {env: map[string]string{"SOCK_BRIDGE_BUDGET": "0"}},
		"budget leading zero":      {env: map[string]string{"SOCK_BRIDGE_BUDGET": "0125"}},
		"budget dollars":           {env: map[string]string{"SOCK_BRIDGE_BUDGET": "1.25"}},
		"budget ten digits":        {env: map[string]string{"SOCK_BRIDGE_BUDGET": "1234567890"}},
		"budget newline":           {env: map[string]string{"SOCK_BRIDGE_BUDGET": "1\n2"}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newConvHarness(t)
			r := h.run(rnOpts{Env: c.env, Unset: c.unset})
			if r.Code != 2 || len(h.api.kinds()) != 0 {
				t.Fatalf("exit %d, requests %v", r.Code, h.api.kinds())
			}
		})
	}
	// the creation settings mean nothing without the agent
	for _, k := range []string{"SOCK_BRIDGE_ENVIRONMENT", "SOCK_BRIDGE_AGENT_VERSION", "SOCK_BRIDGE_BUDGET"} {
		t.Run(k+" without the agent", func(t *testing.T) {
			h := newBridgeHarness(t)
			r := h.run(rnOpts{Env: map[string]string{k: "1"}})
			if r.Code != 2 || len(h.api.kinds()) != 0 {
				t.Fatalf("exit %d, requests %v", r.Code, h.api.kinds())
			}
		})
	}
	t.Run("SOCK_BRIDGE_SESSION with a newline", func(t *testing.T) {
		h := newBridgeHarness(t)
		r := h.run(rnOpts{Env: map[string]string{"SOCK_BRIDGE_SESSION": bnSession + "\n../x"}})
		if r.Code != 2 || len(h.api.kinds()) != 0 {
			t.Fatalf("exit %d, requests %v", r.Code, h.api.kinds())
		}
	})
}

// a session deleted between the check and the turn is a retry, not a
// configuration error: the retry checks again and replaces it
func TestBridgeConversation404OnTheTurnRetries(t *testing.T) {
	h := newConvHarness(t)
	h.writeSessions(rnConvID + " sesn_OLD\n")
	h.api.set(func(f *fakeMA) {
		f.store = []map[string]any{maSession("sesn_OLD", rnConvID, bnPuppet)}
		f.status["stream"] = []int{404}
	})
	r := h.run(rnOpts{})
	if r.Code != 1 || !strings.Contains(h.bridgeLog(), "http 404") {
		t.Fatalf("exit %d, log %q", r.Code, h.bridgeLog())
	}
}

// the owner id is cached with the puppet's account id, and a cache without
// it costs one whoami in this mode
func TestBridgeConversationPuppetIDIsCached(t *testing.T) {
	h := newConvHarness(t)
	h.script(idle("end_turn"))
	h.script(idle("end_turn"))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	tok, _ := os.ReadFile(filepath.Join(h.rec, "sock-token"))
	before := strings.Count(string(tok), "\n")
	if r := h.run(h.inConv(2, bnConv2)); r.Code != 0 {
		t.Fatalf("exit %d", r.Code)
	}
	tok, _ = os.ReadFile(filepath.Join(h.rec, "sock-token"))
	// the second run calls sock only to send: whoami came from the cache
	if after := strings.Count(string(tok), "\n"); after != before+1 {
		t.Fatalf("sock calls %d then %d", before, after)
	}
	var body map[string]any
	mustJSON(t, []byte(h.api.createBodies()[1]), &body)
	if m := body["metadata"].(map[string]any); m["bridge_puppet"] != bnPuppet {
		t.Fatalf("metadata %v", m)
	}
}

const bnConfigMsg = "the bridge configuration changed; this reply started a new session"

func budgetOf(cents string) map[string]any {
	return map[string]any{"type": "limit", "max_list_cost": map[string]any{"amount": cents, "currency": "USD"}}
}

// a mapped session made for another agent, environment, pinned version or
// budget is not this configuration's: it is replaced and the owner is told
func TestBridgeConversationConfigChangeReplacesTheSession(t *testing.T) {
	for name, c := range map[string]struct {
		env     map[string]string
		mod     func(map[string]any)
		replace bool
	}{
		"other agent":          {mod: func(s map[string]any) { s["agent"].(map[string]any)["id"] = "agent_OLDPERMISSIVE" }, replace: true},
		"other environment":    {mod: func(s map[string]any) { s["environment_id"] = "env_OLD" }, replace: true},
		"other pinned version": {env: map[string]string{"SOCK_BRIDGE_AGENT_VERSION": "3"}, mod: func(s map[string]any) { s["agent"].(map[string]any)["version"] = float64(2) }, replace: true},
		"no budget, one set":   {env: map[string]string{"SOCK_BRIDGE_BUDGET": "100"}, mod: func(map[string]any) {}, replace: true},
		"lower config budget":  {env: map[string]string{"SOCK_BRIDGE_BUDGET": "50"}, mod: func(s map[string]any) { s["budget"] = budgetOf("100") }, replace: true},
		"console-raised":       {env: map[string]string{"SOCK_BRIDGE_BUDGET": "100"}, mod: func(s map[string]any) { s["budget"] = budgetOf("999") }, replace: true},
		"higher config budget": {env: map[string]string{"SOCK_BRIDGE_BUDGET": "100"}, mod: func(s map[string]any) { s["budget"] = budgetOf("50") }},
		"odd budget amount":    {env: map[string]string{"SOCK_BRIDGE_BUDGET": "100"}, mod: func(s map[string]any) { s["budget"] = budgetOf("5e1") }, replace: true},
		"budget not a limit": {env: map[string]string{"SOCK_BRIDGE_BUDGET": "100"}, mod: func(s map[string]any) {
			s["budget"] = map[string]any{"type": "unlimited", "max_list_cost": map[string]any{"amount": "100", "currency": "USD"}}
		}, replace: true},
		"same pinned version": {env: map[string]string{"SOCK_BRIDGE_AGENT_VERSION": "2"}, mod: func(s map[string]any) { s["agent"].(map[string]any)["version"] = float64(2) }},
		"same budget":         {env: map[string]string{"SOCK_BRIDGE_BUDGET": "100"}, mod: func(s map[string]any) { s["budget"] = budgetOf("100") }},
		"unpinned version":    {mod: func(s map[string]any) { s["agent"].(map[string]any)["version"] = float64(7) }},
		"budget not set":      {mod: func(s map[string]any) { s["budget"] = budgetOf("50") }},
	} {
		t.Run(name, func(t *testing.T) {
			h := newConvHarness(t)
			h.writeSessions(rnConvID + " sesn_OLD\n")
			old := maSession("sesn_OLD", rnConvID, bnPuppet)
			c.mod(old)
			h.api.set(func(f *fakeMA) { f.store = []map[string]any{old} })
			h.script(agentMsg(bnAnswer), idle("end_turn"))
			r := h.run(rnOpts{Env: c.env})
			if r.Code != 0 {
				t.Fatalf("exit %d, log %q", r.Code, h.bridgeLog())
			}
			s := h.sends()
			if !c.replace {
				if n := len(h.api.createBodies()); n != 0 || len(s) != 1 || h.sessionsFile() != rnConvID+" sesn_OLD\n" {
					t.Fatalf("%d creates, sends %v, sessions %q", n, s, h.sessionsFile())
				}
				return
			}
			if n := len(h.api.createBodies()); n != 1 || h.sessionsFile() != rnConvID+" sesn_NEW1\n" {
				t.Fatalf("%d creates, sessions %q", n, h.sessionsFile())
			}
			if len(s) != 2 || s[0].Text != bnConfigMsg || !strings.Contains(s[1].Text, bnAnswer) {
				t.Fatalf("sends %v", s)
			}
			if ev := h.api.eventSessions(); !slices.Equal(ev, []string{"sesn_NEW1", "sesn_NEW1"}) {
				t.Fatalf("events went to %v", ev)
			}
			if !strings.Contains(h.bridgeLog(), "session config changed") {
				t.Fatalf("log %q", h.bridgeLog())
			}
		})
	}
}

// Find adopts only a session that fits the configuration: agent, environment,
// the pinned version and the budget when set
func TestBridgeConversationFindAdoptsOnlyWhatFitsTheConfig(t *testing.T) {
	h := newConvHarness(t)
	h.env["SOCK_BRIDGE_AGENT_VERSION"] = "3"
	h.env["SOCK_BRIDGE_BUDGET"] = "100"
	good := maSession("sesn_GOOD", rnConvID, bnPuppet)
	good["agent"].(map[string]any)["version"] = float64(3)
	good["budget"] = budgetOf("100")
	clone := func(id string, mod func(map[string]any)) map[string]any {
		s := maSession(id, rnConvID, bnPuppet)
		s["agent"].(map[string]any)["version"] = float64(3)
		s["budget"] = budgetOf("100")
		mod(s)
		return s
	}
	h.api.set(func(f *fakeMA) {
		f.store = []map[string]any{good,
			clone("sesn_ENV", func(s map[string]any) { s["environment_id"] = "env_ATTACKER" }),
			clone("sesn_VERSION", func(s map[string]any) { s["agent"].(map[string]any)["version"] = float64(1) }),
			clone("sesn_NOBUDGET", func(s map[string]any) { s["budget"] = nil }),
			clone("sesn_BUDGET", func(s map[string]any) { s["budget"] = budgetOf("999") }),
		}
	})
	h.script(idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 || len(h.api.createBodies()) != 0 || h.sessionsFile() != rnConvID+" sesn_GOOD\n" {
		t.Fatalf("exit %d, %d creates, sessions %q, log %q", r.Code, len(h.api.createBodies()), h.sessionsFile(), h.bridgeLog())
	}
}

// a session of another agent that the list returns anyway is not adopted: the
// bridge does not rely on the server's agent_id filter alone
func TestBridgeConversationFindChecksTheAgentItself(t *testing.T) {
	h2 := newConvHarness(t)
	other := maSession("sesn_OTHERAGENT", rnConvID, bnPuppet)
	other["agent"].(map[string]any)["id"] = "agent_OTHER"
	h2.api.set(func(f *fakeMA) { f.store = []map[string]any{other}; f.noAgentFilter = true })
	h2.script(idle("end_turn"))
	if r := h2.run(rnOpts{}); r.Code != 0 || len(h2.api.createBodies()) != 1 || h2.sessionsFile() != rnConvID+" sesn_NEW1\n" {
		t.Fatalf("exit %d, %d creates, sessions %q", r.Code, len(h2.api.createBodies()), h2.sessionsFile())
	}
}

// a sessions file the bridge cannot read fails closed: no request, and the
// other conversations' lines are kept
func TestBridgeConversationUnreadableMapFailsClosed(t *testing.T) {
	h := newConvHarness(t)
	h.writeSessions(bnConv2 + " sesn_KEEP\n")
	p := filepath.Join(h.state, "sessions")
	if err := os.Chmod(p, 0o200); err != nil {
		t.Fatal(err)
	}
	h.script(idle("end_turn"))
	r := h.run(rnOpts{})
	_ = os.Chmod(p, 0o600)
	if r.Code != 1 || len(h.api.kinds()) != 0 || !strings.Contains(h.bridgeLog(), "sessions not readable") {
		t.Fatalf("exit %d, requests %v, log %q", r.Code, h.api.kinds(), h.bridgeLog())
	}
	if got := h.sessionsFile(); got != bnConv2+" sesn_KEEP\n" {
		t.Fatalf("sessions %q", got)
	}
}

// Find adopts a session whose budget is within the configured one, as Check
// keeps it: raising SOCK_BRIDGE_BUDGET does not orphan a conversation's session
func TestBridgeConversationFindAdoptsALowerBudget(t *testing.T) {
	h := newConvHarness(t)
	h.env["SOCK_BRIDGE_BUDGET"] = "100"
	low := maSession("sesn_LOW", rnConvID, bnPuppet)
	low["budget"] = budgetOf("50")
	high := maSession("sesn_HIGH", rnConvID, bnPuppet)
	high["budget"] = budgetOf("101")
	h.api.set(func(f *fakeMA) { f.store = []map[string]any{low, high} })
	h.script(idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 || len(h.api.createBodies()) != 0 || h.sessionsFile() != rnConvID+" sesn_LOW\n" {
		t.Fatalf("exit %d, %d creates, sessions %q", r.Code, len(h.api.createBodies()), h.sessionsFile())
	}
}

const bnHaltMsg = "the bridge cannot start a session that fits its configuration; check SOCK_BRIDGE_AGENT, SOCK_BRIDGE_ENVIRONMENT and SOCK_BRIDGE_BUDGET"

func (h *bnHarness) haltNotices() int {
	n := 0
	for _, s := range h.sends() {
		if s.Text == bnHaltMsg {
			n++
		}
	}
	return n
}

// a create the bridge cannot use trips one durable breaker: 6 messages, each
// delivered twice, make one create, and the owner hears of it once
func TestBridgeConversationCreateBreaker(t *testing.T) {
	for name, c := range map[string]struct {
		env map[string]string
		set func(*fakeMA)
	}{
		"refused id":        {set: func(f *fakeMA) { f.createID = "sesn a" }},
		"budget not echoed": {env: map[string]string{"SOCK_BRIDGE_BUDGET": "100"}, set: func(f *fakeMA) { f.createNoBudget = true }},
	} {
		t.Run(name, func(t *testing.T) {
			h := newConvHarness(t)
			for k, v := range c.env {
				h.env[k] = v
			}
			h.api.set(c.set)
			for i := range 6 {
				h.script(agentMsg(bnAnswer), idle("end_turn"))
				o := h.inConv(i+1, rnConvID)
				for range 2 {
					if r := h.run(o); r.Code != 2 {
						t.Fatalf("message %d: exit %d, log %q", i, r.Code, h.bridgeLog())
					}
				}
			}
			if n := count(h.api.kinds(), "create"); n != 1 {
				t.Fatalf("%d creates, want 1", n)
			}
			if n := count(h.api.kinds(), "find"); n != 1 {
				t.Fatalf("%d finds: a halted bridge adopts nothing either", n)
			}
			if n := h.haltNotices(); n != 1 || len(h.sends()) != 1 {
				t.Fatalf("%d halt notices, sends %v", n, h.sends())
			}
			if h.sessionsFile() != "" || len(h.api.sentBodies()) != 0 {
				t.Fatalf("sessions %q, %d user.messages", h.sessionsFile(), len(h.api.sentBodies()))
			}
			if lg := h.bridgeLog(); strings.Count(lg, "create halted") < 12 {
				t.Fatalf("log %q", lg)
			}
			st, err := os.Stat(filepath.Join(h.state, "create.halt"))
			if err != nil || st.Mode().Perm() != 0o600 {
				t.Fatalf("create.halt %v %v", st, err)
			}
			if _, err := os.Stat(filepath.Join(h.state, "create.halt.tmp")); err == nil {
				t.Fatal("create.halt.tmp left behind")
			}
		})
	}
}

// a changed configuration clears the breaker by itself, and so does deleting
// the file; a mapped session that fits still serves while it is tripped
func TestBridgeConversationCreateBreakerClears(t *testing.T) {
	t.Run("config change", func(t *testing.T) {
		h := newConvHarness(t)
		h.env["SOCK_BRIDGE_BUDGET"] = "100"
		h.api.set(func(f *fakeMA) { f.createNoBudget = true })
		if r := h.run(rnOpts{}); r.Code != 2 {
			t.Fatalf("trip: exit %d", r.Code)
		}
		h.api.set(func(f *fakeMA) { f.createNoBudget = false })
		h.script(agentMsg(bnAnswer), idle("end_turn"))
		if r := h.run(h.inConv(2, rnConvID)); r.Code != 2 {
			t.Fatalf("same config: exit %d", r.Code)
		}
		h.env["SOCK_BRIDGE_BUDGET"] = "200"
		r := h.run(h.inConv(3, rnConvID))
		if r.Code != 0 || count(h.api.kinds(), "create") != 2 {
			t.Fatalf("new config: exit %d, kinds %v, log %q", r.Code, h.api.kinds(), h.bridgeLog())
		}
		if _, err := os.Stat(filepath.Join(h.state, "create.halt")); err == nil {
			t.Fatal("create.halt kept after the config changed")
		}
	})
	t.Run("file deleted", func(t *testing.T) {
		h := newConvHarness(t)
		h.api.set(func(f *fakeMA) { f.createID = "sesn a" })
		if r := h.run(rnOpts{}); r.Code != 2 {
			t.Fatalf("trip: exit %d", r.Code)
		}
		h.api.set(func(f *fakeMA) { f.createID = "" })
		if err := os.Remove(filepath.Join(h.state, "create.halt")); err != nil {
			t.Fatal(err)
		}
		h.script(agentMsg(bnAnswer), idle("end_turn"))
		if r := h.run(h.inConv(2, rnConvID)); r.Code != 0 {
			t.Fatalf("after delete: exit %d, log %q", r.Code, h.bridgeLog())
		}
	})
	t.Run("mapped session serves", func(t *testing.T) {
		h := newConvHarness(t)
		h.writeSessions(bnConv2 + " sesn_KEEP\n")
		h.api.set(func(f *fakeMA) {
			f.createID = "sesn a"
			f.store = []map[string]any{maSession("sesn_KEEP", bnConv2, bnPuppet)}
		})
		if r := h.run(rnOpts{}); r.Code != 2 {
			t.Fatalf("trip: exit %d", r.Code)
		}
		h.script(agentMsg(bnAnswer), idle("end_turn"))
		if r := h.run(h.inConv(2, bnConv2)); r.Code != 0 {
			t.Fatalf("mapped: exit %d, log %q", r.Code, h.bridgeLog())
		}
	})
}

// a session made from this exact configuration that a Check shows in another
// shape has drifted: the breaker trips and the session is kept, unused, rather
// than replaced on every reply
func TestBridgeConversationShapeDriftTripsTheBreaker(t *testing.T) {
	h := newConvHarness(t)
	h.env["SOCK_BRIDGE_BUDGET"] = "100"
	h.api.set(func(f *fakeMA) { f.checkNoBudget = true })
	for i := range 4 {
		h.script(agentMsg(bnAnswer), idle("end_turn"))
		want := 2
		if i == 0 {
			want = 0
		}
		if r := h.run(h.inConv(i+1, rnConvID)); r.Code != want {
			t.Fatalf("message %d: exit %d, log %q", i, r.Code, h.bridgeLog())
		}
	}
	if n := count(h.api.kinds(), "create"); n != 1 {
		t.Fatalf("%d creates, want 1", n)
	}
	if n := h.haltNotices(); n != 1 {
		t.Fatalf("%d halt notices, sends %v", n, h.sends())
	}
	for _, s := range h.sends() {
		if s.Text == bnConfigMsg {
			t.Fatal("a drifted session was replaced as a configuration change")
		}
	}
	if got := h.sessionsRaw(); !strings.HasPrefix(got, rnConvID+" sesn_NEW1 ") {
		t.Fatalf("sessions %q: the session is kept", got)
	}
	if n := len(h.api.sentBodies()); n != 1 {
		t.Fatalf("%d user.messages: a drifted session is not used", n)
	}
}

// the hash on a sessions line tells an operator's change from drift: a session
// created under one budget and checked under another is replaced, not halted
func TestBridgeConversationRecordedHashSeparatesAConfigChange(t *testing.T) {
	h := newConvHarness(t)
	h.env["SOCK_BRIDGE_BUDGET"] = "100"
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	if r := h.run(rnOpts{}); r.Code != 0 {
		t.Fatalf("first: exit %d", r.Code)
	}
	if f := strings.Fields(h.sessionsRaw()); len(f) != 3 || len(f[2]) != 16 {
		t.Fatalf("sessions %q: want the config hash on the line", h.sessionsRaw())
	}
	h.env["SOCK_BRIDGE_BUDGET"] = "50"
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(h.inConv(2, rnConvID))
	if r.Code != 0 || count(h.api.kinds(), "create") != 2 || h.haltNotices() != 0 {
		t.Fatalf("exit %d, kinds %v, log %q", r.Code, h.api.kinds(), h.bridgeLog())
	}
	if s := h.sends(); len(s) < 2 || s[1].Text != bnConfigMsg {
		t.Fatalf("sends %v", s)
	}
}
