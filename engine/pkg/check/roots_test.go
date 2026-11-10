package check_test

import (
	"reflect"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// TestRoots checks the values of a file whose types a .cellens entry can
// name the root of: the versions of a CRD, and params, object, oldObject and
// the variables of a policy, typed as the API server types them (#16).
func TestRoots(t *testing.T) {
	t.Parallel()
	policy := `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits
spec:
  paramKind: {apiVersion: example.com/v1, kind: Missing}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  variables:
    - name: max
      expression: "params.max"
    - name: replicas
      expression: "object.spec.replicas"
  validations:
    - expression: "true"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {}
spec: {}
`
	data := append(crdWith("true"), []byte("---\n"+policy)...)
	roots, err := check.Roots(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	type summary struct {
		kind, name, version, variable, typ string
		schemas                            int
	}
	var got []summary
	for _, r := range roots {
		got = append(got, summary{r.Kind, r.Name, r.Version, r.Variable, r.Type, len(r.Schemas)})
	}
	want := []summary{
		{"CustomResourceDefinition", "widgets.example.com", "v1", "", "Widget", 1},
		{"ValidatingAdmissionPolicy", "limits", "", "params", "dyn", 0},
		{"ValidatingAdmissionPolicy", "limits", "", "object", "apps/v1 Deployment", 1},
		{"ValidatingAdmissionPolicy", "limits", "", "oldObject", "apps/v1 Deployment", 1},
		{"ValidatingAdmissionPolicy", "limits", "", "variables.max", "dyn", 0},
		{"ValidatingAdmissionPolicy", "limits", "", "variables.replicas", "int", 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Roots = %+v,\nwant %+v", got, want)
	}
	if roots[0].Line != 1 || roots[1].Line != 29 {
		t.Errorf("Roots lines = %d, %d, want 1 and 29", roots[0].Line, roots[1].Line)
	}
}
