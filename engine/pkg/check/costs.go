package check

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	kyvernov1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	"go.yaml.in/yaml/v3"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// CostKind says whether Costs has the API server's numbers for an
// expression, and why not when it has none.
type CostKind string

// The kinds of Cost.
const (
	// CostEstimated: the API server estimates the expression's cost when the
	// CustomResourceDefinition is created; the numbers of the Cost are set.
	CostEstimated CostKind = "estimated"
	// CostNotCompiled: the API server compiles the expression to nothing it
	// can estimate: it does not compile, it is the messageExpression of a
	// rule that does not compile (the API server stops at the rule), or its
	// schema node has no type for CEL.
	CostNotCompiled CostKind = "not compiled"
	// CostSchemaNotCompiled: the API server compiles no rule of the
	// expression's schema, one that is not structural or whose defaults it
	// refuses, or none of the expression's schema node, which has a schema
	// error at or below it: the expressions Check reports as not compiled
	// with StopSchema.
	CostSchemaNotCompiled CostKind = "schema not compiled"
	// CostPolicy: an expression of a ValidatingAdmissionPolicy or a
	// MutatingAdmissionPolicy. The API server estimates no cost when a policy
	// is applied; it stops an evaluation that goes past its run-time limit.
	CostPolicy CostKind = "policy"
	// CostKyverno: an expression of a Kyverno ValidatingPolicy, answered while
	// feature.Kyverno is on. Kyverno estimates no cost when a policy is
	// applied.
	CostKyverno CostKind = "kyverno"
)

// Cost is what the API server of the Kubernetes version of Costs would
// estimate an expression to cost, or why it estimates nothing. Only a Cost of
// kind CostEstimated has numbers; the others have them all zero and no
// causes. The numbers are units of the API server's cost estimator.
type Cost struct {
	// Kind says whether the numbers are set, and why not.
	Kind CostKind
	// Start and End are the range of the expression in the file, as package
	// extract finds it.
	Start, End extract.Position
	// FieldPath is the path of the expression as the API server writes it:
	// for a CustomResourceDefinition whose versions share their schema, the
	// path in spec.validation, where the API server moves it, such as
	// "spec.validation.openAPIV3Schema.x-kubernetes-validations[0].rule",
	// for every version's copy of the expression; otherwise its path in the
	// file, such as
	// "spec.versions[0].schema.openAPIV3Schema.properties[spec].x-kubernetes-validations[0].rule"
	// or "spec.validations[0].expression".
	FieldPath string
	// Version is the name of the CustomResourceDefinition version whose
	// schema holds the expression, such as "v1". It is empty for a schema
	// every version shares (spec.validation) and for a policy.
	Version string
	// Versions are the names of the CustomResourceDefinition versions whose
	// schema holds the expression, in the file's order: Version alone for a
	// schema of its own, every version for one they share, which the API
	// server moves to spec.validation even when there is only one version.
	// Empty for a policy.
	Versions []string
	// Cost is the expression's estimated cost: a rule's MaxCost times the
	// cardinality of its schema node (how many times it may run in one
	// object), a messageExpression's own MaxCost.
	Cost uint64
	// Budget is the most an expression may cost for the API server to take it
	// (validation.StaticEstimatedCostLimit, 10,000,000). The API server
	// refuses the CustomResourceDefinition when Cost is over it.
	Budget uint64
	// Total is the estimated cost of every rule and messageExpression of the
	// expression's schema together, those of every node the API server
	// compiles.
	Total uint64
	// TotalBudget is the most the expressions of one schema may cost together
	// (validation.StaticEstimatedCRDCostLimit, 100,000,000). The API server
	// refuses the CustomResourceDefinition when Total is over it.
	TotalBudget uint64
	// MostExpensive are the expressions that add the most to Total, the most
	// expensive first, at most four, as the API server's TotalCost keeps
	// them: an expression under 1% of TotalBudget is never among them.
	MostExpensive []ExpressionCost
	// Causes are what makes the expression expensive, as the cost
	// diagnostics of Check name them, at most three: an unbounded list or map
	// it runs for each element of, an unbounded string, list or map it reads,
	// a number it turns into text. Empty when none is found.
	Causes []Cause
}

// ExpressionCost is the estimated cost of one expression of a
// CustomResourceDefinition schema.
type ExpressionCost struct {
	// FieldPath is the path of the expression as the API server writes it
	// (see Cost.FieldPath).
	FieldPath string
	// Node is the field path, in the custom resource, of the schema node the
	// expression is on, such as "spec.ports[*]", or "self" for the root.
	Node string
	// Cost is the expression's estimated cost (see Cost.Cost).
	Cost uint64
}

// Cause is something in a CustomResourceDefinition schema that makes an
// expression expensive.
type Cause struct {
	// Text says what it is, in the words of the cost diagnostics of Check,
	// with field paths as Markdown inline code.
	Text string
	// FieldPath is the field, in the custom resource, that the limit would go
	// on, such as "spec.ports" (see Fix.FieldPath); empty when Limit is.
	FieldPath string
	// Limit is the schema keyword that would bound the cause: "maxLength",
	// "maxItems" or "maxProperties". It is empty when no limit would bound
	// it, as for a number turned into text.
	Limit string
	// Size is how many characters, items or entries the estimator assumes
	// for the field, 0 when not known; for a cause that Repeats, how many
	// times the estimator assumes the expression runs.
	Size uint64
	// Repeats is true for a list or map above the expression's schema node:
	// the expression runs once for each of its elements. It is false for a
	// field the expression reads, and for a number turned into text.
	Repeats bool
}

// Costs answers, for every CEL expression of data that Check checks, the cost
// the API server of the Kubernetes version chosen with WithVersion would
// estimate, or why it estimates none, in file order. It takes the file's name
// and text and the options Check takes; data and file are read as Check reads
// them.
//
// A CustomResourceDefinition's expressions are estimated with the API
// server's own compiler, environment and cost estimator, under UnderVersion,
// as when the CustomResourceDefinition is created: the API server skips the
// per-expression limit for a rule an update leaves unchanged, and Costs does
// not. The types of WithAssumedTypes are not placed, as the API server never
// sees them. The expressions of a policy are answered with CostPolicy, and of
// a Kyverno ValidatingPolicy, while feature.Kyverno is on, with CostKyverno.
// A document Check cannot read, and every document Check leaves alone, add
// nothing.
//
// When ctx is cancelled, Costs stops and returns ctx's error and no costs. A
// document whose YAML does not parse adds nothing, and Costs returns the
// costs of the others with the error Check returns for it (see
// extract.SyntaxError).
func Costs(ctx context.Context, file string, data []byte, opts ...Option) ([]Cost, error) {
	var set settings
	for _, o := range opts {
		o(&set)
	}
	text := extract.UTF8(data)
	docs, broken, err := extract.Documents(ctx, file, text)
	if err != nil {
		return nil, err
	}
	exprs, findErr := extract.FindIn(ctx, file, text, docs, broken)
	if errors.Is(findErr, context.Canceled) || errors.Is(findErr, context.DeadlineExceeded) {
		return nil, findErr
	}
	byDoc := expressionsByDocument(exprs)
	r := reporter{version: set.version, kyverno: set.on.Has(feature.Kyverno), file: file, lines: extract.SplitLines(text), data: data, eol: "\n", validations: set.validations}
	if r.validations != nil {
		r.textSum = textSum(data)
	}
	var out []Cost
	for _, d := range docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.doc = d.Index
		got, err := r.documentCosts(ctx, d.Node, byDoc[d.Index])
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		if err != nil {
			continue // Check cannot read the document, so it checks none of its expressions
		}
		out = append(out, got...)
	}
	slices.SortStableFunc(out, func(a, b Cost) int {
		if a.Start.Line != b.Start.Line {
			return a.Start.Line - b.Start.Line
		}
		return a.Start.Column - b.Start.Column
	})
	return out, findErr
}

// documentCosts answers the costs of the expressions exprs of one document,
// by field path, for the kinds Check checks. The error says the document
// cannot be read, or is ctx's.
func (r *reporter) documentCosts(ctx context.Context, node *yaml.Node, exprs map[string]extract.Expression) ([]Cost, error) {
	head, ok := typeOf(node)
	if !ok || len(exprs) == 0 {
		return nil, nil
	}
	group, version, _ := strings.Cut(head.APIVersion, "/")
	switch {
	case group == "apiextensions.k8s.io" && version == "v1" && head.Kind == "CustomResourceDefinition":
		return r.crdCosts(ctx, node, exprs)
	case group == "admissionregistration.k8s.io" && head.Kind == "ValidatingAdmissionPolicy":
		return unestimated(exprs, CostPolicy), r.readOnly(ctx, node, &admissionregistrationv1.ValidatingAdmissionPolicy{}, head.Kind)
	case group == "admissionregistration.k8s.io" && head.Kind == "MutatingAdmissionPolicy":
		return unestimated(exprs, CostPolicy), r.readOnly(ctx, node, &admissionregistrationv1.MutatingAdmissionPolicy{}, head.Kind)
	case group == "policies.kyverno.io" && version == "v1" && head.Kind == "ValidatingPolicy" && r.kyverno:
		return unestimated(exprs, CostKyverno), r.readOnly(ctx, node, &kyvernov1.ValidatingPolicy{}, head.Kind)
	}
	return nil, nil
}

// expressionsByDocument groups exprs by the index of their document, and in
// each by field path.
func expressionsByDocument(exprs []extract.Expression) map[int]map[string]extract.Expression {
	byDoc := map[int]map[string]extract.Expression{}
	for _, e := range exprs {
		if byDoc[e.Document] == nil {
			byDoc[e.Document] = map[string]extract.Expression{}
		}
		byDoc[e.Document][e.FieldPath] = e
	}
	return byDoc
}

// unestimated answers every expression of exprs with kind and no number.
func unestimated(exprs map[string]extract.Expression, kind CostKind) []Cost {
	out := make([]Cost, 0, len(exprs))
	for path, e := range exprs {
		out = append(out, Cost{Kind: kind, Start: e.Start, End: e.End, FieldPath: path})
	}
	return out
}

// crdCosts answers the costs of the expressions exprs of a
// CustomResourceDefinition, read from node. The API server's validation of it
// tells which schemas and schema nodes it compiles no rule of; the others are
// compiled and estimated as the validation does, and every cost is observed by
// its schema's TotalCost. Both run under UnderVersion.
func (r *reporter) crdCosts(ctx context.Context, node *yaml.Node, exprs map[string]extract.Expression) ([]Cost, error) {
	var versioned apiextensionsv1.CustomResourceDefinition
	if _, err := r.read(ctx, node, &versioned); err != nil {
		return nil, fmt.Errorf("read CustomResourceDefinition: %w", err)
	}
	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(&versioned)
	var crd apiextensions.CustomResourceDefinition
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&versioned, &crd, nil); err != nil {
		return nil, fmt.Errorf("convert CustomResourceDefinition: %w", err)
	}
	comp := &compiler{envSet: r.version.EnvSet()}
	problems, whole, kept := r.validations.get(r.textSum, r.version, r.doc)
	if !kept {
		// While the validation runs, the rules are compiled in the
		// background; with the validation kept there is nothing to overlap.
		wait, cancel := r.warmCompile(ctx, crd.DeepCopy(), comp)
		defer func() {
			cancel()
			wait()
		}()
		if err := UnderVersion(r.version, func() { problems = validation.ValidateCustomResourceDefinition(ctx, &crd) }); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		if whole, err = r.wholeSchemaErrors(ctx, &crd, problems); err != nil {
			return nil, err
		}
		r.validations.put(r.textSum, r.version, r.doc, problems, whole)
		wait()
	}
	var errNodes []string
	for _, fe := range problems {
		// A cost error is no schema error: the API server finds it by
		// compiling the rules, and the total's is at the root.
		if !isCostError(fe) && isSchemaError(fe) {
			errNodes = append(errNodes, errorNode(fe))
		}
	}
	var out []Cost
	var estimateErr error
	if err := UnderVersion(r.version, func() {
		out, estimateErr = r.estimate(ctx, &crd, exprs, whole, errNodes, comp)
	}); err != nil {
		return nil, err
	}
	return out, estimateErr
}

// estimate is crdCosts once the schema errors are known: whole are the
// schemas the API server compiles no rule of, errNodes the field paths of
// the schema errors, by the node they keep from being compiled (see
// errorNode). The rules are compiled with comp.
func (r *reporter) estimate(ctx context.Context, crd *apiextensions.CustomResourceDefinition, exprs map[string]extract.Expression, whole, errNodes []string, comp *compiler) ([]Cost, error) {
	var out []Cost
	answered := map[string]bool{} // the field paths, in the file, of the expressions answered
	var versions []string         // those of the schema in hand
	answer := func(c Cost, path string) {
		for _, e := range expressionsAt(exprs, path) {
			if answered[e.FieldPath] {
				continue
			}
			answered[e.FieldPath] = true
			c.Start, c.End, c.FieldPath = e.Start, e.End, path
			c.Versions = slices.Clone(versions)
			c.MostExpensive = slices.Clone(c.MostExpensive)
			out = append(out, c)
		}
	}
	for _, root := range crdRoots(crd) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		versions = rootVersions(crd, root)
		rootNode := &schemaNode{schema: root.schema, path: root.path}
		isWhole := slices.Contains(whole, root.path.String())
		if !isWhole {
			rootNode.cel = validation.RootCELContext(root.schema)
		}
		buildNodes(rootNode, nil)
		skipped := func(n *schemaNode) bool { return isWhole || underError(n.path.String(), errNodes) }
		var rules []ruleCost
		if err := collectRules(ctx, rootNode, comp, func(n *schemaNode) bool {
			return len(n.schema.XValidations) > 0 && !skipped(n) && !r.ruleOfGuessedSchema(&field.Error{Field: n.path.Child("x-kubernetes-validations").String() + "["})
		}, &rules); err != nil {
			return nil, err
		}
		if err := forEachNode(ctx, rootNode, func(n *schemaNode) {
			if !skipped(n) {
				return
			}
			for i, v := range n.schema.XValidations {
				entry := n.path.Child("x-kubernetes-validations").Index(i)
				answer(Cost{Kind: CostSchemaNotCompiled, Version: root.version}, entry.Child("rule").String())
				if v.MessageExpression != "" {
					answer(Cost{Kind: CostSchemaNotCompiled, Version: root.version}, entry.Child("messageExpression").String())
				}
			}
		}); err != nil {
			return nil, err
		}
		if len(rules) == 0 {
			continue
		}
		total := rootNode.cel.TotalCost
		byPath := make(map[string]ruleCost, len(rules))
		for _, rc := range rules {
			byPath[rc.path.String()] = rc
		}
		top := mostExpensive(total, byPath)
		for _, rc := range rules {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if rc.failed {
				answer(Cost{Kind: CostNotCompiled, Version: root.version}, rc.path.String())
				continue
			}
			c := Cost{
				Kind: CostEstimated, Version: root.version,
				Cost: rc.cost, Budget: validation.StaticEstimatedCostLimit,
				Total: total.Total, TotalBudget: validation.StaticEstimatedCRDCostLimit,
				MostExpensive: top,
			}
			for _, d := range drivers(rc) {
				c.Causes = append(c.Causes, Cause{Text: d.text, FieldPath: d.fix.FieldPath, Limit: d.fix.Limit, Size: d.size, Repeats: d.repeats})
			}
			answer(c, rc.path.String())
		}
	}
	// What is left the API server compiles to nothing it can estimate: the
	// messageExpression of a rule that does not compile, the rules of a node
	// with no type for CEL.
	for path, e := range exprs {
		if !answered[path] {
			server, version := serverPath(crd, path)
			names := []string{version}
			if version == "" {
				names = rootVersions(crd, schemaRoot{})
			}
			out = append(out, Cost{Kind: CostNotCompiled, Start: e.Start, End: e.End, FieldPath: server, Version: version, Versions: names})
		}
	}
	return out, ctx.Err()
}

// warmCompile compiles the rules of every schema of crd with comp in the
// background, under UnderVersion, while the API server's validation runs, so
// that comp knows them when estimate asks: estimate leaves out the rules the
// validation says the API server does not compile, so it waits for the
// validation, but most of its work does not. What it finds is only kept in
// comp, so the result of estimate is the same without it; a failure here is
// met again by estimate. The work ends by itself; wait waits for it and cancel
// stops it early. Both can be called more than once.
func (r *reporter) warmCompile(ctx context.Context, crd *apiextensions.CustomResourceDefinition, comp *compiler) (wait, cancel func()) {
	ctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = UnderVersion(r.version, func() {
			for _, root := range crdRoots(crd) {
				n := &schemaNode{schema: root.schema, cel: validation.RootCELContext(root.schema), path: root.path}
				buildNodes(n, nil)
				var rules []ruleCost
				if collectRules(ctx, n, comp, func(n *schemaNode) bool { return len(n.schema.XValidations) > 0 }, &rules) != nil {
					return
				}
			}
		})
	}()
	return func() { <-done }, stop
}

// schemaRoot is a schema of a CustomResourceDefinition.
type schemaRoot struct {
	path   *field.Path
	schema *apiextensions.JSONSchemaProps
	// version is the name of the version it is the schema of, "" for the
	// one every version shares (spec.validation).
	version string
}

// rootVersions are the names of the versions of crd whose schema root is:
// its version, or every version for the schema they share.
func rootVersions(crd *apiextensions.CustomResourceDefinition, root schemaRoot) []string {
	if root.version != "" {
		return []string{root.version}
	}
	names := make([]string, 0, len(crd.Spec.Versions))
	for _, v := range crd.Spec.Versions {
		names = append(names, v.Name)
	}
	return names
}

// crdRoots are the schemas of crd: the one in spec.validation, which the
// API server's conversion makes when every version has the same, and those of
// the versions.
func crdRoots(crd *apiextensions.CustomResourceDefinition) []schemaRoot {
	var roots []schemaRoot
	if v := crd.Spec.Validation; v != nil && v.OpenAPIV3Schema != nil {
		roots = append(roots, schemaRoot{field.NewPath("spec", "validation", "openAPIV3Schema"), v.OpenAPIV3Schema, ""})
	}
	for i, v := range crd.Spec.Versions {
		if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
			roots = append(roots, schemaRoot{field.NewPath("spec", "versions").Index(i).Child("schema", "openAPIV3Schema"), v.Schema.OpenAPIV3Schema, v.Name})
		}
	}
	return roots
}

// mostExpensive is the MostExpensive of a Cost: those of total, with the
// schema node of each from byPath.
func mostExpensive(total *validation.TotalCost, byPath map[string]ruleCost) []ExpressionCost {
	out := make([]ExpressionCost, 0, len(total.MostExpensive))
	for _, m := range total.MostExpensive {
		ec := ExpressionCost{FieldPath: m.Path.String(), Cost: m.Cost}
		if rc, ok := byPath[ec.FieldPath]; ok {
			ec.Node = nodeName(rc.node)
		}
		out = append(out, ec)
	}
	return out
}

// forEachNode calls fn for n and every node below it, until ctx is
// cancelled, whose error it returns.
func forEachNode(ctx context.Context, n *schemaNode, fn func(*schemaNode)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fn(n)
	for _, c := range n.children {
		if err := forEachNode(ctx, c, fn); err != nil {
			return err
		}
	}
	return nil
}

// serverPath is the field path the API server gives the expression at path,
// a field path in the file: in spec.validation when the versions of crd share
// their schema, else path itself; and the name of the version whose schema
// holds it, "" for a shared schema.
func serverPath(crd *apiextensions.CustomResourceDefinition, path string) (server, version string) {
	shared := crd.Spec.Validation != nil && crd.Spec.Validation.OpenAPIV3Schema != nil
	for i, v := range crd.Spec.Versions {
		prefix := fmt.Sprintf("spec.versions[%d].schema.openAPIV3Schema", i)
		rest, ok := strings.CutPrefix(path, prefix)
		switch {
		case !ok:
		case shared:
			return "spec.validation.openAPIV3Schema" + rest, ""
		default:
			return path, v.Name
		}
	}
	return path, ""
}
