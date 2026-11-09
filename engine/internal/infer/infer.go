// Package infer works out an OpenAPI v3 schema from example values, in the
// form of a CustomResourceDefinition's openAPIV3Schema as YAML decodes it: the
// form of a .cellens schema: entry, which a person then refines.
//
// The schema is conservative: Kubernetes would accept every example given,
// and the API server would accept the schema in a CustomResourceDefinition.
// Objects get properties, lists items merged over their elements, true and
// false boolean, whole numbers integer, other numbers number, and strings
// string. Values that disagree across the examples are merged the way
// Kubernetes accepts both, or left without a type, with a note.
package infer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

const (
	// MaxBytes is the size of the largest text Schema reads, in bytes: that
	// of the largest workspace file the language server reads.
	MaxBytes = 1 << 20
	// MaxDepth is how many objects and lists an example nests at most, as a
	// .cellens type does.
	MaxDepth = 100
	// MaxValues is how many values the examples of a text hold at most, once
	// their aliases are expanded: twice what 1 MiB of YAML holds without. A
	// text holds at most ten times the nodes it writes, and 10,000 in any
	// case.
	MaxValues = 1_000_000
)

// The errors Schema returns, wrapped with what was wrong and where.
var (
	// ErrTooBig is for a text over MaxBytes, or examples over the values
	// MaxValues allows.
	ErrTooBig = errors.New("the text is too big")
	// ErrTooDeep is for an example that nests deeper than MaxDepth.
	ErrTooDeep = errors.New("an example nests too deep")
	// ErrSyntax is for a text that is neither YAML nor JSON, or holds what
	// JSON cannot, such as a key written twice.
	ErrSyntax = errors.New("the text does not parse")
	// ErrNoExample is for a text with no example in it, or none at the line
	// or path asked.
	ErrNoExample = errors.New("no example")
)

// Options change what Schema works out. The zero value infers from every
// example of the text, whole, and lists no field in required.
type Options struct {
	// Required lists in an object's required the fields every example of
	// that object has.
	Required bool
	// Line, when not 0, takes only the document of the text at that line,
	// from 1: from the "---" at or above it to the next one. What the other
	// documents hold, even YAML that does not parse, does not count.
	Line int
	// Path, when not empty, takes the value at that path in each example,
	// one step each: a field name, assume.Items for a list's items or
	// assume.Values for a map's values. An example with no value there
	// counts for nothing.
	Path []string
}

// Result is the schema Schema works out, and what it could not settle.
type Result struct {
	// Schema is an OpenAPI v3 schema as YAML decodes a
	// CustomResourceDefinition's openAPIV3Schema: maps with string keys,
	// []any, strings and bools. JSON writes its keys sorted.
	Schema map[string]any `json:"schema"`
	// Notes say where the examples did not settle a type, each starting
	// with the value's path, such as "spec.items: an empty list says nothing
	// about its items", in the order of the paths; empty, never nil, when
	// there are none.
	Notes []string `json:"notes"`
}

// root names the value itself in notes, where its path is empty.
const root = "the value"

// Schema works out the schema of the examples in text, YAML or JSON with
// one or more documents. A document that is a list of objects, or a v1 List,
// gives each of its items as an example; an empty document gives none. At
// the root of an example taken whole, apiVersion and kind are typed string
// and metadata object, as a CustomResourceDefinition writes them, whatever
// the example holds there: Kubernetes types metadata as ObjectMeta. With
// Options.Path only the value there counts, so the rest of each example, its
// status too, is left out.
//
// The error wraps ErrTooBig, ErrSyntax (with the line), ErrTooDeep (with the
// line), ErrNoExample, or ctx's error when ctx is cancelled.
func Schema(ctx context.Context, text []byte, opts Options) (Result, error) {
	if len(text) > MaxBytes {
		return Result{}, fmt.Errorf("%w: it is %d bytes, over the limit of %d", ErrTooBig, len(text), MaxBytes)
	}
	text = extract.UTF8(text)
	if opts.Line > 0 {
		text = documentAt(text, opts.Line)
	}
	docs, broken, err := extract.Documents(ctx, "", text)
	if err != nil {
		return Result{}, err
	}
	if len(broken) > 0 {
		return Result{}, fmt.Errorf("%w: line %d: %s", ErrSyntax, broken[0].Line, broken[0].Reason)
	}
	dec := newDecoder(docs)
	var examples []any
	for _, d := range docs {
		v, err := dec.value(ctx, d.Node, 0)
		if err != nil {
			return Result{}, err
		}
		examples = append(examples, unwrap(v)...)
	}
	examples = at(examples, opts.Path)
	switch {
	case len(examples) > 0:
	case len(opts.Path) > 0:
		return Result{}, fmt.Errorf("%w: no example has a value at %s", ErrNoExample, pathString(opts.Path))
	case opts.Line > 0:
		return Result{}, fmt.Errorf("%w at line %d: its document has no value", ErrNoExample, opts.Line)
	default:
		return Result{}, fmt.Errorf("%w: the text has no document with a value", ErrNoExample)
	}
	in := inferrer{required: opts.Required}
	var top shape
	for _, ex := range examples {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		in.add(&top, ex, "", len(opts.Path) == 0)
	}
	res := Result{Notes: []string{}}
	res.Schema = in.schema(&top, "", &res.Notes)
	return res, nil
}

// documentAt returns text with only the document at line, from 1, left: the
// lines from the "---" at or above it to the next "---", and the lines above
// those blanked, so each keeps its number.
func documentAt(text []byte, line int) []byte {
	lines := bytes.SplitAfter(text, []byte("\n"))
	start, end := 0, len(lines)
	for i, l := range lines {
		if !isSeparator(l) {
			continue
		}
		if i+1 > line {
			end = i
			break
		}
		start = i
	}
	out := bytes.Repeat([]byte("\n"), start)
	for _, l := range lines[start:end] {
		out = append(out, l...)
	}
	return out
}

// isSeparator reports whether the line l starts a YAML document.
func isSeparator(l []byte) bool {
	l = bytes.TrimRight(l, "\r\n")
	return bytes.Equal(l, []byte("---")) || bytes.HasPrefix(l, []byte("--- ")) || bytes.HasPrefix(l, []byte("---\t"))
}

// unwrap returns the examples a document v gives: the items of a list of
// objects or of a v1 List, nothing for an empty document, else v.
func unwrap(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		if len(t) > 0 && !slices.ContainsFunc(t, func(item any) bool { _, ok := item.(map[string]any); return !ok }) {
			return t
		}
	case map[string]any:
		if plain(t["apiVersion"]) == "v1" && plain(t["kind"]) == "List" {
			if items, ok := plain(t["items"]).([]any); ok {
				return items
			}
		}
	}
	return []any{v}
}

// at returns the values at path in each of examples.
func at(examples []any, path []string) []any {
	for _, step := range path {
		var next []any
		for _, ex := range examples {
			switch step {
			case assume.Items:
				if list, ok := ex.([]any); ok {
					next = append(next, list...)
				}
			case assume.Values:
				if m, ok := ex.(map[string]any); ok {
					for _, k := range slices.Sorted(maps.Keys(m)) {
						next = append(next, plain(m[k]))
					}
				}
			default:
				if m, ok := ex.(map[string]any); ok {
					if v, ok := m[step]; ok {
						next = append(next, plain(v))
					}
				}
			}
		}
		examples = next
	}
	return examples
}

// decoder makes values of YAML nodes as Kubernetes reads them: objects with
// string keys, lists, and scalars as yaml.v3 decodes them. Unlike yaml.v3's
// own decoding, it finds a key written twice in time linear in the keys. It
// decodes a node with an anchor once, and gives the same value to each alias
// and merge of it, and it counts the values the examples hold with their
// aliases expanded, which must stay within limit. newDecoder makes one.
type decoder struct {
	values, limit int
	// anchored are the values of the nodes with an anchor decoded so far.
	anchored map[*yaml.Node]decoded
}

// decoded is the value of a node, how many values it holds with its aliases
// expanded, and how many objects and lists deep it nests.
type decoded struct {
	v            any
	size, height int
}

// newDecoder returns a decoder for docs: the values they hold may be ten
// times the nodes they write, at least 10,000 and at most MaxValues.
func newDecoder(docs []extract.Document) *decoder {
	nodes := 0
	var count func(n *yaml.Node)
	count = func(n *yaml.Node) {
		nodes++
		for _, c := range n.Content {
			count(c)
		}
	}
	for _, d := range docs {
		count(d.Node)
	}
	return &decoder{limit: min(max(10*nodes, 10_000), MaxValues), anchored: map[*yaml.Node]decoded{}}
}

// yaml11Bool is a plain string that YAML 1.1, which kubectl reads, takes for
// a boolean, such as yes or off.
type yaml11Bool string

// yaml11Key is the value of a key YAML 1.1, which kubectl reads, takes for a
// boolean, such as on: kubectl writes the key true or false.
type yaml11Key struct {
	key   string
	value any
}

// plain is v without the yaml11Key around it.
func plain(v any) any {
	if k, ok := v.(yaml11Key); ok {
		return k.value
	}
	return v
}

// isYAML11Bool reports whether s is a plain string that YAML 1.1 reads as a
// boolean and YAML 1.2 as a string.
func isYAML11Bool(s string) bool {
	switch s {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON",
		"n", "N", "no", "No", "NO", "off", "Off", "OFF":
		return true
	}
	return false
}

// yaml11True reports whether YAML 1.1 reads s, a string isYAML11Bool
// accepts, as true.
func yaml11True(s string) bool {
	switch strings.ToLower(s) {
	case "y", "yes", "on":
		return true
	}
	return false
}

// isYAML11BoolNode reports whether n is a plain scalar that YAML 1.1 reads as
// a boolean and YAML 1.2 as a string.
func isYAML11BoolNode(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Style == 0 && n.ShortTag() == "!!str" && isYAML11Bool(n.Value)
}

// value is the value of the node n, depth objects and lists deep.
func (d *decoder) value(ctx context.Context, n *yaml.Node, depth int) (any, error) {
	v, _, err := d.node(ctx, n, depth)
	return v, err
}

// ctxEvery is how many values the decoder makes between two looks at its
// context.
const ctxEvery = 4096

// count adds n values to those the examples hold, and every ctxEvery values
// returns ctx's error, so a cancelled decoding stops.
func (d *decoder) count(ctx context.Context, n int) error {
	before := d.values
	d.values += n
	if d.values > d.limit {
		return fmt.Errorf("%w: the examples hold more than %d values once their aliases are expanded, ten times what the text writes", ErrTooBig, d.limit)
	}
	if d.values/ctxEvery != before/ctxEvery {
		return ctx.Err()
	}
	return nil
}

// node is the value of the node n, depth objects and lists deep, and how
// many objects and lists deep it nests.
func (d *decoder) node(ctx context.Context, n *yaml.Node, depth int) (any, int, error) {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if c, ok := d.anchored[n]; ok {
		if depth+c.height > MaxDepth {
			return nil, 0, tooDeep(n, depth+c.height-1)
		}
		return c.v, c.height, d.count(ctx, c.size)
	}
	start := d.values
	var v any
	height := 0
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, 0, nil
		}
		return d.node(ctx, n.Content[0], depth)
	case yaml.ScalarNode:
		if err := d.count(ctx, 1); err != nil {
			return nil, 0, err
		}
		if isYAML11BoolNode(n) {
			v = yaml11Bool(n.Value)
			break
		}
		if err := n.Decode(&v); err != nil {
			return nil, 0, fmt.Errorf("%w: line %d: %w", ErrSyntax, n.Line, err)
		}
	case yaml.SequenceNode, yaml.MappingNode:
		if depth >= MaxDepth {
			return nil, 0, tooDeep(n, depth)
		}
		if err := d.count(ctx, 1); err != nil {
			return nil, 0, err
		}
		var err error
		if n.Kind == yaml.SequenceNode {
			v, height, err = d.sequence(ctx, n, depth)
		} else {
			v, height, err = d.mapping(ctx, n, depth)
		}
		if err != nil {
			return nil, 0, err
		}
	case yaml.AliasNode: // resolved above: an anchor never names an alias
		return nil, 0, fmt.Errorf("%w: line %d: an alias of an alias", ErrSyntax, n.Line)
	default:
		return nil, 0, fmt.Errorf("%w: line %d: a node of kind %d", ErrSyntax, n.Line, n.Kind)
	}
	if n.Anchor != "" {
		d.anchored[n] = decoded{v, d.values - start, height}
	}
	return v, height, nil
}

func tooDeep(n *yaml.Node, depth int) error {
	return fmt.Errorf("%w: line %d is %d lists and objects deep, over the limit of %d", ErrTooDeep, n.Line, depth+1, MaxDepth)
}

// sequence is the list of the sequence n, depth deep, and its height.
func (d *decoder) sequence(ctx context.Context, n *yaml.Node, depth int) ([]any, int, error) {
	out := make([]any, 0, len(n.Content))
	height := 1
	for _, item := range n.Content {
		v, h, err := d.node(ctx, item, depth+1)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
		height = max(height, h+1)
	}
	return out, height, nil
}

// mapping is the object of the mapping n, depth deep, and its height. A key
// written twice is an error; the keys a merge key (<<) brings count only
// where the mapping, or a mapping merged before, does not have them, as YAML
// merges.
func (d *decoder) mapping(ctx context.Context, n *yaml.Node, depth int) (map[string]any, int, error) {
	out := make(map[string]any, len(n.Content)/2)
	lines := make(map[string]int, len(n.Content)/2)
	height := 1
	var merges []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
			merges = append(merges, v)
			continue
		}
		key, err := d.key(k)
		if err != nil {
			return nil, 0, err
		}
		if first, twice := lines[key]; twice {
			return nil, 0, fmt.Errorf("%w: line %d: the key %q is written twice, at line %d first", ErrSyntax, k.Line, key, first)
		}
		lines[key] = k.Line
		val, h, err := d.node(ctx, v, depth+1)
		if err != nil {
			return nil, 0, err
		}
		height = max(height, h+1)
		out[key] = val
		for k.Kind == yaml.AliasNode {
			k = k.Alias
		}
		if isYAML11BoolNode(k) {
			out[key] = yaml11Key{key, val}
		}
	}
	for _, m := range merges {
		h, err := d.merge(ctx, out, m, depth)
		if err != nil {
			return nil, 0, err
		}
		height = max(height, h)
	}
	return out, height, nil
}

// merge adds to out the keys of the mapping, or the list of mappings, m
// merges that out does not have yet, and returns the height of the deepest
// mapping merged.
func (d *decoder) merge(ctx context.Context, out map[string]any, m *yaml.Node, depth int) (int, error) {
	for m.Kind == yaml.AliasNode {
		m = m.Alias
	}
	sources := []*yaml.Node{m}
	if m.Kind == yaml.SequenceNode {
		sources = m.Content
	}
	height := 0
	for _, src := range sources {
		for src.Kind == yaml.AliasNode {
			src = src.Alias
		}
		if src.Kind != yaml.MappingNode {
			return 0, fmt.Errorf("%w: line %d: << merges only an object or a list of objects", ErrSyntax, src.Line)
		}
		v, h, err := d.node(ctx, src, depth)
		if err != nil {
			return 0, err
		}
		merged, _ := v.(map[string]any) // the value of a mapping is an object
		for k, v := range merged {
			if _, ok := out[k]; !ok {
				out[k] = v
			}
		}
		height = max(height, h)
	}
	return height, nil
}

// key is the key k as JSON writes it: a key that is no string is written as
// YAML prints it.
func (d *decoder) key(k *yaml.Node) (string, error) {
	for k.Kind == yaml.AliasNode {
		k = k.Alias
	}
	if k.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("%w: line %d: a key that is a list or an object, which JSON has not", ErrSyntax, k.Line)
	}
	if k.ShortTag() == "!!str" {
		return k.Value, nil
	}
	var v any
	if err := k.Decode(&v); err != nil {
		return "", fmt.Errorf("%w: line %d: %w", ErrSyntax, k.Line, err)
	}
	switch v := v.(type) {
	case string:
		return v, nil
	case nil:
		return "null", nil
	case time.Time:
		return k.Value, nil
	}
	return fmt.Sprint(v), nil
}

// The kinds of value, which a shape holds as a set, one bit each.
const (
	kindNull = 1 << iota
	kindBoolean
	kindInteger
	kindNumber
	kindString
	kindObject
	kindList
)

// shape is what the examples hold at one place: the kinds of value, and for
// objects and lists what they hold in turn.
type shape struct {
	kinds uint8
	// fields are the fields of the objects seen here, and present how many
	// of the objects had each; objects counts the objects.
	fields  map[string]*shape
	present map[string]int
	objects int
	// items are the items of the lists seen here, nil while none had one.
	items *shape
	// fixed is the schema Kubernetes gives this place, which the values do
	// not change; nil for one the values decide.
	fixed func() map[string]any
	// yaml11 is the first plain string seen here that kubectl reads as a
	// boolean, "" when none; yaml11Key is the key of this field when kubectl
	// reads it as a boolean.
	yaml11, yaml11Key string
}

// inferrer adds examples to shapes and makes their schemas.
type inferrer struct {
	required bool
}

// add adds the value v at path to s. top is true for the root of an example
// taken whole, whose apiVersion, kind and metadata get the schemas a
// CustomResourceDefinition gives them.
func (in *inferrer) add(s *shape, v any, path string, top bool) {
	switch t := v.(type) {
	case nil:
		s.kinds |= kindNull
	case bool:
		s.kinds |= kindBoolean
	case yaml11Bool:
		s.kinds |= kindString
		if s.yaml11 == "" {
			s.yaml11 = string(t)
		}
	case string, time.Time, []byte:
		s.kinds |= kindString
	case int, int64:
		s.kinds |= kindInteger
	case uint64:
		s.kinds |= numberKind(t <= math.MaxInt64, float64(t))
	case float64:
		s.kinds |= numberKind(false, t)
	case []any:
		s.kinds |= kindList
		for _, item := range t {
			if s.items == nil {
				s.items = &shape{}
			}
			in.add(s.items, item, path+assume.Items, false)
		}
	case map[string]any:
		s.kinds |= kindObject
		s.objects++
		for _, k := range slices.Sorted(maps.Keys(t)) {
			f := s.field(k)
			s.present[k]++
			v := t[k]
			if w, ok := v.(yaml11Key); ok {
				f.yaml11Key, v = w.key, w.value
			}
			switch {
			case top && (k == "apiVersion" || k == "kind"):
				f.fixed = schemaOf("string")
			case top && k == "metadata":
				f.fixed = schemaOf("object")
			default:
				in.add(f, v, join(path, k), false)
			}
		}
	}
}

// numberKind is the kind of a number: integer when the decoder read it as
// one, or when f is whole and within the integers JSON numbers hold
// exactly, as Kubernetes validates an integer; number otherwise.
func numberKind(isInt bool, f float64) uint8 {
	if isInt || f == math.Trunc(f) && math.Abs(f) <= 1<<53 {
		return kindInteger
	}
	return kindNumber
}

// field returns the shape of the field k of the objects s holds.
func (s *shape) field(k string) *shape {
	if s.fields == nil {
		s.fields, s.present = map[string]*shape{}, map[string]int{}
	}
	f := s.fields[k]
	if f == nil {
		f = &shape{}
		s.fields[k] = f
	}
	return f
}

// schema makes the schema of s, at path, and adds to notes what it could
// not settle there and below.
func (in *inferrer) schema(s *shape, path string, notes *[]string) map[string]any {
	if s.yaml11Key != "" {
		b := yaml11True(s.yaml11Key)
		*notes = append(*notes, fmt.Sprintf("%s: the key %s, unquoted, is the boolean %t to kubectl (YAML 1.1), which sends it as the key \"%t\": quote it", name(path), s.yaml11Key, b, b))
	}
	if s.fixed != nil {
		return s.fixed()
	}
	if s.yaml11 != "" {
		*notes = append(*notes, fmt.Sprintf("%s: an example has %s, unquoted, which kubectl reads as a boolean (YAML 1.1): quote it if a string is meant", name(path), s.yaml11))
	}
	out := map[string]any{}
	if s.kinds&kindNull != 0 {
		out["nullable"] = true
	}
	switch k := s.kinds &^ kindNull; k {
	case 0:
		out["x-kubernetes-preserve-unknown-fields"] = true
		*notes = append(*notes, name(path)+": only null was seen, which says nothing about its type")
	case kindBoolean:
		out["type"] = "boolean"
	case kindInteger:
		out["type"] = "integer"
	case kindNumber, kindInteger | kindNumber:
		out["type"] = "number"
	case kindString:
		out["type"] = "string"
	case kindInteger | kindString:
		out["x-kubernetes-int-or-string"] = true
	case kindObject:
		out["type"] = "object"
		if len(s.fields) == 0 {
			out["x-kubernetes-preserve-unknown-fields"] = true
			*notes = append(*notes, name(path)+": an empty object says nothing about its fields")
			break
		}
		props := map[string]any{}
		var required []any
		for _, f := range slices.Sorted(maps.Keys(s.fields)) {
			props[f] = in.schema(s.fields[f], join(path, f), notes)
			if in.required && s.present[f] == s.objects {
				required = append(required, f)
			}
		}
		out["properties"] = props
		if len(required) > 0 {
			out["required"] = required
		}
	case kindList:
		out["type"] = "array"
		if s.items == nil {
			out["items"] = map[string]any{"x-kubernetes-preserve-unknown-fields": true}
			*notes = append(*notes, name(path)+": an empty list says nothing about its items")
			break
		}
		out["items"] = in.schema(s.items, path+assume.Items, notes)
	default:
		out["x-kubernetes-preserve-unknown-fields"] = true
		*notes = append(*notes, fmt.Sprintf("%s: the examples have %s here, which no type Kubernetes accepts holds together: it is left without a type", name(path), describe(k)))
	}
	return out
}

// describe names the kinds of k, "boolean and string", with an integer
// counted as a number when a number is there too.
func describe(k uint8) string {
	if k&kindNumber != 0 {
		k &^= kindInteger
	}
	var names []string
	for _, n := range [...]struct {
		k    uint8
		name string
	}{
		{kindBoolean, "boolean"},
		{kindInteger, "integer"},
		{kindNumber, "number"},
		{kindString, "string"},
		{kindObject, "object"},
		{kindList, "list"},
	} {
		if k&n.k != 0 {
			names = append(names, n.name)
		}
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// join is the path of the field f of the object at path.
func join(path, f string) string {
	if path == "" {
		return f
	}
	return path + "." + f
}

// name is path as notes and errors name it.
func name(path string) string {
	if path == "" {
		return root
	}
	return path
}

// pathString writes path as a .cellens path.
func pathString(path []string) string {
	var b strings.Builder
	for i, step := range path {
		if i > 0 && step != assume.Items && step != assume.Values {
			b.WriteByte('.')
		}
		b.WriteString(step)
	}
	return b.String()
}

// schemaOf returns a function that makes the schema of the type t.
func schemaOf(t string) func() map[string]any {
	return func() map[string]any { return map[string]any{"type": t} }
}
