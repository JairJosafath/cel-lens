package lsp

import (
	"bytes"
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/examples"
	"github.com/JairJosafath/cel-lens/engine/internal/inline"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// The check of the examples also runs the rules of each CRD on its
// examples, as cel-lens.exampleResults does, and what each rule does to
// them goes where a person looks: a part of the line above the rule, a
// section of its hover, and a warning on the rule and on the example for
// each wrong verdict (package inline writes the words). The line and the
// hover find a rule's results by its ruleID, which an edit elsewhere in the
// file keeps, so they show the last results known while typing and are
// refreshed when the check of the new text ends; the warnings are those of
// the text they were found in, as the schema's problems on the examples.

// ruleID names a rule of a CRD file across edits: the index of its YAML
// document, the field path of its rule and its text.
type ruleID struct {
	document int
	field    string
	text     string
}

// fileFound is a file of examples a check found, with its text and its
// examples, checked.
type fileFound struct {
	f    walked
	text []byte
	objs []*examples.Object
}

// ruleFindings are what the rules of the CRDs did to their examples: the
// warnings on the examples, by the key of their file, and what each file of
// CRDs shows.
type ruleFindings struct {
	warnings map[string][]check.Diagnostic
	crds     map[string]crdFindings
}

// crdFindings are what a file of CRDs shows of what its rules do to their
// examples: the warnings on its rules, found in text, and what each rule
// does to each example, by rule.
type crdFindings struct {
	text     []byte
	warnings []check.Diagnostic
	rules    map[ruleID][]inline.Example
}

// exampleAt names an example by its file and line, across the copies Run
// makes of it.
type exampleAt struct {
	file string
	line int
}

// at is where o is.
func at(o *examples.Object) exampleAt { return exampleAt{o.File, o.Line} }

// evaluated is what the rules of the CRDs and the admission policies did
// to their examples, the expectations reconciled across them (see
// evaluate).
type evaluated struct {
	// crds are the results of the rules of each file of CRDs, by key, and
	// objs the examples of its CRDs.
	crds  map[string][]examples.Result
	rules map[string][]*examples.Rule
	objs  map[string][]*examples.Object
	// policies are what each admission policy did, by the key of its file.
	policies map[string][]policyRan
	// rels are the paths of the files below their folders, by key; right
	// are the examples that expect to be refused and something refuses as
	// they expect.
	rels  map[string]string
	right map[exampleAt]bool
}

// policyRan is what an admission policy did to its examples.
type policyRan struct {
	policy *examples.Policy
	rules  []*examples.PolicyRule
	run    examples.PolicyRun
}

// evaluate runs the rules of crds, the CRDs the check read, on found, their
// examples, file of CRDs by file, with c.results, then each admission
// policy the check found on its examples with c.policies, an example a
// rule of its CRD refuses being refused before the policies run, as the
// API server validates a custom resource before its admission. An example
// that expects to be refused is right when the schema, a rule or a policy
// refuses it as it expects, and each of its results then says it is
// expected, whatever refused it. The error is ctx's.
func (c *exampleCheck) evaluate(ctx context.Context, crds []*examples.CRD, found []fileFound) (evaluated, error) {
	ev := evaluated{crds: map[string][]examples.Result{}, rules: map[string][]*examples.Rule{}, objs: map[string][]*examples.Object{}, policies: map[string][]policyRan{}, rels: map[string]string{}, right: map[exampleAt]bool{}}
	maps.Copy(ev.rels, c.admission.rels)
	for _, ff := range found {
		ev.rels[ff.f.key] = ff.f.rel
		for _, o := range ff.objs {
			ev.objs[o.CRD.File] = append(ev.objs[o.CRD.File], o)
		}
	}
	before := map[exampleAt]string{}
	var files []string
	for _, crd := range crds {
		if !slices.Contains(files, crd.File) && len(ev.objs[crd.File]) > 0 {
			files = append(files, crd.File)
		}
	}
	for _, file := range files {
		var rules []*examples.Rule
		for _, crd := range crds {
			if crd.File != file {
				continue
			}
			found, err := crd.Rules(ctx)
			if err != nil {
				return evaluated{}, err
			}
			rules = append(rules, found...)
		}
		ev.rules[file] = rules
		if len(rules) == 0 || c.results == nil {
			ev.crds[file] = nil
			continue
		}
		results, err := c.results.Run(ctx, file, rules, ev.objs[file], examples.Options{Pause: c.wait, Checked: c.checks[file]})
		if err != nil {
			return evaluated{}, err
		}
		ev.crds[file] = results
		for _, r := range results {
			if (r.Verdict == examples.VerdictRefused || r.Verdict == examples.VerdictError) && before[at(r.Example)] == "" {
				msg := r.Error
				if len(r.Refusals) > 0 {
					msg = r.Refusals[0].Message
				}
				before[at(r.Example)] = "the rule " + strconv.Quote(r.Rule.Text) + " of its CustomResourceDefinition refuses the example, so the API server runs no admission policy on it: " + msg
			}
		}
	}
	if c.policies != nil {
		in := examples.PolicyInput{CRDs: crds, Bindings: c.admission.bindings, Objects: c.admission.objects}
		for _, p := range c.admission.policies {
			rules, err := p.Rules(ctx)
			if err != nil {
				return evaluated{}, err
			}
			run, err := c.policies.Run(ctx, p, rules, in, examples.PolicyOptions{
				Pause: c.wait, Checked: c.checks[p.File], MaxExamples: c.maxExamples, Before: func(o *examples.Object) string { return before[at(o)] },
			})
			if err != nil {
				return evaluated{}, err
			}
			ev.policies[p.File] = append(ev.policies[p.File], policyRan{policy: p, rules: rules, run: run})
		}
	}
	ev.reconcile()
	return ev, nil
}

// reconcile finds the examples that expect to be refused and that the
// schema, a rule or a policy refuses as they expect, and makes every result
// of such an example say so, and every other result of an example that
// expects to be refused say it is not.
func (ev *evaluated) reconcile() {
	for _, results := range ev.crds {
		for _, r := range results {
			if r.Example.Expect == examples.Refused && r.Expected {
				ev.right[at(r.Example)] = true
			}
		}
	}
	for _, runs := range ev.policies {
		for _, pr := range runs {
			for _, r := range pr.run.Results {
				if r.Example.Expect == examples.Refused && r.Expected {
					ev.right[at(r.Example)] = true
				}
			}
		}
	}
	for _, results := range ev.crds {
		for i := range results {
			if r := &results[i]; r.Example.Expect == examples.Refused {
				r.Expected, r.OnExample = ev.right[at(r.Example)], !ev.right[at(r.Example)]
			}
		}
	}
	for _, runs := range ev.policies {
		for _, pr := range runs {
			for i := range pr.run.Results {
				if r := &pr.run.Results[i]; r.Example.Expect == examples.Refused {
					r.Expected, r.OnExample = ev.right[at(r.Example)], !ev.right[at(r.Example)]
				}
			}
		}
	}
}

// runRules evaluates the rules of crds and the admission policies on their
// examples (see evaluate) and returns what they did, as the lines, the
// hovers and the warnings show it. A file of CRDs no example of which was
// found is skipped. The error is ctx's.
func (c *exampleCheck) runRules(ctx context.Context, crds []*examples.CRD, found []fileFound) (ruleFindings, error) {
	ev, err := c.evaluate(ctx, crds, found)
	if err != nil {
		return ruleFindings{}, err
	}
	out := ruleFindings{warnings: map[string][]check.Diagnostic{}, crds: map[string]crdFindings{}}
	file := func(key string, text []byte) crdFindings {
		cf, ok := out.crds[key]
		if !ok {
			cf = crdFindings{text: text, rules: map[ruleID][]inline.Example{}}
		}
		return cf
	}
	onExample := func(o *examples.Object, rule string, ie inline.Example) {
		start, end := o.Head()
		out.warnings[o.File] = append(out.warnings[o.File], warning(start, end, inline.ExampleWarning(rule, ie)))
	}
	var seen []*examples.Object
	for key, results := range ev.crds {
		cf := file(key, c.memo.crds[key].text)
		for _, r := range results {
			ie := exampleOf(r, ev.rels[r.Example.File])
			id := ruleID{r.Rule.Document, r.Rule.FieldPath, r.Rule.Text}
			cf.rules[id] = append(cf.rules[id], ie)
			if r.Expected || r.OnExample {
				continue
			}
			cf.warnings = append(cf.warnings, warning(r.Rule.Start, r.Rule.End, inline.RuleWarning(ie)))
			onExample(r.Example, r.Rule.Text, ie)
		}
		out.crds[key] = cf
		seen = append(seen, ev.objs[key]...)
	}
	for key, runs := range ev.policies {
		// As a file of CRDs without examples, a file of policies that run on
		// none shows nothing.
		if !slices.ContainsFunc(runs, func(pr policyRan) bool { return len(pr.run.Results) > 0 }) {
			continue
		}
		cf := file(key, c.admission.texts[key])
		for _, pr := range runs {
			for _, r := range pr.run.Results {
				ie := policyExampleOf(r, ev.rels)
				id := ruleID{r.Rule.Document, r.Rule.FieldPath, r.Rule.Text}
				cf.rules[id] = append(cf.rules[id], ie)
				seen = append(seen, r.Example)
				if r.Expected || r.OnExample {
					continue
				}
				cf.warnings = append(cf.warnings, warning(r.Rule.Start, r.Rule.End, inline.RuleWarning(ie)))
				onExample(r.Example, r.Rule.Text, ie)
			}
		}
		out.crds[key] = cf
	}
	warned := map[exampleAt]bool{}
	for _, o := range seen {
		k := at(o)
		if warned[k] || o.Expect != examples.Refused || ev.right[k] || o.RefusedBySchema() {
			continue
		}
		warned[k] = true
		start, end := o.Head()
		out.warnings[o.File] = append(out.warnings[o.File], warning(start, end, inline.NothingRefuses(o.Message)))
	}
	return out, nil
}

// policyExampleOf is r, what an expression of an admission policy does to
// an example, as the line and the hover say it; rels are the paths of the
// files below their folders, by key.
func policyExampleOf(r examples.PolicyResult, rels map[string]string) inline.Example {
	o := r.Example
	e := inline.Example{
		File: rels[o.File], URI: pathURI(filepath.FromSlash(o.File)), Line: o.Line, Update: r.Update,
		Wrong: !r.Expected, OnExample: r.OnExample, Message: r.Message, Binding: inline.Unbound,
	}
	if r.Binding != nil {
		e.Binding = r.Binding.Name
	}
	if r.Param != nil {
		e.Param = rels[r.Param.File] + ":" + strconv.Itoa(r.Param.Line)
	}
	if o.Expect == examples.Refused {
		e.Want = o.Message
	}
	switch r.Verdict {
	case examples.PolicyAllowed:
		e.Verdict = inline.Allowed
	case examples.PolicyRefused:
		e.Verdict = inline.Refused
	case examples.PolicyWarned:
		e.Verdict = inline.Warned
	case examples.PolicyAudited:
		e.Verdict = inline.Audited
	case examples.PolicyAnnotated:
		e.Verdict = inline.Annotated
	case examples.PolicyExcluded:
		e.Verdict = inline.Excluded
	case examples.PolicyNotRun:
		e.Verdict, e.Skip = inline.NotRun, skipOf(r.Cause)
	case examples.PolicyError:
		e.Verdict = inline.Failed
	}
	return e
}

// warning is a warning of cel-lens from start to end saying msg.
func warning(start, end extract.Position, msg string) check.Diagnostic {
	return check.Diagnostic{Message: msg, Severity: check.SeverityWarning, Start: start, End: end}
}

// exampleOf is r, what a rule does to an example whose file is rel below
// its workspace folder, as the line and the hover say it.
func exampleOf(r examples.Result, rel string) inline.Example {
	o := r.Example
	e := inline.Example{
		File: rel, URI: pathURI(filepath.FromSlash(o.File)), Line: o.Line, Update: r.Update,
		Wrong: !r.Expected, OnExample: r.OnExample,
	}
	if o.Expect == examples.Refused {
		e.Want = o.Message
	}
	switch r.Verdict {
	case examples.VerdictAllowed:
		e.Verdict = inline.Allowed
	case examples.VerdictRefused:
		e.Verdict = inline.Refused
		msgs := make([]string, len(r.Refusals))
		for i, f := range r.Refusals {
			msgs[i] = f.Message
		}
		e.Message = strings.Join(msgs, "; ")
	case examples.VerdictNotRun:
		e.Verdict, e.Message, e.Skip = inline.NotRun, r.Reason, skipOf(r.Cause)
	case examples.VerdictError:
		e.Verdict, e.Message = inline.Failed, r.Error
	}
	return e
}

// skipOf is c as the line says it.
func skipOf(c examples.Cause) inline.Skip {
	switch c {
	case examples.CauseCRDRefused:
		return inline.SkipCRD
	case examples.CauseExampleRefused:
		return inline.SkipExample
	case examples.CauseNotUpdate:
		return inline.SkipNotUpdate
	case examples.CauseNoOldValue:
		return inline.SkipNoOldValue
	case examples.CauseAbsent:
		return inline.SkipAbsent
	case examples.CausePolicyRefused:
		return inline.SkipPolicy
	case examples.CauseExcluded:
		return inline.SkipExcluded
	case examples.CauseNoBinding:
		return inline.SkipNoBinding
	case examples.CauseNoParams:
		return inline.SkipNoParams
	case examples.CauseBindingRefused:
		return inline.SkipBinding
	case examples.CauseAuthorizer:
		return inline.SkipAuthorizer
	case examples.CauseConversion:
		return inline.SkipConversion
	case examples.CauseNone:
	}
	return inline.SkipOther
}

// ruleExamples returns what the rule e, an expression of the open document
// with the key, does to the examples of its CRD, as the last check of the
// examples found it for the Kubernetes version in use; nil while the
// setting exampleResults is off, for an expression that is no rule of a
// CRD, and while nothing is known of it.
func (ss *session) ruleExamples(key string, e extract.Expression) []inline.Example {
	f, ok := ss.exampleFound[key]
	if ss.examplesOff || !ok || f.rules == nil || f.kube != ss.kube || e.Kind == extract.KindMessageExpression {
		return nil
	}
	return f.rules[ruleID{e.Document, e.FieldPath, e.Text}]
}

// knowsRules reports whether the last check of the examples found what the
// rules of the file with the key do to them, for the Kubernetes version in
// use.
func (ss *session) knowsRules(key string) bool {
	f, ok := ss.exampleFound[key]
	return !ss.examplesOff && ok && f.rules != nil && f.kube == ss.kube
}

// loopChecks are the message loop's checks of the open documents that may
// hold a CRD, by key, for the check of the examples to take rather than
// check them again (see examples.Options.Checked). They are those the loop
// sends as diagnostics, made already but for a document not checked since
// it changed. The error is ctx's.
func (ss *session) loopChecks(ctx context.Context) (map[string][]check.Diagnostic, error) {
	out := map[string][]check.Diagnostic{}
	for _, uri := range slices.Sorted(maps.Keys(ss.docs)) {
		d := ss.docs[uri]
		if !bytes.Contains(d.text, []byte(crdWord)) && !examples.HasPolicies(d.text) {
			continue
		}
		diags, err := ss.diagnostics(ctx, uri, d)
		if err != nil {
			return nil, err
		}
		out[crdKey(uri)] = append([]check.Diagnostic{}, diags...)
	}
	return out, nil
}

// switchExamples follows the setting exampleResults, which changed: off, it
// stops the check of the examples and takes back what they showed, the
// schema's problems and the warnings, and the parts of the lines; on, it
// starts the wait for the next check, which shows them again.
func (ss *session) switchExamples(ctx context.Context) error {
	if !ss.examplesOff {
		ss.examplesChanged()
		return nil
	}
	ss.stopExamples()
	return ss.showExamples(ctx, nil)
}
