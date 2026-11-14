package check_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	celschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// costCRD is a CustomResourceDefinition with the versions given, each a name
// and the properties of its root schema indented to 12 spaces.
func costCRD(versions ...[2]string) string {
	var b strings.Builder
	b.WriteString(crdHead)
	for i, v := range versions {
		fmt.Fprintf(&b, "    - name: %s\n      served: true\n      storage: %t\n      schema:\n        openAPIV3Schema:\n          type: object\n%s", v[0], i == 0, v[1])
	}
	return b.String()
}

// apiCost is what the API server's own compiler and estimator give, at the
// default version, the first rule (its MaxCost times its node's cardinality,
// as getExpressionCost) and the first messageExpression (its own MaxCost) of
// the schema node at the property path of the first version of the CRD data.
// "[]" in props steps into a list's items, "{}" into a map's values.
func apiCost(t *testing.T, data string, props ...string) (rule, message uint64) {
	t.Helper()
	var v apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal([]byte(data), &v); err != nil {
		t.Fatal(err)
	}
	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(&v)
	var crd apiextensions.CustomResourceDefinition
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&v, &crd, nil); err != nil {
		t.Fatal(err)
	}
	schema := crd.Spec.Validation.OpenAPIV3Schema
	cel := validation.RootCELContext(schema)
	for _, p := range props {
		switch p {
		case "[]":
			schema = schema.Items.Schema
			cel = cel.ChildItemsContext(schema)
		case "{}":
			schema = schema.AdditionalProperties.Schema
			cel = cel.ChildAdditionalPropertiesContext(schema)
		default:
			child := schema.Properties[p]
			cel = cel.ChildPropertyContext(&child, p)
			schema = &child
		}
	}
	info, err := cel.TypeInfo()
	if err != nil {
		t.Fatal(err)
	}
	res, err := celschema.Compile(info.Schema, info.DeclType, celconfig.PerCallLimit, kubeversion.Default().EnvSet(), celschema.NewExpressionsEnvLoader())
	if err != nil {
		t.Fatal(err)
	}
	card := res[0].MaxCardinality
	if cel.MaxCardinality != nil {
		card = *cel.MaxCardinality
	}
	return res[0].MaxCost * card, res[0].MessageExpressionMaxCost
}

// costAt returns the costs of the expressions whose line in data holds text.
func costAt(costs []check.Cost, data, text string) []check.Cost {
	lines := strings.Split(data, "\n")
	var out []check.Cost
	for _, c := range costs {
		if c.Start.Line >= 1 && c.Start.Line <= len(lines) && strings.Contains(lines[c.Start.Line-1], text) {
			out = append(out, c)
		}
	}
	return out
}

// costsOf runs Costs on data at the default version and fails on an error.
func costsOf(t *testing.T, data string, opts ...check.Option) []check.Cost {
	t.Helper()
	got, err := check.Costs(context.Background(), "c.yaml", []byte(data), opts...)
	if err != nil {
		t.Fatalf("Costs() error = %v", err)
	}
	return got
}

// onlyCostAt is the one cost of the expression on the line that holds text.
func onlyCostAt(t *testing.T, costs []check.Cost, data, text string) check.Cost {
	t.Helper()
	got := costAt(costs, data, text)
	if len(got) != 1 {
		t.Fatalf("Costs() at %q = %+v, want one entry", text, got)
	}
	return got[0]
}

// The schemas of the table tests.
const (
	rootRule = `          x-kubernetes-validations:
            - rule: "self.name.startsWith('a')"
          properties:
            name:
              type: string
`
	nestedRule = `          properties:
            spec:
              type: object
              properties:
                a:
                  type: object
                  x-kubernetes-validations:
                    - rule: "self.b > 0"
                  properties:
                    b:
                      type: integer
`
	listsRule = `          properties:
            spec:
              type: object
              properties:
                outer:
                  type: array
                  items:
                    type: array
                    items:
                      type: object
                      x-kubernetes-validations:
                        - rule: "self.count > 0"
                      properties:
                        count:
                          type: integer
`
	mapRule = `          properties:
            spec:
              type: object
              properties:
                m:
                  type: object
                  additionalProperties:
                    type: object
                    x-kubernetes-validations:
                      - rule: "self.count > 0"
                    properties:
                      count:
                        type: integer
`
	messageRule = `          properties:
            spec:
              type: object
              properties:
                tags:
                  type: array
                  items:
                    type: string
                    maxLength: 100
                    x-kubernetes-validations:
                      - rule: "self.size() < 50"
                        messageExpression: "'too long: ' + self + ' is ' + string(self.size()) + ' long'"
`
)

// farRule is a rule on an unbounded list of unbounded strings that reads
// every string twice, far over its budget.
const farRule = `          properties:
            l:
              type: array
              items:
                type: string
              x-kubernetes-validations:
                - rule: "self.all(x, x.matches('^a.*$') && x.matches('^b.*$'))"
`

// TestCostsEstimated checks the numbers of an estimated expression against
// the API server's own compiler and estimator at the node: on the root, on a
// nested object, on list items under two unbounded lists (where the node's
// cardinality multiplies the cost), on a map's values, and a messageExpression,
// whose cost is its own, against a budget of its own.
func TestCostsEstimated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		schema  string
		text    string // on the expression's line
		props   []string
		message bool
		causes  []string // the field paths and limits of the causes
	}{
		{"root", rootRule, "startsWith", nil, false, []string{"name maxLength"}},
		{"nested object", nestedRule, "self.b > 0", []string{"spec", "a"}, false, nil},
		{"list items under two unbounded lists", listsRule, "self.count > 0", []string{"spec", "outer", "[]", "[]"}, false, []string{"spec.outer[*] maxItems", "spec.outer maxItems"}},
		{"map values", mapRule, "self.count > 0", []string{"spec", "m", "{}"}, false, []string{"spec.m maxProperties"}},
		{"messageExpression", messageRule, "messageExpression", []string{"spec", "tags", "[]"}, true, nil},
		// Above 100 times its budget the API server prints only "more than
		// 100x", so the truth test cannot pin the number there; this does
		// (review of #60).
		{"far over its budget", farRule, "matches", []string{"l"}, false, []string{"l maxItems", "l[*] maxLength"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := costCRD([2]string{"v1", tt.schema})
			got := onlyCostAt(t, costsOf(t, data), data, tt.text)
			rule, message := apiCost(t, data, tt.props...)
			want := rule
			if tt.message {
				want = message
			}
			if got.Kind != check.CostEstimated || got.Cost != want || got.Budget != 10_000_000 || got.TotalBudget != 100_000_000 {
				t.Errorf("Costs(%s) = %+v, want estimated, cost %d, budget 10M, total budget 100M", tt.name, got, want)
			}
			if tt.name == "far over its budget" && got.Cost/got.Budget <= 100 {
				t.Errorf("Costs(%s) cost = %d, want it over 100 times the budget of %d", tt.name, got.Cost, got.Budget)
			}
			if got.Version != "" || !strings.HasPrefix(got.FieldPath, "spec.validation.openAPIV3Schema") {
				t.Errorf("Costs(%s) version %q at %q, want the one version's schema in spec.validation, with no version", tt.name, got.Version, got.FieldPath)
			}
			var causes []string
			for _, c := range got.Causes {
				causes = append(causes, c.FieldPath+" "+c.Limit)
				if c.Text == "" {
					t.Errorf("Costs(%s) cause %+v has no text", tt.name, c)
				}
			}
			if !slices.Equal(causes, tt.causes) {
				t.Errorf("Costs(%s) causes = %q, want %q: %+v", tt.name, causes, tt.causes, got.Causes)
			}
		})
	}
}

// TestCostsListCardinality checks that a rule on list items under two
// unbounded lists costs its MaxCost once for every item the estimator assumes
// the lists may hold together, which the causes name.
func TestCostsListCardinality(t *testing.T) {
	t.Parallel()
	data := costCRD([2]string{"v1", listsRule})
	got := onlyCostAt(t, costsOf(t, data), data, "self.count > 0")
	once := onlyCostAt(t, costsOf(t, costCRD([2]string{"v1", nestedRule})), costCRD([2]string{"v1", nestedRule}), "self.b > 0")
	if got.Cost <= 1000*once.Cost {
		t.Errorf("Costs(two lists) cost = %d, want far more than the same rule on an object (%d)", got.Cost, once.Cost)
	}
	for _, c := range got.Causes {
		if c.Size <= 1 || !strings.Contains(c.Text, "runs for every element") {
			t.Errorf("Costs(two lists) cause = %+v, want a list it runs for every element of, with the size assumed", c)
		}
	}
}

// TestCostsMessageExpressionNotMultiplied (#54): a messageExpression's cost is its
// own, so the unbounded list above it is no cause of it, in Costs and in
// Check's message for it, while the rule's is.
func TestCostsMessageExpressionNotMultiplied(t *testing.T) {
	t.Parallel()
	const schema = `          properties:
            tags:
              type: array
              items:
                type: string
                x-kubernetes-validations:
                  - rule: "self.size() < 50"
                    messageExpression: "'bad: ' + string(self.size()) + string(self.size()) + string(self.size()) + string(self.size())"
`
	data := costCRD([2]string{"v1", schema})
	costs := costsOf(t, data)
	msg := onlyCostAt(t, costs, data, "messageExpression")
	for _, c := range msg.Causes {
		if c.FieldPath == "tags" {
			t.Errorf("Costs() messageExpression cause = %+v, want none about the list above it", c)
		}
	}
	if msg.Cost <= msg.Budget {
		t.Fatalf("Costs() messageExpression cost = %d, want it over %d for the message test", msg.Cost, msg.Budget)
	}
	rule := onlyCostAt(t, costs, data, "self.size() < 50")
	if !slices.ContainsFunc(rule.Causes, func(c check.Cause) bool { return c.FieldPath == "tags" && c.Limit == "maxItems" }) {
		t.Errorf("Costs() rule causes = %+v, want the list it runs for every element of", rule.Causes)
	}
	diags, err := check.Check(context.Background(), "c.yaml", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Start == msg.Start && strings.Contains(d.Message, "runs for every element") {
			t.Errorf("Check() messageExpression problem = %q, want no list it runs for every element of", d.Message)
		}
	}
}

// TestCostsSchemas checks the versions and totals: a schema every version
// shares is answered once for each version's copy, in spec.validation and with
// no version, with one total; a schema per version is answered per version,
// each with its own total. A total is the sum of its schema's costs.
func TestCostsSchemas(t *testing.T) {
	t.Parallel()
	const v2 = `          x-kubernetes-validations:
            - rule: "self.name.endsWith('b')"
              messageExpression: "'no b in ' + self.name"
          properties:
            name:
              type: string
`
	t.Run("shared", func(t *testing.T) {
		t.Parallel()
		data := costCRD([2]string{"v1", rootRule}, [2]string{"v2", rootRule})
		got := costAt(costsOf(t, data), data, "startsWith")
		if len(got) != 2 || got[0].Start.Line == got[1].Start.Line {
			t.Fatalf("Costs(shared) = %+v, want one entry for each version's copy", got)
		}
		single := onlyCostAt(t, costsOf(t, costCRD([2]string{"v1", rootRule})), costCRD([2]string{"v1", rootRule}), "startsWith")
		if single.Version != "" || !slices.Equal(single.Versions, []string{"v1"}) {
			t.Errorf("Costs(one version) = version %q, versions %q, want no version (the API server moves the schema to spec.validation) and v1 as its versions", single.Version, single.Versions)
		}
		for _, c := range got {
			if !slices.Equal(c.Versions, []string{"v1", "v2"}) {
				t.Errorf("Costs(shared) versions = %q, want both, which share the schema", c.Versions)
			}
			if c.Version != "" || c.FieldPath != "spec.validation.openAPIV3Schema.x-kubernetes-validations[0].rule" || c.Cost != single.Cost || c.Total != single.Cost {
				t.Errorf("Costs(shared) = %+v, want no version, the path in spec.validation, cost and total %d", c, single.Cost)
			}
		}
	})
	t.Run("per version", func(t *testing.T) {
		t.Parallel()
		data := costCRD([2]string{"v1", rootRule}, [2]string{"v2", v2})
		costs := costsOf(t, data)
		if len(costs) != 3 {
			t.Fatalf("Costs(per version) = %+v, want 3 entries", costs)
		}
		sums := map[string]uint64{}
		for _, c := range costs {
			sums[c.Version] += c.Cost
		}
		for _, c := range costs {
			if !slices.Equal(c.Versions, []string{c.Version}) {
				t.Errorf("Costs(per version) versions = %q, want its own, %q", c.Versions, c.Version)
			}
			wantPath := map[string]string{"v1": "spec.versions[0].", "v2": "spec.versions[1]."}[c.Version]
			if wantPath == "" || !strings.HasPrefix(c.FieldPath, wantPath) {
				t.Errorf("Costs(per version) = %+v, want version v1 or v2 with its path", c)
			}
			if c.Total != sums[c.Version] {
				t.Errorf("Costs(per version) %s total = %d, want the sum of its version's costs %d", c.FieldPath, c.Total, sums[c.Version])
			}
		}
		if sums["v1"] == sums["v2"] {
			t.Errorf("Costs(per version) totals = %v, want them different", sums)
		}
	})
}

// TestCostsMostExpensive checks that MostExpensive names the expressions that
// add the most to the total, the most expensive first, and leaves out those
// under 1% of the total's budget, as the API server's TotalCost does, though
// they count in the total.
func TestCostsMostExpensive(t *testing.T) {
	t.Parallel()
	const schema = `          x-kubernetes-validations:
            - rule: "self.cheap > 0"
          properties:
            cheap:
              type: integer
            few:
              type: array
              maxItems: 2000
              items:
                type: string
                maxLength: 1000
                x-kubernetes-validations:
                  - rule: "self.matches('^a.*$')"
            many:
              type: array
              maxItems: 10000
              items:
                type: string
                maxLength: 1000
                x-kubernetes-validations:
                  - rule: "self.matches('^b.*$')"
`
	data := costCRD([2]string{"v1", schema})
	costs := costsOf(t, data)
	var sum uint64
	var want []check.ExpressionCost
	for _, c := range costs {
		sum += c.Cost
		if c.Cost >= c.TotalBudget/100 {
			want = append(want, check.ExpressionCost{FieldPath: c.FieldPath, Cost: c.Cost})
		}
	}
	slices.SortFunc(want, func(a, b check.ExpressionCost) int { return cmp.Compare(b.Cost, a.Cost) })
	cheap := onlyCostAt(t, costs, data, "self.cheap")
	if cheap.Cost >= cheap.TotalBudget/100 || len(want) != 2 {
		t.Fatalf("Costs() = %+v, want one rule under 1%% of the total's budget and two over", costs)
	}
	for _, c := range costs {
		if c.Total != sum {
			t.Errorf("Costs() total = %d, want %d, the sum of every expression with the cheap one", c.Total, sum)
		}
		var got []check.ExpressionCost
		for _, m := range c.MostExpensive {
			got = append(got, check.ExpressionCost{FieldPath: m.FieldPath, Cost: m.Cost})
			if m.Node != "few[*]" && m.Node != "many[*]" {
				t.Errorf("Costs() most expensive %+v, want its node few[*] or many[*]", m)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("Costs() most expensive = %+v, want %+v", got, want)
		}
	}
}

// TestCostsUnestimated checks the CRD expressions the API server estimates
// no cost for: one that does not compile, the messageExpression of a rule that
// does not compile, a messageExpression that does not compile; the rules of a
// schema that is not structural and of a node with a schema error, while the
// others of that schema are estimated.
func TestCostsUnestimated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		schema string
		want   map[string]check.CostKind // the kind of the expression on the line that holds the key
	}{
		{"not compiled", `          x-kubernetes-validations:
            - rule: "self.nope > 0"
              messageExpression: "'fine'"
            - rule: "self.a > 0"
              messageExpression: "1"
            - rule: "self.a > 1"
          properties:
            a:
              type: integer
`, map[string]check.CostKind{"nope": check.CostNotCompiled, "'fine'": check.CostNotCompiled, "self.a > 0": check.CostEstimated, `"1"`: check.CostNotCompiled, "self.a > 1": check.CostEstimated}},
		{"schema not structural", `          x-kubernetes-validations:
            - rule: "self.a > 0"
          properties:
            a:
              type: integer
            l:
              type: array
              items:
                x-kubernetes-validations:
                  - rule: "true"
`, map[string]check.CostKind{"self.a > 0": check.CostSchemaNotCompiled, `"true"`: check.CostSchemaNotCompiled}},
		{"node with a schema error", `          x-kubernetes-validations:
            - rule: "self.a > 0"
          properties:
            a:
              type: integer
            l:
              type: array
              uniqueItems: true
              items:
                type: string
              x-kubernetes-validations:
                - rule: "self.size() > 0"
                  messageExpression: "'empty'"
`, map[string]check.CostKind{"self.a > 0": check.CostSchemaNotCompiled, "self.size() > 0": check.CostSchemaNotCompiled, "'empty'": check.CostSchemaNotCompiled}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := costCRD([2]string{"v1", tt.schema})
			costs := costsOf(t, data)
			if len(costs) != len(tt.want) {
				t.Errorf("Costs(%s) = %d entries, want %d: %+v", tt.name, len(costs), len(tt.want), costs)
			}
			for text, kind := range tt.want {
				got := onlyCostAt(t, costs, data, text)
				if got.Kind != kind {
					t.Errorf("Costs(%s) at %q = %s, want %s", tt.name, text, got.Kind, kind)
				}
				if kind != check.CostEstimated && (got.Cost != 0 || got.Budget != 0 || got.Total != 0 || got.Causes != nil || got.MostExpensive != nil) {
					t.Errorf("Costs(%s) at %q = %+v, want no number", tt.name, text, got)
				}
			}
		})
	}
}

// TestCostsFileOrder checks that the expressions of a file with a policy and
// a CRD are answered in file order, each with its range.
func TestCostsFileOrder(t *testing.T) {
	t.Parallel()
	data := costlyValidating + "---\n" + costCRD([2]string{"v1", messageRule})
	costs := costsOf(t, data)
	if len(costs) != 9 {
		t.Fatalf("Costs() = %d entries, want 9 (7 of the policy, 2 of the CRD): %+v", len(costs), costs)
	}
	for i, c := range costs {
		if i > 0 && (c.Start.Line < costs[i-1].Start.Line || c.Start.Line == costs[i-1].Start.Line && c.Start.Column <= costs[i-1].Start.Column) {
			t.Errorf("Costs()[%d] at %+v, after %+v: want file order", i, c.Start, costs[i-1].Start)
		}
		if c.End.Line < c.Start.Line || c.End.Line == c.Start.Line && c.End.Column <= c.Start.Column {
			t.Errorf("Costs()[%d] range %+v-%+v, want the expression's", i, c.Start, c.End)
		}
	}
}

// TestCostsPolicies checks that every expression of a policy is answered
// with its kind and no number at every supported version, as the API server
// estimates no cost for one (beside TestPolicyNoCost): a ValidatingAdmissionPolicy's
// and a MutatingAdmissionPolicy's as policy, and a Kyverno ValidatingPolicy's
// as kyverno while kyverno is on, and not at all while it is off, as Check
// leaves it alone.
func TestCostsPolicies(t *testing.T) {
	t.Parallel()
	const kyvernoPolicy = `apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: costly
spec:
  matchConditions:
    - name: costly
      expression: "` + costlyAll + `"
  variables:
    - name: costly
      expression: "` + costlyAll + `"
  validations:
    - expression: "` + costlyAll + `"
      messageExpression: "` + costlyMsg + `"
`
	tests := []struct {
		name string
		doc  string
		on   feature.Set
		want int
		kind check.CostKind
	}{
		{"validating", costlyValidating, 0, 7, check.CostPolicy},
		{"mutating", costlyMutating, 0, 3, check.CostPolicy},
		{"kyverno", kyvernoPolicy, feature.Of(feature.Kyverno), 4, check.CostKyverno},
		{"kyverno off", kyvernoPolicy, 0, 0, ""},
	}
	for _, v := range kubeversion.Supported() {
		for _, tt := range tests {
			t.Run(v.String()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				got := costsOf(t, tt.doc, check.WithVersion(v), check.WithFeatures(tt.on))
				if len(got) != tt.want {
					t.Errorf("Costs(%s, %s) = %d entries, want %d: %+v", tt.name, v, len(got), tt.want, got)
				}
				for _, c := range got {
					if c.Kind != tt.kind || c.Version != "" || c.FieldPath == "" || c.Cost != 0 || c.Budget != 0 || c.Total != 0 || c.TotalBudget != 0 || c.MostExpensive != nil || c.Causes != nil {
						t.Errorf("Costs(%s, %s) = %+v, want %s with its field path and no number", tt.name, v, c, tt.kind)
					}
				}
			})
		}
	}
}

// cancelAt is a context that reports context.Canceled from its n-th Err call
// on, so a test can cancel at an exact point without timing.
type cancelAt struct {
	context.Context
	n, calls int
}

func (c *cancelAt) Err() error {
	c.calls++
	if c.calls >= c.n {
		return context.Canceled
	}
	return nil
}

// TestCostsCancelled cancels Costs at every point it looks at its context,
// and checks that it stops with the context's error and no costs.
func TestCostsCancelled(t *testing.T) {
	t.Parallel()
	data := []byte(costCRD([2]string{"v1", listsRule + "                tags:\n                  type: array\n                  items:\n                    type: string\n                    x-kubernetes-validations:\n                      - rule: \"self.size() < 5\"\n"}))
	counter := &cancelAt{Context: context.Background(), n: 1 << 30}
	if got, err := check.Costs(counter, "c.yaml", data); err != nil || len(got) != 2 {
		t.Fatalf("Costs() = %+v, %v, want 2 entries", got, err)
	}
	for n := 1; n <= counter.calls; n++ {
		got, err := check.Costs(&cancelAt{Context: context.Background(), n: n}, "c.yaml", data)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("Costs(cancelled at check %d of %d) = %+v, %v, want no costs and context.Canceled", n, counter.calls, got, err)
		}
	}
}

// hoverCRD is the CustomResourceDefinition of the hover bench of
// internal/lsp (bigCRD): about lines lines, a rule on every group of
// properties, about every 35 lines, and a wrong one in every tenth group.
func hoverCRD(lines int) string {
	var b strings.Builder
	b.WriteString(crdHead + `    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
`)
	for i := 0; strings.Count(b.String(), "\n") < lines-10; i++ {
		rule := fmt.Sprintf("self.size() >= 0 && self.a%d.size() < 10 && self.b%d.all(x, x > 0)", i, i)
		if i%10 == 7 {
			rule = fmt.Sprintf("self.a%d.nope > 0", i)
		}
		fmt.Fprintf(&b, `                group%d:
                  type: object
                  description: Group number %d.
                  properties:
                    a%d:
                      type: string
                      maxLength: 20
                    b%d:
                      type: array
                      maxItems: 10
                      items:
                        type: integer
                    c:
                      type: string
                      description: A free-form note.
                    d:
                      type: integer
                      minimum: 0
                      maximum: 100
                    e:
                      type: object
                      properties:
                        f:
                          type: string
                        g:
                          type: boolean
                  x-kubernetes-validations:
                    - rule: "%s"
                      message: group %d is invalid
`, i, i, i, i, rule, i)
	}
	return b.String()
}

// unboundedListsCRD is a CustomResourceDefinition of about 1,000 lines whose
// 30 lists of objects have no maxItems and whose strings no maxLength, each
// with a rule on the list that reads every item and one on its items.
func unboundedListsCRD() []byte {
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, `            l%d:
              type: array
              x-kubernetes-validations:
                - rule: "self.all(x, x.name != '' && x.tags.all(t, t.size() < 64))"
                  message: every item needs a name and short tags
              items:
                type: object
                x-kubernetes-validations:
                  - rule: "self.tags.exists(t, t == self.name)"
                    messageExpression: "'no tag ' + self.name"
                properties:
                  name:
                    type: string
                  tags:
                    type: array
                    items:
                      type: string
                  note:
                    type: string
                    maxLength: 100
                  size:
                    type: integer
                    minimum: 0
                  labels:
                    type: object
                    additionalProperties:
                      type: string
                  owner:
                    type: object
                    properties:
                      name:
                        type: string
                        maxLength: 63
`, i)
	}
	return []byte(costCRD([2]string{"v1", "          properties:\n" + b.String()}))
}

// BenchmarkCosts measures Costs on the 1,000-line CRD of the hover bench and on
// a CRD of about 1,000 lines whose rules read unbounded lists.
func BenchmarkCosts(b *testing.B) {
	for _, bc := range []struct {
		name string
		data []byte
	}{
		{"hover-crd", []byte(hoverCRD(1000))},
		{"unbounded-lists", unboundedListsCRD()},
	} {
		b.Run(bc.name, func(b *testing.B) {
			got, err := check.Costs(context.Background(), "b.yaml", bc.data)
			if err != nil || len(got) == 0 {
				b.Fatalf("Costs() = %d entries, %v", len(got), err)
			}
			b.Logf("input: %d lines, %d expressions", strings.Count(string(bc.data), "\n"), len(got))
			b.SetBytes(int64(len(bc.data)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := check.Costs(context.Background(), "b.yaml", bc.data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
