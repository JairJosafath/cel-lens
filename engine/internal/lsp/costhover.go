package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/JairJosafath/cel-lens/engine/internal/hover"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// costKey is what the costs of a version of a document depend on besides its
// text: the Kubernetes version and the experimental features.
type costKey struct {
	kube     kubeversion.Version
	features feature.Set
}

// estimates are the costs of a document's expressions for the Kubernetes
// version kube, as check.Costs answers them, and the fixes of the near ones
// sized so far. costs does not change once made, so work off the loop may
// read it; near is the loop's alone.
type estimates struct {
	costs []check.Cost
	kube  kubeversion.Version
	// near are the limits sized for the expressions near their budget, by
	// where the expression starts (see sizeNear).
	near map[extract.Position]nearFix
}

// nearFix is the limits sized for an expression near its budget, as
// check.Limits answers them: none when its fields all have limits.
type nearFix struct {
	fixes    []check.Fix
	together bool
}

// of is the cost of the expression e, false when es is nil or has none for
// it.
func (es *estimates) of(e extract.Expression) (check.Cost, bool) {
	if es == nil {
		return check.Cost{}, false
	}
	for _, c := range es.costs {
		if c.Start == e.Start && c.End == e.End {
			return c, true
		}
	}
	return check.Cost{}, false
}

// nearOrOver reports whether the estimated cost of e is at half its budget
// or more (see hover.NearOrOver), for the Docs: paragraph (costLevel); false
// when es is nil or has no estimate of e.
func (es *estimates) nearOrOver(e extract.Expression) bool {
	c, ok := es.of(e)
	return ok && hover.NearOrOver(c)
}

// needsFix reports whether the cost c is near its budget, so it has a fix to
// size, and es has not sized it yet.
func (es *estimates) needsFix(c check.Cost) bool {
	if !isNear(c) {
		return false
	}
	_, sized := es.near[c.Start]
	return !sized
}

// isNear reports whether c is estimated at half its budget or more, and not
// over it: such an expression gets the fix that brings it under half.
func isNear(c check.Cost) bool {
	return c.Kind == check.CostEstimated && c.Cost > hover.NotNear(c.Budget) && c.Cost <= c.Budget
}

// keepFix keeps f, the fix sized for the expression that starts at, in es.
func (es *estimates) keepFix(at extract.Position, f nearFix) {
	if es.near == nil {
		es.near = map[extract.Position]nearFix{}
	}
	es.near[at] = f
}

// commandHover names the hover's jobs, which estimate costs.
const commandHover = "textDocument/hover"

// answerHover answers the hover request msg. When the hover is inside an
// expression and the costs of its document are not known for this version
// of its text, the Kubernetes version and the features, or the expression
// is near its budget and its fix is not sized, they are worked out in a job,
// off the message loop, and the hover is answered when they are (see
// costsJob): they are then kept with the document, so a hover asked again
// works out nothing. A hover cancelled with $/cancelRequest meanwhile is
// answered as cancelled, and when no other hover waits the work stops and
// nothing is kept.
func (ss *session) answerHover(ctx context.Context, msg jsonrpc.Message, reply func(any, error) error) error {
	var p positionParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return reply(nil, badParams("textDocument/hover", err))
	}
	doc, ok := ss.docs[p.TextDocument.URI]
	if !ok || isLens(p.TextDocument.URI) {
		return reply(ss.hoverAt(ctx, p, nil))
	}
	e, in := doc.parse(ctx, p.TextDocument.URI).Expression(fromPosition(doc.runes(), p.Position))
	known := ss.knownCosts(doc)
	if !in || known != nil && !known.needsFixAt(e) {
		return reply(ss.hoverAt(ctx, p, known))
	}
	if msg.IsNotification() || len(ss.jobs) >= maxJobs {
		// With every slot taken, the hover goes with what is known (no
		// Cost: without the costs, no fix not yet sized) rather than work
		// it out on the message loop, where every keystroke would wait for
		// it (decision 11); the next hover starts the job.
		return reply(ss.hoverAt(ctx, p, known))
	}
	return ss.startJob(ctx, msg, ss.costsJob(ctx, p, doc, e, known))
}

// needsFixAt reports whether the expression e is near its budget and its fix
// is not sized.
func (es *estimates) needsFixAt(e extract.Expression) bool {
	c, ok := es.of(e)
	return ok && es.needsFix(c)
}

// knownCosts are the costs kept with doc, nil when doc is nil or they were
// made for another Kubernetes version or other features.
func (ss *session) knownCosts(doc *document) *estimates {
	if doc == nil || doc.costs == nil || doc.costsFor != (costKey{ss.kube, ss.features}) {
		return nil
	}
	return doc.costs
}

// costsWork returns the work that estimates the costs of doc, at uri, for
// the session's Kubernetes version and features, and the key it does it for:
// it reads nothing of the session, so it may run off the loop. The API
// server's validation the check of this version of doc ran is shared
// (document.validations). A file whose YAML does not parse in part gives the
// costs of the rest.
func (ss *session) costsWork(uri string, doc *document) (func(context.Context) (*estimates, error), costKey) {
	run := check.Costs
	if ss.coster != nil {
		run = ss.coster
	}
	key := costKey{ss.kube, ss.features}
	text, shared := doc.text, &doc.validations
	return func(ctx context.Context) (*estimates, error) {
		costs, err := run(ctx, uri, text, check.WithVersion(key.kube), check.WithFeatures(key.features), check.WithValidations(shared))
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		var syntax *extract.SyntaxError
		if err != nil && !errors.As(err, &syntax) {
			return nil, fmt.Errorf("estimate the costs of %s: %w", uri, err)
		}
		return &estimates{costs: costs, kube: key.kube}, nil
	}, key
}

// sizeWork returns the work that sizes the fix of c, an expression of doc
// at uri near its budget, to bring it under half of it; it reads nothing of
// the session.
func (ss *session) sizeWork(uri string, doc *document, key costKey) func(context.Context, check.Cost) (nearFix, error) {
	run := check.Limits
	if ss.limiter != nil {
		run = ss.limiter
	}
	text := doc.text
	return func(ctx context.Context, c check.Cost) (nearFix, error) {
		fixes, together, err := run(ctx, uri, text, c, hover.NotNear(c.Budget), check.WithVersion(key.kube), check.WithFeatures(key.features))
		if cerr := ctx.Err(); cerr != nil {
			return nearFix{}, cerr
		}
		if err != nil {
			return nearFix{}, fmt.Errorf("size the fix of %s: %w", uri, err)
		}
		return nearFix{fixes, together}, nil
	}
}

// keep keeps es, made for key, with doc, when doc is still the document at
// uri and key the session's.
func (ss *session) keep(uri string, doc *document, key costKey, es *estimates) {
	if ss.docs[uri] == doc && key == (costKey{ss.kube, ss.features}) {
		doc.costs, doc.costsFor = es, key
	}
}

// estimateNow returns the costs of doc, estimating them on the message loop
// and keeping them when they are not known. A cancelled ctx gives its error
// and keeps nothing.
func (ss *session) estimateNow(ctx context.Context, uri string, doc *document) (*estimates, error) {
	if es := ss.knownCosts(doc); es != nil {
		return es, nil
	}
	work, key := ss.costsWork(uri, doc)
	es, err := work(ctx)
	if err != nil {
		return nil, err
	}
	ss.keep(uri, doc, key, es)
	return es, nil
}

// sizeNow sizes the fix of c, an expression of doc in es near its budget, on
// the message loop, unless es has it, and keeps it in es.
func (ss *session) sizeNow(ctx context.Context, uri string, doc *document, es *estimates, c check.Cost) error {
	if !es.needsFix(c) {
		return nil
	}
	f, err := ss.sizeWork(uri, doc, costKey{es.kube, ss.features})(ctx, c)
	if err != nil {
		return err
	}
	es.keepFix(c.Start, f)
	return nil
}

// hoverWork is what the job of a hover worked out: the costs, new when known
// was nil, and the fix of the hovered expression, when it was sized.
type hoverWork struct {
	es    *estimates
	fresh bool
	fix   *nearFix
	at    extract.Position
}

// costsJob is the job of a hover at p in doc, on the expression e, whose
// costs are not known (known is nil) or whose fix is not sized: its work
// estimates the costs and sizes the fix of e when it is near its budget, its
// settle keeps them with doc, and its answer is the hover at p. Hovers on the
// same expression of the same version of doc, for the same Kubernetes
// version and features, share the job; one elsewhere replaces it. A hover
// answered once doc changed gets ContentModified, as its position is in text
// the client no longer has.
func (ss *session) costsJob(ctx context.Context, p positionParams, doc *document, e extract.Expression, known *estimates) jobSpec {
	uri := p.TextDocument.URI
	estimate, key := ss.costsWork(uri, doc)
	size := ss.sizeWork(uri, doc, key)
	var costs []check.Cost
	if known != nil {
		costs = known.costs // the loop never changes it
	}
	return jobSpec{
		command: commandHover,
		slot:    "costs",
		key:     fmt.Sprintf("%p %s %d %d:%d", doc, key.kube, key.features, e.Start.Line, e.Start.Column),
		work: func(ctx context.Context) (any, error) {
			w := hoverWork{es: known, at: e.Start}
			if known == nil {
				es, err := estimate(ctx)
				if err != nil {
					return nil, err
				}
				w.es, w.fresh, costs = es, true, es.costs
			}
			c, ok := (&estimates{costs: costs}).of(e)
			if !ok || !isNear(c) {
				return w, nil
			}
			f, err := size(ctx, c)
			if err != nil {
				return nil, err
			}
			w.fix = &f
			return w, nil
		},
		settle: func(result any) (any, error) {
			w := result.(hoverWork) //nolint:forcetypeassert // work returns hoverWork
			if w.fresh {
				ss.keep(uri, doc, key, w.es)
			}
			if w.fix != nil {
				w.es.keepFix(w.at, *w.fix)
			}
			return w.es, nil
		},
		answer: func(result any) (any, error) {
			if ss.docs[uri] != doc {
				return nil, &rpcError{jsonrpc.CodeContentModified, "the document changed while the costs of its expressions were estimated: hover again"}
			}
			return ss.hoverAt(ctx, p, result.(*estimates)) //nolint:forcetypeassert // settle returns *estimates
		},
	}
}
