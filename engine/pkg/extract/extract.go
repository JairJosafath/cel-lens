// Package extract finds the CEL expressions embedded in Kubernetes YAML files
// and reports where each one sits in the file.
//
// It knows two kinds of resource: CustomResourceDefinitions (the rule and
// messageExpression of x-kubernetes-validations, at any depth of
// openAPIV3Schema) and ValidatingAdmissionPolicies. Other documents are
// ignored.
package extract

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Kind says which field an expression was found in.
type Kind string

// The kinds of expression.
const (
	// KindRule is the rule of an x-kubernetes-validations entry.
	KindRule Kind = "rule"
	// KindMessageExpression is a messageExpression field.
	KindMessageExpression Kind = "messageExpression"
	// KindExpression is an expression field: a validation, match condition or variable.
	KindExpression Kind = "expression"
	// KindValueExpression is the valueExpression of an audit annotation.
	KindValueExpression Kind = "valueExpression"
)

// Position is a place in a file. Line and Column count from 1; Column counts
// characters (runes), not bytes.
type Position struct {
	Line   int
	Column int
}

// Expression is one CEL expression found in a file.
type Expression struct {
	// Text is the expression as YAML decodes it. For a block scalar it
	// follows the YAML chomping rules, so it may end in a newline.
	Text string
	// Kind is the field the expression came from.
	Kind Kind
	// File is the name passed to Find.
	File string
	// Start is the position of the first character of the text in the file.
	// For block scalars it is the first character of the first non-blank
	// content line, so Text may begin with newlines before it. For quoted
	// scalars it is the first character after the opening quote.
	Start Position
	// End is the position just after the last character of the text in the
	// file, before any closing quote. Start and End cover the source as
	// written, so for a double-quoted scalar with escapes Text (decoded) is
	// shorter than the range.
	End Position
	// SchemaPath locates, for a CRD, the schema the rule is attached to,
	// starting at openAPIV3Schema. It is empty for the root and otherwise
	// dot-separated keys such as "properties.spec.properties.ports.items".
	// It is always empty for admission policies.
	SchemaPath string
	// Document is the index, from 0, of the YAML document in the file.
	Document int
	// FieldPath is the Kubernetes field path of the expression within its
	// document, the way the API server writes it in validation errors, such
	// as "spec.validations[0].expression" or
	// "spec.versions[0].schema.openAPIV3Schema.properties[spec].x-kubernetes-validations[1].rule".
	FieldPath string
}

// Find returns every CEL expression in data, a YAML file with any number of
// documents, in file order. file is only recorded in the results. Documents
// that are not CRDs or ValidatingAdmissionPolicies add nothing, so the result
// is empty and the error nil for them, and for empty input.
//
// A document that does not parse adds nothing, and the documents after it are
// still read. Find then returns the expressions found together with a
// *SyntaxError for each such document, joined. When ctx is cancelled, Find
// returns ctx's error.
//
// A schema reached through YAML aliases is visited once, and an expression is
// reported once, so a file cannot make Find do more work than its size.
func Find(ctx context.Context, file string, data []byte) ([]Expression, error) {
	docs, broken, err := Documents(ctx, file, data)
	if err != nil {
		return nil, err
	}
	return FindIn(ctx, file, data, docs, broken)
}

// FindIn is Find for documents already parsed by Documents from data. broken
// is returned, joined, as the error.
func FindIn(ctx context.Context, file string, data []byte, docs []Document, broken []*SyntaxError) ([]Expression, error) {
	f := finder{file: file, lines: splitLines(data)}
	errs := make([]error, len(broken))
	for i, b := range broken {
		errs[i] = b
	}
	for _, d := range docs {
		if err := ctx.Err(); err != nil {
			return f.found, err
		}
		f.document = d.Index
		if err := f.parse(ctx, d.Node); err != nil {
			return f.found, err
		}
	}
	return f.found, errors.Join(errs...)
}

type finder struct {
	file  string
	lines [][]rune
	found []Expression
	// document is the index of the document being read.
	document int
	// seenSchema holds the schema nodes already visited and seen the
	// expressions already reported, so that aliases are followed once.
	seenSchema map[*yaml.Node]bool
	seen       map[seenKey]bool
	// walked holds the properties maps with every key visited; skipped the
	// entries of the others that a shadowing map kept from being visited, and
	// walking the maps being walked now.
	walked  map[*yaml.Node]bool
	skipped map[*yaml.Node]*skipped
	walking map[*yaml.Node]bool
	// holders lists the shadowing mappings that have a key of their own, built
	// once per mapping (held).
	held    map[*yaml.Node]bool
	holders map[string][]*yaml.Node
	// maps and entries cache what is worked out about mappings, so merge
	// keys are followed once per mapping.
	maps    map[*yaml.Node]*mapping
	entries map[lookupKey]pair
}

type seenKey struct {
	kind  Kind
	start Position
}

func (f *finder) parse(ctx context.Context, doc *yaml.Node) error {
	f.seenSchema = map[*yaml.Node]bool{}
	f.seen = map[seenKey]bool{}
	f.maps = map[*yaml.Node]*mapping{}
	f.entries = map[lookupKey]pair{}
	f.walked = map[*yaml.Node]bool{}
	f.skipped = map[*yaml.Node]*skipped{}
	f.walking = map[*yaml.Node]bool{}
	f.held = map[*yaml.Node]bool{}
	f.holders = map[string][]*yaml.Node{}
	root := resolve(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	apiVersion, err := f.get(ctx, root, "apiVersion")
	if err != nil {
		return err
	}
	kind, err := f.get(ctx, root, "kind")
	if err != nil {
		return err
	}
	spec, err := f.get(ctx, root, "spec")
	if err != nil {
		return err
	}
	group, version, _ := strings.Cut(str(apiVersion), "/")
	switch {
	case group == "apiextensions.k8s.io" && str(kind) == "CustomResourceDefinition":
		return f.crd(ctx, spec)
	case group == "admissionregistration.k8s.io" && str(kind) == "ValidatingAdmissionPolicy":
		return f.policy(ctx, spec)
	case group == "admissionregistration.k8s.io" && str(kind) == "MutatingAdmissionPolicy":
		return f.mutatingPolicy(ctx, spec)
	case group == "policies.kyverno.io" && version == "v1" && str(kind) == "ValidatingPolicy":
		return f.policy(ctx, spec)
	}
	return nil
}

func (f *finder) crd(ctx context.Context, spec *yaml.Node) error {
	// v1 keeps a schema per version; v1beta1 allowed one under spec.validation.
	legacy, err := f.get(ctx, spec, "validation", "openAPIV3Schema")
	if err != nil {
		return err
	}
	if err := f.schema(ctx, legacy, "", "spec.validation.openAPIV3Schema"); err != nil {
		return err
	}
	versions, err := f.get(ctx, spec, "versions")
	if err != nil {
		return err
	}
	if versions = resolve(versions); versions != nil && versions.Kind == yaml.SequenceNode {
		for i, v := range versions.Content {
			s, err := f.get(ctx, v, "schema", "openAPIV3Schema")
			if err != nil {
				return err
			}
			if err := f.schema(ctx, s, "", fmt.Sprintf("spec.versions[%d].schema.openAPIV3Schema", i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// schema visits the schema n, at SchemaPath path and Kubernetes field path
// field.
func (f *finder) schema(ctx context.Context, n *yaml.Node, path, field string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode || f.seenSchema[n] {
		return nil
	}
	f.seenSchema[n] = true
	rules, err := f.get(ctx, n, "x-kubernetes-validations")
	if err != nil {
		return err
	}
	if rules = resolve(rules); rules != nil && rules.Kind == yaml.SequenceNode {
		for i, r := range rules.Content {
			rule := fmt.Sprintf("%s.x-kubernetes-validations[%d]", field, i)
			if err := f.add(ctx, r, "rule", KindRule, path, rule+".rule"); err != nil {
				return err
			}
			if err := f.add(ctx, r, "messageExpression", KindMessageExpression, path, rule+".messageExpression"); err != nil {
				return err
			}
		}
	}
	props, err := f.get(ctx, n, "properties")
	if err != nil {
		return err
	}
	if props != nil {
		if _, err := f.walk(ctx, props, path, field, &taken{names: map[string]bool{}}); err != nil {
			return err
		}
	}
	for _, key := range []string{"items", "additionalProperties"} {
		child, err := f.get(ctx, n, key)
		if err != nil {
			return err
		}
		if err := f.schema(ctx, child, join(path, key), field+"."+key); err != nil {
			return err
		}
	}
	return nil
}

// policy visits the spec of a ValidatingAdmissionPolicy or a Kyverno
// ValidatingPolicy, which keep their CEL in the same fields.
func (f *finder) policy(ctx context.Context, spec *yaml.Node) error {
	return f.policyFields(ctx, spec, []policyField{
		{"validations", "", "expression", KindExpression},
		{"validations", "", "messageExpression", KindMessageExpression},
		{"matchConditions", "", "expression", KindExpression},
		{"variables", "", "expression", KindExpression},
		{"auditAnnotations", "", "valueExpression", KindValueExpression},
	})
}

// mutatingPolicy visits the spec of a MutatingAdmissionPolicy: its match
// conditions, variables and the expression of each mutation.
func (f *finder) mutatingPolicy(ctx context.Context, spec *yaml.Node) error {
	return f.policyFields(ctx, spec, []policyField{
		{"matchConditions", "", "expression", KindExpression},
		{"variables", "", "expression", KindExpression},
		{"mutations", "applyConfiguration", "expression", KindExpression},
		{"mutations", "jsonPatch", "expression", KindExpression},
	})
}

// policyField is where a policy spec keeps CEL: the field of each item of the
// list, inside the mapping sub of the item when there is one.
type policyField struct {
	list, sub, field string
	kind             Kind
}

// policyFields visits the CEL of spec in the given fields.
func (f *finder) policyFields(ctx context.Context, spec *yaml.Node, fields []policyField) error {
	for _, fl := range fields {
		if err := ctx.Err(); err != nil {
			return err
		}
		items, err := f.get(ctx, spec, fl.list)
		if err != nil {
			return err
		}
		if items = resolve(items); items == nil || items.Kind != yaml.SequenceNode {
			continue
		}
		for i, item := range items.Content {
			path := fmt.Sprintf("spec.%s[%d].", fl.list, i)
			if fl.sub != "" {
				sub, err := f.get(ctx, item, fl.sub)
				if err != nil {
					return err
				}
				item, path = sub, path+fl.sub+"."
			}
			if err := f.add(ctx, item, fl.field, fl.kind, "", path+fl.field); err != nil {
				return err
			}
		}
	}
	return nil
}

// add records the value of key in mapping m as an expression when it is a
// non-empty string scalar. It returns ctx's error when ctx is cancelled.
func (f *finder) add(ctx context.Context, m *yaml.Node, key string, kind Kind, path, field string) error {
	k, n, err := f.entry(ctx, m, key)
	if err != nil {
		return err
	}
	n = resolve(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" || n.Value == "" {
		return nil
	}
	start, end, ok := f.span(n, k.Column-1)
	if !ok {
		return nil
	}
	if f.seen[seenKey{kind, start}] {
		return nil
	}
	f.seen[seenKey{kind, start}] = true
	f.found = append(f.found, Expression{
		Text: n.Value, Kind: kind, File: f.file,
		Start: start, End: end, SchemaPath: path,
		Document: f.document, FieldPath: field,
	})
	return nil
}

func join(path string, elems ...string) string {
	if path == "" {
		return strings.Join(elems, ".")
	}
	return path + "." + strings.Join(elems, ".")
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

// get returns the value reached from mapping n through keys, or nil. It
// returns ctx's error when ctx is cancelled.
func (f *finder) get(ctx context.Context, n *yaml.Node, keys ...string) (*yaml.Node, error) {
	for _, key := range keys {
		_, v, err := f.entry(ctx, n, key)
		if err != nil {
			return nil, err
		}
		n = v
	}
	return n, nil
}

// entry returns the key node and value node of key in mapping n, or nils.
// Keys merged in with "<<" are found too; an explicit key wins over a merged
// one, and an earlier mapping in a merge list wins over a later one. Each
// (mapping, key) is worked out once per document, so a mapping merged in by
// many others costs one lookup, not one scan, per mapping. It returns ctx's
// error when ctx is cancelled while the merge sources are followed.
func (f *finder) entry(ctx context.Context, n *yaml.Node, key string) (k, v *yaml.Node, err error) {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil, nil
	}
	lk := lookupKey{n, key}
	if p, ok := f.entries[lk]; ok {
		return p.key, p.value, nil
	}
	// A mapping that merges itself, directly or not, finds nothing through
	// the cycle.
	f.entries[lk] = pair{}
	m := f.index(n)
	p, ok := m.own[key]
	for i := 0; !ok && i < len(m.merges); i++ {
		for _, src := range mergeSources(m.merges[i]) {
			if err := ctx.Err(); err != nil {
				delete(f.entries, lk)
				return nil, nil, err
			}
			k, v, err := f.entry(ctx, src, key)
			if err != nil {
				delete(f.entries, lk)
				return nil, nil, err
			}
			if k != nil {
				p, ok = pair{k, v}, true
				break
			}
		}
	}
	f.entries[lk] = p
	return p.key, p.value, nil
}

// mergeSources lists the mappings a "<<" value stands for: the value itself,
// or the items of a sequence.
func mergeSources(v *yaml.Node) []*yaml.Node {
	if v = resolve(v); v != nil && v.Kind == yaml.SequenceNode {
		return v.Content
	}
	return []*yaml.Node{v}
}

type lookupKey struct {
	mapping *yaml.Node
	key     string
}

type pair struct{ key, value *yaml.Node }

// mapping is a mapping node split into its own keys and its merge values.
type mapping struct {
	own    map[string]pair
	merges []*yaml.Node
}

// index returns the own keys and merges of mapping n, built on first use.
// For a repeated key the first one wins.
func (f *finder) index(n *yaml.Node) *mapping {
	if m, ok := f.maps[n]; ok {
		return m
	}
	m := &mapping{own: make(map[string]pair, len(n.Content)/2)}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.ShortTag() == "!!merge" {
			m.merges = append(m.merges, v)
		} else if _, dup := m.own[k.Value]; !dup {
			m.own[k.Value] = pair{k, v}
		}
	}
	f.maps[n] = m
	return m
}

// walk visits the schemas in the properties map n: its own keys, then those of
// each mapping merged in with "<<". names holds the keys already visited, so an
// explicit key wins over a merged one and an earlier source over a later one. A
// mapping whose keys were all visited is walked once per document, however many
// properties maps alias or merge it, so the work stays linear in the size of the
// file. A key skipped because names shadows it is remembered in f.skipped and
// visited by the next walk that does not shadow it. It reports whether every key
// of n, and of the mappings it merges, has been visited by now, and returns
// ctx's error when ctx is cancelled.
func (f *finder) walk(ctx context.Context, n *yaml.Node, path, field string, names *taken) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode || f.walking[n] {
		return true, nil
	}
	if f.walked[n] {
		f.shadow(names, n, true)
		return true, nil
	}
	f.walking[n] = true
	defer delete(f.walking, n)
	skip := f.skipped[n]
	if skip == nil {
		skip = &skipped{byShadow: map[*yaml.Node][]int{}}
		for i := 0; i+1 < len(n.Content); i += 2 {
			skip.own = append(skip.own, i)
		}
	}
	// Keys held back by a mapping that still shadows them are not looked at
	// again, so a mapping overlapping an earlier one costs nothing per walk.
	todo := skip.own
	skip.own = nil
	for by, idx := range skip.byShadow {
		if !names.shadowed[by] {
			todo = append(todo, idx...)
			delete(skip.byShadow, by)
		}
	}
	// Map order is random; the keys are visited in file order.
	slices.Sort(todo)
	own := f.index(n).own
	for _, i := range todo {
		k := n.Content[i]
		if k.ShortTag() == "!!merge" || own[k.Value].key != k {
			continue
		}
		if by, ok := f.taken(names, k.Value); ok {
			if by == nil {
				skip.own = append(skip.own, i)
			} else {
				skip.byShadow[by] = append(skip.byShadow[by], i)
			}
			continue
		}
		names.names[k.Value] = true
		if c := resolve(n.Content[i+1]); c == nil || f.seenSchema[c] {
			continue
		}
		if err := f.schema(ctx, n.Content[i+1], join(path, "properties", k.Value), fmt.Sprintf("%s.properties[%s]", field, k.Value)); err != nil {
			return false, err
		}
	}
	complete := len(skip.own) == 0 && len(skip.byShadow) == 0
	for _, v := range f.index(n).merges {
		for _, src := range mergeSources(v) {
			ok, err := f.walk(ctx, src, path, field, names)
			if err != nil {
				return false, err
			}
			complete = complete && ok
		}
	}
	if complete {
		f.walked[n] = true
		delete(f.skipped, n)
	} else {
		f.skipped[n] = skip
	}
	// Whether or not every key was visited, n's keys decide the same keys of
	// the sources that come after it.
	f.shadow(names, n, true)
	return complete, nil
}

// skipped is the entries of a properties map that a shadowing map kept from
// being visited: own those shadowed by a key already visited in that walk,
// byShadow the others, under the walked mapping that shadows them.
type skipped struct {
	own      []int
	byShadow map[*yaml.Node][]int
}

// taken is the keys of a properties map already decided: names holds those
// visited, shadowed the walked mappings and the ones they merge, whose keys shadow the
// same keys without being listed one by one.
type taken struct {
	names    map[string]bool
	shadowed map[*yaml.Node]bool
}

// shadow adds the mapping m, walked by now, and the mappings it merges, to
// those shadowing later keys. A mapping that merges itself, directly or not,
// is added once. walked is true for a mapping the walk itself visited: without
// an anchor it cannot be met again, and the walk decided its keys through names,
// so it is not remembered. The mappings it merges are reached only through an
// anchored mapping or are added by their own walk, so those are remembered
// whatever their anchor.
func (f *finder) shadow(t *taken, m *yaml.Node, walked bool) {
	if t.shadowed[m] || walked && m.Anchor == "" {
		return
	}
	idx := f.index(m)
	if t.shadowed == nil {
		t.shadowed = map[*yaml.Node]bool{}
	}
	t.shadowed[m] = true
	if !f.held[m] {
		f.held[m] = true
		for k := range idx.own {
			f.holders[k] = append(f.holders[k], m)
		}
	}
	for _, v := range idx.merges {
		for _, src := range mergeSources(v) {
			if src = resolve(src); src != nil && src.Kind == yaml.MappingNode {
				f.shadow(t, src, false)
			}
		}
	}
}

// taken reports whether key is already decided in t, and by which walked
// mapping; the mapping is nil when key was visited by name. Only the mappings
// with key of their own are asked, not every shadowing one: whichever is the
// shorter list, the mappings remembered for key or those shadowing in t, is
// the one that is searched.
func (f *finder) taken(t *taken, key string) (*yaml.Node, bool) {
	if t.names[key] {
		return nil, true
	}
	if len(t.shadowed) < len(f.holders[key]) {
		for m := range t.shadowed {
			if _, ok := f.index(m).own[key]; ok {
				return m, true
			}
		}
		return nil, false
	}
	for _, m := range f.holders[key] {
		if t.shadowed[m] {
			return m, true
		}
	}
	return nil, false
}

func str(n *yaml.Node) string {
	if n = resolve(n); n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}
