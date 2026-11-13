package examples

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel/model"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/apiserver/pkg/cel/common"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// specCRD is a namespaced CRD of widgets, version v1, whose spec has the
// schema spec, written as a YAML flow mapping.
func specCRD(spec string) string {
	return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec: ` + spec + "\n"
}

// crdRefuses starts the reason of a rule for which the API server refuses
// the CRD.
const crdRefuses = refusedForRule

// specWidget is a Widget named name with spec, a YAML flow mapping.
func specWidget(name, spec string) string {
	return "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: " + name + "}\nspec: " + spec + "\n"
}

// ruleResults reads the CRD crd, finds its examples in the file of docs and
// runs its rules on them.
func ruleResults(t *testing.T, crd string, docs ...string) []Result {
	t.Helper()
	crds := Read(t.Context(), crdFile, []byte(crd), kubeversion.Default())
	if len(crds) != 1 {
		t.Fatalf("Read(%s) = %d CRDs, want 1", crd, len(crds))
	}
	rules, err := crds[0].Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(strings.Join(docs, "---\n")))
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), objs); err != nil {
		t.Fatal(err)
	}
	var c Cache
	results, err := c.Run(t.Context(), crdFile, rules, objs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return results
}

// String is a result as the tests compare it: the rule's index and
// example's line, the operation, the verdict and what it holds.
func (r Result) String() string {
	op := "create"
	if r.Update {
		op = "update"
	}
	parts := []string{fmt.Sprintf("%d@%d %s %s", r.Rule.Index, r.Example.Line, op, r.Verdict)}
	for _, f := range r.Refusals {
		parts = append(parts, "["+f.Path+"] "+f.Message)
	}
	for _, i := range r.Ignored {
		parts = append(parts, "ignored "+i)
	}
	for _, s := range []string{r.Reason, r.Error} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " | ")
}

func TestRules(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  scope: Namespaced
  versions:
    - name: v0
      served: false
      storage: false
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations: [{rule: "true"}]
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: self.metadata.name != ''
          properties:
            spec:
              type: object
              properties:
                ports:
                  type: array
                  items:
                    type: object
                    properties:
                      name: {type: string}
                    x-kubernetes-validations:
                      - rule: self.name != 'x'
                        messageExpression: "'bad ' + self.name"
                labels:
                  type: object
                  additionalProperties:
                    type: string
                    x-kubernetes-validations: [{rule: self != 'x'}]
                "a.b": {type: integer, x-kubernetes-validations: [{rule: self > 0}]}
              x-kubernetes-validations:
                - rule: has(self.ports)
                - rule: has(self.labels)
`
	crds := Read(t.Context(), crdFile, []byte("apiVersion: v1\nkind: ConfigMap\n---\n"+crd), kubeversion.Default())
	if len(crds) != 1 {
		t.Fatalf("Read = %d CRDs, want 1", len(crds))
	}
	rules, err := crds[0].Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rules {
		s := fmt.Sprintf("%s %q %d %q %d:%d-%d:%d", r.Version, r.Path, r.Index, r.Text, r.Start.Line, r.Start.Column, r.End.Line, r.End.Column)
		if r.MessageStart.Line > 0 {
			s += fmt.Sprintf(" message %d:%d", r.MessageStart.Line, r.MessageStart.Column)
		}
		got = append(got, s)
	}
	want := []string{
		`v1 "" 0 "self.metadata.name != ''" 27:21-27:45`,
		`v1 "spec.ports[]" 0 "self.name != 'x'" 39:31-39:47 message 40:45`,
		`v1 "spec.labels[*]" 0 "self != 'x'" 45:55-45:66`,
		`v1 "spec.a.b" 0 "self > 0" 46:74-46:82`,
		`v1 "spec" 0 "has(self.ports)" 48:25-48:40`,
		`v1 "spec" 1 "has(self.labels)" 49:25-49:41`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("Rules =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// #15: the line above a rule finds its results by its document, field
	// path and text, which an edit elsewhere in the file keeps.
	if r := rules[1]; r.Document != 1 || r.FieldPath != "spec.versions[1].schema.openAPIV3Schema.properties[spec].properties[ports].items.x-kubernetes-validations[0].rule" {
		t.Errorf("Rules()[1] document, field path = %d, %q, want 1 and the field path of its rule", r.Document, r.FieldPath)
	}
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := crds[0].Rules(ctx); err == nil {
			t.Error("Rules(cancelled) error = nil, want the context's")
		}
	})
}

// TestRun checks each kind of result a rule gives an example: allowed,
// refused with the API server's message, path, reason and messageExpression,
// once for each item of a list and value of a map, not run with its reason,
// and an error; creates and updates, optionalOldSelf, and the refusals
// ratcheting ignores.
func TestRun(t *testing.T) {
	t.Parallel()
	ports := `{type: object, properties: {ports: {type: array, items: {type: object, properties: {name: {type: string}}, x-kubernetes-validations: [{rule: "self.name.size() <= 3", message: name too long}]}}}}`
	for _, tc := range []struct {
		name string
		spec string // the schema of spec
		docs []string
		want []string
	}{
		{
			"allowed and refused, with the message",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size <= 10", message: at most 10}]}`,
			[]string{specWidget("a", "{size: 1}"), specWidget("b", "{size: 11}")},
			[]string{"0@1 create allowed", "0@6 create refused | [spec] spec: Invalid value: at most 10"},
		},
		{
			"no message",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size <= 10"}]}`,
			[]string{specWidget("a", "{size: 11}")},
			[]string{"0@1 create refused | [spec] spec: Invalid value: failed rule: self.size <= 10"},
		},
		{
			"a scalar's value in the message",
			`{type: object, properties: {size: {type: integer, x-kubernetes-validations: [{rule: "self <= 10", message: at most 10}]}}}`,
			[]string{specWidget("a", "{size: 11}")},
			[]string{"0@1 create refused | [spec.size] spec.size: Invalid value: 11: at most 10"},
		},
		{
			"messageExpression, reason and fieldPath",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size <= 10", messageExpression: "'size ' + (self.size > 100 ? 'far ' : '') + 'over 10'", reason: FieldValueForbidden, fieldPath: .size}]}`,
			[]string{specWidget("a", "{size: 11}")},
			[]string{"0@1 create refused | [spec.size] spec.size: Forbidden: size over 10"},
		},
		{
			"reason Required",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "has(self.size)", message: give a size, reason: FieldValueRequired}]}`,
			[]string{specWidget("a", "{}")},
			[]string{"0@1 create refused | [spec] spec: Required value: give a size"},
		},
		{
			"a messageExpression that fails gives the message",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size <= 10", message: at most 10, messageExpression: "string(1/0)"}]}`,
			[]string{specWidget("a", "{size: 11}")},
			[]string{"0@1 create refused | [spec] spec: Invalid value: at most 10"},
		},
		{
			"each item of a list",
			ports,
			[]string{specWidget("a", "{ports: [{name: http}, {name: dns}, {name: https}]}")},
			[]string{"0@1 create refused | [spec.ports[0]] spec.ports[0]: Invalid value: name too long | [spec.ports[2]] spec.ports[2]: Invalid value: name too long"},
		},
		{
			"each value of a map",
			`{type: object, properties: {labels: {type: object, additionalProperties: {type: string, x-kubernetes-validations: [{rule: "self != 'x'"}]}}}}`,
			[]string{specWidget("a", "{labels: {a: x, b: ok, c: x}}")},
			[]string{"0@1 create refused | [spec.labels[a]] spec.labels[a]: Invalid value: \"x\": failed rule: self != 'x' | [spec.labels[c]] spec.labels[c]: Invalid value: \"x\": failed rule: self != 'x'"},
		},
		{
			"the field absent",
			ports,
			[]string{specWidget("a", "{}"), specWidget("b", "{ports: []}")},
			[]string{"0@1 create not run | the rule's field is absent from the example", "0@6 create not run | the rule's field is absent from the example"},
		},
		{
			"no compile",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size > 'a'"}]}`,
			[]string{specWidget("a", "{size: 1}")},
			[]string{"0@1 create not run | " + crdRefuses + "found no matching overload for '_>_' applied to '(int, string)'"},
		},
		{
			"a messageExpression that does not compile",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size > 0", messageExpression: "self.size"}]}`,
			[]string{specWidget("a", "{size: 1}")},
			[]string{"0@1 create not run | " + crdRefuses + "messageExpression must evaluate to a string"},
		},
		{
			"oldSelf: a create, then an update",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size == oldSelf.size", message: immutable}]}`,
			[]string{specWidget("a", "{size: 1}"), specWidget("a", "{size: 1}"), specWidget("a", "{size: 2}")},
			[]string{"0@1 create not run | the example is not an update and the rule needs oldSelf", "0@6 update allowed", "0@11 update refused | [spec] spec: Invalid value: immutable"},
		},
		{
			"oldSelf after an example the schema refuses",
			`{type: object, required: [size], properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size == oldSelf.size", message: immutable}]}`,
			[]string{specWidget("a", "{}"), specWidget("a", "{size: 2}")},
			[]string{
				"0@1 create not run | the schema refuses the example (spec.size: Required value), and the API server then runs no rule",
				"0@6 create not run | the example is not an update and the rule needs oldSelf: the API server refuses the example before it, so kubectl apply -f creates this one",
			},
		},
		{
			"oldSelf where the update has no old value",
			`{type: object, properties: {size: {type: integer, x-kubernetes-validations: [{rule: "self == oldSelf", message: immutable}]}}}`,
			[]string{specWidget("a", "{}"), specWidget("a", "{size: 2}")},
			[]string{"0@1 create not run | the example is not an update and the rule needs oldSelf", "0@6 update not run | the rule needs oldSelf, and the update has no old value where the rule is"},
		},
		{
			"optionalOldSelf runs on a create and an update",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "!oldSelf.hasValue() || self.size >= oldSelf.value().size", message: no shrinking, optionalOldSelf: true}]}`,
			[]string{specWidget("a", "{size: 2}"), specWidget("a", "{size: 1}")},
			[]string{"0@1 create allowed", "0@6 update refused | [spec] spec: Invalid value: no shrinking"},
		},
		{
			// kubectl apply -f creates the second, as the first is refused
			// (the review of #9).
			"after an example a rule refuses, a create",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size < 10", message: under 10}]}`,
			[]string{specWidget("a", "{size: 20}"), specWidget("a", "{size: 20}")},
			[]string{"0@1 create refused | [spec] spec: Invalid value: under 10", "0@6 create refused | [spec] spec: Invalid value: under 10"},
		},
		{
			// The second is refused, so the object is still the first's,
			// and the third updates it; the fourth is refused for its
			// transition from the third.
			"after a refused update, an update from the example stored",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size < 10", message: under 10}, {rule: "self.size >= oldSelf.size", message: no shrinking}]}`,
			[]string{specWidget("a", "{size: 5}"), specWidget("a", "{size: 20}"), specWidget("a", "{size: 6}"), specWidget("a", "{size: 3}")},
			[]string{
				"0@1 create allowed", "0@6 update refused | [spec] spec: Invalid value: under 10", "0@11 update allowed", "0@16 update allowed",
				"1@1 create not run | the example is not an update and the rule needs oldSelf", "1@6 update allowed", "1@11 update allowed", "1@16 update refused | [spec] spec: Invalid value: no shrinking",
			},
		},
		{
			// The first is refused, the second created, the third refused,
			// and the fourth updates the second.
			"a chain of creates and updates",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size < 10", message: under 10}, {rule: "self.size >= oldSelf.size", message: no shrinking}]}`,
			[]string{specWidget("a", "{size: 20}"), specWidget("a", "{size: 5}"), specWidget("a", "{size: 30}"), specWidget("a", "{size: 4}")},
			[]string{
				"0@1 create refused | [spec] spec: Invalid value: under 10", "0@6 create allowed", "0@11 update refused | [spec] spec: Invalid value: under 10", "0@16 update allowed",
				"1@1 create not run | the example is not an update and the rule needs oldSelf",
				"1@6 create not run | the example is not an update and the rule needs oldSelf: the API server refuses the example before it, so kubectl apply -f creates this one",
				"1@11 update allowed", "1@16 update refused | [spec] spec: Invalid value: no shrinking",
			},
		},
		{
			"a runtime error",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "10 / self.size > 1"}]}`,
			[]string{specWidget("a", "{size: 0}")},
			[]string{"0@1 create error | spec: Invalid value: \"object\": division by zero evaluating rule: 10 / self.size > 1"},
		},
		{
			"an unknown field stops the rules",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size <= 10"}]}`,
			[]string{specWidget("a", "{size: 11, extra: 1}")},
			[]string{"0@1 create not run | the API server refuses the example while it decodes it, before its rules (kubectl asks for strict field validation): unknown field \"spec.extra\""},
		},
		{
			"an invalid value does not stop the rules",
			`{type: object, properties: {size: {type: integer, maximum: 5}}, x-kubernetes-validations: [{rule: "self.size <= 10"}]}`,
			[]string{specWidget("a", "{size: 11}")},
			[]string{"0@1 create refused | [spec] spec: Invalid value: failed rule: self.size <= 10"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, r := range ruleResults(t, specCRD(tc.spec), tc.docs...) {
				got = append(got, r.String())
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Run(%s) =\n%s\nwant\n%s", tc.name, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestRunCause checks what kind of reason a rule that does not run has,
// for the line above it to say in a few words (#15).
func TestRunCause(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, spec string
		docs       []string
		want       []Cause // of each result
	}{
		{
			"ran",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size < 10"}]}`,
			[]string{specWidget("a", "{size: 1}"), specWidget("b", "{size: 11}"), specWidget("c", "{size: 0}")},
			[]Cause{CauseNone, CauseNone, CauseNone},
		},
		{
			"the CRD refused for the rule",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size > 'a'"}]}`,
			[]string{specWidget("a", "{size: 1}")},
			[]Cause{CauseCRDRefused},
		},
		{
			"the example refused before the rules",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size < 10"}]}`,
			[]string{specWidget("a", "{size: 1, extra: 2}")},
			[]Cause{CauseExampleRefused},
		},
		{
			"not an update",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size == oldSelf.size"}]}`,
			[]string{specWidget("a", "{size: 1}")},
			[]Cause{CauseNotUpdate},
		},
		{
			"an update without an old value",
			`{type: object, properties: {a: {type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size == oldSelf.size"}]}}}`,
			[]string{specWidget("a", "{}"), specWidget("a", "{a: {size: 1}}")},
			[]Cause{CauseNotUpdate, CauseNoOldValue},
		},
		{
			"the field absent",
			`{type: object, properties: {a: {type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size < 10"}]}}}`,
			[]string{specWidget("a", "{}")},
			[]Cause{CauseAbsent},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			results := ruleResults(t, specCRD(tc.spec), tc.docs...)
			var got []Cause
			for _, r := range results {
				got = append(got, r.Cause)
				if (r.Cause == CauseNone) != (r.Verdict != VerdictNotRun) {
					t.Errorf("Run(%s) = %s with cause %d, want a cause exactly when it did not run", tc.name, r, r.Cause)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Run(%s) causes = %v, want %v: %v", tc.name, got, tc.want, results)
			}
		})
	}
}

// TestRunRoot checks a rule on the root of the object, whose path is "".
func TestRunRoot(t *testing.T) {
	t.Parallel()
	crd := strings.Replace(specCRD(`{type: object}`), "          type: object\n", "          type: object\n          x-kubernetes-validations: [{rule: \"self.metadata.name.startsWith('w')\"}]\n", 1)
	var got []string
	for _, r := range ruleResults(t, crd, specWidget("a", "{}"), specWidget("w", "{}")) {
		got = append(got, r.String()+" | ran "+strings.Join(r.Ran, ","))
	}
	want := []string{
		"0@1 create refused | [] <nil>: Invalid value: failed rule: self.metadata.name.startsWith('w') | ran ",
		"0@6 create allowed | ran ",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Run(root) =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// costCRD is a CRD whose spec has n rules that each look for spec.t in
// spec.s, of at most s and t characters, which the API server's cost model
// charges by the product of their lengths, whatever the time it takes, then
// one that costs little.
func costCRD(n, s, t int) string {
	var rules []string
	for i := range n {
		rules = append(rules, fmt.Sprintf(`{rule: "self.s.contains(self.t) || self.s.size() > %d"}`, i))
	}
	rules = append(rules, `{rule: "self.s.size() > 0"}`)
	return specCRD(fmt.Sprintf(`{type: object, properties: {s: {type: string, maxLength: %d}, t: {type: string, maxLength: %d}}, x-kubernetes-validations: [`, s, t) + strings.Join(rules, ", ") + `]}`)
}

// lengths is a Widget whose spec.s and spec.t have s and t characters.
func lengths(s, t int) string {
	return specWidget("a", "{s: "+strings.Repeat("a", s)+", t: "+strings.Repeat("a", t)+"}")
}

// TestRunCost checks the API server's cost limits through Run on a CRD the
// API server accepts (the review of #9): a rule over the limit of one call
// at runtime, though its estimated cost is within the limit of a rule, is an
// error with the API server's message, and every other rule that ran on the
// example is an error too, as the API server runs no further rule. A rule
// under the limits counts what it costs.
func TestRunCost(t *testing.T) {
	t.Parallel()
	spec := `{type: object, properties: {l: {type: array, maxItems: 600, items: {type: integer, minimum: 0, maximum: 1000}}}, x-kubernetes-validations: [{rule: "self.l.all(x, self.l.all(y, x + y >= 0))"}, {rule: "has(self.l)"}]}`
	list := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = strconv.Itoa(i)
		}
		return specWidget("a", "{l: ["+strings.Join(items, ", ")+"]}")
	}
	results := ruleResults(t, specCRD(spec), list(400))
	var got []string
	for _, r := range results {
		got = append(got, r.String())
	}
	want := []string{
		`0@1 create error | spec: Invalid value: "object": 'operation cancelled: actual cost limit exceeded': no further validation rules will be run due to call cost exceeds limit for rule: self.l.all(x, self.l.all(y, x + y >= 0))`,
		"1@1 create error | another rule of v1 hit a cost limit on this example, after which the API server runs no further rule, so whether this one runs depends on the order it visits the object's fields in",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Run(over the limit of a call) =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	under := ruleResults(t, specCRD(spec), list(100))
	if len(under) != 2 || under[0].Verdict != VerdictAllowed || under[0].cost < 30000 || under[1].Verdict != VerdictAllowed {
		t.Errorf("Run(under the limits) = %v, cost %d, want both allowed, the first costing about 70000", under, under[0].cost)
	}
}

// TestRunRefusedRules checks that a rule for which the API server refuses
// the whole CRD is not run on any example, with what the engine's check of
// the CRD says, as the API server validates no object with it (the review
// of #9): one over its estimated cost limit, an invalid fieldPath, oldSelf
// where the schema cannot correlate old values, optionalOldSelf on a rule
// without oldSelf, and one that does not compile. The other rules of the
// CRD still run.
func TestRunRefusedRules(t *testing.T) {
	t.Parallel()
	const fine = `{rule: "has(self.size)", message: give a size}`
	for _, tc := range []struct {
		name string
		spec string // the schema of spec, whose last rule is the one refused
		want string // a part of the message
	}{
		{
			"estimated cost",
			`{type: object, properties: {size: {type: integer}, tags: {type: array, maxItems: 100000, items: {type: string, maxLength: 1000}}}, x-kubernetes-validations: [` + fine + `, {rule: "self.tags.all(x, x.matches('^a'))"}]}`,
			"is over the per-expression budget of 10M for a rule",
		},
		{
			"an invalid fieldPath",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [` + fine + `, {rule: "self.size < 10", fieldPath: .nope}]}`,
			"nope",
		},
		{
			"oldSelf where old values do not correlate",
			`{type: object, properties: {size: {type: integer}, tags: {type: array, items: {type: object, properties: {n: {type: integer}}, x-kubernetes-validations: [{rule: "self.n == oldSelf.n"}]}}}, x-kubernetes-validations: [` + fine + `]}`,
			"oldSelf cannot be used on the uncorrelatable portion of the schema",
		},
		{
			"optionalOldSelf without oldSelf",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [` + fine + `, {rule: "self.size < 10", optionalOldSelf: true}]}`,
			"may not be set if oldSelf is not used in rule",
		},
		{
			"no compile",
			`{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [` + fine + `, {rule: "self.size > 'a'"}]}`,
			"no matching overload",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			results := ruleResults(t, specCRD(tc.spec), specWidget("a", "{size: 1}"), specWidget("b", "{}"))
			var refused, others []Result
			for _, r := range results {
				if strings.HasPrefix(r.Reason, crdRefuses) {
					refused = append(refused, r)
				} else {
					others = append(others, r)
				}
			}
			if len(refused) != 2 {
				t.Fatalf("Run(%s) = %v, want the refused rule not run on both examples", tc.name, results)
			}
			for _, r := range refused {
				if r.Verdict != VerdictNotRun || !strings.Contains(r.Reason, tc.want) || !r.Expected {
					t.Errorf("Run(%s), the refused rule = %s, want not run with %q, as expected", tc.name, r, tc.want)
				}
			}
			for _, r := range others {
				if strings.HasPrefix(r.Reason, crdRefuses) || r.Verdict == VerdictNotRun && r.Rule.Text == "has(self.size)" {
					t.Errorf("Run(%s), another rule = %s, want it run", tc.name, r)
				}
			}
		})
	}
}

// TestRunRefusedTotal checks that when the estimated cost of all the rules
// of a CRD's schema together is over the API server's budget for one schema,
// which the engine's check places on one rule, every rule of the CRD is not
// run, as the API server refuses the CRD for them together (the review of
// #9); a rule over the limit of a rule alone stops only that rule.
func TestRunRefusedTotal(t *testing.T) {
	t.Parallel()
	var rules []string
	for i := range 45 {
		rules = append(rules, fmt.Sprintf(`{rule: "self.size() < %d"}`, i+5))
	}
	total := `{type: object, properties: {l: {type: array, maxItems: 1000, items: {type: array, maxItems: 1000, items: {type: string, maxLength: 10, x-kubernetes-validations: [` + strings.Join(rules, ", ") + `]}}}}}`
	results := ruleResults(t, specCRD(total), specWidget("a", "{l: [[a, b]]}"))
	if len(results) != 45 {
		t.Fatalf("Run(total) = %d results, want 45", len(results))
	}
	for _, r := range results {
		if r.Verdict != VerdictNotRun || !strings.HasPrefix(r.Reason, refusedForTotal) || !strings.Contains(r.Reason, "of all rules together is over the budget of 100M") {
			t.Errorf("Run(total), rule %q = %s, want not run for the total", r.Rule.Text, r)
		}
	}
	one := `{type: object, properties: {size: {type: integer}, tags: {type: array, maxItems: 100000, items: {type: string, maxLength: 1000}}}, x-kubernetes-validations: [{rule: "has(self.size)"}, {rule: "self.tags.all(x, x.matches('^a'))"}]}`
	var got []string
	for _, r := range ruleResults(t, specCRD(one), specWidget("a", "{size: 1}")) {
		got = append(got, fmt.Sprintf("%d %s %t", r.Rule.Index, r.Verdict, strings.HasPrefix(r.Reason, refusedForRule)))
	}
	if want := []string{"0 allowed false", "1 not run true"}; !slices.Equal(got, want) {
		t.Errorf("Run(one rule over its limit) = %q, want %q", got, want)
	}
}

// TestRunCompileReason checks that a rule that does not compile gets the
// reason of a rule the API server refuses the CRD for when the engine's
// check of the CRD reports nothing on it, as when another rule's fieldPath
// stops it (the review of #9).
func TestRunCompileReason(t *testing.T) {
	t.Parallel()
	spec := `{type: object, properties: {size: {type: integer}}, x-kubernetes-validations: [{rule: "self.size < 10", fieldPath: .nope}, {rule: "self.size > 'a'"}]}`
	for _, r := range ruleResults(t, specCRD(spec), specWidget("a", "{size: 1}")) {
		if r.Verdict != VerdictNotRun || !strings.HasPrefix(r.Reason, refusedForRule) {
			t.Errorf("Run, rule %q = %s, want not run as refused with the CRD", r.Rule.Text, r)
		}
	}
}

// TestOverBudget checks that when a rule hits a cost limit on an example,
// after which the API server runs no further rule, or the rules of an
// example together cost more than the budget of one object, each rule that
// ran on it is an error, and a rule that did not run stays as it is.
// TestRunCost goes through the first case with Run; the budget of an object
// is tested here alone, as the rules of a CRD the API server accepts that
// cost more than it together take about a minute under -race.
func TestOverBudget(t *testing.T) {
	t.Parallel()
	rule := &Rule{Version: "v1"}
	stopped, costly, fine := &Object{}, &Object{}, &Object{}
	results := []Result{
		{Rule: rule, Example: stopped, Verdict: VerdictError, Error: "call cost exceeds limit", Ran: []string{"spec"}, stopped: true},
		{Rule: rule, Example: stopped, Verdict: VerdictAllowed, Ran: []string{"spec"}},
		{Rule: rule, Example: stopped, Verdict: VerdictNotRun, Reason: "absent"},
		{Rule: rule, Example: costly, Verdict: VerdictAllowed, Ran: []string{"spec"}, cost: 6000000},
		{Rule: rule, Example: costly, Verdict: VerdictRefused, Ran: []string{"spec"}, cost: 5000000},
		{Rule: rule, Example: fine, Verdict: VerdictAllowed, Ran: []string{"spec"}, cost: 9000000},
	}
	overBudget(results)
	var got []string
	for _, r := range results {
		got = append(got, r.Verdict.String()+" | "+r.Error+r.Reason)
	}
	const total = "error | the rules of v1 together cost 11000000 on this example, more than the API server's budget of 10000000 for one object, so it stops at the rule that runs it out, which depends on the order it visits the object's fields in"
	want := []string{
		"error | call cost exceeds limit",
		"error | another rule of v1 hit a cost limit on this example, after which the API server runs no further rule, so whether this one runs depends on the order it visits the object's fields in",
		"not run | absent",
		total, total,
		"allowed | ",
	}
	if !slices.Equal(got, want) {
		t.Errorf("overBudget =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestRunIgnored checks that a refusal the API server's ratcheting ignores
// on an update is kept in Ignored, by running a rule on an update Check
// paired with an example the rule refuses, which Run does not do.
func TestRunIgnored(t *testing.T) {
	t.Parallel()
	crds := Read(t.Context(), crdFile, []byte(specCRD(`{type: object, properties: {labels: {type: object, maxProperties: 10, additionalProperties: {type: string, maxLength: 10, x-kubernetes-validations: [{rule: "self != 'x'"}]}}}}`)), kubeversion.Default())
	rules, err := crds[0].Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(specWidget("a", "{labels: {a: x}}")+"---\n"+specWidget("a", "{labels: {a: x, b: x}}")))
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), objs); err != nil {
		t.Fatal(err)
	}
	oc, err := run(t.Context(), rules[0].prepare(), objs[1])
	if err != nil {
		t.Fatal(err)
	}
	want := outcome{verdict: VerdictRefused, refusals: []Refusal{{Path: "spec.labels[b]", Message: `spec.labels[b]: Invalid value: "x": failed rule: self != 'x'`}}, ignored: []string{`spec.labels[a]: Invalid value: "x": failed rule: self != 'x'`}}
	if oc.verdict != want.verdict || !slices.Equal(oc.refusals, want.refusals) || !slices.Equal(oc.ignored, want.ignored) {
		t.Errorf("run(update) = %+v, want %+v", oc, want)
	}
}

// TestExpected checks whether each result is what its example expects: an
// example that expects to be allowed is wrong at a rule that refuses it or
// fails on it; one that expects to be refused is right when the schema or a
// rule refuses it, with the message part it gives, and otherwise wrong on
// the example.
func TestExpected(t *testing.T) {
	t.Parallel()
	spec := `{type: object, properties: {size: {type: integer, maximum: 100}, name: {type: string}}, x-kubernetes-validations: [{rule: "self.size <= 10", message: at most 10}, {rule: "!has(self.name) || self.name != 'x'", message: not x}, {rule: "10 / self.size >= 0"}]}`
	refused := func(msg, s string) string {
		if msg != "" {
			msg = " " + `"` + msg + `"`
		}
		return "# cel-lens: refused" + msg + "\n" + specWidget("a", s)
	}
	for _, tc := range []struct {
		name string
		doc  string
		want string // each rule's: right, rule (wrong at it) or example (wrong on it)
	}{
		{"allowed, as expected", specWidget("a", "{size: 1}"), "right right right"},
		{"refused, expected allowed", specWidget("a", "{size: 11, name: x}"), "rule rule right"},
		{"an error, expected allowed", specWidget("a", "{size: 0}"), "right right rule"},
		{"refused, as expected", refused("", "{size: 11}"), "right right right"},
		{"refused with the message part", refused("at most", "{size: 11}"), "right right right"},
		{"refused with another message", refused("not x", "{size: 11}"), "example example example"},
		{"refused by another rule", refused("not x", "{size: 11, name: x}"), "right right right"},
		{"refused by the schema", refused("less than or equal to 100", "{size: 101}"), "right right right"},
		{"an error, expected refused", refused("division by zero", "{size: 0}"), "right right right"},
		{"allowed, expected refused", refused("", "{size: 1}"), "example example example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, r := range ruleResults(t, specCRD(spec), tc.doc) {
				switch {
				case r.Expected:
					got = append(got, "right")
				case r.OnExample:
					got = append(got, "example")
				default:
					got = append(got, "rule")
				}
			}
			if strings.Join(got, " ") != tc.want {
				t.Errorf("Run(%s) expected = %s, want %s", tc.name, strings.Join(got, " "), tc.want)
			}
		})
	}
}

func TestRunCancelled(t *testing.T) {
	t.Parallel()
	crds := Read(t.Context(), crdFile, []byte(costCRD(1, 9000, 7300)), kubeversion.Default())
	rules, err := crds[0].Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(lengths(3000, 2000)))
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), objs); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var c Cache
	if _, err := c.Run(ctx, crdFile, rules, objs, Options{}); err == nil {
		t.Error("Run(cancelled) error = nil, want the context's")
	}
	if len(c.files) != 0 {
		t.Errorf("Run(cancelled) kept %d files, want none", len(c.files))
	}
}

// oracleCRD is a CRD with rules of every kind TestRunMatchesAPIServer holds
// to the API server's verdicts: at the root, on spec with a message, a
// messageExpression, a reason and a fieldPath, on the items of a list map
// and of an atomic list, on the values of a map, transition rules with and
// without optionalOldSelf, and one that can fail at runtime.
const oracleCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      subresources: {status: {}}
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: "!self.metadata.name.startsWith('x')"
              message: no x
          properties:
            spec:
              type: object
              required: [size]
              properties:
                size: {type: integer, maximum: 1000}
                mode: {type: string, default: fast}
                tags: {type: array, items: {type: string, x-kubernetes-validations: [{rule: "self.size() <= 3", message: tag too long}]}}
                ports:
                  type: array
                  x-kubernetes-list-type: map
                  x-kubernetes-list-map-keys: [name]
                  maxItems: 10
                  items:
                    type: object
                    required: [name]
                    properties:
                      name: {type: string, maxLength: 10}
                      port: {type: integer}
                    x-kubernetes-validations:
                      - rule: self.port > 1000
                        messageExpression: "'port ' + self.name + ' is privileged'"
                      - rule: self.port == oldSelf.port
                        message: a port's number is immutable
                labels:
                  type: object
                  additionalProperties:
                    type: string
                    x-kubernetes-validations: [{rule: "self != 'bad'", reason: FieldValueForbidden, message: bad label}]
              x-kubernetes-validations:
                - rule: self.size <= 10
                  message: at most 10
                  fieldPath: .size
                  reason: FieldValueInvalid
                - rule: self.mode == 'fast' || self.size < 5
                  message: slow widgets are small
                - rule: self.size >= oldSelf.size
                  message: no shrinking
                - rule: "!oldSelf.hasValue() || self.mode == oldSelf.value().mode"
                  optionalOldSelf: true
                  message: the mode is immutable
                - rule: 100 / self.size > 0
            status:
              type: object
              x-kubernetes-validations: [{rule: "self.ready", message: never seen}]
              properties:
                ready: {type: boolean}
`

// TestRunMatchesAPIServer holds the engine's verdicts to the API server's:
// for each example, the messages of the schema's problems (from
// ValidateCustomResource and the rest of the check, see Check) and of every
// rule's refusals and errors are the messages the API server's CEL
// validator, given every rule of the schema at once as the API server gives
// it, adds to the schema's, with its ratcheting for an update; none when
// the schema's problems stop it.
func TestRunMatchesAPIServer(t *testing.T) {
	t.Parallel()
	head := func(name string) string {
		return "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: " + name + "}\n"
	}
	for _, tc := range []struct {
		name string
		docs []string
	}{
		{"allowed", []string{head("a") + "spec: {size: 3, ports: [{name: http, port: 8080}], labels: {a: ok}, tags: [abc]}\n"}},
		{"refused by several rules", []string{head("xa") + "spec: {size: 30, mode: slow, ports: [{name: http, port: 80}, {name: dns, port: 53}], labels: {a: bad, b: bad}, tags: [abcd, ab, abcde]}\n"}},
		{"a runtime error", []string{head("a") + "spec: {size: 0}\n"}},
		{"a schema problem that does not stop the rules", []string{head("a") + "spec: {size: 2000}\n"}},
		{"a schema problem that stops the rules", []string{head("a") + "spec: {ports: [{port: 1}]}\n"}},
		{"an update", []string{
			head("a") + "spec: {size: 5, ports: [{name: http, port: 8080}], labels: {a: ok}}\n",
			head("a") + "spec: {size: 4, mode: slow, ports: [{name: http, port: 8081}, {name: dns, port: 53}], labels: {a: ok}}\n",
		}},
		{"an update ratchets what it leaves", []string{
			head("a") + "spec: {size: 20, ports: [{name: dns, port: 53}], labels: {a: bad}, tags: [abcd]}\n",
			head("a") + "spec: {size: 20, ports: [{name: dns, port: 53}], labels: {a: bad, b: bad}, tags: [abcd]}\n",
		}},
		{"an update after a refused create", []string{head("a") + "spec: {}\n", head("a") + "spec: {size: 1}\n"}},
		{"status kept on an update", []string{head("a") + "spec: {size: 1}\n", head("a") + "spec: {size: 2}\nstatus: {ready: false}\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			results := ruleResults(t, oracleCRD, tc.docs...)
			for _, r := range results {
				if strings.HasPrefix(r.Reason, crdRefuses) {
					t.Fatalf("rule %q is not run, as the API server refuses the CRD for it: %s", r.Rule.Text, r.Reason)
				}
			}
			ours := map[*Object][]string{}
			var objs []*Object
			for _, r := range results {
				o := r.Example
				if !slices.Contains(objs, o) {
					objs = append(objs, o)
					for _, p := range o.Problems {
						ours[o] = append(ours[o], p.Message)
					}
				}
				for _, f := range r.Refusals {
					ours[o] = append(ours[o], f.Message)
				}
				if r.Error != "" {
					ours[o] = append(ours[o], strings.Split(r.Error, "\n")...)
				}
			}
			if len(objs) != len(tc.docs) {
				t.Fatalf("Run(%s) has results for %d examples, want %d", tc.name, len(objs), len(tc.docs))
			}
			for _, o := range objs {
				want := apiServerMessages(t, o)
				got := ours[o]
				slices.Sort(got)
				if !slices.Equal(got, want) {
					t.Errorf("Run(%s), example at line %d: messages =\n%s\nthe API server's =\n%s", tc.name, o.Line, strings.Join(got, "\n"), strings.Join(want, "\n"))
				}
			}
		})
	}
}

// apiServerMessages returns, sorted, the messages the API server gives o,
// as customResourceStrategy.Validate and ValidateUpdate do: the schema's
// problems, then, unless one of them stops it, those of its CEL validator
// with every rule of the schema, for an update with ratcheting.
func apiServerMessages(t *testing.T, o *Object) []string {
	t.Helper()
	var out []string
	for _, p := range o.Problems {
		out = append(out, p.Message)
	}
	if !o.DecodeFailed && !o.RulesSkipped {
		v := cel.NewValidator(o.version.structural, true, celconfig.PerCallLimit)
		var opts []cel.Option
		old := o.old()
		if old != nil {
			opts = append(opts, cel.WithRatcheting(common.NewCorrelatedObject(o.Object, old, &model.Structural{Structural: o.version.structural})))
		}
		errs, _ := v.Validate(t.Context(), nil, o.version.structural, o.Object, old, celconfig.RuntimeCELCostBudget, opts...)
		for _, fe := range errs {
			out = append(out, fe.Error())
		}
	}
	slices.Sort(out)
	return out
}

// TestCache checks that a Cache keeps what a rule did to an example until
// the rule, its schema, the example or the Kubernetes version changes, and
// drops what no rule or example of the file has any more.
func TestCache(t *testing.T) {
	t.Parallel()
	const (
		limit  = `{rule: "self.size <= 10", message: at most 10}`
		parity = `{rule: "self.size % 2 == 0", message: even}`
	)
	crd := func(rules, size string) string {
		return specCRD(`{type: object, properties: {size: {type: integer` + size + `}}, x-kubernetes-validations: [` + rules + `]}`)
	}
	var c Cache
	// run runs the rules of crdText on docs for v, and returns the
	// results' Reason, which the test marks in the cache to tell a kept
	// result from one run again.
	run := func(crdText string, v kubeversion.Version, docs ...string) []string {
		t.Helper()
		crds := Read(t.Context(), crdFile, []byte(crdText), v)
		rules, err := crds[0].Rules(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(strings.Join(docs, "---\n")))
		if err != nil {
			t.Fatal(err)
		}
		if err := Check(t.Context(), objs); err != nil {
			t.Fatal(err)
		}
		results, err := c.Run(t.Context(), crdFile, rules, objs, Options{})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range results {
			out = append(out, fmt.Sprintf("%d@%d %s", r.Rule.Index, r.Example.Line, r.Reason))
		}
		return out
	}
	mark := func() {
		t.Helper()
		f := c.files[crdFile]
		for k, oc := range f.outcomes {
			oc.reason = "kept"
			f.outcomes[k] = oc
		}
	}
	a, b := specWidget("a", "{size: 1}"), specWidget("b", "{size: 2}")
	v137, v133 := kubeversion.Default(), kubeversion.Supported()[0]
	for _, step := range []struct {
		name, crd string
		v         kubeversion.Version
		docs      []string
		want      []string
	}{
		{"first run", crd(limit+", "+parity, ""), v137, []string{a, b}, []string{"0@1 ", "0@6 ", "1@1 ", "1@6 "}},
		{"nothing changed", crd(limit+", "+parity, ""), v137, []string{a, b}, []string{"0@1 kept", "0@6 kept", "1@1 kept", "1@6 kept"}},
		{"a rule changed", crd(limit+", "+strings.Replace(parity, "even", "odd", 1), ""), v137, []string{a, b}, []string{"0@1 kept", "0@6 kept", "1@1 ", "1@6 "}},
		{"an example changed", crd(limit+", "+strings.Replace(parity, "even", "odd", 1), ""), v137, []string{a, specWidget("b", "{size: 4}")}, []string{"0@1 kept", "0@6 ", "1@1 kept", "1@6 "}},
		{"the schema changed", crd(limit+", "+strings.Replace(parity, "even", "odd", 1), ", minimum: 0"), v137, []string{a, specWidget("b", "{size: 4}")}, []string{"0@1 ", "0@6 ", "1@1 ", "1@6 "}},
		{"the version changed", crd(limit+", "+strings.Replace(parity, "even", "odd", 1), ", minimum: 0"), v133, []string{a, specWidget("b", "{size: 4}")}, []string{"0@1 ", "0@6 ", "1@1 ", "1@6 "}},
	} {
		got := run(step.crd, step.v, step.docs...)
		if !slices.Equal(got, step.want) {
			t.Errorf("%s: results = %q, want %q", step.name, got, step.want)
		}
		if f := c.files[crdFile]; len(f.prepared) != 2 || len(f.outcomes) != 4 {
			t.Errorf("%s: the cache keeps %d rules and %d results, want 2 and 4", step.name, len(f.prepared), len(f.outcomes))
		}
		mark()
	}
}
