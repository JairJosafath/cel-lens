package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// applyFixed is what cel-lens.applyFix answers once the editor applied the
// fix: its title.
type applyFixed struct {
	Applied string `json:"applied"`
}

// applyFix is cel-lens.applyFix: it takes {uri, version, range, title}, works
// out the quick fixes the engine would answer for that range of that open
// document now (see actionsAt, which estimates and sizes what a fix needs),
// and asks the editor to apply the one with that title, labelled with it,
// with a workspace/applyEdit on that version of the document. A version
// that moved since, a document that is not open, a title the range has no
// fix of, and a client that cannot apply an edit are refused.
func (ss *session) applyFix(ctx context.Context, raw []json.RawMessage) (*workspaceEdit, error) {
	const usage = commandApplyFix + ": give one argument, {uri, version, range, title}, as a hover's link to it gives"
	if len(raw) != 1 {
		return nil, invalid(usage)
	}
	var a applyFixArgs
	if err := json.Unmarshal(raw[0], &a); err != nil || a.URI == "" || a.Title == "" {
		return nil, invalid(usage)
	}
	if !ss.edits.apply {
		return nil, failed("the editor cannot apply an edit for cel-lens (workspace/applyEdit), which is how " + commandApplyFix + " applies a fix: use the quick fix of the editor")
	}
	doc, ok := ss.docs[a.URI]
	if !ok {
		return nil, failed(fmt.Sprintf("%s: %s is not open: open it and hover again", commandApplyFix, a.URI))
	}
	if doc.version != a.Version {
		return nil, failed("the file changed since this hover; hover again")
	}
	actions, err := ss.actionsAt(ctx, a.URI, doc, a.Range, true)
	if err != nil {
		return nil, err
	}
	var titles []string
	for _, act := range actions {
		if act.title != a.Title {
			titles = append(titles, act.title)
			continue
		}
		edit := map[string]any{"changes": map[string][]textEdit{a.URI: act.edits}}
		if ss.edits.documentChanges {
			edit = map[string]any{"documentChanges": []any{map[string]any{
				"textDocument": map[string]any{"uri": a.URI, "version": doc.version},
				"edits":        act.edits,
			}}}
		}
		return &workspaceEdit{label: a.Title, path: a.URI, edit: edit, result: applyFixed{a.Title}}, nil
	}
	offers := "none"
	if len(titles) > 0 {
		offers = strings.Join(titles, "; ")
	}
	return nil, failed(fmt.Sprintf("%s: that range of %s has no quick fix %q now (it has: %s); hover again", commandApplyFix, a.URI, a.Title, offers))
}
