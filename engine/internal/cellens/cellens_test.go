package cellens_test

import (
	"reflect"
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
		{"neither type nor kind", "types:\n  - file: a.yaml\n    " + policy, 2, 5, []string{"neither type nor kind"}},
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
