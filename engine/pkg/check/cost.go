package check

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	celschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/apiserver/pkg/cel/environment"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// Fix is a limit that would bring an expression under its cost budget, for an
// editor to offer as a quick fix.
type Fix struct {
	// FieldPath is the path of the field in the custom resource that needs the
	// limit, such as "spec.name". A list item or map value is written "[*]",
	// as in "spec.tags[*]".
	FieldPath string
	// Limit is the schema keyword to add to that field: "maxLength" for a
	// string, "maxItems" for a list, "maxProperties" for a map.
	Limit string
	// Value is the number to give the limit: the largest of 1, 2, 5, 10, 20, 50
	// and so on up to 10M that brings the expression under its budget, with the
	// other fixes of the diagnostic when it says they belong together. 0 when no
	// such number is known.
	Value int64
	// Edits write the limit into the file, one for each place the field's
	// schema is written, such as one per version when the versions share their
	// schema. Empty when the schema cannot be edited in place.
	Edits []Edit
}

// Edit is text to insert into a file.
type Edit struct {
	// At is where Text goes.
	At extract.Position
	// Text adds a limit to a schema: a new line with the schema's indentation,
	// or for a schema written in braces the key, value and comma to put just
	// after the opening brace.
	Text string
}

// isCostError reports whether fe is one of the cost errors of the API server's
// validation of a CustomResourceDefinition. The API server has no sentinel or
// type for them, so this matches their text.
func isCostError(fe *field.Error) bool {
	return fe.Type == field.ErrorTypeForbidden &&
		(strings.Contains(fe.Detail, "exceeds budget by factor") || strings.HasPrefix(fe.Detail, "contributed to estimated rule cost total"))
}

// schemaNode is a node of a CRD schema with the API server's cost context for it.
type schemaNode struct {
	// name is the field path of the node in the custom resource.
	name     string
	schema   *apiextensions.JSONSchemaProps
	cel      *validation.CELSchemaContext
	parent   *schemaNode
	children []*schemaNode
	path     *field.Path
	// keys is where the node is relative to its root schema, key by key; a
	// position in a sequence is its index.
	keys []string
	// element is true for the items and additionalProperties of its parent.
	element bool
}

// ruleCost is the estimated cost of one rule or messageExpression.
type ruleCost struct {
	path        *field.Path
	node        *schemaNode
	index       int // of the rule in its node's x-kubernetes-validations
	text        string
	isMessage   bool
	cost        uint64
	cardinality uint64
}

// costs explains the cost errors costErrs of the CRD with the numbers of the
// API server's own cost estimator, one diagnostic per expression over its
// budget and one for the CRD's total. Like the API server it leaves out the
// rules of schema nodes that have one of schemaErrs (field paths) at or below
// them, and it compiles only the rules that costErrs name, unless the CRD's
// total is over its budget, which needs them all. It returns the field paths of
// the rules it placed a diagnostic on.
func (r *reporter) costs(ctx context.Context, doc *yaml.Node, crd *apiextensions.CustomResourceDefinition, exprs map[string]extract.Expression, costErrs []*field.Error, schemaErrs []string, envSet *compiler) (map[string]bool, error) {
	type schemaRoot struct {
		path   *field.Path
		schema *apiextensions.JSONSchemaProps
		keys   [][]string // where the schema is in the file: more than one for a shared schema
	}
	var roots []schemaRoot
	if v := crd.Spec.Validation; v != nil && v.OpenAPIV3Schema != nil {
		roots = append(roots, schemaRoot{field.NewPath("spec", "validation", "openAPIV3Schema"), v.OpenAPIV3Schema, sharedKeys(doc)})
	}
	for i, v := range crd.Spec.Versions {
		if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
			roots = append(roots, schemaRoot{field.NewPath("spec", "versions").Index(i).Child("schema", "openAPIV3Schema"), v.Schema.OpenAPIV3Schema, [][]string{versionKeys(i)}})
		}
	}
	all := false
	named := map[string]bool{} // the nodes with a rule over its budget
	for _, fe := range costErrs {
		if before, _, ok := strings.Cut(fe.Field, ".x-kubernetes-validations["); ok {
			named[before] = true
		} else {
			all = true
		}
	}
	want := func(n *schemaNode) bool {
		if len(n.schema.XValidations) == 0 {
			return false
		}
		p := n.path.String()
		return (all || named[p]) && !underError(p, schemaErrs)
	}
	placed := map[string]bool{}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return placed, err
		}
		rootNode := &schemaNode{schema: root.schema, cel: validation.RootCELContext(root.schema), path: root.path}
		buildNodes(rootNode, nil)
		sz := &sizer{envSet: envSet, root: root.schema, path: root.path, nodes: map[string]*schemaNode{}, doc: doc, roots: root.keys, lines: r.lines, eol: r.eol, schemaErrs: schemaErrs}
		nodesByName(rootNode, sz.nodes)
		var rules []ruleCost
		total := rootNode.cel.TotalCost
		if err := collectRules(ctx, rootNode, envSet, want, &rules); err != nil {
			return placed, err
		}
		sz.base = rules
		byPath := map[string]ruleCost{}
		first := len(r.diags)
		// With the total over budget, every rule's advice must bring it under too.
		sz.total = total.Total > validation.StaticEstimatedCRDCostLimit
		for _, rc := range rules {
			byPath[rc.path.String()] = rc
			if rc.cost > validation.StaticEstimatedCostLimit {
				msg, ex, err := explainRule(ctx, rc, sz)
				if err != nil {
					return placed, err
				}
				if r.reportCost(exprs, rc.path.String(), msg, ex) {
					placed[rc.path.String()] = true
				}
			}
		}
		sz.total = false
		if total.Total > validation.StaticEstimatedCRDCostLimit && len(total.MostExpensive) > 0 {
			top := byPath[total.MostExpensive[0].Path.String()]
			ok, err := r.reportTotal(ctx, exprs, top, total, byPath, first, sz)
			if err != nil {
				return placed, err
			}
			if ok {
				placed[top.path.String()] = true
			}
		}
	}
	return placed, nil
}

// buildNodes fills in the children of n the way the API server's validation
// descends into a schema: additionalProperties, properties, items. When keep
// is not nil, only the nodes whose field path it accepts are built, and
// nothing below one it refuses.
func buildNodes(n *schemaNode, keep func(path string) bool) {
	s := n.schema
	add := func(c *schemaNode, schema *apiextensions.JSONSchemaProps) bool {
		if keep != nil && !keep(c.path.String()) {
			return false
		}
		c.schema, c.parent = schema, n
		n.children = append(n.children, c)
		return true
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		c := &schemaNode{name: n.name + "[*]", element: true, path: n.path.Child("additionalProperties"), keys: appendKeys(n.keys, "additionalProperties")}
		if add(c, s.AdditionalProperties.Schema) {
			c.cel = n.cel.ChildAdditionalPropertiesContext(c.schema)
			buildNodes(c, keep)
		}
	}
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		prop := s.Properties[name]
		c := &schemaNode{name: strings.TrimPrefix(n.name+"."+name, "."), path: n.path.Child("properties").Key(name), keys: appendKeys(n.keys, "properties", name)}
		if add(c, &prop) {
			c.cel = n.cel.ChildPropertyContext(c.schema, name)
			buildNodes(c, keep)
		}
	}
	if s.Items != nil && s.Items.Schema != nil {
		c := &schemaNode{name: n.name + "[*]", element: true, path: n.path.Child("items"), keys: appendKeys(n.keys, "items")}
		if add(c, s.Items.Schema) {
			c.cel = n.cel.ChildItemsContext(c.schema)
			buildNodes(c, keep)
		}
	}
	if s.Items != nil {
		for i := range s.Items.JSONSchemas {
			c := &schemaNode{name: n.name + "[*]", element: true, path: n.path.Child("items").Index(i), keys: appendKeys(n.keys, "items", strconv.Itoa(i))}
			if add(c, &s.Items.JSONSchemas[i]) {
				c.cel = n.cel.ChildItemsContext(c.schema)
				buildNodes(c, keep)
			}
		}
	}
}

// appendKeys returns keys followed by more, without sharing keys' array.
func appendKeys(keys []string, more ...string) []string {
	return append(append([]string(nil), keys...), more...)
}

// withLimits returns a copy of s with the limits set on the fields they name,
// by name as buildNodes gives them. Field names below a schema are looked up
// the same way, so the copy has to be made before any node is built from it.
// The copy shares what it does not change with s, so neither may be modified.
func withLimits(s *apiextensions.JSONSchemaProps, limits map[string]int64) *apiextensions.JSONSchemaProps {
	// apply returns s itself when nothing below it changes, else a copy that
	// shares the parts that did not.
	var apply func(s *apiextensions.JSONSchemaProps, name string) *apiextensions.JSONSchemaProps
	apply = func(s *apiextensions.JSONSchemaProps, name string) *apiextensions.JSONSchemaProps {
		c := s
		own := func() *apiextensions.JSONSchemaProps {
			if c == s {
				cp := *s
				c = &cp
			}
			return c
		}
		if v, ok := limits[nodeName(&schemaNode{name: name})]; ok {
			n := v
			switch s.Type {
			case "string":
				own().MaxLength = &n
			case "array":
				own().MaxItems = &n
			default:
				own().MaxProperties = &n
			}
		}
		if ap := s.AdditionalProperties; ap != nil && ap.Schema != nil {
			if ch := apply(ap.Schema, name+"[*]"); ch != ap.Schema {
				own().AdditionalProperties = &apiextensions.JSONSchemaPropsOrBool{Allows: ap.Allows, Schema: ch}
			}
		}
		var props map[string]apiextensions.JSONSchemaProps
		for key, prop := range s.Properties {
			if ch := apply(&prop, strings.TrimPrefix(name+"."+key, ".")); ch != &prop {
				if props == nil {
					props = maps.Clone(s.Properties)
				}
				props[key] = *ch
			}
		}
		if props != nil {
			own().Properties = props
		}
		if it := s.Items; it != nil {
			items := *it
			changed, cloned := false, false
			if it.Schema != nil {
				if ch := apply(it.Schema, name+"[*]"); ch != it.Schema {
					items.Schema, changed = ch, true
				}
			}
			for i := range it.JSONSchemas {
				if ch := apply(&it.JSONSchemas[i], name+"[*]"); ch != &it.JSONSchemas[i] {
					if !cloned {
						items.JSONSchemas, cloned = slices.Clone(it.JSONSchemas), true
					}
					items.JSONSchemas[i], changed = *ch, true
				}
			}
			if changed {
				own().Items = &items
			}
		}
		return c
	}
	return apply(s, "")
}

// underError reports whether a field path in errs is path or below it.
func underError(path string, errs []string) bool {
	for _, e := range errs {
		if rest, ok := strings.CutPrefix(e, path); ok && (rest == "" || rest[0] == '.' || rest[0] == '[') {
			return true
		}
	}
	return false
}

// collectRules compiles the rules of n and its descendants for which want is
// true with the API server's compiler and cost estimator and appends their
// costs to out. Every cost is also observed by the CRD's TotalCost, as the API
// server does.
func collectRules(ctx context.Context, n *schemaNode, envSet *compiler, want func(*schemaNode) bool, out *[]ruleCost) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if want(n) {
		if err := nodeRules(n, envSet, out); err != nil {
			return err
		}
	}
	for _, c := range n.children {
		if err := collectRules(ctx, c, envSet, want, out); err != nil {
			return err
		}
	}
	return nil
}

// nodeRules compiles the rules of n alone and appends their costs to out.
func nodeRules(n *schemaNode, envSet *compiler, out *[]ruleCost) error {
	if len(n.schema.XValidations) > 0 {
		results, err := envSet.compile(n)
		if err != nil {
			return fmt.Errorf("estimate cost at %s: %w", n.path, err)
		}
		for i, cr := range results {
			rule := n.path.Child("x-kubernetes-validations").Index(i).Child("rule")
			cardinality := cr.cardinality
			if n.cel.MaxCardinality != nil {
				cardinality = *n.cel.MaxCardinality
			}
			cost := multiply(cr.cost, cardinality)
			n.cel.TotalCost.ObserveExpressionCost(rule, cost)
			*out = append(*out, ruleCost{path: rule, node: n, index: i, text: n.schema.XValidations[i].Rule, cost: cost, cardinality: cardinality})
			if cr.hasMessage {
				msg := n.path.Child("x-kubernetes-validations").Index(i).Child("messageExpression")
				n.cel.TotalCost.ObserveExpressionCost(msg, cr.messageCost)
				*out = append(*out, ruleCost{
					path: msg, node: n, index: i, text: n.schema.XValidations[i].MessageExpression, isMessage: true,
					cost: cr.messageCost, cardinality: cardinality,
				})
			}
		}
	}
	return nil
}

// compiled is what the cost of a rule needs of the API server's compilation.
type compiled struct {
	cost, cardinality uint64
	// messageCost is the cost of the messageExpression, if hasMessage.
	messageCost uint64
	hasMessage  bool
}

// compiler compiles the rules of schema nodes with the API server's compiler
// and keeps what it learns: the rules of a node cost the same wherever they
// are, so a schema that appears again, as in a CRD that repeats a structure or
// in the many estimates of sizing, is not compiled again. It is safe for
// concurrent use. The zero value is not usable; set envSet.
type compiler struct {
	envSet *environment.EnvSet
	mu     sync.Mutex
	seen   map[string][]compiled
	// typed is whether the node at a field path has type information, so that
	// the same CRD walked again does not convert its schema again.
	typed map[string]bool
}

// maxRemembered is the most descendants a schema node can have to be
// remembered; the key of a bigger one costs more to make than a repeat saves.
const maxRemembered = 32

// descendants counts the nodes below n.
func descendants(n *schemaNode) int {
	c := len(n.children)
	for _, k := range n.children {
		c += descendants(k)
	}
	return c
}

// usesSelf reports whether a rule or message expression of n may see the
// schema. It is told from the text: any "self" in the rule or message
// expression, in any case, counts, so it may be too careful (a string that says
// "self") but never too bold; `oldSelf` is covered too, as it contains "self".
func usesSelf(n *schemaNode) bool {
	return slices.ContainsFunc(n.schema.XValidations, func(v apiextensions.ValidationRule) bool {
		return strings.Contains(strings.ToLower(v.Rule+v.MessageExpression), "self")
	})
}

// schemaKey is what the costs of the rules of n are remembered under when they
// can see the schema: the whole schema, and whether n is the root, whose type
// differs. It needs no type information, so a repeat is found without
// converting the schema. It is empty when the schema is too big to be worth its
// key.
func schemaKey(n *schemaNode) string {
	if descendants(n) > maxRemembered {
		return ""
	}
	b, err := json.Marshal(n.schema)
	if err != nil {
		return ""
	}
	if n.parent == nil {
		return "root:" + string(b)
	}
	return string(b)
}

// rulesKey is what the costs of the rules of n are remembered under when none
// of them can see the schema: the rules and the smallest serialized size of the
// node, which sets the cardinality of a node with no limit (0 when the
// cardinality is fixed). A big schema with
// such rules then costs nothing to compile again.
func rulesKey(n *schemaNode, size int64) string {
	rules, err := json.Marshal(n.schema.XValidations)
	if err != nil {
		return ""
	}
	return "rules:" + strconv.FormatInt(size, 10) + ":" + string(rules)
}

// recall returns the costs remembered under key.
func (c *compiler) recall(key string) ([]compiled, bool) {
	if key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	got, ok := c.seen[key]
	return got, ok
}

// hasTypeInfo reports whether n has type information, as the node that the
// costs remembered under key were made for does; a node without it has no cost
// to estimate. A key made from the whole schema (not a rules key) says n's
// schema is that node's, so n has type information when its parent has; a rules
// key says nothing of the schema, so n's own is looked at.
func (c *compiler) hasTypeInfo(n *schemaNode, key string) bool {
	node := n
	if !strings.HasPrefix(key, "rules:") && n.parent != nil {
		node = n.parent
	}
	path := node.path.String()
	c.mu.Lock()
	got, ok := c.typed[path]
	c.mu.Unlock()
	if ok {
		return got
	}
	got = c.noteTypeInfo(node)
	return got
}

// noteTypeInfo reports whether n has type information, and keeps the answer for
// the field path of n, so that the same CRD walked again, as the API server's
// own check and the estimates do, does not convert its schema again.
func (c *compiler) noteTypeInfo(n *schemaNode) bool {
	info, err := n.cel.TypeInfo()
	got := err == nil && (info == nil || info.DeclType != nil)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.typed == nil {
		c.typed = map[string]bool{}
	}
	c.typed[n.path.String()] = got
	return got
}

// compile returns the costs of the rules of n, one per rule, before its
// cardinality is applied.
func (c *compiler) compile(n *schemaNode) ([]compiled, error) {
	// Without a look at self a rule costs the same for any schema, and for a
	// node whose cardinality is fixed, the size of its schema does not matter
	// either; such a repeat is found without converting the schema, which for the
	// root is the whole CRD.
	var key string
	switch {
	case usesSelf(n):
		key = schemaKey(n)
	case n.cel.MaxCardinality != nil:
		key = rulesKey(n, 0)
	}
	if got, ok := c.recall(key); ok && c.hasTypeInfo(n, key) {
		return got, nil
	}
	info, err := n.cel.TypeInfo()
	if n.parent != nil {
		c.noteTypeInfo(n.parent) // free: the parent's was made for n's
	}
	if err == nil && info != nil && info.DeclType != nil {
		c.noteTypeInfo(n)
		return c.compileTyped(n, info, key)
	}
	return nil, nil // a schema without type information has no cost to estimate, and none is kept
}

// compileTyped is compile for a node n with type information info, whose costs
// are remembered under key; without one the key is made from the size in info.
func (c *compiler) compileTyped(n *schemaNode, info *validation.CELTypeInfo, key string) ([]compiled, error) {
	if key == "" && !usesSelf(n) {
		key = rulesKey(n, info.DeclType.MinSerializedSize)
		if got, ok := c.recall(key); ok {
			return got, nil
		}
	}
	results, err := celschema.Compile(info.Schema, info.DeclType, celconfig.PerCallLimit, c.envSet, celschema.NewExpressionsEnvLoader())
	if err != nil {
		return nil, err
	}
	out := make([]compiled, len(results))
	for i, cr := range results {
		out[i] = compiled{cost: cr.MaxCost, cardinality: cr.MaxCardinality}
		if cr.MessageExpression != nil && cr.MessageExpressionError == nil {
			out[i].hasMessage, out[i].messageCost = true, cr.MessageExpressionMaxCost
		}
	}
	if key != "" {
		c.mu.Lock()
		if c.seen == nil {
			c.seen = map[string][]compiled{}
		}
		c.seen[key] = out
		c.mu.Unlock()
	}
	return out, nil
}

// multiply returns a*b, or math.MaxUint64 when that overflows, like the API
// server's estimator.
func multiply(a, b uint64) uint64 {
	if a == 0 {
		return 0
	}
	if math.MaxUint64/a < b {
		return math.MaxUint64
	}
	return a * b
}

// driver is something in the schema that makes an expression expensive.
type driver struct {
	// fix is the limit that would bound it; empty for a conversion.
	fix Fix
	// size is the number of elements or characters the estimator assumes, 0 if unknown.
	size uint64
	// text is the sentence that explains it.
	text string
}

// maxDrivers is the most drivers one message names.
const maxDrivers = 3

// drivers finds what makes the expression of rc expensive: the unbounded lists
// and maps it runs once per element of, the unbounded strings, lists and maps of
// its schema node that it uses, and the number-to-text conversions.
func drivers(rc ruleCost) []driver {
	var found []driver
	if rc.node.cel.MaxCardinality == nil {
		for a := rc.node.parent; a != nil; a = a.parent {
			if fix, ok := unboundedLimit(a); ok {
				fix.FieldPath = nodeName(a)
				found = append(found, driver{
					fix: fix, size: rc.cardinality,
					text: fmt.Sprintf("it runs for every element of %s, which has no %s, and the estimator assumes up to %s elements", quote(fix.FieldPath), quote(fix.Limit), count(rc.cardinality)),
				})
			}
		}
	}
	var walk func(n *schemaNode, depth int)
	walk = func(n *schemaNode, depth int) {
		if depth > 0 && !n.element && !mentions(rc.text, n) {
			return
		}
		if fix, ok := unboundedLimit(n); ok {
			fix.FieldPath = nodeName(n)
			d := driver{fix: fix, size: assumedSize(n)}
			noun := map[string]string{"maxLength": "a string", "maxItems": "a list", "maxProperties": "a map"}[fix.Limit]
			unit := map[string]string{"maxLength": "characters", "maxItems": "items", "maxProperties": "entries"}[fix.Limit]
			d.text = fmt.Sprintf("%s is %s with no %s", quote(fix.FieldPath), noun, quote(fix.Limit))
			if d.size > 0 {
				d.text += fmt.Sprintf(", and the estimator assumes up to %s %s", count(d.size), unit)
			}
			found = append(found, d)
		}
		for _, c := range n.children {
			walk(c, depth+1)
		}
	}
	walk(rc.node, 0)
	for _, m := range conversionPattern.FindAllStringSubmatch(rc.text, -1) {
		if n := resolve(rc.node, m[1]); n != nil && (n.schema.Type == "integer" || n.schema.Type == "number") {
			found = append(found, driver{text: fmt.Sprintf("`string(self%s)` turns a number into text, and the estimator cannot tell how long that text is, so it assumes the longest string it can count (about %s characters) and charges one unit for every 10 of them when the text is joined with `+`", m[1], count(math.MaxUint64))})
		}
	}
	if len(found) > maxDrivers {
		found = found[:maxDrivers]
	}
	return found
}

// conversionPattern matches string(self.a.b), capturing ".a.b".
var conversionPattern = regexp.MustCompile(`string\(\s*self((?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*\)`)

// resolve follows the properties of the dotted path (".a.b") from n.
func resolve(n *schemaNode, path string) *schemaNode {
	for _, name := range strings.Split(strings.TrimPrefix(path, "."), ".") {
		if name == "" {
			continue
		}
		var next *schemaNode
		for _, c := range n.children {
			if c.name == strings.TrimPrefix(n.name+"."+name, ".") {
				next = c
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

// mentions reports whether expression uses the field of n by its name.
func mentions(expression string, n *schemaNode) bool {
	name := n.name[strings.LastIndexAny(n.name, ".")+1:]
	name = strings.TrimSuffix(name, "[*]")
	if name == "" {
		return true
	}
	return regexp.MustCompile(`(?:^|[^.\w])` + regexp.QuoteMeta(name) + `\b|\.` + regexp.QuoteMeta(name) + `\b[^(\w]|\.` + regexp.QuoteMeta(name) + `$`).MatchString(expression)
}

// unboundedLimit says which limit n lacks: maxLength for a string, maxItems
// for a list, maxProperties for a map. Strings with a format or an enum are
// not counted: their size is known.
func unboundedLimit(n *schemaNode) (Fix, bool) {
	return unbounded(n.schema)
}

// unbounded is unboundedLimit for a schema.
func unbounded(s *apiextensions.JSONSchemaProps) (Fix, bool) {
	switch {
	case s.XIntOrString || s.Format != "" || len(s.Enum) > 0:
		return Fix{}, false
	case s.Type == "string" && s.MaxLength == nil:
		return Fix{Limit: "maxLength"}, true
	case s.Type == "array" && s.MaxItems == nil:
		return Fix{Limit: "maxItems"}, true
	case s.Type == "object" && s.AdditionalProperties != nil && s.MaxProperties == nil:
		return Fix{Limit: "maxProperties"}, true
	}
	return Fix{}, false
}

// assumedSize is the number of characters, items or entries the estimator
// assumes for n, or 0 when it has none.
func assumedSize(n *schemaNode) uint64 {
	info, err := n.cel.TypeInfo()
	if err != nil || info == nil || info.DeclType.MaxElements <= 0 {
		return 0
	}
	return uint64(info.DeclType.MaxElements)
}

// nodeName is the field path of n, or "self" for the root.
func nodeName(n *schemaNode) string {
	if n.name == "" {
		return "self"
	}
	return n.name
}

// quote writes s as inline code.
func quote(s string) string { return "`" + s + "`" }

// count writes n with a K, M, B or T suffix, or as a power of ten from 10^15 on.
func count(n uint64) string {
	f := float64(n)
	switch {
	case n >= 1e15:
		return fmt.Sprintf("%.1e", f)
	case n >= 1e12:
		return trim(f/1e12) + "T"
	case n >= 1e9:
		return trim(f/1e9) + "B"
	case n >= 1e6:
		return trim(f/1e6) + "M"
	case n >= 1e4:
		return trim(f/1e3) + "K"
	}
	return strconv.FormatUint(n, 10)
}

// trim writes f with one decimal, without a trailing ".0".
func trim(f float64) string {
	return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0")
}

// explained says why an expression is expensive and what would help.
type explained struct {
	// why lists the causes, advice what to do about them.
	why, advice string
	// fixes are the limits to add; together is true when they only help all at once.
	fixes    []Fix
	together bool
}

// explain describes the drivers of rc: why it is expensive and what would
// bring it under, and the limits to add as data for a quick fix, sized and
// placed by sz.
func explain(ctx context.Context, rc ruleCost, sz *sizer) (explained, error) {
	return explainDrivers(ctx, rc, drivers(rc), sz)
}

// sharedDrivers finds what the rules have in common that makes them expensive:
// the drivers of the first that every other rule has too, by field and limit,
// and the first rule's conversion drivers, which have no limit to compare.
// When they share no limit to add it gives all the drivers of the first.
func sharedDrivers(rules []ruleCost) []driver {
	first := drivers(rules[0])
	var others [][]driver
	for _, rc := range rules[1:] {
		others = append(others, drivers(rc))
	}
	var shared []driver
	for _, d := range first {
		if d.fix.Limit == "" || !slices.ContainsFunc(others, func(ds []driver) bool {
			return !slices.ContainsFunc(ds, func(o driver) bool { return o.fix.FieldPath == d.fix.FieldPath && o.fix.Limit == d.fix.Limit })
		}) {
			shared = append(shared, d)
		}
	}
	if !slices.ContainsFunc(shared, func(d driver) bool { return d.fix.Limit != "" }) {
		return first
	}
	return shared
}

// explainDrivers is explain for the drivers ds, which rc is sized by.
func explainDrivers(ctx context.Context, rc ruleCost, ds []driver, sz *sizer) (explained, error) {
	var texts []string
	var fixes []Fix
	converts := false
	for _, d := range ds {
		texts = append(texts, d.text)
		if d.fix.Limit == "" {
			converts = true
			continue
		}
		fixes = append(fixes, d.fix)
	}
	together := true
	if len(fixes) > 0 {
		var err error
		if fixes, together, err = sz.size(ctx, rc, fixes); err != nil {
			return explained{}, err
		}
	}
	var adds []string
	for _, f := range fixes {
		limit := quote(f.Limit)
		if f.Value > 0 {
			limit = quote(fmt.Sprintf("%s: %d", f.Limit, f.Value))
		}
		adds = append(adds, fmt.Sprintf("%s to %s", limit, quote(f.FieldPath)))
	}
	var parts []string
	if len(adds) > 0 {
		joiner := " and "
		if !together {
			joiner = " or "
		}
		parts = append(parts, "add "+strings.Join(adds, joiner))
	}
	if converts {
		parts = append(parts, "a number has no `maxLength`, so avoid building text from it with `+`, or write a fixed `message` instead of a `messageExpression`")
	}
	if len(parts) == 0 {
		return explained{advice: "Try simplifying the expression."}, nil
	}
	advice := strings.Join(parts, "; ")
	return explained{why: "Why: " + strings.Join(texts, "; ") + ". ", advice: strings.ToUpper(advice[:1]) + advice[1:] + ".", fixes: fixes, together: together}, nil
}

// kind names what rc is, for a message.
func kind(rc ruleCost) string {
	if rc.isMessage {
		return "messageExpression"
	}
	return "rule"
}

// explainRule is the message for a rule or messageExpression over its budget,
// with what would bring it under.
func explainRule(ctx context.Context, rc ruleCost, sz *sizer) (string, explained, error) {
	ex, err := explain(ctx, rc, sz)
	if err != nil {
		return "", ex, err
	}
	return ruleMessage(rc, ex), ex, nil
}

// ruleMessage is the message for rc over its budget, with the advice of ex.
func ruleMessage(rc ruleCost, ex explained) string {
	return fmt.Sprintf("estimated cost %s is over the per-expression budget of %s for a %s. %s%s",
		count(rc.cost), count(validation.StaticEstimatedCostLimit), kind(rc), ex.why, ex.advice)
}

// reportCost places message on the expressions at fieldPath, for the schema
// shared by all versions on each of them, and says whether there was one.
func (r *reporter) reportCost(exprs map[string]extract.Expression, fieldPath, message string, ex explained) bool {
	found := expressionsAt(exprs, fieldPath)
	for _, e := range found {
		r.diags = append(r.diags, Diagnostic{File: r.file, Message: message, Severity: SeverityError, Start: e.Start, End: e.End, Fixes: ex.fixes, FixesTogether: ex.together})
	}
	return len(found) > 0
}

// reportTotal reports the CRD's total over its budget once, on the most
// expensive expression top, and lists the expressions that add the most. When
// top is itself over its budget, its diagnostic (among r.diags[first:]) gets a
// sentence about the total instead of a second diagnostic on the same range.
func (r *reporter) reportTotal(ctx context.Context, exprs map[string]extract.Expression, top ruleCost, total *validation.TotalCost, byPath map[string]ruleCost, first int, sz *sizer) (bool, error) {
	found := expressionsAt(exprs, top.path.String())
	if len(found) == 0 {
		return false, nil
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Start.Line < found[j].Start.Line })
	var lines []string
	listed := []ruleCost{top}
	for _, c := range total.MostExpensive {
		rc := byPath[c.Path.String()]
		if rc.node != nil && rc.path.String() != top.path.String() {
			listed = append(listed, rc)
		}
		_, entry, _ := strings.Cut(rc.path.String(), ".x-kubernetes-validations[")
		lines = append(lines, fmt.Sprintf("%s at %s (%s)", quote("x-kubernetes-validations["+entry), quote(nodeName(rc.node)), count(c.Cost)))
	}
	if top.cost > validation.StaticEstimatedCostLimit {
		sentence := fmt.Sprintf(" All rules together cost an estimated %s, over the budget of %s for one CustomResourceDefinition schema; the rules that add the most: %s.",
			count(total.Total), count(validation.StaticEstimatedCRDCostLimit), strings.Join(lines, ", "))
		var mine []*Diagnostic
		for i := first; i < len(r.diags); i++ {
			if d := &r.diags[i]; d.Start == found[0].Start && d.End == found[0].End {
				mine = append(mine, d)
			}
		}
		if len(mine) > 0 {
			// The advice sized for the rule alone leaves the total over budget.
			sz.total = true
			ex, err := explain(ctx, top, sz)
			sz.total = false
			if err != nil {
				return false, err
			}
			for _, d := range mine {
				d.Message = ruleMessage(top, ex) + sentence
				d.Fixes, d.FixesTogether = ex.fixes, ex.together
			}
			return true, nil
		}
	}
	sz.total = true // only the total is over budget: a number has to bring it under
	ex, err := explainDrivers(ctx, top, sharedDrivers(listed), sz)
	sz.total = false
	if err != nil {
		return false, err
	}
	msg := fmt.Sprintf("estimated cost %s of all rules together is over the budget of %s for one CustomResourceDefinition schema. The rules that add the most: %s. %s%s",
		count(total.Total), count(validation.StaticEstimatedCRDCostLimit), strings.Join(lines, ", "), ex.why, ex.advice)
	e := found[0]
	r.diags = append(r.diags, Diagnostic{File: r.file, Message: msg, Severity: SeverityError, Start: e.Start, End: e.End, Fixes: ex.fixes, FixesTogether: ex.together})
	return true, nil
}
