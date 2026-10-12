package check_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

const crdHead = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
  scope: Namespaced
  versions:
`

// crdVersion is a CRD version named name with the schema properties, which
// must be indented to 14 spaces.
func crdVersion(name, properties string) string {
	return fmt.Sprintf(`    - name: %s
      served: true
      storage: %t
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: "true"
          properties:
%s`, name, name == "v1", properties)
}

func TestCheckWrongTypedField(t *testing.T) {
	t.Parallel()
	policy := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n"
	tests := []struct {
		name     string
		src      string
		wantLine int    // of the wrong field
		wantCol  int    // of the wrong field's value
		wantEnd  int    // the column just after the problem, on the same line
		wantMsg  string // contained in its message
		wantCEL  int    // the line of a CEL problem elsewhere in the document, or 0
	}{
		{
			name:     "policy failurePolicy",
			src:      policy + "  failurePolicy: 3\n  validations:\n    - expression: object.x +\n",
			wantLine: 4, wantCol: 18, wantEnd: 19, wantMsg: "spec.failurePolicy: expected a string, got the number 3",
			wantCEL: 6,
		},
		{
			name:     "policy validation item",
			src:      policy + "  validations:\n    - expression: \"true\"\n    - expression: \"true\"\n      message: [a]\n",
			wantLine: 7, wantCol: 16, wantEnd: 19, wantMsg: "spec.validations[1].message: expected a string, got a list",
		},
		{
			name: "crd property named type",
			src: crdHead + crdVersion("v1", `            spec:
              type: object
              properties:
                type:
                  type: string
                  maxLength: 10
                size:
                  type: 5
`) + crdVersion("v2", "            spec:\n              type: object\n              x-kubernetes-validations:\n                - rule: self.nope > 1\n"),
			wantLine: 28, wantCol: 25, wantEnd: 26, wantMsg: "spec.versions[0].schema.openAPIV3Schema.properties[spec].properties[size].type: expected a string, got the number 5",
			wantCEL: 41,
		},
		{
			name: "crd property named properties",
			src: crdHead + crdVersion("v1", `            properties:
              type: object
              properties:
                properties:
                  type: object
                  properties:
                    type:
                      type: string
                    maxItems:
                      type: array
                      maxItems: many
`),
			wantLine: 31, wantCol: 33, wantEnd: 37, wantMsg: "properties[properties].properties[properties].properties[maxItems].maxItems: expected an integer, got a string",
		},
		{
			name: "crd items",
			src: crdHead + crdVersion("v1", `            list:
              type: array
              items:
                type: 5
`),
			wantLine: 24, wantCol: 23, wantEnd: 24, wantMsg: "properties[list].items.type: expected a string, got the number 5",
		},
		{
			name:     "crd names",
			src:      strings.Replace(crdHead, "plural: widgets", "plural: [widgets]", 1) + crdVersion("v1", "            a:\n              type: string\n"),
			wantLine: 9, wantCol: 13, wantEnd: 22, wantMsg: "spec.names.plural: expected a string, got a list",
		},
		{
			name:     "double quoted scalar with a comment",
			src:      policy + "  failurePolicy: 3 # why\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 18, wantEnd: 19, wantMsg: "failurePolicy",
		},
		{
			name:     "quoted scalar with an escape and a comment",
			src:      policy + "  paramKind: \"x\\\"y\" # q\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 14, wantEnd: 20, wantMsg: "paramKind",
		},
		{
			name:     "anchored scalar",
			src:      policy + "  failurePolicy: &a 3\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 21, wantEnd: 22, wantMsg: "failurePolicy",
		},
		{
			name:     "block list",
			src:      policy + "  failurePolicy:\n    - a\n    - b\n  validations:\n    - expression: \"true\"\n",
			wantLine: 5, wantCol: 5, wantEnd: 8, wantMsg: "expected a string, got a list",
		},
		{
			name:     "block mapping",
			src:      policy + "  failurePolicy:\n    a: b\n  validations:\n    - expression: \"true\"\n",
			wantLine: 5, wantCol: 5, wantEnd: 9, wantMsg: "expected a string, got an object",
		},
		{
			name:     "flow list with a comment",
			src:      policy + "  failurePolicy: [a, \"]\"] # why\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 18, wantEnd: 26, wantMsg: "got a list",
		},
		{
			name:     "flow list with an apostrophe in a plain scalar",
			src:      policy + "  failurePolicy: [it's, 'x'] # c\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 18, wantEnd: 29, wantMsg: "got a list",
		},
		{
			name:     "flow list with an apostrophe after a colon in a plain scalar",
			src:      policy + "  failurePolicy: [a:'b] # c\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 18, wantEnd: 24, wantMsg: "got a list",
		},
		{
			name:     "flow list with a quote after an anchor",
			src:      policy + "  failurePolicy: [&a 'x]', b] # c\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 18, wantEnd: 30, wantMsg: "got a list",
		},
		{
			name:     "flow list with a quote after a tag",
			src:      policy + "  failurePolicy: [!!str 'x]', b] # c\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 18, wantEnd: 33, wantMsg: "got a list",
		},
		{
			name:     "flow list with a quote after a verbatim tag with a comma",
			src:      policy + "  failurePolicy: [!<tag:yaml.org,2002:str> 'x]', b] # c\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 18, wantEnd: 52, wantMsg: "got a list",
		},
		{
			name:     "flow mapping followed by fields",
			src:      policy + "  validations: [{message: [b], expression: \"y +\"}]\n",
			wantLine: 4, wantCol: 27, wantEnd: 30, wantMsg: "got a list",
			wantCEL: 4,
		},
		{
			name:     "multi-line plain scalar",
			src:      policy + "  paramKind: foo\n    bar\n  validations:\n    - expression: \"true\"\n",
			wantLine: 4, wantCol: 14, wantEnd: 17, wantMsg: "paramKind",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "w.yaml", []byte(tt.src))
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			var field, cel int
			for _, d := range got {
				if strings.Contains(d.Message, "expected") && strings.Contains(d.Message, "got") {
					field++
					if d.Start.Line != tt.wantLine || d.Start.Column != tt.wantCol || d.End.Line != tt.wantLine || d.End.Column != tt.wantEnd || !strings.Contains(d.Message, tt.wantMsg) {
						t.Errorf("Check(%s) = %v at %v, want %q at %d:%d-%d", tt.name, d.Message, d.Start, tt.wantMsg, tt.wantLine, tt.wantCol, tt.wantEnd)
					}
				} else if tt.wantCEL != 0 && d.Start.Line == tt.wantCEL {
					cel++
				}
			}
			if field != 1 {
				t.Errorf("Check(%s) = %d wrong-type problems, want 1: %v", tt.name, field, got)
			}
			if tt.wantCEL != 0 && cel == 0 {
				t.Errorf("Check(%s) has no CEL problem on line %d, want one: %v", tt.name, tt.wantCEL, got)
			}
			if tt.wantCEL == 0 && len(got) != 1 {
				t.Errorf("Check(%s) = %d problems, want only the wrong type: %v", tt.name, len(got), got)
			}
		})
	}
}

// deepCRD is a CRD of two versions whose schemas hold properties named
// properties nested depth deep; only the second has a wrong-typed field.
func deepCRD(depth int) string {
	var schema func(indent int, wrong bool) string
	schema = func(indent int, wrong bool) string {
		pad := strings.Repeat(" ", indent)
		if indent > 14+4*depth {
			if wrong {
				return pad + "leaf:\n" + pad + "  type: 5\n"
			}
			return pad + "leaf:\n" + pad + "  type: string\n"
		}
		return fmt.Sprintf("%sproperties:\n%s  properties:\n%s", pad, pad, schema(indent+4, wrong))
	}
	return crdHead + crdVersion("v1", schema(12, false)) + crdVersion("v2", schema(12, true))
}

func TestCheckDeeplyNestedWrongType(t *testing.T) {
	t.Parallel()
	src := deepCRD(14)
	got, err := check.Check(context.Background(), "d.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Message, "spec.versions[1].") || !strings.Contains(got[0].Message, ".type: expected a string") {
		t.Fatalf("Check(deep) = %v, want one wrong type in version 2", got)
	}
	if want := strings.Count(src, "\n"); got[0].Start.Line < want-1 {
		t.Errorf("Check(deep) on line %d, want the last line, %d", got[0].Start.Line, want)
	}
}

func TestCheckWrongTypeInSelfDecodingType(t *testing.T) {
	t.Parallel()
	src := crdHead + crdVersion("v1", "            list:\n              type: array\n              items:\n                type: 5\n") +
		crdVersion("v2", "            spec:\n              type: object\n              x-kubernetes-validations:\n                - rule: self.nope > 1\n")
	got, err := check.Check(context.Background(), "s.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	var wrong, undefined bool
	for _, d := range got {
		wrong = wrong || strings.Contains(d.Message, "items.type: expected a string")
		undefined = undefined || strings.Contains(d.Message, "nope")
	}
	if !wrong || !undefined {
		t.Errorf("Check(items: {type: 5} then a bad rule) = %v, want the wrong type and the undefined field", got)
	}
}

// largeCRD is a CRD of about 4,000 lines; with wrong, one field deep in it is
// of the wrong type.
func largeCRD(wrong bool) string {
	var b strings.Builder
	for i := range 100 {
		fmt.Fprintf(&b, "            f%d:\n              type: object\n              properties:\n", i)
		for j := range 9 {
			fmt.Fprintf(&b, "                g%d:\n                  type: string\n                  maxLength: 10\n", j)
		}
		if wrong && i == 90 {
			b.WriteString("                bad:\n                  type: 5\n")
		}
		b.WriteString("              x-kubernetes-validations:\n                - rule: \"self.g0 != ''\"\n")
	}
	return crdHead + crdVersion("v1", b.String())
}

func BenchmarkCheckLargeCRD(b *testing.B) {
	for _, c := range []struct {
		name  string
		wrong bool
	}{{"valid", false}, {"wrong-type", true}} {
		data := []byte(largeCRD(c.wrong))
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				if _, err := check.Check(context.Background(), "b.yaml", data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestCheckWrongTypeKeepsRulesOfSameVersion(t *testing.T) {
	t.Parallel()
	src := crdHead + crdVersion("v1", `            f0:
              type: object
              properties:
                bad:
                  type: 5
              x-kubernetes-validations:
                - rule: self.nope != ''
            f1:
              type: object
              properties:
                ok:
                  type: string
              x-kubernetes-validations:
                - rule: self.nope != ''
                - rule: self.ok != ''
`)
	got, err := check.Check(context.Background(), "s.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	var wrong, nope, other int
	for _, d := range got {
		switch {
		case strings.Contains(d.Message, "type: expected a string"):
			wrong++
		case strings.Contains(d.Message, "'nope'"):
			nope++
		default:
			other++
		}
	}
	if wrong != 1 || nope != 2 || other != 0 {
		t.Errorf("Check(wrong type with rules in the same version) = %v, want 1 wrong type and 2 undefined fields", got)
	}
}

func TestCheckWrongTypeSkipsRulesOfGuessedSchema(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		versions   func(properties string) string
		properties string
		want       []string // substrings of the messages, in any order
	}{
		{
			name: "own rule",
			properties: `            size:
              type: 5
              x-kubernetes-validations:
                - rule: self.startsWith('a')
`,
			want: []string{"size].type: expected a string, got the number 5"},
		},
		{
			name: "parent and sibling rules still checked",
			properties: `            parent:
              type: object
              x-kubernetes-validations:
                - rule: self.nope != ''
              properties:
                size:
                  type: 5
                  x-kubernetes-validations:
                    - rule: self.startsWith('a')
                sibling:
                  type: string
                  x-kubernetes-validations:
                    - rule: self.missing()
`,
			want: []string{"nope", "type: expected a string, got the number 5", "missing"},
		},
		{
			name: "own rule in one of several versions",
			versions: func(properties string) string {
				return crdVersion("v1", properties) + crdVersion("v2", `            other:
              type: string
`)
			},
			properties: `            size:
              type: 5
              x-kubernetes-validations:
                - rule: self.startsWith('a')
`,
			want: []string{"size].type: expected a string, got the number 5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			versions := func(p string) string { return crdVersion("v1", p) }
			if tt.versions != nil {
				versions = tt.versions
			}
			got, err := check.Check(context.Background(), "s.yaml", []byte(crdHead+versions(tt.properties)))
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Check(guessed type) = %v, want %d problems", got, len(tt.want))
			}
			for _, w := range tt.want {
				found := false
				for _, d := range got {
					found = found || strings.Contains(d.Message, w)
				}
				if !found {
					t.Errorf("Check(guessed type) = %v, want a problem containing %q", got, w)
				}
			}
		})
	}
}

func TestCheckWrongTypedAliasEndsAtName(t *testing.T) {
	t.Parallel()
	src := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  generation: &a 3\nspec:\n  failurePolicy: *a # c\n  validations:\n    - expression: \"true\"\n"
	got, err := check.Check(context.Background(), "a.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	for _, d := range got {
		if d.Start.Line == 6 {
			if d.Start.Column != 18 || d.End.Line != 6 || d.End.Column != 20 {
				t.Errorf("Check(alias) = %v-%v, want 6:18-6:20", d.Start, d.End)
			}
			return
		}
	}
	t.Errorf("Check(alias) has no problem on line 6: %v", got)
}

func TestCheckWrongTypedListAlias(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		field string // the line of the schema that holds the alias, at 10 spaces
	}{
		{name: "description", field: "          description: *a\n"},
		{name: "rule", field: "          x-kubernetes-validations:\n            - rule: *a\n"},
		{name: "message", field: "          x-kubernetes-validations:\n            - rule: \"true\"\n              message: *a\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := crdHead + `    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          enum: &a [1, 2]
` + tt.field + `          properties:
            x:
              type: object
              x-kubernetes-validations:
                - rule: self.nope > 1
`
			got, err := check.Check(context.Background(), "a.yaml", []byte(src))
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			var wrong, undefined bool
			for _, d := range got {
				if strings.Contains(d.Message, "expected a string, got a list") {
					wrong = true
					line := strings.Split(src, "\n")[d.Start.Line-1]
					if line[d.Start.Column-1:d.End.Column-1] != "*a" || d.End.Line != d.Start.Line {
						t.Errorf("Check(alias) problem = %v-%v, want it on *a", d.Start, d.End)
					}
				}
				undefined = undefined || strings.Contains(d.Message, "nope")
			}
			if !wrong || !undefined {
				t.Errorf("Check(alias of a list) = %v, want the wrong type and the undefined field", got)
			}
		})
	}
}

func TestCheckWrongTypeInFirstDocumentKeepsLaterDocuments(t *testing.T) {
	t.Parallel()
	src := crdHead + crdVersion("v1", "            size:\n              type: 5\n") +
		"---\napiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: object.x +\n"
	policyLine := strings.Count(src, "\n")
	got, err := check.Check(context.Background(), "m.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	var wrong, rule int
	for _, d := range got {
		switch {
		case strings.Contains(d.Message, "type: expected a string, got the number 5") && d.Start.Line < policyLine-4:
			wrong++
		case d.Start.Line == policyLine:
			rule++
		}
	}
	if wrong != 1 || rule == 0 || len(got) != wrong+rule {
		t.Errorf("Check(wrong-typed CRD, then a VAP with a bad rule) = %v, want 1 wrong type and a problem on line %d", got, policyLine)
	}
}

func BenchmarkCheckLongFlowLine(b *testing.B) {
	data := []byte("apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  failurePolicy: [a" + strings.Repeat("'", 50000) + "] # c\n  validations:\n    - expression: \"true\"\n")
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := check.Check(context.Background(), "b.yaml", data); err != nil {
			b.Fatal(err)
		}
	}
}
