package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// examplesCRD is a CRD of widgets with one rule, on spec.size.
const examplesCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                size: {type: integer}
              x-kubernetes-validations:
                - rule: self.size <= 10
`

// examplesWidget is a Widget of size 3.
const examplesWidget = "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec: {size: 3}\n"

// deployments is n Deployments, named by their numbers.
func deployments(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: d%d}\nspec: {replicas: 1, selector: {matchLabels: {a: b}}, template: {metadata: {labels: {a: b}}, spec: {containers: [{name: c, image: i}]}}}\n---\n", i)
	}
	return b.String()
}

// failingDir is a file system whose folder sub cannot be read.
type failingDir struct{ fs.FS }

// ReadDir lists the folder name, or fails for sub.
func (f failingDir) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == "sub" {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return fs.ReadDir(f.FS, name)
}

// TestRunExamplesLimits checks what a run leaves out for the limits of the
// workspace scan, and says so in a note and in FilesLeftOut: the files past the
// cap and the files over the size limit, and that it takes more examples of a
// CRD and of a policy than the editor's 100, and what it cannot read, and
// names in its error: a folder it cannot read (#24).
func TestRunExamplesLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		files     map[string]string
		fileCap   int
		wantNotes []string
		wantLeft  int
		wantErr   string
		wantFiles []string // the files of the examples of the rule
	}{
		{
			name:      "file cap",
			files:     map[string]string{"a.yaml": examplesWidget, "b.yaml": examplesWidget, "c.yaml": examplesWidget, "d.yaml": examplesWidget, "sub/e.yaml": examplesWidget},
			fileCap:   3,
			wantNotes: []string{"3 YAML files past the first 3 are not read"},
			wantLeft:  3,
			wantFiles: []string{"a.yaml", "b.yaml"},
		},
		{
			name:      "size limit",
			files:     map[string]string{"a.yaml": examplesWidget, "big.yaml": strings.Repeat("# filler\n", maxWorkspaceFileSize/8+1)},
			wantNotes: []string{"@DIR@/big.yaml is over the limit of 1048576 bytes, so it is not read"},
			wantLeft:  1,
			wantFiles: []string{"a.yaml"},
		},
		{
			name:      "no cap on the examples of a CRD",
			files:     map[string]string{"a.yaml": strings.Repeat(examplesWidget+"---\n", maxExamples+40)},
			wantFiles: slices.Repeat([]string{"a.yaml"}, maxExamples+40),
		},
		{
			name: "no cap on the examples of a policy",
			files: map[string]string{
				"policy.yaml": "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: replicas}\nspec:\n  matchConstraints:\n    resourceRules:\n      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE], resources: [deployments]}\n  validations:\n    - expression: \"object.spec.replicas < 3\"\n",
				"d.yaml":      deployments(maxExamples + 40),
			},
			wantFiles: slices.Repeat([]string{"d.yaml"}, maxExamples+40),
		},
		{
			name:      "a folder that cannot be read",
			files:     map[string]string{"a.yaml": examplesWidget, "sub/b.yaml": examplesWidget},
			wantErr:   "read @DIR@/sub: permission denied",
			wantFiles: []string{"a.yaml"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tc.files["0crd.yaml"] = examplesCRD // the first file the walk reads
			for name, text := range tc.files {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			limit := tc.fileCap
			if limit == 0 {
				limit = maxWorkspaceFiles
			}
			dirFS := func(dir string) fs.FS { return failingDir{os.DirFS(dir)} }
			if tc.wantErr == "" {
				dirFS = os.DirFS
			}
			report, err := runExamples(t.Context(), dirFS, limit, []string{dir}, kubeversion.Default())
			if report == nil {
				t.Fatalf("runExamples error = %v, want a report", err)
			}
			if got := errString(err); tc.wantErr == "" && got != "" || !strings.Contains(got, strings.ReplaceAll(tc.wantErr, "@DIR@", dir)) {
				t.Errorf("runExamples error = %q, want one with %q", got, tc.wantErr)
			}
			var wantNotes []string
			for _, n := range tc.wantNotes {
				wantNotes = append(wantNotes, strings.ReplaceAll(n, "@DIR@", dir))
			}
			if !slices.Equal(report.Notes, wantNotes) {
				t.Errorf("runExamples notes = %q, want %q", report.Notes, wantNotes)
			}
			if report.FilesLeftOut != tc.wantLeft {
				t.Errorf("runExamples left %d files out, want %d", report.FilesLeftOut, tc.wantLeft)
			}
			var files []string
			for _, r := range report.Rules {
				for _, res := range r.Results {
					files = append(files, res.Example.File)
				}
			}
			slices.Sort(files)
			if !slices.Equal(files, tc.wantFiles) {
				t.Errorf("runExamples ran the rule on %d examples %q, want %d %q", len(files), files[:min(len(files), 3)], len(tc.wantFiles), tc.wantFiles[:min(len(tc.wantFiles), 3)])
			}
		})
	}
}

// errString is err's message, "" for nil.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestWorkspaceOf checks the workspace a list of paths makes: the folders
// without those below another, then the folder of each file that is not
// below one, in which only the files given are, and an error for each path
// that cannot be used.
func TestWorkspaceOf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a/x.yaml", "a/y.yml", "a/z.json", "a/sub/w.yaml", "b/v.yaml"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	at := func(rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
	tests := []struct {
		name        string
		paths       []string
		wantFolders []string
		wantListed  map[string]string // the files of a folder's file system, space separated
		wantErr     string
	}{
		{"a folder below another", []string{at("a/sub"), at("a")}, []string{at("a")}, map[string]string{at("a"): "sub/w.yaml x.yaml y.yml z.json"}, ""},
		{"files of one folder", []string{at("a/x.yaml"), at("a/y.yml"), at("a/x.yaml")}, []string{at("a")}, map[string]string{at("a"): "x.yaml y.yml"}, ""},
		{"a file below a folder", []string{at("a"), at("a/x.yaml"), at("b/v.yaml")}, []string{at("a"), at("b")}, map[string]string{at("b"): "v.yaml"}, ""},
		{"a folder below the folder of a file", []string{at("a/x.yaml"), at("a/sub")}, []string{at("a/sub"), at("a")}, map[string]string{at("a"): "x.yaml"}, ""},
		{"a missing path and a text file", []string{at("c.yaml"), at("a/z.json")}, nil, nil, "read " + at("c.yaml") + ": no such file or directory\nread " + at("a/z.json") + ": not a .yaml or .yml file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws, err := workspaceOf(os.DirFS, tc.paths)
			if got := errString(err); got != tc.wantErr {
				t.Fatalf("workspaceOf(%q) error = %q, want %q", tc.paths, got, tc.wantErr)
			}
			if !slices.Equal(ws.folders, tc.wantFolders) {
				t.Errorf("workspaceOf(%q) folders = %q, want %q", tc.paths, ws.folders, tc.wantFolders)
			}
			for folder, want := range tc.wantListed {
				var got []string
				_ = fs.WalkDir(ws.dirFS(folder), ".", func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if !d.IsDir() {
						got = append(got, path)
					}
					return nil
				})
				if joined := strings.Join(got, " "); joined != want {
					t.Errorf("workspaceOf(%q) walks %s as %q, want %q", tc.paths, folder, joined, want)
				}
			}
		})
	}
	t.Run("a file outside its set is not there", func(t *testing.T) {
		t.Parallel()
		ws, err := workspaceOf(os.DirFS, []string{at("a/x.yaml")})
		if err != nil {
			t.Fatal(err)
		}
		set := ws.dirFS(at("a"))
		if _, err := fs.Stat(set, "y.yml"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(y.yml) error = %v, want %v", err, fs.ErrNotExist)
		}
		if _, err := fs.ReadDir(set, "sub"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadDir(sub) error = %v, want %v", err, fs.ErrNotExist)
		}
	})
}

// TestRuleLocationKeys checks that a rule's location is written with its
// range before its URI, as it was when it was a map, whose keys encoding/json
// sorts, so that the answer of cel-lens.exampleResults stays byte for byte
// what it was.
func TestRuleLocationKeys(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(RuleResults{Location: RuleLocation{URI: "file:///a.yaml", Range: lspRange{End: position{Line: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `"location":{"range":{"start":{"line":0,"character":0},"end":{"line":1,"character":0}},"uri":"file:///a.yaml"}`
	if !strings.Contains(string(data), want) {
		t.Errorf("a rule's JSON = %s, want it to have %s", data, want)
	}
}
