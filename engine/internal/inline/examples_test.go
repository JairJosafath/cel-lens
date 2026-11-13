package inline_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/inline"
)

// ex returns n examples of what a rule does, each like e, numbered.
func ex(n int, e inline.Example) []inline.Example {
	out := make([]inline.Example, n)
	for i := range out {
		out[i] = e
		out[i].File = "samples/w" + strconv.Itoa(len(out)*100+i) + ".yaml"
		out[i].URI = "file:///ws/" + out[i].File
		out[i].Line = 1
	}
	return out
}

// The kinds of results, as Run gives them.
var (
	pass       = inline.Example{Verdict: inline.Allowed}
	refusedOK  = inline.Example{Verdict: inline.Refused, Message: "spec.size: Invalid value: at most 10"}
	refusesBad = inline.Example{Verdict: inline.Refused, Wrong: true, Message: "spec.size: Invalid value: at most 10"}
	allowsBad  = inline.Example{Verdict: inline.Allowed, Wrong: true, OnExample: true}
	otherMsg   = inline.Example{Verdict: inline.Refused, Wrong: true, OnExample: true, Want: "at most 5", Message: "spec.size: Invalid value: at most 10"}
	failed     = inline.Example{Verdict: inline.Failed, Wrong: true, Message: "division by zero evaluating rule: 10 / self.size > 1"}
	notUpdate  = inline.Example{Verdict: inline.NotRun, Skip: inline.SkipNotUpdate, Message: "the example is not an update and the rule needs oldSelf"}
	crdRefused = inline.Example{Verdict: inline.NotRun, Skip: inline.SkipCRD, Message: "the API server refuses the CustomResourceDefinition for this rule"}
	absent     = inline.Example{Verdict: inline.NotRun, Skip: inline.SkipAbsent, Message: "the rule's field is absent from the example"}
)

// join is the examples of each list, one list.
func join(lists ...[]inline.Example) []inline.Example {
	var out []inline.Example
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// TestLineExamples checks the wording of the part of the line above a
// CRD's rule that says what the rule does to the examples (#15): quiet when
// all is as expected, the wrong verdicts first, an expected refusal
// passing, nothing for no example, why for a rule run on none, and the part
// shortened first when the line is long.
func TestLineExamples(t *testing.T) {
	t.Parallel()
	const head = "self: Widget.spec · compiles"
	rule := func(examples []inline.Example) inline.Rule {
		return inline.Rule{Subject: "self", SubjectType: "Widget.spec", Examples: examples}
	}
	// short is rule with a one-letter kind, which leaves the part room.
	short := func(examples []inline.Example) inline.Rule {
		return inline.Rule{Subject: "self", SubjectType: "W", Examples: examples}
	}
	tests := []struct {
		name string
		rule inline.Rule
		want []string // the titles after the first part; none for no part
	}{
		{"no example", rule(nil), nil},
		{"every one passes", rule(ex(3, pass)), []string{"3 examples pass"}},
		{"one passes", rule(ex(1, pass)), []string{"1 example passes"}},
		{"an expected refusal passes", rule(join(ex(2, pass), ex(1, refusedOK))), []string{"3 examples pass"}},
		{"refuses one it should allow", rule(join(ex(1, refusesBad), ex(2, pass))), []string{"refuses 1 example it should allow · 2 pass"}},
		{"refuses two it should allow, one passes", rule(join(ex(2, refusesBad), ex(1, pass))), []string{"refuses 2 examples it should allow · 1 passes"}},
		{"allows one it should refuse", rule(ex(1, allowsBad)), []string{"allows 1 example it should refuse"}},
		{"refuses with another message", rule(join(ex(1, otherMsg), ex(1, pass))), []string{"refuses 1 example with another message · 1 passes"}},
		// The hover's section has these in words (TestExamplesHover).
		{"both kinds of wrong, too long in words", short(join(ex(1, allowsBad), ex(2, refusesBad))), []string{"3 wrong"}},
		{"both kinds of wrong and a pass, too long", rule(join(ex(1, pass), ex(1, allowsBad), ex(2, refusesBad))), []string{"3 wrong · 1 passes"}},
		{"one errors", rule(ex(1, failed)), []string{"1 example errors"}},
		{"two error", rule(ex(2, failed)), []string{"2 examples error"}},
		{"wrong, then errors, then passes", short(join(ex(1, failed), ex(3, pass), ex(1, refusesBad))), []string{"refuses 1 example it should allow · 1 error · 3 pass"}},
		{"not run on some is left out", rule(join(ex(2, pass), ex(3, notUpdate))), []string{"2 examples pass"}},
		{"not run on any: needs an update", rule(ex(3, notUpdate)), []string{"not run: needs an update example"}},
		{"not run on any: CRD refused", rule(ex(2, crdRefused)), []string{"not run: CRD refused"}},
		{"not run on any: field absent", rule(ex(1, absent)), []string{"not run: field absent from the example"}},
		{"not run on any, for several reasons", rule(join(ex(1, absent), ex(2, notUpdate))), []string{"not run on 3 examples"}},
		{
			"after the values",
			inline.Rule{Subject: "self", SubjectType: "W", Values: []inline.Value{dynValue("self.a")}, Examples: ex(3, pass)},
			[]string{"self.a: dyn · Give it a type", "3 examples pass"},
		},
		{
			"shortened to the wrong as one count",
			inline.Rule{Subject: "self", SubjectType: "Widget.spec", Values: []inline.Value{dynValue("self.a")}, Examples: join(ex(1, refusesBad), ex(2, pass))},
			[]string{"self.a: dyn · Give it a type", "1 wrong · 2 pass"},
		},
		{
			"shortened to its first count",
			inline.Rule{Subject: "self", SubjectType: "Widget.spec", Values: []inline.Value{dynValue("self.spec.c")}, Examples: join(ex(1, refusesBad), ex(1, failed), ex(2, pass))},
			[]string{"self.spec.c: dyn · Give it a type", "1 wrong"},
		},
		{
			"shortened before a value is",
			inline.Rule{Subject: "self", SubjectType: "Widget.spec", Values: []inline.Value{dynValue("self.spec.config")}, Examples: ex(3, pass)},
			[]string{"self.spec.config: dyn · Give it a type", "3 pass"},
		},
		{
			"a value cut to make room",
			inline.Rule{Subject: "self", SubjectType: "Widget.spec", Values: []inline.Value{dynValue("self.spec.configure.name")}, Examples: join(ex(1, refusesBad), ex(2, pass))},
			[]string{"self.…name: dyn · Give it a type", "1 wrong"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := inline.Line(tc.rule)
			if w := inline.Width(got); w > inline.MaxWidth {
				t.Errorf("Width(Line(%s)) = %d, want at most %d", tc.name, w, inline.MaxWidth)
			}
			if got[0].Command != inline.ShowRule || !strings.HasPrefix(got[0].Title, "self: ") {
				t.Fatalf("Line(%s)[0] = %+v, want the subject", tc.name, got[0])
			}
			if tc.rule.Values == nil && tc.rule.SubjectType != "W" && got[0].Title != head {
				t.Errorf("Line(%s)[0] = %q, want %q", tc.name, got[0].Title, head)
			}
			var titles []string
			for _, p := range got[1:] {
				titles = append(titles, p.Title)
			}
			if !reflect.DeepEqual(titles, tc.want) {
				t.Errorf("Line(%s) titles after the first = %q, want %q", tc.name, titles, tc.want)
			}
		})
	}
}

// TestLineExamplesCommand checks that the part about examples runs
// cel-lens.showExampleResults with the rule's location, as the first part
// runs cel-lens.showRule (#15).
func TestLineExamplesCommand(t *testing.T) {
	t.Parallel()
	where := map[string]any{"uri": "file:///crd.yaml"}
	got := inline.Line(inline.Rule{Subject: "self", SubjectType: "Widget", Location: where, Examples: ex(2, pass)})
	last := got[len(got)-1]
	if inline.ShowExampleResults != "cel-lens.showExampleResults" || last.Command != inline.ShowExampleResults || !reflect.DeepEqual(last.Arguments, []any{where}) {
		t.Errorf("Line() part about examples = %+v, want cel-lens.showExampleResults with the rule's location", last)
	}
}

// TestLineExamplesWidth checks that no line with a part about examples
// passes 80 characters, whatever the lengths, and that the part is always
// there (#15).
func TestLineExamplesWidth(t *testing.T) {
	t.Parallel()
	kinds := [][]inline.Example{
		ex(3, pass), join(ex(999, refusesBad), ex(999, allowsBad), ex(999, failed), ex(999, pass)), ex(2, notUpdate), join(ex(1, absent), ex(1, crdRefused)),
	}
	for _, examples := range kinds {
		for n := range 5 {
			for length := 1; length <= 120; length += 7 {
				name := "self." + strings.Repeat("a", length)
				var values []inline.Value
				for i := range n {
					values = append(values, dynValue(name+strconv.Itoa(i)))
				}
				rule := inline.Rule{Subject: "object", SubjectType: strings.Repeat("K", length), Problems: length, Values: values, Examples: examples}
				parts := inline.Line(rule)
				if w := inline.Width(parts); w > inline.MaxWidth {
					t.Errorf("Width(Line(%d values, length %d)) = %d, want at most %d: %+v", n, length, w, inline.MaxWidth, titles(parts))
				}
				if last := parts[len(parts)-1]; last.Command != inline.ShowExampleResults {
					t.Errorf("Line(%d values, length %d) ends with %+v, want the part about examples", n, length, last)
				}
			}
		}
	}
}

// TestExamplesHover checks the section of a rule's hover about its
// examples (#15): the line's part in words, each example linked to its
// file and line with its verdict and the API server's message, the wrong
// verdicts first, at most 10, then "and N more".
func TestExamplesHover(t *testing.T) {
	t.Parallel()
	if got := inline.ExamplesHover(nil); got != "" {
		t.Errorf("ExamplesHover(nil) = %q, want none", got)
	}
	examples := []inline.Example{
		{File: "samples/ok.yaml", URI: "file:///ws/samples/ok.yaml", Line: 1, Verdict: inline.Allowed},
		{File: "samples/big.yaml", URI: "file:///ws/samples/big.yaml", Line: 3, Verdict: inline.Refused, Wrong: true, Update: true, Message: "spec.size: Invalid value: 20: at most *10*"},
		{File: "test/invalid/x.yaml", URI: "file:///ws/test/invalid/x.yaml", Line: 1, Verdict: inline.Refused, Message: "spec.size: Invalid value: 20: at most 10"},
		{File: "test/invalid/y.yaml", URI: "file:///ws/test/invalid/y.yaml", Line: 2, Verdict: inline.Allowed, Wrong: true, OnExample: true, Want: "at most 5"},
		{File: "samples/c.yaml", URI: "file:///ws/samples/c.yaml", Line: 1, Verdict: inline.NotRun, Skip: inline.SkipNotUpdate, Message: "the example is not an update and the rule needs oldSelf"},
		{File: "samples/d.yaml", URI: "file:///ws/samples/d.yaml", Line: 1, Verdict: inline.Failed, Message: "division by zero\nevaluating rule"},
	}
	want := "**Examples:** refuses 1 example it should allow · allows 1 it should refuse · 1 error · 2 pass\n\n" +
		"- [samples/big.yaml:3](<file:///ws/samples/big.yaml#L3>) **refused as an update**, expected to be allowed: spec.size: Invalid value: 20: at most \\*10\\*\n" +
		"- [test/invalid/y.yaml:2](<file:///ws/test/invalid/y.yaml#L2>) **allowed**, expected to be refused with \"at most 5\"\n" +
		"- [samples/d.yaml:1](<file:///ws/samples/d.yaml#L1>) **error**: division by zero evaluating rule\n" +
		"- [samples/ok.yaml:1](<file:///ws/samples/ok.yaml#L1>) allowed\n" +
		"- [test/invalid/x.yaml:1](<file:///ws/test/invalid/x.yaml#L1>) refused: spec.size: Invalid value: 20: at most 10\n" +
		"- [samples/c.yaml:1](<file:///ws/samples/c.yaml#L1>) not run: the example is not an update and the rule needs oldSelf\n"
	if got := inline.ExamplesHover(examples); got != want {
		t.Errorf("ExamplesHover() =\n%s\nwant\n%s", got, want)
	}
	many := join(ex(12, pass), ex(1, refusesBad))
	got := inline.ExamplesHover(many)
	if n := strings.Count(got, "\n- "); n != 10 {
		t.Errorf("ExamplesHover(13 examples) lists %d, want 10:\n%s", n, got)
	}
	if !strings.Contains(got, "\n- [samples/w100.yaml:1](<file:///ws/samples/w100.yaml#L1>) **refused**, expected to be allowed") || !strings.HasSuffix(got, "\n\nand 3 more\n") {
		t.Errorf("ExamplesHover(13 examples) =\n%s\nwant the wrong one first and and 3 more", got)
	}
}

// TestWarnings checks the words of the warnings a wrong verdict gives, on
// the rule and on the example (#15).
func TestWarnings(t *testing.T) {
	t.Parallel()
	e := inline.Example{File: "config/samples/big.yaml", Line: 1, Verdict: inline.Refused, Wrong: true, Message: "spec.size: Invalid value: 20: at most 10"}
	for _, tc := range []struct{ got, want string }{
		{inline.RuleWarning(e), "refuses config/samples/big.yaml:1, which expects to be allowed: spec.size: Invalid value: 20: at most 10"},
		{inline.ExampleWarning("self.size <= 10", e), `expected to be allowed, but the rule "self.size <= 10" refuses it: spec.size: Invalid value: 20: at most 10`},
		{inline.ExampleWarning("self.size\n  <= 10 && "+strings.Repeat("a", 60), e), `expected to be allowed, but the rule "self.size <= 10 && ` + strings.Repeat("a", 40) + `…" refuses it: spec.size: Invalid value: 20: at most 10`},
		{inline.NothingRefuses(""), "expected to be refused, but nothing refuses it"},
		{inline.NothingRefuses("at most 10"), `expected to be refused with "at most 10", but nothing refuses it with that message`},
	} {
		if tc.got != tc.want {
			t.Errorf("warning = %q, want %q", tc.got, tc.want)
		}
	}
}

// TestLinePolicyExamples checks the words a policy's expression adds to the
// table (#19): warns, audits, excludes and annotates, an example counted
// once whatever the bindings and params it ran under, the binding they all
// ran under, and the reasons a policy's expression does not run.
func TestLinePolicyExamples(t *testing.T) {
	t.Parallel()
	under := func(binding, param string, e inline.Example) inline.Example {
		e.Binding, e.Param = binding, param
		return e
	}
	warned := inline.Example{Verdict: inline.Warned, Message: "at most 3"}
	audited := inline.Example{Verdict: inline.Audited, Message: "at most 3"}
	excluded := inline.Example{Verdict: inline.Excluded}
	annotated := inline.Example{Verdict: inline.Annotated, Message: "5"}
	web := func(e inline.Example) inline.Example {
		e.File, e.URI, e.Line = "deploy.yaml", "file:///ws/deploy.yaml", 1
		return e
	}
	rule := func(examples []inline.Example) inline.Rule {
		return inline.Rule{Subject: "o", SubjectType: "D", Examples: examples}
	}
	for _, tc := range []struct {
		name string
		ex   []inline.Example
		want string
	}{
		{"warns", join(ex(1, under("limits", "", warned)), ex(2, under("limits", "", pass))), "warns 1 example · 2 pass · under binding limits"},
		{"audits", ex(2, under("audit", "", audited)), "audits 2 examples · under binding audit"},
		{"excludes", join(ex(1, under("b", "", excluded)), ex(2, under("b", "", pass))[1:]), "excludes 1 example · 1 passes · under binding b"},
		{"annotates", ex(3, under("b", "", annotated)), "annotates 3 examples · under binding b"},
		{"no binding", ex(1, under(inline.Unbound, "", refusedOK)), "1 example passes · under no binding"},
		{"two bindings", join(ex(1, under("a", "", pass)), ex(2, under("b", "", warned))[1:]), "warns 1 example · 1 passes · under 2 bindings"},
		{
			"an example under two params counts once, as the first of what they did",
			[]inline.Example{web(under("limits", "params.yaml:1", warned)), web(under("limits", "params.yaml:6", pass))},
			"warns 1 example · under binding limits",
		},
		{"a refusal of an example that expects to be allowed is wrong first", join(ex(1, under("d", "", refusesBad)), ex(2, under("d", "", warned))[1:]), "refuses 1 example it should allow · warns 1 · under binding d"},
		{"excluded from all", ex(2, under("b", "", inline.Example{Verdict: inline.NotRun, Skip: inline.SkipExcluded})), "not run: excluded by matchConditions"},
		{"no binding gives params", ex(1, under(inline.Unbound, "", inline.Example{Verdict: inline.NotRun, Skip: inline.SkipNoBinding})), "not run: no binding gives params"},
		{"authorizer", ex(1, under("b", "", inline.Example{Verdict: inline.NotRun, Skip: inline.SkipAuthorizer})), "not run: reads authorizer"},
		{"policy refused", ex(1, under("b", "", inline.Example{Verdict: inline.NotRun, Skip: inline.SkipPolicy})), "not run: policy refused"},
		{"no params", ex(1, under("b", "", inline.Example{Verdict: inline.NotRun, Skip: inline.SkipNoParams})), "not run: no params"},
		{"the binding refuses", ex(1, under("b", "", inline.Example{Verdict: inline.NotRun, Skip: inline.SkipBinding})), "not run: binding refuses the request"},
		{"a conversion", ex(1, under("b", "", inline.Example{Verdict: inline.NotRun, Skip: inline.SkipConversion})), "not run: needs a conversion"},
	} {
		got := inline.Line(rule(tc.ex))
		if title := got[len(got)-1].Title; title != tc.want {
			t.Errorf("Line(%s) ends with %q, want %q", tc.name, title, tc.want)
		}
	}
	// Shortened, the binding goes first.
	long := inline.Rule{Subject: "object", SubjectType: "apps/v1 Deployment", Examples: join(ex(1, under("limits", "", warned)), ex(2, under("limits", "", pass)))}
	if got := inline.Line(long); got[len(got)-1].Title != "warns 1 example · 2 pass" {
		t.Errorf("Line(long) ends with %q, want warns 1 example · 2 pass", got[len(got)-1].Title)
	}
}

// TestPolicyHoverAndWarnings checks that the hover and the warnings name
// the binding and the param of a policy's result (#19).
func TestPolicyHoverAndWarnings(t *testing.T) {
	t.Parallel()
	e := inline.Example{File: "deploy.yaml", URI: "file:///ws/deploy.yaml", Line: 1, Verdict: inline.Refused, Wrong: true, Binding: "limits", Param: "params.yaml:1", Message: "at most 3"}
	w := inline.Example{File: "deploy.yaml", URI: "file:///ws/deploy.yaml", Line: 6, Verdict: inline.Warned, Binding: "limits", Message: "at most 3"}
	n := inline.Example{File: "deploy.yaml", URI: "file:///ws/deploy.yaml", Line: 11, Verdict: inline.Refused, Binding: inline.Unbound, Message: "no zero"}
	want := "**Examples:** refuses 1 example it should allow · warns 1 · 1 passes · under 2 bindings\n\n" +
		"- [deploy.yaml:1](<file:///ws/deploy.yaml#L1>) **refused** under binding limits with param params.yaml:1, expected to be allowed: at most 3\n" +
		"- [deploy.yaml:6](<file:///ws/deploy.yaml#L6>) **warned** under binding limits: at most 3\n" +
		"- [deploy.yaml:11](<file:///ws/deploy.yaml#L11>) refused under no binding: no zero\n"
	if got := inline.ExamplesHover([]inline.Example{e, w, n}); got != want {
		t.Errorf("ExamplesHover() =\n%s\nwant\n%s", got, want)
	}
	for _, tc := range []struct{ got, want string }{
		{inline.RuleWarning(e), "refuses deploy.yaml:1 under binding limits with param params.yaml:1, which expects to be allowed: at most 3"},
		{inline.ExampleWarning("object.spec.replicas <= 3", e), `expected to be allowed, but the rule "object.spec.replicas <= 3" refuses it under binding limits with param params.yaml:1: at most 3`},
	} {
		if tc.got != tc.want {
			t.Errorf("warning = %q, want %q", tc.got, tc.want)
		}
	}
}
