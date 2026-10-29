package assist_test

import (
	"context"
	"slices"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// crdWith returns a CRD whose schema has the properties props, given as YAML
// indented to sit under "properties:", with the rule zz on the string "leaf".
func crdWith(props string) string {
	return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
` + props
}

const (
	setItems = `            tags:
              type: array
              x-kubernetes-list-type: set
              items:
                type: string
                x-kubernetes-validations:
                  - rule: zz
`
	atomicItems = `            tags:
              type: array
              x-kubernetes-list-type: atomic
              items:
                type: string
                x-kubernetes-validations:
                  - rule: zz
`
	defaultItems = `            tags:
              type: array
              items:
                type: string
                x-kubernetes-validations:
                  - rule: zz
`
	mapItems = `            tags:
              type: array
              x-kubernetes-list-type: map
              x-kubernetes-list-map-keys: [k]
              items:
                type: object
                x-kubernetes-validations:
                  - rule: zz
`
	atomicMapChild = `            conf:
              type: object
              x-kubernetes-map-type: atomic
              properties:
                leaf:
                  type: string
                  x-kubernetes-validations:
                    - rule: zz
`
	atomicAdditional = `            conf:
              type: object
              x-kubernetes-map-type: atomic
              additionalProperties:
                type: string
                x-kubernetes-validations:
                  - rule: zz
`
	granularMapChild = `            conf:
              type: object
              x-kubernetes-map-type: granular
              properties:
                leaf:
                  type: string
                  x-kubernetes-validations:
                    - rule: zz
`
	plainProperty = `            leaf:
              type: string
              x-kubernetes-validations:
                - rule: zz
`
)

const (
	vapDoc = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
spec:
  paramKind: {apiVersion: v1, kind: ConfigMap}
  validations:
    - expression: zz
`
	mapDoc = `apiVersion: admissionregistration.k8s.io/v1beta1
kind: MutatingAdmissionPolicy
spec:
  paramKind: {apiVersion: v1, kind: ConfigMap}
  mutations:
    - patchType: ApplyConfiguration
      applyConfiguration:
        expression: zz
`
)

func TestCompleteNamesByDocument(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	admission := []string{"object", "oldObject", "params", "request", "namespaceObject", "authorizer", "variables"}
	kyverno := []string{"http", "resource", "image", "globalContext", "exceptions"}
	tests := []struct {
		name, doc  string
		has, lacks []string
	}{
		{"crd property", crdWith(plainProperty), []string{"self", "oldSelf", "size", "has"}, append(slices.Clone(admission), kyverno...)},
		{"crd map list items", crdWith(mapItems), []string{"self", "oldSelf", "size", "has"}, append(slices.Clone(admission), kyverno...)},
		{"crd granular map child", crdWith(granularMapChild), []string{"self", "oldSelf", "size", "has"}, append(slices.Clone(admission), kyverno...)},
		{"crd set list items", crdWith(setItems), []string{"self", "size", "has"}, append([]string{"oldSelf"}, append(slices.Clone(admission), kyverno...)...)},
		{"crd atomic list items", crdWith(atomicItems), []string{"self", "size", "has"}, append([]string{"oldSelf"}, append(slices.Clone(admission), kyverno...)...)},
		{"crd default list items", crdWith(defaultItems), []string{"self", "size", "has"}, append([]string{"oldSelf"}, append(slices.Clone(admission), kyverno...)...)},
		{"crd atomic map child", crdWith(atomicMapChild), []string{"self", "oldSelf", "size", "has"}, append(slices.Clone(admission), kyverno...)},
		{"crd atomic map additionalProperties", crdWith(atomicAdditional), []string{"self", "oldSelf", "size", "has"}, append(slices.Clone(admission), kyverno...)},
		{"ValidatingAdmissionPolicy", vapDoc, append(slices.Clone(admission), "size", "has"), append([]string{"self", "oldSelf"}, kyverno...)},
		{"MutatingAdmissionPolicy", mapDoc, append(slices.Clone(admission), "size", "has"), append([]string{"self", "oldSelf"}, kyverno...)},
		{"Kyverno ValidatingPolicy", vpol(deploymentRule, "zz"), append(slices.Clone(kyverno), "object", "request", "size"), []string{"self", "oldSelf", "params", "authorizer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := assist.Parse(context.Background(), "f.yaml", []byte(tt.doc))
			items, ok := a.Complete(doc, posIn(t, tt.doc, "zz\n", 2))
			if !ok {
				t.Fatal("Complete(doc) = none, want items")
			}
			var labels []string
			for _, it := range items {
				labels = append(labels, it.Label)
				if it.Kind == assist.KindFunction && it.Documentation == "" {
					t.Errorf("Complete: function %s has no docs", it.Label)
				}
			}
			for _, w := range tt.has {
				if !slices.Contains(labels, w) {
					t.Errorf("Complete(%s) = %v, want %q", tt.name, labels, w)
				}
			}
			for _, l := range tt.lacks {
				if slices.Contains(labels, l) {
					t.Errorf("Complete(%s) = %v, want no %q", tt.name, labels, l)
				}
			}
		})
	}
}
