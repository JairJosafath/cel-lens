package examples

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel/model"
	celconfig "k8s.io/apiserver/pkg/apis/cel"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// Rule is an x-kubernetes-validations rule of a version a
// CustomResourceDefinition serves.
type Rule struct {
	// CRD is the CustomResourceDefinition it is in.
	CRD *CRD
	// Version is the name of its version, such as v1. Path is the schema
	// node it is on, from the root of the object, as a .cellens entry
	// writes a path: field names separated by dots, [] for the items of a
	// list and [*] for the values of a map, "" for the root, such as
	// "spec.ports[]". Index is its place in that node's
	// x-kubernetes-validations, from 0.
	Version, Path string
	Index         int
	// Text is the rule as YAML decodes it; Start and End are where it is
	// written in the file of the CRD, as package extract gives them.
	Text       string
	Start, End extract.Position
	// MessageStart and MessageEnd are where its messageExpression is
	// written, both zero when it has none.
	MessageStart, MessageEnd extract.Position
	// Document and FieldPath are those of its expression as package extract
	// finds it: the index of its YAML document in the file, from 0, and its
	// field path there, such as
	// spec.versions[0].schema.openAPIV3Schema.properties[spec].x-kubernetes-validations[1].rule.
	// With Text they name the rule across edits of the file elsewhere.
	Document  int
	FieldPath string
	// from and to are where its x-kubernetes-validations entry is written:
	// from its first character to the first of what follows it.
	from, to extract.Position

	version *servedVersion
	// steps lead from the root of the schema to its node.
	steps []schemaStep
	rule  apiextensionsv1.ValidationRule
}

// schemaStep is one step from a schema node to the one below it: a
// property, the items of a list or the values of a map.
type schemaStep struct {
	property string
	items    bool
	values   bool
}

// Rules returns the x-kubernetes-validations rules of the versions c
// serves, in the order of its file. A rule whose place in the schema the
// API server's structural schema does not have, as one inside allOf, is
// left out: the API server does not take it. The error is ctx's.
func (c *CRD) Rules(ctx context.Context) ([]*Rule, error) {
	// A document of the file that does not parse is not c's, which Read
	// parsed: the error that says so is not c's either.
	exprs, _ := extract.Find(ctx, c.File, c.data)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []*Rule
	byItem := map[string]*Rule{}
	for _, e := range exprs {
		if e.Document != c.doc {
			continue
		}
		item, ok := strings.CutSuffix(e.FieldPath, "."+string(e.Kind))
		if !ok {
			continue
		}
		switch e.Kind {
		case extract.KindRule:
			r, ok := c.rule(item)
			if !ok {
				continue
			}
			r.Text, r.Start, r.End = e.Text, e.Start, e.End
			r.Document, r.FieldPath = e.Document, e.FieldPath
			r.from, r.to = span(c.node, item)
			byItem[item] = r
			out = append(out, r)
		case extract.KindMessageExpression:
			if r, ok := byItem[item]; ok {
				r.MessageStart, r.MessageEnd = e.Start, e.End
			}
		case extract.KindExpression, extract.KindValueExpression:
		}
	}
	slices.SortStableFunc(out, func(a, b *Rule) int {
		return cmp.Or(a.Start.Line-b.Start.Line, a.Start.Column-b.Start.Column)
	})
	return out, nil
}

// rule returns the rule at item, the field path of an x-kubernetes-validations
// entry, such as
// spec.versions[0].schema.openAPIV3Schema.properties[spec].x-kubernetes-validations[1];
// false when it is not in a version c serves or not where its structural
// schema has rules.
func (c *CRD) rule(item string) (*Rule, bool) {
	rest, ok := strings.CutPrefix(item, "spec.versions[")
	if !ok {
		return nil, false
	}
	index, rest, ok := strings.Cut(rest, "].schema.openAPIV3Schema")
	i, err := strconv.Atoi(index)
	if !ok || err != nil || i < 0 || i >= len(c.names) {
		return nil, false
	}
	sv, ok := c.versions[c.names[i]]
	if !ok {
		return nil, false
	}
	at := strings.LastIndex(rest, ".x-kubernetes-validations[")
	if at < 0 || !strings.HasSuffix(rest, "]") {
		return nil, false
	}
	n, err := strconv.Atoi(rest[at+len(".x-kubernetes-validations[") : len(rest)-1])
	if err != nil {
		return nil, false
	}
	steps, ok := parseSteps(rest[:at])
	if !ok {
		return nil, false
	}
	node := walkSchema(sv.structural, steps)
	if node == nil || n < 0 || n >= len(node.XValidations) {
		return nil, false
	}
	return &Rule{CRD: c, Version: sv.name, Path: cellensPath(steps), Index: n, version: sv, steps: steps, rule: node.XValidations[n]}, true
}

// parseSteps reads the steps of a schema field path below openAPIV3Schema,
// such as ".properties[spec].items.additionalProperties"; false for one
// with any other step.
func parseSteps(path string) ([]schemaStep, bool) {
	var out []schemaStep
	for path != "" {
		switch {
		case strings.HasPrefix(path, ".properties["):
			path = path[len(".properties["):]
			end := strings.Index(path, "]")
			for end >= 0 && end+1 < len(path) && path[end+1] != '.' {
				next := strings.Index(path[end+1:], "]")
				if next < 0 {
					return nil, false
				}
				end += 1 + next
			}
			if end < 0 {
				return nil, false
			}
			out = append(out, schemaStep{property: path[:end]})
			path = path[end+1:]
		case strings.HasPrefix(path, ".items"):
			out = append(out, schemaStep{items: true})
			path = path[len(".items"):]
		case strings.HasPrefix(path, ".additionalProperties"):
			out = append(out, schemaStep{values: true})
			path = path[len(".additionalProperties"):]
		default:
			return nil, false
		}
	}
	return out, true
}

// cellensPath writes steps as a .cellens entry writes a path.
func cellensPath(steps []schemaStep) string {
	var b strings.Builder
	for _, s := range steps {
		switch {
		case s.items:
			b.WriteString("[]")
		case s.values:
			b.WriteString("[*]")
		default:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s.property)
		}
	}
	return b.String()
}

// walkSchema follows steps from s, nil where s has no such node.
func walkSchema(s *structuralschema.Structural, steps []schemaStep) *structuralschema.Structural {
	for _, st := range steps {
		if s == nil {
			return nil
		}
		switch {
		case st.items:
			s = s.Items
		case st.values:
			if s.AdditionalProperties == nil {
				return nil
			}
			s = s.AdditionalProperties.Structural
		default:
			p, ok := s.Properties[st.property]
			if !ok {
				return nil
			}
			s = &p
		}
	}
	return s
}

// withRules returns s with rules as the x-kubernetes-validations of the node
// steps lead to, sharing every node off the way to it with s, which it does
// not change; nil when s has no such node.
func withRules(s *structuralschema.Structural, steps []schemaStep, rules apiextensionsv1.ValidationRules) *structuralschema.Structural {
	if s == nil {
		return nil
	}
	out := *s
	if len(steps) == 0 {
		out.XValidations = rules
		return &out
	}
	st := steps[0]
	switch {
	case st.items:
		out.Items = withRules(s.Items, steps[1:], rules)
		if out.Items == nil {
			return nil
		}
	case st.values:
		if s.AdditionalProperties == nil {
			return nil
		}
		values := *s.AdditionalProperties
		values.Structural = withRules(values.Structural, steps[1:], rules)
		if values.Structural == nil {
			return nil
		}
		out.AdditionalProperties = &values
	default:
		p, ok := s.Properties[st.property]
		if !ok {
			return nil
		}
		changed := withRules(&p, steps[1:], rules)
		if changed == nil {
			return nil
		}
		out.Properties = maps.Clone(s.Properties)
		out.Properties[st.property] = *changed
	}
	return &out
}

// withoutRules removes every x-kubernetes-validations of s, which it owns.
func withoutRules(s *structuralschema.Structural) {
	if s == nil {
		return
	}
	s.XValidations = nil
	for k, p := range s.Properties {
		withoutRules(&p)
		s.Properties[k] = p
	}
	withoutRules(s.Items)
	if s.AdditionalProperties != nil {
		withoutRules(s.AdditionalProperties.Structural)
	}
	if s.ValueValidation != nil {
		for i := range s.ValueValidation.AllOf {
			withoutNestedRules(&s.ValueValidation.AllOf[i])
		}
	}
}

// withoutNestedRules removes every x-kubernetes-validations of v.
func withoutNestedRules(v *structuralschema.NestedValueValidation) {
	if v == nil {
		return
	}
	v.XValidations = nil
	for k, p := range v.Properties {
		withoutNestedRules(&p)
		v.Properties[k] = p
	}
	withoutNestedRules(v.Items)
	withoutNestedRules(v.AdditionalProperties)
	for i := range v.AllOf {
		withoutNestedRules(&v.AllOf[i])
	}
}

// bareSchema returns the schema of sv with no rule, and a key that changes
// with that schema and nothing else: made the first time, then kept.
func (sv *servedVersion) bareSchema() (*structuralschema.Structural, string) {
	sv.rules.Do(func() {
		s := sv.structural.DeepCopy()
		withoutRules(s)
		js, err := json.Marshal(s)
		if err != nil {
			js = fmt.Appendf(nil, "%p", sv) // never the same as another schema's
		}
		sum := sha256.Sum256(js)
		sv.bare, sv.schemaKey = s, hex.EncodeToString(sum[:])
	})
	return sv.bare, sv.schemaKey
}

// key is what r's results depend on but the example: the Kubernetes
// version, its schema with no rule, its place, and the rule itself.
func (r *Rule) key() string {
	_, schema := r.version.bareSchema()
	rule, err := json.Marshal(r.rule)
	if err != nil {
		rule = []byte(r.rule.Rule)
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%q\x00%s", r.CRD.kube, schema, cellensPathKey(r.steps), rule))
	return hex.EncodeToString(sum[:])
}

// cellensPathKey writes steps so that no two ways lead to the same text.
func cellensPathKey(steps []schemaStep) string {
	var b strings.Builder
	for _, s := range steps {
		switch {
		case s.items:
			b.WriteString("\x00i")
		case s.values:
			b.WriteString("\x00v")
		default:
			b.WriteString("\x00p" + s.property)
		}
	}
	return b.String()
}

// The probe of a rule is a rule at its node that is false wherever it runs,
// so that the API server's validator says, by refusing, each value it runs
// a rule on: probeOldSelf stands for a rule that reads oldSelf, which runs
// only where there is an old value, unless optionalOldSelf is set. Its
// meaning is never that of the rule.
const (
	probe        = "false"
	probeOldSelf = "[oldSelf].size() == 0"
	probeMessage = "cel-lens: the rule runs here"
)

// prepared is a rule made ready to run: the problem that keeps it from
// compiling for the Kubernetes version, or the API server's validator of
// the schema with only this rule, and that of its probe.
type prepared struct {
	problem string
	// usesOldSelf reports whether the rule reads oldSelf, and optional
	// whether it has optionalOldSelf set.
	usesOldSelf, optional bool
	validator             *cel.Validator
	probe                 *cel.Validator
}

// prepare makes r ready to run. It compiles r as the API server compiles a
// new rule for the Kubernetes version of its CRD, so a rule that does not
// compile for that version does not run, with the reason of a rule for
// which the API server refuses the CRD (the engine's check of the CRD,
// which Run reads first, may report no error on it when another rule stops
// it); the validators then compile it as the API server does when it
// serves the CRD.
func (r *Rule) prepare() *prepared {
	bare, _ := r.version.bareSchema()
	node := walkSchema(bare, r.steps)
	root := len(r.steps) == 0
	if node == nil {
		return &prepared{problem: refusedForRule + "its structural schema has no node at " + r.Path}
	}
	alone := *node
	alone.XValidations = apiextensionsv1.ValidationRules{r.rule}
	decl := model.SchemaDeclType(&alone, root || node.XEmbeddedResource)
	results, err := cel.Compile(&alone, decl, celconfig.PerCallLimit, r.CRD.kube.EnvSet(), cel.NewExpressionsEnvLoader())
	switch {
	case err != nil:
		return &prepared{problem: refusedForRule + err.Error()}
	case len(results) != 1, results[0].Error == nil && results[0].Program == nil:
		return &prepared{problem: refusedForRule + "the rule is empty"}
	case results[0].Error != nil:
		return &prepared{problem: refusedForRule + results[0].Error.Detail}
	case results[0].MessageExpressionError != nil:
		return &prepared{problem: refusedForRule + results[0].MessageExpressionError.Detail}
	}
	p := &prepared{usesOldSelf: results[0].UsesOldSelf, optional: r.rule.OptionalOldSelf != nil && *r.rule.OptionalOldSelf}
	p.validator = cel.NewValidator(withRules(bare, r.steps, apiextensionsv1.ValidationRules{r.rule}), true, celconfig.PerCallLimit)
	stand := apiextensionsv1.ValidationRule{Rule: probe, Message: probeMessage, OptionalOldSelf: r.rule.OptionalOldSelf}
	if p.usesOldSelf {
		stand.Rule = probeOldSelf
	}
	p.probe = cel.NewValidator(withRules(bare, r.steps, apiextensionsv1.ValidationRules{stand}), true, celconfig.PerCallLimit)
	return p
}

// span returns where the node at the field path item of the document doc
// is written: from its first character to the first character of the
// document after it that is not part of it, the end of the file for the
// last; both zero when doc has no such node.
func span(doc *yaml.Node, item string) (from, to extract.Position) {
	n := content(doc)
	for _, st := range steps(item) {
		if n == nil {
			return from, to
		}
		n = resolve(n)
		switch n.Kind {
		case yaml.MappingNode:
			_, n = lookup(n, st.text)
		case yaml.SequenceNode:
			i, err := strconv.Atoi(st.text)
			if err != nil || i < 0 || i >= len(n.Content) {
				return from, to
			}
			n = n.Content[i]
		case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
			return from, to
		}
	}
	if n == nil {
		return from, to
	}
	inside := map[*yaml.Node]bool{}
	var mark func(*yaml.Node)
	mark = func(m *yaml.Node) {
		inside[m] = true
		for _, c := range m.Content {
			mark(c)
		}
	}
	mark(n)
	from = extract.Position{Line: n.Line, Column: n.Column}
	to = extract.Position{Line: math.MaxInt, Column: 1}
	var after func(*yaml.Node)
	after = func(m *yaml.Node) {
		at := extract.Position{Line: m.Line, Column: m.Column}
		if !inside[m] && before(from, at) && before(at, to) {
			to = at
		}
		for _, c := range m.Content {
			after(c)
		}
	}
	after(doc)
	return from, to
}

// before reports whether a comes before b in a file.
func before(a, b extract.Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
}
