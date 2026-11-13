package examples

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	plugincel "k8s.io/apiserver/pkg/admission/plugin/cel"
	"k8s.io/apiserver/pkg/admission/plugin/policy/generic"
	"k8s.io/apiserver/pkg/admission/plugin/policy/matching"
	"k8s.io/apiserver/pkg/admission/plugin/policy/validating"
	"k8s.io/apiserver/pkg/admission/plugin/webhook/matchconditions"
	auditinternal "k8s.io/apiserver/pkg/apis/audit"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/cel/environment"
	"k8s.io/apiserver/pkg/warning"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/scheme"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// The API server's admission of an example by a ValidatingAdmissionPolicy,
// driven without an API server: the policy is compiled as the API server's
// ValidatingAdmissionPolicy plugin compiles it (compilePolicy of
// k8s.io/apiserver/pkg/admission/plugin/policy/validating), for the
// Kubernetes version picked, into the plugin's own evaluator
// (validating.NewValidator), and the plugin's own dispatcher
// (validating.NewDispatcher) admits each request, with the plugin's own
// matcher (generic.NewPolicyMatcher over matching.NewMatcher). What the
// API server would read from its informers, the namespaces and the params,
// are listers over the documents of the workspace (cache.NewIndexer); no
// informer runs and nothing is read from a cluster.

// compiled is a policy compiled as the API server compiles it: its
// evaluator, and an evaluator of each of its match conditions alone, which
// says what that condition does (the API server's evaluator says only
// whether all of them match).
type compiled struct {
	validator  validating.Validator
	matcher    matchconditions.Matcher
	conditions []matchconditions.Matcher
}

// compilePolicy compiles spec as compilePolicy of the API server's
// ValidatingAdmissionPolicy plugin does, but in the environment of the
// Kubernetes version kube rather than the API server's own.
func compilePolicy(spec *admissionregistrationv1.ValidatingAdmissionPolicy, kube kubeversion.Version) compiled {
	hasParams := spec.Spec.ParamKind != nil
	vars := plugincel.OptionalVariableDeclarations{HasParams: hasParams, HasAuthorizer: true}
	messageVars := plugincel.OptionalVariableDeclarations{HasParams: hasParams, HasAuthorizer: false}
	failure := spec.Spec.FailurePolicy
	compiler := func() (*plugincel.CompositedCompiler, error) {
		c, err := plugincel.NewCompositedCompiler(kube.EnvSet())
		if err != nil {
			return nil, err
		}
		variables := make([]plugincel.NamedExpressionAccessor, len(spec.Spec.Variables))
		for i, v := range spec.Spec.Variables {
			variables[i] = &validating.Variable{Name: v.Name, Expression: v.Expression}
		}
		c.CompileAndStoreVariables(variables, vars, environment.StoredExpressions)
		return c, nil
	}
	c, err := compiler()
	if err != nil {
		return compiled{validator: validating.NewValidator(nil, nil, nil, nil, failure, err)}
	}
	var matcher matchconditions.Matcher
	var conditions []matchconditions.Matcher
	if len(spec.Spec.MatchConditions) > 0 {
		all := make([]plugincel.ExpressionAccessor, len(spec.Spec.MatchConditions))
		for i := range spec.Spec.MatchConditions {
			all[i] = (*matchconditions.MatchCondition)(&spec.Spec.MatchConditions[i])
			alone, err := compiler()
			if err != nil {
				return compiled{validator: validating.NewValidator(nil, nil, nil, nil, failure, err)}
			}
			one := alone.CompileCondition([]plugincel.ExpressionAccessor{all[i]}, vars, environment.StoredExpressions)
			// With Fail, a condition alone says its error, which Ignore
			// would hide as a condition that does not match; what the
			// policy does with the error is the evaluator's.
			fail := admissionregistrationv1.Fail
			conditions = append(conditions, matchconditions.NewMatcher(one, &fail, "policy", "validate", spec.Name))
		}
		matcher = matchconditions.NewMatcher(c.CompileCondition(all, vars, environment.StoredExpressions), failure, "policy", "validate", spec.Name)
	}
	validations := make([]plugincel.ExpressionAccessor, len(spec.Spec.Validations))
	messages := make([]plugincel.ExpressionAccessor, len(spec.Spec.Validations))
	for i, v := range spec.Spec.Validations {
		validations[i] = &validating.ValidationCondition{Expression: v.Expression, Message: v.Message, Reason: v.Reason}
		if v.MessageExpression != "" {
			messages[i] = &validating.MessageExpressionCondition{MessageExpression: v.MessageExpression}
		}
	}
	annotations := make([]plugincel.ExpressionAccessor, len(spec.Spec.AuditAnnotations))
	for i, a := range spec.Spec.AuditAnnotations {
		annotations[i] = &validating.AuditAnnotationCondition{Key: a.Key, ValueExpression: a.ValueExpression}
	}
	return compiled{
		validator: validating.NewValidator(
			c.CompileCondition(validations, vars, environment.StoredExpressions),
			matcher,
			c.CompileCondition(annotations, vars, environment.StoredExpressions),
			c.CompileCondition(messages, messageVars, environment.StoredExpressions),
			failure,
			nil,
		),
		matcher:    matcher,
		conditions: conditions,
	}
}

// evaluation is one evaluation of a policy by the dispatcher: under a
// param, nil for none, what the evaluator decided, what its match
// conditions did together, as the evaluator sees them, and each alone.
type evaluation struct {
	param      runtime.Object
	result     validating.ValidateResult
	match      matchconditions.MatchResult
	conditions []matchconditions.MatchResult
}

// evaluator is the evaluator the dispatcher runs: the policy's, recording
// each evaluation. It is used by one dispatch at a time.
type evaluator struct {
	compiled
	evaluations []evaluation
}

// Validate runs the policy's evaluator, and each match condition alone, and
// records what they did.
func (r *evaluator) Validate(ctx context.Context, resource schema.GroupVersionResource, attr *admission.VersionedAttributes, params runtime.Object, namespace *corev1.Namespace, budget int64, authz authorizer.UnconditionalAuthorizer) validating.ValidateResult {
	res := r.validator.Validate(ctx, resource, attr, params, namespace, budget, authz)
	e := evaluation{param: params, result: res, match: matchconditions.MatchResult{Matches: true}}
	if r.matcher != nil {
		e.match = r.matcher.Match(ctx, attr, params, authz)
	}
	for _, m := range r.conditions {
		e.conditions = append(e.conditions, m.Match(ctx, attr, params, authz))
	}
	r.evaluations = append(r.evaluations, e)
	return res
}

// CompileError is the policy's evaluator's.
func (r *evaluator) CompileError() error { return r.validator.CompileError() }

// noAuthorizer is the authorizer of the admission: cel-lens runs no
// authorizer check, so it has no opinion. No expression that reads
// authorizer is evaluated (see runnable).
type noAuthorizer struct{}

// Authorize has no opinion.
func (noAuthorizer) Authorize(context.Context, authorizer.Attributes) (authorizer.Decision, string, error) {
	return authorizer.DecisionNoOpinion, "cel-lens runs no authorizer check", nil
}

// annotated are the attributes of a request, keeping the audit annotations
// the dispatcher adds.
type annotated struct {
	admission.Attributes
	mu          sync.Mutex // guards annotations
	annotations map[string]string
}

// AddAnnotation keeps the annotation.
func (a *annotated) AddAnnotation(key, value string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.annotations == nil {
		a.annotations = map[string]string{}
	}
	a.annotations[key] = value
	return nil
}

// AddAnnotationWithLevel keeps the annotation.
func (a *annotated) AddAnnotationWithLevel(key, value string, _ auditinternal.Level) error {
	return a.AddAnnotation(key, value)
}

// answer is what the API server answers to one request under one
// binding: the error it refuses it with, "" when it allows it, its
// warnings and the audit annotations it adds.
type answer struct {
	denied      string
	warnings    []string
	annotations map[string]string
}

// cluster is what the API server knows of the workspace when it admits an
// example: the resources it serves, the namespaces and the params.
type cluster struct {
	mapper     runtime.EquivalentResourceRegistry
	namespaces listersv1.NamespaceLister
	matcher    generic.PolicyMatcher
	// params are the params of each kind, by its group and kind, and the
	// scope of that kind; a kind with none has an empty lister.
	params map[schema.GroupKind]paramStore
}

// paramStore is the params of one kind, and the document each is.
type paramStore struct {
	informer informers.GenericInformer
	scope    meta.RESTScope
	objects  map[runtime.Object]*Object
}

// paramStore returns the params of the paramKind of spec; false for a
// policy without one, and for a kind the cluster does not serve.
func (cl *cluster) paramStore(spec *admissionregistrationv1.ValidatingAdmissionPolicy) (paramStore, bool) {
	pk := spec.Spec.ParamKind
	if pk == nil {
		return paramStore{}, false
	}
	gv, err := schema.ParseGroupVersion(pk.APIVersion)
	if err != nil {
		return paramStore{}, false
	}
	s, ok := cl.params[schema.GroupKind{Group: gv.Group, Kind: pk.Kind}]
	return s, ok
}

// paramObject is the document the param p is, nil for none.
func (cl *cluster) paramObject(p runtime.Object) *Object {
	if p == nil {
		return nil
	}
	for _, s := range cl.params {
		if o, ok := s.objects[p]; ok {
			return o
		}
	}
	return nil
}

// syncedInformer is the informer of a paramStore: always synced, as its
// lister holds every param of the workspace. Only HasSynced is called (by
// generic.CollectParams).
type syncedInformer struct {
	cache.SharedIndexInformer
}

// HasSynced is true.
func (syncedInformer) HasSynced() bool { return true }

// genericInformer is a param kind's informer as the dispatcher reads it.
type genericInformer struct {
	lister cache.GenericLister
}

func (g genericInformer) Informer() cache.SharedIndexInformer { return syncedInformer{} }

// sortedLister lists params by namespace and name, where the API server's
// informer lists them in no fixed order, so that the results of a
// paramRef.selector come in the same order each time.
type sortedLister struct {
	cache.GenericLister
}

// List lists the params selector selects, sorted.
func (l sortedLister) List(selector labels.Selector) ([]runtime.Object, error) {
	out, err := l.GenericLister.List(selector)
	return sortObjects(out), err
}

// ByNamespace is the params of namespace, listed sorted.
func (l sortedLister) ByNamespace(namespace string) cache.GenericNamespaceLister {
	return sortedNamespaceLister{l.GenericLister.ByNamespace(namespace)}
}

// sortedNamespaceLister lists the params of a namespace sorted.
type sortedNamespaceLister struct {
	cache.GenericNamespaceLister
}

// List lists the params selector selects, sorted.
func (l sortedNamespaceLister) List(selector labels.Selector) ([]runtime.Object, error) {
	out, err := l.GenericNamespaceLister.List(selector)
	return sortObjects(out), err
}

// sortObjects sorts objs by namespace and name.
func sortObjects(objs []runtime.Object) []runtime.Object {
	key := func(o runtime.Object) string {
		k, _ := cache.MetaNamespaceKeyFunc(o)
		return k
	}
	slices.SortFunc(objs, func(a, b runtime.Object) int { return strings.Compare(key(a), key(b)) })
	return objs
}
func (g genericInformer) Lister() cache.GenericLister { return g.lister }

// request is an example as the API server's admission gets it: a create,
// or an update from old.
type request struct {
	example   *Object
	old       *Object
	gvk       schema.GroupVersionKind
	gvr       schema.GroupVersionResource
	namespace string
	// matched is the kind the policy matches the request as.
	matched schema.GroupVersionKind
}

// attributes are the admission attributes of r, by no user in particular.
func (r request) attributes() *annotated {
	obj := &unstructured.Unstructured{Object: runtime.DeepCopyJSON(r.example.Object)}
	var old runtime.Object
	op, options := admission.Create, runtime.Object(&metav1.CreateOptions{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "CreateOptions"}})
	if r.old != nil {
		old = &unstructured.Unstructured{Object: runtime.DeepCopyJSON(r.old.Object)}
		op, options = admission.Update, &metav1.UpdateOptions{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "UpdateOptions"}}
	}
	return &annotated{Attributes: admission.NewAttributesRecord(obj, old, r.gvk, r.namespace, obj.GetName(), r.gvr, "", op, options, false, &user.DefaultInfo{})}
}

// objectInterfaces are what the dispatcher converts an object with: the
// API server's conversion of a custom resource whose
// CustomResourceDefinition sets the conversion strategy None, which sets
// its apiVersion alone. Run admits no other example whose matched version
// is not its own.
type objectInterfaces struct {
	mapper runtime.EquivalentResourceMapper
}

func (o objectInterfaces) GetObjectCreater() runtime.ObjectCreater     { return o }
func (o objectInterfaces) GetObjectTyper() runtime.ObjectTyper         { return nil }
func (o objectInterfaces) GetObjectDefaulter() runtime.ObjectDefaulter { return nil }
func (o objectInterfaces) GetObjectConvertor() runtime.ObjectConvertor { return o }
func (o objectInterfaces) GetEquivalentResourceMapper() runtime.EquivalentResourceMapper {
	return o.mapper
}

// New makes an empty custom resource of gvk.
func (objectInterfaces) New(gvk schema.GroupVersionKind) (runtime.Object, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	return u, nil
}

// Convert copies the custom resource in to out, which has the apiVersion to
// convert to.
func (objectInterfaces) Convert(in, out, _ any) error {
	from, ok := in.(*unstructured.Unstructured)
	to, ok2 := out.(*unstructured.Unstructured)
	if !ok || !ok2 {
		return errors.New("cel-lens converts only custom resources")
	}
	gvk := to.GroupVersionKind()
	to.Object = runtime.DeepCopyJSON(from.Object)
	to.SetGroupVersionKind(gvk)
	return nil
}

// ConvertToVersion is not used by the dispatcher.
func (objectInterfaces) ConvertToVersion(runtime.Object, runtime.GroupVersioner) (runtime.Object, error) {
	return nil, errors.New("cel-lens converts only custom resources")
}

// ConvertFieldLabel is not used by the dispatcher.
func (objectInterfaces) ConvertFieldLabel(schema.GroupVersionKind, string, string) (label, value string, err error) {
	return "", "", errors.New("cel-lens converts no field label")
}

// admit admits r under binding with the policy p, compiled as c, as the
// API server's dispatcher does, and returns its answer and the
// evaluations the dispatcher ran. hasParams says whether the policy has a
// paramKind whose params the cluster has.
func (cl *cluster) admit(ctx context.Context, spec *admissionregistrationv1.ValidatingAdmissionPolicy, c compiled, binding *admissionregistrationv1.ValidatingAdmissionPolicyBinding, r request) (answer, []evaluation) {
	rec := &evaluator{compiled: c}
	hook := validating.PolicyHook{Policy: spec, Bindings: []*admissionregistrationv1.ValidatingAdmissionPolicyBinding{binding}, Evaluator: rec}
	if s, ok := cl.paramStore(spec); ok {
		hook.ParamInformer, hook.ParamScope = s.informer, s.scope
	}
	var w recorder
	attrs := r.attributes()
	err := validating.NewDispatcher(noAuthorizer{}, cl.matcher).Dispatch(warning.WithWarningRecorder(ctx, &w), attrs, objectInterfaces{mapper: cl.mapper}, []validating.PolicyHook{hook})
	a := answer{warnings: w.texts, annotations: attrs.annotations}
	if err != nil {
		a.denied = err.Error()
	}
	return a, rec.evaluations
}

// newCluster makes the cluster of the documents objs, of the CRDs crds and
// of the built-in kinds the Kubernetes version kube serves: each served
// version of a resource is equivalent to the others of its group, as for
// the API server; the namespaces are the Namespace documents, and any other
// namespace an example names, with no label but the one the API server
// gives every namespace (kubernetes.io/metadata.name); params are the
// documents of each param kind of policies.
func newCluster(crds []*CRD, objs []*Object, policies []*Policy, kube kubeversion.Version) *cluster {
	cl := &cluster{mapper: runtime.NewEquivalentResourceRegistry(), params: map[schema.GroupKind]paramStore{}}
	for _, k := range builtInKinds(kube) {
		cl.mapper.RegisterKindFor(k.gvr, "", k.gvk)
	}
	for _, c := range crds {
		for _, name := range c.names {
			if _, ok := c.versions[name]; ok {
				gv := schema.GroupVersion{Group: c.Group, Version: name}
				cl.mapper.RegisterKindFor(gv.WithResource(c.Plural), "", gv.WithKind(c.Kind))
			}
		}
	}
	nsIndex := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	named := map[string]bool{}
	for _, o := range objs {
		if o.CRD != nil || o.APIVersion != "v1" || o.Kind != "Namespace" || o.Object == nil || o.Name == "" || named[o.Name] {
			continue
		}
		named[o.Name] = true
		var ns corev1.Namespace
		if runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, &ns) != nil {
			continue
		}
		withNameLabel(&ns)
		_ = nsIndex.Add(&ns) // an indexer without index functions fails on no object
	}
	for _, o := range objs {
		if n := o.requestNamespace(); n != "" && !named[n] {
			named[n] = true
			ns := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: n}}
			withNameLabel(&ns)
			_ = nsIndex.Add(&ns)
		}
	}
	cl.namespaces = listersv1.NewNamespaceLister(nsIndex)
	cl.matcher = generic.NewPolicyMatcher(matching.NewMatcher(cl.namespaces, nil))
	for _, p := range policies {
		pk := p.spec.Spec.ParamKind
		if pk == nil {
			continue
		}
		gv, err := schema.ParseGroupVersion(pk.APIVersion)
		if err != nil {
			continue
		}
		gk := schema.GroupKind{Group: gv.Group, Kind: pk.Kind}
		if _, done := cl.params[gk]; done {
			continue
		}
		if s, ok := paramsOf(gv.WithKind(pk.Kind), crds, objs); ok {
			cl.params[gk] = s
		}
	}
	return cl
}

// withNameLabel gives ns the label the API server gives every namespace.
func withNameLabel(ns *corev1.Namespace) {
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	ns.Labels[corev1.LabelMetadataName] = ns.Name
}

// paramsOf returns the params of the kind gvk among objs, as the API server
// lists them for a policy whose paramKind it is: the documents of that
// kind, in its version, a document of another version of a custom resource
// whose CustomResourceDefinition converts by its apiVersion alone taken in
// that version; false when gvk is neither built in nor a version a CRD of
// crds serves.
func paramsOf(gvk schema.GroupVersionKind, crds []*CRD, objs []*Object) (paramStore, bool) {
	var scope meta.RESTScope
	var crd *CRD
	if _, namespaced, ok := matchkind.Resource(gvk); ok {
		scope = scopeOf(namespaced)
	} else {
		for _, c := range crds {
			if _, ok := c.serves(gvk.GroupVersion().String(), gvk.Kind); ok {
				crd = c
				break
			}
		}
		if crd == nil {
			return paramStore{}, false
		}
		scope = scopeOf(crd.namespaced)
	}
	index := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	objects := map[runtime.Object]*Object{}
	for _, o := range objs {
		gv, err := schema.ParseGroupVersion(o.APIVersion)
		if err != nil || o.Object == nil || o.DecodeFailed || o.RulesSkipped || o.Kind != gvk.Kind || gv.Group != gvk.Group {
			continue
		}
		if gv.Version != gvk.Version && (crd == nil || o.CRD != crd || !crd.converts) {
			continue
		}
		obj, ok := paramObject(o, gvk, crd == nil)
		if !ok {
			continue
		}
		key, err := cache.MetaNamespaceKeyFunc(obj)
		if _, exists, _ := index.GetByKey(key); err != nil || exists {
			continue // the first document of a name is the param
		}
		if index.Add(obj) == nil {
			objects[obj] = o
		}
	}
	resource := schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}
	return paramStore{informer: genericInformer{lister: sortedLister{cache.NewGenericLister(index, resource)}}, scope: scope, objects: objects}, true
}

// paramObject is o as the param informer holds it: a built-in kind in its
// Go type, as the API server's informer of that kind has it, a custom
// resource in the version gvk; false when o does not read as one.
func paramObject(o *Object, gvk schema.GroupVersionKind, builtIn bool) (runtime.Object, bool) {
	u := &unstructured.Unstructured{Object: runtime.DeepCopyJSON(o.Object)}
	u.SetAPIVersion(gvk.GroupVersion().String())
	if !builtIn {
		return u, true
	}
	typed, err := scheme.Scheme.New(gvk)
	if err != nil || runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, typed) != nil {
		return nil, false
	}
	// An informer's objects have no apiVersion and kind, and the dispatcher
	// gives them those of paramKind; this one has them already, which the
	// policy reads the same, so the dispatcher hands the evaluator this
	// very object, and Run knows which document it is.
	typed.GetObjectKind().SetGroupVersionKind(gvk)
	return typed, true
}

// scopeOf is the RESTScope of a namespaced kind or of a cluster-scoped one.
func scopeOf(namespaced bool) meta.RESTScope {
	if namespaced {
		return meta.RESTScopeNamespace
	}
	return meta.RESTScopeRoot
}

// builtInKind is a built-in resource and its kind.
type builtInKind struct {
	gvr schema.GroupVersionResource
	gvk schema.GroupVersionKind
}

// builtInKinds are the built-in resources the Kubernetes version kube
// serves (see matchkind.Kinds).
func builtInKinds(kube kubeversion.Version) []builtInKind {
	var out []builtInKind
	for _, k := range matchkind.Kinds(kube.Minor()) {
		if gvr, _, ok := matchkind.Resource(k.GVK); ok {
			out = append(out, builtInKind{gvr: gvr, gvk: k.GVK})
		}
	}
	return out
}
