// Package yamlfield finds the YAML field that made decoding into a Go type
// fail with a *json.UnmarshalTypeError.
//
// encoding/json reports the path of the field by the JSON names of the struct
// fields only: it leaves out list indexes and map keys. The search therefore
// walks the YAML alongside the Go type, a field at a time, and tries the items
// of a list or the entries of a map only where the type has one. Each node is
// visited once for each step of the path it can stand at, so the work is
// linear in the size of the document.
package yamlfield

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Field is a YAML field that cannot be decoded into its Go type.
type Field struct {
	// Node is the node to point at: the key of a mapping entry, the value for
	// an item of a list, or an alias that is the value of a mapping entry.
	Node *yaml.Node
	// Value is the value that has the wrong type, as the author wrote it: the
	// alias node when the value comes from an alias.
	Value *yaml.Node
	// Path is the Kubernetes-style path of the field, such as
	// "spec.versions[1].schema.openAPIV3Schema.properties[size].type".
	Path string
	// Expected is the kind of value the Go type takes: "a string", "an
	// integer", "a number", "a boolean", "an object" or "a list".
	Expected string
	// Got describes the value found, such as "a string" or "the number 3.5".
	Got string
}

// Find locates the field that err, from decoding the YAML document root into
// out (a pointer), is about. It reports false when the document has no field
// that fits err. The search stops when ctx is cancelled and returns its error.
// out is only used for its type.
func Find(ctx context.Context, root *yaml.Node, out any, err *json.UnmarshalTypeError) (Field, bool, error) {
	w := &walker{
		ctx:     ctx,
		want:    kindOf(err.Value),
		numeric: strings.HasPrefix(err.Value, "number "),
		segs:    strings.Split(err.Field, "."),
		seen:    map[visit]bool{},
	}
	if err.Field == "" {
		w.segs = nil
	}
	return w.at(nil, nil, root, reflect.TypeOf(out), 0, "")
}

var unmarshaler = reflect.TypeFor[json.Unmarshaler]()

type visit struct {
	node *yaml.Node
	typ  reflect.Type
	step int
}

type walker struct {
	ctx     context.Context
	want    string // the JSON kind of the value, from the error
	numeric bool   // the error is about a number that does not fit, not a wrong kind
	segs    []string
	seen    map[visit]bool
}

// at looks for the field below node n, decoded into type t, with segs[step:]
// of the error path left. pos is the node to point at for n, or nil for n.
// alias is the first alias the route went through, if any: a problem found
// below it is on the alias, where the author wrote the value, not on its anchor.
func (w *walker) at(alias, pos, n *yaml.Node, t reflect.Type, step int, path string) (Field, bool, error) {
	if err := w.ctx.Err(); err != nil {
		return Field{}, false, err
	}
	if pos == nil {
		pos = n
	}
	if alias == nil && n != nil && n.Kind == yaml.AliasNode {
		alias = n
	}
	n = resolve(n)
	for n != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if n == nil || w.seen[visit{n, t, step}] {
		return Field{}, false, nil
	}
	w.seen[visit{n, t, step}] = true

	if step == len(w.segs) && w.mismatch(n, t) {
		value := n
		if alias != nil {
			pos = alias
			value = alias
		}
		return Field{pos, value, path, expected(t), got(n)}, true, nil
	}
	switch {
	case t.Kind() == reflect.Struct && reflect.PointerTo(t).Implements(unmarshaler):
		// A type with its own decoder, such as a schema that is a schema or a
		// list of schemas, shows its contents without a name in the path.
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() && (f.Type.Kind() == reflect.Pointer || f.Type.Kind() == reflect.Slice) {
				if fld, ok, err := w.at(alias, pos, n, f.Type, step, path); ok || err != nil {
					return fld, ok, err
				}
			}
		}
	case (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && n.Kind == yaml.SequenceNode:
		for i, item := range n.Content {
			if fld, ok, err := w.at(alias, nil, item, t.Elem(), step, fmt.Sprintf("%s[%d]", path, i)); ok || err != nil {
				return fld, ok, err
			}
		}
	case t.Kind() == reflect.Map && n.Kind == yaml.MappingNode:
		// encoding/json reports the first error in key order.
		entries := pairs(n)
		slices.SortFunc(entries, func(a, b pair) int { return strings.Compare(a.key.Value, b.key.Value) })
		for _, e := range entries {
			if fld, ok, err := w.at(alias, e.key, e.value, t.Elem(), step, fmt.Sprintf("%s[%s]", path, e.key.Value)); ok || err != nil {
				return fld, ok, err
			}
		}
	case t.Kind() == reflect.Struct && n.Kind == yaml.MappingNode && step < len(w.segs):
		name := w.segs[step]
		ft, ok := fieldType(t, name)
		if !ok {
			return Field{}, false, nil
		}
		k, v := lookup(n, name)
		if k == nil {
			return Field{}, false, nil
		}
		if path != "" {
			name = "." + name
		}
		return w.at(alias, k, v, ft, step+1, path+name)
	}
	return Field{}, false, nil
}

// mismatch reports whether n is the value the error is about: a value of the
// kind the error names that type t does not take.
func (w *walker) mismatch(n *yaml.Node, t reflect.Type) bool {
	if nodeKind(n) != w.want || reflect.PointerTo(t).Implements(unmarshaler) {
		return false
	}
	kind := jsonKind(t)
	switch {
	case kind == "":
		return false
	case kind == "number":
		return w.want != "number" || w.numeric
	case kind == "array" && t.Elem().Kind() == reflect.Uint8:
		return w.want != "array" && w.want != "string"
	}
	return kind != w.want
}

// jsonKind is the JSON kind that Go type t takes, or "" for a type that takes
// any.
func jsonKind(t reflect.Type) string {
	k := t.Kind()
	switch {
	case k == reflect.String:
		return "string"
	case k == reflect.Bool:
		return "bool"
	case isInteger(k) || k == reflect.Float32 || k == reflect.Float64:
		return "number"
	case k == reflect.Struct || k == reflect.Map:
		return "object"
	case k == reflect.Slice || k == reflect.Array:
		return "array"
	}
	return ""
}

func isInteger(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Uintptr
}

// kindOf is the JSON kind that the Value of a json.UnmarshalTypeError starts
// with.
func kindOf(value string) string {
	kind, _, _ := strings.Cut(value, " ")
	return kind
}

// nodeKind is the JSON kind of the value that YAML node n becomes.
func nodeKind(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "object"
	case yaml.SequenceNode:
		return "array"
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!int", "!!float":
			return "number"
		case "!!bool":
			return "bool"
		case "!!null":
			return "null"
		default:
			return "string"
		}
	case yaml.DocumentNode, yaml.AliasNode:
	}
	return "string"
}

func expected(t reflect.Type) string {
	switch kind := jsonKind(t); {
	case kind == "string":
		return "a string"
	case kind == "bool":
		return "a boolean"
	case kind == "number" && isInteger(t.Kind()):
		return "an integer"
	case kind == "number":
		return "a number"
	case kind == "array":
		return "a list"
	}
	return "an object"
}

func got(n *yaml.Node) string {
	switch nodeKind(n) {
	case "number":
		return "the number " + n.Value
	case "bool":
		return "a boolean"
	case "object":
		return "an object"
	case "array":
		return "a list"
	}
	return "a string"
}

// fieldType finds the Go type of the field of struct type t with JSON name
// name, looking inside embedded structs.
func fieldType(t reflect.Type, name string) (reflect.Type, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case tag == "-" && opts == "":
		case tag == "" && (f.Anonymous || strings.Contains(opts, "inline")):
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if found, ok := fieldType(ft, name); ok {
					return found, true
				}
			}
		case tag == "" && f.IsExported() && f.Name == name, tag == name:
			return f.Type, true
		}
	}
	return nil, false
}

type pair struct{ key, value *yaml.Node }

// lookup returns the key and value of name in mapping n, or nils. A key
// merged in with "<<" counts; an explicit key wins over a merged one.
func lookup(n *yaml.Node, name string) (k, v *yaml.Node) {
	for _, p := range pairs(n) {
		if p.key.Value == name {
			return p.key, p.value
		}
	}
	for _, p := range pairs(n) {
		if strings.EqualFold(p.key.Value, name) {
			return p.key, p.value
		}
	}
	return nil, nil
}

// pairs lists the entries of mapping n, with those merged in with "<<" after
// its own, each key once. The result is new.
func pairs(n *yaml.Node) []pair {
	var out []pair
	seen := map[string]bool{}
	collect(n, map[*yaml.Node]bool{}, seen, &out)
	return out
}

func collect(n *yaml.Node, visited map[*yaml.Node]bool, seen map[string]bool, out *[]pair) {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode || visited[n] {
		return
	}
	visited[n] = true
	var merges []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch {
		case k.ShortTag() == "!!merge":
			merges = append(merges, v)
		case !seen[k.Value]:
			seen[k.Value] = true
			*out = append(*out, pair{k, v})
		}
	}
	for _, m := range merges {
		if m = resolve(m); m != nil && m.Kind == yaml.SequenceNode {
			for _, src := range m.Content {
				collect(src, visited, seen, out)
			}
			continue
		}
		collect(m, visited, seen, out)
	}
}

// resolve follows an alias to the node it stands for and unwraps a document.
func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && (n.Kind == yaml.AliasNode || n.Kind == yaml.DocumentNode) {
		switch {
		case n.Kind == yaml.AliasNode:
			n = n.Alias
		case len(n.Content) > 0:
			n = n.Content[0]
		default:
			return nil
		}
	}
	return n
}
