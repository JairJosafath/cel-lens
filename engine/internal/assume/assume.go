// Package assume puts a type into an OpenAPI schema where Kubernetes leaves
// the value dyn, as an entry of a .cellens file asks.
//
// Schemas are maps in the form YAML decodes a CustomResourceDefinition's
// schema to, the form pkg/check and internal/assist read. A path leads from
// the root of a schema to a value, one step at a time: a field name steps into
// the properties of an object, Items into the items of a list and Values into
// the additionalProperties of a map. Placing a type never changes the schema
// it is given: At returns a new one.
package assume

import (
	"fmt"
	"maps"
	"strings"
)

// The steps of a path that are no field name.
const (
	// Items steps into the items of a list, as [] in a .cellens path.
	Items = "[]"
	// Values steps into the values of a map, as [*] in a .cellens path.
	Values = "[*]"
)

// Reason says why At placed no type.
type Reason int

// The reasons At places no type.
const (
	// NoField: an object on the path has no field of the step's name.
	NoField Reason = iota
	// NotList: the path steps into the items of a value that is no list.
	NotList
	// NotMap: the path steps into the values of a value that is no map.
	NotMap
	// BelowDyn: a value before the end of the path is dyn, so what is below it is not known.
	BelowDyn
	// Typed: the value already has a type.
	Typed
	// Unreadable: in Rules, the value keeps unknown fields, which the API
	// server lets no rule of a CustomResourceDefinition read.
	Unreadable
	// NotIntOrString: in Rules, the value is x-kubernetes-int-or-string,
	// which a rule sees as an int or a string, and the type is neither.
	NotIntOrString
)

// Mode says which values are dyn, as the CEL of a kind of document sees them.
type Mode int

// The modes.
const (
	// Policies is the mode of an admission policy's variables, which the API
	// server reads whole at run time: a value with no type, or an object that
	// keeps unknown fields and declares none, is dyn.
	Policies Mode = iota
	// Rules is the mode of the x-kubernetes-validations rules of a
	// CustomResourceDefinition: only an x-kubernetes-int-or-string value is
	// dyn. The API server gives a rule no unknown field: a value that keeps
	// them with x-kubernetes-preserve-unknown-fields exposes only its declared
	// fields, and one with no type is not there at all (k8s.io/apiserver
	// pkg/cel/common SchemaDeclType).
	Rules
)

// IsDyn reports whether CEL in mode m has no type for a value of schema s.
func (m Mode) IsDyn(s map[string]any) bool {
	if m == Rules {
		intOrString, _ := s["x-kubernetes-int-or-string"].(bool)
		return intOrString
	}
	return IsDyn(s)
}

// Error says why At placed no type, about the value the first Depth steps of
// Path lead to from the root called Root.
type Error struct {
	Reason Reason
	Root   string
	Path   []string
	Depth  int
	// Type is the CEL type of the value, such as object or list(string).
	Type string
}

func (e *Error) Error() string {
	at := Name(e.Root, e.Path[:e.Depth])
	switch e.Reason {
	case NoField:
		msg := fmt.Sprintf("%s has no field %s", at, e.Path[e.Depth])
		if strings.HasPrefix(e.Type, "map(") {
			msg += ": it is a map, write [*] for its values"
		}
		return msg
	case NotList:
		return fmt.Sprintf("%s is %s, not a list: [] steps into a list's items", at, e.Type)
	case NotMap:
		return fmt.Sprintf("%s is %s, not a map: [*] steps into a map's values", at, e.Type)
	case BelowDyn:
		return fmt.Sprintf("%s is dyn, so nothing below it is known: write the entry for %s", at, at)
	case Typed:
		return fmt.Sprintf("%s is already typed as %s: remove this entry", at, e.Type)
	case Unreadable:
		return fmt.Sprintf("the API server lets no rule read %s, which keeps unknown fields: give it a schema in the CustomResourceDefinition instead of this entry", at)
	case NotIntOrString:
		return fmt.Sprintf("%s is x-kubernetes-int-or-string, an int or a string to a rule: give it int or string", at)
	}
	return at
}

// Name writes the value path leads to from the root called root, such as
// params.spec.ports[] or, for a root without a name, spec.ports[]. The root
// itself, without a name, is "the root".
func Name(root string, path []string) string {
	var b strings.Builder
	b.WriteString(root)
	for _, step := range path {
		if step != Items && step != Values && b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(step)
	}
	if b.Len() == 0 {
		return "the root"
	}
	return b.String()
}

// IsDyn reports whether CEL in Policies has no type for a value of schema s:
// s has no type, as with x-kubernetes-preserve-unknown-fields or
// x-kubernetes-int-or-string, or s is an object that keeps unknown fields and
// declares none.
func IsDyn(s map[string]any) bool {
	switch t, _ := s["type"].(string); t {
	case "":
		return true
	case "object":
		keep, _ := s["x-kubernetes-preserve-unknown-fields"].(bool)
		return keep && s["properties"] == nil && s["additionalProperties"] == nil
	}
	return false
}

// TypeName is the CEL type of a value of schema s, as Kubernetes maps OpenAPI
// types to CEL: string, int, double, bool, bytes, timestamp, duration,
// list(T), map(string, T), object, or dyn when IsDyn.
func TypeName(s map[string]any) string {
	if IsDyn(s) {
		return "dyn"
	}
	switch t, _ := s["type"].(string); t {
	case "string":
		switch f, _ := s["format"].(string); f {
		case "date-time", "date":
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
		items, _ := s["items"].(map[string]any)
		return "list(" + TypeName(items) + ")"
	case "object":
		values, _ := s["additionalProperties"].(map[string]any)
		if values != nil && s["properties"] == nil {
			return "map(string, " + TypeName(values) + ")"
		}
		return "object"
	default:
		return t
	}
}

// keptKeys are the keys of a dyn value's schema that At keeps when it places a
// type there: those that say nothing about the type. The others, such as anyOf
// or a default, could contradict the new type.
var keptKeys = []string{"description", "title", "nullable", "x-kubernetes-validations"}

// At returns a copy of root, the schema of the value called rootName, with
// typ placed at the value path leads to. That value must be dyn in mode m,
// and every value before it on the path must have a type, so the path can
// follow it. In Rules the value is x-kubernetes-int-or-string, so typ must be
// an integer or a string without a format: int, uint or string. Of the old value only its description, title, nullable and
// x-kubernetes-validations stay, so its rules still apply. When
// the type cannot be placed, the error is an *Error. Neither root nor typ
// changes; the result shares the parts of both it does not change.
func At(root map[string]any, rootName string, path []string, typ map[string]any, m Mode) (map[string]any, error) {
	return place(root, rootName, path, 0, typ, m)
}

func place(node map[string]any, rootName string, path []string, depth int, typ map[string]any, m Mode) (map[string]any, error) {
	fail := func(r Reason) error {
		return &Error{Reason: r, Root: rootName, Path: path, Depth: depth, Type: TypeName(node)}
	}
	if m == Rules && !m.IsDyn(node) && IsDyn(node) {
		return nil, fail(Unreadable)
	}
	if depth == len(path) {
		if !m.IsDyn(node) {
			return nil, fail(Typed)
		}
		if m == Rules && !intOrString(typ) {
			return nil, fail(NotIntOrString)
		}
		out := maps.Clone(typ)
		for _, k := range keptKeys {
			if v, ok := node[k]; ok {
				out[k] = v
			}
		}
		return out, nil
	}
	if m.IsDyn(node) {
		return nil, fail(BelowDyn)
	}
	t, _ := node["type"].(string)
	var key, field string
	switch step := path[depth]; {
	case step == Items && t == "array":
		key = "items"
	case step == Items:
		return nil, fail(NotList)
	case step == Values && t == "object" && node["additionalProperties"] != nil:
		key = "additionalProperties"
	case step == Values:
		return nil, fail(NotMap)
	default:
		key, field = "properties", step
	}
	sub, _ := node[key].(map[string]any)
	props := sub
	if field != "" {
		sub, _ = props[field].(map[string]any)
	}
	if sub == nil {
		if field == "" {
			return nil, fail(BelowDyn) // a list or map with no schema for its elements
		}
		return nil, fail(NoField)
	}
	placed, err := place(sub, rootName, path, depth+1, typ, m)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(node)
	if field == "" {
		out[key] = placed
		return out, nil
	}
	props = maps.Clone(props)
	props[field] = placed
	out["properties"] = props
	return out, nil
}

// intOrString reports whether a value of schema s is an int or a string to
// CEL: an integer, or a string without a format, which would make it a
// timestamp, duration or bytes.
func intOrString(s map[string]any) bool {
	format, _ := s["format"].(string)
	switch t, _ := s["type"].(string); t {
	case "integer":
		return true
	case "string":
		return format == ""
	}
	return false
}

// Copy returns a deep copy of s: its maps and lists are new, so the copy can
// change without changing s.
func Copy(s map[string]any) map[string]any {
	out := make(map[string]any, len(s))
	for k, v := range s {
		out[k] = copyValue(v)
	}
	return out
}

func copyValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		return Copy(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = copyValue(item)
		}
		return out
	}
	return v
}
