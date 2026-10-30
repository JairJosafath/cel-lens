package check

import (
	"context"
	"encoding/json"
	"fmt"
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
// and oldObject typed from the schema of the kind, and reports what fails as
// warnings that name the kind. What fails because of params, which does not
// depend on the matched kind, is reported once, naming the kind of paramKind.
// Nothing is checked when a matched kind cannot be
// known (see matchkind.Resolve), so object stays dyn. Only expressions in clean, those
// that compile without the types, are checked, so no problem is shown twice.
func (r *reporter) typeCheck(ctx context.Context, p *admissionregistrationv1.ValidatingAdmissionPolicy, exprs map[string]extract.Expression, clean map[string]bool) error {
	if p.Spec.MatchConstraints == nil || len(clean) == 0 {
		return nil
	}
	rules := make([]matchkind.Rule, len(p.Spec.MatchConstraints.ResourceRules))
	for i, rule := range p.Spec.MatchConstraints.ResourceRules {
		rules[i] = matchkind.Rule{Groups: rule.APIGroups, Versions: rule.APIVersions, Resources: rule.Resources}
	}
	kinds, ok := matchkind.Resolve(rules)
	if !ok {
		return nil
	}
	byParams, err := r.typeCheckParams(ctx, p, exprs, clean)
	if err != nil {
		return err
	}
	for _, kind := range kinds {
		if err := r.typeCheckKind(ctx, p, kind, exprs, clean, byParams); err != nil {
			return err
		}
	}
	return nil
}

// typeCheckParams compiles the expressions in clean with params typed and
// object and oldObject dyn, and reports what fails with the kind of paramKind.
// It returns the issues it reported, by field, for typeCheckKind to skip. It
// reports nothing when params stays dyn.
func (r *reporter) typeCheckParams(ctx context.Context, p *admissionregistrationv1.ValidatingAdmissionPolicy, exprs map[string]extract.Expression, clean map[string]bool) (map[string]map[string]bool, error) {
	pk := p.Spec.ParamKind
	paramsType := r.paramsType(pk)
	if paramsType == nil {
		return nil, nil
	}
	envSet, err := typedEnvSet(r.version.EnvSet(), nil, paramsType, true)
	if err != nil {
		return nil, fmt.Errorf("build CEL environment for %s %s: %w", pk.APIVersion, pk.Kind, err)
	}
	composite, err := plugincel.NewCompositedCompilerForTypeChecking(envSet)
	if err != nil {
		return nil, fmt.Errorf("build CEL environment for %s %s: %w", pk.APIVersion, pk.Kind, err)
	}
	composite.Compiler = typedCompiler{composite}
	decls := plugincel.OptionalVariableDeclarations{HasParams: true, HasAuthorizer: true}
	reported := map[string]map[string]bool{}
	err = r.compileValidations(ctx, composite, decls, p, exprs, clean, func(field string, e extract.Expression, issues []string) {
		set := map[string]bool{}
		for _, issue := range issues {
			set[issue] = true
		}
		reported[field] = set
		r.reportAs(e, SeverityWarning, pk.APIVersion+" "+pk.Kind+": ", strings.Join(issues, "\n"))
	})
	return reported, err
}

// compileValidations stores the variables of p in composite, compiles each
// expression of the validations that is in clean, and calls fail with the
// issues of each one that does not compile.
func (r *reporter) compileValidations(ctx context.Context, composite *plugincel.CompositedCompiler, decls plugincel.OptionalVariableDeclarations, p *admissionregistrationv1.ValidatingAdmissionPolicy, exprs map[string]extract.Expression, clean map[string]bool, fail func(field string, e extract.Expression, issues []string)) error {
	for _, v := range p.Spec.Variables {
		composite.CompileAndStoreVariable(accessor{name: v.Name, expression: v.Expression, returns: anyTypes}, decls, environment.NewExpressions)
	}
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
			res := composite.CompileCELExpression(accessor{expression: e.Text}, decls, environment.NewExpressions)
			if res.Error == nil || res.Error.Type != apiservercel.ErrorTypeInvalid {
				continue
			}
			issues := issuePattern.FindAllString(res.Error.Detail, -1)
			if len(issues) == 0 {
				issues = []string{res.Error.Detail}
			}
			fail(field, e, issues)
		}
	}
	return nil
}

// typeCheckKind is typeCheck for one kind. It skips the issues in byParams, by
// field, which typeCheckParams reported.
func (r *reporter) typeCheckKind(ctx context.Context, p *admissionregistrationv1.ValidatingAdmissionPolicy, kind matchkind.Kind, exprs map[string]extract.Expression, clean map[string]bool, byParams map[string]map[string]bool) error {
	composite, decls, err := r.typedComposite(p, kind)
	if err != nil || composite == nil {
		return err
	}
	return r.compileValidations(ctx, composite, decls, p, exprs, clean, func(field string, e extract.Expression, issues []string) {
		var kept []string
		for _, issue := range issues {
			if !byParams[field][issue] {
				kept = append(kept, issue)
			}
		}
		if len(kept) > 0 {
			r.reportAs(e, SeverityWarning, kind.String()+": ", strings.Join(kept, "\n"))
		}
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
	envSet, err := typedEnvSet(r.version.EnvSet(), declType, r.paramsType(p.Spec.ParamKind), hasParams)
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
// as the API server's type checker declares them: object and oldObject of
// objectType (dyn when that is nil), params of paramsType (dyn when that is nil), and request,
// namespaceObject and authorizer.
func typedEnvSet(base *environment.EnvSet, objectType, paramsType *apiservercel.DeclType, hasParams bool) (*environment.EnvSet, error) {
	request, namespace := plugincel.BuildRequestType(), plugincel.BuildNamespaceType()
	opts := []celgo.EnvOption{
		celgo.Variable(plugincel.NamespaceVarName, namespace.CelType()),
		celgo.Variable(plugincel.RequestVarName, request.CelType()),
		celgo.Variable("authorizer", library.AuthorizerType),
	}
	declTypes := []*apiservercel.DeclType{namespace, request}
	if objectType != nil {
		opts = append(opts,
			celgo.Variable(plugincel.ObjectVarName, objectType.CelType()),
			celgo.Variable(plugincel.OldObjectVarName, objectType.CelType()))
		declTypes = append(declTypes, objectType)
	} else {
		opts = append(opts,
			celgo.Variable(plugincel.ObjectVarName, celgo.DynType),
			celgo.Variable(plugincel.OldObjectVarName, celgo.DynType))
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
