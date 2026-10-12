package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	if err := copyTree(root, work); err != nil {
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

// copyTree copies the regular files under src into dst, leaving out .git and
// the build output directories.
func copyTree(src, dst string) error {
	fsys := os.DirFS(src)
	return fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch rel {
			case ".git", ".gotmp", "dist", "bin":
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o750)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o600)
	})
}

// TestChangelogHeadings checks that every released section of CHANGELOG.md has
// a heading of the form "## [X.Y.Z] - YYYY-MM-DD" with a real calendar date.
func TestChangelogHeadings(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	heading := regexp.MustCompile(`^## \[(\d+\.\d+\.\d+)\] - (\d{4}-\d{2}-\d{2})$`)
	released := 0
	for i, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "## [") || strings.HasPrefix(line, "## [Unreleased]") {
			continue
		}
		released++
		m := heading.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("CHANGELOG.md:%d: heading %q is not \"## [X.Y.Z] - YYYY-MM-DD\"", i+1, line)
			continue
		}
		if _, err := time.Parse(time.DateOnly, m[2]); err != nil {
			t.Errorf("CHANGELOG.md:%d: date %q of %s: %v", i+1, m[2], m[1], err)
		}
	}
	if released == 0 {
		t.Error("CHANGELOG.md has no released sections")
	}
}
