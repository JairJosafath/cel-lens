package assist_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

const paramsWidgetCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  group: example.com
  names: {kind: Widget}
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              description: What the widget limits.
              properties:
                maxReplicas: {type: integer}
`

// withParamKind returns a policy with paramKind, a flow mapping or "" for none,
// and expression as its only validation.
func withParamKind(paramKind, expression string) string {
	pk := ""
	if paramKind != "" {
		pk = "  paramKind: " + paramKind + "\n"
	}
	return "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n" + pk +
		"  validations:\n    - expression: " + expression + "\n"
}

func paramsAssistant(t *testing.T) *assist.Assistant {
	t.Helper()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func labelsAfter(t *testing.T, doc, needle string, offset int, crds *check.CRDs) []string {
	t.Helper()
	items, ok := paramsAssistant(t).Complete(assist.Parse(context.Background(), "policy.yaml", []byte(doc), assist.WithCRDs(crds)), posIn(t, doc, needle, offset))
	if !ok {
		t.Fatalf("Complete after %q = none", needle[:offset])
	}
	var labels []string
	for _, it := range items {
		if it.Kind == assist.KindField {
			labels = append(labels, it.Label)
		}
	}
	slices.Sort(labels)
	return labels
}

func TestParamsComplete(t *testing.T) {
	t.Parallel()
	var crds check.CRDs
	crds.Set(context.Background(), "crd.yaml", []byte(paramsWidgetCRD))
	tests := []struct {
		name, paramKind, expr string
		offset                int
		want                  []string // the fields offered; none when params is dyn
	}{
		{"built-in kind", "{apiVersion: v1, kind: ConfigMap}", "params.data", len("params."), []string{"apiVersion", "binaryData", "data", "immutable", "kind", "metadata"}},
		{"CRD at the top", "{apiVersion: example.com/v1, kind: Widget}", "params.spec.maxReplicas", len("params."), []string{"apiVersion", "kind", "metadata", "spec"}},
		{"CRD metadata", "{apiVersion: example.com/v1, kind: Widget}", "params.metadata.labels", len("params.metadata."), []string{"annotations", "creationTimestamp", "deletionGracePeriodSeconds", "deletionTimestamp", "finalizers", "generateName", "generation", "labels", "managedFields", "name", "namespace", "ownerReferences", "resourceVersion", "selfLink", "uid"}},
		{"CRD down the tree", "{apiVersion: example.com/v1, kind: Widget}", "params.spec.maxReplicas", len("params.spec."), []string{"maxReplicas"}},
		{"kind not found", "{apiVersion: example.com/v9, kind: Widget}", "params.spec.maxReplicas", len("params."), nil},
		{"paramKind is no mapping", "nope", "params.spec.maxReplicas", len("params."), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := withParamKind(tt.paramKind, tt.expr)
			if got := labelsAfter(t, doc, tt.expr, tt.offset, &crds); !slices.Equal(got, tt.want) {
				t.Errorf("fields after params in %q = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
	t.Run("CRD not given", func(t *testing.T) {
		t.Parallel()
		doc := withParamKind("{apiVersion: example.com/v1, kind: Widget}", "params.spec.maxReplicas")
		if got := labelsAfter(t, doc, "params.spec", len("params."), nil); got != nil {
			t.Errorf("fields without CRDs = %v, want none", got)
		}
	})
}

func TestParamsHover(t *testing.T) {
	t.Parallel()
	var crds check.CRDs
	crds.Set(context.Background(), "crd.yaml", []byte(paramsWidgetCRD))
	tests := []struct {
		name, paramKind, expr, needle string
		want, notWant                 []string
	}{
		{"built-in kind", "{apiVersion: v1, kind: ConfigMap}", "params.data", "params.data", []string{"(variable) params: v1 ConfigMap"}, []string{"dyn"}},
		{"CRD", "{apiVersion: example.com/v1, kind: Widget}", "params.spec.maxReplicas", "params.spec", []string{"(variable) params: example.com/v1 Widget"}, []string{"dyn"}},
		{"CRD field", "{apiVersion: example.com/v1, kind: Widget}", "params.spec.maxReplicas", "spec.maxReplicas", []string{"(field) spec: object", "What the widget limits."}, nil},
		{
			"kind not found", "{apiVersion: example.com/v9, kind: Widget}", "params.spec.maxReplicas", "params.spec",
			[]string{"(variable) params: dyn", "dyn, not typed", "example.com/v9 Widget was not found"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := withParamKind(tt.paramKind, tt.expr)
			pos := posIn(t, doc, tt.needle, 2)
			if tt.name == "CRD field" {
				pos = posIn(t, doc, tt.needle, 1)
			}
			got, ok := paramsAssistant(t).Hover(assist.Parse(context.Background(), "policy.yaml", []byte(doc), assist.WithCRDs(&crds)), pos)
			if !ok {
				t.Fatalf("Hover(%v) = none", pos)
			}
			for _, w := range tt.want {
				if !strings.Contains(got.Markdown, w) {
					t.Errorf("Hover(%v) = %q, want it to contain %q", pos, got.Markdown, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got.Markdown, w) {
					t.Errorf("Hover(%v) = %q, want it without %q", pos, got.Markdown, w)
				}
			}
		})
	}
}
