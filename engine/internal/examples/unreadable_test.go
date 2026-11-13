package examples

import (
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// TestReadProblems checks what the readers say of a CRD, a policy or a
// binding they leave out: for a field of the wrong type, check's problem at
// the word, as kubectl reads an unquoted n as a boolean; for a CRD the API
// server refuses, what it refuses; and nothing for a document with no name,
// a CRD that serves no version, or a CRD that reads, with y and n in an enum
// (#31).
func TestReadProblems(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  scope: %s
  versions:
    - name: v1
      served: %s
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          %s
`
	crdOf := func(scope, served, extra string) string {
		return strings.Replace(strings.Replace(strings.Replace(crd, "%s", scope, 1), "%s", served, 1), "%s", extra, 1)
	}
	policy := func(spec string) string {
		return "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: p}\nspec:\n" + spec
	}
	binding := func(name, spec string) string {
		return "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicyBinding\nmetadata: {name: " + name + "}\nspec:\n" + spec
	}
	tests := []struct {
		name string
		kind string // crd, policy or binding
		text string
		want []string // line:column: message, one for each problem
	}{
		{"a CRD with a description n", "crd", crdOf("Namespaced", "true", "description: n"), []string{"15:24: spec.versions[0].schema.openAPIV3Schema.description: expected a string, got a boolean: kubectl reads an unquoted n as one (YAML 1.1), so quote it if a string is meant"}},
		{"a CRD with y and n in an enum", "crd", crdOf("Namespaced", "true", "enum: [y, n]"), nil},
		{"a CRD the API server refuses", "crd", crdOf("Everywhere", "true", ""), []string{`1:1: the API server refuses the CustomResourceDefinition: spec.scope: Unsupported value: "Everywhere": supported values: "Cluster", "Namespaced"`}},
		{"a CRD that serves no version", "crd", crdOf("Namespaced", "false", ""), nil},
		{"a policy with an annotation key n", "policy", policy("  auditAnnotations:\n    - key: n\n      valueExpression: \"'x'\"\n"), []string{"6:12: spec.auditAnnotations[0].key: expected a string, got a boolean: kubectl reads an unquoted n as one (YAML 1.1), so quote it if a string is meant"}},
		{"a policy that reads", "policy", policy("  validations: [{expression: \"true\"}]\n"), nil},
		{"a binding with a label no", "binding", binding("b", "  policyName: p\n  matchResources: {namespaceSelector: {matchLabels: {team: no}}}\n"), []string{"6:60: spec.matchResources.namespaceSelector.matchLabels[team]: expected a string, got a boolean: kubectl reads an unquoted no as one (YAML 1.1), so quote it if a string is meant"}},
		{"a binding that names no policy", "binding", binding("b", "  validationActions: [Deny]\n"), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var problems []string
			var read int
			switch tc.kind {
			case "crd":
				crds, ds := ReadWithProblems(t.Context(), "/w/x.yaml", []byte(tc.text), kubeversion.Default())
				read = len(crds)
				for _, d := range ds {
					problems = append(problems, itoa(d.Start.Line)+":"+itoa(d.Start.Column)+": "+d.Message)
				}
			case "policy":
				ps, ds := ReadPoliciesWithProblems(t.Context(), "/w/x.yaml", []byte(tc.text), kubeversion.Default())
				read = len(ps)
				for _, d := range ds {
					problems = append(problems, itoa(d.Start.Line)+":"+itoa(d.Start.Column)+": "+d.Message)
				}
			default:
				bs, ds := ReadBindingsWithProblems(t.Context(), "/w/x.yaml", []byte(tc.text))
				read = len(bs)
				for _, d := range ds {
					problems = append(problems, itoa(d.Start.Line)+":"+itoa(d.Start.Column)+": "+d.Message)
				}
			}
			if strings.Join(problems, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("problems of %s =\n%s\nwant\n%s", tc.name, strings.Join(problems, "\n"), strings.Join(tc.want, "\n"))
			}
			if len(tc.want) > 0 && read != 0 {
				t.Errorf("read %d documents that have a problem, want none", read)
			}
		})
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
