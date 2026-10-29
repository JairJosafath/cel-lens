package assist

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// allDynReasons lists every dynReason; TestDynReport fails on a hover reason not in it.
var allDynReasons = []dynReason{
	reasonWildcard, reasonNoRule, reasonUnknownKind, reasonParamKind, reasonOpenSchema, reasonNoType,
	reasonVariable, reasonVarResult, reasonVarInput, reasonVarUncompiled, reasonVarKinds,
}

// dynTally is what the dyn report counts for a set of policies.
type dynTally struct {
	policies    int
	expressions int
	dyn         int
	// reasons counts the expressions that meet each reason; an expression with
	// several reasons is counted once under each.
	reasons map[dynReason]int
}

// dynReasonsOf returns the reasons hover gives anywhere in the expression e of doc.
func dynReasonsOf(t *testing.T, a *Assistant, doc *Document, e extract.Expression) []dynReason {
	t.Helper()
	found := map[dynReason]bool{}
	seen := map[[2]extract.Position]bool{}
	for line := e.Start.Line; line <= e.End.Line && line <= len(doc.lines); line++ {
		text := []rune(doc.lines[line-1])
		for col := 1; col <= len(text); col++ {
			if !isWordStart(text, col-1) || isCall(text, col-1) {
				continue
			}
			h, ok := a.Hover(doc, extract.Position{Line: line, Column: col})
			if !ok || seen[[2]extract.Position{h.Start, h.End}] {
				continue
			}
			seen[[2]extract.Position{h.Start, h.End}] = true
			_, sentence, ok := strings.Cut(h.Markdown, "\n\ndyn: ")
			if !ok {
				continue
			}
			i := slices.IndexFunc(allDynReasons, func(r dynReason) bool { return r.text() == sentence })
			if i < 0 {
				t.Fatalf("hover at %d:%d gives the dyn reason %q, which is in no dynReason", line, col, sentence)
			}
			found[allDynReasons[i]] = true
		}
	}
	var list []dynReason
	for _, r := range allDynReasons {
		if found[r] {
			list = append(list, r)
		}
	}
	return list
}

// isWordStart reports whether text[i] begins a name.
func isWordStart(text []rune, i int) bool {
	word := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	return word(text[i]) && (i == 0 || !word(text[i-1]))
}

// isCall reports whether the name at text[i] is followed by an opening
// parenthesis: a function, which has no dyn reason of its own.
func isCall(text []rune, i int) bool {
	for i < len(text) && (text[i] == '_' || unicode.IsLetter(text[i]) || unicode.IsDigit(text[i])) {
		i++
	}
	return i < len(text) && text[i] == '('
}

// tallyDir adds the expressions of every policy in dir to tally, checking the
// files in parallel.
func tallyDir(t *testing.T, a *Assistant, dir string, tally *dynTally) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	results := make([]dynTally, len(files))
	t.Run(filepath.Base(dir), func(t *testing.T) {
		for i, file := range files {
			t.Run(filepath.Base(file), func(t *testing.T) {
				t.Parallel()
				results[i] = tallyFile(t, a, file)
			})
		}
	})
	for _, r := range results {
		tally.policies += r.policies
		tally.expressions += r.expressions
		tally.dyn += r.dyn
		for reason, n := range r.reasons {
			tally.reasons[reason] += n
		}
	}
}

// tallyFile counts the expressions of the policy in file and checks its source note.
func tallyFile(t *testing.T, a *Assistant, file string) dynTally {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	note, err := os.ReadFile(file + ".source")
	if err != nil {
		t.Errorf("%s has no source note: %v", file, err)
	} else if !strings.Contains(string(note), "Source: https://") || !strings.Contains(string(note), "Licence: ") {
		t.Errorf("%s.source names no source URL and licence", file)
	}
	doc := Parse(context.Background(), file, data)
	tally := dynTally{policies: 1, reasons: map[dynReason]int{}}
	for _, e := range doc.exprs {
		tally.expressions++
		reasons := dynReasonsOf(t, a, doc, e)
		if len(reasons) > 0 {
			tally.dyn++
		}
		for _, r := range reasons {
			tally.reasons[r]++
		}
	}
	return tally
}

// writeDynReport writes the report of the tallies to w.
func writeDynReport(w io.Writer, groups []string, tallies []*dynTally) {
	var all dynTally
	all.reasons = map[dynReason]int{}
	for i, tally := range tallies {
		all.policies += tally.policies
		all.expressions += tally.expressions
		all.dyn += tally.dyn
		for r, n := range tally.reasons {
			all.reasons[r] += n
		}
		fmt.Fprintf(w, "%-22s %3d policies  %4d expressions  %4d meet dyn\n", groups[i], tally.policies, tally.expressions, tally.dyn)
	}
	fmt.Fprintf(w, "%-22s %3d policies  %4d expressions  %4d meet dyn\n", "total", all.policies, all.expressions, all.dyn)
	fmt.Fprintln(w, "expressions per dyn reason (one expression can have several):")
	for _, r := range allDynReasons {
		if n := all.reasons[r]; n > 0 {
			fmt.Fprintf(w, "  %-26s %4d  %s\n", r, n, r.text())
		}
	}
}

// TestDynReport checks every expression of the real policies in testdata/dyn
// and prints how many meet a dyn value, and why. make check shows the report.
func TestDynReport(t *testing.T) {
	a, err := New(kubeversion.Default(), WithFeatures(feature.Of(feature.Known()...)))
	if err != nil {
		t.Fatal(err)
	}
	groups := []string{"Kyverno policy library", "Kubernetes docs"}
	var tallies []*dynTally
	for _, g := range []string{"kyverno", "kubernetes"} {
		tally := &dynTally{reasons: map[dynReason]int{}}
		tallyDir(t, a, filepath.Join("..", "..", "testdata", "dyn", g), tally)
		tallies = append(tallies, tally)
	}
	if tallies[0].policies < 20 || tallies[1].policies < 5 {
		t.Errorf("testdata/dyn has %d Kyverno and %d Kubernetes policies, want at least 20 and 5", tallies[0].policies, tallies[1].policies)
	}
	for i, tally := range tallies {
		if tally.expressions == 0 {
			t.Errorf("%s: no expression found", groups[i])
		}
		counted := 0
		for _, n := range tally.reasons {
			counted += n
		}
		if counted < tally.dyn {
			t.Errorf("%s: %d expressions meet dyn but the reasons add up to %d", groups[i], tally.dyn, counted)
		}
	}
	fmt.Fprintln(os.Stdout, "dyn report: expressions of real policies that meet dyn")
	writeDynReport(os.Stdout, groups, tallies)
}
