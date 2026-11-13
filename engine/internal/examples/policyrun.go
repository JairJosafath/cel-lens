package examples

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission/plugin/policy/validating"
	admissionrules "k8s.io/apiserver/pkg/admission/plugin/webhook/predicates/rules"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// PolicyVerdict is what an expression of a policy does to an example.
type PolicyVerdict int

// The verdicts of a policy's expressions.
const (
	// PolicyAllowed is a validation that admits the example, an audit
	// annotation that adds none (its value is null or empty), or a match
	// condition that matches it.
	PolicyAllowed PolicyVerdict = iota
	// PolicyRefused is a validation that fails under a binding whose
	// validationActions have Deny: the API server refuses the example.
	PolicyRefused
	// PolicyWarned is a validation that fails under a binding whose
	// validationActions have Warn and not Deny: the example is allowed, with
	// a warning (Message).
	PolicyWarned
	// PolicyAudited is a validation that fails under a binding whose
	// validationActions have Audit alone: the example is allowed, and the
	// failure is an audit annotation.
	PolicyAudited
	// PolicyAnnotated is an audit annotation the example gets (Message is
	// its value): it never refuses.
	PolicyAnnotated
	// PolicyExcluded is a match condition that is false on the example: the
	// policy does not run on it.
	PolicyExcluded
	// PolicyNotRun is an expression the API server does not run on the
	// example, or that cel-lens does not (see Cause).
	PolicyNotRun
	// PolicyError is an expression whose evaluation failed (Message);
	// Refuses says whether the API server then refuses the example.
	PolicyError
)

// String is the verdict in a word or two: "allowed", "refused", "warned",
// "audited", "annotated", "excluded", "not run" or "error".
func (v PolicyVerdict) String() string {
	switch v {
	case PolicyAllowed:
		return "allowed"
	case PolicyRefused:
		return "refused"
	case PolicyWarned:
		return "warned"
	case PolicyAudited:
		return "audited"
	case PolicyAnnotated:
		return "annotated"
	case PolicyExcluded:
		return "excluded"
	case PolicyNotRun:
		return "not run"
	case PolicyError:
		return "error"
	}
	return "verdict " + strconv.Itoa(int(v))
}

// PolicyResult is what an expression of a policy does to an example under
// a binding and a param.
type PolicyResult struct {
	Rule    *PolicyRule
	Example *Object
	// Update reports whether the example was admitted as an update from the
	// example before it, as opposed to a create.
	Update bool
	// Binding is the binding it was admitted under, nil when no binding of
	// the workspace binds the policy (see PolicyCache.Run); Param is the
	// param it was evaluated with, nil for none.
	Binding *Binding
	Param   *Object
	Verdict PolicyVerdict
	// Message is the API server's: the validation's message for a failed
	// validation, the value of an audit annotation, the error of an error,
	// or, for PolicyNotRun, why the expression did not run, which Cause
	// says the kind of.
	Message string
	Cause   Cause
	// Refuses reports whether this result makes the API server refuse the
	// example: a validation that fails under Deny, an error under the
	// failurePolicy Fail and Deny, or an audit annotation that fails.
	Refuses bool
	// Expected and OnExample are as for Result.
	Expected, OnExample bool

	// rule and param name Rule and Param across runs, for a result a
	// PolicyCache kept.
	rule  fieldIndex
	param place
}

// place names a document by its file and line.
type place struct {
	file string
	line int
}

// Admission is what the API server answers to an example under a binding
// of a policy, as its ValidatingAdmissionPolicy dispatcher answers it.
type Admission struct {
	Example *Object
	// Old is the example it is an update from, nil for a create.
	Old *Object
	// Binding is nil when no binding of the workspace binds the policy.
	Binding *Binding
	// Denied is the error the API server refuses the example with, "" when
	// it allows it; Warnings and Annotations are the warnings and the
	// audit annotations it adds.
	Denied      string
	Warnings    []string
	Annotations map[string]string
	// NotRun says why the dispatcher did not run on the example, "" when it
	// did.
	NotRun string
}

// PolicyRun is what a policy does to its examples: each result, ordered by
// expression then example, and the API server's answer to each example
// under each binding.
type PolicyRun struct {
	Results    []PolicyResult
	Admissions []Admission
}

// PolicyInput is the workspace a policy runs on.
type PolicyInput struct {
	// CRDs are the CustomResourceDefinitions the API server serves.
	CRDs []*CRD
	// Bindings are the bindings of the workspace, of any policy.
	Bindings []*Binding
	// Objects are the documents of the workspace a policy may run on, and
	// that its params and namespaces come from: the examples of CRDs,
	// checked, and the documents of built-in kinds (FindBuiltIn), each file's
	// in its order.
	Objects []*Object
}

// PolicyOptions are what PolicyCache.Run takes beside the policy and the
// workspace. The zero value has Run do all the work itself.
type PolicyOptions struct {
	// Pause, when not nil, is called before each step of the work.
	Pause func(context.Context)
	// Checked, when not nil, are the diagnostics of the engine's check of
	// the file of the policy (check.Check), of its text, for the
	// Kubernetes version of the policy.
	Checked []check.Diagnostic
	// Before, when not nil, says why the API server refuses an example
	// before any admission policy runs on it, "" when it does not: besides
	// what Check found, such as a rule of its CustomResourceDefinition that
	// refuses it.
	Before func(*Object) string
}

// maxPolicyExamples is the most examples of a policy Run runs it on: the
// first that it matches, in the order of the objects.
const maxPolicyExamples = 100

// PolicyCache keeps what each policy did to each example and the policies
// compiled, until the policy, the binding, the params, the namespace, the
// example or the Kubernetes version changes. The zero value is ready to
// use; it is safe for concurrent use.
type PolicyCache struct {
	mu sync.Mutex // guards policies
	// policies are what the cache keeps, by the file and name of the policy.
	policies map[string]*policyCache
}

// policyCache is what a PolicyCache keeps of one policy.
type policyCache struct {
	compiled   map[string]compiled
	outcomes   map[string]policyOutcome
	checked    []check.Diagnostic
	checkedKey string
}

// policyOutcome is what a policy did to one example under one binding,
// before what the example expects is known.
type policyOutcome struct {
	results   []PolicyResult
	admission Admission
}

// Run runs p, whose expressions are rules (Policy.Rules), on the documents
// of in that its matchConstraints match, as the API server's
// ValidatingAdmissionPolicy plugin admits them: matched by its own matcher
// (resource rules, excludeResourceRules, matchPolicy with the equivalent
// versions of a resource, namespaceSelector against the Namespace document
// of the example's namespace, objectSelector against the example's labels),
// then, for each binding of in that binds p and whose matchResources match
// the example, with its params (paramRef: name or selector, namespace,
// parameterNotFoundAction), its match conditions, variables, validations,
// failurePolicy, audit annotations and validationActions, all by the API
// server's code. A document in the file of p is none of its examples, nor
// is a policy or a binding, which the API server exempts; at most 100
// examples, the first, are run.
//
// The request is a create, or an update from the last example before it of
// the same object that the API server would have stored (Before said
// nothing of it and no binding refused it), by no user in particular. An
// expression that reads authorizer is not run, as cel-lens runs no
// authorizer check; with a match condition that reads it, nothing is. With
// no binding of in for p, p runs as if bound with the validationActions
// Deny and nothing else, and the expressions that read params do not run.
// When the engine's check of the file finds an error on an expression of
// p, the API server would refuse p, and none of its expressions runs.
//
// A result is Expected as for Run of a Cache: an example that expects to be
// allowed expects no result to refuse it; one that expects to be refused is
// right when Before refuses it or the API server refuses it under a
// binding, with its message part in the message when it gives one.
func (c *PolicyCache) Run(ctx context.Context, p *Policy, rules []*PolicyRule, in PolicyInput, opts PolicyOptions) (PolicyRun, error) {
	pause := opts.Pause
	if pause == nil {
		pause = func(context.Context) {}
	}
	key := p.File + "\x00" + p.Name
	c.mu.Lock()
	old := c.policies[key]
	c.mu.Unlock()
	if old == nil {
		old = &policyCache{}
	}
	next := &policyCache{compiled: map[string]compiled{}, outcomes: map[string]policyOutcome{}}
	refused, err := next.problems(ctx, old, p, opts.Checked, pause)
	if err != nil {
		return PolicyRun{}, err
	}
	r := &policyRunner{p: p, rules: rules, in: in, refused: refused, reads: policyReads(p.spec.Spec), before: opts.Before}
	r.prepare()
	var out PolicyRun
	stored := map[*Object]bool{}
	matched := 0
	for _, o := range in.Objects {
		if matched == maxPolicyExamples {
			break
		}
		req, ok := r.request(o, stored)
		if !ok {
			// The API server stores what the policy does not match.
			if r.refusedBefore(o) == "" {
				stored[o] = true
			}
			continue
		}
		matched++
		before := r.refusedBefore(o)
		refusedBy := before != ""
		for _, b := range r.bindings {
			if !r.bindingMatches(b, req) {
				continue
			}
			okey := r.outcomeKey(b, req, before)
			oc, ok := old.outcomes[okey]
			if !ok {
				if oc, ok = next.outcomes[okey]; !ok {
					pause(ctx)
					if err := ctx.Err(); err != nil {
						return PolicyRun{}, err
					}
					oc = r.admit(ctx, next, old, b, req, before)
				}
			}
			next.outcomes[okey] = oc
			refusedBy = refusedBy || oc.admission.Denied != ""
			// What the cache kept was found for other copies of the rules,
			// the example, its binding and its params.
			for _, res := range oc.results {
				res.Rule, res.Example, res.Binding, res.Param = r.rule(res.rule), req.example, r.binding(b), r.objectAt(res.param)
				if res.Rule != nil {
					out.Results = append(out.Results, res)
				}
			}
			adm := oc.admission
			adm.Example, adm.Old, adm.Binding = req.example, req.old, r.binding(b)
			out.Admissions = append(out.Admissions, adm)
		}
		if !refusedBy {
			stored[o] = true
		}
	}
	order := make(map[*PolicyRule]int, len(rules))
	for i, rule := range rules {
		order[rule] = i
	}
	slices.SortStableFunc(out.Results, func(a, b PolicyResult) int { return cmp.Compare(order[a.Rule], order[b.Rule]) })
	policyExpected(out, r.refusedBefore)
	c.mu.Lock()
	if c.policies == nil {
		c.policies = map[string]*policyCache{}
	}
	c.policies[key] = next
	c.mu.Unlock()
	return out, nil
}

// The reasons an expression of a policy does not run.
const (
	refusedPolicy      = "the API server refuses the ValidatingAdmissionPolicy, so it admits nothing with it: "
	readsAuthz         = "the expression reads authorizer, and cel-lens runs no authorizer check"
	conditionAuthz     = "a match condition of the policy reads authorizer, and cel-lens runs no authorizer check, so whether the policy runs on the example is not known"
	conditionNoBinding = "a match condition of the policy reads params, and no ValidatingAdmissionPolicyBinding of the workspace binds the policy, so nothing gives it params, and whether the policy runs on the example is not known"
	noBinding          = "no ValidatingAdmissionPolicyBinding of the workspace binds the policy, so nothing gives it params"
)

// policyRunner is one Run of a policy.
type policyRunner struct {
	p       *Policy
	rules   []*PolicyRule
	in      PolicyInput
	refused string
	reads   func(string) reads
	before  func(*Object) string
	// cluster is what the API server knows of the workspace; bindings are
	// those of p, a stand-in for none (see Run); spec is p as it runs, the
	// expressions cel-lens does not run left out, and validations and
	// annotations the indexes in spec of p's.
	cluster     *cluster
	bindings    []*Binding
	spec        *admissionregistrationv1.ValidatingAdmissionPolicy
	validations []int
	annotations []int
	// skipped says why an expression of p does not run, by its field and
	// index; conditions says why none of p runs, as a match condition
	// cel-lens does not run decides whether p runs at all, "" when all of
	// them run, and conditionsCause what kind of reason that is.
	skipped         map[fieldIndex]string
	conditions      string
	conditionsCause Cause
	compiledKey     string
}

// fieldIndex names an expression of a policy.
type fieldIndex struct {
	field PolicyField
	index int
}

// prepare finds the bindings of the policy, the cluster, and the policy as
// it runs.
func (r *policyRunner) prepare() {
	for _, b := range r.in.Bindings {
		if b.PolicyName() == r.p.Name {
			r.bindings = append(r.bindings, b)
		}
	}
	implicit := len(r.bindings) == 0
	if implicit {
		r.bindings = []*Binding{{Name: "", spec: admissionregistrationv1.ValidatingAdmissionPolicyBinding{
			Spec: admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec{PolicyName: r.p.Name, ValidationActions: []admissionregistrationv1.ValidationAction{admissionregistrationv1.Deny}},
		}}}
	}
	r.cluster = newCluster(r.in.CRDs, r.in.Objects, []*Policy{r.p}, r.p.kube)
	spec := r.p.spec.DeepCopy()
	r.skipped = map[fieldIndex]string{}
	hasParams := spec.Spec.ParamKind != nil
	skip := func(field PolicyField, i int, expr, alsoMessage string) bool {
		rd := r.reads(expr)
		if alsoMessage != "" {
			m := r.reads(alsoMessage)
			rd.params, rd.authorizer = rd.params || m.params, rd.authorizer || m.authorizer
		}
		switch {
		case rd.authorizer:
			r.skipped[fieldIndex{field, i}] = readsAuthz
		case implicit && hasParams && rd.params:
			r.skipped[fieldIndex{field, i}] = noBinding
		default:
			return false
		}
		return true
	}
	for i, c := range spec.Spec.MatchConditions {
		if !skip(FieldMatchCondition, i, c.Expression, "") || r.conditions != "" {
			continue
		}
		r.conditions, r.conditionsCause = conditionNoBinding, CauseNoBinding
		if r.skipped[fieldIndex{FieldMatchCondition, i}] == readsAuthz {
			r.conditions, r.conditionsCause = conditionAuthz, CauseAuthorizer
		}
	}
	var validations []admissionregistrationv1.Validation
	for i, v := range spec.Spec.Validations {
		if !skip(FieldValidation, i, v.Expression, v.MessageExpression) {
			r.validations = append(r.validations, i)
			validations = append(validations, v)
		}
	}
	var annotations []admissionregistrationv1.AuditAnnotation
	for i, a := range spec.Spec.AuditAnnotations {
		if !skip(FieldAuditAnnotation, i, a.ValueExpression, "") {
			r.annotations = append(r.annotations, i)
			annotations = append(annotations, a)
		}
	}
	spec.Spec.Validations, spec.Spec.AuditAnnotations = validations, annotations
	r.spec = spec
	r.compiledKey = hashOf(r.p.kube.String(), spec)
}

// request returns the request for o when the policy's matchConstraints
// match it, as the API server's matcher matches them: a create, or an
// update from the last example of the same object before it that is
// stored; false when o is no example of the policy.
func (r *policyRunner) request(o *Object, stored map[*Object]bool) (request, bool) {
	if o.File == r.p.File || o.Object == nil || r.p.spec.Spec.MatchConstraints == nil {
		return request{}, false
	}
	gv, err := schema.ParseGroupVersion(o.APIVersion)
	if err != nil {
		return request{}, false
	}
	gvk := gv.WithKind(o.Kind)
	var gvr schema.GroupVersionResource
	switch {
	case o.CRD != nil:
		gvr = gv.WithResource(o.CRD.Plural)
	default:
		res, _, ok := matchkind.Resource(gvk)
		if !ok {
			return request{}, false
		}
		gvr = res
	}
	req := request{example: o, gvk: gvk, gvr: gvr, namespace: o.requestNamespace()}
	for before := o.Old; before != nil; before = before.Old {
		if stored[before] {
			req.old = before
			break
		}
	}
	attrs := req.attributes()
	if admissionrules.IsExemptAdmissionConfigurationResource(attrs) {
		return request{}, false
	}
	ok, _, kind, err := r.cluster.matcher.DefinitionMatches(attrs, objectInterfaces{mapper: r.cluster.mapper}, validating.NewValidatingAdmissionPolicyAccessor(&r.p.spec))
	if err != nil || !ok {
		return request{}, false
	}
	req.matched = kind
	return req, true
}

// bindingMatches reports whether the matchResources of b match req.
func (r *policyRunner) bindingMatches(b *Binding, req request) bool {
	ok, err := r.cluster.matcher.BindingMatches(req.attributes(), objectInterfaces{mapper: r.cluster.mapper}, validating.NewValidatingAdmissionPolicyBindingAccessor(&b.spec))
	return err == nil && ok
}

// refusedBefore says why the API server refuses o before any admission
// policy runs on it, "" when it does not.
func (r *policyRunner) refusedBefore(o *Object) string {
	if msg := o.noRules(); msg != "" {
		return msg
	}
	if len(o.Problems) > 0 {
		return "the API server refuses the example before the admission policies: " + o.Problems[0].Message
	}
	if r.before != nil {
		return r.before(o)
	}
	return ""
}

// outcomeKey is what the outcome of the policy on req under b depends on.
func (r *policyRunner) outcomeKey(b *Binding, req request, before string) string {
	var old map[string]any
	if req.old != nil {
		old = req.old.Object
	}
	var params []runtime.Object
	var where []place
	if s, ok := r.cluster.paramStore(r.spec); ok {
		params, _ = s.informer.Lister().List(labels.Everything())
		for _, p := range params {
			if o := s.objects[p]; o != nil {
				where = append(where, place{o.File, o.Line})
			}
		}
	}
	ns, _ := r.cluster.namespaces.Get(req.namespace)
	return hashOf(r.compiledKey, r.refused, b.Name, b.spec.Spec, req.gvk, req.matched, req.example.Object, old, before, params, fmt.Sprint(where), ns)
}

// admit admits req under b and returns what each expression did.
func (r *policyRunner) admit(ctx context.Context, next, old *policyCache, b *Binding, req request, before string) policyOutcome {
	adm := Admission{Example: req.example, Old: req.old, Binding: r.binding(b)}
	notRun := func(reason string, cause Cause) policyOutcome {
		adm.NotRun = reason
		var out []PolicyResult
		for _, rule := range r.rules {
			out = append(out, r.result(rule, req, b, nil, PolicyResult{Verdict: PolicyNotRun, Message: reason, Cause: cause}))
		}
		return policyOutcome{results: out, admission: adm}
	}
	switch {
	case before != "":
		return notRun(before, CauseExampleRefused)
	case r.refused != "":
		return notRun(r.refused, CausePolicyRefused)
	case r.conditions != "":
		return notRun(r.conditions, r.conditionsCause)
	case req.matched != req.gvk && (req.example.CRD == nil || !req.example.CRD.converts):
		return notRun(fmt.Sprintf("the policy matches the example as %s, and the API server would convert it to that version, which cel-lens does not", req.matched), CauseConversion)
	}
	if pk := r.spec.Spec.ParamKind; pk != nil {
		if _, ok := r.cluster.paramStore(r.spec); !ok {
			return notRun(fmt.Sprintf("the paramKind %s %s is neither built in nor served by a CustomResourceDefinition of the workspace", pk.APIVersion, pk.Kind), CauseNoParams)
		}
	}
	comp, ok := next.compiled[r.compiledKey]
	if !ok {
		if comp, ok = old.compiled[r.compiledKey]; !ok {
			comp = compilePolicy(r.spec, r.p.kube)
		}
		next.compiled[r.compiledKey] = comp
	}
	a, evals := r.cluster.admit(ctx, r.spec, comp, &b.spec, req)
	adm.Denied, adm.Warnings, adm.Annotations = a.denied, a.warnings, a.annotations
	if len(evals) == 0 {
		cause, reason := CauseNoParams, "no params found for the binding, and its paramRef.parameterNotFoundAction is Allow, so the API server allows the example without running the policy"
		if a.denied != "" {
			cause, reason = CauseBindingRefused, "the API server refuses the example for the binding, without running the policy: "+a.denied
		}
		out := notRun(reason, cause)
		out.admission.NotRun = reason
		out.admission.Denied = a.denied
		return out
	}
	var out []PolicyResult
	for _, e := range evals {
		param := r.cluster.paramObject(e.param)
		out = append(out, r.evaluated(req, b, param, e)...)
	}
	return policyOutcome{results: out, admission: adm}
}

// evaluated are the results of one evaluation of the policy on req, under
// b, with param.
func (r *policyRunner) evaluated(req request, b *Binding, param *Object, e evaluation) []PolicyResult {
	actions := b.spec.Spec.ValidationActions
	var out []PolicyResult
	excluded := ""
	failed := ""
	for i, m := range e.conditions {
		switch {
		case m.Error != nil:
			msg := m.Error.Error()
			if !r.failsClosed() {
				msg += ": the policy's failurePolicy is Ignore, so the API server ignores the error and does not run the policy on the example"
			}
			if failed == "" {
				failed = r.p.spec.Spec.MatchConditions[i].Name
			}
			out = append(out, r.at(FieldMatchCondition, i, req, b, param, PolicyResult{Verdict: PolicyError, Message: msg, Refuses: e.match.Error != nil && slices.Contains(actions, admissionregistrationv1.Deny)}))
		case !m.Matches:
			if excluded == "" {
				excluded = r.p.spec.Spec.MatchConditions[i].Name
			}
			out = append(out, r.at(FieldMatchCondition, i, req, b, param, PolicyResult{Verdict: PolicyExcluded}))
		default:
			out = append(out, r.at(FieldMatchCondition, i, req, b, param, PolicyResult{Verdict: PolicyAllowed}))
		}
	}
	res := e.result
	switch {
	case e.match.Error == nil && !e.match.Matches:
		reason := "the match condition " + strconv.Quote(cmp.Or(e.match.FailedConditionName, excluded)) + " is false, so the policy does not run on the example"
		if e.match.FailedConditionName == "" && failed != "" {
			// No condition is false: one failed, and Ignore skipped the
			// policy.
			reason = "the match condition " + strconv.Quote(failed) + " fails, and the policy's failurePolicy is Ignore, so the API server does not run the policy on the example"
		}
		for _, f := range []PolicyField{FieldValidation, FieldAuditAnnotation} {
			for _, i := range r.indexes(f) {
				out = append(out, r.at(f, i, req, b, param, PolicyResult{Verdict: PolicyNotRun, Message: reason, Cause: CauseExcluded}))
			}
		}
		return r.withSkipped(out, req, b, param)
	case e.match.Error != nil && len(res.Decisions) > 0:
		d := res.Decisions[0]
		for _, f := range []PolicyField{FieldValidation, FieldAuditAnnotation} {
			for _, i := range r.indexes(f) {
				out = append(out, r.at(f, i, req, b, param, PolicyResult{Verdict: PolicyError, Message: d.Message, Refuses: d.Action == validating.ActionDeny && slices.Contains(actions, admissionregistrationv1.Deny)}))
			}
		}
		return r.withSkipped(out, req, b, param)
	}
	for k, d := range res.Decisions {
		if k >= len(r.validations) {
			break
		}
		out = append(out, r.at(FieldValidation, r.validations[k], req, b, param, decision(d, actions)))
	}
	for k, a := range res.AuditAnnotations {
		if k >= len(r.annotations) {
			break
		}
		pr := PolicyResult{Verdict: PolicyAllowed}
		switch a.Action {
		case validating.AuditAnnotationActionPublish:
			pr = PolicyResult{Verdict: PolicyAnnotated, Message: a.Value}
		case validating.AuditAnnotationActionError:
			pr = PolicyResult{Verdict: PolicyError, Message: a.Error, Refuses: true}
		case validating.AuditAnnotationActionExclude:
		}
		out = append(out, r.at(FieldAuditAnnotation, r.annotations[k], req, b, param, pr))
	}
	return r.withSkipped(out, req, b, param)
}

// decision is what a validation's decision d means under a binding with
// actions, as the API server's dispatcher acts on it: a denial refuses the
// example under Deny, warns under Warn, and is an audit annotation under
// Audit.
func decision(d validating.PolicyDecision, actions []admissionregistrationv1.ValidationAction) PolicyResult {
	deny := slices.Contains(actions, admissionregistrationv1.Deny)
	switch {
	case d.Evaluation == validating.EvalError:
		return PolicyResult{Verdict: PolicyError, Message: d.Message, Refuses: d.Action == validating.ActionDeny && deny}
	case d.Action == validating.ActionAdmit:
		return PolicyResult{Verdict: PolicyAllowed}
	case deny:
		return PolicyResult{Verdict: PolicyRefused, Message: d.Message, Refuses: true}
	case slices.Contains(actions, admissionregistrationv1.Warn):
		return PolicyResult{Verdict: PolicyWarned, Message: d.Message}
	}
	return PolicyResult{Verdict: PolicyAudited, Message: d.Message}
}

// failsClosed reports whether the policy's failurePolicy is Fail.
func (r *policyRunner) failsClosed() bool {
	return r.p.spec.Spec.FailurePolicy == nil || *r.p.spec.Spec.FailurePolicy == admissionregistrationv1.Fail
}

// indexes are the indexes of the expressions of field the policy runs.
func (r *policyRunner) indexes(field PolicyField) []int {
	if field == FieldValidation {
		return r.validations
	}
	return r.annotations
}

// withSkipped adds to out a result for each expression cel-lens does not
// run on req.
func (r *policyRunner) withSkipped(out []PolicyResult, req request, b *Binding, param *Object) []PolicyResult {
	for _, rule := range r.rules {
		if reason, ok := r.skipped[fieldIndex{rule.Field, rule.Index}]; ok {
			cause := CauseAuthorizer
			if reason == noBinding {
				cause = CauseNoBinding
			}
			out = append(out, r.result(rule, req, b, param, PolicyResult{Verdict: PolicyNotRun, Message: reason, Cause: cause}))
		}
	}
	return out
}

// at is pr, the result of the expression at field and index, on req under
// b with param; nothing when the policy has no such rule.
func (r *policyRunner) at(field PolicyField, index int, req request, b *Binding, param *Object, pr PolicyResult) PolicyResult {
	for _, rule := range r.rules {
		if rule.Field == field && rule.Index == index {
			return r.result(rule, req, b, param, pr)
		}
	}
	return r.result(nil, req, b, param, pr)
}

// result is pr for rule, on req under b with param.
func (r *policyRunner) result(rule *PolicyRule, req request, b *Binding, param *Object, pr PolicyResult) PolicyResult {
	pr.Rule, pr.Example, pr.Update, pr.Binding, pr.Param = rule, req.example, req.old != nil, r.binding(b), param
	if rule != nil {
		pr.rule = fieldIndex{rule.Field, rule.Index}
	}
	if param != nil {
		pr.param = place{param.File, param.Line}
	}
	return pr
}

// rule is the rule of the policy at at, nil for none.
func (r *policyRunner) rule(at fieldIndex) *PolicyRule {
	for _, rule := range r.rules {
		if rule.Field == at.field && rule.Index == at.index {
			return rule
		}
	}
	return nil
}

// objectAt is the document of the workspace at p, nil for none.
func (r *policyRunner) objectAt(p place) *Object {
	if p == (place{}) {
		return nil
	}
	for _, o := range r.in.Objects {
		if o.File == p.file && o.Line == p.line {
			return o
		}
	}
	return nil
}

// binding is b as a result names it: nil for the stand-in for none.
func (r *policyRunner) binding(b *Binding) *Binding {
	if b.Name == "" && b.File == "" {
		return nil
	}
	return b
}

// problems returns why the API server refuses p, as the engine's check of
// its file finds it for the Kubernetes version of p: the errors on its
// variables, validations, match conditions and audit annotations; "" when
// it finds none. checked is the check when the caller has it, nil to run
// it; problems keeps the check in f, from old when the file and the version
// are the same. The error is ctx's.
func (f *policyCache) problems(ctx context.Context, old *policyCache, p *Policy, checked []check.Diagnostic, pause func(context.Context)) (string, error) {
	f.checkedKey = hashOf(p.kube.String(), string(p.data))
	switch {
	case checked != nil:
		f.checked = errorsOf(checked)
	case old.checkedKey == f.checkedKey:
		f.checked = old.checked
	default:
		pause(ctx)
		diags, err := check.Check(ctx, p.File, p.data, check.WithVersion(p.kube))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		_ = err // a document that does not parse has its diagnostic, and no expression of a policy ReadPolicies read
		f.checked = errorsOf(diags)
	}
	var msgs []string
	for _, list := range []string{"spec.variables", "spec.validations", "spec.matchConditions", "spec.auditAnnotations"} {
		from, to := span(p.node, list)
		if from == (extract.Position{}) {
			continue
		}
		for _, d := range f.checked {
			if !before(d.Start, from) && before(d.Start, to) && !slices.Contains(msgs, d.Message) {
				msgs = append(msgs, d.Message)
			}
		}
	}
	if len(msgs) == 0 {
		return "", nil
	}
	return refusedPolicy + strings.Join(msgs, "; "), nil
}

// policyExpected sets Expected and OnExample of each result of run.
func policyExpected(run PolicyRun, before func(*Object) string) {
	refused := map[*Object]bool{}
	for _, a := range run.Admissions {
		if o := a.Example; o.Expect == Refused && (a.Denied != "" && strings.Contains(a.Denied, o.Message) || strings.Contains(before(o), o.Message) && before(o) != "") {
			refused[o] = true
		}
	}
	for i := range run.Results {
		res := &run.Results[i]
		if o := res.Example; o.Expect == Refused {
			res.Expected, res.OnExample = refused[o], !refused[o]
			continue
		}
		res.Expected = !res.Refuses
	}
}

// hashOf is a key of values, as JSON.
func hashOf(values ...any) string {
	js, err := json.Marshal(values)
	if err != nil {
		js = fmt.Appendf(nil, "%#v", values)
	}
	sum := sha256.Sum256(js)
	return hex.EncodeToString(sum[:])
}
