package check_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// TestCheckWrongTypedItemKeepsIndices checks that a list item of the wrong type
// is reported and does not move the items after it: their errors name their own
// index and their expressions are still compiled.
func TestCheckWrongTypedItemKeepsIndices(t *testing.T) {
	t.Parallel()
	const head = "apiVersion: admissionregistration.k8s.io/v1\nkind: "
	tests := []struct {
		name string
		src  string
		want []string // substrings of the messages, in order
	}{
		{
			name: "validations",
			src: head + "ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: \"true\"\n    - scalar\n" +
				"    - messageExpression: [x]\n    - expression: \"1\"\n",
			want: []string{
				"spec.validations[1]: expected an object, got a string",
				"spec.validations[2].messageExpression: expected a string, got a list",
				"must evaluate to bool",
			},
		},
		{
			name: "mutations",
			src: head + "MutatingAdmissionPolicy\nspec:\n  mutations:\n    - scalar\n" +
				"    - patchType: ApplyConfiguration\n      applyConfiguration:\n        expression: \"1\"\n",
			want: []string{
				"spec.mutations[0]: expected an object, got a string",
				"must evaluate to Object but got int",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(context.Background(), "policy.yaml", []byte(tt.src))
			if err != nil {
				t.Fatalf("Check(%s) error: %v", tt.name, err)
			}
			var got []string
			for _, d := range diags {
				got = append(got, d.Message)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Check(%s) = %q, want %d diagnostics", tt.name, got, len(tt.want))
			}
			for i, w := range tt.want {
				if !strings.Contains(got[i], w) {
					t.Errorf("Check(%s)[%d] = %q, want it to contain %q", tt.name, i, got[i], w)
				}
			}
		})
	}
}
