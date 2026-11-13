package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/JairJosafath/cel-lens/engine/internal/examples"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// resultsArgs are the arguments of exampleResults: the file of the rules,
// and, to ask for one rule only, its location, as the line above the rule
// gives it to cel-lens.showRule ({uri, range}).
type resultsArgs struct {
	URI   string    `json:"uri"`
	Range *lspRange `json:"range"`
}

// resultsAnswer is the answer of exampleResults: the results of each rule.
type resultsAnswer struct {
	KubernetesVersion string        `json:"kubernetesVersion"`
	Rules             []ruleResults `json:"rules"`
}

// ruleResults are the results of a rule, named as the line above it and a
// .cellens entry name it: its document, version and path, its index there,
// its text, and its location as cel-lens.showRule takes it.
//
// An admission policy's expression has no version and path, which are
// left out, as a rule always has them, "" for the root path: its field
// says which it is (validation, matchCondition or auditAnnotation), and
// name a match condition's name or an annotation's key.
type ruleResults struct {
	Document string          `json:"document"`
	Version  *string         `json:"version,omitempty"`
	Path     *string         `json:"path,omitempty"`
	Field    string          `json:"field,omitempty"`
	Name     string          `json:"name,omitempty"`
	Index    int             `json:"index"`
	Rule     string          `json:"rule"`
	Location map[string]any  `json:"location"`
	Results  []ruleOnExample `json:"results"`
}

// ruleOnExample is what a rule does to an example (see examples.Result).
//
// For an admission policy's expression it also gives the binding it was
// admitted under, left out when no binding binds the policy, the param,
// the message of a validation that fails, or an annotation's value, and
// the API server's answer to the example under that binding.
type ruleOnExample struct {
	Example   exampleRef      `json:"example"`
	Binding   string          `json:"binding,omitempty"`
	Param     *exampleRef     `json:"param,omitempty"`
	Result    string          `json:"result"`
	Message   string          `json:"message,omitempty"`
	Refusals  []resultRefusal `json:"refusals,omitempty"`
	Ignored   []string        `json:"ignored,omitempty"`
	Ran       []string        `json:"ran,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Error     string          `json:"error,omitempty"`
	Admission *admissionRef   `json:"admission,omitempty"`
	Expected  bool            `json:"expected"`
	Wrong     string          `json:"wrong,omitempty"`
}

// admissionRef is the API server's answer to an example under a binding
// of an admission policy: the error it refuses it with, its warnings and
// its audit annotations.
type admissionRef struct {
	Denied      string            `json:"denied,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// resultRefusal is a refusal of a rule, as the API server returns it.
type resultRefusal struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// exampleRef names an example: its file, by URI and by path below its
// workspace folder, the line its content starts at, from 1, what it is,
// whether the rules ran on it as a create or an update, and what it
// expects.
type exampleRef struct {
	URI        string `json:"uri"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	Operation  string `json:"operation"`
	Expect     string `json:"expect"`
	Message    string `json:"message,omitempty"`
}

// answerExampleResults answers the request msg with what each rule of the
// CustomResourceDefinitions of the file args names does to each of their
// examples (see examples.Cache.Run), or, with a location, that rule's
// alone. A job finds the examples and runs the rules, off the message loop;
// the request waits for it, a request for the same file and location while
// it runs waits for the same, and one for the same file and another
// location, or cancelling the request, stops it. What the rules did is
// kept in the session's cache until the rule, its schema, the example or
// the Kubernetes version changes, so asking again runs only what changed.
// The file is read as the editor has it, or from disk; it need not be a
// file whose CRDs the session knows. While the setting exampleResults is
// false, the request is refused and nothing runs.
func (ss *session) answerExampleResults(ctx context.Context, msg jsonrpc.Message, raw []json.RawMessage, reply func(any, error) error) error {
	usage := commandExampleResults + " takes one argument: {uri}, a file, for the results of its rules, or {uri, range}, a rule's location, as the line above it gives it, for that rule's"
	if len(raw) != 1 {
		return reply(nil, invalid(usage))
	}
	var a resultsArgs
	if err := json.Unmarshal(raw[0], &a); err != nil || a.URI == "" {
		return reply(nil, invalid(usage))
	}
	if ss.examplesOff {
		return reply(nil, failed(commandExampleResults+": the setting exampleResults is false, so no rule runs on the examples"))
	}
	open := make(map[string][]byte, len(ss.docs))
	for uri, d := range ss.docs {
		open[crdKey(uri)] = d.text // a document's text is replaced, never changed
	}
	limit := ss.fileCap
	if limit <= 0 {
		limit = maxWorkspaceFiles
	}
	key := crdKey(a.URI)
	// The checks of the open files of CRDs and policies are the message
	// loop's, made for their texts and the Kubernetes version, so the job
	// does not check them again.
	checks, err := ss.loopChecks(ctx)
	if err != nil {
		return err
	}
	crdFiles := ss.crds.Files()
	if !slices.Contains(crdFiles, key) {
		crdFiles = append(crdFiles, key)
	}
	// The job takes what the last check of the examples kept, when no check
	// holds it, and gives back what it kept (see settle): the examples of a
	// file that did not change are then not checked again.
	memo := ss.exampleMemo
	if memo == nil {
		memo = &exampleMemo{}
	}
	for _, stale := range ss.exampleStale {
		delete(memo.files, stale)
	}
	ss.exampleMemo = nil
	c := resultsCheck{
		exampleCheck: exampleCheck{
			crdFiles: crdFiles, policyFiles: []string{key}, open: open, folders: outermost(ss.folders), dirFS: ss.dirFS(), limit: limit, kube: ss.kube, memo: memo, loop: &ss.loopBusy,
			results: &ss.results, checks: checks, policies: &ss.policyResults,
		},
		uri: a.URI, key: key, at: a.Range,
	}
	where := ""
	if a.Range != nil {
		where = fmt.Sprint(*a.Range)
	}
	return ss.startJob(ctx, msg, jobSpec{command: commandExampleResults, slot: key, key: where, work: c.run, settle: func(v any) (any, error) {
		done, _ := v.(resultsDone) // what work returns
		if ss.exampleMemo == nil {
			ss.exampleMemo = done.memo
		}
		return done.answer, nil
	}})
}

// resultsDone is what the work of exampleResults returns: the answer, and
// what it kept for the next check of the examples.
type resultsDone struct {
	answer resultsAnswer
	memo   *exampleMemo
}

// resultsCheck is the work of exampleResults: what it needs of the session,
// made on the message loop, so it runs off it.
type resultsCheck struct {
	exampleCheck
	// uri and key name the file of the rules; at is the rule's location,
	// nil for every rule of the file.
	uri, key string
	at       *lspRange
}

// run reads the CRDs of the file, finds their examples, runs the rules on
// them and returns the answer.
func (c *resultsCheck) run(ctx context.Context) (any, error) {
	text, ok := c.open[c.key]
	if !ok {
		path, isFile := diskPath(c.uri)
		if !isFile {
			return nil, failed(fmt.Sprintf("%s: %s is neither open nor a file", commandExampleResults, c.uri))
		}
		var err error
		if text, err = readUnder(c.folders, path); err != nil {
			return nil, failed(fmt.Sprintf("%s: read %s: %v", commandExampleResults, path, err))
		}
	}
	var found []fileFound
	crds, err := c.examples(ctx, func(f walked, text []byte, objs []*examples.Object) {
		found = append(found, fileFound{f: f, text: text, objs: objs})
	})
	if err != nil {
		return nil, err
	}
	ev, err := c.evaluate(ctx, crds, found)
	if err != nil {
		return nil, err
	}
	rules := ev.rules[c.key]
	if _, ran := ev.rules[c.key]; !ran {
		// A file of CRDs without examples has its rules, with no results.
		for _, crd := range crds {
			if crd.File != c.key {
				continue
			}
			found, err := crd.Rules(ctx)
			if err != nil {
				return nil, err
			}
			rules = append(rules, found...)
		}
	}
	answer := c.answer(text, rules, ev.crds[c.key], ev.rels)
	c.answerPolicies(&answer, text, ev.policies[c.key], ev.rels)
	return resultsDone{answer: answer, memo: c.memo}, nil
}

// answer is the answer for results, of rules, the rules of text, those of
// the location asked for alone; rels are the paths of the examples' files
// below their folders, by key.
func (c *resultsCheck) answer(text []byte, rules []*examples.Rule, results []examples.Result, rels map[string]string) resultsAnswer {
	lines := splitLines(text)
	out := resultsAnswer{KubernetesVersion: c.kube.String(), Rules: []ruleResults{}}
	byRule := map[*examples.Rule]int{}
	for _, r := range rules {
		at := toRange(lines, r.Start, r.End)
		if c.at != nil && !within(c.at.Start, at) && (r.MessageStart == (extract.Position{}) || !within(c.at.Start, toRange(lines, r.MessageStart, r.MessageEnd))) {
			continue
		}
		byRule[r] = len(out.Rules)
		out.Rules = append(out.Rules, ruleResults{
			Document: "CustomResourceDefinition " + r.CRD.Name, Version: &r.Version, Path: &r.Path, Index: r.Index, Rule: r.Text,
			Location: map[string]any{"uri": c.uri, "range": at}, Results: []ruleOnExample{},
		})
	}
	for _, res := range results {
		i, ok := byRule[res.Rule]
		if !ok {
			continue
		}
		o := res.Example
		ref := exampleRef{
			URI: pathURI(filepath.FromSlash(o.File)), File: rels[o.File], Line: o.Line,
			APIVersion: o.APIVersion, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name,
			Operation: "create", Expect: o.Expect.String(), Message: o.Message,
		}
		if res.Update {
			ref.Operation = "update"
		}
		one := ruleOnExample{Example: ref, Result: res.Verdict.String(), Ignored: res.Ignored, Ran: res.Ran, Reason: res.Reason, Error: res.Error, Expected: res.Expected}
		for _, f := range res.Refusals {
			one.Refusals = append(one.Refusals, resultRefusal{Path: f.Path, Message: f.Message})
		}
		switch {
		case res.Expected:
		case res.OnExample:
			one.Wrong = "example"
		default:
			one.Wrong = "rule"
		}
		out.Rules[i].Results = append(out.Rules[i].Results, one)
	}
	return out
}

// within reports whether p is in r, its ends included.
func within(p position, r lspRange) bool {
	after := func(a, b position) bool { return a.Line > b.Line || a.Line == b.Line && a.Character >= b.Character }
	return after(p, r.Start) && after(r.End, p)
}

// answerPolicies adds to out the results of runs, the admission policies
// of text, those of the location asked for alone; rels are the paths of
// the files below their folders, by key.
func (c *resultsCheck) answerPolicies(out *resultsAnswer, text []byte, runs []policyRan, rels map[string]string) {
	lines := splitLines(text)
	ref := func(o *examples.Object, update bool) exampleRef {
		r := exampleRef{
			URI: pathURI(filepath.FromSlash(o.File)), File: rels[o.File], Line: o.Line,
			APIVersion: o.APIVersion, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name,
			Operation: "create", Expect: o.Expect.String(), Message: o.Message,
		}
		if update {
			r.Operation = "update"
		}
		return r
	}
	for _, pr := range runs {
		byRule := map[*examples.PolicyRule]int{}
		for _, r := range pr.rules {
			at := toRange(lines, r.Start, r.End)
			if c.at != nil && !within(c.at.Start, at) && (r.MessageStart == (extract.Position{}) || !within(c.at.Start, toRange(lines, r.MessageStart, r.MessageEnd))) {
				continue
			}
			byRule[r] = len(out.Rules)
			out.Rules = append(out.Rules, ruleResults{
				Document: "ValidatingAdmissionPolicy " + pr.policy.Name, Field: policyFieldName(r.Field), Name: r.Name, Index: r.Index, Rule: r.Text,
				Location: map[string]any{"uri": c.uri, "range": at}, Results: []ruleOnExample{},
			})
		}
		admissions := map[*examples.Object]map[*examples.Binding]examples.Admission{}
		for _, a := range pr.run.Admissions {
			if admissions[a.Example] == nil {
				admissions[a.Example] = map[*examples.Binding]examples.Admission{}
			}
			admissions[a.Example][a.Binding] = a
		}
		for _, res := range pr.run.Results {
			i, ok := byRule[res.Rule]
			if !ok {
				continue
			}
			one := ruleOnExample{Example: ref(res.Example, res.Update), Result: res.Verdict.String(), Expected: res.Expected}
			if res.Binding != nil {
				one.Binding = res.Binding.Name
			}
			if res.Param != nil {
				p := ref(res.Param, false)
				p.Operation, p.Expect, p.Message = "", "", ""
				one.Param = &p
			}
			switch res.Verdict {
			case examples.PolicyNotRun:
				one.Reason = res.Message
			case examples.PolicyError:
				one.Error = res.Message
			case examples.PolicyAllowed, examples.PolicyRefused, examples.PolicyWarned, examples.PolicyAudited, examples.PolicyAnnotated, examples.PolicyExcluded:
				one.Message = res.Message
			}
			if a, ok := admissions[res.Example][res.Binding]; ok && a.NotRun == "" {
				one.Admission = &admissionRef{Denied: a.Denied, Warnings: a.Warnings, Annotations: a.Annotations}
			}
			switch {
			case res.Expected:
			case res.OnExample:
				one.Wrong = "example"
			default:
				one.Wrong = "rule"
			}
			out.Rules[i].Results = append(out.Rules[i].Results, one)
		}
	}
}

// policyFieldName names f as the answer of exampleResults does.
func policyFieldName(f examples.PolicyField) string {
	switch f {
	case examples.FieldMatchCondition:
		return "matchCondition"
	case examples.FieldAuditAnnotation:
		return "auditAnnotation"
	case examples.FieldValidation:
	}
	return "validation"
}
