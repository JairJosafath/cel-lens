package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const prepareChangelog = `# Changelog

Versions 0.1.1 was never tagged on the public repository, so it has no link.

## [Unreleased]

### Fixed

- A fix.

## [0.3.0] - 2026-03-01

### Added

- Three.

## [0.2.0] - 2026-02-01

### Added

- Two.

## [0.1.1] - 2026-01-15

### Fixed

- One and a bit.

## [0.1.0] - 2026-01-01

### Added

- One.

[Unreleased]: ` + changelogRepo + `compare/core-v0.3.0...HEAD
[0.3.0]: ` + changelogRepo + `compare/core-v0.1.0...core-v0.3.0
[0.2.0]: ` + changelogRepo + `compare/core-v0.1.0...core-v0.2.0
[0.1.0]: ` + changelogRepo + `releases/tag/core-v0.1.0
`

// TestPrepareReleaseScript runs scripts/prepare-release.sh against a local
// repository standing in for the public one, so the test stays offline.
func TestPrepareReleaseScript(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	script, err := filepath.Abs("scripts/prepare-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	newRemote := func(tags ...string) string {
		remote := t.TempDir()
		gitIn(t, remote, "init", "-q")
		gitIn(t, remote, "commit", "-q", "--allow-empty", "-m", "x")
		for _, tag := range tags {
			gitIn(t, remote, "tag", "-a", "-m", tag, tag)
		}
		return remote
	}
	run := func(t *testing.T, remote, version, list, changelog string) (dir, stderr string, exit int) {
		t.Helper()
		dir = t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "testdata"), 0o750); err != nil {
			t.Fatal(err)
		}
		for name, text := range map[string]string{publicTagsFile: list, "CHANGELOG.md": changelog} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.CommandContext(t.Context(), script, version) //nolint:gosec // the script of this repository
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "CEL_LENS_PUBLIC_REMOTE="+remote, "RELEASE_DATE=2026-04-01")
		var errOut strings.Builder
		cmd.Stderr = &errOut
		err := cmd.Run()
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return dir, errOut.String(), exit
	}
	read := func(t *testing.T, dir, name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	t.Run("a public tag the list lacks is added and linked", func(t *testing.T) {
		t.Parallel()
		remote := newRemote("core-v0.1.0", "core-v0.2.0", "vscode/v0.9.0")
		dir, stderr, exit := run(t, remote, "0.3.0", "# c\ncore-v0.1.0\ncore-v0.9.0\n", prepareChangelog)
		if exit != 0 {
			t.Fatalf("prepare-release.sh 0.3.0 exit = %d, want 0; stderr %q", exit, stderr)
		}
		if got, want := read(t, dir, publicTagsFile), "# c\ncore-v0.1.0\ncore-v0.9.0\ncore-v0.2.0\n"; got != want {
			t.Errorf("tag list = %q, want %q (the tag added, none removed)", got, want)
		}
		log := read(t, dir, "CHANGELOG.md")
		for _, want := range []string{
			"[0.3.0]: " + changelogRepo + "compare/core-v0.2.0...core-v0.3.0\n",
			"[0.2.0]: " + changelogRepo + "compare/core-v0.1.0...core-v0.2.0\n",
			"[0.1.0]: " + changelogRepo + "releases/tag/core-v0.1.0\n",
			"[Unreleased]: " + changelogRepo + "compare/core-v0.3.0...HEAD\n",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("CHANGELOG.md lacks %q:\n%s", want, log)
			}
		}
		if strings.Contains(log, "[0.1.1]:") {
			t.Errorf("CHANGELOG.md links 0.1.1, whose tag is not public:\n%s", log)
		}
		if !strings.Contains(log, "never tagged on the public repository") {
			t.Errorf("CHANGELOG.md lost its intro:\n%s", log)
		}
		if problems := changelogProblems(log, publicTags(read(t, dir, publicTagsFile))); len(problems) != 0 {
			t.Errorf("changelogProblems(prepared changelog) = %q, want none", problems)
		}
	})

	t.Run("the version being prepared gets its section and a link from the newest public tag", func(t *testing.T) {
		t.Parallel()
		remote := newRemote("core-v0.1.0", "core-v0.2.0", "core-v0.3.0")
		dir, stderr, exit := run(t, remote, "0.4.0", "core-v0.1.0\n", prepareChangelog)
		if exit != 0 {
			t.Fatalf("prepare-release.sh 0.4.0 exit = %d, want 0; stderr %q", exit, stderr)
		}
		log := read(t, dir, "CHANGELOG.md")
		for _, want := range []string{
			"## [Unreleased]\n\n## [0.4.0] - 2026-04-01\n\n### Fixed\n\n- A fix.\n",
			"[0.4.0]: " + changelogRepo + "compare/core-v0.3.0...core-v0.4.0\n",
			"[0.3.0]: " + changelogRepo + "compare/core-v0.2.0...core-v0.3.0\n",
			"[Unreleased]: " + changelogRepo + "compare/core-v0.4.0...HEAD\n",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("CHANGELOG.md lacks %q:\n%s", want, log)
			}
		}
		if got := publicTags(read(t, dir, publicTagsFile)); !slices.Equal(got, []string{"core-v0.1.0", "core-v0.2.0", "core-v0.3.0"}) {
			t.Errorf("tag list = %q, want 0.1.0, 0.2.0 and 0.3.0", got)
		}
		if problems := changelogProblems(log, publicTags(read(t, dir, publicTagsFile))); len(problems) != 0 {
			t.Errorf("changelogProblems(prepared changelog) = %q, want none", problems)
		}
	})

	for _, c := range []struct {
		name   string
		remote func() string
		reason string
	}{
		{"an unreadable remote", func() string { return filepath.Join(t.TempDir(), "no-such-repository") }, "does not appear to be a git repository"},
		{"an answer with no core tag", func() string { return newRemote("vscode/v0.9.0") }, "no core-v tag"},
	} {
		t.Run("fails and writes nothing for "+c.name, func(t *testing.T) {
			t.Parallel()
			remote := c.remote()
			list := "core-v0.1.0\n"
			dir, stderr, exit := run(t, remote, "0.3.0", list, prepareChangelog)
			if exit != 1 {
				t.Errorf("prepare-release.sh exit = %d, want 1", exit)
			}
			if lines := strings.Split(strings.TrimSpace(stderr), "\n"); len(lines) != 1 ||
				!strings.Contains(lines[0], "JairJosafath/cel-lens") || !strings.Contains(lines[0], remote) || !strings.Contains(lines[0], c.reason) {
				t.Errorf("prepare-release.sh stderr = %q, want one line naming the repository, %q and %q", stderr, remote, c.reason)
			}
			if got := read(t, dir, publicTagsFile); got != list {
				t.Errorf("tag list = %q, want it unchanged", got)
			}
			if got := read(t, dir, "CHANGELOG.md"); got != prepareChangelog {
				t.Errorf("CHANGELOG.md changed:\n%s", got)
			}
		})
	}

	t.Run("bad usage", func(t *testing.T) {
		t.Parallel()
		_, _, exit := run(t, newRemote("core-v0.1.0"), "v1", "core-v0.1.0\n", prepareChangelog)
		if exit != 2 {
			t.Errorf("prepare-release.sh v1 exit = %d, want 2", exit)
		}
	})
}
