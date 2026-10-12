// Package matchkind finds the built-in Kubernetes resource a policy matches,
// and the schema of that resource, as the API server does when it type-checks
// a ValidatingAdmissionPolicy against spec.matchConstraints.resourceRules.
//
// The schemas come from the Go types of k8s.io/api, with the descriptions of
// their generated swagger docs, so they are those of the Kubernetes version the
// engine was built with.
package matchkind

import (
	"reflect"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// Rule is the part of a resource rule that names resources: the apiGroups,
// apiVersions and resources of an entry of spec.matchConstraints.resourceRules.
type Rule struct {
	Groups, Versions, Resources []string
}

// Kind is a built-in resource kind with its schema.
type Kind struct {
	// GVK is the group, version and kind, such as apps/v1 Deployment.
	GVK schema.GroupVersionKind
	// Schema is the OpenAPI schema of the kind, with descriptions.
	Schema *spec.Schema
}

// String names the kind with its API version, such as "apps/v1 Deployment" or
// "v1 Pod".
func (k Kind) String() string {
	return k.GVK.GroupVersion().String() + " " + k.GVK.Kind
}

// Resolve returns the kinds that rules match, in the order the rules name them,
// each once. It reports false when a rule has a wildcard in its groups,
// versions or resources, when a resource is not one the engine has a schema
// for, such as a custom resource, and when the rules name no resource: the
// kinds are then not all known.
//
// Like the API server, it skips a rule that lacks groups, versions or
// resources, and it skips subresources such as deployments/scale: only the
// kind of the resource itself is typed.
func Resolve(rules []Rule) ([]Kind, bool) {
	var kinds []Kind
	var found []schema.GroupVersionKind
	for _, r := range rules {
		if len(r.Groups) == 0 || len(r.Versions) == 0 || len(r.Resources) == 0 {
			continue
		}
		if slices.ContainsFunc(r.Groups, wild) || slices.ContainsFunc(r.Versions, wild) {
			return nil, false
		}
		for _, res := range r.Resources {
			if strings.Contains(res, "/") {
				continue
			}
			if wild(res) {
				return nil, false
			}
			for _, g := range r.Groups {
				for _, v := range r.Versions {
					gvk, t, ok := kindOf(schema.GroupVersionResource{Group: g, Version: v, Resource: res})
					if !ok {
						return nil, false
					}
					if !slices.Contains(found, gvk) {
						found = append(found, gvk)
						kinds = append(kinds, Kind{GVK: gvk, Schema: schemaOf(t)})
					}
				}
			}
		}
	}
	return kinds, len(kinds) > 0
}

func wild(s string) bool { return strings.Contains(s, "*") }

// kindOf finds the kind served as the resource gvr.
func kindOf(gvr schema.GroupVersionResource) (schema.GroupVersionKind, reflect.Type, bool) {
	for kind, t := range scheme.Scheme.KnownTypes(gvr.GroupVersion()) {
		gvk := gvr.GroupVersion().WithKind(kind)
		if resourceName(gvk) == gvr.Resource && isResource(gvk, t) {
			return gvk, t, true
		}
	}
	return schema.GroupVersionKind{}, nil, false
}

// resourceName is the plural name the API server serves gvk under.
func resourceName(gvk schema.GroupVersionKind) string {
	if gvk.Kind == "Endpoints" {
		return "endpoints" // already plural
	}
	plural, _ := meta.UnsafeGuessKindToResource(gvk)
	return plural.Resource
}

// Lookup returns the built-in kind with the given API version, such as "v1" or
// "apps/v1", and kind name. It reports false for a kind the engine has no
// schema for, such as a custom resource.
func Lookup(apiVersion, kind string) (Kind, bool) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return Kind{}, false
	}
	gvk := gv.WithKind(kind)
	t, ok := scheme.Scheme.AllKnownTypes()[gvk]
	if !ok || !isResource(gvk, t) {
		return Kind{}, false
	}
	return Kind{GVK: gvk, Schema: schemaOf(t)}, true
}
