// Package meaning says what a CEL check checks, in a plain sentence, or
// says nothing. The sentence is a translation of the parsed expression by
// a fixed table of shapes (the comparisons, has, in, the comprehension
// macros, a few string and format functions, and the connectives that join
// them); a boolean part that matches no shape, and anything that would make
// the sentence long or ambiguous, gives no sentence at all, never a part of
// one. Every value is quoted from the expression's source, in a code span,
// so the words never restate a value they could get wrong.
//
// Parser parses with cel-go's parser alone, without macros, so the macros
// are seen as they are written; Sentence takes what it parsed, which the
// hover shares with the names the expression reads. The package knows
// nothing of Kubernetes.
package meaning

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/parser"

	"github.com/JairJosafath/cel-lens/engine/internal/hover"
)

// Kind says what an expression is for, which the sentence starts with.
type Kind int

// The kinds of expression a sentence is made for.
const (
	// Check is an expression that allows what it is true for: a
	// CustomResourceDefinition's rule, a policy's validation.
	Check Kind = iota
	// MatchCondition is a policy's match condition: its sentence starts
	// "the policy applies only when".
	MatchCondition
)

// The limits past which no sentence is given: a longer one is hard to read
// in a hover, and more clauses hard to follow.
const (
	maxClauses = 4
	maxShown   = 200
)

// Parser parses CEL expressions as Sentence reads them, and as the hover
// reads the names an expression uses: with cel-go's parser, without
// macros, so a macro is the call it is written as, and with optional syntax
// and && and || chains flat. It is safe for concurrent use. A Parser needs
// NewParser.
type Parser struct {
	parser *parser.Parser
}

// NewParser returns a Parser.
func NewParser() (*Parser, error) {
	p, err := parser.NewParser(parser.EnableOptionalSyntax(true), parser.EnableVariadicOperatorASTs(true))
	if err != nil {
		return nil, fmt.Errorf("build the CEL parser: %w", err)
	}
	return &Parser{parser: p}, nil
}

// Expression is a CEL expression parsed: its text and its tree, with the
// offsets of its tokens in the tree's source info.
type Expression struct {
	Text string
	AST  *ast.AST
}

// Parse parses text. It is false for text that does not parse.
func (p *Parser) Parse(text string) (Expression, bool) {
	parsed, errs := p.parser.Parse(common.NewTextSource(text))
	if len(errs.GetErrors()) > 0 || parsed == nil {
		return Expression{}, false
	}
	return Expression{Text: text, AST: parsed}, true
}

// Sentence returns what the expression e, parsed by a Parser, checks, as a
// sentence in Markdown that ends with a full stop, its values in code
// spans: "for every `p` in `self.ports`, `p.port` is less than `65536`." A
// MatchCondition's starts "the policy applies only when". It is false, and
// there is no sentence, when e holds no tree, when a boolean part of it
// matches no shape of the table, when && and || mix at one level, when more
// than one part opens a scope (a comprehension, a "when" or an "if") in one
// list of clauses, when the sentence would have more than 4 clauses or 200
// characters as shown, when e's text is not valid UTF-8, and, for a
// MatchCondition, when it is a ternary.
func Sentence(e Expression, kind Kind) (string, bool) {
	// Text that is not valid UTF-8 has no rule's source to quote: YAML
	// refuses it, and cel-go reads each bad byte as U+FFFD, whose length
	// is not the byte's (review of #61).
	if e.AST == nil || !utf8.ValidString(e.Text) {
		return "", false
	}
	q := newQuoter(e.Text, e.AST)
	top := e.AST.Expr()
	var p phrase
	ok := false
	switch {
	case kind != MatchCondition:
		p, ok = q.clause(top)
	case isCall(top, operators.Conditional):
		// "only when if ..." does not read: no sentence.
	case isCall(top, operators.LogicalOr):
		// Not "only when when `x` is set, ...": the plain list of
		// alternatives, "`x` is not set, or ...".
		p, ok = q.chain(top.AsCall().Args(), ", or ", operators.LogicalAnd)
	default:
		p, ok = q.clause(top)
	}
	if !ok || p.clauses > maxClauses {
		return "", false
	}
	if kind == MatchCondition {
		p = words("the policy applies only when ").then(p)
	}
	if utf8.RuneCountInString(p.shown) > maxShown {
		return "", false
	}
	return p.md + ".", true
}

// phrase is part of a sentence: md is it in Markdown, shown as a reader sees
// it, clauses how many simple clauses it holds, and scoped is true when it
// opens a scope that whatever follows it would be read as part of: "for
// every ...", "when ...", "if ...".
type phrase struct {
	md, shown string
	clauses   int
	scoped    bool
}

// words is a phrase of plain words.
func words(s string) phrase { return phrase{md: s, shown: s} }

// then is p followed by next.
func (p phrase) then(next phrase) phrase {
	return phrase{md: p.md + next.md, shown: p.shown + next.shown, clauses: p.clauses + next.clauses, scoped: p.scoped || next.scoped}
}

// quoter turns the parts of one parsed expression into phrases.
type quoter struct {
	text string
	// ranges are the tokens of the parts of the expression, by id; partner
	// pairs its brackets (see brackets), and paired is false when they do
	// not pair, as when the text holds a comment.
	ranges  map[int64]ast.OffsetRange
	partner map[int]int
	paired  bool
}

// newQuoter makes the quoter of text, parsed. The parser counts where a
// token starts in characters, and where it stops as that plus the token's
// length in bytes (cel-go v0.30.0, parser/helper.go); the quoter keeps both
// in bytes: the start turned into bytes, and the stop as the start plus
// that length (#53, review comment 683).
func newQuoter(text string, parsed *ast.AST) *quoter {
	partner, paired := brackets(text)
	at := make([]int, 0, len(text)+1) // the byte each character starts at
	for i := range text {
		at = append(at, i)
	}
	at = append(at, len(text))
	toByte := func(n int32) int32 { return int32(at[min(max(int(n), 0), len(at)-1)]) } //nolint:gosec // an offset into text, which the parser limits to 100,000 characters
	ranges := map[int64]ast.OffsetRange{}
	for id, r := range parsed.SourceInfo().OffsetRanges() {
		start := toByte(r.Start)
		ranges[id] = ast.OffsetRange{Start: start, Stop: min(start+max(r.Stop-r.Start, 0), int32(len(text)))} //nolint:gosec // as toByte
	}
	return &quoter{text: text, ranges: ranges, partner: partner, paired: paired}
}

// clause translates e, a boolean part of the expression, by the table.
func (q *quoter) clause(e ast.Expr) (phrase, bool) {
	if e.Kind() == ast.SelectKind {
		return q.fieldRead(e)
	}
	if e.Kind() != ast.CallKind {
		return phrase{}, false
	}
	c := e.AsCall()
	args := c.Args()
	switch name := c.FunctionName(); {
	case name == operators.LogicalAnd:
		return q.chain(args, ", and ", operators.LogicalOr)
	case name == operators.LogicalOr:
		if len(args) >= 2 {
			if x, ok := notHas(args[0]); ok {
				return q.when(x, args[1:])
			}
		}
		return q.chain(args, ", or ", operators.LogicalAnd)
	case name == operators.Conditional && len(args) == 3:
		return q.ternary(args[0], args[1], args[2])
	case name == operators.LogicalNot && len(args) == 1:
		return q.negated(args[0])
	case comparison(name) != "" && len(args) == 2:
		return q.compare(comparison(name), args[0], args[1])
	case name == operators.In && len(args) == 2:
		return q.in(args[0], args[1])
	case name == "has" && !c.IsMemberFunction() && len(args) == 1:
		return q.with(args[0], " is set")
	case c.IsMemberFunction():
		return q.member(name, c.Target(), args)
	case len(args) == 1 && formatWords(name) != "":
		return q.with(args[0], " is "+formatWords(name))
	}
	return phrase{}, false
}

// fieldRead is a field read used as the result, `self.enabled`: "`x` is
// true". Only a chain of fields from a name counts.
func (q *quoter) fieldRead(e ast.Expr) (phrase, bool) {
	for x := e; x.Kind() != ast.IdentKind; x = x.AsSelect().Operand() {
		if x.Kind() != ast.SelectKind || x.AsSelect().IsTestOnly() {
			return phrase{}, false
		}
	}
	return q.with(e, " is true")
}

// chain joins args, the operands of one && or ||, with sep. An operand that
// is the other connective mixes them at one level, and gives none. A
// scoped operand goes last, as && and || do not depend on the order of
// their operands, so that nothing after it is read as part of it; two give
// none.
func (q *quoter) chain(args []ast.Expr, sep, other string) (phrase, bool) {
	var plain, scoped []phrase
	for _, a := range args {
		if isCall(a, other) {
			return phrase{}, false
		}
		p, ok := q.clause(a)
		if !ok {
			return phrase{}, false
		}
		if p.scoped {
			scoped = append(scoped, p)
		} else {
			plain = append(plain, p)
		}
	}
	if len(scoped) > 1 {
		return phrase{}, false
	}
	var out phrase
	for i, p := range append(plain, scoped...) {
		if i > 0 {
			out = out.then(words(sep))
		}
		out = out.then(p)
	}
	return out, true
}

// isCall reports whether e is a call of the function name.
func isCall(e ast.Expr, name string) bool {
	return e.Kind() == ast.CallKind && e.AsCall().FunctionName() == name
}

// notHas returns x when e is !has(x).
func notHas(e ast.Expr) (ast.Expr, bool) {
	if e.Kind() != ast.CallKind || e.AsCall().FunctionName() != operators.LogicalNot || len(e.AsCall().Args()) != 1 {
		return nil, false
	}
	h := e.AsCall().Args()[0]
	if h.Kind() != ast.CallKind || h.AsCall().FunctionName() != "has" || h.AsCall().IsMemberFunction() || len(h.AsCall().Args()) != 1 {
		return nil, false
	}
	return h.AsCall().Args()[0], true
}

// when is !has(x) || B || ...: "when `x` is set, B, or ...".
func (q *quoter) when(x ast.Expr, rest []ast.Expr) (phrase, bool) {
	set, ok := q.with(x, " is set")
	if !ok {
		return phrase{}, false
	}
	body, ok := q.chain(rest, ", or ", operators.LogicalAnd)
	if !ok {
		return phrase{}, false
	}
	p := words("when ").then(set).then(words(", ")).then(body)
	p.scoped = true
	return p, true
}

// ternary is c ? a : b with boolean branches: "if C, then A; otherwise B".
func (q *quoter) ternary(c, a, b ast.Expr) (phrase, bool) {
	var parts [3]phrase
	for i, e := range []ast.Expr{c, a, b} {
		p, ok := q.clause(e)
		if !ok {
			return phrase{}, false
		}
		parts[i] = p
	}
	p := words("if ").then(parts[0]).then(words(", then ")).then(parts[1]).then(words("; otherwise ")).then(parts[2])
	p.scoped = true
	return p, true
}

// negated is !e, for the shapes that have a negative form: has, and the
// string functions.
func (q *quoter) negated(e ast.Expr) (phrase, bool) {
	if e.Kind() != ast.CallKind {
		return phrase{}, false
	}
	c := e.AsCall()
	args := c.Args()
	switch name := c.FunctionName(); {
	case name == "has" && !c.IsMemberFunction() && len(args) == 1:
		return q.with(args[0], " is not set")
	case c.IsMemberFunction() && len(args) == 1 && stringWords(name, true) != "":
		return q.relation(c.Target(), stringWords(name, true), args[0])
	}
	return phrase{}, false
}

// comparison returns the words of a comparison operator, "" for another
// function.
func comparison(name string) string {
	switch name {
	case operators.Less:
		return " is less than "
	case operators.LessEquals:
		return " is at most "
	case operators.Greater:
		return " is greater than "
	case operators.GreaterEquals:
		return " is at least "
	case operators.Equals:
		return " equals "
	case operators.NotEquals:
		return " does not equal "
	}
	return ""
}

// compare is x <op> y: "`x` is less than `y`", or "the size of `x` is less
// than `y`" when x is size(x) or x.size().
func (q *quoter) compare(op string, x, y ast.Expr) (phrase, bool) {
	if of, ok := sizeOf(x); ok {
		sized, ok := q.relation(of, op, y)
		return words("the size of ").then(sized), ok
	}
	return q.relation(x, op, y)
}

// sizeOf returns x when e is size(x) or x.size().
func sizeOf(e ast.Expr) (ast.Expr, bool) {
	if e.Kind() != ast.CallKind || e.AsCall().FunctionName() != "size" {
		return nil, false
	}
	c := e.AsCall()
	switch {
	case c.IsMemberFunction() && len(c.Args()) == 0:
		return c.Target(), true
	case !c.IsMemberFunction() && len(c.Args()) == 1:
		return c.Args()[0], true
	}
	return nil, false
}

// in is x in [a, b]: "`x` is one of `a`, `b`", or x in y: "`x` is in `y`".
func (q *quoter) in(x, y ast.Expr) (phrase, bool) {
	if y.Kind() != ast.ListKind {
		return q.relation(x, " is in ", y)
	}
	elems := y.AsList().Elements()
	if len(elems) == 0 || len(y.AsList().OptionalIndices()) > 0 {
		return phrase{}, false
	}
	p, ok := q.with(x, " is one of ")
	for i, el := range elems {
		v, okv := q.value(el)
		if i > 0 {
			p = p.then(words(", "))
		}
		p, ok = p.then(v), ok && okv
	}
	return p, ok
}

// member is a function called on target: a comprehension macro, or a
// string function.
func (q *quoter) member(name string, target ast.Expr, args []ast.Expr) (phrase, bool) {
	if quantifier(name) != "" && (len(args) == 2 || len(args) == 3) {
		return q.comprehension(quantifier(name), target, args)
	}
	if len(args) == 1 && stringWords(name, false) != "" {
		return q.relation(target, stringWords(name, false), args[0])
	}
	return phrase{}, false
}

// quantifier returns the words of a comprehension macro that is true or
// false, "" for another name.
func quantifier(name string) string {
	switch name {
	case "all":
		return "for every "
	case "exists":
		return "for at least one "
	case "exists_one", "existsOne":
		return "for exactly one "
	}
	return ""
}

// comprehension is x.all(v, P), or with two variables x.all(k, v, P): "for
// every `v` in `x`, P", "for every `k`, `v` in `x`, P".
func (q *quoter) comprehension(quant string, x ast.Expr, args []ast.Expr) (phrase, bool) {
	p := words(quant)
	for i, v := range args[:len(args)-1] {
		if v.Kind() != ast.IdentKind {
			return phrase{}, false
		}
		name, ok := q.value(v)
		if !ok {
			return phrase{}, false
		}
		if i > 0 {
			p = p.then(words(", "))
		}
		p = p.then(name)
	}
	rng, ok := q.value(x)
	if !ok {
		return phrase{}, false
	}
	body, ok := q.clause(args[len(args)-1])
	if !ok {
		return phrase{}, false
	}
	p = p.then(words(" in ")).then(rng).then(words(", ")).then(body)
	p.scoped = true
	return p, true
}

// stringWords returns the words of a string function, negated or not, ""
// for another name.
func stringWords(name string, negated bool) string {
	type pair struct{ yes, no string }
	w := map[string]pair{
		"startsWith": {" starts with ", " does not start with "},
		"endsWith":   {" ends with ", " does not end with "},
		"contains":   {" contains ", " does not contain "},
		"matches":    {" matches the regular expression ", " does not match the regular expression "},
	}[name]
	if negated {
		return w.no
	}
	return w.yes
}

// formatWords returns what a format function says its argument is, "" for
// another name.
func formatWords(name string) string {
	switch name {
	case "isQuantity":
		return "a quantity"
	case "isURL":
		return "a URL"
	case "isIP":
		return "an IP address"
	case "isCIDR":
		return "a CIDR"
	case "isSemver":
		return "a semantic version"
	}
	return ""
}

// relation is "`x`<words>`y`", one simple clause.
func (q *quoter) relation(x ast.Expr, w string, y ast.Expr) (phrase, bool) {
	p, ok := q.with(x, w)
	v, okv := q.value(y)
	return p.then(v), ok && okv
}

// with is "`x`<words>", one simple clause.
func (q *quoter) with(x ast.Expr, w string) (phrase, bool) {
	v, ok := q.value(x)
	p := v.then(words(w))
	p.clauses = 1
	return p, ok
}

// value quotes e from the source, in a code span: its text, on one line.
// It is false when the text of e cannot be told apart from what is around
// it, or holds a comment or a line break inside a string.
func (q *quoter) value(e ast.Expr) (phrase, bool) {
	start, end, ok := q.span(e)
	if !ok {
		return phrase{}, false
	}
	s, ok := oneLine(q.text[start:end])
	if !ok {
		return phrase{}, false
	}
	return phrase{md: hover.Code(s), shown: s}, true
}

// span returns where the source of e starts and ends: from the first to the
// last token of e, with the name of a call or a message before its
// parenthesis or brace, the name of a field after its dot, and the
// brackets that close what it opens. It is false unless the span holds no
// token of another part of the expression and its brackets match.
func (q *quoter) span(e ast.Expr) (start, end int, ok bool) {
	inside := map[int64]bool{}
	start, end = len(q.text), -1
	var walk func(x ast.Expr) bool
	walk = func(x ast.Expr) bool {
		r, found := q.ranges[x.ID()]
		if !found {
			return false
		}
		inside[x.ID()] = true
		start, end = min(start, int(r.Start)), max(end, int(r.Stop))
		lo, hi, ok := q.named(x, int(r.Start), int(r.Stop))
		if !ok {
			return false
		}
		start, end = min(start, lo), max(end, hi)
		for _, id := range entryIDs(x) {
			inside[id] = true
		}
		for _, child := range children(x) {
			if !walk(child) {
				return false
			}
		}
		return true
	}
	if !q.paired || !walk(e) || end < start {
		return 0, 0, false
	}
	start, end = widen(q.partner, start, end)
	for id, r := range q.ranges {
		if !inside[id] && int(r.Start) < end && int(r.Stop) > start {
			return 0, 0, false
		}
	}
	return start, end, true
}

// named widens the token from to of x to the name that belongs to it but is
// no token of the tree: a global function's or a message's name before the
// token, a field's after it. It is false when the name is not where it
// should be.
func (q *quoter) named(x ast.Expr, from, to int) (start, end int, ok bool) {
	before := func(name string) (int, int, bool) {
		head := strings.TrimRightFunc(q.text[:from], unicode.IsSpace)
		if !strings.HasSuffix(head, name) {
			return 0, 0, false
		}
		return len(head) - len(name), to, true
	}
	switch x.Kind() {
	case ast.CallKind:
		if c := x.AsCall(); !c.IsMemberFunction() && !strings.HasPrefix(q.text[from:], c.FunctionName()) && identStart(c.FunctionName()) {
			return before(c.FunctionName())
		}
	case ast.StructKind:
		return before(x.AsStruct().TypeName())
	case ast.SelectKind:
		rest := strings.TrimPrefix(strings.TrimLeftFunc(q.text[to:], unicode.IsSpace), "?")
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		at := len(q.text) - len(rest)
		name := x.AsSelect().FieldName()
		switch {
		case strings.HasPrefix(rest, name):
			return from, at + len(name), true
		case strings.HasPrefix(rest, "`"):
			if quote := strings.IndexByte(rest[1:], '`'); quote >= 0 {
				return from, at + quote + 2, true
			}
		}
		return 0, 0, false
	case ast.IdentKind:
		// A name from the root, .name, holds its dot, which its token
		// does not.
		if strings.HasPrefix(x.AsIdent(), ".") && !strings.HasPrefix(q.text[from:], ".") {
			return before(".")
		}
	case ast.ComprehensionKind, ast.ListKind, ast.LiteralKind, ast.MapKind, ast.UnspecifiedExprKind:
	}
	return from, to, true
}

// identStart reports whether name is written as a name, as a function
// such as size, sets.contains or .f from the root is, which an operator such as _<_ or _[_]
// is not.
func identStart(name string) bool {
	name = strings.TrimPrefix(name, ".") // a name from the root
	for i, r := range name {
		if i == 0 && !unicode.IsLetter(r) || !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' {
			return false
		}
	}
	return name != ""
}

// entryIDs returns the ids of the entries of a map or the fields of a
// message, which have tokens of their own.
func entryIDs(x ast.Expr) []int64 {
	var ids []int64
	switch x.Kind() {
	case ast.MapKind:
		for _, en := range x.AsMap().Entries() {
			ids = append(ids, en.ID())
		}
	case ast.StructKind:
		for _, f := range x.AsStruct().Fields() {
			ids = append(ids, f.ID())
		}
	case ast.CallKind, ast.ComprehensionKind, ast.IdentKind, ast.ListKind, ast.LiteralKind, ast.SelectKind, ast.UnspecifiedExprKind:
	}
	return ids
}

// children returns the parts of x.
func children(x ast.Expr) []ast.Expr {
	switch x.Kind() {
	case ast.CallKind:
		c := x.AsCall()
		if c.IsMemberFunction() {
			return append([]ast.Expr{c.Target()}, c.Args()...)
		}
		return c.Args()
	case ast.SelectKind:
		return []ast.Expr{x.AsSelect().Operand()}
	case ast.ListKind:
		return x.AsList().Elements()
	case ast.MapKind:
		var out []ast.Expr
		for _, en := range x.AsMap().Entries() {
			out = append(out, en.AsMapEntry().Key(), en.AsMapEntry().Value())
		}
		return out
	case ast.StructKind:
		var out []ast.Expr
		for _, f := range x.AsStruct().Fields() {
			out = append(out, f.AsStructField().Value())
		}
		return out
	case ast.ComprehensionKind, ast.IdentKind, ast.LiteralKind, ast.UnspecifiedExprKind:
	}
	return nil
}
