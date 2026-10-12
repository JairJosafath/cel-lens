package kyverno_test

import (
	"context"
	"errors"
	"testing"

	kyvernov1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"

	"github.com/JairJosafath/cel-lens/engine/internal/kyverno"
)

func TestCompile(t *testing.T) {
	t.Parallel()
	spec := &kyvernov1.ValidatingPolicySpec{
		Variables: []admissionregistrationv1.Variable{
			{Name: "a", Expression: "1 +"},
			{Name: "b", Expression: "variables.a + 1"},
		},
		Validations: []admissionregistrationv1.Validation{
			{Expression: "variables.b > 0", MessageExpression: "1"},
			{Expression: "variables.c"},
		},
	}
	got, err := kyverno.Compile(context.Background(), spec, "", nil)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	var fields []string
	for _, p := range got {
		fields = append(fields, p.Field)
	}
	want := []string{"spec.variables[0].expression", "spec.validations[0].messageExpression", "spec.validations[1].expression"}
	if len(fields) != len(want) {
		t.Fatalf("Compile() problems at %v, want %v", fields, want)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Errorf("Compile() problem %d at %s, want %s", i, fields[i], want[i])
		}
	}
}

func TestCompileCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spec := &kyvernov1.ValidatingPolicySpec{Validations: []admissionregistrationv1.Validation{{Expression: "true"}}}
	if _, err := kyverno.Compile(ctx, spec, "", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Compile(cancelled) error = %v, want context.Canceled", err)
	}
}
