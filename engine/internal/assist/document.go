package assist

import (
	"context"
	"strings"
	"sync"

	"github.com/JairJosafath/cel-lens/engine/internal/meaning"
	"github.com/JairJosafath/cel-lens/engine/internal/yamlfield"
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
	// assumed are the types to assume where values are dyn (WithAssumedTypes).
	assumed []check.AssumedType
	// lensesOff is true when the types of .cellens are off (WithoutCellens).
	lensesOff bool
	// memo holds what Rule works out of each expression that depends on
	// nothing but the file, by the expression's start, made by the first
	// Rule that needs it; memoMu guards it.
	memoMu sync.Mutex
	memo   map[extract.Position]*exprMemo
}

// exprMemo is what Rule keeps of an expression of a Document.
type exprMemo struct {
	// expr is the expression parsed, once done is true; ok is false when
	// it does not parse. Uses: and Checks: share it (see parsed).
	expr     meaning.Expression
	done, ok bool
	// reads are the names the expression reads (see names), once read is
	// true.
	reads []string
	read  bool
	// uses is the Uses section of a CustomResourceDefinition's rule, which
	// depends only on the file, once used is true.
	uses string
	used bool
	// sentence is what the expression checks (see meaning), "" for none,
	// once said is true.
	sentence string
	said     bool
}

// memoOf returns what is kept of the expression that starts at start. The
// caller holds d.memoMu.
func (d *Document) memoOf(start extract.Position) *exprMemo {
	if d.memo == nil {
		d.memo = map[extract.Position]*exprMemo{}
	}
	m := d.memo[start]
	if m == nil {
		m = &exprMemo{}
		d.memo[start] = m
	}
	return m
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

// WithoutCellens says that the types of .cellens are off, the experimental
// feature cellens-types: hover on a dyn value a .cellens entry could type
// says so, as it does for crd-typed-policies (see cellensOffNote).
func WithoutCellens() Option {
	return func(d *Document) { d.lensesOff = true }
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
		if err := yamlfield.Decode(doc.Node, &root); err == nil {
			if root["apiVersion"] == "apiextensions.k8s.io/v1" && root["kind"] == "CustomResourceDefinition" {
				assumeInCRD(root, d.assumedFor(root))
			}
			d.roots[doc.Index] = root
		}
	}
	return d
}

// Expression returns the CEL expression of d whose text has pos, its ends
// included, as package extract finds it, and false when there is none.
func (d *Document) Expression(pos extract.Position) (extract.Expression, bool) {
	for _, e := range d.exprs {
		if !before(pos, e.Start) && !before(e.End, pos) {
			return e, true
		}
	}
	return extract.Expression{}, false
}

func splitText(data []byte) []string {
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
}
