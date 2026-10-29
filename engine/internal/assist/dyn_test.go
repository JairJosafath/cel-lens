package assist_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// vapMatching returns a ValidatingAdmissionPolicy that matches resourceRules, a
// YAML list item body, with expression as its only validation.
func vapMatching(rules, expression string) string {
	return "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n" + rules +
		"  validations:\n    - expression: " + expression + "\n"
}

const openCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  group: example.com
  names: {kind: Widget}
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                count: {type: integer}
                data: {type: object, x-kubernetes-preserve-unknown-fields: true}
                loose: {}
                blob: {x-kubernetes-preserve-unknown-fields: true}
              x-kubernetes-validations:
                - rule: %s
`

// TestHoverDynReason checks that hover over a dyn value gives its type and why
// it is dyn, and that hover over a value of a known type gives no reason.
func TestHoverDynReason(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	const (
		deployments = "  matchConstraints:\n    resourceRules:\n      - apiGroups: [apps]\n        apiVersions: [v1]\n        resources: [deployments]\n"
		wildcard    = "  matchConstraints:\n    resourceRules:\n      - apiGroups: ['*']\n        apiVersions: ['*']\n        resources: ['*']\n"
		custom      = "  matchConstraints:\n    resourceRules:\n      - apiGroups: [example.com]\n        apiVersions: [v1]\n        resources: [widgets]\n"
		paramKind   = "  paramKind: {apiVersion: example.com/v1, kind: Missing}\n"
	)
	variable := func(rules, expr string) string {
		return vapMatching(rules+"  variables:\n    - name: v\n      expression: "+expr+"\n", "variables.v == null")
	}
	const cronjobs = "  matchConstraints:\n    resourceRules:\n      - apiGroups: [apps]\n        apiVersions: [v1]\n        resources: [deployments]\n      - apiGroups: [batch]\n        apiVersions: [v1]\n        resources: [cronjobs]\n"
	const dynPrefix = "dyn: "
	tests := []struct {
		name, doc, needle string
		want              string // "" for no reason
	}{
		{"wildcard resource rule", vapMatching(wildcard, "object.spec == null"), "object.spec", "this policy matches resources with a wildcard (for example `resources: [\"*\"]`)"},
		{"no resource rule", vapMatching("", "object.spec == null"), "object.spec", "this policy has no match rule that names a resource"},
		{"unknown kind", vapMatching(custom, "object.spec == null"), "object.spec", "such as a custom resource"},
		{"oldObject", vapMatching(wildcard, "oldObject.spec == null"), "oldObject.spec", "wildcard"},
		{"paramKind not found", vapMatching(paramKind, "params.x == 1"), "params.x", "no CustomResourceDefinition among the workspace files defines it"},
		{"preserve unknown fields", strings.Replace(openCRD, "%s", "self.blob.a == 1", 1), "blob.a", "`x-kubernetes-preserve-unknown-fields`"},
		{"schema without type", strings.Replace(openCRD, "%s", "self.loose.a == 1", 1), "loose.a", "the schema gives this value no type"},
		{"admission request options", vapMatching(deployments, "request.options == null"), "options", "`x-kubernetes-preserve-unknown-fields`"},
		{"kyverno wildcard", vpol("{apiGroups: ['*'], apiVersions: ['*'], resources: ['*']}", "object.spec == null"), "object.spec", "wildcard"},
		{"kyverno policy variable", vpol("{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}", "variables.replicas == 1"), "replicas ==", "the type of this policy variable is not worked out"},
		{"variable holding an object", variable(deployments, "object.spec"), ".v ==", "returns an object"},
		{"variable holding a dyn object under a wildcard", variable(wildcard, "object.spec"), ".v ==", "reads a value whose type is not known"},
		{"variable reading a dyn object", variable(wildcard, "object.spec.replicas"), ".v ==", "reads a value whose type is not known"},
		{"variable reading dyn params", variable(deployments+paramKind, "params.x"), ".v ==", "reads a value whose type is not known"},
		{"variable that does not compile", variable(deployments, "object.nosuch + 1"), ".v ==", "does not compile"},
		{"variable typed differently by kinds", variable(cronjobs, "object.spec.schedule"), ".v ==", "different types"},
		{"typed variable", variable(deployments, "object.spec.replicas"), ".v ==", ""},
		{"typed object", vapMatching(deployments, "object.spec == null"), "object.spec", ""},
		{"typed field", vapMatching(deployments, "object.spec.replicas == 1"), "replicas", ""},
		{"typed CRD field", strings.Replace(openCRD, "%s", "self.count == 1", 1), "count ==", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := assist.Parse(context.Background(), "p.yaml", []byte(tt.doc))
			got, ok := a.Hover(doc, posIn(t, tt.doc, tt.needle, 1))
			if !ok {
				t.Fatalf("Hover(%q) = none, want a hover", tt.needle)
			}
			if tt.want == "" {
				if strings.Contains(got.Markdown, dynPrefix) {
					t.Errorf("Hover(%q) = %q, want no dyn reason", tt.needle, got.Markdown)
				}
				return
			}
			if !strings.Contains(got.Markdown, ": dyn") || !strings.Contains(got.Markdown, dynPrefix) || !strings.Contains(got.Markdown, tt.want) {
				t.Errorf("Hover(%q) = %q, want type dyn and a reason containing %q", tt.needle, got.Markdown, tt.want)
			}
		})
	}
}
