package assist_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// vpol returns a Kyverno ValidatingPolicy that matches rule, a flow mapping,
// with a variable named replicas and expression as its only validation.
func vpol(rule, expression string) string {
	return `apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - ` + rule + `
  variables:
    - name: replicas
      expression: "1"
  validations:
    - expression: ` + expression + `
`
}

func labelsAt(t *testing.T, a *assist.Assistant, doc, needle string, offset int) []string {
	t.Helper()
	pos := posIn(t, doc, needle, offset)
	items, ok := a.Complete(assist.Parse(context.Background(), "policy.yaml", []byte(doc)), pos)
	if !ok {
		t.Fatalf("Complete(%v) = none, want items", pos)
	}
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	return labels
}

func TestKyvernoComplete(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, doc, needle string
		offset            int
		has, lacks        []string
	}{
		{
			name: "top level", doc: vpol(deploymentRule, "tr"), needle: "tr\n", offset: 2,
			has:   []string{"object", "oldObject", "request", "variables", "resource", "http", "globalContext", "image", "isImage", "math", "time", "size"},
			lacks: []string{"authorizer", "params", "self", "oldSelf"},
		},
		{
			name: "after resource.", doc: vpol(deploymentRule, "resource.Get('v1', 'pods', 'a', 'b')"), needle: "resource.Get", offset: len("resource."),
			has:   []string{"Get", "List", "Post", "get", "list"},
			lacks: []string{"Client", "startsWith", "all"},
		},
		{
			name: "after http.", doc: vpol(deploymentRule, "http.Get('https://x')"), needle: "http.Get", offset: len("http."),
			has:   []string{"Get", "Post", "Client"},
			lacks: []string{"List", "GetMetadata"},
		},
		{
			name: "after a namespace", doc: vpol(deploymentRule, "time.now()"), needle: "time.now", offset: len("time."),
			has: []string{"now", "toCron", "truncate"},
		},
		{
			name: "variables", doc: vpol(deploymentRule, "variables.replicas > 1"), needle: "variables.replicas", offset: len("variables."),
			has:   []string{"replicas"},
			lacks: []string{"nope"},
		},
		{
			name: "object fields", doc: vpol(deploymentRule, "object.spec.replicas > 1"), needle: "object.spec", offset: len("object."),
			has: []string{"apiVersion", "kind", "metadata", "spec", "status"},
		},
		{
			name: "object fields down the tree", doc: vpol(deploymentRule, "object.spec.template.spec.hostNetwork"), needle: "object.spec.template", offset: len("object.spec."),
			has: []string{"replicas", "selector", "strategy", "template"},
		},
		{
			name: "oldObject fields", doc: vpol(deploymentRule, "oldObject.spec.replicas > 1"), needle: "oldObject.spec", offset: len("oldObject."),
			has: []string{"apiVersion", "spec"},
		},
		{
			name: "object stays dyn for a wildcard", doc: vpol("{apiGroups: ['*'], apiVersions: [v1], resources: [pods]}", "object.spec.nodeName == ''"), needle: "object.spec", offset: len("object."),
			has: []string{"size", "all"}, lacks: []string{"apiVersion", "metadata"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			labels := labelsAt(t, a, tt.doc, tt.needle, tt.offset)
			for _, w := range tt.has {
				if !slices.Contains(labels, w) {
					t.Errorf("Complete(%s) lacks %q: %v", tt.name, w, labels)
				}
			}
			for _, w := range tt.lacks {
				if slices.Contains(labels, w) {
					t.Errorf("Complete(%s) offers %q, which Kyverno lacks", tt.name, w)
				}
			}
		})
	}
}

// TestKyvernoCompleteSeveralKinds checks that shared fields come first and name the kinds.
func TestKyvernoCompleteSeveralKinds(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	doc := vpol(bothRule, "object.spec.replicas > 1")
	items, ok := a.Complete(assist.Parse(context.Background(), "policy.yaml", []byte(doc)), posIn(t, doc, "object.spec.replicas", len("object.spec.")))
	if !ok {
		t.Fatal("Complete() = none, want items")
	}
	keys := map[string]string{}
	for _, it := range items {
		if it.Kind == assist.KindField {
			keys[it.Label] = it.SortKey + " " + it.Detail
		}
	}
	for label, want := range map[string]string{
		"replicas":    "0replicas int · Deployment, StatefulSet",
		"strategy":    "1strategy object · Deployment",
		"serviceName": "1serviceName string · StatefulSet",
	} {
		if keys[label] != want {
			t.Errorf("%s = %q, want %q", label, keys[label], want)
		}
	}
}

func TestKyvernoHover(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	doc := vpol(deploymentRule, "isImage(object.spec.template.spec.containers[0].image) && image('a:b').tag() == 'b' && resource.List('v1', 'pods', 'ns').items.size() > 0 && variables.replicas > 1 && object.spec.replicas > 1")
	tests := []struct {
		name, needle string
		offset       int
		want         []string
		lacks        string
	}{
		{"global function", "isImage(", 2, []string{"(function) isImage(string) -> bool", "True when the string is a valid image reference."}, "kubernetes.io"},
		{"image function", "image('a", 2, []string{"(function) image(string) -> kyverno.image"}, ""},
		{"member function", "tag()", 1, []string{"(function) kyverno.image.tag() -> string", "The tag of an image reference"}, ""},
		{"method of a library variable", "List(", 1, []string{"(function) resource.Context.List(", "Lists the resources"}, ""},
		{"variable", "resource.List", 2, []string{"(variable) resource: resource.Context"}, ""},
		{"variables", "variables.replicas", 2, []string{"(variable) variables: kyverno.variables"}, ""},
		{"policy variable", "variables.replicas", len("variables.") + 2, []string{"(field) replicas: ", "Policy variable replicas"}, ""},
		{"object", "object.spec.replicas >", 2, []string{"(variable) object: apps/v1 Deployment"}, ""},
		{"object field", "spec.replicas >", len("spec.") + 2, []string{"(field) replicas: int"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pos := posIn(t, doc, tt.needle, tt.offset)
			got, ok := a.Hover(assist.Parse(context.Background(), "policy.yaml", []byte(doc)), pos)
			if !ok {
				t.Fatalf("Hover(%v) = none", pos)
			}
			for _, w := range tt.want {
				if !strings.Contains(got.Markdown, w) {
					t.Errorf("Hover(%s) = %q, want it to contain %q", tt.name, got.Markdown, w)
				}
			}
			if tt.lacks != "" && strings.Contains(got.Markdown, tt.lacks) {
				t.Errorf("Hover(%s) = %q, want no link to the Kubernetes docs", tt.name, got.Markdown)
			}
		})
	}
}

// TestKyvernoVariablesInOrder checks that a policy variable sees only the
// variables before it, and match conditions see none.
func TestKyvernoVariablesInOrder(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	doc := `apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: demo
spec:
  matchConditions:
    - name: m
      expression: "vari"
  variables:
    - name: first
      expression: "variables.x"
    - name: second
      expression: "variables.first"
  validations:
    - expression: "variables.second"
`
	if labels := labelsAt(t, a, doc, "variables.x", len("variables.")); len(labels) != 0 {
		t.Errorf("Complete(variables. in the first variable) = %v, want none", labels)
	}
	if labels := labelsAt(t, a, doc, "variables.first\"\n  validations", len("variables.")); !slices.Equal(labels, []string{"first"}) {
		t.Errorf("Complete(variables. in the second variable) = %v, want [first]", labels)
	}
	if labels := labelsAt(t, a, doc, "variables.second", len("variables.")); !slices.Equal(slices.Sorted(slices.Values(labels)), []string{"first", "second"}) {
		t.Errorf("Complete(variables. in a validation) = %v, want [first second]", labels)
	}
	if labels := labelsAt(t, a, doc, "vari\"", len("vari")); slices.Contains(labels, "variables") {
		t.Error("Complete(in a match condition) offers variables")
	}
}
