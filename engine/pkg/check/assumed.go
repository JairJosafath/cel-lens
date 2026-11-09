package check

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// AssumedType is a type to assume for a value that Kubernetes leaves dyn, as
// an entry of a .cellens file gives one. It names the value by its document,
// a CustomResourceDefinition's version or a policy's variable, and a path.
type AssumedType struct {
	// Kind and Name name the document the value is in by its kind and
	// metadata.name, such as CustomResourceDefinition and widgets.example.com.
	Kind, Name string
	// Version is the served version of a CustomResourceDefinition the value is
	// in; empty for a policy.
	Version string
	// Variable is the policy variable the value is in: params, object,
	// oldObject or variables.<name>; empty for a CustomResourceDefinition.
	Variable string
	// Path leads from the root of the version's schema, or from the variable,
	// to the value, one step each: a field name, "[]" for a list's items or
	// "[*]" for a map's values. Empty, it is the root.
	Path []string
	// Schema is the type, as an OpenAPI schema in the form YAML decodes a
	// CustomResourceDefinition's schema to.
	Schema map[string]any
	// Type names the type for a reader, such as "{name: string}" or
	// "apps/v1 Deployment".
	Type string
	// Source and Line say where the type is written, such as ".cellens" and
	// 12; messages name them as .cellens:12.
	Source string
	Line   int
}

// origin is where t is written, as messages name it: .cellens:12.
func (t AssumedType) origin() string { return t.Source + ":" + strconv.Itoa(t.Line) }

// WithAssumedTypes makes Check assume the types in types where the values
// they name are dyn: in a CustomResourceDefinition's rules only an
// x-kubernetes-int-or-string value, as the API server gives a rule none of the
// unknown fields x-kubernetes-preserve-unknown-fields keeps; in a policy also
// a value with no type or that keeps unknown fields. A value Kubernetes
// already types keeps its type, and a
// type whose value is not in the file is left out (AssumedTypeProblems says
// why). Check then reports what the API server reports, and also, as a
// warning, every problem in a CEL expression that only the assumed types
// show; the message of such a warning names the Source and Line of the types.
// Types are assumed in the x-kubernetes-validations rules of a
// CustomResourceDefinition, and in the validations of a
// ValidatingAdmissionPolicy, which the API server type checks. The types are
// read, not changed.
func WithAssumedTypes(types []AssumedType) Option {
	return func(s *settings) { s.assumed = types }
}

// AssumedTypeProblem says why the type at Index of those WithAssumedTypes
// gives is not assumed in a file.
type AssumedTypeProblem struct {
	Index   int
	Message string
}

// AssumedTypeProblems returns, ordered by Index, why each type that
// WithAssumedTypes gives is not assumed in data, a YAML file with any number
// of documents: the file has no such document, version or variable, the path
// leads to no value, or the value is not dyn, such as one no rule of a
// CustomResourceDefinition may read. A type that applies has no
// problem. Options WithVersion, WithFeatures and WithCRDs apply, as they
// decide which values are dyn. The error is for a cancelled ctx.
func AssumedTypeProblems(ctx context.Context, data []byte, opts ...Option) ([]AssumedTypeProblem, error) {
	var set settings
	for _, o := range opts {
		o(&set)
	}
	if len(set.assumed) == 0 {
		return nil, nil
	}
	docs, _, err := extract.Documents(ctx, "", extract.UTF8(data))
	if err != nil {
		return nil, err
	}
	r := reporter{version: set.version, crds: set.typingCRDs(), assumed: set.assumed}
	problems := map[int]string{}
	matched := map[int]bool{}
	for _, d := range docs {
		types := r.assumedIn(d.Node)
		types = slices.DeleteFunc(types, func(t indexedType) bool { return matched[t.index] })
		if len(types) == 0 {
			continue
		}
		for _, t := range types {
			matched[t.index] = true
		}
		var found map[int]string
		switch head, _ := typeOf(d.Node); {
		case head.Kind == kindCRD && head.APIVersion == "apiextensions.k8s.io/v1":
			var versioned apiextensionsv1.CustomResourceDefinition
			var scratch reporter // takes the wrong-typed fields read reports, which are Check's to report
			if _, err := scratch.read(ctx, d.Node, &versioned); err != nil {
				found = allProblems(types, "the CustomResourceDefinition cannot be read: "+err.Error())
				break
			}
			_, found = placeCRD(crdSchemas(&versioned), types)
		case head.Kind == kindCRD:
			found = allProblems(types, "cel-lens checks only CustomResourceDefinitions of apiextensions.k8s.io/v1")
		case isAdmissionPolicy(head.APIVersion, head.Kind):
			var perr error
			if _, found, perr = r.placePolicy(ctx, d.Node, types); perr != nil {
				return nil, perr
			}
		default:
			found = allProblems(types, "cel-lens assumes types only in CustomResourceDefinitions, ValidatingAdmissionPolicies and MutatingAdmissionPolicies")
		}
		for i, msg := range found {
			problems[i] = msg
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i, t := range set.assumed {
		if !matched[i] {
			problems[i] = fmt.Sprintf("the file has no document %s %s", t.Kind, t.Name)
		}
	}
	out := make([]AssumedTypeProblem, 0, len(problems))
	for i, msg := range problems {
		out = append(out, AssumedTypeProblem{Index: i, Message: msg})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

const kindCRD = "CustomResourceDefinition"

// isAdmissionPolicy reports whether a document with the apiVersion and kind
// is a ValidatingAdmissionPolicy or MutatingAdmissionPolicy.
func isAdmissionPolicy(apiVersion, kind string) bool {
	group, _, _ := strings.Cut(apiVersion, "/")
	return group == "admissionregistration.k8s.io" && (kind == "ValidatingAdmissionPolicy" || kind == "MutatingAdmissionPolicy")
}

// allProblems gives each of types the same problem.
func allProblems(types []indexedType, msg string) map[int]string {
	out := map[int]string{}
	for _, t := range types {
		out[t.index] = msg
	}
	return out
}

// indexedType is an assumed type with its index among those WithAssumedTypes gives.
type indexedType struct {
	index int
	AssumedType
}

// assumedIn returns the assumed types for the document node: those whose
// kind and name are its kind and metadata.name.
func (r *reporter) assumedIn(node *yaml.Node) []indexedType {
	if len(r.assumed) == 0 {
		return nil
	}
	head, ok := typeOf(node)
	if !ok {
		return nil
	}
	name := metadataName(node)
	var out []indexedType
	for i, t := range r.assumed {
		if t.Kind == head.Kind && t.Name == name && name != "" {
			out = append(out, indexedType{i, t})
		}
	}
	return out
}

// metadataName returns the metadata.name of a document, or "".
func metadataName(node *yaml.Node) string {
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	meta := mappingValue(node, "metadata")
	if name := mappingValue(meta, "name"); name != nil && name.Kind == yaml.ScalarNode {
		return name.Value
	}
	return ""
}

// mappingValue returns the value of key in the mapping node, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// crdSchemas returns the openAPIV3Schema of each version of crd, as a schema
// map, by version name, in the order of the versions.
func crdSchemas(crd *apiextensionsv1.CustomResourceDefinition) []namedSchema {
	var out []namedSchema
	for _, v := range crd.Spec.Versions {
		if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
			continue
		}
		s, ok := toMap(v.Schema.OpenAPIV3Schema)
		if !ok {
			continue
		}
		out = append(out, namedSchema{v.Name, s})
	}
	return out
}

type namedSchema struct {
	name   string
	schema map[string]any
}

// placeCRD places types into the schemas of the versions of a
// CustomResourceDefinition. It returns the schemas that changed, by version,
// and why each type that is not placed is not, by index.
func placeCRD(versions []namedSchema, types []indexedType) (map[string]map[string]any, map[int]string) {
	placed := map[string]map[string]any{}
	problems := map[int]string{}
	first := map[string]AssumedType{}
	for _, t := range types {
		if t.Variable != "" {
			problems[t.index] = "variable is for policies: a CustomResourceDefinition's entry names the value with version and path"
			continue
		}
		i := slices.IndexFunc(versions, func(v namedSchema) bool { return v.name == t.Version })
		if i < 0 {
			names := make([]string, len(versions))
			for j, v := range versions {
				names[j] = v.name
			}
			problems[t.index] = fmt.Sprintf("CustomResourceDefinition %s has no version %s with a schema: name one of %s", t.Name, t.Version, strings.Join(names, ", "))
			continue
		}
		key := t.Version + "\x00" + strings.Join(t.Path, "\x00")
		if prev, ok := first[key]; ok {
			problems[t.index] = "a second type for this value, after the one of " + prev.origin() + ": keep one"
			continue
		}
		cur := placed[t.Version]
		if cur == nil {
			cur = versions[i].schema
		}
		s, err := assume.At(cur, "", t.Path, t.Schema, assume.Rules)
		if err != nil {
			problems[t.index] = err.Error()
			continue
		}
		first[key] = t.AssumedType
		placed[t.Version] = s
	}
	return placed, problems
}

// policyPlaced is where the assumed types of one policy go. Each schema is
// that of the variable with the types placed; nil where no type is placed.
type policyPlaced struct {
	types  []AssumedType // the types placed
	params map[string]any
	// kinds are the kinds the policy matches, nil when they are not known;
	// object and oldObject then have one schema, else one per kind.
	kinds             []matchkind.Kind
	object, oldObject []map[string]any
	variables         map[string]map[string]any
}

// policyVars is the part of a policy that decides the types of its variables.
type policyVars struct {
	Spec struct {
		ParamKind        *admissionregistrationv1.ParamKind      `json:"paramKind"`
		MatchConstraints *admissionregistrationv1.MatchResources `json:"matchConstraints"`
		Variables        []admissionregistrationv1.Variable      `json:"variables"`
	} `json:"spec"`
}

// readPolicyVars reads the part of root, a decoded policy, that decides the
// types of its variables, so a wrong value elsewhere does not hide them.
func readPolicyVars(root map[string]any) (*policyVars, error) {
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	var in policyVars
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// rules are the match rules of p, as matchkind reads them.
func (p *policyVars) rules() []matchkind.Rule {
	if p.Spec.MatchConstraints == nil {
		return nil
	}
	rules := make([]matchkind.Rule, len(p.Spec.MatchConstraints.ResourceRules))
	for i, rule := range p.Spec.MatchConstraints.ResourceRules {
		rules[i] = matchkind.Rule{Groups: rule.APIGroups, Versions: rule.APIVersions, Resources: rule.Resources}
	}
	return rules
}

// placePolicy places types into the variables of the policy in node, a
// ValidatingAdmissionPolicy or MutatingAdmissionPolicy, where they are dyn as
// the API server's type checker types them. It returns where they go and why
// each type that is not placed is not, by index. The error is for a cancelled ctx.
func (r *reporter) placePolicy(ctx context.Context, node *yaml.Node, types []indexedType) (policyPlaced, map[int]string, error) {
	var pl policyPlaced
	problems := map[int]string{}
	var root map[string]any
	err := node.Decode(&root)
	var p *policyVars
	if err == nil {
		p, err = readPolicyVars(root)
	}
	if err != nil {
		return pl, allProblems(types, "the policy cannot be read: "+err.Error()), ctx.Err()
	}
	kind, _ := root["kind"].(string)
	meta, _ := root["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	params, paramsName := r.paramsSchema(p.Spec.ParamKind)
	var kindSchemas []map[string]any
	kindNames := ""
	if kinds, ok := r.crds.Resolve(p.rules()); ok && p.Spec.MatchConstraints != nil {
		names := make([]string, len(kinds))
		for i, k := range kinds {
			s, ok := toMap(k.Schema)
			if !ok {
				kindSchemas = nil
				break
			}
			kindSchemas = append(kindSchemas, s)
			names[i] = k.String()
		}
		if kindSchemas != nil {
			pl.kinds, kindNames = kinds, strings.Join(names, ", ")
		}
	}
	pl.object = make([]map[string]any, max(len(pl.kinds), 1))
	pl.oldObject = make([]map[string]any, max(len(pl.kinds), 1))
	var vars []Variable
	for _, t := range types {
		switch {
		case t.Version != "":
			problems[t.index] = "version is for CustomResourceDefinitions: a policy's entry names the value with variable and path"
		case t.Variable == "params" && p.Spec.ParamKind == nil:
			problems[t.index] = fmt.Sprintf("%s %s has no spec.paramKind, so it has no params", kind, name)
		case t.Variable == "params":
			s, err := placeIn(orSchema(pl.params, params), "params", paramsName, t)
			if err != nil {
				problems[t.index] = err.Error()
				continue
			}
			pl.params = s
		case t.Variable == "object" || t.Variable == "oldObject":
			at := pl.object
			if t.Variable == "oldObject" {
				at = pl.oldObject
			}
			if msg := placeInKinds(at, kindSchemas, kindNames, t); msg != "" {
				problems[t.index] = msg
				continue
			}
		case strings.HasPrefix(t.Variable, "variables."):
			if vars == nil {
				if vars, err = r.variableTypes(ctx, p); err != nil {
					return pl, nil, err
				}
			}
			vname := strings.TrimPrefix(t.Variable, "variables.")
			i := slices.IndexFunc(vars, func(v Variable) bool { return v.Name == vname })
			if i < 0 {
				problems[t.index] = fmt.Sprintf("%s %s has no variable %s in spec.variables", kind, name, vname)
				continue
			}
			if pl.variables == nil {
				pl.variables = map[string]map[string]any{}
			}
			s, err := placeIn(orSchema(pl.variables[vname], vars[i].Schema), t.Variable, "", t)
			if err != nil {
				problems[t.index] = err.Error()
				continue
			}
			pl.variables[vname] = s
		default:
			problems[t.index] = fmt.Sprintf("variable %q: use params, object, oldObject or variables.<name>", t.Variable)
			continue
		}
		if _, bad := problems[t.index]; !bad {
			pl.types = append(pl.types, t.AssumedType)
		}
	}
	return pl, problems, ctx.Err()
}

// orSchema is placed when it is not nil, and base otherwise.
func orSchema(placed, base map[string]any) map[string]any {
	if placed != nil {
		return placed
	}
	return base
}

// placeIn places t into s, the schema of the variable called name, which is
// typed as typeName when that is set; a nil s is dyn.
func placeIn(s map[string]any, name, typeName string, t indexedType) (map[string]any, error) {
	if s == nil {
		s = map[string]any{"x-kubernetes-preserve-unknown-fields": true}
	}
	out, err := assume.At(s, name, t.Path, t.Schema, assume.Policies)
	var ae *assume.Error
	if errors.As(err, &ae) && ae.Reason == assume.Typed && ae.Depth == 0 && typeName != "" {
		ae.Type = typeName
	}
	return out, err
}

// placeInKinds places t into at, the schemas of object or oldObject: one per
// kind of kinds, whose names are names, or one when kinds is nil, as the kinds
// are not known. A type with a path is placed in every kind that has a dyn
// value there. It returns why t is not placed, or "".
func placeInKinds(at, kinds []map[string]any, names string, t indexedType) string {
	if kinds == nil {
		s, err := placeIn(at[0], t.Variable, "", t)
		if err != nil {
			return err.Error()
		}
		at[0] = s
		return ""
	}
	var first error
	for i, k := range kinds {
		s, err := placeIn(orSchema(at[i], k), t.Variable, names, t)
		if err != nil {
			first = cmp.Or(first, err)
			continue
		}
		at[i] = s
	}
	if first != nil && !slices.ContainsFunc(at, func(s map[string]any) bool { return s != nil }) {
		return first.Error()
	}
	return ""
}

// paramsSchema returns the schema of params for the paramKind pk, and the name
// of its kind, as paramsType finds it; nil when params is dyn.
func (r *reporter) paramsSchema(pk *admissionregistrationv1.ParamKind) (map[string]any, string) {
	if pk == nil {
		return nil, ""
	}
	if kind, ok := matchkind.Lookup(pk.APIVersion, pk.Kind); ok {
		s, _ := toMap(kind.Schema)
		return s, kind.String()
	}
	if s, ok := r.crds.Schema(pk.APIVersion, pk.Kind); ok {
		return s, pk.APIVersion + " " + pk.Kind
	}
	return nil, ""
}

// toMap returns v, a schema type such as *spec.Schema, as a schema map.
func toMap(v any) (map[string]any, bool) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, false
	}
	return out, true
}

// fromMap returns the schema map s as a *spec.Schema.
func fromMap(s map[string]any) (*spec.Schema, bool) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	out := new(spec.Schema)
	if json.Unmarshal(raw, out) != nil {
		return nil, false
	}
	return out, true
}

// addAssumed runs again, through run, the checks that found the diagnostics
// from base on, this time with assumed types, and adds what only that run finds
// in an expression of exprs, as a warning whose message names the types that
// typesOf gives for the expression. A problem at a place where the API server
// already reports one is not added. A failure of run other than ctx's leaves
// the diagnostics as they were.
func (r *reporter) addAssumed(ctx context.Context, base int, exprs map[string]extract.Expression, typesOf func(extract.Expression) []AssumedType, run func() error) error {
	own := slices.Clone(r.diags[base:])
	kept := r.diags
	r.diags = nil
	err := run()
	found := r.diags
	r.diags = kept
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if err != nil {
		found = nil // the API server's own diagnostics stand without the assumed types
	}
	for _, d := range found {
		e, ok := expressionOf(exprs, d)
		if !ok || slices.ContainsFunc(own, func(o Diagnostic) bool { return o.Start == d.Start && o.End == d.End }) {
			continue
		}
		d.Severity = SeverityWarning
		d.Message += assumedNote(typesOf(e))
		r.diags = append(r.diags, d)
	}
	return nil
}

// expressionOf returns the expression of exprs the diagnostic d is in.
func expressionOf(exprs map[string]extract.Expression, d Diagnostic) (extract.Expression, bool) {
	for _, e := range exprs {
		if !before(d.Start, e.Start) && !before(e.End, d.End) {
			return e, true
		}
	}
	return extract.Expression{}, false
}

func before(a, b extract.Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
}

// assumedNote is what a problem found only with types adds to its message.
func assumedNote(types []AssumedType) string {
	origins := make([]string, 0, len(types))
	for _, t := range types {
		if o := t.origin(); !slices.Contains(origins, o) {
			origins = append(origins, o)
		}
	}
	switch len(origins) {
	case 0:
		return ""
	case 1:
		return " (the type comes from " + origins[0] + ": the API server would not report this)"
	}
	return " (the types come from " + strings.Join(origins, ", ") + ": the API server would not report this)"
}

// usedIn returns the types of pl whose variable the expression text names,
// or all of them when it names none.
func (pl policyPlaced) usedIn(text string) []AssumedType {
	var out []AssumedType
	for _, t := range pl.types {
		if namesVariable(text, t.Variable) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return pl.types
	}
	return out
}

// namesVariable reports whether text names the variable name as a whole word.
func namesVariable(text, name string) bool {
	return regexp.MustCompile(`(^|[^\w.])` + regexp.QuoteMeta(name) + `\b`).MatchString(text)
}

// typesOfVersion returns the types of the version of a CustomResourceDefinition
// the expression e is in.
func typesOfVersion(crd *apiextensionsv1.CustomResourceDefinition, types []indexedType) func(extract.Expression) []AssumedType {
	return func(e extract.Expression) []AssumedType {
		var version string
		if rest, ok := strings.CutPrefix(e.FieldPath, "spec.versions["); ok {
			if n, _, ok := strings.Cut(rest, "]"); ok {
				if i, err := strconv.Atoi(n); err == nil && i < len(crd.Spec.Versions) {
					version = crd.Spec.Versions[i].Name
				}
			}
		}
		var out []AssumedType
		for _, t := range types {
			if t.Version == version {
				out = append(out, t.AssumedType)
			}
		}
		return out
	}
}

// withAssumed returns a copy of crd with the schemas of placed, by version.
func withAssumed(crd *apiextensionsv1.CustomResourceDefinition, placed map[string]map[string]any) (*apiextensionsv1.CustomResourceDefinition, bool) {
	out := crd.DeepCopy()
	for i, v := range out.Spec.Versions {
		s, ok := placed[v.Name]
		if !ok {
			continue
		}
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, false
		}
		props := new(apiextensionsv1.JSONSchemaProps)
		if json.Unmarshal(raw, props) != nil {
			return nil, false
		}
		out.Spec.Versions[i].Schema.OpenAPIV3Schema = props
	}
	return out, true
}
