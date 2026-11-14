package check_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// defaultedCRD returns a valid CustomResourceDefinition whose spec has n
// objects, each with a default of its own, two properties with defaults and
// a rule: 3n defaults and n rules, and no problem.
func defaultedCRD(n int) string {
	var b strings.Builder
	b.WriteString(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  names: {plural: widgets, kind: Widget}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
`)
	for i := range n {
		fmt.Fprintf(&b, `                o%d:
                  type: object
                  default: {a: 1, b: x}
                  properties:
                    a: {type: integer, default: 1}
                    b: {type: string, default: x}
                  x-kubernetes-validations:
                    - rule: self.a >= 0 && self.b != ''
`, i)
	}
	return b.String()
}

// BenchmarkCheckDefaultedCRD measures checking a valid CRD with 900
// defaults and 300 rules. The API server's validation of the CRD validates
// the defaults; a schema with no problem must not pay for a second
// validation of them, which deciding whether a version compiles no rule
// would cost (cel-lens-core#35).
func BenchmarkCheckDefaultedCRD(b *testing.B) {
	data := []byte(defaultedCRD(300))
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		diags, err := check.Check(context.Background(), "d.yaml", data)
		if err != nil || len(diags) > 0 {
			b.Fatalf("Check() = %+v, %v, want no problem", diags, err)
		}
	}
}
