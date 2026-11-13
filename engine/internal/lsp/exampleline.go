package lsp

import (
	"bytes"
	"context"
	"maps"
	"path/filepath"
	"slices"
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

// runRules runs the rules of crds, the CRDs the check read, on found, their
// examples, file of CRDs by file, with c.results, and returns what they did.
// A file of CRDs no example of which was found is skipped. The error is
// ctx's.
func (c *exampleCheck) runRules(ctx context.Context, crds []*examples.CRD, found []fileFound) (ruleFindings, error) {
	out := ruleFindings{warnings: map[string][]check.Diagnostic{}, crds: map[string]crdFindings{}}
	rels := map[string]string{}
	byFile := map[string][]*examples.Object{}
	for _, ff := range found {
		rels[ff.f.key] = ff.f.rel
		for _, o := range ff.objs {
			byFile[o.CRD.File] = append(byFile[o.CRD.File], o)
		}
	}
	var files []string
	for _, crd := range crds {
		if !slices.Contains(files, crd.File) && len(byFile[crd.File]) > 0 {
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
				return ruleFindings{}, err
			}
			rules = append(rules, found...)
		}
		var results []examples.Result
		if len(rules) > 0 {
			var err error
			if results, err = c.results.Run(ctx, file, rules, byFile[file], examples.Options{Pause: c.wait, Checked: c.checks[file]}); err != nil {
				return ruleFindings{}, err
			}
		}
		cf := crdFindings{text: c.memo.crds[file].text, rules: map[ruleID][]inline.Example{}}
		type at struct {
			file string
			line int
		}
		ran := map[at]bool{}
		onExample := map[at]bool{}
		for _, r := range results {
			o := r.Example
			ie := exampleOf(r, rels[o.File])
			id := ruleID{r.Rule.Document, r.Rule.FieldPath, r.Rule.Text}
			cf.rules[id] = append(cf.rules[id], ie)
			ran[at{o.File, o.Line}] = true
			if r.OnExample {
				onExample[at{o.File, o.Line}] = true
			}
			if r.Expected || r.OnExample {
				continue
			}
			cf.warnings = append(cf.warnings, warning(r.Rule.Start, r.Rule.End, inline.RuleWarning(ie)))
			start, end := o.Head()
			out.warnings[o.File] = append(out.warnings[o.File], warning(start, end, inline.ExampleWarning(r.Rule.Text, ie)))
		}
		for _, o := range byFile[file] {
			k := at{o.File, o.Line}
			if onExample[k] || !ran[k] && o.Expect == examples.Refused && !o.RefusedBySchema() {
				start, end := o.Head()
				out.warnings[o.File] = append(out.warnings[o.File], warning(start, end, inline.NothingRefuses(o.Message)))
			}
		}
		out.crds[file] = cf
	}
	return out, nil
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
	if ss.examplesOff || !ok || f.rules == nil || f.kube != ss.kube || e.Kind != extract.KindRule {
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
		if !bytes.Contains(d.text, []byte(crdWord)) {
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
