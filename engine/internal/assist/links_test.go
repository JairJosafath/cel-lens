package assist_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// TestRuleDocs is the first criterion of #57: Docs: links the pinned page
// and heading for each field, its text naming its subject and then the
// release, and with the cost near or over the pages about cost.
func TestRuleDocs(t *testing.T) {
	t.Parallel()
	const (
		crdPage = "/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/"
		vapPage = "/docs/reference/access-authn-authz/validating-admission-policy/"
		mapPage = "/docs/reference/access-authn-authz/mutating-admission-policy/"
		budget  = " · [Runtime cost budget](https://kubernetes.io/docs/reference/using-api/cel/#runtime-cost-budget) (Kubernetes 1.37)"
		kyvPage = "https://release-1-19-0.kyverno.io/docs/policy-types/validating-policy/"
	)
	k8s := func(title, path, anchor string) string {
		if anchor != "" {
			anchor = "#" + anchor
		}
		return "[" + title + "](https://kubernetes.io" + path + anchor + ") (Kubernetes 1.37)"
	}
	kyverno := strings.Replace(vpol("{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}", "object.spec.replicas > 0"),
		"  validations:\n    - expression: object.spec.replicas > 0\n",
		"  validations:\n    - expression: object.spec.replicas > 0\n      messageExpression: \"'none: ' + object.metadata.name\"\n  auditAnnotations:\n    - key: k\n      valueExpression: \"'v'\"\n", 1)
	tests := []struct {
		name, text, needle string
		on                 []feature.Feature
		heavy              bool
		want               string
	}{
		{"a CRD's rule", usesCRD, "has(self.spec)", nil, false, k8s("Validation rules", crdPage, "validation-rules")},
		{"a CRD's rule reading oldSelf", usesCRD, "self.ports.all", nil, false, k8s("Transition rules", crdPage, "transition-rules")},
		{"a CRD's messageExpression", usesCRD, "'too many: ' + string(self", nil, false, k8s("The messageExpression field", crdPage, "the-messageexpression-field")},
		{
			"a CRD's rule near its budget", usesCRD, "self.port > 0", nil, true,
			k8s("Validation rules", crdPage, "validation-rules") + " · " + k8s("Resource use by validation functions", crdPage, "resource-use-by-validation-functions"),
		},
		{"a validation", usesVAP, "variables.replicas <= params", nil, false, k8s("Validation expressions", vapPage, "validation-expression")},
		{"a validation over its budget", usesVAP, "variables.replicas <= params", nil, true, k8s("Validation expressions", vapPage, "validation-expression") + budget},
		{"a validation's messageExpression", usesVAP, "'too many: ' + string(variables", nil, false, k8s("Message expressions", vapPage, "message-expression")},
		{"a match condition", usesVAP, "request.namespace", nil, false, k8s("Match conditions", vapPage, "matching-requests-matchconditions")},
		{"a variable", usesVAP, "object.spec.replicas\"", nil, false, k8s("Variable composition", vapPage, "variable-composition")},
		{"an audit annotation", usesVAP, "string(object.spec.replicas)", nil, false, k8s("Audit annotations", vapPage, "audit-annotations")},
		{"a mutating policy's match condition", usesMAP, "object.metadata.name", nil, false, k8s("MutatingAdmissionPolicy", mapPage, "")},
		{"a mutating policy's variable", usesMAP, "\"3\"", nil, false, k8s("MutatingAdmissionPolicy", mapPage, "")},
		{"an apply configuration", usesMAP, "Object{spec", nil, false, k8s("Apply configuration patches", mapPage, "patch-type-apply-configuration")},
		{"a JSON patch, near its budget", usesMAP, "[JSONPatch", nil, true, k8s("JSON patches", mapPage, "patch-type-json-patch") + budget},
		{"a Kyverno validation", kyverno, "object.spec.replicas > 0", []feature.Feature{feature.Kyverno}, false, "[ValidatingPolicy](" + kyvPage + ") (Kyverno 1.19)"},
		{
			"a Kyverno messageExpression", kyverno, "'none: '",
			[]feature.Feature{feature.Kyverno},
			false,
			"[Messages from messageExpression](" + kyvPage + "#using-messageexpression-to-generate-dynamic-messages) (Kyverno 1.19)",
		},
		{
			"a Kyverno audit annotation", kyverno, "'v'",
			[]feature.Feature{feature.Kyverno},
			true,
			"[Audit annotations](" + kyvPage + "#using-auditannotations-to-add-custom-data) (Kyverno 1.19)",
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
			if got := with(t, tt.on...).RuleDocs(doc, e, tt.heavy); got != tt.want {
				t.Errorf("RuleDocs(%q, heavy %t) =\n%s\nwant\n%s", e.Text, tt.heavy, got, tt.want)
			}
		})
	}
}

// TestRuleDocsVersion checks that the Kubernetes pages are those of the
// version picked (#57).
func TestRuleDocsVersion(t *testing.T) {
	t.Parallel()
	v134, err := kubeversion.Parse("1.34")
	if err != nil {
		t.Fatal(err)
	}
	a, err := assist.New(v134)
	if err != nil {
		t.Fatal(err)
	}
	doc := assist.Parse(context.Background(), "f.yaml", []byte(usesCRD))
	e, _ := doc.Expression(posIn(t, usesCRD, "has(self.spec)", 1))
	want := "[Validation rules](https://v1-34.docs.kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules) (Kubernetes 1.34)"
	if got := a.RuleDocs(doc, e, false); got != want {
		t.Errorf("RuleDocs() in 1.34 = %q, want %q", got, want)
	}
	if got := a.RuleDocs(assist.Parse(context.Background(), "f.yaml", []byte(strings.Replace(usesCRD, "apiextensions.k8s.io/v1", "apiextensions.k8s.io/v1beta1", 1))), e, false); got != "" {
		t.Errorf("RuleDocs(a v1beta1 CRD) = %q, want none: the check does not check it", got)
	}
}

// TestHoverKyvernoFunctionDocs checks that a Kyverno function links its
// library's heading on Kyverno's page of CEL libraries, by its receiver,
// where it had no link before (#57).
func TestHoverKyvernoFunctionDocs(t *testing.T) {
	t.Parallel()
	const libs = "https://release-1-19-0.kyverno.io/docs/policy-types/cel-libraries/"
	tests := []struct{ expr, word, want string }{
		{"resource.Get('v1', 'pods', 'ns', 'web') != null", "Get", "[`Get`, Kyverno resource library](" + libs + "#resource-library) (Kyverno 1.19)"},
		{"http.Get('https://example.invalid') != null", "Get", "[`Get`, Kyverno HTTP library](" + libs + "#http-library) (Kyverno 1.19)"},
		{"isImage('nginx')", "isImage", "[`isImage`, Kyverno image library](" + libs + "#image-library) (Kyverno 1.19)"},
	}
	a := with(t, feature.Kyverno)
	for _, tt := range tests {
		text := vpol("{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}", "\""+tt.expr+"\"")
		h, ok := a.Hover(assist.Parse(context.Background(), "p.yaml", []byte(text)), posIn(t, text, tt.expr, strings.Index(tt.expr, tt.word)+1))
		if !ok || h.Docs != tt.want {
			t.Errorf("Hover(%s in %q).Docs = %q, %t, want %q", tt.word, tt.expr, h.Docs, ok, tt.want)
		}
	}
}
