package assist_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// usesCRD has rules on the root, on a nested node and on the items of a
// list, and one that reads no variable.
const usesCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {kind: Widget, plural: widgets}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: "has(self.spec)"
          properties:
            spec:
              type: object
              x-kubernetes-validations:
                - rule: "self.ports.all(p, p.port > 0) && (oldSelf.replicas == self.replicas || self.replicas < 10)"
                  messageExpression: "'too many: ' + string(self.replicas)"
                - rule: "1 < 2"
              properties:
                replicas: {type: integer}
                ports:
                  type: array
                  items:
                    type: object
                    x-kubernetes-validations:
                      - rule: "self.port > 0"
                    properties:
                      port: {type: integer}
`

// usesVAP reads every variable of a ValidatingAdmissionPolicy somewhere.
const usesVAP = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  paramKind: {apiVersion: example.com/v1, kind: Missing}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments], operations: [CREATE, UPDATE]}
  matchConditions:
    - name: not-system
      expression: "request.namespace != 'kube-system'"
  variables:
    - name: replicas
      expression: "object.spec.replicas"
  validations:
    - expression: "variables.replicas <= params.max && variables.replicas > 0 && oldObject == null || namespaceObject.metadata.name != '' && authorizer.group('apps').resource('deployments').check('get').allowed()"
      messageExpression: "'too many: ' + string(variables.?replicas.orValue(0))"
  auditAnnotations:
    - key: replicas
      valueExpression: "string(object.spec.replicas)"
`

// usesMAP is a MutatingAdmissionPolicy whose mutations read a variable.
const usesMAP = `apiVersion: admissionregistration.k8s.io/v1beta1
kind: MutatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments], operations: [CREATE]}
  matchConditions:
    - name: named
      expression: "object.metadata.name != ''"
  variables:
    - name: want
      expression: "3"
  mutations:
    - patchType: ApplyConfiguration
      applyConfiguration:
        expression: "Object{spec: Object.spec{replicas: variables.want}}"
    - patchType: JSONPatch
      jsonPatch:
        expression: "[JSONPatch{op: 'add', path: '/spec/replicas', value: variables['want']}]"
`

// TestRuleUses is the Uses criterion of #52: the variables of the scope the
// expression reads, in the order first read, each once, without a macro's
// own variables, each with its type and a few words, in each kind of
// document; nothing for an expression that reads no variable.
func TestRuleUses(t *testing.T) {
	t.Parallel()
	const (
		widgetSpec = "`self`: `Widget.spec` (object), the value the rule is on."
		object     = "`object`: `apps/v1 Deployment`, the object being admitted, null on DELETE."
		replicas   = "`variables.replicas`: `int`, the policy variable defined by `object.spec.replicas`."
	)
	tests := []struct {
		name, text, needle string
		on                 []feature.Feature
		want               string
	}{
		{"a CRD's root", usesCRD, "has(self.spec)", nil, "`self`: `Widget` (object), the value the rule is on."},
		{
			"a nested node, with oldSelf after self and the macro's p left out", usesCRD, "self.ports.all", nil,
			"\n\n- " + widgetSpec + "\n- `oldSelf`: `Widget.spec` (object), the value before the update, so the rule runs only on update.",
		},
		{"a CRD's messageExpression", usesCRD, "'too many: ' + string(self", nil, widgetSpec},
		{"the items of a list", usesCRD, "self.port > 0", nil, "`self`: `Widget.spec.ports[]` (object), the value the rule is on."},
		{"no variable", usesCRD, "1 < 2", nil, ""},
		{
			"a validation", usesVAP, "variables.replicas <= params", nil,
			"\n\n- " + replicas +
				"\n- `params`: `dyn`, the parameter resource the binding refers to, null when there is none; dyn because typing from a CustomResourceDefinition is the experimental feature `crd-typed-policies`, which is off." +
				"\n- `oldObject`: `apps/v1 Deployment`, the existing object, null on CREATE." +
				"\n- `namespaceObject`: `kubernetes.Namespace`, the namespace the object belongs to, null for cluster-scoped objects." +
				"\n- `authorizer`: `kubernetes.authorization.Authorizer`, checks what the requesting user may do.",
		},
		{
			"params of a kind no CRD defines, with crd-typed-policies on", usesVAP, "variables.replicas <= params",
			[]feature.Feature{feature.CRDTypedPolicies},
			"spec.paramKind is no built-in kind and no CustomResourceDefinition among the workspace files defines it",
		},
		{"a messageExpression reading an optional field", usesVAP, "'too many: ' + string(variables", nil, replicas},
		{"a match condition", usesVAP, "request.namespace", nil, "`request`: `kubernetes.AdmissionRequest`, the attributes of the admission request."},
		{"a variable", usesVAP, "object.spec.replicas\"", nil, object},
		{"an audit annotation", usesVAP, "string(object.spec.replicas)", nil, object},
		{"a mutating policy's match condition", usesMAP, "object.metadata.name", nil, object},
		{"a mutating policy's variable", usesMAP, "\"3\"", nil, ""},
		{"an apply configuration", usesMAP, "Object{spec", nil, "`variables.want`: `int`, the policy variable defined by `3`."},
		{"a JSON patch, by index", usesMAP, "[JSONPatch", nil, "`variables.want`: `int`, the policy variable defined by `3`."},
		{
			"a Kyverno policy", vpol("{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}", "variables.replicas == 1 && object.spec != null && image.GetMetadata('x') != null"), "variables.replicas ==",
			[]feature.Feature{feature.Kyverno},
			"\n\n- `variables.replicas`: `dyn`, a policy variable of spec.variables; dyn because the type of this policy variable is not worked out for this policy." +
				"\n- " + object +
				"\n- `image`: `imagedata.Context`, reads image metadata from a registry: image.GetMetadata(...).",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := assist.Parse(context.Background(), "f.yaml", []byte(tt.text))
			e, ok := doc.Expression(posIn(t, tt.text, tt.needle, 1))
			if !ok {
				t.Fatalf("Expression(%q) = none", tt.needle)
			}
			r, ok := with(t, tt.on...).Rule(doc, e)
			if !ok {
				t.Fatalf("Rule(%q) = none, want the rule's sections", e.Text)
			}
			// A sentence alone is part of the line; anything else is the
			// whole section.
			if part := !strings.HasPrefix(tt.want, "`") && !strings.HasPrefix(tt.want, "\n") && tt.want != ""; part && !strings.Contains(r.Uses, tt.want) || !part && r.Uses != tt.want {
				t.Errorf("Rule(%q).Uses =\n%q\nwant\n%q", e.Text, r.Uses, tt.want)
			}
		})
	}
}

// TestRuleNotChecked checks that an expression the check does not check
// gets no rule: a CustomResourceDefinition of apiextensions.k8s.io/v1beta1,
// and a Kyverno policy while kyverno is off.
func TestRuleNotChecked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, text, needle string }{
		{"v1beta1", strings.Replace(usesCRD, "apiextensions.k8s.io/v1", "apiextensions.k8s.io/v1beta1", 1), "has(self.spec)"},
		{"Kyverno, off", vpol("{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}", "object.spec != null"), "object.spec"},
	} {
		doc := assist.Parse(context.Background(), "f.yaml", []byte(tc.text))
		e, ok := doc.Expression(posIn(t, tc.text, tc.needle, 1))
		if !ok {
			t.Fatalf("%s: Expression(%q) = none", tc.name, tc.needle)
		}
		if r, ok := with(t).Rule(doc, e); ok {
			t.Errorf("%s: Rule(%q) = %+v, want none", tc.name, e.Text, r)
		}
	}
}

// TestRuleUsesAssumed checks that a type .cellens assumes says so in Uses,
// as the part hover does, and that the CEL of a variable holding three
// backticks is quoted whole in one code span.
func TestRuleUsesAssumed(t *testing.T) {
	t.Parallel()
	text := strings.Replace(replicaLimitPolicy, "{name: named, expression: params.maxReplicas}", "{name: named, expression: \"'```'\"}", 1)
	doc := assumedDoc(text, replicaLimitParams())
	e, ok := doc.Expression(posIn(t, text, "object.spec.replicas <=", 1))
	if !ok {
		t.Fatal("Expression = none")
	}
	r, _ := with(t).Rule(doc, e)
	for _, want := range []string{
		"- `variables.limit`: `int`, the policy variable defined by `params.maxReplicas`. Its type follows from params, typed from the schema at .cellens, line 6: Kubernetes leaves this value dyn.",
		"- `variables.named`: `string`, the policy variable defined by ````'```'````.",
	} {
		if !strings.Contains(r.Uses, want) {
			t.Errorf("Rule().Uses =\n%s\nwant it to contain\n%s", r.Uses, want)
		}
	}
	params := strings.Replace(text, "object.spec.replicas <= variables.limit", "object.spec.replicas <= params.maxReplicas", 1)
	doc = assumedDoc(params, replicaLimitParams())
	e, _ = doc.Expression(posIn(t, params, "object.spec.replicas <=", 1))
	r, _ = with(t).Rule(doc, e)
	want := "`params`: `{maxReplicas: int, team: string}`, the parameter resource the binding refers to, null when there is none. Typed from the schema at .cellens, line 6: Kubernetes leaves this value dyn."
	if !strings.Contains(r.Uses, want) {
		t.Errorf("Rule().Uses =\n%s\nwant it to contain\n%s", r.Uses, want)
	}
}

// TestDynReasonsFittingOff is the dyn criterion of #52: each of the
// thirteen reasons a value is dyn is said, in its sentence, by the part
// hover and, for a variable, by Uses, with fitting-completion off.
func TestDynReasonsFittingOff(t *testing.T) {
	t.Parallel()
	const (
		deployments = "  matchConstraints:\n    resourceRules:\n      - apiGroups: [apps]\n        apiVersions: [v1]\n        resources: [deployments]\n"
		wildcard    = "  matchConstraints:\n    resourceRules:\n      - apiGroups: ['*']\n        apiVersions: ['*']\n        resources: ['*']\n"
		custom      = "  matchConstraints:\n    resourceRules:\n      - apiGroups: [example.com]\n        apiVersions: [v1]\n        resources: [widgets]\n"
		cronjobs    = deployments + "      - apiGroups: [batch]\n        apiVersions: [v1]\n        resources: [cronjobs]\n"
		paramKind   = "  paramKind: {apiVersion: example.com/v1, kind: Missing}\n"
	)
	variable := func(rules, expr string) string {
		return vapMatching(rules+"  variables:\n    - name: v\n      expression: "+expr+"\n", "variables.v == null")
	}
	var crds check.CRDs
	crds.Set(context.Background(), "crd.yaml", []byte(paramsWidgetCRD))
	failing := strings.Replace(replicaLimitPolicy, "{name: limit, expression: params.maxReplicas}", "{name: limit, expression: params.maxReplicaz}", 1)
	tests := []struct {
		name, text, needle string
		offset             int
		on                 []feature.Feature
		opts               []assist.Option
		// uses is the line of Uses that says it, "" for a field's reason,
		// which Uses does not list.
		want, uses string
	}{
		{
			name: "wildcard-resource-rule", text: vapMatching(wildcard, "object.spec == null"), needle: "object.spec", offset: 1,
			want: "this policy matches resources with a wildcard (for example `resources: [\"*\"]`), so no one kind is known", uses: "`object`: `dyn`",
		},
		{
			name: "no-resource-rule", text: vapMatching("", "object.spec == null"), needle: "object.spec", offset: 1,
			want: "this policy has no match rule that names a resource, so no kind is known", uses: "`object`: `dyn`",
		},
		{
			name: "unknown-kind", text: vapMatching(custom, "object.spec == null"), needle: "object.spec", offset: 1, on: []feature.Feature{feature.CRDTypedPolicies},
			want: "this policy matches a resource whose fields the engine does not know, such as a custom resource", uses: "`object`: `dyn`",
		},
		{
			name: "paramkind-not-found", text: vapMatching(paramKind, "params.x == 1"), needle: "params.x", offset: 1, on: []feature.Feature{feature.CRDTypedPolicies},
			want: "spec.paramKind is no built-in kind and no CustomResourceDefinition among the workspace files defines it", uses: "`params`: `dyn`",
		},
		{
			name: "preserve-unknown-fields", text: strings.Replace(openCRD, "%s", "self.blob.a == 1", 1), needle: "blob.a", offset: 1,
			want: "the schema leaves this value open with `x-kubernetes-preserve-unknown-fields`",
		},
		{
			name: "crd-typed-policies-off", text: vapMatching(custom, "object.spec == null"), needle: "object.spec", offset: 1, opts: []assist.Option{assist.WithCRDs(&crds)},
			want: "typing from a CustomResourceDefinition is the experimental feature `crd-typed-policies`, which is off", uses: "`object`: `dyn`",
		},
		{
			name: "schema-without-type", text: strings.Replace(openCRD, "%s", "self.loose.a == 1", 1), needle: "loose.a", offset: 1,
			want: "the schema gives this value no type",
		},
		{
			name: "policy-variable-untyped", text: vpol("{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}", "variables.replicas == 1"), needle: "replicas ==", offset: 1, on: []feature.Feature{feature.Kyverno},
			want: "the type of this policy variable is not worked out for this policy", uses: "`variables.replicas`: `dyn`",
		},
		{
			name: "variable-result-dyn", text: variable(deployments, "object.spec"), needle: ".v ==", offset: 1,
			want: "the API server makes the type of this variable dyn, because its expression returns an object", uses: "`variables.v`: `dyn`",
		},
		{
			name: "variable-input-dyn", text: variable(wildcard, "object.spec.replicas"), needle: ".v ==", offset: 1,
			want: "the expression of this policy variable reads a value whose type is not known (dyn)", uses: "`variables.v`: `dyn`",
		},
		{
			name: "variable-uncompiled", text: variable(deployments, "object.nosuch + 1"), needle: ".v ==", offset: 1,
			want: "the expression of this policy variable does not compile, so it has no type", uses: "`variables.v`: `dyn`",
		},
		{
			name: "variable-kinds-differ", text: variable(cronjobs, "object.spec.schedule"), needle: ".v ==", offset: 1,
			want: "the kinds this policy matches give this policy variable different types", uses: "`variables.v`: `dyn`",
		},
		{
			name: "variable-assumed-uncompiled", text: failing, needle: "variables.limit &&", offset: len("variables.l"), opts: []assist.Option{assist.WithAssumedTypes([]check.AssumedType{replicaLimitParams()})},
			want: "the expression of this policy variable does not compile with the types .cellens gives the values it reads", uses: "`variables.limit`: `dyn`",
		},
	}
	if len(tests) != 13 {
		t.Fatalf("%d reasons, want the thirteen of dyn.go", len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if slices.Contains(tt.on, feature.FittingCompletion) {
				t.Fatal("the test is for fitting-completion off")
			}
			a := with(t, tt.on...)
			doc := assist.Parse(context.Background(), "p.yaml", []byte(tt.text), tt.opts...)
			pos := posIn(t, tt.text, tt.needle, tt.offset)
			h, ok := a.Hover(doc, pos)
			if !ok || !strings.Contains(h.Markdown, "dyn: "+tt.want) {
				t.Errorf("Hover(%q) = %q, want the reason %q", tt.needle, h.Markdown, tt.want)
			}
			if tt.uses == "" {
				return
			}
			e, _ := doc.Expression(pos)
			r, _ := a.Rule(doc, e)
			if want := tt.uses + ", "; !strings.Contains(r.Uses, want) || !strings.Contains(r.Uses, "; dyn because "+tt.want+".") {
				t.Errorf("Rule(%q).Uses = %q, want %q… and the reason %q", e.Text, r.Uses, want, tt.want)
			}
		})
	}
}

// TestRuleUsesKept checks that the rule of a CRD, worked out once, is the
// same when asked again, by another Assistant too: it depends only on the
// file.
func TestRuleUsesKept(t *testing.T) {
	t.Parallel()
	doc := assist.Parse(context.Background(), "f.yaml", []byte(usesCRD))
	e, _ := doc.Expression(posIn(t, usesCRD, "self.port > 0", 1))
	first, _ := with(t).Rule(doc, e)
	again, _ := with(t, feature.Known()...).Rule(doc, e)
	if first != again || first.Uses == "" {
		t.Errorf("Rule() = %+v, then %+v, want the same Uses", first, again)
	}
	if _, ok := with(t).Rule(doc, extract.Expression{Document: 9}); ok {
		t.Error("Rule(an expression of no document) = ok, want none")
	}
}

// TestRuleChecksReturns is the field table of #53: a check (a CRD's rule,
// a validation, a match condition, a Kyverno validation) gets Checks:, and
// every other expression the fixed Returns: sentence of its field.
func TestRuleChecksReturns(t *testing.T) {
	t.Parallel()
	const message = "the message the API server gives when the rule is false."
	kyverno := strings.Replace(vpol("{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}", "object.spec.replicas > 0"),
		"  validations:\n    - expression: object.spec.replicas > 0\n",
		"  validations:\n    - expression: object.spec.replicas > 0\n      messageExpression: \"'none: ' + object.metadata.name\"\n", 1)
	tests := []struct {
		name, text, needle string
		on                 []feature.Feature
		checks, returns    string
	}{
		{"a CRD's rule", usesCRD, "has(self.spec)", nil, "`self.spec` is set.", ""},
		{"a CRD's rule with no sentence", usesCRD, "self.ports.all", nil, "", ""},
		{"a CRD's messageExpression", usesCRD, "'too many: ' + string(self", nil, "", message},
		{"a validation", usesVAP, "variables.replicas <= params", nil, "", ""},
		{"a validation's messageExpression", usesVAP, "'too many: ' + string(variables", nil, "", message},
		{"a match condition", usesVAP, "request.namespace", nil, "the policy applies only when `request.namespace` does not equal `'kube-system'`.", ""},
		{"a variable", usesVAP, "object.spec.replicas\"", nil, "", "the value of `variables.replicas`."},
		{"an audit annotation", usesVAP, "string(object.spec.replicas)", nil, "", "the value of the audit annotation `replicas`."},
		{"a mutating policy's match condition", usesMAP, "object.metadata.name", nil, "the policy applies only when `object.metadata.name` does not equal `''`.", ""},
		{"a mutating policy's variable", usesMAP, "\"3\"", nil, "", "the value of `variables.want`."},
		{"an apply configuration", usesMAP, "Object{spec", nil, "", "the apply configuration the policy applies."},
		{"a JSON patch", usesMAP, "[JSONPatch", nil, "", "the JSON patch the policy applies."},
		{"a Kyverno validation", kyverno, "object.spec.replicas > 0", []feature.Feature{feature.Kyverno}, "`object.spec.replicas` is greater than `0`.", ""},
		{"a Kyverno messageExpression", kyverno, "'none: '", []feature.Feature{feature.Kyverno}, "", "the message Kyverno gives when the validation is false."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := assist.Parse(context.Background(), "f.yaml", []byte(tt.text))
			e, ok := doc.Expression(posIn(t, tt.text, tt.needle, 1))
			if !ok {
				t.Fatalf("Expression(%q) = none", tt.needle)
			}
			r, ok := with(t, tt.on...).Rule(doc, e)
			if !ok || r.Checks != tt.checks || r.Returns != tt.returns {
				t.Errorf("Rule(%q) = Checks %q, Returns %q, want %q, %q", e.Text, r.Checks, r.Returns, tt.checks, tt.returns)
			}
		})
	}
}

// TestCompletionDocumentationEscaped checks that completion documents a
// field with its description escaped, as hover does, and ignores a
// Markdown description a file writes under cel-lens's own key (#52).
func TestCompletionDocumentationEscaped(t *testing.T) {
	t.Parallel()
	text := crdWith(`            spec:
              type: object
              x-kubernetes-validations:
                - rule: "self.port > 0"
              properties:
                port:
                  type: integer
                  x-cel-lens-markdown: '[run](command:workbench.action.terminal.new)'
                  description: 'Port [x](command:workbench.action.terminal.new) <img src="https://example.invalid/a.png">'
`)
	items, ok := with(t).Complete(assist.Parse(context.Background(), "f.yaml", []byte(text)), posIn(t, text, "self.port", len("self.")))
	if !ok {
		t.Fatal("Complete(self.) = none")
	}
	const want = `Port \[x\]\(command:workbench.action.terminal.new\) \<img src\="https://example.invalid/a.png"\>`
	for _, it := range items {
		if it.Label == "port" && it.Documentation != want {
			t.Errorf("Complete(self.) port documentation = %q, want %q", it.Documentation, want)
		}
		if it.Label == "port" {
			return
		}
	}
	t.Errorf("Complete(self.) = %+v, want port", items)
}

// TestRuleUsesMacros checks that Uses:, read from the parse it shares with
// Checks:, leaves out the variables of every macro written as a call: one
// or two variables, map with a filter, the transforms and cel.bind, whose
// variable is not bound in its initial value (#53).
func TestRuleUsesMacros(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, expr, want string }{
		{"map, exists and a filtered map", "object.spec.template.spec.containers.map(c, c.name).exists(n, n == params.x) && [1].map(x, x > 0, x * oldObject.y).size() > 0", "`object`, `params`, `oldObject`"},
		{"two variables", "object.metadata.labels.all(k, v, k != request.name && v != '')", "`object`, `request`"},
		{"transforms", "[1].transformList(i, e, e > i).size() > 0 && {'a': 1}.transformMap(k, v, v > 0, v + 1).size() >= 0 && params.x", "`params`"},
		{"cel.bind", "cel.bind(v, object.metadata, v.name != '' && params.x)", "`object`, `params`"},
		{"cel.bind of itself", "cel.bind(params, object, params.metadata.name != '')", "`object`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			text := strings.Replace(usesVAP, "\"variables.replicas <= params.max && variables.replicas > 0 && oldObject == null || namespaceObject.metadata.name != '' && authorizer.group('apps').resource('deployments').check('get').allowed()\"", strconv.Quote(tt.expr), 1)
			doc := assist.Parse(context.Background(), "f.yaml", []byte(text))
			e, ok := doc.Expression(posIn(t, text, tt.expr[:20], 1))
			if !ok {
				t.Fatal("Expression = none")
			}
			r, _ := with(t).Rule(doc, e)
			var got []string
			for _, line := range strings.Split(strings.TrimSpace(r.Uses), "\n") {
				name, _, _ := strings.Cut(strings.TrimPrefix(line, "- "), ":")
				got = append(got, name)
			}
			if g := strings.Join(got, ", "); g != tt.want {
				t.Errorf("Rule(%q) uses %s, want %s (Uses: %q)", tt.expr, g, tt.want, r.Uses)
			}
		})
	}
}
