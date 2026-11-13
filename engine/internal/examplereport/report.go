// Package examplereport writes what the rules of CustomResourceDefinitions
// and admission policies do to their examples, for the command line and CI,
// as text or JSON, and says whether a run fails.
//
// It only formats and counts: running the rules is the job of package lsp,
// which gives the same results to the editor.
package examplereport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/JairJosafath/cel-lens/engine/internal/lsp"
)

// Format is how Write writes a report.
type Format string

// The formats.
const (
	// Text writes what a person reads in a CI log.
	Text Format = "text"
	// JSON writes the report as one object, as cel-lens.exampleResults
	// answers, with the problems of the examples beside its rules.
	JSON Format = "json"
)

// ErrFormat is returned, wrapped, by ParseFormat for a format it does not know.
var ErrFormat = errors.New("unknown format")

// ParseFormat returns the Format s names, and an error wrapping ErrFormat
// for any other.
func ParseFormat(s string) (Format, error) {
	switch f := Format(s); f {
	case Text, JSON:
		return f, nil
	}
	return "", fmt.Errorf("format %q: %w (want text or json)", s, ErrFormat)
}

// FailOn is what makes a run fail.
type FailOn string

// What fails a run.
const (
	// FailWrong fails a run with a wrong verdict, or a problem of an example.
	FailWrong FailOn = "wrong"
	// FailError fails it also with a rule that stopped with an error.
	FailError FailOn = "error"
	// FailNone fails it never.
	FailNone FailOn = "none"
)

// ErrFailOn is returned, wrapped, by ParseFailOn for a value it does not know.
var ErrFailOn = errors.New("unknown value")

// ParseFailOn returns the FailOn s names, and an error wrapping ErrFailOn for
// any other.
func ParseFailOn(s string) (FailOn, error) {
	switch f := FailOn(s); f {
	case FailWrong, FailError, FailNone:
		return f, nil
	}
	return "", fmt.Errorf("%q: %w (want wrong, error or none)", s, ErrFailOn)
}

// Counts is what a report holds, by kind. Each result of a rule on an example
// is in one kind, as the line above a rule counts it: wrong, when the verdict
// is not what the example expects and the rule is to blame, the example
// expecting to be allowed, an error included, as a rule that stops with an
// error refuses the example; else an error, which only a result that the
// example expects can be; else not run; else as expected. An example that
// expects to be refused, and that nothing refuses so, is not counted for each
// rule that allows it: it is one of the problems of the examples, which are
// wrong too.
type Counts struct {
	// Rules are the rules, and Examples the examples any rule has a result
	// for.
	Rules, Examples int
	Wrong, Errors   int
	NotRun          int
	// FilesLeftOut are the files the run did not read for the limits.
	FilesLeftOut int
}

type kind int

const (
	asExpected kind = iota
	// wrong is a verdict that is not what its example expects, for which the
	// rule is to blame.
	wrong
	// onExample is a verdict that is not what its example expects, which
	// expects to be refused, for which the example is to blame: it is a
	// problem of the example, once.
	onExample
	failed
	notRun
)

// kindOf is the kind of the result r.
func kindOf(r lsp.RuleOnExample) kind {
	switch {
	case r.Result == "not run":
		return notRun
	case r.Wrong == "example":
		return onExample
	case r.Wrong != "":
		return wrong
	case r.Result == "error":
		return failed
	}
	return asExpected
}

// Count counts what r holds.
func Count(r *lsp.ExamplesReport) Counts {
	c := Counts{Rules: len(r.Rules), Wrong: len(r.Problems), FilesLeftOut: r.FilesLeftOut}
	seen := map[string]bool{}
	for _, rule := range r.Rules {
		for _, res := range rule.Results {
			seen[res.Example.URI+":"+strconv.Itoa(res.Example.Line)] = true
			switch kindOf(res) {
			case wrong:
				c.Wrong++
			case failed:
				c.Errors++
			case notRun:
				c.NotRun++
			case asExpected, onExample:
			}
		}
	}
	c.Examples = len(seen)
	return c
}

// Failed reports whether r fails a run under on.
func Failed(r *lsp.ExamplesReport, on FailOn) bool {
	c := Count(r)
	switch on {
	case FailWrong:
		return c.Wrong > 0
	case FailError:
		return c.Wrong+c.Errors > 0
	case FailNone:
	}
	return false
}

// Options are how Write writes a report in text.
type Options struct {
	// Verbose lists every result, where the text lists only the wrong ones
	// and the errors.
	Verbose bool
	// Dir is the folder the paths in the text are shown relative to, as long
	// as they are below it; "" shows them as they are.
	Dir string
}

// Write writes r to w in format f.
func Write(w io.Writer, f Format, r *lsp.ExamplesReport, o Options) error {
	var err error
	switch f {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		err = enc.Encode(r)
	case Text:
		err = writeText(w, r, o)
	default:
		return fmt.Errorf("format %q: %w (want text or json)", f, ErrFormat)
	}
	if err != nil {
		return fmt.Errorf("write results: %w", err)
	}
	return nil
}

// maxRule is how many characters of a rule's text the text format shows.
const maxRule = 60

// writeText writes r as text: the problems of the examples, then each rule
// with a result to show, the wrong ones first, and the summary line.
func writeText(w io.Writer, r *lsp.ExamplesReport, o Options) error {
	var b strings.Builder
	rel := func(path string) string {
		if o.Dir == "" {
			return path
		}
		if p, err := filepath.Rel(o.Dir, path); err == nil && filepath.IsLocal(p) {
			return p
		}
		return path
	}
	for _, p := range r.Problems {
		fmt.Fprintf(&b, "%s:%d:%d: %s: %s\n", rel(lsp.FilePath(p.URI)), p.Line, p.Column, p.Severity, oneLine(p.Message))
	}
	// rank is where a rule goes in the list: with its worst result.
	rank := func(rule lsp.RuleResults) int {
		best := order(asExpected)
		for _, res := range rule.Results {
			best = min(best, order(kindOf(res)))
		}
		return best
	}
	rules := slices.Clone(r.Rules)
	slices.SortStableFunc(rules, func(a, b lsp.RuleResults) int { return rank(a) - rank(b) })
	for _, rule := range rules {
		results := slices.Clone(rule.Results)
		if !o.Verbose {
			results = slices.DeleteFunc(results, func(res lsp.RuleOnExample) bool {
				k := kindOf(res)
				return k != wrong && k != failed
			})
			if len(results) == 0 {
				continue
			}
		}
		slices.SortStableFunc(results, func(a, b lsp.RuleOnExample) int { return order(kindOf(a)) - order(kindOf(b)) })
		path, line := rule.Start()
		fmt.Fprintf(&b, "%s:%d: %s  (%s)\n", rel(path), line, shorten(rule.Rule), where(rule))
		if len(results) == 0 {
			b.WriteString("  no examples\n")
		}
		for _, res := range results {
			fmt.Fprintf(&b, "  %-5s %s:%d: %s\n", mark(kindOf(res)), rel(lsp.FilePath(res.Example.URI)), res.Example.Line, describe(res, rel))
		}
	}
	b.WriteString(summary(Count(r)) + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// order is where a kind goes in the list: the wrong ones first, then the
// errors, then the others.
func order(k kind) int {
	switch k {
	case wrong:
		return 0
	case failed, onExample:
		return 1
	case asExpected, notRun:
	}
	return 2
}

// mark is the word that marks a result of the kind k.
func mark(k kind) string {
	switch k {
	case wrong, onExample:
		return "wrong"
	case failed:
		return "error"
	case notRun:
		return "skip"
	case asExpected:
	}
	return "ok"
}

// where says what the rule is of: its document, and where in it.
func where(r lsp.RuleResults) string {
	s := r.Document
	if r.Version != nil && *r.Version != "" {
		s += " " + *r.Version
	}
	if r.Path != nil && *r.Path != "" {
		s += " " + *r.Path
	}
	switch {
	case r.Field != "" && r.Name != "":
		s += ", " + r.Field + " " + strconv.Quote(r.Name)
	case r.Field != "":
		s += ", " + r.Field
	}
	return s
}

// describe is what a rule does to an example, as the text lists it: the
// verdict, how it was run, what the example expects when that is not what
// happened, and the API server's message.
func describe(r lsp.RuleOnExample, rel func(string) string) string {
	var s string
	if r.Result != "not run" {
		s = r.Result
	}
	if r.Example.Operation == "update" {
		s += " as an update"
	}
	if r.Binding != "" {
		s += " under binding " + strconv.Quote(r.Binding)
	}
	if r.Param != nil {
		s += " with the param " + rel(lsp.FilePath(r.Param.URI)) + ":" + strconv.Itoa(r.Param.Line)
	}
	if r.Wrong != "" {
		s += ", expected to be " + r.Example.Expect
		if r.Example.Expect == "refused" && r.Example.Message != "" {
			s += " with " + strconv.Quote(r.Example.Message)
		}
	}
	msg := message(r)
	switch {
	case r.Result == "not run":
		s = strings.TrimSpace("not run" + s)
		if msg != "" {
			s += ": " + msg
		}
	case msg != "":
		s += ": " + msg
	}
	return strings.TrimPrefix(s, " ")
}

// message is the API server's message of r: its refusals, or its error, or why
// the rule did not run.
func message(r lsp.RuleOnExample) string {
	var parts []string
	for _, f := range r.Refusals {
		parts = append(parts, f.Message)
	}
	switch {
	case len(parts) > 0:
		return oneLine(strings.Join(parts, "; "))
	case r.Error != "":
		return oneLine(r.Error)
	case r.Reason != "":
		return oneLine(r.Reason)
	case r.Message != "":
		return oneLine(r.Message)
	case r.Admission != nil && r.Admission.Denied != "":
		return oneLine(r.Admission.Denied)
	}
	return ""
}

// summary is the last line of the text: "12 rules, 40 examples: 2 wrong, 1
// error, 3 not run", with the errors and the rules that did not run left out
// when there are none, and, when the limits left files out, " · 1 file left out
// at the limits".
func summary(c Counts) string {
	s := strconv.Itoa(c.Rules) + plural(c.Rules, " rule", " rules") + ", " + strconv.Itoa(c.Examples) + plural(c.Examples, " example", " examples") + ": " + strconv.Itoa(c.Wrong) + " wrong"
	if c.Errors > 0 {
		s += ", " + strconv.Itoa(c.Errors) + plural(c.Errors, " error", " errors")
	}
	if c.NotRun > 0 {
		s += ", " + strconv.Itoa(c.NotRun) + " not run"
	}
	if c.FilesLeftOut > 0 {
		s += " · " + strconv.Itoa(c.FilesLeftOut) + plural(c.FilesLeftOut, " file", " files") + " left out at the limits"
	}
	return s
}

// plural is one when n is 1, else many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// shorten is the rule's text on one line, at most maxRule characters.
func shorten(rule string) string {
	s := oneLine(rule)
	if utf8.RuneCountInString(s) <= maxRule {
		return s
	}
	return string([]rune(s)[:maxRule-1]) + "…"
}

// oneLine joins the lines of s, so that a message takes one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
