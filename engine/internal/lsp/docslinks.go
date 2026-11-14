package lsp

import (
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// costLevel says whether the estimated cost of an expression is near or
// over its budget, which adds the pages about cost to the hover's Docs:
// paragraph. The costs the Cost: paragraph shows give one (estimates); nil
// says no expression is near or over, as before they are known.
type costLevel interface {
	nearOrOver(e extract.Expression) bool
}

// docsSection is the Docs: paragraph of the hover on the expression e of
// doc: the pinned pages of its kind of rule, with those about cost when
// costs says it is near or over its budget, then part, the link to the
// docs of the function under the cursor, "" for none.
func docsSection(a *assist.Assistant, doc *assist.Document, e extract.Expression, part string, costs costLevel) string {
	heavy := costs != nil && costs.nearOrOver(e)
	var links []string
	for _, l := range []string{a.RuleDocs(doc, e, heavy), part} {
		if l != "" {
			links = append(links, l)
		}
	}
	return strings.Join(links, " · ")
}
