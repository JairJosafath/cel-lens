package assist

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	apiservercel "k8s.io/apiserver/pkg/cel"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// Summary is what the line above a CEL expression says about it: what the
// expression is about, and the values it reads that Kubernetes leaves dyn.
type Summary struct {
	// Start and End are the range of the expression's text, as
	// extract.Expression has them.
	Start, End extract.Position
	// Subject is the variable the expression is about: self in a
	// CustomResourceDefinition, object in a policy.
	Subject string
	// SubjectType says what the subject is: for self, the kind and the place
	// of the schema the rule is on, such as Widget.spec or Widget for the root;
	// for object, the kinds the policy matches, such as apps/v1 Deployment,
	// the type a .cellens entry gives it, or dyn.
	SubjectType string
	// Values are the values the expression reads that a .cellens entry types
	// or can type, in the order the expression first reads them, each once.
	Values []Value
}

// Value is a value an expression reads that Kubernetes leaves dyn, named as a
// .cellens entry names it.
type Value struct {
	// Name is the value as the expression reads it, written as a .cellens
	// path writes it, with field names unescaped: self.spec.config, params,
	// object.spec.ports[].config, self.spec.my-port for self.spec.my__dash__port.
	Name string
	// Type is the type a .cellens entry gives the value, as the entry writes
	// it; empty when the value is dyn.
	Type string
	// DocumentKind and DocumentName are the kind and metadata.name of the
	// document the value is in.
	DocumentKind, DocumentName string
	// Version is the version of a CustomResourceDefinition the value is in;
	// empty for a policy.
	Version string
	// Variable is the policy variable the value is in: params, object,
	// oldObject or variables.<name>; empty for a CustomResourceDefinition.
	Variable string
	// Path leads to the value from the root of the object (a
	// CustomResourceDefinition) or from the variable (a policy), as a
	// .cellens path such as spec.config or spec.ports[]; empty for the root.
	Path string
}

// Summaries returns the Summary of each CEL expression of doc that a answers
// for, in file order: those of an apiextensions.k8s.io/v1
// CustomResourceDefinition, a ValidatingAdmissionPolicy, a
// MutatingAdmissionPolicy and, when the experimental feature kyverno is on, a
// Kyverno ValidatingPolicy, the documents cel-lens checks. A value is listed
// only where a .cellens entry types it or could: in a
// CustomResourceDefinition a value with x-kubernetes-int-or-string, the only
// dyn value the API server lets a rule read, and in a policy params, object,
// oldObject, variables.<name> or a dyn value inside them. A Kyverno policy
// gets no values, as .cellens types none of its own. A cancelled ctx gives
// ctx.Err() and no summaries.
func (a *Assistant) Summaries(ctx context.Context, doc *Document) ([]Summary, error) {
	var out []Summary
	scopes := map[scopeKey]scoped{}
	policies := map[int]*policyTypes{}
	// reached says, by document and variable, whether a variable that is the
	// same in every scope of its policy reaches a value listed (see reaches).
	reached := map[reachKey]bool{}
	kinds := map[string]kindsSchema{}
	typeFromCRDs := a.features.Has(feature.CRDTypedPolicies)
	for _, e := range doc.exprs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Document >= len(doc.roots) || doc.roots[e.Document] == nil {
			continue
		}
		root := doc.roots[e.Document]
		b, ok := a.forExpr(doc, e)
		if !ok {
			continue
		}
		kind, _ := root["kind"].(string)
		var s Summary
		switch {
		case kind == "CustomResourceDefinition" && root["apiVersion"] == "apiextensions.k8s.io/v1":
			s, ok = a.crdSummary(root, e)
		case b.vars != nil: // a Kyverno policy, whose values .cellens does not type
			policySpec, _ := root["spec"].(map[string]any)
			s = Summary{Subject: "object", SubjectType: cmp.Or(cachedKinds(kinds, policySpec, doc.crds, typeFromCRDs).name, "dyn")}
		case kind == "ValidatingAdmissionPolicy" || kind == "MutatingAdmissionPolicy":
			// An expression that does not name variables reads none, so its
			// scope leaves them out and their types are not worked out.
			withVariables := strings.Contains(e.Text, "variables")
			key := scopeKey{e.Document, strings.HasPrefix(e.FieldPath, "spec.matchConditions["), variableLimit(e), withVariables}
			sc, cached := scopes[key]
			if !cached {
				p, made := policies[e.Document]
				if !made {
					p = newPolicyTypes(root, a.version, doc.crds, typeFromCRDs, kinds)
					policies[e.Document] = p
				}
				sc.vars = p.scope(e, withVariables)
				assumeInPolicy(sc.vars, doc.assumedFor(root))
				for _, v := range sc.vars {
					if !readable(v.name) || sc.reaches {
						continue
					}
					if v.name == "variables" {
						sc.reaches = reaches(v.schema, assume.Policies, 0)
						continue
					}
					rk := reachKey{e.Document, v.name}
					r, known := reached[rk]
					if !known {
						r = reaches(v.schema, assume.Policies, 0)
						reached[rk] = r
					}
					sc.reaches = r
				}
				scopes[key] = sc
			}
			s, ok = a.policySummary(root, e, sc), true
		default:
			ok = false
		}
		if ok {
			s.Start, s.End = e.Start, e.End
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(x, y Summary) int {
		if before(x.Start, y.Start) {
			return -1
		}
		if before(y.Start, x.Start) {
			return 1
		}
		return 0
	})
	return out, nil
}

// scoped is the scope of the expressions of a policy with one scopeKey: its
// variables, and whether one of the variables a .cellens entry can type
// reaches a value Summaries lists (see reaches); when none does, no
// expression of the scope is parsed.
type scoped struct {
	vars    []variable
	reaches bool
}

// readable reports whether a .cellens entry can type the policy variable
// called name, or values inside it.
func readable(name string) bool {
	return name == "params" || name == "object" || name == "oldObject" || name == "variables"
}

// maxReach is how deep reaches looks into a schema.
const maxReach = 256

// reaches reports whether a value of schema s, or one below it, is one
// Summaries lists: typed from .cellens, or dyn in mode m, which in a
// CustomResourceDefinition (assume.Rules) means with
// x-kubernetes-int-or-string. An expression whose variables reach none reads
// none, and is not parsed.
func reaches(s schema, m assume.Mode, depth int) bool {
	switch {
	case s == nil || depth > maxReach:
		return false
	case stops(s, m):
		return true
	}
	for _, p := range s.sub("properties") {
		if sub, ok := p.(map[string]any); ok && reaches(sub, m, depth+1) {
			return true
		}
	}
	return reaches(s.sub("items"), m, depth+1) || reaches(s.sub("additionalProperties"), m, depth+1)
}

// scopeKey tells apart the expressions of a policy whose scopes differ: the
// document, whether the expression is a match condition, which has no
// variables, how many of spec.variables it may use (see variableLimit), and
// whether it names variables at all.
type scopeKey struct {
	document      int
	match         bool
	limit         int
	withVariables bool
}

// reachKey names a variable of the policy in a document.
type reachKey struct {
	document int
	variable string
}

// crdSummary is the Summary of the rule or messageExpression e of root, a
// CustomResourceDefinition. It is false when e is not under a version's
// schema.
func (a *Assistant) crdSummary(root map[string]any, e extract.Expression) (Summary, bool) {
	version, place, ok := crdPlace(root, e.FieldPath)
	if !ok {
		return Summary{}, false
	}
	crdSpec, _ := root["spec"].(map[string]any)
	names, _ := crdSpec["names"].(map[string]any)
	kind, _ := names["kind"].(string)
	if kind == "" {
		kind = cmp.Or(documentName(root), "CustomResourceDefinition")
	}
	s := Summary{Subject: "self", SubjectType: assume.Name(kind, place)}
	vars := crdScope(root, e, false) // self, and oldSelf with the same schema
	if len(vars) == 0 || !reaches(vars[0].schema, assume.Rules, 0) {
		return s, true
	}
	r := reader{doc: root, version: version, mode: assume.Rules}
	for _, v := range vars {
		r.roots = append(r.roots, readRoot{ident: v.name, written: v.name, schema: v.schema, path: place})
	}
	s.Values = r.read(a, e.Text)
	return s, true
}

// policySummary is the Summary of the expression e of root, a policy whose
// scope is sc.
func (a *Assistant) policySummary(root map[string]any, e extract.Expression, sc scoped) Summary {
	s := Summary{Subject: "object", SubjectType: "dyn"}
	for _, v := range sc.vars {
		if v.name == "object" {
			s.SubjectType = v.typ()
		}
	}
	if !sc.reaches {
		return s
	}
	r := reader{doc: root, mode: assume.Policies}
	for _, v := range sc.vars {
		if readable(v.name) {
			r.roots = append(r.roots, readRoot{ident: v.name, written: v.name, variable: v.name, schema: v.schema})
		}
	}
	s.Values = r.read(a, e.Text)
	return s
}

// documentName is the metadata.name of root, a decoded document, or "".
func documentName(root map[string]any) string {
	meta, _ := root["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	return name
}

// crdPlace returns, for the field path of an expression of root, a
// CustomResourceDefinition, the name of the version it is in and the path
// from the version's openAPIV3Schema to the schema its rule is on, one step
// each as assume takes them. It is false for a path it cannot follow.
func crdPlace(root map[string]any, fieldPath string) (version string, place []string, ok bool) {
	rest, ok := strings.CutPrefix(fieldPath, "spec.versions[")
	if !ok {
		return "", nil, false
	}
	n, rest, ok := strings.Cut(rest, "]")
	i, err := strconv.Atoi(n)
	if !ok || err != nil {
		return "", nil, false
	}
	ver, _ := walk(root, "spec.versions["+n+"]").(map[string]any)
	version, _ = ver["name"].(string)
	rest, ok = strings.CutPrefix(rest, ".schema.openAPIV3Schema")
	if !ok || version == "" || i < 0 {
		return "", nil, false
	}
	end := strings.LastIndex(rest, ".x-kubernetes-validations[")
	if end < 0 {
		return "", nil, false
	}
	rest = rest[:end]
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, ".properties["):
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return "", nil, false
			}
			place = append(place, rest[len(".properties["):end])
			rest = rest[end+1:]
		case strings.HasPrefix(rest, ".items"):
			place, rest = append(place, assume.Items), rest[len(".items"):]
		case strings.HasPrefix(rest, ".additionalProperties"):
			place, rest = append(place, assume.Values), rest[len(".additionalProperties"):]
		default:
			return "", nil, false
		}
	}
	return version, place, true
}

// reader finds the values an expression reads that Kubernetes leaves dyn.
type reader struct {
	doc map[string]any // the document the expression is in
	// version is the version of a CustomResourceDefinition; mode says which
	// values are dyn: assume.Rules in one, assume.Policies in a policy.
	version string
	mode    assume.Mode
	roots   []readRoot
	found   []Value
	seen    map[string]bool
}

// readRoot is a name an expression can start a chain with: a variable, or
// the iteration variable of a macro, which stands for the items of a list or
// the values of a map.
type readRoot struct {
	// ident is the name in the expression; written is how the names of the
	// values below it start: the variable, or for an iteration variable the
	// range's name and its step, such as self.items[].
	ident, written string
	// variable is the policy variable the name is in, params, object,
	// oldObject or variables; empty in a CustomResourceDefinition.
	variable string
	schema   schema
	// path leads to the value the name stands for from the version's
	// openAPIV3Schema (a CRD) or from the variable (a policy).
	path []string
}

// read parses text with the parser of a and returns the values it reads.
// Text that does not parse reads nothing.
func (r *reader) read(a *Assistant, text string) []Value {
	parsed, iss := a.parser.Parse(text)
	if iss.Err() != nil {
		return nil
	}
	r.seen = map[string]bool{}
	r.visit(parsed.NativeRep().Expr(), r.roots)
	return r.found
}

// visit records the values that e reads, where names are the names in scope.
func (r *reader) visit(e ast.Expr, names []readRoot) {
	if r.chain(e, names) {
		return
	}
	switch e.Kind() {
	case ast.SelectKind:
		r.visit(e.AsSelect().Operand(), names)
	case ast.CallKind:
		c := e.AsCall()
		if c.IsMemberFunction() {
			r.visit(c.Target(), names)
		}
		for _, arg := range c.Args() {
			r.visit(arg, names)
		}
	case ast.ListKind:
		for _, el := range e.AsList().Elements() {
			r.visit(el, names)
		}
	case ast.MapKind:
		for _, en := range e.AsMap().Entries() {
			r.visit(en.AsMapEntry().Key(), names)
			r.visit(en.AsMapEntry().Value(), names)
		}
	case ast.StructKind:
		for _, f := range e.AsStruct().Fields() {
			r.visit(f.AsStructField().Value(), names)
		}
	case ast.ComprehensionKind:
		r.comprehension(e.AsComprehension(), names)
	case ast.UnspecifiedExprKind, ast.IdentKind, ast.LiteralKind:
		// A name that is no chain's start, or a constant, reads nothing.
	}
}

// comprehension visits a macro such as all or map: its range, then its body,
// where the iteration variable stands for the items of a list range, or the
// second one, in a two-variable macro, for the items or values, and the
// variables of the macro hide the names they shadow.
func (r *reader) comprehension(c ast.ComprehensionExpr, names []readRoot) {
	r.visit(c.IterRange(), names)
	r.visit(c.AccuInit(), names)
	inner := withoutNames(names, c.IterVar(), c.IterVar2(), c.AccuVar())
	if item, isMap, ok := r.items(c.IterRange(), names); ok {
		switch {
		case c.HasIterVar2():
			item.ident = c.IterVar2()
			inner = append(inner, item)
		case !isMap:
			item.ident = c.IterVar()
			inner = append(inner, item)
		}
	}
	r.visit(c.LoopCondition(), inner)
	r.visit(c.LoopStep(), inner)
	r.visit(c.Result(), inner)
}

// withoutNames returns names without the ones called any of hidden.
func withoutNames(names []readRoot, hidden ...string) []readRoot {
	return slices.DeleteFunc(slices.Clone(names), func(n readRoot) bool { return slices.Contains(hidden, n.ident) })
}

// items returns what the items of the list, or the values of the map, that
// rangeExpr reads stand for, and whether it is a map. It is false when the
// range is no chain of known values, or is typed from .cellens or dyn.
func (r *reader) items(rangeExpr ast.Expr, names []readRoot) (item readRoot, isMap, ok bool) {
	root, steps, ok := chainOf(rangeExpr, names)
	if !ok {
		return readRoot{}, false, false
	}
	s, path, ok := follow(root, steps, r.mode)
	if !ok || stops(s, r.mode) {
		return readRoot{}, false, false
	}
	elem, at, ok := child(s, chainStep{index: true})
	if !ok {
		return readRoot{}, false, false
	}
	path = append(slices.Clone(path), at)
	item = readRoot{written: assume.Name(root.written, path[len(root.path):]), variable: root.variable, schema: elem, path: path}
	return item, at == assume.Values, true
}

// chainStep is one step of a chain such as self.ports[0].name: a field or an
// index.
type chainStep struct {
	field string
	index bool
}

// chainOf reads e as a chain of fields and indexes from a name in names, and
// returns the name and the steps, first step first. A has() test reads its
// operand only.
func chainOf(e ast.Expr, names []readRoot) (readRoot, []chainStep, bool) {
	var rev []chainStep
	for {
		if e.Kind() == ast.SelectKind {
			sel := e.AsSelect()
			if !sel.IsTestOnly() {
				rev = append(rev, chainStep{field: sel.FieldName()})
			}
			e = sel.Operand()
			continue
		}
		step, operand, ok := indexStep(e)
		if !ok {
			break
		}
		rev, e = append(rev, step), operand
	}
	if e.Kind() != ast.IdentKind {
		return readRoot{}, nil, false
	}
	for i := len(names) - 1; i >= 0; i-- {
		if names[i].ident == e.AsIdent() {
			slices.Reverse(rev)
			return names[i], rev, true
		}
	}
	return readRoot{}, nil, false
}

// indexStep reads e as one step of a chain written as a call: an optional
// field, operand.?name, or an index, operand[i] or operand[?i]. It returns
// the step and the operand.
func indexStep(e ast.Expr) (chainStep, ast.Expr, bool) {
	if e.Kind() != ast.CallKind {
		return chainStep{}, nil, false
	}
	c := e.AsCall()
	args := c.Args()
	switch {
	case len(args) != 2:
		return chainStep{}, nil, false
	case c.FunctionName() == operators.OptSelect && args[1].Kind() == ast.LiteralKind:
		field, ok := args[1].AsLiteral().Value().(string)
		return chainStep{field: field}, args[0], ok
	case c.FunctionName() == operators.Index || c.FunctionName() == operators.OptIndex:
		return chainStep{index: true}, args[0], true
	}
	return chainStep{}, nil, false
}

// follow walks steps from root, and returns the schema and the path of the
// value they lead to, or of the first value on the way that stops the walk
// in mode m (see stops). It is false when a step leads to no value.
func follow(root readRoot, steps []chainStep, m assume.Mode) (schema, []string, bool) {
	s, path := root.schema, root.path
	for _, st := range steps {
		if stops(s, m) {
			break
		}
		next, at, ok := child(s, st)
		if !ok {
			return nil, nil, false
		}
		s, path = next, append(slices.Clone(path), at)
	}
	return s, path, true
}

// stops reports whether a value of schema s is typed from .cellens or dyn in
// mode m: nothing below it is known as Kubernetes types it.
func stops(s schema, m assume.Mode) bool { return s.str(assumedTypeKey) != "" || m.IsDyn(s) }

// chain records the value that e reads when e is a chain from a name in
// names: the first value along it that a .cellens entry types, or that is dyn
// and an entry can type. It visits the indexes of the chain, and reports
// whether e was a chain.
func (r *reader) chain(e ast.Expr, names []readRoot) bool {
	root, steps, ok := chainOf(e, names)
	if !ok {
		return false
	}
	r.indexes(e, names)
	s, path, ok := follow(root, steps, r.mode)
	if ok && stops(s, r.mode) {
		r.record(root, path, s.str(assumedTypeKey))
	}
	return true
}

// indexes visits the index expressions of the chain e, which may read values
// of their own.
func (r *reader) indexes(e ast.Expr, names []readRoot) {
	for {
		if e.Kind() == ast.SelectKind {
			e = e.AsSelect().Operand()
			continue
		}
		step, operand, ok := indexStep(e)
		if !ok {
			return
		}
		if step.index {
			r.visit(e.AsCall().Args()[1], names)
		}
		e = operand
	}
}

// child returns the schema one step below s, and that step as a .cellens path
// writes it: a field name, assume.Items or assume.Values. It is false when s
// has no such value.
func child(s schema, st chainStep) (schema, string, bool) {
	values := s.sub("additionalProperties")
	if st.index {
		switch {
		case s.str("type") == "array" && s.sub("items") != nil:
			return s.sub("items"), assume.Items, true
		case s.str("type") == "object" && values != nil:
			return values, assume.Values, true
		}
		return nil, "", false
	}
	// A rule writes a field whose name is no CEL name escaped, as Kubernetes
	// escapes it: my-port as my__dash__port.
	name, ok := apiservercel.Unescape(st.field)
	if !ok {
		name = st.field
	}
	if f := s.sub("properties").sub(name); f != nil {
		return f, name, true
	}
	if s.str("type") == "object" && values != nil {
		return values, assume.Values, true
	}
	return nil, "", false
}

// record adds the value at path below root, typed t or dyn when t is "",
// unless it is already recorded.
func (r *reader) record(root readRoot, path []string, t string) {
	name := documentName(r.doc)
	if name == "" {
		return // a .cellens entry names a document by its metadata.name
	}
	kind, _ := r.doc["kind"].(string)
	v := Value{
		Name: assume.Name(root.written, path[len(root.path):]),
		Type: t, DocumentKind: kind, DocumentName: name, Version: r.version, Variable: root.variable,
	}
	for _, step := range path {
		if step != assume.Items && step != assume.Values && strings.ContainsAny(step, ".[]") {
			return // a .cellens path cannot name the field
		}
	}
	if root.variable == "variables" {
		if len(path) == 0 {
			return // all the variables, not one of them
		}
		v.Variable, path = "variables."+path[0], path[1:]
	}
	if len(path) > 0 {
		v.Path = assume.Name("", path)
	}
	key := v.Variable + "\x00" + v.Path
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	r.found = append(r.found, v)
}
