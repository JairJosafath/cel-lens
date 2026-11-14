package check

import (
	"fmt"
	"testing"

	kfield "k8s.io/apimachinery/pkg/util/validation/field"

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

// TestErrorNodeSegment is #43: errorNode cuts a list-items error's path at
// its last ".items" path segment, not at the last ".items" in the text, which
// a property name such as "a.items" holds: the cut was then in the middle of
// the name and the rules of the items were over-marked as not compiled.
func TestErrorNodeSegment(t *testing.T) {
	t.Parallel()
	const detail = "must be atomic as item of a list with x-kubernetes-list-type=set"
	for _, tc := range []struct {
		name, field, detail, want string
	}{
		{"items of the list", "spec.versions[0].schema.openAPIV3Schema.properties[ports].items", detail, "spec.versions[0].schema.openAPIV3Schema.properties[ports]"},
		{"a property of the items", "spec.versions[0].schema.openAPIV3Schema.properties[ports].items.properties[key]", detail, "spec.versions[0].schema.openAPIV3Schema.properties[ports]"},
		{"a key that holds .items", "spec.versions[0].schema.openAPIV3Schema.properties[ports].items.properties[a.items]", detail, "spec.versions[0].schema.openAPIV3Schema.properties[ports]"},
		{"a key that starts with items", "spec.versions[0].schema.openAPIV3Schema.properties[ports].items.properties[x.itemsKey]", detail, "spec.versions[0].schema.openAPIV3Schema.properties[ports]"},
		{"a list that holds .items in its name", "spec.versions[0].schema.openAPIV3Schema.properties[a.items].items", detail, "spec.versions[0].schema.openAPIV3Schema.properties[a.items]"},
		{"a list named with an opening bracket", "spec.properties[x[y].items.properties[k]", detail, "spec.properties[x[y]"},
		{"a key with brackets and .items", "spec.properties[x].items.properties[a[0].items]", detail, "spec.properties[x]"},
		{"a nested list cuts at the innermost", "spec.properties[a].items.properties[b].items", detail, "spec.properties[a].items.properties[b]"},
		{"no items segment", "spec.versions[0].schema.openAPIV3Schema.properties[a.items]", detail, "spec.versions[0].schema.openAPIV3Schema.properties[a.items]"},
		{"another error stays", "spec.properties[a].items", "some other error", "spec.properties[a].items"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := errorNode(&kfield.Error{Field: tc.field, Detail: tc.detail})
			if got != tc.want {
				t.Errorf("errorNode(%q) = %q, want %q", tc.field, got, tc.want)
			}
		})
	}
}
