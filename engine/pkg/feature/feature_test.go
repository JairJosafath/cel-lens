package feature_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		in          []string
		want        feature.Set
		wantUnknown []string
	}{
		{"none", nil, 0, nil},
		{"one", []string{"kyverno"}, feature.Of(feature.Kyverno), nil},
		{"spaces and empty", []string{" kyverno ", ""}, feature.Of(feature.Kyverno), nil},
		{"all", []string{"kyverno", "crd-typed-policies", "fitting-completion"}, feature.Of(feature.Known()...), nil},
		{"unknown once", []string{"nope", "kyverno", "nope", "other"}, feature.Of(feature.Kyverno), []string{"nope", "other"}},
		{"case matters", []string{"Kyverno"}, 0, []string{"Kyverno"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, unknown := feature.Parse(tt.in)
			if got != tt.want || !slices.Equal(unknown, tt.wantUnknown) {
				t.Errorf("Parse(%q) = %v, %q, want %v, %q", tt.in, got, unknown, tt.want, tt.wantUnknown)
			}
		})
	}
}

func TestSetHas(t *testing.T) {
	t.Parallel()
	var zero feature.Set
	for _, f := range feature.Known() {
		if zero.Has(f) {
			t.Errorf("zero Set.Has(%q) = true, want false", f)
		}
		if on := feature.Of(f); !on.Has(f) {
			t.Errorf("Of(%q).Has(%q) = false, want true", f, f)
		}
	}
	if feature.Of(feature.Kyverno).Has(feature.FittingCompletion) {
		t.Error("Of(kyverno).Has(fitting-completion) = true, want false")
	}
	if feature.Of("nope").Has("nope") {
		t.Error(`Of("nope").Has("nope") = true, want false`)
	}
}

func TestWarning(t *testing.T) {
	t.Parallel()
	if got := feature.Warning(nil); got != "" {
		t.Errorf("Warning(nil) = %q, want empty", got)
	}
	got := feature.Warning([]string{"nope"})
	for _, want := range []string{`"nope"`, "crd-typed-policies, fitting-completion, kyverno"} {
		if !strings.Contains(got, want) {
			t.Errorf("Warning([nope]) = %q, want it to contain %q", got, want)
		}
	}
}
