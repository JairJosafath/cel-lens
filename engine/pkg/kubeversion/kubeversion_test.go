package kubeversion_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"1.37", "1.37", false},
		{"1.33", "1.33", false},
		{"v1.36", "1.36", false},
		{"1.32", "", true},
		{"1.38", "", true},
		{"1.037", "", true},
		{"2.37", "", true},
		{"1.37.1", "", true},
		{"", "", true},
		{"latest", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := kubeversion.Parse(tt.in)
			if tt.wantErr {
				if !errors.Is(err, kubeversion.ErrUnsupported) || !strings.Contains(err.Error(), "1.33, 1.34, 1.35, 1.36, 1.37") {
					t.Errorf("Parse(%q) error = %v, want ErrUnsupported listing 1.33, 1.34, 1.35, 1.36, 1.37", tt.in, err)
				}
				return
			}
			if err != nil || got.String() != tt.want {
				t.Errorf("Parse(%q) = %v, %v, want %s", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestDefaultAndRange(t *testing.T) {
	t.Parallel()
	var zero kubeversion.Version
	if zero.String() != kubeversion.Default().String() {
		t.Errorf("zero Version = %v, want the default %v", zero, kubeversion.Default())
	}
	if got := kubeversion.Default().String(); got != "1.37" {
		t.Errorf("Default() = %s, want 1.37", got)
	}
	sup := kubeversion.Supported()
	if len(sup) < 3 || sup[len(sup)-1] != kubeversion.Default() {
		t.Errorf("Supported() = %v, want at least three versions ending with the default", sup)
	}
	want := "matches Kubernetes 1.37; supports 1.33–1.37"
	if got := kubeversion.Summary(); got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}

func TestEnvSet(t *testing.T) {
	t.Parallel()
	v, _ := kubeversion.Parse("1.33")
	first, second := v.EnvSet(), v.EnvSet()
	if first != second {
		t.Error("EnvSet() builds a new environment on each call, want one per version")
	}
	if v.EnvSet() == kubeversion.Default().EnvSet() {
		t.Error("EnvSet() of 1.35 is the one of 1.37, want one per version")
	}
}
