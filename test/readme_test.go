package test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// README guards: the README names paths, make targets and integrations. Each
// is checked against the tree so the README cannot describe a repo that no
// longer exists.

func readme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestReadmePathsExist: every backticked or linked repo path in the README
// points at a file or directory in the tree.
func TestReadmePathsExist(t *testing.T) {
	text := readme(t)
	seen := map[string]bool{}
	check := func(p string) {
		p = strings.TrimSuffix(p, "/")
		if seen[p] {
			return
		}
		seen[p] = true
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("README names %q, which is not in the tree: fix the path or remove the reference", p)
		}
	}
	// `dir/file` with a known top-level directory, or a relative link target.
	tops := []string{"agent-sdk", "assets", "claude-code", "codex", "docs", "hosted", "runner", "scripts", "skills", "test"}
	codeRe := regexp.MustCompile("`([A-Za-z0-9_./-]+)`")
	for _, m := range codeRe.FindAllStringSubmatch(text, -1) {
		p := m[1]
		top, _, ok := strings.Cut(p, "/")
		if ok && slices.Contains(tops, top) {
			check(p)
		}
	}
	linkRe := regexp.MustCompile(`\]\(([^)#:]+)(#[^)]*)?\)`)
	for _, m := range linkRe.FindAllStringSubmatch(text, -1) {
		check(m[1])
	}
	imgRe := regexp.MustCompile(`src="([^"]+)"`)
	for _, m := range imgRe.FindAllStringSubmatch(text, -1) {
		check(m[1])
	}
}

// TestReadmeMakeTargets: every `make <target>` in the README is a Makefile
// target, and every .PHONY target is described in the README.
func TestReadmeMakeTargets(t *testing.T) {
	text := readme(t)
	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	phony := regexp.MustCompile(`(?m)^\.PHONY:\s*(.*)$`).FindStringSubmatch(string(mk))
	if phony == nil {
		t.Fatal("Makefile has no .PHONY line")
	}
	targets := strings.Fields(phony[1])
	named := map[string]bool{}
	for _, m := range regexp.MustCompile("`make ([a-z0-9-]+)`").FindAllStringSubmatch(text, -1) {
		named[m[1]] = true
		if !slices.Contains(targets, m[1]) {
			t.Errorf("README names `make %s`, not a Makefile target: fix the README or add the target to .PHONY", m[1])
		}
	}
	for _, tg := range targets {
		if !named[tg] {
			t.Errorf("Makefile target %q is not in the README: add a `make %s` line to its Make targets list", tg, tg)
		}
	}
}

// TestReadmeListsEveryIntegration: each integration directory and each
// runner command has a README section or install line naming it.
func TestReadmeListsEveryIntegration(t *testing.T) {
	text := readme(t)
	want := []string{"claude-code/", "codex/", "agent-sdk/", "hosted/"}
	ents, err := os.ReadDir(filepath.Join(root, "runner"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "sock-") {
			continue
		}
		want = append(want, "runner/"+e.Name())
	}
	for _, w := range want {
		if !strings.Contains(text, "`"+w+"`") && !strings.Contains(text, "`"+strings.TrimSuffix(w, "/")+"`") {
			t.Errorf("README does not name `%s`: every integration gets a section with its install or run commands", w)
		}
	}
}
