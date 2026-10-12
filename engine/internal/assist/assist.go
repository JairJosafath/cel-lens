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

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
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
	// kyverno answers for the expressions of a Kyverno ValidatingPolicy.
	kyverno *Assistant
}

// New builds an Assistant from the Kubernetes CEL environment of version v.
func New(v kubeversion.Version) (*Assistant, error) {
	funcs, macros, err := buildCatalog(v)
	if err != nil {
		return nil, err
	}
	a := &Assistant{funcs: funcs, macros: macros, byName: make(map[string]function, len(funcs))}
	for _, f := range funcs {
		a.byName[f.name] = f
	}
	if a.kyverno, err = newKyverno(); err != nil {
		return nil, err
	}
	return a, nil
}

// forExpr returns the Assistant that knows the environment of the expression e of doc.
func (a *Assistant) forExpr(doc *Document, e extract.Expression) *Assistant {
	if a.kyverno != nil && isKyverno(doc, e) {
		return a.kyverno
	}
	return a
}

// scope returns the variables the expression e of doc can use.
func (a *Assistant) scope(doc *Document, e extract.Expression) []variable {
	if a.vars != nil {
		return a.kyvernoScope(doc.roots[e.Document], e)
	}
	return scope(doc, e)
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
}

func (s site) position(col int) extract.Position {
	return extract.Position{Line: s.row, Column: s.base + col + 1}
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
		return site{expr: e, line: line, col: col, base: base, row: pos.Line}, true
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
func resolve(vars []variable, chain []step) resolved {
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
	a = a.forExpr(doc, s.expr)
	start, end := wordAt(s.line, s.col)
	if start == end || s.line[start] >= '0' && s.line[start] <= '9' {
		return Hover{}, false
	}
	word := string(s.line[start:end])
	hover := func(md string) (Hover, bool) {
		return Hover{Markdown: md, Start: s.position(start), End: s.position(end)}, true
	}
	called := calledAt(s.line, end)
	dotted := start > 0 && s.line[start-1] == '.'
	vars := withLoopVars(s.line, start, a.scope(doc, s.expr))
	switch {
	case dotted:
		chain, isChain := chainBefore(s.line, start-1)
		var recv resolved
		if isChain {
			recv = resolve(vars, chain)
		}
		if called {
			if ns := namespace(chain, vars); isChain && ns != "" {
				if _, ok := a.byName[ns+"."+word]; ok {
					return a.hoverFunction(ns+"."+word, false, a.argTypes(s.line, end, vars), hover)
				}
			}
			var actual []string
			if t := a.argType(receiverBefore(s.line, start-1), vars); t != "dyn" {
				actual = append([]string{t}, a.argTypes(s.line, end, vars)...)
			}
			return a.hoverFunction(word, true, actual, hover)
		}
		if f := recv.known.field(word); isChain && recv.found && f != nil {
			return hover(declaration("field", word, f.name(), withKinds(f.str("description"), f.kinds())))
		}
	case called:
		return a.hoverFunction(word, false, a.argTypes(s.line, end, vars), hover)
	default:
		for _, v := range vars {
			if v.name == word {
				return hover(declaration("variable", word, v.typ(), v.desc))
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
	a = a.forExpr(doc, s.expr)
	start := s.col
	for start > 0 && identRune(s.line[start-1]) {
		start--
	}
	vars := a.scope(doc, s.expr)
	if start == 0 || s.line[start-1] != '.' {
		return a.topLevel(vars), true
	}
	chain, ok := chainBefore(s.line, start-1)
	if !ok {
		return a.members(schema(nil), optionalCallBefore(s.line, start-1), ""), true
	}
	if ns := namespace(chain, vars); ns != "" {
		if items := a.namespaced(ns); len(items) > 0 {
			return items, true
		}
	}
	recv := resolve(vars, chain)
	var items []Item
	if recv.found && recv.known != nil {
		items = fieldItems(recv.known)
	}
	return append(items, a.members(recv.known, recv.optional, recv.celType)...), true
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
func (a *Assistant) members(s schema, optional bool, celType string) []Item {
	n := s.name()
	if n == "object" && !optional {
		return nil
	}
	kind := s.kind()
	var items []Item
	for _, f := range a.funcs {
		if internal(f.name) {
			continue
		}
		for _, m := range f.members {
			if optional && m.acceptsOptional() || !optional && m.accepts(kind) && m.onType(celType) {
				items = append(items, Item{Label: f.name, Kind: KindMethod, Detail: m.sig, Documentation: f.desc})
				break
			}
		}
	}
	for i, m := range a.macros {
		if m.member && celType == "" && (i == 0 || a.macros[i-1].name != m.name) && m.accepts(n, optional) {
			items = append(items, Item{Label: m.name, Kind: KindMethod, Detail: m.sig, Documentation: m.desc})
		}
	}
	sortItems(items)
	return items
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
	return strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_") || strings.HasPrefix(name, "@") || reserved(name)
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
