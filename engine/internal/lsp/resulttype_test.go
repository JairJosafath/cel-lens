package lsp_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/scan"
)

// TestPolicyResultTypeSameProblems checks that the language server and
// `cel-lens-core check` report the same wrong result types of a policy.
func TestPolicyResultTypeSameProblems(t *testing.T) {
	const doc = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConditions:
    - name: c
      expression: "'s'"
  validations:
    - expression: "1"
      messageExpression: object.spec.replicas
`
	file := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := scan.Run(t.Context(), []string{file}, scan.JSON, &out); err != nil {
		t.Fatalf("Run(%s) error: %v", file, err)
	}
	var cli []struct {
		Message    string
		Start, End struct{ Line, Column int }
	}
	if err := json.Unmarshal(out.Bytes(), &cli); err != nil || len(cli) != 3 {
		t.Fatalf("Run(%s) wrote %q (%v), want three diagnostics", file, out.String(), err)
	}

	c := start(t)
	initialize(t, c)
	c.open(doc)
	_, diags := c.diagnostics()
	if len(diags) != len(cli) {
		t.Fatalf("diagnostics = %+v, want %d", diags, len(cli))
	}
	for i, d := range diags {
		r, got := d.Range, cli[i]
		if d.Message != got.Message || r.Start.Line+1 != got.Start.Line || r.Start.Character+1 != got.Start.Column ||
			r.End.Line+1 != got.End.Line || r.End.Character+1 != got.End.Column {
			t.Errorf("language server problem %d = %q %+v, command line = %q %+v, want the same", i, d.Message, r, got.Message, got)
		}
	}
	shutdown(t, c)
}
