package main

import (
	"os"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestGitignoreListsGoTmp(t *testing.T) {
	t.Parallel()
	// Go skips directories starting with "." or "_" as packages.
	for _, line := range strings.Split(readFile(t, ".gitignore"), "\n") {
		if line == "/.gotmp/" {
			return
		}
	}
	t.Error(".gitignore does not list /.gotmp/")
}
