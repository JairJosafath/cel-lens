package check

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
)

// TestStatusRootTypeIsNotSchemaError takes the status subresource's root type
// error from the API server's own validation, so that a Kubernetes upgrade that
// rewords it makes this test fail, and checks it is not taken for a schema
// error, which would hide the cost of the root's rules.
func TestStatusRootTypeIsNotSchemaError(t *testing.T) {
	t.Parallel()
	crd := &apiextensions.CustomResourceDefinition{
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensions.CustomResourceDefinitionNames{Kind: "Widget", Plural: "widgets"},
			Scope: apiextensions.NamespaceScoped,
			Versions: []apiextensions.CustomResourceDefinitionVersion{{
				Name: "v1", Served: true, Storage: true,
				Schema:       &apiextensions.CustomResourceValidation{OpenAPIV3Schema: &apiextensions.JSONSchemaProps{Type: "array"}},
				Subresources: &apiextensions.CustomResourceSubresources{Status: &apiextensions.CustomResourceSubresourceStatus{}},
			}},
		},
	}
	crd.Name = "widgets.example.com"
	found := false
	for _, fe := range validation.ValidateCustomResourceDefinition(context.Background(), crd) {
		if !strings.Contains(fe.Detail, "is allowed as the type at the root of the schema if the status subresource is enabled") {
			continue
		}
		found = true
		if isSchemaError(fe) {
			t.Errorf("isSchemaError(%q at %s) = true, want false", fe.Detail, fe.Field)
		}
	}
	if !found {
		t.Error("the API server reported no status subresource root type error for an array root")
	}
}
