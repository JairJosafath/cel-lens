package lsp_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/lsp"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// runExamples runs the examples of paths for the default Kubernetes version.
func runExamples(t *testing.T, paths ...string) *lsp.ExamplesReport {
	t.Helper()
	report, err := lsp.RunExamples(t.Context(), paths, kubeversion.Default())
	if err != nil {
		t.Fatalf("RunExamples(%q) error = %v", paths, err)
	}
	return report
}

// asJSON returns v as the JSON values json.Unmarshal makes of it, for a
// comparison that does not depend on a Go type.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRunExamplesIsExampleResults checks that the command line runs the
// examples as the language server does: for a workspace with a CRD, a
// policy and its binding, and the examples of both, RunExamples gives the
// rules that cel-lens.exampleResults answers for each file with rules, in the
// order of the files, with the same results (#24).
func TestRunExamplesIsExampleResults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crdPath := filepath.Join(dir, "crd.yaml")
	policyPath := filepath.Join(dir, "policy", "replicas.yaml")
	writeIn(t, crdPath, ruleCRD)
	writeIn(t, filepath.Join(dir, "samples", "ports.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: p}\nspec:\n  size: 3\n  ports: [{name: http}, {name: dns}, {name: https}]\n")
	writeIn(t, filepath.Join(dir, "samples", "widget.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec:\n  size: 3\n---\napiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec: {size: 4}\n")
	writeIn(t, filepath.Join(dir, "testdata", "invalid", "big.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: big}\nspec: {size: 11}\n")
	writeIn(t, filepath.Join(dir, "testdata", "small.yaml"), "# cel-lens: refused \"at most 10\"\napiVersion: example.com/v1\nkind: Widget\nmetadata: {name: small}\nspec: {size: 1}\n")
	writeIn(t, policyPath, linePolicy)
	writeIn(t, filepath.Join(dir, "policy", "params.yaml"), lineParams)
	writeIn(t, filepath.Join(dir, "examples", "deploy.yaml"), lineDeployment("web", "5", "{}")+"---\n"+lineDeployment("test-web", "50", "{}"))
	writeIn(t, filepath.Join(dir, "examples", "invalid", "zero.yaml"), lineDeployment("zero", "0", "{}"))

	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": fileURI(dir), "name": "ws"}}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	var want []any
	for _, path := range []string{crdPath, policyPath} {
		_, raw, rerr := c.command("cel-lens.exampleResults", map[string]any{"uri": fileURI(path)}, nil)
		if rerr != nil {
			t.Fatalf("cel-lens.exampleResults(%s) error = %+v", path, rerr)
		}
		var answer struct {
			Rules []any `json:"rules"`
		}
		if err := json.Unmarshal(raw, &answer); err != nil {
			t.Fatalf("cel-lens.exampleResults(%s) = %s: %v", path, raw, err)
		}
		want = append(want, answer.Rules...)
	}
	if len(want) < 6 {
		t.Fatalf("cel-lens.exampleResults gave %d rules, want the CRD's 4 and the policy's 4 expressions", len(want))
	}

	report := runExamples(t, dir)
	if got := asJSON(t, report.Rules); !reflect.DeepEqual(got, want) {
		t.Errorf("RunExamples(%s) rules =\n%v\nwant what cel-lens.exampleResults answers\n%v", dir, got, want)
	}
	if report.KubernetesVersion != "1.37" {
		t.Errorf("RunExamples kubernetesVersion = %q, want 1.37", report.KubernetesVersion)
	}
	// The two examples that expect to be refused and that nothing refuses
	// are its only problems, though each rule says it is wrong.
	var problems []string
	for _, p := range report.Problems {
		problems = append(problems, p.File+":"+itoa(p.Line)+": "+p.Message)
	}
	wantProblems := []string{
		"examples/invalid/zero.yaml:1: expected to be refused, but nothing refuses it",
		"testdata/small.yaml:2: expected to be refused with \"at most 10\", but nothing refuses it with that message",
	}
	if !slices.Equal(problems, wantProblems) {
		t.Errorf("RunExamples problems = %q, want %q", problems, wantProblems)
	}
}

// TestRunExamplesProblems checks that the problems the editor shows on the
// examples are in the report: the schema's problem on an example that
// expects to be allowed, and none on one that expects to be refused, a
// # cel-lens: comment that says none of the three forms, and the example that
// expects to be refused and nothing refuses, once, though each rule says it is
// wrong in its results.
func TestRunExamplesProblems(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIn(t, filepath.Join(dir, "crd.yaml"), ruleCRD)
	writeIn(t, filepath.Join(dir, "a.yaml"), `apiVersion: example.com/v1
kind: Widget
metadata: {name: str}
spec: {size: "three"}
---
# cel-lens: maybe
apiVersion: example.com/v1
kind: Widget
metadata: {name: comment}
spec: {size: 1}
---
# cel-lens: refused
apiVersion: example.com/v1
kind: Widget
metadata: {name: nothing}
spec: {size: 1}
`)
	writeIn(t, filepath.Join(dir, "invalid", "b.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: str}\nspec: {size: \"three\"}\n")
	report := runExamples(t, dir)
	type problem struct {
		file         string
		line, column int
		severity     string
		message      string
	}
	var got []problem
	for _, p := range report.Problems {
		got = append(got, problem{p.File, p.Line, p.Column, p.Severity, p.Message})
	}
	want := []problem{
		{"a.yaml", 4, 8, "error", `spec.size: Invalid value: "string": spec.size in body must be of type integer: "string"`},
		{"a.yaml", 6, 1, "error", `this # cel-lens: comment is none of # cel-lens: allowed, # cel-lens: refused and # cel-lens: refused "part of the message"`},
		{"a.yaml", 13, 1, "error", "expected to be refused, but nothing refuses it"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("RunExamples problems = %+v, want %+v", got, want)
	}
	var wrong []string
	for _, r := range report.Rules {
		for _, res := range r.Results {
			if res.Wrong != "" {
				wrong = append(wrong, r.Rule+" | "+res.Example.File+":"+itoa(res.Example.Line)+" | "+res.Result+" | wrong at "+res.Wrong)
			}
		}
	}
	wantWrong := []string{
		"self.name.size() <= 3 | a.yaml:13 | not run | wrong at example",
		"self.size <= 10 | a.yaml:13 | allowed | wrong at example",
		"self.size == oldSelf.size | a.yaml:13 | not run | wrong at example",
		"self.nope > 1 | a.yaml:13 | not run | wrong at example",
	}
	if !slices.Equal(wrong, wantWrong) {
		t.Errorf("RunExamples wrong verdicts =\n%s\nwant\n%s", strings.Join(wrong, "\n"), strings.Join(wantWrong, "\n"))
	}
}

// TestRunExamplesWorkspace checks which files a run reads: those of the
// folders walked as the language server's workspace scan walks them, hidden
// folders and node_modules left out, and a file given alone is the whole
// workspace, but for the folders given.
func TestRunExamplesWorkspace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sample := func(name string) string {
		return "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: " + name + "}\nspec: {size: 3}\n"
	}
	writeIn(t, filepath.Join(dir, "crd.yaml"), ruleCRD)
	writeIn(t, filepath.Join(dir, "a.yaml"), sample("a"))
	writeIn(t, filepath.Join(dir, "b.yaml"), sample("b"))
	writeIn(t, filepath.Join(dir, ".hidden", "c.yaml"), sample("c"))
	writeIn(t, filepath.Join(dir, "node_modules", "d.yaml"), sample("d"))
	writeIn(t, filepath.Join(dir, "sub", "e.yaml"), sample("e"))
	writeIn(t, filepath.Join(dir, "notes.txt"), sample("f"))
	examplesOf := func(paths ...string) string {
		t.Helper()
		var files []string
		for _, r := range runExamples(t, paths...).Rules {
			if r.Rule != "self.size <= 10" {
				continue
			}
			for _, res := range r.Results {
				files = append(files, res.Example.File)
			}
		}
		return strings.Join(files, " ")
	}
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"a folder", []string{dir}, "a.yaml b.yaml sub/e.yaml"},
		{"a folder below", []string{filepath.Join(dir, "sub"), filepath.Join(dir, "crd.yaml")}, "e.yaml"},
		{"files alone", []string{filepath.Join(dir, "crd.yaml"), filepath.Join(dir, "b.yaml")}, "b.yaml"},
		{"a file below a folder is read with it", []string{dir, filepath.Join(dir, "a.yaml")}, "a.yaml b.yaml sub/e.yaml"},
		{"a file in a hidden folder, alone", []string{filepath.Join(dir, "crd.yaml"), filepath.Join(dir, ".hidden", "c.yaml")}, "c.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := examplesOf(tc.paths...); got != tc.want {
				t.Errorf("examples of %q = %q, want %q", tc.paths, got, tc.want)
			}
		})
	}
}

// TestRunExamplesPaths checks what a run does with paths it cannot use: it
// runs nothing, and the error names each, whether it is missing or no YAML
// file; and a cancelled context stops it.
func TestRunExamplesPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIn(t, filepath.Join(dir, "crd.yaml"), ruleCRD)
	writeIn(t, filepath.Join(dir, "notes.txt"), "hello")
	missing, text := filepath.Join(dir, "missing.yaml"), filepath.Join(dir, "notes.txt")
	report, err := lsp.RunExamples(t.Context(), []string{dir, missing, text}, kubeversion.Default())
	if report != nil {
		t.Errorf("RunExamples with a missing path ran: %+v", report)
	}
	if err == nil || !strings.Contains(err.Error(), "read "+missing+": no such file") || !strings.Contains(err.Error(), "read "+text+": not a .yaml or .yml file") {
		t.Errorf("RunExamples error = %v, want one for the missing path and one for the text file", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lsp.RunExamples(ctx, []string{dir}, kubeversion.Default()); !errors.Is(err, context.Canceled) {
		t.Errorf("RunExamples with a cancelled context error = %v, want %v", err, context.Canceled)
	}
}

// TestExampleDiagnosticsUnreadable checks that the editor says, on a policy
// of the workspace that is not open and that the API server cannot read, what
// is wrong with it, as the command line does, where it said nothing and the
// policy had no examples (#31). Opened, the document has the check's problem
// and the examples add no second one.
func TestExampleDiagnosticsUnreadable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: p}\nspec:\n  auditAnnotations:\n    - key: n\n      valueExpression: \"'x'\"\n"
	path := filepath.Join(dir, "policy.yaml")
	writeIn(t, path, policy)
	const message = "spec.auditAnnotations[0].key: expected a string, got a boolean: kubectl reads an unquoted n as one (YAML 1.1), so quote it if a string is meant"
	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": fileURI(dir), "name": "ws"}}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	got, _ := c.diagnosticsFor(fileURI(path), saying(message))
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Range.Start.Line != 5 {
		t.Errorf("diagnostics of the closed policy = %+v, want the wrong type on line 6", got.Diagnostics)
	}
	c.openURI(fileURI(path), policy)
	opened, _ := c.diagnosticsFor(fileURI(path), saying(message))
	if len(opened.Diagnostics) != 1 {
		t.Errorf("diagnostics of the open policy = %+v, want the check's one", opened.Diagnostics)
	}
}
