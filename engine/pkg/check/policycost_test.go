package check_test

import (
	"context"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// Expensive expressions: four nested all() over the containers of a pod
// template, a messageExpression that joins every container, and string() on an int.
const (
	costlyAll = "object.spec.template.spec.containers.all(a, object.spec.template.spec.containers.all(b, object.spec.template.spec.containers.all(c, object.spec.template.spec.containers.all(d, a.name != '' && b.name != '' && c.name != '' && d.name != ''))))"
	costlyMsg = "object.spec.template.spec.containers.map(c, c.name + string(c.ports.size())).join(',')"
)

// costlyValidating is a ValidatingAdmissionPolicy with an expensive expression
// in each field that takes CEL.
const costlyValidating = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: costly
spec:
  matchConstraints:
    resourceRules:
      - ` + deployments + `
  matchConditions:
    - name: costly
      expression: "` + costlyAll + `"
  variables:
    - name: costly
      expression: "` + costlyAll + `"
  validations:
    - expression: "` + costlyAll + `"
      messageExpression: "` + costlyMsg + `"
    - expression: "string(object.spec.replicas) != ''"
      messageExpression: "string(object.spec.replicas)"
  auditAnnotations:
    - key: costly
      valueExpression: "` + costlyMsg + `"
`

// costlyMutating is a MutatingAdmissionPolicy with an expensive expression in
// its match condition and variable.
const costlyMutating = `apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: costly
spec:
  matchConstraints:
    resourceRules:
      - ` + deployments + `
  matchConditions:
    - name: costly
      expression: "` + costlyAll + `"
  variables:
    - name: costly
      expression: "` + costlyAll + `"
  mutations:
    - patchType: JSONPatch
      jsonPatch:
        expression: "` + costlyAll + ` ? [JSONPatch{op: 'remove', path: '/a'}] : []"
`

// TestPolicyNoCost checks that no policy expression gets a cost problem at any
// supported version. The API server makes none: policy validation in
// pkg/apis/admissionregistration/validation/validation.go (Kubernetes 1.33 and
// 1.37) only compiles each expression, messageExpression, match condition,
// variable and audit annotation; it never estimates a cost or compares it with a
// limit. Policies have runtime budgets only (PerCallLimit, RuntimeCELCostBudget
// and the match condition budget), used when a request is evaluated.
func TestPolicyNoCost(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, doc string }{
		{"validating", costlyValidating},
		{"mutating", costlyMutating},
	}
	for _, v := range kubeversion.Supported() {
		for _, tt := range tests {
			t.Run(v.String()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				diags, err := check.Check(context.Background(), "policy.yaml", []byte(tt.doc), check.WithVersion(v))
				if err != nil {
					t.Fatalf("Check(%s, %s) error: %v", tt.name, v, err)
				}
				if len(diags) != 0 {
					t.Errorf("Check(%s, %s) = %+v, want no problem", tt.name, v, diags)
				}
			})
		}
	}
}
