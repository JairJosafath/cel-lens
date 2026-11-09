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
	// inferredKey is the extension of the schema of a policy variable whose
	// type follows from assumed types, which no entry names: its type, as the
	// line above a rule shows it. inferredFromKey is where the first of
	// those types is written: {source, line}, as check.AssumedType has them.
	inferredKey     = "x-cel-lens-inferred"
	inferredFromKey = "x-cel-lens-inferred-from"
)

// WithAssumedTypes makes the Document assume the types in types where the
// values they name are dyn, as check.WithAssumedTypes does: the variables and
// fields there get the type, and hover says where it is written. A policy
// variable that reads them gets the type its expression returns with them
// (see check.VariableTypes). A value Kubernetes types keeps its type. The
// types are read, not changed.
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

// inferred returns s, the schema of the policy variable v, copied, marked as
// inferred from the types its type follows from (v.Assumed): hover says
// which, and where they are written.
func inferred(s map[string]any, v check.Variable) map[string]any {
	out := assume.Copy(s)
	from := make([]string, len(v.Assumed))
	for i, t := range v.Assumed {
		where := t.From
		if where == "" {
			where = fmt.Sprintf("%s, line %d", t.Source, t.Line)
		}
		from[i] = assume.Name(t.Variable, t.Path) + ", typed from " + where
	}
	mark(out, "Its type follows from "+strings.Join(from, ", and ")+": Kubernetes leaves this value dyn.")
	out[inferredKey] = v.Type
	if len(v.Assumed) > 0 {
		out[inferredFromKey] = map[string]any{"source": v.Assumed[0].Source, "line": v.Assumed[0].Line}
	}
	return out
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
