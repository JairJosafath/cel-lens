package cellens_test

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
)

// cursor splits text at its "|": the text without it, and the line and
// character of the "|", from 0.
func cursor(t *testing.T, text string) (out string, line, col int) {
	t.Helper()
	before, after, ok := strings.Cut(text, "|")
	if !ok {
		t.Fatalf("no | in %q", text)
	}
	line = strings.Count(before, "\n")
	col = utf8.RuneCountInString(before[strings.LastIndex(before, "\n")+1:])
	return before + after, line, col
}

const entryHead = "types:\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n"

// TestLocate checks what Locate reads at a position of a .cellens: the
// mapping, the key or value, the word and the keys around it, in block and
// flow mappings, in a schema, and in a text YAML cannot read (#16).
func TestLocate(t *testing.T) {
	t.Parallel()
	entry := map[string]string{"file": "a.yaml", "document": "ValidatingAdmissionPolicy p"}
	schema := []string{"types", "-", "schema"}
	tests := []struct {
		name, text string
		want       cellens.Spot
	}{
		{"a new key of an entry", entryHead + "    |\n", cellens.Spot{Path: []string{"types", "-"}, Start: 4, End: 4, Indent: 4, Keys: entry}},
		{"a key being typed", entryHead + "    vari|\n", cellens.Spot{Path: []string{"types", "-"}, Start: 4, End: 8, Indent: 4, Keys: entry}},
		{"a key of a compact list", "types:\n- file: a.yaml\n  |\n", cellens.Spot{Path: []string{"types", "-"}, Start: 2, End: 2, Indent: 2, Keys: map[string]string{"file": "a.yaml"}}},
		{"the first key, after the dash", "types:\n  - fi|\n", cellens.Spot{Path: []string{"types", "-"}, Start: 4, End: 6, Indent: 4, Keys: map[string]string{}}},
		{"on a key", "types:\n  - fi|le: a.yaml\n", cellens.Spot{Path: []string{"types", "-"}, KeyAt: "file", Start: 4, End: 8, Colon: true, Indent: 4, Keys: map[string]string{"file": "a.yaml"}}},
		{"a value", "types:\n  - file: a.yaml\n    document: Validating|AdmissionPolicy p\n", cellens.Spot{Path: []string{"types", "-"}, Key: "document", KeyAt: "document", Start: 14, End: 41, Indent: 4, Keys: entry}},
		{"an empty value", entryHead + "    variable: |\n", cellens.Spot{Path: []string{"types", "-"}, Key: "variable", KeyAt: "variable", Start: 14, End: 14, Indent: 4, Keys: with(entry, "variable", "")}},
		{"a value right after the colon", "types:\n  - file:|\n", cellens.Spot{Path: []string{"types", "-"}, Key: "file", KeyAt: "file", Start: 9, End: 9, Pad: true, Indent: 4, Keys: map[string]string{"file": ""}}},
		{"an open quote", entryHead + "    type: \"{name: st|\n", cellens.Spot{Path: []string{"types", "-"}, Key: "type", KeyAt: "type", Start: 11, End: 20, Quote: '"', Indent: 4, Keys: with(entry, "type", "{name: st")}},
		{"before a comment", entryHead + "    path: spec.|  # here\n", cellens.Spot{Path: []string{"types", "-"}, Key: "path", KeyAt: "path", Start: 10, End: 15, Indent: 4, Keys: with(entry, "path", "spec.")}},
		{"a flow entry", "types:\n  - {file: a.yaml, document: |}\n", cellens.Spot{Path: []string{"types", "-"}, Key: "document", KeyAt: "document", Start: 29, End: 29, Flow: true, Indent: 4, Keys: map[string]string{"file": "a.yaml", "document": ""}}},
		{"a key of a flow entry", "types:\n  - {file: a.yaml, ve|", cellens.Spot{Path: []string{"types", "-"}, KeyAt: "ve", Start: 19, End: 21, Flow: true, Indent: 4, Keys: map[string]string{"file": "a.yaml"}}},
		{"a schema keyword", entryHead + "    schema:\n      type: object\n      properties:\n        max:\n          ty|\n", cellens.Spot{Path: append(schema, "properties", "max"), Start: 10, End: 12, Indent: 10, Keys: map[string]string{}}},
		{"a schema value", entryHead + "    schema:\n      type: objec|t\n      properties: {}\n", cellens.Spot{Path: schema, Key: "type", KeyAt: "type", Start: 12, End: 18, Indent: 6, Keys: map[string]string{"type": "object", "properties": ""}}},
		{"a value in a flow schema", entryHead + "    schema:\n      properties:\n        max: {type: integer, minimum: |}\n", cellens.Spot{Path: append(schema, "properties", "max"), Key: "minimum", KeyAt: "minimum", Start: 38, End: 38, Flow: true, Indent: 8, Keys: map[string]string{"type": "integer", "minimum": ""}}},
		{"a nested flow mapping", entryHead + "    schema: {type: object, items: {ty|}}\n", cellens.Spot{Path: append(schema, "items"), KeyAt: "ty", Start: 35, End: 37, Flow: true, Indent: 4, Keys: map[string]string{}}},
		{"an item of required", entryHead + "    schema:\n      required:\n        - na|\n", cellens.Spot{Path: schema, Key: "required", Item: true, Start: 10, End: 12, Indent: 10, Keys: map[string]string{}}},
		{"an item of required in brackets", entryHead + "    schema:\n      required: [a, |]\n", cellens.Spot{Path: schema, Key: "required", KeyAt: "required", Item: true, Start: 20, End: 20, Flow: true, Indent: 6, Keys: map[string]string{}}},
		{"the root", "|", cellens.Spot{Start: 0, End: 0, Keys: map[string]string{}}},
		{"a root key being typed", "# head\nty|\n", cellens.Spot{Start: 0, End: 2, Keys: map[string]string{}}},
		{"a line ending in CR LF", "types:\r\n  - file: a.yaml\r\n    |\r\n", cellens.Spot{Path: []string{"types", "-"}, Start: 4, End: 4, Indent: 4, Keys: map[string]string{"file": "a.yaml"}}},
		{"after an entry YAML cannot read", "types:\n  - file: b.yaml\n    pa\n  - file: a.yaml\n    document: ValidatingAdmissionPolicy p\n    vari|", cellens.Spot{Path: []string{"types", "-"}, Start: 4, End: 8, Indent: 4, Keys: entry}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			text, line, col := cursor(t, tt.text)
			got, ok := cellens.Locate([]byte(text), line, col)
			if !ok {
				t.Fatalf("Locate(%q, %d, %d) = false, want a spot", text, line, col)
			}
			want := tt.want
			want.Line = line
			got.Entry, got.EntryLine = nil, 0 // TestLocateEntry checks them
			if !slices.Equal(got.Path, want.Path) || got.Key != want.Key || got.Item != want.Item || got.Line != want.Line ||
				got.Start != want.Start || got.End != want.End || got.Quote != want.Quote || got.Colon != want.Colon ||
				got.Pad != want.Pad || got.Flow != want.Flow || got.Indent != want.Indent || got.KeyAt != want.KeyAt || !maps.Equal(got.Keys, want.Keys) {
				t.Errorf("Locate(%q, %d, %d) =\n %+v, want\n %+v", text, line, col, got, want)
			}
		})
	}
}

func with(m map[string]string, k, v string) map[string]string {
	out := maps.Clone(m)
	out[k] = v
	return out
}

// TestLocateEntry checks the keys of the entry a position is in, at any
// depth, and the line of its first key.
func TestLocateEntry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, text string
		keys       map[string]string
		line       int
	}{
		{"in the entry", entryHead + "    vari|", map[string]string{"file": "a.yaml", "document": "ValidatingAdmissionPolicy p"}, 1},
		{"deep in its schema", entryHead + "    schema:\n      properties:\n        max: {ty|", map[string]string{"file": "a.yaml", "document": "ValidatingAdmissionPolicy p", "schema": ""}, 1},
		{"a dash on its own line", "types:\n  -\n    file: a.yaml\n    pa|", map[string]string{"file": "a.yaml"}, 2},
		{"a flow entry", "types:\n  - {file: a.yaml, variable: params, ty|}", map[string]string{"file": "a.yaml", "variable": "params"}, 1},
		{"the second entry", entryHead + "    type: int\n  - file: b.yaml\n    pa|", map[string]string{"file": "b.yaml"}, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			text, line, col := cursor(t, tt.text)
			got, ok := cellens.Locate([]byte(text), line, col)
			if !ok || !maps.Equal(got.Entry, tt.keys) || got.EntryLine != tt.line {
				t.Errorf("Locate(%q) entry = %v at line %d (%t), want %v at line %d", text, got.Entry, got.EntryLine, ok, tt.keys, tt.line)
			}
		})
	}
}

// TestLocateNowhere checks the positions Locate has no spot for.
func TestLocateNowhere(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		entryHead + "    type: int  # a com|ment\n",
		entryHead + "  |  type: int\n",
		entryHead + "    type: >\n      {name: st|\n",
		entryHead + "    type: list(\n      in|\n",
	} {
		text, line, col := cursor(t, text)
		if got, ok := cellens.Locate([]byte(text), line, col); ok {
			t.Errorf("Locate(%q, %d, %d) = %+v, want none", text, line, col, got)
		}
	}
	if _, ok := cellens.Locate([]byte("types:\n"), 5, 0); ok {
		t.Error("Locate past the end = a spot, want none")
	}
}

func TestTypeNameAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text  string
		ok    bool
		start int
		end   int
	}{
		{"|", true, 0, 0},
		{"st|r", true, 0, 3},
		{"list(|", true, 5, 5},
		{"map(string, in|t)", true, 12, 15},
		{"{|", false, 1, 1},
		{"{na|me: string}", false, 1, 5},
		{"{name: |}", true, 7, 7},
		{"{name: string, |}", false, 15, 15},
		{"{name: list({port: i|})}", true, 19, 20},
		{"int |", false, 4, 4},
	}
	for _, tt := range tests {
		text, _, at := cursor(t, tt.text)
		start, end, ok := cellens.TypeNameAt(text, at)
		if ok != tt.ok || start != tt.start || end != tt.end {
			t.Errorf("TypeNameAt(%q, %d) = %d, %d, %t, want %d, %d, %t", text, at, start, end, ok, tt.start, tt.end, tt.ok)
		}
	}
}

// FuzzLocate checks that Locate reads any text at any position without a
// panic, and gives a word on the position's line that holds the position,
// as completion replaces it; and that Parse reads it too.
func FuzzLocate(f *testing.F) {
	for _, s := range []string{
		"", entryHead + "    type: \"{name: st\n", "types:\n  - {file: a, document: [x, {y: \"z\n", "types:\n  -\n    schema:\n      required:\n        - a\n",
		"types:\r\n- file: 'a''b'\r\n  type: |\r\n    x\r\n", "  - - [\n\t- {\"a\": b}: c # d\n", "types:\n  - file: a\n    pa\n  - file: \"b\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		cellens.Parse([]byte(text))
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		for i, l := range lines {
			n := utf8.RuneCountInString(strings.TrimSuffix(l, "\r"))
			for col := 0; col <= n+1; col++ {
				s, ok := cellens.Locate([]byte(text), i, col)
				if ok && (s.Line != i || s.Start < 0 || s.Start > s.End || s.End > n || s.Start > min(col, n) || s.End < min(col, n)) {
					t.Fatalf("Locate(%q, %d, %d) = %+v: its word is not on line %d of %d characters around the position", text, i, col, s, i, n)
				}
			}
		}
	})
}

func FuzzTypeNameAt(f *testing.F) {
	for _, s := range []string{"", "{a: list(map(string, {b: int}))}", "{,:(}}"} {
		f.Add(s, 3)
	}
	f.Fuzz(func(t *testing.T, s string, at int) {
		start, end, _ := cellens.TypeNameAt(s, at)
		if n := utf8.RuneCountInString(s); start < 0 || start > end || end > n {
			t.Errorf("TypeNameAt(%q, %d) = %d, %d, out of the %d characters", s, at, start, end, n)
		}
	})
}
