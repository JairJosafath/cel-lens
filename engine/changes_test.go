package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitIn runs git in dir and fails the test when it does.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // git with arguments this test chose
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestChangesScript(t *testing.T) {
	t.Parallel()
	script, err := filepath.Abs("scripts/changes.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	const changelog = "# Changelog\n\n## [Unreleased]\n\n## [0.1.0] - 2026-01-01\n"
	for _, c := range []struct {
		name     string
		edit     map[string]string // path -> new content
		docsOnly bool
		prepare  bool
	}{
		{"docs the tests do not read", map[string]string{"docs/agents/a.md": "more\n", "docs/style/b.md": "x\n", "AGENTS.md": "y\n"}, true, false},
		{"image in docs", map[string]string{"docs/pic.png": "p\n"}, false, false},
		{"factory address in a non-Markdown docs file", map[string]string{"docs/x.txt": "see c4" + "factory.local\n"}, false, false},
		{"factory address in an image", map[string]string{"logo.svg": "c4" + "factory.local\n"}, false, false},
		{"markdown a test scans", map[string]string{"docs/guide.md": "more\n"}, false, false},
		{"markdown outside docs", map[string]string{"notes/todo.md": "x\n"}, false, false},
		{"changelog entry without a version", map[string]string{"CHANGELOG.md": changelog + "\n- fix\n"}, false, false},
		{"one Go file", map[string]string{"docs/guide.md": "more\n", "main.go": "package main\n// x\n"}, false, false},
		{"go.mod", map[string]string{"go.mod": "module x\n"}, false, false},
		{"workflow", map[string]string{".gitea/workflows/check.yaml": "name: x\n"}, false, false},
		{"makefile", map[string]string{"Makefile": "all:\n"}, false, false},
		{"readme, which a test reads", map[string]string{"README.md": "changed\n"}, false, false},
		{"markdown in test data", map[string]string{"pkg/testdata/a.md": "changed\n"}, false, false},
		{"changelog with a version heading", map[string]string{
			"CHANGELOG.md": "# Changelog\n\n## [Unreleased]\n\n## [0.2.0] - 2026-02-01\n\n## [0.1.0] - 2026-01-01\n",
		}, false, true},
		{"changelog heading and docs", map[string]string{
			"CHANGELOG.md":  "# Changelog\n\n## [Unreleased]\n\n## [0.2.0] - 2026-02-01\n\n## [0.1.0] - 2026-01-01\n",
			"docs/guide.md": "more\n",
		}, false, true},
		{"factory address in docs", map[string]string{"docs/g.md": "a\nsee c4" + "Factory.local/x\n"}, false, false},
		{"changelog heading with a bad date", map[string]string{
			"CHANGELOG.md": "# Changelog\n\n## [Unreleased]\n\n## [v0.0.9] - yesterday\n\n## [0.1.0] - 2026-01-01\n",
		}, false, false},
		{"changelog heading edited", map[string]string{
			"CHANGELOG.md": "# Changelog\n\n## [Unreleased]\n\n## [0.1.0] - 2026-01-02\n",
		}, false, true},
		{"no files", nil, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			gitIn(t, dir, "init", "-q", ".")
			write := func(files map[string]string) {
				for path, body := range files {
					full := filepath.Join(dir, path)
					if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			write(map[string]string{
				"CHANGELOG.md": changelog, "docs/agents/a.md": "a\n", "docs/style/b.md": "a\n", "AGENTS.md": "a\n", "docs/pic.png": "a\n", "docs/x.txt": "a\n", "logo.svg": "a\n", "docs/guide.md": "a\n", "notes/todo.md": "a\n", "main.go": "package main\n",
				"go.mod": "module a\n", "Makefile": "a:\n", "README.md": "a\n", "pkg/testdata/a.md": "a\n",
				".gitea/workflows/check.yaml": "name: a\n",
			})
			gitIn(t, dir, "add", "-A")
			gitIn(t, dir, "commit", "-q", "-m", "base")
			write(c.edit)
			gitIn(t, dir, "add", "-A")
			gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "change")
			for sub, want := range map[string]bool{"docs-only": c.docsOnly, "prepare": c.prepare} {
				cmd := exec.CommandContext(t.Context(), script, sub, "HEAD~1") //nolint:gosec // script is this repository's own
				cmd.Dir = dir
				got := cmd.Run() == nil
				if got != want {
					t.Errorf("changes.sh %s = %v, want %v", sub, got, want)
				}
			}
		})
	}
}

func TestChangesScriptUsage(t *testing.T) {
	t.Parallel()
	err := exec.CommandContext(t.Context(), "scripts/changes.sh", "nonsense", "HEAD").Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Errorf("changes.sh nonsense = %v, want exit status 2", err)
	}
}
