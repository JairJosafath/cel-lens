package check_test

import (
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// configCRD is a CustomResourceDefinition whose spec.config is dyn to the
// API server: x-kubernetes-int-or-string gives it no type, so a rule may read
// anything from it. Its spec.open keeps unknown fields, which no rule may read.
// RULE is replaced by the rule under test.
const configCRD = `apiVersion: apiextensions.k8s.io/v1
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
          x-kubernetes-validations:
            - rule: "RULE"
          properties:
            spec:
              type: object
              properties:
                config:
                  x-kubernetes-int-or-string: true
                open:
                  x-kubernetes-preserve-unknown-fields: true
                name:
                  type: string
`

// configType is the type a .cellens entry gives the value at path: int, one
// of the two types the API server lets an x-kubernetes-int-or-string value
// have.
func configType(path ...string) check.AssumedType {
	return check.AssumedType{
		Kind: "CustomResourceDefinition", Name: "widgets.example.com", Version: "v1",
		Path: path, Type: "int", Source: ".cellens", Line: 3,
		Schema: map[string]any{"type": "integer"},
	}
}

func crdWith(rule string) []byte { return []byte(strings.Replace(configCRD, "RULE", rule, 1)) }

const assumedNote = " (the type comes from .cellens:3: the API server would not report this)"

// TestAssumedCRDType is the first acceptance criterion of #2: with
// spec.config of v1, an x-kubernetes-int-or-string value, typed int from
// .cellens, a rule that uses it right has no problem, and one that uses it as
// something else gets a warning naming the problem and .cellens with its line.
func TestAssumedCRDType(t *testing.T) {
	t.Parallel()
	assumed := check.WithAssumedTypes([]check.AssumedType{configType("spec", "config")})
	for _, tc := range []struct {
		name, rule string
		want       []string // the messages, in order
		severity   check.Severity
	}{
		{name: "right use", rule: "self.spec.config > 0"},
		{
			name: "a field of an int", rule: "self.spec.config.nosuch == 1",
			want:     []string{"type 'int' does not support field selection" + assumedNote},
			severity: check.SeverityWarning,
		},
		{
			name: "wrong type", rule: "self.spec.config.startsWith('a')",
			want:     []string{"found no matching overload for 'startsWith' applied to 'int.(string)'" + assumedNote},
			severity: check.SeverityWarning,
		},
		{
			// The API server's own error stays an error, and the assumed
			// type adds nothing where the API server already reports.
			name: "the API server's own error", rule: "self.spec.nosuch == 1",
			want:     []string{"undefined field 'nosuch'"},
			severity: check.SeverityError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(t.Context(), "crd.yaml", crdWith(tc.rule), assumed)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, d := range diags {
				got = append(got, d.Message)
				if d.Severity != tc.severity {
					t.Errorf("Check(%q) severity of %q = %s, want %s", tc.rule, d.Message, d.Severity, tc.severity)
				}
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("Check(%q) = %q, want %q", tc.rule, got, tc.want)
			}
		})
	}
}

// TestAssumedCRDTypeKeepsPositions checks that the warning a .cellens type
// brings lies where the error of a typed schema would.
func TestAssumedCRDTypeKeepsPositions(t *testing.T) {
	t.Parallel()
	data := crdWith("self.spec.config.nosuch == 1")
	diags, err := check.Check(t.Context(), "crd.yaml", data, check.WithAssumedTypes([]check.AssumedType{configType("spec", "config")}))
	if err != nil {
		t.Fatal(err)
	}
	row, col := 0, 0
	for i, line := range strings.Split(string(data), "\n") {
		if c := strings.Index(line, "nosuch"); c >= 0 {
			row, col = i+1, c+1
		}
	}
	if len(diags) != 1 || diags[0].Start.Line != row || diags[0].Start.Column != col || diags[0].End.Line != row || diags[0].End.Column != col+len("nosuch") {
		t.Errorf("Check() = %+v, want one diagnostic at %d:%d-%d", diags, row, col, col+len("nosuch"))
	}
}

// TestAssumedCRDTypeOnUnreadableValue checks that in a CRD an entry on a value
// that keeps unknown fields does not apply: the API server lets no rule read
// it, so its "undefined field" stays the one problem, an error, and the entry
// is a problem that says so (#2 review).
func TestAssumedCRDTypeOnUnreadableValue(t *testing.T) {
	t.Parallel()
	types := check.WithAssumedTypes([]check.AssumedType{configType("spec", "open")})
	diags, err := check.Check(t.Context(), "crd.yaml", crdWith("self.spec.open > 0"), types)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 1 || diags[0].Severity != check.SeverityError || diags[0].Message != "undefined field 'open'" {
		t.Errorf("Check() = %+v, want only the API server's error undefined field 'open'", diags)
	}
	problems, err := check.AssumedTypeProblems(t.Context(), crdWith("true"), types)
	if err != nil {
		t.Fatal(err)
	}
	const want = "the API server lets no rule read spec.open, which keeps unknown fields: give it a schema in the CustomResourceDefinition instead of this entry"
	if len(problems) != 1 || problems[0].Message != want {
		t.Errorf("AssumedTypeProblems() = %+v, want %q", problems, want)
	}
}

// TestAssumedTypeProblems checks that an entry applies only to the document
// and value it names, and only where the value is dyn: every other entry is a
// problem that says why.
func TestAssumedTypeProblems(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*check.AssumedType)
		want string // the problem, "" for none
	}{
		{name: "dyn value", edit: func(*check.AssumedType) {}},
		{name: "typed value", edit: func(a *check.AssumedType) { a.Path = []string{"spec", "name"} }, want: "spec.name is already typed as string: remove this entry"},
		{name: "typed root", edit: func(a *check.AssumedType) { a.Path = nil }, want: "the root is already typed as object: remove this entry"},
		{name: "no such value", edit: func(a *check.AssumedType) { a.Path = []string{"spec", "nosuch"} }, want: "spec has no field nosuch"},
		{name: "below an int-or-string value", edit: func(a *check.AssumedType) { a.Path = []string{"spec", "config", "x"} }, want: "spec.config is dyn, so nothing below it is known: write the entry for spec.config"},
		{name: "no such version", edit: func(a *check.AssumedType) { a.Version = "v2" }, want: "CustomResourceDefinition widgets.example.com has no version v2 with a schema: name one of v1"},
		{name: "no such document", edit: func(a *check.AssumedType) { a.Name = "gadgets.example.com" }, want: "the file has no document CustomResourceDefinition gadgets.example.com"},
		{name: "a kind not read", edit: func(a *check.AssumedType) { a.Kind = "ConfigMap" }, want: "the file has no document ConfigMap widgets.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			at := configType("spec", "config")
			tc.edit(&at)
			got, err := check.AssumedTypeProblems(t.Context(), crdWith("true"), check.WithAssumedTypes([]check.AssumedType{at}))
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.want == "" && len(got) != 0:
				t.Errorf("AssumedTypeProblems() = %+v, want none", got)
			case tc.want != "" && (len(got) != 1 || got[0].Index != 0 || got[0].Message != tc.want):
				t.Errorf("AssumedTypeProblems() = %+v, want %q at index 0", got, tc.want)
			}
		})
	}
}

// TestAssumedTypeOnTypedValueIgnored checks that Kubernetes's type wins over
// an entry on a value it types: the check is the API server's.
func TestAssumedTypeOnTypedValueIgnored(t *testing.T) {
	t.Parallel()
	at := configType("spec", "name")
	diags, err := check.Check(t.Context(), "crd.yaml", crdWith("self.spec.name.size() > 0"), check.WithAssumedTypes([]check.AssumedType{at}))
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Errorf("Check() = %+v, want none: spec.name stays a string", diags)
	}
}

// limitsPolicy is a ValidatingAdmissionPolicy whose params, object, oldObject
// and variables.cfg are dyn: the paramKind and the matched resource are
// custom kinds with no CustomResourceDefinition. EXPR is replaced by the
// validation under test, RESOURCE by the resource matched.
const limitsPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits
spec:
  paramKind: {apiVersion: example.com/v1, kind: Limit}
  matchConstraints:
    resourceRules:
      - {apiGroups: [RESOURCE], apiVersions: [v1], resources: [widgets]}
  variables:
    - name: cfg
      expression: "object.spec.config"
    - name: count
      expression: "1"
  validations:
    - expression: "EXPR"
`

func policyWith(expr, group string) []byte {
	return []byte(strings.NewReplacer("EXPR", expr, "RESOURCE", group).Replace(limitsPolicy))
}

// policyType is a type a .cellens entry gives a variable of limitsPolicy.
func policyType(variable string, line int, path ...string) check.AssumedType {
	return check.AssumedType{
		Kind: "ValidatingAdmissionPolicy", Name: "limits", Variable: variable, Path: path,
		Type: "{spec: {max: int}}", Source: "policies/.cellens", Line: line,
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"spec": map[string]any{"type": "object", "properties": map[string]any{"max": map[string]any{"type": "integer"}}},
		}},
	}
}

// TestAssumedPolicyTypes is the second acceptance criterion of #2: params,
// object, oldObject and variables.<name> of a ValidatingAdmissionPolicy typed
// from .cellens get the warnings of a typed value.
func TestAssumedPolicyTypes(t *testing.T) {
	t.Parallel()
	types := check.WithAssumedTypes([]check.AssumedType{
		policyType("params", 2), policyType("object", 5), policyType("oldObject", 8), policyType("variables.cfg", 11),
	})
	for _, tc := range []struct {
		name, expr string
		want       string // the one warning, "" for none
	}{
		{"params right", "params.spec.max > 0", ""},
		{"params wrong", "params.spec.nosuch == 1", "undefined field 'nosuch' (the type comes from policies/.cellens:2: the API server would not report this)"},
		{"object right", "object.spec.max > 0", ""},
		{"object metadata", "object.metadata.name != ''", ""},
		{"object wrong", "object.spec.nosuch == 1", "undefined field 'nosuch' (the type comes from policies/.cellens:5: the API server would not report this)"},
		{"oldObject wrong", "oldObject.spec.nosuch == 1", "undefined field 'nosuch' (the type comes from policies/.cellens:8: the API server would not report this)"},
		{"variable right", "variables.cfg.spec.max > 0", ""},
		{"variable wrong", "variables.cfg.nosuch == 1", "undefined field 'nosuch' (the type comes from policies/.cellens:11: the API server would not report this)"},
		{"variable typed by its expression", "variables.count > 0", ""},
		{"wrong type", "params.spec.max == 'a'", "found no matching overload for '_==_' applied to '(int, string)' (the type comes from policies/.cellens:2: the API server would not report this)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(t.Context(), "policy.yaml", policyWith(tc.expr, "example.com"), types)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.want == "" && len(diags) != 0:
				t.Errorf("Check(%q) = %+v, want none", tc.expr, diags)
			case tc.want != "" && (len(diags) != 1 || diags[0].Message != tc.want || diags[0].Severity != check.SeverityWarning):
				t.Errorf("Check(%q) = %+v, want one warning %q", tc.expr, diags, tc.want)
			}
		})
	}
}

// TestAssumedPolicyTypesWithoutThem checks that the expressions of
// TestAssumedPolicyTypes have no problem without the types: every warning
// there comes from .cellens.
func TestAssumedPolicyTypesWithoutThem(t *testing.T) {
	t.Parallel()
	for _, expr := range []string{"params.spec.nosuch == 1", "object.spec.nosuch == 1", "variables.cfg.nosuch == 1"} {
		diags, err := check.Check(t.Context(), "policy.yaml", policyWith(expr, "example.com"))
		if err != nil {
			t.Fatal(err)
		}
		if len(diags) != 0 {
			t.Errorf("Check(%q) without assumed types = %+v, want none", expr, diags)
		}
	}
}

// TestAssumedPolicyTypeWithMatchedKind checks that with a matched built-in
// kind, a warning from an assumed params names the kind, as the API server's
// type check does, and that an entry for the typed object does not apply.
func TestAssumedPolicyTypeWithMatchedKind(t *testing.T) {
	t.Parallel()
	data := []byte(strings.Replace(string(policyWith("params.spec.nosuch == 1 && object.spec.nosuch == 1", "apps")), "widgets", "deployments", 1))
	types := []check.AssumedType{policyType("params", 2), policyType("object", 5)}
	diags, err := check.Check(t.Context(), "policy.yaml", data, check.WithAssumedTypes(types))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"warning apps/v1 Deployment: undefined field 'nosuch' (the type comes from policies/.cellens:2: the API server would not report this)",
		"warning apps/v1 Deployment: undefined field 'nosuch'",
	}
	var got []string
	for _, d := range diags {
		got = append(got, string(d.Severity)+" "+d.Message)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Check() = %q, want %q: the API server's warning on object and the assumed type's on params", got, want)
	}
	problems, err := check.AssumedTypeProblems(t.Context(), data, check.WithAssumedTypes(types))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Index != 1 || problems[0].Message != "object is already typed as apps/v1 Deployment: remove this entry" {
		t.Errorf("AssumedTypeProblems() = %+v, want object already typed as apps/v1 Deployment", problems)
	}
}

// TestAssumedPolicyTypeProblems checks the problems of entries for a policy.
func TestAssumedPolicyTypeProblems(t *testing.T) {
	t.Parallel()
	noParams := strings.Replace(string(policyWith("true", "example.com")), "  paramKind: {apiVersion: example.com/v1, kind: Limit}\n", "", 1)
	for _, tc := range []struct {
		name string
		data string
		typ  check.AssumedType
		want string
	}{
		{"dyn params", string(policyWith("true", "example.com")), policyType("params", 2), ""},
		{"no paramKind", noParams, policyType("params", 2), "ValidatingAdmissionPolicy limits has no spec.paramKind, so it has no params"},
		{"below dyn params", string(policyWith("true", "example.com")), policyType("params", 2, "spec"), "params is dyn, so nothing below it is known: write the entry for params"},
		{"no such variable", string(policyWith("true", "example.com")), policyType("variables.nosuch", 2), "ValidatingAdmissionPolicy limits has no variable nosuch in spec.variables"},
		{"typed variable", string(policyWith("true", "example.com")), policyType("variables.count", 2), "variables.count is already typed as int: remove this entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := check.AssumedTypeProblems(t.Context(), []byte(tc.data), check.WithAssumedTypes([]check.AssumedType{tc.typ}))
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.want == "" && len(got) != 0:
				t.Errorf("AssumedTypeProblems() = %+v, want none", got)
			case tc.want != "" && (len(got) != 1 || got[0].Message != tc.want):
				t.Errorf("AssumedTypeProblems() = %+v, want %q", got, tc.want)
			}
		})
	}
}
