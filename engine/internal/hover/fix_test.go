package hover_test

import (
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/hover"
)

// TestFixBlock checks the Fix block (#56): each problem at the cursor with its
// message first for a client that does not show it, its fixes only for one
// that does; a fix as a link to cel-lens.applyFix when the client runs it,
// else in words; the message escaped, and the tooltip's quotes too.
func TestFixBlock(t *testing.T) {
	t.Parallel()
	name := hover.Fix{Words: "Change to `replicas`", Title: "Change to `replicas`"}
	nameLinked := name
	nameLinked.Argument = "%5B%7B%7D%5D"
	limit := hover.Fix{Words: "Add `maxItems: 1000` to ``spec.a`b``", Title: "Add `maxItems: 1000` to \"spec.a`b\"", Argument: "%5B%5D"}
	problems := func(fixes ...hover.Fix) []hover.Problem {
		return []hover.Problem{
			{Message: "undefined field 'replica' <img>", Fixes: fixes},
			{Message: "a problem with no fix"},
		}
	}
	tests := []struct {
		name     string
		problems []hover.Problem
		messages bool
		want     string
	}{
		{
			"messages, in words", problems(name), true,
			"undefined field 'replica' \\<img\\>\n\nFix: Change to `replicas` (a quick fix)\n\na problem with no fix",
		},
		{
			"messages, linked", problems(nameLinked), true,
			"undefined field 'replica' \\<img\\>\n\nFix: [Change to `replicas`](command:cel-lens.applyFix?%5B%7B%7D%5D \"Change to `replicas`\")\n\na problem with no fix",
		},
		{
			"fixes only", problems(nameLinked), false,
			"Fix: [Change to `replicas`](command:cel-lens.applyFix?%5B%7B%7D%5D \"Change to `replicas`\")",
		},
		{"fixes only, none", problems(), false, ""},
		{
			"a title with quotes",
			[]hover.Problem{{Fixes: []hover.Fix{limit}}},
			false,
			"Fix: [Add `maxItems: 1000` to ``spec.a`b``](command:cel-lens.applyFix?%5B%5D \"Add `maxItems: 1000` to \\\"spec.a`b\\\"\")",
		},
		{"nothing", nil, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hover.FixBlock(tt.problems, tt.messages); got != tt.want {
				t.Errorf("FixBlock() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
