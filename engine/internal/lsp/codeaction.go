package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/hover"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// quickFix is the code action kind of every action the server offers.
const quickFix = "quickfix"

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// namePattern matches the diagnostics of cel-go about a name it does not know:
// an undefined field or an undeclared function, and captures the name.
var namePattern = regexp.MustCompile(`^(?:undefined field|undeclared reference to) '([^']+)'`)

// codeAction answers textDocument/codeAction: the quick fixes of the problems
// the document has in the requested range, and of the expressions there near
// their cost budget. A fix is offered for a misspelled field, function or
// macro when one name valid there is clearly the one meant, for a cost
// problem when limits would solve it, and for an expression at half its
// budget or more when limits would bring it under half (see actionsAt). The
// problems are the ones the last check found in the document as it is now,
// not the ones the client sends; the document is checked again only if no
// check of this version completed.
func (ss *session) codeAction(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Range   lspRange `json:"range"`
		Context struct {
			Only        []string `json:"only"`
			TriggerKind int      `json:"triggerKind"`
		} `json:"context"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, badParams("textDocument/codeAction", err)
	}
	doc, ok := ss.docs[p.TextDocument.URI]
	if !ok || !wantsQuickFix(p.Context.Only) {
		return []any{}, nil
	}
	found, err := ss.actionsAt(ctx, p.TextDocument.URI, doc, p.Range, p.Context.TriggerKind != triggerAutomatic)
	if err != nil {
		return nil, err
	}
	lines := doc.runes()
	actions := []any{}
	for _, a := range found {
		action := map[string]any{
			"title":       a.title,
			"kind":        quickFix,
			"isPreferred": a.preferred,
			"edit":        map[string]any{"changes": map[string]any{p.TextDocument.URI: a.edits}},
		}
		if a.diag != nil {
			action["diagnostics"] = []any{diagnostic(lines, *a.diag)}
		}
		actions = append(actions, action)
	}
	return actions, nil
}

// triggerAutomatic is the CodeActionTriggerKind of a request the editor makes
// by itself, as the cursor moves, to show a light bulb; 1, Invoked, is one the
// person asked for.
const triggerAutomatic = 2

// action is a quick fix of a range of a document: that of the problem diag,
// or of an expression near its cost budget when diag is nil.
type action struct {
	fix
	diag *check.Diagnostic
}

// actionsAt lists the quick fixes of the range r of doc at uri: those of the
// problems the range touches, then those of the expressions it touches whose
// cost is near their budget, sized to bring them under half of it. With
// work, the costs are estimated and the fixes sized when they are not known,
// and kept with the document's version; without, as for a light bulb the
// editor asks for by itself while the person types, only those known are
// offered, so typing pays for no estimate.
func (ss *session) actionsAt(ctx context.Context, uri string, doc *document, r lspRange, work bool) ([]action, error) {
	diags, err := ss.diagnostics(ctx, uri, doc)
	if err != nil {
		return nil, err
	}
	lines := doc.runes()
	from, to := fromPosition(lines, r.Start), fromPosition(lines, r.End)
	var out []action
	for i, d := range diags {
		if before(to, d.Start) || before(d.End, from) {
			continue
		}
		for _, f := range ss.fixesFor(ctx, uri, doc, lines, d) {
			out = append(out, action{f, &diags[i]})
		}
	}
	es := ss.knownCosts(doc)
	if es == nil && work {
		if es, err = ss.estimateNow(ctx, uri, doc); err != nil {
			return nil, err
		}
	}
	if es == nil {
		return out, nil
	}
	for _, c := range es.costs {
		if before(to, c.Start) || before(c.End, from) || !isNear(c) {
			continue
		}
		if work {
			if err := ss.sizeNow(ctx, uri, doc, es, c); err != nil {
				return nil, err
			}
		}
		if f, ok := es.near[c.Start]; ok {
			for _, lf := range limitFixes(lines, f.fixes, f.together) {
				out = append(out, action{fix: lf})
			}
		}
	}
	return out, nil
}

func before(a, b extract.Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
}

// wantsQuickFix reports whether a client that asks only for the kinds only
// wants quick fixes; one that names none wants everything.
func wantsQuickFix(only []string) bool {
	if len(only) == 0 {
		return true
	}
	for _, k := range only {
		if k == quickFix || k == "" {
			return true
		}
	}
	return false
}

// fix is one code action: what it is called, as a title and in Markdown
// for a hover (words, with the text it takes from the file in code spans),
// and the edits it makes.
type fix struct {
	title, words string
	edits        []textEdit
	preferred    bool
}

// fixesFor lists the fixes of one problem.
func (ss *session) fixesFor(ctx context.Context, uri string, doc *document, lines [][]rune, d check.Diagnostic) []fix {
	if len(d.Fixes) > 0 {
		return limitFixes(lines, d.Fixes, d.FixesTogether)
	}
	m := namePattern.FindStringSubmatch(d.Message)
	if m == nil || d.Start.Line != d.End.Line || d.Start.Line > len(lines) || d.End.Column-1 > len(lines[d.Start.Line-1]) {
		return nil
	}
	if string(lines[d.Start.Line-1][d.Start.Column-1:d.End.Column-1]) != m[1] {
		return nil
	}
	name, ok := ss.assist.Suggest(doc.parse(ctx, uri), d.Start, d.End)
	if !ok {
		return nil
	}
	return []fix{{
		title:     "Change to `" + name + "`",
		words:     "Change to " + hover.Code(name),
		edits:     []textEdit{{toRange(lines, d.Start, d.End), name}},
		preferred: true,
	}}
}

// limitFixes lists the fixes of a cost that has limits to add, fixes: one
// each, or one for all when they only help together.
func limitFixes(lines [][]rune, fixes []check.Fix, together bool) []fix {
	var all []fix
	for _, f := range fixes {
		if f.Value == 0 || len(f.Edits) == 0 {
			continue
		}
		edits := make([]textEdit, len(f.Edits))
		for i, e := range f.Edits {
			at := toPosition(lines, e.At)
			edits[i] = textEdit{lspRange{at, at}, e.Text}
		}
		limit := fmt.Sprintf("%s: %d", f.Limit, f.Value)
		all = append(all, fix{
			title: fmt.Sprintf("Add %#q to %#q", limit, f.FieldPath),
			words: "Add " + hover.Code(limit) + " to " + hover.Code(f.FieldPath),
			edits: edits,
		})
	}
	if together && len(all) != len(fixes) {
		return nil // a limit that cannot be written leaves the rest half a fix
	}
	if !together || len(all) < 2 {
		return all
	}
	var titles, words []string
	var edits []textEdit
	for _, f := range all {
		titles = append(titles, strings.TrimPrefix(f.title, "Add "))
		words = append(words, strings.TrimPrefix(f.words, "Add "))
		edits = append(edits, f.edits...)
	}
	return []fix{{title: "Add " + strings.Join(titles, " and "), words: "Add " + strings.Join(words, " and "), edits: edits}}
}
