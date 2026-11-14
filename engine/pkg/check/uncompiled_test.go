package check_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// uncompiledCRD is a CRD whose ports have rules, under spec, beside other,
// with rules of its own; broken is put in the schema of the ports' items.
func uncompiledCRD(items string) string {
	return `apiVersion: apiextensions.k8s.io/v1
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
                ports:
                  type: array
` + items + `                  items:
                    type: object
                    properties:
                      name: {type: string}
                    x-kubernetes-validations:
                      - rule: self.name.size() <= 3
              x-kubernetes-validations:
                - rule: self.nope > 1
            other:
              type: object
              x-kubernetes-validations:
                - rule: self.nope2 > 1
`
}

// TestUncompiled checks that a problem that keeps the check from compiling
// CEL lists where the expressions it kept from being compiled start
// (Diagnostic.Uncompiled, cel-lens-core#35): every expression of a document
// that cannot be read; every rule of a CRD schema that is not structural,
// which the API server compiles none of; the rules at and above a schema
// node with an error of its own, but not those beside or below it, which
// are compiled; none of an expression that compiled.
func TestUncompiled(t *testing.T) {
	t.Parallel()
	at := func(text, needle string) extract.Position {
		for i, l := range strings.Split(text, "\n") {
			if c := strings.Index(l, needle); c >= 0 {
				return extract.Position{Line: i + 1, Column: c + 1}
			}
		}
		t.Fatalf("%q not in the text", needle)
		return extract.Position{}
	}
	structural := uncompiledCRD("")
	notStructural := strings.Replace(structural, "                    type: object\n                    properties:\n                      name", "                    properties:\n                      name", 1)
	unique := uncompiledCRD("                  uniqueItems: true\n")
	specType := "            spec:\n              type: object\n"
	nestedDefault := strings.Replace(structural, specType, specType+"              default: {ports: [{name: 3}]}\n", 1)
	listMapKey := uncompiledCRD("                  x-kubernetes-list-type: map\n                  x-kubernetes-list-map-keys: [name]\n")
	listMapKeyBroken := strings.Replace(listMapKey, "rule: self.name.size() <= 3", "rule: self.nope3 > 0", 1)
	underAllOf := strings.Replace(structural, specType, specType+"              allOf:\n                - properties:\n                    ports:\n                      x-kubernetes-validations:\n                        - rule: self.size() > 0\n", 1)
	unreadable := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  name: p\n  creationTimestamp: notatime\nspec:\n  validations:\n    - expression: object.a > 0\n      messageExpression: \"'a'\"\n"
	tests := []struct {
		name, text string
		// stopper is a word of the message of the problem that stopped the check.
		stopper string
		// uncompiled are words of the expressions it lists, compiled are those
		// of the expressions with a problem of their own.
		uncompiled, compiled []string
	}{
		{"structural", structural, "", nil, []string{"nope > 1", "nope2"}},
		{"not structural", notStructural, "must not be empty for specified array items", []string{"self.name.size()", "self.nope > 1", "self.nope2"}, nil},
		{"error at a schema node", unique, "uniqueItems", []string{"self.nope > 1"}, []string{"nope2"}},
		// A default wrong below its own node is refused at default.ports[0].name,
		// and the API server compiles no rule of the version (#35).
		{"nested default", nestedDefault, "must be of type string", []string{"self.name.size()", "self.nope > 1", "self.nope2"}, nil},
		// A list-map key with neither a default nor required is the error of
		// the list, not of the version or of the items: the rules beside it and
		// those of its items are compiled (#35).
		{"list-map key", listMapKeyBroken, "must have a default or be a required property", []string{"self.nope > 1"}, []string{"nope2", "nope3"}},
		// A rule under allOf makes the version not structural, whatever the
		// kind of its error (#35).
		{"rule under allOf", underAllOf, "must be empty to be structural", []string{"self.name.size()", "self.nope > 1", "self.nope2"}, nil},
		{"unreadable document", unreadable, "notatime", []string{"object.a > 0", "'a'"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(context.Background(), "f.yaml", []byte(tt.text))
			if err != nil {
				t.Fatal(err)
			}
			var stoppers []check.Diagnostic
			var others []string
			for _, d := range diags {
				if len(d.Uncompiled) > 0 {
					stoppers = append(stoppers, d)
				} else {
					others = append(others, d.Message)
				}
			}
			if tt.stopper == "" {
				if len(stoppers) > 0 {
					t.Errorf("Check() = %+v, want no problem that stops a compile", stoppers)
				}
			} else {
				var want []extract.Position
				for _, w := range tt.uncompiled {
					want = append(want, at(tt.text, w))
				}
				slices.SortFunc(want, func(a, b extract.Position) int { return a.Line - b.Line })
				wantStop := check.StopSchema
				if tt.name == "unreadable document" {
					wantStop = check.StopUnreadable
				}
				if len(stoppers) > 0 && stoppers[0].Stop != wantStop {
					t.Errorf("Check() stopping problem's Stop = %q, want %q (#42)", stoppers[0].Stop, wantStop)
				}
				if len(stoppers) == 0 || !strings.Contains(stoppers[0].Message, tt.stopper) || !slices.Equal(stoppers[0].Uncompiled, want) || stoppers[0].Severity != check.SeverityError {
					t.Errorf("Check() stopping problems = %+v, want one with %q listing %v", stoppers, tt.stopper, want)
				}
			}
			for _, w := range tt.compiled {
				if !slices.ContainsFunc(others, func(m string) bool { return strings.Contains(m, strings.TrimSuffix(strings.Fields(w)[0], ">")) }) {
					t.Errorf("Check() others = %q, want the problem of %q, which is compiled", others, w)
				}
				// A compiled rule is never also listed as not compiled.
				for _, d := range stoppers {
					if slices.Contains(d.Uncompiled, at(tt.text, "self."+w)) {
						t.Errorf("Check() lists %q, which is compiled, in Uncompiled of %q", w, d.Message)
					}
				}
			}
		})
	}
}
