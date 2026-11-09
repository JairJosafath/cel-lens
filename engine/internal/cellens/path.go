package cellens

import (
	"fmt"
	"strings"
)

// StepKind says what one Step of a Path does.
type StepKind int

const (
	// FieldStep steps into the field Step.Name of an object.
	FieldStep StepKind = iota
	// ItemsStep steps into the items of a list, written [].
	ItemsStep
	// ValuesStep steps into the values of a map, written [*].
	ValuesStep
)

// Step is one step of a Path.
type Step struct {
	Kind StepKind
	// Name is the field's name, for a FieldStep.
	Name string
}

// Path leads from the root of the object a rule or variable sees to a value
// in it, such as spec.ports[].config or metadata.labels[*]. The empty Path is
// the root itself.
type Path []Step

// ParsePath reads a path: field names separated by dots, [] for a list's
// items and [*] for a map's values. The empty string is the root.
func ParsePath(s string) (Path, error) {
	var path Path
	rest := s
	for rest != "" {
		if len(path) > 0 && rest[0] == '.' {
			rest = rest[1:]
		} else if len(path) > 0 && rest[0] != '[' {
			return nil, fmt.Errorf("want . or [ after %q", path.String())
		}
		switch {
		case strings.HasPrefix(rest, "[*]"):
			path, rest = append(path, Step{Kind: ValuesStep}), rest[3:]
		case strings.HasPrefix(rest, "[]"):
			path, rest = append(path, Step{Kind: ItemsStep}), rest[2:]
		case rest == "" || rest[0] == '.' || rest[0] == '[':
			return nil, fmt.Errorf("a field name is missing after %q: write name, [] or [*]", path.String())
		default:
			end := strings.IndexAny(rest, ".[]")
			if end < 0 {
				end = len(rest)
			}
			if rest[end:] != "" && rest[end] == ']' {
				return nil, fmt.Errorf("unexpected ] after %q: write [] or [*]", rest[:end])
			}
			path, rest = append(path, Step{Kind: FieldStep, Name: rest[:end]}), rest[end:]
		}
	}
	return path, nil
}

// String writes the path as ParsePath reads it.
func (p Path) String() string {
	var b strings.Builder
	for i, s := range p {
		switch s.Kind {
		case FieldStep:
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s.Name)
		case ItemsStep:
			b.WriteString("[]")
		case ValuesStep:
			b.WriteString("[*]")
		}
	}
	return b.String()
}
