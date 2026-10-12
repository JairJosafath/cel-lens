package check_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// policy returns a ValidatingAdmissionPolicy that matches rule, a flow mapping
// such as {apiGroups: [apps], ...}, and has validation, a flow mapping or a
// plain entry such as expression: "true", as its only validation.
func policy(rule, validation string) string {
	return `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - ` + rule + `
  validations:
    - ` + validation + `
`
}

const both = "{apiGroups: [apps], apiVersions: [v1], resources: [deployments, statefulsets]}"

const deployments = "{apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments]}"

// TestTypedWarnings checks the warnings of the API server's status.typeChecking.
func TestTypedWarnings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, doc string
		// want is the messages and the text each covers; none for a clean file.
		want []warning
	}{
		{
			// The example of "Type Checking" in the Kubernetes documentation of
			// ValidatingAdmissionPolicy (kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy):
			// status.typeChecking has "apps/v1, Kind=Deployment: ERROR: <input>:1:7: undefined field 'replicas'".
			name: "docs example",
			doc:  policy(deployments, `{expression: "object.replicas > 1", message: "must be replicated", reason: Invalid}`),
			want: []warning{{"apps/v1 Deployment: undefined field 'replicas'", "replicas"}},
		},
		{
			name: "no such field",
			doc:  policy(deployments, `expression: "object.additionalContainers == null"`),
			want: []warning{{"apps/v1 Deployment: undefined field 'additionalContainers'", "additionalContainers"}},
		},
		{
			name: "no such field down the tree",
			doc:  policy(deployments, `expression: "object.spec.template.spec.containers.all(c, c.imagee != '')"`),
			want: []warning{{"apps/v1 Deployment: undefined field 'imagee'", "imagee"}},
		},
		{
			name: "oldObject",
			doc:  policy(deployments, `expression: "oldObject.spec.nope == 1"`),
			want: []warning{{"apps/v1 Deployment: undefined field 'nope'", "nope"}},
		},
		{
			// typechecking_test.go of k8s.io/apiserver, "field type mismatch".
			name: "type mismatch",
			doc:  policy(deployments, `expression: "object.spec.replicas == 'three'"`),
			want: []warning{{"apps/v1 Deployment: found no matching overload for '_==_' applied to '(int, string)'", "=="}},
		},
		{
			name: "message expression",
			doc:  policy(deployments, "{expression: \"true\", messageExpression: \"'x' + object.spec.nope\"}"),
			want: []warning{{"apps/v1 Deployment: undefined field 'nope'", "nope"}},
		},
		{
			name: "core group",
			doc:  policy("{apiGroups: [''], apiVersions: [v1], resources: [pods]}", `expression: "object.spec.containers.all(c, c.nope == '')"`),
			want: []warning{{"v1 Pod: undefined field 'nope'", "nope"}},
		},
		{
			name: "valid fields",
			doc:  policy(deployments, `expression: "object.spec.replicas <= 5 && object.metadata.name.startsWith('a') && request.operation == 'CREATE'"`),
		},
		{
			name: "subresource only stays dyn",
			doc:  policy("{apiGroups: [apps], apiVersions: [v1], resources: [deployments/scale]}", `expression: "object.additionalContainers == null"`),
		},
		{
			name: "subresource beside the resource",
			doc:  policy("{apiGroups: [apps], apiVersions: [v1], resources: [deployments, deployments/scale]}", `expression: "object.additionalContainers == null"`),
			want: []warning{{"apps/v1 Deployment: undefined field 'additionalContainers'", "additionalContainers"}},
		},
		{
			name: "wildcard stays dyn",
			doc:  policy("{apiGroups: ['*'], apiVersions: [v1], resources: [deployments]}", `expression: "object.additionalContainers == null"`),
		},
		{
			name: "wildcard resource stays dyn",
			doc:  policy("{apiGroups: [apps], apiVersions: [v1], resources: ['*']}", `expression: "object.additionalContainers == null"`),
		},
		{
			name: "custom resource stays dyn",
			doc:  policy("{apiGroups: [example.com], apiVersions: [v1], resources: [widgets]}", `expression: "object.additionalContainers == null"`),
		},
		{
			name: "field only one of several kinds has",
			doc:  policy(both, `expression: "object.spec.serviceName != ''"`),
			want: []warning{{"apps/v1 Deployment: undefined field 'serviceName'", "serviceName"}},
		},
		{
			name: "field the other kind has",
			doc:  policy(both, `expression: "object.spec.strategy.type != ''"`),
			want: []warning{{"apps/v1 StatefulSet: undefined field 'strategy'", "strategy"}},
		},
		{
			name: "field no kind has",
			doc:  policy(both, `expression: "object.additionalContainers == null"`),
			want: []warning{
				{"apps/v1 Deployment: undefined field 'additionalContainers'", "additionalContainers"},
				{"apps/v1 StatefulSet: undefined field 'additionalContainers'", "additionalContainers"},
			},
		},
		{
			name: "field every kind has",
			doc:  policy(both, `expression: "object.spec.replicas > 1"`),
		},
		{
			name: "one unknown kind keeps object dyn",
			doc:  policy("{apiGroups: [apps], apiVersions: [v1], resources: [deployments, widgets]}", `expression: "object.spec.serviceName != ''"`),
		},
		{
			name: "one wildcard rule keeps object dyn",
			doc: strings.Replace(policy(both, `expression: "object.spec.serviceName != ''"`),
				"resourceRules:\n", "resourceRules:\n      - {apiGroups: ['*'], apiVersions: [v1], resources: [pods]}\n", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(context.Background(), "policy.yaml", []byte(tt.doc))
			if err != nil {
				t.Fatalf("Check(%s) error: %v", tt.name, err)
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

type warning struct{ message, text string }

// TestTypedNoDuplicates checks that an expression that does not compile gets
// its error once, and no warning about the typed object.
func TestTypedNoDuplicates(t *testing.T) {
	t.Parallel()
	doc := policy(deployments, `expression: "object.spec.nope == "`)
	diags, err := check.Check(context.Background(), "policy.yaml", []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 1 || diags[0].Severity != check.SeverityError {
		t.Errorf("Check(broken expression) = %v, want one error", diags)
	}
}

// TestTypedVariables checks that policy variables stay usable with a typed object.
func TestTypedVariables(t *testing.T) {
	t.Parallel()
	doc := `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  variables:
    - name: replicas
      expression: "object.spec.replicas"
  validations:
    - expression: "variables.replicas <= 5"
`
	diags, err := check.Check(context.Background(), "policy.yaml", []byte(doc))
	if err != nil || len(diags) != 0 {
		t.Errorf("Check(variables) = %v, %v, want none", diags, err)
	}
}
