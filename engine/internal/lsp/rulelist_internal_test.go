package lsp

import (
	"cmp"
	"context"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

const listURI = "file:///repo/rules.yaml"

// listCRD is a v1 CustomResourceDefinition of widgets with a rule at the
// root of spec, one with a messageExpression on each port, and one in a
// second version.
const listCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
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
              x-kubernetes-validations:
                - rule: self.size <= 10
                  message: at most 10
              properties:
                size: {type: integer}
                ports:
                  type: array
                  items:
                    type: object
                    properties:
                      name: {type: string}
                    x-kubernetes-validations:
                      - rule: self.name.size() <= 3
                        messageExpression: "'port ' + self.name + ' is too long'"
    - name: v2
      served: true
      storage: false
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: has(self.spec)
`

const listVAP = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE], resources: [deployments]}
  matchConditions:
    - name: not-system
      expression: "!object.metadata.namespace.startsWith('kube-')"
  variables:
    - name: replicas
      expression: object.spec.replicas
  validations:
    - expression: variables.replicas <= 5
      message: at most 5 replicas
    - expression: variables.replicas >= 1
      messageExpression: "'too few: ' + string(variables.replicas)"
  auditAnnotations:
    - key: high-replicas
      valueExpression: "'replicas ' + string(variables.replicas)"
`

const listMAP = `apiVersion: admissionregistration.k8s.io/v1beta1
kind: MutatingAdmissionPolicy
metadata:
  name: add-label
spec:
  matchConditions:
    - name: has-labels
      expression: has(object.metadata.labels)
  variables:
    - name: team
      expression: "'platform'"
  mutations:
    - patchType: ApplyConfiguration
      applyConfiguration:
        expression: "Object{metadata: Object.metadata{labels: {'team': variables.team}}}"
`

const listKyverno = `apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: require-team
spec:
  validations:
    - expression: has(object.metadata.labels.team)
      message: a team label is required
`

// listed lists text as the rule index lists a file, with Kyverno's
// documents when kyverno is true, and fails the test on an error.
func listed(t *testing.T, text string, kyverno bool) []RuleDocument {
	t.Helper()
	docs, err := listRules(context.Background(), listURI, []byte(text), kyverno)
	if err != nil {
		t.Fatalf("listRules() error = %v", err)
	}
	return withCompile(docs, nil)
}

// documentNames are the documents of docs, as they name themselves.
func documentNames(docs []RuleDocument) []string {
	var out []string
	for _, d := range docs {
		out = append(out, d.Document)
	}
	return out
}

// TestRuleListKinds checks that the rule index lists the documents
// pkg/check checks, and no more (cel-lens-core#35): a v1 CRD, a
// ValidatingAdmissionPolicy and a MutatingAdmissionPolicy of
// admissionregistration.k8s.io in any version, and a Kyverno
// ValidatingPolicy of policies.kyverno.io/v1 only with kyverno on and in a
// mode the check checks.
func TestRuleListKinds(t *testing.T) {
	t.Parallel()
	vapBeta := strings.Replace(listVAP, "admissionregistration.k8s.io/v1", "admissionregistration.k8s.io/v1beta1", 1)
	tests := []struct {
		name    string
		text    string
		kyverno bool
		want    []string
	}{
		{"v1 CRD", listCRD, false, []string{"CustomResourceDefinition widgets.example.com"}},
		{"v1beta1 CRD", strings.Replace(listCRD, "apiextensions.k8s.io/v1", "apiextensions.k8s.io/v1beta1", 1) + "    validation:\n      openAPIV3Schema:\n        x-kubernetes-validations:\n          - rule: self.a > 0\n", false, nil},
		{"v1 CRD's legacy spec.validation only", "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: a.b}\nspec:\n  validation:\n    openAPIV3Schema:\n      x-kubernetes-validations:\n        - rule: self.a > 0\n", false, nil},
		{"CRD with no CEL", "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: plain.example.com}\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema: {type: object}\n", false, nil},
		{"ValidatingAdmissionPolicy v1", listVAP, false, []string{"ValidatingAdmissionPolicy limits"}},
		{"ValidatingAdmissionPolicy v1beta1", vapBeta, false, []string{"ValidatingAdmissionPolicy limits"}},
		{"ValidatingAdmissionPolicy with no CEL", "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: empty}\nspec:\n  failurePolicy: Fail\n", false, nil},
		{"MutatingAdmissionPolicy", listMAP, false, []string{"MutatingAdmissionPolicy add-label"}},
		{"ValidatingAdmissionPolicyBinding", "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicyBinding\nmetadata: {name: b}\nspec:\n  policyName: limits\n  matchResources:\n    matchPolicy: Equivalent\n", false, nil},
		{"Kyverno ValidatingPolicy, kyverno on", listKyverno, true, []string{"ValidatingPolicy require-team"}},
		{"Kyverno ValidatingPolicy, kyverno off", listKyverno, false, nil},
		{"Kyverno ValidatingPolicy in JSON mode", strings.Replace(listKyverno, "spec:\n", "spec:\n  evaluation: {mode: JSON}\n", 1), true, []string{"ValidatingPolicy require-team"}},
		{"Kyverno ValidatingPolicy in Kubernetes mode", strings.Replace(listKyverno, "spec:\n", "spec:\n  evaluation: {mode: Kubernetes}\n", 1), true, []string{"ValidatingPolicy require-team"}},
		{"Kyverno ValidatingPolicy in Envoy mode", strings.Replace(listKyverno, "spec:\n", "spec:\n  evaluation: {mode: Envoy}\n", 1), true, nil},
		{"Kyverno ValidatingPolicy of v1alpha1", strings.Replace(listKyverno, "policies.kyverno.io/v1", "policies.kyverno.io/v1alpha1", 1), true, nil},
		{"Kyverno ClusterPolicy", "apiVersion: kyverno.io/v1\nkind: ClusterPolicy\nmetadata: {name: c}\nspec:\n  rules:\n    - name: r\n      validate:\n        cel:\n          expressions:\n            - expression: object.a > 0\n", true, nil},
		{"Kyverno MutatingPolicy", "apiVersion: policies.kyverno.io/v1\nkind: MutatingPolicy\nmetadata: {name: m}\nspec:\n  matchConditions:\n    - name: a\n      expression: has(object.a)\n", true, nil},
		{"a ConfigMap that names the kinds", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: CustomResourceDefinition}\ndata:\n  kind: ValidatingAdmissionPolicy\n", true, nil},
		{"every kind in one file", listCRD + "---\n" + listVAP + "---\n" + listMAP + "---\n" + listKyverno, true, []string{
			"CustomResourceDefinition widgets.example.com", "ValidatingAdmissionPolicy limits", "MutatingAdmissionPolicy add-label", "ValidatingPolicy require-team",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := documentNames(listed(t, tt.text, tt.kyverno)); !slices.Equal(got, tt.want) {
				t.Errorf("listRules(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// ruleName is a rule as cel-lens.exampleResults names it, with its id and
// its label.
type ruleName struct {
	id, document, version, path, field, name string
	index                                    int
	label, labelFrom                         string
}

func ruleNames(docs []RuleDocument) []ruleName {
	var out []ruleName
	for _, d := range docs {
		for _, r := range d.Rules {
			n := ruleName{id: r.ID, document: r.Document, field: r.Field, name: r.Name, index: r.Index, label: r.Label, labelFrom: r.LabelFrom}
			if r.Version != nil {
				n.version = *r.Version
			}
			if r.Path != nil {
				n.path = *r.Path
			}
			out = append(out, n)
		}
	}
	return out
}

// TestRuleListNames checks that each rule is one entry of a list holding
// CEL, named as cel-lens.exampleResults names it, with its id and its label
// (cel-lens-core#35): a CRD's rule and messageExpression are one rule, a
// messageExpression never labels, and a second document of the same kind
// and name gets #2 after its name in the ids.
func TestRuleListNames(t *testing.T) {
	t.Parallel()
	const crd, vap, mp, vp = "CustomResourceDefinition widgets.example.com", "ValidatingAdmissionPolicy limits", "MutatingAdmissionPolicy add-label", "ValidatingPolicy require-team"
	id := func(parts ...string) string { return listURI + "#" + strings.Join(parts, "#") }
	got := ruleNames(listed(t, listCRD+"---\n"+listVAP+"---\n"+listMAP+"---\n"+listKyverno+"---\n"+listKyverno, true))
	want := []ruleName{
		{id(crd, "v1", "spec", "0"), crd, "v1", "spec", "", "", 0, "at most 10", "message"},
		{id(crd, "v1", "spec.ports[]", "0"), crd, "v1", "spec.ports[]", "", "", 0, "self.name.size() <= 3", "expression"},
		{id(crd, "v2", "", "0"), crd, "v2", "", "", "", 0, "has(self.spec)", "expression"},
		{id(vap, "matchCondition", "not-system", "0"), vap, "", "", "matchCondition", "not-system", 0, "not-system", "name"},
		{id(vap, "variable", "replicas", "0"), vap, "", "", "variable", "replicas", 0, "replicas", "name"},
		{id(vap, "validation", "", "0"), vap, "", "", "validation", "", 0, "at most 5 replicas", "message"},
		{id(vap, "validation", "", "1"), vap, "", "", "validation", "", 1, "variables.replicas >= 1", "expression"},
		{id(vap, "auditAnnotation", "high-replicas", "0"), vap, "", "", "auditAnnotation", "high-replicas", 0, "high-replicas", "key"},
		{id(mp, "matchCondition", "has-labels", "0"), mp, "", "", "matchCondition", "has-labels", 0, "has-labels", "name"},
		{id(mp, "variable", "team", "0"), mp, "", "", "variable", "team", 0, "team", "name"},
		{id(mp, "mutation", "", "0"), mp, "", "", "mutation", "", 0, "Object{metadata: Object.metadata{labels: {'team': variables…", "expression"},
		{id(vp, "validation", "", "0"), vp, "", "", "validation", "", 0, "a team label is required", "message"},
		{id(vp+"#2", "validation", "", "0"), vp, "", "", "validation", "", 0, "a team label is required", "message"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listRules() rules =\n%s\nwant\n%s", dumpNames(got), dumpNames(want))
	}
}

func dumpNames(names []ruleName) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString("  ")
		b.WriteString(strings.Join([]string{n.id, n.document, n.version, n.path, n.field, n.name, strconv.Itoa(n.index), n.label, n.labelFrom}, " | "))
		b.WriteString("\n")
	}
	return b.String()
}

// TestRuleListIDsStay checks that the id of a rule does not change when its
// text changes, nor when a document or a rule is added above it in another
// document (cel-lens-core#35): it names the rule by its document's kind and
// name and its place there, not by its line or text.
func TestRuleListIDsStay(t *testing.T) {
	t.Parallel()
	ids := func(text string) map[string]string {
		out := map[string]string{}
		for _, d := range listed(t, text, true) {
			for _, r := range d.Rules {
				out[r.Label] = r.ID
			}
		}
		return out
	}
	base := ids(listCRD + "---\n" + listVAP)
	if len(base) != 8 {
		t.Fatalf("listRules() = %d rules, want 8", len(base))
	}
	other := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: other}\nspec:\n  validations:\n    - expression: object.a > 0\n      message: other rule\n"
	for _, tc := range []struct {
		name, text string
		// renamed are the labels that changed with the text, the new by the old.
		renamed map[string]string
		// moved is the document whose rules moved, whose ids are not compared.
		moved string
	}{
		{"text of a CRD rule changed", strings.Replace(listCRD, "self.size <= 10", "self.size <= 12 && self.size >= 0", 1) + "---\n" + listVAP, nil, ""},
		{"text of a validation changed", listCRD + "---\n" + strings.Replace(listVAP, "variables.replicas >= 1", "variables.replicas >= 2", 1), map[string]string{"variables.replicas >= 1": "variables.replicas >= 2"}, ""},
		{"document added above", other + "---\n" + listCRD + "---\n" + listVAP, nil, ""},
		{"rule added above in another document", strings.Replace(listCRD, "              x-kubernetes-validations:\n                - rule: self.size <= 10", "              x-kubernetes-validations:\n                - rule: self.size >= 0\n                  message: new rule\n                - rule: self.size <= 10", 1) + "---\n" + listVAP, nil, "CustomResourceDefinition"},
		{"rule added above in another file's document", listCRD + "---\n" + strings.Replace(listVAP, "  matchConditions:\n", "  matchConditions:\n    - name: first\n      expression: \"true\"\n", 1), nil, "ValidatingAdmissionPolicy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ids(tc.text)
			for label, id := range base {
				if tc.moved != "" && strings.HasPrefix(id, listURI+"#"+tc.moved) {
					continue // the rules after the one added in their own list move
				}
				if l, ok := tc.renamed[label]; ok {
					label = l
				}
				if got[label] != id {
					t.Errorf("id of %q = %q, want %q as before", label, got[label], id)
				}
			}
		})
	}
}

// TestRuleListPlaces checks where a rule and its document are (#35): the
// location is the expression's range, the span runs from the line of the
// rule's list item to the end of the item, and the document's range from
// its first line to its last.
func TestRuleListPlaces(t *testing.T) {
	t.Parallel()
	text := "# a comment\n---\n" + listVAP + "---\n" + listKyverno
	docs := listed(t, text, false)
	if len(docs) != 1 {
		t.Fatalf("listRules() = %d documents, want 1", len(docs))
	}
	d := docs[0]
	lines := strings.Split(text, "\n")
	at := func(needle string) int {
		return slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, needle) })
	}
	end := func(line int) position { return position{Line: line, Character: len(lines[line])} }
	if want := (lspRange{position{Line: at("apiVersion: admissionregistration"), Character: 0}, end(at("valueExpression"))}); d.Range != want {
		t.Errorf("document range = %+v, want %+v", d.Range, want)
	}
	if d.URI != listURI || d.Kind != "ValidatingAdmissionPolicy" || d.Name != "limits" {
		t.Errorf("document = %s %s %s, want %s ValidatingAdmissionPolicy limits", d.URI, d.Kind, d.Name, listURI)
	}
	for _, tc := range []struct {
		label         string
		item, last    string // the first and last line of the rule's list item
		expr          string // the expression's text on its line
		character     int    // where it starts there
		endCharacters int    // and how long it is
	}{
		{"not-system", "- name: not-system", "expression: \"!object", "!object", len("      expression: \""), len("!object.metadata.namespace.startsWith('kube-')")},
		{"replicas", "- name: replicas", "expression: object.spec.replicas", "object.spec.replicas", len("      expression: "), len("object.spec.replicas")},
		{"at most 5 replicas", "- expression: variables.replicas <= 5", "message: at most 5 replicas", "variables.replicas <= 5", len("    - expression: "), len("variables.replicas <= 5")},
		{"variables.replicas >= 1", "- expression: variables.replicas >= 1", "messageExpression: \"'too few", "variables.replicas >= 1", len("    - expression: "), len("variables.replicas >= 1")},
		{"high-replicas", "- key: high-replicas", "valueExpression", "'replicas ", len("      valueExpression: \""), len("'replicas ' + string(variables.replicas)")},
	} {
		i := slices.IndexFunc(d.Rules, func(r ListedRule) bool { return r.Label == tc.label })
		if i < 0 {
			t.Errorf("no rule labelled %q in %+v", tc.label, d.Rules)
			continue
		}
		r := d.Rules[i]
		line := at(tc.expr)
		if want := (RuleLocation{URI: listURI, Range: lspRange{position{line, tc.character}, position{line, tc.character + tc.endCharacters}}}); r.Location != want {
			t.Errorf("%s: location = %+v, want %+v", tc.label, r.Location, want)
		}
		if want := (lspRange{position{Line: at(tc.item), Character: 0}, end(at(tc.last))}); r.Span != want {
			t.Errorf("%s: span = %+v, want %+v", tc.label, r.Span, want)
		}
	}
}

// TestRuleListSpanEdges checks the span of a list item that is a flow
// mapping, one followed by a comment, and the last of a file without a
// line break at its end.
func TestRuleListSpanEdges(t *testing.T) {
	t.Parallel()
	text := "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: p}\nspec:\n" +
		"  validations: [{expression: a > 0, message: first}, {expression: b > 0}]\n" +
		"  matchConditions:\n    - name: one\n      expression: c > 0\n    # about two\n\n    - name: two\n      expression: |\n        d > 0 &&\n        e > 0"
	docs := listed(t, text, false)
	if len(docs) != 1 {
		t.Fatalf("listRules() = %d documents, want 1", len(docs))
	}
	spans := map[string]lspRange{}
	for _, r := range docs[0].Rules {
		spans[r.Label] = r.Span
	}
	for label, want := range map[string]lspRange{
		"first": {position{4, 0}, position{4, len("  validations: [{expression: a > 0, message: first}")}},
		"b > 0": {position{4, 0}, position{4, len("  validations: [{expression: a > 0, message: first}, {expression: b > 0}]")}},
		"one":   {position{6, 0}, position{7, len("      expression: c > 0")}},
		"two":   {position{10, 0}, position{13, len("        e > 0")}},
	} {
		if spans[label] != want {
			t.Errorf("span of %q = %+v, want %+v", label, spans[label], want)
		}
	}
}

// TestRuleListCompileState checks the compile state of a rule from the
// diagnostics of its file (#35): error when one of severity error starts
// in one of its expressions, a messageExpression included, else warning,
// else ok, with the count and the first three messages; a diagnostic
// elsewhere counts for no rule, but one that kept the check from compiling
// a rule's expressions makes it an error and comes first in its messages.
func TestRuleListCompileState(t *testing.T) {
	t.Parallel()
	found, err := listRules(context.Background(), listURI, []byte(listVAP), false)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(listVAP, "\n")
	pos := func(needle string) extract.Position {
		for i, l := range lines {
			if c := strings.Index(l, needle); c >= 0 {
				return extract.Position{Line: i + 1, Column: c + 1}
			}
		}
		t.Fatalf("%q not in the policy", needle)
		return extract.Position{}
	}
	diag := func(needle, msg string, sev check.Severity) check.Diagnostic {
		return check.Diagnostic{Message: msg, Severity: sev, Start: pos(needle), End: pos(needle)}
	}
	diags := []check.Diagnostic{
		diag("metadata:", "not in a rule", check.SeverityError),
		diag("object.metadata.namespace", "w1", check.SeverityWarning),
		diag("variables.replicas <= 5", "w2", check.SeverityWarning),
		diag("variables.replicas >= 1", "e1", check.SeverityError),
		diag("'too few", "w3", check.SeverityWarning),
		diag("too few: ", "e3", check.SeverityError),
		diag("string(variables.replicas)", "e2", check.SeverityError),
		diag("'replicas ", "w4", check.SeverityWarning),
	}
	// The problem that kept the check from compiling a rule makes it an error
	// and comes first, wherever it is (#35).
	stopper := diag("metadata:", "the schema is not structural", check.SeverityError)
	stopper.Uncompiled = []extract.Position{pos("object.spec.replicas"), pos("'replicas ")}
	diags = append(diags, stopper)
	slices.SortStableFunc(diags, func(a, b check.Diagnostic) int {
		return cmp.Or(a.Start.Line-b.Start.Line, a.Start.Column-b.Start.Column)
	})
	got := map[string]CompileState{}
	for _, r := range withCompile(found, diags)[0].Rules {
		got[r.Label] = r.Compile
	}
	msg := func(m, sev string) CompileMessage { return CompileMessage{Message: m, Severity: sev} }
	want := map[string]CompileState{
		"not-system":              {State: "warning", Problems: 1, Messages: []CompileMessage{msg("w1", "warning")}},
		"replicas":                {State: "error", Problems: 1, Messages: []CompileMessage{msg("the schema is not structural", "error")}},
		"at most 5 replicas":      {State: "warning", Problems: 1, Messages: []CompileMessage{msg("w2", "warning")}},
		"variables.replicas >= 1": {State: "error", Problems: 4, Messages: []CompileMessage{msg("e1", "error"), msg("w3", "warning"), msg("e3", "error")}},
		"high-replicas":           {State: "error", Problems: 2, Messages: []CompileMessage{msg("the schema is not structural", "error"), msg("w4", "warning")}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("compile states = %+v, want %+v", got, want)
	}
}
