package assist_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// matching returns a ValidatingAdmissionPolicy that matches rule, a flow
// mapping, with expression as its only validation.
func matching(rule, expression string) string {
	return `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - ` + rule + `
  validations:
    - expression: ` + expression + `
`
}

const deploymentRule = "{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}"

func TestTypedComplete(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, doc, needle string
		offset            int
		want              []string // every label offered, when typed
		wantSome          []string // labels that must be offered, when not typed
	}{
		{
			name: "object", doc: matching(deploymentRule, "object.spec.replicas > 1"), needle: "object.spec", offset: len("object."),
			want: []string{"apiVersion", "kind", "metadata", "spec", "status"},
		},
		{
			name: "oldObject", doc: matching(deploymentRule, "oldObject.spec.replicas > 1"), needle: "oldObject.spec", offset: len("oldObject."),
			want: []string{"apiVersion", "kind", "metadata", "spec", "status"},
		},
		{
			name: "down the tree", doc: matching(deploymentRule, "object.spec.template.spec.hostNetwork"), needle: "object.spec.template", offset: len("object.spec."),
			want: []string{"minReadySeconds", "paused", "progressDeadlineSeconds", "replicas", "revisionHistoryLimit", "selector", "strategy", "template"},
		},
		{
			name: "list of containers", doc: matching(deploymentRule, "object.spec.template.spec.containers.all(c, c.name != '')"), needle: "containers.all", offset: len("containers."),
			wantSome: []string{"all", "exists", "size"},
		},
		{
			name: "core group", doc: matching("{apiGroups: [''], apiVersions: [v1], resources: [pods]}", "object.spec.nodeName == ''"), needle: "object.spec", offset: len("object."),
			want: []string{"apiVersion", "kind", "metadata", "spec", "status"},
		},
		{
			name: "wildcard stays dyn", doc: matching("{apiGroups: ['*'], apiVersions: [v1], resources: [pods]}", "object.spec.nodeName == ''"), needle: "object.spec", offset: len("object."),
			wantSome: []string{"size", "all", "startsWith"},
		},
		{
			name: "custom resource stays dyn", doc: matching("{apiGroups: [example.com], apiVersions: [v1], resources: [widgets]}", "object.spec.size == 1"), needle: "object.spec", offset: len("object."),
			wantSome: []string{"size", "all", "startsWith"},
		},
		{
			name: "subresource only stays dyn", doc: matching("{apiGroups: [apps], apiVersions: [v1], resources: [deployments/scale]}", "object.spec.size == 1"), needle: "object.spec", offset: len("object."),
			wantSome: []string{"size", "all", "startsWith"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pos := posIn(t, tt.doc, tt.needle, tt.offset)
			items, ok := a.Complete(assist.Parse(context.Background(), "policy.yaml", []byte(tt.doc)), pos)
			if !ok {
				t.Fatalf("Complete(%v) = none, want items", pos)
			}
			var labels []string
			for _, it := range items {
				labels = append(labels, it.Label)
			}
			slices.Sort(labels)
			if tt.want != nil && !slices.Equal(labels, tt.want) {
				t.Errorf("Complete(%v) = %v, want %v", pos, labels, tt.want)
			}
			for _, w := range tt.wantSome {
				if !slices.Contains(labels, w) {
					t.Errorf("Complete(%v) lacks %q", pos, w)
				}
			}
		})
	}
}

func TestTypedHover(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	doc := matching(deploymentRule, "object.spec.replicas > 1 && oldObject.metadata.name != ''")
	tests := []struct {
		name, needle string
		offset       int
		want         []string
	}{
		{"object names the kind", "object.spec", 2, []string{"(variable) object: apps/v1 Deployment"}},
		{"oldObject names the kind", "oldObject.metadata", 2, []string{"(variable) oldObject: apps/v1 Deployment"}},
		{"field has type and description", "replicas > 1", 2, []string{"(field) replicas: int", "Number of desired pods."}},
		{"nested field", "metadata.name", 11, []string{"(field) name: string", "Name must be unique within a namespace."}},
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
					t.Errorf("Hover(%v) = %q, want it to contain %q", pos, got.Markdown, w)
				}
			}
		})
	}
}

const bothRule = "{apiGroups: [apps], apiVersions: [v1], resources: [deployments, statefulsets]}"

// TestSeveralKindsComplete checks that completion after object. offers the
// fields every matched kind has first, names the kinds on each item, and that
// a kind that can't be known keeps object dyn.
func TestSeveralKindsComplete(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	complete := func(t *testing.T, doc, needle string, offset int) []assist.Item {
		t.Helper()
		pos := posIn(t, doc, needle, offset)
		items, ok := a.Complete(assist.Parse(context.Background(), "policy.yaml", []byte(doc)), pos)
		if !ok {
			t.Fatalf("Complete(%v) = none, want items", pos)
		}
		return items
	}
	byKey := func(items []assist.Item) []assist.Item {
		var fields []assist.Item
		for _, it := range items {
			if it.Kind == assist.KindField {
				fields = append(fields, it)
			}
		}
		slices.SortFunc(fields, func(x, y assist.Item) int { return strings.Compare(x.SortKey, y.SortKey) })
		return fields
	}
	t.Run("spec fields", func(t *testing.T) {
		t.Parallel()
		doc := matching(bothRule, "object.spec.replicas > 1")
		fields := byKey(complete(t, doc, "object.spec.replicas", len("object.spec.")))
		var labels []string
		for _, f := range fields {
			labels = append(labels, f.Label)
		}
		shared := []string{"minReadySeconds", "replicas", "revisionHistoryLimit", "selector", "template"}
		others := []string{
			"paused", "persistentVolumeClaimRetentionPolicy", "podManagementPolicy", "progressDeadlineSeconds",
			"ordinals", "serviceName", "strategy", "updateStrategy", "volumeClaimTemplates",
		}
		slices.Sort(others)
		if want := slices.Concat(shared, others); !slices.Equal(labels, want) {
			t.Errorf("fields after object.spec. = %v, want %v", labels, want)
		}
		details := map[string]string{}
		for _, f := range fields {
			details[f.Label] = f.Detail
		}
		for label, want := range map[string]string{
			"replicas":    "int · Deployment, StatefulSet",
			"strategy":    "object · Deployment",
			"serviceName": "string · StatefulSet",
		} {
			if details[label] != want {
				t.Errorf("detail of %s = %q, want %q", label, details[label], want)
			}
		}
	})
	t.Run("top level fields are shared", func(t *testing.T) {
		t.Parallel()
		fields := byKey(complete(t, matching(bothRule, "object.spec.replicas > 1"), "object.spec", len("object.")))
		for _, f := range fields {
			if !strings.HasPrefix(f.SortKey, "0") || !strings.HasSuffix(f.Detail, "· Deployment, StatefulSet") {
				t.Errorf("item %q = %+v, want a field both kinds have", f.Label, f)
			}
		}
	})
	t.Run("same kind in two versions", func(t *testing.T) {
		t.Parallel()
		doc := matching("{apiGroups: [apps], apiVersions: [v1, v1beta2], resources: [deployments]}", "object.spec.replicas > 1")
		for _, f := range byKey(complete(t, doc, "object.spec", len("object."))) {
			if f.Label == "spec" && f.Detail != "object · apps/v1 Deployment, apps/v1beta2 Deployment" {
				t.Errorf("detail of spec = %q, want both versions named", f.Detail)
			}
		}
	})
	t.Run("one unknown kind stays dyn", func(t *testing.T) {
		t.Parallel()
		doc := matching("{apiGroups: [apps], apiVersions: [v1], resources: [deployments, widgets]}", "object.spec.replicas > 1")
		if fields := byKey(complete(t, doc, "object.spec", len("object."))); len(fields) != 0 {
			t.Errorf("fields = %v, want none for a dyn object", fields)
		}
	})
}

// TestSeveralKindsHover checks that hover on object lists the matched kinds and
// hover on a field says which kinds have it.
func TestSeveralKindsHover(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	doc := matching(bothRule, "object.spec.replicas > 1 && object.spec.serviceName != ''")
	tests := []struct {
		name, needle string
		offset       int
		want         []string
	}{
		{"object", "object.spec.replicas", 2, []string{"(variable) object: apps/v1 Deployment, apps/v1 StatefulSet"}},
		{"shared field", "replicas >", 2, []string{"(field) replicas: int", "In: Deployment, StatefulSet", "Number of desired pods."}},
		{"field of one kind", "serviceName !=", 2, []string{"(field) serviceName: string", "In: StatefulSet"}},
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
					t.Errorf("Hover(%v) = %q, want it to contain %q", pos, got.Markdown, w)
				}
			}
		})
	}
}
