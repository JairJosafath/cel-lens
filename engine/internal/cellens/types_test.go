package cellens_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
)

func TestParseTypeSchema(t *testing.T) {
	t.Parallel()
	str := map[string]any{"type": "string"}
	integer := map[string]any{"type": "integer"}
	tests := []struct {
		in   string
		want map[string]any
	}{
		{"string", str},
		{"int", integer},
		{"uint", map[string]any{"type": "integer", "minimum": 0}},
		{"double", map[string]any{"type": "number"}},
		{"bool", map[string]any{"type": "boolean"}},
		{"bytes", map[string]any{"type": "string", "format": "byte"}},
		{"timestamp", map[string]any{"type": "string", "format": "date-time"}},
		{"duration", map[string]any{"type": "string", "format": "duration"}},
		{"list(int)", map[string]any{"type": "array", "items": integer}},
		{"map(string, string)", map[string]any{"type": "object", "additionalProperties": str}},
		{"{name: string, replicas: int}", map[string]any{"type": "object", "properties": map[string]any{"name": str, "replicas": integer}}},
		{"{}", map[string]any{"type": "object", "properties": map[string]any{}}},
		{" { a : list( { b: map(string, {c: bool}) } ) , } ", map[string]any{"type": "object", "properties": map[string]any{
			"a": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
				"b": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "object", "properties": map[string]any{
					"c": map[string]any{"type": "boolean"},
				}}},
			}}},
		}}},
		{"{x-kubernetes-id: string, $ref: int, _a1: bool}", map[string]any{"type": "object", "properties": map[string]any{
			"x-kubernetes-id": str, "$ref": integer, "_a1": map[string]any{"type": "boolean"},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			typ, err := cellens.ParseType(tt.in)
			if err != nil {
				t.Fatalf("ParseType(%q) error = %v", tt.in, err)
			}
			if got := typ.Schema(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseType(%q).Schema() = %v, want %v", tt.in, got, tt.want)
			}
			again, err := cellens.ParseType(typ.String())
			if err != nil || !reflect.DeepEqual(again, typ) {
				t.Errorf("ParseType(%q) = %v, %v, want it to read back %q", typ.String(), again, err, tt.in)
			}
		})
	}
}

func TestParseTypeFieldOrder(t *testing.T) {
	t.Parallel()
	typ, err := cellens.ParseType("{zeta: string, alpha: int, mid: bool}")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range typ.Fields {
		names = append(names, f.Name)
	}
	if want := []string{"zeta", "alpha", "mid"}; !reflect.DeepEqual(names, want) {
		t.Errorf("ParseType fields = %v, want %v", names, want)
	}
}

func TestParseTypeErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in     string
		offset int
		words  string
	}{
		{"", 0, "a type is missing"},
		{"strin", 0, `unknown type "strin"`},
		{"list(int", 8, `")" is missing`},
		{"list int", 5, `want "("`},
		{"map(int, string)", 0, "keys are strings"},
		{"map(string)", 10, `want ","`},
		{"{name string}", 6, `want ":"`},
		{"{name: string", 13, `"}" is missing`},
		{"{a: int, a: string}", 9, `field "a" appears twice`},
		{"{1a: int}", 1, "where a field name starts"},
		{"string int", 7, "after the type"},
		{"(", 0, "where a type starts"},
		{"{a: }", 4, "where a type starts"},
		{strings.Repeat("list(", 101) + "int" + strings.Repeat(")", 101), 500, "deeper than 100"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			_, err := cellens.ParseType(tt.in)
			var te *cellens.TypeError
			if !errors.As(err, &te) {
				t.Fatalf("ParseType(%q) error = %v, want a *TypeError", tt.in, err)
			}
			if te.Offset != tt.offset || !strings.Contains(te.Message, tt.words) {
				t.Errorf("ParseType(%q) = %d %q, want %d and %q", tt.in, te.Offset, te.Message, tt.offset, tt.words)
			}
		})
	}
}
