package lsp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// offNote is what hover adds to a dyn value a .cellens could type while
// cellens-types is off.
const offNote = "Giving it a type of your own in `.cellens` is the experimental feature `cellens-types`, which is off."

// typesText types spec.config of lensCRD, in crd.yaml, and params of
// inlinePolicy, in policy.yaml, both beside it.
const typesText = lensText + `  - file: policy.yaml
    document: ValidatingAdmissionPolicy limits
    variable: params
    type: "{max: int}"
`

// typesWorkspace writes crd.yaml (lensCRD), policy.yaml (inlinePolicy) and
// a .cellens that types a value of each (typesText) to a new folder, and
// returns it.
func typesWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	writeFile(t, filepath.Join(dir, "policy.yaml"), inlinePolicy)
	writeFile(t, filepath.Join(dir, ".cellens"), typesText)
	return dir
}

// initializeTypesOff initializes c with every experimental feature off,
// cellens-types among them, in folder unless it is "".
func initializeTypesOff(t *testing.T, c *client, folder string, caps map[string]any) {
	t.Helper()
	params := map[string]any{"processId": nil, "capabilities": caps, "rootUri": nil, "initializationOptions": map[string]any{"experimental": []string{}}}
	if folder != "" {
		params["workspaceFolders"] = []map[string]any{{"uri": fileURI(folder), "name": "ws"}}
	}
	if _, rerr := c.call("initialize", params); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
}

// callSeeing is call that also returns every other message the server sent
// before the response.
func (c *client) callSeeing(method string, params any) (json.RawMessage, *jsonrpc.Error, []jsonrpc.Message) {
	c.t.Helper()
	c.nextID++
	id := strconv.Itoa(c.nextID)
	c.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params})
	var seen []jsonrpc.Message
	for {
		m := c.read("the response to " + method)
		if m.Method == "" && string(m.ID) == id {
			return m.Result, m.Error, seen
		}
		seen = append(seen, m)
	}
}

// notices are the notices among msgs that cellens-types is off.
func notices(msgs []jsonrpc.Message) []jsonrpc.Message {
	var out []jsonrpc.Message
	for _, m := range msgs {
		if m.Method == "window/showMessageRequest" && strings.Contains(string(m.Params), "cellens-types") {
			out = append(out, m)
		}
	}
	return out
}

// TestTypesOffLine checks that, with cellens-types off, the line above a
// rule shows no Give it a type, Edit or Remove, though a .cellens of the
// workspace types a value the rule reads (#44).
func TestTypesOffLine(t *testing.T) {
	t.Parallel()
	dir := typesWorkspace(t)
	c := start(t)
	initializeTypesOff(t, c, dir, map[string]any{})
	crd, policy := fileURI(filepath.Join(dir, "crd.yaml")), fileURI(filepath.Join(dir, "policy.yaml"))
	c.openAll(crd, lensCRD, policy, inlinePolicy)
	for _, u := range []string{crd, policy} {
		lines := c.codeLenses(u)
		if len(lines) == 0 {
			t.Fatalf("code lenses of %s = none, want a line above each rule", u)
		}
		for _, l := range lines {
			if len(l.titles) != 1 || l.commands[0] != show {
				t.Errorf("line %d of %s = %q, want only the subject: no Give it a type, Edit or Remove", l.line, u, l.titles)
			}
		}
	}
}

// TestTypesOffHover checks that, with cellens-types off, a value a .cellens
// of the workspace types is dyn, in the checks and in hover, which says the
// feature is off on a dyn value a .cellens could type and only there (#44).
func TestTypesOffHover(t *testing.T) {
	t.Parallel()
	dir := typesWorkspace(t)
	c := start(t)
	initializeTypesOff(t, c, dir, map[string]any{})
	crd, policy := fileURI(filepath.Join(dir, "crd.yaml")), fileURI(filepath.Join(dir, "policy.yaml"))
	c.openURI(crd, lensCRD)
	if diags := c.diagnosticsOf(crd, func([]diagnostic) bool { return true }); len(diags) != 0 {
		t.Errorf("diagnostics of the CRD = %+v, want none: spec.config is dyn", diags)
	}
	c.openURI(policy, inlinePolicy)
	c.diagnosticsOf(policy, func([]diagnostic) bool { return true })
	tests := []struct {
		name, u, text, at string
		n                 int
		want, notWant     []string
	}{
		{"int-or-string of a CRD", crd, lensCRD, "self.spec.config > 0", len("self.spec.con"), []string{offNote}, []string{"from .cellens", "(field) config: int\n"}},
		{"params of a policy", policy, inlinePolicy, "params.max", len("par"), []string{"params: dyn", offNote}, []string{"from .cellens"}},
		{"object of a policy, typed by its kind", policy, inlinePolicy, "object.spec", len("obj"), []string{"apps/v1 Deployment"}, []string{offNote}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			var hover *struct{ Contents struct{ Value string } }
			c.request("textDocument/hover", tt.u, tt.text, tt.at, tt.n, &hover)
			got := ""
			if hover != nil {
				got = hover.Contents.Value
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("hover = %q, want it to contain %q", got, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("hover = %q, want no %q", got, w)
				}
			}
		})
	}
}

// TestTypesOffOpenLens checks that, with cellens-types off, an open .cellens
// gets no diagnostics, hover or completion, and that opening it sends the
// notice once, outside any workspace folder too (#44).
func TestTypesOffOpenLens(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	text := typesText + "  - file: missing.yaml\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    type: string\n"
	c := start(t)
	initializeTypesOff(t, c, "", map[string]any{})
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c.openURI(lens, text)
	c.changeURI(lens, 2, text+"  - file:\n")
	at := map[string]any{"textDocument": map[string]any{"uri": lens}, "position": map[string]any{"line": 1, "character": 5}}
	res, rerr, seen := c.callSeeing("textDocument/hover", at)
	if rerr != nil || string(res) != "null" {
		t.Errorf("hover in the .cellens = %s, %+v, want null", res, rerr)
	}
	res, rerr, more := c.callSeeing("textDocument/completion", at)
	if rerr != nil || string(res) != "null" {
		t.Errorf("completion in the .cellens = %s, %+v, want null", res, rerr)
	}
	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": lens}})
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	c.openURI(crd, lensCRD)
	_, _, last := c.callSeeing("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": crd}, "position": map[string]any{"line": 0, "character": 0}})
	seen = append(append(seen, more...), last...)
	for _, m := range seen {
		if m.Method == "textDocument/publishDiagnostics" && strings.Contains(string(m.Params), ".cellens") {
			t.Errorf("the server sent %s, want no diagnostics for the .cellens", m.Params)
		}
	}
	if n := len(notices(seen)); n != 1 {
		t.Errorf("the server sent %d notices, want one", n)
	}
}

// TestTypesOffCommands checks that, with cellens-types off, each command
// behind Give it a type answers that the feature is off (#44).
func TestTypesOffCommands(t *testing.T) {
	t.Parallel()
	dir := typesWorkspace(t)
	c := start(t)
	initializeTypesOff(t, c, dir, map[string]any{})
	arg := map[string]any{"cellens": fileURI(filepath.Join(dir, ".cellens")), "file": "policy.yaml", "document": "ValidatingAdmissionPolicy limits", "variable": "params", "value": "params", "type": "int"}
	for _, name := range []string{"cel-lens.typeChoices", "cel-lens.checkType", "cel-lens.setType", "cel-lens.removeType", "cel-lens.inferType"} {
		t.Run(name, func(t *testing.T) {
			c.use(t)
			_, _, rerr := c.command(name, arg, nil)
			if rerr == nil || rerr.Code != jsonrpc.CodeRequestFailed || !strings.Contains(rerr.Message, "the experimental feature cellens-types, which is off") {
				t.Errorf("%s error = %+v, want request failed: cellens-types is off", name, rerr)
			}
		})
	}
}

// TestTypesOffExampleResults checks that, with cellens-types off, the
// example results run with the values a .cellens of the workspace types as
// dyn: they are those of the same workspace without the .cellens (#44).
func TestTypesOffExampleResults(t *testing.T) {
	t.Parallel()
	dir := typesWorkspace(t)
	writeIn(t, filepath.Join(dir, "samples", "widget.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec: {config: 5}\n")
	results := func() resultsAnswer {
		c := start(t)
		initializeTypesOff(t, c, dir, map[string]any{})
		return c.exampleResults(map[string]any{"uri": fileURI(filepath.Join(dir, "crd.yaml"))})
	}
	off := results()
	if len(off.Rules) == 0 {
		t.Fatalf("results = %+v, want the CRD's rules", off)
	}
	if err := os.Remove(filepath.Join(dir, ".cellens")); err != nil {
		t.Fatal(err)
	}
	if without := results(); !reflect.DeepEqual(off, without) {
		t.Errorf("results with the .cellens = %+v, want those without it, %+v", off, without)
	}
}

// TestTypesOffNotice checks that, with cellens-types off, a workspace that
// holds a .cellens gets one notice per session that it is not read, with
// the action that turns the feature on, and no second one for a .cellens
// opened, created or changed after (#44).
func TestTypesOffNotice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIn(t, filepath.Join(dir, "deploy", ".cellens"), "types: []\n")
	c := start(t)
	initializeTypesOff(t, c, dir, map[string]any{})
	var notice jsonrpc.Message
	for notice.Method == "" {
		if m := c.read("the notice that cellens-types is off"); m.Method == "window/showMessageRequest" {
			notice = m
		}
	}
	var p struct {
		Type    int
		Message string
		Actions []struct{ Title, Experimental string }
	}
	if err := json.Unmarshal(notice.Params, &p); err != nil {
		t.Fatal(err)
	}
	const want = "cel-lens does not read deploy/.cellens: giving a value a type of your own in .cellens is the experimental feature cellens-types, which is off: add cellens-types to the experimental features (cel-lens.experimental in VS Code) to read it."
	if p.Type != 2 || p.Message != want {
		t.Errorf("notice = type %d %q, want type 2 %q", p.Type, p.Message, want)
	}
	if len(p.Actions) != 1 || p.Actions[0].Title != "Turn on cellens-types" || p.Actions[0].Experimental != "cellens-types" {
		t.Errorf("notice actions = %+v, want one that turns cellens-types on", p.Actions)
	}
	c.send(map[string]any{"jsonrpc": "2.0", "id": notice.ID, "result": map[string]any{"title": "Turn on cellens-types", "experimental": "cellens-types"}})

	other := filepath.Join(dir, "other", ".cellens")
	writeIn(t, other, "types: []\n")
	c.openURI(fileURI(filepath.Join(dir, "deploy", ".cellens")), "types: []\n")
	c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{
		{"uri": fileURI(other), "type": 1}, {"uri": fileURI(filepath.Dir(other)), "type": 1},
	}})
	if _, _, seen := c.callSeeing("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": "file:///none.yaml"}}); len(notices(seen)) != 0 {
		t.Errorf("the server sent %s after the notice, want no second one", notices(seen)[0].Params)
	}
}

// TestTypesSwitch checks that turning cellens-types on and off with
// workspace/didChangeConfiguration takes effect without a restart: on, the
// .cellens of the workspace is searched for and the open documents are
// checked again with its types, an open .cellens with the editor's text it
// had while off; off, they are checked again without them, the line above a
// rule loses Edit and the diagnostics of the .cellens are taken back (#44).
func TestTypesSwitch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	const missing = "  - file: missing.yaml\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    type: string\n"
	// On disk the .cellens types nothing; the editor's text types spec.config.
	writeFile(t, filepath.Join(dir, ".cellens"), lensOff+missing)
	c := start(t)
	initializeTypesOff(t, c, dir, refreshCaps)
	crd, lens := fileURI(filepath.Join(dir, "crd.yaml")), fileURI(filepath.Join(dir, ".cellens"))
	c.openURI(lens, lensText+missing)
	c.openURI(crd, lensCRD)
	if diags := c.diagnosticsOf(crd, func([]diagnostic) bool { return true }); len(diags) != 0 {
		t.Errorf("diagnostics of the CRD while off = %+v, want none", diags)
	}
	edits := func() bool {
		for _, l := range c.codeLenses(crd) {
			for _, title := range l.titles {
				if strings.Contains(title, "Edit") {
					return true
				}
			}
		}
		return false
	}
	setting := func(names ...string) {
		c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": append([]string{}, names...)}})
	}
	missingWarning := hasMessage("file missing.yaml does not exist")

	setting("cellens-types")
	c.diagnosticsOf(crd, hasMessage(configWarning))
	c.diagnosticsOf(lens, missingWarning)
	if !edits() {
		t.Error("the line above the rule has no Edit once cellens-types is on, want one for spec.config")
	}

	setting()
	c.diagnosticsOf(crd, none)
	c.diagnosticsOf(lens, none)
	if edits() {
		t.Error("the line above the rule has Edit once cellens-types is off, want none")
	}

	// On again, with the .cellens closed: the search finds it on disk.
	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": lens}})
	setting("cellens-types")
	c.diagnosticsOf(lens, hasMessage("spec.name is already typed as string: remove this entry"))
}

// TestTypesTurnedOffSendsNoNotice checks that a session in which the person
// turns cellens-types off sends no notice that tells them to turn it on: not
// when they turn it off, nor for a .cellens changed, created or opened
// after (#44).
func TestTypesTurnedOffSendsNoNotice(t *testing.T) {
	t.Parallel()
	dir := typesWorkspace(t)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{}) // every experimental feature on
	crd, lens := fileURI(filepath.Join(dir, "crd.yaml")), fileURI(filepath.Join(dir, ".cellens"))
	c.openURI(crd, lensCRD)
	c.diagnosticsOf(crd, hasMessage(configWarning))
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": []string{}}})
	c.diagnosticsOf(crd, none)
	other := filepath.Join(dir, "other", ".cellens")
	writeIn(t, other, "types: []\n")
	c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{
		{"uri": lens, "type": 2}, {"uri": fileURI(other), "type": 1}, {"uri": fileURI(filepath.Dir(other)), "type": 1},
	}})
	c.openURI(lens, typesText)
	_, _, seen := c.callSeeing("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": crd}})
	if n := notices(seen); len(n) != 0 {
		t.Errorf("the server sent %s after cellens-types was turned off, want no notice", n[0].Params)
	}
}
