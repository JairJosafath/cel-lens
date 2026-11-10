package check_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// TestPolicyVariableTypes checks the problems reported for variables.<name> at
// every supported Kubernetes version. A variable has the type its expression
// returns, as the API server's composited compiler registers it
// (k8s.io/apiserver/pkg/admission/plugin/cel/composition.go, CompileAndStoreVariable
// and convertCelTypeToDeclType); one that does not compile is dyn there. A name
// that is not yet a field of variables, because the policy defines it later or
// not at all, is the undefined field error of cel-go's checker
// (github.com/google/cel-go/checker/errors.go, undefinedField), which the
// compiler returns unchanged.
func TestPolicyVariableTypes(t *testing.T) {
	t.Parallel()
	const vars = "variables: [{name: replicas, expression: object.spec.replicas}, {name: broken, expression: 'object.spec.replicas +'}, {name: after, expression: variables.replicas}]"
	tests := []struct {
		name, variables, expr string
		want                  []warning
	}{
		{name: "used as its type", variables: vars, expr: "variables.replicas + 1 > 0"},
		{name: "method its type lacks", variables: vars, expr: "variables.replicas.size() > 0", want: []warning{{"apps/v1 Deployment: found no matching overload for 'size' applied to 'int.()'", "size"}}},
		{name: "wrong operand", variables: vars, expr: "variables.replicas == 'a'", want: []warning{{"apps/v1 Deployment: found no matching overload for '_==_' applied to '(int, string)'", "=="}}},
		{name: "name the policy lacks", variables: vars, expr: "variables.nope > 0", want: []warning{{"undefined field 'nope'", "nope"}}},
		{name: "defined later", variables: "variables: [{name: first, expression: variables.second}, {name: second, expression: object.spec.replicas}]", expr: "variables.second > 0", want: []warning{{"undefined field 'second'", "second"}}},
		{name: "variable that does not compile", variables: vars, expr: "variables.broken > 0 && variables.broken.size() > 0"},
		{name: "variable built on another", variables: vars, expr: "variables.after + 1 > 0"},
	}
	for _, v := range kubeversion.Supported() {
		for _, tt := range tests {
			t.Run(v.String()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				doc := policy(deployments, "expression: \""+tt.expr+"\"")
				doc = doc[:len(doc)-len("  validations:\n    - expression: \""+tt.expr+"\"\n")] + "  " + tt.variables + "\n  validations:\n    - expression: \"" + tt.expr + "\"\n"
				diags, err := check.Check(context.Background(), "policy.yaml", []byte(doc), check.WithVersion(v))
				if err != nil {
					t.Fatalf("Check(%s, %s) error: %v", tt.name, v, err)
				}
				var got []warning
				own := 0
				for _, w := range texts(doc, diags) {
					if strings.HasPrefix(w.message, "Syntax error") {
						own++ // the error of the variable that does not compile, once
					} else {
						got = append(got, w)
					}
				}
				if wantOwn := strings.Count(tt.variables, "name: broken"); own != wantOwn {
					t.Errorf("Check(%s, %s) gives %d syntax errors, want %d", tt.name, v, own, wantOwn)
				}
				if !slices.Equal(got, tt.want) {
					t.Errorf("Check(%s, %s) = %v, want %v", tt.name, v, got, tt.want)
				}
			})
		}
	}
}

// TestVariableTypes checks the types check.VariableTypes gives variables.
func TestVariableTypes(t *testing.T) {
	t.Parallel()
	var root map[string]any
	if err := yaml.Unmarshal([]byte(policy(deployments, "expression: 'true'")+`  variables:
    - {name: replicas, expression: object.spec.replicas}
    - {name: groups, expression: request.userInfo.groups}
    - {name: labels, expression: object.metadata.labels}
    - {name: template, expression: object.spec.template}
    - {name: broken, expression: "1 +"}
    - {name: sum, expression: variables.replicas + 1}
`), &root); err != nil {
		t.Fatal(err)
	}
	got, err := check.VariableTypes(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"int", "list(string)", "map(string, string)", "dyn", "dyn", "int"}
	if len(got) != len(want) {
		t.Fatalf("VariableTypes(policy) = %v, want %d variables", got, len(want))
	}
	for i, v := range got {
		if v.Type != want[i] {
			t.Errorf("VariableTypes(policy)[%d] (%s) type = %q, want %q", i, v.Name, v.Type, want[i])
		}
	}
	if got[0].Expression != "object.spec.replicas" {
		t.Errorf("VariableTypes(policy)[0].Expression = %q, want object.spec.replicas", got[0].Expression)
	}
}

// replicaLimitPolicy is the policy of #22: params of a kind no CRD defines,
// so Kubernetes leaves params and every variable that reads it dyn.
const replicaLimitPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: replica-limit
spec:
  paramKind: {apiVersion: rules.example.com/v1, kind: ReplicaLimit}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments]}
  variables:
    - {name: limit, expression: params.maxReplicas}
    - {name: doubled, expression: variables.limit * 2}
    - {name: again, expression: variables.limit}
    - {name: team, expression: params.team}
    - {name: whole, expression: params}
    - {name: replicas, expression: object.spec.replicas}
    - {name: named, expression: params.maxReplicas}
    - {name: fromNamed, expression: variables.named}
  validations:
    - expression: VALIDATION
`

// replicaLimitTypes are the .cellens entries of #22: params typed by a
// schema, and variables.named by a type of its own.
var replicaLimitTypes = []check.AssumedType{
	{
		Kind: "ValidatingAdmissionPolicy", Name: "replica-limit", Variable: "params",
		Type: "{maxReplicas: int, team: string}", Source: ".cellens", Line: 2,
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"maxReplicas": map[string]any{"type": "integer", "minimum": 0},
			"team":        map[string]any{"type": "string"},
		}},
	},
	{
		Kind: "ValidatingAdmissionPolicy", Name: "replica-limit", Variable: "variables.named",
		Type: "string", Source: ".cellens", Line: 12, Schema: map[string]any{"type": "string"},
	},
}

// TestVariableTypesWithAssumedTypes is #22: with params typed by .cellens,
// each variable has the type its expression returns with that type, in
// order, a later variable seeing earlier ones, as the API server's
// composition types it (CompileAndStoreVariable); a variable whose
// expression still returns dyn stays dyn, a variable an entry names keeps the
// API server's type for that entry to replace, and a variable Kubernetes
// types keeps its type.
func TestVariableTypesWithAssumedTypes(t *testing.T) {
	t.Parallel()
	var root map[string]any
	if err := yaml.Unmarshal([]byte(strings.Replace(replicaLimitPolicy, "VALIDATION", "'true'", 1)), &root); err != nil {
		t.Fatal(err)
	}
	params, named := replicaLimitTypes[0], replicaLimitTypes[1]
	want := []struct {
		typ, dyn string
		assumed  []check.AssumedType
	}{
		{"int", "", []check.AssumedType{params}},
		{"int", "", nil}, // dyn * int is int to the API server too
		{"int", "", []check.AssumedType{params}},
		{"string", "", []check.AssumedType{params}},
		{"dyn", check.DynResult, []check.AssumedType{params}}, // an object, as the API server makes it dyn
		{"int", "", nil},
		{"dyn", check.DynInput, nil}, // its entry gives its type
		{"string", "", []check.AssumedType{named}},
	}
	got, err := check.VariableTypes(t.Context(), root, check.WithAssumedTypes(replicaLimitTypes))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("VariableTypes(#22 policy) = %+v, want %d variables", got, len(want))
	}
	origins := func(types []check.AssumedType) []string {
		var out []string
		for _, ty := range types {
			out = append(out, ty.Variable+"@"+ty.Source+":"+strconv.Itoa(ty.Line))
		}
		return out
	}
	for i, v := range got {
		if w := want[i]; v.Type != w.typ || v.Dyn != w.dyn || !slices.Equal(origins(v.Assumed), origins(w.assumed)) {
			t.Errorf("VariableTypes(#22 policy)[%d] (%s) = %s, dyn %q, assumed %v; want %s, dyn %q, assumed %v",
				i, v.Name, v.Type, v.Dyn, origins(v.Assumed), w.typ, w.dyn, origins(w.assumed))
		}
	}
	if s := got[0].Schema; s["type"] != "integer" {
		t.Errorf("VariableTypes(#22 policy)[0].Schema = %v, want an integer", s)
	}
	without, err := check.VariableTypes(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if without[0].Type != "dyn" || without[0].Dyn != check.DynInput || len(without[0].Assumed) != 0 {
		t.Errorf("VariableTypes(#22 policy) without .cellens: limit = %+v, want dyn as the API server types it", without[0])
	}
}

// TestCheckWithVariableTypedFromAssumed checks that the checks of #22's
// policy type its variables with params as .cellens types it: a validation
// that uses variables.limit as the int it is has no problem, and one that
// uses it as a string gets the warning only that type shows, naming the
// entries of the values the validation reads.
func TestCheckWithVariableTypedFromAssumed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ validation, want string }{
		{"object.spec.replicas <= variables.limit", ""},
		{"object.spec.replicas <= variables.doubled", ""},
		{"variables.limit == 'many'", "apps/v1 Deployment: found no matching overload for '_==_' applied to '(int, string)' (the type comes from .cellens:2: the API server would not report this)"},
		// The review of #22: params in a string, or as a macro's variable, is
		// no read of params, so its entry is not named.
		{"variables.named != 'params' && [1].all(params, params > 0) && variables.named > 1", "apps/v1 Deployment: found no matching overload for '_>_' applied to '(string, int)' (the type comes from .cellens:12: the API server would not report this)"},
	} {
		doc := strings.Replace(replicaLimitPolicy, "VALIDATION", strconv.Quote(tc.validation), 1)
		diags, err := check.Check(t.Context(), "policy.yaml", []byte(doc), check.WithAssumedTypes(replicaLimitTypes))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range diags {
			got = append(got, d.Message)
		}
		if want := slices.DeleteFunc([]string{tc.want}, func(s string) bool { return s == "" }); !slices.Equal(got, want) {
			t.Errorf("Check(%q) = %q, want %q", tc.validation, got, want)
		}
	}
}

// TestCheckVariableWrongOnlyWithAssumed is the review of #22: a variable
// whose expression fails only with the types .cellens assumes, as
// params.maxReplicaz with params typed, gets the warning only that type
// shows, as a validation does, so its dyn is explained where it is written;
// without the types it has none. Its reason names those types, in a
// MutatingAdmissionPolicy too, which no check types with them (#27).
func TestCheckVariableWrongOnlyWithAssumed(t *testing.T) {
	t.Parallel()
	doc := strings.Replace(strings.Replace(replicaLimitPolicy, "VALIDATION", strconv.Quote("object.spec.replicas <= variables.limit"), 1),
		"{name: limit, expression: params.maxReplicas}", "{name: limit, expression: params.maxReplicaz}", 1)
	want := []string{"apps/v1 Deployment: undefined field 'maxReplicaz' (the type comes from .cellens:2: the API server would not report this)"}
	for _, tc := range []struct {
		name  string
		opts  []check.Option
		wants []string
	}{
		{"with the types", []check.Option{check.WithAssumedTypes(replicaLimitTypes)}, want},
		{"without them", nil, nil},
	} {
		diags, err := check.Check(t.Context(), "policy.yaml", []byte(doc), tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range diags {
			if d.Start.Line != 11 {
				t.Errorf("Check(%s) diagnostic %+v, want it on line 11, the variable's", tc.name, d)
			}
			got = append(got, d.Message)
		}
		if !slices.Equal(got, tc.wants) {
			t.Errorf("Check(%s) = %q, want %q", tc.name, got, tc.wants)
		}
	}
	// #27: in both, the reason names the types the failure comes from.
	for _, tc := range []struct{ kind, dyn string }{
		{"ValidatingAdmissionPolicy", check.DynAssumedUncompiled},
		{"MutatingAdmissionPolicy", check.DynAssumedUncompiled},
	} {
		var root map[string]any
		if err := yaml.Unmarshal([]byte(strings.Replace(doc, "kind: ValidatingAdmissionPolicy", "kind: "+tc.kind, 1)), &root); err != nil {
			t.Fatal(err)
		}
		types := slices.Clone(replicaLimitTypes)
		for i := range types {
			types[i].Kind = tc.kind
		}
		got, err := check.VariableTypes(t.Context(), root, check.WithAssumedTypes(types))
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Type != "dyn" || got[0].Dyn != tc.dyn || len(got[0].Assumed) != 1 || got[0].Assumed[0].Variable != "params" {
			t.Errorf("VariableTypes(%s)[0] = %s, dyn %q, assumed %v, want dyn %q from params", tc.kind, got[0].Type, got[0].Dyn, got[0].Assumed, tc.dyn)
		}
	}
}

// variablePolicy is a policy of KIND, with params of a kind no CRD defines,
// that matches deployments and RESOURCES, with the variable VARIABLE.
const variablePolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: KIND
metadata:
  name: replicas
spec:
  paramKind: {apiVersion: rules.example.com/v1, kind: ReplicaLimit}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE], resources: [deployments]}
      RESOURCES
  variables:
    - name: replicas
      expression: VARIABLE
`

// untypedCRD is a CustomResourceDefinition whose schema gives its kind no
// CEL type.
const untypedCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: loose.example.com}
spec:
  group: example.com
  names: {plural: loose, singular: loose, kind: Loose}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema: {x-kubernetes-preserve-unknown-fields: true}
`

// TestVariableProblemsShown is #27: every policy variable the engine types
// as one that does not compile gets a warning at its expression that says
// why, labelled as the engine's own check, which the API server would not
// report: one that fails with the type of the kind matched, which the API
// server's type checker skips, in a ValidatingAdmissionPolicy and in a
// MutatingAdmissionPolicy, whose variables the engine types with the kinds
// too; also beside a matched kind without a type. One that does not compile
// at all keeps the API server's error alone.
func TestVariableProblemsShown(t *testing.T) {
	t.Parallel()
	const own = "apps/v1 Deployment: undefined field 'replicaz' (cel-lens checks a variable with the type of the kind matched, which the API server's type check skips: the API server would not report this)"
	crds := check.WithCRDs(loadCRDs(t, map[string]string{"loose.yaml": untypedCRD}))
	for _, tc := range []struct {
		name, kind, resources, variable string
		want                            []string
	}{
		{"validating", "ValidatingAdmissionPolicy", "", "object.spec.replicaz", []string{"warning " + own}},
		{"mutating", "MutatingAdmissionPolicy", "", "object.spec.replicaz", []string{"warning " + own}},
		{"beside a kind without a type", "ValidatingAdmissionPolicy", "- {apiGroups: [example.com], apiVersions: [v1], operations: [CREATE], resources: [loose]}", "object.spec.replicaz", []string{"warning " + own}},
		{"compiles", "ValidatingAdmissionPolicy", "", "object.spec.replicas", nil},
		{"no compile at all", "ValidatingAdmissionPolicy", "", "object.spec.replicas +", []string{"error "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := strings.NewReplacer("KIND", tc.kind, "RESOURCES", tc.resources, "VARIABLE", strconv.Quote(tc.variable)).Replace(variablePolicy)
			diags, err := check.Check(t.Context(), "policy.yaml", []byte(doc), allOn(), crds)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, d := range diags {
				if d.Start.Line != 13 {
					t.Errorf("Check(%s) diagnostic %+v, want it on line 13, the variable's", tc.name, d)
				}
				got = append(got, string(d.Severity)+" "+d.Message)
			}
			if len(got) != len(tc.want) || len(got) > 0 && !strings.HasPrefix(got[0], tc.want[0]) {
				t.Errorf("Check(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
