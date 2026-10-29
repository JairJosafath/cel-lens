// Package assist answers the questions an editor asks about the CEL in a
// Kubernetes YAML file: what a name under the cursor is (hover) and what can
// be typed there (completion).
//
// It uses the types the API server gives an expression: self and oldSelf have
// the type of the CRD schema the rule is attached to, a ValidatingAdmissionPolicy
// has object, request, variables and the others, and the functions are the ones
// the Kubernetes CEL environment declares.
package assist

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/google/cel-go/common/types"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// Assistant answers hover and completion requests. It is safe for concurrent
// use. An Assistant needs New.
type Assistant struct {
	funcs  []function
	macros []macro
	byName map[string]function
	// vars are the variables of a Kyverno policy: set only on kyverno.
	vars []variable
	// kyverno answers for the expressions of a Kyverno ValidatingPolicy; nil
	// unless the experimental feature kyverno is on.
	kyverno *Assistant
	// version is the Kubernetes version the types follow.
	version kubeversion.Version
	// features are the experimental features that are on.
	features feature.Set
}

// NewOption changes how New builds an Assistant.
type NewOption func(*Assistant)

// WithFeatures switches on the experimental features in f. Without it every
// one is off: a Kyverno ValidatingPolicy gets no hover or completion
// (feature.Kyverno), params of a custom resource stay dyn
// (feature.CRDTypedPolicies), and completion and hover do not fit the spot
// (feature.FittingCompletion).
func WithFeatures(f feature.Set) NewOption {
	return func(a *Assistant) { a.features = f }
}

// New builds an Assistant from the Kubernetes CEL environment of version v.
func New(v kubeversion.Version, opts ...NewOption) (*Assistant, error) {
	funcs, macros, err := buildCatalog(v)
	if err != nil {
		return nil, err
	}
	a := &Assistant{funcs: funcs, macros: macros, byName: make(map[string]function, len(funcs)), version: v}
	for _, o := range opts {
		o(a)
	}
	for _, f := range funcs {
		a.byName[f.name] = f
	}
	if a.features.Has(feature.Kyverno) {
		if a.kyverno, err = newKyverno(); err != nil {
			return nil, err
		}
		a.kyverno.features = a.features
	}
	return a, nil
}

// forExpr returns the Assistant that knows the environment of the expression e
// of doc. It is false for the expression of a Kyverno ValidatingPolicy while the
// experimental feature kyverno is off: nothing is answered there.
func (a *Assistant) forExpr(doc *Document, e extract.Expression) (*Assistant, bool) {
	if a.vars == nil && isKyverno(doc, e) {
		return a.kyverno, a.kyverno != nil
	}
	return a, true
}

// fitting reports whether completion and hover fit the spot, the experimental
// feature fitting-completion.
func (a *Assistant) fitting() bool { return a.features.Has(feature.FittingCompletion) }

// scope returns the variables the expression e of doc can use.
func (a *Assistant) scope(doc *Document, e extract.Expression) []variable {
	if a.vars != nil {
		return a.kyvernoScope(doc.roots[e.Document], e)
	}
	return scope(doc, e, a.version, a.features)
}

// Hover describes what is under the cursor.
type Hover struct {
	// Markdown is the description: the declaration, then what it is for.
	Markdown string
	// Start and End are the range of the name described, End just after it.
	Start, End extract.Position
}

// ItemKind says what a completion item is.
type ItemKind int

// The kinds of completion item.
const (
	// KindVariable is a variable such as self.
	KindVariable ItemKind = iota
	// KindField is a field of the type before the dot.
	KindField
	// KindFunction is a function called as name(args).
	KindFunction
	// KindMethod is a function called as receiver.name(args).
	KindMethod
)

// Item is one thing that can be typed at the cursor.
type Item struct {
	Label string
	Kind  ItemKind
	// Detail is the type or signature, one line.
	Detail string
	// Documentation is a short description, possibly empty.
	Documentation string
	// SortKey orders the item among the others when it is not by Label; empty
	// when the label does. Fields of a policy that matches several kinds are
	// ordered by it: those every kind has come first.
	SortKey string
	// Start and End are the range of the file that the item replaces: the
	// part of the name already typed, empty when none is. NewText is what
	// goes there: Label, written as the rule's YAML scalar needs, so a quote
	// in it is escaped in a double-quoted or single-quoted rule. They are set
	// by Complete.
	Start, End extract.Position
	NewText    string
}

// site is the cursor inside the text of a CEL expression.
type site struct {
	expr extract.Expression
	// line is the line of the expression's text the cursor is on, col the
	// cursor's column in it counting from 0, and base the file column, from
	// 0, of the line's first character.
	line      []rune
	col, base int
	row       int // the file line, from 1
	index     int // the position of line among the lines of the expression's text
	// chars and first are set for an expression with a map to the file, see
	// extract.Expression.Chars: line starts at text offset first, and base
	// and row are not used. cursor is where the cursor is in the file.
	chars  []extract.Range
	first  int
	cursor extract.Position
}

// flat returns the whole text of the expression on one line, and the column in
// it where s.line starts, for a receiver that spans several lines.
func (s site) flat() ([]rune, int) {
	text := []rune(strings.ReplaceAll(s.expr.Text, "\n", " "))
	if s.chars != nil {
		return text, s.first
	}
	off := 0
	for _, l := range strings.Split(s.expr.Text, "\n")[:s.index] {
		off += len([]rune(l)) + 1
	}
	return text, off
}

func (s site) position(col int) extract.Position {
	return extract.Position{Line: s.row, Column: s.base + col + 1}
}

// span maps the columns from to to (exclusive, from < to) of the line to the
// file: from the start of the first character to the end of the last.
func (s site) span(from, to int) (start, end extract.Position) {
	if s.chars == nil {
		return s.position(from), s.position(to)
	}
	return s.chars[s.first+from].Start, s.chars[s.first+to-1].End
}

// typedRange is the range of the file from column from of the line to the
// cursor.
func (s site) typedRange(from int) (start, end extract.Position) {
	switch {
	case s.chars == nil:
		return s.position(from), s.position(s.col)
	case from == s.col:
		return s.cursor, s.cursor
	}
	return s.chars[s.first+from].Start, s.cursor
}

// column returns the column of the line that pos, a position of the file, is
// at; it may lie outside the line.
func (s site) column(pos extract.Position) int {
	if s.chars == nil {
		return pos.Column - 1 - s.base
	}
	return charOffset(s.chars, pos) - s.first
}

// charOffset returns how many characters of the text come before pos: those
// that end at or before it, and those with an empty range, such as the space
// that a line break folds into, that start before it.
func charOffset(chars []extract.Range, pos extract.Position) int {
	n := 0
	for _, c := range chars {
		if empty := c.Start == c.End; empty && before(c.Start, pos) || !empty && !before(pos, c.End) {
			n++
		}
	}
	return n
}

// atChars is at for an expression with a map from its text to the file.
func atChars(e extract.Expression, pos extract.Position) (site, bool) {
	text := []rune(e.Text)
	if len(e.Chars) != len(text) {
		return site{}, false
	}
	off := charOffset(e.Chars, pos)
	first := 0
	for i := off; i > 0; i-- {
		if text[i-1] == '\n' {
			first = i
			break
		}
	}
	last := off
	for last < len(text) && text[last] != '\n' {
		last++
	}
	return site{expr: e, line: text[first:last], col: off - first, chars: e.Chars, first: first, cursor: pos}, true
}

// scalarText writes s as it must be inside the YAML scalar of e: in
// double quotes a backslash is written \\ and a quote \", in single quotes
// a quote doubled. The quote is the character before the text in the file.
func scalarText(doc *Document, e extract.Expression, s string) string {
	if e.Start.Line < 1 || e.Start.Line > len(doc.lines) || e.Start.Column < 2 {
		return s
	}
	line := []rune(doc.lines[e.Start.Line-1])
	if e.Start.Column-2 >= len(line) {
		return s
	}
	switch line[e.Start.Column-2] {
	case '"':
		return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
	case '\'':
		return strings.ReplaceAll(s, "'", "''")
	}
	return s
}

// at finds the CEL expression of doc that contains pos, if any. It reports
// false for a place outside every expression, and for one where the file does
// not show the expression's text as it is (escapes, folded lines).
func at(doc *Document, pos extract.Position) (site, bool) {
	lines := doc.lines
	for _, e := range doc.exprs {
		if before(pos, e.Start) || before(e.End, pos) {
			continue
		}
		if e.Chars != nil {
			return atChars(e, pos)
		}
		text := strings.Split(e.Text, "\n")
		lead := len(e.Text) - len(strings.TrimLeft(e.Text, "\n"))
		i := pos.Line - e.Start.Line + lead
		if i < lead || i >= len(text) || pos.Line > len(lines) {
			return site{}, false
		}
		line := []rune(text[i])
		file := []rune(lines[pos.Line-1])
		base := e.Start.Column - 1
		col := pos.Column - 1 - base
		if col < 0 || col > len(line) || base+len(line) > len(file) || string(file[base:base+len(line)]) != string(line) {
			return site{}, false
		}
		return site{expr: e, line: line, col: col, base: base, row: pos.Line, index: i}, true
	}
	return site{}, false
}

func before(a, b extract.Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
}

// resolved is what a chain of names leads to.
type resolved struct {
	typ   string
	kind  string // "variable" or "field"
	desc  string
	known schema // nil when the fields are not known
	found bool
	// optional is true when the value is an optional, as after .?name.
	optional bool
	// celType is the opaque type of a variable without a schema, see variable.
	celType string
}

// resolve follows chain through the variables of s.
func (a *Assistant) resolve(vars []variable, chain []step) resolved {
	if len(chain) == 0 || chain[0].index {
		return resolved{}
	}
	var cur resolved
	for _, v := range vars {
		if v.name == chain[0].name {
			cur = resolved{typ: v.typ(), kind: "variable", desc: v.desc, known: v.schema, found: true, celType: v.celType}
		}
	}
	for _, st := range chain[1:] {
		if !cur.found || cur.known == nil {
			return resolved{}
		}
		next := cur.known.elem()
		if !st.index {
			next = cur.known.field(st.name)
		}
		if next == nil && a.fitting() && !st.index && cur.known.kind() == types.UnspecifiedKind && len(cur.known.fieldNames()) == 0 {
			next = cur.known // a field of a value of unknown type is of unknown type
		}
		if next == nil {
			return resolved{}
		}
		cur = resolved{typ: next.name(), kind: "field", desc: next.str("description"), known: next, found: true, optional: cur.optional || st.optional}
	}
	return cur
}

// Hover describes the variable, field or function at pos, a position in the
// file of doc. It reports false outside CEL expressions and where the cursor is
// not on a name it knows.
func (a *Assistant) Hover(doc *Document, pos extract.Position) (Hover, bool) {
	s, ok := at(doc, pos)
	if !ok || inString(s.line, s.col) {
		return Hover{}, false
	}
	a, ok = a.forExpr(doc, s.expr)
	if !ok {
		return Hover{}, false
	}
	start, end := wordAt(s.line, s.col)
	if start == end || s.line[start] >= '0' && s.line[start] <= '9' {
		return Hover{}, false
	}
	word := string(s.line[start:end])
	hover := func(md string) (Hover, bool) {
		from, to := s.span(start, end)
		return Hover{Markdown: md, Start: from, End: to}, true
	}
	called := calledAt(s.line, end)
	dotted := start > 0 && s.line[start-1] == '.'
	vars := a.withLoopVars(s.line, start, a.scope(doc, s.expr))
	switch {
	case dotted:
		chain, isChain := chainBefore(s.line, start-1)
		var recv resolved
		if isChain {
			recv = a.resolve(vars, chain)
		}
		if called {
			if ns := namespace(chain, vars); isChain && ns != "" {
				if _, ok := a.byName[ns+"."+word]; ok {
					return a.hoverFunction(ns+"."+word, false, a.argTypes(s.line, end, vars), hover)
				}
			}
			var actual []string
			flat, off := s.flat()
			t, opaque := a.opaqueBefore(flat, off+start-1, vars)
			if !opaque {
				t = a.argType(receiverBefore(s.line, start-1), vars)
			}
			if t != "dyn" {
				actual = append([]string{t}, a.argTypes(s.line, end, vars)...)
			}
			return a.hoverFunction(word, true, actual, hover)
		}
		if isChain && !called && word == requestResourceVar && recv.celType == authorizerType {
			return hover(declaration("field", word, resourceCheckType, requestResourceDesc))
		}
		if f := recv.known.field(word); isChain && recv.found && f != nil {
			return hover(a.withDynReason(declaration("field", word, f.name(), withKinds(f.str("description"), f.kinds())), f))
		}
	case called:
		return a.hoverFunction(word, false, a.argTypes(s.line, end, vars), hover)
	default:
		for _, v := range vars {
			if v.name == word {
				md := declaration("variable", word, v.typ(), v.desc)
				if v.typeName == "" {
					md = a.withDynReason(md, v.schema)
				}
				return hover(md)
			}
		}
	}
	return Hover{}, false
}

// namespace returns the name of the namespace chain stands for, when it is a
// single name that is no variable, as in sets.contains or optional.of, and ""
// otherwise.
func namespace(chain []step, vars []variable) string {
	if len(chain) != 1 || chain[0].index {
		return ""
	}
	for _, v := range vars {
		if v.name == chain[0].name {
			return ""
		}
	}
	return chain[0].name
}

func declaration(kind, name, typ, desc string) string {
	md := fmt.Sprintf("```cel\n(%s) %s: %s\n```", kind, name, typ)
	if desc != "" {
		md += "\n\n" + desc
	}
	return md
}

// hoverFunction describes the function or macro called name: its overloads,
// with the one the call's types select marked, then an example and a link to
// the docs. actual holds the types of the receiver, for a member, then of the
// arguments, "dyn" where one is not known; no overload is marked when none is
// known or, for a member, the receiver's type is not.
func (a *Assistant) hoverFunction(name string, isMember bool, actual []string, hover func(string) (Hover, bool)) (Hover, bool) {
	var sigs []string
	marked := -1
	desc := ""
	macroDesc := false
	if f, ok := a.byName[name]; ok {
		desc = f.desc
		var overloads []overload
		if isMember {
			for _, m := range f.members {
				overloads = append(overloads, m.overload)
			}
		} else {
			overloads = f.global
		}
		if isMember && len(actual) > 0 && !unknown(actual[0]) {
			overloads = onReceiver(overloads, actual[0])
		}
		picked := pick(overloads, actual)
		for i, o := range overloads {
			sig := o.sig
			if picked[i] {
				sig += " // this call"
				if marked < 0 {
					marked = len(sigs)
				}
			}
			sigs = append(sigs, sig)
		}
	}
	for _, m := range a.macros {
		if m.name == name && m.member == isMember {
			sigs = append(sigs, m.sig)
			if !macroDesc {
				desc, macroDesc = m.desc, true
			}
		}
	}
	if len(sigs) == 0 {
		return Hover{}, false
	}
	const most = 6
	if len(sigs) > most {
		if marked >= most {
			sigs[most-1] = sigs[marked]
		}
		sigs = append(sigs[:most], "...")
	}
	md := "```cel\n(function) " + strings.Join(sigs, "\n(function) ") + "\n```"
	if desc != "" {
		md += "\n\n" + desc
	}
	if a.vars != nil {
		return hover(md)
	}
	return hover(md + docsMarkdown(name))
}

// onReceiver returns the member overloads that accept a receiver of the type
// named recv, or all of them when none does.
func onReceiver(overloads []overload, recv string) []overload {
	var fit []overload
	for _, o := range overloads {
		if len(o.params) > 0 && fits(o.params[0], recv) {
			fit = append(fit, o)
		}
	}
	if len(fit) == 0 {
		return overloads
	}
	return fit
}

// Complete lists what can be typed at pos, a position in the file of doc:
// after a dot the fields of the type before it and the functions that apply
// to it, otherwise the variables, the functions and the macros. It reports
// false outside CEL expressions and inside string literals. The client
// filters the items by what is already typed.
func (a *Assistant) Complete(doc *Document, pos extract.Position) ([]Item, bool) {
	s, ok := at(doc, pos)
	if !ok || inString(s.line, s.col) {
		return nil, false
	}
	a, ok = a.forExpr(doc, s.expr)
	if !ok {
		return nil, false
	}
	start := s.col
	for start > 0 && identRune(s.line[start-1]) {
		start--
	}
	items := a.complete(doc, s, start)
	for i := range items {
		items[i].Start, items[i].End = s.typedRange(start)
		items[i].NewText = scalarText(doc, s.expr, items[i].Label)
	}
	return items, true
}

// complete lists the items for the cursor at s, where the name typed so far
// starts at column start of the line.
func (a *Assistant) complete(doc *Document, s site, start int) []Item {
	vars := a.scope(doc, s.expr)
	if start == 0 || s.line[start-1] != '.' {
		return a.topLevel(vars)
	}
	flat, off := s.flat()
	if t, ok := a.opaqueBefore(flat, off+start-1, vars); ok {
		items := a.members(nil, false, t, false)
		if t == authorizerType {
			items = append(items, Item{Label: requestResourceVar, Kind: KindField, Detail: resourceCheckType, Documentation: requestResourceDesc})
		}
		return items
	}
	chain, ok := chainBefore(s.line, start-1)
	if !ok {
		return a.members(schema(nil), optionalCallBefore(s.line, start-1), "", false)
	}
	if ns := namespace(chain, vars); ns != "" {
		if items := a.namespaced(ns); len(items) > 0 {
			return items
		}
	}
	recv := a.resolve(vars, chain)
	var items []Item
	if recv.found && recv.known != nil {
		items = fieldItems(recv.known)
	}
	return append(items, a.members(recv.known, recv.optional, recv.celType, recv.found)...)
}

// fieldItems lists the properties of s. When s merges several kinds, each item
// names the kinds that have the field, and the fields every kind has come first.
func fieldItems(s schema) []Item {
	var items []Item
	for _, n := range s.fieldNames() {
		f := s.field(n)
		it := Item{Label: n, Kind: KindField, Detail: f.name(), Documentation: f.str("description")}
		if kinds := f.kinds(); kinds != nil {
			it.Detail += " · " + strings.Join(kinds, ", ")
			it.SortKey = "1" + n
			if len(kinds) == len(s.kinds()) {
				it.SortKey = "0" + n
			}
		}
		items = append(items, it)
	}
	return items
}

// withKinds adds to desc the kinds that have the field, when there are any.
func withKinds(desc string, kinds []string) string {
	if kinds == nil {
		return desc
	}
	line := "In: " + strings.Join(kinds, ", ")
	if desc == "" {
		return line
	}
	return line + "\n\n" + desc
}

// topLevel lists the variables, global functions and global macros.
func (a *Assistant) topLevel(vars []variable) []Item {
	var items []Item
	for _, v := range vars {
		items = append(items, Item{Label: v.name, Kind: KindVariable, Detail: v.typ(), Documentation: v.desc})
	}
	for _, f := range a.funcs {
		if len(f.global) > 0 && !strings.Contains(f.name, ".") && !internal(f.name) {
			items = append(items, Item{Label: f.name, Kind: KindFunction, Detail: f.global[0].sig, Documentation: f.desc})
		}
	}
	for _, ns := range a.namespaces(vars) {
		items = append(items, Item{Label: ns, Kind: KindVariable, Detail: "namespace", Documentation: "Functions called as " + ns + ".name(...)."})
	}
	for i, m := range a.macros {
		if !m.member && (i == 0 || a.macros[i-1].name != m.name) {
			items = append(items, Item{Label: m.name, Kind: KindFunction, Detail: m.sig, Documentation: m.desc})
		}
	}
	sortItems(items)
	return items
}

// members lists the functions and macros that can be called on a receiver of
// type s, or of an optional of it; every one when s is nil, as its type is not
// known. A celType, the name of an opaque type, limits them to those declared on it.
// When found, the receiver was resolved, so a schema with no type is a truly
// dyn value and only what fits any value is offered, with a note saying so;
// otherwise the receiver is just not known and every member is offered.
func (a *Assistant) members(s schema, optional bool, celType string, found bool) []Item {
	n := s.name()
	if n == "object" && !optional {
		return nil
	}
	kind := s.kind()
	dyn := found && a.fitting() && !optional && celType == "" && kind == types.UnspecifiedKind
	var items []Item
	for _, f := range a.funcs {
		if operator(f.name) {
			continue
		}
		for _, m := range f.members {
			if dyn && !m.anyType() {
				continue
			}
			if optional && m.acceptsOptional() || !optional && m.accepts(kind) && m.onType(celType) {
				items = append(items, Item{Label: f.name, Kind: KindMethod, Detail: m.sig, Documentation: dynNote(dyn, f.desc)})
				break
			}
		}
	}
	for i, m := range a.macros {
		if m.member && celType == "" && (i == 0 || a.macros[i-1].name != m.name) && m.accepts(n, optional) {
			items = append(items, Item{Label: m.name, Kind: KindMethod, Detail: m.sig, Documentation: dynNote(dyn, m.desc)})
		}
	}
	sortItems(items)
	return items
}

// dynNote adds to desc, when dyn, that the receiver's type is not known, so
// only what fits any value is offered.
func dynNote(dyn bool, desc string) string {
	if !dyn {
		return desc
	}
	const note = "The receiver is dyn: its type is not known, so only what fits any value is offered."
	if desc == "" {
		return note
	}
	return desc + "\n\n" + note
}

// namespaced lists the functions called as ns.name(...), such as sets.contains.
func (a *Assistant) namespaced(ns string) []Item {
	var items []Item
	for _, f := range a.funcs {
		if rest, ok := strings.CutPrefix(f.name, ns+"."); ok && len(f.global) > 0 && !internal(rest) {
			items = append(items, Item{Label: rest, Kind: KindFunction, Detail: f.global[0].sig, Documentation: f.desc})
		}
	}
	sortItems(items)
	return items
}

// reserved reports whether name is one of the words the CEL specification reserves; an expression
// cannot use them as identifiers, so completion never offers them.
func reserved(name string) bool {
	switch name {
	case "as", "break", "const", "continue", "else", "for", "function", "if", "import", "let",
		"loop", "package", "namespace", "return", "var", "void", "while", "in":
		return true
	}
	return false
}

// internal reports whether name is one cel-go uses for itself, such as the
// operator overloads `_+_` and `!_` or the `@in` operator, or a CEL reserved
// word such as `in`: developers cannot type these.
func internal(name string) bool {
	return operator(name) || reserved(name)
}

// operator reports whether name is one cel-go uses for itself, such as `_+_`.
// Unlike a reserved word, a method may have such a name: authorizer's
// namespace('ns') is called after a dot.
func operator(name string) bool {
	return strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_") || strings.HasPrefix(name, "@")
}

func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Label < items[j].Label })
}

// namespaces lists the names that group functions, such as image in
// image.identifier(...), when no variable has the name. Only a Kyverno policy
// offers them, as Kyverno's own docs name its libraries that way.
func (a *Assistant) namespaces(vars []variable) []string {
	if a.vars == nil {
		return nil
	}
	var out []string
	for _, f := range a.funcs {
		ns, _, ok := strings.Cut(f.name, ".")
		if !ok || internal(f.name) || slices.Contains(out, ns) || namespace([]step{{name: ns}}, vars) == "" {
			continue
		}
		out = append(out, ns)
	}
	slices.Sort(out)
	return out
}
