package examples

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// policyFile is the file the policies of a test are in.
const policyFile = "/w/policy.yaml"

// workspace reads the policies of the file policyFile and the documents of
// files, by name below /w, in the order given: the CRDs, their examples,
// checked, and the documents of built-in kinds.
type workspace struct {
	policies []*Policy
	in       PolicyInput
}

func readWorkspace(t *testing.T, policy string, files ...[2]string) workspace {
	t.Helper()
	ctx := t.Context()
	kube := kubeversion.Default()
	ws := workspace{policies: ReadPolicies(ctx, policyFile, []byte(policy), kube)}
	ws.in.Bindings = ReadBindings(ctx, policyFile, []byte(policy))
	for _, f := range files {
		ws.in.CRDs = append(ws.in.CRDs, Read(ctx, "/w/"+f[0], []byte(f[1]), kube)...)
		ws.in.Bindings = append(ws.in.Bindings, ReadBindings(ctx, "/w/"+f[0], []byte(f[1]))...)
	}
	for _, f := range append([][2]string{{"policy.yaml", policy}}, files...) {
		crs, err := Find(ctx, ws.in.CRDs, "/w/"+f[0], f[0], []byte(f[1]))
		if err != nil {
			t.Fatal(err)
		}
		if err := Check(ctx, crs); err != nil {
			t.Fatal(err)
		}
		builtIn, err := FindBuiltIn(ctx, "/w/"+f[0], f[0], []byte(f[1]), func(string, string) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		all := append(slices.Clone(crs), builtIn...)
		slices.SortStableFunc(all, func(a, b *Object) int { return a.Line - b.Line })
		ws.in.Objects = append(ws.in.Objects, all...)
	}
	return ws
}

// run runs the first policy of ws.
func (ws workspace) run(t *testing.T) PolicyRun {
	t.Helper()
	if len(ws.policies) == 0 {
		t.Fatal("no policy")
	}
	p := ws.policies[0]
	rules, err := p.Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var c PolicyCache
	run, err := c.Run(t.Context(), p, rules, ws.in, PolicyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// String writes r on one line, for test messages: the rule, the example,
// the binding and param, the verdict and the message.
func (r PolicyResult) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s:%d", r.Rule.FieldPath, strings.TrimPrefix(r.Example.File, "/w/"), r.Example.Line)
	if r.Update {
		b.WriteString(" update")
	}
	if r.Binding != nil {
		b.WriteString(" binding " + r.Binding.Name)
	}
	if r.Param != nil {
		b.WriteString(" param " + r.Param.Name)
	}
	b.WriteString(" | " + r.Verdict.String())
	if r.Message != "" {
		b.WriteString(" | " + r.Message)
	}
	if !r.Expected {
		b.WriteString(" | wrong")
		if r.OnExample {
			b.WriteString(" on example")
		}
	}
	return b.String()
}

// vap is a ValidatingAdmissionPolicy named name of Deployments, with
// spec, more of its spec, without the defaults the API server gives.
func vap(name, spec string) string {
	return `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: ` + name + `}
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments]}
` + spec
}

// deployment is a Deployment named name with replicas.
func deployment(name string, replicas int) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + "}\nspec: {replicas: " + strconv.Itoa(replicas) +
		", selector: {matchLabels: {a: b}}, template: {metadata: {labels: {a: b}}, spec: {containers: [{name: c, image: i}]}}}\n"
}

func TestPolicyRun(t *testing.T) {
	t.Parallel()
	twoVersions := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  scope: Namespaced
  versions:
    - {name: v1, served: true, storage: true, schema: {openAPIV3Schema: {type: object, properties: {spec: {type: object, properties: {size: {type: integer}}}}}}}
    - {name: v2, served: true, storage: false, schema: {openAPIV3Schema: {type: object, properties: {spec: {type: object, properties: {size: {type: integer}}}}}}}
`
	for _, tc := range []struct {
		name   string
		policy string
		files  [][2]string
		want   []string
	}{{
		name: "no binding: as if bound with Deny, and what reads params does not run",
		policy: vap("p", `  paramKind: {apiVersion: v1, kind: ConfigMap}
  variables:
    - {name: max, expression: "int(params.data.max)"}
  validations:
    - {expression: "object.spec.replicas <= variables.max"}
    - {expression: "object.spec.replicas > 0", message: no zero}
`),
		files: [][2]string{{"deploy.yaml", deployment("zero", 0)}},
		want: []string{
			"spec.validations[0].expression deploy.yaml:1 | not run | " + noBinding,
			"spec.validations[1].expression deploy.yaml:1 | refused | no zero | wrong",
		},
	}, {
		// Review of #22: the stand-in binding gives no params, so the
		// condition would fail and, under Fail, refuse every example.
		name: "no binding, and a match condition reads params: nothing runs",
		policy: vap("p", `  paramKind: {apiVersion: v1, kind: ConfigMap}
  failurePolicy: Fail
  matchConditions:
    - {name: flagged, expression: "params.data.on == 'yes'"}
  validations:
    - {expression: "object.spec.replicas < 3"}
`),
		files: [][2]string{{"deploy.yaml", deployment("one", 1)}},
		want: []string{
			"spec.matchConditions[0].expression deploy.yaml:1 | not run | " + conditionNoBinding,
			"spec.validations[0].expression deploy.yaml:1 | not run | " + conditionNoBinding,
		},
	}, {
		name: "a match condition that fails under failurePolicy Ignore: the API server ignores the error and skips the policy",
		policy: vap("p", `  failurePolicy: Ignore
  matchConditions:
    - {name: ratio, expression: "10 / (object.spec.replicas - 1) > 0"}
  validations:
    - {expression: "object.spec.replicas < 3"}
`),
		files: [][2]string{{"deploy.yaml", deployment("one", 1)}},
		want: []string{
			"spec.matchConditions[0].expression deploy.yaml:1 | error | expression '10 / (object.spec.replicas - 1) > 0' resulted in error: division by zero: the policy's failurePolicy is Ignore",
			`spec.validations[0].expression deploy.yaml:1 | not run | the match condition "ratio" fails, and the policy's failurePolicy is Ignore, so the API server does not run the policy on the example`,
		},
	}, {
		name: "a match condition that fails beside one that is false refuses nothing, under Fail",
		policy: vap("p", `  failurePolicy: Fail
  matchConditions:
    - {name: never, expression: "false"}
    - {name: ratio, expression: "10 / (object.spec.replicas - 1) > 0"}
  validations:
    - {expression: "object.spec.replicas < 3"}
`),
		files: [][2]string{{"deploy.yaml", deployment("one", 1)}},
		want: []string{
			"spec.matchConditions[0].expression deploy.yaml:1 | excluded",
			"spec.matchConditions[1].expression deploy.yaml:1 | error | expression '10 / (object.spec.replicas - 1) > 0' resulted in error: division by zero",
			`spec.validations[0].expression deploy.yaml:1 | not run | the match condition "never" is false`,
		},
	}, {
		name: "authorizer is not run",
		policy: vap("p", `  validations:
    - {expression: "authorizer.group('apps').resource('deployments').check('create').allowed()"}
    - {expression: "object.spec.replicas > 0"}
`),
		files: [][2]string{{"deploy.yaml", deployment("one", 1)}},
		want: []string{
			"spec.validations[0].expression deploy.yaml:1 | not run | " + readsAuthz,
			"spec.validations[1].expression deploy.yaml:1 | allowed",
		},
	}, {
		name: "a match condition that reads authorizer: nothing runs",
		policy: vap("p", `  matchConditions:
    - {name: allowed, expression: "authorizer.serviceAccount('ns', 'sa').group('').resource('pods').check('get').allowed()"}
  validations:
    - {expression: "object.spec.replicas > 0"}
`),
		files: [][2]string{{"deploy.yaml", deployment("one", 1)}},
		want: []string{
			"spec.matchConditions[0].expression deploy.yaml:1 | not run | " + conditionAuthz,
			"spec.validations[0].expression deploy.yaml:1 | not run | " + conditionAuthz,
		},
	}, {
		name: "a policy the API server refuses runs nothing",
		policy: vap("p", `  validations:
    - {expression: "object.spec.replicas >"}
    - {expression: "object.spec.replicas > 0"}
`),
		files: [][2]string{{"deploy.yaml", deployment("one", 1)}},
		want: []string{
			"spec.validations[0].expression deploy.yaml:1 | not run | " + refusedPolicy,
			"spec.validations[1].expression deploy.yaml:1 | not run | " + refusedPolicy,
		},
	}, {
		name: "match conditions: one excludes an example, and the validations do not run on it",
		policy: vap("p", `  matchConditions:
    - {name: named, expression: "has(object.metadata.name)"}
    - {name: not-test, expression: "!object.metadata.name.startsWith('test')"}
  validations:
    - {expression: "object.spec.replicas < 3"}
`),
		files: [][2]string{{"deploy.yaml", deployment("test-one", 5) + "---\n" + deployment("prod", 1)}},
		want: []string{
			"spec.matchConditions[0].expression deploy.yaml:1 | allowed",
			"spec.matchConditions[0].expression deploy.yaml:6 | allowed",
			"spec.matchConditions[1].expression deploy.yaml:1 | excluded",
			"spec.matchConditions[1].expression deploy.yaml:6 | allowed",
			`spec.validations[0].expression deploy.yaml:1 | not run | the match condition "not-test" is false, so the policy does not run on the example`,
			"spec.validations[0].expression deploy.yaml:6 | allowed",
		},
	}, {
		name: "what the examples expect",
		policy: vap("p", `  validations:
    - {expression: "object.spec.replicas < 3", message: "fewer than 3"}
`),
		files: [][2]string{
			{"invalid/big.yaml", deployment("big", 5) + "---\n# cel-lens: refused \"fewer than 3\"\n" + deployment("bigger", 9) + "---\n# cel-lens: refused \"other\"\n" + deployment("biggest", 9)},
			{"invalid/small.yaml", deployment("small", 1)},
			{"ok.yaml", deployment("ok", 1) + "---\n" + deployment("not-ok", 7)},
		},
		want: []string{
			"spec.validations[0].expression invalid/big.yaml:1 | refused | fewer than 3",
			"spec.validations[0].expression invalid/big.yaml:7 | refused | fewer than 3",
			"spec.validations[0].expression invalid/big.yaml:13 | refused | fewer than 3 | wrong on example",
			"spec.validations[0].expression invalid/small.yaml:1 | allowed | wrong on example",
			"spec.validations[0].expression ok.yaml:1 | allowed",
			"spec.validations[0].expression ok.yaml:6 | refused | fewer than 3 | wrong",
		},
	}, {
		name: "an update is from the last example the API server stored",
		policy: vap("p", `  validations:
    - {expression: "oldObject == null || object.spec.replicas >= oldObject.spec.replicas", message: never fewer}
`) + "---\napiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicyBinding\nmetadata: {name: b}\nspec: {policyName: p, validationActions: [Deny]}\n",
		files: [][2]string{{"deploy.yaml", deployment("w", 3) + "---\n" + deployment("w", 1) + "---\n" + deployment("w", 2) + "---\n" + deployment("w", 4)}},
		want: []string{
			"spec.validations[0].expression deploy.yaml:1 binding b | allowed",
			"spec.validations[0].expression deploy.yaml:6 update binding b | refused | never fewer | wrong",
			"spec.validations[0].expression deploy.yaml:11 update binding b | refused | never fewer | wrong",
			"spec.validations[0].expression deploy.yaml:16 update binding b | allowed",
		},
	}, {
		name: "an update is from an example the policy did not match, which the API server stored",
		policy: vap("p", `    objectSelector: {matchExpressions: [{key: skip, operator: DoesNotExist}]}
  validations:
    - {expression: "oldObject == null || object.spec.replicas >= oldObject.spec.replicas", message: never fewer}
`),
		files: [][2]string{{"deploy.yaml", strings.Replace(deployment("w", 3), "{name: w}", "{name: w, labels: {skip: x}}", 1) + "---\n" + deployment("w", 1)}},
		want: []string{
			"spec.validations[0].expression deploy.yaml:6 update | refused | never fewer | wrong",
		},
	}, {
		name: "a custom resource the policy matches in another version, converted as the conversion strategy None converts it",
		policy: `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: p}
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [example.com], apiVersions: [v2], operations: [CREATE], resources: [widgets]}
  validations:
    - {expression: "object.apiVersion == 'example.com/v2'", message: "not v2"}
    - {expression: "object.spec.size < 10"}
`,
		files: [][2]string{{"crd.yaml", twoVersions}, {"w.yaml", specWidget("w", "{size: 3}") + "---\n" + specWidget("bad", "{size: oops}")}},
		want: []string{
			"spec.validations[0].expression w.yaml:1 | allowed",
			"spec.validations[0].expression w.yaml:6 | not run | the schema refuses the example",
			"spec.validations[1].expression w.yaml:1 | allowed",
			"spec.validations[1].expression w.yaml:6 | not run | the schema refuses the example",
		},
	}, {
		name:   "not the examples: documents of the policy's file, policies and bindings, kinds that do not match",
		policy: vap("p", "  validations:\n    - {expression: \"false\"}\n") + "---\n" + deployment("here", 1),
		files:  [][2]string{{"other.yaml", vap("q", "") + "---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\n---\n" + deployment("there", 1)}},
		want: []string{
			"spec.validations[0].expression other.yaml:13 | refused | failed expression: false | wrong",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := readWorkspace(t, tc.policy, tc.files...).run(t)
			var got []string
			for _, r := range run.Results {
				got = append(got, r.String())
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Run() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
			for i := range got {
				if !strings.HasPrefix(got[i], tc.want[i]) {
					t.Errorf("Run()[%d] = %s, want %s…", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestPolicyRules(t *testing.T) {
	t.Parallel()
	policy := vap("p", `  variables:
    - {name: v, expression: "1"}
  matchConditions:
    - {name: first, expression: "true"}
  validations:
    - expression: "object.spec.replicas > 0"
      messageExpression: "'no'"
  auditAnnotations:
    - {key: k, valueExpression: "'x'"}
`)
	ps := ReadPolicies(t.Context(), policyFile, []byte(policy), kubeversion.Default())
	if len(ps) != 1 || ps[0].Name != "p" || ps[0].Line != 1 {
		t.Fatalf("ReadPolicies() = %v, want the policy p", ps)
	}
	rules, err := ps[0].Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rules {
		got = append(got, fmt.Sprintf("%d %d %s %s %s message:%v", r.Field, r.Index, r.Name, r.FieldPath, r.Text, r.MessageStart.Line > 0))
	}
	want := []string{
		"1 0 first spec.matchConditions[0].expression true message:false",
		"0 0  spec.validations[0].expression object.spec.replicas > 0 message:true",
		"2 0 k spec.auditAnnotations[0].valueExpression 'x' message:false",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Rules() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The defaults the API server gives.
	spec := ps[0].spec.Spec
	if *spec.FailurePolicy != "Fail" || *spec.MatchConstraints.MatchPolicy != "Equivalent" || spec.MatchConstraints.NamespaceSelector == nil ||
		spec.MatchConstraints.ObjectSelector == nil || *spec.MatchConstraints.ResourceRules[0].Scope != "*" {
		t.Errorf("ReadPolicies() spec = %+v, want the API server's defaults", spec)
	}
}

func TestPolicyReads(t *testing.T) {
	t.Parallel()
	p := ReadPolicies(t.Context(), policyFile, []byte(vap("p", `  variables:
    - {name: limit, expression: "params.max"}
    - {name: may, expression: "authorizer.group('').resource('pods').check('get').allowed()"}
    - {name: both, expression: "variables.limit > 0 && variables.may"}
`)), kubeversion.Default())[0]
	of := policyReads(p.spec.Spec)
	for _, tc := range []struct {
		expr string
		want reads
	}{
		{"object.spec.replicas > 0", reads{}},
		{"params.max > 0", reads{params: true}},
		{"has(variables.limit)", reads{params: true}},
		{"variables.may", reads{authorizer: true}},
		{"variables.both", reads{params: true, authorizer: true}},
		{"object.params > 0", reads{}},
		{"object.spec.all(x, x > 0)", reads{}},
		{"not ( CEL", reads{}},
	} {
		if got := of(tc.expr); got != tc.want {
			t.Errorf("policyReads()(%q) = %+v, want %+v", tc.expr, got, tc.want)
		}
	}
}

func TestPolicyKinds(t *testing.T) {
	t.Parallel()
	read := func(spec string) []*Policy {
		return ReadPolicies(t.Context(), policyFile, []byte(`apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: p}
spec:
`+spec), kubeversion.Default())
	}
	kinds, anyKind := PolicyKinds(read(`  paramKind: {apiVersion: v1, kind: ConfigMap}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: ["*"], resources: [deployments, deployments/scale, statefulsets]}
      - {apiGroups: [example.com], apiVersions: [v1], resources: [widgets]}
`), kubeversion.Default())
	if want := []string{"ConfigMap", "Deployment", "Namespace", "StatefulSet"}; !slices.Equal(kinds, want) || anyKind {
		t.Errorf("PolicyKinds() = %q, %v, want %q, false", kinds, anyKind, want)
	}
	if _, anyKind := PolicyKinds(read("  matchConstraints:\n    resourceRules:\n      - {apiGroups: [\"*\"], apiVersions: [v1], resources: [pods]}\n"), kubeversion.Default()); !anyKind {
		t.Error("PolicyKinds(a rule of any group) is not of any kind")
	}
	if kinds, _ := PolicyKinds(nil, kubeversion.Default()); kinds != nil {
		t.Errorf("PolicyKinds(no policy) = %q, want none", kinds)
	}
}

func TestFindBuiltIn(t *testing.T) {
	t.Parallel()
	text := deployment("web", 2) + "---\n" + deployment("web", 3) +
		"---\napiVersion: apps/v1\nkind: Deployment\nmetadata: {generateName: gen-}\nspec: {replicas: 1, unknown: 1, selector: {matchLabels: {a: b}}, template: {metadata: {labels: {a: b}}, spec: {containers: [{name: c, image: i}]}}}\n" +
		"---\napiVersion: v1\nkind: Namespace\nmetadata: {name: shop, namespace: ignored}\n" +
		"---\napiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\n"
	objs, err := FindBuiltIn(t.Context(), "/w/invalid/d.yaml", "invalid/d.yaml", []byte(text), func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 4 {
		t.Fatalf("FindBuiltIn() = %d documents, want the 4 of built-in kinds", len(objs))
	}
	web, update, gen, ns := objs[0], objs[1], objs[2], objs[3]
	meta := func(o *Object) map[string]any {
		m, _ := o.Object["metadata"].(map[string]any)
		return m
	}
	if web.CRD != nil || web.Expect != Refused || web.requestNamespace() != "default" || meta(web)["namespace"] != "default" || web.Old != nil {
		t.Errorf("FindBuiltIn() first = %+v, want a create in default, expected to be refused by its path", web)
	}
	if update.Old != web {
		t.Error("FindBuiltIn() second is not an update from the first")
	}
	if !gen.DecodeFailed || len(gen.Problems) != 1 || !strings.Contains(gen.Problems[0].Message, `unknown field "spec.unknown"`) {
		t.Errorf("FindBuiltIn() third problems = %+v, want the strict decoding error of spec.unknown", gen.Problems)
	}
	if name, _ := meta(gen)["name"].(string); name != "gen-"+generatedSuffix {
		t.Errorf("FindBuiltIn() third name = %q, want one for its generateName", name)
	}
	if ns.requestNamespace() != "" || meta(ns)["namespace"] != nil {
		t.Errorf("FindBuiltIn() Namespace = %+v, want no namespace", ns.Object)
	}
	none, err := FindBuiltIn(t.Context(), "/w/d.yaml", "d.yaml", []byte(text), func(_, kind string) bool { return kind == "Namespace" })
	if err != nil || len(none) != 1 {
		t.Errorf("FindBuiltIn(only Namespace) = %d documents, %v, want 1", len(none), err)
	}
}

// TestPolicyCache checks that a PolicyCache keeps what a policy did to an
// example and runs it again when the policy, the binding, the params or
// the example change, and that what it kept names the rules, examples and
// params of the run it is in.
func TestPolicyCache(t *testing.T) {
	t.Parallel()
	policy := vap("p", `  paramKind: {apiVersion: v1, kind: ConfigMap}
  validations:
    - {expression: "object.spec.replicas <= int(params.data.max)"}
`) + "---\napiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicyBinding\nmetadata: {name: b}\nspec: {policyName: p, validationActions: [Deny], paramRef: {name: limit, namespace: default, parameterNotFoundAction: Deny}}\n"
	param := func(limit string) [2]string {
		return [2]string{"params.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: limit}\ndata: {max: \"" + limit + "\"}\n"}
	}
	var c PolicyCache
	run := func(policy string, files ...[2]string) []PolicyResult {
		t.Helper()
		ws := readWorkspace(t, policy, files...)
		rules, err := ws.policies[0].Rules(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		out, err := c.Run(t.Context(), ws.policies[0], rules, ws.in, PolicyOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range out.Results {
			if !slices.Contains(rules, r.Rule) || !slices.Contains(ws.in.Objects, r.Example) || r.Param != nil && !slices.Contains(ws.in.Objects, r.Param) {
				t.Errorf("Run() result %s names a rule, an example or a param of another run", r)
			}
		}
		return out.Results
	}
	verdict := func(rs []PolicyResult) string { return rs[0].Verdict.String() }
	deploy := [2]string{"deploy.yaml", deployment("web", 5)}
	if got := verdict(run(policy, param("3"), deploy)); got != "refused" {
		t.Errorf("first run = %s, want refused", got)
	}
	if got := verdict(run(policy, param("3"), deploy)); got != "refused" {
		t.Errorf("unchanged = %s, want refused", got)
	}
	if got := verdict(run(policy, param("10"), deploy)); got != "allowed" {
		t.Errorf("param changed = %s, want allowed", got)
	}
	if got := verdict(run(policy, param("10"), [2]string{"deploy.yaml", deployment("web", 50)})); got != "refused" {
		t.Errorf("example changed = %s, want refused", got)
	}
	if got := verdict(run(strings.Replace(policy, "[Deny]", "[Warn]", 1), param("10"), [2]string{"deploy.yaml", deployment("web", 50)})); got != "warned" {
		t.Errorf("binding changed = %s, want warned", got)
	}
	if got := verdict(run(strings.Replace(strings.Replace(policy, "[Deny]", "[Warn]", 1), "<=", "<", 1), param("50"), [2]string{"deploy.yaml", deployment("web", 50)})); got != "warned" {
		t.Errorf("policy changed = %s, want warned", got)
	}
}

// TestPolicyRunMaxExamples checks that a policy runs on the first 100
// examples it matches, and on as many as PolicyOptions.MaxExamples says (#24).
func TestPolicyRunMaxExamples(t *testing.T) {
	t.Parallel()
	var docs strings.Builder
	for i := range maxPolicyExamples + 50 {
		docs.WriteString(deployment("d"+strconv.Itoa(i), 1) + "---\n")
	}
	ws := readWorkspace(t, vap("p", "  validations:\n    - {expression: \"object.spec.replicas < 3\"}\n"), [2]string{"deploy.yaml", docs.String()})
	rules, err := ws.policies[0].Rules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ max, want int }{{0, maxPolicyExamples}, {10, 10}, {maxPolicyExamples + 50, maxPolicyExamples + 50}, {1 << 30, maxPolicyExamples + 50}} {
		var c PolicyCache
		run, err := c.Run(t.Context(), ws.policies[0], rules, ws.in, PolicyOptions{MaxExamples: tc.max})
		if err != nil {
			t.Fatal(err)
		}
		if len(run.Results) != tc.want {
			t.Errorf("Run(MaxExamples: %d) gave %d results, want %d", tc.max, len(run.Results), tc.want)
		}
	}
}

func TestPolicyRunCancelled(t *testing.T) {
	t.Parallel()
	ws := readWorkspace(t, vap("p", "  validations:\n    - {expression: \"true\"}\n"), [2]string{"deploy.yaml", deployment("web", 5)})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var c PolicyCache
	if _, err := c.Run(ctx, ws.policies[0], nil, ws.in, PolicyOptions{Checked: []check.Diagnostic{}}); !errors.Is(err, context.Canceled) {
		t.Errorf("Run(cancelled) error = %v, want context.Canceled", err)
	}
}
