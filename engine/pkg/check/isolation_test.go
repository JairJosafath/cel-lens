package check_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// isolationPolicy is a ValidatingAdmissionPolicy over resource with the given
// extra spec lines and one validation.
func isolationPolicy(resource, extra, expression string) string {
	return `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: ` + resource + `
spec:
` + extra + `  matchConstraints:
    resourceRules:
      - {apiGroups: ["` + map[string]string{"deployments": "apps", "configmaps": ""}[resource] + `"], apiVersions: [v1], resources: [` + resource + `]}
  validations:
    - expression: "` + expression + `"
`
}

// isolationCRD is a CRD of kind whose spec.size has the given type and whose
// one rule is expression.
func isolationCRD(kind, sizeType, expression string) string {
	return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: ` + strings.ToLower(kind) + `s.example.com
spec:
  group: example.com
  scope: Namespaced
  names: {plural: ` + strings.ToLower(kind) + `s, singular: ` + strings.ToLower(kind) + `, kind: ` + kind + `, listKind: ` + kind + `List}
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
                size: {type: ` + sizeType + `}
              x-kubernetes-validations:
                - rule: "` + expression + `"
`
}

// problemLines returns the number of problems check reports on each line of
// the file.
func problemLines(t *testing.T, file string) map[int]string {
	t.Helper()
	diags, err := check.Check(context.Background(), "multi.yaml", []byte(file))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	got := map[int]string{}
	for _, d := range diags {
		got[d.Start.Line] += d.Message + "\n"
	}
	return got
}

func lineOf(t *testing.T, file, needle string) int {
	t.Helper()
	i := strings.Index(file, needle)
	if i < 0 || strings.Count(file, needle) != 1 {
		t.Fatalf("needle %q occurs %d times, want 1", needle, strings.Count(file, needle))
	}
	return strings.Count(file[:i], "\n") + 1
}

func TestDocumentsDoNotTypeEachOther(t *testing.T) {
	t.Parallel()
	t.Run("each policy types object from its own kinds", func(t *testing.T) {
		t.Parallel()
		file := isolationPolicy("deployments", "", "object.spec.replicas > 0 && object.data == 1") + "---\n" +
			isolationPolicy("configmaps", "", "object.data.size() > 0 && object.spec.replicas > 0")
		got := problemLines(t, file)
		if len(got) != 2 {
			t.Errorf("Check() reports on lines %v, want one on each policy", got)
		}
		if m := got[lineOf(t, file, "object.data == 1")]; !strings.Contains(m, "'data'") || strings.Contains(m, "'spec'") {
			t.Errorf("deployments policy: %q, want a problem on data only", m)
		}
		if m := got[lineOf(t, file, "object.data.size()")]; !strings.Contains(m, "'spec'") || strings.Contains(m, "'data'") {
			t.Errorf("configmaps policy: %q, want a problem on spec only", m)
		}
	})
	t.Run("variables and params come only from the policy itself", func(t *testing.T) {
		t.Parallel()
		first := isolationPolicy("deployments", "  paramKind: {apiVersion: v1, kind: ConfigMap}\n  variables:\n    - name: mine\n      expression: object.spec.replicas\n", "variables.mine > 0 && params.data.size() > 0")
		second := isolationPolicy("configmaps", "", "variables.mine > 0 || params.data.size() > 0")
		third := isolationPolicy("configmaps", "  variables:\n    - name: other\n      expression: object.data\n", "variables.other.size() > 0 && variables.mine == 1")
		file := first + "---\n" + second + "---\n" + third
		got := problemLines(t, file)
		if m, ok := got[lineOf(t, file, "variables.mine > 0 && params")]; ok {
			t.Errorf("first policy: %q, want none", m)
		}
		m := got[lineOf(t, file, "variables.mine > 0 || params")]
		if !strings.Contains(m, "'mine'") || !strings.Contains(m, "'params'") {
			t.Errorf("second policy: %q, want problems for variables and params", m)
		}
		m = got[lineOf(t, file, "variables.other.size()")]
		if !strings.Contains(m, "mine") || strings.Contains(m, "other") {
			t.Errorf("third policy: %q, want a problem for mine only", m)
		}
	})
	t.Run("self comes from the CRD's own schema", func(t *testing.T) {
		t.Parallel()
		file := isolationCRD("Alpha", "integer", "self.size + 1 > 0") + "---\n" + isolationCRD("Beta", "string", "self.size + 'a' == 'xa'")
		if got := problemLines(t, file); len(got) != 0 {
			t.Errorf("Check() = %v, want none", got)
		}
		swapped := isolationCRD("Alpha", "integer", "self.size + 'a' == 'xa'") + "---\n" + isolationCRD("Beta", "string", "self.size + 1 > 0")
		if got := problemLines(t, swapped); len(got) != 2 {
			t.Errorf("Check() on swapped types = %v, want a problem on each rule", got)
		}
	})
}
