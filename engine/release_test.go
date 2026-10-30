package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestDist checks that the Makefile lists all six release platforms, then runs
// make dist for the host and one foreign platform and checks the release files:
// names, checksums, static builds and the version the binary prints. It
// cross-compiles twice, so it runs only when CEL_LENS_DIST_TEST is set; CI sets
// it through make test-dist.
func TestDist(t *testing.T) {
	if os.Getenv("CEL_LENS_DIST_TEST") == "" {
		t.Skip("builds binaries; set CEL_LENS_DIST_TEST=1 or run make test-dist")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	const version = "v9.8.7"
	host := runtime.GOOS + "/" + runtime.GOARCH
	foreign := "windows/arm64"
	if host == foreign {
		foreign = "linux/amd64"
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// dist/ is the Makefile's fixed output; work in a copy of the build inputs.
	work := t.TempDir()
	if err := copyBuildInputs(t.Context(), root, work); err != nil {
		t.Fatalf("copy sources: %v", err)
	}
	// The real platform list: dropping one from the Makefile fails here.
	dry := exec.CommandContext(t.Context(), "make", "-n", "dist", "VERSION="+version)
	dry.Dir = work
	dryOut, err := dry.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n dist: %v\n%s", err, dryOut)
	}
	for _, p := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"} {
		if !bytes.Contains(dryOut, []byte(p)) {
			t.Errorf("make -n dist does not build %s", p)
		}
	}
	//nolint:gosec // G204: fixed test inputs
	cmd := exec.CommandContext(t.Context(), "make", "dist", "VERSION="+version, "PLATFORMS="+host+" "+foreign)
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make dist: %v\n%s", err, out)
	}

	dist := filepath.Join(work, "dist")
	wantNames := map[string]bool{}
	for _, p := range []string{host, foreign} {
		osName, arch, _ := strings.Cut(p, "/")
		n := "cel-lens-core_" + version + "_" + osName + "_" + arch
		if osName == "windows" {
			n += ".exe"
		}
		wantNames[n] = true
	}
	wantNames["checksums.txt"] = true
	entries, err := os.ReadDir(dist)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !wantNames[e.Name()] {
			t.Errorf("dist/ has unexpected file %s", e.Name())
		}
		delete(wantNames, e.Name())
	}
	for n := range wantNames {
		t.Errorf("dist/ lacks %s", n)
	}

	list, err := os.ReadFile(filepath.Join(dist, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sums := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(list))
	for sc.Scan() {
		sum, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			t.Fatalf("checksums.txt line %q is not '<sha256>  <name>'", sc.Text())
		}
		sums[name] = sum
	}
	for _, e := range entries {
		if e.Name() == "checksums.txt" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dist, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(data)
		if got := sums[e.Name()]; got != hex.EncodeToString(h[:]) {
			t.Errorf("checksums.txt[%s] = %q, want the SHA-256 of the file", e.Name(), got)
		}
		info, err := buildinfo.ReadFile(filepath.Join(dist, e.Name()))
		if err != nil {
			t.Errorf("read build info of %s: %v", e.Name(), err)
			continue
		}
		static := false
		for _, kv := range info.Settings {
			if kv.Key == "CGO_ENABLED" && kv.Value == "0" {
				static = true
			}
		}
		if !static {
			t.Errorf("%s was not built with CGO_ENABLED=0", e.Name())
		}
	}

	hostBin := filepath.Join(dist, "cel-lens-core_"+version+"_"+strings.ReplaceAll(host, "/", "_"))
	if runtime.GOOS == "windows" {
		hostBin += ".exe"
	}
	//nolint:gosec // G204: fixed test inputs
	out, err := exec.CommandContext(t.Context(), hostBin, "--version").Output()
	if err != nil {
		t.Fatalf("%s --version: %v", hostBin, err)
	}
	if got, want := strings.TrimSpace(string(out)), "cel-lens-core "+version+" (matches Kubernetes 1.37; supports 1.33–1.37)"; got != want {
		t.Errorf("--version = %q, want %q", got, want)
	}
}

// copyBuildInputs copies the files git tracks in src into dst, so untracked and
// ignored files, and the build output directories, cannot change a build in dst.
func copyBuildInputs(ctx context.Context, src, dst string) error {
	files, err := trackedFiles(ctx, src)
	if err != nil {
		return err
	}
	return copyTracked(files, src, dst)
}

// TestCopyBuildInputs checks that the copy TestDist builds in holds tracked
// files and leaves out an untracked one.
func TestCopyBuildInputs(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not on PATH: %v", err)
	}
	src := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"add", "tracked.txt"}} {
		if args[0] == "init" {
			if err := os.WriteFile(filepath.Join(src, "tracked.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // G204: fixed test inputs
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "untracked.txt"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := copyBuildInputs(t.Context(), src, dst); err != nil {
		t.Fatalf("copyBuildInputs(%s) = %v", src, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "tracked.txt")); err != nil {
		t.Errorf("tracked.txt is not in the copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "untracked.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("untracked.txt in the copy: Stat error = %v, want not exist", err)
	}
}

// TestChangelogHeadings checks that every released section of CHANGELOG.md has
// a heading of the form "## [X.Y.Z] - YYYY-MM-DD" with a real calendar date,
// that each has a "[X.Y.Z]:" link definition, and that "[Unreleased]:" compares
// the newest release to HEAD.
func TestChangelogHeadings(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range changelogProblems(string(data)) {
		t.Error(p)
	}
}

// TestChangelogProblems checks that changelogProblems reports a missing or wrong version
// link and a stale [Unreleased] link.
func TestChangelogProblems(t *testing.T) {
	t.Parallel()
	const good = "## [Unreleased]\n\n## [1.1.0] - 2026-01-02\n\n## [1.0.0] - 2026-01-01\n\n" +
		"[Unreleased]: ../../compare/v1.1.0...HEAD\n[1.1.0]: ../../compare/v1.0.0...v1.1.0\n[1.0.0]: ../../releases/tag/v1.0.0\n"
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"valid", good, 0},
		{"missing version link", strings.Replace(good, "[1.1.0]: ../../compare/v1.0.0...v1.1.0\n", "", 1), 1},
		{"stale unreleased link", strings.Replace(good, "v1.1.0...HEAD", "v1.0.0...HEAD", 1), 1},
		{"unreleased not to HEAD", strings.Replace(good, "v1.1.0...HEAD", "v1.1.0...v1.1.0", 1), 1},
		{"missing unreleased link", strings.Replace(good, "[Unreleased]: ../../compare/v1.1.0...HEAD\n", "", 1), 1},
		{"copied compare link", strings.Replace(good, "[1.1.0]: ../../compare/v1.0.0...v1.1.0", "[1.1.0]: ../../compare/v0.9.0...v1.0.0", 1), 1},
		{"compare ends at another version", strings.Replace(good, "v1.0.0...v1.1.0", "v1.0.0...v1.2.0", 1), 1},
		{"oldest compares", strings.Replace(good, "../../releases/tag/v1.0.0", "../../compare/v0.9.0...v1.0.0", 1), 1},
		{"no releases", "## [Unreleased]\n", 1},
		{"repeated heading in unreleased", "## [Unreleased]\n\n### Changed\n\n### Changed\n\n" + good[len("## [Unreleased]\n\n"):], 1},
		{"headings out of order in newest release", strings.Replace(good, "## [1.1.0] - 2026-01-02\n", "## [1.1.0] - 2026-01-02\n\n### Fixed\n\n### Added\n", 1), 1},
		{"older release may differ", strings.Replace(good, "## [1.0.0] - 2026-01-01\n", "## [1.0.0] - 2026-01-01\n\n### Fixed\n\n### Added\n", 1), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := changelogProblems(tt.in); len(got) != tt.want {
				t.Errorf("changelogProblems(%s) = %q, want %d problems", tt.name, got, tt.want)
			}
		})
	}
}

// changelogProblems returns one message for each problem in the changelog text:
// a malformed heading or date, a released version without a link definition or whose link does not compare
// the previous release to it (the oldest links to its release tag), or
// an [Unreleased] link that does not compare the newest release to HEAD. It also
// reports a repeated change-type heading, or one out of Keep a Changelog order,
// in the [Unreleased] section and the newest release.
func changelogProblems(text string) []string {
	heading := regexp.MustCompile(`^## \[(\d+\.\d+\.\d+)\] - (\d{4}-\d{2}-\d{2})$`)
	link := regexp.MustCompile(`^\[([^\]]+)\]: (\S+)$`)
	links := map[string]string{}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if m := link.FindStringSubmatch(line); m != nil {
			links[m[1]] = m[2]
		}
	}
	var problems []string
	var versions []string
	order := []string{"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"}
	sections := 0 // sections seen so far; only the first two are checked
	last := -1    // index in order of the previous change-type heading of this section
	for i, line := range lines {
		if strings.HasPrefix(line, "## [") {
			sections++
			last = -1
		}
		if name, ok := strings.CutPrefix(line, "### "); ok && sections <= 2 {
			at := slices.Index(order, name)
			switch {
			case at < 0:
				problems = append(problems, fmt.Sprintf("CHANGELOG.md:%d: heading %q is not a Keep a Changelog change type", i+1, line))
			case at == last:
				problems = append(problems, fmt.Sprintf("CHANGELOG.md:%d: heading %q repeats in its section", i+1, line))
			case at < last:
				problems = append(problems, fmt.Sprintf("CHANGELOG.md:%d: heading %q is out of order, want %s", i+1, line, strings.Join(order, ", ")))
			}
			last = max(last, at)
		}
		if !strings.HasPrefix(line, "## [") || strings.HasPrefix(line, "## [Unreleased]") {
			continue
		}
		m := heading.FindStringSubmatch(line)
		if m == nil {
			problems = append(problems, fmt.Sprintf("CHANGELOG.md:%d: heading %q is not \"## [X.Y.Z] - YYYY-MM-DD\"", i+1, line))
			continue
		}
		versions = append(versions, m[1])
		if _, err := time.Parse(time.DateOnly, m[2]); err != nil {
			problems = append(problems, fmt.Sprintf("CHANGELOG.md:%d: date %q of %s: %v", i+1, m[2], m[1], err))
		}
		if _, ok := links[m[1]]; !ok {
			problems = append(problems, fmt.Sprintf("CHANGELOG.md:%d: no \"[%s]:\" link definition for the heading", i+1, m[1]))
		}
	}
	if len(versions) == 0 {
		return append(problems, "CHANGELOG.md has no released sections")
	}
	for i, v := range versions {
		got, ok := links[v]
		if !ok {
			continue
		}
		want := "../../releases/tag/v" + v
		if i+1 < len(versions) {
			want = "../../compare/v" + versions[i+1] + "...v" + v
		}
		if got != want {
			problems = append(problems, fmt.Sprintf("CHANGELOG.md: [%s]: %s, want %s", v, got, want))
		}
	}
	want := "../../compare/v" + versions[0] + "...HEAD"
	if got, ok := links["Unreleased"]; !ok {
		problems = append(problems, "CHANGELOG.md has no \"[Unreleased]:\" link definition, want "+want)
	} else if got != want {
		problems = append(problems, fmt.Sprintf("CHANGELOG.md: [Unreleased]: %s, want %s", got, want))
	}
	return problems
}
