package assist

import (
	"context"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// Document is a file read once for any number of Hover, Complete and Suggest
// calls: its CEL expressions, its lines and the YAML of each of its documents.
// Build one with Parse and reuse it while the text is unchanged. It is
// read-only, so safe for concurrent use. The zero value is an empty file.
type Document struct {
	exprs []extract.Expression
	lines []string
	// roots holds the decoded YAML documents of the file, nil for one that
	// is not a mapping or does not parse, indexed like Expression.Document.
	roots []map[string]any
	// crds are the custom resources a policy's params may be typed from.
	crds *check.CRDs
}

// Option changes how Parse reads a file.
type Option func(*Document)

// WithCRDs makes the Document type the params of a policy from the custom
// resource its spec.paramKind names, when the CustomResourceDefinitions in c
// define it. The Document reads c when asked, not when parsed, so c must not
// change while the Document is in use.
func WithCRDs(c *check.CRDs) Option {
	return func(d *Document) { d.crds = c }
}

// Parse reads data, the text of file. A YAML error does not fail it: the
// expressions before the error still count, and so do the documents after it.
// A cancelled ctx gives a Document without expressions, which answers nothing;
// do not keep it.
func Parse(ctx context.Context, file string, data []byte, opts ...Option) *Document {
	docs, broken, err := extract.Documents(ctx, file, data)
	if err != nil {
		return &Document{lines: splitText(data)}
	}
	exprs, _ := extract.FindIn(ctx, file, data, docs, broken)
	d := &Document{exprs: exprs, lines: splitText(data)}
	for _, o := range opts {
		o(d)
	}
	for _, doc := range docs {
		for len(d.roots) <= doc.Index {
			d.roots = append(d.roots, nil)
		}
		var root map[string]any
		if err := doc.Node.Decode(&root); err == nil {
			d.roots[doc.Index] = root
		}
	}
	return d
}

func splitText(data []byte) []string {
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
}
