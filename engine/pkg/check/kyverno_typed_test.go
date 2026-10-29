package check_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// kyvernoPolicy returns a Kyverno ValidatingPolicy that matches rule, a flow
// mapping, and has the validation expression.
func kyvernoPolicy(rule, expression string) string {
	return `apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - ` + rule + `
  variables:
    - name: replicas
      expression: "object.spec.replicas"
  validations:
    - expression: "` + expression + `"
`
}

// TestKyvernoTypedWarnings checks that object and oldObject of a Kyverno
// ValidatingPolicy are typed from the kinds it matches, as for a
// ValidatingAdmissionPolicy.
func TestKyvernoTypedWarnings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, doc string
		want      []warning
	}{
		{"no such field", kyvernoPolicy(deployments, "object.spec.nope == 1"), []warning{{"apps/v1 Deployment: undefined field 'nope'", "nope"}}},
		{"oldObject", kyvernoPolicy(deployments, "oldObject.nope == null"), []warning{{"apps/v1 Deployment: undefined field 'nope'", "nope"}}},
		{"valid fields", kyvernoPolicy(deployments, "object.spec.replicas <= variables.replicas"), nil},
		{"field only one kind has", kyvernoPolicy(both, "object.spec.serviceName != ''"), []warning{{"apps/v1 Deployment: undefined field 'serviceName'", "serviceName"}}},
		{"field every kind has", kyvernoPolicy(both, "object.spec.replicas > 1"), nil},
		{"field named like a CEL keyword", kyvernoPolicy("{apiGroups: [''], apiVersions: [v1], resources: [pods]}", "object.metadata.namespace == ''"), nil},
		{"wildcard stays dyn", kyvernoPolicy("{apiGroups: ['*'], apiVersions: [v1], resources: [deployments]}", "object.nope == null"), nil},
		{"custom resource stays dyn", kyvernoPolicy("{apiGroups: [example.com], apiVersions: [v1], resources: [widgets]}", "object.nope == null"), nil},
		{"broken expression gets its error once", kyvernoPolicy(deployments, "object.spec.nope == "), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(context.Background(), "policy.yaml", []byte(tt.doc), allOn())
			if err != nil {
				t.Fatalf("Check(%s) error: %v", tt.name, err)
			}
			if strings.HasPrefix(tt.name, "broken") {
				if len(diags) != 1 || diags[0].Severity != check.SeverityError {
					t.Errorf("Check(%s) = %v, want one error", tt.name, diags)
				}
				return
			}
			var got []warning
			lines := strings.Split(tt.doc, "\n")
			for _, d := range diags {
				if d.Severity != check.SeverityWarning {
					t.Errorf("Check(%s) has %s %q, want only warnings", tt.name, d.Severity, d.Message)
					continue
				}
				got = append(got, warning{d.Message, lines[d.Start.Line-1][d.Start.Column-1 : d.End.Column-1]})
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Check(%s) = %v, want %v", tt.name, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Check(%s)[%d] = %v, want %v", tt.name, i, got[i], tt.want[i])
				}
			}
		})
	}
}
