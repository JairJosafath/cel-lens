package main

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestLicenseIsApache2(t *testing.T) {
	t.Parallel()
	s := readFile(t, "LICENSE")
	for _, want := range []string{"Apache License", "Version 2.0, January 2004", "END OF TERMS AND CONDITIONS"} {
		if !strings.Contains(s, want) {
			t.Errorf("LICENSE lacks %q", want)
		}
	}
}

func TestReadmeForContributors(t *testing.T) {
	t.Parallel()
	s := readFile(t, "README.md")
	for _, want := range []string{
		"Apache-2.0",
		"(LICENSE)",
		"https://github.com/JairJosafath/cel-lens/issues",
		"cel-lens-core lsp",
		"cel-lens-core check",
		"make build",
		"make check",
		"go.dev",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("README.md lacks %q", want)
		}
	}
	for _, bad := range []string{"devcontainer", "image"} {
		if strings.Contains(strings.ToLower(s), bad) {
			t.Errorf("README.md names %q", bad)
		}
	}
}

// TestNoFactoryAddresses fails when a file a user or contributor sees names
// the factory's own Gitea host or a c4factory address. The factory's CI
// files and the protected Standard files are the factory's own.
func TestNoFactoryAddresses(t *testing.T) {
	t.Parallel()
	needles := []string{"c4factory", "gitea" + ".gitea", "gitea" + "-url", "gitea" + ".svc", "<gitea"}
	skipDirs := map[string]bool{".git": true, ".gitea": true, ".devcontainer": true, ".gotmp": true, "bin": true, "dist": true, "agents": true, "style": true}
	skipFiles := map[string]bool{".c4factory.yaml": true, "AGENTS.md": true, "license_test.go": true}
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if skipFiles[d.Name()] {
			return nil
		}
		b, err := fs.ReadFile(root.FS(), path)
		if err != nil {
			return err
		}
		low := strings.ToLower(string(b))
		for _, n := range needles {
			if strings.Contains(low, n) {
				t.Errorf("%s names the factory (%q)", path, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
