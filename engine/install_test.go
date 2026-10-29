package main

import (
	"archive/zip"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// modulePath is the module path in go.mod.
const modulePath = "github.com/JairJosafath/cel-lens/engine"

// writeModuleProxy writes the module at dir as version into root, in the
// layout of a module proxy: the list, .info, .mod and .zip files under @v. Only the
// files of the module go into the zip, as in a real release.
func writeModuleProxy(t *testing.T, root, dir, version string) {
	t.Helper()
	v := filepath.Join(root, filepath.FromSlash(escapePath(modulePath)), "@v")
	if err := os.MkdirAll(v, 0o750); err != nil {
		t.Fatal(err)
	}
	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for ext, content := range map[string][]byte{
		".info": []byte(`{"Version":"` + version + `"}`),
		".mod":  gomod,
	} {
		if err := os.WriteFile(filepath.Join(v, version+ext), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(v, "list"), []byte(version+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(v, version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	zw := zip.NewWriter(out)
	prefix := modulePath + "@" + version + "/"
	err = fs.WalkDir(os.DirFS(dir), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", ".gotmp", "bin", "dist": // not part of a tagged module; .gotmp changes while tests build
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel), "go.mod")); err == nil && rel != "." {
				return filepath.SkipDir // another module
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := fs.ReadFile(os.DirFS(dir), rel)
		if err != nil {
			return err
		}
		w, err := zw.Create(prefix + rel)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestGoInstallFromProxy installs the module the way go install does, from a
// file-based module proxy with no network, and checks that the installed
// program reports the version it was installed at.
func TestGoInstallFromProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("installing builds every dependency from a fresh module cache")
	}
	const version = "v1.2.3"
	proxy := t.TempDir()
	writeModuleProxy(t, proxy, ".", version)

	// The dependencies come from the module cache's download directory, which
	// is itself a proxy tree; no network is used.
	cache, err := exec.CommandContext(t.Context(), "go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	deps := filepath.Join(strings.TrimSpace(string(cache)), "cache", "download")

	bin := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "go", "install", modulePath+"@"+version)
	cmd.Dir = t.TempDir() // outside the checkout, so its go.mod is not used
	cmd.Env = append(os.Environ(),
		"GOPROXY="+fileURL(proxy)+","+fileURL(deps),
		"GOSUMDB=off",
		"GOFLAGS=-modcacherw -trimpath", // trimpath keeps the build cache valid across the temporary module cache
		"GOTOOLCHAIN=local",
		"GOMODCACHE="+t.TempDir(),
		"GOBIN="+bin,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go install %s@%s = %v\n%s", modulePath, version, err, out)
	}

	out, err := exec.CommandContext(t.Context(), filepath.Join(bin, "engine"), "--version").Output() //nolint:gosec // the binary this test installed
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != name || fields[1] != version {
		t.Errorf("installed %s --version = %q, want %s %s", name, out, name, version)
	}
}

// fileURL returns the file URL of the absolute path p.
func fileURL(p string) string {
	return "file://" + filepath.ToSlash(p)
}

// escapePath returns the module path as a proxy spells it in URLs: each capital
// letter becomes ! and its lower-case letter.
func escapePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
