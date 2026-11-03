package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPublicTagsScript runs scripts/public-tags.sh against a local repository
// standing in for the public one, so the test stays offline.
func TestPublicTagsScript(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	script, err := filepath.Abs("scripts/public-tags.sh")
	if err != nil {
		t.Fatal(err)
	}
	remote := t.TempDir()
	gitIn(t, remote, "init", "-q")
	gitIn(t, remote, "commit", "-q", "--allow-empty", "-m", "x")
	for _, tag := range []string{"core-v0.1.0", "core-v0.2.0", "vscode/v0.9.0"} {
		gitIn(t, remote, "tag", "-a", "-m", tag, tag)
	}
	for _, c := range []struct {
		name string
		list string
		exit int
		out  []string
		args []string
		file string
	}{
		{"agree", "# c\ncore-v0.1.0\n\ncore-v0.2.0\n", 0, nil, nil, ""},
		{"unlisted public tag", "core-v0.1.0\n", 1, []string{"not listed: core-v0.2.0"}, nil, ""},
		{"listed tag not public", "core-v0.1.0\ncore-v0.2.0\ncore-v0.3.0\n", 1, []string{"not public: core-v0.3.0"}, nil, ""},
		{"both", "core-v0.2.0\ncore-v0.3.0\n", 1, []string{"not public: core-v0.3.0", "not listed: core-v0.1.0"}, nil, ""},
		{"append unlisted", "core-v0.1.0\n", 0, []string{"appended: core-v0.2.0"}, []string{"--append"}, "core-v0.1.0\ncore-v0.2.0\n"},
		{"append without final newline", "# c\ncore-v0.2.0", 0, []string{"appended: core-v0.1.0"}, []string{"--append"}, "# c\ncore-v0.2.0\ncore-v0.1.0\n"},
		{"append with a tag not public", "core-v0.3.0\n", 1, []string{"not public: core-v0.3.0", "appended: core-v0.1.0", "appended: core-v0.2.0"}, []string{"--append"}, "core-v0.3.0\ncore-v0.1.0\ncore-v0.2.0\n"},
		{"no flag changes nothing", "core-v0.1.0\n", 1, []string{"not listed: core-v0.2.0"}, nil, "core-v0.1.0\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "testdata"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, publicTagsFile), []byte(c.list), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), script, append(c.args, remote)...) //nolint:gosec // the script of this repository
			cmd.Dir = dir
			out, err := cmd.Output()
			got := 0
			if ee := (&exec.ExitError{}); errors.As(err, &ee) {
				got = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if got != c.exit {
				t.Errorf("public-tags.sh exit = %d, want %d", got, c.exit)
			}
			wantFile := c.file
			if wantFile == "" {
				wantFile = c.list
			}
			if data, err := os.ReadFile(filepath.Join(dir, publicTagsFile)); err != nil {
				t.Fatal(err)
			} else if string(data) != wantFile {
				t.Errorf("public-tags.sh left list = %q, want %q", data, wantFile)
			}
			for _, want := range c.out {
				if !strings.Contains(string(out), want) {
					t.Errorf("public-tags.sh output = %q, want it to contain %q", out, want)
				}
			}
		})
	}
}
