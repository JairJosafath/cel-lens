package cellens_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
)

func TestParsePath(t *testing.T) {
	t.Parallel()
	field := func(n string) cellens.Step { return cellens.Step{Kind: cellens.FieldStep, Name: n} }
	items := cellens.Step{Kind: cellens.ItemsStep}
	values := cellens.Step{Kind: cellens.ValuesStep}
	tests := []struct {
		in   string
		want cellens.Path
	}{
		{"", nil},
		{"spec", cellens.Path{field("spec")}},
		{"spec.config", cellens.Path{field("spec"), field("config")}},
		{"spec.ports[].config", cellens.Path{field("spec"), field("ports"), items, field("config")}},
		{"metadata.labels[*]", cellens.Path{field("metadata"), field("labels"), values}},
		{"[]", cellens.Path{items}},
		{"a[][*].b", cellens.Path{field("a"), items, values, field("b")}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := cellens.ParsePath(tt.in)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParsePath(%q) = %v, %v, want %v", tt.in, got, err, tt.want)
			}
			if s := got.String(); s != tt.in {
				t.Errorf("ParsePath(%q).String() = %q, want %q", tt.in, s, tt.in)
			}
		})
	}
}

func TestParsePathErrors(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, words string }{
		{"spec..config", "missing"},
		{"spec.", "missing"},
		{".spec", "missing"},
		{"spec]", "unexpected ]"},
		{"spec[x]", "missing"},
		{"spec[]x", "want . or ["},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if _, err := cellens.ParsePath(tt.in); err == nil || !strings.Contains(err.Error(), tt.words) {
				t.Errorf("ParsePath(%q) error = %v, want one saying %q", tt.in, err, tt.words)
			}
		})
	}
}
