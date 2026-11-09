package assist_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// summaryCRD is a CustomResourceDefinition with one rule, rule, on the root.
// spec.port, spec.my-port, spec.a.b and the items' target are dyn values a
// rule can read (x-kubernetes-int-or-string); spec.extra is kept open, which a
// rule cannot read into.
func summaryCRD(rule string) string {
	return fmt.Sprintf(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {plural: widgets, kind: Widget}
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: %q
          properties:
            spec:
              type: object
              x-kubernetes-validations:
                - rule: "self.name.size() > 0"
                  messageExpression: "'bad ' + self.name"
              properties:
                name: {type: string}
                port: {x-kubernetes-int-or-string: true}
                my-port: {x-kubernetes-int-or-string: true}
                a.b: {x-kubernetes-int-or-string: true}
                extra: {type: object, x-kubernetes-preserve-unknown-fields: true}
                opts:
                  type: object
                  additionalProperties: {x-kubernetes-int-or-string: true}
                ports:
                  type: array
                  items:
                    type: object
                    x-kubernetes-validations:
                      - rule: "self.target != 0"
                    properties:
                      target: {x-kubernetes-int-or-string: true}
`, rule)
}

// summaryPolicy is a ValidatingAdmissionPolicy with one validation,
// expression. Its params and object are dyn: the paramKind is not found and
// it matches every resource. variables.cfg is dyn, as it returns an object.
func summaryPolicy(expression string) string {
	return fmt.Sprintf(`apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits
spec:
  paramKind: {apiVersion: example.com/v1, kind: Limit}
  matchConstraints:
    resourceRules:
      - {apiGroups: ["*"], apiVersions: ["*"], resources: ["*"]}
  variables:
    - name: cfg
      expression: "object.spec"
    - name: count
      expression: "1"
  validations:
    - expression: %q
`, expression)
}

const deploymentPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: replicas
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments], operations: [CREATE]}
  validations:
    - expression: "object.spec.replicas <= 5 && request.options.dryRun == true"
`

// kyvernoSummaryPolicy is a Kyverno ValidatingPolicy that reads object, dyn
// as it matches every resource: .cellens types nothing of Kyverno's.
const kyvernoSummaryPolicy = `apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: limits
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: ["*"], apiVersions: ["*"], resources: ["*"], operations: [CREATE]}
  validations:
    - expression: "object.spec.max > 0"
`

// mutatingPolicy is a MutatingAdmissionPolicy whose mutation reads params,
// dyn as no CRD defines its paramKind.
const mutatingPolicy = `apiVersion: admissionregistration.k8s.io/v1beta1
kind: MutatingAdmissionPolicy
metadata:
  name: add-label
spec:
  paramKind: {apiVersion: example.com/v1, kind: Missing}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments], operations: [CREATE]}
  mutations:
    - patchType: ApplyConfiguration
      applyConfiguration:
        expression: "Object{metadata: Object.metadata{labels: {'a': params.x}}}"
`

func crdValue(name, path string) assist.Value {
	return assist.Value{Name: name, DocumentKind: "CustomResourceDefinition", DocumentName: "widgets.example.com", Version: "v1", Path: path}
}

func policyValue(name, variable string) assist.Value {
	return assist.Value{Name: name, DocumentKind: "ValidatingAdmissionPolicy", DocumentName: "limits", Variable: variable}
}

// TestSummaries checks what the line above each expression is made from: the
// subject and its type, and the dyn values the expression reads that a
// .cellens entry types or can type, each once, in the order read (#3).
func TestSummaries(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	portType := check.AssumedType{
		Kind: "CustomResourceDefinition", Name: "widgets.example.com", Version: "v1", Path: []string{"spec", "port"},
		Type: "int", Source: ".cellens", Line: 2, Schema: map[string]any{"type": "integer"},
	}
	extraType := portType
	extraType.Path = []string{"spec", "extra"}
	paramsType := check.AssumedType{
		Kind: "ValidatingAdmissionPolicy", Name: "limits", Variable: "params", Type: "v1 ConfigMap", Source: ".cellens", Line: 7,
		Schema: map[string]any{"type": "object", "properties": map[string]any{"data": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}},
	}
	limitStruct := check.AssumedType{
		Kind: "ValidatingAdmissionPolicy", Name: "limits", Variable: "params", Type: "{max: int, x: string}", Source: ".cellens", Line: 7,
		Schema: map[string]any{"type": "object", "properties": map[string]any{"max": map[string]any{"type": "integer"}, "x": map[string]any{"type": "string"}}},
	}
	objectStruct := limitStruct
	objectStruct.Variable = "object"
	typed := func(v assist.Value, typ string) assist.Value { v.Type = typ; return v }
	tests := []struct {
		name  string
		text  string
		types []check.AssumedType
		// subject is the first summary's subject and its type, as Subject: SubjectType.
		subject string
		values  []assist.Value
	}{
		{name: "dyn field", text: summaryCRD("self.spec.port > 0"), subject: "self: Widget", values: []assist.Value{crdValue("self.spec.port", "spec.port")}},
		{name: "typed field", text: summaryCRD("self.spec.port > 0"), types: []check.AssumedType{portType}, subject: "self: Widget", values: []assist.Value{typed(crdValue("self.spec.port", "spec.port"), "int")}},
		{name: "an entry on an open field does not apply", text: summaryCRD("self.spec.extra.name == 'a'"), types: []check.AssumedType{extraType}, subject: "self: Widget"},
		{name: "escaped field name", text: summaryCRD("self.spec.my__dash__port > 0"), subject: "self: Widget", values: []assist.Value{crdValue("self.spec.my-port", "spec.my-port")}},
		{name: "a field no .cellens path can name", text: summaryCRD("self.spec.a__dot__b > 0"), subject: "self: Widget"},
		{name: "open field a rule cannot read", text: summaryCRD("self.spec.extra.name == 'a'"), subject: "self: Widget"},
		{name: "Kubernetes types it", text: summaryCRD("self.spec.name.size() > 0"), subject: "self: Widget"},
		{name: "read once, in order", text: summaryCRD("self.spec.opts['a'] == 1 && self.spec.port == 1 || oldSelf.spec.port == 'x'"), subject: "self: Widget", values: []assist.Value{crdValue("self.spec.opts[*]", "spec.opts[*]"), crdValue("self.spec.port", "spec.port")}},
		{name: "map field", text: summaryCRD("self.spec.opts.a == 1"), subject: "self: Widget", values: []assist.Value{crdValue("self.spec.opts[*]", "spec.opts[*]")}},
		{name: "has tests presence only", text: summaryCRD("has(self.spec.port)"), subject: "self: Widget"},
		{name: "list items in a macro", text: summaryCRD("self.spec.ports.all(p, p.target > 0)"), subject: "self: Widget", values: []assist.Value{crdValue("self.spec.ports[].target", "spec.ports[].target")}},
		{name: "list index", text: summaryCRD("self.spec.ports[0].target > 0"), subject: "self: Widget", values: []assist.Value{crdValue("self.spec.ports[].target", "spec.ports[].target")}},
		{name: "optional field", text: summaryCRD("self.?spec.?port.orValue(1) == 1"), subject: "self: Widget", values: []assist.Value{crdValue("self.spec.port", "spec.port")}},
		{name: "map values in a two-variable macro", text: summaryCRD("self.spec.opts.all(k, v, v == 1)"), subject: "self: Widget", values: []assist.Value{crdValue("self.spec.opts[*]", "spec.opts[*]")}},
		{name: "map keys stand for nothing", text: summaryCRD("self.spec.opts.all(k, k.size() > 0)"), subject: "self: Widget"},
		{name: "a macro variable hides a name", text: summaryCRD("[1].all(self, self > 0)"), subject: "self: Widget"},
		{name: "does not parse", text: summaryCRD("self.spec.port >"), subject: "self: Widget"},
		{name: "dyn params and object", text: summaryPolicy("params.data.size() > 0 && object.spec.name != ''"), subject: "object: dyn", values: []assist.Value{policyValue("params", "params"), policyValue("object", "object")}},
		{name: "typed params", text: summaryPolicy("params.data.size() > 0"), types: []check.AssumedType{paramsType}, subject: "object: dyn", values: []assist.Value{typed(policyValue("params", "params"), "v1 ConfigMap")}},
		{name: "params typed as a struct", text: summaryPolicy("params.max > 0"), types: []check.AssumedType{limitStruct}, subject: "object: dyn", values: []assist.Value{typed(policyValue("params", "params"), "{max: int, x: string}")}},
		{name: "object typed as a struct", text: summaryPolicy("object.max > 0"), types: []check.AssumedType{objectStruct}, subject: "object: {max: int, x: string}", values: []assist.Value{typed(policyValue("object", "object"), "{max: int, x: string}")}},
		{name: "dyn variable", text: summaryPolicy("variables.cfg.max > 0 && variables.count > 0"), subject: "object: dyn", values: []assist.Value{policyValue("variables.cfg", "variables.cfg")}},
		{name: "oldObject", text: summaryPolicy("oldObject == null"), subject: "object: dyn", values: []assist.Value{policyValue("oldObject", "oldObject")}},
		{name: "matched kind", text: deploymentPolicy, subject: "object: apps/v1 Deployment"},
		{name: "Kyverno", text: kyvernoSummaryPolicy, subject: "object: dyn"},
		{name: "mutation", text: mutatingPolicy, subject: "object: apps/v1 Deployment", values: []assist.Value{{Name: "params", DocumentKind: "MutatingAdmissionPolicy", DocumentName: "add-label", Variable: "params"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := assist.Parse(context.Background(), "file.yaml", []byte(tc.text), assist.WithAssumedTypes(tc.types))
			got, err := a.Summaries(context.Background(), doc)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 {
				t.Fatal("Summaries() = none, want some")
			}
			// The case is about the root rule of a CRD, the first, or the
			// validation of a policy, the last.
			i := 0
			if !strings.Contains(tc.text, "kind: CustomResourceDefinition") {
				i = len(got) - 1
			}
			if s := got[i].Subject + ": " + got[i].SubjectType; s != tc.subject {
				t.Errorf("Summaries() subject = %q, want %q", s, tc.subject)
			}
			if !reflect.DeepEqual(got[i].Values, tc.values) {
				t.Errorf("Summaries().Values = %+v, want %+v", got[i].Values, tc.values)
			}
		})
	}
}

// TestSummariesOfEveryExpression checks that every rule, messageExpression
// and policy field gets a summary, at the expression's place, and that the
// subject of a CRD rule names the schema it is on (#3).
func TestSummariesOfEveryExpression(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	crd := summaryCRD("self.spec.port > 0")
	policy := strings.Replace(summaryPolicy("params.x > 0"), "  validations:\n", "  matchConditions:\n    - name: m\n      expression: \"request.name != ''\"\n  validations:\n", 1)
	policy = strings.Replace(policy, "  variables:\n", "  auditAnnotations:\n    - key: a\n      valueExpression: \"'x'\"\n  variables:\n", 1)
	policy += "      messageExpression: \"'too many'\"\n"
	for _, tc := range []struct {
		name, text string
		want       []string // Subject: SubjectType @ line, one per summary
	}{
		{"CRD", crd, []string{"self: Widget @ 14", "self: Widget.spec @ 19", "self: Widget.spec @ 20", "self: Widget.spec.ports[] @ 35"}},
		{"policy", policy, []string{"object: dyn @ 12", "object: dyn @ 15", "object: dyn @ 17", "object: dyn @ 20", "object: dyn @ 22", "object: dyn @ 23"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := a.Summaries(context.Background(), assist.Parse(context.Background(), "file.yaml", []byte(tc.text)))
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, s := range got {
				lines = append(lines, fmt.Sprintf("%s: %s @ %d", s.Subject, s.SubjectType, s.Start.Line))
			}
			if !reflect.DeepEqual(lines, tc.want) {
				t.Errorf("Summaries() = %q, want %q", lines, tc.want)
			}
		})
	}
}

// TestSummariesCancelled checks that a cancelled context stops Summaries.
func TestSummariesCancelled(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	doc := assist.Parse(context.Background(), "file.yaml", []byte(summaryCRD("self.spec.port > 0")))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := a.Summaries(ctx, doc); !errors.Is(err, context.Canceled) || got != nil {
		t.Errorf("Summaries(cancelled) = %v, %v, want nil and context.Canceled", got, err)
	}
}

// TestSummariesOnlyWhereChecked checks that no summary says "compiles" about
// an expression cel-lens does not check: a Kyverno policy while the
// experimental feature kyverno is off, and a CustomResourceDefinition of
// apiextensions.k8s.io/v1beta1 (#3).
func TestSummariesOnlyWhereChecked(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, text string }{
		{"Kyverno with the feature off", kyvernoSummaryPolicy},
		{"CRD of v1beta1", strings.Replace(summaryCRD("self.spec.port > 0"), "apiextensions.k8s.io/v1", "apiextensions.k8s.io/v1beta1", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := a.Summaries(context.Background(), assist.Parse(context.Background(), "file.yaml", []byte(tc.text)))
			if err != nil || len(got) != 0 {
				t.Errorf("Summaries() = %+v, %v, want none", got, err)
			}
		})
	}
}

// policies returns n ValidatingAdmissionPolicies matching deployments, each
// with vars variables and one validation, which reads variables.v0 when
// readsVariables.
func policies(n, vars int, readsVariables bool) string {
	var b strings.Builder
	for p := range n {
		fmt.Fprintf(&b, "---\napiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  name: p%d\nspec:\n  paramKind: {apiVersion: example.com/v1, kind: Limit}\n  matchConstraints:\n    resourceRules:\n      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}\n  variables:\n", p)
		for v := range vars {
			fmt.Fprintf(&b, "    - name: v%d\n      expression: \"object.spec.replicas + %d\"\n", v, v)
		}
		expression := "params.max > 0"
		if readsVariables {
			expression += " && variables.v0 > 0"
		}
		fmt.Fprintf(&b, "  validations:\n    - expression: %q\n", expression)
	}
	return b.String()
}

// TestSummariesWorkOncePerPolicy checks that Summaries works out what a
// policy's expressions share once: the kinds it matches, for every policy that
// matches the same, and its variables' types, for all its expressions, not
// once per variable (#3, review). It counts allocations, which do not depend
// on the machine: before, 20 variables cost 11 times one, and 20 policies 20
// times one. Not parallel, as other tests' allocations would count.
func TestSummariesWorkOncePerPolicy(t *testing.T) {
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	allocs := func(text string) float64 {
		doc := assist.Parse(context.Background(), "file.yaml", []byte(text))
		return testing.AllocsPerRun(3, func() {
			if _, err := a.Summaries(context.Background(), doc); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		name      string
		one, many string
		most      float64 // how many times one many may cost
	}{
		{"20 variables, read", policies(1, 1, true), policies(1, 20, true), 2},
		{"20 policies matching the same kind", policies(1, 1, false), policies(20, 1, false), 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			one, many := allocs(tc.one), allocs(tc.many)
			if many > tc.most*one {
				t.Errorf("Summaries allocates %.0f times for %s and %.0f for one, want at most %.0f times as much", many, tc.name, one, tc.most)
			}
		})
	}
}
