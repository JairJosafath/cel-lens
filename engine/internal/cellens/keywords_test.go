package cellens_test

import (
	"slices"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
)

func names(words []cellens.Word) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.Name
	}
	return out
}

// TestSchemaKeywords checks the keywords offered at a key of a schema: type
// first while it has none, then those of its type, without those it has
// (#16).
func TestSchemaKeywords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		keys map[string]string
		want []string
	}{
		{"no type yet", map[string]string{}, []string{"type", "x-kubernetes-int-or-string", "x-kubernetes-preserve-unknown-fields", "description", "nullable", "enum"}},
		{"an object with properties", map[string]string{"type": "object", "properties": ""}, []string{"description", "nullable", "enum", "required", "maxProperties", "minProperties", "x-kubernetes-preserve-unknown-fields", "x-kubernetes-embedded-resource", "x-kubernetes-map-type"}},
		{"a map", map[string]string{"type": "object", "additionalProperties": ""}, []string{"description", "nullable", "enum", "maxProperties", "minProperties", "x-kubernetes-preserve-unknown-fields", "x-kubernetes-embedded-resource", "x-kubernetes-map-type"}},
		{"a string with a description", map[string]string{"type": "string", "description": "x"}, []string{"nullable", "enum", "format", "maxLength", "minLength", "pattern"}},
		{"an int or a string", map[string]string{"x-kubernetes-int-or-string": "true"}, []string{"description", "nullable", "enum"}},
	}
	for _, tt := range tests {
		if got := names(cellens.SchemaKeywords(tt.keys)); !slices.Equal(got, tt.want) {
			t.Errorf("%s: SchemaKeywords(%v) = %q, want %q", tt.name, tt.keys, got, tt.want)
		}
	}
	for _, w := range cellens.SchemaKeywords(map[string]string{}) {
		if w.Doc == "" {
			t.Errorf("keyword %s has no description", w.Name)
		}
	}
}

func TestSchemaValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key  string
		keys map[string]string
		want []string
	}{
		{"type", nil, []string{"object", "array", "string", "integer", "number", "boolean"}},
		{"format", map[string]string{"type": "integer"}, []string{"int32", "int64"}},
		{"x-kubernetes-list-type", nil, []string{"atomic", "set", "map"}},
		{"nullable", nil, []string{"true", "false"}},
		{"description", nil, nil},
	}
	for _, tt := range tests {
		if got := names(cellens.SchemaValues(tt.key, tt.keys)); !slices.Equal(got, tt.want) {
			t.Errorf("SchemaValues(%q, %v) = %q, want %q", tt.key, tt.keys, got, tt.want)
		}
	}
}

func TestSchemaAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path []string
		want bool
	}{
		{nil, true},
		{[]string{"properties"}, false},
		{[]string{"properties", "max"}, true},
		{[]string{"properties", "ports", "items"}, true},
		{[]string{"additionalProperties"}, true},
		{[]string{"anyOf", "-"}, true},
		{[]string{"default"}, false},
		{[]string{"properties", "a", "required"}, false},
	}
	for _, tt := range tests {
		if got := cellens.SchemaAt(tt.path); got != tt.want {
			t.Errorf("SchemaAt(%q) = %t, want %t", tt.path, got, tt.want)
		}
	}
}
