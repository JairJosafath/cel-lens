package check

import (
	"slices"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/ast"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// spans parses expressions to find where the nodes of their syntax trees lie.
// The positions do not depend on declarations, so env has none. It keeps the
// tree of the last text only, so it parses a text once as long as the calls for
// it follow each other, however many problems the expression has, and uses
// constant memory. env must be set; it is not safe for concurrent use.
type spans struct {
	env    *cel.Env
	text   string   // the text last parsed, valid when parsed is true
	tree   *ast.AST // nil for a text that does not parse
	parsed bool
	parses int // how many times env parsed
}

// parse returns the syntax tree of text, or nil if it does not parse.
func (s *spans) parse(text string) *ast.AST {
	if s.parsed && s.text == text {
		return s.tree
	}
	var tree *ast.AST
	if parsed, iss := s.env.Parse(text); iss.Err() == nil {
		tree = parsed.NativeRep()
	}
	s.parses++
	s.text, s.tree, s.parsed = text, tree, true
	return tree
}

// locate maps a place in the text of e, line counting from 1 and col from 0,
// to a range in the file. The range is the part of the expression that the
// syntax tree node at that place covers, see nodeSpan. Where the expression
// does not parse, there is no tree and the range is the one character at the
// place. It reports false when the source does not show the text as it is, as
// with escapes or folded lines, so that the caller falls back to the whole
// expression.
func locate(parsed *ast.AST, lines [][]rune, e extract.Expression, line, col int) (start, end extract.Position, ok bool) {
	text := []rune(e.Text)
	if e.Chars != nil {
		return locateChars(parsed, text, e.Chars, line, col)
	}
	rows := strings.Split(e.Text, "\n")
	lead := len(e.Text) - len(strings.TrimLeft(e.Text, "\n"))
	if line-1 < lead || line-1 >= len(rows) || col > len([]rune(rows[line-1])) {
		return start, end, false
	}
	off := col
	for _, r := range rows[:line-1] {
		off += len([]rune(r)) + 1
	}
	from, to, spanned := off, off+1, false
	if parsed != nil {
		if f, t, found := nodeSpan(parsed, text, off); found {
			from, to, spanned = f, t, true
		}
	}
	// at maps an offset in the text to the file, if the file shows the text
	// before it as it is.
	at := func(off int) (extract.Position, bool) {
		i, c := 0, off
		for i < len(rows)-1 && c > len([]rune(rows[i])) {
			c -= len([]rune(rows[i])) + 1
			i++
		}
		t := []rune(rows[i])
		row := e.Start.Line - 1 + i - lead
		base := e.Start.Column - 1
		if i < lead || row < 0 || row >= len(lines) || c > len(t) || base+c > len(lines[row]) ||
			string(lines[row][base:base+c]) != string(t[:c]) {
			return extract.Position{}, false
		}
		return extract.Position{Line: row + 1, Column: base + c + 1}, true
	}
	start, ok = at(from)
	if !ok {
		return start, end, false
	}
	if !spanned {
		end = extract.Position{Line: start.Line, Column: start.Column + 1}
		return start, end, true
	}
	end, ok = at(to)
	return start, end, ok
}

// locateChars is locate for an expression with a map from its characters to
// the file, see extract.Expression.Chars.
func locateChars(parsed *ast.AST, text []rune, chars []extract.Range, line, col int) (start, end extract.Position, ok bool) {
	rows := strings.Split(string(text), "\n")
	if line < 1 || line > len(rows) || col > len([]rune(rows[line-1])) || len(chars) != len(text) {
		return start, end, false
	}
	off := col
	for _, r := range rows[:line-1] {
		off += len([]rune(r)) + 1
	}
	if off > len(text) || len(chars) == 0 {
		return start, end, false
	}
	if off == len(text) {
		// cel-go points just past the end, as for an incomplete rule.
		last := chars[len(chars)-1].End
		return last, extract.Position{Line: last.Line, Column: last.Column + 1}, true
	}
	from, to := off, off+1
	if parsed != nil {
		if f, t, found := nodeSpan(parsed, text, off); found && t > f && t <= len(text) {
			from, to = f, t
		}
	}
	return chars[from].Start, chars[to-1].End, true
}

// nodeSpan finds the part of text that the node at offset off in a covers, as
// rune offsets from and to (exclusive). cel-go reports a problem at the offset
// the parser recorded for a node: the start of a name or literal, the token of
// an operator, the dot of a field selection, or the opening parenthesis of a
// call or of the has macro. The span is the name of the field or function (for
// has, the field inside it), the whole operator or the whole literal.
func nodeSpan(a *ast.AST, text []rune, off int) (from, to int, ok bool) {
	info := a.SourceInfo()
	rng := func(e ast.Expr) ast.OffsetRange {
		r, _ := info.GetOffsetRange(e.ID())
		return r
	}
	// stop is the end of r in runes. cel-go gives Stop as Start plus the byte
	// length of the token, while Start counts runes.
	stop := func(r ast.OffsetRange) int {
		if r.Stop <= r.Start || int(r.Start) > len(text) {
			return int(r.Stop)
		}
		n := int(r.Stop - r.Start)
		b := 0
		for i, c := range text[r.Start:] {
			if b >= n {
				return int(r.Start) + i
			}
			b += len(string(c))
		}
		return len(text)
	}
	var end func(e ast.Expr) int
	end = func(e ast.Expr) int {
		r := rng(e)
		n := stop(r)
		if e.Kind() == ast.SelectKind {
			sel := e.AsSelect()
			if i := indexRunes(text, max(int(r.Start)+1, end(sel.Operand())), sel.FieldName()); i >= 0 {
				n = i + len([]rune(sel.FieldName()))
			}
		}
		if e.Kind() == ast.CallKind {
			c := e.AsCall()
			if c.IsMemberFunction() {
				n = max(n, end(c.Target()))
			}
			for _, arg := range c.Args() {
				n = max(n, end(arg))
			}
		}
		if e.Kind() == ast.ListKind {
			for _, el := range e.AsList().Elements() {
				n = max(n, end(el))
			}
		}
		return n
	}
	ast.PostOrderVisit(a.Expr(), ast.NewExprVisitor(func(e ast.Expr) {
		r := rng(e)
		if ok || int(r.Start) != off {
			return
		}
		switch {
		case e.Kind() == ast.SelectKind:
			sel := e.AsSelect()
			lo := int(r.Start) + 1
			if sel.IsTestOnly() {
				lo = end(sel.Operand())
			}
			if i := indexRunes(text, lo, sel.FieldName()); i >= 0 {
				from, to, ok = i, i+len([]rune(sel.FieldName())), true
			}
		case e.Kind() == ast.CallKind && int(r.Start) < len(text) && text[r.Start] == '(':
			// The name sits before the parenthesis, maybe after spaces.
			name := e.AsCall().FunctionName()
			if i := lastIndexRunes(text[:r.Start], name); i >= 0 {
				from, to, ok = i, i+len([]rune(name)), true
			}
		case r.Stop > r.Start:
			from, to, ok = int(r.Start), stop(r), true
		}
	}))
	return from, to, ok
}

// indexRunes finds the first place at or after from where name occurs in text,
// or -1.
func indexRunes(text []rune, from int, name string) int {
	n := []rune(name)
	for i := max(from, 0); i+len(n) <= len(text); i++ {
		if slices.Equal(text[i:i+len(n)], n) {
			return i
		}
	}
	return -1
}

// lastIndexRunes finds the last place where name occurs in text, or -1.
func lastIndexRunes(text []rune, name string) int {
	n := []rune(name)
	for i := len(text) - len(n); i >= 0; i-- {
		if slices.Equal(text[i:i+len(n)], n) {
			return i
		}
	}
	return -1
}
