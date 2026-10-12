package lsp

import (
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

func TestLimitFixes(t *testing.T) {
	t.Parallel()
	lines := splitLines([]byte("a: 1\n"))
	placed := check.Fix{FieldPath: "a", Limit: "maxLength", Value: 5, Edits: []check.Edit{{At: extract.Position{Line: 1, Column: 1}, Text: "maxLength: 5\n"}}}
	unplaced := check.Fix{FieldPath: "b", Limit: "maxItems", Value: 5}
	tests := []struct {
		name     string
		fixes    []check.Fix
		together bool
		want     int
	}{
		{"alone, one without edits", []check.Fix{placed, unplaced}, false, 1},
		{"together, one without edits", []check.Fix{placed, unplaced}, true, 0},
		{"together, all placed", []check.Fix{placed, placed}, true, 1},
		{"together, a single fix", []check.Fix{placed}, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := limitFixes(lines, check.Diagnostic{Fixes: tt.fixes, FixesTogether: tt.together})
			if len(got) != tt.want {
				t.Errorf("limitFixes(%s) = %d fixes, want %d", tt.name, len(got), tt.want)
			}
		})
	}
}
