package hover

import (
	"fmt"
	"math"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/shortnum"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// nearShare says when a cost is near its budget: at 1/nearShare of it or
// more, that is at half. An estimate grows with each limit it depends on, so
// an expression at half its budget goes over when one of those limits
// doubles, the most common edit to a limit; a share lower than half would
// flag most rules over lists, a higher one only rules that a doubled limit
// already breaks.
const nearShare = 2

// PickVersionCommand is the client command that picks the Kubernetes
// version; Cost links the version to it when the client runs it.
const PickVersionCommand = "cel-lens.pickKubernetesVersion"

// mostNamed is the most rules the total names.
const mostNamed = 3

// NotNear is the most an expression with the budget may cost without being
// near it: just under 1/nearShare of it. A near expression's fix is sized to
// bring it there, so the hover stops calling it near once the fix is made.
func NotNear(budget uint64) uint64 { return budget/nearShare - 1 }

// CostOptions are what the Cost: paragraph says besides the estimate.
type CostOptions struct {
	// Version is the Kubernetes version the numbers are for, such as
	// "1.37"; Pick links it to PickVersionCommand.
	Version string
	Pick    bool
	// Fixes are the fixes of an expression near its budget, written after
	// what makes it expensive. An over-budget expression's fixes are the
	// cost problem's, in the Fix block, and are not repeated here.
	Fixes []Fix
}

// Cost returns the Cost: paragraph of an expression, without its label: what
// c, the expression's estimate by check.Costs, says it costs against its
// budget and its schema's total against the total's (near at half a budget,
// with what makes it expensive and o.Fixes), or why there is no estimate. It
// ends with the Kubernetes version the numbers are for, o.Version: a link to
// PickVersionCommand when o.Pick is true, plain text otherwise. It is "" for a
// Cost of no known kind.
func Cost(c check.Cost, o CostOptions) string {
	var parts []string
	switch c.Kind {
	case check.CostEstimated:
		parts = append(parts, expressionCost(c, o.Fixes), totalCost(c))
	case check.CostNotCompiled:
		parts = append(parts, "not estimated: the expression does not compile, so the API server estimates no cost for it.")
	case check.CostSchemaNotCompiled:
		parts = append(parts, "not estimated: the API server compiles no rule of this schema, or of this part of it, for its schema error.")
	case check.CostPolicy:
		parts = append(parts, "the API server estimates no cost when a policy is applied; it stops an evaluation past 1,000,000.")
	case check.CostKyverno:
		parts = append(parts, "Kyverno estimates no cost when a policy is applied.")
	default:
		return ""
	}
	name := "Kubernetes " + Escape(o.Version)
	if o.Pick {
		name = "[" + name + "](command:" + PickVersionCommand + ` "Pick the Kubernetes version")`
	}
	return strings.Join(append(parts, name), " ")
}

// expressionCost is the sentence of the expression's own cost, with the
// fixes of a near one.
func expressionCost(c check.Cost, fixes []Fix) string {
	n, budget := shortnum.Format(c.Cost), shortnum.Format(c.Budget)
	switch {
	case c.Cost > c.Budget:
		return fmt.Sprintf("estimated %s, over the per-expression budget of %s (%s times): the API server refuses this CustomResourceDefinition.", n, budget, times(c.Cost, c.Budget))
	case !near(c.Cost, c.Budget):
		return fmt.Sprintf("estimated %s of the per-expression budget of %s (%s).", n, budget, share(c.Cost, c.Budget))
	}
	out := fmt.Sprintf("estimated %s of the per-expression budget of %s (%s), half or more: doubling a limit it depends on can take it over.", n, budget, share(c.Cost, c.Budget))
	var why []string
	limited := true
	for _, cause := range c.Causes {
		why = append(why, causeText(cause))
		if cause.Limit != "" {
			limited = false
		}
	}
	if len(why) > 0 {
		out += " Why: " + strings.Join(why, "; ") + "."
	}
	if limited {
		out += " Every list, map and string it reads has a limit already."
	}
	var b strings.Builder
	b.WriteString(out)
	for _, f := range fixes {
		b.WriteString(" " + f.Markdown() + ".")
	}
	return b.String()
}

// totalCost is the sentence of the total of the expression's schema, with
// the rules that add the most when it is near or over its budget.
func totalCost(c check.Cost) string {
	schema := schemaName(c)
	n, budget := shortnum.Format(c.Total), shortnum.Format(c.TotalBudget)
	var out string
	switch {
	case c.Total > c.TotalBudget:
		out = fmt.Sprintf("All rules of %s: estimated %s, over the budget of %s (%s times): the API server refuses this CustomResourceDefinition.", schema, n, budget, times(c.Total, c.TotalBudget))
	case near(c.Total, c.TotalBudget):
		out = fmt.Sprintf("All rules of %s: %s of %s (%s), half or more: doubling a limit they depend on can take them over.", schema, n, budget, share(c.Total, c.TotalBudget))
	default:
		return fmt.Sprintf("All rules of %s: %s of %s (%s).", schema, n, budget, share(c.Total, c.TotalBudget))
	}
	var named []string
	for _, m := range c.MostExpensive[:min(len(c.MostExpensive), mostNamed)] {
		entry := m.FieldPath
		if i := strings.LastIndex(entry, ".x-kubernetes-validations["); i >= 0 {
			entry = entry[i+1:]
		}
		named = append(named, fmt.Sprintf("%s at %s (%s)", Code(entry), Code(m.Node), shortnum.Format(m.Cost)))
	}
	if len(named) > 0 {
		out += " The rules that add the most: " + strings.Join(named, ", ") + "."
	}
	return out
}

// schemaName names the schema of c by its versions: "`v1`'s schema" for a
// version's own, or the one version of a CustomResourceDefinition, whose
// schema the API server calls shared as it would one every version shares;
// "the schema `v1` and `v2` share" for several; "the shared schema" when
// the versions are not known.
func schemaName(c check.Cost) string {
	names := c.Versions
	if c.Version != "" {
		names = []string{c.Version}
	}
	switch len(names) {
	case 0:
		return "the shared schema"
	case 1:
		return Code(names[0]) + "'s schema"
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = Code(n)
	}
	return "the schema " + strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1] + " share"
}

// NearOrOver reports whether c is estimated at half its budget or more (see
// nearShare), over it included: such an expression's hover adds the pages
// about cost to Docs:.
func NearOrOver(c check.Cost) bool {
	return c.Kind == check.CostEstimated && (c.Cost > c.Budget || near(c.Cost, c.Budget))
}

// near reports whether cost, not over budget, is near it (see nearShare).
func near(cost, budget uint64) bool { return cost >= budget/nearShare }

// share writes cost as a share of budget, rounded down: "62%", or "under
// 1%".
func share(cost, budget uint64) string {
	pct := uint64(0)
	if budget >= 100 {
		pct = cost / (budget / 100)
	}
	if pct < 1 {
		return "under 1%"
	}
	return fmt.Sprintf("%d%%", pct)
}

// times writes how many times budget cost is, rounded down: with one
// decimal below 10 ("2.4"), else as a count ("240", "13K").
func times(cost, budget uint64) string {
	if budget == 0 {
		return "many"
	}
	if q := cost / budget; q >= 10 {
		return shortnum.Format(q)
	}
	return fmt.Sprintf("%.1f", math.Floor(float64(cost)*10/float64(budget))/10)
}

// causeText says what cause is, in the words of the cost diagnostics, with
// the field paths, which come from the file, in code spans.
func causeText(c check.Cause) string {
	if c.Limit == "" {
		return c.Text // a number turned into text: the text names a CEL expression of letters, digits, dots and underscores
	}
	field, limit := Code(c.FieldPath), Code(c.Limit)
	if c.Repeats {
		s := fmt.Sprintf("it runs for every element of %s, which has no %s", field, limit)
		if c.Size > 0 {
			s += fmt.Sprintf(", and the estimator assumes up to %s elements", shortnum.Format(c.Size))
		}
		return s
	}
	noun := map[string]string{"maxLength": "a string", "maxItems": "a list", "maxProperties": "a map"}[c.Limit]
	unit := map[string]string{"maxLength": "characters", "maxItems": "items", "maxProperties": "entries"}[c.Limit]
	s := fmt.Sprintf("%s is %s with no %s", field, noun, limit)
	if c.Size > 0 {
		s += fmt.Sprintf(", and the estimator assumes up to %s %s", shortnum.Format(c.Size), unit)
	}
	return s
}
