package matchkind_test

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
)

func TestResolve(t *testing.T) {
	t.Parallel()
	one := func(g, v, r string) []matchkind.Rule {
		return []matchkind.Rule{{Groups: []string{g}, Versions: []string{v}, Resources: []string{r}}}
	}
	tests := []struct {
		name  string
		rules []matchkind.Rule
		want  string // "" when the kind is not known
	}{
		{"deployments", one("apps", "v1", "deployments"), "apps/v1 Deployment"},
		{"core group", one("", "v1", "pods"), "v1 Pod"},
		{"irregular plural", one("", "v1", "endpoints"), "v1 Endpoints"},
		{"y plural", one("networking.k8s.io", "v1", "networkpolicies"), "networking.k8s.io/v1 NetworkPolicy"},
		{"same kind twice", []matchkind.Rule{
			{Groups: []string{"apps"}, Versions: []string{"v1"}, Resources: []string{"deployments"}},
			{Groups: []string{"apps"}, Versions: []string{"v1"}, Resources: []string{"deployments", "deployments/scale"}},
		}, "apps/v1 Deployment"},
		{"subresource beside the resource", one("apps", "v1", "deployments/scale"), ""},
		{"wildcard group", one("*", "v1", "pods"), ""},
		{"wildcard version", one("apps", "*", "deployments"), ""},
		{"wildcard resource", one("apps", "v1", "*"), ""},
		{"custom resource", one("example.com", "v1", "widgets"), ""},
		{"unserved version", one("apps", "v9", "deployments"), ""},
		{"singular name", one("apps", "v1", "deployment"), ""},
		{"list kind", one("apps", "v1", "deploymentlists"), ""},
		{"two kinds", []matchkind.Rule{{Groups: []string{"apps"}, Versions: []string{"v1"}, Resources: []string{"deployments", "statefulsets"}}}, "apps/v1 Deployment, apps/v1 StatefulSet"},
		{"two kinds, one unknown", []matchkind.Rule{{Groups: []string{"apps"}, Versions: []string{"v1"}, Resources: []string{"deployments", "widgets"}}}, ""},
		{"two kinds, one wildcard", []matchkind.Rule{
			{Groups: []string{"apps"}, Versions: []string{"v1"}, Resources: []string{"deployments"}},
			{Groups: []string{"*"}, Versions: []string{"v1"}, Resources: []string{"pods"}},
		}, ""},
		{"no rules", nil, ""},
		{"rule without resources", []matchkind.Rule{{Groups: []string{"apps"}, Versions: []string{"v1"}}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			kinds, ok := matchkind.Resolve(tt.rules)
			var names []string
			for _, k := range kinds {
				names = append(names, k.String())
			}
			if got := strings.Join(names, ", "); ok != (tt.want != "") || got != tt.want {
				t.Errorf("Resolve(%v) = %q, %v, want %q", tt.rules, got, ok, tt.want)
			}
		})
	}
}

func TestSchema(t *testing.T) {
	t.Parallel()
	kinds, ok := matchkind.Resolve([]matchkind.Rule{{Groups: []string{"apps"}, Versions: []string{"v1"}, Resources: []string{"deployments"}}})
	if !ok || len(kinds) != 1 {
		t.Fatal("Resolve(apps/v1 deployments) is not known")
	}
	raw, err := json.Marshal(kinds[0].Schema)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	at := func(path ...string) map[string]any {
		cur := root
		for _, p := range path {
			props, _ := cur["properties"].(map[string]any)
			next, ok := props[p].(map[string]any)
			if !ok {
				t.Fatalf("schema has no %v", path)
			}
			cur = next
		}
		return cur
	}
	for _, tt := range []struct {
		path       []string
		typ, field string
	}{
		{[]string{"apiVersion"}, "string", ""},
		{[]string{"metadata", "name"}, "string", ""},
		{[]string{"metadata", "creationTimestamp"}, "string", "date-time"},
		{[]string{"spec", "replicas"}, "integer", ""},
		{[]string{"spec", "template", "spec", "containers"}, "array", ""},
		{[]string{"spec", "selector", "matchLabels"}, "object", ""},
		{[]string{"status", "availableReplicas"}, "integer", ""},
	} {
		s := at(tt.path...)
		if s["type"] != tt.typ || tt.field != "" && s["format"] != tt.field {
			t.Errorf("schema of %v = type %v format %v, want %v %v", tt.path, s["type"], s["format"], tt.typ, tt.field)
		}
	}
	if d, _ := at("spec", "replicas")["description"].(string); d == "" {
		t.Error("spec.replicas has no description")
	}
}

func TestLookup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, apiVersion, kind string
		want                   string // "" when the kind is not known
	}{
		{"core kind", "v1", "ConfigMap", "v1 ConfigMap"},
		{"grouped kind", "apps/v1", "Deployment", "apps/v1 Deployment"},
		{"unknown kind", "v1", "Widget", ""},
		{"list kind", "v1", "ConfigMapList", ""},
		{"unserved version", "apps/v9", "Deployment", ""},
		{"custom group", "example.com/v1", "Widget", ""},
		{"bad api version", "a/b/c", "ConfigMap", ""},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			k, ok := matchkind.Lookup(tt.apiVersion, tt.kind)
			if ok != (tt.want != "") || ok && k.String() != tt.want || ok && k.Schema == nil {
				t.Errorf("Lookup(%q, %q) = %q, %v, want %q", tt.apiVersion, tt.kind, k.String(), ok, tt.want)
			}
		})
	}
}

// TestKinds checks that Kinds lists the built-in kinds a version serves, by
// their prerelease lifecycle, sorted, with their descriptions (#4).
func TestKinds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		minor int
		kind  string
		want  bool
	}{
		{"a kind without a lifecycle", 33, "apps/v1 Deployment", true},
		{"the core group", 33, "v1 ConfigMap", true},
		{"an alpha kind introduced before", 33, "admissionregistration.k8s.io/v1alpha1 MutatingAdmissionPolicy", true},
		{"a beta kind introduced after", 33, "admissionregistration.k8s.io/v1beta1 MutatingAdmissionPolicy", false},
		{"a beta kind introduced then", 34, "admissionregistration.k8s.io/v1beta1 MutatingAdmissionPolicy", true},
		{"a GA kind introduced after", 35, "admissionregistration.k8s.io/v1 MutatingAdmissionPolicy", false},
		{"a GA kind introduced then", 36, "admissionregistration.k8s.io/v1 MutatingAdmissionPolicy", true},
		{"a list", 37, "apps/v1 DeploymentList", false},
		{"no resource", 37, "v1 Status", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kinds := matchkind.Kinds(tc.minor)
			got := slices.ContainsFunc(kinds, func(k matchkind.Listed) bool {
				return k.GVK.GroupVersion().String()+" "+k.GVK.Kind == tc.kind
			})
			if got != tc.want {
				t.Errorf("Kinds(%d) has %s = %t, want %t", tc.minor, tc.kind, got, tc.want)
			}
		})
	}
	kinds := matchkind.Kinds(37)
	sorted := slices.IsSortedFunc(kinds, func(a, b matchkind.Listed) int {
		return cmp.Or(cmp.Compare(a.GVK.Group, b.GVK.Group), cmp.Compare(a.GVK.Version, b.GVK.Version), cmp.Compare(a.GVK.Kind, b.GVK.Kind))
	})
	if !sorted {
		t.Error("Kinds(37) is not sorted by group, version and kind")
	}
	i := slices.IndexFunc(kinds, func(k matchkind.Listed) bool { return k.GVK.Group == "apps" && k.GVK.Kind == "Deployment" })
	const want = "Deployment enables declarative updates for Pods and ReplicaSets."
	if i < 0 || kinds[i].Description != want {
		t.Errorf("Kinds(37) lists apps/v1 Deployment at %d, want it with the description %q", i, want)
	}
}
