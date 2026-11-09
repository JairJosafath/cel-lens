// Package inline writes the inline info of cel-lens: the quiet line above
// each CEL expression, as GitLens shows one above each block. The line says
// what the expression is about, whether it compiles, and which values it reads
// that Kubernetes leaves dyn, each with the command that gives it a type, kept
// in a .cellens file.
//
// A line is a list of parts, each with the client command it runs when
// clicked: every part acts. The language server sends each part as a code
// lens; VS Code shows the parts of a line side by side with Separator between
// them. Every word of the line is written here, and only here.
package inline

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
)

// The commands the parts run in the client. ShowRule takes one argument, the
// Rule's Location, and shows the expression's problems or its hover.
// GiveType, EditType and RemoveType take one argument, the Target of the
// value. ListValues takes, as its arguments, the parts of the values the line
// leaves out, each a Part whose JSON form is a protocol Command; the client
// lists them and runs the one picked.
const (
	ShowRule   = "cel-lens.showRule"
	GiveType   = "cel-lens.giveType"
	EditType   = "cel-lens.editType"
	RemoveType = "cel-lens.removeType"
	ListValues = "cel-lens.listValues"
)

const (
	// MaxWidth is the most characters (runes) a line has, Separator between
	// its parts included.
	MaxWidth = 80
	// Separator is what VS Code shows between the code lenses of a line.
	Separator = " | "
	// maxShown is the most values a line shows; ListValues lists the others.
	maxShown = 2
	// maxType is the most characters of a type the line shows.
	maxType = 40
)

// The words of the line.
const (
	dot      = " · "
	compiles = "compiles"
	give     = "Give it a type"
	edit     = "Edit"
	remove   = "Remove"
	dyn      = "dyn"
)

// Rule is what the line says about one CEL expression.
type Rule struct {
	// Subject is the variable the expression is about, self or object, and
	// SubjectType what it is, such as Widget.spec or apps/v1 Deployment.
	Subject, SubjectType string
	// Location is the argument of ShowRule: where the expression is, as the
	// client names a place, such as {uri, range}.
	Location any
	// Problems is how many problems the expression has.
	Problems int
	// Values are the dyn values the expression reads, in the order it reads
	// them.
	Values []Value
}

// Value is a dyn value an expression reads.
type Value struct {
	// Name is the value as the expression reads it, such as self.spec.config.
	// A long one is cut in the middle, after its root: the variable it is in
	// (Target.Variable, such as variables.max) or its first name (self).
	Name string
	// Type is the type a .cellens entry gives it, as the entry writes it,
	// such as "{name: string}" or "apps/v1 Deployment"; empty for a value that
	// is dyn.
	Type string
	// Entry reports whether a .cellens entry names the value, so Edit and
	// Remove act on it: always when Type is set, and for a value still dyn
	// because the entry does not apply, such as one whose kind is not found.
	Entry bool
	// Target is the argument of the value's commands.
	Target Target
}

// Target names a value the way a .cellens entry does: the argument of
// GiveType, EditType and RemoveType, so the client can ask the engine to
// write the entry.
type Target struct {
	// Cellens is the URI of the .cellens file the entry is in, or would go in.
	Cellens string `json:"cellens"`
	// File is the YAML file the value is in, relative to the folder of Cellens.
	File string `json:"file"`
	// Document is the kind and metadata.name of the document, separated by a
	// space.
	Document string `json:"document"`
	// Version is a CustomResourceDefinition's version; Variable a policy's
	// variable.
	Version  string `json:"version,omitempty"`
	Variable string `json:"variable,omitempty"`
	// Path is the value's path from the root or the variable; empty for the
	// root.
	Path string `json:"path,omitempty"`
	// Value is the value as the expression reads it, for the client to show.
	Value string `json:"value"`
	// Type or Kind is what the entry gives the value now; neither is set for a
	// value that is dyn.
	Type string `json:"type,omitempty"`
	Kind string `json:"kind,omitempty"`
}

// Part is one part of a line: its text and the command it runs when
// clicked. Its JSON form is the protocol's Command.
type Part struct {
	Title string `json:"title"`
	// Command is empty for a part that only says something.
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

// Line returns the parts of the line above the expression r: first the
// subject, its type and whether it compiles, such as
// "self: Widget.spec · compiles", which runs ShowRule; then a part for each
// of the first two dyn values, as long as the line stays within MaxWidth,
// cutting a long name in the middle ("object.…valueFrom") and a long type to
// fewer fields when that makes it fit; then "+N more" for the values left
// out, whose command lists them.
func Line(r Rule) []Part {
	parts := []Part{{Title: r.Subject + ": " + shortType(r.SubjectType, maxType) + dot + status(r.Problems), Command: ShowRule, Arguments: []any{r.Location}}}
	var rest []Value
	shown := 0
	for i, v := range r.Values {
		if shown == maxShown || len(rest) > 0 {
			rest = append(rest, v)
			continue
		}
		p, ok := fit(parts, v, len(r.Values)-i-1)
		if !ok {
			rest = append(rest, v)
			continue
		}
		parts = append(parts, p...)
		shown++
	}
	if len(rest) > 0 {
		parts = append(parts, more(rest))
	}
	return parts
}

// Width is how many characters VS Code shows for the line of parts.
func Width(parts []Part) int {
	n := 0
	for i, p := range parts {
		if i > 0 {
			n += utf8.RuneCountInString(Separator)
		}
		n += utf8.RuneCountInString(p.Title)
	}
	return n
}

// status says whether an expression with n problems compiles.
func status(n int) string {
	switch n {
	case 0:
		return compiles
	case 1:
		return "1 problem"
	}
	return strconv.Itoa(n) + " problems"
}

// fit returns the parts of v that fit after parts within MaxWidth, with room
// for a "+N more" when left values come after it: with its whole name and
// type when they fit, else with the name cut in the middle, and then with the
// type cut to the room left. It is false when even the shortest do not fit.
func fit(parts []Part, v Value, left int) ([]Part, bool) {
	tail := 0
	if left > 0 {
		tail = utf8.RuneCountInString(Separator) + utf8.RuneCountInString(moreTitle(left))
	}
	room := func(p []Part) int { return MaxWidth - tail - Width(append(append([]Part(nil), parts...), p...)) }
	typ := shortType(v.Type, maxType)
	names := shorterNames(v.Name, root(v))
	for _, name := range names {
		if p := valueParts(v, name, typ); room(p) >= 0 {
			return p, true
		}
	}
	if !v.Entry || v.Type == "" {
		return nil, false
	}
	shortest := names[len(names)-1]
	n := room(valueParts(v, shortest, ""))
	if n < utf8.RuneCountInString("{…}") {
		return nil, false
	}
	return valueParts(v, shortest, shortType(v.Type, n)), true
}

// valueParts are the parts of v, its name written as name and its type as
// typ: "name: dyn · Give it a type" for a dyn value no entry names, and
// "name: typ · Edit" then "Remove" for one an entry names, typ being dyn when
// the entry does not apply.
func valueParts(v Value, name, typ string) []Part {
	arg := []any{v.Target}
	if !v.Entry {
		return []Part{{Title: name + ": " + dyn + dot + give, Command: GiveType, Arguments: arg}}
	}
	if v.Type == "" {
		typ = dyn
	}
	return []Part{
		{Title: name + ": " + typ + dot + edit, Command: EditType, Arguments: arg},
		{Title: remove, Command: RemoveType, Arguments: arg},
	}
}

// more is the part that stands for the values rest, which the line leaves
// out: "+N more", whose command lists each one's parts, with whole names.
func more(rest []Value) Part {
	var items []any
	for _, v := range rest {
		arg := []any{v.Target}
		if !v.Entry {
			items = append(items, Part{Title: v.Name + ": " + dyn + dot + give, Command: GiveType, Arguments: arg})
			continue
		}
		typ := v.Name + ": " + dyn
		if v.Type != "" {
			typ = v.Name + ": " + shortType(v.Type, maxType)
		}
		items = append(items,
			Part{Title: typ + dot + edit, Command: EditType, Arguments: arg},
			Part{Title: typ + dot + remove, Command: RemoveType, Arguments: arg})
	}
	return Part{Title: moreTitle(len(rest)), Command: ListValues, Arguments: items}
}

func moreTitle(n int) string { return "+" + strconv.Itoa(n) + " more" }

// root is the part of the name of v that a cut keeps: the policy variable
// it is in, such as params or variables.max, or else its first name, such as
// self.
func root(v Value) string {
	if r := v.Target.Variable; r != "" && (v.Name == r || strings.HasPrefix(v.Name, r+".") || strings.HasPrefix(v.Name, r+"[")) {
		return r
	}
	if i := strings.IndexAny(v.Name, ".["); i > 0 {
		return v.Name[:i]
	}
	return v.Name
}

// shorterNames returns name, then name cut in the middle after its root at
// each dot, shortest last: object.spec.template.name, object.…template.name,
// object.…name. A name that is its root, or one more name, is not cut.
func shorterNames(name, root string) []string {
	out := []string{name}
	rest, ok := strings.CutPrefix(name, root+".")
	if !ok {
		return out
	}
	for i, r := range rest {
		if r == '.' && i > 0 && i+1 < len(rest) {
			out = append(out, root+".…"+rest[i+1:])
		}
	}
	return out
}

// shortType writes the type t, as a .cellens entry or the API server writes
// it, in at most limit characters: a struct by its field names, as
// {name, replicas}, with "…" for those that do not fit, and anything longer
// cut with "…". A text that is no .cellens type, such as a kind, is only cut.
func shortType(t string, limit int) string {
	parsed, err := cellens.ParseType(t)
	if err != nil {
		return cut(t, limit)
	}
	if parsed.Name == "struct" {
		return fieldNames(parsed.Fields, limit)
	}
	return cut(brief(parsed), limit)
}

// brief writes t with each struct in it by its field names.
func brief(t *cellens.Type) string {
	switch t.Name {
	case "list":
		return "list(" + brief(t.Elem) + ")"
	case "map":
		return "map(" + t.Key.String() + ", " + brief(t.Elem) + ")"
	case "struct":
		names := make([]string, len(t.Fields))
		for i, f := range t.Fields {
			names[i] = f.Name
		}
		return "{" + strings.Join(names, ", ") + "}"
	}
	return t.Name
}

// fieldNames writes the names of fields as {a, b}, in at most limit
// characters: {a, …} when not all fit.
func fieldNames(fields []cellens.Field, limit int) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	all := "{" + strings.Join(names, ", ") + "}"
	if utf8.RuneCountInString(all) <= limit {
		return all
	}
	b := "{"
	for _, n := range names {
		next := b + n + ", "
		if utf8.RuneCountInString(next+"…}") > limit {
			break
		}
		b = next
	}
	if b == "{" {
		return cut(all, limit-1) + "}"
	}
	return b + "…}"
}

// cut returns s, or its first limit-1 characters and "…" when s is longer
// than limit.
func cut(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:max(limit-1, 0)]) + "…"
}
