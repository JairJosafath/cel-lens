package cellens

import (
	"fmt"
	"strings"
	"unicode"
)

// Type is a type written in a .cellens file: one of CEL's type names, a list,
// a map or a struct. The zero value is not a valid Type; ParseType makes one.
type Type struct {
	// Name is a scalar's CEL name (string, int, uint, double, bool, bytes,
	// timestamp, duration), or list, map or struct.
	Name string
	// Key is a map's key type; Kubernetes objects only have string keys.
	Key *Type
	// Elem is a list's item type or a map's value type.
	Elem *Type
	// Fields are a struct's fields, in the order written.
	Fields []Field
}

// Field is one field of a struct Type.
type Field struct {
	Name string
	Type *Type
}

// TypeError says where a type string is wrong: Offset counts characters
// (runes) from the start of the string, from 0.
type TypeError struct {
	Offset  int
	Message string
}

func (e *TypeError) Error() string {
	return fmt.Sprintf("character %d: %s", e.Offset+1, e.Message)
}

// scalars maps each scalar CEL type name to the schema the engine uses for it,
// as Kubernetes maps OpenAPI types to CEL.
var scalars = map[string]func() map[string]any{
	"string":    func() map[string]any { return map[string]any{"type": "string"} },
	"int":       func() map[string]any { return map[string]any{"type": "integer"} },
	"uint":      func() map[string]any { return map[string]any{"type": "integer", "minimum": 0} },
	"double":    func() map[string]any { return map[string]any{"type": "number"} },
	"bool":      func() map[string]any { return map[string]any{"type": "boolean"} },
	"bytes":     func() map[string]any { return map[string]any{"type": "string", "format": "byte"} },
	"timestamp": func() map[string]any { return map[string]any{"type": "string", "format": "date-time"} },
	"duration":  func() map[string]any { return map[string]any{"type": "string", "format": "duration"} },
}

// ParseType reads a type such as "{name: string, replicas: int,
// labels: map(string, string), ports: list({port: int})}". The error, when
// there is one, is a *TypeError.
func ParseType(s string) (*Type, error) {
	p := &typeParser{src: []rune(s)}
	t, err := p.parseType()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos < len(p.src) {
		return nil, p.errorf("unexpected %q after the type", string(p.src[p.pos]))
	}
	return t, nil
}

// Schema returns the type as the OpenAPI-style schema map the engine types
// CEL values with: a struct is an object with properties, a map an object with
// additionalProperties, a list an array with items. Each call returns a new map.
func (t *Type) Schema() map[string]any {
	switch t.Name {
	case "list":
		return map[string]any{"type": "array", "items": t.Elem.Schema()}
	case "map":
		return map[string]any{"type": "object", "additionalProperties": t.Elem.Schema()}
	case "struct":
		props := make(map[string]any, len(t.Fields))
		for _, f := range t.Fields {
			props[f.Name] = f.Type.Schema()
		}
		return map[string]any{"type": "object", "properties": props}
	default:
		return scalars[t.Name]()
	}
}

// String writes the type back in its canonical form, which ParseType reads.
func (t *Type) String() string {
	switch t.Name {
	case "list":
		return "list(" + t.Elem.String() + ")"
	case "map":
		return "map(" + t.Key.String() + ", " + t.Elem.String() + ")"
	case "struct":
		fields := make([]string, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = f.Name + ": " + f.Type.String()
		}
		return "{" + strings.Join(fields, ", ") + "}"
	default:
		return t.Name
	}
}

// maxDepth is how deep types may nest; deeper input is refused rather than
// risking the stack.
const maxDepth = 100

type typeParser struct {
	src   []rune
	pos   int
	depth int
}

func (p *typeParser) errorf(format string, args ...any) *TypeError {
	return &TypeError{Offset: p.pos, Message: fmt.Sprintf(format, args...)}
}

func (p *typeParser) skipSpace() {
	for p.pos < len(p.src) && unicode.IsSpace(p.src[p.pos]) {
		p.pos++
	}
}

// peek skips spaces and returns the next character, or 0 at the end.
func (p *typeParser) peek() rune {
	p.skipSpace()
	if p.pos == len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *typeParser) expect(r rune) error {
	if p.peek() != r {
		if p.pos == len(p.src) {
			return p.errorf("the type ends where %q is missing", string(r))
		}
		return p.errorf("want %q, found %q", string(r), string(p.src[p.pos]))
	}
	p.pos++
	return nil
}

func (p *typeParser) parseType() (*Type, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return nil, p.errorf("the type nests deeper than %d levels", maxDepth)
	}
	switch r := p.peek(); {
	case r == 0:
		return nil, p.errorf("a type is missing: write one, such as string or {name: string}")
	case r == '{':
		return p.parseStruct()
	case isNameStart(r):
		return p.parseNamed()
	default:
		return nil, p.errorf("unexpected %q where a type starts", string(r))
	}
}

func (p *typeParser) parseNamed() (*Type, error) {
	start := p.pos
	name := p.name()
	switch name {
	case "list":
		elems, err := p.parseArgs(1)
		if err != nil {
			return nil, err
		}
		return &Type{Name: "list", Elem: elems[0]}, nil
	case "map":
		args, err := p.parseArgs(2)
		if err != nil {
			return nil, err
		}
		if args[0].Name != "string" {
			return nil, &TypeError{Offset: start, Message: "a map's keys are strings in Kubernetes objects: write map(string, " + args[1].String() + ")"}
		}
		return &Type{Name: "map", Key: args[0], Elem: args[1]}, nil
	}
	if _, ok := scalars[name]; !ok {
		return nil, &TypeError{Offset: start, Message: fmt.Sprintf("unknown type %q: use string, int, uint, double, bool, bytes, timestamp, duration, list(T), map(string, T) or {field: T}", name)}
	}
	return &Type{Name: name}, nil
}

// parseArgs reads n types in parentheses, separated by commas.
func (p *typeParser) parseArgs(n int) ([]*Type, error) {
	if err := p.expect('('); err != nil {
		return nil, err
	}
	args := make([]*Type, 0, n)
	for i := range n {
		if i > 0 {
			if err := p.expect(','); err != nil {
				return nil, err
			}
		}
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		args = append(args, t)
	}
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	return args, nil
}

func (p *typeParser) parseStruct() (*Type, error) {
	p.pos++ // the '{'
	t := &Type{Name: "struct"}
	seen := map[string]bool{}
	for p.peek() != '}' {
		if p.pos == len(p.src) {
			return nil, p.errorf("the struct ends where %q is missing", "}")
		}
		if len(t.Fields) > 0 {
			if err := p.expect(','); err != nil {
				return nil, err
			}
			if p.peek() == '}' {
				break // a trailing comma
			}
		}
		f, err := p.parseField(seen)
		if err != nil {
			return nil, err
		}
		t.Fields = append(t.Fields, f)
	}
	p.pos++ // the '}'
	return t, nil
}

func (p *typeParser) parseField(seen map[string]bool) (Field, error) {
	r := p.peek()
	if r == 0 {
		return Field{}, p.errorf("the struct ends where %q is missing", "}")
	}
	if !isNameStart(r) {
		return Field{}, p.errorf("unexpected %q where a field name starts", string(r))
	}
	start := p.pos
	name := p.name()
	if seen[name] {
		return Field{}, &TypeError{Offset: start, Message: fmt.Sprintf("field %q appears twice", name)}
	}
	seen[name] = true
	if err := p.expect(':'); err != nil {
		return Field{}, err
	}
	t, err := p.parseType()
	if err != nil {
		return Field{}, err
	}
	return Field{Name: name, Type: t}, nil
}

// TypeNameAt reports whether the character at, from 0, of the type s, as
// type: writes it, is where the name of a type goes: at its start, after
// "(" or a comma in list(...) or map(...), or after the colon of a struct's
// field; not where a field's name goes. It also returns the name written
// around at, from start to end, which may be empty. s need not parse.
func TypeNameAt(s string, at int) (start, end int, ok bool) {
	src := []rune(s)
	at = min(max(at, 0), len(src))
	start, end = at, at
	for start > 0 && isNamePart(src[start-1]) {
		start--
	}
	for end < len(src) && isNamePart(src[end]) {
		end++
	}
	var open []rune // the brackets open before start
	ok = true
	for _, r := range src[:start] {
		switch {
		case r == '{':
			open, ok = append(open, r), false
		case r == '(':
			open, ok = append(open, r), true
		case r == '}' || r == ')':
			if len(open) > 0 {
				open = open[:len(open)-1]
			}
			ok = false
		case r == ',':
			ok = len(open) > 0 && open[len(open)-1] == '('
		case r == ':':
			ok = len(open) > 0 && open[len(open)-1] == '{'
		case !unicode.IsSpace(r):
			ok = false
		}
	}
	return start, end, ok
}

// name reads a name: a letter or _ or $, then letters, digits, _, $ and -,
// as JSON field names in Kubernetes objects use them.
func (p *typeParser) name() string {
	start := p.pos
	for p.pos < len(p.src) && isNamePart(p.src[p.pos]) {
		p.pos++
	}
	return string(p.src[start:p.pos])
}

func isNameStart(r rune) bool { return unicode.IsLetter(r) || r == '_' || r == '$' }

func isNamePart(r rune) bool { return isNameStart(r) || unicode.IsDigit(r) || r == '-' }
