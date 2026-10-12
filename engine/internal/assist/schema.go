package assist

import (
	"slices"
	"strings"

	"github.com/google/cel-go/common/types"
)

// schema is an OpenAPI v3 schema as YAML decodes it, the form a CRD has it
// in. A nil schema is an unknown type.
type schema map[string]any

// sub returns the schema stored under key, or nil.
func (s schema) sub(key string) schema {
	m, _ := s[key].(map[string]any)
	return m
}

func (s schema) str(key string) string {
	v, _ := s[key].(string)
	return v
}

func (s schema) flag(key string) bool {
	v, _ := s[key].(bool)
	return v
}

// name is the CEL type the API server gives the schema.
func (s schema) name() string {
	if s == nil || s.flag("x-kubernetes-int-or-string") {
		if s != nil {
			return "int|string"
		}
		return "dyn"
	}
	switch s.str("type") {
	case "string":
		switch s.str("format") {
		case "date-time":
			return "timestamp"
		case "duration":
			return "duration"
		case "byte":
			return "bytes"
		}
		return "string"
	case "integer":
		return "int"
	case "number":
		return "double"
	case "boolean":
		return "bool"
	case "array":
		return "list(" + s.sub("items").name() + ")"
	case "object":
		if values := s.sub("additionalProperties"); values != nil && s.sub("properties") == nil {
			return "map(string, " + values.name() + ")"
		}
		return "object"
	}
	return "dyn"
}

// kind is the CEL kind of the schema's type, or types.UnspecifiedKind when
// the type is not known well enough to say which functions apply.
func (s schema) kind() types.Kind {
	name, _, _ := strings.Cut(s.name(), "(")
	switch name {
	case "string":
		return types.StringKind
	case "int":
		return types.IntKind
	case "double":
		return types.DoubleKind
	case "bool":
		return types.BoolKind
	case "bytes":
		return types.BytesKind
	case "timestamp":
		return types.TimestampKind
	case "duration":
		return types.DurationKind
	case "list":
		return types.ListKind
	case "map":
		return types.MapKind
	}
	return types.UnspecifiedKind
}

// field returns the schema of the field called name, or nil.
func (s schema) field(name string) schema {
	if p := s.sub("properties"); p != nil {
		if f := p.sub(name); f != nil {
			return f
		}
	}
	if s.str("type") == "object" {
		return s.sub("additionalProperties")
	}
	return nil
}

// elem returns the schema of the elements of a list or the values of a map,
// or nil.
func (s schema) elem() schema {
	switch s.str("type") {
	case "array":
		return s.sub("items")
	case "object":
		return s.sub("additionalProperties")
	}
	return nil
}

// fieldNames returns the names of the properties, sorted.
func (s schema) fieldNames() []string {
	var names []string
	for n := range s.sub("properties") {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// kindsKey is the extension of a merged schema that lists the names of the
// kinds that have the value, see merge.
const kindsKey = "x-cel-lens-kinds"

// kinds returns the names of the kinds that have the value, when the schema
// is a merge of several, and nil otherwise.
func (s schema) kinds() []string {
	k, _ := s[kindsKey].([]string)
	return k
}

// part is the schema of a value in one kind.
type part struct {
	name   string
	schema schema
}

// merge returns the schema of a value that has the type of any of parts: the
// first part's own keys, the union of the properties, and the merge of the
// items and of the additional properties. Every merged value lists under
// kindsKey the names of the parts that have it, in the order of parts.
func merge(parts []part) schema {
	out := schema{}
	for k, v := range parts[0].schema {
		if k != "properties" && k != "items" && k != "additionalProperties" {
			out[k] = v
		}
	}
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = p.name
	}
	out[kindsKey] = names

	props := map[string]any{}
	var order []string
	for _, p := range parts {
		for _, n := range p.schema.fieldNames() {
			if _, ok := props[n]; !ok {
				props[n] = nil
				order = append(order, n)
			}
		}
	}
	for _, n := range order {
		var have []part
		for _, p := range parts {
			if f := p.schema.sub("properties").sub(n); f != nil {
				have = append(have, part{p.name, f})
			}
		}
		props[n] = map[string]any(merge(have))
	}
	if len(order) > 0 {
		out["properties"] = props
	}
	for _, key := range []string{"items", "additionalProperties"} {
		var have []part
		for _, p := range parts {
			if v := p.schema.sub(key); v != nil {
				have = append(have, part{p.name, v})
			}
		}
		if len(have) > 0 {
			out[key] = map[string]any(merge(have))
		}
	}
	return out
}
