package lsp

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// stubCosts says the expressions that start on the lines heavy are near or
// over their budget, as Cost: will.
type stubCosts struct{ heavy map[int]bool }

func (s stubCosts) nearOrOver(e extract.Expression) bool { return s.heavy[e.Start.Line] }

// TestDocsSectionCost checks the seam the Cost: paragraph sets: an
// expression near or over its budget gets the page about cost on its
// Docs:, one under it or with no costs known does not, and the function's
// link comes after the rule's pages (#57).
func TestDocsSectionCost(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
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
            - rule: "has(self.metadata)"
`
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	doc := assist.Parse(context.Background(), "crd.yaml", []byte(crd))
	first, _ := doc.Expression(extract.Position{Line: 17, Column: 22})
	second, _ := doc.Expression(extract.Position{Line: 18, Column: 22})
	costs := stubCosts{heavy: map[int]bool{17: true}}
	const resource = "#resource-use-by-validation-functions"
	if got := docsSection(a, doc, first, "", costs); !strings.Contains(got, "#validation-rules") || !strings.Contains(got, resource) {
		t.Errorf("docsSection(a rule near its budget) = %q, want its page and the page about cost", got)
	}
	if got := docsSection(a, doc, second, "", costs); strings.Contains(got, resource) || !strings.Contains(got, "#validation-rules") {
		t.Errorf("docsSection(a rule under its budget) = %q, want its page alone", got)
	}
	if got := docsSection(a, doc, first, "[`has`](x) (y)", nil); strings.Contains(got, resource) || !strings.HasSuffix(got, " · [`has`](x) (y)") {
		t.Errorf("docsSection(no costs, a function) = %q, want the rule's page, then the function's", got)
	}
}
