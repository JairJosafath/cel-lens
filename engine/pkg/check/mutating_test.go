package check_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// mutating returns a MutatingAdmissionPolicy for deployments with the given
// mutation, a YAML mapping body such as "applyConfiguration: {expression: ...}".
func mutating(mutation string) string {
	return `apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: demo
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - ` + deployments + `
  matchConditions:
    - name: not-dry-run
      expression: "!request.dryRun"
  mutations:
    - patchType: ` + mutation + `
`
}

// TestMutatingPolicy checks that the CEL of a MutatingAdmissionPolicy is
// compiled with the admission compiler at every supported version: its
// variables are declared, and a mutation returns the patch type of its patchType.
func TestMutatingPolicy(t *testing.T) {
	t.Parallel()
	const apply = "ApplyConfiguration\n      applyConfiguration:\n        expression: "
	const patch = "JSONPatch\n      jsonPatch:\n        expression: "
	tests := []struct {
		name, doc string
		want      []warning
	}{
		{name: "apply configuration", doc: mutating(apply + `"Object{spec: Object.spec{replicas: 3}}"`)},
		{name: "apply with variables", doc: mutating(apply + `"Object{metadata: Object.metadata{labels: {'u': request.userInfo.username}}}"`)},
		{name: "json patch", doc: mutating(patch + `"[JSONPatch{op: 'add', path: '/spec/replicas', value: 3}]"`)},
		{name: "oldObject and namespaceObject", doc: mutating(patch + `"oldObject == null && namespaceObject == null ? [] : [JSONPatch{op: 'remove', path: '/spec'}]"`)},
		{name: "authorizer", doc: mutating(patch + `"authorizer.group('apps').resource('deployments').check('update').allowed() ? [JSONPatch{op: 'remove', path: '/a'}] : [JSONPatch{op: 'remove', path: '/b'}]"`)},
		{name: "wrong request field", doc: mutating(patch + `"request.usrInfo == null ? [] : [JSONPatch{op: 'remove', path: '/a'}]"`), want: []warning{{"undefined field 'usrInfo'", "usrInfo"}}},
		{name: "self is not declared", doc: mutating(patch + `"self.spec == 1 ? [] : [JSONPatch{op: 'remove', path: '/a'}]"`), want: []warning{{"undeclared reference to 'self' (in container '')", "self"}}},
		{name: "wrong result type", doc: mutating(apply + `"1"`), want: []warning{{"must evaluate to Object but got int", "1"}}},
		{name: "match condition", doc: `apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConditions:
    - name: bad
      expression: "self.x"
  mutations: []
`, want: []warning{{"undeclared reference to 'self' (in container '')", "self"}}},
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

// TestSelfPolicyAndRule runs one expression as a policy expression and as a
// CRD rule: self is undeclared in the policy, as the API server reports, and
// declared in the rule.
func TestSelfPolicyAndRule(t *testing.T) {
	t.Parallel()
	const expr = "self.spec == 1"
	crd := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec: {type: integer}
          x-kubernetes-validations:
            - rule: "` + expr + `"
`
	tests := []struct {
		name, doc string
		want      []warning
	}{
		{name: "validating policy", doc: policy(deployments, `expression: "`+expr+`"`), want: []warning{{"undeclared reference to 'self' (in container '')", "self"}}},
		{name: "mutating policy", doc: mutating("JSONPatch\n      jsonPatch:\n        expression: \"" + expr + " ? [] : []\""), want: []warning{{"undeclared reference to 'self' (in container '')", "self"}}},
		{name: "CRD rule", doc: crd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(context.Background(), "f.yaml", []byte(tt.doc))
			if err != nil {
				t.Fatalf("Check(%s) error: %v", tt.name, err)
			}
			if got := texts(tt.doc, diags); !slices.Equal(got, tt.want) {
				t.Errorf("Check(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestMutatingPolicyVariableTypes checks that variables.<name> has the type of
// its variable's expression in a MutatingAdmissionPolicy, as in a validating one.
// The variable is built from request, as object is dyn in a mutating policy.
func TestMutatingPolicyVariableTypes(t *testing.T) {
	t.Parallel()
	const withVars = "variables:\n    - {name: replicas, expression: request.userInfo.username.size()}\n  mutations:"
	const patch = "JSONPatch\n      jsonPatch:\n        expression: "
	tests := []struct {
		name, mutation string
		want           []warning
	}{
		{name: "used as its type", mutation: `"variables.replicas > 1 ? [] : [JSONPatch{op: 'remove', path: '/a'}]"`},
		{name: "method its type lacks", mutation: `"variables.replicas.size() > 1 ? [] : [JSONPatch{op: 'remove', path: '/a'}]"`, want: []warning{{"found no matching overload for 'size' applied to 'int.()'", "size"}}},
	}
	for _, v := range kubeversion.Supported() {
		for _, tt := range tests {
			t.Run(v.String()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				doc := strings.Replace(mutating(patch+tt.mutation), "mutations:", withVars, 1)
				diags, err := check.Check(context.Background(), "policy.yaml", []byte(doc), check.WithVersion(v))
				if err != nil {
					t.Fatalf("Check(%s, %s) error: %v", tt.name, v, err)
				}
				if got := texts(doc, diags); !slices.Equal(got, tt.want) {
					t.Errorf("Check(%s, %s) = %v, want %v", tt.name, v, got, tt.want)
				}
			})
		}
	}
}
