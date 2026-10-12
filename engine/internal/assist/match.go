package assist

import (
	"regexp"
	"strings"
)

// chainText matches an argument that is a variable, fields and indexes.
var chainText = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9.?]*(\[[^\[\]]*\])*(\.\??[A-Za-z_][A-Za-z_0-9]*(\[[^\[\]]*\])*)*$`)

// argTypes returns the CEL type name of each argument of the call whose
// opening parenthesis follows column c of line, "dyn" for one whose type it
// cannot tell. Arguments that run past the line are not read.
func (a *Assistant) argTypes(line []rune, c int, vars []variable) []string {
	for c < len(line) && (line[c] == ' ' || line[c] == '\t') {
		c++
	}
	var out []string
	depth, from := 0, c+1
	quoted, _ := quotedRunes(line, len(line))
	for i := c; i < len(line); i++ {
		r := line[i]
		switch {
		case quoted[i]:
		case r == '(' || r == '[' || r == '{':
			depth++
		case r == ',' && depth == 1:
			out = append(out, a.argType(string(line[from:i]), vars))
			from = i + 1
		case r == ')' || r == ']' || r == '}':
			depth--
			if depth == 0 {
				if text := strings.TrimSpace(string(line[from:i])); text != "" || len(out) > 0 {
					out = append(out, a.argType(text, vars))
				}
				return out
			}
		}
	}
	return []string{"dyn"}
}

// stringLiteral reports whether text is one single-line string literal, raw
// (r'...') or not, triple-quoted or not, with no closing quote inside.
func stringLiteral(text string) bool {
	if text != "" && (text[0] == 'r' || text[0] == 'R') {
		text = text[1:]
	}
	if text == "" || (text[0] != '\'' && text[0] != '"') {
		return false
	}
	q := string(text[0])
	delim := q
	if strings.HasPrefix(text, q+q+q) && len(text) >= 6 {
		delim = q + q + q
	}
	if len(text) < 2*len(delim) || !strings.HasPrefix(text, delim) || !strings.HasSuffix(text, delim) {
		return false
	}
	return !strings.Contains(text[len(delim):len(text)-len(delim)], delim)
}

// argType returns the CEL type name of the argument written text.
func (a *Assistant) argType(text string, vars []variable) string {
	text = strings.TrimSpace(text)
	switch {
	case text == "true" || text == "false":
		return "bool"
	case stringLiteral(text):
		return "string"
	case isInt(text):
		return "int"
	case strings.HasSuffix(text, "u") && isInt(strings.TrimSuffix(text, "u")):
		return "uint"
	case chainText.MatchString(text):
		r := []rune(text + ".")
		chain, ok := chainBefore(r, len(r)-1)
		if !ok {
			return "dyn"
		}
		if r := resolve(vars, chain); r.found && r.known != nil {
			if r.optional {
				return "optional_type(" + r.known.name() + ")"
			}
			return r.known.name()
		}
	case strings.HasSuffix(text, ")"):
		return a.callType(text, vars)
	}
	return "dyn"
}

// callType returns the CEL type name of the call written text, "dyn" when it
// cannot tell: the receiver or the function is not known, or the overloads the
// types select return different types.
func (a *Assistant) callType(text string, vars []variable) string {
	runes := []rune(text)
	open := openingBefore(runes, len(runes)-1)
	if open < 0 {
		return "dyn"
	}
	callee := runes[:open]
	name := len(callee)
	for name > 0 && identRune(callee[name-1]) {
		name--
	}
	if name == len(callee) {
		return "dyn"
	}
	fn := string(callee[name:])
	args := a.argTypes(runes, open, vars)
	member := name > 0 && callee[name-1] == '.'
	var overloads []overload
	if member {
		recv := string(callee[:name-1])
		r := []rune(recv + ".")
		if chain, ok := chainBefore(r, len(r)-1); ok && chainText.MatchString(recv) {
			if ns := namespace(chain, vars); ns != "" {
				member, fn = false, ns+"."+fn
			}
		}
		if member {
			t := a.argType(recv, vars)
			if unknown(t) {
				return "dyn"
			}
			if (fn == "optMap" || fn == "optFlatMap") && strings.HasPrefix(t, "optional_type(") {
				return "optional_type(dyn)"
			}
			args = append([]string{t}, args...)
		}
	} else if name > 0 {
		return "dyn"
	}
	f, ok := a.byName[fn]
	if !ok {
		return "dyn"
	}
	if member {
		for _, m := range f.members {
			overloads = append(overloads, m.overload)
		}
	} else {
		overloads = f.global
	}
	result := ""
	for i, p := range pick(overloads, args) {
		if !p {
			continue
		}
		r := overloads[i].result
		if bound := map[string]string{}; len(args) == len(overloads[i].params) {
			for j, param := range overloads[i].params {
				bind(param, args[j], bound)
			}
			r = substitute(r, bound)
		}
		if result == "" {
			result = r
		} else if r != result {
			return "dyn"
		}
	}
	if result == "" || unknown(result) {
		return "dyn"
	}
	return result
}

// bind records in bound the type each type parameter in param, such as <V>,
// stands for when a value of the type named actual is given to it.
func bind(param, actual string, bound map[string]string) {
	if strings.HasPrefix(param, "<") && strings.HasSuffix(param, ">") {
		if _, ok := bound[param]; !ok && !unknown(actual) {
			bound[param] = actual
		}
		return
	}
	ph, pargs := splitType(param)
	ah, aargs := splitType(actual)
	if ph != ah || len(pargs) != len(aargs) {
		return
	}
	for i := range pargs {
		bind(pargs[i], aargs[i], bound)
	}
}

// substitute replaces the type parameters in t by the types bound gives them.
func substitute(t string, bound map[string]string) string {
	for param, typ := range bound {
		t = strings.ReplaceAll(t, param, typ)
	}
	return t
}

// quotedRunes reports, for each rune of line[:n], whether it is inside a string
// literal or is one of its quotes, and whether line[:n] ends inside a string.
// A raw string (r'...') has no escapes, so a backslash does not hide its closing
// quote. A triple-quoted string (”'...”') ends only at three quotes in a row,
// so single quotes of the same kind inside it do not close it.
func quotedRunes(line []rune, n int) (in []bool, open bool) {
	in = make([]bool, n)
	var quote rune
	raw := false
	width := 0 // quotes that open and close the string: 1 or 3
	for i := 0; i < n && i < len(line); i++ {
		r := line[i]
		switch {
		case quote != 0 && r == '\\' && !raw:
			in[i] = true
			if i+1 < n {
				in[i+1] = true
			}
			i++
		case quote != 0 && r == quote && closesAt(line, i, width):
			for j := i; j < i+width && j < n; j++ {
				in[j] = true
			}
			i += width - 1
			quote = 0
		case quote != 0:
			in[i] = true
		case r == '"' || r == '\'':
			quote = r
			raw = rawPrefix(line, i)
			width = 1
			if i+2 < len(line) && line[i+1] == r && line[i+2] == r {
				width = 3
			}
			for j := i; j < i+width && j < n; j++ {
				in[j] = true
			}
			i += width - 1
		}
	}
	return in, quote != 0
}

// closesAt reports whether the quote at line[i] starts the width quotes that
// end a string.
func closesAt(line []rune, i, width int) bool {
	if i+width > len(line) {
		return false
	}
	for _, r := range line[i : i+width] {
		if r != line[i] {
			return false
		}
	}
	return true
}

// rawPrefix reports whether the quote at line[q] opens a raw string: it follows
// an r or R that does not end a longer name.
func rawPrefix(line []rune, q int) bool {
	if q == 0 || (line[q-1] != 'r' && line[q-1] != 'R') {
		return false
	}
	p := q - 1
	if p > 0 && (line[p-1] == 'b' || line[p-1] == 'B') {
		p--
	}
	return p == 0 || !identRune(line[p-1])
}

// openingBefore returns the column of the bracket that closes at line[c], or
// -1 when there is none on the line. Brackets inside string literals are skipped.
func openingBefore(line []rune, c int) int {
	depth := 0
	quoted, _ := quotedRunes(line, c+1)
	for i := c; i >= 0; i-- {
		if quoted[i] {
			continue
		}
		switch line[i] {
		case ')', ']', '}':
			depth++
		case '(', '[', '{':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// receiverBefore returns the text of the receiver that ends just before the dot
// at line[dot]: names, dots, calls and indexes, up to a space or an operator.
func receiverBefore(line []rune, dot int) string {
	p := dot
	for p > 0 {
		switch r := line[p-1]; {
		case r == ')' || r == ']':
			open := openingBefore(line, p-1)
			if open < 0 {
				return string(line[p:dot])
			}
			p = open
		case identRune(r) || r == '.' || r == '?':
			p--
		default:
			return string(line[p:dot])
		}
	}
	return string(line[:dot])
}

// withLoopVars returns vars and the loop variables of the macros whose
// arguments contain column col of line, outermost first, typed from the range
// each loops over.
func withLoopVars(line []rune, col int, vars []variable) []variable {
	var open []int
	quoted, _ := quotedRunes(line, col)
	for i := 0; i < col && i < len(line); i++ {
		switch {
		case quoted[i]:
		case line[i] == '(':
			open = append(open, i)
		case line[i] == ')' && len(open) > 0:
			open = open[:len(open)-1]
		}
	}
	for _, i := range open {
		vars = append(vars, loopVars(line, i, col, vars)...)
	}
	return vars
}

// twoVarMacro reports whether name is a macro that takes two loop variables, as
// in all(k, v, p).
func twoVarMacro(name string) bool {
	switch name {
	case "all", "exists", "exists_one", "existsOne", "transformList", "transformMap", "transformMapEntry":
		return true
	}
	return false
}

// loopMacro reports whether name is a macro that takes a loop variable.
func loopMacro(name string) bool {
	switch name {
	case "map", "filter", "sortBy", "optMap", "optFlatMap":
		return true
	}
	return twoVarMacro(name)
}

// loopHead matches the loop variables that open a macro's arguments.
var loopHead = regexp.MustCompile(`^\(\s*([A-Za-z_][A-Za-z_0-9]*)\s*,(?:\s*([A-Za-z_][A-Za-z_0-9]*)\s*,)?`)

// loopVars returns the loop variables of the macro whose argument list opens at
// line[open], or none when the parenthesis is not a macro's. Their types come
// from the range the macro is called on when that is a chain of fields. col is
// the cursor's column; before the variables are declared there are none.
func loopVars(line []rune, open, col int, vars []variable) []variable {
	name := open
	for name > 0 && identRune(line[name-1]) {
		name--
	}
	if name == 0 || name == open || line[name-1] != '.' || !loopMacro(string(line[name:open])) {
		return nil
	}
	m := loopHead.FindStringSubmatch(string(line[open:]))
	if len(m) == 0 || col < open+len(m[0]) {
		return nil
	}
	if m[2] != "" && !twoVarMacro(string(line[name:open])) {
		m[2] = ""
	}
	var rng schema
	if text := receiverBefore(line, name-1); chainText.MatchString(text) {
		r := []rune(text + ".")
		if chain, ok := chainBefore(r, len(r)-1); ok {
			if res := resolve(vars, chain); res.found {
				rng = res.known
			}
		}
	}
	elem, first := rng.elem(), schema{"type": "integer"}
	if rng.str("type") == "object" {
		first = schema{"type": "string"}
	}
	if name := string(line[name:open]); name == "optMap" || name == "optFlatMap" {
		// The variable is the value inside the optional the macro is called on.
		return []variable{{name: m[1], schema: rng}}
	}
	if m[2] == "" {
		// A single variable is the element of a list and the key of a map.
		if rng.str("type") == "array" {
			first = elem
		}
		return []variable{{name: m[1], schema: first}}
	}
	return []variable{{name: m[1], schema: first}, {name: m[2], schema: elem}}
}

func isInt(s string) bool {
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// pick returns which of overloads the types actual select: those every
// parameter of which accepts the matching type, or only the ones with no
// wildcard parameter where an exact one exists. Nothing is picked when no
// type of actual is known.
func pick(overloads []overload, actual []string) []bool {
	picked := make([]bool, len(overloads))
	if len(actual) > 0 && !slicesAnyKnown(actual) {
		return picked
	}
	exact := false
	loose := make([]bool, len(overloads))
	for i, o := range overloads {
		if len(o.params) != len(actual) {
			continue
		}
		ok := true
		for j, p := range o.params {
			if !fits(p, actual[j]) {
				ok = false
				break
			}
			if unknown(p) && !unknown(actual[j]) {
				loose[i] = true
			}
		}
		picked[i] = ok
		exact = exact || ok && !loose[i]
	}
	if exact {
		for i := range picked {
			picked[i] = picked[i] && !loose[i]
		}
	}
	return picked
}

func slicesAnyKnown(actual []string) bool {
	for _, a := range actual {
		if !unknown(a) {
			return true
		}
	}
	return false
}

// unknown reports whether the type name t stands for any type: dyn, a type
// parameter such as <A>, or a type that cannot be told from the schema.
func unknown(t string) bool {
	return t == "dyn" || t == "any" || t == "object" || strings.HasPrefix(t, "<") || strings.Contains(t, "|")
}

// fits reports whether a value of the type named actual can be given to a
// parameter of the type named param.
func fits(param, actual string) bool {
	if unknown(param) || unknown(actual) {
		return true
	}
	ph, pargs := splitType(param)
	ah, aargs := splitType(actual)
	if ph != ah {
		return false
	}
	if len(pargs) != len(aargs) {
		return true
	}
	for i := range pargs {
		if !fits(pargs[i], aargs[i]) {
			return false
		}
	}
	return true
}

// splitType splits "map(string, list(int))" into "map" and its arguments.
func splitType(t string) (string, []string) {
	head, rest, ok := strings.Cut(t, "(")
	if !ok || !strings.HasSuffix(rest, ")") {
		return t, nil
	}
	rest = strings.TrimSuffix(rest, ")")
	var args []string
	depth, from := 0, 0
	for i, r := range rest {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(rest[from:i]))
				from = i + 1
			}
		}
	}
	return head, append(args, strings.TrimSpace(rest[from:]))
}
