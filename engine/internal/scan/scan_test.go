package scan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/scan"
)

const (
	cleanCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            a:
              type: integer
          x-kubernetes-validations:
            - rule: "self.a > 0"
`
	badCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            a:
              type: integer
          x-kubernetes-validations:
            - rule: "self.b > 0"
`
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		files     map[string]string
		paths     []string // relative to the temp dir
		wantFound bool
		wantErr   bool
		wantLines []string // prefixes of the text lines, with the dir as {dir}
	}{
		{
			name:  "clean file",
			files: map[string]string{"ok.yaml": cleanCRD},
			paths: []string{"ok.yaml"},
		},
		{
			name:      "file with errors",
			files:     map[string]string{"bad.yaml": badCRD},
			paths:     []string{"bad.yaml"},
			wantFound: true,
			wantLines: []string{"{dir}/bad.yaml:13:"},
		},
		{
			name: "folder with mixed files",
			files: map[string]string{
				"a/ok.yml":      cleanCRD,
				"a/b/bad.yaml":  badCRD,
				"a/notes.txt":   badCRD,
				"a/other.YAML":  cleanCRD,
				"a/b/also.yaml": badCRD,
			},
			paths:     []string{"a"},
			wantFound: true,
			wantLines: []string{"{dir}/a/b/also.yaml:13:", "{dir}/a/b/bad.yaml:13:"},
		},
		{
			name:    "missing path",
			paths:   []string{"nope"},
			wantErr: true,
		},
		{
			name:      "missing path does not stop the others",
			files:     map[string]string{"bad.yaml": badCRD},
			paths:     []string{"nope", "bad.yaml"},
			wantFound: true,
			wantErr:   true,
			wantLines: []string{"{dir}/bad.yaml:13:"},
		},
		{
			name:      "malformed yaml is reported and is a read error",
			files:     map[string]string{"m.yaml": "a: [\n"},
			paths:     []string{"m.yaml"},
			wantFound: true,
			wantErr:   true,
			wantLines: []string{"{dir}/m.yaml:1:1: error: YAML is broken here: "},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for p, c := range tt.files {
				write(t, filepath.Join(dir, p), c)
			}
			paths := make([]string, len(tt.paths))
			for i, p := range tt.paths {
				paths[i] = filepath.Join(dir, p)
			}
			var out bytes.Buffer
			found, err := scan.Run(t.Context(), paths, scan.Text, &out)
			if found != tt.wantFound || (err != nil) != tt.wantErr {
				t.Errorf("Run(%q) = %v, %v, want found %v, error %v", tt.paths, found, err, tt.wantFound, tt.wantErr)
			}
			var lines []string
			if s := strings.TrimSuffix(out.String(), "\n"); s != "" {
				lines = strings.Split(s, "\n")
			}
			if len(lines) != len(tt.wantLines) {
				t.Fatalf("Run(%q) wrote %q, want %d lines", tt.paths, out.String(), len(tt.wantLines))
			}
			for i, l := range lines {
				want := strings.ReplaceAll(tt.wantLines[i], "{dir}", dir)
				if !strings.HasPrefix(l, want) || !strings.Contains(l, ": error: ") {
					t.Errorf("Run(%q) line %d = %q, want prefix %q and severity error", tt.paths, i, l, want)
				}
			}
		})
	}
}

func TestRunMissingPathIsNotExist(t *testing.T) {
	t.Parallel()
	_, err := scan.Run(t.Context(), []string{filepath.Join(t.TempDir(), "nope")}, scan.Text, &bytes.Buffer{})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Run(missing) error = %v, want fs.ErrNotExist", err)
	}
}

func TestRunJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	ok := filepath.Join(dir, "ok.yaml")
	write(t, bad, badCRD)
	write(t, ok, cleanCRD)
	var out bytes.Buffer
	found, err := scan.Run(t.Context(), []string{bad}, scan.JSON, &out)
	if !found || err != nil {
		t.Fatalf("Run(json) = %v, %v, want true, nil", found, err)
	}
	var got []struct {
		File     string
		Start    struct{ Line, Column int }
		End      struct{ Line, Column int }
		Severity string
		Message  string
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("Run(json) wrote %q: %v", out.String(), err)
	}
	if len(got) != 1 || got[0].File != bad || got[0].Severity != "error" || got[0].Message == "" ||
		got[0].Start.Line != 13 || got[0].End.Line != 13 || got[0].End.Column-got[0].Start.Column != 1 {
		t.Errorf("Run(json) = %+v, want one error on line 13 ending one column after its start (the field b)", got)
	}

	out.Reset()
	if _, err := scan.Run(t.Context(), []string{ok}, scan.JSON, &out); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("Run(json, clean) = %q, %v, want [] and nil", out.String(), err)
	}
}

func TestRunFormat(t *testing.T) {
	t.Parallel()
	_, err := scan.Run(t.Context(), []string{"x"}, "xml", &bytes.Buffer{})
	if !errors.Is(err, scan.ErrFormat) {
		t.Errorf("Run(xml) error = %v, want ErrFormat", err)
	}
}

func TestRunCancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "bad.yaml"), badCRD)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := scan.Run(ctx, []string{dir}, scan.Text, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Run(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestRunJSONFixes(t *testing.T) {
	t.Parallel()
	path := "../../pkg/check/testdata/crd-cost-name.yaml"
	var out bytes.Buffer
	if _, err := scan.Run(t.Context(), []string{path}, scan.JSON, &out); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Fixes []struct{ FieldPath, Limit string }
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("Run(json) wrote %q: %v", out.String(), err)
	}
	want := "[{[{spec.entries maxItems} {spec.entries[*].name maxLength}]}]"
	if s := fmt.Sprint(got); s != want {
		t.Errorf("Run(json) fixes = %s, want %s", s, want)
	}
}

// failDirFS is a file system in which reading the folder fail fails, whoever
// runs the test.
type failDirFS struct {
	fs.FS
	fail string
}

func (f failDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.fail {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return fs.ReadDir(f.FS, name)
}

func TestRunUnreadableSubfolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a", "bad.yaml"), badCRD)
	write(t, filepath.Join(dir, "z", "bad.yaml"), badCRD)
	locked := filepath.Join(dir, "m", "locked")
	write(t, filepath.Join(locked, "hidden.yaml"), badCRD)
	dirFS := func(d string) fs.FS { return failDirFS{FS: os.DirFS(d), fail: "m/locked"} }

	var out bytes.Buffer
	found, err := scan.RunFS(t.Context(), dirFS, []string{dir}, scan.Text, &out)
	if !found {
		t.Errorf("Run(%q) found = false, want true", dir)
	}
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), locked) {
		t.Errorf("Run(%q) error = %v, want a permission error naming %s", dir, err, locked)
	}
	for _, want := range []string{filepath.Join(dir, "a", "bad.yaml"), filepath.Join(dir, "z", "bad.yaml")} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Run(%q) output = %q, want a diagnostic for %s", dir, out.String(), want)
		}
	}
	if strings.Contains(out.String(), "hidden.yaml") {
		t.Errorf("Run(%q) output = %q, want nothing from the unreadable folder", dir, out.String())
	}
}

// TestRunTypedWarning checks that a field the matched kind lacks is printed as
// a warning, with the language server's message, and does not fail the run.
func TestRunTypedWarning(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	write(t, path, `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
spec:
  matchConstraints:
    resourceRules:
      - apiGroups: [apps]
        apiVersions: [v1]
        resources: [deployments]
  validations:
    - expression: "object.additionalContainers == null"
`)
	var out bytes.Buffer
	found, err := scan.Run(t.Context(), []string{path}, scan.Text, &out)
	want := path + ":10:27: warning: apps/v1 Deployment: undefined field 'additionalContainers'\n"
	if found || err != nil || out.String() != want {
		t.Errorf("Run(policy) = %v, %v, %q, want false, nil, %q", found, err, out.String(), want)
	}
}
