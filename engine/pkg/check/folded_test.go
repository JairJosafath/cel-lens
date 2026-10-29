package check_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// where is a place in a file, counting from 1; a range is two of them.
type where struct{ line, col int }

func (w where) String() string { return fmt.Sprintf("%d:%d", w.line, w.col) }

// find returns the range of the only occurrence of word in doc, the way an
// editor shows it: the line and the column in characters.
func find(t *testing.T, doc, word string) (start, end where) {
	t.Helper()
	if strings.Count(doc, word) != 1 {
		t.Fatalf("%q occurs %d times in the document, want once", word, strings.Count(doc, word))
	}
	before, _, _ := strings.Cut(doc, word)
	line := strings.Count(before, "\n")
	col := len([]rune(before[strings.LastIndex(before, "\n")+1:]))
	return where{line + 1, col + 1}, where{line + 1, col + 1 + len([]rune(word))}
}

// foldedKind is a CEL field with a document that has one such field, holding
// the YAML scalar given after its key.
type foldedKind struct {
	name string
	doc  func(scalar string) string
	// filler is an expression for the lines before the typo and join the
	// operator between two of them.
	filler, join string
}

const foldedCRD = `apiVersion: apiextensions.k8s.io/v1
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
            size: {type: integer}
          x-kubernetes-validations:
            - message: m
              `

func foldedPolicy(kind, field, scalar string) string {
	return `apiVersion: admissionregistration.k8s.io/v1
kind: ` + kind + `
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  validations:
    - ` + field + `: ` + scalar + `
`
}

var foldedKinds = []foldedKind{
	{"CRD rule", func(s string) string { return foldedCRD + "rule: " + s + "\n" }, "true", "&&"},
	{"CRD messageExpression", func(s string) string {
		return foldedCRD + "rule: self.size > 0\n              messageExpression: " + s + "\n"
	}, "string(1)", "+"},
	{"VAP expression", func(s string) string { return foldedPolicy("ValidatingAdmissionPolicy", "expression", s) }, "true", "&&"},
	{"VAP messageExpression", func(s string) string {
		return strings.Replace(foldedPolicy("ValidatingAdmissionPolicy", "messageExpression", s), "- messageExpression", "- expression: 'true'\n      messageExpression", 1)
	}, "string(1)", "+"},
	{"MAP expression", func(s string) string {
		return `apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  mutations:
    - patchType: JSONPatch
      jsonPatch:
        expression: ` + s + `
`
	}, "true", "&&"},
	{"Kyverno expression", func(s string) string {
		return strings.Replace(foldedPolicy("ValidatingPolicy", "expression", s), "admissionregistration.k8s.io/v1", "policies.kyverno.io/v1", 1)
	}, "true", "&&"},
}

// diagnose checks doc and returns the range of its only diagnostic.
func diagnose(t *testing.T, doc string) (start, end where) {
	t.Helper()
	got, err := check.Check(context.Background(), "f.yaml", []byte(doc), allOn())
	if err != nil || len(got) != 1 {
		t.Fatalf("Check(%q) = %v, %v, want one diagnostic", doc, got, err)
	}
	d := got[0]
	return where{d.Start.Line, d.Start.Column}, where{d.End.Line, d.End.Column}
}

// scalar writes the lines of an expression in the YAML style: the first goes
// after the key, the others are indented. A quoted scalar is closed on the
// last line.
func scalar(style string, lines []string) string {
	body := strings.Join(lines, "\n                    ")
	switch style {
	case "double":
		return `"` + body + `"`
	case "single":
		return `'` + body + `'`
	}
	return body
}

// TestFoldedScalarPositions checks that a problem in a rule over many lines is
// underlined on the word cel-go names, in every scalar style and field.
func TestFoldedScalarPositions(t *testing.T) {
	t.Parallel()
	for _, k := range foldedKinds {
		for _, style := range []string{"plain", "double", "single"} {
			for _, n := range []int{2, 3, 200} {
				for _, at := range []string{"first", "middle", "last"} {
					if n == 2 && at == "middle" {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/%d lines/typo on %s", k.name, style, n, at), func(t *testing.T) {
						t.Parallel()
						typo := map[string]int{"first": 0, "middle": n / 2, "last": n - 1}[at]
						lines := make([]string, n)
						for i := range lines {
							lines[i] = k.filler
							if i < n-1 {
								lines[i] += " " + k.join
							}
						}
						lines[typo] = strings.Replace(lines[typo], k.filler, "maxSiz", 1)
						doc := k.doc(scalar(style, lines))
						wantStart, wantEnd := find(t, doc, "maxSiz")
						gotStart, gotEnd := diagnose(t, doc)
						if gotStart != wantStart || gotEnd != wantEnd {
							t.Errorf("Check(%d-line %s rule) range = %v-%v, want %v-%v", n, style, gotStart, gotEnd, wantStart, wantEnd)
						}
					})
				}
			}
		}
	}
}

// TestFoldedScalarExample checks the rule that the issue gives, in a CRD.
func TestFoldedScalarExample(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, rule string }{
		{"plain", "self.size != 4 &&\n                    self.size <= self.maxSiz"},
		{"double", "\"self.size != 4 &&\n                    self.size <= self.maxSiz\""},
		{"single", "'self.size != 4 &&\n                    self.size <= self.maxSiz'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := foldedCRD + "rule: " + tt.rule + "\n"
			wantStart, wantEnd := find(t, doc, "maxSiz")
			gotStart, gotEnd := diagnose(t, doc)
			if gotStart != wantStart || gotEnd != wantEnd {
				t.Errorf("Check(%s example) range = %v-%v, want %v-%v", tt.name, gotStart, gotEnd, wantStart, wantEnd)
			}
		})
	}
}

// TestFoldedScalarEscapes checks that escapes, line joins, doubled quotes and
// empty lines before the typo do not move the underline.
func TestFoldedScalarEscapes(t *testing.T) {
	t.Parallel()
	const in = "\n                    "
	tests := []struct{ name, scalar string }{
		{"escapes on the same line", `"\"a\\b\té\" != \"\" && maxSiz == 1"`},
		{"quote and backslash escapes on an earlier line", `"\"a\\b\" != \"\" &&` + in + `maxSiz == 1"`},
		{"tab and unicode escapes on an earlier line", `"\"\té\x41\U0001F600\" != \"\" &&` + in + `true && maxSiz == 1"`},
		{"unicode escape on the same line", `"\"\u00e9\" != \"\" && maxSiz == 1"`},
		{"unicode escape on an earlier line", `"\"\u00e9\" != \"\" &&` + in + `maxSiz == 1"`},
		{"line-end join", `"true && \` + in + `maxSiz == 1"`},
		{"line-end join after an escape", `"\"\\\\\" != \"\" && \` + in + `maxSiz == 1"`},
		{"doubled single quote on the same line", `'''a'' != '''' && maxSiz == 1'`},
		{"doubled single quote on an earlier line", `'''a'' != '''' &&` + in + `maxSiz == 1'`},
		{"empty line in a plain rule", `true &&` + "\n" + in + `maxSiz == 1`},
		{"empty line in a double-quoted rule", `"true &&` + "\n" + in + `maxSiz == 1"`},
		{"two empty lines in a plain rule", `true &&` + "\n\n" + in + `maxSiz == 1`},
		{"empty line after a line-end join", `"true && \` + "\n" + in + `maxSiz == 1"`},
		{"trailing spaces before the break", `"true &&   ` + in + `maxSiz == 1"`},
		{"comment after a plain rule", `true && maxSiz == 1 # note`},
	}
	for _, k := range foldedKinds {
		for _, tt := range tests {
			t.Run(k.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				doc := k.doc(tt.scalar)
				wantStart, wantEnd := find(t, doc, "maxSiz")
				gotStart, gotEnd := diagnose(t, doc)
				if gotStart != wantStart || gotEnd != wantEnd {
					t.Errorf("Check(%s) range = %v-%v, want %v-%v", tt.name, gotStart, gotEnd, wantStart, wantEnd)
				}
			})
		}
	}
}

// TestFoldedScalarWholeRule checks that a problem about the whole of a rule
// over several lines starts on the rule's first character and ends after its
// last.
func TestFoldedScalarWholeRule(t *testing.T) {
	t.Parallel()
	doc := strings.Replace(foldedCRD, "size: {type: integer}", "entries: {type: array, items: {type: string}}", 1)
	tests := []struct{ name, scalar string }{
		{"plain", "self.entries.all(a,\n                    self.entries.all(b,\n                    self.entries.all(c, a != b || b != c)))"},
		{"double", "\"self.entries.all(a, \\\n                    self.entries.all(b,\n                    self.entries.all(c, a != b || b != c)))\""},
		{"single", "'self.entries.all(a,\n                    self.entries.all(b,\n                    self.entries.all(c, a != b || b != c)))'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := doc + "rule: " + tt.scalar + "\n"
			got, err := check.Check(context.Background(), "f.yaml", []byte(d), allOn())
			if err != nil || len(got) != 1 || !strings.Contains(got[0].Message, "cost") {
				t.Fatalf("Check(%s) = %v, %v, want one cost problem", tt.name, got, err)
			}
			first := strings.Index(d, "self.entries.all(a")
			startLine := strings.Count(d[:first], "\n") + 1
			startCol := len([]rune(d[strings.LastIndex(d[:first], "\n")+1:first])) + 1
			endLine := strings.Count(d, "\n")
			lastLine := strings.Split(d, "\n")[endLine-1]
			endCol := len([]rune(strings.TrimSuffix(strings.TrimSuffix(lastLine, "\""), "'"))) + 1
			gotStart, gotEnd := where{got[0].Start.Line, got[0].Start.Column}, where{got[0].End.Line, got[0].End.Column}
			if wantStart, wantEnd := (where{startLine, startCol}), (where{endLine, endCol}); gotStart != wantStart || gotEnd != wantEnd {
				t.Errorf("Check(%s) range = %v-%v, want %v-%v", tt.name, gotStart, gotEnd, wantStart, wantEnd)
			}
		})
	}
}

// TestFoldedScalarIncomplete checks that a syntax error at the end of an
// incomplete rule over several lines is one character just after the rule's
// last character, as for the same rule on one line.
func TestFoldedScalarIncomplete(t *testing.T) {
	t.Parallel()
	const in = "                    "
	for _, style := range []string{"plain", "double", "single"} {
		t.Run(style, func(t *testing.T) {
			t.Parallel()
			doc := foldedCRD + "rule: " + scalar(style, []string{"self.size > 0 &&", "self.size >"}) + "\n"
			_, last := find(t, doc, in+"self.size >")
			gotStart, gotEnd := diagnose(t, doc)
			if want := (where{last.line, last.col}); gotStart != want || gotEnd != (where{want.line, want.col + 1}) {
				t.Errorf("Check(%s incomplete) range = %v-%v, want %v-%v", style, gotStart, gotEnd, want, where{want.line, want.col + 1})
			}
		})
	}
}
