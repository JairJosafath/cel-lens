package check_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// kyvernoVersion is the Kyverno release the examples in testdata/kyverno come from.
const kyvernoVersion = "v1.19.1"

// oldKyvernoVersion matches the apiVersion of the examples written before
// policies.kyverno.io/v1, which has the same fields.
var oldKyvernoVersion = regexp.MustCompile(`(?m)^apiVersion: policies\.kyverno\.io/v1(alpha1|beta1)$`)

// TestKyvernoExamples checks that every ValidatingPolicy of the docs and tests
// of Kyverno v1.19.1 that Kyverno accepts has no diagnostics. Each file in
// testdata/kyverno is a copy of the file of the Kyverno repository named by the
// file, with / written as __ (testdata/kyverno/test__cli__x__policy.yaml is
// test/cli/x/policy.yaml). Templates of Helm charts are not valid YAML and are
// left out. The test reads an example of an earlier apiVersion as v1.
func TestKyvernoExamples(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("testdata/kyverno/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples: %v", err)
	}
	for _, file := range files {
		source := strings.ReplaceAll(strings.TrimSuffix(filepath.Base(file), ".yaml"), "__", "/")
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			data = oldKyvernoVersion.ReplaceAll(data, []byte("apiVersion: policies.kyverno.io/v1"))
			got, err := check.Check(context.Background(), file, data)
			if err != nil {
				t.Fatalf("Check(%s from kyverno %s) error = %v", source, kyvernoVersion, err)
			}
			if len(got) != 0 {
				t.Errorf("Check(%s from kyverno %s) = %v, want no diagnostics", source, kyvernoVersion, got)
			}
		})
	}
}
