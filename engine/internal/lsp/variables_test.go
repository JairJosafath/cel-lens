package lsp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

const variablesPolicyText = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  variables:
    - name: replicas
      expression: object.spec.replicas
  validations:
    - expression: "variables.replicas + 1 > 0 && variables.nope > 0"
    - expression: "variables.replicas.size() > 0"
`

// TestPolicyVariableAssist checks that the language server reports the problems
// check does for variables.<name>, and types, completes and hovers it.
func TestPolicyVariableAssist(t *testing.T) {
	t.Parallel()
	want, err := check.Check(context.Background(), "policy.yaml", []byte(variablesPolicyText))
	if err != nil || len(want) != 2 {
		t.Fatalf("Check(policy) = %v, %v, want two problems", want, err)
	}
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	c.openURI(policyURI, variablesPolicyText)
	diags := c.nextDiagnostics()
	if len(diags) != len(want) {
		t.Fatalf("diagnostics = %+v, want the %d problems of check", diags, len(want))
	}
	for i, d := range diags {
		if d.Message != want[i].Message {
			t.Errorf("diagnostic %d = %q, want %q as check gives", i, d.Message, want[i].Message)
		}
	}
	if labels := c.completeLabels(variablesPolicyText, "variables.replicas + 1", len("variables.")); !contains(labels, "replicas") {
		t.Errorf("completion after variables. = %v, want replicas", labels)
	}
	if got := c.hoverText(variablesPolicyText, "variables.replicas + 1", len("variables.")+2); !strings.Contains(got, "replicas: int") || !strings.Contains(got, "object.spec.replicas") {
		t.Errorf("hover on variables.replicas = %q, want its type int and its expression", got)
	}
}
