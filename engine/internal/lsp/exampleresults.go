package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/JairJosafath/cel-lens/engine/internal/examples"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
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
type ruleResults struct {
	Document string          `json:"document"`
	Version  string          `json:"version"`
	Path     string          `json:"path"`
	Index    int             `json:"index"`
	Rule     string          `json:"rule"`
	Location map[string]any  `json:"location"`
	Results  []ruleOnExample `json:"results"`
}

// ruleOnExample is what a rule does to an example (see examples.Result).
type ruleOnExample struct {
	Example  exampleRef      `json:"example"`
	Result   string          `json:"result"`
	Refusals []resultRefusal `json:"refusals,omitempty"`
	Ignored  []string        `json:"ignored,omitempty"`
	Ran      []string        `json:"ran,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Error    string          `json:"error,omitempty"`
	Expected bool            `json:"expected"`
	Wrong    string          `json:"wrong,omitempty"`
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
	// The check of an open CRD is the message loop's, made for its text
	// and the Kubernetes version, so the job does not check it again.
	var checked []check.Diagnostic
	for _, uri := range slices.Sorted(maps.Keys(ss.docs)) {
		if crdKey(uri) != key {
			continue
		}
		diags, err := ss.diagnostics(ctx, uri, ss.docs[uri])
		if err != nil {
			return err
		}
		checked = append([]check.Diagnostic{}, diags...)
		break
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
		exampleCheck: exampleCheck{crdFiles: crdFiles, open: open, folders: outermost(ss.folders), dirFS: ss.dirFS(), limit: limit, kube: ss.kube, memo: memo, loop: &ss.loopBusy},
		uri:          a.URI, key: key, at: a.Range, cache: &ss.results, checked: checked,
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
	cache    *examples.Cache
	// checked is the message loop's check of the file when the editor has
	// it open, nil when it does not.
	checked []check.Diagnostic
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
	rels := map[string]string{}
	var objs []*examples.Object
	crds, err := c.examples(ctx, func(f walked, _ []byte, found []*examples.Object) {
		for _, o := range found {
			if o.CRD.File == c.key {
				rels[f.key] = f.rel
				objs = append(objs, o)
			}
		}
	})
	if err != nil {
		return nil, err
	}
	var rules []*examples.Rule
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
	results, err := c.cache.Run(ctx, c.key, rules, objs, examples.Options{Pause: c.wait, Checked: c.checked})
	if err != nil {
		return nil, err
	}
	return resultsDone{answer: c.answer(text, rules, results, rels), memo: c.memo}, nil
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
			Document: "CustomResourceDefinition " + r.CRD.Name, Version: r.Version, Path: r.Path, Index: r.Index, Rule: r.Text,
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
