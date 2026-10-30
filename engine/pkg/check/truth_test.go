package check_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// truthCase is one file of testdata/truth: a policy and the lines of
// status.typeChecking the API server wrote for it.
type truthCase struct {
	Name         string   `yaml:"name"`
	Version      string   `yaml:"version"`
	Divergence   string   `yaml:"divergence"`
	Policy       string   `yaml:"policy"`
	TypeChecking []string `yaml:"typeChecking"`
}

// truthLine reads "<group/version>, Kind=<Kind>: ERROR: <input>:<line>:<col>: <message>".
var truthLine = regexp.MustCompile(`^(\S+), Kind=(\S+): ERROR: <input>:\d+:\d+: (.+)$`)

// truthFailures runs every file of fsys and returns one failure per difference.
func truthFailures(ctx context.Context, fsys fs.FS) []string {
	files, err := fs.Glob(fsys, "*.y*ml")
	if err != nil {
		return []string{fmt.Sprintf("list truth cases: %v", err)}
	}
	if len(files) == 0 {
		return []string{"no truth cases found"}
	}
	var out []string
	for _, file := range files {
		out = append(out, truthFileFailures(ctx, fsys, file)...)
	}
	return out
}

func truthFileFailures(ctx context.Context, fsys fs.FS, file string) []string {
	data, err := fs.ReadFile(fsys, file)
	if err != nil {
		return []string{fmt.Sprintf("%s: read: %v", file, err)}
	}
	var c truthCase
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return []string{fmt.Sprintf("%s: parse: %v", file, err)}
	}
	if c.Name == "" || c.Policy == "" || len(c.TypeChecking) == 0 {
		return []string{fmt.Sprintf("%s: name, policy and typeChecking are required", file)}
	}
	// The recorded version is v1.37.0; the engine's versions are minors.
	parts := strings.SplitN(strings.TrimPrefix(c.Version, "v"), ".", 3)
	v, err := kubeversion.Parse(strings.Join(parts[:min(len(parts), 2)], "."))
	if err != nil {
		return []string{fmt.Sprintf("%s: case %s: version %q: %v", file, c.Name, c.Version, err)}
	}
	diags, err := check.Check(ctx, path.Base(file), []byte(c.Policy), check.WithVersion(v), allOn())
	if err != nil {
		return []string{fmt.Sprintf("%s: case %s: check: %v", file, c.Name, err)}
	}
	var warnings []string
	for _, d := range diags {
		if d.Severity == check.SeverityWarning {
			warnings = append(warnings, d.Message)
		}
	}
	var out []string
	if len(warnings) != len(c.TypeChecking) {
		out = append(out, fmt.Sprintf("case %s: %d warnings, want %d; engine warnings: %q", c.Name, len(warnings), len(c.TypeChecking), warnings))
	}
	for _, line := range c.TypeChecking {
		m := truthLine.FindStringSubmatch(line)
		if m == nil {
			out = append(out, fmt.Sprintf("case %s: recorded line %q is not in the form '<group/version>, Kind=<Kind>: ERROR: <input>:<line>:<col>: <message>'", c.Name, line))
			continue
		}
		want := strings.TrimPrefix(m[1], "/") + " " + m[2] + ": " + m[3]
		n := 0
		for _, w := range warnings {
			if rest, ok := strings.CutPrefix(w, want); ok && (rest == "" || rest[0] == ' ') {
				n++
			}
		}
		if n != 1 {
			out = append(out, fmt.Sprintf("case %s: recorded line %q matches %d warnings, want 1 starting %q; engine warnings: %q", c.Name, line, n, want, warnings))
		}
	}
	if c.Divergence != "" {
		// A known divergence stays in the set so the day the engine agrees
		// with the API server, the test says to remove the note.
		if len(out) == 0 {
			return []string{fmt.Sprintf("case %s: the engine now agrees with the API server; remove its divergence note", c.Name)}
		}
		return nil
	}
	return out
}

// TestTruthSet checks the engine's type-check warnings against the answers
// the API server recorded in status.typeChecking.
func TestTruthSet(t *testing.T) {
	t.Parallel()
	for _, f := range truthFailures(context.Background(), os.DirFS("testdata/truth")) {
		t.Error(f)
	}
}

// TestTruthSetFailures checks that a difference from a recorded line fails.
func TestTruthSetFailures(t *testing.T) {
	t.Parallel()
	good, err := os.ReadFile("testdata/truth/vap-authorizer-request-resource.yaml")
	if err != nil {
		t.Fatal(err)
	}
	two, err := os.ReadFile("testdata/truth/vap-authorizer-request-resource-two-kinds.yaml")
	if err != nil {
		t.Fatal(err)
	}
	diverging := strings.Replace(strings.Replace(string(good), "Kind=Deployment", "Kind=StatefulSet", 1), "version:", "divergence: engine differs\nversion:", 1)
	tests := []struct {
		name, data, want string
	}{
		{"known divergence that remains", diverging, ""},
		{"same", string(good), ""},
		{"wrong kind", strings.Replace(string(good), "Kind=Deployment", "Kind=StatefulSet", 1), "recorded line"},
		{"wrong message", strings.Replace(string(good), "unexpected failed resolution of 'kubernetes.authorization.Authorizer'", "unexpected failed resolution of 'kubernetes.authorization.Other'", 1), "recorded line"},
		{"more warnings than lines", strings.Replace(string(two), "  - \"apps/v1, Kind=StatefulSet: ERROR: <input>:1:11: unexpected failed resolution of 'kubernetes.authorization.Authorizer'\"\n", "", 1), "2 warnings"},
		{"more lines than warnings", strings.Replace(string(good), "typeChecking:\n", "typeChecking:\n  - \"apps/v1, Kind=StatefulSet: ERROR: <input>:1:11: unexpected failed resolution of 'kubernetes.authorization.Authorizer'\"\n", 1), "want 2"},
		{"known divergence that is gone", strings.Replace(string(good), "version:", "divergence: engine differs\nversion:", 1), "now agrees"},
		{"not yaml", "name: [", "parse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truthFailures(context.Background(), fstest.MapFS{"case.yaml": {Data: []byte(tt.data)}})
			joined := strings.Join(got, "\n")
			if (tt.want == "") != (len(got) == 0) || !strings.Contains(joined, tt.want) {
				t.Errorf("truthFailures(%s) = %q, want a failure containing %q", tt.name, got, tt.want)
			}
		})
	}
	if got := truthFailures(context.Background(), fstest.MapFS{"dir.yaml": {Mode: fs.ModeDir}}); len(got) == 0 || !strings.Contains(got[0], "read") {
		t.Errorf("truthFailures(unreadable file) = %q, want a read failure", got)
	}
	if got := truthFailures(context.Background(), fstest.MapFS{}); len(got) == 0 {
		t.Error("truthFailures(empty folder) = no failure, want one")
	}
}
