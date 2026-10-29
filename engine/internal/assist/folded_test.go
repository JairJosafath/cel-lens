package assist_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

const foldedHead = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {kind: Widget, plural: widgets}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            size: {type: integer}
            name: {type: string}
            q"x: {type: string}
            it's: {type: string}
          x-kubernetes-validations:
            - message: m
              rule: `

// foldedRule writes the lines of a rule as a plain, "double-quoted" or
// 'single-quoted' scalar of a CRD, one source line each, and returns the file.
func foldedRule(style string, lines []string) string {
	var b strings.Builder
	b.WriteString(foldedHead)
	open, closing := "", ""
	switch style {
	case "double":
		open, closing = `"`, `"`
	case "single":
		open, closing = "'", "'"
	}
	for i, l := range lines {
		if i == 0 {
			b.WriteString(open + l)
		} else {
			b.WriteString("\n                " + l)
		}
	}
	return b.String() + closing + "\n"
}

// ruleLines returns n lines of a rule, line k (from 0) being cursor when
// k == at, a comparison of self.size otherwise.
func ruleLines(n, at int, cursor string) []string {
	lines := make([]string, n)
	for k := range lines {
		lines[k] = "self.size >= 0"
		if k == at {
			lines[k] = cursor
		}
		if k < n-1 {
			lines[k] += " &&"
		}
	}
	return lines
}

// cursorAt finds the line holding the needle, which occurs once on it, and
// returns the position offset runes into it.
func cursorAt(t *testing.T, doc, needle string, line, offset int) extract.Position {
	t.Helper()
	var hits []int
	for i, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, needle) {
			hits = append(hits, i)
		}
	}
	if line >= len(hits) {
		t.Fatalf("%q occurs on %d lines, want at least %d", needle, len(hits), line+1)
	}
	l := strings.Split(doc, "\n")[hits[line]]
	before, _, _ := strings.Cut(l, needle)
	return extract.Position{Line: hits[line] + 1, Column: len([]rune(before)) + offset + 1}
}

// cursorOn returns the position offset runes into needle on line k (from 0)
// of the rule, which starts on the line with "rule: ".
func cursorOn(t *testing.T, doc, needle string, k, offset int) extract.Position {
	t.Helper()
	lines := strings.Split(doc, "\n")
	for i, l := range lines {
		if strings.Contains(l, "rule: ") {
			l = lines[i+k]
			j := strings.Index(l, needle)
			if j < 0 {
				t.Fatalf("line %d of the rule is %q, want it to hold %q", k, l, needle)
			}
			return extract.Position{Line: i + k + 1, Column: len([]rune(l[:j])) + offset + 1}
		}
	}
	t.Fatal("no rule in the file")
	return extract.Position{}
}

func foldedAssistant(t *testing.T) *assist.Assistant {
	t.Helper()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestFoldedHover(t *testing.T) {
	t.Parallel()
	a := foldedAssistant(t)
	one := assist.Parse(context.Background(), "w.yaml", []byte(foldedRule("plain", []string{strings.Join(ruleLines(1, 0, "self.size >= 0"), " ")})))
	want, ok := a.Hover(one, cursorAt(t, foldedRule("plain", ruleLines(1, 0, "self.size >= 0")), "self.size", 0, 6))
	if !ok || !strings.Contains(want.Markdown, "(field) size: int") {
		t.Fatalf("Hover on one line = %q, %v, want the field size", want.Markdown, ok)
	}
	for _, style := range []string{"plain", "double", "single"} {
		for _, n := range []int{2, 3, 200} {
			for name, k := range map[string]int{"first": 0, "middle": n / 2, "last": n - 1} {
				t.Run(fmt.Sprintf("%s over %d lines, %s", style, n, name), func(t *testing.T) {
					t.Parallel()
					doc := foldedRule(style, ruleLines(n, -1, ""))
					pos := cursorAt(t, doc, "self.size", k, 6)
					got, ok := a.Hover(assist.Parse(context.Background(), "w.yaml", []byte(doc)), pos)
					if !ok || got.Markdown != want.Markdown {
						t.Fatalf("Hover(%v) = %q, %v, want %q", pos, got.Markdown, ok, want.Markdown)
					}
					if got.Start.Line != pos.Line || got.End.Line != pos.Line || got.Start.Column != pos.Column-1 || got.End.Column != pos.Column+3 {
						t.Errorf("Hover(%v) range = %v to %v, want size on line %d", pos, got.Start, got.End, pos.Line)
					}
				})
			}
		}
	}
}

func TestFoldedComplete(t *testing.T) {
	t.Parallel()
	a := foldedAssistant(t)
	labelsOf := func(items []assist.Item) []string {
		var out []string
		for _, it := range items {
			if it.Kind == assist.KindField {
				out = append(out, it.Label)
			}
		}
		return out
	}
	oneDoc := foldedRule("plain", []string{"self. && self.size >= 0"})
	items, ok := a.Complete(assist.Parse(context.Background(), "w.yaml", []byte(oneDoc)), cursorAt(t, oneDoc, "self.", 0, 5))
	want := labelsOf(items)
	if !ok || !slices.Contains(want, "size") {
		t.Fatalf("Complete on one line = %v, %v, want the field size", want, ok)
	}
	for _, style := range []string{"plain", "double", "single"} {
		for _, n := range []int{2, 3, 200} {
			for name, k := range map[string]int{"first": 0, "middle": n / 2, "last": n - 1} {
				t.Run(fmt.Sprintf("%s over %d lines, %s", style, n, name), func(t *testing.T) {
					t.Parallel()
					doc := foldedRule(style, ruleLines(n, k, "self."))
					pos := cursorOn(t, doc, "self.", k, 5)
					items, ok := a.Complete(assist.Parse(context.Background(), "w.yaml", []byte(doc)), pos)
					if !ok || !slices.Equal(labelsOf(items), want) {
						t.Fatalf("Complete(%v) = %v, %v, want %v", pos, labelsOf(items), ok, want)
					}
					for _, it := range items {
						if it.Start != pos || it.End != pos {
							t.Fatalf("Complete(%v) item %s edits %v to %v, want the cursor", pos, it.Label, it.Start, it.End)
						}
					}
				})
			}
		}
	}
}

func TestFoldedCompleteReplacesTypedName(t *testing.T) {
	t.Parallel()
	a := foldedAssistant(t)
	doc := foldedRule("double", []string{"self.size >= 0 &&", "self.na"})
	pos := cursorAt(t, doc, "self.na", 0, 7)
	items, ok := a.Complete(assist.Parse(context.Background(), "w.yaml", []byte(doc)), pos)
	if !ok || len(items) == 0 {
		t.Fatalf("Complete(%v) = %v, %v, want items", pos, items, ok)
	}
	start := extract.Position{Line: pos.Line, Column: pos.Column - 2}
	if items[0].Start != start || items[0].End != pos {
		t.Errorf("Complete(%v) edits %v to %v, want %v to %v", pos, items[0].Start, items[0].End, start, pos)
	}
}

func TestFoldedEscapesBeforeCursor(t *testing.T) {
	t.Parallel()
	a := foldedAssistant(t)
	tests := []struct {
		name  string
		doc   string
		hover string // text on the cursor's line the hover is on
	}{
		{
			name:  "escaped quote, tab and line-end backslash",
			doc:   foldedRule("double", []string{`self.name != \"a\" ` + `&& self.name != \"b\"\t&& \`, `self.size >= 0 && self.`}),
			hover: "self.size",
		},
		{
			name:  "doubled quote",
			doc:   foldedRule("single", []string{`self.name != ''a'' &&`, `self.size >= 0 && self.`}),
			hover: "self.size",
		},
		{
			name:  "escape on the cursor's line",
			doc:   foldedRule("double", []string{`self.name != \"b\"\t&&`, `self.size >= 0 && self.`}),
			hover: "self.size",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := assist.Parse(context.Background(), "w.yaml", []byte(tt.doc))
			pos := cursorAt(t, tt.doc, tt.hover, 0, 6)
			h, ok := a.Hover(d, pos)
			if !ok || !strings.Contains(h.Markdown, "(field) size: int") || h.Start.Line != pos.Line {
				t.Errorf("Hover(%v) = %+v, %v, want the field size on line %d", pos, h, ok, pos.Line)
			}
			end := cursorAt(t, tt.doc, "&& self.", 0, len("&& self."))
			items, ok := a.Complete(d, end)
			var got []string
			for _, it := range items {
				got = append(got, it.Label)
			}
			if !ok || !slices.Contains(got, "size") || !slices.Contains(got, "name") {
				t.Errorf("Complete(%v) = %v, %v, want the fields size and name", end, got, ok)
			}
			if len(items) > 0 && (items[0].Start != end || items[0].End != end) {
				t.Errorf("Complete(%v) edits %v to %v, want the cursor", end, items[0].Start, items[0].End)
			}
		})
	}
}

func TestFoldedCompleteEscapesQuotes(t *testing.T) {
	t.Parallel()
	a := foldedAssistant(t)
	tests := []struct {
		name, style, label, want string
	}{
		{name: "double quote in double quotes", style: "double", label: `q"x`, want: `q\"x`},
		{name: "single quote in double quotes", style: "double", label: "it's", want: "it's"},
		{name: "single quote in single quotes", style: "single", label: "it's", want: "it''s"},
		{name: "double quote in single quotes", style: "single", label: `q"x`, want: `q"x`},
		{name: "double quote in a plain scalar", style: "plain", label: `q"x`, want: `q"x`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := foldedRule(tt.style, []string{"self.size >= 0 &&", "self. && true"})
			pos := cursorAt(t, doc, "self. ", 0, 5)
			items, ok := a.Complete(assist.Parse(context.Background(), "w.yaml", []byte(doc)), pos)
			if !ok {
				t.Fatalf("Complete(%v) = none", pos)
			}
			for _, it := range items {
				if it.Label == tt.label {
					if it.NewText != tt.want {
						t.Errorf("Complete(%v) item %s inserts %q, want %q", pos, it.Label, it.NewText, tt.want)
					}
					return
				}
			}
			t.Errorf("Complete(%v) has no item %s", pos, tt.label)
		})
	}
}

// TestFoldedPolicies checks hover and completion on the second line of a plain
// scalar rule of a ValidatingAdmissionPolicy and a Kyverno ValidatingPolicy.
func TestFoldedPolicies(t *testing.T) {
	t.Parallel()
	a := foldedAssistant(t)
	const folded = "object.spec.replicas >= 0 &&\n        object.spec.replicas >= 0"
	const typing = "object.spec.replicas >= 0 &&\n        object. && true"
	tests := []struct {
		name  string
		doc   func(rule, expression string) string
		field string
	}{
		{name: "ValidatingAdmissionPolicy", doc: matching},
		{name: "Kyverno ValidatingPolicy", doc: vpol},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			oneLine := tt.doc(deploymentRule, "object.spec.replicas >= 0 && object.spec.replicas >= 0")
			want, ok := a.Hover(assist.Parse(context.Background(), "p.yaml", []byte(oneLine)), cursorAt(t, oneLine, "object.spec.replicas", 0, 14))
			if !ok {
				t.Fatal("Hover on one line = none")
			}
			doc := tt.doc(deploymentRule, folded)
			pos := cursorAt(t, doc, "object.spec.replicas", 1, 14)
			got, ok := a.Hover(assist.Parse(context.Background(), "p.yaml", []byte(doc)), pos)
			if !ok || got.Markdown != want.Markdown || got.Start.Line != pos.Line {
				t.Errorf("Hover(%v) = %+v, %v, want %q on line %d", pos, got, ok, want.Markdown, pos.Line)
			}
			doc = tt.doc(deploymentRule, typing)
			pos = cursorAt(t, doc, "object. ", 0, 7)
			items, ok := a.Complete(assist.Parse(context.Background(), "p.yaml", []byte(doc)), pos)
			var labels []string
			for _, it := range items {
				labels = append(labels, it.Label)
			}
			if !ok || !slices.Contains(labels, "spec") || !slices.Contains(labels, "metadata") || items[0].Start != pos {
				t.Errorf("Complete(%v) = %v, %v, want the fields of object, edited at the cursor", pos, labels, ok)
			}
		})
	}
}
