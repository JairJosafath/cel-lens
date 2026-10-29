package assist_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// with returns an Assistant with the experimental features on.
func with(t *testing.T, on ...feature.Feature) *assist.Assistant {
	t.Helper()
	a, err := assist.New(kubeversion.Default(), assist.WithFeatures(feature.Of(on...)))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func completeLabels(t *testing.T, a *assist.Assistant, text, needle string, offset int, opts ...assist.Option) (labels []string, ok bool) {
	t.Helper()
	items, ok := a.Complete(assist.Parse(context.Background(), "f.yaml", []byte(text), opts...), posIn(t, text, needle, offset))
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	return labels, ok
}

func TestKyvernoOffAnswersNothing(t *testing.T) {
	t.Parallel()
	doc := vpol(deploymentRule, "object.spec.replicas > 1")
	for _, tt := range []struct {
		name string
		on   []feature.Feature
		want bool
	}{
		{"off", nil, false},
		{"other features", []feature.Feature{feature.CRDTypedPolicies, feature.FittingCompletion}, false},
		{"on", []feature.Feature{feature.Kyverno}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := with(t, tt.on...)
			parsed := assist.Parse(context.Background(), "p.yaml", []byte(doc))
			if _, ok := a.Hover(parsed, posIn(t, doc, "object.spec", 1)); ok != tt.want {
				t.Errorf("Hover(object) ok = %v, want %v", ok, tt.want)
			}
			if _, ok := completeLabels(t, a, doc, "object.spec", len("object.")); ok != tt.want {
				t.Errorf("Complete(object.) ok = %v, want %v", ok, tt.want)
			}
			if _, ok := a.Suggest(parsed, posIn(t, doc, "object.spec", 0), posIn(t, doc, "object.spec", len("object.spec"))); ok && !tt.want {
				t.Error("Suggest(object.spec) ok = true, want false")
			}
		})
	}
}

func TestCRDTypedPoliciesOff(t *testing.T) {
	t.Parallel()
	var crds check.CRDs
	crds.Set(context.Background(), "crd.yaml", []byte(paramsWidgetCRD))
	doc := withParamKind("{apiVersion: example.com/v1, kind: Widget}", "params.spec.maxReplicas > 1")
	off := with(t)
	parsed := assist.Parse(context.Background(), "policy.yaml", []byte(doc), assist.WithCRDs(&crds))
	h, ok := off.Hover(parsed, posIn(t, doc, "params.spec", 1))
	if !ok {
		t.Fatal("Hover(params) = none, want a hover")
	}
	for _, want := range []string{"params: dyn", "experimental", "off", "crd-typed-policies"} {
		if !strings.Contains(h.Markdown, want) {
			t.Errorf("Hover(params) with the feature off = %q, want it to contain %q", h.Markdown, want)
		}
	}
	if labels, _ := completeLabels(t, off, doc, "params.spec", len("params."), assist.WithCRDs(&crds)); slices.Contains(labels, "spec") {
		t.Errorf("Complete(params.) with the feature off = %v, want no fields", labels)
	}

	on := with(t, feature.CRDTypedPolicies)
	if labels, _ := completeLabels(t, on, doc, "params.spec", len("params."), assist.WithCRDs(&crds)); !slices.Contains(labels, "spec") {
		t.Errorf("Complete(params.) with the feature on = %v, want spec", labels)
	}
	if h, ok := on.Hover(parsed, posIn(t, doc, "params.spec", 1)); !ok || !strings.Contains(h.Markdown, "params: example.com/v1 Widget") {
		t.Errorf("Hover(params) with the feature on = %q, want it typed example.com/v1 Widget", h.Markdown)
	}
}

// TestFittingCompletionOff checks that completion and hover are those of
// v0.17.1 without the feature: a dyn receiver offers every member, oldSelf is
// offered where the API server rejects it, and hover gives no reason for dyn.
func TestFittingCompletionOff(t *testing.T) {
	t.Parallel()
	off, on := with(t), with(t, feature.FittingCompletion)

	t.Run("dyn receiver", func(t *testing.T) {
		t.Parallel()
		doc := receiverCRD("self.any.all(x, true)")
		gotOff, _ := completeLabels(t, off, doc, "self.any.all", len("self.any."))
		gotOn, _ := completeLabels(t, on, doc, "self.any.all", len("self.any."))
		for _, l := range []string{"startsWith", "isSorted", "size"} {
			if !slices.Contains(gotOff, l) {
				t.Errorf("Complete(self.any.) off = %v, want %q", gotOff, l)
			}
			if slices.Contains(gotOn, l) {
				t.Errorf("Complete(self.any.) on = %v, want no %q", gotOn, l)
			}
		}
	})

	t.Run("oldSelf on the uncorrelatable portion", func(t *testing.T) {
		t.Parallel()
		doc := crdWith(setItems)
		gotOff, _ := completeLabels(t, off, doc, "zz\n", 2)
		gotOn, _ := completeLabels(t, on, doc, "zz\n", 2)
		if !slices.Contains(gotOff, "oldSelf") {
			t.Errorf("Complete(zz) off = %v, want oldSelf", gotOff)
		}
		if slices.Contains(gotOn, "oldSelf") {
			t.Errorf("Complete(zz) on = %v, want no oldSelf", gotOn)
		}
	})

	t.Run("hover on dyn", func(t *testing.T) {
		t.Parallel()
		doc := vapMatching("  matchConstraints:\n    resourceRules:\n      - apiGroups: ['*']\n        apiVersions: ['*']\n        resources: ['*']\n", "object.spec == null")
		parsed := assist.Parse(context.Background(), "p.yaml", []byte(doc))
		hOff, ok := off.Hover(parsed, posIn(t, doc, "object.spec", 1))
		if !ok || strings.Contains(hOff.Markdown, "dyn: ") {
			t.Errorf("Hover(object.spec) off = %q, want no reason for dyn", hOff.Markdown)
		}
		if hOn, ok := on.Hover(parsed, posIn(t, doc, "object.spec", 1)); !ok || !strings.Contains(hOn.Markdown, "dyn: ") {
			t.Errorf("Hover(object.spec) on = %q, want a reason for dyn", hOn.Markdown)
		}
	})
}
