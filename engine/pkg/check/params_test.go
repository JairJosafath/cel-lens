package check_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// paramsPolicy returns a ValidatingAdmissionPolicy over deployments with the
// given paramKind line (a flow mapping, or "" for none) and expression.
func paramsPolicy(paramKind, expression string) string {
	pk := ""
	if paramKind != "" {
		pk = "  paramKind: " + paramKind + "\n"
	}
	return `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
` + pk + `  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  validations:
    - expression: "` + expression + `"
`
}

const widgetCRD = `apiVersion: apiextensions.k8s.io/v1
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
          properties:
            spec:
              type: object
              properties:
                maxReplicas: {type: integer}
                labels:
                  type: array
                  items: {type: string}
    - name: v2
      served: true
      storage: false
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                limit: {type: integer}
`

const widgetKind = "{apiVersion: example.com/v1, kind: Widget}"

func loadCRDs(t *testing.T, files map[string]string) *check.CRDs {
	t.Helper()
	var c check.CRDs
	for name, text := range files {
		c.Set(context.Background(), name, []byte(text))
	}
	return &c
}

// TestParamsType checks that params is typed from the policy's paramKind, and
// that a field it lacks gets the warning of status.typeChecking, naming the
// kind the policy matches.
func TestParamsType(t *testing.T) {
	t.Parallel()
	crds := loadCRDs(t, map[string]string{"widget.yaml": widgetCRD})
	tests := []struct {
		name, doc string
		crds      *check.CRDs
		want      []warning
	}{
		{name: "built-in field", doc: paramsPolicy("{apiVersion: v1, kind: ConfigMap}", "params.data['k'] == 'v'")},
		{
			name: "built-in wrong field", doc: paramsPolicy("{apiVersion: v1, kind: ConfigMap}", "params.nope == 1"),
			want: []warning{{"apps/v1 Deployment: undefined field 'nope'", "nope"}},
		},
		{name: "custom resource field", doc: paramsPolicy(widgetKind, "object.spec.replicas <= params.spec.maxReplicas"), crds: crds},
		{
			name: "custom resource wrong field", doc: paramsPolicy(widgetKind, "object.spec.replicas <= params.spec.maxReplica"), crds: crds,
			want: []warning{{"apps/v1 Deployment: undefined field 'maxReplica'", "maxReplica"}},
		},
		{
			name: "custom resource wrong type", doc: paramsPolicy(widgetKind, "params.spec.maxReplicas == 'a'"), crds: crds,
			want: []warning{{"apps/v1 Deployment: found no matching overload for '_==_' applied to '(int, string)'", "=="}},
		},
		{name: "full metadata", doc: paramsPolicy(widgetKind, "has(params.metadata.labels) && params.metadata.labels['a'] == 'b' && params.metadata.namespace == 'x' && params.kind == 'Widget'"), crds: crds},
		{name: "rest of ObjectMeta", doc: paramsPolicy(widgetKind, "params.metadata.ownerReferences.size() == 0 && params.metadata.selfLink == '' && params.metadata.managedFields.size() == 0"), crds: crds},
		{
			name: "metadata wrong field", doc: paramsPolicy(widgetKind, "params.metadata.nope == 'x'"), crds: crds,
			want: []warning{{"apps/v1 Deployment: undefined field 'nope'", "nope"}},
		},
		{name: "the named version", doc: paramsPolicy("{apiVersion: example.com/v2, kind: Widget}", "params.spec.limit > 1"), crds: crds},
		{
			name: "another version's field", doc: paramsPolicy("{apiVersion: example.com/v2, kind: Widget}", "params.spec.maxReplicas > 1"), crds: crds,
			want: []warning{{"apps/v1 Deployment: undefined field 'maxReplicas'", "maxReplicas"}},
		},
		{
			name: "wrong on object and params", doc: paramsPolicy(widgetKind, "object.spec.replicaz <= params.spec.maxReplica"), crds: crds,
			want: []warning{{"apps/v1 Deployment: undefined field 'replicaz'", "replicaz"}, {"apps/v1 Deployment: undefined field 'maxReplica'", "maxReplica"}},
		},
		{
			name: "wrong on object only", doc: paramsPolicy(widgetKind, "object.spec.replicaz <= params.spec.maxReplicas"), crds: crds,
			want: []warning{{"apps/v1 Deployment: undefined field 'replicaz'", "replicaz"}},
		},
		{name: "kind not in the CRDs stays dyn", doc: paramsPolicy(widgetKind, "params.spec.nope == 1")},
		{name: "unknown version stays dyn", doc: paramsPolicy("{apiVersion: example.com/v3, kind: Widget}", "params.spec.nope == 1"), crds: crds},
		{name: "unknown built-in stays dyn", doc: paramsPolicy("{apiVersion: v1, kind: Nothing}", "params.nope == 1")},
		{name: "list kind stays dyn", doc: paramsPolicy("{apiVersion: v1, kind: ConfigMapList}", "params.nope == 1")},
		{name: "bad apiVersion stays dyn", doc: paramsPolicy("{apiVersion: a/b/c, kind: ConfigMap}", "params.nope == 1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(context.Background(), "policy.yaml", []byte(tt.doc), check.WithCRDs(tt.crds), allOn())
			if err != nil {
				t.Fatalf("Check(%s) error: %v", tt.name, err)
			}
			if got := texts(tt.doc, diags); !slices.Equal(got, tt.want) {
				t.Errorf("Check(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestParamsOncePerMatchedKind checks that a wrong params field of a policy
// that matches two kinds gets one warning per kind, as status.typeChecking.
func TestParamsOncePerMatchedKind(t *testing.T) {
	t.Parallel()
	doc := strings.Replace(paramsPolicy("{apiVersion: v1, kind: ConfigMap}", "params.dataa == 'x'"),
		"resources: [deployments]}", "resources: [deployments]}\n      - {apiGroups: [apps], apiVersions: [v1], resources: [statefulsets]}", 1)
	diags, err := check.Check(context.Background(), "policy.yaml", []byte(doc), allOn())
	if err != nil {
		t.Fatal(err)
	}
	want := []warning{{"apps/v1 Deployment: undefined field 'dataa'", "dataa"}, {"apps/v1 StatefulSet: undefined field 'dataa'", "dataa"}}
	if got := texts(doc, diags); !slices.Equal(got, want) {
		t.Errorf("Check(two kinds) = %v, want %v", got, want)
	}
}

// TestKindlessWarningPerMatchedKind checks that a warning that depends on
// neither object nor params is one line per matched kind, naming that kind and
// never the params kind, as status.typeChecking writes it.
func TestKindlessWarningPerMatchedKind(t *testing.T) {
	t.Parallel()
	const msg = "unexpected failed resolution of 'kubernetes.authorization.Authorizer'"
	both := strings.Replace(paramsPolicy("{apiVersion: v1, kind: ConfigMap}", "authorizer.requestResource.check('update').allowed()"),
		"resources: [deployments]}", "resources: [deployments]}\n      - {apiGroups: [apps], apiVersions: [v1], resources: [statefulsets]}", 1)
	one := paramsPolicy("{apiVersion: v1, kind: ConfigMap}", "authorizer.requestResource.check('update').allowed()")
	tests := []struct {
		name, doc string
		want      []warning
	}{
		{"two kinds", both, []warning{{"apps/v1 Deployment: " + msg, "requestResource"}, {"apps/v1 StatefulSet: " + msg, "requestResource"}}},
		{"one kind", one, []warning{{"apps/v1 Deployment: " + msg, "requestResource"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags, err := check.Check(context.Background(), "policy.yaml", []byte(tt.doc), allOn())
			if err != nil {
				t.Fatal(err)
			}
			if got := texts(tt.doc, diags); !slices.Equal(got, tt.want) {
				t.Errorf("Check(authorizer) = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestParamsWithoutParamKind checks the result when a policy uses params with
// no spec.paramKind. The API server's admission compiler declares params only
// when the policy has one (createEnvForOpts in
// k8s.io/apiserver/pkg/admission/plugin/cel/compile.go: "if opts.HasParams"),
// so cel-go reports the undeclared reference, as an error.
func TestParamsWithoutParamKind(t *testing.T) {
	t.Parallel()
	doc := paramsPolicy("", "params.spec.max > 1")
	diags, err := check.Check(context.Background(), "policy.yaml", []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := texts(doc, diags), []warning{{"undeclared reference to 'params' (in container '')", "params"}}; !slices.Equal(got, want) {
		t.Errorf("Check(params without paramKind) = %v, want %v", got, want)
	}
	if len(diags) != 1 || diags[0].Severity != check.SeverityError {
		t.Errorf("Check(params without paramKind) = %v, want one error", diags)
	}
}

// TestCRDsSet checks how the set follows the files it is given.
func TestCRDsSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var c check.CRDs
	if _, ok := c.Schema("example.com/v1", "Widget"); ok {
		t.Fatal("empty set has Widget")
	}
	if !c.Set(ctx, "a.yaml", []byte(widgetCRD)) {
		t.Error("Set(new CRD) = false, want true")
	}
	if c.Set(ctx, "a.yaml", []byte(widgetCRD)) {
		t.Error("Set(same CRD) = true, want false")
	}
	if _, ok := c.Schema("example.com/v1", "Widget"); !ok {
		t.Error("Schema(Widget v1) not found after Set")
	}
	changed := strings.Replace(widgetCRD, "maxReplicas", "maxCount", 1)
	if !c.Set(ctx, "a.yaml", []byte(changed)) {
		t.Error("Set(changed CRD) = false, want true")
	}
	if c.Set(ctx, "b.yaml", []byte("kind: ConfigMap\n")) {
		t.Error("Set(file without a CRD) = true, want false")
	}
	if !c.Set(ctx, "a.yaml", []byte("not: a crd\n")) {
		t.Error("Set(CRD removed from the file) = false, want true")
	}
	if _, ok := c.Schema("example.com/v1", "Widget"); ok {
		t.Error("Schema(Widget v1) found after the CRD left its file")
	}
	c.Set(ctx, "a.yaml", []byte(widgetCRD))
	if !c.Forget("a.yaml") || c.Forget("a.yaml") {
		t.Error("Forget twice = want true, then false")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if c.Set(cancelled, "a.yaml", []byte(widgetCRD)) {
		t.Error("Set(cancelled) = true, want false")
	}
	var nilSet *check.CRDs
	if _, ok := nilSet.Schema("example.com/v1", "Widget"); ok {
		t.Error("nil set has Widget")
	}
}
