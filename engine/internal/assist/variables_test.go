package assist_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// varsPolicy is a policy that matches deployments, with the variables of the
// issue and one validation, the expression under test.
func varsPolicy(expression string) string {
	return `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  variables:
    - name: replicas
      expression: object.spec.replicas
    - name: groups
      expression: request.userInfo.groups
    - name: broken
      expression: "object.spec.replicas +"
    - name: later
      expression: variables.replicas + 1
  validations:
    - expression: ` + expression + "\n"
}

func completeItems(t *testing.T, doc, needle string, offset int) []assist.Item {
	t.Helper()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	items, ok := a.Complete(assist.Parse(context.Background(), "policy.yaml", []byte(doc)), posIn(t, doc, needle, offset))
	if !ok {
		t.Fatalf("Complete after %q = none", needle[:offset])
	}
	return items
}

func TestVariablesComplete(t *testing.T) {
	t.Parallel()
	fields := func(items []assist.Item) map[string]string {
		got := map[string]string{}
		for _, it := range items {
			if it.Kind == assist.KindField {
				got[it.Label] = it.Detail
			}
		}
		return got
	}
	t.Run("names with their types", func(t *testing.T) {
		t.Parallel()
		got := fields(completeItems(t, varsPolicy("variables.replicas > 0"), "variables.replicas > 0", len("variables.")))
		want := map[string]string{"replicas": "int", "groups": "list(string)", "broken": "dyn", "later": "int"}
		for name, typ := range want {
			if got[name] != typ {
				t.Errorf("variables. offers %s as %q, want %q (all: %v)", name, got[name], typ, got)
			}
		}
	})
	t.Run("only variables before the variable", func(t *testing.T) {
		t.Parallel()
		doc := strings.Replace(varsPolicy("true"), "expression: variables.replicas + 1", "expression: variables.replicas", 1)
		got := fields(completeItems(t, doc, "variables.replicas\n  validations", len("variables.")))
		if _, ok := got["later"]; ok || got["replicas"] != "int" || got["groups"] != "list(string)" {
			t.Errorf("variables. in the 4th variable offers %v, want replicas, groups and broken only", got)
		}
	})
	t.Run("members of the type", func(t *testing.T) {
		t.Parallel()
		var labels []string
		for _, it := range completeItems(t, varsPolicy("variables.groups.size() > 0"), "variables.groups.size", len("variables.groups.")) {
			labels = append(labels, it.Label)
		}
		for _, want := range []string{"size", "exists", "map"} {
			if !slices.Contains(labels, want) {
				t.Errorf("variables.groups. offers %v, want it to include %q", labels, want)
			}
		}
	})
	t.Run("not in matchConditions", func(t *testing.T) {
		t.Parallel()
		doc := strings.Replace(varsPolicy("true"), "  validations:", "  matchConditions:\n    - name: m\n      expression: variables.replicas > 0\n  validations:", 1)
		a, err := assist.New(kubeversion.Default())
		if err != nil {
			t.Fatal(err)
		}
		items, _ := a.Complete(assist.Parse(context.Background(), "policy.yaml", []byte(doc)), posIn(t, doc, "variables.replicas > 0", len("variables.")))
		for _, it := range items {
			if it.Kind == assist.KindField {
				t.Errorf("variables. in matchConditions offers %q, want none", it.Label)
			}
		}
	})
}

func TestVariablesHover(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, expr, needle string
		offset             int
		want               []string
	}{
		{"name", "variables.replicas > 0", "variables.replicas > 0", len("variables.") + 2, []string{"(field) replicas: int", "object.spec.replicas"}},
		{"list", "variables.groups.size() > 0", "variables.groups", len("variables.") + 2, []string{"(field) groups: list(string)", "request.userInfo.groups"}},
		{"does not compile", "variables.broken > 0", "variables.broken", len("variables.") + 2, []string{"(field) broken: dyn"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := varsPolicy(tt.expr)
			pos := posIn(t, doc, tt.needle, tt.offset)
			got, ok := a.Hover(assist.Parse(context.Background(), "policy.yaml", []byte(doc)), pos)
			if !ok {
				t.Fatalf("Hover(%v) = none", pos)
			}
			for _, w := range tt.want {
				if !strings.Contains(got.Markdown, w) {
					t.Errorf("Hover(%v) = %q, want it to contain %q", pos, got.Markdown, w)
				}
			}
		})
	}
}
