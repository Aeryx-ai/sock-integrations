package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wrapTools puts a wrapper for each tool first on PATH: it appends its own
// environment to $STUB_DIR/childenv, then runs the real tool.
func (h *rnHarness) wrapTools(tools ...string) {
	h.t.Helper()
	for _, name := range tools {
		w := "#!/bin/sh\nenv >> \"$STUB_DIR/childenv\"\nPATH=" + rnPathTail + "\nexport PATH\nexec " + name + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(h.stubs, name), []byte(w), 0o755); err != nil {
			h.t.Fatal(err)
		}
	}
}

// M1: the API key is unset before the bridge runs any child, whoami included
func TestBridgeKeyIsInNoChildEnvironment(t *testing.T) {
	h := newBridgeHarness(t)
	h.wrapTools("dirname", "jq", "ps", "find", "cat", "grep", "sed", "tr", "readlink", "curl", "date")
	sock := strings.Replace(bnSockStub, "#!/bin/sh\n", "#!/bin/sh\nenv >> \"$STUB_DIR/childenv\"\n", 1)
	if err := os.WriteFile(filepath.Join(h.stubs, "sock"), []byte(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	h.script(agentMsg(bnAnswer), idle("end_turn"))
	r := h.run(rnOpts{})
	if r.Code != 0 || len(h.sends()) != 1 {
		t.Fatalf("exit %d, %d sends, stderr %q", r.Code, len(h.sends()), r.Stderr)
	}
	env := string(readFile(t, filepath.Join(h.rec, "childenv")))
	tok, _ := os.ReadFile(filepath.Join(h.rec, "sock-token"))
	if strings.Count(string(tok), "\n") < 2 {
		t.Fatalf("whoami did not run: %q", tok)
	}
	if strings.Contains(env, bnKey) || strings.Contains(env, "ANTHROPIC_API_KEY") {
		t.Fatal("the API key is in the environment of a child")
	}
}
