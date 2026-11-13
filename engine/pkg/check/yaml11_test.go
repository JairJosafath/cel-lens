package check_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// yaml11Words are the plain scalars that kubectl and the API server, whose
// YAML is sigs.k8s.io/yaml, read as booleans (YAML 1.1), and go-yaml v3 as
// strings.
var yaml11Words = []string{"y", "n", "yes", "no", "on", "off", "Y", "N", "Yes", "No", "On", "Off", "YES", "NO", "ON", "OFF"}

// wrongType is the message of a boolean where a string is wanted, at path.
func wrongType(path string) func(word string) string {
	return func(word string) string {
		return fmt.Sprintf("%s: expected a string, got a boolean: kubectl reads an unquoted %s as one (YAML 1.1), so quote it if a string is meant", path, word)
	}
}

// truthy reports whether YAML 1.1 reads word as true.
func truthy(word string) bool {
	switch strings.ToLower(word) {
	case "y", "yes", "on":
		return true
	}
	return false
}

// TestCheckYAML11Words is the test of #27: a plain y, n, yes, no, on or off
// where the API server wants a string is a boolean to it, and to kubectl,
// which sends the boolean: it refuses the document. The check read these
// words as strings, and said nothing of a policy with an audit annotation
// whose key was n.
func TestCheckYAML11Words(t *testing.T) {
	t.Parallel()
	const policyHead = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  name: p\n"
	tests := []struct {
		name string
		src  string                   // %s is the word
		line int                      // of the word
		col  int                      // of the word
		want func(word string) string // the message
		cel  int                      // the line of a mistake in the policy's CEL, which is still reported, or 0
	}{
		{
			name: "audit annotation key",
			src:  policyHead + "spec:\n  validations:\n    - expression: object.spec.replicas > 1 &&\n  auditAnnotations:\n    - key: %s\n      valueExpression: \"'x'\"\n",
			line: 9, col: 12,
			want: wrongType("spec.auditAnnotations[0].key"),
			cel:  7,
		},
		{
			name: "label value",
			src:  policyHead + "  labels:\n    tier: %s\nspec:\n  validations:\n    - expression: object.spec.replicas > 1 &&\n",
			line: 6, col: 11,
			want: wrongType("metadata.labels[tier]"),
			cel:  9,
		},
		{
			name: "label value of a policy without expressions",
			src:  policyHead + "  labels:\n    tier: %s\nspec:\n  failurePolicy: Fail\n",
			line: 6, col: 11,
			want: wrongType("metadata.labels[tier]"),
		},
		{
			name: "label value of a binding",
			src: "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicyBinding\nmetadata:\n  name: b\n  labels: {tier: %s}\n" +
				"spec:\n  policyName: p\n  validationActions: [Deny]\n",
			line: 5, col: 18,
			want: wrongType("metadata.labels[tier]"),
		},
		{
			name: "enum value of a CRD",
			src: "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: example.com\n" +
				"  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}\n  scope: Namespaced\n  versions:\n    - name: v1\n      served: true\n      storage: true\n" +
				"      schema:\n        openAPIV3Schema:\n          type: object\n          properties:\n            spec:\n              type: object\n              properties:\n" +
				"                answer:\n                  type: string\n                  enum: [a, %s]\n",
			line: 22, col: 29,
			want: func(word string) string {
				return fmt.Sprintf("spec.versions[0].schema.openAPIV3Schema.properties[spec].properties[answer].enum[1]: kubectl reads the unquoted %s as the boolean %t, not the string %q (YAML 1.1): quote it", word, truthy(word), word)
			},
		},
	}
	for _, tt := range tests {
		for _, word := range yaml11Words {
			t.Run(tt.name+"/"+word, func(t *testing.T) {
				t.Parallel()
				src := strings.ReplaceAll(tt.src, "%s", word)
				want := tt.want(word)
				got, err := check.Check(context.Background(), "w.yaml", []byte(src))
				if err != nil {
					t.Fatalf("Check() error = %v", err)
				}
				var found int
				for _, d := range got {
					if d.Start.Line == tt.line && d.Message == want {
						found++
						if d.Start.Column != tt.col || d.End.Line != tt.line || d.End.Column != tt.col+len(word) {
							t.Errorf("Check(%s %s) = %q at %v-%v, want it at %d:%d-%d", tt.name, word, d.Message, d.Start, d.End, tt.line, tt.col, tt.col+len(word))
						}
					}
				}
				if found != 1 {
					t.Errorf("Check(%s %s) = %d problems like %q, want 1: %v", tt.name, word, found, want, got)
				}
				if tt.cel != 0 && !hasCELProblem(got, tt.cel) {
					t.Errorf("Check(%s %s) = %v, want the policy's CEL checked still", tt.name, word, got)
				}
			})
		}
	}
}

// hasCELProblem reports whether ds has a diagnostic on line, from 1, that is
// not about a wrong type.
func hasCELProblem(ds []check.Diagnostic, line int) bool {
	for _, d := range ds {
		if d.Start.Line == line && !strings.Contains(d.Message, "expected a string") {
			return true
		}
	}
	return false
}

// TestCheckYAML11Quoted checks, for #27, that the words are strings, with
// nothing said, where they are quoted or tagged, or where a boolean is wanted.
func TestCheckYAML11Quoted(t *testing.T) {
	t.Parallel()
	const policyHead = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  name: p\n"
	const crdHead = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: example.com\n" +
		"  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}\n  scope: Namespaced\n  versions:\n    - name: v1\n      served: true\n      storage: true\n" +
		"      schema:\n        openAPIV3Schema:\n          type: object\n          properties:\n            spec:\n              type: object\n              properties:\n"
	for name, src := range map[string]string{
		"double quoted key": policyHead + "spec:\n  validations:\n    - expression: \"true\"\n  auditAnnotations:\n    - key: \"n\"\n      valueExpression: \"'x'\"\n",
		"single quoted key": policyHead + "spec:\n  validations:\n    - expression: \"true\"\n  auditAnnotations:\n    - key: 'yes'\n      valueExpression: \"'x'\"\n",
		"tagged key":        policyHead + "spec:\n  validations:\n    - expression: \"true\"\n  auditAnnotations:\n    - key: !!str off\n      valueExpression: \"'x'\"\n",
		"quoted label":      policyHead[:len(policyHead)-1] + "\n  labels: {tier: \"no\"}\nspec:\n  failurePolicy: Fail\n",
		"a boolean field":   "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: example.com\n  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}\n  scope: Namespaced\n  versions:\n    - {name: v1, served: yes, storage: on, schema: {openAPIV3Schema: {type: object}}}\n",
		"quoted enum":       crdHead + "                answer:\n                  type: string\n                  enum: [\"y\", 'n', a]\n",
		"boolean enum":      crdHead + "                answer:\n                  type: boolean\n                  enum: [yes, no]\n",
		"a word as a name":  crdHead + "                answer:\n                  type: string\n                  enum: [yesterday, nope, only, offer]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "w.yaml", []byte(src))
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			for _, d := range got {
				if strings.Contains(d.Message, "YAML 1.1") || strings.Contains(d.Message, "expected a ") {
					t.Errorf("Check(%s) = %q, want no problem with a YAML 1.1 boolean", name, d.Message)
				}
			}
		})
	}
}

// TestCheckYAML11Boolean checks, for #27, that a plain yes or on where the API
// server wants a boolean is the boolean: the CRD whose version is served: yes
// and storage: on is clean, where it had an error for each, "expected a
// boolean, got a string".
func TestCheckYAML11Boolean(t *testing.T) {
	t.Parallel()
	src := "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: example.com\n" +
		"  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}\n  scope: Namespaced\n  versions:\n    - name: v1\n      served: yes\n      storage: on\n" +
		"      schema:\n        openAPIV3Schema:\n          type: object\n          x-kubernetes-validations:\n            - rule: self.nope > 1\n"
	got, err := check.Check(context.Background(), "w.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	for _, d := range got {
		if strings.Contains(d.Message, "expected a boolean") {
			t.Errorf("Check(served: yes) = %q, want the word read as a boolean", d.Message)
		}
	}
	if len(got) == 0 {
		t.Error("Check(served: yes) = no problems, want the rule's: the CRD is read")
	}
}

// TestCheckYAML11Expression checks, for #27, that a CEL expression that is a
// plain yes is one problem, a boolean where a string is wanted, and not also
// the expression "yes" failing to compile.
func TestCheckYAML11Expression(t *testing.T) {
	t.Parallel()
	src := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  name: p\nspec:\n  validations:\n    - expression: yes\n"
	got, err := check.Check(context.Background(), "w.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	want := "spec.validations[0].expression: expected a string, got a boolean: kubectl reads an unquoted yes as one (YAML 1.1), so quote it if a string is meant"
	if len(got) != 1 || got[0].Message != want {
		t.Errorf("Check(expression: yes) = %v, want only %q", got, want)
	}
}

// TestCheckYAML11Kyverno checks, for #27, that a Kyverno ValidatingPolicy whose
// only expressions are plain yes words is not left alone, with the kyverno
// feature on: the API server refuses it, and so does the check.
func TestCheckYAML11Kyverno(t *testing.T) {
	t.Parallel()
	const head = "apiVersion: policies.kyverno.io/v1\nkind: ValidatingPolicy\nmetadata:\n  name: p\nspec:\n  validations:\n"
	for name, tt := range map[string]struct {
		src  string
		want string
		line int
	}{
		"an expression":      {head + "    - expression: yes\n", "spec.validations[0].expression", 7},
		"an alias":           {head + "    - expression: &a yes\n      message: *a\n", "spec.validations[0].expression", 7},
		"a message and none": {head + "    - expression: \"true\"\n      message: no\n", "spec.validations[0].message", 8},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := check.Check(context.Background(), "k.yaml", []byte(tt.src), check.WithFeatures(feature.Of(feature.Kyverno)))
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			var found bool
			for _, d := range got {
				found = found || strings.HasPrefix(d.Message, tt.want+": expected a string, got a boolean") && d.Start.Line >= 7
			}
			if !found {
				t.Errorf("Check(%s) = %v, want %q: expected a string, got a boolean", name, got, tt.want)
			}
		})
	}
}
