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

	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel/model"
	apiextensionsfeatures "k8s.io/apiextensions-apiserver/pkg/features"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/apiserver/pkg/cel/common"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/apiserver/pkg/warning"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// Verdict is what a rule does to an example.
type Verdict int

// The verdicts.
const (
	// VerdictAllowed is a rule that ran on the example and refused nothing, or
	// whose refusals the API server ignored (see Result.Ignored).
	VerdictAllowed Verdict = iota
	// VerdictRefused is a rule that refused the example (see Result.Refusals).
	VerdictRefused
	// VerdictNotRun is a rule the API server does not run on the example (see
	// Result.Reason).
	VerdictNotRun
	// VerdictError is a rule whose run stopped with an error: a runtime error or a
	// cost limit (see Result.Error).
	VerdictError
)

// String is "allowed", "refused", "not run" or "error".
func (v Verdict) String() string {
	switch v {
	case VerdictAllowed:
		return "allowed"
	case VerdictRefused:
		return "refused"
	case VerdictNotRun:
		return "not run"
	case VerdictError:
		return "error"
	}
	return "verdict " + strconv.Itoa(int(v))
}

// Cause is why the API server does not run a rule on an example, for a
// Result whose Verdict is VerdictNotRun.
type Cause int

// The causes.
const (
	// CauseNone is a rule that ran, or failed to.
	CauseNone Cause = iota
	// CauseCRDRefused is a rule for which the API server refuses the
	// CustomResourceDefinition, alone or with the other rules for their
	// estimated cost together: it validates no object with it.
	CauseCRDRefused
	// CauseExampleRefused is an example the API server refuses before its
	// rules: it cannot decode it, or its schema has a problem after which it
	// runs none.
	CauseExampleRefused
	// CauseNotUpdate is a rule that reads oldSelf, without optionalOldSelf,
	// on an example that is a create.
	CauseNotUpdate
	// CauseNoOldValue is such a rule on an update without an old value where
	// the rule is.
	CauseNoOldValue
	// CauseAbsent is a rule whose field the example does not have.
	CauseAbsent
	// CausePolicyRefused is an expression of a ValidatingAdmissionPolicy
	// the API server refuses: it admits nothing with the policy.
	CausePolicyRefused
	// CauseExcluded is a policy whose match condition is false on the
	// example.
	CauseExcluded
	// CauseNoBinding is an expression that reads params, of a policy no
	// binding of the workspace binds.
	CauseNoBinding
	// CauseNoParams is a policy for which a binding has no params.
	CauseNoParams
	// CauseBindingRefused is a binding the API server refuses the example
	// for before it runs the policy, such as for params not found with
	// parameterNotFoundAction Deny.
	CauseBindingRefused
	// CauseAuthorizer is an expression that reads authorizer, which cel-lens
	// does not run, or a policy with a match condition that does.
	CauseAuthorizer
	// CauseConversion is an example the API server would convert to the
	// version the policy matches it as, which cel-lens does not.
	CauseConversion
)

// Refusal is a refusal of an example by a rule, as the API server returns
// it.
type Refusal struct {
	// Path is the field it is about, as the API server writes it, such as
	// spec.ports[1].name; "" for the root.
	Path string
	// Message is the API server's message, the path first, such as
	// "spec.size: Invalid value: 20: must be at most 10".
	Message string
}

// Result is what a rule does to an example.
type Result struct {
	Rule    *Rule
	Example *Object
	// Update reports whether the rule ran on the example as an update from
	// the example before it (see Object.Old), as opposed to a create.
	Update  bool
	Verdict Verdict
	// Refusals are the refusals of the rule, each with its own path: one for
	// each item of a list or value of a map it refuses. A rule whose verdict
	// is VerdictError may have refused values before its error.
	Refusals []Refusal
	// Ignored are the messages of refusals the API server ignored, as an
	// update did not change the value they are about (validation
	// ratcheting). As Run pairs an update only with an example the API
	// server would have stored, which the same rule then allowed on the same
	// value, there are none unless a rule's verdict on a value can change
	// with nothing else; they are kept so that no refusal is lost unsaid.
	Ignored []string
	// Ran are the paths of the values the rule ran on, as the API server
	// writes them, "" for the root; none for a rule that did not run.
	Ran []string
	// Reason says why the rule did not run, for VerdictNotRun, and Cause
	// what kind of reason that is; Error is what stopped it, for
	// VerdictError.
	Reason, Error string
	Cause         Cause
	// Expected reports whether the result is what the example expects (see
	// Run). When it is not, OnExample reports whether that shows on the
	// example, as for an example that should be refused and that nothing
	// refuses, rather than at the rule.
	Expected, OnExample bool

	// cost is what the rule's run cost, in the API server's cost units;
	// stopped reports that it hit a cost limit, after which the API server
	// runs no further rule on the object.
	cost    int64
	stopped bool
}

// outcome is what a rule does to an example, before what the example
// expects is known: what a Cache keeps.
type outcome struct {
	verdict  Verdict
	refusals []Refusal
	ignored  []string
	ran      []string
	reason   string
	cause    Cause
	err      string
	cost     int64
	stopped  bool
}

// Cache keeps the rules made ready to run and what each did to each example,
// for each file of CRDs Run was given, until the rule, its schema, the
// example or the Kubernetes version changes. The zero value is ready to
// use; it is safe for concurrent use.
type Cache struct {
	mu sync.Mutex // guards files
	// files are what Cache keeps, by the File of the CRDs.
	files map[string]*fileCache
}

// fileCache is what a Cache keeps for one file of CRDs: the rules ready to
// run, and the outcomes, by the keys of the rule and of the example.
type fileCache struct {
	prepared map[string]*prepared
	outcomes map[string]outcome
	// checked are the errors the engine's check of the file found, for the
	// file's text and Kubernetes version that checkedKey stands for.
	checked    []check.Diagnostic
	checkedKey string
}

// Run runs rules, the rules of the CRDs of file, as Rules returns them, on
// objs, their examples, after Check checked them, as the API server's CEL
// validator runs them on a custom resource: each rule on the examples of
// its version, with the API server's cost limits and after the schema's
// check, which stops them as the API server stops them. It returns a Result
// for each rule and example, ordered by rule then example, and keeps what
// it found in c, dropping what c kept for file that no rule or example of
// this run has. The error is ctx's.
//
// A rule for which the API server refuses the CustomResourceDefinition, as
// the engine's check of the file finds it (package check, for the
// Kubernetes version of the CRD), is not run: no object is ever validated
// with it.
//
// An example runs as an update from the example before it (see Object.Old)
// when the API server would have stored that one: when neither its schema
// nor any of its rules refuses it, or fails on it. Otherwise it runs as a
// create, as kubectl apply -f of the file creates it, and is checked against
// the schema again as one; an example after it is an update from it when it
// would be stored as a create.
//
// A result is Expected when it is what its example expects. An example
// that expects to be allowed expects each rule to allow it or not to run:
// a rule that refuses it, or fails on it, is wrong at the rule. An example
// that expects to be refused is right when the schema or at least one rule
// refuses it, or fails on it, which the API server answers with a refusal
// too, with the message part it expects in the message when it gives one;
// each of its results is then Expected, and otherwise none is, OnExample.
// The rules of one version together get the API server's cost budget for
// one object: when they cost more on an example, or one of them hits a cost
// limit, each that ran on it gets VerdictError, as the API server stops
// there, at a rule that depends on the order it visits the object's fields
// in.
//
// opts.Pause is called before each step; opts.Checked, when given, is the
// check of the file to use (see Options).
func (c *Cache) Run(ctx context.Context, file string, rules []*Rule, objs []*Object, opts Options) ([]Result, error) {
	pause := opts.Pause
	if pause == nil {
		pause = func(context.Context) {}
	}
	c.mu.Lock()
	old := c.files[file]
	c.mu.Unlock()
	if old == nil {
		old = &fileCache{}
	}
	next := &fileCache{prepared: map[string]*prepared{}, outcomes: map[string]outcome{}}
	refused, err := next.crdProblems(ctx, old, rules, opts.Checked, pause)
	if err != nil {
		return nil, err
	}
	ruleKeys := make([]string, len(rules))
	for i, r := range rules {
		k := r.key()
		ruleKeys[i] = k
		if p, ok := old.prepared[k]; ok {
			next.prepared[k] = p
			continue
		}
		pause(ctx)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next.prepared[k] = r.prepare()
	}
	var out []Result
	// seen are the examples run so far; stored has, for each of them the
	// API server would store, what it would store.
	seen, stored := map[*Object]bool{}, map[*Object]*Object{}
	for _, o := range objs {
		e, err := paired(ctx, o, seen, stored, pause)
		if err != nil {
			return nil, err
		}
		key := e.key()
		var mine []Result
		for i, r := range rules {
			if r.version != o.version {
				continue
			}
			if msg, ok := refused[r]; ok {
				mine = append(mine, Result{Rule: r, Example: e, Update: e.old() != nil, Verdict: VerdictNotRun, Reason: msg, Cause: CauseCRDRefused})
				continue
			}
			k := ruleKeys[i] + key
			oc, ok := old.outcomes[k]
			if !ok {
				if oc, ok = next.outcomes[k]; !ok {
					pause(ctx)
					if oc, err = run(ctx, next.prepared[ruleKeys[i]], e); err != nil {
						return nil, err
					}
				}
			}
			next.outcomes[k] = oc
			mine = append(mine, Result{
				Rule: r, Example: e, Update: e.old() != nil, Verdict: oc.verdict, Refusals: oc.refusals, Ignored: oc.ignored,
				Ran: oc.ran, Reason: oc.reason, Cause: oc.cause, Error: oc.err, cost: oc.cost, stopped: oc.stopped,
			})
		}
		overBudget(mine)
		if len(e.Problems) == 0 && e.Object != nil && !slices.ContainsFunc(mine, func(r Result) bool { return refuses(r, "") }) {
			stored[o] = e
		}
		seen[o] = true
		out = append(out, mine...)
	}
	order := make(map[*Rule]int, len(rules))
	for i, r := range rules {
		order[r] = i
	}
	slices.SortStableFunc(out, func(a, b Result) int { return cmp.Compare(order[a.Rule], order[b.Rule]) })
	expected(out)
	c.mu.Lock()
	if c.files == nil {
		c.files = map[string]*fileCache{}
	}
	c.files[file] = next
	c.mu.Unlock()
	return out, nil
}

// paired returns o as the API server would get it after kubectl apply -f
// of its file: an update from the last example before it, of the same
// object, that the API server would have stored, which stored has, or a
// create when it would have stored none. That is o itself when Check paired
// it so; else a copy of o, checked again against what it is an update from.
// An example whose example before it Run has not seen, as one past the
// limit of examples, keeps the pairing of Check. The error is ctx's.
func paired(ctx context.Context, o *Object, seen map[*Object]bool, stored map[*Object]*Object, pause func(context.Context)) (*Object, error) {
	if o.Old == nil || !seen[o.Old] {
		return o, nil
	}
	var want *Object
	for before := o.Old; before != nil && seen[before] && want == nil; before = before.Old {
		want = stored[before]
	}
	if want == nil && o.old() == nil || want == o.Old && o.old() != nil {
		return o, nil
	}
	pause(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e := *o
	e.paired, e.prev = true, want
	e.check(ctx)
	return &e, nil
}

// crdProblems returns, for each of rules, all of one file, for which the
// API server refuses its CustomResourceDefinition, what it says, as the
// engine's check of the file finds it for the Kubernetes version of the
// CRDs: the errors placed on the rule's x-kubernetes-validations entry. The
// estimated cost of all the rules of a schema together, which the check
// places on one of them, is said of every rule of that CRD, as the API
// server refuses the whole CRD for it, whichever rules it names as the
// most expensive. checked is the
// check when the caller has it, nil to run it; crdProblems keeps the check
// in f, from old when the file and the version are the same. The error is
// ctx's.
func (f *fileCache) crdProblems(ctx context.Context, old *fileCache, rules []*Rule, checked []check.Diagnostic, pause func(context.Context)) (map[*Rule]string, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	crd := rules[0].CRD
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s", crd.kube, crd.data))
	f.checkedKey = hex.EncodeToString(sum[:])
	switch {
	case checked != nil:
		f.checked = errorsOf(checked)
	case old.checkedKey == f.checkedKey:
		f.checked = old.checked
	default:
		pause(ctx)
		diags, err := check.Check(ctx, crd.File, crd.data, check.WithVersion(crd.kube))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		_ = err // a document that does not parse has its diagnostic, and no rule of a CRD Read read
		f.checked = errorsOf(diags)
	}
	on := func(r *Rule, d check.Diagnostic) bool { return !before(d.Start, r.from) && before(d.Start, r.to) }
	total := map[*CRD][]string{}
	for _, d := range f.checked {
		if !d.CostTotal {
			continue
		}
		for _, r := range rules {
			if on(r, d) && !slices.Contains(total[r.CRD], d.Message) {
				total[r.CRD] = append(total[r.CRD], d.Message)
			}
		}
	}
	out := map[*Rule]string{}
	for _, r := range rules {
		if msgs := total[r.CRD]; len(msgs) > 0 {
			out[r] = refusedForTotal + strings.Join(msgs, "; ")
			continue
		}
		var msgs []string
		for _, d := range f.checked {
			if on(r, d) && !slices.Contains(msgs, d.Message) {
				msgs = append(msgs, d.Message)
			}
		}
		if len(msgs) > 0 {
			out[r] = refusedForRule + strings.Join(msgs, "; ")
		}
	}
	return out, nil
}

// errorsOf returns the errors among diags.
func errorsOf(diags []check.Diagnostic) []check.Diagnostic {
	return slices.DeleteFunc(slices.Clone(diags), func(d check.Diagnostic) bool { return d.Severity != check.SeverityError })
}

// Options are what Run takes beside the rules and the examples. The zero
// value has Run do all the work itself, with no pause.
type Options struct {
	// Pause, when not nil, is called before each step of the work: the
	// check of the file, each rule made ready, each example checked again,
	// each run of a rule on an example. So a caller can hold the work back
	// between two steps.
	Pause func(context.Context)
	// Checked, when not nil, are the diagnostics of the engine's check of
	// the file of the rules (check.Check), of the text Read read and for
	// the Kubernetes version of the CRDs, which the caller has: Run then
	// does not check the file itself. An empty, not nil, slice is a check
	// that found nothing.
	Checked []check.Diagnostic
}

// The reasons a rule for which the API server refuses its
// CustomResourceDefinition does not run: for the rule itself, or for the
// estimated cost of all the rules of a schema together.
const (
	refusedForRule  = "the API server refuses the CustomResourceDefinition for this rule, so it validates no object with it: "
	refusedForTotal = "the API server refuses the CustomResourceDefinition for the estimated cost of its rules together, so it validates no object with any of them: "
)

// key is what the results of a rule on o depend on but the rule: its
// object, the object it is an update from, and why no rule runs on it.
func (o *Object) key() string {
	obj, err := json.Marshal(o.Object)
	if err != nil {
		obj = fmt.Appendf(nil, "%p", o)
	}
	old, err := json.Marshal(o.old())
	if err != nil {
		old = fmt.Appendf(nil, "%p", o)
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%s", obj, old, o.noRules()))
	return hex.EncodeToString(sum[:])
}

// noRules says why the API server runs no CEL rule on o, from what Check
// found: it refuses o while it decodes it (DecodeFailed), or the validation
// has an error after which it runs none (RulesSkipped); "" when it runs
// them.
func (o *Object) noRules() string {
	switch {
	case o.DecodeFailed && len(o.Problems) > 0:
		return "the API server refuses the example while it decodes it, before its rules (kubectl asks for strict field validation): " + o.Problems[0].Message
	case o.DecodeFailed:
		return "the API server refuses the example while it decodes it, before its rules"
	case o.RulesSkipped:
		for _, fe := range o.Errors {
			if blocking(fe) {
				return fmt.Sprintf("the schema refuses the example (%s), and the API server then runs no rule", fe.Error())
			}
		}
	}
	return ""
}

// run runs the rule p on o. The error is ctx's.
func run(ctx context.Context, p *prepared, o *Object) (outcome, error) {
	old := o.old()
	switch {
	case p.problem != "":
		return outcome{verdict: VerdictNotRun, reason: p.problem, cause: CauseCRDRefused}, nil
	case o.Object == nil || o.DecodeFailed || o.RulesSkipped:
		return outcome{verdict: VerdictNotRun, reason: o.noRules(), cause: CauseExampleRefused}, nil
	}
	ran, err := sites(ctx, p, o.Object, old)
	if err != nil {
		return outcome{}, err
	}
	if len(ran) == 0 {
		reason, cause := notRunReason(p, o, old)
		return outcome{verdict: VerdictNotRun, reason: reason, cause: cause}, nil
	}
	var warned recorder
	wctx := warning.WithWarningRecorder(ctx, &warned)
	var opts []cel.Option
	if old != nil && utilfeature.DefaultFeatureGate.Enabled(apiextensionsfeatures.CRDValidationRatcheting) {
		opts = append(opts, cel.WithRatcheting(common.NewCorrelatedObject(o.Object, old, &model.Structural{Structural: o.version.structural})))
	}
	errs, left := p.validator.Validate(wctx, nil, o.version.structural, o.Object, old, celconfig.RuntimeCELCostBudget, opts...)
	if err := ctx.Err(); err != nil {
		return outcome{}, err
	}
	oc := outcome{verdict: VerdictAllowed, ran: ran, ignored: warned.texts, cost: celconfig.RuntimeCELCostBudget - max(left, 0)}
	for _, fe := range errs {
		if failure(fe) {
			oc.verdict = VerdictError
			oc.err = strings.TrimSpace(oc.err + "\n" + fe.Error())
			continue
		}
		if oc.verdict == VerdictAllowed {
			oc.verdict = VerdictRefused
		}
		oc.refusals = append(oc.refusals, Refusal{Path: fieldPath(fe), Message: fe.Error()})
	}
	// The API server visits the values of a map in no fixed order.
	slices.SortStableFunc(oc.refusals, func(a, b Refusal) int { return comparePaths(a.Path, b.Path) })
	slices.SortStableFunc(oc.ignored, comparePaths)
	oc.stopped = left < 0
	return oc, nil
}

// sites returns the paths of the values of obj the API server runs the rule
// p on, for an update from old or a create when old is nil: its probe
// refuses each, and only those. The error is ctx's.
func sites(ctx context.Context, p *prepared, obj, old map[string]any) ([]string, error) {
	var ran []string
	// The probe runs without ratcheting, which would only turn its
	// refusals of unchanged values into warnings.
	errs, _ := p.probe.Validate(ctx, nil, nil, obj, old, celconfig.RuntimeCELCostBudget)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, fe := range errs {
		if fe.Detail == probeMessage {
			ran = append(ran, fieldPath(fe))
		}
	}
	slices.SortStableFunc(ran, comparePaths)
	return ran, nil
}

// comparePaths orders field paths, or messages that start with one, as
// their text, but for the indexes of lists, which it orders as numbers:
// spec.ports[2] before spec.ports[10].
func comparePaths(a, b string) int {
	for a != "" && b != "" {
		na, nb := digits(a), digits(b)
		if na > 0 && nb > 0 {
			x, _ := strconv.Atoi(a[:na])
			y, _ := strconv.Atoi(b[:nb])
			if c := cmp.Compare(x, y); c != 0 {
				return c
			}
			a, b = a[na:], b[nb:]
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

// digits is how many ASCII digits s starts with, at most 9, so they make an
// int.
func digits(s string) int {
	n := 0
	for n < len(s) && n < 9 && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// notRunReason says why the API server ran the rule p on no value of o,
// with old the object it is an update from, nil for a create, and what
// kind of reason that is.
func notRunReason(p *prepared, o *Object, old map[string]any) (string, Cause) {
	switch {
	case p.usesOldSelf && !p.optional && old == nil && o.Old != nil:
		return "the example is not an update and the rule needs oldSelf: the API server refuses the example before it, so kubectl apply -f creates this one", CauseNotUpdate
	case p.usesOldSelf && !p.optional && old == nil:
		return "the example is not an update and the rule needs oldSelf", CauseNotUpdate
	case p.usesOldSelf && !p.optional:
		return "the rule needs oldSelf, and the update has no old value where the rule is", CauseNoOldValue
	}
	return "the rule's field is absent from the example", CauseAbsent
}

// failure reports whether fe, an error of the API server's CEL validator, is
// a rule's failure to run rather than its refusal: a runtime error, a cost
// limit, or an internal error, as validation.go of
// k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel writes them. A
// refusal's message is the rule's.
func failure(fe *field.Error) bool {
	if fe.Type == field.ErrorTypeInternal {
		return true
	}
	d := fe.Detail
	for _, prefix := range []string{"rule compile error: ", "rule compiler initialization error: ", "runtime cost could not be calculated for validation rule: "} {
		if strings.HasPrefix(d, prefix) {
			return true
		}
	}
	for _, part := range []string{" evaluating rule: ", "': call arguments did not match a supported operator, function or macro signature for rule: ", "no further validation rules will be run"} {
		if strings.Contains(d, part) {
			return true
		}
	}
	return false
}

// fieldPath is the path of fe, "" for the root.
func fieldPath(fe *field.Error) string {
	if fe.Field == "<nil>" {
		return ""
	}
	return fe.Field
}

// recorder keeps the warnings the API server adds: the refusals ratcheting
// ignores.
type recorder struct {
	texts []string
}

// AddWarning keeps the text of a warning.
func (r *recorder) AddWarning(_, text string) { r.texts = append(r.texts, text) }

// overBudget makes an error of each rule that ran on an example on which
// the API server stops running rules for their cost: the rules of its
// version together cost more than its budget for one object, or another
// rule hit a cost limit. Which rules it runs before it stops depends on
// the order it visits the object's fields in, so none is known to run.
func overBudget(results []Result) {
	total := map[*Object]int64{}
	stopped := map[*Object]bool{}
	for _, r := range results {
		total[r.Example] += r.cost
		stopped[r.Example] = stopped[r.Example] || r.stopped
	}
	for i := range results {
		r := &results[i]
		if len(r.Ran) == 0 || r.stopped {
			continue
		}
		switch {
		case stopped[r.Example]:
			r.Error = "another rule of " + r.Rule.Version + " hit a cost limit on this example, after which the API server runs no further rule, so whether this one runs depends on the order it visits the object's fields in"
		case total[r.Example] > celconfig.RuntimeCELCostBudget:
			r.Error = fmt.Sprintf("the rules of %s together cost %d on this example, more than the API server's budget of %d for one object, so it stops at the rule that runs it out, which depends on the order it visits the object's fields in", r.Rule.Version, total[r.Example], celconfig.RuntimeCELCostBudget)
		default:
			continue
		}
		r.Verdict = VerdictError
	}
}

// expected sets Expected and OnExample of each result, as Run says.
func expected(results []Result) {
	refused := map[*Object]bool{}
	for _, r := range results {
		if o := r.Example; o.Expect == Refused && refuses(r, o.Message) {
			refused[o] = true
		}
	}
	for i := range results {
		r := &results[i]
		if o := r.Example; o.Expect == Refused {
			right := refused[o] || o.RefusedBySchema()
			r.Expected, r.OnExample = right, !right
			continue
		}
		r.Expected = r.Verdict == VerdictAllowed || r.Verdict == VerdictNotRun
	}
}

// RefusedBySchema reports whether the schema refuses o, as Check found, with
// the message part o expects when it gives one (Message).
func (o *Object) RefusedBySchema() bool {
	for _, p := range o.Problems {
		if strings.Contains(p.Message, o.Message) {
			return true
		}
	}
	return false
}

// refuses reports whether r refuses its example, or fails on it, with msg
// in the message, "" for any.
func refuses(r Result, msg string) bool {
	switch r.Verdict {
	case VerdictRefused, VerdictError:
		if r.Verdict == VerdictError && strings.Contains(r.Error, msg) {
			return true
		}
		for _, f := range r.Refusals {
			if strings.Contains(f.Message, msg) {
				return true
			}
		}
	case VerdictAllowed, VerdictNotRun:
	}
	return false
}
