package check

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestParentField(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, field, want string }{
		{"name", "a.b", "a"},
		{"bracketed key", "a.properties[b]", "a.properties"},
		{"brackets in key", "a.properties[c[d]]", "a.properties"},
		{"nested brackets in key", "a.properties[c[d[e]]]", "a.properties"},
		{"key after bracketed key", "a.properties[c[d]].type", "a.properties[c[d]]"},
		{"only a bracketed key", "[0]", ""},
		{"top level", "a", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parentField(tt.field); got != tt.want {
				t.Errorf("parentField(%q) = %q, want %q", tt.field, got, tt.want)
			}
		})
	}
}

func TestWalkBracketedKeys(t *testing.T) {
	t.Parallel()
	var doc yaml.Node
	src := "properties:\n  c[d]: {a: 1}\n  e[f[g]]: {a: 2}\n"
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, field, want string }{
		{"brackets in key", "properties[c[d]].a", "1"},
		{"nested brackets in key", "properties[e[f[g]]].a", "2"},
		{"unclosed", "properties[c[d]", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ""
			if l := walk(&doc, tt.field); l.value != nil {
				got = l.value.Value
			}
			if got != tt.want {
				t.Errorf("walk(%q) = %q, want %q", tt.field, got, tt.want)
			}
		})
	}
}
