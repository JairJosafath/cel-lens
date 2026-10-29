package extract_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

type pos = extract.Position

func TestFindPositions(t *testing.T) {
	t.Parallel()
	const head = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n"
	tests := []struct {
		name       string
		body       string
		text       string
		start, end pos
	}{
		{"plain", "    - expression: object.x > 1\n", "object.x > 1", pos{5, 19}, pos{5, 31}},
		{"plain with comment", "    - expression: a == b # why\n", "a == b", pos{5, 19}, pos{5, 25}},
		{"plain folded over lines", "    - expression: a &&\n        b\n", "a && b", pos{5, 19}, pos{6, 10}},
		{"single quoted", "    - expression: 'x == ''a'''\n", "x == 'a'", pos{5, 20}, pos{5, 30}},
		{"double quoted", "    - expression: \"x == \\\"a\\\"\"\n", `x == "a"`, pos{5, 20}, pos{5, 30}},
		{"double quoted multi-line", "    - expression: \"a &&\n        b\"\n", "a && b", pos{5, 20}, pos{6, 10}},
		{"literal block", "    - expression: |\n        a &&\n          b\n\n    - messageExpression: x\n", "a &&\n  b\n", pos{6, 9}, pos{7, 12}},
		{"folded block strip", "    - expression: >-\n        a &&\n        b\n      \n", "a && b", pos{6, 9}, pos{7, 10}},
		{"literal indicator extra indent", "    - expression: |2\n          a &&\n        b\n", "  a &&\nb\n", pos{6, 9}, pos{7, 10}},
		{"anchor literal indicator extra indent", "    - expression: &a |2\n          a &&\n        b\n", "  a &&\nb\n", pos{6, 9}, pos{7, 10}},
		{"anchor tab literal indicator extra indent", "    - expression: &a\t|2\n          a &&\n        b\n", "  a &&\nb\n", pos{6, 9}, pos{7, 10}},
		{"anchor line break literal indicator extra indent", "    - expression: &a\n      |2\n          a &&\n        b\n", "  a &&\nb\n", pos{7, 9}, pos{8, 10}},
		{"anchor comment line break literal indicator extra indent", "    - expression: &a # c\n      |2\n          a &&\n        b\n", "  a &&\nb\n", pos{7, 9}, pos{8, 10}},
		{"tag comment line break folded strip", "    - expression: !!str # c\n      >-\n        a &&\n        b\n", "a && b", pos{7, 9}, pos{8, 10}},
		{"tag literal indicator extra indent", "    - expression: !!str |2\n          a &&\n        b\n", "  a &&\nb\n", pos{6, 9}, pos{7, 10}},
		{"literal indicator strip", "    - expression: |-2\n          a &&\n        b\n", "  a &&\nb", pos{6, 9}, pos{7, 10}},
		{"folded indicator keep", "    - expression: >2+\n          a\n        b\n\n", "  a\nb\n\n", pos{6, 9}, pos{7, 10}},
		{"folded indicator strip first", "    - expression: >-4\n            a\n          b\n", "  a\nb", pos{6, 11}, pos{7, 12}},
		{"block ends at sibling key", "    - expression: |\n        a\n      messageExpression: m\n", "a\n", pos{6, 9}, pos{6, 10}},
		{"unicode", "    - expression: \"é == 'ü'\"\n", "é == 'ü'", pos{5, 20}, pos{5, 28}},
		{"crlf", "    - expression: a\r\n", "a", pos{5, 19}, pos{5, 20}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := extract.Find(context.Background(), "p.yaml", []byte(head+tt.body))
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			if len(got) == 0 {
				t.Fatalf("Find() = no expressions, want one with text %q", tt.text)
			}
			e := got[0]
			if e.Text != tt.text || e.Start != tt.start || e.End != tt.end || e.File != "p.yaml" || e.Kind != extract.KindExpression {
				t.Errorf("Find()[0] = {%q %v %v %v %s}, want {%q %v %v expression p.yaml}",
					e.Text, e.Start, e.End, e.Kind, e.File, tt.text, tt.start, tt.end)
			}
		})
	}
}

const crd = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          x-kubernetes-validations:
            - rule: self.a
          properties:
            spec:
              properties:
                list:
                  items:
                    x-kubernetes-validations:
                      - rule: self.b
                        messageExpression: "'m'"
                map:
                  additionalProperties:
                    x-kubernetes-validations:
                      - rule: self.c
`

func TestFindCRD(t *testing.T) {
	t.Parallel()
	got, err := extract.Find(context.Background(), "crd.yaml", []byte(crd))
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	type r struct {
		text, path string
		kind       extract.Kind
		line       int
	}
	var have []r
	for _, e := range got {
		have = append(have, r{e.Text, e.SchemaPath, e.Kind, e.Start.Line})
	}
	want := []r{
		{"self.a", "", extract.KindRule, 9},
		{"self.b", "properties.spec.properties.list.items", extract.KindRule, 16},
		{"'m'", "properties.spec.properties.list.items", extract.KindMessageExpression, 17},
		{"self.c", "properties.spec.properties.map.additionalProperties", extract.KindRule, 21},
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("Find(crd) = %v, want %v", have, want)
	}
}

func TestFindAliasBomb(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n")
	b.WriteString("        x-defs:\n          l0: &l0\n            x-kubernetes-validations:\n              - rule: self.a\n")
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "          l%d: &l%d {properties: {a: *l%d, b: *l%d, c: *l%d, d: *l%d}}\n", i, i, i-1, i-1, i-1, i-1)
	}
	b.WriteString("        openAPIV3Schema: *l20\n")
	got, err := extract.Find(context.Background(), "bomb.yaml", []byte(b.String()))
	if err != nil || len(got) != 1 {
		t.Errorf("Find(alias bomb) = %d expressions, %v, want 1, nil", len(got), err)
	}
}

func TestFindCRDBlockColumn(t *testing.T) {
	t.Parallel()
	src := "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  validation:\n    openAPIV3Schema:\n      x-kubernetes-validations:\n        - rule: |\n            self.a\n"
	got, err := extract.Find(context.Background(), "c.yaml", []byte(src))
	if err != nil || len(got) != 1 || got[0].Start != (pos{8, 13}) || got[0].End != (pos{8, 19}) {
		t.Errorf("Find(block rule) = %v, %v, want start 8:13 end 8:19", got, err)
	}
}

func BenchmarkFind(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n")
	for range 500 {
		sb.WriteString("    - expression: \"object.spec.replicas > 0 && object.metadata.name.startsWith('x')\"\n      messageExpression: |\n        'replicas must be positive'\n")
	}
	data := []byte(sb.String())
	ctx := context.Background()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := extract.Find(ctx, "p.yaml", data); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFindCRDv1beta1(t *testing.T) {
	t.Parallel()
	src := "apiVersion: apiextensions.k8s.io/v1beta1\nkind: CustomResourceDefinition\nspec:\n  validation:\n    openAPIV3Schema:\n      x-kubernetes-validations:\n        - rule: self.a\n"
	got, err := extract.Find(context.Background(), "c.yaml", []byte(src))
	if err != nil || len(got) != 1 {
		t.Errorf("Find(v1beta1 CRD) = %v, %v, want one expression", got, err)
	}
}

const policy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
spec:
  matchConditions:
    - name: m
      expression: mc
  variables:
    - name: v
      expression: var
  validations:
    - expression: val
      messageExpression: msg
  auditAnnotations:
    - key: k
      valueExpression: audit
`

func TestFindPolicyFields(t *testing.T) {
	t.Parallel()
	got, err := extract.Find(context.Background(), "p.yaml", []byte(policy))
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	type r struct {
		text string
		kind extract.Kind
	}
	var have []r
	for _, e := range got {
		have = append(have, r{e.Text, e.Kind})
	}
	want := []r{
		{"val", extract.KindExpression},
		{"msg", extract.KindMessageExpression},
		{"mc", extract.KindExpression},
		{"var", extract.KindExpression},
		{"audit", extract.KindValueExpression},
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("Find(policy) = %v, want %v", have, want)
	}
}

func TestFindMultiDocument(t *testing.T) {
	t.Parallel()
	src := "---\nkind: ConfigMap\n---\n" + policy + "---\n" + crd
	got, err := extract.Find(context.Background(), "m.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(got) != 9 {
		t.Fatalf("Find(multi-document) found %d expressions, want 9", len(got))
	}
	if first := got[0]; first.Text != "val" || first.Start.Line != 14 {
		t.Errorf("Find(multi-document)[0] = %q at line %d, want \"val\" at line 14", first.Text, first.Start.Line)
	}
}

func TestFindIgnoresOtherFiles(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"empty":      "",
		"comment":    "# nothing\n",
		"configmap":  "apiVersion: v1\nkind: ConfigMap\ndata:\n  rule: x\n",
		"list":       "- a\n- b\n",
		"scalar":     "hello\n",
		"wrong kind": "apiVersion: example.com/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: x\n",
		"non-string": "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: 5\n    - expression: [a]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := extract.Find(context.Background(), "f.yaml", []byte(src))
			if len(got) != 0 || err != nil {
				t.Errorf("Find(%q) = %v, %v, want no expressions and no error", name, got, err)
			}
		})
	}
}

func TestFindMalformed(t *testing.T) {
	t.Parallel()
	src := policy + "---\nkey: [unclosed\n"
	got, err := extract.Find(context.Background(), "bad.yaml", []byte(src))
	if err == nil {
		t.Fatal("Find(malformed) error = nil, want an error")
	}
	if len(got) != 5 {
		t.Errorf("Find(malformed) found %d expressions before the error, want 5", len(got))
	}
}

func TestFindCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := extract.Find(ctx, "f.yaml", []byte(policy))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Find(cancelled) error = %v, want context.Canceled", err)
	}
}

func FuzzFind(f *testing.F) {
	f.Add([]byte(policy))
	f.Add([]byte(crd))
	f.Add([]byte("kind: ValidatingAdmissionPolicy\napiVersion: admissionregistration.k8s.io/v1\nspec: {validations: [{expression: \"a}]}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, _ := extract.Find(context.Background(), "f.yaml", data)
		for _, e := range got {
			if e.Start.Line < 1 || e.Start.Column < 1 || e.End.Line < e.Start.Line {
				t.Fatalf("Find(%q) gave bad range %v-%v", data, e.Start, e.End)
			}
		}
	})
}

func TestFindFieldPath(t *testing.T) {
	t.Parallel()
	src := crd + "---\n" + `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
spec:
  variables:
    - name: v
      expression: "1"
  validations:
    - expression: "true"
    - expression: "false"
      messageExpression: "'m'"
`
	got, err := extract.Find(context.Background(), "f.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	type r struct {
		doc   int
		field string
	}
	var have []r
	for _, e := range got {
		have = append(have, r{e.Document, e.FieldPath})
	}
	const s = "spec.versions[0].schema.openAPIV3Schema"
	want := []r{
		{0, s + ".x-kubernetes-validations[0].rule"},
		{0, s + ".properties[spec].properties[list].items.x-kubernetes-validations[0].rule"},
		{0, s + ".properties[spec].properties[list].items.x-kubernetes-validations[0].messageExpression"},
		{0, s + ".properties[spec].properties[map].additionalProperties.x-kubernetes-validations[0].rule"},
		{1, "spec.validations[0].expression"},
		{1, "spec.validations[1].expression"},
		{1, "spec.validations[1].messageExpression"},
		{1, "spec.variables[0].expression"},
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("Find() document and field paths = %v, want %v", have, want)
	}
}

func TestFindMergeKeys(t *testing.T) {
	t.Parallel()
	const head = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
`
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"validations merged by alias", `          description: &base
            x-kubernetes-validations:
              - rule: self.base
          <<: *base
`, []string{"self.base"}},
		{"properties merged", `          properties:
            <<: &props
              a:
                x-kubernetes-validations:
                  - rule: self.a
`, []string{"self.a"}},
		{"explicit key overrides merged", `          <<: &base
            x-kubernetes-validations:
              - rule: self.merged
          x-kubernetes-validations:
            - rule: self.explicit
`, []string{"self.explicit"}},
		{"explicit property overrides merged", `          properties:
            <<: &props
              a:
                x-kubernetes-validations:
                  - rule: self.merged
            a:
              x-kubernetes-validations:
                - rule: self.explicit
`, []string{"self.explicit"}},
		{"merge list, first wins", `          <<: [{x-kubernetes-validations: [{rule: self.first}]}, {x-kubernetes-validations: [{rule: self.second}]}]
`, []string{"self.first"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := head + tt.body
			got, err := extract.Find(context.Background(), "m.yaml", []byte(src))
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			var have []string
			for _, e := range got {
				have = append(have, e.Text)
			}
			if !reflect.DeepEqual(have, tt.want) {
				t.Errorf("Find(%s) = %v, want %v", tt.name, have, tt.want)
			}
		})
	}
}

func TestFindMergeKeysNested(t *testing.T) {
	t.Parallel()
	// Each anchor merges the previous one twice; expanding every reference
	// would take 2^40 steps.
	var b strings.Builder
	b.WriteString("a0: &a0 {x-kubernetes-validations: [{rule: self.deep}]}\n")
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "a%d: &a%d {<<: [*a%d, *a%d]}\n", i, i, i-1, i-1)
	}
	b.WriteString(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          <<: *a40
`)
	got, err := extract.Find(context.Background(), "m.yaml", []byte(b.String()))
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(got) != 1 || got[0].Text != "self.deep" {
		t.Errorf("Find(nested merges) = %v, want one expression self.deep", got)
	}
}

// manyMerges is a CRD whose n properties each merge one anchor of n keys.
func manyMerges(n int) []byte {
	var b strings.Builder
	b.WriteString("big: &big\n  x-kubernetes-validations: [{rule: self.big}]\n")
	for i := range n {
		fmt.Fprintf(&b, "  k%d: 0\n", i)
	}
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          properties:\n")
	for i := range n {
		fmt.Fprintf(&b, "            p%d: {<<: *big}\n", i)
	}
	return []byte(b.String())
}

func TestFindManyMerges(t *testing.T) {
	t.Parallel()
	got, err := extract.Find(context.Background(), "m.yaml", manyMerges(2000))
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(got) != 1 || got[0].Text != "self.big" {
		t.Errorf("Find(many merges of one anchor) = %d expressions, want one self.big", len(got))
	}
}

func BenchmarkFindManyMerges(b *testing.B) {
	data := manyMerges(1000)
	ctx := context.Background()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := extract.Find(ctx, "m.yaml", data); err != nil {
			b.Fatal(err)
		}
	}
}

// sharedForms are the ways a schema can take the shared properties map.
var sharedForms = []struct{ name, schema string }{
	{"alias", "{properties: *shared}"},
	{"merge-schema", "{<<: *holder}"},
	{"merge-properties", "{properties: {<<: *shared, own%[1]d: {type: string}}}"},
	{"overlap", "{properties: {<<: [*shared, *plain]}}"},
	{"overlap-rules", "{properties: {<<: [*shared, *ruled]}}"},
}

// sharedProperties is a CRD whose k schemas each take one anchored properties
// map of m entries, in the given forms in turn. Entry p0 carries a rule.
func sharedProperties(k, m int, forms ...int) []byte {
	var b strings.Builder
	b.WriteString("shared: &shared\n  p0: {x-kubernetes-validations: [{rule: self.shared}]}\n")
	for i := 1; i < m; i++ {
		fmt.Fprintf(&b, "  p%d: {type: string}\n", i)
	}
	b.WriteString("holder: &holder\n  properties: *shared\n")
	// plain and ruled have the keys of shared; the rules on ruled are shadowed.
	b.WriteString("plain: &plain\n")
	for i := range m {
		fmt.Fprintf(&b, "  p%d: {type: string}\n", i)
	}
	b.WriteString("ruled: &ruled\n")
	for i := range m {
		fmt.Fprintf(&b, "  p%d: {x-kubernetes-validations: [{rule: self.ruled}]}\n", i)
	}
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          properties:\n")
	for i := range k {
		fmt.Fprintf(&b, "            s%d: "+sharedForms[forms[i%len(forms)]].schema+"\n", i)
	}
	return []byte(b.String())
}

func TestFindSharedProperties(t *testing.T) {
	t.Parallel()
	for i, form := range append([]struct{ name, schema string }{{"all", ""}}, sharedForms...) {
		forms := []int{0, 1, 2, 3, 4}
		if i > 0 {
			forms = []int{i - 1}
		}
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			got, err := extract.Find(context.Background(), "s.yaml", sharedProperties(4000, 4000, forms...))
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			if len(got) != 1 || got[0].Text != "self.shared" || got[0].Start != (pos{2, 42}) {
				t.Errorf("Find(shared properties) = %v, want one self.shared at 2:42", got)
			}
		})
	}
}

// manyKeys is a CRD of k schemas of 20 keys each. Plain gives each schema its
// own properties map and no anchors. Anchored gives each its own anchored map,
// walked alone through an alias and then merged ahead of the anchored map m,
// which has the same keys. Key a carries a rule; m's are shadowed.
func manyKeys(k int, anchored bool) []byte {
	const keys = "abcdefghijklmnopqrst"
	var b strings.Builder
	if anchored {
		b.WriteString("m: &m\n")
		for _, c := range keys {
			fmt.Fprintf(&b, "  %c: {x-kubernetes-validations: [{rule: self.m}]}\n", c)
		}
		for i := range k {
			fmt.Fprintf(&b, "s%d: &s%[1]d\n  a: {x-kubernetes-validations: [{rule: self.s%[1]d}]}\n", i)
			for _, c := range keys[1:] {
				fmt.Fprintf(&b, "  %c: {type: string}\n", c)
			}
		}
	}
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          properties:\n")
	for i := range k {
		if !anchored {
			fmt.Fprintf(&b, "            f%d: {properties: {a: {x-kubernetes-validations: [{rule: self.f%[1]d}]}", i)
			for _, c := range keys[1:] {
				fmt.Fprintf(&b, ", %c: {}", c)
			}
			b.WriteString("}}\n")
		} else {
			fmt.Fprintf(&b, "            w%d: {properties: *s%[1]d}\n", i)
		}
	}
	if anchored {
		for i := range k {
			fmt.Fprintf(&b, "            z%d: {properties: {<<: [*s%[1]d, *m]}}\n", i)
		}
	}
	return []byte(b.String())
}

func TestFindManyKeys(t *testing.T) {
	t.Parallel()
	for _, anchored := range []bool{false, true} {
		t.Run(fmt.Sprintf("anchored=%v", anchored), func(t *testing.T) {
			t.Parallel()
			const k = 300
			got, err := extract.Find(context.Background(), "s.yaml", manyKeys(k, anchored))
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			if len(got) != k {
				t.Errorf("Find(many keys) = %d expressions, want %d", len(got), k)
			}
			for _, e := range got {
				if e.Text == "self.m" {
					t.Errorf("Find(many keys) reported shadowed %v", e)
				}
			}
		})
	}
}

// BenchmarkFindManyKeys runs each form at K and 2K schemas; the time per
// operation should about double from one size to the next.
func BenchmarkFindManyKeys(b *testing.B) {
	ctx := context.Background()
	for _, anchored := range []bool{false, true} {
		for _, k := range []int{4000, 8000, 16000} {
			b.Run(fmt.Sprintf("anchored=%v/K=%d", anchored, k), func(b *testing.B) {
				data := manyKeys(k, anchored)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				for b.Loop() {
					if _, err := extract.Find(ctx, "s.yaml", data); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestFindShadowedShared(t *testing.T) {
	t.Parallel()
	const head = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          properties:\n"
	const a = "            a: {properties: {<<: *shared, p0: {type: string}}}\n"
	const b = "            b: {properties: {<<: *shared}}\n"
	const lists = "s1: &s1 {p0: {type: string}}\ns2: &s2 {p0: {x-kubernetes-validations: [{rule: self.fromS2}]}}\n"
	const partial = "m: &m {p0: {x-kubernetes-validations: [{rule: self.fromM}]}, p1: {type: string}}\ns2: &s2 {p1: {x-kubernetes-validations: [{rule: self.fromS2}]}}\n"
	none := []struct{ name, doc string }{
		{"walked source shadows later source", lists + head + "            c: {properties: {<<: *s1}}\n            a: {properties: {<<: [*s1, *s2]}}\n"},
		{"without the walking map", lists + head + "            a: {properties: {<<: [*s1, *s2]}}\n"},
		{"nested form", lists + "m: &m {<<: *s1}\n" + head + "            c: {properties: *m}\n            a: {properties: {<<: [*m, *s2]}}\n"},
		{"inline merge source", "s2: &s2 {p0: {x-kubernetes-validations: [{rule: self.fromS2}]}}\nm: &m {<<: {p0: {type: string}}}\n" + head + "            c: {properties: *m}\n            a: {properties: {<<: [*m, *s2]}}\n"},
		{"inline merge source without the walking map", "s2: &s2 {p0: {x-kubernetes-validations: [{rule: self.fromS2}]}}\nm: &m {<<: {p0: {type: string}}}\n" + head + "            a: {properties: {<<: [*m, *s2]}}\n"},
		{"inline merge sequence", "s2: &s2 {p0: {x-kubernetes-validations: [{rule: self.fromS2}]}}\nm: &m {<<: [{p0: {type: string}}]}\n" + head + "            c: {properties: *m}\n            a: {properties: {<<: [*m, *s2]}}\n"},
		{"inline merge sequence without the walking map", "s2: &s2 {p0: {x-kubernetes-validations: [{rule: self.fromS2}]}}\nm: &m {<<: [{p0: {type: string}}]}\n" + head + "            a: {properties: {<<: [*m, *s2]}}\n"},
	}
	for _, tt := range none {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := extract.Find(context.Background(), "s.yaml", []byte(tt.doc))
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			if len(got) != 0 {
				t.Errorf("Find(%s) = %v, want none", tt.name, got)
			}
		})
	}
	tests := []struct {
		name, doc string
		want      string
		line, col int
	}{
		// y's p1 comes from m, so self.fromS2 never runs; self.fromM does.
		{"partly walked source shadows later source", partial + head + "            x: {properties: {p0: {type: string}, <<: *m}}\n            y: {properties: {<<: [*m, *s2]}}\n", "self.fromM", 1, 47},
		{"partly walked source without the walking map", partial + head + "            y: {properties: {<<: [*m, *s2]}}\n", "self.fromM", 1, 47},
		{"shadowing map first", "shared: &shared {p0: {x-kubernetes-validations: [{rule: self.fromShared}]}}\n" + head + a + b, "self.fromShared", 1, 57},
		{"shadowing map last", "shared: &shared {p0: {x-kubernetes-validations: [{rule: self.fromShared}]}}\n" + head + b + a, "self.fromShared", 1, 57},
		{"two sources", "s1: &s1 {p0: {type: string}}\ns2: &s2 {p0: {x-kubernetes-validations: [{rule: self.fromS2}]}}\n" + head +
			"            a: {properties: {<<: [*s1, *s2]}}\n            b: {properties: {<<: *s2}}\n", "self.fromS2", 2, 49},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := extract.Find(context.Background(), "s.yaml", []byte(tt.doc))
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			if len(got) != 1 || got[0].Text != tt.want || got[0].Start != (pos{tt.line, tt.col}) {
				t.Errorf("Find(%s) = %v, want one %s at %d:%d", tt.name, got, tt.want, tt.line, tt.col)
			}
		})
	}
}

func BenchmarkFindSharedProperties(b *testing.B) {
	ctx := context.Background()
	for i, form := range sharedForms {
		b.Run(form.name, func(b *testing.B) {
			data := sharedProperties(4000, 4000, i)
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := extract.Find(ctx, "s.yaml", data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// cancelAfter is a context that reports context.Canceled from its n-th Err
// call on, so a test can cancel at an exact point without timing.
type cancelAfter struct {
	context.Context
	n, calls int
}

func (c *cancelAfter) Err() error {
	c.calls++
	if c.calls >= c.n {
		return context.Canceled
	}
	return nil
}

// mergedProperties is a CRD whose properties merge n anchors in one list. Every
// anchor has the same key, so the merge yields one property and only the merge
// expansion itself can reach n ctx checks.
func mergedProperties(n int) []byte {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "a%d: &a%d {k: 0}\n", i, i)
	}
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          properties:\n            <<: [")
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "*a%d", i)
	}
	b.WriteString("]\n")
	return []byte(b.String())
}

func TestFindCancelledWhileMerging(t *testing.T) {
	t.Parallel()
	const sources = 60
	data := mergedProperties(sources)
	counter := &cancelAfter{Context: context.Background(), n: 1 << 30}
	if _, err := extract.Find(counter, "m.yaml", data); err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if counter.calls < sources {
		t.Fatalf("Find() checked ctx %d times, want at least once per merge source (%d)", counter.calls, sources)
	}
	// Cancelling at any check, including those inside the merge expansion,
	// stops Find.
	for n := 1; n <= counter.calls; n++ {
		ctx := &cancelAfter{Context: context.Background(), n: n}
		if _, err := extract.Find(ctx, "m.yaml", data); !errors.Is(err, context.Canceled) {
			t.Fatalf("Find(cancelled at check %d of %d) error = %v, want context.Canceled", n, counter.calls, err)
		}
	}
}

// singleSources is a CRD where n one-key maps are each walked alone, then one
// properties map merges all of them and an n-key map holding the same keys and
// one more, "extra", that carries a rule.
func singleSources(n int) []byte {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "s%d: &s%d {q%d: {type: string}}\n", i, i, i)
	}
	b.WriteString("m: &m\n  extra: {x-kubernetes-validations: [{rule: self.extra}]}\n")
	for i := range n {
		fmt.Fprintf(&b, "  q%d: {x-kubernetes-validations: [{rule: self.shadowed}]}\n", i)
	}
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          properties:\n")
	for i := range n {
		fmt.Fprintf(&b, "            w%d: {properties: *s%d}\n", i, i)
	}
	b.WriteString("            z: {properties: {<<: [")
	for i := range n {
		fmt.Fprintf(&b, "*s%d, ", i)
	}
	b.WriteString("*m]}}\n")
	return []byte(b.String())
}

func TestFindSingleSources(t *testing.T) {
	t.Parallel()
	got, err := extract.Find(context.Background(), "s.yaml", singleSources(4000))
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(got) != 1 || got[0].Text != "self.extra" {
		t.Errorf("Find(single sources) = %v, want one self.extra", got)
	}
}

func BenchmarkFindSingleSources(b *testing.B) {
	data := singleSources(4000)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := extract.Find(context.Background(), "s.yaml", data); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFindHeldBackOrder(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("a: &a\n")
	for i := range 4 {
		fmt.Fprintf(&b, "  p%d: {x-kubernetes-validations: [{rule: self.p%d}]}\n", i, i)
	}
	for i := range 4 {
		fmt.Fprintf(&b, "b%d: &b%d {p%d: {type: string}}\n", i+1, i+1, i)
	}
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          properties:\n")
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&b, "            w%d: {properties: *b%d}\n", i, i)
	}
	b.WriteString("            x: {properties: {<<: [*b1, *b2, *b3, *b4, *a]}}\n            y: {properties: *a}\n")
	for range 30 {
		got, err := extract.Find(context.Background(), "s.yaml", []byte(b.String()))
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Start.Line < got[i-1].Start.Line {
				t.Fatalf("Find() = %v, want file order", got)
			}
		}
		if len(got) != 4 {
			t.Fatalf("Find() = %d expressions, want 4", len(got))
		}
	}
}

// mergedSpec is a policy whose spec merges n anchors that hold none of the
// keys looked up, so only entry's walk over the merge sources can reach n ctx
// checks.
func mergedSpec(n int) []byte {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "a%d: &a%d {k: 0}\n", i, i)
	}
	b.WriteString("apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  <<: [")
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "*a%d", i)
	}
	b.WriteString("]\n")
	return []byte(b.String())
}

func TestFindCancelledWhileLookingUpMergedKey(t *testing.T) {
	t.Parallel()
	const sources = 60
	data := mergedSpec(sources)
	counter := &cancelAfter{Context: context.Background(), n: 1 << 30}
	if _, err := extract.Find(counter, "m.yaml", data); err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if counter.calls < sources {
		t.Fatalf("Find() checked ctx %d times, want at least once per merge source (%d)", counter.calls, sources)
	}
	for n := 1; n <= counter.calls; n++ {
		ctx := &cancelAfter{Context: context.Background(), n: n}
		if _, err := extract.Find(ctx, "m.yaml", data); !errors.Is(err, context.Canceled) {
			t.Fatalf("Find(cancelled at check %d of %d) error = %v, want context.Canceled", n, counter.calls, err)
		}
	}
}

func TestFindUTF16MultiDocument(t *testing.T) {
	t.Parallel()
	doc := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: %s\n"
	text := fmt.Sprintf(doc, "a") + "---\n" + fmt.Sprintf(doc, "é")
	data := []byte{0xff, 0xfe}
	for _, u := range utf16.Encode([]rune(text)) {
		data = binary.LittleEndian.AppendUint16(data, u)
	}
	got, err := extract.Find(context.Background(), "u.yaml", data)
	if err != nil || len(got) != 2 {
		t.Fatalf("Find(UTF-16 stream) = %v, %v, want two expressions", got, err)
	}
	if got[1].Text != "é" || got[1].Document != 1 || got[1].Start.Line != 11 {
		t.Errorf("Find(UTF-16 stream)[1] = %+v, want é in document 1 on line 11", got[1])
	}
}

// TestDocumentsBadCharacter pins which document Documents reports as broken
// for each reason IsBadCharacter accepts, with the bad character in the last
// of several documents. The parser reads ahead and gives the reason on the
// first line of the stream, so the report must still land on the right
// document, in UTF-8 and in UTF-16.
func TestDocumentsBadCharacter(t *testing.T) {
	t.Parallel()
	const (
		good   = "a: 1\n---\nb: 2\n---\n"    // every document before the bad one parses
		broken = "a: b: c\n---\nb: 2\n---\n" // the first document is broken too
		mapErr = "line 1: mapping values are not allowed in this context"
	)
	utf8 := func(tail string) func(head string) string {
		return func(head string) string { return head + tail }
	}
	utf16le := func(tail ...uint16) func(head string) string {
		return func(head string) string {
			b := []byte{0xff, 0xfe}
			for _, u := range append(utf16.Encode([]rune(head)), tail...) {
				b = binary.LittleEndian.AppendUint16(b, u)
			}
			return string(b)
		}
	}
	// outcome is the Index of each document returned and the line and reason of
	// each syntax error.
	type outcome struct {
		docs []int
		errs []string
	}
	// utf8Outcomes is what every UTF-8 reason gives: the bad document, the
	// fourth line, is reported on its own line, and a broken first document is
	// reported apart from it.
	utf8Outcomes := func(reason string) (outcome, outcome) {
		return outcome{[]int{0, 1}, []string{"line 4: " + reason}},
			outcome{[]int{1}, []string{mapErr, "line 4: " + reason}}
	}
	tests := []struct {
		reason string
		build  func(head string) string
		utf16  bool
		good   outcome // for the stream starting with good
		broken outcome // for the stream starting with broken
	}{
		{reason: "control characters are not allowed", build: utf8("k: \x00\n")},
		{reason: "invalid leading UTF-8 octet", build: utf8("k: \xff\n")},
		{reason: "incomplete UTF-8 octet sequence", build: utf8("k: \xe2\x82")},
		{reason: "invalid trailing UTF-8 octet", build: utf8("k: \xc3(\n")},
		{reason: "invalid length of a UTF-8 sequence", build: utf8("k: \xc0\x80\n")},
		{reason: "invalid Unicode character", build: utf8("k: \xed\xa0\x80\n")},
		{
			reason: "incomplete UTF-16 character", utf16: true,
			build:  func(head string) string { return utf16le('k', ':', ' ', 'x')(head) + "\x0a" },
			good:   outcome{[]int{0, 1}, []string{"line 4: incomplete UTF-16 character"}},
			broken: outcome{[]int{1}, []string{mapErr, "line 4: incomplete UTF-16 character"}},
		},
		{
			reason: "unexpected low surrogate area", utf16: true,
			build:  utf16le(0xdc00, '\n'),
			good:   outcome{[]int{0, 1}, []string{"line 4: unexpected low surrogate area"}},
			broken: outcome{[]int{1}, []string{mapErr, "line 4: unexpected low surrogate area"}},
		},
		{
			reason: "incomplete UTF-16 surrogate pair", utf16: true,
			build:  utf16le(0xd800),
			good:   outcome{[]int{0, 1}, []string{"line 4: incomplete UTF-16 surrogate pair"}},
			broken: outcome{[]int{1}, []string{mapErr, "line 4: incomplete UTF-16 surrogate pair"}},
		},
		{
			reason: "expected low surrogate area", utf16: true,
			build:  utf16le(0xd800, 'x', '\n'),
			good:   outcome{[]int{0, 1}, []string{"line 4: expected low surrogate area"}},
			broken: outcome{[]int{1}, []string{mapErr, "line 4: expected low surrogate area"}},
		},
	}
	for _, tt := range tests {
		if !tt.utf16 {
			tt.good, tt.broken = utf8Outcomes(tt.reason)
		}
		if !extract.IsBadCharacter(tt.reason) {
			t.Fatalf("IsBadCharacter(%q) = false, want true", tt.reason)
		}
		for _, l := range []struct {
			name, head string
			want       outcome
		}{
			{"parsing documents before the bad one", good, tt.good},
			{"broken first document", broken, tt.broken},
		} {
			t.Run(tt.reason+"/"+l.name, func(t *testing.T) {
				t.Parallel()
				docs, errs, err := extract.Documents(context.Background(), "s.yaml", []byte(tt.build(l.head)))
				if err != nil {
					t.Fatalf("Documents(%s) error = %v", tt.reason, err)
				}
				var got outcome
				for _, d := range docs {
					got.docs = append(got.docs, d.Index)
				}
				for _, e := range errs {
					got.errs = append(got.errs, fmt.Sprintf("line %d: %s", e.Line, e.Reason))
				}
				if !reflect.DeepEqual(got, l.want) {
					t.Errorf("Documents(%s) = documents %v, errors %q, want documents %v, errors %q",
						tt.reason, got.docs, got.errs, l.want.docs, l.want.errs)
				}
			})
		}
	}
}

// TestDocumentsUTF16BigEndian pins that the "---" lines of a big-endian UTF-16
// stream with a byte order mark are found, so a bad character is reported on
// its own document.
func TestDocumentsUTF16BigEndian(t *testing.T) {
	t.Parallel()
	data := []byte{0xfe, 0xff}
	for _, u := range append(utf16.Encode([]rune("a: 1\n---\nb: 2\n---\nk: ")), 0xdc00, '\n') {
		data = binary.BigEndian.AppendUint16(data, u)
	}
	docs, errs, err := extract.Documents(context.Background(), "s.yaml", data)
	if err != nil || len(docs) != 2 || len(errs) != 1 || errs[0].Line != 4 {
		t.Fatalf("Documents(big-endian UTF-16) = %d documents, %v, %v, want 2 documents and one error on line 4", len(docs), errs, err)
	}
}

func TestDocumentsBareCarriageReturn(t *testing.T) {
	t.Parallel()
	const text = "a: 1\r---\rb: 2\r---\rk: "
	utf16le := []byte{0xff, 0xfe}
	for _, u := range append(utf16.Encode([]rune(text)), 0xdc00, '\n') {
		utf16le = binary.LittleEndian.AppendUint16(utf16le, u)
	}
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"UTF-8", []byte(text + "\xff\n")},
		{"UTF-16LE", utf16le},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			docs, errs, err := extract.Documents(context.Background(), "s.yaml", tt.data)
			if err != nil || len(docs) != 2 || len(errs) != 1 || errs[0].Line != 4 {
				t.Fatalf("Documents(bare CR, %s) = %d documents, %v, %v, want 2 documents and one error on line 4", tt.name, len(docs), errs, err)
			}
		})
	}
}

func TestDocumentsUnicodeLineBreaks(t *testing.T) {
	t.Parallel()
	for _, br := range []struct{ name, text string }{
		{"NEL", "\u0085"},
		{"LS", "\u2028"},
		{"PS", "\u2029"},
	} {
		text := "a: 1" + br.text + "---" + br.text + "b: 2" + br.text + "---" + br.text + "k: "
		utf16le := []byte{0xff, 0xfe}
		for _, u := range append(utf16.Encode([]rune(text)), 0xdc00, '\n') {
			utf16le = binary.LittleEndian.AppendUint16(utf16le, u)
		}
		for _, tt := range []struct {
			name string
			data []byte
		}{
			{"UTF-8", []byte(text + "\xff\n")},
			{"UTF-16LE", utf16le},
		} {
			t.Run(br.name+" "+tt.name, func(t *testing.T) {
				t.Parallel()
				docs, errs, err := extract.Documents(context.Background(), "s.yaml", tt.data)
				if err != nil || len(docs) != 2 || len(errs) != 1 || errs[0].Line != 4 {
					t.Fatalf("Documents(%s, %s) = %d documents, %v, %v, want 2 documents and one error on line 4", br.name, tt.name, len(docs), errs, err)
				}
			})
		}
	}
}

func TestDocumentsParserProblemAfterUnicodeLineBreak(t *testing.T) {
	t.Parallel()
	for _, br := range []struct{ name, text string }{
		{"NEL", "\u0085"},
		{"LS", "\u2028"},
		{"PS", "\u2029"},
	} {
		for _, tt := range []struct {
			name string
			text string
			line int
		}{
			{"first document", "a: 1\nb:\n  - x\n  y: 2\n", 4},
			{"after separator", "a: 1\n---\nb:\n  - x\n  y: 2\n---\nc: 3\n", 5},
			{"flow sequence", "a: 1\n---\nb: [1, 2\nc: 3\n---\nf: 3\n", 3},
		} {
			t.Run(br.name+" "+tt.name, func(t *testing.T) {
				t.Parallel()
				text := strings.ReplaceAll(tt.text, "\n", br.text)
				_, errs, err := extract.Documents(context.Background(), "s.yaml", []byte(text))
				if err != nil || len(errs) != 1 || errs[0].Line != tt.line {
					t.Fatalf("Documents(%s %s) = %v, %v, want one error on line %d", br.name, tt.name, errs, err, tt.line)
				}
			})
		}
	}
}

func TestFindKyvernoValidatingPolicy(t *testing.T) {
	t.Parallel()
	const src = `apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
spec:
  variables:
    - name: v
      expression: "object.a"
  validations:
    - expression: >-
        variables.v == 1
      messageExpression: 'msg'
  matchConditions:
    - name: m
      expression: plain
  auditAnnotations:
    - key: k
      valueExpression: |
        audit
`
	got, err := extract.Find(context.Background(), "p.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	type r struct {
		text  string
		field string
		line  int
	}
	var have []r
	for _, e := range got {
		have = append(have, r{e.Text, e.FieldPath, e.Start.Line})
	}
	want := []r{
		{"variables.v == 1", "spec.validations[0].expression", 9},
		{"msg", "spec.validations[0].messageExpression", 10},
		{"plain", "spec.matchConditions[0].expression", 13},
		{"object.a", "spec.variables[0].expression", 6},
		{"audit\n", "spec.auditAnnotations[0].valueExpression", 17},
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("Find(ValidatingPolicy) = %v, want %v", have, want)
	}
}

// BenchmarkDocuments measures splitting a large multi-document stream, in
// UTF-8 and in UTF-16LE, clean and with a bad character in the last document.
func BenchmarkDocuments(b *testing.B) {
	var sb strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&sb, "---\napiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  name: policy-%d\nspec:\n  validations:\n    - expression: \"object.spec.replicas > 0 && object.metadata.name.startsWith('é')\"\n      message: replicas must be positive\n", i)
	}
	text := sb.String()
	utf16le := func(tail ...uint16) []byte {
		out := []byte{0xff, 0xfe}
		for _, u := range append(utf16.Encode([]rune(text)), tail...) {
			out = binary.LittleEndian.AppendUint16(out, u)
		}
		return out
	}
	tests := []struct {
		name string
		data []byte
	}{
		{"utf8", []byte(text)},
		{"utf8-bad-late", []byte(text + "---\nk: \x00\n")},
		{"utf16le", utf16le()},
		{"utf16le-bad-late", utf16le('-', '-', '-', '\n', 'k', ':', ' ', 0xd800)},
	}
	ctx := context.Background()
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.SetBytes(int64(len(tt.data)))
			for b.Loop() {
				if _, _, err := extract.Documents(ctx, "p.yaml", tt.data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestDocumentsBrokenBeforeSeparator(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		text    string
		indexes []int
		lines   []int
	}{
		{"after end marker", "a: 1\n...\nb: [1,\n---\nc: 3\n", []int{0, 2}, []int{3}},
		{"after end marker, later", "a: 1\n---\nb: 2\n...\nc: d: e\n---\nf: 3\n", []int{0, 1, 3}, []int{5}},
		{"after end marker, indented", "---\nb: 2\n...\nkind: x\n  bad: 1\n---\nc: 3\n", []int{0, 2}, []int{4}},
		{"directive", "a: 1\n...\n%YAML 1.2\n%YAML 1.2\n---\nb: 2\n", []int{0, 2}, []int{3}},
		{"bad document skipped past", "a: 1\n---\nb: [1,\n---\nc: 3\n---\nd: 4\n", []int{0, 2, 3}, []int{3}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			docs, errs, err := extract.Documents(context.Background(), "s.yaml", []byte(tt.text))
			if err != nil {
				t.Fatalf("Documents(%q) error = %v", tt.text, err)
			}
			var indexes, lines []int
			for _, d := range docs {
				indexes = append(indexes, d.Index)
			}
			for _, e := range errs {
				lines = append(lines, e.Line)
			}
			if !slices.Equal(indexes, tt.indexes) || !slices.Equal(lines, tt.lines) {
				t.Errorf("Documents(%q) = documents %v, error lines %v, want %v, %v", tt.text, indexes, lines, tt.indexes, tt.lines)
			}
		})
	}
}
