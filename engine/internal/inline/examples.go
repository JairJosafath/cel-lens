package inline

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ShowExampleResults is the command of the part that says what a rule does
// to the examples. It takes one argument, the Rule's Location, and lists
// each example with what the rule does to it; the client implements it.
const ShowExampleResults = "cel-lens.showExampleResults"

// Verdict is what a rule does to an example.
type Verdict int

// The verdicts.
const (
	// Allowed is a rule that ran on the example and refused nothing.
	Allowed Verdict = iota
	// Refused is a rule that refused the example.
	Refused
	// NotRun is a rule the API server does not run on the example (see
	// Skip).
	NotRun
	// Failed is a rule whose run stopped with an error, which the API
	// server answers with a refusal: a runtime error or a cost limit.
	Failed
	// Warned is an admission policy's validation that fails under a binding
	// that warns: the API server allows the example, with a warning.
	Warned
	// Audited is an admission policy's validation that fails under a
	// binding that only audits: the API server allows the example, and
	// records the failure in its audit log.
	Audited
	// Annotated is an admission policy's audit annotation the example gets.
	Annotated
	// Excluded is an admission policy's match condition that is false on
	// the example, so the policy does not run on it.
	Excluded
)

// Skip is why a rule did not run on an example.
type Skip int

// The reasons a rule does not run.
const (
	// SkipOther is a reason none of the others names.
	SkipOther Skip = iota
	// SkipCRD: the API server refuses the CustomResourceDefinition for the
	// rule, so it validates nothing with it.
	SkipCRD
	// SkipExample: the API server refuses the example before its rules.
	SkipExample
	// SkipNotUpdate: the rule reads oldSelf and the example is a create.
	SkipNotUpdate
	// SkipNoOldValue: the rule reads oldSelf and the update has no old
	// value where it is.
	SkipNoOldValue
	// SkipAbsent: the example does not have the rule's field.
	SkipAbsent
	// SkipPolicy: the API server refuses the admission policy.
	SkipPolicy
	// SkipExcluded: a match condition of the policy excludes the example.
	SkipExcluded
	// SkipNoBinding: the expression reads params, and no binding binds the
	// policy.
	SkipNoBinding
	// SkipNoParams: the binding has no params for the example.
	SkipNoParams
	// SkipBinding: the API server refuses the example for the binding
	// before it runs the policy.
	SkipBinding
	// SkipAuthorizer: the expression, or a match condition, reads
	// authorizer, which cel-lens does not run.
	SkipAuthorizer
	// SkipConversion: the API server would convert the example to another
	// version first, which cel-lens does not.
	SkipConversion
)

// Example is what a rule does to one example.
type Example struct {
	// File is the example's file below its workspace folder,
	// slash-separated, URI the file's URI and Line the line, from 1, its
	// document's content starts at.
	File, URI string
	Line      int
	// Update reports that the rule ran on it as an update, not a create.
	Update  bool
	Verdict Verdict
	// Wrong reports that the verdict is not what the example expects.
	// OnExample reports that this shows on the example, not at the rule:
	// the example expects to be refused, with Want in the message when
	// Want is not empty, and nothing refuses it so.
	Wrong, OnExample bool
	Want             string
	// Skip is why the rule did not run, for NotRun.
	Skip Skip
	// Message is the API server's: its refusals, separated by "; ", for
	// Refused; its error for Failed; why the rule did not run for NotRun;
	// the validation's message for Warned and Audited; the annotation for
	// Annotated; empty for Allowed and Excluded.
	Message string
	// Binding names the binding of an admission policy the example was
	// admitted under, Unbound when no binding binds the policy, "" for a
	// CustomResourceDefinition's rule; Param is the param it was evaluated
	// with, as file:line, "" for none.
	Binding, Param string
}

// Unbound is the Binding of an Example of an admission policy no binding
// binds, which runs as if bound with the validationActions Deny.
const Unbound = "\x00unbound"

// The most examples the hover lists.
const maxListed = 10

// kind is how the line counts an example.
type kind int

const (
	// refusesWrongly: refused, expected to be allowed.
	refusesWrongly kind = iota
	// refusesOtherwise: refused, but not with the message expected.
	refusesOtherwise
	// allowsWrongly: allowed, expected to be refused.
	allowsWrongly
	errs
	warns
	audits
	excludes
	annotates
	passes
	notRun
)

// kindOf is how the line counts e: an error whatever it expects, a rule that
// did not run apart, and otherwise by whether the verdict is the one
// expected.
func kindOf(e Example) kind {
	switch {
	case e.Verdict == Failed:
		return errs
	case e.Verdict == NotRun:
		return notRun
	case e.Verdict == Warned && !e.Wrong:
		return warns
	case e.Verdict == Audited && !e.Wrong:
		return audits
	case e.Verdict == Excluded && !e.Wrong:
		return excludes
	case e.Verdict == Annotated && !e.Wrong:
		return annotates
	case !e.Wrong:
		return passes
	case e.Verdict == Refused && e.OnExample:
		return refusesOtherwise
	case e.Verdict == Refused:
		return refusesWrongly
	}
	return allowsWrongly
}

// plural is one when n is 1, else many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// count writes "n examples" or "1 example".
func count(n int) string { return strconv.Itoa(n) + " " + plural(n, "example", "examples") }

// examplesTitles returns the titles the part about examples can have,
// longest first: every count in words, the wrong ones first, then the
// wrong ones as one count, then the first count alone. It is nil when there
// is nothing to say: no example.
//
// In words, the first count names the examples and those after it do not:
// "refuses 1 example it should allow · 2 pass", "3 examples pass", "1
// example passes", "allows 1 example it should refuse", "1 example errors".
// An example a rule refuses as expected passes; one it did not run on is
// left out, but when it ran on none, which says why: "not run: needs an
// update example".
//
// An example an admission policy ran on under several bindings or params
// counts once, as the first of what they did in that order. The words end
// with the binding when every result is of one: "under binding warn", or
// "under 2 bindings", "under no binding".
func examplesTitles(ex []Example) []string {
	if len(ex) == 0 {
		return nil
	}
	n, skips, under, examples := tally(ex)
	if n[notRun] == examples {
		why := "not run on " + count(examples)
		if len(skips) == 1 {
			for s := range skips {
				why = "not run: " + skipPhrase(s, examples)
			}
		}
		return dedupe(why, "not run")
	}
	var words, short []string
	add := func(w string) { words = append(words, w) }
	first := func() bool { return len(words) == 0 }
	if c := n[refusesWrongly]; c > 0 {
		add("refuses " + countIf(c, first()) + " it should allow")
	}
	if c := n[refusesOtherwise]; c > 0 {
		add("refuses " + countIf(c, first()) + " with another message")
	}
	if c := n[allowsWrongly]; c > 0 {
		add("allows " + countIf(c, first()) + " it should refuse")
	}
	if wrong := wrongs(n); wrong > 0 {
		short = append(short, strconv.Itoa(wrong)+" wrong")
	}
	if c := n[errs]; c > 0 {
		if first() {
			add(count(c) + plural(c, " errors", " error"))
		} else {
			add(strconv.Itoa(c) + plural(c, " error", " errors"))
		}
		short = append(short, strconv.Itoa(c)+plural(c, " error", " errors"))
	}
	for _, v := range []struct {
		k    kind
		verb string
	}{{warns, "warns"}, {audits, "audits"}, {excludes, "excludes"}, {annotates, "annotates"}} {
		if c := n[v.k]; c > 0 {
			add(v.verb + " " + countIf(c, first()))
			short = append(short, v.verb+" "+strconv.Itoa(c))
		}
	}
	if c := n[passes]; c > 0 {
		p := strconv.Itoa(c) + plural(c, " passes", " pass")
		if first() {
			p = count(c) + plural(c, " passes", " pass")
		}
		add(p)
		short = append(short, strconv.Itoa(c)+plural(c, " passes", " pass"))
	}
	full := strings.Join(words, dot)
	if under != "" {
		full += dot + under
	}
	return dedupe(full, strings.Join(words, dot), strings.Join(short, dot), short[0])
}

// wrongs is how many examples of the counts n have a wrong verdict.
func wrongs(n [notRun + 1]int) int {
	return n[refusesWrongly] + n[refusesOtherwise] + n[allowsWrongly]
}

// tally counts the examples of ex by kind, each counted once as the worst
// of what was done to it (see examplesTitles), and returns the counts, why
// the rule did not run on those it did not run on, the binding the words
// end with ("" for none), and how many examples there are.
func tally(ex []Example) (n [notRun + 1]int, skips map[Skip]int, under string, examples int) {
	type at struct {
		uri    string
		line   int
		update bool
		// rule is the index of a CustomResourceDefinition's rule's result,
		// each of which is its own example.
		rule int
	}
	worst := map[at]Example{}
	var order []at
	bindings := map[string]bool{}
	for i, e := range ex {
		k := at{e.URI, e.Line, e.Update, -1}
		if e.Binding == "" {
			k.rule = i
		}
		if w, ok := worst[k]; !ok || kindOf(e) < kindOf(w) {
			if !ok {
				order = append(order, k)
			}
			worst[k] = e
		}
		bindings[e.Binding] = true
	}
	skips = map[Skip]int{}
	for _, k := range order {
		kind := kindOf(worst[k])
		n[kind]++
		if kind == notRun {
			skips[worst[k].Skip]++
		}
	}
	switch {
	case len(bindings) > 1:
		under = "under " + strconv.Itoa(len(bindings)) + " bindings"
	case bindings[Unbound]:
		under = "under no binding"
	case !bindings[""]:
		for b := range bindings {
			under = "under binding " + b
		}
	}
	return n, skips, under, len(order)
}

// The states of a rule's examples (see ExamplesState).
const (
	ExamplesWrong  = "wrong"
	ExamplesError  = "error"
	ExamplesPass   = "pass"
	ExamplesNotRun = "notRun"
)

// ExamplesState returns what the examples ex of a rule say, as a list of
// rules shows it: the state, ExamplesWrong when a verdict is wrong, at the
// rule or on an example, ExamplesError when an example errors and none is
// wrong, ExamplesNotRun when the rule ran on none, else ExamplesPass; and
// the words, exactly the part about examples of the line above the rule
// before the line shortens it to fit (see Line). It is false, with neither,
// for no example.
func ExamplesState(ex []Example) (state, words string, ok bool) {
	titles := examplesTitles(ex)
	if titles == nil {
		return "", "", false
	}
	n, _, _, examples := tally(ex)
	switch {
	case wrongs(n) > 0:
		state = ExamplesWrong
	case n[errs] > 0:
		state = ExamplesError
	case n[notRun] == examples:
		state = ExamplesNotRun
	default:
		state = ExamplesPass
	}
	return state, titles[0], true
}

// countIf writes c examples as "c examples" when they come first, as "c"
// when they come after another count.
func countIf(c int, first bool) string {
	if first {
		return count(c)
	}
	return strconv.Itoa(c)
}

// skipPhrase says in a few words why a rule did not run on n examples.
func skipPhrase(s Skip, n int) string {
	switch s {
	case SkipCRD:
		return "CRD refused"
	case SkipExample:
		return plural(n, "example refused before the rules", "examples refused before the rules")
	case SkipNotUpdate:
		return "needs an update example"
	case SkipNoOldValue:
		return plural(n, "no old value in the update", "no old value in the updates")
	case SkipAbsent:
		return plural(n, "field absent from the example", "field absent from the examples")
	case SkipPolicy:
		return "policy refused"
	case SkipExcluded:
		return "excluded by matchConditions"
	case SkipNoBinding:
		return "no binding gives params"
	case SkipNoParams:
		return "no params"
	case SkipBinding:
		return "binding refuses the request"
	case SkipAuthorizer:
		return "reads authorizer"
	case SkipConversion:
		return "needs a conversion"
	case SkipOther:
	}
	return count(n)
}

// dedupe returns titles without a title equal to the one before it.
func dedupe(titles ...string) []string {
	out := titles[:1]
	for _, t := range titles[1:] {
		if t != out[len(out)-1] {
			out = append(out, t)
		}
	}
	return out
}

// examplesPart is the part titled title about the examples of r.
func examplesPart(r Rule, title string) Part {
	return Part{Title: title, Command: ShowExampleResults, Arguments: []any{r.Location}}
}

// ExamplesHover returns the section of a rule's hover about the examples
// ex, in Markdown: the line's part in words, then each example, linked to
// its file and line, with its verdict and the API server's message, the
// wrong verdicts first, then the errors, then the others in the order of
// ex, at most 10, then "and N more". It is "" for no example.
func ExamplesHover(ex []Example) string {
	titles := examplesTitles(ex)
	if titles == nil {
		return ""
	}
	order := func(e Example) int {
		switch kindOf(e) {
		case refusesWrongly, refusesOtherwise, allowsWrongly:
			return 0
		case errs:
			return 1
		case warns, audits, excludes, annotates, passes, notRun:
		}
		return 2
	}
	sorted := make([]Example, 0, len(ex))
	for rank := range 3 {
		for _, e := range ex {
			if order(e) == rank {
				sorted = append(sorted, e)
			}
		}
	}
	var b strings.Builder
	b.WriteString("**Examples:** " + escape(titles[0]) + "\n\n")
	for _, e := range sorted[:min(len(sorted), maxListed)] {
		b.WriteString("- " + listed(e) + "\n")
	}
	if left := len(sorted) - maxListed; left > 0 {
		b.WriteString("\nand " + strconv.Itoa(left) + " more\n")
	}
	return b.String()
}

// listed is e as the hover lists it: "[config/samples/big.yaml:3](link)
// **refused**, expected to be allowed: <message>".
func listed(e Example) string {
	link := "[" + escape(e.File+":"+strconv.Itoa(e.Line)) + "](<" + e.URI + "#L" + strconv.Itoa(e.Line) + ">)"
	verdict := verdictWord(e.Verdict)
	if e.Update {
		verdict += " as an update"
	}
	if k := kindOf(e); k != passes && k != notRun && k != annotates && k != excludes {
		verdict = "**" + verdict + "**"
	}
	verdict += under(e)
	switch {
	case e.Wrong && e.OnExample:
		verdict += ", expected to be refused"
		if e.Want != "" {
			verdict += " with " + strconv.Quote(e.Want)
		}
	case e.Wrong:
		verdict += ", expected to be allowed"
	}
	if e.Message != "" {
		verdict += ": " + escape(oneLine(e.Message))
	}
	return link + " " + verdict
}

// verdictWord is v in a word or two.
func verdictWord(v Verdict) string {
	switch v {
	case Allowed:
		return "allowed"
	case Refused:
		return "refused"
	case NotRun:
		return "not run"
	case Warned:
		return "warned"
	case Audited:
		return "audited"
	case Annotated:
		return "annotated"
	case Excluded:
		return "excluded"
	case Failed:
	}
	return "error"
}

// under says which binding and param e was evaluated under, as the hover
// and the warnings say it: " under binding warn with param
// params.yaml:3", "" for a CustomResourceDefinition's rule.
func under(e Example) string {
	out := ""
	switch e.Binding {
	case "":
	case Unbound:
		out = " under no binding"
	default:
		out = " under binding " + e.Binding
	}
	if e.Param != "" {
		out += " with param " + e.Param
	}
	return out
}

// RuleWarning is the warning on a rule that refuses e, an example that
// expects to be allowed, or fails on it, which the API server answers with
// a refusal: "refuses config/samples/big.yaml:3, which expects to be
// allowed: <message>".
func RuleWarning(e Example) string {
	return "refuses " + e.File + ":" + strconv.Itoa(e.Line) + under(e) + ", which expects to be allowed: " + oneLine(e.Message)
}

// ExampleWarning is the warning on e, an example that expects to be
// allowed, which the rule whose text is rule refuses or fails on:
// `expected to be allowed, but the rule "self.size <= 10" refuses it:
// <message>`, the rule on one line and cut past 60 characters.
func ExampleWarning(rule string, e Example) string {
	return "expected to be allowed, but the rule " + strconv.Quote(cut(oneLine(rule), maxRule)) + " refuses it" + under(e) + ": " + oneLine(e.Message)
}

// NothingRefuses is the warning on an example that expects to be refused,
// with want in the message, "" for any, and that neither the schema nor a
// rule refuses so.
func NothingRefuses(want string) string {
	if want == "" {
		return "expected to be refused, but nothing refuses it"
	}
	return "expected to be refused with " + strconv.Quote(want) + ", but nothing refuses it with that message"
}

// maxRule is the most characters of a rule a warning shows.
const maxRule = 60

// oneLine is s with each run of white space, line breaks included, as one
// space, and none at its ends.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// escape writes s so Markdown shows it as it is.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("\\`*_{}[]<>()#+-!|~", r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// titleWidth is how many characters t is.
func titleWidth(t string) int { return utf8.RuneCountInString(t) }
