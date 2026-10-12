package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

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
// the document has in the requested range. A fix is offered for a misspelled
// field, function or macro when one name valid there is clearly the one meant,
// and for a cost problem when limits would solve it. The problems are the ones
// the last check found in the document as it is now, not the ones the client
// sends; the document is checked again only if no check of this version
// completed.
func (ss *session) codeAction(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Range   lspRange `json:"range"`
		Context struct {
			Only []string `json:"only"`
		} `json:"context"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, badParams("textDocument/codeAction", err)
	}
	doc, ok := ss.docs[p.TextDocument.URI]
	if !ok || !wantsQuickFix(p.Context.Only) {
		return []any{}, nil
	}
	diags, err := ss.diagnostics(ctx, p.TextDocument.URI, doc)
	if err != nil {
		return nil, err
	}
	lines := doc.runes()
	from, to := fromPosition(lines, p.Range.Start), fromPosition(lines, p.Range.End)
	actions := []any{}
	for _, d := range diags {
		if before(to, d.Start) || before(d.End, from) {
			continue
		}
		for _, f := range ss.fixesFor(ctx, p.TextDocument.URI, doc, lines, d) {
			actions = append(actions, map[string]any{
				"title":       f.title,
				"kind":        quickFix,
				"diagnostics": []any{diagnostic(lines, d)},
				"isPreferred": f.preferred,
				"edit":        map[string]any{"changes": map[string]any{p.TextDocument.URI: f.edits}},
			})
		}
	}
	return actions, nil
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

// fix is one code action: what it is called and the edits it makes.
type fix struct {
	title     string
	edits     []textEdit
	preferred bool
}

// fixesFor lists the fixes of one problem.
func (ss *session) fixesFor(ctx context.Context, uri string, doc *document, lines [][]rune, d check.Diagnostic) []fix {
	if len(d.Fixes) > 0 {
		return limitFixes(lines, d)
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
		edits:     []textEdit{{toRange(lines, d.Start, d.End), name}},
		preferred: true,
	}}
}

// limitFixes lists the fixes of a cost problem that has limits to add: one
// each, or one for all when they only solve the problem together.
func limitFixes(lines [][]rune, d check.Diagnostic) []fix {
	var all []fix
	for _, f := range d.Fixes {
		if f.Value == 0 || len(f.Edits) == 0 {
			continue
		}
		edits := make([]textEdit, len(f.Edits))
		for i, e := range f.Edits {
			at := toPosition(lines, e.At)
			edits[i] = textEdit{lspRange{at, at}, e.Text}
		}
		all = append(all, fix{title: fmt.Sprintf("Add %#q to %#q", fmt.Sprintf("%s: %d", f.Limit, f.Value), f.FieldPath), edits: edits})
	}
	if d.FixesTogether && len(all) != len(d.Fixes) {
		return nil // a limit that cannot be written leaves the rest half a fix
	}
	if !d.FixesTogether || len(all) < 2 {
		return all
	}
	var titles []string
	var edits []textEdit
	for _, f := range all {
		titles = append(titles, strings.TrimPrefix(f.title, "Add "))
		edits = append(edits, f.edits...)
	}
	return []fix{{title: "Add " + strings.Join(titles, " and "), edits: edits}}
}
