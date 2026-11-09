// Package matchkind finds the built-in Kubernetes resource a policy matches,
// and the schema of that resource, as the API server does when it type-checks
// a ValidatingAdmissionPolicy against spec.matchConstraints.resourceRules.
//
// The schemas come from the Go types of k8s.io/api, with the descriptions of
// their generated swagger docs, so they are those of the Kubernetes version the
// engine was built with.
package matchkind

import (
	"cmp"
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
func Resolve(rules []Rule) ([]Kind, bool) { return ResolveWith(rules, nil) }

// ResolveWith is Resolve with custom, when it is not nil, asked for each
// resource that is no built-in one; it returns the kind served as that resource
// and reports false for one it does not know either.
func ResolveWith(rules []Rule, custom func(schema.GroupVersionResource) (Kind, bool)) ([]Kind, bool) {
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
					gvr := schema.GroupVersionResource{Group: g, Version: v, Resource: res}
					var kind Kind
					if gvk, t, ok := kindOf(gvr); ok {
						kind = Kind{GVK: gvk, Schema: schemaOf(t)}
					} else if kind, ok = lookupCustom(custom, gvr); !ok {
						return nil, false
					}
					if !slices.Contains(found, kind.GVK) {
						found = append(found, kind.GVK)
						kinds = append(kinds, kind)
					}
				}
			}
		}
	}
	return kinds, len(kinds) > 0
}

func lookupCustom(custom func(schema.GroupVersionResource) (Kind, bool), gvr schema.GroupVersionResource) (Kind, bool) {
	if custom == nil {
		return Kind{}, false
	}
	return custom(gvr)
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

// Listed is a built-in kind as Kinds lists it.
type Listed struct {
	// GVK is the group, version and kind, such as apps/v1 Deployment.
	GVK schema.GroupVersionKind
	// Description is the kind's description from its generated swagger doc,
	// "" when it has none.
	Description string
}

// Kinds returns the built-in resource kinds that an API server of Kubernetes
// 1.minor serves, sorted by group, version and kind: the kinds Lookup knows
// whose API version was introduced by 1.minor and not yet removed, as the
// prerelease lifecycle k8s.io/api generates for a type says. A type without
// one is in every version.
func Kinds(minor int) []Listed {
	var out []Listed
	for gvk, t := range scheme.Scheme.AllKnownTypes() {
		if !isResource(gvk, t) || !served(t, minor) {
			continue
		}
		out = append(out, Listed{GVK: gvk, Description: docs(t)[""]})
	}
	slices.SortFunc(out, func(a, b Listed) int {
		return cmp.Or(cmp.Compare(a.GVK.Group, b.GVK.Group), cmp.Compare(a.GVK.Version, b.GVK.Version), cmp.Compare(a.GVK.Kind, b.GVK.Kind))
	})
	return out
}

// The methods k8s.io/api generates for a type's prerelease lifecycle.
type (
	introduced interface{ APILifecycleIntroduced() (major, minor int) }
	removed    interface{ APILifecycleRemoved() (major, minor int) }
)

// served reports whether Kubernetes 1.minor serves the type t, as its
// prerelease lifecycle says.
func served(t reflect.Type, minor int) bool {
	v := reflect.New(t).Interface()
	if i, ok := v.(introduced); ok {
		if major, m := i.APILifecycleIntroduced(); major > 1 || major == 1 && m > minor {
			return false
		}
	}
	if r, ok := v.(removed); ok {
		if major, m := r.APILifecycleRemoved(); major < 1 || major == 1 && m <= minor {
			return false
		}
	}
	return true
}
