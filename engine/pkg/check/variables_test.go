package check_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// TestPolicyVariableTypes checks the problems reported for variables.<name> at
// every supported Kubernetes version. A variable has the type its expression
// returns, as the API server's composited compiler registers it
// (k8s.io/apiserver/pkg/admission/plugin/cel/composition.go, CompileAndStoreVariable
// and convertCelTypeToDeclType); one that does not compile is dyn there. A name
// that is not yet a field of variables, because the policy defines it later or
// not at all, is the undefined field error of cel-go's checker
// (github.com/google/cel-go/checker/errors.go, undefinedField), which the
// compiler returns unchanged.
func TestPolicyVariableTypes(t *testing.T) {
	t.Parallel()
	const vars = "variables: [{name: replicas, expression: object.spec.replicas}, {name: broken, expression: 'object.spec.replicas +'}, {name: after, expression: variables.replicas}]"
	tests := []struct {
		name, variables, expr string
		want                  []warning
	}{
		{name: "used as its type", variables: vars, expr: "variables.replicas + 1 > 0"},
		{name: "method its type lacks", variables: vars, expr: "variables.replicas.size() > 0", want: []warning{{"apps/v1 Deployment: found no matching overload for 'size' applied to 'int.()'", "size"}}},
		{name: "wrong operand", variables: vars, expr: "variables.replicas == 'a'", want: []warning{{"apps/v1 Deployment: found no matching overload for '_==_' applied to '(int, string)'", "=="}}},
		{name: "name the policy lacks", variables: vars, expr: "variables.nope > 0", want: []warning{{"undefined field 'nope'", "nope"}}},
		{name: "defined later", variables: "variables: [{name: first, expression: variables.second}, {name: second, expression: object.spec.replicas}]", expr: "variables.second > 0", want: []warning{{"undefined field 'second'", "second"}}},
		{name: "variable that does not compile", variables: vars, expr: "variables.broken > 0 && variables.broken.size() > 0"},
		{name: "variable built on another", variables: vars, expr: "variables.after + 1 > 0"},
	}
	for _, v := range kubeversion.Supported() {
		for _, tt := range tests {
			t.Run(v.String()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				doc := policy(deployments, "expression: \""+tt.expr+"\"")
				doc = doc[:len(doc)-len("  validations:\n    - expression: \""+tt.expr+"\"\n")] + "  " + tt.variables + "\n  validations:\n    - expression: \"" + tt.expr + "\"\n"
				diags, err := check.Check(context.Background(), "policy.yaml", []byte(doc), check.WithVersion(v))
				if err != nil {
					t.Fatalf("Check(%s, %s) error: %v", tt.name, v, err)
				}
				var got []warning
				own := 0
				for _, w := range texts(doc, diags) {
					if strings.HasPrefix(w.message, "Syntax error") {
						own++ // the error of the variable that does not compile, once
					} else {
						got = append(got, w)
					}
				}
				if wantOwn := strings.Count(tt.variables, "name: broken"); own != wantOwn {
					t.Errorf("Check(%s, %s) gives %d syntax errors, want %d", tt.name, v, own, wantOwn)
				}
				if !slices.Equal(got, tt.want) {
					t.Errorf("Check(%s, %s) = %v, want %v", tt.name, v, got, tt.want)
				}
			})
		}
	}
}

// TestVariableTypes checks the types check.VariableTypes gives variables.
func TestVariableTypes(t *testing.T) {
	t.Parallel()
	var root map[string]any
	if err := yaml.Unmarshal([]byte(policy(deployments, "expression: 'true'")+`  variables:
    - {name: replicas, expression: object.spec.replicas}
    - {name: groups, expression: request.userInfo.groups}
    - {name: labels, expression: object.metadata.labels}
    - {name: template, expression: object.spec.template}
    - {name: broken, expression: "1 +"}
    - {name: sum, expression: variables.replicas + 1}
`), &root); err != nil {
		t.Fatal(err)
	}
	got, err := check.VariableTypes(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"int", "list(string)", "map(string, string)", "dyn", "dyn", "int"}
	if len(got) != len(want) {
		t.Fatalf("VariableTypes(policy) = %v, want %d variables", got, len(want))
	}
	for i, v := range got {
		if v.Type != want[i] {
			t.Errorf("VariableTypes(policy)[%d] (%s) type = %q, want %q", i, v.Name, v.Type, want[i])
		}
	}
	if got[0].Expression != "object.spec.replicas" {
		t.Errorf("VariableTypes(policy)[0].Expression = %q, want object.spec.replicas", got[0].Expression)
	}
}
