// Package crdschema reads an OpenAPI v3 schema written as the openAPIV3Schema
// of a CustomResourceDefinition's version, and checks it as the API server
// checks one when the CustomResourceDefinition is created.
//
// Read takes the schema from YAML: every keyword must be one a CRD's schema
// has, with a value of the kind that keyword takes. Validate then runs the API
// server's own validation of a CRD (k8s.io/apiextensions-apiserver), which
// requires a structural schema and refuses what the API server refuses, and
// places each of its errors on the key of the schema it is about.
//
// Arrange puts the keys of a schema in the order the API server writes them,
// and JSON writes a schema as JSON in the order of its keys, for the schemas
// the engine writes.
//
// The schema is that of a value, not of a whole resource: the rules the API
// server has only for the root of a CRD's schema (it must be an object, it may
// not be nullable, its metadata is restricted) do not apply. Nor does a schema
// here carry x-kubernetes-validations: it gives a value a type, and rules
// belong in the CustomResourceDefinition or the policy that reads the value.
package crdschema

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// Problem is something the API server would refuse in a schema: Node is the
// node of the YAML it is about, the key of a mapping entry where there is one.
// Message says what, after the path of the place in the schema, such as
// "properties[max].type: Unsupported value: ...", as the API server words
// it; a problem of the schema's root has no path.
type Problem struct {
	Node    *yaml.Node
	Message string
}

// kind is the kind of value a keyword of a schema takes.
type kind int

const (
	kindString        kind = iota // a string
	kindNumber                    // an integer or a number
	kindInteger                   // an integer
	kindBool                      // a boolean
	kindStrings                   // a list of strings
	kindAny                       // any value: default, example
	kindList                      // a list of any values: enum
	kindSchema                    // a schema
	kindSchemas                   // a list of schemas: allOf, anyOf, oneOf
	kindSchemaMap                 // a mapping of names to schemas: properties
	kindSchemaOrBool              // a schema or a boolean: additionalProperties
	kindSchemaOrList              // a schema or a list of schemas: items
	kindSchemaOrNames             // a mapping of names to a schema or a list of strings: dependencies
	kindDocs                      // externalDocs: description and url
	kindRules                     // x-kubernetes-validations, refused here
)

// keywords are the keys of a CRD's schema, the JSON names of the fields of
// apiextensions.k8s.io/v1 JSONSchemaProps, with the kind of value each takes.
var keywords = map[string]kind{
	"id": kindString, "$schema": kindString, "$ref": kindString, "description": kindString,
	"type": kindString, "format": kindString, "title": kindString, "default": kindAny,
	"maximum": kindNumber, "exclusiveMaximum": kindBool, "minimum": kindNumber, "exclusiveMinimum": kindBool,
	"maxLength": kindInteger, "minLength": kindInteger, "pattern": kindString,
	"maxItems": kindInteger, "minItems": kindInteger, "uniqueItems": kindBool, "multipleOf": kindNumber,
	"enum": kindList, "maxProperties": kindInteger, "minProperties": kindInteger,
	"required": kindStrings, "items": kindSchemaOrList,
	"allOf": kindSchemas, "oneOf": kindSchemas, "anyOf": kindSchemas, "not": kindSchema,
	"properties": kindSchemaMap, "additionalProperties": kindSchemaOrBool,
	"patternProperties": kindSchemaMap, "dependencies": kindSchemaOrNames,
	"additionalItems": kindSchemaOrBool, "definitions": kindSchemaMap,
	"externalDocs": kindDocs, "example": kindAny, "nullable": kindBool,
	"x-kubernetes-preserve-unknown-fields": kindBool, "x-kubernetes-embedded-resource": kindBool,
	"x-kubernetes-int-or-string": kindBool, "x-kubernetes-list-map-keys": kindStrings,
	"x-kubernetes-list-type": kindString, "x-kubernetes-map-type": kindString,
	"x-kubernetes-validations": kindRules,
}

// Read reads the schema in node, a YAML mapping written as a CRD's
// openAPIV3Schema, into the form YAML decodes a CRD's schema to: maps, lists
// and JSON values. It returns a problem for each key that is no keyword of a
// CRD's schema or is written twice, and each value of the wrong kind, such as
// a string for maxLength; with any problem the schema is nil. A YAML alias,
// and so a merge key, is a problem too: Read never follows one, so a schema
// that contains itself, or aliases that multiply, cannot hold it. A scalar
// YAML reads as a timestamp, such as 2020-01-01, is the string written, as the
// API server reads one. Read does not apply the API server's rules: Validate
// does.
func Read(node *yaml.Node) (map[string]any, []Problem) {
	r := reader{}
	r.schema(node, "")
	if len(r.problems) > 0 {
		return nil, r.problems
	}
	js, err := json.Marshal(jsonValue(node))
	if err != nil {
		return nil, []Problem{{Node: node, Message: "the schema is not JSON: " + err.Error()}}
	}
	var out map[string]any
	if err := json.Unmarshal(js, &out); err != nil {
		return nil, []Problem{{Node: node, Message: "the schema is not JSON: " + err.Error()}}
	}
	return out, nil
}

// jsonValue is the value of n, which has no alias, as JSON has it: a mapping
// is a map by its keys as written, a scalar the value of its tag, and a
// timestamp or a scalar of an unknown tag the string written.
func jsonValue(n *yaml.Node) any {
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = jsonValue(n.Content[i+1])
		}
		return m
	case yaml.SequenceNode:
		l := make([]any, len(n.Content))
		for i, item := range n.Content {
			l[i] = jsonValue(item)
		}
		return l
	case yaml.DocumentNode, yaml.AliasNode:
		return nil
	case yaml.ScalarNode:
	}
	switch n.Tag {
	case "!!null":
		return nil
	case "!!int", "!!float", "!!bool":
		var v any
		if n.Decode(&v) == nil {
			return v
		}
	}
	return n.Value
}

// reader collects the problems of a schema as Read walks it.
type reader struct {
	problems []Problem
}

func (r *reader) add(n *yaml.Node, path, msg string) {
	if path != "" {
		msg = path + ": " + msg
	}
	r.problems = append(r.problems, Problem{Node: n, Message: msg})
}

// schema reads the schema n at path.
func (r *reader) schema(n *yaml.Node, path string) {
	if r.alias(n, path) {
		return
	}
	if n.Kind != yaml.MappingNode {
		r.add(n, path, "expected an object, got "+got(n))
		return
	}
	first := map[string]int{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		at := join(path, key.Value)
		k, known := keywords[key.Value]
		switch {
		case key.Tag == "!!merge":
			r.add(key, path, "a merge key (<<) is not taken in a schema: write its keys out")
			continue
		case r.alias(key, path) || r.alias(value, at):
			continue
		case first[key.Value] > 0:
			r.add(key, at, fmt.Sprintf("duplicate field %q (first at line %d)", key.Value, first[key.Value]))
			continue
		case !known:
			r.add(key, path, fmt.Sprintf("unknown field %q", key.Value))
			continue
		}
		first[key.Value] = key.Line
		r.value(key, value, at, k)
	}
}

// alias adds a problem on n when it is an alias, and reports whether it is.
func (r *reader) alias(n *yaml.Node, path string) bool {
	if n.Kind != yaml.AliasNode {
		return false
	}
	r.add(n, path, "a YAML alias (*"+n.Value+") is not taken in a schema: write the schema out")
	return true
}

// isString reports whether the scalar n is a string to JSON: a timestamp is
// the string written, as the API server reads YAML.
func isString(n *yaml.Node) bool { return n.Tag == "!!str" || n.Tag == "!!timestamp" }

// value reads the value of the keyword key, of kind k, at path.
func (r *reader) value(key, v *yaml.Node, path string, k kind) {
	if v.Tag == "!!null" {
		return // null is the keyword left out, as JSON reads it
	}
	switch k {
	case kindString:
		r.want(key, v, path, "a string", isString(v))
	case kindNumber:
		r.want(key, v, path, "a number", v.Tag == "!!int" || v.Tag == "!!float")
	case kindInteger:
		r.want(key, v, path, "an integer", v.Tag == "!!int")
	case kindBool:
		r.want(key, v, path, "a boolean", v.Tag == "!!bool")
	case kindStrings:
		if r.want(key, v, path, "a list of strings", v.Kind == yaml.SequenceNode) {
			for i, item := range v.Content {
				if !r.alias(item, path) {
					r.want(item, item, fmt.Sprintf("%s[%d]", path, i), "a string", isString(item))
				}
			}
		}
	case kindList:
		if r.want(key, v, path, "a list", v.Kind == yaml.SequenceNode) {
			r.noAlias(v, path)
		}
	case kindSchema:
		r.schema(v, path)
	case kindSchemas:
		if r.want(key, v, path, "a list of schemas", v.Kind == yaml.SequenceNode) {
			for i, item := range v.Content {
				r.schema(item, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	case kindSchemaMap:
		r.schemaMap(key, v, path)
	case kindSchemaOrBool:
		if v.Tag != "!!bool" {
			r.schema(v, path)
		}
	case kindSchemaOrList:
		if v.Kind != yaml.SequenceNode {
			r.schema(v, path)
			break
		}
		for i, item := range v.Content {
			r.schema(item, fmt.Sprintf("%s[%d]", path, i))
		}
	case kindSchemaOrNames:
		if !r.want(key, v, path, "an object", v.Kind == yaml.MappingNode) {
			break
		}
		for i := 0; i+1 < len(v.Content); i += 2 {
			name, dep := v.Content[i], v.Content[i+1]
			if r.alias(dep, path) {
				continue
			}
			if dep.Kind == yaml.SequenceNode {
				r.value(name, dep, path+"["+name.Value+"]", kindStrings)
				continue
			}
			r.schema(dep, path+"["+name.Value+"]")
		}
	case kindDocs:
		r.docs(key, v, path)
	case kindRules:
		r.add(key, path, "a .cellens schema gives the value a type: its rules go in the CustomResourceDefinition or the policy that reads it, not here")
	case kindAny:
		r.noAlias(v, path)
	}
}

// noAlias adds a problem for each alias in n, a value of any kind.
func (r *reader) noAlias(n *yaml.Node, path string) {
	if r.alias(n, path) {
		return
	}
	for _, c := range n.Content {
		r.noAlias(c, path)
	}
}

// schemaMap reads a mapping of names to schemas, such as properties.
func (r *reader) schemaMap(key, v *yaml.Node, path string) {
	if !r.want(key, v, path, "an object", v.Kind == yaml.MappingNode) {
		return
	}
	first := map[string]int{}
	for i := 0; i+1 < len(v.Content); i += 2 {
		name := v.Content[i]
		at := path + "[" + name.Value + "]"
		if r.alias(name, path) {
			continue
		}
		if first[name.Value] > 0 {
			r.add(name, at, fmt.Sprintf("duplicate field %q (first at line %d)", name.Value, first[name.Value]))
			continue
		}
		first[name.Value] = name.Line
		r.schema(v.Content[i+1], at)
	}
}

// docs reads externalDocs.
func (r *reader) docs(key, v *yaml.Node, path string) {
	if !r.want(key, v, path, "an object", v.Kind == yaml.MappingNode) {
		return
	}
	for i := 0; i+1 < len(v.Content); i += 2 {
		k, val := v.Content[i], v.Content[i+1]
		if r.alias(val, path) {
			continue
		}
		if k.Value != "description" && k.Value != "url" {
			r.add(k, path, fmt.Sprintf("unknown field %q", k.Value))
			continue
		}
		r.want(k, val, join(path, k.Value), "a string", isString(val) || val.Tag == "!!null")
	}
}

// want adds a problem on key when ok is false: the value v is not what, and
// reports ok.
func (r *reader) want(key, v *yaml.Node, path, what string, ok bool) bool {
	if !ok {
		r.add(key, path, fmt.Sprintf("expected %s, got %s", what, got(v)))
	}
	return ok
}

// got describes the value n for a message, such as "a string" or "the number 1.5".
func got(n *yaml.Node) string {
	switch {
	case n.Kind == yaml.MappingNode:
		return "an object"
	case n.Kind == yaml.SequenceNode:
		return "a list"
	case isString(n):
		return fmt.Sprintf("the string %q", n.Value)
	case n.Tag == "!!int" || n.Tag == "!!float":
		return "the number " + n.Value
	case n.Tag == "!!bool":
		return n.Value
	case n.Tag == "!!null":
		return "null"
	}
	return fmt.Sprintf("%q, of the YAML tag %s", n.Value, n.Tag)
}

// join adds the keyword name to path, as the API server writes field paths.
func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// wrapped is the field path the API server gives the schema Validate checks:
// the property value of the root of a CRD's schema.
const wrapped = "spec.validation.openAPIV3Schema.properties[value]"

// Validate runs the API server's validation of a CustomResourceDefinition on
// one whose schema has schema, read from node by Read, as the schema of a
// field, and returns a problem for each of its errors, on the key of node
// the error is about, or on node when it names none there. ctx is passed to
// that validation, which uses it for the CEL of default values. It runs
// under check.UnderVersion for the Kubernetes version v, as that
// validation reads the version from a setting of the whole process: what
// it says of a schema without rules is the same for every version, and the
// language server passes the version it checks everything else for, so
// the validation never waits for its checks. An empty result means the API
// server would take the schema.
func Validate(ctx context.Context, v kubeversion.Version, node *yaml.Node, schema map[string]any) []Problem {
	props, err := internalSchema(schema)
	if err != nil {
		return []Problem{{Node: node, Message: err.Error()}}
	}
	crd := &apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "values.cel-lens.local"},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:    "cel-lens.local",
			Version:  "v1",
			Scope:    apiextensions.NamespaceScoped,
			Names:    apiextensions.CustomResourceDefinitionNames{Plural: "values", Singular: "value", Kind: "Value", ListKind: "ValueList"},
			Versions: []apiextensions.CustomResourceDefinitionVersion{{Name: "v1", Served: true, Storage: true}},
			Validation: &apiextensions.CustomResourceValidation{OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
				Type:       "object",
				Properties: map[string]apiextensions.JSONSchemaProps{"value": *props},
			}},
			PreserveUnknownFields: new(bool),
		},
		Status: apiextensions.CustomResourceDefinitionStatus{StoredVersions: []string{"v1"}},
	}
	var errs field.ErrorList
	if err := check.UnderVersion(v, func() { errs = validation.ValidateCustomResourceDefinition(ctx, crd) }); err != nil {
		return []Problem{{Node: node, Message: err.Error()}}
	}
	var out []Problem
	for _, fe := range errs {
		rest, ok := strings.CutPrefix(fe.Field, wrapped)
		if !ok {
			// Only an error of the whole schema has no place in it, such as
			// one that it is not structural; its text names the place.
			out = append(out, Problem{Node: node, Message: strings.ReplaceAll(fe.ErrorBody(), "properties[value].", "")})
			continue
		}
		rest = strings.TrimPrefix(rest, ".")
		msg := fe.ErrorBody()
		if rest != "" {
			msg = rest + ": " + msg
		}
		out = append(out, Problem{Node: at(node, rest, msg), Message: msg})
	}
	slices.SortStableFunc(out, func(a, b Problem) int {
		if a.Node.Line != b.Node.Line {
			return a.Node.Line - b.Node.Line
		}
		return a.Node.Column - b.Node.Column
	})
	return out
}

// internalSchema converts schema to the API server's internal JSONSchemaProps,
// as the API server reads a CRD of apiextensions.k8s.io/v1.
func internalSchema(schema map[string]any) (*apiextensions.JSONSchemaProps, error) {
	js, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("the schema is not JSON: %w", err)
	}
	var v1 apiextensionsv1.JSONSchemaProps
	if err := json.Unmarshal(js, &v1); err != nil {
		return nil, fmt.Errorf("the schema cannot be read: %w", err)
	}
	var out apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&v1, &out, nil); err != nil {
		return nil, fmt.Errorf("the schema cannot be read: %w", err)
	}
	return &out, nil
}

// at returns the node of the schema root the field path leads to, as the API
// server writes one ("properties[max].type", "items.enum[0]"): the key of a
// mapping entry or the item of a list. A path that leads past what root has,
// such as to a key that is missing, ends at the extension of that mapping msg,
// the error, names, such as x-kubernetes-embedded-resource for a missing
// properties, or else at the nearest node above it.
func at(root *yaml.Node, path, msg string) *yaml.Node {
	cur, pos := root, root
	for path != "" {
		var step string
		switch path[0] {
		case '.':
			path = path[1:]
			continue
		case '[':
			// A name in brackets may have ] in it: the longest key of cur
			// the path goes on after is the step.
			step = bracketed(cur, path)
			if step == "" {
				end := closing(path)
				if end < 0 {
					return named(cur, pos, msg)
				}
				step = path[1:end]
			}
			path = path[len(step)+2:]
		default:
			end := strings.IndexAny(path, ".[")
			if end < 0 {
				end = len(path)
			}
			step, path = path[:end], path[end:]
		}
		key, next := child(cur, step)
		if next == nil {
			return named(cur, pos, msg)
		}
		cur, pos = next, key
	}
	return pos
}

// bracketed returns the longest key of the mapping n that path, which starts
// with [, has in brackets, or "".
func bracketed(n *yaml.Node, path string) string {
	best := ""
	if n.Kind != yaml.MappingNode {
		return best
	}
	for i := 0; i < len(n.Content); i += 2 {
		if k := n.Content[i].Value; strings.HasPrefix(path, "["+k+"]") && len(k) > len(best) {
			best = k
		}
	}
	return best
}

// named returns the first key of the mapping n that is an x-kubernetes-
// extension msg names, as the one a missing key is required for, or pos when
// it names none.
func named(n, pos *yaml.Node, msg string) *yaml.Node {
	if n.Kind != yaml.MappingNode {
		return pos
	}
	for i := 0; i < len(n.Content); i += 2 {
		if k := n.Content[i]; strings.HasPrefix(k.Value, "x-kubernetes-") && strings.Contains(msg, k.Value) {
			return k
		}
	}
	return pos
}

// closing returns the offset of the ']' that closes the '[' at the start of
// s, counting nested brackets, or -1.
func closing(s string) int {
	depth := 0
	for i := range len(s) {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// child returns the entry step of the mapping n, its key and value, or the
// item at index step of the list n, twice; nil when n has none.
func child(n *yaml.Node, step string) (key, value *yaml.Node) {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == step {
				return n.Content[i], n.Content[i+1]
			}
		}
	}
	if i, err := strconv.Atoi(step); n.Kind == yaml.SequenceNode && err == nil && i >= 0 && i < len(n.Content) {
		return n.Content[i], n.Content[i]
	}
	return nil, nil
}

// order is where each keyword comes in a schema the engine writes: the order
// in which apiextensions.k8s.io/v1 JSONSchemaProps declares its fields, as
// the API server writes a schema.
var order = keywordOrder()

// keywordOrder is the place of each field of JSONSchemaProps, by its JSON
// name, from 0.
func keywordOrder() map[string]int {
	t := reflect.TypeFor[apiextensionsv1.JSONSchemaProps]()
	out := make(map[string]int, t.NumField())
	for i := range t.NumField() {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" {
			out[name] = i
		}
	}
	return out
}

// Arrange returns a copy of n, a schema as Read takes it, with the keys of
// each schema in it in the order apiextensions.k8s.io/v1 JSONSchemaProps
// declares them, as the API server writes a schema: description, type and
// format first, then the limits, required, items, the properties and the
// other schemas below it, and the x-kubernetes- extensions last. A key that
// is no keyword comes after them, in the order written. The names of
// properties, patternProperties, definitions and dependencies keep the order
// written, as do lists and the values of default, enum and example. n is not
// changed; a node that is no mapping is returned as it is.
func Arrange(n *yaml.Node) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return n
	}
	type entry struct{ key, value *yaml.Node }
	entries := make([]entry, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		entries = append(entries, entry{key, arrangeValue(key.Value, n.Content[i+1])})
	}
	rank := func(e entry) int {
		if i, ok := order[e.key.Value]; ok {
			return i
		}
		return len(order)
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return cmp.Compare(rank(a), rank(b)) })
	c := *n
	c.Content = make([]*yaml.Node, 0, len(n.Content))
	for _, e := range entries {
		c.Content = append(c.Content, e.key, e.value)
	}
	return &c
}

// arrangeValue is v, the value of the keyword key, with the schemas in it
// arranged (see Arrange).
func arrangeValue(key string, v *yaml.Node) *yaml.Node {
	k, known := keywords[key]
	if !known {
		return v
	}
	switch k {
	case kindSchema, kindSchemaOrBool:
		return Arrange(v)
	case kindSchemas, kindSchemaOrList:
		if v.Kind != yaml.SequenceNode {
			return Arrange(v)
		}
		return each(v, 1, Arrange)
	case kindSchemaMap, kindSchemaOrNames:
		if v.Kind != yaml.MappingNode {
			return v
		}
		return each(v, 2, Arrange) // a list of names in dependencies is no mapping, and stays
	case kindString, kindNumber, kindInteger, kindBool, kindStrings, kindAny, kindList, kindDocs, kindRules:
		// no schema in it: its order is the one written
	}
	return v
}

// each returns a copy of n whose every step-th child, from the step-th, is
// f of it: every item of a list for 1, every value of a mapping for 2.
func each(n *yaml.Node, step int, f func(*yaml.Node) *yaml.Node) *yaml.Node {
	c := *n
	c.Content = slices.Clone(n.Content)
	for i := step - 1; i < len(c.Content); i += step {
		c.Content[i] = f(c.Content[i])
	}
	return &c
}

// JSON writes n, a schema as Read takes it, as JSON: the keys of each
// mapping in the order written, each scalar as Read reads it. The error is
// for a scalar JSON cannot write, such as an infinite number.
func JSON(n *yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	if err := writeJSON(&b, n); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeJSON(b *bytes.Buffer, n *yaml.Node) error {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			b.WriteString("null")
			return nil
		}
		return writeJSON(b, n.Content[0])
	case yaml.MappingNode:
		b.WriteByte('{')
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			key, err := json.Marshal(n.Content[i].Value)
			if err != nil {
				return err
			}
			b.Write(key)
			b.WriteByte(':')
			if err := writeJSON(b, n.Content[i+1]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
		return nil
	case yaml.SequenceNode:
		b.WriteByte('[')
		for i, item := range n.Content {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSON(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	case yaml.ScalarNode, yaml.AliasNode:
		// a scalar is its value; Read takes no alias, and jsonValue makes one null
	}
	v, err := json.Marshal(jsonValue(n))
	if err != nil {
		return err
	}
	b.Write(v)
	return nil
}
