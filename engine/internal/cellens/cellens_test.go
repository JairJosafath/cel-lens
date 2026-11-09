package cellens_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
)

// The example of docs/cellens.md.
const example = `# .cellens: types cel-lens assumes where Kubernetes gives dyn. Commit it.
types:
  - file: config/crd/bases/widgets.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.maxSurge
    type: int
  - file: policies/limits.yaml
    document: ValidatingAdmissionPolicy limits
    variable: params
    type: "{name: string, replicas: int, labels: map(string, string)}"
  - file: policies/limits.yaml
    document: ValidatingAdmissionPolicy limits
    variable: object
    kind: example.com/v1 Widget
`

func TestParseExample(t *testing.T) {
	t.Parallel()
	entries, problems := cellens.Parse([]byte(example))
	if len(problems) > 0 {
		t.Fatalf("Parse(example) problems = %v, want none", problems)
	}
	if len(entries) != 3 {
		t.Fatalf("Parse(example) = %d entries, want 3", len(entries))
	}
	crd, params, object := entries[0], entries[1], entries[2]
	checks := []struct {
		name      string
		got, want any
	}{
		{"crd line", crd.Line, 3},
		{"crd file", crd.File, "config/crd/bases/widgets.yaml"},
		{"crd document", crd.Document, cellens.Document{Kind: "CustomResourceDefinition", Name: "widgets.example.com"}},
		{"crd version", crd.Version, "v1"},
		{"crd variable", crd.Variable, ""},
		{"crd path", crd.Path.String(), "spec.maxSurge"},
		{"crd type", crd.Type.String(), "int"},
		{"crd kind", crd.Kind, (*cellens.KindRef)(nil)},
		{"params line", params.Line, 8},
		{"params file", params.File, "policies/limits.yaml"},
		{"params document", params.Document, cellens.Document{Kind: "ValidatingAdmissionPolicy", Name: "limits"}},
		{"params version", params.Version, ""},
		{"params variable", params.Variable, "params"},
		{"params path", params.Path.String(), ""},
		{"params type", params.Type.String(), "{name: string, replicas: int, labels: map(string, string)}"},
		{"params kind", params.Kind, (*cellens.KindRef)(nil)},
		{"object line", object.Line, 12},
		{"object variable", object.Variable, "object"},
		{"object type", object.Type, (*cellens.Type)(nil)},
		{"object kind", *object.Kind, cellens.KindRef{APIVersion: "example.com/v1", Kind: "Widget"}},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("Parse(example) %s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestParseFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, entry string
		check       func(cellens.Entry) (got, want any)
	}{
		{
			"file is cleaned", "file: ./a/../b/c.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: object\n    type: string",
			func(e cellens.Entry) (any, any) { return e.File, "b/c.yaml" },
		},
		{
			"no path is the root", "file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: object\n    type: string",
			func(e cellens.Entry) (any, any) { return len(e.Path), 0 },
		},
		{
			"a variables entry", "file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: variables.limit\n    type: int",
			func(e cellens.Entry) (any, any) { return e.Variable, "variables.limit" },
		},
		{
			"oldObject", "file: a.yaml\n    document: MutatingAdmissionPolicy p\n    variable: oldObject\n    type: string",
			func(e cellens.Entry) (any, any) { return e.Variable, "oldObject" },
		},
		{
			"a built-in kind", "file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    kind: v1 ConfigMap",
			func(e cellens.Entry) (any, any) { return *e.Kind, cellens.KindRef{APIVersion: "v1", Kind: "ConfigMap"} },
		},
		{
			"a beta version", "file: a.yaml\n    document: CustomResourceDefinition w.example.com\n    version: v1beta1\n    type: string",
			func(e cellens.Entry) (any, any) { return e.Version, "v1beta1" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entries, problems := cellens.Parse([]byte("types:\n  - " + tt.entry + "\n"))
			if len(problems) > 0 || len(entries) != 1 {
				t.Fatalf("Parse = %d entries, problems %v, want 1 entry and none", len(entries), problems)
			}
			if got, want := tt.check(entries[0]); !reflect.DeepEqual(got, want) {
				t.Errorf("Parse = %v, want %v", got, want)
			}
		})
	}
}

func TestParseProblems(t *testing.T) {
	t.Parallel()
	const policy = "document: ValidatingAdmissionPolicy p\n    variable: params\n"
	tests := []struct {
		name, file string
		// line and column of the one problem, and words its message has.
		line, column int
		words        []string
	}{
		{"unknown key", "types:\n  - file: a.yaml\n    " + policy + "    type: string\n    colour: red\n", 6, 5, []string{`unknown key "colour"`}},
		{"unknown top-level key", "kinds: []\n", 1, 1, []string{`unknown key "kinds"`, "only types"}},
		{"no file", "types:\n  - " + policy + "    type: string\n", 2, 5, []string{"no file"}},
		{"file outside the folder", "types:\n  - file: ../x.yaml\n    " + policy + "    type: string\n", 2, 11, []string{"not a file in this .cellens's folder"}},
		{"absolute file", "types:\n  - file: /x.yaml\n    " + policy + "    type: string\n", 2, 11, []string{"not a file in this .cellens's folder"}},
		{"no document", "types:\n  - file: a.yaml\n    variable: params\n    type: string\n", 2, 5, []string{"no document", "metadata.name"}},
		{"document without a name", "types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy\n    variable: params\n    type: string\n", 3, 15, []string{"kind and metadata.name"}},
		{"both type and kind", "types:\n  - file: a.yaml\n    " + policy + "    type: string\n    kind: v1 ConfigMap\n", 6, 11, []string{"both type and kind"}},
		{"no type", "types:\n  - file: a.yaml\n    " + policy, 2, 5, []string{"the entry has no type", "schema", "go"}},
		{"version on a policy", "types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    version: v1\n    variable: params\n    type: string\n", 4, 14, []string{"version is for CustomResourceDefinitions"}},
		{"variable on a CRD", "types:\n  - file: a.yaml\n    document: CustomResourceDefinition w.example.com\n    version: v1\n    variable: params\n    type: string\n", 5, 15, []string{"variable is for policies"}},
		{"a CRD without version", "types:\n  - file: a.yaml\n    document: CustomResourceDefinition w.example.com\n    type: string\n", 2, 5, []string{"needs version"}},
		{"a policy without variable", "types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    type: string\n", 2, 5, []string{"needs variable"}},
		{"an unknown variable", "types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: request\n    type: string\n", 4, 15, []string{`variable "request"`}},
		{"a bad path", "types:\n  - file: a.yaml\n    " + policy + "    path: spec..x\n    type: string\n", 5, 11, []string{`path "spec..x"`}},
		{"a bad kind", "types:\n  - file: a.yaml\n    " + policy + "    kind: Limit\n", 5, 11, []string{`kind "Limit"`, "apps/v1 Deployment"}},
		{"a type that doesn't parse, quoted", "types:\n  - file: a.yaml\n    " + policy + "    type: \"{name: strin}\"\n", 5, 19, []string{"type:", `unknown type "strin"`}},
		{"a type that doesn't parse, plain", "types:\n  - file: a.yaml\n    " + policy + "    type: list(int\n", 5, 19, []string{"type:", `")" is missing`}},
		{"a second entry for one value", "types:\n  - file: a.yaml\n    " + policy + "    type: string\n  - file: a.yaml\n    " + policy + "    type: int\n", 6, 5, []string{"second entry", "line 2"}},
		{"not YAML", "types: [unclosed\n", 1, 1, []string{"not YAML"}},
		{"not a mapping", "- a\n- b\n", 1, 1, []string{"mapping"}},
		{"types not a list", "types: yes\n", 1, 8, []string{"list of entries"}},
		{"an entry not a mapping", "types:\n  - a.yaml\n", 2, 5, []string{"mapping"}},
		{"a field not a single value", "types:\n  - file: [a.yaml]\n    " + policy + "    type: string\n", 2, 11, []string{"single value"}},
		{"a second document", "types: []\n---\ntypes: []\n", 2, 1, []string{"second YAML document"}},
		// The review of #5: duplicate keys, unquoted structs, multi-line types,
		// Windows paths, CRD versions, document kinds, a broken second document.
		{"types twice", "types: []\ntypes: []\n", 2, 1, []string{`key "types" appears twice (first at line 1)`}},
		{"a key twice in an entry", "types:\n  - file: a.yaml\n    file: b.yaml\n    " + policy + "    type: string\n", 3, 5, []string{`key "file" appears twice (first at line 2)`}},
		{"a bad type hidden by a second type", "types:\n  - file: a.yaml\n    " + policy + "    type: strin\n    type: int\n", 6, 5, []string{`key "type" appears twice`}},
		{"an unquoted struct type", "types:\n  - file: a.yaml\n    " + policy + "    type: {name: string}\n", 5, 11, []string{"quote the type", `type: "{name: string}"`}},
		{"a literal block type", "types:\n  - file: a.yaml\n    " + policy + "    type: |\n      {name: string,\n       replicas: strin}\n", 5, 11, []string{"character 27", `unknown type "strin"`}},
		{"a plain type over two lines", "types:\n  - file: a.yaml\n    " + policy + "    type: list(\n      strin)\n", 5, 11, []string{"character 7", `unknown type "strin"`}},
		{"an escaped quoted type", "types:\n  - file: a.yaml\n    " + policy + "    type: \"\\u007bname: strin}\"\n", 5, 11, []string{"character 8", `unknown type "strin"`}},
		{"a Windows path out", "types:\n  - file: ..\\..\\secret.yaml\n    " + policy + "    type: string\n", 2, 11, []string{"not a file in this .cellens's folder"}},
		{"a drive letter", "types:\n  - file: C:/x.yaml\n    " + policy + "    type: string\n", 2, 11, []string{"not a file in this .cellens's folder"}},
		{"the folder itself", "types:\n  - file: .\n    " + policy + "    type: string\n", 2, 11, []string{"not a file in this .cellens's folder"}},
		{"a CRD version that isn't one", "types:\n  - file: a.yaml\n    document: CustomResourceDefinition w.example.com\n    version: not-a-version\n    type: string\n", 4, 14, []string{`version "not-a-version"`}},
		{"a kind without CEL", "types:\n  - file: a.yaml\n    document: Deployment web\n    variable: object\n    type: string\n", 3, 15, []string{`document "Deployment web"`, "ValidatingAdmissionPolicy"}},
		{"a broken second document", "types: []\n---\n[unclosed\n", 2, 1, []string{"not YAML"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, problems := cellens.Parse([]byte(tt.file))
			if len(problems) != 1 {
				t.Fatalf("Parse(%q) problems = %v, want one", tt.file, problems)
			}
			p := problems[0]
			if p.Line != tt.line || p.Column != tt.column {
				t.Errorf("Parse(%q) problem at %d:%d, want %d:%d (%s)", tt.file, p.Line, p.Column, tt.line, tt.column, p.Message)
			}
			for _, w := range tt.words {
				if !strings.Contains(p.Message, w) {
					t.Errorf("Parse(%q) problem = %q, want it to say %q", tt.file, p.Message, w)
				}
			}
		})
	}
}

func TestParseNothing(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"", "# only a comment\n# and another\n", "types:\n", "types: []\n"} {
		entries, problems := cellens.Parse([]byte(file))
		if len(entries) > 0 || len(problems) > 0 {
			t.Errorf("Parse(%q) = %v, %v, want no entries and no problems", file, entries, problems)
		}
	}
}

func TestParseKeepsValidEntries(t *testing.T) {
	t.Parallel()
	file := "types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: nope\n" +
		"  - file: b.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: string\n"
	entries, problems := cellens.Parse([]byte(file))
	if len(entries) != 1 || entries[0].File != "b.yaml" || len(problems) != 1 {
		t.Errorf("Parse = %v, %v, want the entry of b.yaml and one problem", entries, problems)
	}
}

func TestParseProblemsInFileOrder(t *testing.T) {
	t.Parallel()
	// The second document's problem comes after the first document's.
	file := "types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: strin\n---\ntypes: []\n"
	_, problems := cellens.Parse([]byte(file))
	if len(problems) != 2 || problems[0].Line != 5 || problems[1].Line != 6 {
		t.Errorf("Parse = %v, want the problem of line 5, then of line 6", problems)
	}
}

// issueExample is the example of #15: a schema and a Go type.
const issueExample = `types:
  - file: policy.yaml
    document: ValidatingAdmissionPolicy replica-limit
    variable: params
    schema:                      # as in a CRD's openAPIV3Schema
      type: object
      properties:
        maxReplicas: {type: integer, minimum: 0}
        team: {type: string}
  - file: other.yaml
    document: ValidatingAdmissionPolicy quota
    variable: params
    go: ./api/v1#QuotaSpec       # a Go type: folder of its package, relative to this .cellens, and its name
`

// TestParseSchemaAndGo checks that an entry gives its type as an OpenAPI
// schema with schema: or as a Go type with go: (#15).
func TestParseSchemaAndGo(t *testing.T) {
	t.Parallel()
	entries, problems := cellens.Parse([]byte(issueExample))
	if len(problems) > 0 || len(entries) != 2 {
		t.Fatalf("Parse(issueExample) = %v, %v, want 2 entries and no problems", entries, problems)
	}
	s := entries[0].Schema
	want := map[string]any{"type": "object", "properties": map[string]any{
		"maxReplicas": map[string]any{"type": "integer", "minimum": float64(0)},
		"team":        map[string]any{"type": "string"},
	}}
	if s == nil || s.Line != 5 || !reflect.DeepEqual(s.Value, want) || entries[0].Type != nil {
		t.Errorf("Parse(issueExample) schema = %+v, want %v at line 5", s, want)
	}
	if g := entries[1].Go; g == nil || *g != (cellens.GoType{Folder: "api/v1", Name: "QuotaSpec"}) || g.String() != "./api/v1#QuotaSpec" {
		t.Errorf("Parse(issueExample) go = %+v, want api/v1 QuotaSpec, written ./api/v1#QuotaSpec", g)
	}
	if kept, problems := cellens.CheckSchemas(t.Context(), entries); len(kept) != 2 || len(problems) > 0 {
		t.Errorf("CheckSchemas(issueExample) = %v, %v, want both entries and no problems", kept, problems)
	}
}

// TestParseSchemaAndGoProblems checks the problems of schema: and go: (#15):
// each at the line of the offending key, an entry with one type key only.
func TestParseSchemaAndGoProblems(t *testing.T) {
	t.Parallel()
	const head = "types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n"
	for _, tc := range []struct {
		name, file   string
		line, column int
		words        []string
	}{
		{"a schema that is no mapping", head + "    schema: object\n", 5, 13, []string{"schema is a mapping"}},
		{"an unknown keyword", head + "    schema:\n      type: object\n      properties:\n        max: {type: integer, minimun: 0}\n", 8, 30, []string{`schema: properties[max]: unknown field "minimun"`}},
		{"a value of the wrong kind", head + "    schema:\n      type: string\n      maxLength: ten\n", 7, 7, []string{"schema: maxLength: expected an integer"}},
		{"rules in a schema", head + "    schema:\n      type: object\n      x-kubernetes-validations: [{rule: 'true'}]\n", 7, 7, []string{"its rules go in the CustomResourceDefinition"}},
		{"a schema and a type", head + "    type: int\n    schema: {type: integer}\n", 6, 5, []string{"both type and schema"}},
		{"a type and go", head + "    go: ./api#Q\n    type: int\n", 6, 11, []string{"both go and type"}},
		{"go without a name", head + "    go: ./api/v1\n", 5, 9, []string{`go "./api/v1"`, "# and the type's name are missing", "./api/v1#QuotaSpec"}},
		{"go without a folder", head + "    go: '#Quota'\n", 5, 9, []string{"the folder is missing"}},
		{"go with an absolute folder", head + "    go: /api#Quota\n", 5, 9, []string{"not relative"}},
		{"go with a bad name", head + "    go: ./api#1Quota\n", 5, 9, []string{`"1Quota" is no Go type name`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entries, problems := cellens.Parse([]byte(tc.file))
			if len(problems) != 1 || len(entries) > 0 {
				t.Fatalf("Parse(%q) = %v, problems %v, want no entry and one problem", tc.file, entries, problems)
			}
			p := problems[0]
			if p.Line != tc.line || p.Column != tc.column {
				t.Errorf("Parse(%q) problem at %d:%d, want %d:%d (%s)", tc.file, p.Line, p.Column, tc.line, tc.column, p.Message)
			}
			for _, w := range tc.words {
				if !strings.Contains(p.Message, w) {
					t.Errorf("Parse(%q) problem = %q, want it to say %q", tc.file, p.Message, w)
				}
			}
		})
	}
}

// TestCheckSchemas checks that a schema the API server would refuse as a
// CRD's schema is a problem at the line of the offending key, in the API
// server's words, and its entry is left out (#15).
func TestCheckSchemas(t *testing.T) {
	t.Parallel()
	const file = `types:
  - file: a.yaml
    document: ValidatingAdmissionPolicy a
    variable: params
    schema:
      type: object
      properties:
        max:
          type: int
  - file: b.yaml
    document: ValidatingAdmissionPolicy b
    variable: params
    schema:
      type: object
      properties:
        open:
          minimum: 1
  - file: c.yaml
    document: ValidatingAdmissionPolicy c
    variable: params
    schema:
      x-kubernetes-list-type: set
  - file: d.yaml
    document: ValidatingAdmissionPolicy d
    variable: params
    schema: {type: integer, minimum: 0}
`
	entries, problems := cellens.Parse([]byte(file))
	if len(problems) > 0 || len(entries) != 4 {
		t.Fatalf("Parse = %v, %v, want 4 entries", entries, problems)
	}
	kept, problems := cellens.CheckSchemas(t.Context(), entries)
	if len(kept) != 1 || kept[0].File != "d.yaml" {
		t.Errorf("CheckSchemas kept %v, want only the entry of d.yaml", kept)
	}
	want := []struct {
		line, column int
		message      string
	}{
		{9, 11, `schema: properties[max].type: Unsupported value: "int": supported values: "array", "boolean", "integer", "number", "object", "string"`},
		{16, 9, "schema: properties[open].type: Required value: must not be empty for specified object fields"},
		{21, 5, "schema: type: Required value"},
	}
	if len(problems) < len(want) {
		t.Fatalf("CheckSchemas problems = %v, want %d", problems, len(want))
	}
	for i, w := range want {
		if p := problems[i]; p.Line != w.line || p.Column != w.column || !strings.HasPrefix(p.Message, w.message) {
			t.Errorf("CheckSchemas problem %d = %v, want %d:%d: %s", i, p, w.line, w.column, w.message)
		}
	}
}

// TestDocExamples checks that the examples of docs/cellens.md read without a
// problem, and show the four forms of a type: type, kind, schema and go (#15).
func TestDocExamples(t *testing.T) {
	t.Parallel()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "cellens.md"))
	if err != nil {
		t.Fatal(err)
	}
	forms := map[string]bool{}
	for _, block := range strings.Split(string(doc), "```yaml\n")[1:] {
		text, _, _ := strings.Cut(block, "```")
		if !strings.Contains(text, "types:") {
			continue
		}
		entries, problems := cellens.Parse([]byte(text))
		if entries, more := cellens.CheckSchemas(t.Context(), entries); len(problems)+len(more) > 0 || len(entries) == 0 {
			t.Errorf("Parse(%q) = %d entries, problems %v %v, want entries and none", text, len(entries), problems, more)
		}
		for _, e := range entries {
			forms["type"] = forms["type"] || e.Type != nil
			forms["kind"] = forms["kind"] || e.Kind != nil
			forms["schema"] = forms["schema"] || e.Schema != nil
			forms["go"] = forms["go"] || e.Go != nil
		}
	}
	if len(forms) != 4 || slices.Contains(slices.Collect(maps.Values(forms)), false) {
		t.Errorf("the examples of docs/cellens.md show %v, want each of type, kind, schema and go", forms)
	}
	if !strings.Contains(string(doc), issueExample) {
		t.Error("docs/cellens.md lacks the example of #15")
	}
}

// aliasBomb is a schema whose aliases multiply: each level names the one
// below ten times, seven levels down, which expands to ten million schemas.
func aliasBomb() string {
	var b strings.Builder
	b.WriteString("types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    schema:\n      type: object\n      properties:\n        l0: &l0 {type: string}\n")
	for level := 1; level <= 7; level++ {
		fmt.Fprintf(&b, "        l%d: &l%d {type: object, properties: {", level, level)
		for i := range 10 {
			fmt.Fprintf(&b, "p%d: *l%d, ", i, level-1)
		}
		b.WriteString("}}\n")
	}
	return b.String()
}

// TestParseSchemaAliases checks that a schema that contains itself through an
// alias, or whose aliases multiply, is refused at once instead of holding the
// server: Parse follows no alias in a schema (the review of 6f82d2a).
func TestParseSchemaAliases(t *testing.T) {
	t.Parallel()
	const head = "types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n"
	for _, tc := range []struct{ name, file, want string }{
		{"a schema that contains itself", head + "    schema: &a {type: object, properties: {x: *a}}\n", "schema: properties[x]: a YAML alias (*a) is not taken in a schema"},
		{"properties that contain themselves", head + "    schema:\n      type: object\n      properties: &p {b: {type: object, properties: *p}}\n", "schema: properties[b].properties: a YAML alias (*p) is not taken"},
		{"aliases that multiply", aliasBomb(), "schema: properties[l1].properties[p0]: a YAML alias (*l0) is not taken"},
		{"a schema that is an alias", "x: &s {type: string}\n" + head + "    schema: *s\n", "schema is a mapping"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entries, problems := cellens.Parse([]byte(tc.file))
			if len(entries) > 0 || !slices.ContainsFunc(problems, func(p cellens.Problem) bool { return strings.Contains(p.Message, tc.want) }) {
				t.Errorf("Parse = %d entries, problems %v, want none and one saying %q", len(entries), problems, tc.want)
			}
		})
	}
}
