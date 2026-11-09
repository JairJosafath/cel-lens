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

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
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
// with the types of pl, and reports what fails as typeCheck does. A variable
// in clean that reads a value pl types, and whose expression fails with the
// types where it compiles with the API server's own, is reported the same
// way: it is dyn then, and nothing else would say why.
func (r *reporter) typeCheckAssumed(ctx context.Context, p *admissionregistrationv1.ValidatingAdmissionPolicy, pl policyPlaced, exprs map[string]extract.Expression, clean map[string]bool) error {
	passes, err := r.assumedPasses(p.Spec.ParamKind, p.Spec.Variables, pl)
	if err != nil {
		return err
	}
	for _, ps := range passes {
		var own []*celgo.Type // the API server's types of the variables for ps.kind, made when needed
		for i, detail := range ps.failures {
			field := fmt.Sprintf("spec.variables[%d].expression", i)
			e, ok := exprs[field]
			if !ok || !clean[field] || detail == "" || len(pl.reads[i].types) == 0 {
				continue
			}
			if ps.kind != nil {
				if own == nil {
					if own, err = r.typedVariables(p, *ps.kind); err != nil {
						return err
					}
				}
				if own[i] == nil {
					continue // it fails with the kind's own type: the API server's to report, which it does not
				}
			}
			r.reportAs(e, SeverityWarning, ps.prefix, strings.Join(issuesOf(detail), "\n"))
		}
		err = eachValidation(ctx, p, exprs, clean, func(text string) *apiservercel.Error {
			if _, issues := ps.env.Compile(text); issues.Err() != nil {
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

// assumedPass is one compile of the expressions of a policy with the types
// assumed for it: one for each kind the policy matches, or one when the kinds
// are not known.
type assumedPass struct {
	// prefix names the kind in a message, as typeCheck does:
	// "apps/v1 Deployment: "; empty when the kinds are not known. kind is
	// that kind, nil then.
	prefix string
	kind   *matchkind.Kind
	// object and oldObject are their types, nil for dyn.
	object, oldObject *apiservercel.DeclType
	// env compiles an expression that may read the variables.
	env *celgo.Env
	// outputs are the types the expressions of the variables return, in the
	// order of spec.variables: nil for one that does not compile, or whose
	// type an assumed type gives; failures are the issues of each that does
	// not compile, as cel-go writes them, "" for the others.
	outputs  []*celgo.Type
	failures []string
}

// assumedPasses returns the passes that compile the expressions of a policy
// whose paramKind is pk and whose variables are variables with the types of
// pl, as typeCheck compiles them with the kinds' types.
func (r *reporter) assumedPasses(pk *admissionregistrationv1.ParamKind, variables []admissionregistrationv1.Variable, pl policyPlaced) ([]assumedPass, error) {
	hasParams := pk != nil
	paramsType := r.paramsType(pk)
	if pl.params != nil {
		name := "cellens.params"
		if hasParams {
			name = pk.Kind + "Params"
		}
		paramsType = declOf(pl.params, true, name)
	}
	assumed := map[string]*apiservercel.DeclType{}
	for name, s := range pl.variables {
		if t := declOf(s, false, "cellens.variables."+name); t != nil {
			assumed[name] = t
		}
	}
	var passes []assumedPass
	if pl.kinds == nil {
		passes = append(passes, assumedPass{
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
		passes = append(passes, assumedPass{prefix: kind.String() + ": ", kind: &pl.kinds[i], object: object, oldObject: oldObject})
	}
	for i, ps := range passes {
		env, outputs, failures, err := r.assumedEnv(ps.object, ps.oldObject, paramsType, hasParams, variables, assumed)
		if err != nil {
			return nil, err
		}
		passes[i].env, passes[i].outputs, passes[i].failures = env, outputs, failures
	}
	return passes, nil
}

// typedVariables returns the types the expressions of the variables of p
// return as the API server's type checker composes them for kind, in order:
// nil for one that does not compile, and for each when the kind has no type.
func (r *reporter) typedVariables(p *admissionregistrationv1.ValidatingAdmissionPolicy, kind matchkind.Kind) ([]*celgo.Type, error) {
	composite, decls, err := r.typedComposite(p, kind)
	if err != nil {
		return nil, err
	}
	if composite == nil {
		return make([]*celgo.Type, len(p.Spec.Variables)), nil
	}
	return storeVariables(composite, decls, p.Spec.Variables), nil
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
// assumed gives it a type. It also returns the type each expression of
// variables returns, nil for one that does not compile or that assumed types,
// and the issues of each that does not compile, "" for the others.
func (r *reporter) assumedEnv(object, oldObject, params *apiservercel.DeclType, hasParams bool, variables []admissionregistrationv1.Variable, assumed map[string]*apiservercel.DeclType) (*celgo.Env, []*celgo.Type, []string, error) {
	base, err := typedEnvSet(r.version.EnvSet(), object, oldObject, params, hasParams)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build CEL environment: %w", err)
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
		return nil, nil, nil, fmt.Errorf("build CEL environment: %w", err)
	}
	env, err := set.Env(environment.NewExpressions)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build CEL environment: %w", err)
	}
	// The environment finds the fields of variables when it compiles, so each
	// variable sees those before it, as in the API server's composition.
	outputs, failures := make([]*celgo.Type, len(variables)), make([]string, len(variables))
	for i, v := range variables {
		t, ok := assumed[v.Name]
		if !ok {
			if ast, issues := env.Compile(v.Expression); issues.Err() == nil {
				outputs[i] = ast.OutputType()
			} else {
				failures[i] = issues.String()
			}
			t = declTypeOf(outputs[i])
		}
		vars.Fields[v.Name] = apiservercel.NewDeclField(v.Name, t, true, nil, nil)
	}
	return env, outputs, failures, nil
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
