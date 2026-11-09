package check

import (
	"context"
	"fmt"
	"strings"

	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types/traits"
	"go.yaml.in/yaml/v3"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/util/version"
	apiservercel "k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/common"
	"k8s.io/apiserver/pkg/cel/environment"
	"k8s.io/apiserver/pkg/cel/openapi"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// assumedTypeCheck does typeCheck again with the types assumed for the policy p,
// read from node (WithAssumedTypes), and adds what only that finds as a warning
// (see addAssumed). The diagnostics of typeCheck start at base. When the kinds p
// matches are not known, which typeCheck then skips, the expressions are compiled
// once, with object and oldObject dyn unless a type is assumed for them.
func (r *reporter) assumedTypeCheck(ctx context.Context, node *yaml.Node, p *admissionregistrationv1.ValidatingAdmissionPolicy, exprs map[string]extract.Expression, clean map[string]bool, base int) error {
	types := r.assumedIn(node)
	if len(types) == 0 || len(clean) == 0 {
		return nil
	}
	pl, _, err := r.placePolicy(ctx, node, types)
	if err != nil || len(pl.types) == 0 {
		return err
	}
	return r.addAssumed(ctx, base, exprs, func(e extract.Expression) []AssumedType { return pl.usedIn(e.Text) }, func() error {
		return r.typeCheckAssumed(ctx, p, pl, exprs, clean)
	})
}

// typeCheckAssumed compiles the validations of p in clean as typeCheck does,
// with the types of pl, and reports what fails as typeCheck does.
func (r *reporter) typeCheckAssumed(ctx context.Context, p *admissionregistrationv1.ValidatingAdmissionPolicy, pl policyPlaced, exprs map[string]extract.Expression, clean map[string]bool) error {
	hasParams := p.Spec.ParamKind != nil
	paramsType := r.paramsType(p.Spec.ParamKind)
	if pl.params != nil {
		name := "cellens.params"
		if hasParams {
			name = p.Spec.ParamKind.Kind + "Params"
		}
		paramsType = declOf(pl.params, true, name)
	}
	variables := map[string]*apiservercel.DeclType{}
	for name, s := range pl.variables {
		if t := declOf(s, false, "cellens.variables."+name); t != nil {
			variables[name] = t
		}
	}
	type pass struct {
		prefix            string
		object, oldObject *apiservercel.DeclType
	}
	var passes []pass
	if pl.kinds == nil {
		passes = append(passes, pass{
			object:    declOf(pl.object[0], true, "cellens.object"),
			oldObject: declOf(pl.oldObject[0], true, "cellens.oldObject"),
		})
	}
	for i, kind := range pl.kinds {
		plain := common.SchemaDeclType(&openapi.Schema{Schema: kind.Schema}, true)
		if plain == nil {
			continue // typedComposite skips such a kind too
		}
		object := plain.MaybeAssignTypeName(kind.GVK.Kind)
		oldObject := object
		if pl.object[i] != nil {
			object = declOf(pl.object[i], true, kind.GVK.Kind+".object")
		}
		if pl.oldObject[i] != nil {
			oldObject = declOf(pl.oldObject[i], true, kind.GVK.Kind+".oldObject")
		}
		passes = append(passes, pass{prefix: kind.String() + ": ", object: object, oldObject: oldObject})
	}
	for _, ps := range passes {
		env, err := r.assumedEnv(ps.object, ps.oldObject, paramsType, hasParams, p.Spec.Variables, variables)
		if err != nil {
			return err
		}
		err = eachValidation(ctx, p, exprs, clean, func(text string) *apiservercel.Error {
			if _, issues := env.Compile(text); issues.Err() != nil {
				return &apiservercel.Error{Type: apiservercel.ErrorTypeInvalid, Detail: issues.String()}
			}
			return nil
		}, func(e extract.Expression, issues []string) {
			r.reportAs(e, SeverityWarning, ps.prefix, strings.Join(issues, "\n"))
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// declOf returns the CEL type of the schema map s, named name, as the API
// server types a schema; root says s is the root of a resource, which always
// has apiVersion, kind and metadata. It is nil when s is nil or has no type.
func declOf(s map[string]any, root bool, name string) *apiservercel.DeclType {
	if s == nil {
		return nil
	}
	sch, ok := fromMap(s)
	if !ok {
		return nil
	}
	t := common.SchemaDeclType(&openapi.Schema{Schema: sch}, root)
	if t == nil {
		return nil
	}
	return t.MaybeAssignTypeName(name)
}

// assumedEnv returns the environment the API server's type checker compiles
// the validations of a policy in, with object, oldObject and params of the
// types given (see typedEnvSet), and variables composed from variables in
// order, as the API server composes them: each variables.<name> has the type
// its expression returns, made dyn where the API server makes it so, unless
// assumed gives it a type.
func (r *reporter) assumedEnv(object, oldObject, params *apiservercel.DeclType, hasParams bool, variables []admissionregistrationv1.Variable, assumed map[string]*apiservercel.DeclType) (*celgo.Env, error) {
	base, err := typedEnvSet(r.version.EnvSet(), object, oldObject, params, hasParams)
	if err != nil {
		return nil, fmt.Errorf("build CEL environment: %w", err)
	}
	vars := apiservercel.NewObjectType(variablesTypeName, map[string]*apiservercel.DeclField{})
	declTypes := []*apiservercel.DeclType{vars}
	for _, t := range assumed {
		declTypes = append(declTypes, t)
	}
	set, err := base.Extend(environment.VersionedOptions{
		IntroducedVersion: version.MajorMinor(1, 0),
		EnvOptions:        []celgo.EnvOption{celgo.Variable("variables", vars.CelType())},
		DeclTypes:         declTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("build CEL environment: %w", err)
	}
	env, err := set.Env(environment.NewExpressions)
	if err != nil {
		return nil, fmt.Errorf("build CEL environment: %w", err)
	}
	// The environment finds the fields of variables when it compiles, so each
	// variable sees those before it, as in the API server's composition.
	for _, v := range variables {
		t, ok := assumed[v.Name]
		if !ok {
			var out *celgo.Type
			if ast, issues := env.Compile(v.Expression); issues.Err() == nil {
				out = ast.OutputType()
			}
			t = declTypeOf(out)
		}
		vars.Fields[v.Name] = apiservercel.NewDeclField(v.Name, t, true, nil, nil)
	}
	return env, nil
}

// variablesTypeName is the name the API server gives the type of variables.
const variablesTypeName = "kubernetes.variables"

// declTypeOf is the type of variables.<name> for an expression that returns t,
// as the API server's convertCelTypeToDeclType in k8s.io/apiserver
// plugin/cel/composition.go makes it: scalars, lists and maps are kept, any
// other type is dyn.
func declTypeOf(t *celgo.Type) *apiservercel.DeclType {
	if t == nil {
		return apiservercel.DynType
	}
	switch t {
	case celgo.AnyType:
		return apiservercel.AnyType
	case celgo.BoolType:
		return apiservercel.BoolType
	case celgo.BytesType:
		return apiservercel.BytesType
	case celgo.DoubleType:
		return apiservercel.DoubleType
	case celgo.DurationType:
		return apiservercel.DurationType
	case celgo.IntType:
		return apiservercel.IntType
	case celgo.NullType:
		return apiservercel.NullType
	case celgo.StringType:
		return apiservercel.StringType
	case celgo.TimestampType:
		return apiservercel.TimestampType
	case celgo.UintType:
		return apiservercel.UintType
	}
	if t.HasTrait(traits.ContainerType) && t.HasTrait(traits.IndexerType) {
		switch p := t.Parameters(); len(p) {
		case 1:
			return apiservercel.NewListType(declTypeOf(p[0]), -1)
		case 2:
			return apiservercel.NewMapType(declTypeOf(p[0]), declTypeOf(p[1]), -1)
		}
	}
	return apiservercel.DynType
}
