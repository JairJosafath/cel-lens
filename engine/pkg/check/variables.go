package check

import (
	"context"
	"fmt"

	celgo "github.com/google/cel-go/cel"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	plugincel "k8s.io/apiserver/pkg/admission/plugin/cel"
	"k8s.io/apiserver/pkg/cel/environment"
)

// Variable is one entry of a policy's spec.variables with the type the API
// server gives variables.<Name>.
type Variable struct {
	// Name is the name of the variable.
	Name string
	// Expression is the CEL expression that defines it.
	Expression string
	// Type is the CEL type of variables.<Name>, such as "int" or "list(string)".
	Type string
	// Schema describes Type as an OpenAPI schema, in the form YAML decodes a CRD's
	// schema to, for the fields and elements a reader can reach.
	Schema map[string]any
	// Dyn is why Type is dyn, one of the Dyn constants, and "" when it is not.
	Dyn string
	// Assumed are the types of WithAssumedTypes that Type and Dyn follow
	// from, when with them the expression returns another type than the API
	// server gives the variable: the types of the values the expression reads,
	// params, object, oldObject or variables.<name>, and those an earlier
	// variable it reads follows from. It is empty when Type is the API
	// server's own.
	Assumed []AssumedType
}

// The reasons a Variable is dyn.
const (
	// DynResult: the API server makes the type of the result dyn, as for an object.
	DynResult = "result-dyn"
	// DynUncompiled: the expression does not compile, so it has no type.
	DynUncompiled = "uncompiled"
	// DynKindsDiffer: the kinds the policy matches give the expression different types.
	DynKindsDiffer = "kinds-differ"
	// DynInput: the expression is already dyn, because it reads a value of unknown type.
	DynInput = "input-dyn"
	// DynAssumedUncompiled: the expression compiles without the types of
	// WithAssumedTypes, but not with them; Assumed lists those it reads.
	DynAssumedUncompiled = "assumed-uncompiled"
)

// VariableTypes returns the variables of the ValidatingAdmissionPolicy or
// MutatingAdmissionPolicy in root, a decoded YAML document, in the order of
// spec.variables, typed as the API server types them: the variable has the
// type its expression returns when that is a scalar, a list or a map, and is
// dyn otherwise, as for an object, or when the expression does not compile.
// The expressions are compiled with object and oldObject typed from the
// kinds the policy matches, when they are all known. If the kinds differ in
// the type of a variable, it is dyn. It returns nil when root has no
// variables, and an error when spec cannot be read. Options WithVersion and
// WithCRDs apply. A cancelled ctx returns its error.
//
// With WithAssumedTypes, the variables are typed as the API server would type
// them if Kubernetes had the types those of the policy give params, object,
// oldObject and variables.<name> where it leaves them dyn (see
// AssumedTypeProblems): in order, each with the type its expression returns,
// so a later variable sees the types of earlier ones. A variable whose type
// they change lists in Assumed those it follows from. A variable a type names
// keeps the type the API server gives it, which that type replaces where it
// applies, and later variables see it with that type. When no CEL environment
// can be built with the types, the variables keep the API server's types, as
// Check then reports only the API server's problems.
func VariableTypes(ctx context.Context, root map[string]any, opts ...Option) ([]Variable, error) {
	var set settings
	for _, o := range opts {
		o(&set)
	}
	// Only what the types depend on is read, so a wrong value elsewhere in the
	// policy does not hide them.
	in, err := readPolicyVars(root)
	if err != nil {
		return nil, fmt.Errorf("read spec.variables: %w", err)
	}
	r := reporter{version: set.version, crds: set.typingCRDs(), assumed: set.assumed}
	vars, err := r.variableTypes(ctx, in)
	if err != nil || len(vars) == 0 {
		return vars, err
	}
	return r.assumedVariables(ctx, root, in, vars)
}

// variableTypes is VariableTypes for the policy in.
func (r *reporter) variableTypes(ctx context.Context, in *policyVars) ([]Variable, error) {
	var p admissionregistrationv1.ValidatingAdmissionPolicy
	p.Spec.ParamKind, p.Spec.MatchConstraints, p.Spec.Variables = in.Spec.ParamKind, in.Spec.MatchConstraints, in.Spec.Variables
	if len(p.Spec.Variables) == 0 {
		return nil, nil
	}
	var perKind [][]*celgo.Type
	if p.Spec.MatchConstraints != nil {
		if kinds, ok := r.crds.Resolve(in.rules()); ok {
			for _, kind := range kinds {
				composite, decls, err := r.typedComposite(&p, kind)
				if err != nil {
					return nil, err
				}
				if composite == nil {
					perKind = nil
					break
				}
				perKind = append(perKind, storeVariables(composite, decls, p.Spec.Variables))
			}
		}
	}
	if perKind == nil {
		composite, err := plugincel.NewCompositedCompiler(r.version.EnvSet())
		if err != nil {
			return nil, fmt.Errorf("build CEL environment: %w", err)
		}
		decls := plugincel.OptionalVariableDeclarations{HasParams: p.Spec.ParamKind != nil, HasAuthorizer: true}
		perKind = [][]*celgo.Type{storeVariables(composite, decls, p.Spec.Variables)}
	}
	out := make([]Variable, len(p.Spec.Variables))
	for i, v := range p.Spec.Variables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, why := variableType(perKind, i)
		out[i] = Variable{Name: v.Name, Expression: v.Expression, Type: t.String(), Schema: typeSchema(t), Dyn: why}
	}
	return out, nil
}

// variableType is the type of the i-th variable, whose expression returns
// perKind[k][i] for the k-th kind the policy matches (nil when it does not
// compile), and why it is dyn, one of the Dyn constants, or "".
func variableType(perKind [][]*celgo.Type, i int) (*celgo.Type, string) {
	t := varType(perKind[0][i])
	why := ""
	switch {
	case perKind[0][i] == nil:
		why = DynUncompiled
	case perKind[0][i].Kind() == celgo.DynKind:
		why = DynInput
	case t.IsExactType(celgo.DynType):
		why = DynResult
	}
	for _, other := range perKind[1:] {
		if !t.IsExactType(varType(other[i])) {
			t, why = celgo.DynType, DynKindsDiffer
		}
	}
	return t, why
}

// assumedVariables types vars, the variables of the policy in root, whose
// part in reads, as the API server types them, again with the types assumed
// for the policy (see VariableTypes). The error is for a cancelled ctx.
func (r *reporter) assumedVariables(ctx context.Context, root map[string]any, in *policyVars, vars []Variable) ([]Variable, error) {
	kind, _ := root["kind"].(string)
	meta, _ := root["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	types := r.assumedNamed(kind, name)
	if len(types) == 0 {
		return vars, nil
	}
	pl, _, err := r.placeInPolicy(ctx, root, in, types, vars)
	if err != nil || len(pl.types) == 0 {
		return vars, err
	}
	passes, err := r.assumedPasses(in.Spec.ParamKind, in.Spec.Variables, pl)
	if err != nil || len(passes) == 0 {
		return vars, ctx.Err() // the API server's types stand without the assumed ones
	}
	perPass := make([][]*celgo.Type, len(passes))
	for k, ps := range passes {
		perPass[k] = ps.outputs
	}
	for i, v := range vars {
		if _, named := pl.variables[v.Name]; named || len(pl.reads[i].types) == 0 {
			continue // a type that names it gives its type, or it reads none
		}
		t, why := variableType(perPass, i)
		if why == DynUncompiled && v.Dyn != DynUncompiled {
			why = DynAssumedUncompiled // which Assumed then names
		}
		if t.String() != v.Type || why != v.Dyn {
			vars[i].Type, vars[i].Schema, vars[i].Dyn, vars[i].Assumed = t.String(), typeSchema(t), why, pl.reads[i].types
		}
	}
	return vars, ctx.Err()
}

// storeVariables compiles variables in order into composite, as the API server
// does, and returns the type each expression returns, nil for one that does
// not compile.
func storeVariables(composite *plugincel.CompositedCompiler, decls plugincel.OptionalVariableDeclarations, variables []admissionregistrationv1.Variable) []*celgo.Type {
	types := make([]*celgo.Type, len(variables))
	for i, v := range variables {
		res := composite.CompileAndStoreVariable(accessor{name: v.Name, expression: v.Expression, returns: anyTypes}, decls, environment.NewExpressions)
		types[i] = res.OutputType
	}
	return types
}

// varType is the type variables.<name> has for an expression that returns t:
// the API server's convertCelTypeToDeclType in k8s.io/apiserver
// plugin/cel/composition.go keeps the scalars, lists and maps, converting
// their parameters the same way, and makes every other type dyn.
func varType(t *celgo.Type) *celgo.Type {
	if t == nil {
		return celgo.DynType
	}
	switch t.String() {
	case "bool", "bytes", "double", "google.protobuf.Duration", "int", "null_type", "string", "google.protobuf.Timestamp", "uint":
		return t
	}
	if p := t.Parameters(); t.Kind() == celgo.ListKind && len(p) == 1 {
		return celgo.ListType(varType(p[0]))
	} else if t.Kind() == celgo.MapKind && len(p) == 2 {
		return celgo.MapType(varType(p[0]), varType(p[1]))
	}
	return celgo.DynType
}

// typeSchema describes t, a type varType returns, as an OpenAPI schema.
func typeSchema(t *celgo.Type) map[string]any {
	switch t.String() {
	case "bool":
		return map[string]any{"type": "boolean"}
	case "int", "uint":
		return map[string]any{"type": "integer"}
	case "double":
		return map[string]any{"type": "number"}
	case "string":
		return map[string]any{"type": "string"}
	case "bytes":
		return map[string]any{"type": "string", "format": "byte"}
	case "google.protobuf.Timestamp":
		return map[string]any{"type": "string", "format": "date-time"}
	case "google.protobuf.Duration":
		return map[string]any{"type": "string", "format": "duration"}
	}
	if p := t.Parameters(); t.Kind() == celgo.ListKind && len(p) == 1 {
		return map[string]any{"type": "array", "items": typeSchema(p[0])}
	} else if t.Kind() == celgo.MapKind && len(p) == 2 {
		return map[string]any{"type": "object", "additionalProperties": typeSchema(p[1])}
	}
	return map[string]any{"x-kubernetes-preserve-unknown-fields": true}
}
