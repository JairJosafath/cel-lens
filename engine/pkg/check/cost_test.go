package check_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	celschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/apiserver/pkg/cel/environment"
	"sigs.k8s.io/yaml"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// estimate returns the cost the Kubernetes estimator gives the first rule, and
// the cost of its messageExpression, of the schema at the property path of the
// first version of the CRD in file. A "[]" in props steps into the items of a list.
func estimate(t *testing.T, file string, props ...string) (rule, message uint64) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var v apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(&v)
	var crd apiextensions.CustomResourceDefinition
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&v, &crd, nil); err != nil {
		t.Fatal(err)
	}
	schema := crd.Spec.Validation.OpenAPIV3Schema
	cel := validation.RootCELContext(schema)
	for _, p := range props {
		if p == "[]" {
			schema = schema.Items.Schema
			cel = cel.ChildItemsContext(schema)
			continue
		}
		child := schema.Properties[p]
		cel = cel.ChildPropertyContext(&child, p)
		schema = &child
	}
	info, err := cel.TypeInfo()
	if err != nil {
		t.Fatal(err)
	}
	res, err := celschema.Compile(info.Schema, info.DeclType, celconfig.PerCallLimit, environment.MustBaseEnvSet(environment.DefaultCompatibilityVersion()), celschema.NewExpressionsEnvLoader())
	if err != nil {
		t.Fatal(err)
	}
	card := res[0].MaxCardinality
	if cel.MaxCardinality != nil {
		card = *cel.MaxCardinality
	}
	return res[0].MaxCost * card, res[0].MessageExpressionMaxCost
}

// short writes a cost the way the messages do, for the sizes the fixtures use.
func short(n uint64) string {
	switch {
	case n >= 1e15:
		return fmt.Sprintf("%.1e", float64(n))
	case n >= 1e12:
		return fmt.Sprintf("%.1fT", float64(n)/1e12)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
	return strconv.FormatUint(n, 10)
}

func checkFile(t *testing.T, file string) []check.Diagnostic {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := check.Check(context.Background(), file, data)
	if err != nil {
		t.Fatalf("Check(%s) error = %v", file, err)
	}
	return got
}

// TestCostMatchesEstimator compares the numbers in the messages with the
// Kubernetes cost estimator run on the same schema, and checks that one rule
// over budget gives one problem, and the total one more.
func TestCostMatchesEstimator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		file    string
		props   []string
		message bool
	}{
		{"unbounded list", "testdata/crd-cost.yaml", []string{"items"}, false},
		{"unbounded string in unbounded list", "testdata/crd-cost-name.yaml", []string{"spec", "entries", "[]"}, false},
		{"string conversion in messageExpression", "testdata/crd-cost-message-expression.yaml", []string{"spec"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := checkFile(t, tt.file)
			rule, message := estimate(t, tt.file, tt.props...)
			want := rule
			if tt.message {
				want = message
			}
			if len(got) != 1 {
				t.Fatalf("Check(%s) = %d diagnostics, want 1: %v", tt.file, len(got), got)
			}
			if wantText := "estimated cost " + short(want) + " is over the per-expression budget of 10M"; !strings.Contains(got[0].Message, wantText) {
				t.Errorf("Check(%s)[0] = %q, want it to contain %q", tt.file, got[0].Message, wantText)
			}
			if !strings.Contains(got[0].Message, "All rules together cost an estimated") {
				t.Errorf("Check(%s)[0] = %q, want a sentence about the total", tt.file, got[0].Message)
			}
			for _, d := range got {
				if strings.Contains(d.Message, "contributed to") {
					t.Errorf("Check(%s) reports %q, want no separate contributed line", tt.file, d.Message)
				}
			}
		})
	}
}

func TestCostTotal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		file     string
		wants    []string
		notWants []string
		fixes    []check.Fix
	}{
		{
			"names the unbounded field and the limit",
			"testdata/crd-cost-total.yaml",
			[]string{"estimated cost 151M of all rules together", "budget of 100M", "`name` is a string with no `maxLength`", "Add `maxLength: 500000` to `name`."},
			nil,
			[]check.Fix{{FieldPath: "name", Limit: "maxLength", Value: 500000}},
		},
		{
			"names only the field every listed rule shares",
			"testdata/crd-cost-total-shared.yaml",
			[]string{"`name` is a string with no `maxLength`", "Add `maxLength: 500000` to `name`."},
			[]string{"other"},
			[]check.Fix{{FieldPath: "name", Limit: "maxLength", Value: 500000}},
		},
		{
			"advises simplifying when nothing is unbounded",
			"testdata/crd-cost-total-bounded.yaml",
			[]string{"estimated cost 115.6M of all rules together", "budget of 100M", "Try simplifying the expression."},
			[]string{"Add "},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := checkFile(t, tt.file)
			if len(got) != 1 {
				t.Fatalf("Check(%s) = %d diagnostics, want 1: %v", tt.file, len(got), got)
			}
			for _, want := range tt.wants {
				if !strings.Contains(got[0].Message, want) {
					t.Errorf("Check(%s) = %q, want it to contain %q", tt.file, got[0].Message, want)
				}
			}
			for _, not := range tt.notWants {
				if strings.Contains(got[0].Message, not) {
					t.Errorf("Check(%s) = %q, want it not to contain %q", tt.file, got[0].Message, not)
				}
			}
			var fixes []check.Fix
			for _, f := range got[0].Fixes {
				f.Edits = nil
				fixes = append(fixes, f)
			}
			if fmt.Sprint(fixes) != fmt.Sprint(tt.fixes) {
				t.Errorf("Check(%s)[0].Fixes = %v, want %v", tt.file, fixes, tt.fixes)
			}
		})
	}
}

func TestCostExplanation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		file  string
		wants []string
		fixes []check.Fix
	}{
		{
			"names the unbounded fields",
			"testdata/crd-cost-name.yaml",
			[]string{"`spec.entries`, which has no `maxItems`", "`spec.entries[*].name` is a string with no `maxLength`", "up to 3.1M characters", "Add `maxItems: 1000` to `spec.entries` and `maxLength: 1000` to `spec.entries[*].name`."},
			[]check.Fix{{FieldPath: "spec.entries", Limit: "maxItems", Value: 1000}, {FieldPath: "spec.entries[*].name", Limit: "maxLength", Value: 1000}},
		},
		{
			"explains the string concatenation",
			"testdata/crd-cost-message-expression.yaml",
			[]string{"`string(self.minReplicas)` turns a number into text", "longest string", "one unit for every 10", "write a fixed `message`"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := checkFile(t, tt.file)
			if len(got) == 0 {
				t.Fatalf("Check(%s) = no diagnostics", tt.file)
			}
			for _, want := range tt.wants {
				if !strings.Contains(got[0].Message, want) {
					t.Errorf("Check(%s)[0] = %q, want it to contain %q", tt.file, got[0].Message, want)
				}
			}
			var fixes []check.Fix
			for _, f := range got[0].Fixes {
				f.Edits = nil
				fixes = append(fixes, f)
			}
			if fmt.Sprint(fixes) != fmt.Sprint(tt.fixes) {
				t.Errorf("Check(%s)[0].Fixes = %v, want %v", tt.file, fixes, tt.fixes)
			}
		})
	}
}

// apply writes the edits into data. Edits are positions in the original text.
func apply(t *testing.T, data []byte, edits []check.Edit) []byte {
	t.Helper()
	lines := strings.SplitAfter(string(data), "\n")
	sort.Slice(edits, func(i, j int) bool {
		a, b := edits[i].At, edits[j].At
		return a.Line > b.Line || a.Line == b.Line && a.Column > b.Column
	})
	for _, e := range edits {
		runes := []rune(lines[e.At.Line-1])
		lines[e.At.Line-1] = string(runes[:e.At.Column-1]) + e.Text + string(runes[e.At.Column-1:])
	}
	return []byte(strings.Join(lines, ""))
}

// TestCostFixesSolve applies each fix to its fixture, all of a diagnostic's
// fixes at once when it says they belong together, and checks the cost problem
// is gone.
func TestCostFixesSolve(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		file  string
		edits []string // the text inserted by each edit of each fix
	}{
		{"block mapping", "testdata/crd-cost.yaml", []string{"              maxItems: 200\n", "maxLength: 200, "}},
		{"flow mapping and block mapping", "testdata/crd-cost-name.yaml", []string{"                  maxItems: 1000\n", "maxLength: 1000, "}},
		{"versions share a schema by an anchor", "testdata/crd-cost-anchor.yaml", []string{"              maxItems: 200\n", "maxLength: 200, "}},
		{"two versions share a schema", "testdata/crd-cost-versions.yaml", []string{"                  maxItems: 1000\n", "                  maxItems: 1000\n", "maxLength: 1000, ", "maxLength: 1000, "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			got := checkFile(t, tt.file)
			if len(got) == 0 || len(got[0].Fixes) == 0 {
				t.Fatalf("Check(%s) = %v, want a diagnostic with fixes", tt.file, got)
			}
			d := got[0]
			var sets [][]check.Edit
			for _, f := range d.Fixes {
				if len(f.Edits) == 0 || f.Value == 0 {
					t.Fatalf("Check(%s) fix %+v has no edits or value", tt.file, f)
				}
				if d.FixesTogether {
					if len(sets) == 0 {
						sets = append(sets, nil)
					}
					sets[0] = append(sets[0], f.Edits...)
				} else {
					sets = append(sets, f.Edits)
				}
			}
			var texts []string
			for _, f := range d.Fixes {
				for _, e := range f.Edits {
					texts = append(texts, e.Text)
				}
			}
			if fmt.Sprintf("%q", texts) != fmt.Sprintf("%q", tt.edits) {
				t.Errorf("Check(%s)[0].Fixes insert %q, want %q", tt.file, texts, tt.edits)
			}
			for _, set := range sets {
				fixed := apply(t, data, append([]check.Edit(nil), set...))
				again, err := check.Check(context.Background(), tt.file, fixed)
				if err != nil {
					t.Fatalf("Check(fixed %s) error = %v\n%s", tt.file, err, fixed)
				}
				for _, a := range again {
					if strings.Contains(a.Message, "estimated cost") {
						t.Errorf("Check(fixed %s) = %q, want no cost problem\n%s", tt.file, a.Message, fixed)
					}
				}
			}
		})
	}
}

// manyRules is a CRD whose n rules each run over an unbounded list of strings.
func manyRules(n int) []byte {
	var b strings.Builder
	b.WriteString(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            list:
              type: array
              items:
                type: string
                x-kubernetes-validations:
`)
	for i := range n {
		fmt.Fprintf(&b, "                  - rule: self.matches('^a%d.*$')\n", i)
	}
	return []byte(b.String())
}

// TestCostFixesManyRules checks that every over-budget rule gets a number, even
// when the CRD has several, each of which would be over the total on its own.
func TestCostFixesManyRules(t *testing.T) {
	t.Parallel()
	got, err := check.Check(context.Background(), "many.yaml", manyRules(5))
	if err != nil {
		t.Fatal(err)
	}
	costs := 0
	for _, d := range got {
		if !strings.Contains(d.Message, "estimated cost") || !strings.Contains(d.Message, "per-expression") {
			continue
		}
		costs++
		if len(d.Fixes) == 0 {
			t.Errorf("Check(many)[%q] has no fixes", d.Message)
		}
		for _, f := range d.Fixes {
			if f.Value == 0 || len(f.Edits) == 0 {
				t.Errorf("Check(many)[%q] fix %+v has no number or edits", d.Message, f)
			}
		}
	}
	if costs == 0 {
		t.Fatalf("Check(many) = %v, want cost problems", got)
	}
}

// BenchmarkCostFixes measures checking a CRD with many cost errors, each of
// which is sized.
func BenchmarkCostFixes(b *testing.B) {
	data := manyRules(20)
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := check.Check(context.Background(), "many.yaml", data); err != nil {
			b.Fatal(err)
		}
	}
}

// TestCostFixesManyRulesTotal applies the fix of the diagnostic that reports
// the total of a CRD with 40 rules over their budget, and checks that the
// edited document has no cost problem, the total included.
func TestCostFixesManyRulesTotal(t *testing.T) {
	t.Parallel()
	data := manyRules(40)
	got, err := check.Check(context.Background(), "many.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	fixes := 0
	for _, d := range got {
		if !strings.Contains(d.Message, "All rules together") || len(d.Fixes) == 0 {
			continue
		}
		fixes++
		for _, f := range d.Fixes {
			if want := fmt.Sprintf("%#q to %#q", fmt.Sprintf("%s: %d", f.Limit, f.Value), f.FieldPath); !strings.Contains(d.Message, want) {
				t.Errorf("Check(many) message = %q, want it to name %s as the fix does", d.Message, want)
			}
		}
		var edits []check.Edit
		for _, f := range d.Fixes {
			if d.FixesTogether || len(edits) == 0 {
				edits = append(edits, f.Edits...)
			}
		}
		fixed := apply(t, data, edits)
		again, err := check.Check(context.Background(), "many.yaml", fixed)
		if err != nil {
			t.Fatalf("Check(fixed many) error = %v\n%s", err, fixed)
		}
		for _, a := range again {
			if strings.Contains(a.Message, "estimated cost") {
				t.Errorf("Check(fixed many by %q) = %q, want no cost problem", d.Message, a.Message)
			}
		}
	}
	if fixes == 0 {
		t.Fatalf("Check(many) = %v, want cost problems with fixes", got)
	}
}

// TestCostFixesManyRulesEveryDiagnostic applies the fix of every cost
// diagnostic of a CRD with 40 rules over their budget, and checks that the
// message names the limits of its fixes and that the edited document has no
// cost problem, the total included.
func TestCostFixesManyRulesEveryDiagnostic(t *testing.T) {
	t.Parallel()
	data := manyRules(40)
	got, err := check.Check(context.Background(), "many.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	costs := 0
	rechecked := map[string]bool{} // diagnostics with the same edits have the same result
	for _, d := range got {
		if !strings.Contains(d.Message, "estimated cost") {
			continue
		}
		costs++
		if len(d.Fixes) == 0 {
			t.Errorf("Check(many)[%q] has no fixes", d.Message)
		}
		var edits []check.Edit
		for _, f := range d.Fixes {
			if want := fmt.Sprintf("%#q to %#q", fmt.Sprintf("%s: %d", f.Limit, f.Value), f.FieldPath); !strings.Contains(d.Message, want) {
				t.Errorf("Check(many) message = %q, want it to name %s as the fix does", d.Message, want)
			}
			if d.FixesTogether || len(edits) == 0 {
				edits = append(edits, f.Edits...)
			}
		}
		key := fmt.Sprint(edits)
		if rechecked[key] {
			continue
		}
		rechecked[key] = true
		again, err := check.Check(context.Background(), "many.yaml", apply(t, data, edits))
		if err != nil {
			t.Fatalf("Check(fixed many) error = %v", err)
		}
		for _, a := range again {
			if strings.Contains(a.Message, "estimated cost") {
				t.Errorf("Check(fixed many by %q) = %q, want no cost problem", d.Message, a.Message)
			}
		}
	}
	if costs == 0 {
		t.Fatalf("Check(many) = %v, want cost problems", got)
	}
}

// TestFixLineEnding checks that the text a quick fix inserts ends its line the
// way the file does, whatever the encoding.
func TestFixLineEnding(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("testdata/crd-cost-total.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(src), "name: {type: string}", "name:\n              type: string", 1)
	crlf := strings.ReplaceAll(text, "\n", "\r\n")
	tests := []struct {
		name string
		data string
		eol  string
	}{
		{"UTF-8 LF", text, "\n"},
		{"UTF-8 CRLF", crlf, "\r\n"},
		{"UTF-16LE LF", utf16le(units(text)...), "\n"},
		{"UTF-16LE CRLF", utf16le(units(crlf)...), "\r\n"},
		{"UTF-16BE LF", utf16be(units(text)...), "\n"},
		{"UTF-16BE CRLF", utf16be(units(crlf)...), "\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "crd.yaml", []byte(tt.data))
			if err != nil {
				t.Fatalf("Check(%s) error = %v", tt.name, err)
			}
			var inserted []string
			for _, d := range got {
				for _, f := range d.Fixes {
					for _, e := range f.Edits {
						inserted = append(inserted, e.Text)
					}
				}
			}
			if len(inserted) == 0 {
				t.Fatalf("Check(%s) offers no edits, want a fix", tt.name)
			}
			for _, ins := range inserted {
				if !strings.HasSuffix(ins, tt.eol) || tt.eol == "\n" && strings.Contains(ins, "\r") {
					t.Errorf("Check(%s) edit = %q, want it to end with %q", tt.name, ins, tt.eol)
				}
			}
		})
	}
}
