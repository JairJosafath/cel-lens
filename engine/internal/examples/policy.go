package examples

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	celcommon "github.com/google/cel-go/common"
	celast "github.com/google/cel-go/common/ast"
	celparser "github.com/google/cel-go/parser"
	"go.yaml.in/yaml/v3"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// policyWord is in the text of every file with a ValidatingAdmissionPolicy
// or a ValidatingAdmissionPolicyBinding.
const policyWord = "ValidatingAdmissionPolicy"

// The API version and kinds of a policy and its binding.
const (
	admissionV1 = "admissionregistration.k8s.io/v1"
	policyKind  = "ValidatingAdmissionPolicy"
	bindingKind = "ValidatingAdmissionPolicyBinding"
)

// Policy is a ValidatingAdmissionPolicy (admissionregistration.k8s.io/v1),
// with the defaults the API server gives one. ReadPolicies makes it; it does
// not change after, so it is safe for concurrent use.
type Policy struct {
	// File is the name of the file it is in, as ReadPolicies was given it;
	// Name its metadata.name; Line the line its content starts at, from 1.
	File, Name string
	Line       int
	spec       admissionregistrationv1.ValidatingAdmissionPolicy
	// data is the text of File, doc the index of its document there, node
	// that document, and kube the Kubernetes version it was read for.
	data []byte
	doc  int
	node *yaml.Node
	kube kubeversion.Version
}

// Binding is a ValidatingAdmissionPolicyBinding
// (admissionregistration.k8s.io/v1), with the defaults the API server gives
// one. It is safe for concurrent use.
type Binding struct {
	// File is the name of the file it is in, Line the line its content
	// starts at, from 1, and Name its metadata.name.
	File string
	Line int
	Name string
	spec admissionregistrationv1.ValidatingAdmissionPolicyBinding
}

// PolicyName is the name of the policy b binds.
func (b *Binding) PolicyName() string { return b.spec.Spec.PolicyName }

// Actions are b's validationActions, such as Deny or Warn.
func (b *Binding) Actions() []string {
	out := make([]string, len(b.spec.Spec.ValidationActions))
	for i, a := range b.spec.Spec.ValidationActions {
		out[i] = string(a)
	}
	return out
}

// HasPolicies reports whether data may hold a ValidatingAdmissionPolicy or a
// ValidatingAdmissionPolicyBinding: whether it has their kind's name.
func HasPolicies(data []byte) bool { return bytes.Contains(data, []byte(policyWord)) }

// ReadPolicies returns the ValidatingAdmissionPolicies of data, the text of
// file, for the Kubernetes version v, in the order of the file. One whose
// spec cannot be read, as the API server would not read it either, or that
// has no name, is left out. A cancelled ctx gives none.
func ReadPolicies(ctx context.Context, file string, data []byte, v kubeversion.Version) []*Policy {
	out, _ := ReadPoliciesWithProblems(ctx, file, data, v)
	return out
}

// ReadPoliciesWithProblems is ReadPolicies, with the problems of each policy
// it leaves out because its YAML cannot be read as the API server reads it:
// for a field of the wrong type, the problem check gives, at the value (such
// as `n`, which kubectl reads as a boolean where a string is wanted). A policy
// with no name is left out without one.
func ReadPoliciesWithProblems(ctx context.Context, file string, data []byte, v kubeversion.Version) ([]*Policy, []check.Diagnostic) {
	if !HasPolicies(data) {
		return nil, nil
	}
	docs, _, err := extract.Documents(ctx, file, data)
	if err != nil {
		return nil, nil
	}
	var (
		out      []*Policy
		problems []check.Diagnostic
	)
	for _, d := range docs {
		apiVersion, kind, line := typeMeta(d.Node)
		if apiVersion != admissionV1 || kind != policyKind {
			continue
		}
		var p admissionregistrationv1.ValidatingAdmissionPolicy
		if !decodeInto(d.Node, &p) {
			problems = append(problems, unreadable(ctx, file, data, d.Node, &admissionregistrationv1.ValidatingAdmissionPolicy{}, "the ValidatingAdmissionPolicy cannot be read")...)
			continue
		}
		if p.Name == "" {
			continue
		}
		defaultPolicy(&p)
		out = append(out, &Policy{File: file, Name: p.Name, Line: line, spec: p, data: data, doc: d.Index, node: d.Node, kube: v})
	}
	return out, problems
}

// ReadBindings returns the ValidatingAdmissionPolicyBindings of data, the
// text of file, in the order of the file; one that cannot be read, or that
// names no policy, is left out.
func ReadBindings(ctx context.Context, file string, data []byte) []*Binding {
	out, _ := ReadBindingsWithProblems(ctx, file, data)
	return out
}

// ReadBindingsWithProblems is ReadBindings, with the problems of each binding
// it leaves out because its YAML cannot be read as the API server reads it,
// as ReadPoliciesWithProblems gives them. A binding that names no policy is
// left out without one.
func ReadBindingsWithProblems(ctx context.Context, file string, data []byte) ([]*Binding, []check.Diagnostic) {
	if !HasPolicies(data) {
		return nil, nil
	}
	docs, _, err := extract.Documents(ctx, file, data)
	if err != nil {
		return nil, nil
	}
	var (
		out      []*Binding
		problems []check.Diagnostic
	)
	for _, d := range docs {
		apiVersion, kind, line := typeMeta(d.Node)
		if apiVersion != admissionV1 || kind != bindingKind {
			continue
		}
		var b admissionregistrationv1.ValidatingAdmissionPolicyBinding
		if !decodeInto(d.Node, &b) {
			problems = append(problems, unreadable(ctx, file, data, d.Node, &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}, "the ValidatingAdmissionPolicyBinding cannot be read")...)
			continue
		}
		if b.Spec.PolicyName == "" {
			continue
		}
		if b.Spec.MatchResources != nil {
			defaultMatchResources(b.Spec.MatchResources)
		}
		out = append(out, &Binding{File: file, Line: line, Name: b.Name, spec: b})
	}
	return out, problems
}

// decodeInto reads the document doc into v as kubectl sends it to the API
// server, false when it cannot.
func decodeInto(doc *yaml.Node, v any) bool {
	js, err := toJSON(doc)
	return err == nil && json.Unmarshal(js, v) == nil
}

// defaultPolicy gives p the defaults the API server gives a
// ValidatingAdmissionPolicy (SetDefaults_ValidatingAdmissionPolicySpec and
// SetDefaults_MatchResources of Kubernetes' pkg/apis/admissionregistration/v1):
// failurePolicy Fail, and those of its matchConstraints.
func defaultPolicy(p *admissionregistrationv1.ValidatingAdmissionPolicy) {
	if p.Spec.FailurePolicy == nil {
		fail := admissionregistrationv1.Fail
		p.Spec.FailurePolicy = &fail
	}
	if p.Spec.MatchConstraints != nil {
		defaultMatchResources(p.Spec.MatchConstraints)
	}
}

// defaultMatchResources gives m the defaults the API server gives
// matchResources: matchPolicy Equivalent, selectors that select everything,
// and the scope * of each rule.
func defaultMatchResources(m *admissionregistrationv1.MatchResources) {
	if m.MatchPolicy == nil {
		equivalent := admissionregistrationv1.Equivalent
		m.MatchPolicy = &equivalent
	}
	if m.NamespaceSelector == nil {
		m.NamespaceSelector = &metav1.LabelSelector{}
	}
	if m.ObjectSelector == nil {
		m.ObjectSelector = &metav1.LabelSelector{}
	}
	for _, rules := range [][]admissionregistrationv1.NamedRuleWithOperations{m.ResourceRules, m.ExcludeResourceRules} {
		for i := range rules {
			if rules[i].Scope == nil {
				all := admissionregistrationv1.AllScopes
				rules[i].Scope = &all
			}
		}
	}
}

// PolicyField is the part of a ValidatingAdmissionPolicy a PolicyRule is in.
type PolicyField int

// The parts of a policy with a verdict of their own.
const (
	// FieldValidation is an entry of spec.validations.
	FieldValidation PolicyField = iota
	// FieldMatchCondition is an entry of spec.matchConditions.
	FieldMatchCondition
	// FieldAuditAnnotation is an entry of spec.auditAnnotations.
	FieldAuditAnnotation
)

// PolicyRule is an expression of a ValidatingAdmissionPolicy with a verdict
// of its own on an example: a validation, a match condition or an audit
// annotation. A variable has none: the API server evaluates it when an
// expression reads it, and its error is that expression's.
type PolicyRule struct {
	Policy *Policy
	Field  PolicyField
	// Index is its place in its list, from 0; Name is a match condition's
	// name or an audit annotation's key, "" for a validation.
	Index int
	Name  string
	// Text is the expression as YAML decodes it; Start and End are where it
	// is written, MessageStart and MessageEnd where a validation's
	// messageExpression is, both zero when it has none.
	Text                     string
	Start, End               extract.Position
	MessageStart, MessageEnd extract.Position
	// Document and FieldPath are those of its expression as package extract
	// finds it, such as spec.validations[1].expression.
	Document  int
	FieldPath string
}

// Rules returns the expressions of p with a verdict of their own, in the
// order of its file. The error is ctx's.
func (p *Policy) Rules(ctx context.Context) ([]*PolicyRule, error) {
	exprs, _ := extract.Find(ctx, p.File, p.data)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []*PolicyRule
	byItem := map[string]*PolicyRule{}
	for _, e := range exprs {
		if e.Document != p.doc {
			continue
		}
		item, ok := strings.CutSuffix(e.FieldPath, "."+string(e.Kind))
		if !ok {
			continue
		}
		if e.Kind == extract.KindMessageExpression {
			if r, ok := byItem[item]; ok {
				r.MessageStart, r.MessageEnd = e.Start, e.End
			}
			continue
		}
		field, index, ok := policyItem(item, e.Kind)
		if !ok {
			continue
		}
		r := &PolicyRule{Policy: p, Field: field, Index: index, Text: e.Text, Start: e.Start, End: e.End, Document: e.Document, FieldPath: e.FieldPath}
		switch field {
		case FieldMatchCondition:
			if index < len(p.spec.Spec.MatchConditions) {
				r.Name = p.spec.Spec.MatchConditions[index].Name
			}
		case FieldAuditAnnotation:
			if index < len(p.spec.Spec.AuditAnnotations) {
				r.Name = p.spec.Spec.AuditAnnotations[index].Key
			}
		case FieldValidation:
		}
		byItem[item] = r
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b *PolicyRule) int {
		return cmp.Or(a.Start.Line-b.Start.Line, a.Start.Column-b.Start.Column)
	})
	return out, nil
}

// policyItem reads the field path item of an expression of kind, such as
// spec.validations[2], as the part and the index it names; false for an
// expression with no verdict of its own.
func policyItem(item string, kind extract.Kind) (PolicyField, int, bool) {
	for _, f := range []struct {
		prefix string
		kind   extract.Kind
		field  PolicyField
	}{
		{"spec.validations[", extract.KindExpression, FieldValidation},
		{"spec.matchConditions[", extract.KindExpression, FieldMatchCondition},
		{"spec.auditAnnotations[", extract.KindValueExpression, FieldAuditAnnotation},
	} {
		rest, ok := strings.CutPrefix(item, f.prefix)
		if !ok || kind != f.kind {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSuffix(rest, "]"))
		if err != nil || !strings.HasSuffix(rest, "]") {
			return 0, 0, false
		}
		return f.field, index, true
	}
	return 0, 0, false
}

// reads are the variables of a policy's environment an expression reads,
// directly or through spec.variables.
type reads struct {
	params, authorizer bool
}

// policyReads returns what each expression of spec reads, by its text: the
// identifiers params and authorizer, and variables.<name> of a variable
// that reads them. An expression that does not parse reads nothing.
func policyReads(spec admissionregistrationv1.ValidatingAdmissionPolicySpec) func(expr string) reads {
	vars := map[string]reads{}
	of := func(expr string) reads {
		var r reads
		for _, ref := range references(expr) {
			switch ref {
			case "params":
				r.params = true
			case "authorizer":
				r.authorizer = true
			default:
				if name, ok := strings.CutPrefix(ref, "variables."); ok {
					v := vars[name]
					r.params, r.authorizer = r.params || v.params, r.authorizer || v.authorizer
				}
			}
		}
		return r
	}
	for _, v := range spec.Variables {
		vars[v.Name] = of(v.Expression)
	}
	return of
}

// references returns the identifiers expr reads, and variables.<name> for
// each variable it reads, as the CEL parser reads the expression; none when
// it does not parse.
func references(expr string) []string {
	p, err := celparser.NewParser(celparser.Macros(celparser.AllMacros...), celparser.EnableOptionalSyntax(true))
	if err != nil {
		return nil
	}
	parsed, errs := p.Parse(celcommon.NewTextSource(expr))
	if len(errs.GetErrors()) > 0 {
		return nil
	}
	var out []string
	celast.PostOrderVisit(parsed.Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		if e.Kind() == celast.IdentKind {
			out = append(out, e.AsIdent())
		}
		if e.Kind() != celast.SelectKind {
			return
		}
		if s := e.AsSelect(); s.Operand().Kind() == celast.IdentKind && s.Operand().AsIdent() == "variables" {
			out = append(out, "variables."+s.FieldName())
		}
	}))
	return out
}

// PolicyKinds returns the built-in kinds the documents of a workspace may
// need to be read as for policies to run, which the Kubernetes version kube
// serves: those their resource rules may match, in any version, as
// matchPolicy Equivalent matches another version, their params' kinds, and
// Namespace, whose labels a namespaceSelector reads; sorted, each once.
// anyKind is true when a rule matches any group or any resource: any
// built-in kind may then be one.
func PolicyKinds(policies []*Policy, kube kubeversion.Version) (kinds []string, anyKind bool) {
	if len(policies) == 0 {
		return nil, false
	}
	served := matchkind.Kinds(kube.Minor())
	kinds = []string{"Namespace"}
	for _, p := range policies {
		if pk := p.spec.Spec.ParamKind; pk != nil {
			kinds = append(kinds, pk.Kind)
		}
		m := p.spec.Spec.MatchConstraints
		if m == nil {
			continue
		}
		for _, r := range m.ResourceRules {
			for _, g := range r.APIGroups {
				for _, res := range r.Resources {
					res, _, _ = strings.Cut(res, "/")
					if g == "*" || res == "*" {
						anyKind = true
						continue
					}
					for _, k := range served {
						if gvr, _, ok := matchkind.Resource(k.GVK); ok && k.GVK.Group == g && gvr.Resource == res {
							kinds = append(kinds, k.GVK.Kind)
						}
					}
				}
			}
		}
	}
	slices.Sort(kinds)
	return slices.Compact(kinds), anyKind
}
