package check_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// costlyCRD is a CRD of about 4,000 lines with cheap rules on 100 objects and a
// list of unbounded strings with rules (and another with one rule) that are over the per-expression budget.
// listExtra and itemsExtra are lines to add to the schemas of the list and its
// items.
func costlyCRD(listExtra, itemsExtra string, rules int) []byte {
	var b strings.Builder
	for i := range 100 {
		fmt.Fprintf(&b, "            f%d:\n              type: object\n              properties:\n", i)
		for j := range 9 {
			fmt.Fprintf(&b, "                g%d:\n                  type: string\n                  maxLength: 10\n", j)
		}
		b.WriteString("              x-kubernetes-validations:\n                - rule: \"self.g0 != ''\"\n")
	}
	b.WriteString("            list:\n              type: array\n" + listExtra + "              items:\n                type: string\n" + itemsExtra + "                x-kubernetes-validations:\n")
	for i := range rules {
		fmt.Fprintf(&b, "                  - rule: self.matches('^a%d.*$')\n", i)
	}
	b.WriteString("            other:\n              type: array\n              items:\n                type: string\n                x-kubernetes-validations:\n                  - rule: self.matches('^b.*$')\n")
	return []byte(crdHead + crdVersion("v1", b.String()))
}

func countCost(ds []check.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if strings.Contains(d.Message, "estimated cost") {
			n++
		}
	}
	return n
}

func TestCheckNoCostUnderSchemaError(t *testing.T) {
	t.Parallel()
	const uniq = "              uniqueItems: true\n"
	for _, c := range []struct {
		name        string
		list, items string // lines added to their schemas
		want        int    // the cost problems
	}{
		{"no schema error", "", "", 4},
		{"error in the schema of the rules", "", "                uniqueItems: true\n", 1},
		{"error above the rules", uniq, "", 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "c.yaml", costlyCRD(c.list, c.items, 3))
			if err != nil {
				t.Fatalf("Check(%s) error = %v", c.name, err)
			}
			if n := countCost(got); n != c.want {
				t.Errorf("Check(%s) has %d cost problems, want %d: %v", c.name, n, c.want, got)
			}
		})
	}
}

// TestCheckRootNullableKeepsRootCost checks that the root errors the API server
// adds outside the schema walk do not hide the cost of the root's rules.
func TestCheckRootNullableKeepsRootCost(t *testing.T) {
	t.Parallel()
	src := crdHead + `    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          nullable: true
          x-kubernetes-validations:
            - rule: self.list.all(x, x.matches('^r.*$'))
          properties:
            list:
              type: array
              items:
                type: string
            other:
              type: array
              items:
                type: string
                x-kubernetes-validations:
                  - rule: self.matches('^b.*$')
`
	got, err := check.Check(context.Background(), "c.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check(nullable root) error = %v", err)
	}
	found := false
	for _, d := range got {
		if strings.Contains(d.Message, "estimated cost") && d.Start.Line == 20 {
			found = true
		}
	}
	if !found {
		t.Errorf("Check(nullable root) has no cost problem on the root rule (line 20): %v", got)
	}
}

// TestCheckRootSchemaErrorHidesRootCost checks that a schema error the walk
// finds at the root, as the API server's, hides the cost of the root's rules.
func TestCheckRootSchemaErrorHidesRootCost(t *testing.T) {
	t.Parallel()
	src := crdHead + `    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-list-type: atomic
          x-kubernetes-validations:
            - rule: self.list.all(x, x.matches('^r.*$'))
          properties:
            list:
              type: array
              items:
                type: string
            other:
              type: array
              items:
                type: string
                x-kubernetes-validations:
                  - rule: self.matches('^b.*$')
`
	got, err := check.Check(context.Background(), "c.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check(root list type) error = %v", err)
	}
	for _, d := range got {
		if strings.Contains(d.Message, "estimated cost") && d.Start.Line == 20 {
			t.Errorf("Check(root list type) has a cost problem on the root rule: %v", d)
		}
	}
}

// TestCheckCostAdviceSameForSameRules checks that rules of one schema that cost
// the same get the same advice, however many there are: sizing keeps what it
// learns for one rule and narrows the search for the next with it.
func TestCheckCostAdviceSameForSameRules(t *testing.T) {
	t.Parallel()
	got, err := check.Check(context.Background(), "c.yaml", costlyCRD("", "", 20))
	if err != nil {
		t.Fatalf("Check(20 rules) error = %v", err)
	}
	var want string
	n := 0
	for _, d := range got {
		if !strings.Contains(d.Message, "estimated cost") || strings.Contains(d.Message, "All rules together") || len(d.Fixes) == 0 || !strings.HasPrefix(d.Fixes[0].FieldPath, "list") {
			continue
		}
		n++
		if want == "" {
			want = d.Message
		}
		if d.Message != want {
			t.Errorf("Check(20 rules) message at line %d = %q, want the first rule's %q", d.Start.Line, d.Message, want)
		}
	}
	if n < 19 {
		t.Errorf("Check(20 rules) has %d cost problems on the list rules, want at least 19: %v", n, got)
	}
}

// BenchmarkCheckCostErrors measures checking a large CRD with rules over their
// cost budget.
func BenchmarkCheckCostErrors(b *testing.B) {
	data := costlyCRD("", "", 20)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := check.Check(context.Background(), "b.yaml", data); err != nil {
			b.Fatal(err)
		}
	}
}

// twoLists is a CRD with lists l0 and l1 with no maxItems, of the item types
// given, each with the same two costly rules on its items.
func twoLists(types ...string) []byte {
	const rule = "[1,2,3,4,5,6,7,8,9,10].all(x, x > 0) && [1,2,3,4,5,6,7,8,9,10].all(x, x > 0)"
	var b strings.Builder
	for i, typ := range types {
		fmt.Fprintf(&b, "            l%d:\n              type: array\n              items:\n                type: %s\n                x-kubernetes-validations:\n                  - rule: %q\n", i, typ, rule)
	}
	return []byte(crdHead + crdVersion("v1", b.String()))
}

// TestCheckCostPerNodeSchema checks that rules without `self` that are the same
// on lists of different item types are costed for each type, whichever comes
// first, as the API server does: 127.9M for strings, 191.9M for integers.
func TestCheckCostPerNodeSchema(t *testing.T) {
	t.Parallel()
	for _, types := range [][]string{{"string", "integer"}, {"integer", "string"}} {
		t.Run(strings.Join(types, "-"), func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "c.yaml", twoLists(types...))
			if err != nil {
				t.Fatalf("Check(%v) error = %v", types, err)
			}
			var all strings.Builder
			for _, d := range got {
				all.WriteString(d.Message + "\n")
			}
			for _, want := range []string{"cost 127.9M", "cost 191.9M", "cost an estimated 319.8M"} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("Check(%v) = %q, want it to contain %q", types, all.String(), want)
				}
			}
		})
	}
}

// TestCheckCostSameRuleDifferentSchema checks that a rule that reads `self` is
// costed for the schema it is on: the same rule on lists whose items have
// different limits costs differently.
func TestCheckCostSameRuleDifferentSchema(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i, max := range []int{1000, 100000} {
		fmt.Fprintf(&b, "            l%d:\n              type: array\n              items:\n                type: string\n                maxLength: %d\n                x-kubernetes-validations:\n                  - rule: \"self.matches('^a.*$') && self.matches('^b.*$')\"\n", i, max)
	}
	got, err := check.Check(context.Background(), "c.yaml", []byte(crdHead+crdVersion("v1", b.String())))
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}
	costs := map[string]bool{}
	for _, d := range got {
		if strings.Contains(d.Message, "estimated cost") {
			costs[strings.Fields(d.Message)[2]] = true
		}
	}
	if len(costs) != 2 {
		t.Errorf("Check costs = %v, want two different ones for the two lists: %v", costs, got)
	}
}

// TestCheckCostWithEveryLimitSet checks that a rule over its budget with a
// limit on every string and list is still reported, though nothing is missing
// that would warn of it.
func TestCheckCostWithEveryLimitSet(t *testing.T) {
	t.Parallel()
	got, err := check.Check(context.Background(), "c.yaml", costlyCRD("              maxItems: 100000\n", "                maxLength: 100000\n", 20))
	if err != nil {
		t.Fatalf("Check(limits set) error = %v", err)
	}
	if countCost(got) == 0 {
		t.Errorf("Check(limits set) = %v, want a cost problem", got)
	}
}

// TestCheckRootStatusSubresourceKeepsRootCost pins the texts of the API
// server's status-subresource errors at the root of a version's schema, which
// isVersionRootMessage matches: if a Kubernetes upgrade rewords them, the cost
// of the root's rules is hidden and this test fails. The root type error only
// comes with the walk's "must be object at the root", which stops the API
// server from costing any rule, so TestStatusRootTypeIsNotSchemaError checks
// that one.
func TestCheckRootStatusSubresourceKeepsRootCost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		schema   string
		want     string
		wantCost bool
	}{
		{"root type", `          type: array
          items:
            type: array
            items:
              type: string
          x-kubernetes-validations:
            - rule: self.all(l, l.all(x, x.matches('^r.*$')))
`, `only "object" is allowed as the type at the root of the schema if the status subresource is enabled`, false},
		{"disallowed root fields", `          type: object
          maxProperties: 3
          x-kubernetes-validations:
            - rule: self.list.all(x, x.matches('^r.*$'))
          properties:
            list:
              type: array
              items:
                type: string
`, "fields are allowed at the root of the schema if the status subresource is enabled", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := crdHead + `    - name: v1
      served: true
      storage: true
      subresources:
        status: {}
      schema:
        openAPIV3Schema:
` + c.schema
			got, err := check.Check(context.Background(), "c.yaml", []byte(src))
			if err != nil {
				t.Fatalf("Check(%s) error = %v", c.name, err)
			}
			var status, cost bool
			for _, d := range got {
				status = status || strings.Contains(d.Message, c.want)
				cost = cost || strings.Contains(d.Message, "estimated cost")
			}
			if !status {
				t.Errorf("Check(%s) has no status subresource problem containing %q: %v", c.name, c.want, got)
			}
			if c.wantCost && !cost {
				t.Errorf("Check(%s) has no cost problem on the root rule: %v", c.name, got)
			}
		})
	}
}

// TestCheckUntypedNodeGetsNoCost checks that a list whose items have no valid
// type, with the same rules as a typed list, gets no cost problem of its own.
// Both lists have a fixed maxItems, so their nodes share remembered costs and
// the untyped one could be given the typed one's.
func TestCheckUntypedNodeGetsNoCost(t *testing.T) {
	t.Parallel()
	src := bytes.ReplaceAll(twoLists("string", "bogus"),
		[]byte("              type: array\n"), []byte("              type: array\n              maxItems: 1000000\n"))
	got, err := check.Check(context.Background(), "c.yaml", src)
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}
	untypedLine := 0
	for i, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, "l1:") {
			untypedLine = i + 1
		}
	}
	if untypedLine == 0 {
		t.Fatal("test setup: no untyped list in the CRD")
	}
	typed := 0
	for _, d := range got {
		if !strings.Contains(d.Message, "estimated cost") {
			continue
		}
		if d.Start.Line >= untypedLine {
			t.Errorf("Check cost problem %q at line %d, want none on the untyped list from line %d", d.Message, d.Start.Line, untypedLine)
		} else {
			typed++
		}
	}
	if typed == 0 {
		t.Errorf("Check = %v, want a cost problem on the typed list", got)
	}
}
