package lsp_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// inlineCRD is a CustomResourceDefinition with rules on its root and on spec.
// spec.config and spec.port are dyn values a rule can read
// (x-kubernetes-int-or-string).
const inlineCRD = `apiVersion: apiextensions.k8s.io/v1
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
            - rule: "self.spec.name.nosuch == 1"
              messageExpression: "'bad: ' + self.spec.name"
          properties:
            spec:
              type: object
              x-kubernetes-validations:
                - rule: |
                    self.port == 1
              properties:
                config:
                  x-kubernetes-int-or-string: true
                port:
                  x-kubernetes-int-or-string: true
                name:
                  type: string
`

// inlinePolicy is a ValidatingAdmissionPolicy whose params are dyn: no CRD
// defines its paramKind. So is variables.max, which reads params.
const inlinePolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits
spec:
  paramKind: {apiVersion: example.com/v1, kind: Missing}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  matchConditions:
    - name: not-dry
      expression: "request.dryRun != true"
  variables:
    - name: max
      expression: "params.max"
  validations:
    - expression: "object.spec.replicas <= variables.max"
`

// show is the command of the first part of every line.
const show = "cel-lens.showRule"

// codeLens is a code lens as the server sends it.
type codeLens struct {
	Range struct {
		Start, End struct{ Line, Character int }
	}
	Command struct {
		Title     string
		Command   string
		Arguments []map[string]any
	}
}

// lensLine is the code lenses of one line: their titles and commands, and
// the argument of the first command after the one that shows the rule.
type lensLine struct {
	line     int
	titles   []string
	commands []string
	target   map[string]any
}

// openAll opens each document, uri then text, and reads what the server
// sends for each before the next, as the pipes hold no message.
func (c *client) openAll(docs ...string) {
	c.t.Helper()
	for i := 0; i+1 < len(docs); i += 2 {
		c.openURI(docs[i], docs[i+1])
		c.call("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": "file:///none.yaml"}})
	}
}

// codeLenses asks for the code lenses of the document at u and returns them
// by line, in order.
func (c *client) codeLenses(u string) []lensLine {
	c.t.Helper()
	res, rerr := c.call("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": u}})
	if rerr != nil {
		c.t.Fatalf("codeLens error = %+v", rerr)
	}
	var lenses []codeLens
	if err := json.Unmarshal(res, &lenses); err != nil {
		c.t.Fatalf("codeLens result %s: %v", res, err)
	}
	var out []lensLine
	for _, l := range lenses {
		if l.Range.Start.Line != l.Range.End.Line {
			c.t.Errorf("code lens %+v spans lines, want one", l)
		}
		if len(out) == 0 || out[len(out)-1].line != l.Range.Start.Line {
			out = append(out, lensLine{line: l.Range.Start.Line})
		}
		last := &out[len(out)-1]
		last.titles = append(last.titles, l.Command.Title)
		last.commands = append(last.commands, l.Command.Command)
		if last.target == nil && l.Command.Command != "cel-lens.showRule" && len(l.Command.Arguments) > 0 {
			last.target = l.Command.Arguments[0]
		}
	}
	return out
}

// TestCodeLensCapability checks that initialize declares code lenses, with
// no resolve: the commands come with their arguments (#3).
func TestCodeLensCapability(t *testing.T) {
	t.Parallel()
	c := start(t)
	res, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil})
	if rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	var r struct {
		Capabilities struct {
			CodeLensProvider *map[string]any
		}
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	if p := r.Capabilities.CodeLensProvider; p == nil || len(*p) != 0 {
		t.Errorf("initialize codeLensProvider = %v, want {} (no resolveProvider)", p)
	}
}

// TestCodeLensLines checks the line above each rule, messageExpression and
// policy expression: the subject and compiles or the problems, and a part
// for each dyn value with the command that gives it a type, its target named
// as a .cellens entry names it, in the .cellens at the workspace folder's
// root (#3).
func TestCodeLensLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crd := fileURI(filepath.Join(dir, "deploy", "crd.yaml"))
	policy := fileURI(filepath.Join(dir, "policy.yaml"))
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	c.openURI(crd, inlineCRD)
	c.openURI(policy, inlinePolicy)

	at := func(text, needle string) int {
		line, _ := lineOf(t, text, needle)
		return line
	}
	// A target names the value by its path from the root, and as the rule reads it.
	crdTarget := func(path, value string) map[string]any {
		return map[string]any{"cellens": lens, "file": "deploy/crd.yaml", "document": "CustomResourceDefinition widgets.example.com", "version": "v1", "path": path, "value": value}
	}
	policyTarget := func(variable string) map[string]any {
		return map[string]any{"cellens": lens, "file": "policy.yaml", "document": "ValidatingAdmissionPolicy limits", "variable": variable, "value": variable}
	}
	tests := []struct {
		name string
		uri  string
		want []lensLine
	}{
		{"CustomResourceDefinition", crd, []lensLine{
			{at(inlineCRD, "self.spec.config > 0"), []string{"self: Widget · compiles", "self.spec.config: dyn · Give it a type"}, []string{show, "cel-lens.giveType"}, crdTarget("spec.config", "self.spec.config")},
			{at(inlineCRD, "self.spec.name.nosuch"), []string{"self: Widget · 1 problem"}, []string{show}, nil},
			{at(inlineCRD, "messageExpression"), []string{"self: Widget · compiles"}, []string{show}, nil},
			// A block scalar's line goes above its key.
			{at(inlineCRD, "- rule: |"), []string{"self: Widget.spec · compiles", "self.port: dyn · Give it a type"}, []string{show, "cel-lens.giveType"}, crdTarget("spec.port", "self.port")},
		}},
		{"ValidatingAdmissionPolicy", policy, []lensLine{
			{at(inlinePolicy, "request.dryRun"), []string{"object: apps/v1 Deployment · compiles"}, []string{show}, nil},
			{at(inlinePolicy, "params.max"), []string{"object: apps/v1 Deployment · compiles", "params: dyn · Give it a type"}, []string{show, "cel-lens.giveType"}, policyTarget("params")},
			{at(inlinePolicy, "variables.max"), []string{"object: apps/v1 Deployment · compiles", "variables.max: dyn · Give it a type"}, []string{show, "cel-lens.giveType"}, policyTarget("variables.max")},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c.use(t)
			got := c.codeLenses(tc.uri)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("code lenses = %+v,\nwant %+v", got, tc.want)
			}
		})
	}
}

// TestCodeLensTyped checks that a value a .cellens entry types reads
// "name: type · Edit" and "Remove", whose commands name the entry and its
// type: an int-or-string of a CRD typed int, and params typed as a struct;
// and that a dyn value's entry would go in the nearest .cellens (#3).
func TestCodeLensTyped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	policy := fileURI(filepath.Join(dir, "policy.yaml"))
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	c.openAll(lens, lensText+`  - file: policy.yaml
    document: ValidatingAdmissionPolicy limits
    variable: params
    type: "{max: int, x: string}"
`, crd, inlineCRD, policy, inlinePolicy)
	got := c.codeLenses(crd)
	if len(got) != 4 {
		t.Fatalf("code lenses = %+v, want four lines", got)
	}
	want := lensLine{
		line:     got[0].line,
		titles:   []string{"self: Widget · compiles", "self.spec.config: int · Edit", "Remove"},
		commands: []string{show, "cel-lens.editType", "cel-lens.removeType"},
		target: map[string]any{
			"cellens": lens, "file": "crd.yaml", "document": "CustomResourceDefinition widgets.example.com", "version": "v1",
			"path": "spec.config", "value": "self.spec.config", "type": "int",
		},
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("code lenses of the typed rule = %+v, want %+v", got[0], want)
	}
	if target := got[3].target; target["cellens"] != lens || target["file"] != "crd.yaml" || target["path"] != "spec.port" {
		t.Errorf("target of the dyn spec.port = %v, want spec.port in %s, file crd.yaml", target, lens)
	}
	lines := c.codeLenses(policy)
	if len(lines) != 3 {
		t.Fatalf("code lenses of the policy = %+v, want three lines", lines)
	}
	if l := lines[1]; !reflect.DeepEqual(l.titles, []string{"object: apps/v1 Deployment · compiles", "params: {max, x} · Edit", "Remove"}) ||
		l.target["type"] != "{max: int, x: string}" || l.target["variable"] != "params" {
		t.Errorf("code lenses of the variable reading params = %+v, want params typed as {max, x}", l)
	}
}

// TestCodeLensEntryThatDoesNotApply checks that a value whose .cellens entry
// does not apply, as its kind is not found, offers Edit and Remove for that
// entry, not Give it a type, which would write a second one (#3).
func TestCodeLensEntryThatDoesNotApply(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy := fileURI(filepath.Join(dir, "policy.yaml"))
	lens := fileURI(filepath.Join(dir, "sub", ".cellens"))
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	entry := "types:\n  - file: policy.yaml\n    document: ValidatingAdmissionPolicy limits\n    variable: params\n    kind: example.com/v1 Missing\n"
	// The entry of the folder above the policy's comes first; the one of a
	// folder below it names another file.
	c.openAll(lens, strings.Replace(entry, "file: policy.yaml", "file: policy.yaml\n    path: spec", 1), fileURI(filepath.Join(dir, ".cellens")), entry, policy, inlinePolicy)
	lines := c.codeLenses(policy)
	if len(lines) != 3 {
		t.Fatalf("code lenses = %+v, want three lines", lines)
	}
	want := lensLine{
		line:     lines[1].line,
		titles:   []string{"object: apps/v1 Deployment · compiles", "params: dyn · Edit", "Remove"},
		commands: []string{show, "cel-lens.editType", "cel-lens.removeType"},
		target: map[string]any{
			"cellens": fileURI(filepath.Join(dir, ".cellens")), "file": "policy.yaml", "document": "ValidatingAdmissionPolicy limits",
			"variable": "params", "value": "params", "kind": "example.com/v1 Missing",
		},
	}
	if !reflect.DeepEqual(lines[1], want) {
		t.Errorf("code lenses of the variable reading params = %+v, want %+v", lines[1], want)
	}
}

// TestCodeLensShowRule checks that the first part of a line shows the rule:
// cel-lens.showRule with the document and the range of the expression (#3).
func TestCodeLensShowRule(t *testing.T) {
	t.Parallel()
	crd := fileURI(filepath.Join(t.TempDir(), "crd.yaml"))
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	c.openURI(crd, inlineCRD)
	res, rerr := c.call("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": crd}})
	if rerr != nil {
		t.Fatalf("codeLens error = %+v", rerr)
	}
	var lenses []struct {
		Command struct {
			Title, Command string
			Arguments      []struct {
				URI   string
				Range struct{ Start, End struct{ Line, Character int } }
			}
		}
	}
	if err := json.Unmarshal(res, &lenses); err != nil || len(lenses) == 0 {
		t.Fatalf("codeLens = %s, %v, want lenses", res, err)
	}
	line, char := lineOf(t, inlineCRD, "self.spec.config > 0")
	first := lenses[0].Command
	if first.Command != show || len(first.Arguments) != 1 {
		t.Fatalf("first part = %+v, want %s with one argument", first, show)
	}
	if a := first.Arguments[0]; a.URI != crd || a.Range.Start.Line != line || a.Range.Start.Character != char ||
		a.Range.End.Line != line || a.Range.End.Character != char+len("self.spec.config > 0") {
		t.Errorf("%s argument = %+v, want %s at %d:%d-%d", show, a, crd, line, char, char+len("self.spec.config > 0"))
	}
}

// TestCodeLensMoreValues checks that a rule reading more dyn values than its
// line shows ends with +N more, whose command lists the others (#3).
func TestCodeLensMoreValues(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	text := strings.Replace(inlineCRD, `"self.spec.config > 0"`, `"self.spec.config == 1 && self.spec.port == 2"`, 1)
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	c.openURI(crd, text)
	res, rerr := c.call("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": crd}})
	if rerr != nil {
		t.Fatalf("codeLens error = %+v", rerr)
	}
	var lenses []struct {
		Command struct {
			Title, Command string
			Arguments      []struct {
				Title, Command string
				Arguments      []map[string]any
			}
		}
	}
	if err := json.Unmarshal(res, &lenses); err != nil {
		t.Fatal(err)
	}
	if len(lenses) < 3 {
		t.Fatalf("code lenses = %+v, want three on the first line", lenses)
	}
	more := lenses[2].Command
	if lenses[1].Command.Title != "self.spec.config: dyn · Give it a type" || more.Title != "+1 more" || more.Command != "cel-lens.listValues" || len(more.Arguments) != 1 {
		t.Fatalf("first line = %+v, want self.spec.config and +1 more listing one", lenses[:3])
	}
	if a := more.Arguments[0]; a.Title != "self.spec.port: dyn · Give it a type" || a.Command != "cel-lens.giveType" || a.Arguments[0]["path"] != "spec.port" {
		t.Errorf("+1 more lists %+v, want self.spec.port with cel-lens.giveType", a)
	}
}

// refreshCaps are the client capabilities of a client that takes
// workspace/codeLens/refresh.
var refreshCaps = map[string]any{"workspace": map[string]any{"codeLens": map[string]any{"refreshSupport": true}}}

// codeLensSeeingRefresh asks for the code lenses of the document at u and
// returns them, and whether the server sent workspace/codeLens/refresh
// before its answer.
func (c *client) codeLensSeeingRefresh(u string) (json.RawMessage, bool) {
	c.t.Helper()
	c.nextID++
	id := `"` + "seeing" + strings.Repeat("x", c.nextID) + `"`
	c.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": "textDocument/codeLens", "params": map[string]any{"textDocument": map[string]any{"uri": u}}})
	saw := false
	for {
		m := c.read("the code lenses")
		if m.Method == "workspace/codeLens/refresh" {
			saw = true
		}
		if string(m.ID) == id && m.Method == "" {
			return m.Result, saw
		}
	}
}

// TestCodeLensInlineInfo checks that the setting inlineInfo turns every line
// off and on without a restart, and that the server asks the client to ask
// again for the lenses when it changes (#3).
func TestCodeLensInlineInfo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": refreshCaps, "rootUri": nil, "initializationOptions": map[string]any{"inlineInfo": false}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	c.openURI(crd, inlineCRD)
	lenses := func() (int, bool) {
		res, refreshed := c.codeLensSeeingRefresh(crd)
		var got []json.RawMessage
		if err := json.Unmarshal(res, &got); err != nil {
			t.Fatalf("codeLens result %s: %v", res, err)
		}
		return len(got), refreshed
	}
	set := func(settings map[string]any) {
		c.notify("workspace/didChangeConfiguration", map[string]any{"settings": settings})
	}
	if n, _ := lenses(); n != 0 {
		t.Errorf("code lenses with inlineInfo false at initialize = %d, want none", n)
	}
	set(map[string]any{"inlineInfo": true})
	if n, refreshed := lenses(); n == 0 || !refreshed {
		t.Errorf("code lenses after inlineInfo true = %d, refreshed %t, want some and a refresh", n, refreshed)
	}
	set(map[string]any{"kubernetesVersion": ""})
	if n, refreshed := lenses(); n == 0 || refreshed {
		t.Errorf("code lenses after a setting that changes nothing = %d, refreshed %t, want some and no refresh", n, refreshed)
	}
	set(map[string]any{"inlineInfo": false})
	if n, refreshed := lenses(); n != 0 || !refreshed {
		t.Errorf("code lenses after inlineInfo false = %d, refreshed %t, want none and a refresh", n, refreshed)
	}
}

// TestCodeLensRefreshOnCellens checks that a .cellens that changes asks the
// client for the lenses again, which then read Edit and Remove (#3).
func TestCodeLensRefreshOnCellens(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	c := start(t)
	initializeIn(t, c, "", refreshCaps)
	c.openURI(crd, inlineCRD)
	if got := c.codeLenses(crd); len(got) == 0 || len(got[0].titles) != 2 || got[0].commands[1] != "cel-lens.giveType" {
		t.Fatalf("code lenses before the .cellens = %+v, want Give it a type", got)
	}
	c.openURI(fileURI(filepath.Join(dir, ".cellens")), lensText)
	res, refreshed := c.codeLensSeeingRefresh(crd)
	if !refreshed || !strings.Contains(string(res), "cel-lens.editType") {
		t.Errorf("after the .cellens opened: refreshed %t, code lenses %s, want a refresh and Edit", refreshed, res)
	}
}

// TestCodeLensOneLinePerLine checks that a rule and its messageExpression
// written on one line give that line to the rule, so it stays one line of at
// most 80 characters (#3).
func TestCodeLensOneLinePerLine(t *testing.T) {
	t.Parallel()
	crd := fileURI(filepath.Join(t.TempDir(), "crd.yaml"))
	text := strings.Replace(inlineCRD, `            - rule: "self.spec.name.nosuch == 1"
              messageExpression: "'bad: ' + self.spec.name"`, `            - {rule: "self.spec.name == 'a'", messageExpression: "'bad: ' + self.spec.name"}`, 1)
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	c.openURI(crd, text)
	got := c.codeLenses(crd)
	line, _ := lineOf(t, text, "messageExpression")
	for _, l := range got {
		if l.line == line && !reflect.DeepEqual(l.titles, []string{"self: Widget · compiles"}) {
			t.Errorf("code lenses of the line with a rule and a messageExpression = %q, want the rule's one", l.titles)
		}
	}
	if len(got) != 3 {
		t.Errorf("code lenses = %+v, want three lines", got)
	}
}

// TestCodeLensNoRefreshWithoutSupport checks that a client whose capabilities
// lack workspace.codeLens.refreshSupport is never asked to refresh the lenses:
// not when inlineInfo changes, nor when a .cellens does (#3).
func TestCodeLensNoRefreshWithoutSupport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	c.openURI(crd, inlineCRD)
	ask := func(what string) {
		t.Helper()
		if _, refreshed := c.codeLensSeeingRefresh(crd); refreshed {
			t.Errorf("after %s the server asked for a refresh, which the client does not support", what)
		}
	}
	ask("didOpen")
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"inlineInfo": false}})
	ask("inlineInfo false")
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"inlineInfo": true}})
	ask("inlineInfo true")
	c.openURI(fileURI(filepath.Join(dir, ".cellens")), lensText)
	ask("a .cellens opened")
}

// replicaLimitPolicy is the policy of the workspace #22 was found in: params
// of a kind no CRD defines, so Kubernetes leaves params dyn, and with it
// variables.limit, which reads it.
const replicaLimitPolicy = `# A policy whose params come from a custom kind that no CRD in this
# workspace describes, so Kubernetes gives ` + "`params`" + ` the type dyn.
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: replica-limit
spec:
  paramKind:
    apiVersion: rules.example.com/v1
    kind: ReplicaLimit
  matchConstraints:
    resourceRules:
      - apiGroups: ["apps"]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["deployments"]
  variables:
    - name: limit
      expression: params.maxReplicas
  validations:
    - expression: object.spec.replicas <= variables.limit
      message: Too many replicas for this namespace.
    - expression: params.teem == '' || object.metadata.labels['team'] == params.teem
      message: The deployment must carry the team label of its limit.
`

// replicaLimitLens types params of replicaLimitPolicy with a schema, as the
// .cellens of #22 does; TYPE is the type of maxReplicas.
const replicaLimitLens = `# .cellens: types cel-lens assumes where Kubernetes gives dyn. Commit it.
types:
  - file: policy.yaml
    document: ValidatingAdmissionPolicy replica-limit
    variable: params
    schema:
      type: object
      properties:
        maxReplicas: {type: TYPE, minimum: 0}
        team: {type: string}
`

// TestCodeLensInferredVariable is #22: with params typed by .cellens, the
// line above a rule that reads a variable reading params says the variable's
// type, variables.limit: int, as hover does, and no longer offers Give it a
// type, but shows the entry its type follows from; a change of the .cellens
// types it again. A variable whose expression
// returns dyn all the same still offers Give it a type, and one an entry
// names shows that entry's type with Edit and Remove.
func TestCodeLensInferredVariable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy := fileURI(filepath.Join(dir, "policy.yaml"))
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c := start(t)
	initializeIn(t, c, dir, refreshCaps)
	c.openAll(lens, strings.Replace(replicaLimitLens, "TYPE", "integer", 1), policy, replicaLimitPolicy)
	at := func(needle string) int {
		line, _ := lineOf(t, replicaLimitPolicy, needle)
		return line
	}
	lines := c.codeLenses(policy)
	if len(lines) != 3 {
		t.Fatalf("code lenses = %+v, want three lines", lines)
	}
	params := []string{"object: apps/v1 Deployment · compiles", "params: {maxReplicas, …} · Edit", "Remove"}
	if l := lines[0]; l.line != at("expression: params.maxReplicas") || !reflect.DeepEqual(l.titles, params) {
		t.Errorf("line above the variable = %+v, want %q", l, params)
	}
	want := lensLine{
		line: at("object.spec.replicas <= variables.limit"), titles: []string{"object: apps/v1 Deployment · compiles", "variables.limit: int"},
		commands: []string{show, show},
	}
	if !reflect.DeepEqual(lines[1], want) {
		t.Errorf("line above the rule reading variables.limit = %+v, want %+v", lines[1], want)
	}
	// The review of #22: the part shows the entry the type follows from, at
	// the first key of its schema.
	res, rerr := c.call("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": policy}})
	if rerr != nil {
		t.Fatal(rerr)
	}
	var raw []codeLens
	if err := json.Unmarshal(res, &raw); err != nil {
		t.Fatal(err)
	}
	from := `{"range":{"end":{"character":6,"line":6},"start":{"character":6,"line":6}},"uri":"` + lens + `"}`
	if args, _ := json.Marshal(raw[4].Command.Arguments); raw[4].Command.Title != "variables.limit: int" || string(args) != "["+from+"]" {
		t.Errorf("part variables.limit: int = %q %s, want %s with %s", raw[4].Command.Title, args, show, from)
	}
	var h struct{ Contents struct{ Value string } }
	c.request("textDocument/hover", policy, replicaLimitPolicy, "variables.limit", len("variables.l"), &h)
	if !strings.Contains(h.Contents.Value, "(field) limit: int") {
		t.Errorf("hover on variables.limit = %q, want limit: int", h.Contents.Value)
	}

	c.changeURI(lens, 2, strings.Replace(replicaLimitLens, "TYPE", "number", 1))
	res, refreshed := c.codeLensSeeingRefresh(policy)
	if !refreshed || !strings.Contains(string(res), `"variables.limit: double"`) {
		t.Errorf("after maxReplicas became a number: refreshed %t, code lenses %s, want a refresh and variables.limit: double", refreshed, res)
	}

	more := fileURI(filepath.Join(dir, "more.yaml"))
	moreText := strings.Replace(replicaLimitPolicy, "      expression: params.maxReplicas\n",
		"      expression: params.maxReplicas\n    - name: whole\n      expression: params\n    - name: named\n      expression: params.maxReplicas\n", 1)
	moreText = strings.Replace(moreText, "object.spec.replicas <= variables.limit", "has(variables.whole.team)", 1)
	moreText = strings.Replace(moreText, "params.teem == '' || object.metadata.labels['team'] == params.teem", "variables.named != ''", 1)
	c.openAll(more, moreText)
	c.changeURI(lens, 3, strings.Replace(replicaLimitLens, "TYPE", "integer", 1)+
		"  - file: more.yaml\n    document: ValidatingAdmissionPolicy replica-limit\n    variable: variables.named\n    type: string\n")
	lines = c.codeLenses(more)
	if len(lines) != 5 {
		t.Fatalf("code lenses of more.yaml = %+v, want five lines", lines)
	}
	target := map[string]any{"cellens": lens, "file": "more.yaml", "document": "ValidatingAdmissionPolicy replica-limit", "value": "variables.whole", "variable": "variables.whole"}
	for i, want := range []lensLine{
		{
			line: at("object.spec.replicas <= variables.limit") + 4, titles: []string{"object: apps/v1 Deployment · compiles", "variables.whole: dyn · Give it a type"},
			commands: []string{show, "cel-lens.giveType"}, target: target,
		},
		{
			line: at("params.teem == ''") + 4, titles: []string{"object: apps/v1 Deployment · compiles", "variables.named: string · Edit", "Remove"},
			commands: []string{show, "cel-lens.editType", "cel-lens.removeType"},
			target:   map[string]any{"cellens": lens, "file": "more.yaml", "document": "ValidatingAdmissionPolicy replica-limit", "value": "variables.named", "variable": "variables.named", "type": "string"},
		},
	} {
		if got := lines[3+i]; !reflect.DeepEqual(got, want) {
			t.Errorf("line above the rule reading %s = %+v,\nwant %+v", want.target["value"], got, want)
		}
	}
}

// TestCodeLensSchemaOrder is #29: the schema a line's target carries is the
// entry's, written as the engine writes every schema: the keywords of each
// schema in the order the API server writes them, whatever the .cellens
// writes, and the properties in the order the .cellens writes them, as the
// raw JSON of the answer shows (a map would hide both).
func TestCodeLensSchemaOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy := fileURI(filepath.Join(dir, "policy.yaml"))
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	c.openAll(fileURI(filepath.Join(dir, ".cellens")), `types:
  - file: policy.yaml
    document: ValidatingAdmissionPolicy limits
    variable: params
    schema:
      properties:
        x: {x-kubernetes-int-or-string: true, description: anything}
        max: {minimum: 0, type: integer}
      required: [max]
      type: object
`, policy, inlinePolicy)
	res, rerr := c.call("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": policy}})
	if rerr != nil {
		t.Fatal(rerr)
	}
	want := `"schema":{"type":"object","required":["max"],"properties":{"x":{"description":"anything","x-kubernetes-int-or-string":true},"max":{"type":"integer","minimum":0}}}`
	if !strings.Contains(string(res), `"variable":"params"`) || !strings.Contains(string(res), want) {
		t.Errorf("code lenses = %s,\nwant params's target with %s", res, want)
	}
}
