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

// readFactoryFile reads a file that only the factory's checkout has, and skips
// the test when the published source does not contain it.
func readFactoryFile(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("%s is the factory's and absent from the published source", path)
	}
	return readFile(t, path)
}

func TestWorkflowsKeepGoTempOutOfTmp(t *testing.T) {
	t.Parallel()
	for _, path := range []string{".gitea/workflows/check.yaml", ".gitea/workflows/release.yaml"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			s := readFactoryFile(t, path)
			if !strings.Contains(s, "GOTMPDIR: ${{ gitea.workspace }}/.gotmp") {
				t.Errorf("%s does not set GOTMPDIR to .gotmp in the workspace", path)
			}
			mk := strings.Index(s, `mkdir -p "$GOTMPDIR"`)
			build := strings.Index(s, "run: make ")
			if mk < 0 || mk > build {
				t.Errorf("%s must create GOTMPDIR before its first make step", path)
			}
			if strings.Contains(s, "rm ") && strings.Contains(s, "/tmp") {
				t.Errorf("%s must not delete files in /tmp", path)
			}
			if strings.Contains(s, "GOCACHE") {
				t.Errorf("%s must leave GOCACHE alone", path)
			}
		})
	}
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

func TestCheckWorkflowBuildsForWindows(t *testing.T) {
	t.Parallel()
	workflow := readFactoryFile(t, ".gitea/workflows/check.yaml")
	makefile := readFile(t, "Makefile")
	for _, c := range []struct{ name, step, rule string }{
		{"vet", "run: make vet-windows", "GOOS=windows go vet ./..."},
		{"test binaries", "run: make test-windows", "GOOS=windows go test -c"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(workflow, c.step) {
				t.Errorf("check.yaml does not run %q", c.step)
			}
			if !strings.Contains(makefile, c.rule) {
				t.Errorf("Makefile does not run %q", c.rule)
			}
		})
	}
}

func TestCheckWorkflowSkipsHeavyStepsForDocsOnly(t *testing.T) {
	t.Parallel()
	s := readFactoryFile(t, ".gitea/workflows/check.yaml")
	if !strings.Contains(s, `scripts/changes.sh docs-only "$BASE"`) {
		t.Error("check.yaml does not ask scripts/changes.sh docs-only")
	}
	for _, step := range []string{"run: make check", "run: make vet-windows", "run: make test-windows", "run: make test-dist"} {
		i := strings.Index(s, step)
		if i < 0 {
			t.Errorf("check.yaml does not run %q", step)
			continue
		}
		before := s[:i]
		if j := strings.LastIndex(before, "- name:"); !strings.Contains(before[j:], "steps.changes.outputs.code == 'true'") {
			t.Errorf("check.yaml runs %q even for a docs-only change", step)
		}
	}
}
