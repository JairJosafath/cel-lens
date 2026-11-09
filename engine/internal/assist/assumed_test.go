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

const assumedCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {plural: widgets, kind: Widget}
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: "self.spec.config > 0 && self.spec.name.size() > 0 && has(self.spec.open.max)"
          properties:
            spec:
              type: object
              properties:
                config:
                  x-kubernetes-int-or-string: true
                open:
                  x-kubernetes-preserve-unknown-fields: true
                name:
                  type: string
`

const assumedPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits
spec:
  paramKind: {apiVersion: example.com/v1, kind: Limit}
  matchConstraints:
    resourceRules:
      - {apiGroups: [example.com], apiVersions: [v1], resources: [widgets]}
  variables:
    - name: cfg
      expression: "object.spec.config"
  validations:
    - expression: "params.spec.max > 0 && object.spec.max > 0 && oldObject.spec.max > 0 && variables.cfg.spec.max > 0"
`

// limitType is the type {spec: {max: int}}, as a .cellens entry at line gives it.
func limitType(kind, name, version, variable string, line int, path ...string) check.AssumedType {
	return check.AssumedType{
		Kind: kind, Name: name, Version: version, Variable: variable, Path: path,
		Type: "{spec: {max: int}}", Source: ".cellens", Line: line,
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"spec": map[string]any{"type": "object", "properties": map[string]any{"max": map[string]any{"type": "integer"}}},
		}},
	}
}

func assumedDoc(text string, types ...check.AssumedType) *assist.Document {
	return assist.Parse(context.Background(), "file.yaml", []byte(text), assist.WithAssumedTypes(types))
}

func fieldsAt(t *testing.T, a *assist.Assistant, doc *assist.Document, text, needle string, offset int) []string {
	t.Helper()
	items, ok := a.Complete(doc, posIn(t, text, needle, offset))
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

// TestAssumedTypesComplete checks that completion after a value typed from
// .cellens offers the fields of the type (#2), for a CRD's field and for
// params, object, oldObject and variables.<name> of a policy.
func TestAssumedTypesComplete(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	open := limitType("CustomResourceDefinition", "widgets.example.com", "v1", "", 3, "spec", "open")
	policyTypes := []check.AssumedType{
		limitType("ValidatingAdmissionPolicy", "limits", "", "params", 5),
		limitType("ValidatingAdmissionPolicy", "limits", "", "object", 8),
		limitType("ValidatingAdmissionPolicy", "limits", "", "oldObject", 11),
		limitType("ValidatingAdmissionPolicy", "limits", "", "variables.cfg", 14),
	}
	for _, tc := range []struct {
		name, text, needle string
		types              []check.AssumedType
		want               []string
	}{
		// No rule may read a value that keeps unknown fields, so its entry is not placed.
		{"CRD field that keeps unknown fields", assumedCRD, "self.spec.open.max", []check.AssumedType{open}, nil},
		{"params", assumedPolicy, "params.spec.max", policyTypes, []string{"spec"}},
		{"params down the tree", assumedPolicy, "params.spec.max", policyTypes, []string{"max"}},
		{"object", assumedPolicy, "object.spec.max", policyTypes, []string{"max"}},
		{"oldObject", assumedPolicy, "oldObject.spec.max", policyTypes, []string{"max"}},
		{"variables.cfg", assumedPolicy, "variables.cfg.spec.max", policyTypes, []string{"max"}},
		{"another document's type", assumedPolicy, "params.spec.max", []check.AssumedType{limitType("ValidatingAdmissionPolicy", "other", "", "params", 5)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			offset := strings.LastIndex(tc.needle, ".") + 1
			if strings.HasSuffix(tc.name, "params") {
				offset = len("params.")
			}
			got := fieldsAt(t, a, assumedDoc(tc.text, tc.types...), tc.text, tc.needle, offset)
			if !slices.Equal(got, tc.want) {
				t.Errorf("fields after %q = %v, want %v", tc.needle[:offset], got, tc.want)
			}
		})
	}
}

// TestAssumedTypesHover checks that hover on a value typed from .cellens
// shows the type and says it is from .cellens, with the line (#2).
func TestAssumedTypesHover(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	config := limitType("CustomResourceDefinition", "widgets.example.com", "v1", "", 3, "spec", "config")
	config.Schema, config.Type = map[string]any{"type": "integer"}, "int"
	params := limitType("ValidatingAdmissionPolicy", "limits", "", "params", 5)
	for _, tc := range []struct {
		name, text, needle string
		offset             int
		typ                check.AssumedType
		want               []string
	}{
		{"CRD field", assumedCRD, "self.spec.config > 0", len("self.spec.co"), config, []string{"(field) config: int", "from .cellens, line 3"}},
		{"field that keeps unknown fields", assumedCRD, "self.spec.open.max", len("self.spec.open.m"), limitType("CustomResourceDefinition", "widgets.example.com", "v1", "", 3, "spec", "open"), nil},
		{"params", assumedPolicy, "params.spec.max", len("par"), params, []string{"(variable) params: {spec: {max: int}}", "from .cellens, line 5"}},
		{"params field", assumedPolicy, "params.spec.max", len("params.spec.m"), params, []string{"(field) max: int", "from .cellens, line 5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, ok := a.Hover(assumedDoc(tc.text, tc.typ), posIn(t, tc.text, tc.needle, tc.offset))
			if tc.want == nil {
				if ok {
					t.Errorf("Hover(%q) = %q, want none: the type is not placed there", tc.needle, h.Markdown)
				}
				return
			}
			if !ok {
				t.Fatalf("Hover(%q) = none", tc.needle)
			}
			for _, w := range tc.want {
				if !strings.Contains(h.Markdown, w) {
					t.Errorf("Hover(%q) = %q, want it to contain %q", tc.needle, h.Markdown, w)
				}
			}
			if strings.Contains(h.Markdown, "It is dyn") {
				t.Errorf("Hover(%q) = %q, still says the value is dyn", tc.needle, h.Markdown)
			}
		})
	}
}

// TestAssumedTypeOnTypedValue checks that Kubernetes's type wins over an
// entry on a value it types (#2).
func TestAssumedTypeOnTypedValue(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	typed := limitType("CustomResourceDefinition", "widgets.example.com", "v1", "", 3, "spec", "name")
	h, ok := a.Hover(assumedDoc(assumedCRD, typed), posIn(t, assumedCRD, "self.spec.name", len("self.spec.na")))
	if !ok || !strings.Contains(h.Markdown, "(field) name: string") || strings.Contains(h.Markdown, ".cellens") {
		t.Errorf("Hover(self.spec.name) = %q, %t, want the string field, not the .cellens type", h.Markdown, ok)
	}
}
