package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// factoryPaths are the files the published source leaves out, and the
// directories a clone does not have.
var factoryPaths = []string{
	".gitea", "AGENTS.md", ".c4" + "factory.yaml", ".devcontainer", "docs/agents",
	".git", ".gotmp", "bin", "dist",
}

// TestPublicSourceTestsPass proves a contributor's clone, which lacks the
// factory's files, has a green go test ./....
func TestPublicSourceTestsPass(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(".gitea"); os.IsNotExist(err) {
		t.Skip(".gitea is the factory's and absent from the published source; this is the public copy")
	}
	if testing.Short() {
		t.Skip("copies the tree and runs every test")
	}
	src, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
	for _, p := range factoryPaths {
		if err := os.RemoveAll(filepath.Join(dst, p)); err != nil {
			t.Fatalf("remove %s from the copy: %v", p, err)
		}
	}
	cmd := exec.Command("go", "test", "-count=1", "-timeout", "30m", "./...")
	cmd.Dir = dst
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test ./... in the public copy: %v\n%s", err, out)
	}
}
