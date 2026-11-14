package check

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	kfield "k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// sharedCRD is a CustomResourceDefinition with one rule whose versions share
// their schema, for the tests of Validations.
const sharedCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {kind: Widget, plural: widgets}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            a: {type: string, maxLength: 10}
          x-kubernetes-validations:
            - rule: "self.a.size() >= 0"
`

// TestValidationsCheckThenCosts checks that Costs reads the validation that
// Check of the same text and version kept, rather than validate again (review
// of #60): with what was kept saying the API server compiles no rule of the
// schema, Costs says so, though the schema is fine.
func TestValidationsCheckThenCosts(t *testing.T) {
	t.Parallel()
	ctx, data := context.Background(), []byte(sharedCRD)
	var v Validations
	if _, err := Check(ctx, "c.yaml", data, WithValidations(&v)); err != nil {
		t.Fatal(err)
	}
	problems, _, ok := v.get(textSum(data), kubeversion.Default(), 0)
	if !ok {
		t.Fatal("Check kept no validation")
	}
	v.put(textSum(data), kubeversion.Default(), 0, problems, []string{"spec.validation.openAPIV3Schema"})
	got, err := Costs(ctx, "c.yaml", data, WithValidations(&v))
	if err != nil || len(got) != 1 || got[0].Kind != CostSchemaNotCompiled {
		t.Errorf("Costs(kept: no rule compiled) = %+v, %v, want the rule not compiled, as kept", got, err)
	}
	if again, err := Costs(ctx, "c.yaml", data, WithValidations(&v), WithVersion(kubeversion.Supported()[0])); err != nil || len(again) != 1 || again[0].Kind != CostEstimated {
		t.Errorf("Costs(another version) = %+v, %v, want it estimated: what was kept is for the version of the check", again, err)
	}
	if fresh, err := Costs(ctx, "c.yaml", data); err != nil || len(fresh) != 1 || fresh[0].Kind != CostEstimated {
		t.Errorf("Costs(nothing kept) = %+v, %v, want it estimated", fresh, err)
	}
}

// TestValidationsCostsThenCheck checks that Check reads the validation Costs
// kept, and that another text drops it.
func TestValidationsCostsThenCheck(t *testing.T) {
	t.Parallel()
	ctx, data := context.Background(), []byte(sharedCRD)
	var v Validations
	if _, err := Costs(ctx, "c.yaml", data, WithValidations(&v)); err != nil {
		t.Fatal(err)
	}
	problems, whole, ok := v.get(textSum(data), kubeversion.Default(), 0)
	if !ok || len(problems) != 0 {
		t.Fatalf("Costs kept %v, %v, want no problem kept", problems, ok)
	}
	rule := kfield.Invalid(kfield.NewPath("spec", "validation", "openAPIV3Schema", "x-kubernetes-validations").Index(0).Child("rule"), "", "kept by Costs")
	v.put(textSum(data), kubeversion.Default(), 0, kfield.ErrorList{rule}, whole)
	diags, err := Check(ctx, "c.yaml", data, WithValidations(&v))
	if err != nil || !slices.ContainsFunc(diags, func(d Diagnostic) bool { return strings.Contains(d.Message, "kept by Costs") }) {
		t.Errorf("Check(kept) = %+v, %v, want the problem that was kept", diags, err)
	}
	other := []byte(strings.Replace(sharedCRD, ">= 0", ">= 1", 1))
	if diags, err := Check(ctx, "c.yaml", other, WithValidations(&v)); err != nil || len(diags) != 0 {
		t.Errorf("Check(another text) = %+v, %v, want no problem", diags, err)
	}
	if _, _, ok := v.get(textSum(data), kubeversion.Default(), 0); ok {
		t.Error("the validation of the first text is still kept after another text was checked")
	}
}

// TestValidationsSameResults checks, over the CustomResourceDefinitions of
// testdata, that Check and Costs answer the same with a validation the other
// kept as without one.
func TestValidationsSameResults(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("testdata/crd-*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("list testdata: %v", err)
	}
	ctx := context.Background()
	for _, f := range files {
		data := readFile(t, f)
		wantDiags, _ := Check(ctx, f, data)
		wantCosts, _ := Costs(ctx, f, data)
		var byCheck, byCosts Validations
		_, _ = Check(ctx, f, data, WithValidations(&byCheck))
		gotCosts, _ := Costs(ctx, f, data, WithValidations(&byCheck))
		_, _ = Costs(ctx, f, data, WithValidations(&byCosts))
		gotDiags, _ := Check(ctx, f, data, WithValidations(&byCosts))
		if !equalJSON(t, gotCosts, wantCosts) {
			t.Errorf("Costs(%s) after Check = %+v, want %+v", f, gotCosts, wantCosts)
		}
		if !equalJSON(t, gotDiags, wantDiags) {
			t.Errorf("Check(%s) after Costs = %+v, want %+v", f, gotDiags, wantDiags)
		}
	}
}

// readFile reads name or fails the test.
func readFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// equalJSON reports whether a and b write the same JSON.
func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(ja, jb)
}

// TestValidationsNotWithAssumedTypes checks that the validation Check runs
// with the .cellens types placed is never kept in Validations nor read from
// it (review of #60 and #62): what is kept after a check with types is what a
// check without them keeps, and the typed run finds its own problems though
// what is kept has none.
func TestValidationsNotWithAssumedTypes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	data := []byte(strings.Replace(sharedCRD, `rule: "self.a.size() >= 0"`, `rule: "self.config.nosuch == 1"`, 1))
	data = []byte(strings.Replace(string(data), "            a: {type: string, maxLength: 10}\n", "            a: {type: string, maxLength: 10}\n            config: {x-kubernetes-int-or-string: true}\n", 1))
	typed := WithAssumedTypes([]AssumedType{{
		Kind: "CustomResourceDefinition", Name: "widgets.example.com", Version: "v1",
		Path: []string{"config"}, Type: "int", Source: ".cellens", Line: 3,
		Schema: map[string]any{"type": "integer"},
	}})
	var plain, withTypes Validations
	if _, err := Check(ctx, "c.yaml", data, WithValidations(&plain)); err != nil {
		t.Fatal(err)
	}
	diags, err := Check(ctx, "c.yaml", data, typed, WithValidations(&withTypes))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(diags, func(d Diagnostic) bool {
		return d.Severity == SeverityWarning && strings.Contains(d.Message, "does not support field selection")
	}) {
		t.Fatalf("Check(with types) = %+v, want the typed run's warning", diags)
	}
	want, _, okPlain := plain.get(textSum(data), kubeversion.Default(), 0)
	got, _, okTyped := withTypes.get(textSum(data), kubeversion.Default(), 0)
	if !okPlain || !okTyped || !equalJSON(t, got, want) || len(got) != 0 {
		t.Errorf("kept after a check with types = %v (%t), want what a check without them keeps, %v (%t)", got, okTyped, want, okPlain)
	}
	// The typed run reads nothing kept: with the untyped validation kept
	// first, it still finds its warning.
	again, err := Check(ctx, "c.yaml", data, typed, WithValidations(&plain))
	if err != nil || !equalJSON(t, again, diags) {
		t.Errorf("Check(with types, validation kept) = %+v, %v, want %+v", again, err, diags)
	}
}
