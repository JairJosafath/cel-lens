package check_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
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
	Engine       []string `yaml:"engine"`
	Policy       string   `yaml:"policy"`
	TypeChecking []string `yaml:"typeChecking"`
}

// truthLine reads "<group/version>, Kind=<Kind>: ERROR: <input>:<line>:<col>: <message>".
var truthLine = regexp.MustCompile(`^(\S+), Kind=(\S+): ERROR: <input>:\d+:\d+: (.+)$`)

// truthEntries splits each recorded answer into one line per ERROR entry. An
// answer with several entries for one kind holds them on lines of its own, and
// each entry after the first (starting "ERROR: ") takes the kind of the first.
func truthEntries(answers []string) []string {
	var out []string
	for _, a := range answers {
		prefix, _, _ := strings.Cut(a, " ERROR: ")
		for i, entry := range strings.Split(a, "\n") {
			if i > 0 {
				entry = prefix + " " + entry
			}
			out = append(out, entry)
		}
	}
	return out
}

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
	if c.Name == "" || c.Policy == "" || c.TypeChecking == nil {
		return []string{fmt.Sprintf("%s: name, policy and typeChecking are required (typeChecking: [] for an answer with no line)", file)}
	}
	warnings, err := engineWarnings(ctx, path.Base(file), c)
	if err != nil {
		return []string{fmt.Sprintf("%s: case %s: %v", file, c.Name, err)}
	}
	var out []string
	malformed := false
	lines := truthEntries(c.TypeChecking)
	if len(warnings) != len(lines) {
		out = append(out, fmt.Sprintf("case %s: %d warnings, want %d; engine warnings: %q", c.Name, len(warnings), len(lines), warnings))
	}
	for _, line := range lines {
		m := truthLine.FindStringSubmatch(line)
		if m == nil {
			out = append(out, fmt.Sprintf("case %s: recorded line %q is not in the form '<group/version>, Kind=<Kind>: ERROR: <input>:<line>:<col>: <message>'", c.Name, line))
			malformed = true
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
	if c.Divergence == "" {
		if c.Engine != nil {
			out = append(out, fmt.Sprintf("case %s: engine is only for a case with a divergence note", c.Name))
		}
		return out
	}
	// A known divergence stays in the set so the day the engine agrees with
	// the API server, the test says to remove the note, and any other change
	// of the engine's answer fails until the note records the new one.
	if c.Engine == nil {
		return []string{fmt.Sprintf("case %s: a divergence note needs the engine's current warnings in engine (engine: [] when it gives none); they are %q", c.Name, warnings)}
	}
	if malformed {
		return out
	}
	if len(out) == 0 {
		return []string{fmt.Sprintf("case %s: the engine now agrees with the API server; remove its divergence note and engine list", c.Name)}
	}
	if !slices.Equal(warnings, c.Engine) {
		return []string{fmt.Sprintf("case %s: the engine's warnings changed but still differ from the API server; engine warnings: %q, recorded engine: %q", c.Name, warnings, c.Engine)}
	}
	return nil
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
	wrongKind := strings.Replace(string(good), "Kind=Deployment", "Kind=StatefulSet", 1)
	engineWarning := checkWarnings(t, string(good))
	note := "divergence: engine differs\nengine:\n  - " + strconv.Quote(engineWarning) + "\nversion:"
	diverging := strings.Replace(wrongKind, "version:", note, 1)
	silent := strings.Replace(string(good), "authorizer.requestResource.check('update').allowed()", "true", 1)
	silent = strings.Replace(silent, "version:", "divergence: engine misses the error\nengine: []\nversion:", 1)
	tests := []struct {
		name, data, want string
	}{
		{"known divergence that remains", diverging, ""},
		{"same", string(good), ""},
		{"wrong kind", strings.Replace(string(good), "Kind=Deployment", "Kind=StatefulSet", 1), "recorded line"},
		{"wrong message", strings.Replace(string(good), "unexpected failed resolution of 'kubernetes.authorization.Authorizer'", "unexpected failed resolution of 'kubernetes.authorization.Other'", 1), "recorded line"},
		{"more warnings than lines", strings.Replace(string(two), "  - \"apps/v1, Kind=StatefulSet: ERROR: <input>:1:11: unexpected failed resolution of 'kubernetes.authorization.Authorizer'\"\n", "", 1), "2 warnings"},
		{"more lines than warnings", strings.Replace(string(good), "typeChecking:\n", "typeChecking:\n  - \"apps/v1, Kind=StatefulSet: ERROR: <input>:1:11: unexpected failed resolution of 'kubernetes.authorization.Authorizer'\"\n", 1), "want 2"},
		{"divergence with a malformed recorded line", strings.Replace(diverging, "apps/v1, Kind=StatefulSet: ERROR: <input>:1:11:", "apps/v1, Kind=StatefulSet: ERROR:", 1), "is not in the form"},
		{"known divergence that is gone", strings.Replace(string(good), "version:", note, 1), "now agrees"},
		{"divergence whose engine answer changed", strings.Replace(diverging, "unexpected failed resolution", "changed wording", 1), "changed but still differ"},
		{"divergence without engine answer", strings.Replace(wrongKind, "version:", "divergence: engine differs\nversion:", 1), "needs the engine's current warnings"},
		{"engine answer without divergence", strings.Replace(string(good), "version:", "engine:\n  - x\nversion:", 1), "only for a case with a divergence"},
		{"divergence the engine misses, recorded as none", silent, ""},
		{"divergence recorded as none but the engine warns", strings.Replace(wrongKind, "version:", "divergence: engine differs\nengine: []\nversion:", 1), "changed but still differ"},
		{"no typeChecking key", strings.Split(string(good), "typeChecking:")[0], "name, policy and typeChecking are required"},
		{"empty typeChecking for a clean policy", strings.Split(strings.Replace(silent, "divergence: engine misses the error\nengine: []\n", "", 1), "typeChecking:")[0] + "typeChecking: []\n", ""},
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

// engineWarnings returns the messages of the warnings the engine gives for the
// policy of c, checked as the Kubernetes version the case records.
func engineWarnings(ctx context.Context, name string, c truthCase) ([]string, error) {
	// The recorded version is v1.37.0; the engine's versions are minors.
	parts := strings.SplitN(strings.TrimPrefix(c.Version, "v"), ".", 3)
	v, err := kubeversion.Parse(strings.Join(parts[:min(len(parts), 2)], "."))
	if err != nil {
		return nil, fmt.Errorf("version %q: %w", c.Version, err)
	}
	diags, err := check.Check(ctx, name, []byte(c.Policy), check.WithVersion(v), allOn())
	if err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	var out []string
	for _, d := range diags {
		if d.Severity == check.SeverityWarning {
			out = append(out, d.Message)
		}
	}
	return out, nil
}

// checkWarnings returns the one warning the engine gives for the truth case in data.
func checkWarnings(t *testing.T, data string) string {
	t.Helper()
	var c truthCase
	if err := yaml.Unmarshal([]byte(data), &c); err != nil {
		t.Fatal(err)
	}
	out, err := engineWarnings(context.Background(), "case.yaml", c)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("engine warnings = %q, want one", out)
	}
	return out[0]
}
