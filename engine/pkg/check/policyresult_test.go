package check_test

import (
	"context"
	"slices"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// resultPolicy returns a ValidatingAdmissionPolicy for deployments with one
// validation, a match condition and an audit annotation, each given as the
// YAML value of its expression.
func resultPolicy(condition, validation, message, audit string) string {
	return `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - ` + deployments + `
  matchConditions:
    - name: c
      expression: ` + condition + `
  validations:
    - expression: ` + validation + `
      messageExpression: ` + message + `
  auditAnnotations:
    - key: k
      valueExpression: ` + audit + `
`
}

// TestPolicyResultType checks that a policy expression of the wrong result type
// gets the API server's message, at every supported version.
//
// The messages are those of the admission CEL compiler, CompileCELExpression in
// k8s.io/apiserver/pkg/admission/plugin/cel/compile.go: with one allowed type,
//
//	fmt.Sprintf("must evaluate to %v but got %v", returnTypes[0].String(), ast.OutputType().String())
//
// and with several, "must evaluate to one of %v but got %v". The API server
// accepts an expression only when its type is exactly an allowed one, so one of
// type dyn, such as object.spec.replicas, is not accepted: "got dyn". A
// messageExpression allows string, a validation or match condition bool, and an
// audit annotation string or null.
func TestPolicyResultType(t *testing.T) {
	t.Parallel()
	const ok = `"true"`
	const str = `"'s'"`
	tests := []struct {
		name, doc string
		want      []warning
	}{
		{name: "all right", doc: resultPolicy(ok, ok, str, str)},
		{name: "null audit value", doc: resultPolicy(ok, ok, str, "'null'")},
		{name: "message int", doc: resultPolicy(ok, ok, "object.spec.replicas", str), want: []warning{{"must evaluate to string but got dyn", "object.spec.replicas"}}},
		{name: "message int literal", doc: resultPolicy(ok, ok, `"1"`, str), want: []warning{{"must evaluate to string but got int", "1"}}},
		{name: "validation int", doc: resultPolicy(ok, `"1"`, str, str), want: []warning{{"must evaluate to bool but got int", "1"}}},
		{name: "validation string", doc: resultPolicy(ok, `"'s'"`, str, str), want: []warning{{"must evaluate to bool but got string", "'s'"}}},
		{name: "validation dyn", doc: resultPolicy(ok, "object.spec.paused", str, str), want: []warning{{"must evaluate to bool but got dyn", "object.spec.paused"}}},
		{name: "condition int", doc: resultPolicy(`"1"`, ok, str, str), want: []warning{{"must evaluate to bool but got int", "1"}}},
		{name: "condition string", doc: resultPolicy(`"'s'"`, ok, str, str), want: []warning{{"must evaluate to bool but got string", "'s'"}}},
		{name: "audit int", doc: resultPolicy(ok, ok, str, `"1"`), want: []warning{{"must evaluate to one of [string null_type] but got int", "1"}}},
		{name: "every expression", doc: resultPolicy(`"1"`, `"2"`, `"3"`, `"4"`), want: []warning{
			{"must evaluate to bool but got int", "1"},
			{"must evaluate to bool but got int", "2"},
			{"must evaluate to string but got int", "3"},
			{"must evaluate to one of [string null_type] but got int", "4"},
		}},
	}
	for _, v := range kubeversion.Supported() {
		for _, tt := range tests {
			t.Run(v.String()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				diags, err := check.Check(context.Background(), "policy.yaml", []byte(tt.doc), check.WithVersion(v))
				if err != nil {
					t.Fatalf("Check(%s, %s) error: %v", tt.name, v, err)
				}
				if got := texts(tt.doc, diags); !slices.Equal(got, tt.want) {
					t.Errorf("Check(%s, %s) = %v, want %v", tt.name, v, got, tt.want)
				}
			})
		}
	}
}

// TestMutatingPolicyResultType checks the match condition of a
// MutatingAdmissionPolicy, which must give a bool, at every supported version.
func TestMutatingPolicyResultType(t *testing.T) {
	t.Parallel()
	doc := `apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConditions:
    - name: c
      expression: "'s'"
    - name: d
      expression: object.paused
  mutations: []
`
	want := []warning{{"must evaluate to bool but got string", "'s'"}, {"must evaluate to bool but got dyn", "object.paused"}}
	for _, v := range kubeversion.Supported() {
		t.Run(v.String(), func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(context.Background(), "policy.yaml", []byte(doc), check.WithVersion(v))
			if err != nil {
				t.Fatalf("Check(%s) error: %v", v, err)
			}
			if got := texts(doc, diags); !slices.Equal(got, want) {
				t.Errorf("Check(%s) = %v, want %v", v, got, want)
			}
		})
	}
}
