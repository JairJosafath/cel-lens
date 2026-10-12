package matchkind_test

import (
	"encoding/json"
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
