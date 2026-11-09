package lsp_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// marker marks the position in the texts of these tests.
const marker = "‸"

// at splits text at marker: the text without it, and the protocol's
// position of the marker, in UTF-16 units.
func at(t *testing.T, text string) (string, map[string]any) {
	t.Helper()
	before, after, ok := strings.Cut(text, marker)
	if !ok {
		t.Fatalf("no %s in %q", marker, text)
	}
	line := strings.Count(before, "\n")
	prefix := before[strings.LastIndex(before, "\n")+1:]
	return before + after, map[string]any{"line": line, "character": len(utf16.Encode([]rune(prefix)))}
}

// completionItem is a completion item as the server sends it.
type completionItem struct {
	Label            string
	Kind             int
	Detail           string
	InsertTextFormat int
	InsertTextMode   int
	TextEdit         struct {
		Range struct {
			Start, End struct{ Line, Character int }
		}
		NewText string
	}
}

// guided is a client with a workspace for completion and hover in a
// .cellens, which it has open.
type guided struct {
	*client
	dir, lens string
	version   int
}

// otherCRD is a CustomResourceDefinition without dyn values.
const otherCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.example.com
spec:
  group: example.com
  scope: Namespaced
  names: {plural: gadgets, singular: gadget, kind: Gadget, listKind: GadgetList}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema: {type: object, properties: {spec: {type: object, properties: {name: {type: string}}}}}
`

// newGuided writes a workspace, the Go one of goWorkspace with crd.yaml (the
// CRD of lensCRD, then otherCRD), policy.yaml (inlinePolicy) and YAML files
// in folders the scan skips, starts a server on it with caps and opens its
// .cellens, empty.
func newGuided(t *testing.T, caps map[string]any) *guided {
	t.Helper()
	dir := goWorkspace(t, "")
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD+"---\n"+otherCRD)
	writeFile(t, filepath.Join(dir, "policy.yaml"), inlinePolicy)
	for _, d := range []string{".git", "node_modules", "sub"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, d, "x.yml"), "a: 1\n")
	}
	g := &guided{client: start(t), dir: dir, lens: fileURI(filepath.Join(dir, ".cellens")), version: 1}
	initializeIn(t, g.client, dir, caps)
	g.openURI(g.lens, "")
	return g
}

// set makes text, with its marker taken out, the editor's text of the
// .cellens, and returns the position of the marker.
func (g *guided) set(text string) map[string]any {
	g.t.Helper()
	text, pos := at(g.t, text)
	g.version++
	g.changeURI(g.lens, g.version, text)
	return pos
}

// complete sets the .cellens to text and returns the completion items at
// its marker, none for null. While the server says the list is incomplete,
// as it lists the Go types, it asks again, as an editor does as the person
// types.
func (g *guided) complete(text string) []completionItem {
	g.t.Helper()
	pos := g.set(text)
	for {
		res, rerr := g.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": g.lens}, "position": pos})
		if rerr != nil {
			g.t.Fatalf("completion error = %+v", rerr)
		}
		var list struct {
			IsIncomplete bool
			Items        []completionItem
		}
		if strings.HasPrefix(string(res), "{") {
			if err := json.Unmarshal(res, &list); err != nil {
				g.t.Fatalf("completion result %s: %v", res, err)
			}
		} else if err := json.Unmarshal(res, &list.Items); err != nil {
			g.t.Fatalf("completion result %s: %v", res, err)
		}
		if !list.IsIncomplete {
			return list.Items
		}
	}
}

func labels(items []completionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

func item(items []completionItem, label string) (completionItem, bool) {
	i := slices.IndexFunc(items, func(it completionItem) bool { return it.Label == label })
	if i < 0 {
		return completionItem{}, false
	}
	return items[i], true
}

const (
	crdEntry    = "types:\n  - file: crd.yaml\n    document: CustomResourceDefinition widgets.example.com\n"
	policyEntry = "types:\n  - file: policy.yaml\n    document: ValidatingAdmissionPolicy limits\n"
)

// TestLensCompletion is the first acceptance criterion of #16: completion
// in a .cellens offers an entry's keys, the YAML files below its folder, the
// documents of that file, what the document has, dyn values first, the CEL
// type names, the kinds, the Go packages and their structs, and the keywords
// of a schema valid where they go, in block and flow mappings, in quotes,
// and in a .cellens YAML cannot read.
func TestLensCompletion(t *testing.T) {
	t.Parallel()
	g := newGuided(t, map[string]any{"textDocument": map[string]any{"completion": map[string]any{"completionItem": map[string]any{"snippetSupport": true}}}})
	// The CRDs of crd.yaml are known once it is open, whatever the scan.
	g.openURI(fileURI(filepath.Join(g.dir, "crd.yaml")), lensCRD+"---\n"+otherCRD)
	tests := []struct {
		name, text string
		// first are the first labels, in order; has are labels among the
		// others; not are labels that must not be there; details are the
		// details of some labels.
		first, has, not []string
		details         map[string]string
	}{
		// A CRD's dyn values take an int or a string: no kind or Go type (the review of #16).
		{name: "the keys of a CRD's entry", text: crdEntry + "    ‸", first: []string{"version", "path", "type", "schema"}, not: []string{"file", "document", "variable", "kind", "go"}},
		{name: "the keys of a policy's entry", text: policyEntry + "    variable: params\n    ‸", first: []string{"path", "type", "kind", "schema", "go"}},
		{name: "the keys of a typed policy entry", text: policyEntry + "    variable: params\n    kind: v1 ConfigMap\n    ‸", first: []string{"path"}, not: []string{"version", "type", "schema"}},
		{name: "the root key", text: "# head\n‸", first: []string{"types"}},
		{name: "the YAML files below the folder", text: "types:\n  - file: ‸", has: []string{"crd.yaml", "policy.yaml", "deploy/quota.yaml", "sub/x.yml"}, not: []string{".git/x.yml", "node_modules/x.yml", ".cellens"}},
		{
			name: "the documents of the file, dyn values first", text: "types:\n  - file: crd.yaml\n    document: ‸", first: []string{"CustomResourceDefinition widgets.example.com", "CustomResourceDefinition gadgets.example.com"},
			details: map[string]string{"CustomResourceDefinition widgets.example.com": "1 dyn value", "CustomResourceDefinition gadgets.example.com": "no dyn value"},
		},
		{name: "the versions of a CRD", text: crdEntry + "    version: ‸", first: []string{"v1"}, details: map[string]string{"v1": "1 dyn value"}},
		{
			name: "the variables of a policy, dyn first", text: policyEntry + "    variable: ‸", first: []string{"params", "variables.max", "object", "oldObject"},
			details: map[string]string{"params": "dyn", "variables.max": "dyn", "object": "apps/v1 Deployment"},
		},
		{
			name: "the dyn values of a CRD, then the way to them", text: crdEntry + "    version: v1\n    path: ‸", first: []string{"spec.config", "spec"}, not: []string{"spec.open"},
			details: map[string]string{"spec.config": "dyn", "spec": "object · 1 dyn value"},
		},
		// Only dyn values, and the way to them (the review of #16).
		{name: "no typed value below the path written", text: crdEntry + "    version: v1\n    path: spec.na‸", first: []string{"spec.config"}, not: []string{"spec.name", "spec.open"}},
		{name: "no typed value of a kind a policy matches", text: policyEntry + "    variable: object\n    path: spec.‸", first: []string{}},
		{name: "the CEL types at a field's type", text: policyEntry + "    variable: params\n    type: \"{max: ‸}\"", first: []string{"string", "int", "uint"}, has: []string{"list(T)", "map(string, T)", "{field: T}"}},
		{name: "nothing invented at a field's name", text: policyEntry + "    variable: params\n    type: \"{‸}\"", first: []string{}},
		{name: "string at a map's key", text: policyEntry + "    variable: params\n    type: \"map(‸\"", first: []string{"string"}, not: []string{"int", "list(T)"}},
		{name: "int and string for a CRD's value", text: crdEntry + "    version: v1\n    path: spec.config\n    type: ‸", first: []string{"string", "int"}, not: []string{"uint", "double", "list(T)", "map(string, T)", "{field: T}"}},
		{name: "no kind for a CRD's value", text: crdEntry + "    version: v1\n    path: spec.config\n    kind: ‸", first: []string{}},
		{name: "no Go type for a CRD's value", text: crdEntry + "    version: v1\n    path: spec.config\n    go: ‸", first: []string{}},
		{name: "the keywords of a CRD's schema", text: crdEntry + "    version: v1\n    path: spec.config\n    schema:\n      ‸", first: []string{"type"}, not: []string{"x-kubernetes-int-or-string", "x-kubernetes-preserve-unknown-fields"}},
		{name: "the types of a CRD's schema", text: crdEntry + "    version: v1\n    path: spec.config\n    schema:\n      type: ‸", first: []string{"string", "integer"}, not: []string{"object", "array", "number", "boolean"}},
		{name: "no format of a CRD's string", text: crdEntry + "    version: v1\n    path: spec.config\n    schema:\n      type: string\n      ‸", has: []string{"maxLength"}, not: []string{"format"}},
		{name: "the kinds of the workspace's CRDs first", text: policyEntry + "    variable: params\n    kind: ‸", first: []string{"example.com/v1 Gadget", "example.com/v1 Widget"}},
		{
			name: "a kind", text: policyEntry + "    variable: params\n    kind: apps/v1 Dep‸", has: []string{"apps/v1 Deployment", "v1 ConfigMap"},
			details: map[string]string{"apps/v1 Deployment": "Deployment enables declarative updates for Pods and ReplicaSets."},
		},
		{name: "the Go packages", text: policyEntry + "    variable: params\n    go: ‸", first: []string{"./api/v1"}, not: []string{"./api/v1/testdata", "./vendor/x"}, details: map[string]string{"./api/v1": "QuotaSpec"}},
		{name: "the struct types of a Go package", text: policyEntry + "    variable: params\n    go: ./api/v1#‸", first: []string{"QuotaSpec"}, not: []string{"TestSpec"}, details: map[string]string{"QuotaSpec": "QuotaSpec is the quota of a team."}},
		{name: "the keywords of a schema without type", text: policyEntry + "    variable: params\n    schema:\n      ‸", first: []string{"type", "x-kubernetes-int-or-string", "x-kubernetes-preserve-unknown-fields"}, not: []string{"properties", "x-kubernetes-validations"}},
		{
			name: "the keywords of an object", text: policyEntry + "    variable: params\n    schema:\n      type: object\n      ‸", has: []string{"properties", "additionalProperties", "required"}, not: []string{"type", "items", "maxLength"},
			details: map[string]string{"properties": "The fields of the object, each with its schema."},
		},
		{name: "the types of a schema", text: policyEntry + "    variable: params\n    schema:\n      type: ‸", first: []string{"object", "array", "string", "integer", "number", "boolean"}},
		{name: "the keywords of a property in brackets", text: policyEntry + "    variable: params\n    schema:\n      type: object\n      properties:\n        max: {type: integer, ‸}", has: []string{"minimum", "maximum", "format"}, not: []string{"type", "items"}},
		{name: "nothing at a property's name", text: policyEntry + "    variable: params\n    schema:\n      type: object\n      properties:\n        ‸", first: []string{}},
		{name: "a flow entry", text: "types:\n  - {file: policy.yaml, document: ValidatingAdmissionPolicy limits, variable: ‸}", first: []string{"params", "variables.max"}},
		{name: "beside an entry YAML cannot read", text: "types:\n  - file: crd.yaml\n    pa\n" + policyEntry[len("types:\n"):] + "    variable: ‸", first: []string{"params", "variables.max"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g.use(t)
			items := g.complete(tt.text)
			got := labels(items)
			if len(got) < len(tt.first) || !slices.Equal(got[:len(tt.first)], tt.first) || tt.first != nil && len(tt.first) == 0 && len(got) > 0 {
				t.Errorf("completion = %q, want it to start with %q", got, tt.first)
			}
			for _, h := range tt.has {
				if !slices.Contains(got, h) {
					t.Errorf("completion = %q, want %q", got, h)
				}
			}
			for _, n := range tt.not {
				if slices.Contains(got, n) {
					t.Errorf("completion = %q, want no %q", got, n)
				}
			}
			for label, want := range tt.details {
				if it, ok := item(items, label); !ok || it.Detail != want {
					t.Errorf("completion item %q detail = %q, want %q", label, it.Detail, want)
				}
			}
		})
	}
}

// TestLensCompletionEdits checks the text and the range of the items: the
// range of the value, or of the type name in it, in UTF-16 units, quoting
// and escaping where YAML needs it, the line ending of the text, and
// snippets and text kept as is only for a client that takes them (#16).
func TestLensCompletionEdits(t *testing.T) {
	t.Parallel()
	full := newGuided(t, map[string]any{"textDocument": map[string]any{"completion": map[string]any{"completionItem": map[string]any{
		"snippetSupport": true, "insertTextModeSupport": map[string]any{"valueSet": []int{1, 2}},
	}}}})
	plain := newGuided(t, map[string]any{})
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	tests := []struct {
		name, text, label string
		// want is the text once the item is applied.
		want string
		// snippet and asIs say whether the item is a snippet, and keeps its
		// indentation.
		snippet, asIs bool
		// plain is true for a client that takes neither.
		plain bool
	}{
		{"a key", crdEntry + "    vers‸", "version", crdEntry + "    version: ", false, false, false},
		{"a value right after the colon", crdEntry + "    version:‸", "v1", crdEntry + "    version: v1", false, false, false},
		{"a value written over", crdEntry + "    version: v1\n    path: spec.co‸nf", "spec.config", crdEntry + "    version: v1\n    path: spec.config", false, false, false},
		{"a type name after a wide character", policyEntry + "    variable: params\n    type: \"{𝒳: in‸}\"", "int", policyEntry + "    variable: params\n    type: \"{𝒳: int}\"", false, false, false},
		{"a list type", policyEntry + "    variable: params\n    type: ‸", "list(T)", policyEntry + "    variable: params\n    type: list($1)", true, false, false},
		{"a struct in an empty value comes quoted", policyEntry + "    variable: params\n    type: ‸", "{field: T}", policyEntry + "    variable: params\n    type: \"{$1}\"", true, false, false},
		{
			"a value in brackets is quoted", "types:\n  - {file: crd.yaml, document: CustomResourceDefinition widgets.example.com, version: v1, path: spec.‸}", "spec.config",
			"types:\n  - {file: crd.yaml, document: CustomResourceDefinition widgets.example.com, version: v1, path: spec.config}", false, false, false,
		},
		{"a schema below its key", policyEntry + "    variable: params\n    sch‸", "schema", policyEntry + "    variable: params\n    schema:\n      ", false, true, false},
		// The review of #16: the line ending of the text, and what a client takes.
		{"a schema below its key with CR LF", crlf(policyEntry + "    variable: params\n    sch‸"), "schema", crlf(policyEntry + "    variable: params\n    schema:\n      "), false, true, false},
		{"properties below its key with CR LF", crlf(policyEntry + "    variable: params\n    schema:\n      type: object\n      prop‸"), "properties", crlf(policyEntry + "    variable: params\n    schema:\n      type: object\n      properties:\n        "), false, true, false},
		{"a schema without text as is", policyEntry + "    variable: params\n    sch‸", "schema", policyEntry + "    variable: params\n    schema:", false, false, true},
		{"a list type without snippets", policyEntry + "    variable: params\n    type: ‸", "list(T)", policyEntry + "    variable: params\n    type: list()", false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := full
			if tt.plain {
				g = plain
			}
			g.use(t)
			items := g.complete(tt.text)
			it, ok := item(items, tt.label)
			if !ok {
				t.Fatalf("completion = %q, want %q", labels(items), tt.label)
			}
			text, _ := at(t, tt.text)
			e := it.TextEdit
			got := apply(t, text, []lspEdit{{Range: e.Range, NewText: e.NewText}})
			if got != tt.want || (it.InsertTextFormat == 2) != tt.snippet || (it.InsertTextMode == 1) != tt.asIs {
				t.Errorf("item %q makes\n%q (snippet %t, as is %t), want\n%q (snippet %t, as is %t)", tt.label, got, it.InsertTextFormat == 2, it.InsertTextMode == 1, tt.want, tt.snippet, tt.asIs)
			}
		})
	}
}

// TestCompletionTriggers checks that the characters that ask for completion
// in a .cellens ask for nothing in a CEL expression, but the dot.
func TestCompletionTriggers(t *testing.T) {
	t.Parallel()
	c := start(t)
	res, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil})
	if rerr != nil {
		t.Fatal(rerr)
	}
	var r struct {
		Capabilities struct {
			CompletionProvider struct{ TriggerCharacters []string }
		}
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	if got := r.Capabilities.CompletionProvider.TriggerCharacters; !slices.Equal(got, []string{".", "/", "#", "(", ":"}) {
		t.Errorf("triggerCharacters = %q, want . / # ( :", got)
	}
	c.notify("initialized", map[string]any{})
	text := readFile(t, "widget.yaml")
	c.open(text)
	c.diagnostics()
	line, char := lineOf(t, text, "self.replcas")
	for _, tc := range []struct {
		trigger string
		null    bool
	}{{"/", true}, {".", false}} {
		res, _ := c.call("textDocument/completion", map[string]any{
			"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char + len("self.")},
			"context": map[string]any{"triggerKind": 2, "triggerCharacter": tc.trigger},
		})
		if (string(res) == "null") != tc.null {
			t.Errorf("completion after %q in CEL = %s, want null %t", tc.trigger, res, tc.null)
		}
	}
}

// hover returns the Markdown of the hover at the marker of text in the
// .cellens, "" for none.
func (g *guided) hover(text string) string {
	g.t.Helper()
	pos := g.set(text)
	res, rerr := g.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": g.lens}, "position": pos})
	if rerr != nil {
		g.t.Fatalf("hover error = %+v", rerr)
	}
	var h struct{ Contents struct{ Value string } }
	if err := json.Unmarshal(res, &h); err != nil {
		g.t.Fatalf("hover result %s: %v", res, err)
	}
	return h.Contents.Value
}

// TestLensHover is the second acceptance criterion of #16: hover on an
// entry's file, document or path says which value it names and whether it is
// dyn there, and on a kind, go or schema the type it gives, in the short
// form of the line above a rule.
func TestLensHover(t *testing.T) {
	t.Parallel()
	g := newGuided(t, map[string]any{})
	config := crdEntry + "    version: v1\n    path: spec.config\n    type: int\n"
	params := policyEntry + "    variable: params\n"
	tests := []struct {
		name, text string
		words      []string
	}{
		{"on a path", strings.Replace(config, "spec.config", "spec.co‸nfig", 1), []string{"```cel\n(value) self.spec.config: dyn\n```", "CustomResourceDefinition widgets.example.com, version v1, in crd.yaml.", "x-kubernetes-int-or-string", "This entry gives it `int`."}},
		{"on a file", strings.Replace(config, "file:", "fi‸le:", 1), []string{"(value) self.spec.config: dyn"}},
		{"on a document", strings.Replace(config, "widgets", "wid‸gets", 1), []string{"(value) self.spec.config: dyn"}},
		{"on a typed value", strings.Replace(config, "spec.config", "spec.na‸me", 1), []string{"(value) self.spec.name: string", "Kubernetes types it: an entry for it does not apply."}},
		{"on params no kind types", "‸" + params + "    type: int\n", nil},
		{"on the variable params", strings.Replace(params, "params", "par‸ams", 1) + "    type: int\n", []string{"(value) params: dyn", "spec.paramKind"}},
		{"on a kind", params + "    kind: v1 Config‸Map\n", []string{"```cel\n(kind) params: v1 ConfigMap\n```", "ConfigMap holds configuration data for pods to consume."}},
		{"on a kind a CRD's value cannot have", crdEntry + "    version: v1\n    path: spec.config\n    kind: v1 Config‸Map\n", []string{"```cel\n(kind) self.spec.config\n```", "The entry does not apply: spec.config is x-kubernetes-int-or-string, an int or a string to a rule: give it int or string."}},
		{"on a kind not found", params + "    kind: example.com/v1 No‸such\n", []string{"(kind) params\n", "no CustomResourceDefinition of the workspace defines it"}},
		{"on a schema", params + "    sche‸ma:\n      type: object\n      properties:\n        maxReplicas: {type: integer}\n        team: {type: string}\n", []string{"```cel\n(schema) params: {maxReplicas, team}\n```", "`{maxReplicas: int, team: string}`"}},
		{"on a Go type", params + "    go: ./api/v1#Quota‸Spec\n", []string{"```cel\n(go) params: {maxReplicas, since, team}\n```", "The Go type QuotaSpec in api/v1/quota_types.go:10. QuotaSpec is the quota of a team.", "other.Owner"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g.use(t)
			got := g.hover(tt.text)
			if tt.words == nil && got != "" {
				t.Errorf("hover = %q, want none", got)
			}
			for _, w := range tt.words {
				if !strings.Contains(got, w) {
					t.Errorf("hover = %q, want it to say %q", got, w)
				}
			}
		})
	}
}

// TestLensTyping is the third acceptance criterion of #16: the problems of
// an entry show as it is typed, with didChange, and so do the lines above the
// rules of the file it names, while the other entries keep their types even
// when YAML cannot read the entry being typed. The client is asked for the
// lines again when they changed, not for a keystroke that changes no entry.
func TestLensTyping(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	lens := fileURI(filepath.Join(dir, ".cellens"))
	writeFile(t, filepath.Join(dir, "crd.yaml"), inlineCRD)
	c := start(t)
	initializeIn(t, c, dir, refreshCaps)
	c.openURI(crd, inlineCRD)
	c.diagnosticsOf(crd, func([]diagnostic) bool { return true })
	entry := "types:\n  - file: crd.yaml\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    path: spec.config\n    type: int\n"
	c.openURI(lens, entry)
	lensLine := func(what string, refresh bool) string {
		t.Helper()
		res, refreshed := c.codeLensSeeingRefresh(crd)
		if refreshed != refresh {
			t.Errorf("after %s: workspace/codeLens/refresh %t, want %t", what, refreshed, refresh)
		}
		return string(res)
	}
	if got := lensLine("the .cellens opened", true); !strings.Contains(got, "self.spec.config: int · Edit") {
		t.Fatalf("code lenses = %s, want self.spec.config: int", got)
	}
	steps := []struct {
		name, text string
		// sent is true when the diagnostics of the .cellens change, to
		// lensDiag, the start of the one there is, if any.
		sent     bool
		lensDiag string
		// line is in the code lenses of crd.yaml after the step, and
		// refresh says whether the client is asked for them again.
		line    string
		refresh bool
	}{
		{"a second entry YAML cannot read", entry + "  - file: crd.yaml\n    pa\n", true, "not YAML: line 8: could not find expected ':'", "self.spec.config: int · Edit", false},
		{"a type that does not parse", strings.Replace(entry, "type: int", "type: strin", 1), true, `type: unknown type "strin"`, "self.spec.config: dyn · Give it a type", true},
		{"the type typed", strings.Replace(entry, "type: int", "type: string", 1), true, "", "self.spec.config: string · Edit", true},
		{"a comment", strings.Replace(entry, "type: int", "type: string # a port", 1), false, "", "self.spec.config: string · Edit", false},
	}
	for i, st := range steps {
		c.changeURI(lens, i+2, st.text)
		if st.sent {
			diags := c.diagnosticsOf(lens, func([]diagnostic) bool { return true })
			switch {
			case st.lensDiag == "" && len(diags) != 0, st.lensDiag != "" && (len(diags) != 1 || !strings.HasPrefix(diags[0].Message, st.lensDiag)):
				t.Errorf("%s: diagnostics of .cellens = %+v, want %q", st.name, diags, st.lensDiag)
			}
		}
		if got := lensLine(st.name, st.refresh); !strings.Contains(got, st.line) {
			t.Errorf("%s: code lenses = %s, want %q", st.name, got, st.line)
		}
	}
}

// snippetArgs are the arguments for a snippet of kind for the value of
// configArgs, or for the params of limitPolicy in policy.yaml.
func snippetArgs(lens, kind string, policy bool) map[string]any {
	if policy {
		return map[string]any{"cellens": fileURI(lens), "file": "policy.yaml", "document": "ValidatingAdmissionPolicy replica-limit", "variable": "params", "snippet": kind}
	}
	return with(configArgs(lens, "crd.yaml"), "snippet", kind)
}

// TestSetTypeSnippet is the fourth acceptance criterion of #16: setType with
// a snippet writes a new entry with a type to fill in, an empty struct or an
// object schema with empty properties, or for a CRD's int-or-string value
// int, and answers where the cursor goes; for a value with an entry it
// changes nothing and answers where its type is.
func TestSetTypeSnippet(t *testing.T) {
	t.Parallel()
	const start0 = "# types\ntypes:\n"
	tests := []struct {
		name, kind string
		policy     bool
		// want is the .cellens after the edit, with the selection between
		// ⟨ and ⟩.
		want string
	}{
		{"a struct type", "type", true, start0 + "  - file: policy.yaml\n    document: ValidatingAdmissionPolicy replica-limit\n    variable: params\n    type: \"{⟨⟩}\"\n"},
		{"an object schema", "schema", true, start0 + "  - file: policy.yaml\n    document: ValidatingAdmissionPolicy replica-limit\n    variable: params\n    schema:\n      type: object\n      properties: {⟨⟩}\n"},
		{"int for an int-or-string value", "type", false, start0 + "  - file: crd.yaml\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    path: spec.config\n    type: ⟨int⟩\n"},
		{"integer for an int-or-string value", "schema", false, start0 + "  - file: crd.yaml\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    path: spec.config\n    schema:\n      type: ⟨integer⟩\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
			writeFile(t, filepath.Join(dir, "policy.yaml"), limitPolicy)
			lens := filepath.Join(dir, ".cellens")
			writeFile(t, lens, start0)
			c := start(t)
			initializeIn(t, c, dir, editing("create"))
			edit, res, rerr := c.command("cel-lens.setType", snippetArgs(lens, tt.kind, tt.policy), applied)
			if rerr != nil || edit == nil {
				t.Fatalf("setType = %s, %+v, want an edit", res, rerr)
			}
			var r struct {
				Cellens, Change string
				Selection       struct{ Start, End struct{ Line, Character int } }
			}
			if err := json.Unmarshal(res, &r); err != nil {
				t.Fatal(err)
			}
			got := apply(t, start0, edit.Edit.DocumentChanges[0].Edits)
			sel := apply(t, got, []lspEdit{{Range: r.Selection, NewText: "⟨" + selected(t, got, r.Selection) + "⟩"}})
			if sel != tt.want || r.Cellens != fileURI(lens) || r.Change != "added" {
				t.Errorf("setType makes\n%s(%s %s), want\n%s", sel, r.Cellens, r.Change, tt.want)
			}
		})
	}
	// The review of #16: a value with an entry needs no edit, so neither
	// does it need a client that applies one.
	for _, caps := range []map[string]any{editing("create"), {}} {
		t.Run(fmt.Sprintf("a value with an entry, caps %v", caps), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
			lens := filepath.Join(dir, ".cellens")
			writeFile(t, lens, lensText)
			c := start(t)
			initializeIn(t, c, dir, caps)
			edit, res, rerr := c.command("cel-lens.setType", snippetArgs(lens, "type", false), applied)
			want := `{"cellens":"` + fileURI(lens) + `","change":"unchanged","selection":{"start":{"line":6,"character":10},"end":{"line":6,"character":10}}}`
			if rerr != nil || edit != nil || string(res) != want {
				t.Errorf("setType = %v, %s, %+v, want no edit and %s", edit, res, rerr, want)
			}
		})
	}
	t.Run("a .cellens that does not exist yet", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "policy.yaml"), limitPolicy)
		lens := filepath.Join(dir, ".cellens")
		c := start(t)
		initializeIn(t, c, dir, editing("create"))
		edit, res, rerr := c.command("cel-lens.setType", snippetArgs(lens, "type", true), applied)
		if rerr != nil || edit == nil || len(edit.Edit.DocumentChanges) != 2 || edit.Edit.DocumentChanges[0].Kind != "create" {
			t.Fatalf("setType = %+v, %s, %+v, want the file created, then an edit", edit, res, rerr)
		}
		var r struct {
			Change    string
			Selection struct{ Start, End struct{ Line, Character int } }
		}
		if err := json.Unmarshal(res, &r); err != nil {
			t.Fatal(err)
		}
		got := apply(t, "", edit.Edit.DocumentChanges[1].Edits)
		sel := apply(t, got, []lspEdit{{Range: r.Selection, NewText: "⟨" + selected(t, got, r.Selection) + "⟩"}})
		want := cellens.Header + "\ntypes:\n  - file: policy.yaml\n    document: ValidatingAdmissionPolicy replica-limit\n    variable: params\n    type: \"{⟨⟩}\"\n"
		if sel != want || r.Change != "created" {
			t.Errorf("setType makes\n%s(%s), want\n%s(created)", sel, r.Change, want)
		}
	})
	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		lens := filepath.Join(dir, ".cellens")
		c := start(t)
		initializeIn(t, c, dir, editing("create"))
		for _, args := range []map[string]any{
			with(snippetArgs(lens, "type", false), "type", "int"),
			snippetArgs(lens, "struct", false),
		} {
			if _, _, rerr := c.command("cel-lens.setType", args, applied); rerr == nil || rerr.Code != jsonrpc.CodeInvalidParams {
				t.Errorf("setType(%v) error = %+v, want InvalidParams", args, rerr)
			}
		}
	})
}

// selected returns the text of text in the range r, on one line.
func selected(t *testing.T, text string, r struct{ Start, End struct{ Line, Character int } }) string {
	t.Helper()
	line := []rune(strings.Split(text, "\n")[r.Start.Line])
	return string(line[r.Start.Character:r.End.Character])
}
