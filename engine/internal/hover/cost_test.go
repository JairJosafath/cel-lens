package hover_test

import (
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/hover"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// estimated is an estimated Cost of the expression with cost, in v1's
// schema, whose total is total, with the budgets of the API server.
func estimated(cost, total uint64) check.Cost {
	return check.Cost{Kind: check.CostEstimated, Version: "v1", Cost: cost, Budget: 10_000_000, Total: total, TotalBudget: 100_000_000}
}

func TestCost(t *testing.T) {
	t.Parallel()
	const v1Small = " All rules of `v1`'s schema: 6.4M of 100M (6%). Kubernetes 1.37"
	ports := check.Cause{FieldPath: "spec.ports", Limit: "maxItems", Size: 3_300_000, Repeats: true}
	name := check.Cause{FieldPath: "spec.name", Limit: "maxLength", Size: 3_145_728}
	conversion := check.Cause{Text: "`string(self.size)` turns a number into text"}
	most := []check.ExpressionCost{
		{FieldPath: "spec.versions[0].schema.openAPIV3Schema.properties[spec].x-kubernetes-validations[0].rule", Node: "spec", Cost: 30_000_000},
		{FieldPath: "spec.versions[0].schema.openAPIV3Schema.properties[spec].properties[ports].items.x-kubernetes-validations[1].messageExpression", Node: "spec.ports[*]", Cost: 20_000_000},
		{FieldPath: "spec.versions[0].schema.openAPIV3Schema.x-kubernetes-validations[2].rule", Node: "self", Cost: 9_000_000},
		{FieldPath: "spec.versions[0].schema.openAPIV3Schema.x-kubernetes-validations[3].rule", Node: "self", Cost: 1_000_000},
	}
	with := func(c check.Cost, f func(*check.Cost)) check.Cost { f(&c); return c }
	tests := []struct {
		name string
		cost check.Cost
		pick bool
		want string
	}{
		{
			"under its budget", estimated(1_200_000, 6_400_000), false,
			"estimated 1.2M of the per-expression budget of 10M (12%)." + v1Small,
		},
		{
			"a share under 1%", estimated(12_000, 6_400_000), false,
			"estimated 12K of the per-expression budget of 10M (under 1%)." + v1Small,
		},
		{
			"a small cost", estimated(3, 3), false,
			"estimated 3 of the per-expression budget of 10M (under 1%). All rules of `v1`'s schema: 3 of 100M (under 1%). Kubernetes 1.37",
		},
		{
			"a share rounded down", with(estimated(9_960_000, 6_400_000), func(c *check.Cost) { c.Causes = []check.Cause{name} }), false,
			"estimated 10M of the per-expression budget of 10M (99%), half or more: doubling a limit it depends on can take it over. Why: `spec.name` is a string with no `maxLength`, and the estimator assumes up to 3.1M characters." + v1Small,
		},
		{
			"just under half", estimated(4_999_999, 6_400_000), false,
			"estimated 5M of the per-expression budget of 10M (49%)." + v1Small,
		},
		{
			"at half", with(estimated(5_000_000, 6_400_000), func(c *check.Cost) { c.Causes = []check.Cause{ports, name} }), false,
			"estimated 5M of the per-expression budget of 10M (50%), half or more: doubling a limit it depends on can take it over. Why: it runs for every element of `spec.ports`, which has no `maxItems`, and the estimator assumes up to 3.3M elements; `spec.name` is a string with no `maxLength`, and the estimator assumes up to 3.1M characters." + v1Small,
		},
		{
			"near with every size limited", estimated(6_200_000, 6_400_000), false,
			"estimated 6.2M of the per-expression budget of 10M (62%), half or more: doubling a limit it depends on can take it over. Every list, map and string it reads has a limit already." + v1Small,
		},
		{
			"near for a number turned into text", with(estimated(6_200_000, 6_400_000), func(c *check.Cost) { c.Causes = []check.Cause{conversion} }), false,
			"estimated 6.2M of the per-expression budget of 10M (62%), half or more: doubling a limit it depends on can take it over. Why: `string(self.size)` turns a number into text. Every list, map and string it reads has a limit already." + v1Small,
		},
		{
			"at the budget", estimated(10_000_000, 10_000_000), false,
			"estimated 10M of the per-expression budget of 10M (100%), half or more: doubling a limit it depends on can take it over. Every list, map and string it reads has a limit already. All rules of `v1`'s schema: 10M of 100M (10%). Kubernetes 1.37",
		},
		{
			"over its budget", with(estimated(2_400_000_000, 2_400_000_000), func(c *check.Cost) { c.Causes = []check.Cause{ports}; c.MostExpensive = most[:1] }), false,
			"estimated 2.4B, over the per-expression budget of 10M (240 times): the API server refuses this CustomResourceDefinition. All rules of `v1`'s schema: estimated 2.4B, over the budget of 100M (24 times): the API server refuses this CustomResourceDefinition. The rules that add the most: `x-kubernetes-validations[0].rule` at `spec` (30M). Kubernetes 1.37",
		},
		{
			"just over its budget", estimated(10_500_000, 10_500_000), false,
			"estimated 10.5M, over the per-expression budget of 10M (1.0 times): the API server refuses this CustomResourceDefinition. All rules of `v1`'s schema: 10.5M of 100M (10%). Kubernetes 1.37",
		},
		{
			"the total near", with(estimated(1_000_000, 62_000_000), func(c *check.Cost) { c.MostExpensive = most }), false,
			"estimated 1M of the per-expression budget of 10M (10%). All rules of `v1`'s schema: 62M of 100M (62%), half or more: doubling a limit they depend on can take them over. The rules that add the most: `x-kubernetes-validations[0].rule` at `spec` (30M), `x-kubernetes-validations[1].messageExpression` at `spec.ports[*]` (20M), `x-kubernetes-validations[2].rule` at `self` (9M). Kubernetes 1.37",
		},
		{
			"the total over", with(estimated(9_000_000, 1_300_000_000_000), func(c *check.Cost) { c.MostExpensive = most[2:] }), false,
			"estimated 9M of the per-expression budget of 10M (90%), half or more: doubling a limit it depends on can take it over. Every list, map and string it reads has a limit already. All rules of `v1`'s schema: estimated 1.3T, over the budget of 100M (13K times): the API server refuses this CustomResourceDefinition. The rules that add the most: `x-kubernetes-validations[2].rule` at `self` (9M), `x-kubernetes-validations[3].rule` at `self` (1M). Kubernetes 1.37",
		},
		{
			"the shared schema", with(estimated(1_200_000, 6_400_000), func(c *check.Cost) { c.Version = "" }), false,
			"estimated 1.2M of the per-expression budget of 10M (12%). All rules of the shared schema: 6.4M of 100M (6%). Kubernetes 1.37",
		},
		// The API server moves the schema of a CRD with one version to
		// spec.validation, as one every version shares: it is still that
		// version's (review of #62).
		{
			"the one version's schema", with(estimated(1_200_000, 6_400_000), func(c *check.Cost) { c.Version, c.Versions = "", []string{"v1"} }), false,
			"estimated 1.2M of the per-expression budget of 10M (12%). All rules of `v1`'s schema: 6.4M of 100M (6%). Kubernetes 1.37",
		},
		{
			"a schema two versions share", with(estimated(1_200_000, 6_400_000), func(c *check.Cost) { c.Version, c.Versions = "", []string{"v1", "v2"} }), false,
			"estimated 1.2M of the per-expression budget of 10M (12%). All rules of the schema `v1` and `v2` share: 6.4M of 100M (6%). Kubernetes 1.37",
		},
		{
			"a schema three versions share", with(estimated(1_200_000, 6_400_000), func(c *check.Cost) { c.Version, c.Versions = "", []string{"v1", "v2", "v3`x"} }), false,
			"estimated 1.2M of the per-expression budget of 10M (12%). All rules of the schema `v1`, `v2` and ``v3`x`` share: 6.4M of 100M (6%). Kubernetes 1.37",
		},
		{
			"a name from the file in code", with(estimated(6_200_000, 6_400_000), func(c *check.Cost) {
				c.Version = "v`1"
				c.Causes = []check.Cause{{FieldPath: "spec.a`b", Limit: "maxProperties", Repeats: true}}
			}), false,
			"estimated 6.2M of the per-expression budget of 10M (62%), half or more: doubling a limit it depends on can take it over. Why: it runs for every element of ``spec.a`b``, which has no `maxProperties`. All rules of ``v`1``'s schema: 6.4M of 100M (6%). Kubernetes 1.37",
		},
		{
			"the version linked", estimated(1_200_000, 6_400_000), true,
			`estimated 1.2M of the per-expression budget of 10M (12%). All rules of ` + "`v1`" + `'s schema: 6.4M of 100M (6%). [Kubernetes 1.37](command:cel-lens.pickKubernetesVersion "Pick the Kubernetes version")`,
		},
		{
			"not compiled",
			check.Cost{Kind: check.CostNotCompiled},
			false,
			"not estimated: the expression does not compile, so the API server estimates no cost for it. Kubernetes 1.37",
		},
		{
			"schema not compiled",
			check.Cost{Kind: check.CostSchemaNotCompiled},
			false,
			"not estimated: the API server compiles no rule of this schema, or of this part of it, for its schema error. Kubernetes 1.37",
		},
		{
			"a policy",
			check.Cost{Kind: check.CostPolicy},
			false,
			"the API server estimates no cost when a policy is applied; it stops an evaluation past 1,000,000. Kubernetes 1.37",
		},
		{
			"Kyverno",
			check.Cost{Kind: check.CostKyverno},
			true,
			`Kyverno estimates no cost when a policy is applied. [Kubernetes 1.37](command:cel-lens.pickKubernetesVersion "Pick the Kubernetes version")`,
		},
		{"no kind", check.Cost{}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hover.Cost(tt.cost, hover.CostOptions{Version: "1.37", Pick: tt.pick}); got != tt.want {
				t.Errorf("Cost(%+v) =\n%s\nwant\n%s", tt.cost, got, tt.want)
			}
		})
	}
}

// TestCostFixes checks that a near expression's fixes follow what makes it
// expensive (#56), as a link to cel-lens.applyFix for a client that runs it
// and in words for one that does not, and that an over-budget one's are not
// repeated: they are the cost problem's, in the Fix block.
func TestCostFixes(t *testing.T) {
	t.Parallel()
	ports := check.Cause{FieldPath: "spec.ports", Limit: "maxItems", Size: 1_048_576}
	near := estimated(6_200_000, 6_400_000)
	near.Causes = []check.Cause{ports}
	over := estimated(24_000_000, 24_000_000)
	over.Causes = []check.Cause{ports}
	words := hover.Fix{Words: "Add `maxItems: 500000` to `spec.ports`", Title: "Add `maxItems: 500000` to `spec.ports`"}
	linked := words
	linked.Argument = "%5B%5D"
	const why = "estimated 6.2M of the per-expression budget of 10M (62%), half or more: doubling a limit it depends on can take it over. Why: `spec.ports` is a list with no `maxItems`, and the estimator assumes up to 1M items. "
	tests := []struct {
		name string
		cost check.Cost
		fix  hover.Fix
		want string
	}{
		{"near, in words", near, words, why + "Fix: Add `maxItems: 500000` to `spec.ports` (a quick fix). All rules of `v1`'s schema: 6.4M of 100M (6%). Kubernetes 1.37"},
		{"near, linked", near, linked, why + "Fix: [Add `maxItems: 500000` to `spec.ports`](command:cel-lens.applyFix?%5B%5D \"Add `maxItems: 500000` to `spec.ports`\"). All rules of `v1`'s schema: 6.4M of 100M (6%). Kubernetes 1.37"},
		{"over", over, linked, "estimated 24M, over the per-expression budget of 10M (2.4 times): the API server refuses this CustomResourceDefinition. All rules of `v1`'s schema: 24M of 100M (24%). Kubernetes 1.37"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hover.Cost(tt.cost, hover.CostOptions{Version: "1.37", Fixes: []hover.Fix{tt.fix}}); got != tt.want {
				t.Errorf("Cost() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
