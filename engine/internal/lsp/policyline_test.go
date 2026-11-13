package lsp_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// linePolicy is a ValidatingAdmissionPolicy of Deployments with params, a
// match condition, two validations and an audit annotation, and a binding
// that warns, whose paramRef.selector matches two ConfigMaps.
const linePolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: replicas}
spec:
  paramKind: {apiVersion: v1, kind: ConfigMap}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments]}
    objectSelector: {matchExpressions: [{key: skip, operator: DoesNotExist}]}
  matchConditions:
    - name: not-test
      expression: "!object.metadata.name.startsWith('test')"
  validations:
    - expression: "object.spec.replicas <= int(params.data.max)"
      messageExpression: "'at most ' + params.data.max + ' replicas'"
    - expression: "object.spec.replicas > 0"
      message: no zero replicas
  auditAnnotations:
    - key: replicas
      valueExpression: "string(object.spec.replicas)"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata: {name: limits}
spec:
  policyName: replicas
  validationActions: [Warn]
  paramRef: {selector: {matchLabels: {limits: "yes"}}, namespace: default, parameterNotFoundAction: Deny}
`

// lineParams are the two ConfigMaps linePolicy's binding selects.
const lineParams = `apiVersion: v1
kind: ConfigMap
metadata: {name: small, labels: {limits: "yes"}}
data: {max: "3"}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: big, labels: {limits: "yes"}}
data: {max: "10"}
`

// lineDeployment is a Deployment named name with replicas and labels, a
// YAML flow mapping.
func lineDeployment(name, replicas, labels string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + ", labels: " + labels + "}\nspec: {replicas: " + replicas +
		", selector: {matchLabels: {a: b}}, template: {metadata: {labels: {a: b}}, spec: {containers: [{name: c, image: i}]}}}\n"
}

// TestPolicyExampleLine checks what an admission policy does to the
// examples of the workspace where a person looks (#19): the part of the
// line above each validation, match condition and audit annotation, its
// hover, which names the binding and the param of each result, the warning
// on an example that expects to be refused and that only a warning meets,
// and, once the binding denies, the warnings of a refusal of an example
// that expects to be allowed; cel-lens.exampleResults gives each result
// with its binding, param and the API server's answer.
func TestPolicyExampleLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	at := func(rel string) (path, uri string) {
		path = filepath.Join(dir, filepath.FromSlash(rel))
		return path, fileURI(path)
	}
	policyPath, policyURI := at("policy/replicas.yaml")
	_, zeroURI := at("examples/invalid/zero.yaml")
	webPath, webURI := at("examples/deploy.yaml")
	writeIn(t, policyPath, linePolicy)
	writeIn(t, filepath.Join(dir, "policy", "params.yaml"), lineParams)
	writeIn(t, webPath, lineDeployment("web", "5", "{}")+"---\n"+lineDeployment("test-web", "50", "{}")+"---\n"+lineDeployment("skipped", "50", "{skip: x}"))
	writeIn(t, filepath.Join(dir, "examples", "invalid", "zero.yaml"), lineDeployment("zero", "0", "{}"))

	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"capabilities": refreshCaps, "workspaceFolders": []map[string]any{{"uri": fileURI(dir), "name": "ws"}}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	c.openURI(policyURI, linePolicy)
	c.diagnosticsFor(zeroURI, saying("expected to be refused, but nothing refuses it"))
	const (
		limit     = "object.spec.replicas <= int"
		zero      = "object.spec.replicas > 0"
		condition = "!object.metadata.name"
		annotate  = "string(object.spec.replicas)"
	)
	t.Run("the line", func(t *testing.T) {
		c.use(t)
		for _, tc := range []struct{ expr, want string }{
			{limit, "1 wrong · warns 1"},
			{zero, "1 wrong · 1 passes"},
			// zero.yaml expects to be refused, and nothing refuses it: every
			// expression that allows it says it is wrong, as a CRD's rule
			// does.
			{condition, "1 wrong · excludes 1 · 1 passes"},
			{annotate, "1 wrong · annotates 1"},
		} {
			if got := c.examplesPart(policyURI, linePolicy, tc.expr); got != tc.want {
				t.Errorf("the line above %s ends with %q, want %q", tc.expr, got, tc.want)
			}
		}
	})
	t.Run("the hover", func(t *testing.T) {
		c.use(t)
		got := c.hoverAt(policyURI, linePolicy, limit)
		for _, want := range []string{
			"**Examples:** allows 1 example it should refuse · warns 1 · under binding limits\n\n",
			"- [examples/deploy.yaml:1](<" + webURI + "#L1>) **warned** under binding limits with param policy/params.yaml:1: at most 3 replicas\n",
			"- [examples/deploy.yaml:1](<" + webURI + "#L1>) allowed under binding limits with param policy/params.yaml:6\n",
			"examples/invalid/zero.yaml:1",
			"not run under binding limits with param policy/params.yaml:1: the match condition \"not\\-test\" is false",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("hover on %s =\n%s\nwant it to have\n%s", limit, got, want)
			}
		}
	})
	t.Run("exampleResults", func(t *testing.T) {
		c.use(t)
		_, raw, rerr := c.command("cel-lens.exampleResults", map[string]any{"uri": policyURI}, nil)
		if rerr != nil {
			t.Fatalf("cel-lens.exampleResults error = %+v", rerr)
		}
		var answer struct {
			Rules []struct {
				Document, Field, Rule string
				Index                 int
				Results               []struct {
					Example   struct{ File, Operation string }
					Binding   string
					Param     struct{ File, Name string }
					Result    string
					Message   string
					Admission struct{ Warnings []string }
					Expected  bool
				}
			}
		}
		if err := json.Unmarshal(raw, &answer); err != nil {
			t.Fatal(err)
		}
		var fields []string
		for _, r := range answer.Rules {
			fields = append(fields, r.Field+":"+r.Rule)
		}
		if len(answer.Rules) != 4 || answer.Rules[1].Document != "ValidatingAdmissionPolicy replicas" || answer.Rules[1].Field != "validation" {
			t.Fatalf("cel-lens.exampleResults rules = %q, want the match condition, the two validations and the annotation", fields)
		}
		// The params come in the order of their names: big, then small.
		first := answer.Rules[1].Results[1]
		if first.Example.File != "examples/deploy.yaml" || first.Binding != "limits" || first.Param.Name != "small" || first.Result != "warned" || first.Message != "at most 3 replicas" ||
			len(first.Admission.Warnings) != 1 || !strings.Contains(first.Admission.Warnings[0], "with binding 'limits': at most 3 replicas") || !first.Expected {
			t.Errorf("first result of %s = %+v, want web warned under binding limits with param small, and the API server's warning", answer.Rules[1].Rule, first)
		}
	})
	t.Run("the binding denies", func(t *testing.T) {
		c.use(t)
		denying := strings.Replace(linePolicy, "[Warn]", "[Deny]", 1)
		c.changeURI(policyURI, 2, denying)
		p, _ := c.diagnosticsFor(webURI, saying(`expected to be allowed, but the rule "object.spec.replicas <= int(params.data.max)" refuses it under binding limits with param policy/params.yaml:1: at most 3 replicas`))
		if len(p.Diagnostics) != 1 {
			t.Errorf("diagnostics of deploy.yaml = %+v, want one warning", p.Diagnostics)
		}
		c.diagnosticsFor(zeroURI, func(p published) bool { return len(p.Diagnostics) == 0 })
		if got := c.examplesPart(policyURI, denying, limit); got != "1 wrong · 1 passes" {
			t.Errorf("the line above %s with Deny ends with %q, want 1 wrong · 1 passes", limit, got)
		}
		if got := c.hoverAt(policyURI, denying, limit); !strings.Contains(got, "**Examples:** refuses 1 example it should allow · 1 passes · under binding limits\n") {
			t.Errorf("hover on %s with Deny =\n%s\nwant the refusal of web, and zero.yaml passing, as the other validation refuses it", limit, got)
		}
	})
	shutdown(t, c)
}
