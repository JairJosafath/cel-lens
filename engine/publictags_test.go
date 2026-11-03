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
		none bool // no list file exists
	}{
		{"agree", "# c\ncore-v0.1.0\n\ncore-v0.2.0\n", 0, nil, nil, "", false},
		{"unlisted public tag", "core-v0.1.0\n", 1, []string{"not listed: core-v0.2.0"}, nil, "", false},
		{"listed tag not public", "core-v0.1.0\ncore-v0.2.0\ncore-v0.3.0\n", 1, []string{"not public: core-v0.3.0"}, nil, "", false},
		{"both", "core-v0.2.0\ncore-v0.3.0\n", 1, []string{"not public: core-v0.3.0", "not listed: core-v0.1.0"}, nil, "", false},
		{"append unlisted", "core-v0.1.0\n", 0, []string{"appended: core-v0.2.0"}, []string{"--append"}, "core-v0.1.0\ncore-v0.2.0\n", false},
		{"append without final newline", "# c\ncore-v0.2.0", 0, []string{"appended: core-v0.1.0"}, []string{"--append"}, "# c\ncore-v0.2.0\ncore-v0.1.0\n", false},
		{"append with a tag not public", "core-v0.3.0\n", 1, []string{"not public: core-v0.3.0", "appended: core-v0.1.0", "appended: core-v0.2.0"}, []string{"--append"}, "core-v0.3.0\ncore-v0.1.0\ncore-v0.2.0\n", false},
		{"no flag changes nothing", "core-v0.1.0\n", 1, []string{"not listed: core-v0.2.0"}, nil, "core-v0.1.0\n", false},
		{"missing list", "", 2, nil, nil, "", true},
		{"missing list with append", "", 2, nil, []string{"--append"}, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "testdata"), 0o750); err != nil {
				t.Fatal(err)
			}
			if !c.none {
				if err := os.WriteFile(filepath.Join(dir, publicTagsFile), []byte(c.list), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(t.Context(), script, append(c.args, remote)...) //nolint:gosec // the script of this repository
			cmd.Dir = dir
			var stderr strings.Builder
			cmd.Stderr = &stderr
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
			if c.none {
				if !strings.Contains(stderr.String(), publicTagsFile) {
					t.Errorf("public-tags.sh stderr = %q, want it to name %s", stderr.String(), publicTagsFile)
				}
				if _, err := os.Stat(filepath.Join(dir, publicTagsFile)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("public-tags.sh created %s (stat error %v)", publicTagsFile, err)
				}
				return
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
	t.Run("unreadable remote", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "testdata"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, publicTagsFile), []byte("core-v0.1.0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		missing := filepath.Join(dir, "no-such-repository")
		cmd := exec.CommandContext(t.Context(), script, missing) //nolint:gosec // the script of this repository
		cmd.Dir = dir
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if ee := (&exec.ExitError{}); !errors.As(err, &ee) || ee.ExitCode() != 1 {
			t.Errorf("public-tags.sh exit error = %v, want exit status 1", err)
		}
		if n := strings.Count(stderr.String(), "cannot read remote: "+missing); n != 1 {
			t.Errorf("public-tags.sh stderr = %q, want one \"cannot read remote: %s\" line", stderr.String(), missing)
		}
		if strings.Contains(stdout.String()+stderr.String(), "not public") {
			t.Errorf("public-tags.sh output = %q %q, want no \"not public\" line", stdout.String(), stderr.String())
		}
	})
}
