package matchkind

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// TestClusterScoped holds clusterScoped to the typed clients of
// k8s.io/client-go/kubernetes: a kind is cluster-scoped exactly when the
// method of its group's client that returns its client takes no namespace.
func TestClusterScoped(t *testing.T) {
	t.Parallel()
	want := map[schema.GroupKind]bool{}
	clientset := reflect.TypeFor[kubernetes.Interface]()
	for i := range clientset.NumMethod() {
		group := clientset.Method(i).Type.Out(0)
		for j := range group.NumMethod() {
			m := group.Method(j)
			if m.Type.NumOut() != 1 || m.Type.Out(0).Kind() != reflect.Interface {
				continue
			}
			get, ok := m.Type.Out(0).MethodByName("Get")
			if !ok || get.Type.NumOut() != 2 || get.Type.Out(0).Kind() != reflect.Pointer {
				continue
			}
			obj, ok := reflect.New(get.Type.Out(0).Elem()).Interface().(runtime.Object)
			if !ok {
				continue
			}
			kinds, _, err := scheme.Scheme.ObjectKinds(obj)
			if err != nil {
				continue
			}
			if m.Type.NumIn() == 0 {
				want[kinds[0].GroupKind()] = true
			}
		}
	}
	if !reflect.DeepEqual(clusterScoped, want) {
		for gk := range want {
			if !clusterScoped[gk] {
				t.Errorf("clusterScoped lacks %v", gk)
			}
		}
		for gk := range clusterScoped {
			if !want[gk] {
				t.Errorf("clusterScoped has %v, which client-go serves in a namespace", gk)
			}
		}
	}
}

func TestResource(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		gvk        schema.GroupVersionKind
		resource   string
		namespaced bool
		ok         bool
	}{
		{gvk: schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, resource: "deployments", namespaced: true, ok: true},
		{gvk: schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}, resource: "namespaces", ok: true},
		{gvk: schema.GroupVersionKind{Version: "v1", Kind: "Endpoints"}, resource: "endpoints", namespaced: true, ok: true},
		{gvk: schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}},
		{gvk: schema.GroupVersionKind{Version: "v1", Kind: "PodList"}},
	} {
		gvr, namespaced, ok := Resource(tc.gvk)
		if ok != tc.ok || gvr.Resource != tc.resource || namespaced != tc.namespaced {
			t.Errorf("Resource(%v) = %v, %v, %v, want %s, %v, %v", tc.gvk, gvr, namespaced, ok, tc.resource, tc.namespaced, tc.ok)
		}
	}
}
