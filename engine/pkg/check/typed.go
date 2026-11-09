package check

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	celgo "github.com/google/cel-go/cel"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/util/version"
	plugincel "k8s.io/apiserver/pkg/admission/plugin/cel"
	apiservercel "k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/common"
	"k8s.io/apiserver/pkg/cel/environment"
	"k8s.io/apiserver/pkg/cel/library"
	"k8s.io/apiserver/pkg/cel/openapi"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// typeCheck does what the API server does for status.typeChecking: for each
// kind the policy matches, it compiles the validations of p again with object
// and oldObject typed from the schema of the kind, and reports every failure of
// that compile as a warning that names the matched kind, one line per kind, even
// when the problem depends on no variable. params is typed from paramKind, but
// the line still names the matched kind, never the params kind. Nothing is
// checked when a matched kind cannot be known (see matchkind.Resolve). Only
// expressions in clean, those that compile without the types, are checked, so no
// problem is shown twice.
func (r *reporter) typeCheck(ctx context.Context, p *admissionregistrationv1.ValidatingAdmissionPolicy, exprs map[string]extract.Expression, clean map[string]bool) error {
	if p.Spec.MatchConstraints == nil || len(clean) == 0 {
		return nil
	}
	rules := make([]matchkind.Rule, len(p.Spec.MatchConstraints.ResourceRules))
	for i, rule := range p.Spec.MatchConstraints.ResourceRules {
		rules[i] = matchkind.Rule{Groups: rule.APIGroups, Versions: rule.APIVersions, Resources: rule.Resources}
	}
	kinds, ok := r.crds.Resolve(rules)
	if !ok {
		return nil
	}
	for _, kind := range kinds {
		if err := r.typeCheckKind(ctx, p, kind, exprs, clean); err != nil {
			return err
		}
	}
	return nil
}

// compileValidations stores the variables of p in composite, compiles each
// expression of the validations that is in clean, and calls fail with the
// issues of each one that does not compile.
func (r *reporter) compileValidations(ctx context.Context, composite *plugincel.CompositedCompiler, decls plugincel.OptionalVariableDeclarations, p *admissionregistrationv1.ValidatingAdmissionPolicy, exprs map[string]extract.Expression, clean map[string]bool, fail func(e extract.Expression, issues []string)) error {
	for _, v := range p.Spec.Variables {
		composite.CompileAndStoreVariable(accessor{name: v.Name, expression: v.Expression, returns: anyTypes}, decls, environment.NewExpressions)
	}
	return eachValidation(ctx, p, exprs, clean, func(text string) *apiservercel.Error {
		return composite.CompileCELExpression(accessor{expression: text}, decls, environment.NewExpressions).Error
	}, fail)
}

// eachValidation compiles each expression of the validations of p that is in
// clean with compile, and calls fail with the issues of each one that does not
// compile.
func eachValidation(ctx context.Context, p *admissionregistrationv1.ValidatingAdmissionPolicy, exprs map[string]extract.Expression, clean map[string]bool, compile func(text string) *apiservercel.Error, fail func(e extract.Expression, issues []string)) error {
	for i := range p.Spec.Validations {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, name := range []string{"expression", "messageExpression"} {
			field := fmt.Sprintf("spec.validations[%d].%s", i, name)
			e, ok := exprs[field]
			if !ok || !clean[field] {
				continue
			}
			cerr := compile(e.Text)
			if cerr == nil || cerr.Type != apiservercel.ErrorTypeInvalid {
				continue
			}
			fail(e, issuesOf(cerr.Detail))
		}
	}
	return nil
}

// issuesOf splits detail, the issues of a compile as cel-go writes them, into
// one issue each, or gives detail whole when it has none of their form.
func issuesOf(detail string) []string {
	if issues := issuePattern.FindAllString(detail, -1); len(issues) > 0 {
		return issues
	}
	return []string{detail}
}

// typeCheckKind is typeCheck for one kind.
func (r *reporter) typeCheckKind(ctx context.Context, p *admissionregistrationv1.ValidatingAdmissionPolicy, kind matchkind.Kind, exprs map[string]extract.Expression, clean map[string]bool) error {
	composite, decls, err := r.typedComposite(p, kind)
	if err != nil || composite == nil {
		return err
	}
	return r.compileValidations(ctx, composite, decls, p, exprs, clean, func(e extract.Expression, issues []string) {
		r.reportAs(e, SeverityWarning, kind.String()+": ", strings.Join(issues, "\n"))
	})
}

// anyTypes are the types a variable's expression may return: any.
var anyTypes = []*celgo.Type{celgo.AnyType, celgo.DynType}

// typedComposite returns the composited compiler of the type checker for kind,
// with object and oldObject typed from it, and the declarations to compile
// with. It returns nil when the kind has no type.
func (r *reporter) typedComposite(p *admissionregistrationv1.ValidatingAdmissionPolicy, kind matchkind.Kind) (*plugincel.CompositedCompiler, plugincel.OptionalVariableDeclarations, error) {
	var decls plugincel.OptionalVariableDeclarations
	declType := common.SchemaDeclType(&openapi.Schema{Schema: kind.Schema}, true)
	if declType == nil {
		return nil, decls, nil
	}
	declType = declType.MaybeAssignTypeName(kind.GVK.Kind)
	hasParams := p.Spec.ParamKind != nil
	envSet, err := typedEnvSet(r.version.EnvSet(), declType, declType, r.paramsType(p.Spec.ParamKind), hasParams)
	if err != nil {
		return nil, decls, fmt.Errorf("build CEL environment for %s: %w", kind, err)
	}
	composite, err := plugincel.NewCompositedCompilerForTypeChecking(envSet)
	if err != nil {
		return nil, decls, fmt.Errorf("build CEL environment for %s: %w", kind, err)
	}
	composite.Compiler = typedCompiler{composite}
	return composite, plugincel.OptionalVariableDeclarations{HasParams: hasParams, HasAuthorizer: true}, nil
}

// paramsType returns the type of params for the policy's paramKind: the type of
// the built-in kind, or of the custom resource a CRD of the check defines, as
// the API server's type checker resolves it. It is nil, and params stays dyn,
// when there is no paramKind or its kind is not found.
func (r *reporter) paramsType(pk *admissionregistrationv1.ParamKind) *apiservercel.DeclType {
	if pk == nil {
		return nil
	}
	var s *spec.Schema
	if kind, ok := matchkind.Lookup(pk.APIVersion, pk.Kind); ok {
		s = kind.Schema
	} else if raw, ok := r.crds.Schema(pk.APIVersion, pk.Kind); ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		s = new(spec.Schema)
		if json.Unmarshal(data, s) != nil {
			return nil
		}
	} else {
		return nil
	}
	t := common.SchemaDeclType(&openapi.Schema{Schema: s}, true)
	if t == nil {
		return nil
	}
	return t.MaybeAssignTypeName(pk.Kind + "Params")
}

// typedEnvSet extends base with the variables of the admission CEL environment
// as the API server's type checker declares them: object of objectType and
// oldObject of oldObjectType (each dyn when nil; the API server gives both
// the matched kind's type), params of paramsType (dyn when that is nil), and
// request, namespaceObject and authorizer.
func typedEnvSet(base *environment.EnvSet, objectType, oldObjectType, paramsType *apiservercel.DeclType, hasParams bool) (*environment.EnvSet, error) {
	request, namespace := plugincel.BuildRequestType(), plugincel.BuildNamespaceType()
	opts := []celgo.EnvOption{
		celgo.Variable(plugincel.NamespaceVarName, namespace.CelType()),
		celgo.Variable(plugincel.RequestVarName, request.CelType()),
		celgo.Variable("authorizer", library.AuthorizerType),
	}
	declTypes := []*apiservercel.DeclType{namespace, request}
	for _, v := range []struct {
		name string
		typ  *apiservercel.DeclType
	}{{plugincel.ObjectVarName, objectType}, {plugincel.OldObjectVarName, oldObjectType}} {
		if v.typ == nil {
			opts = append(opts, celgo.Variable(v.name, celgo.DynType))
			continue
		}
		opts = append(opts, celgo.Variable(v.name, v.typ.CelType()))
		if !slices.Contains(declTypes, v.typ) {
			declTypes = append(declTypes, v.typ)
		}
	}
	switch {
	case paramsType != nil:
		opts = append(opts, celgo.Variable(plugincel.ParamsVarName, paramsType.CelType()))
		declTypes = append(declTypes, paramsType)
	case hasParams:
		opts = append(opts, celgo.Variable(plugincel.ParamsVarName, celgo.DynType))
	}
	return base.Extend(environment.VersionedOptions{
		// Always present, whatever the version, like the API server's own.
		IntroducedVersion: version.MajorMinor(1, 0),
		EnvOptions:        opts,
		DeclTypes:         declTypes,
	})
}

// typedCompiler compiles expressions in the typed environment of a composited
// compiler, as the API server's type checker does: it does not check the type
// the expression returns, and builds no program.
type typedCompiler struct{ composite *plugincel.CompositedCompiler }

// CompileCELExpression compiles the expression of a.
func (c typedCompiler) CompileCELExpression(a plugincel.ExpressionAccessor, _ plugincel.OptionalVariableDeclarations, mode environment.Type) plugincel.CompilationResult {
	fail := func(typ apiservercel.ErrorType, detail string) plugincel.CompilationResult {
		return plugincel.CompilationResult{Error: &apiservercel.Error{Type: typ, Detail: detail}, ExpressionAccessor: a}
	}
	env, err := c.composite.Env(mode)
	if err != nil {
		return fail(apiservercel.ErrorTypeInternal, fmt.Sprintf("build environment: %v", err))
	}
	ast, issues := env.Compile(a.GetExpression())
	if issues != nil {
		return fail(apiservercel.ErrorTypeInvalid, issues.String())
	}
	return plugincel.CompilationResult{OutputType: ast.OutputType(), ExpressionAccessor: a}
}
