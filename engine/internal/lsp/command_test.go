package lsp_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// editing is the workspace capabilities of a client that applies edits with
// documentChanges and the resource operations ops.
func editing(ops ...string) map[string]any {
	return map[string]any{"workspace": map[string]any{
		"applyEdit":     true,
		"workspaceEdit": map[string]any{"documentChanges": true, "resourceOperations": ops},
	}}
}

// appliedEdit is the params of a workspace/applyEdit.
type appliedEdit struct {
	Label string
	Edit  struct {
		Changes         map[string][]lspEdit
		DocumentChanges []struct {
			Kind         string
			URI          string
			TextDocument struct {
				URI     string
				Version *int
			}
			Edits []lspEdit
		}
	}
}

type lspEdit struct {
	Range struct {
		Start, End struct{ Line, Character int }
	}
	NewText string
}

// command runs the command with args, answers the workspace/applyEdit it
// sends, if any, with answer as its result, or its error when answer is a
// jsonrpc.Error, and returns that edit (nil when none came), the command's
// result and its error.
func (c *client) command(name string, args, answer any) (*appliedEdit, json.RawMessage, *jsonrpc.Error) {
	c.t.Helper()
	c.nextID++
	id := strconv.Itoa(c.nextID)
	sent := []<-chan error{c.post(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": "workspace/executeCommand", "params": map[string]any{"command": name, "arguments": []any{args}}})}
	var edit *appliedEdit
	for {
		m := c.read("the response to " + name)
		switch {
		case m.Method == "workspace/applyEdit":
			edit = new(appliedEdit)
			if err := json.Unmarshal(m.Params, edit); err != nil {
				c.t.Fatal(err)
			}
			if e, ok := answer.(jsonrpc.Error); ok {
				sent = append(sent, c.post(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": e}))
			} else {
				sent = append(sent, c.post(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": answer}))
			}
		case m.Method == "" && string(m.ID) == id:
			for _, done := range sent {
				if err := <-done; err != nil {
					c.t.Fatalf("write to server: %v", err)
				}
			}
			return edit, m.Result, m.Error
		}
	}
}

// post sends v while the test goes on reading: the server may be writing a
// message the client has not read, such as diagnostics at the end of the
// workspace scan, and then reads nothing until the test reads that one. The
// channel gives the error of the write when it ends, which it does before the
// server answers v, or when the test ends and closes the pipe.
func (c *client) post(v any) <-chan error {
	done := make(chan error, 1)
	body, err := json.Marshal(v)
	if err != nil {
		done <- err
		return done
	}
	go func() {
		_, err := fmt.Fprintf(c.out, "Content-Length: %d\r\n\r\n%s", len(body), body)
		done <- err
	}()
	return done
}

var applied = map[string]any{"applied": true}

// apply makes edits, with the protocol's positions, on text.
func apply(t *testing.T, text string, edits []lspEdit) string {
	t.Helper()
	offset := func(line, char int) int {
		off := 0
		for range line {
			i := strings.IndexByte(text[off:], '\n')
			if i < 0 {
				t.Fatalf("line %d is past the end of %q", line, text)
			}
			off += i + 1
		}
		for units := 0; units < char; {
			r, size := utf8.DecodeRuneInString(text[off:])
			units += utf16.RuneLen(r)
			off += size
		}
		return off
	}
	for _, e := range slices.Backward(edits) {
		start, end := offset(e.Range.Start.Line, e.Range.Start.Character), offset(e.Range.End.Line, e.Range.End.Character)
		text = text[:start] + e.NewText + text[end:]
	}
	return text
}

// fileText reads the file at p.
func fileText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// configArgs are the arguments the line above lensCRD's rules gives its
// commands for spec.config, with the .cellens at lens.
func configArgs(lens, file string) map[string]any {
	return map[string]any{
		"cellens": fileURI(lens), "file": file, "document": "CustomResourceDefinition widgets.example.com",
		"version": "v1", "path": "spec.config", "value": "self.spec.config",
	}
}

func with(args map[string]any, key string, value any) map[string]any {
	out := map[string]any{key: value}
	for k, v := range args {
		if k != key {
			out[k] = v
		}
	}
	return out
}

// TestExecuteCommandProvider checks that initialize declares the four
// commands (#4).
func TestExecuteCommandProvider(t *testing.T) {
	t.Parallel()
	c := start(t)
	res, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil})
	if rerr != nil {
		t.Fatal(rerr)
	}
	var r struct {
		Capabilities struct {
			ExecuteCommandProvider struct{ Commands []string }
		}
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	want := []string{"cel-lens.typeChoices", "cel-lens.checkType", "cel-lens.setType", "cel-lens.removeType"}
	if got := r.Capabilities.ExecuteCommandProvider.Commands; !slices.Equal(got, want) {
		t.Errorf("executeCommandProvider.commands = %v, want %v", got, want)
	}
}

type choice struct{ APIVersion, Group, Version, Kind, Description, Stability string }

type choices struct {
	KubernetesVersion string
	BuiltIn, CRDs     []choice
	CRDsNote          string
}

func (c *client) typeChoices() choices {
	c.t.Helper()
	_, res, rerr := c.command("cel-lens.typeChoices", nil, nil)
	if rerr != nil {
		c.t.Fatalf("typeChoices error = %+v", rerr)
	}
	var out choices
	if err := json.Unmarshal(res, &out); err != nil {
		c.t.Fatal(err)
	}
	return out
}

func hasChoice(list []choice, apiVersion, kind string) (choice, bool) {
	i := slices.IndexFunc(list, func(c choice) bool { return c.APIVersion == apiVersion && c.Kind == kind })
	if i < 0 {
		return choice{}, false
	}
	return list[i], true
}

// TestTypeChoices checks that typeChoices lists the built-in kinds of the
// Kubernetes version in use and the CRDs of the workspace scan, each with a
// one-line description (#4).
func TestTypeChoices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), strings.Replace(widgetCRDText, "          type: object\n", "          description: |\n            A widget. It has parts.\n            More here.\n          type: object\n", 1))
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	// The scan runs in the background: the policy, typed from the CRD once
	// the scan ends, shows when it has.
	c.openURI(policyURI, strings.Replace(widgetPolicyText, "params.spec.maxReplicas", "params.spec.maxReplica", 1))
	diags := c.nextDiagnostics()
	for len(diags) == 0 {
		diags = c.nextDiagnostics()
	}
	got := c.typeChoices()
	if got.KubernetesVersion != "1.37" || got.CRDsNote != "" {
		t.Errorf("typeChoices = version %q, note %q, want 1.37 and no note", got.KubernetesVersion, got.CRDsNote)
	}
	if d, ok := hasChoice(got.BuiltIn, "apps/v1", "Deployment"); !ok || d != (choice{"apps/v1", "apps", "v1", "Deployment", "Deployment enables declarative updates for Pods and ReplicaSets.", "ga"}) {
		t.Errorf("typeChoices builtIn apps/v1 Deployment = %+v, %t, want it with group, version, kind and description", d, ok)
	}
	if p, ok := hasChoice(got.BuiltIn, "v1", "Pod"); !ok || p.Group != "" || !strings.HasPrefix(p.Description, "Pod is a collection of containers") || strings.Contains(p.Description, "\n") {
		t.Errorf("typeChoices builtIn v1 Pod = %+v, %t, want the core group and one line", p, ok)
	}
	if w, ok := hasChoice(got.CRDs, "example.com/v1", "Widget"); !ok || w != (choice{"example.com/v1", "example.com", "v1", "Widget", "A widget.", ""}) {
		t.Errorf("typeChoices crds = %+v, want example.com/v1 Widget with its description's first sentence", got.CRDs)
	}
	if _, ok := hasChoice(got.BuiltIn, "admissionregistration.k8s.io/v1", "MutatingAdmissionPolicy"); !ok {
		t.Error("typeChoices builtIn at 1.37 lacks admissionregistration.k8s.io/v1 MutatingAdmissionPolicy")
	}
	if b, _ := hasChoice(got.BuiltIn, "admissionregistration.k8s.io/v1beta1", "MutatingAdmissionPolicy"); b.Stability != "beta" {
		t.Errorf("typeChoices builtIn admissionregistration.k8s.io/v1beta1 MutatingAdmissionPolicy = %+v, want it beta", b)
	}

	t.Run("an older version", func(t *testing.T) {
		t.Parallel()
		c := start(t)
		initializeWith(t, c, map[string]any{"kubernetesVersion": "1.33"})
		got := c.typeChoices()
		_, beta := hasChoice(got.BuiltIn, "admissionregistration.k8s.io/v1beta1", "MutatingAdmissionPolicy")
		a, alpha := hasChoice(got.BuiltIn, "admissionregistration.k8s.io/v1alpha1", "MutatingAdmissionPolicy")
		if got.KubernetesVersion != "1.33" || beta || !alpha || a.Stability != "alpha" {
			t.Errorf("typeChoices at 1.33 = version %s, v1beta1 %t, v1alpha1 %+v; want 1.33 with v1alpha1 only, alpha, as v1beta1 came in 1.34", got.KubernetesVersion, beta, a)
		}
	})
	t.Run("crd-typed-policies off", func(t *testing.T) {
		t.Parallel()
		c := scanSession(t, dir)
		got := c.typeChoices()
		if len(got.CRDs) != 0 || !strings.Contains(got.CRDsNote, "crd-typed-policies") || len(got.BuiltIn) == 0 {
			t.Errorf("typeChoices with crd-typed-policies off = %d CRDs, note %q, want none and a note naming the feature", len(got.CRDs), got.CRDsNote)
		}
	})
}

// TestCheckType checks that checkType finds no problem in a valid type, and
// gives the message and column of one that does not parse, and what an entry
// would get for the value the arguments name (#4).
func TestCheckType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	lens := filepath.Join(dir, ".cellens")
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	for _, tt := range []struct {
		name    string
		args    map[string]any
		message string
		column  int
	}{
		{"valid", map[string]any{"type": "{name: string, ports: list({port: int})}"}, "", 0},
		{"unknown type", map[string]any{"type": "{name: strin}"}, `unknown type "strin": use string, int, uint, double, bool, bytes, timestamp, duration, list(T), map(string, T) or {field: T}`, 8},
		{"cut short", map[string]any{"type": "list(int"}, `the type ends where ")" is missing`, 9},
		{"columns count characters", map[string]any{"type": "{é: strin}"}, `unknown type "strin": use string, int, uint, double, bool, bytes, timestamp, duration, list(T), map(string, T) or {field: T}`, 5},
		{"fits the value", with(configArgs(lens, "crd.yaml"), "type", "int"), "", 0},
		{"a struct for an int-or-string", with(configArgs(lens, "crd.yaml"), "type", "{max: int}"), "spec.config is x-kubernetes-int-or-string, an int or a string to a rule: give it int or string", 0},
		{"a typed value", with(with(configArgs(lens, "crd.yaml"), "path", "spec.name"), "type", "string"), "spec.name is already typed as string: remove this entry", 0},
		{"no such file", with(configArgs(lens, "nosuch.yaml"), "type", "int"), "file nosuch.yaml does not exist", 0},
		{"the kind in the arguments is not checked", with(with(configArgs(lens, "crd.yaml"), "type", "string"), "kind", "v1 Pod"), "", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			_, res, rerr := c.command("cel-lens.checkType", tt.args, nil)
			if rerr != nil {
				t.Fatalf("checkType(%v) error = %+v", tt.args, rerr)
			}
			if tt.message == "" {
				if string(res) != "null" {
					t.Errorf("checkType(%v) = %s, want null", tt.args, res)
				}
				return
			}
			var p struct {
				Message string
				Column  *int
			}
			if err := json.Unmarshal(res, &p); err != nil {
				t.Fatal(err)
			}
			column := 0
			if p.Column != nil {
				column = *p.Column
			}
			if p.Message != tt.message || column != tt.column || (tt.column == 0) != (p.Column == nil) {
				t.Errorf("checkType(%v) = %s, want message %q and column %d", tt.args, res, tt.message, tt.column)
			}
		})
	}
	t.Run("no type", func(t *testing.T) {
		c.use(t)
		if _, _, rerr := c.command("cel-lens.checkType", map[string]any{}, nil); rerr == nil || rerr.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("checkType({}) error = %+v, want invalid params", rerr)
		}
	})
}

// nested is a workspace with a .cellens at the root and one in sub, beside
// lensCRD, both with comments a person wrote.
type nested struct {
	dir, root, sub, crd string
	rootText, subText   string
}

func newNested(t *testing.T, rootText string) nested {
	t.Helper()
	dir := t.TempDir()
	n := nested{dir: dir, root: filepath.Join(dir, ".cellens"), sub: filepath.Join(dir, "sub", ".cellens"), crd: filepath.Join(dir, "sub", "crd.yaml"), rootText: rootText}
	n.subText = `# Types of sub, by the platform team.
types:
  # The gadgets' size.
  - file: a.yaml
    document: CustomResourceDefinition gadgets.example.com
    version: v1
    # it is a count
    path: spec.size
    type: int

  # The z kind.
  - file: z.yaml
    document: CustomResourceDefinition z.example.com
    version: v1
    type: string  # for now
# End.
`
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, n.root, n.rootText)
	writeFile(t, n.sub, n.subText)
	writeFile(t, n.crd, lensCRD)
	return n
}

// configEntry is the entry setType writes for spec.config of crd.yaml.
const configEntry = `  - file: crd.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.config
    type: int
`

// TestSetTypeNearestLens checks that setType adds the entry to the nearest
// .cellens at or above the file, in order, through workspace/applyEdit, with
// the comments kept and nothing written to disk (#4).
func TestSetTypeNearestLens(t *testing.T) {
	t.Parallel()
	n := newNested(t, "# Root types.\ntypes:\n  - file: other.yaml\n    document: CustomResourceDefinition o.example.com\n    version: v1\n    type: int\n")
	c := start(t)
	initializeIn(t, c, n.dir, editing("create", "delete"))
	// The arguments name the file from the root's .cellens: sub's is nearer.
	edit, res, rerr := c.command("cel-lens.setType", with(configArgs(n.root, "sub/crd.yaml"), "type", "int"), applied)
	if rerr != nil {
		t.Fatalf("setType error = %+v", rerr)
	}
	if edit == nil || len(edit.Edit.DocumentChanges) != 1 {
		t.Fatalf("setType sent %+v, want one workspace/applyEdit with one change", edit)
	}
	dc := edit.Edit.DocumentChanges[0]
	if dc.TextDocument.URI != fileURI(n.sub) || dc.TextDocument.Version != nil || dc.Kind != "" {
		t.Errorf("setType edits %s version %v, want %s, not open", dc.TextDocument.URI, dc.TextDocument.Version, fileURI(n.sub))
	}
	want := strings.Replace(n.subText, "  # The z kind.", configEntry+"  # The z kind.", 1)
	if got := apply(t, n.subText, dc.Edits); got != want {
		t.Errorf("setType makes sub's .cellens\n%s\nwant\n%s", got, want)
	}
	if edit.Label != "Give self.spec.config a type" {
		t.Errorf("setType label = %q", edit.Label)
	}
	var r struct{ Cellens, Change string }
	if err := json.Unmarshal(res, &r); err != nil || r.Cellens != fileURI(n.sub) || r.Change != "added" {
		t.Errorf("setType = %s, want sub's .cellens, added", res)
	}
	if fileText(t, n.sub) != n.subText || fileText(t, n.root) != n.rootText {
		t.Error("setType wrote to disk; only the editor may")
	}
}

// TestSetTypeReplacesEntry checks that setType replaces the type of the entry
// that gives the value its type, in whichever .cellens it is, keeping the
// comments around and inside it (#4).
func TestSetTypeReplacesEntry(t *testing.T) {
	t.Parallel()
	rootText := `# Root types.
types:
  # spec.config of the widgets.
  - file: sub/crd.yaml
    document: CustomResourceDefinition widgets.example.com
    # the served version
    version: v1
    path: spec.config
    type: string  # names, for now
`
	n := newNested(t, rootText)
	c := start(t)
	initializeIn(t, c, n.dir, editing())
	edit, _, rerr := c.command("cel-lens.setType", with(with(configArgs(n.sub, "crd.yaml"), "type", "int"), "kind", "v1 Pod"), applied)
	if edit != nil || rerr == nil || !strings.Contains(rerr.Message, "give type or kind, not both") {
		t.Errorf("setType with type and kind = edit %v, error %+v, want no edit and an error: both are given", edit != nil, rerr)
	}
	// A kind of null is no kind, as the client may pass the arguments of
	// Edit with the type it chose.
	edit, res, rerr := c.command("cel-lens.setType", with(with(configArgs(n.sub, "crd.yaml"), "type", "int"), "kind", nil), applied)
	if rerr != nil {
		t.Fatalf("setType error = %+v", rerr)
	}
	dc := edit.Edit.DocumentChanges[0]
	want := strings.Replace(rootText, "type: string  # names, for now", "type: int  # names, for now", 1)
	if got := apply(t, rootText, dc.Edits); dc.TextDocument.URI != fileURI(n.root) || got != want {
		t.Errorf("setType edits %s to\n%s\nwant %s to\n%s", dc.TextDocument.URI, got, fileURI(n.root), want)
	}
	if !strings.Contains(string(res), `"change":"replaced"`) {
		t.Errorf("setType = %s, want replaced", res)
	}
}

// TestSetTypeCreatesLens checks that setType creates .cellens with the header
// at the root of the workspace folder when no .cellens is at or above the file,
// and only when the client can create files (#4).
func TestSetTypeCreatesLens(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "a", "b", "crd.yaml"), lensCRD)
	lens := filepath.Join(dir, "a", "b", ".cellens")
	c := start(t)
	initializeIn(t, c, dir, editing("create"))
	edit, res, rerr := c.command("cel-lens.setType", with(configArgs(lens, "crd.yaml"), "type", "int"), applied)
	if rerr != nil {
		t.Fatalf("setType error = %+v", rerr)
	}
	root := fileURI(filepath.Join(dir, ".cellens"))
	changes := edit.Edit.DocumentChanges
	if len(changes) != 2 || changes[0].Kind != "create" || changes[0].URI != root || changes[1].TextDocument.URI != root {
		t.Fatalf("setType changes = %+v, want the creation of %s and its text", changes, root)
	}
	want := cellens.Header + "\ntypes:\n" + strings.Replace(configEntry, "file: crd.yaml", "file: a/b/crd.yaml", 1)
	if got := apply(t, "", changes[1].Edits); got != want {
		t.Errorf("setType writes\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(string(res), `"change":"created"`) {
		t.Errorf("setType = %s, want created", res)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cellens")); err == nil {
		t.Error("setType created .cellens on disk; only the editor may")
	}

	t.Run("a client that cannot create files", func(t *testing.T) {
		t.Parallel()
		c := start(t)
		initializeIn(t, c, dir, map[string]any{"workspace": map[string]any{"applyEdit": true}})
		edit, _, rerr := c.command("cel-lens.setType", with(configArgs(lens, "crd.yaml"), "type", "int"), applied)
		if edit != nil || rerr == nil || rerr.Code != jsonrpc.CodeRequestFailed || !strings.Contains(rerr.Message, "cannot create files") {
			t.Errorf("setType = edit %+v, error %+v, want no edit and an error that the editor cannot create files", edit, rerr)
		}
	})
}

// TestSetTypeOpenLens checks that setType edits the editor's text of an open
// .cellens, at its version, and the plain changes of a client without
// documentChanges (#4).
func TestSetTypeOpenLens(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	lens := filepath.Join(dir, ".cellens")
	writeFile(t, lens, "types:\n")
	buffer := "# Being written.\ntypes:\n  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    type: int\n"
	for _, tt := range []struct {
		name            string
		documentChanges bool
	}{{"documentChanges", true}, {"changes", false}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := start(t)
			initializeIn(t, c, dir, map[string]any{"workspace": map[string]any{"applyEdit": true, "workspaceEdit": map[string]any{"documentChanges": tt.documentChanges}}})
			c.openURI(fileURI(lens), "")
			c.changeURI(fileURI(lens), 3, buffer)
			// The buffer's entry names a file that does not exist.
			c.diagnosticsOf(fileURI(lens), hasMessage("file a.yaml does not exist"))
			edit, _, rerr := c.command("cel-lens.setType", with(configArgs(lens, "crd.yaml"), "type", "int"), applied)
			if rerr != nil {
				t.Fatalf("setType error = %+v", rerr)
			}
			edits := edit.Edit.Changes[fileURI(lens)]
			if tt.documentChanges {
				dc := edit.Edit.DocumentChanges[0]
				if dc.TextDocument.Version == nil || *dc.TextDocument.Version != 3 {
					t.Errorf("setType edits version %v, want the editor's 3", dc.TextDocument.Version)
				}
				edits = dc.Edits
			}
			if got, want := apply(t, buffer, edits), buffer+configEntry; got != want {
				t.Errorf("setType makes the buffer\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// TestEditRefused checks that a client that does not apply the edit gets an
// error saying so, and that nothing is written to disk (#4).
func TestEditRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	lens := filepath.Join(dir, ".cellens")
	writeFile(t, lens, lensText)
	c := start(t)
	initializeIn(t, c, dir, editing("delete"))
	for _, tt := range []struct {
		name    string
		command string
		answer  any
		want    string
	}{
		{"setType, refused", "cel-lens.setType", map[string]any{"applied": false, "failureReason": "the file changed"}, "the editor refused the edit to .cellens (the file changed): nothing was written"},
		{"removeType, refused", "cel-lens.removeType", map[string]any{"applied": false}, "the editor refused the edit to .cellens: nothing was written"},
		{"setType, an error", "cel-lens.setType", jsonrpc.Error{Code: -32603, Message: "boom"}, "the editor did not apply the edit to .cellens (boom): nothing was written"},
		{"setType, no result", "cel-lens.setType", nil, "the editor refused the edit to .cellens: nothing was written"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			edit, _, rerr := c.command(tt.command, with(configArgs(lens, "crd.yaml"), "type", "string"), tt.answer)
			if edit == nil || rerr == nil || rerr.Code != jsonrpc.CodeRequestFailed || rerr.Message != tt.want {
				t.Errorf("%s = edit %v, error %+v, want an edit and the error %q", tt.command, edit != nil, rerr, tt.want)
			}
			if fileText(t, lens) != lensText {
				t.Error("the .cellens changed on disk")
			}
		})
	}
	t.Run("a client without applyEdit", func(t *testing.T) {
		t.Parallel()
		c := start(t)
		initializeIn(t, c, dir, map[string]any{})
		edit, _, rerr := c.command("cel-lens.setType", with(configArgs(lens, "crd.yaml"), "type", "string"), applied)
		if edit != nil || rerr == nil || !strings.Contains(rerr.Message, "workspace/applyEdit") {
			t.Errorf("setType = edit %v, error %+v, want no edit and an error naming workspace/applyEdit", edit != nil, rerr)
		}
	})
}

// TestSetTypeRefusesWhatAnEntryWould checks that setType writes no entry that
// would get a problem, and says why (#4).
func TestSetTypeRefusesWhatAnEntryWould(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	lens := filepath.Join(dir, ".cellens")
	c := start(t)
	initializeIn(t, c, dir, editing("create"))
	for _, tt := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"a struct for an int-or-string", with(configArgs(lens, "crd.yaml"), "type", "{max: int}"), "cel-lens.setType: an entry for self.spec.config would not apply: spec.config is x-kubernetes-int-or-string, an int or a string to a rule: give it int or string"},
		{"a type that does not parse", with(configArgs(lens, "crd.yaml"), "type", "strin"), `cel-lens.setType: type: character 1: unknown type "strin"`},
		{"a kind", with(configArgs(lens, "crd.yaml"), "kind", "Widget"), `cel-lens.setType: kind "Widget": write the apiVersion and kind`},
		{"neither", configArgs(lens, "crd.yaml"), "cel-lens.setType: give the type"},
		{"a document", with(with(configArgs(lens, "crd.yaml"), "document", "widgets"), "type", "int"), `cel-lens.setType: document "widgets": write its kind and metadata.name`},
		{"a file outside", with(configArgs(lens, "../crd.yaml"), "type", "int"), `cel-lens.setType: file "../crd.yaml" is not a file in this .cellens's folder`},
		{"no .cellens", with(with(configArgs(lens, "crd.yaml"), "cellens", fileURI(filepath.Join(dir, "crd.yaml"))), "type", "int"), "is not the file URI of a .cellens"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			edit, _, rerr := c.command("cel-lens.setType", tt.args, applied)
			if edit != nil || rerr == nil || rerr.Code != jsonrpc.CodeInvalidParams || !strings.Contains(rerr.Message, tt.want) {
				t.Errorf("setType(%v) = edit %v, error %+v, want no edit and an error with %q", tt.args, edit != nil, rerr, tt.want)
			}
		})
	}
}

// TestRemoveType checks that removeType removes the entry, keeping the other
// comments, and the .cellens when nothing but the header is left and the
// client can delete files (#4).
func TestRemoveType(t *testing.T) {
	t.Parallel()
	rootText := `# Root types.
types:
  # spec.config of the widgets.
  - file: sub/crd.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.config
    type: int

  # Another one.
  - file: sub/crd.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.size
    type: int  # inside
# End.
`
	n := newNested(t, rootText)
	c := start(t)
	initializeIn(t, c, n.dir, editing("delete"))
	edit, res, rerr := c.command("cel-lens.removeType", with(configArgs(n.sub, "crd.yaml"), "type", "int"), applied)
	if rerr != nil {
		t.Fatalf("removeType error = %+v", rerr)
	}
	dc := edit.Edit.DocumentChanges[0]
	_, rest, _ := strings.Cut(rootText, "\n\n")
	want := "# Root types.\ntypes:\n" + rest
	if got := apply(t, rootText, dc.Edits); dc.TextDocument.URI != fileURI(n.root) || got != want {
		t.Errorf("removeType edits %s to\n%s\nwant %s to\n%s", dc.TextDocument.URI, got, fileURI(n.root), want)
	}
	if !strings.Contains(string(res), `"change":"removed"`) || edit.Label != "Remove the type of self.spec.config" {
		t.Errorf("removeType = %s, label %q, want removed", res, edit.Label)
	}
	if _, _, rerr := c.command("cel-lens.removeType", with(with(configArgs(n.sub, "crd.yaml"), "path", "spec.name"), "value", ""), applied); rerr == nil || !strings.Contains(rerr.Message, "no .cellens gives self.spec.name a type") {
		t.Errorf("removeType of an untyped value error = %+v, want that no .cellens gives it a type", rerr)
	}
	if fileText(t, n.root) != rootText {
		t.Error("removeType wrote to disk; only the editor may")
	}

	// The root's .cellens with only the header and the entry.
	writeFile(t, n.root, cellens.Header+"\ntypes:\n"+strings.Replace(configEntry, "file: crd.yaml", "file: sub/crd.yaml", 1))
	t.Run("the last entry and the file, with delete", func(t *testing.T) {
		c.use(t)
		edit, res, rerr := c.command("cel-lens.removeType", configArgs(n.sub, "crd.yaml"), applied)
		if rerr != nil {
			t.Fatalf("removeType error = %+v", rerr)
		}
		changes := edit.Edit.DocumentChanges
		if len(changes) != 1 || changes[0].Kind != "delete" || changes[0].URI != fileURI(n.root) || !strings.Contains(string(res), `"change":"deleted"`) {
			t.Errorf("removeType = %+v, %s, want the deletion of %s", changes, res, fileURI(n.root))
		}
	})
	t.Run("the last entry, without delete", func(t *testing.T) {
		t.Parallel()
		c := start(t)
		initializeIn(t, c, n.dir, editing())
		edit, res, rerr := c.command("cel-lens.removeType", configArgs(n.sub, "crd.yaml"), applied)
		if rerr != nil {
			t.Fatalf("removeType error = %+v", rerr)
		}
		dc := edit.Edit.DocumentChanges[0]
		text := fileText(t, n.root)
		if got := apply(t, text, dc.Edits); got != cellens.Header+"\ntypes:\n" || !strings.Contains(string(res), `"change":"removed"`) {
			t.Errorf("removeType leaves %q, %s, want the header", got, res)
		}
	})
}

// TestPendingEditAtShutdown checks that a command waiting for the client's
// answer to its edit gets an error when the session shuts down (#4).
func TestPendingEditAtShutdown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	c := start(t)
	initializeIn(t, c, dir, editing("create"))
	command := c.post(map[string]any{"jsonrpc": "2.0", "id": 77, "method": "workspace/executeCommand", "params": map[string]any{
		"command": "cel-lens.setType", "arguments": []any{with(configArgs(filepath.Join(dir, ".cellens"), "crd.yaml"), "type", "int")},
	}})
	m := c.read("workspace/applyEdit")
	for m.Method != "workspace/applyEdit" {
		m = c.read("workspace/applyEdit")
	}
	if err := <-command; err != nil {
		t.Fatal(err)
	}
	stop := c.post(map[string]any{"jsonrpc": "2.0", "id": 78, "method": "shutdown"})
	var got []string
	for len(got) < 2 {
		m := c.read("the responses to setType and shutdown")
		switch string(m.ID) {
		case "77":
			if m.Error == nil || !strings.Contains(m.Error.Message, "shut down before the editor applied the edit") {
				t.Errorf("setType after shutdown = %+v, want an error", m)
			}
			got = append(got, "setType")
		case "78":
			got = append(got, "shutdown")
		}
	}
	if !slices.Equal(got, []string{"setType", "shutdown"}) {
		t.Errorf("responses = %v, want setType, then shutdown", got)
	}
	if err := <-stop; err != nil {
		t.Fatal(err)
	}
}

// postRequest sends a request with the id and returns the channel post gives.
func (c *client) postRequest(id int, method string, params any) <-chan error {
	return c.post(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
}

// until reads the server's messages until one satisfies done, and returns it.
func (c *client) until(want string, done func(jsonrpc.Message) bool) jsonrpc.Message {
	c.t.Helper()
	for {
		if m := c.read(want); done(m) {
			return m
		}
	}
}

func isApplyEdit(m jsonrpc.Message) bool { return m.Method == "workspace/applyEdit" }

func responseTo(id int) func(jsonrpc.Message) bool {
	return func(m jsonrpc.Message) bool { return m.Method == "" && string(m.ID) == strconv.Itoa(id) }
}

func executeCommand(name string, args any) map[string]any {
	return map[string]any{"command": name, "arguments": []any{args}}
}

// TestOverlappingEditsRefused checks that a command whose .cellens has an
// edit the editor has not answered yet is refused, rather than made on the
// text without that edit, which would land on the wrong lines (#4).
func TestOverlappingEditsRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	lens := filepath.Join(dir, ".cellens")
	var entries strings.Builder
	for _, p := range []string{"spec.a", "spec.b", "spec.c"} {
		entries.WriteString("  - file: crd.yaml\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    path: " + p + "\n    type: int\n")
	}
	writeFile(t, lens, "types:\n"+entries.String())
	c := start(t)
	initializeIn(t, c, dir, editing())
	first := c.postRequest(1001, "workspace/executeCommand", executeCommand("cel-lens.removeType", with(configArgs(lens, "crd.yaml"), "path", "spec.a")))
	edit := c.until("workspace/applyEdit", isApplyEdit)
	second := c.postRequest(1002, "workspace/executeCommand", executeCommand("cel-lens.removeType", with(configArgs(lens, "crd.yaml"), "path", "spec.b")))
	m := c.until("the response to the second removeType", func(m jsonrpc.Message) bool { return isApplyEdit(m) || responseTo(1002)(m) })
	if m.Method != "" || m.Error == nil || m.Error.Code != jsonrpc.CodeRequestFailed || m.Error.Message != "an edit of .cellens is waiting for the editor: run the command again once it is applied" {
		t.Fatalf("second removeType = %+v, want it refused while the first edit waits", m)
	}
	answer := c.post(map[string]any{"jsonrpc": "2.0", "id": edit.ID, "result": applied})
	if m := c.until("the response to the first removeType", responseTo(1001)); m.Error != nil {
		t.Errorf("first removeType error = %+v", m.Error)
	}
	for _, sent := range []<-chan error{first, second, answer} {
		if err := <-sent; err != nil {
			t.Fatal(err)
		}
	}
	// Once the editor answered, a command may edit the file again.
	if next, _, rerr := c.command("cel-lens.removeType", with(configArgs(lens, "crd.yaml"), "path", "spec.b"), applied); next == nil || rerr != nil {
		t.Errorf("removeType after the answer = edit %v, error %+v, want an edit", next != nil, rerr)
	}
}

// TestCancelWaitingCommand checks that a command waiting for the editor's
// answer to its edit is answered when the client cancels it, that its
// .cellens stays reserved until the editor answers the edit, which it may
// still apply, and that the late answer is then ignored (#4).
func TestCancelWaitingCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	lens := filepath.Join(dir, ".cellens")
	c := start(t)
	initializeIn(t, c, dir, editing("create"))
	sent := []<-chan error{c.postRequest(2001, "workspace/executeCommand", executeCommand("cel-lens.setType", with(configArgs(lens, "crd.yaml"), "type", "int")))}
	edit := c.until("workspace/applyEdit", isApplyEdit)
	sent = append(sent, c.post(map[string]any{"jsonrpc": "2.0", "method": "$/cancelRequest", "params": map[string]any{"id": 2001}}))
	if m := c.until("the response to the cancelled setType", responseTo(2001)); m.Error == nil || m.Error.Code != jsonrpc.CodeRequestCancelled {
		t.Errorf("cancelled setType = %+v, want the error RequestCancelled", m)
	}
	// Until the editor answers, a second edit is refused: it would be made on
	// the text without the first one, which may still land.
	if second, _, rerr := c.command("cel-lens.setType", with(configArgs(lens, "crd.yaml"), "type", "string"), applied); second != nil || rerr == nil || !strings.Contains(rerr.Message, "is waiting for the editor") {
		t.Errorf("setType after the cancel, before the answer = edit %v, error %+v, want it refused", second != nil, rerr)
	}
	// The late answer is ignored, and the file can be edited again. The answer
	// is written before the next command, so the server reads it first.
	if err := <-c.post(map[string]any{"jsonrpc": "2.0", "id": edit.ID, "result": applied}); err != nil {
		t.Fatal(err)
	}
	if next, _, rerr := c.command("cel-lens.setType", with(configArgs(lens, "crd.yaml"), "type", "int"), applied); next == nil || rerr != nil {
		t.Errorf("setType after the cancel = edit %v, error %+v, want an edit", next != nil, rerr)
	}
	for _, s := range sent {
		if err := <-s; err != nil {
			t.Fatal(err)
		}
	}
}

// TestCellensOutsideWorkspace checks that a command refuses a .cellens that is
// in no workspace folder and beside no open file (#4).
func TestCellensOutsideWorkspace(t *testing.T) {
	t.Parallel()
	dir, outside := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(outside, "crd.yaml"), lensCRD)
	args := with(configArgs(filepath.Join(outside, ".cellens"), "crd.yaml"), "type", "int")
	c := start(t)
	initializeIn(t, c, dir, editing("create"))
	for _, name := range []string{"cel-lens.checkType", "cel-lens.setType", "cel-lens.removeType"} {
		if edit, _, rerr := c.command(name, args, applied); edit != nil || rerr == nil || rerr.Code != jsonrpc.CodeInvalidParams || !strings.Contains(rerr.Message, "is in no workspace folder and beside no file open in the editor") {
			t.Errorf("%s(.cellens outside) = edit %v, error %+v, want it refused", name, edit != nil, rerr)
		}
	}
	c.openURI(fileURI(filepath.Join(outside, "crd.yaml")), lensCRD)
	if _, res, rerr := c.command("cel-lens.checkType", args, nil); rerr != nil || string(res) != "null" {
		t.Errorf("checkType(.cellens beside an open file) = %s, %+v, want null", res, rerr)
	}
}
