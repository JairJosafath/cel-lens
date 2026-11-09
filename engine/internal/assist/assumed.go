package assist

import (
	"fmt"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

const (
	// assumedKey is the extension of every schema placed from an assumed type:
	// the sentence hover adds, which says where the type is written.
	assumedKey = "x-cel-lens-assumed"
	// assumedTypeKey is the extension of the root of a placed type: the type
	// as the .cellens writes it, which hover shows.
	assumedTypeKey = "x-cel-lens-type"
)

// WithAssumedTypes makes the Document assume the types in types where the
// values they name are dyn, as check.WithAssumedTypes does: the variables and
// fields there get the type, and hover says where it is written. A value
// Kubernetes types keeps its type. The types are read, not changed.
func WithAssumedTypes(types []check.AssumedType) Option {
	return func(d *Document) { d.assumed = types }
}

// marked returns the schema of t, copied, with every schema in it marked as
// placed from t, and its root with the name of t.
func marked(t check.AssumedType) map[string]any {
	s := assume.Copy(t.Schema)
	from := t.From
	if from == "" {
		from = fmt.Sprintf("%s, line %d", t.Source, t.Line)
	}
	mark(s, "Typed from "+from+": Kubernetes leaves this value dyn.")
	if t.Type != "" {
		s[assumedTypeKey] = t.Type
	}
	return s
}

func mark(s map[string]any, note string) {
	s[assumedKey] = note
	props, _ := s["properties"].(map[string]any)
	for _, p := range props {
		if m, ok := p.(map[string]any); ok {
			mark(m, note)
		}
	}
	for _, key := range []string{"items", "additionalProperties"} {
		if m, ok := s[key].(map[string]any); ok {
			mark(m, note)
		}
	}
}

// assumedFor returns the types assumed for root, a decoded document: those
// whose kind and name are its kind and metadata.name.
func (d *Document) assumedFor(root map[string]any) []check.AssumedType {
	if len(d.assumed) == 0 {
		return nil
	}
	kind, _ := root["kind"].(string)
	meta, _ := root["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	var out []check.AssumedType
	for _, t := range d.assumed {
		if t.Kind == kind && t.Name == name && name != "" {
			out = append(out, t)
		}
	}
	return out
}

// assumeInCRD places types into the schemas of the versions of root, a
// decoded CustomResourceDefinition, where the values they name are dyn.
func assumeInCRD(root map[string]any, types []check.AssumedType) {
	crdSpec, _ := root["spec"].(map[string]any)
	versions, _ := crdSpec["versions"].([]any)
	for _, t := range types {
		for _, v := range versions {
			ver, _ := v.(map[string]any)
			if name, _ := ver["name"].(string); name != t.Version || t.Variable != "" {
				continue
			}
			sch, _ := ver["schema"].(map[string]any)
			open, _ := sch["openAPIV3Schema"].(map[string]any)
			if open == nil {
				continue
			}
			if placed, err := assume.At(open, "", t.Path, marked(t), assume.Rules); err == nil {
				sch["openAPIV3Schema"] = placed
			}
		}
	}
}

// assumeInPolicy places types into vars, the variables of a policy, where the
// values they name are dyn. A type placed at the root of a variable names it.
func assumeInPolicy(vars []variable, types []check.AssumedType) {
	for _, t := range types {
		name, path := t.Variable, t.Path
		if v, ok := strings.CutPrefix(name, "variables."); ok {
			name, path = "variables", append([]string{v}, path...)
		}
		for i := range vars {
			if vars[i].name != name {
				continue
			}
			placed, err := assume.At(vars[i].schema, name, path, marked(t), assume.Policies)
			if err != nil {
				continue
			}
			vars[i].schema = placed
			if len(path) == 0 {
				vars[i].typeName = t.Type
				vars[i].desc, _, _ = strings.Cut(vars[i].desc, " It is dyn")
			}
		}
	}
}

// assumedType returns the name of the type of s: the assumed type's own name
// at the root of one, else the CEL type of s.
func assumedType(s schema) string {
	if t := s.str(assumedTypeKey); t != "" {
		return t
	}
	return s.name()
}

// withAssumed adds to desc the sentence that says where the type of s is
// written, when s comes from an assumed type.
func withAssumed(desc string, s schema) string {
	note := s.str(assumedKey)
	switch {
	case note == "":
		return desc
	case desc == "":
		return note
	}
	return desc + "\n\n" + note
}
