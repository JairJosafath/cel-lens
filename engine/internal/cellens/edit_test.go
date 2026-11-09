package cellens_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
)

// entry reads an entry of the value fields name, with the type typ, or the
// kind kind when typ is "".
func entry(tb testing.TB, fields map[string]string, typ, kind string) cellens.Entry {
	tb.Helper()
	e, problems := cellens.ReadValue(fields)
	if len(problems) > 0 {
		tb.Fatalf("ReadValue(%v) problems = %v", fields, problems)
	}
	var err error
	if typ != "" {
		e.Type, err = cellens.ParseType(typ)
	} else {
		e.Kind, err = cellens.ParseKind(kind)
	}
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

// crd is the value of a CRD in file at path.
func crd(file, name, path string) map[string]string {
	return map[string]string{"file": file, "document": "CustomResourceDefinition " + name, "version": "v1", "path": path}
}

// people is a .cellens a person wrote, with comments above, between and
// inside its entries.
const people = `# Types for the widgets, by the platform team.
types:
  # a's size is a number.
  - file: a.yaml
    document: CustomResourceDefinition a.example.com
    # the served one
    version: v1
    path: spec.size
    type: int

  # c's size is a name.
  - file: c.yaml
    document: CustomResourceDefinition c.example.com
    version: v1
    path: spec.size
    type: string  # for now
# End of the list.
`

// apart is a .cellens with comments apart from the entries, after a blank
// line, indented like their keys: one above an entry, one at the end.
const apart = `types:
  - file: a.yaml
    document: CustomResourceDefinition a.example.com
    version: v1
    type: int

    # c: the size is a count
  - file: c.yaml
    document: CustomResourceDefinition c.example.com
    version: v1
    type: int

    # remark
`

// nestedBlock is a .cellens whose only entry ends with a block scalar, below
// its last key, with a blank line in it.
const nestedBlock = "types:\n  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    type: int\n    note:\n      text: |\n        one\n\n        two\n"

// TestSet checks that Set adds an entry in order, or replaces the type of the
// entry for the value, and changes no other line, comments included (#4).
func TestSet(t *testing.T) {
	t.Parallel()
	const bEntry = `  - file: b.yaml
    document: CustomResourceDefinition b.example.com
    version: v1
    path: spec.size
    type: int
`
	tests := []struct {
		name string
		text string
		e    cellens.Entry
		want string
	}{
		{
			name: "an empty text gets the header",
			text: "",
			e:    entry(t, crd("b.yaml", "b.example.com", "spec.size"), "int", ""),
			want: cellens.Header + "\ntypes:\n" + bEntry,
		},
		{
			name: "blank lines get the header",
			text: "\n\n",
			e:    entry(t, crd("b.yaml", "b.example.com", "spec.size"), "int", ""),
			want: cellens.Header + "\ntypes:\n" + bEntry,
		},
		{
			name: "comments only",
			text: "# mine",
			e:    entry(t, crd("b.yaml", "b.example.com", "spec.size"), "int", ""),
			want: "# mine\ntypes:\n" + bEntry,
		},
		{
			name: "types without entries",
			text: "# mine\ntypes:\n# below\n",
			e:    entry(t, crd("b.yaml", "b.example.com", "spec.size"), "int", ""),
			want: "# mine\ntypes:\n" + bEntry + "# below\n",
		},
		{
			name: "types: [] with a comment",
			text: "types: []  # none yet\n",
			e:    entry(t, crd("b.yaml", "b.example.com", "spec.size"), "int", ""),
			want: "types: # none yet\n" + bEntry,
		},
		{
			name: "types: ~",
			text: "types: ~\n",
			e:    entry(t, crd("b.yaml", "b.example.com", "spec.size"), "int", ""),
			want: "types:\n" + bEntry,
		},
		{
			name: "between entries, above the next one's comment",
			text: people,
			e:    entry(t, crd("b.yaml", "b.example.com", "spec.size"), "int", ""),
			want: strings.Replace(people, "  # c's size", bEntry+"  # c's size", 1),
		},
		{
			name: "first, above the first one's comment",
			text: people,
			e:    entry(t, crd("0.yaml", "b.example.com", "spec.size"), "int", ""),
			want: strings.Replace(people, "  # a's size", strings.Replace(bEntry, "b.yaml", "0.yaml", 1)+"  # a's size", 1),
		},
		{
			name: "last, after the last one's line comment",
			text: people,
			e:    entry(t, crd("d.yaml", "b.example.com", "spec.size"), "int", ""),
			want: strings.Replace(people, "# End of the list.", strings.Replace(bEntry, "b.yaml", "d.yaml", 1)+"# End of the list.", 1),
		},
		{
			name: "above the comment above the next one, after a blank line",
			text: apart,
			e:    entry(t, crd("b.yaml", "b.example.com", ""), "int", ""),
			want: strings.Replace(apart, "    # c:", "  - file: b.yaml\n    document: CustomResourceDefinition b.example.com\n    version: v1\n    type: int\n    # c:", 1),
		},
		{
			name: "last, before a comment after a blank line",
			text: apart,
			e:    entry(t, crd("d.yaml", "d.example.com", ""), "int", ""),
			want: strings.Replace(apart, "    type: int\n\n    # remark", "    type: int\n  - file: d.yaml\n    document: CustomResourceDefinition d.example.com\n    version: v1\n    type: int\n\n    # remark", 1),
		},
		{
			name: "same file, by document, then path",
			text: people,
			e:    entry(t, crd("a.yaml", "a.example.com", "spec.small"), "int", ""),
			want: strings.Replace(people, "  # c's size", "  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    path: spec.small\n    type: int\n  # c's size", 1),
		},
		{
			name: "replaces the type, keeping the comments inside",
			text: people,
			e:    entry(t, crd("a.yaml", "a.example.com", "spec.size"), "string", ""),
			want: strings.Replace(people, "    type: int\n", "    type: string\n", 1),
		},
		{
			name: "replaces the type, keeping its line comment",
			text: people,
			e:    entry(t, crd("c.yaml", "c.example.com", "spec.size"), "int", ""),
			want: strings.Replace(people, "type: string  # for now", "type: int  # for now", 1),
		},
		{
			name: "a value written another way is the same value",
			text: strings.Replace(people, "file: c.yaml", "file: ./c.yaml", 1),
			e:    entry(t, crd("c.yaml", "c.example.com", "spec.size"), "int", ""),
			want: strings.NewReplacer("file: c.yaml", "file: ./c.yaml", "type: string  # for now", "type: int  # for now").Replace(people),
		},
		{
			name: "a kind becomes a type, quoted",
			text: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    kind: v1 ConfigMap\n",
			e:    entry(t, map[string]string{"file": "p.yaml", "document": "ValidatingAdmissionPolicy p", "variable": "params"}, "{max:int, names: list(string)}", ""),
			want: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: \"{max: int, names: list(string)}\"\n",
		},
		{
			name: "a type becomes a kind",
			text: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: '{max: int}'  # was\n",
			e:    entry(t, map[string]string{"file": "p.yaml", "document": "ValidatingAdmissionPolicy p", "variable": "params"}, "", "apps/v1 Deployment"),
			want: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    kind: apps/v1 Deployment  # was\n",
		},
		{
			name: "replaces an unquoted struct, a problem",
			text: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: {max: int}\n",
			e:    entry(t, map[string]string{"file": "p.yaml", "document": "ValidatingAdmissionPolicy p", "variable": "params"}, "{max: string}", ""),
			want: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: \"{max: string}\"\n",
		},
		{
			name: "replaces a type over several lines",
			text: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: |\n      {max: int}\n\n    # after\n",
			e:    entry(t, map[string]string{"file": "p.yaml", "document": "ValidatingAdmissionPolicy p", "variable": "params"}, "int", ""),
			want: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: int\n\n    # after\n",
		},
		{
			name: "an entry without a type gets one",
			text: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    # todo\nother: 1\n",
			e:    entry(t, map[string]string{"file": "p.yaml", "document": "ValidatingAdmissionPolicy p", "variable": "params"}, "int", ""),
			want: "types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    # todo\n    type: int\nother: 1\n",
		},
		{
			name: "the indentation of the entries",
			text: "types:\n- file: a.yaml\n  document: CustomResourceDefinition a.example.com\n  version: v1\n  type: int\n",
			e:    entry(t, crd("b.yaml", "b.example.com", ""), "int", ""),
			want: "types:\n- file: a.yaml\n  document: CustomResourceDefinition a.example.com\n  version: v1\n  type: int\n- file: b.yaml\n  document: CustomResourceDefinition b.example.com\n  version: v1\n  type: int\n",
		},
		{
			name: "CRLF line endings",
			text: "# mine\r\ntypes:\r\n  - file: a.yaml\r\n    document: CustomResourceDefinition a.example.com\r\n    version: v1\r\n    type: int",
			e:    entry(t, crd("b.yaml", "b.example.com", "spec.size"), "int", ""),
			want: "# mine\r\ntypes:\r\n  - file: a.yaml\r\n    document: CustomResourceDefinition a.example.com\r\n    version: v1\r\n    type: int\r\n" + strings.ReplaceAll(bEntry, "\n", "\r\n"),
		},
		{
			name: "values YAML would read otherwise are quoted",
			text: "",
			e:    entry(t, map[string]string{"file": "true", "document": "ValidatingAdmissionPolicy p", "variable": "variables.cfg", "path": "labels[*]"}, "map(string, int)", ""),
			want: cellens.Header + "\ntypes:\n  - file: \"true\"\n    document: ValidatingAdmissionPolicy p\n    variable: variables.cfg\n    path: labels[*]\n    type: \"map(string, int)\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ed, err := cellens.Set([]byte(tt.text), tt.e)
			if err != nil {
				t.Fatalf("Set(%q) error = %v", tt.text, err)
			}
			if got := string(ed.Apply([]byte(tt.text))); got != tt.want {
				t.Errorf("Set(%q) gives\n%s\nwant\n%s", tt.text, got, tt.want)
			}
			if !cellens.Has(ed.Apply([]byte(tt.text)), tt.e) {
				t.Errorf("Has(Set(%q)) = false, want true", tt.text)
			}
		})
	}
}

// TestSetRefuses checks that Set refuses a text it cannot edit in place.
func TestSetRefuses(t *testing.T) {
	t.Parallel()
	e := entry(t, crd("a.yaml", "a.example.com", ""), "int", "")
	for _, tt := range []struct{ name, text, want string }{
		{"not YAML", "types: [\n", "not YAML"},
		{"a list at the root", "- a\n", "1:1: the .cellens is no mapping of lines with the key types"},
		{"types in brackets", "types: [{file: b.yaml}]\n", "1:8: types is no list of entries on their own lines"},
		{"types a mapping", "types:\n  file: a.yaml\n", "2:3: types is no list of entries on their own lines"},
		{"two documents", "types:\n---\ntypes:\n", "more than one YAML document"},
		{
			"an entry in braces",
			"types:\n  - {file: a.yaml, document: CustomResourceDefinition a.example.com, version: v1, type: string}\n",
			"2:5: the entry is written on one line in {...}",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := cellens.Set([]byte(tt.text), e)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Set(%q) error = %v, want one with %q", tt.text, err, tt.want)
			}
		})
	}
}

// TestRemove checks that Remove takes the entry with its own comments and
// leaves every other line, comments included (#4).
func TestRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		e    cellens.Entry
		want string
	}{
		{
			name: "the first, with its comments and the blank line after it",
			text: people,
			e:    entry(t, crd("a.yaml", "a.example.com", "spec.size"), "int", ""),
			want: "# Types for the widgets, by the platform team.\ntypes:\n" + people[strings.LastIndex(people, "\n\n")+2:],
		},
		{
			name: "the last, with the blank line before it",
			text: people,
			e:    entry(t, crd("c.yaml", "c.example.com", "spec.size"), "int", ""),
			want: people[:strings.LastIndex(people, "\n\n")+1] + "# End of the list.\n",
		},
		{
			name: "a comment apart from the entries stays",
			text: "types:\n  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    type: int\n\n  # Policies:\n\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: int\n",
			e:    entry(t, crd("a.yaml", "a.example.com", ""), "int", ""),
			want: "types:\n  # Policies:\n\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    type: int\n",
		},
		{
			name: "a comment after a blank line, above the next entry, stays with it",
			text: apart,
			e:    entry(t, crd("a.yaml", "a.example.com", ""), "int", ""),
			want: "types:\n" + apart[strings.Index(apart, "\n\n")+2:],
		},
		{
			name: "the last, without a comment after a blank line",
			text: apart,
			e:    entry(t, crd("c.yaml", "c.example.com", ""), "int", ""),
			want: apart[:strings.Index(apart, "\n\n")+1] + "\n    # remark\n",
		},
		{
			name: "a comment right after an entry goes with it",
			text: "types:\n  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    type: int\n    # a's note\n  - file: c.yaml\n    document: CustomResourceDefinition c.example.com\n    version: v1\n    type: int\n",
			e:    entry(t, crd("a.yaml", "a.example.com", ""), "int", ""),
			want: "types:\n  - file: c.yaml\n    document: CustomResourceDefinition c.example.com\n    version: v1\n    type: int\n",
		},
		{
			name: "the only one leaves the header",
			text: cellens.Header + "\ntypes:\n  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    type: int",
			e:    entry(t, crd("a.yaml", "a.example.com", ""), "int", ""),
			want: cellens.Header + "\ntypes:\n",
		},
		{
			name: "a block scalar deep in the entry, with a blank line",
			text: nestedBlock,
			e:    entry(t, crd("a.yaml", "a.example.com", ""), "int", ""),
			want: "types:\n",
		},
		{
			name: "an entry with a problem",
			text: "types:\n  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    type: strin\n",
			e:    entry(t, crd("a.yaml", "a.example.com", ""), "int", ""),
			want: "types:\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ed, err := cellens.Remove([]byte(tt.text), tt.e)
			if err != nil {
				t.Fatalf("Remove(%q) error = %v", tt.text, err)
			}
			if got := string(ed.Apply([]byte(tt.text))); got != tt.want {
				t.Errorf("Remove(%q) gives\n%s\nwant\n%s", tt.text, got, tt.want)
			}
		})
	}
	_, err := cellens.Remove([]byte(people), entry(t, crd("b.yaml", "b.example.com", ""), "int", ""))
	if !errors.Is(err, cellens.ErrNoEntry) {
		t.Errorf("Remove(no such entry) error = %v, want ErrNoEntry", err)
	}
	// Its indentation indicator lets the scalar's lines be less indented than
	// its first one: what Remove leaves would read as types: x, so it refuses.
	indicated := "types:\n  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    type: int\n    note:\n      text: |2\n          deep\n\n        x\n"
	if ed, err := cellens.Remove([]byte(indicated), entry(t, crd("a.yaml", "a.example.com", ""), "int", "")); err == nil {
		t.Errorf("Remove(%q) gives %q, want an error: types would no longer be a list", indicated, ed.Apply([]byte(indicated)))
	}
}

func TestBare(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		text string
		want bool
	}{
		{"", true},
		{cellens.Header + "\ntypes:\n", true},
		{cellens.Header + "\r\n\r\ntypes: []\r\n", true},
		{"types:\n", true},
		{cellens.Header + "\n# mine\ntypes:\n", false},
		{"types:\n  - file: a.yaml\n", false},
	} {
		if got := cellens.Bare([]byte(tt.text)); got != tt.want {
			t.Errorf("Bare(%q) = %t, want %t", tt.text, got, tt.want)
		}
	}
}

func TestReadValue(t *testing.T) {
	t.Parallel()
	e, problems := cellens.ReadValue(map[string]string{"file": "./x/../a.yaml", "document": "ValidatingAdmissionPolicy p", "variable": "params", "path": "spec.ports[].port", "type": "ignored"})
	if len(problems) > 0 || e.File != "a.yaml" || e.Variable != "params" || e.Path.String() != "spec.ports[].port" || e.Type != nil {
		t.Errorf("ReadValue = %+v, %v, want a.yaml params spec.ports[].port", e, problems)
	}
	for _, tt := range []struct {
		fields map[string]string
		want   string
	}{
		{map[string]string{"file": "../a.yaml", "document": "ValidatingAdmissionPolicy p", "variable": "params"}, `file "../a.yaml" is not a file in this .cellens's folder`},
		{map[string]string{"file": "a.yaml", "document": "Widget w", "variable": "params"}, "cel-lens reads CEL in"},
		{map[string]string{"file": "a.yaml", "document": "CustomResourceDefinition w"}, "needs version"},
		{map[string]string{"file": "a.yaml", "document": "ValidatingAdmissionPolicy p", "variable": "params", "version": "v1"}, "version is for CustomResourceDefinitions"},
		{map[string]string{"file": "a.yaml", "document": "ValidatingAdmissionPolicy p", "variable": "params", "path": "a..b"}, `path "a..b"`},
	} {
		_, problems := cellens.ReadValue(tt.fields)
		if !slices.ContainsFunc(problems, func(p cellens.Problem) bool { return strings.Contains(p.Message, tt.want) }) {
			t.Errorf("ReadValue(%v) problems = %v, want one with %q", tt.fields, problems, tt.want)
		}
	}
}

func TestParseKind(t *testing.T) {
	t.Parallel()
	if k, err := cellens.ParseKind(" example.com/v1  Widget "); err != nil || *k != (cellens.KindRef{APIVersion: "example.com/v1", Kind: "Widget"}) {
		t.Errorf("ParseKind = %v, %v, want example.com/v1 Widget", k, err)
	}
	if _, err := cellens.ParseKind("Widget"); err == nil {
		t.Error("ParseKind(Widget) error = nil, want one")
	}
}

// FuzzSetRemove checks that Set and Remove never break a .cellens: what they
// return reads back with the entry set or gone, and every other entry kept.
func FuzzSetRemove(f *testing.F) {
	for _, s := range []string{
		"", "# c\n", people, apart, nestedBlock, "types:\n", "types: []\n", "types: ~ # c\n", "types:\n- file: a.yaml\n  type: int\n",
		"types:\n  -\n    file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    type: |\n      int\n",
		"types:\n  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    path: spec.size\n    type: \"{a: \\\"x\\\"}\" # q\n",
		"x: 1\ntypes:\n  - file: a.yaml\n    document: CustomResourceDefinition a.example.com\n    version: v1\n    path: spec.size\n    kind: v1 Pod\ny: 2\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		e := entry(t, crd("a.yaml", "a.example.com", "spec.size"), "string", "")
		before, problemsBefore := cellens.Parse([]byte(text))
		others := func(entries []cellens.Entry) []string {
			var out []string
			for _, x := range entries {
				if x.File == e.File && x.Document == e.Document && x.Version == e.Version && x.Path.String() == e.Path.String() {
					continue
				}
				typ := ""
				if x.Type != nil {
					typ = x.Type.String()
				}
				out = append(out, x.File+" "+x.Document.Name+" "+x.Variable+" "+x.Path.String()+" "+typ)
			}
			slices.Sort(out)
			return out
		}
		if ed, err := cellens.Set([]byte(text), e); err == nil {
			out := ed.Apply([]byte(text))
			after, problems := cellens.Parse(out)
			// An entry with a problem, such as an unknown key, is left out.
			if !cellens.Has(out, e) || len(problems) == 0 && !slices.ContainsFunc(after, func(x cellens.Entry) bool {
				return x.File == e.File && x.Path.String() == e.Path.String() && x.Type != nil && x.Type.String() == "string"
			}) {
				t.Errorf("Set(%q) gives %q, without the entry", text, out)
			}
			if !slices.Equal(others(before), others(after)) || len(problems) > len(problemsBefore) {
				t.Errorf("Set(%q) gives %q: entries %v, problems %v; before %v, %v", text, out, others(after), problems, others(before), problemsBefore)
			}
		}
		if ed, err := cellens.Remove([]byte(text), e); err == nil {
			out := ed.Apply([]byte(text))
			after, problems := cellens.Parse(out)
			if !slices.Equal(others(before), others(after)) || len(problems) > len(problemsBefore) {
				t.Errorf("Remove(%q) gives %q: entries %v, problems %v; before %v, %v", text, out, others(after), problems, others(before), problemsBefore)
			}
		}
	})
}
