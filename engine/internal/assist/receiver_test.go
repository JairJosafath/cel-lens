package assist_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// receiverCRD has a rule whose expression is %s, on a spec with a string, a
// list, a map, an object and a field of unknown type.
func receiverCRD(expr string) string {
	return `apiVersion: apiextensions.k8s.io/v1
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
          properties:
            spec:
              type: object
              properties:
                name: {type: string}
                ports:
                  type: array
                  items: {type: integer}
                labels:
                  type: object
                  additionalProperties: {type: string}
                any: {x-kubernetes-preserve-unknown-fields: true}
              x-kubernetes-validations:
                - rule: ` + expr + `
`
}

const (
	stringOnly = "startsWith endsWith matches lowerAscii upperAscii trim"
	listOnly   = "isSorted sum min max"
	macroNames = "all exists exists_one map filter"
)

func TestCompleteReceiver(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, doc, needle string
		offset            int
		want, notWant     []string
		sigs              map[string]string // label -> text its Detail contains
		docs              string            // text every item's Documentation contains, when set
	}{
		{
			name: "CRD string", doc: receiverCRD("self.name.startsWith('a')"), needle: "self.name.starts", offset: len("self.name."),
			want: strings.Fields(stringOnly + " size"), notWant: strings.Fields(listOnly + " " + macroNames + " containsKey"),
			sigs: map[string]string{"size": "string"},
		},
		{
			name: "CRD list", doc: receiverCRD("self.ports.all(p, p > 0)"), needle: "self.ports.all", offset: len("self.ports."),
			want: strings.Fields(macroNames + " size isSorted"), notWant: strings.Fields("startsWith lowerAscii matches"),
			sigs: map[string]string{"size": "list"},
		},
		{
			name: "CRD map", doc: receiverCRD("self.labels.all(k, k != '')"), needle: "self.labels.all", offset: len("self.labels."),
			want: strings.Fields(macroNames + " size"), notWant: strings.Fields("startsWith lowerAscii isSorted"),
			sigs: map[string]string{"size": "map"},
		},
		{
			name: "CRD object", doc: receiverCRD("self.name.startsWith('a')"), needle: "self.name.starts", offset: len("self."),
			want: strings.Fields("name ports labels any"), notWant: strings.Fields("size startsWith all"),
		},
		{
			name: "CRD dyn", doc: receiverCRD("self.any.all(x, true)"), needle: "self.any.all", offset: len("self.any."),
			want: strings.Fields(macroNames), notWant: strings.Fields("startsWith lowerAscii isSorted size"),
			docs: "dyn",
		},
		{
			name: "CRD method result", doc: receiverCRD("self.name.lowerAscii().startsWith('a')"), needle: "lowerAscii().starts", offset: len("lowerAscii()."),
			want: strings.Fields(stringOnly + " size"),
		},
		{
			name: "policy object", doc: matching(deploymentRule, "object.spec.replicas > 1"), needle: "object.spec", offset: len("object."),
			want: strings.Fields("apiVersion kind metadata spec status"), notWant: strings.Fields("size startsWith all exists"),
		},
		{
			name: "policy string", doc: matching(deploymentRule, "object.metadata.name.startsWith('a')"), needle: "name.startsWith", offset: len("name."),
			want: strings.Fields(stringOnly + " size"), notWant: strings.Fields(listOnly + " " + macroNames),
			sigs: map[string]string{"size": "string"},
		},
		{
			name: "policy list", doc: matching(deploymentRule, "object.spec.template.spec.containers.all(c, true)"), needle: "containers.all", offset: len("containers."),
			want: strings.Fields(macroNames + " size"), notWant: strings.Fields("startsWith lowerAscii matches"),
			sigs: map[string]string{"size": "list"},
		},
		{
			name: "policy map", doc: matching(deploymentRule, "object.metadata.labels.all(k, true)"), needle: "labels.all", offset: len("labels."),
			want: strings.Fields(macroNames + " size"), notWant: strings.Fields("startsWith lowerAscii isSorted"),
			sigs: map[string]string{"size": "map"},
		},
		{
			name: "policy dyn", doc: matching("{apiGroups: ['*'], apiVersions: [v1], resources: [pods]}", "object.spec.all(x, true)"), needle: "spec.all", offset: len("spec."),
			want: strings.Fields(macroNames), notWant: strings.Fields("nodeName size startsWith"),
			docs: "dyn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			items, ok := a.Complete(assist.Parse(context.Background(), "f.yaml", []byte(tt.doc)), posIn(t, tt.doc, tt.needle, tt.offset))
			if !ok {
				t.Fatal("Complete = none, want items")
			}
			byLabel := map[string][]assist.Item{}
			for _, it := range items {
				byLabel[it.Label] = append(byLabel[it.Label], it)
			}
			for _, w := range tt.want {
				if len(byLabel[w]) == 0 {
					t.Errorf("Complete lacks %q", w)
				}
			}
			for _, w := range tt.notWant {
				if len(byLabel[w]) > 0 {
					t.Errorf("Complete offers %q, want it absent", w)
				}
			}
			for label, its := range byLabel {
				if len(its) != 1 {
					t.Errorf("Complete offers %q %d times, want once", label, len(its))
				}
			}
			for _, it := range items {
				if tt.docs != "" && !strings.Contains(it.Documentation, tt.docs) {
					t.Errorf("Complete %q docs = %q, want them to say the receiver is %s", it.Label, it.Documentation, tt.docs)
				}
			}
			for label, sig := range tt.sigs {
				if its := byLabel[label]; len(its) == 1 && !strings.Contains(its[0].Detail, sig) {
					t.Errorf("Complete %q detail = %q, want it to contain %q", label, its[0].Detail, sig)
				}
			}
		})
	}
}

func TestCompleteReceiverDocs(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	doc := receiverCRD("self.ports.size() > 0 && self.name.size() > 0")
	var details []string
	for _, needle := range []string{"self.ports.size", "self.name.size"} {
		off := strings.Index(needle, "size")
		items, _ := a.Complete(assist.Parse(context.Background(), "f.yaml", []byte(doc)), posIn(t, doc, needle, off))
		for _, it := range items {
			if it.Label == "size" {
				if it.Documentation == "" || strings.Contains(it.Detail, "\n") {
					t.Errorf("size item = %+v, want docs and a one-line detail", it)
				}
				details = append(details, it.Detail)
			}
		}
	}
	if len(details) != 2 || slices.Equal(details[:1], details[1:]) {
		t.Errorf("size details = %q, want two different signatures", details)
	}
}

func TestSuggestOnDyn(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, expr, word, want string }{
		{"method", "self.any.startsWit('a')", "startsWit", "startsWith"},
		{"another method", "self.any.lowerAsci()", "lowerAsci", "lowerAscii"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := receiverCRD(tt.expr)
			start := posIn(t, doc, tt.word, 0)
			end := start
			end.Column += len(tt.word)
			got, ok := a.Suggest(assist.Parse(context.Background(), "f.yaml", []byte(doc)), start, end)
			if got != tt.want || !ok {
				t.Errorf("Suggest(%q) = %q, %v, want %q", tt.word, got, ok, tt.want)
			}
		})
	}
}
