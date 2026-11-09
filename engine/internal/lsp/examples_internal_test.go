package lsp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
)

// TestExamplesLimits checks that the search for examples stops at the file
// cap of the workspace scan, and lists at most maxExamples documents (#17).
func TestExamplesLimits(t *testing.T) {
	t.Parallel()
	const policy = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: limits}\nspec:\n  paramKind: {apiVersion: example.com/v1, kind: Limit}\n"
	const limit = "apiVersion: example.com/v1\nkind: Limit\n"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		fileCap int
		want    []string
	}{
		{"file cap", map[string]string{"a.yaml": limit, "b.yaml": limit, "c.yaml": limit}, 2, []string{"a.yaml", "b.yaml"}},
		{"example cap", map[string]string{"a.yaml": strings.Repeat(limit+"---\n", maxExamples+1)}, 0, slices.Repeat([]string{"a.yaml"}, maxExamples)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tc.files["z-policy.yaml"] = policy
			for name, text := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ss := newSession(t)
			ss.folders = []string{dir}
			ss.fileCap = tc.fileCap
			v := valueRef{
				lens: crdKey(pathURI(filepath.Join(dir, cellens.FileName))), file: crdKey(pathURI(filepath.Join(dir, "z-policy.yaml"))),
				entry: cellens.Entry{Document: cellens.Document{Kind: "ValidatingAdmissionPolicy", Name: "limits"}, Variable: "params"},
			}
			got, err := ss.exampleSearch(context.Background(), v).run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			files := make([]string, len(got))
			for i, ex := range got {
				files[i] = ex.File
			}
			if strings.Join(files, " ") != strings.Join(tc.want, " ") {
				t.Errorf("examples with file cap %d = %v, want %v", tc.fileCap, files, tc.want)
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		ss := newSession(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := ss.exampleSearch(ctx, valueRef{entry: cellens.Entry{Variable: "params"}}).run(ctx); err == nil {
			t.Error("examples with a cancelled context error = nil, want the context's")
		}
	})
}
