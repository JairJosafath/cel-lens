// Package kyverno compiles the CEL of a Kyverno ValidatingPolicy in Kyverno's
// own environment: the variables and function libraries of Kyverno's
// validating policy compiler, at the release the README names, and the same
// compile functions, so a problem reads as Kyverno reports it.
package kyverno

import (
	"context"
	"fmt"
	"reflect"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/ext"
	policieskyvernoio "github.com/kyverno/api/api/policies.kyverno.io"
	kyvernov1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	"github.com/kyverno/kyverno/pkg/cel/compiler"
	"github.com/kyverno/sdk/extensions/cel/libs/globalcontext"
	"github.com/kyverno/sdk/extensions/cel/libs/gzip"
	"github.com/kyverno/sdk/extensions/cel/libs/hash"
	"github.com/kyverno/sdk/extensions/cel/libs/http"
	"github.com/kyverno/sdk/extensions/cel/libs/image"
	"github.com/kyverno/sdk/extensions/cel/libs/imagedata"
	"github.com/kyverno/sdk/extensions/cel/libs/json"
	"github.com/kyverno/sdk/extensions/cel/libs/math"
	"github.com/kyverno/sdk/extensions/cel/libs/random"
	"github.com/kyverno/sdk/extensions/cel/libs/resource"
	"github.com/kyverno/sdk/extensions/cel/libs/time"
	"github.com/kyverno/sdk/extensions/cel/libs/transform"
	"github.com/kyverno/sdk/extensions/cel/libs/user"
	"github.com/kyverno/sdk/extensions/cel/libs/x509"
	"github.com/kyverno/sdk/extensions/cel/libs/yaml"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/util/version"
	apiservercel "k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/common"
	"k8s.io/apiserver/pkg/cel/environment"
	"k8s.io/apiserver/pkg/cel/openapi"

	"github.com/JairJosafath/cel-lens/engine/internal/kyverno/libs"
	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
)

// compilerVersion is the version of Kyverno's validating policy compiler.
var compilerVersion = version.MajorMinor(1, 0)

// Problem is an expression of a policy that Kyverno does not accept.
type Problem struct {
	// Field is the Kubernetes field path of the expression, such as
	// "spec.validations[0].expression".
	Field string
	// Detail is Kyverno's message. For a compile error it holds the lines
	// cel-go prints, "ERROR: <input>:line:column: message".
	Detail string
}

// Compile compiles every CEL expression of spec in Kyverno's environment and
// returns the problems, in the order Kyverno compiles the fields: match
// conditions, variables, validations, audit annotations. An expression that
// fails does not stop the others from being checked; a variable that fails
// has the dynamic type, so the expressions that use it are still checked.
// A policy in a mode other than Kubernetes or JSON is not checked. namespace is
// the namespace of the policy, empty for a cluster-wide one. When kind is not
// nil, object and oldObject have the schema of that kind, as the API server
// types them; otherwise they are dyn. It returns ctx's error when ctx is
// cancelled, and an error if the environment cannot be built.
func Compile(ctx context.Context, spec *kyvernov1.ValidatingPolicySpec, namespace string, kind *matchkind.Kind) ([]Problem, error) {
	mode := spec.EvaluationMode()
	if mode != policieskyvernoio.EvaluationModeKubernetes && mode != policieskyvernoio.EvaluationModeJSON {
		// Another mode, such as Envoy or HTTP, is evaluated by another engine
		// with functions this compiler does not declare.
		return nil, nil
	}
	envSet, variables, err := newEnv(namespace, kind)
	if err != nil {
		return nil, fmt.Errorf("build Kyverno CEL environment: %w", err)
	}
	env, err := envSet.Env(environment.StoredExpressions)
	if err != nil {
		return nil, fmt.Errorf("build Kyverno CEL environment: %w", err)
	}
	var problems []Problem
	add := func(errs field.ErrorList) {
		for _, e := range errs {
			problems = append(problems, Problem{Field: e.Field, Detail: e.Detail})
		}
	}
	path := field.NewPath("spec")
	for i, mc := range spec.MatchConditions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, errs := compiler.CompileMatchCondition(path.Child("matchConditions").Index(i), env, mc)
		add(errs)
	}
	jsonMode := mode == policieskyvernoio.EvaluationModeJSON
	if jsonMode {
		// JSON mode declares the variables only once the match conditions are compiled.
		if env, err = env.Extend(cel.Variable(compiler.VariablesKey, compiler.VariablesType)); err != nil {
			return nil, fmt.Errorf("build Kyverno CEL environment: %w", err)
		}
	}
	for i, v := range spec.Variables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, errs := compiler.CompileVariable(path.Child("variables").Index(i), env, variables, v)
		if len(errs) > 0 {
			variables.RegisterField(v.Name, types.DynType)
		}
		add(errs)
	}
	for i, v := range spec.Validations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Kyverno stops a validation at its first problem; both parts are checked here.
		at := path.Child("validations").Index(i)
		_, errs := compiler.CompileValidation(at, env, admissionregistrationv1.Validation{Expression: v.Expression})
		add(errs)
		if v.MessageExpression != "" {
			_, errs = compiler.CompileValidation(at, env, admissionregistrationv1.Validation{Expression: "true", MessageExpression: v.MessageExpression})
			add(errs)
		}
	}
	if jsonMode {
		return problems, nil
	}
	for i, a := range spec.AuditAnnotations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, errs := compiler.CompileAuditAnnotation(path.Child("auditAnnotations").Index(i), env, a)
		add(errs)
	}
	return problems, nil
}

// newEnv builds the environment set of Kyverno's validating policy compiler and
// the provider of the type of its variables variable, to which the types of the
// policy's variables are added as they compile.
func newEnv(namespace string, kind *matchkind.Kind) (*environment.EnvSet, *compiler.VariablesProvider, error) {
	objectType, declTypes := cel.DynType, []*apiservercel.DeclType{compiler.NamespaceType, compiler.RequestType}
	if kind != nil {
		decl := common.SchemaDeclType(&openapi.Schema{Schema: kind.Schema}, true)
		if decl == nil {
			return nil, nil, fmt.Errorf("no CEL type for %s", kind)
		}
		decl = decl.MaybeAssignTypeName(kind.GVK.Kind)
		objectType, declTypes = decl.CelType(), append(declTypes, decl)
	}
	baseOpts := compiler.DefaultEnvOptionsWithCompat()
	baseOpts = append(baseOpts,
		cel.Variable(compiler.NamespaceObjectKey, compiler.NamespaceType.CelType()),
		cel.Variable(compiler.ObjectKey, objectType),
		cel.Variable(compiler.OldObjectKey, objectType),
		cel.Variable(compiler.RequestKey, compiler.RequestType.CelType()),
		cel.Types(compiler.NamespaceType.CelType()),
		cel.Types(compiler.RequestType.CelType()),
		cel.Variable(compiler.VariablesKey, compiler.VariablesType),
	)
	base := environment.MustBaseEnvSet(compilerVersion)
	env, err := base.Env(environment.StoredExpressions)
	if err != nil {
		return nil, nil, err
	}
	variables := compiler.NewVariablesProvider(env.CELTypeProvider())
	declProvider := apiservercel.NewDeclTypeProvider(declTypes...)
	// The API server lets an expression name a field after a CEL keyword, such
	// as metadata.namespace, since Kubernetes 1.31.
	declProvider.SetRecognizeKeywordAsFieldName(true)
	declOptions, err := declProvider.EnvOptions(variables)
	if err != nil {
		return nil, nil, err
	}
	baseOpts = append(baseOpts, declOptions...)

	var cluster offline
	libOpts := []cel.EnvOption{
		ext.NativeTypes(reflect.TypeFor[libs.Exception](), ext.ParseStructTags(true)),
		cel.Variable(compiler.ExceptionsKey, types.NewObjectType("libs.Exception")),
		globalcontext.Lib(globalcontext.Context{ContextInterface: cluster}, globalcontext.Latest()),
		resource.Lib(resource.Context{ContextInterface: cluster}, namespace, resource.Latest()),
		image.Lib(image.Latest()),
		imagedata.Lib(imagedata.Context{ContextInterface: cluster}, imagedata.Latest(), nil),
		user.Lib(user.Latest()),
		hash.Lib(hash.Latest()),
		math.Lib(math.Latest()),
		json.Lib(&json.JsonImpl{}, json.Latest()),
		yaml.Lib(&yaml.YamlImpl{}, yaml.Latest()),
		random.Lib(random.Latest()),
		x509.Lib(x509.Latest()),
		time.Lib(time.Latest()),
		transform.Lib(transform.Latest()),
		gzip.Lib(gzip.Latest()),
		http.Lib(http.Context{ContextInterface: cluster}, http.Latest()),
	}
	set, err := base.Extend(
		environment.VersionedOptions{IntroducedVersion: compilerVersion, EnvOptions: baseOpts},
		environment.VersionedOptions{IntroducedVersion: compilerVersion, EnvOptions: libOpts},
	)
	return set, variables, err
}

// Env returns the CEL environment Kyverno compiles the expressions of a
// ValidatingPolicy in, with object and oldObject dynamic. Its Variables and
// Functions tell an editor what a policy can use.
func Env() (*cel.Env, error) {
	set, _, err := newEnv("", nil)
	if err != nil {
		return nil, fmt.Errorf("build Kyverno CEL environment: %w", err)
	}
	env, err := set.Env(environment.StoredExpressions)
	if err != nil {
		return nil, fmt.Errorf("build Kyverno CEL environment: %w", err)
	}
	return env, nil
}
