package check_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// TestFixtures checks every file in testdata. A fixture notes, in comments, the
// result the API server gives for it ("# api-server:") and what cel-lens must
// report ("# want: line:column-line:column message"), the exact range and a part
// of the message, one line per diagnostic in order. A
// fixture without "# want:" lines is accepted by the API server.
func TestFixtures(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("testdata/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".yaml"), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for line := range strings.Lines(string(data)) {
				if w, ok := strings.CutPrefix(line, "# want: "); ok {
					want = append(want, strings.TrimSpace(w))
				}
			}
			got, err := check.Check(context.Background(), file, data, allOn())
			if err != nil {
				t.Fatalf("Check(%s) error = %v", file, err)
			}
			if len(got) != len(want) {
				t.Fatalf("Check(%s) = %d diagnostics %v, want %d", file, len(got), got, len(want))
			}
			for i, d := range got {
				pos, msg, _ := strings.Cut(want[i], " ")
				if at := fmt.Sprintf("%d:%d-%d:%d", d.Start.Line, d.Start.Column, d.End.Line, d.End.Column); at != pos || !strings.Contains(d.Message, msg) ||
					d.Severity != check.SeverityError || d.File != file {
					t.Errorf("Check(%s)[%d] = %s %s %q, want %s error containing %q", file, i, d.File, at, d.Message, pos, msg)
				}
			}
		})
	}
}

func TestCheckMultiDocument(t *testing.T) {
	t.Parallel()
	src := "apiVersion: v1\nkind: ConfigMap\n---\n" +
		"apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: object.x +\n" +
		"---\n" +
		"apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: nope\n"
	got, err := check.Check(context.Background(), "m.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	var lines []string
	for _, d := range got {
		lines = append(lines, strconv.Itoa(d.Start.Line))
	}
	if strings.Join(lines, ",") != "8,14" {
		t.Errorf("Check(multi-document) = diagnostics on lines %v, want 8,14: %v", lines, got)
	}
}

func TestCheckUTF16MultiDocument(t *testing.T) {
	t.Parallel()
	doc := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: %s\n"
	text := fmt.Sprintf(doc, "object.x +") + "---\n" + fmt.Sprintf(doc, "nope")
	clean := "a: 1\n---\nb: 2\n"
	tests := []struct {
		name string
		src  string
		want []int // lines of the diagnostics
	}{
		{"valid LE", utf16le(units(clean)...), nil},
		{"valid policies, LE", utf16le(units(fmt.Sprintf(doc, "'true'") + "---\n" + fmt.Sprintf(doc, "'true'"))...), nil},
		{"valid BE", utf16be(units(clean)...), nil},
		{"CEL error in the first document, LE", utf16le(units(text)...), []int{5, 11}},
		{"CEL error in the first document, BE", utf16be(units(text)...), []int{5, 11}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "u.yaml", []byte(tt.src))
			if err != nil {
				t.Fatalf("Check(%s) error = %v", tt.name, err)
			}
			var lines []int
			for _, d := range got {
				lines = append(lines, d.Start.Line)
			}
			if !slices.Equal(lines, tt.want) {
				t.Errorf("Check(%s) = diagnostics on lines %v, want %v: %v", tt.name, lines, tt.want, got)
			}
		})
	}
}

// diag is the line, the start and end column and the whole message of a diagnostic.
type diag struct {
	line, start, end int
	msg              string
}

// celSyntax is the diagnostic for the unfinished "object.x +" expression that
// the policy template puts on the given line.
func celSyntax(line int) diag {
	return diag{line, 29, 30, "Syntax error: mismatched input '<EOF>' expecting {'[', '{', '(', '.', '-', '!', 'true', 'false', 'null', NUM_FLOAT, NUM_INT, NUM_UINT, STRING, BYTES, IDENTIFIER}"}
}

func TestCheckMalformedYAML(t *testing.T) {
	t.Parallel()
	policy := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: %s\n"
	tests := []struct {
		name string
		src  string
		want []diag // every diagnostic, in order
	}{
		{
			name: "lone carriage return ends a line",
			src:  "\r\t\r\n",
			want: []diag{
				{2, 1, 2, "YAML is broken here: found character that cannot start any token"},
			},
		},
		{
			name: "parser error after lone carriage returns",
			src:  "a: 1\rb: 2\rc: [\n",
			want: []diag{
				{3, 1, 5, "YAML is broken here: did not find expected node content"},
			},
		},
		{
			name: "document after a marker ended by a lone carriage return",
			src:  "a: 1\r---\rb: \t[\n",
			want: []diag{
				{3, 1, 6, "YAML is broken here: did not find expected node content"},
			},
		},
		{
			name: "U+0085 is a line break",
			src:  "a: 1\u0085b: \x01\n",
			want: []diag{
				{2, 4, 5, "YAML is broken here: control characters are not allowed"},
			},
		},
		{
			name: "surrogate code point after the first line",
			src:  "a: 1\n\xed\xa0\x80\n",
			want: []diag{
				{2, 1, 2, "YAML is broken here: invalid Unicode character"},
			},
		},
		{
			name: "incomplete UTF-16 character after the first line",
			src:  utf16le(units("a: 1\n", 'b')...) + "\x0a",
			want: []diag{
				{2, 2, 3, "YAML is broken here: incomplete UTF-16 character"},
			},
		},
		{
			name: "bad unit in the second document of a UTF-16LE stream",
			src:  utf16le(units("a: 1\n---\nb: 2\n", 0xdc00, '\n')...),
			want: []diag{
				{4, 1, 2, "YAML is broken here: unexpected low surrogate area"},
			},
		},
		{
			name: "bad unit in the second document of a UTF-16BE stream",
			src:  utf16be(units("a: 1\n---\nb: 2\n", 0xdc00, '\n')...),
			want: []diag{
				{4, 1, 2, "YAML is broken here: unexpected low surrogate area"},
			},
		},
		{
			name: "unexpected low surrogate after the first line",
			src:  utf16le(units("a: 1\n", 0xdc00, '\n')...),
			want: []diag{
				{2, 1, 2, "YAML is broken here: unexpected low surrogate area"},
			},
		},
		{
			name: "incomplete UTF-16 surrogate pair after the first line",
			src:  utf16le(units("a: 1\n", 0xd800)...),
			want: []diag{
				{2, 1, 2, "YAML is broken here: incomplete UTF-16 surrogate pair"},
			},
		},
		{
			name: "expected low surrogate after the first line",
			src:  utf16le(units("a: 1\n", 0xd800, 'x', '\n')...),
			want: []diag{
				{2, 1, 2, "YAML is broken here: expected low surrogate area"},
			},
		},
		{
			name: "big-endian UTF-16 with CRLF before the bad unit",
			src:  utf16be(units("a: 1\r\n", 0xdc00, '\n')...),
			want: []diag{
				{2, 1, 2, "YAML is broken here: unexpected low surrogate area"},
			},
		},
		{
			name: "overlong UTF-8 sequence after the first line",
			src:  "a: 1\n\xc0\x80\n",
			want: []diag{
				{2, 1, 2, "YAML is broken here: invalid length of a UTF-8 sequence"},
			},
		},
		{
			name: "broken last document",
			src:  fmt.Sprintf(policy, "object.x +") + "---\nkey: [unclosed\n",
			want: []diag{
				celSyntax(5),
				{7, 1, 15, "YAML is broken here: did not find expected ',' or ']'"},
			},
		},
		{
			name: "control character in the document after a valid one",
			src:  fmt.Sprintf(policy, "object.x +") + "---\nkey: \x00\n",
			want: []diag{
				celSyntax(5),
				{7, 6, 7, "YAML is broken here: control characters are not allowed"},
			},
		},
		{
			name: "invalid UTF-8 byte in the document after a broken one",
			src:  "a: b: c\n---\nkey: \xff\n",
			want: []diag{
				{1, 1, 8, "YAML is broken here: mapping values are not allowed in this context"},
				{3, 6, 7, "YAML is broken here: invalid leading UTF-8 octet"},
			},
		},
		{
			name: "broken first document, the next is checked",
			src:  "a: b: c\n---\n" + fmt.Sprintf(policy, "object.y +"),
			want: []diag{
				{1, 1, 8, "YAML is broken here: mapping values are not allowed in this context"},
				celSyntax(7),
			},
		},
		{
			name: "broken middle document",
			src:  fmt.Sprintf(policy, "object.x +") + "---\nfoo:\n  - a\n b: c\n---\n" + fmt.Sprintf(policy, "object.z +"),
			want: []diag{
				celSyntax(5),
				{9, 2, 6, "YAML is broken here: did not find expected key"},
				celSyntax(15),
			},
		},
		{
			name: "mis-indented key far below its block",
			src:  "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          properties:\n            spec:\n              type: object\n              properties:\n                a:\n                  type: string\n                b:\n                  type: string\n              required: [a]\n          x-kubernetes-validations:\n              - rule: self.a\n              type: object\n",
			want: []diag{
				{19, 15, 27, "YAML is broken here: did not find expected '-' indicator"},
			},
		},
		{
			name: "non-ASCII characters on the broken line",
			src:  "kind: X\nspec:\n description: Größe — 日本語: x\n",
			want: []diag{
				{3, 2, 29, "YAML is broken here: mapping values are not allowed in this context"},
			},
		},
		{
			name: "line holding only blanks",
			src:  "\t\t\n",
			want: []diag{
				{1, 1, 3, "YAML is broken here: found character that cannot start any token"},
			},
		},
		{
			name: "empty first lines, the parser blames line 1",
			src:  "\n\n\x00",
			want: []diag{
				{3, 1, 2, "YAML is broken here: control characters are not allowed"},
			},
		},
		{
			name: "control character below empty first lines",
			src:  "\n\nkey: v\n\x00",
			want: []diag{
				{4, 1, 2, "YAML is broken here: control characters are not allowed"},
			},
		},
		{
			name: "control character below NEL, LS and PS line ends",
			src:  "a: 1\u0085b: 2\u2028c: 3\u2029d: x\x01y\n",
			want: []diag{
				{4, 5, 6, "YAML is broken here: control characters are not allowed"},
			},
		},
		{
			name: "control character in the middle of a line",
			src:  "a: 1\nb: \x01\n",
			want: []diag{
				{2, 4, 5, "YAML is broken here: control characters are not allowed"},
			},
		},
		{
			name: "invalid UTF-8 byte after the first line",
			src:  "a: 1\nb: ä\xff\n",
			want: []diag{
				{2, 5, 6, "YAML is broken here: invalid leading UTF-8 octet"},
			},
		},
		{
			name: "invalid trailing UTF-8 octet after the first line",
			src:  "a: 1\nb: \xc3\x28\n",
			want: []diag{
				{2, 4, 5, "YAML is broken here: invalid trailing UTF-8 octet"},
			},
		},
		{
			name: "incomplete UTF-8 octet sequence after the first line",
			src:  "a: 1\nb: \xe2\x82",
			want: []diag{
				{2, 4, 5, "YAML is broken here: incomplete UTF-8 octet sequence"},
			},
		},
		{
			name: "code point beyond U+10FFFF after the first line",
			src:  "a: 1\nb: \xf4\x90\x80\x80\n",
			want: []diag{
				{2, 4, 5, "YAML is broken here: invalid Unicode character"},
			},
		},
		{
			name: "bad character in the last of several documents",
			src:  "a: 1\n---\nb: 2\n---\nc: \x00\n",
			want: []diag{
				{5, 4, 5, "YAML is broken here: control characters are not allowed"},
			},
		},
		{
			name: "broken document before one with a control character",
			src:  "a: 1\n---\nfoo:\n  - a\n b: c\n---\nc: 3\n---\nk: \x00\n",
			want: []diag{
				{5, 2, 6, "YAML is broken here: did not find expected key"},
				{9, 4, 5, "YAML is broken here: control characters are not allowed"},
			},
		},
		{
			name: "blanks after an empty line",
			src:  "\n\t\t\n",
			want: []diag{
				{2, 1, 3, "YAML is broken here: found character that cannot start any token"},
			},
		},
		{
			name: "blanks after an empty line, no final newline",
			src:  "\n\t",
			want: []diag{
				{2, 1, 2, "YAML is broken here: found character that cannot start any token"},
			},
		},
		{
			name: "blanks after several empty lines",
			src:  "\n\n\t\n",
			want: []diag{
				{3, 1, 2, "YAML is broken here: found character that cannot start any token"},
			},
		},
		{
			name: "blanks between CRLF line ends",
			src:  "\r\n\t\r\n",
			want: []diag{
				{2, 1, 2, "YAML is broken here: found character that cannot start any token"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "bad.yaml", []byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), "bad.yaml") {
				t.Errorf("Check(malformed) error = %v, want one naming bad.yaml", err)
			}
			var have []diag
			for _, d := range got {
				have = append(have, diag{d.Start.Line, d.Start.Column, d.End.Column, d.Message})
			}
			if !slices.Equal(have, tt.want) {
				t.Errorf("Check(malformed) = %+v, want %+v", have, tt.want)
			}
		})
	}
}

func TestCheckEmptyAndForeign(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"empty":          "",
		"not kubernetes": "a: 1\n",
		"other kind":     "apiVersion: v1\nkind: Pod\n",
		"old CRD":        "apiVersion: apiextensions.k8s.io/v1beta1\nkind: CustomResourceDefinition\nspec:\n  validation:\n    openAPIV3Schema:\n      x-kubernetes-validations:\n        - rule: nope\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "e.yaml", []byte(src))
			if err != nil || len(got) != 0 {
				t.Errorf("Check(%s) = %v, %v, want no diagnostics", name, got, err)
			}
		})
	}
}

func TestCheckCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: nope\n"
	if _, err := check.Check(ctx, "c.yaml", []byte(src)); err == nil {
		t.Error("Check(cancelled) error = nil, want the context's error")
	}
}

func FuzzCheck(f *testing.F) {
	for _, s := range []string{
		"apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: |\n        a &&\n          b\n",
		"apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  validation:\n    openAPIV3Schema:\n      x-kubernetes-validations:\n        - rule: \"self.a\"\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, src string) {
		_, _ = check.Check(context.Background(), "f.yaml", []byte(src))
	})
}

func BenchmarkCheck(b *testing.B) {
	data, err := os.ReadFile("testdata/vap-valid.yaml")
	if err != nil {
		b.Fatal(err)
	}
	simple, err := os.ReadFile("testdata/vap-no-variables.yaml")
	if err != nil {
		b.Fatal(err)
	}
	crd, err := os.ReadFile("testdata/crd-valid.yaml")
	if err != nil {
		b.Fatal(err)
	}
	kyverno, err := os.ReadFile("testdata/kyverno/test__cli__test-validating-policy__operation-delete__policy.yaml")
	if err != nil {
		b.Fatal(err)
	}
	const policies = 20
	for _, c := range []struct {
		name  string
		data  []byte
		count int // the policies or CRDs in data
	}{
		{"policy", data, 1},
		{"policies-with-variables", bytes.Repeat(append([]byte("---\n"), data...), policies), policies},
		{"policies", bytes.Repeat(append([]byte("---\n"), simple...), policies), policies},
		{"crd", crd, 1},
		{"kyverno-policy", kyverno, 1},
	} {
		b.Run(c.name, func(b *testing.B) {
			ctx := context.Background()
			b.SetBytes(int64(len(c.data)))
			for b.Loop() {
				if _, err := check.Check(ctx, "b.yaml", c.data); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*c.count), "ns/doc")
		})
	}
}

// TestCheckRange checks that a problem covers the whole part that is wrong: a
// name, an operator or a literal, in every style of YAML scalar.
func TestCheckRange(t *testing.T) {
	t.Parallel()
	const head = `apiVersion: apiextensions.k8s.io/v1
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
          properties:
            name: {type: string}
            replicas: {type: integer}
          x-kubernetes-validations:
`
	tests := []struct {
		name  string
		rule  string // the YAML after "- rule:"
		start string // line:column
		end   string
	}{
		{"unknown function", ` self.name.startWith('a')`, "21:31", "21:40"},
		{"space before call", ` self.name.startWith ('a')`, "21:31", "21:40"},
		{"unknown field", ` self.maxReplica > 1`, "21:26", "21:36"},
		{"has unknown field", ` has(self.maxReplica)`, "21:30", "21:40"},
		{"has nested unknown field", ` has(self.name.foo)`, "21:35", "21:38"},
		{"has closing parenthesis on a later line", " |\n                has(self.\n                  maxReplica\n                )", "23:19", "23:29"},
		{"space after dot", ` self. maxReplica > 1`, "21:27", "21:37"},
		{"operator", ` self.replicas == 'a'`, "21:35", "21:37"},
		{"regex literal", ` self.name.matches('[a-')`, "21:39", "21:44"},
		{"regex literal with non-ASCII", ` self.name.matches('[é-')`, "21:39", "21:44"},
		{"double quoted", ` "self.name.matches('[a-')"`, "21:40", "21:45"},
		{"single quoted", ` 'self.replicas == "a"'`, "21:36", "21:38"},
		{"literal block", " |\n                self.replicas == 'a'", "22:31", "22:33"},
		{"folded block", " >\n                self.name.startWith('a')", "22:27", "22:36"},
		{"literal block second line", " |\n                self.replicas > 0 &&\n                  self.maxReplica > 1", "23:24", "23:34"},
		{"escape in double quotes", ` "self.name.matches(\"[a-\")"`, "21:40", "21:47"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := []byte(head + "            - message: m\n              rule:" + tt.rule + "\n")
			got, err := check.Check(context.Background(), "f.yaml", data)
			if err != nil || len(got) != 1 {
				t.Fatalf("Check(%s) = %v, %v, want one diagnostic", tt.name, got, err)
			}
			d := got[0]
			start := fmt.Sprintf("%d:%d", d.Start.Line, d.Start.Column)
			end := fmt.Sprintf("%d:%d", d.End.Line, d.End.Column)
			if start != tt.start || end != tt.end {
				t.Errorf("Check(%s) range = %s-%s, want %s-%s", tt.name, start, end, tt.start, tt.end)
			}
		})
	}
}

// TestCheckTypoAfterUnicodeLineBreak pins that a rule of two lines, broken by a
// next line, line separator or paragraph separator, still underlines the typo
// on its second line: the YAML parser keeps U+2028 and U+2029 in the value and
// folds U+0085 to a space, and the character map follows it.
func TestCheckTypoAfterUnicodeLineBreak(t *testing.T) {
	t.Parallel()
	const head = `apiVersion: apiextensions.k8s.io/v1
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
          properties:
            name: {type: string}
            replicas: {type: integer}
          x-kubernetes-validations:
            - rule:`
	for _, br := range []struct{ name, end string }{{"LS", "\u2028"}, {"PS", "\u2029"}, {"NEL", "\u0085"}} {
		for _, q := range []struct{ name, open, close string }{{"plain", " ", ""}, {"double quoted", ` "`, `"`}} {
			t.Run(br.name+" "+q.name, func(t *testing.T) {
				t.Parallel()
				data := []byte(head + q.open + "self.name == 'a" + br.end + "                b' && self.maxReplica > 1" + q.close + "\n")
				got, err := check.Check(context.Background(), "f.yaml", data)
				if err != nil || len(got) != 1 {
					t.Fatalf("Check(%s %s) = %v, %v, want one diagnostic", br.name, q.name, got, err)
				}
				d := got[0]
				if d.Start.Line != 21 || d.End.Line != 21 || d.End.Column-d.Start.Column != len("maxReplica") {
					t.Errorf("Check(%s %s) range = %v-%v, want maxReplica on line 21", br.name, q.name, d.Start, d.End)
				}
			})
		}
	}
}

// utf16le returns a UTF-16LE stream with a byte order mark, holding the code
// units, characters or raw 16-bit values.
func utf16le(units ...uint16) string {
	return utf16stream([]byte{0xff, 0xfe}, binary.LittleEndian, units)
}

// utf16be is utf16le for UTF-16BE.
func utf16be(units ...uint16) string {
	return utf16stream([]byte{0xfe, 0xff}, binary.BigEndian, units)
}

func utf16stream(bom []byte, order binary.AppendByteOrder, units []uint16) string {
	b := bom
	for _, u := range units {
		b = order.AppendUint16(b, u)
	}
	return string(b)
}

// units returns the UTF-16 code units of text followed by the given ones.
func units(text string, more ...uint16) []uint16 {
	return append(utf16.Encode([]rune(text)), more...)
}

func TestCheckTypoBesideBrokenCharacter(t *testing.T) {
	t.Parallel()
	first := "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: example.com\n  scope: Namespaced\n  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}\n  versions:\n    - name: v1\n      served: true\n      storage: true\n      schema:\n        openAPIV3Schema:\n          type: object\n          properties:\n            replicas: {type: integer}\n          x-kubernetes-validations:\n            - rule: self.replicas > 0 && self.maxReplica > 1\n"
	enc := map[string]func(...uint16) string{"LE": utf16le, "BE": utf16be}
	// The typo is on line 19, column 47, wherever the broken character is.
	want := diag{19, 47, 57, ""}
	cases := []struct {
		name string
		src  string
		bad  bool
	}{
		{"UTF-8, fixed", first + "---\na: x\n", false},
		{"UTF-8, bad byte", first + "---\na: \xff\n", true},
		{"UTF-16LE, fixed", enc["LE"](units(first + "---\na: x\n")...), false},
		{"UTF-16LE, lone surrogate", enc["LE"](units(first+"---\na: ", 0xdc00, '\n')...), true},
		{"UTF-16BE, fixed", enc["BE"](units(first + "---\na: x\n")...), false},
		{"UTF-16BE, lone surrogate", enc["BE"](units(first+"---\na: ", 0xdc00, '\n')...), true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "t.yaml", []byte(tt.src))
			if (err != nil) != tt.bad {
				t.Fatalf("Check(%s) error = %v, want an error only for a broken character", tt.name, err)
			}
			var typo, broken int
			for _, d := range got {
				switch {
				case strings.Contains(d.Message, "maxReplica"):
					typo++
					if d.Start.Line != want.line || d.Start.Column != want.start || d.End.Column != want.end {
						t.Errorf("Check(%s) typo at %d:%d-%d, want %d:%d-%d", tt.name, d.Start.Line, d.Start.Column, d.End.Column, want.line, want.start, want.end)
					}
				case strings.Contains(d.Message, "YAML is broken"):
					broken++
					if d.Start.Line != 21 {
						t.Errorf("Check(%s) broken character on line %d, want 21", tt.name, d.Start.Line)
					}
				}
			}
			wantBroken := 0
			if tt.bad {
				wantBroken = 1
			}
			if typo != 1 || broken != wantBroken {
				t.Errorf("Check(%s) = %d typo and %d broken diagnostics, want 1 and %d: %v", tt.name, typo, broken, wantBroken, got)
			}
		})
	}
}

func TestCheckUnicodeLineEnds(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
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
          properties:
            spec:
              type: object
              properties:
                replicas: {type: integer}
                ports:
                  type: array
                  items: {type: string}
                  x-kubernetes-validations:
                    - rule: self.all(a, self.all(b, a == b || a != b))
              x-kubernetes-validations:
                - rule: self.replicas > 0 && self.maxReplica > 1
                  messageExpression: "'replicas is ' + string(self.replicas)"
                - rule: self.replicas > 0
                  messageExpression: "self.ports.all(a, self.ports.all(b, a != b)) ? 'x' : 'y'"
`
	for _, tt := range []struct{ name, head string }{
		{"one comment", "# a comment\n"},
		{"separators", "# a comment\n---\n# two\n---\n"},
	} {
		want, err := check.Check(context.Background(), "t.yaml", []byte(tt.head+crd))
		if err != nil || len(want) != 3 {
			t.Fatalf("Check(%s, plain) = %v, %v, want 3 diagnostics", tt.name, want, err)
		}
		if last := want[2]; !strings.Contains(last.Message, "estimated cost") || last.End.Line != last.Start.Line {
			t.Fatalf("Check(%s, plain)[2] = %v-%v %q, want a cost problem on one line", tt.name, last.Start, last.End, last.Message)
		}
		for _, br := range []struct{ name, text string }{{"NEL", "\u0085"}, {"LS", "\u2028"}, {"PS", "\u2029"}} {
			t.Run(tt.name+" "+br.name, func(t *testing.T) {
				t.Parallel()
				head := strings.ReplaceAll(tt.head, "\n", br.text)
				got, err := check.Check(context.Background(), "t.yaml", []byte(head+crd))
				if err != nil {
					t.Fatalf("Check(%s %s) error = %v", tt.name, br.name, err)
				}
				// Each line end of head counts as one line, as for plain line feeds.
				if len(got) != len(want) {
					t.Fatalf("Check(%s %s) = %d diagnostics %v, want %d", tt.name, br.name, len(got), got, len(want))
				}
				for i, d := range got {
					if d.Start != want[i].Start || d.End != want[i].End || d.Message != want[i].Message {
						t.Errorf("Check(%s %s)[%d] = %v-%v %q, want %v-%v %q", tt.name, br.name, i, d.Start, d.End, d.Message, want[i].Start, want[i].End, want[i].Message)
					}
				}
			})
		}
	}
}
