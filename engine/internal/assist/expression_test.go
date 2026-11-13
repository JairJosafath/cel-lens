package assist_test

import (
	"context"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// TestExpression checks that a Document gives the expression a position is
// in, its ends included, which the line above a rule and its hover use to
// find the rule's examples (#15), and none outside every expression.
func TestExpression(t *testing.T) {
	t.Parallel()
	doc := assist.Parse(context.Background(), "crd.yaml", []byte(summaryCRD("self.spec.port == 1")))
	for _, tc := range []struct {
		name string
		pos  extract.Position
		want string // the field path, "" for none
	}{
		{"the first character", extract.Position{Line: 14, Column: 22}, "spec.versions[0].schema.openAPIV3Schema.x-kubernetes-validations[0].rule"},
		{"inside", extract.Position{Line: 14, Column: 30}, "spec.versions[0].schema.openAPIV3Schema.x-kubernetes-validations[0].rule"},
		{"after the last character", extract.Position{Line: 14, Column: 41}, "spec.versions[0].schema.openAPIV3Schema.x-kubernetes-validations[0].rule"},
		{"a messageExpression", extract.Position{Line: 20, Column: 40}, "spec.versions[0].schema.openAPIV3Schema.properties[spec].x-kubernetes-validations[0].messageExpression"},
		{"the key", extract.Position{Line: 14, Column: 15}, ""},
		{"another line", extract.Position{Line: 1, Column: 1}, ""},
	} {
		got, ok := doc.Expression(tc.pos)
		if ok != (tc.want != "") || got.FieldPath != tc.want {
			t.Errorf("Expression(%s, %v) = %q, %t, want %q", tc.name, tc.pos, got.FieldPath, ok, tc.want)
		}
	}
}
