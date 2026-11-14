package inline_test

import (
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/inline"
)

// TestExamplesState checks the state and the words of a rule's examples in
// the list of rules (cel-lens-core#37), for each kind of part the line has:
// wrong at the rule or on an example, an error and no wrong, all passing
// (refusals expected, a policy's warnings, audits, exclusions and
// annotations included), not run on any, with the binding; the words are
// exactly the line's part before the line shortens it; no example has none.
func TestExamplesState(t *testing.T) {
	t.Parallel()
	policy := func(v inline.Verdict, line int, binding string, wrong bool) inline.Example {
		return inline.Example{URI: "file:///ws/ex.yaml", File: "ex.yaml", Line: line, Verdict: v, Binding: binding, Wrong: wrong}
	}
	tests := []struct {
		name         string
		ex           []inline.Example
		state, words string
	}{
		{"all pass", []inline.Example{pass, pass, refusedOK}, inline.ExamplesPass, "3 examples pass"},
		{"one passes", []inline.Example{pass}, inline.ExamplesPass, "1 example passes"},
		{"refuses wrongly, at the rule", []inline.Example{refusesBad, pass, pass}, inline.ExamplesWrong, "refuses 1 example it should allow · 2 pass"},
		{"allows wrongly, on the example", []inline.Example{allowsBad}, inline.ExamplesWrong, "allows 1 example it should refuse"},
		{"refuses with another message", []inline.Example{otherMsg, pass}, inline.ExamplesWrong, "refuses 1 example with another message · 1 passes"},
		{"wrong and an error", []inline.Example{failed, refusesBad}, inline.ExamplesWrong, "refuses 1 example it should allow · 1 error"},
		{"an error and none wrong", []inline.Example{failed, pass}, inline.ExamplesError, "1 example errors · 1 passes"},
		{"not run on any, one reason", []inline.Example{notUpdate, notUpdate}, inline.ExamplesNotRun, "not run: needs an update example"},
		{"not run on any, several reasons", []inline.Example{crdRefused, absent}, inline.ExamplesNotRun, "not run on 2 examples"},
		{"not run on some", []inline.Example{absent, pass}, inline.ExamplesPass, "1 example passes"},
		{"a policy warns and audits under a binding", []inline.Example{
			policy(inline.Warned, 1, "warn", false), policy(inline.Audited, 8, "warn", false), policy(inline.Allowed, 15, "warn", false),
		}, inline.ExamplesPass, "warns 1 example · audits 1 · 1 passes · under binding warn"},
		{"a policy's match condition excludes", []inline.Example{policy(inline.Excluded, 1, inline.Unbound, false)}, inline.ExamplesPass, "excludes 1 example · under no binding"},
		{"a policy's annotation", []inline.Example{policy(inline.Annotated, 1, "a", false), policy(inline.Annotated, 1, "b", false)}, inline.ExamplesPass, "annotates 1 example · under 2 bindings"},
		{"a policy refuses wrongly", []inline.Example{policy(inline.Refused, 1, "deny", true)}, inline.ExamplesWrong, "refuses 1 example it should allow · under binding deny"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state, words, ok := inline.ExamplesState(tt.ex)
			if !ok || state != tt.state || words != tt.words {
				t.Errorf("ExamplesState() = %q, %q, %t; want %q, %q", state, words, ok, tt.state, tt.words)
			}
			// The words are the line's own part, which fits this short line whole.
			parts := inline.Line(inline.Rule{Subject: "s", SubjectType: "W", Examples: tt.ex})
			if last := parts[len(parts)-1]; last.Command != inline.ShowExampleResults || last.Title != words {
				t.Errorf("Line() part about the examples = %q, want the words %q", last.Title, words)
			}
		})
	}
	if state, words, ok := inline.ExamplesState(nil); ok || state != "" || words != "" {
		t.Errorf("ExamplesState(nil) = %q, %q, %t; want nothing", state, words, ok)
	}
}
