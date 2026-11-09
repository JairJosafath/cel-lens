package lsp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lensCRD is a CustomResourceDefinition whose spec.config is dyn: the API
// server lets its rules read anything from it.
const lensCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: "self.spec.config > 0"
            - rule: "self.spec.config.startsWith('a')"
          properties:
            spec:
              type: object
              properties:
                config:
                  x-kubernetes-int-or-string: true
                open:
                  x-kubernetes-preserve-unknown-fields: true
                name:
                  type: string
`

// lensText types spec.config of lensCRD, in crd.yaml beside it.
const lensText = `# The types cel-lens assumes.
types:
  - file: crd.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.config
    type: int
`

// configWarning is what lensText's type shows in lensCRD.
const configWarning = "found no matching overload for 'startsWith' applied to 'int.(string)' (the type comes from .cellens:3: the API server would not report this)"

// lensOff is lensText with its entry on a typed value, so it types nothing.
var lensOff = strings.Replace(lensText, "path: spec.config", "path: spec.name", 1)

// diagnosticsOf waits for diagnostics published for u that satisfy done, and
// returns them.
func (c *client) diagnosticsOf(u string, done func([]diagnostic) bool) []diagnostic {
	c.t.Helper()
	for {
		m := c.read("textDocument/publishDiagnostics for " + u)
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p struct {
			URI         string       `json:"uri"`
			Diagnostics []diagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			c.t.Fatal(err)
		}
		if p.URI == u && done(p.Diagnostics) {
			return p.Diagnostics
		}
	}
}

func hasMessage(want string) func([]diagnostic) bool {
	return func(diags []diagnostic) bool {
		for _, d := range diags {
			if d.Message == want {
				return true
			}
		}
		return false
	}
}

func none(diags []diagnostic) bool { return len(diags) == 0 }

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileURI(path string) string { return "file://" + filepath.ToSlash(path) }

// request sends a hover or completion at the end of needle's first n
// characters in the document at u, whose text is text, and decodes the result.
func (c *client) request(method, u, text, needle string, n int, out any) {
	c.t.Helper()
	line, char := lineOf(c.t, text, needle)
	res, rerr := c.call(method, map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char + n}})
	if rerr != nil {
		c.t.Fatalf("%s error = %+v", method, rerr)
	}
	if err := json.Unmarshal(res, out); err != nil {
		c.t.Fatalf("%s result %s: %v", method, res, err)
	}
}

// TestLensFromWorkspace checks that the server reads a .cellens of the
// workspace and uses its types for checks, completion and hover (#2).
func TestLensFromWorkspace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const policy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits
spec:
  paramKind: {apiVersion: example.com/v1, kind: Limit}
  validations:
    - expression: "params.spec.max > 0"
`
	text := lensText + `  - file: policy.yaml
    document: ValidatingAdmissionPolicy limits
    variable: params
    type: "{spec: {max: int}}"
`
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	writeFile(t, filepath.Join(dir, "policy.yaml"), policy)
	writeFile(t, filepath.Join(dir, ".cellens"), text)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	crd, policyAt := fileURI(filepath.Join(dir, "crd.yaml")), fileURI(filepath.Join(dir, "policy.yaml"))
	c.openURI(crd, lensCRD)
	// The search for .cellens files runs in the background: the first check
	// may come before it ends, and the check when it ends follows.
	diags := c.diagnosticsOf(crd, hasMessage(configWarning))
	if len(diags) != 1 || diags[0].Severity != 2 {
		t.Errorf("diagnostics = %+v, want only the warning %q", diags, configWarning)
	}
	line, char := lineOf(t, lensCRD, "self.spec.config.startsWith")
	if d := diags[0]; d.Range.Start.Line != line || d.Range.Start.Character != char+len("self.spec.config.") {
		t.Errorf("warning at %+v, want on startsWith at %d:%d", d.Range, line, char+len("self.spec.config."))
	}
	var hover struct{ Contents struct{ Value string } }
	c.request("textDocument/hover", crd, lensCRD, "self.spec.config > 0", len("self.spec.con"), &hover)
	if got := hover.Contents.Value; !strings.Contains(got, "(field) config: int") || !strings.Contains(got, "from .cellens, line 3") {
		t.Errorf("hover on config = %q, want the type and that it is from .cellens, line 3", got)
	}

	c.openURI(policyAt, policy)
	c.diagnosticsOf(policyAt, none)
	var items []struct{ Label string }
	c.request("textDocument/completion", policyAt, policy, "params.spec.max", len("params.spec."), &items)
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	if !contains(labels, "max") {
		t.Errorf("completion after params.spec. = %v, want max", labels)
	}
}

// TestLensProblems checks that the problems of a .cellens are diagnostics on
// it, at the line of their entry (#2): a format problem as an error, and as
// warnings an entry for a file, document or value that does not exist, or for
// a value Kubernetes already types, and a type other than int or string for
// an x-kubernetes-int-or-string value of a CRD (#4).
func TestLensProblems(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	text := `types:
  - file: crd.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.name
    type: string
  - file: missing.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    type: string
  - file: crd.yaml
    document: CustomResourceDefinition gadgets.example.com
    version: v1
    type: string
  - file: crd.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.nosuch
    type: string
  - file: crd.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.open
    type: "{max: int}"
  - file: crd.yaml
    type: string
  - file: crd.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.config
    type: "{max: int}"
`
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c.openURI(lens, text)
	diags := c.diagnosticsOf(lens, func([]diagnostic) bool { return true })
	want := []struct {
		line     int
		severity int
		message  string
	}{
		{1, 2, "spec.name is already typed as string: remove this entry"},
		{6, 2, "file missing.yaml does not exist"},
		{10, 2, "the file has no document CustomResourceDefinition gadgets.example.com"},
		{14, 2, "spec has no field nosuch"},
		{19, 2, "the API server lets no rule read spec.open, which keeps unknown fields: give it a schema in the CustomResourceDefinition instead of this entry"},
		{24, 1, `the entry has no document: write its kind and metadata.name, such as "CustomResourceDefinition widgets.example.com"`},
		{26, 2, "spec.config is x-kubernetes-int-or-string, an int or a string to a rule: give it int or string"},
	}
	if len(diags) != len(want) {
		t.Fatalf("diagnostics of .cellens = %+v, want %d", diags, len(want))
	}
	for i, w := range want {
		if d := diags[i]; d.Range.Start.Line != w.line || d.Severity != w.severity || d.Message != w.message {
			t.Errorf("diagnostic %d = line %d severity %d %q, want line %d severity %d %q", i, d.Range.Start.Line, d.Severity, d.Message, w.line, w.severity, w.message)
		}
	}
	if d := diags[0]; d.Range.Start.Character != 4 || d.Range.End.Character != len("  - file: crd.yaml") {
		t.Errorf("first diagnostic at %+v, want the entry's first line from file: to its end", d.Range)
	}
}

// TestLensEditRechecks checks that opening, editing and closing a .cellens
// checks the open documents again, without a restart (#2).
func TestLensEditRechecks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	c.openURI(crd, lensCRD)
	c.diagnosticsOf(crd, none)
	c.openURI(lens, lensText)
	c.diagnosticsOf(crd, hasMessage(configWarning))
	c.changeURI(lens, 2, lensOff)
	c.diagnosticsOf(crd, none)
	c.changeURI(lens, 3, lensText)
	c.diagnosticsOf(crd, hasMessage(configWarning))
	// Closed, the .cellens is what the disk has: nothing here.
	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": lens}})
	c.diagnosticsOf(crd, none)
}

// TestLensOnDisk checks that a .cellens created, saved or deleted on disk
// checks the open documents again (#2).
func TestLensOnDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".cellens")
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	c.openURI(crd, lensCRD)
	c.diagnosticsOf(crd, none)
	changed := func(kind int) {
		c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": fileURI(path), "type": kind}}})
	}
	writeFile(t, path, lensText)
	changed(1)
	c.diagnosticsOf(crd, hasMessage(configWarning))
	writeFile(t, path, lensOff)
	changed(2)
	c.diagnosticsOf(crd, none)
	writeFile(t, path, lensText)
	changed(2)
	c.diagnosticsOf(crd, hasMessage(configWarning))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	changed(3)
	c.diagnosticsOf(crd, none)
}

// TestLensInCreatedFolder checks that a folder created with a .cellens in it
// is searched, and the open documents are checked again.
func TestLensInCreatedFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	crd := fileURI(filepath.Join(sub, "crd.yaml"))
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	c.openURI(crd, lensCRD)
	c.diagnosticsOf(crd, none)
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, ".cellens"), lensText)
	c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": fileURI(sub), "type": 1}}})
	// Outside every workspace folder, the .cellens is named by its path.
	c.diagnosticsOf(crd, hasMessage(strings.Replace(configWarning, ".cellens:3", filepath.ToSlash(filepath.Join(sub, ".cellens"))+":3", 1)))
}

// TestLensKinds checks a kind: entry naming a built-in kind and one naming a
// CustomResourceDefinition of the workspace, for params and object of a
// ValidatingAdmissionPolicy (#2).
func TestLensKinds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy := `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits
spec:
  paramKind: {apiVersion: example.com/v1, kind: Missing}
  matchConstraints:
    resourceRules:
      - {apiGroups: ["*"], apiVersions: ["*"], resources: ["*"]}
  validations:
    - expression: "params.data.size() > 0 && object.spec.name != ''"
    - expression: "params.nosuch == 1"
    - expression: "object.spec.nosuch == 1"
`
	text := `types:
  - file: policy.yaml
    document: ValidatingAdmissionPolicy limits
    variable: params
    kind: v1 ConfigMap
  - file: policy.yaml
    document: ValidatingAdmissionPolicy limits
    variable: object
    kind: example.com/v1 Widget
`
	policyAt := fileURI(filepath.Join(dir, "policy.yaml"))
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	c.openURI(fileURI(filepath.Join(dir, "crd.yaml")), lensCRD)
	c.openURI(policyAt, policy)
	c.diagnosticsOf(policyAt, none)
	c.openURI(fileURI(filepath.Join(dir, ".cellens")), text)
	diags := c.diagnosticsOf(policyAt, func(d []diagnostic) bool { return len(d) > 0 })
	want := []string{
		"undefined field 'nosuch' (the type comes from .cellens:2: the API server would not report this)",
		"undefined field 'nosuch' (the type comes from .cellens:6: the API server would not report this)",
	}
	if len(diags) != len(want) || diags[0].Message != want[0] || diags[1].Message != want[1] {
		t.Errorf("diagnostics = %+v, want %q", diags, want)
	}
}

// TestLensKindNeedsCRDTypedPolicies checks that a kind: entry naming a
// CustomResourceDefinition says the experimental feature is off.
func TestLensKindNeedsCRDTypedPolicies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "policy.yaml"), widgetPolicyText)
	c := scanSession(t, t.TempDir())
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c.openURI(lens, "types:\n  - file: policy.yaml\n    document: ValidatingAdmissionPolicy demo\n    variable: params\n    kind: example.com/v1 Widget\n")
	diags := c.diagnosticsOf(lens, func([]diagnostic) bool { return true })
	want := "kind example.com/v1 Widget is no built-in kind, and typing from a CustomResourceDefinition of the workspace is the experimental feature crd-typed-policies, which is off"
	if len(diags) != 1 || diags[0].Message != want || diags[0].Range.Start.Line != 1 {
		t.Errorf("diagnostics of .cellens = %+v, want %q on line 1", diags, want)
	}
}

func TestLensWatcherRegistered(t *testing.T) {
	t.Parallel()
	c := start(t)
	initializeIn(t, c, "", map[string]any{"workspace": map[string]any{"didChangeWatchedFiles": map[string]any{"dynamicRegistration": true}}})
	for {
		m := c.read("client/registerCapability")
		if m.Method != "client/registerCapability" {
			continue
		}
		if !strings.Contains(string(m.Params), `"globPattern":"**/.cellens"`) {
			t.Errorf("registerCapability params = %s, want a watcher for .cellens files", m.Params)
		}
		return
	}
}

// TestLensSecondTypeForAValue checks that when two .cellens files type the
// same value, the first, by path, gives the type, and the later one's entry is
// a problem that names the first (#2 review).
func TestLensSecondTypeForAValue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "a")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	crd := fileURI(filepath.Join(sub, "crd.yaml"))
	inner := fileURI(filepath.Join(sub, ".cellens"))
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	c.openURI(crd, lensCRD)
	c.diagnosticsOf(crd, none)
	c.openURI(fileURI(filepath.Join(dir, ".cellens")), strings.Replace(lensText, "file: crd.yaml", "file: a/crd.yaml", 1))
	c.diagnosticsOf(crd, hasMessage(configWarning))
	c.openURI(inner, strings.Replace(lensText, "type: int", "type: string", 1))
	diags := c.diagnosticsOf(inner, func(d []diagnostic) bool { return len(d) > 0 })
	const want = "a second type for this value: .cellens:3 gives it one: keep one"
	if len(diags) != 1 || diags[0].Message != want || diags[0].Range.Start.Line != 2 {
		t.Errorf("diagnostics of a/.cellens = %+v, want %q on line 2", diags, want)
	}
	// The open document still has the first file's type, int, not string.
	line, char := lineOf(t, lensCRD, "self.spec.config > 0")
	res, rerr := c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": crd}, "position": map[string]any{"line": line, "character": char + len("self.spec.con")}})
	if rerr != nil || !strings.Contains(string(res), "config: int") {
		t.Errorf("hover on config = %s, %v, want the type of the first .cellens, int", res, rerr)
	}
}

// TestLensEntryOnlyForItsFile checks that an entry for one file leaves the
// same document in another file alone (#2).
func TestLensEntryOnlyForItsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := fileURI(filepath.Join(dir, "crd.yaml")), fileURI(filepath.Join(dir, "copy.yaml"))
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	c.openURI(b, lensCRD)
	c.diagnosticsOf(b, none)
	c.openURI(a, lensCRD)
	c.diagnosticsOf(a, none)
	c.diagnosticsOf(b, none) // checked again, as the CRDs changed
	c.openURI(fileURI(filepath.Join(dir, ".cellens")), lensText)
	// Only crd.yaml, whose types changed, is checked again (#16); checked
	// again, copy.yaml still has no type from the .cellens.
	c.diagnosticsOf(a, hasMessage(configWarning))
	c.changeURI(b, 2, lensCRD)
	if diags := c.diagnosticsOf(b, func([]diagnostic) bool { return true }); len(diags) != 0 {
		t.Errorf("diagnostics of copy.yaml = %+v, want none: the entry names crd.yaml", diags)
	}
}
