package check

import (
	"fmt"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// TestLocateNegativeColumn checks, for #27, that a place before the start of
// a line, which a compiler that reports column 0 gives as -1, is the start of
// the line and no crash: locate sliced the line at a negative offset.
func TestLocateNegativeColumn(t *testing.T) {
	t.Parallel()
	src := "x: yes\n"
	e := extract.Expression{Text: "yes", Start: extract.Position{Line: 1, Column: 4}, End: extract.Position{Line: 1, Column: 7}}
	chars := make([]extract.Range, 3)
	for i := range chars {
		chars[i] = extract.Range{Start: extract.Position{Line: 1, Column: 4 + i}, End: extract.Position{Line: 1, Column: 5 + i}}
	}
	withChars := e
	withChars.Chars = chars
	for name, tt := range map[string]extract.Expression{"plain": e, "with a character map": withChars} {
		for _, col := range []int{-1, -5, 0} {
			t.Run(fmt.Sprintf("%s, column %d", name, col), func(t *testing.T) {
				t.Parallel()
				start, _, ok := locate(nil, extract.SplitLines([]byte(src)), tt, 1, col)
				if ok && start != (extract.Position{Line: 1, Column: 4}) {
					t.Errorf("locate(col %d) = %v, want the start of the expression, 1:4", col, start)
				}
			})
		}
	}
}
