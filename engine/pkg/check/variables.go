package check

import (
	"context"
	"encoding/json"
	"fmt"

	celgo "github.com/google/cel-go/cel"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	plugincel "k8s.io/apiserver/pkg/admission/plugin/cel"
	"k8s.io/apiserver/pkg/cel/environment"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
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
func VariableTypes(ctx context.Context, root map[string]any, opts ...Option) ([]Variable, error) {
	var set settings
	for _, o := range opts {
		o(&set)
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("read spec.variables: %w", err)
	}
	// Only what the types depend on is read, so a wrong value elsewhere in the
	// policy does not hide them.
	var in struct {
		Spec struct {
			ParamKind        *admissionregistrationv1.ParamKind      `json:"paramKind"`
			MatchConstraints *admissionregistrationv1.MatchResources `json:"matchConstraints"`
			Variables        []admissionregistrationv1.Variable      `json:"variables"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("read spec.variables: %w", err)
	}
	var p admissionregistrationv1.ValidatingAdmissionPolicy
	p.Spec.ParamKind, p.Spec.MatchConstraints, p.Spec.Variables = in.Spec.ParamKind, in.Spec.MatchConstraints, in.Spec.Variables
	if len(p.Spec.Variables) == 0 {
		return nil, nil
	}
	r := reporter{version: set.version, crds: set.typingCRDs()}
	var perKind [][]*celgo.Type
	if p.Spec.MatchConstraints != nil {
		rules := make([]matchkind.Rule, len(p.Spec.MatchConstraints.ResourceRules))
		for i, rule := range p.Spec.MatchConstraints.ResourceRules {
			rules[i] = matchkind.Rule{Groups: rule.APIGroups, Versions: rule.APIVersions, Resources: rule.Resources}
		}
		if kinds, ok := matchkind.Resolve(rules); ok {
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
		out[i] = Variable{Name: v.Name, Expression: v.Expression, Type: t.String(), Schema: typeSchema(t), Dyn: why}
	}
	return out, nil
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
