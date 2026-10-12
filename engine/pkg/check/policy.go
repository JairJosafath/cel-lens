package check

import (
	"context"
	"fmt"

	celgo "github.com/google/cel-go/cel"
	"go.yaml.in/yaml/v3"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	plugincel "k8s.io/apiserver/pkg/admission/plugin/cel"
	"k8s.io/apiserver/pkg/cel/environment"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// accessor is an expression with the types it may evaluate to, as the
// admission CEL compiler wants it.
type accessor struct {
	name, expression string
	returns          []*celgo.Type
}

func (a accessor) GetExpression() string      { return a.expression }
func (a accessor) GetName() string            { return a.name }
func (a accessor) ReturnTypes() []*celgo.Type { return a.returns }

// plainCompiler returns the admission compiler without variable composition,
// built on first use and then shared by every policy of the Check. It keeps no
// state between expressions. The API server's cost tracking (environment.StrictCostOpt,
// which the API server always applies) is part of the environments the
// admission compiler builds, so there is no setting to choose here.
func (r *reporter) plainCompiler() plugincel.Compiler {
	if r.plain == nil {
		r.plain = plugincel.NewCompiler(r.version.EnvSet())
	}
	return r.plain
}

// compositeCompiler returns the compiler for the validations of a policy. A
// composited compiler stores the variables compiled with it, so a policy with
// variables gets a new one; policies without variables share one, built on
// first use, that never stores any.
func (r *reporter) compositeCompiler(hasVariables bool) (*plugincel.CompositedCompiler, error) {
	if hasVariables {
		return plugincel.NewCompositedCompiler(r.version.EnvSet())
	}
	if r.bare == nil {
		c, err := plugincel.NewCompositedCompiler(r.version.EnvSet())
		if err != nil {
			return nil, err
		}
		r.bare = c
	}
	return r.bare, nil
}

// policy compiles the expressions of a ValidatingAdmissionPolicy with the
// admission CEL compiler, which declares object, oldObject, request,
// namespaceObject, authorizer and, when the policy has a paramKind, params.
// Variables are compiled first and in order, so that later expressions can use
// variables.<name>; matchConditions cannot. When the policy matches one built-in
// kind, typeCheck then types object and oldObject from it.
func (r *reporter) policy(ctx context.Context, node *yaml.Node, exprs map[string]extract.Expression) error {
	var p admissionregistrationv1.ValidatingAdmissionPolicy
	if _, err := r.read(ctx, node, &p); err != nil {
		return fmt.Errorf("read ValidatingAdmissionPolicy: %w", err)
	}
	decls := plugincel.OptionalVariableDeclarations{HasParams: p.Spec.ParamKind != nil, HasAuthorizer: true}
	plain := r.plainCompiler()
	composite, err := r.compositeCompiler(len(p.Spec.Variables) > 0)
	if err != nil {
		return fmt.Errorf("build CEL environment: %w", err)
	}
	anyType := []*celgo.Type{celgo.AnyType, celgo.DynType}

	// clean holds the fields of the expressions that compiled without error.
	clean := map[string]bool{}
	// compile reports the compile error of the expression at field, if any.
	compile := func(c plugincel.Compiler, field string, a accessor) {
		e, ok := exprs[field]
		if !ok {
			return
		}
		a.expression = e.Text
		if res := c.CompileCELExpression(a, decls, environment.NewExpressions); res.Error != nil {
			r.report(e, res.Error.Detail)
		} else {
			clean[field] = true
		}
	}

	for i, v := range p.Spec.Variables {
		if err := ctx.Err(); err != nil {
			return err
		}
		field := fmt.Sprintf("spec.variables[%d].expression", i)
		if e, ok := exprs[field]; ok {
			a := accessor{name: v.Name, expression: e.Text, returns: anyType}
			if res := composite.CompileAndStoreVariable(a, decls, environment.NewExpressions); res.Error != nil {
				r.report(e, res.Error.Detail)
			}
		}
	}
	for i := range p.Spec.MatchConditions {
		compile(plain, fmt.Sprintf("spec.matchConditions[%d].expression", i), accessor{returns: []*celgo.Type{celgo.BoolType}})
	}
	for i := range p.Spec.Validations {
		if err := ctx.Err(); err != nil {
			return err
		}
		compile(composite, fmt.Sprintf("spec.validations[%d].expression", i), accessor{returns: []*celgo.Type{celgo.BoolType}})
		compile(composite, fmt.Sprintf("spec.validations[%d].messageExpression", i), accessor{returns: []*celgo.Type{celgo.StringType}})
	}
	for i := range p.Spec.AuditAnnotations {
		compile(composite, fmt.Sprintf("spec.auditAnnotations[%d].valueExpression", i), accessor{returns: []*celgo.Type{celgo.StringType, celgo.NullType}})
	}
	return r.typeCheck(ctx, &p, exprs, clean)
}
