package check_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// portsCRD is a CustomResourceDefinition whose spec.ports, with the limits
// given (indented to 18 spaces), has the rule given.
func portsCRD(limits, rule string) []byte {
	return []byte(costCRD([2]string{"v1", fmt.Sprintf(`          properties:
            spec:
              type: object
              properties:
                ports:
                  type: array
%s                  x-kubernetes-validations:
                    - rule: %q
                  items:
                    type: object
                    properties:
                      port: {type: integer}
`, limits, rule)}))
}

// nextStep is the number after n on the ladder of limits: 1, 2, 5, 10, 20 …
func nextStep(n int64) int64 {
	for m := int64(1); ; m *= 10 {
		for _, s := range []int64{m, 2 * m, 5 * m} {
			if s > n {
				return s
			}
		}
	}
}

// TestLimits checks the limits sized for a rule near its budget, to bring it
// under half of it (#56): the largest number of the ladder that does, so the
// next one does not; none when every list the rule reads has a limit; none
// for an expression with no estimate.
func TestLimits(t *testing.T) {
	t.Parallel()
	const rule = "self.all(p, p.port < 65536)" // 62% of its budget over an unbounded list
	ctx := context.Background()
	costOf := func(t *testing.T, data []byte) check.Cost {
		t.Helper()
		costs, err := check.Costs(ctx, "c.yaml", data)
		if err != nil || len(costs) != 1 {
			t.Fatalf("Costs() = %+v, %v, want one", costs, err)
		}
		return costs[0]
	}
	t.Run("near over an unbounded list", func(t *testing.T) {
		t.Parallel()
		data := portsCRD("", rule)
		c := costOf(t, data)
		if c.Cost < c.Budget/2 || c.Cost > c.Budget {
			t.Fatalf("Costs() = %d of %d, want the rule near its budget", c.Cost, c.Budget)
		}
		half := c.Budget/2 - 1
		fixes, together, err := check.Limits(ctx, "c.yaml", data, c, half)
		if err != nil || len(fixes) != 1 || together {
			t.Fatalf("Limits() = %+v, %t, %v, want one fix", fixes, together, err)
		}
		f := fixes[0]
		if f.FieldPath != "spec.ports" || f.Limit != "maxItems" || f.Value == 0 || len(f.Edits) != 1 {
			t.Fatalf("Limits() fix = %+v, want maxItems on spec.ports with a number and its edit", f)
		}
		fixed := apply(t, data, f.Edits)
		if got := costOf(t, fixed); got.Cost >= got.Budget/2 {
			t.Errorf("with maxItems: %d the rule costs %d, want under half of %d:\n%s", f.Value, got.Cost, got.Budget, fixed)
		}
		next := portsCRD(fmt.Sprintf("                  maxItems: %d\n", nextStep(f.Value)), rule)
		if got := costOf(t, next); got.Cost < got.Budget/2 {
			t.Errorf("maxItems: %d, the next number, also brings it under half (%d), want the largest that does", nextStep(f.Value), got.Cost)
		}
		if !strings.Contains(string(fixed), fmt.Sprintf("maxItems: %d\n", f.Value)) {
			t.Errorf("the edit wrote\n%s\nwant maxItems: %d", fixed, f.Value)
		}
	})
	t.Run("every list limited", func(t *testing.T) {
		t.Parallel()
		data := portsCRD("                  maxItems: 1000000\n", rule)
		c := costOf(t, data)
		if c.Cost < c.Budget/2 {
			t.Fatalf("Costs() = %d of %d, want the rule near its budget though its list has a limit", c.Cost, c.Budget)
		}
		if fixes, _, err := check.Limits(ctx, "c.yaml", data, c, c.Budget/2-1); err != nil || fixes != nil {
			t.Errorf("Limits() = %+v, %v, want no fix: lowering a limit is the author's call", fixes, err)
		}
	})
	t.Run("no estimate", func(t *testing.T) {
		t.Parallel()
		data := portsCRD("", "self.nope")
		c := costOf(t, data)
		if fixes, _, err := check.Limits(ctx, "c.yaml", data, c, c.Budget/2-1); err != nil || fixes != nil || c.Kind == check.CostEstimated {
			t.Errorf("Limits(%s) = %+v, %v, want no fix", c.Kind, fixes, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		data := portsCRD("", rule)
		c := costOf(t, data)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := check.Limits(cancelled, "c.yaml", data, c, c.Budget/2-1); err == nil {
			t.Error("Limits(cancelled) error = nil, want the context's")
		}
	})
}
