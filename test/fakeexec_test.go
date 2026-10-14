package test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// macOS scans an executable the first time it is exec'd: 0.2 to 5 s per new
// file on a loaded machine, against 20 to 40 ms for a second exec or for
// `sh file`. A fake written fresh per test paid that inside the test's own
// timeout (integrations-9l1). writeFakeExec puts a symlink to one shared,
// already-scanned trampoline at path instead, and the script beside it as
// .<name>.sh. The trampoline sources the script, so $0, $@, $$, stdin and
// exit status are the fake's own and no extra process or PATH lookup runs.
// The trampoline is read-only: a test that writes a stub path directly would
// otherwise write through the symlink and turn every fake into its script.
const fakeTrampoline = "#!/bin/sh\n. \"${0%/*}/.${0##*/}.sh\"\n"

var trampoline struct {
	once sync.Once
	dir  string
	path string
	err  error
}

func trampolinePath(t testing.TB) (string, error) {
	trampoline.once.Do(func() {
		dir, err := os.MkdirTemp("", "fake-exec-")
		if err != nil {
			trampoline.err = err
			return
		}
		trampoline.dir = dir
		p := filepath.Join(dir, "trampoline")
		if err := os.WriteFile(p, []byte(fakeTrampoline), 0o555); err != nil {
			trampoline.err = err
			return
		}
		// Pay the first-exec scan here, once, outside every test's clock.
		warm := filepath.Join(dir, "warm")
		if err := os.WriteFile(filepath.Join(dir, ".warm.sh"), []byte(":\n"), 0o644); err != nil {
			trampoline.err = err
			return
		}
		if err := os.Symlink(p, warm); err != nil {
			trampoline.err = err
			return
		}
		trampoline.err = runCmd(testCmd(t, warm))
		trampoline.path = p
	})
	return trampoline.path, trampoline.err
}

// writeFakeExec makes path an executable that runs script, a /bin/sh script.
func writeFakeExec(t testing.TB, path, script string) {
	t.Helper()
	p, err := trampolinePath(t)
	if err != nil {
		t.Fatalf("fake exec trampoline: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".sh"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	// A harness stub a test replaces is already there.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(p, path); err != nil {
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if trampoline.dir != "" {
		_ = os.RemoveAll(trampoline.dir)
	}
	os.Exit(code)
}
