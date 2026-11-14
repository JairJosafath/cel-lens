package inline_test

import (
	"reflect"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/inline"
)

// TestLineUnchecked checks the first part of the line above an expression
// the check did not compile (cel-lens-core#42): in place of whether it
// compiles, that it was not checked and why, in a few words, and its
// command shows the problem that stopped the check, not the expression.
func TestLineUnchecked(t *testing.T) {
	t.Parallel()
	at := map[string]any{"uri": "file:///crd.yaml", "range": "the stopping problem"}
	tests := []struct {
		name      string
		unchecked inline.Unchecked
		problems  int
		title     string
		argument  any
	}{
		{"compiled", inline.Checked, 0, "self: Widget.spec · compiles", "the rule"},
		{"compiled, with problems", inline.Checked, 2, "self: Widget.spec · 2 problems", "the rule"},
		{"schema error", inline.UncheckedSchema, 0, "self: Widget.spec · not checked: schema error", at},
		{"unreadable document", inline.UncheckedDocument, 0, "self: Widget.spec · not checked: unreadable document", at},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parts := inline.Line(inline.Rule{Subject: "self", SubjectType: "Widget.spec", Location: "the rule", Problems: tt.problems, Unchecked: tt.unchecked, UncheckedAt: at})
			first := parts[0]
			if first.Title != tt.title || first.Command != inline.ShowRule || !reflect.DeepEqual(first.Arguments, []any{tt.argument}) {
				t.Errorf("Line() first part = %q %s %v, want %q %s [%v]", first.Title, first.Command, first.Arguments, tt.title, inline.ShowRule, tt.argument)
			}
		})
	}
}
