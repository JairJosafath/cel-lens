package check

import (
	"context"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// ladder is the numbers a limit is chosen from, in ascending order: 1, 2, 5,
// 10, 20, 50 and so on up to 10M.
func ladder() []int64 {
	var l []int64
	for m := int64(1); m <= 1e6; m *= 10 {
		l = append(l, m, 2*m, 5*m)
	}
	return append(l, 1e7)
}

// sizer finds numbers for the limits of a schema by estimating its cost again
// with them.
type sizer struct {
	envSet *compiler
	// root is the schema, at path in the file and at keys in doc.
	root *apiextensions.JSONSchemaProps
	path *field.Path
	// nodes are the fields of root by name.
	nodes map[string]*schemaNode
	doc   *yaml.Node
	// roots are where the schema is in doc: the keys of each place.
	roots [][]string
	lines [][]rune
	eol   string
	// schemaErrs are the field paths of the schema errors of the CRD.
	schemaErrs []string
	// base are the rules of the schema as they are in the file, with their
	// costs. They give the part of the total that a limit does not change.
	base []ruleCost
	// total is true when a number must also bring the CRD's total under budget,
	// not only the expression; checking it compiles every rule of the schema.
	total bool
	// estimates are the results of estimate by the limits they were made for.
	estimates map[string]estimate
}

// roomy is the smallest number worth suggesting for one limit on its own; a
// limit of 2 items or 5 characters would break most users of the field.
const roomy = 100

// size gives the fixes their numbers for the expression rc, and their place in
// the file. When one of them alone brings rc (and, if sz.total is set, the CRD's
// total) under budget
// with a roomy number, the result holds each that does, and together is false.
// Otherwise, when all of them with the same number do, the result holds all of
// them and together is true. Otherwise it holds those that do alone with a
// smaller number, if any, and else the fixes with Value 0.
func (sz *sizer) size(ctx context.Context, rc ruleCost, fixes []Fix) (sized []Fix, together bool, err error) {
	var alone, tight []Fix
	for _, f := range fixes {
		v, err := sz.largest(ctx, rc, []Fix{f})
		if err != nil {
			return fixes, false, err
		}
		f.Value = v
		switch {
		case v >= roomy:
			alone = append(alone, f)
		case v > 0:
			tight = append(tight, f)
		}
	}
	if len(alone) > 0 {
		return sz.place(alone), false, nil
	}
	v, err := sz.largest(ctx, rc, fixes)
	if err != nil {
		return fixes, false, err
	}
	if v > 0 {
		out := append([]Fix(nil), fixes...)
		for i := range out {
			out[i].Value = v
		}
		return sz.place(out), true, nil
	}
	if len(tight) > 0 {
		return sz.place(tight), false, nil
	}
	return fixes, true, nil
}

// largest is the largest number of the ladder that, given to all fixes, brings
// rc and the total under budget; 0 if none does. A bigger limit never costs less.
func (sz *sizer) largest(ctx context.Context, rc ruleCost, fixes []Fix) (int64, error) {
	steps := ladder()
	limitsAt := func(i int) map[string]int64 {
		limits := map[string]int64{}
		for _, f := range fixes {
			limits[f.FieldPath] = steps[i]
		}
		return limits
	}
	// What the estimates kept for other rules already tell narrows the search:
	// the rules of a node mostly fail at the same numbers.
	lo, hi := 0, len(steps) // steps[:lo] are known to pass, steps[hi:] to fail
	for i := range steps {
		if ok, known := sz.known(rc, limitsAt(i), sz.total); known {
			if ok {
				lo = i + 1
			} else {
				hi = min(hi, i)
			}
		}
		// A number that is too big for the rule alone is too big for the total.
		if ok, known := sz.known(rc, limitsAt(i), false); sz.total && known && !ok {
			hi = min(hi, i)
		}
	}
	// Nothing is known: when even the smallest number is too big, which is common
	// when other fields of the rule have no limit either, one estimate says so.
	if lo == 0 && hi > 0 {
		ok, err := sz.within(ctx, rc, limitsAt(0))
		if err != nil || !ok {
			return 0, err
		}
		lo = 1
	}
	var failed error
	i := lo + sort.Search(max(hi-lo, 0), func(i int) bool {
		if failed != nil {
			return true
		}
		ok, err := sz.within(ctx, rc, limitsAt(lo+i))
		failed = err
		return !ok // the first number that is too big
	})
	if failed != nil || i == 0 {
		return 0, failed
	}
	return steps[i-1], nil
}

// estimate is the costs of the rules a set of limits changes.
type estimate struct {
	rules []ruleCost
	// rest is the cost of the rules the limits do not change plus those in rules;
	// it is only set when sz.total is.
	rest uint64
}

// within reports whether rc is under budget when the fields have the limits,
// and when sz.total is set the total of its CRD too. Without it only the node
// of rc is compiled, which keeps the cost of sizing independent of the number
// of rules in the schema. The estimate of a node and a set of limits is kept,
// as all the rules of a node and all the searches that share a number ask for it.
func (sz *sizer) within(ctx context.Context, rc ruleCost, limits map[string]int64) (bool, error) {
	key := sz.key(rc, limits, sz.total)
	est, ok := sz.estimates[key]
	if !ok {
		var err error
		if est, err = sz.estimate(ctx, rc, limits); err != nil {
			return false, err
		}
		if sz.estimates == nil {
			sz.estimates = map[string]estimate{}
		}
		sz.estimates[key] = est
	}
	return sz.under(rc, est, sz.total), nil
}

// known is within for an estimate that is already kept, of the total if total
// is set; known is false when there is none.
func (sz *sizer) known(rc ruleCost, limits map[string]int64, total bool) (ok, known bool) {
	est, known := sz.estimates[sz.key(rc, limits, total)]
	return known && sz.under(rc, est, total), known
}

// key is what the estimate for rc and the limits is kept under.
func (sz *sizer) key(rc ruleCost, limits map[string]int64, total bool) string {
	key := limitsKey(limits)
	if !total {
		key += "@" + rc.node.path.String()
	}
	return key
}

// under reports whether rc is under budget in est, and when total is set the
// total too.
func (sz *sizer) under(rc ruleCost, est estimate, total bool) bool {
	for i, r := range est.rules {
		if total && r.path.String() == rc.path.String() || !total && r.index == rc.index && r.isMessage == rc.isMessage {
			return est.rules[i].cost <= validation.StaticEstimatedCostLimit && (!total || est.rest <= validation.StaticEstimatedCRDCostLimit)
		}
	}
	return false
}

// limitsKey is the limits as one string, the same for the same limits.
func limitsKey(limits map[string]int64) string {
	names := make([]string, 0, len(limits))
	for name := range limits {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name + "=" + strconv.FormatInt(limits[name], 10) + ";")
	}
	return b.String()
}

// estimate compiles the rules the limits change: those of the node of rc, or
// when sz.total is set all that are related to the limits.
func (sz *sizer) estimate(ctx context.Context, rc ruleCost, limits map[string]int64) (estimate, error) {
	schema := sz.root
	if !sz.total {
		schema = chain(schema, rc.node.keys)
	}
	root := withLimits(schema, limits)
	n := &schemaNode{schema: root, cel: validation.RootCELContext(root), path: sz.path}
	var est estimate
	if sz.total {
		related := sz.related(limits)
		buildNodes(n, related)
		for _, b := range sz.base {
			if !related(b.node.path.String()) {
				est.rest = saturatingAdd(est.rest, b.cost)
			}
		}
		want := func(n *schemaNode) bool {
			return len(n.schema.XValidations) > 0 && related(n.path.String()) && !underError(n.path.String(), sz.schemaErrs)
		}
		if err := collectRules(ctx, n, sz.envSet, want, &est.rules); err != nil {
			return est, err
		}
		for _, r := range est.rules {
			est.rest = saturatingAdd(est.rest, r.cost)
		}
		return est, nil
	}
	buildNodes(n, nil)
	at := findNode(n, rc.node.path.String())
	if at == nil {
		return est, nil
	}
	err := nodeRules(at, sz.envSet, &est.rules)
	return est, err
}

// chain returns a copy of s that holds only the schema at keys, with all that
// is below it, and the schemas above it that lead there. What the rules of the
// last one cost does not depend on the other fields, so the copy is as good for
// estimating them and a good deal smaller. The copy shares s's contents.
func chain(s *apiextensions.JSONSchemaProps, keys []string) *apiextensions.JSONSchemaProps {
	if len(keys) == 0 {
		return s
	}
	c := *s
	c.Properties, c.Items, c.AdditionalProperties = nil, nil, nil
	switch keys[0] {
	case "properties":
		p := s.Properties[keys[1]]
		c.Properties = map[string]apiextensions.JSONSchemaProps{keys[1]: *chain(&p, keys[2:])}
	case "additionalProperties":
		c.AdditionalProperties = &apiextensions.JSONSchemaPropsOrBool{Allows: s.AdditionalProperties.Allows, Schema: chain(s.AdditionalProperties.Schema, keys[1:])}
	case "items":
		if len(keys) > 1 {
			if i, err := strconv.Atoi(keys[1]); err == nil { // one schema of a tuple
				c.Items = &apiextensions.JSONSchemaPropsOrArray{JSONSchemas: slices.Clone(s.Items.JSONSchemas)}
				c.Items.JSONSchemas[i] = *chain(&s.Items.JSONSchemas[i], keys[2:])
				return &c
			}
		}
		c.Items = &apiextensions.JSONSchemaPropsOrArray{Schema: chain(s.Items.Schema, keys[1:])}
	}
	return &c
}

// related returns a function that says whether the node at a field path has a
// cost the limits can change: a limit on a field changes the rules of that
// field, of the fields below it (they run once for every element) and of the
// fields above it (they see its size).
func (sz *sizer) related(limits map[string]int64) func(path string) bool {
	var paths []string
	for name := range limits {
		if n, ok := sz.nodes[name]; ok {
			paths = append(paths, n.path.String())
		}
	}
	return func(path string) bool {
		return underError(path, paths) || slices.ContainsFunc(paths, func(p string) bool { return underError(p, []string{path}) })
	}
}

// saturatingAdd returns a+b, or math.MaxUint64 when that overflows, like the
// API server's total.
func saturatingAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

// findNode returns the node of n or its descendants at the path, or nil.
func findNode(n *schemaNode, path string) *schemaNode {
	if n.path.String() == path {
		return n
	}
	for _, c := range n.children {
		if f := findNode(c, path); f != nil {
			return f
		}
	}
	return nil
}

// place adds to each fix the edits that write it into the file.
func (sz *sizer) place(fixes []Fix) []Fix {
	for i, f := range fixes {
		n := sz.nodes[f.FieldPath]
		if n == nil {
			continue
		}
		placed := 0
		for _, root := range sz.roots {
			if e, ok := sz.insertion(yamlAt(sz.doc, append(append([]string(nil), root...), n.keys...)), f); ok {
				placed++
				if !slices.Contains(fixes[i].Edits, e) { // versions that share a schema by an anchor reach one place
					fixes[i].Edits = append(fixes[i].Edits, e)
				}
			}
		}
		if placed != len(sz.roots) {
			fixes[i].Edits = nil // all places or none: half a fix leaves the versions differing
		}
	}
	return fixes
}

// insertion is the edit that adds f to the schema m: before the first key of a
// block mapping, as a line of its own, or after the opening brace of a flow
// mapping. It gives no edit for a schema it cannot place that way.
func (sz *sizer) insertion(m *yaml.Node, f Fix) (Edit, bool) {
	if m == nil || m.Kind != yaml.MappingNode {
		return Edit{}, false
	}
	entry := f.Limit + ": " + strconv.FormatInt(f.Value, 10)
	if m.Style&yaml.FlowStyle != 0 {
		if m.Line > len(sz.lines) {
			return Edit{}, false
		}
		at := extract.Position{Line: m.Line, Column: m.Column + 1}
		if strings.HasPrefix(strings.TrimSpace(string(sz.lines[m.Line-1][min(m.Column, len(sz.lines[m.Line-1])):])), "}") {
			return Edit{at, entry}, true
		}
		return Edit{at, entry + ", "}, true
	}
	if len(m.Content) == 0 || m.Content[0].Line > len(sz.lines) {
		return Edit{}, false
	}
	key := m.Content[0]
	if strings.TrimLeft(string(sz.lines[key.Line-1][:key.Column-1]), " ") != "" {
		return Edit{}, false // the key follows a "- " on its line
	}
	return Edit{extract.Position{Line: key.Line, Column: 1}, strings.Repeat(" ", key.Column-1) + entry + sz.eol}, true
}

// versionKeys is where the schema of the i-th version is in a CRD.
func versionKeys(i int) []string {
	return []string{"spec", "versions", strconv.Itoa(i), "schema", "openAPIV3Schema"}
}

// sharedKeys is where the schema that all versions share is in the file: the
// schema of each version, as a v1 CRD file has no other place for it.
func sharedKeys(doc *yaml.Node) [][]string {
	var keys [][]string
	if versions := yamlAt(doc, []string{"spec", "versions"}); versions != nil && versions.Kind == yaml.SequenceNode {
		for i := range versions.Content {
			keys = append(keys, versionKeys(i))
		}
	}
	return keys
}

// yamlAt follows keys, mapping keys and sequence indexes, from the document or
// node n, and returns the node there, or nil.
func yamlAt(n *yaml.Node, keys []string) *yaml.Node {
	n = value(n)
	for _, k := range keys {
		if n == nil {
			return nil
		}
		if n.Kind == yaml.SequenceNode {
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(n.Content) {
				return nil
			}
			n = value(n.Content[i])
			continue
		}
		if n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				next = n.Content[i+1]
			}
		}
		n = value(next)
	}
	return n
}

// value looks through a document node or an alias to the node it holds.
func value(n *yaml.Node) *yaml.Node {
	for n != nil {
		switch {
		case n.Kind == yaml.AliasNode:
			n = n.Alias
		case n.Kind == yaml.DocumentNode && len(n.Content) > 0:
			n = n.Content[0]
		case n.Kind == yaml.DocumentNode:
			return nil
		default:
			return n
		}
	}
	return nil
}

// nodesByName indexes n and its descendants by their field path.
func nodesByName(n *schemaNode, into map[string]*schemaNode) {
	into[nodeName(n)] = n
	for _, c := range n.children {
		nodesByName(c, into)
	}
}
