package assist_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

func isolationPolicy(resource, group, extra, expression string) string {
	return `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: ` + resource + `
spec:
` + extra + `  matchConstraints:
    resourceRules:
      - {apiGroups: ["` + group + `"], apiVersions: [v1], resources: [` + resource + `]}
  validations:
    - expression: ` + expression + `
`
}

func isolationCRD(kind, sizeType, expression string) string {
	return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: ` + strings.ToLower(kind) + `s.example.com
spec:
  group: example.com
  names: {plural: ` + strings.ToLower(kind) + `s, kind: ` + kind + `}
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                size: {type: ` + sizeType + `}
                ` + strings.ToLower(kind) + `Only: {type: string}
              x-kubernetes-validations:
                - rule: ` + expression + `
`
}

func hoverText(t *testing.T, doc, needle string, offset int) string {
	t.Helper()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	h, ok := a.Hover(assist.Parse(context.Background(), "multi.yaml", []byte(doc)), posIn(t, doc, needle, offset))
	if !ok {
		t.Fatalf("Hover after %q = none", needle[:offset])
	}
	return h.Markdown
}

func fieldLabels(t *testing.T, doc, needle string, offset int) []string {
	t.Helper()
	return labelsAfter(t, doc, needle, offset, nil)
}

func TestDocumentsDoNotTypeEachOther(t *testing.T) {
	t.Parallel()
	t.Run("object offers only the kinds of its own policy", func(t *testing.T) {
		t.Parallel()
		doc := isolationPolicy("deployments", "apps", "", "object.spec.replicas > 0") + "---\n" +
			isolationPolicy("configmaps", "", "", "object.data.size() > 0")
		dep := fieldLabels(t, doc, "object.spec.replicas", len("object.spec."))
		if !slices.Contains(dep, "replicas") || slices.Contains(dep, "data") {
			t.Errorf("object.spec. in deployments policy offers %v, want replicas", dep)
		}
		top := fieldLabels(t, doc, "object.data.size()", len("object."))
		if !slices.Contains(top, "data") || slices.Contains(top, "spec") {
			t.Errorf("object. in configmaps policy offers %v, want data and not spec", top)
		}
		topDep := fieldLabels(t, doc, "object.spec.replicas", len("object."))
		if !slices.Contains(topDep, "spec") || slices.Contains(topDep, "data") {
			t.Errorf("object. in deployments policy offers %v, want spec and not data", topDep)
		}
		if got := hoverText(t, doc, "object.data.size()", len("object.da")); !strings.Contains(got, "data") {
			t.Errorf("Hover(data of configmaps) = %q, want data", got)
		}
		if got := hoverText(t, doc, "object.spec.replicas", len("object.spec.rep")); !strings.Contains(got, "replicas") || !strings.Contains(got, "int") {
			t.Errorf("Hover(replicas of deployments) = %q, want an int", got)
		}
	})
	t.Run("variables and params come only from the policy itself", func(t *testing.T) {
		t.Parallel()
		first := isolationPolicy("deployments", "apps", "  paramKind: {apiVersion: v1, kind: ConfigMap}\n  variables:\n    - name: mine\n      expression: object.spec.replicas\n", "variables.mine > 0 && params.data.size() > 0")
		second := isolationPolicy("configmaps", "", "", "variables.mine > 0 || params.data.size() > 0")
		third := isolationPolicy("configmaps", "", "  variables:\n    - name: other\n      expression: object.data\n", "variables.other.size() > 0")
		doc := first + "---\n" + second + "---\n" + third
		if got := fieldLabels(t, doc, "variables.mine > 0 && params", len("variables.")); !slices.Equal(got, []string{"mine"}) {
			t.Errorf("variables. in the first policy offers %v, want mine", got)
		}
		if got := fieldLabels(t, doc, "variables.mine > 0 || params", len("variables.")); len(got) != 0 {
			t.Errorf("variables. in the second policy offers %v, want none", got)
		}
		if got := fieldLabels(t, doc, "variables.other.size()", len("variables.")); !slices.Equal(got, []string{"other"}) {
			t.Errorf("variables. in the third policy offers %v, want other", got)
		}
		if got := fieldLabels(t, doc, "&& params.data.size()", len("&& params.")); !slices.Contains(got, "data") {
			t.Errorf("params. in the first policy offers %v, want the fields of ConfigMap", got)
		}
		if got := hoverText(t, doc, "variables.mine > 0 && params", len("variables.mi")); !strings.Contains(got, "mine: int") {
			t.Errorf("Hover(mine) = %q, want int", got)
		}
		if got := hoverText(t, doc, "variables.other.size()", len("variables.ot")); strings.Contains(got, "mine") || !strings.Contains(got, "other") {
			t.Errorf("Hover(other) = %q, want the variable of its own policy", got)
		}
		a, err := assist.New(kubeversion.Default(), allOn())
		if err != nil {
			t.Fatal(err)
		}
		pos := posIn(t, doc, "variables.mine > 0 || params", len("variables.mine > 0 || pa"))
		if h, ok := a.Hover(assist.Parse(context.Background(), "multi.yaml", []byte(doc)), pos); ok {
			t.Errorf("Hover(params) in the second policy = %q, want none: it has no paramKind", h.Markdown)
		}
	})
	t.Run("self comes from the CRD's own schema", func(t *testing.T) {
		t.Parallel()
		doc := isolationCRD("Alpha", "integer", "self.size + 1 > 0") + "---\n" + isolationCRD("Beta", "string", "self.size + 'a' == 'xa'")
		alpha := fieldLabels(t, doc, "self.size + 1", len("self."))
		beta := fieldLabels(t, doc, "self.size + 'a'", len("self."))
		if !slices.Equal(alpha, []string{"alphaOnly", "size"}) || !slices.Equal(beta, []string{"betaOnly", "size"}) {
			t.Errorf("self. offers %v and %v, want each CRD's own fields", alpha, beta)
		}
		if got := hoverText(t, doc, "self.size + 1", len("self.si")); !strings.Contains(got, "size: int") {
			t.Errorf("Hover(size) in Alpha = %q, want int", got)
		}
		if got := hoverText(t, doc, "self.size + 'a'", len("self.si")); !strings.Contains(got, "size: string") {
			t.Errorf("Hover(size) in Beta = %q, want string", got)
		}
	})
	t.Run("a Kyverno policy and an admission policy keep their own variables", func(t *testing.T) {
		t.Parallel()
		kyverno := `apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: k
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [""], apiVersions: [v1], resources: [configmaps]}
  variables:
    - name: kmine
      expression: object.data
  validations:
    - expression: variables.kmine.size() > 0
`
		doc := isolationPolicy("deployments", "apps", "", "object.spec.replicas > 0 && variables.x") + "---\n" + kyverno
		if got := fieldLabels(t, doc, "variables.kmine.size()", len("variables.")); !slices.Equal(got, []string{"kmine"}) {
			t.Errorf("variables. in the Kyverno policy offers %v, want kmine", got)
		}
		if got := fieldLabels(t, doc, "object.spec.replicas", len("object.")); !slices.Contains(got, "spec") || slices.Contains(got, "data") {
			t.Errorf("object. in the admission policy offers %v, want spec and not data", got)
		}
		if got := fieldLabels(t, doc, "&& variables.x", len("&& variables.")); len(got) != 0 {
			t.Errorf("variables. in the admission policy offers %v, want none", got)
		}
	})
}
