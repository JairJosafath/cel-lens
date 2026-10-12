package check_test

import (
	"context"
	"slices"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// authChain is the chain of the issue: every method of the authorizer, in the
// order the API server's authz library declares them.
const authChain = "authorizer.group('apps').resource('deployments').namespace('ns').check('update').allowed()"

// TestPolicyVariables checks the problems reported for request, namespaceObject,
// authorizer and oldObject at every supported Kubernetes version. The words are
// those of cel-go's checker, which the API server's admission compiler
// (k8s.io/apiserver/pkg/admission/plugin/cel, which declares the variables)
// returns unchanged: the undefined field error ("undefined field '<name>'") and
// the undeclared reference error ("undeclared reference to '<name>' (in
// container)") of github.com/google/cel-go/checker/errors.go.
func TestPolicyVariables(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, expr string
		// want is the messages of the diagnostics and the text each covers; none for a clean policy.
		want []warning
	}{
		{name: "request field", expr: "request.userInfo.username == ''"},
		{name: "wrong request field", expr: "request.usrInfo.username == ''", want: []warning{{"undefined field 'usrInfo'", "usrInfo"}}},
		{name: "wrong request field down the tree", expr: "request.userInfo.usernme == ''", want: []warning{{"undefined field 'usernme'", "usernme"}}},
		{name: "request kind", expr: "request.kind.kind == 'Deployment' && request.resource.resource == 'deployments' && request.dryRun"},
		{name: "oldObject is null on create", expr: "oldObject == null || oldObject.spec.replicas > 0"},
		{name: "wrong oldObject field", expr: "oldObject.spec.nope == 1", want: []warning{{"apps/v1 Deployment: undefined field 'nope'", "nope"}}},
		{name: "namespaceObject", expr: "namespaceObject == null || namespaceObject.metadata.labels['team'] == 'a'"},
		{name: "wrong namespaceObject field", expr: "namespaceObject.metadata.nme == ''", want: []warning{{"undefined field 'nme'", "nme"}}},
		{name: "authorizer chain", expr: authChain},
		// The admission compiler declares authorizer.requestResource; the type checker
		// of the API server (policy/validating/typechecking.go) declares only
		// authorizer, so for a policy that matches a built-in kind it warns, as here.
		{name: "authorizer requestResource", expr: "authorizer.requestResource.check('get').allowed()", want: []warning{{"apps/v1 Deployment: unexpected failed resolution of 'kubernetes.authorization.Authorizer'", "requestResource"}}},
		{name: "authorizer path", expr: "authorizer.path('/healthz').check('get').reason() != ''"},
		{name: "authorizer service account", expr: "authorizer.serviceAccount('ci', 'build').group('').resource('pods').name('p').subresource('log').check('get').errored()"},
		{name: "authorizer selectors", expr: "authorizer.group('').resource('pods').fieldSelector('spec.nodeName=n1').labelSelector('a=b').check('list').error() == ''"},
		{name: "authorizer without a method", expr: "authorizer.nope()", want: []warning{{"undeclared reference to 'nope' (in container '')", "nope"}}},
		{name: "self is not declared", expr: "self.spec == 1", want: []warning{{"undeclared reference to 'self' (in container '')", "self"}}},
	}
	for _, v := range kubeversion.Supported() {
		for _, tt := range tests {
			t.Run(v.String()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				doc := policy(deployments, "expression: \""+tt.expr+"\"")
				diags, err := check.Check(context.Background(), "policy.yaml", []byte(doc), check.WithVersion(v))
				if err != nil {
					t.Fatalf("Check(%s, %s) error: %v", tt.name, v, err)
				}
				got := texts(doc, diags)
				if !slices.Equal(got, tt.want) {
					t.Errorf("Check(%s, %s) = %v, want %v", tt.name, v, got, tt.want)
				}
			})
		}
	}
}

// texts lists the message of each diagnostic with the text it covers.
func texts(doc string, diags []check.Diagnostic) []warning {
	lines := splitLines(doc)
	var out []warning
	for _, d := range diags {
		out = append(out, warning{d.Message, lines[d.Start.Line-1][d.Start.Column-1 : d.End.Column-1]})
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := range s {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	return append(lines, s[start:])
}

// TestSelfInRule checks that self, which a policy does not declare, is fine in
// an x-kubernetes-validations rule.
func TestSelfInRule(t *testing.T) {
	t.Parallel()
	doc := `apiVersion: apiextensions.k8s.io/v1
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
            spec:
              type: object
              properties:
                size: {type: integer}
              x-kubernetes-validations:
                - rule: self.size > 0
`
	diags, err := check.Check(context.Background(), "crd.yaml", []byte(doc))
	if err != nil || len(diags) != 0 {
		t.Errorf("Check(rule with self) = %v, %v, want none", diags, err)
	}
}
