package lsp_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ruleCRD is a CRD of widgets whose rules each show one kind of result: a
// limit on spec.size with its own message and fieldPath, an immutable
// spec.size that needs oldSelf, a rule on each port, and one that does not
// compile.
const ruleCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
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
                size: {type: integer}
                ports:
                  type: array
                  items:
                    type: object
                    properties:
                      name: {type: string}
                    x-kubernetes-validations:
                      - rule: self.name.size() <= 3
                        messageExpression: "'port name ' + self.name + ' is too long'"
              x-kubernetes-validations:
                - rule: self.size <= 10
                  message: at most 10
                  fieldPath: .size
                - rule: self.size == oldSelf.size
                  message: size is immutable
                - rule: self.nope > 1
`

// resultsAnswer is the answer of cel-lens.exampleResults, as a client reads
// it.
type resultsAnswer struct {
	KubernetesVersion string `json:"kubernetesVersion"`
	Rules             []struct {
		Document, Version, Path, Rule string
		Index                         int
		Location                      struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct{ Line, Character int }
			} `json:"range"`
		}
		Results []ruleResult
	}
}

// ruleResult is one result of a rule, as a client reads it.
type ruleResult struct {
	Example struct {
		URI, File, APIVersion, Kind, Name, Operation, Expect, Message string
		Line                                                          int
	}
	Result   string
	Refusals []struct{ Path, Message string }
	Ignored  []string
	Ran      []string
	Reason   string
	Error    string
	Expected bool
	Wrong    string
}

// short is a result as the tests compare it: the example's file and line,
// the operation, the verdict, the refusals' messages or the reason or the
// error, and whether it is expected or where it is wrong.
func (r ruleResult) short() string {
	parts := []string{r.Example.File + ":" + itoa(r.Example.Line), r.Example.Operation, r.Result}
	for _, f := range r.Refusals {
		parts = append(parts, f.Message)
	}
	if r.Reason != "" {
		parts = append(parts, r.Reason)
	}
	if r.Error != "" {
		parts = append(parts, r.Error)
	}
	if r.Expected {
		parts = append(parts, "expected")
	} else {
		parts = append(parts, "wrong at "+r.Wrong)
	}
	return strings.Join(parts, " | ")
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// exampleResults runs cel-lens.exampleResults with args and returns its
// answer.
func (c *client) exampleResults(args any) resultsAnswer {
	c.t.Helper()
	_, raw, rerr := c.command("cel-lens.exampleResults", args, nil)
	if rerr != nil {
		c.t.Fatalf("cel-lens.exampleResults(%v) error = %+v", args, rerr)
	}
	var a resultsAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		c.t.Fatalf("cel-lens.exampleResults(%v) = %s: %v", args, raw, err)
	}
	return a
}

// TestExampleResults checks that cel-lens.exampleResults answers, for a
// file of CRDs, each rule, named as the line above it names it, with its
// result on each example: allowed, refused with the API server's message
// and field path, each item of a list on its own, not run with the reason,
// or an error, a create or an update, and whether it is what the example
// expects; and, given a rule's location, that rule's alone. The experimental
// features are off: the results need none.
func TestExampleResults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crdPath := filepath.Join(dir, "crd.yaml")
	writeIn(t, crdPath, ruleCRD)
	writeIn(t, filepath.Join(dir, "samples", "ports.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: p}\nspec:\n  size: 3\n  ports: [{name: http}, {name: dns}, {name: https}]\n")
	writeIn(t, filepath.Join(dir, "samples", "widget.yaml"), `apiVersion: example.com/v1
kind: Widget
metadata: {name: w}
spec:
  size: 3
  ports: [{name: dns}]
---
apiVersion: example.com/v1
kind: Widget
metadata: {name: w}
spec: {size: 4}
`)
	writeIn(t, filepath.Join(dir, "testdata", "invalid", "big.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: big}\nspec: {size: 11}\n")
	writeIn(t, filepath.Join(dir, "testdata", "small.yaml"), "# cel-lens: refused \"at most 10\"\napiVersion: example.com/v1\nkind: Widget\nmetadata: {name: small}\nspec: {size: 1}\n")

	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": fileURI(dir), "name": "ws"}}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	crdURI := fileURI(crdPath)
	got := c.exampleResults(map[string]any{"uri": crdURI})
	if got.KubernetesVersion != "1.37" {
		t.Errorf("kubernetesVersion = %q, want 1.37", got.KubernetesVersion)
	}
	type rule struct{ document, version, path, rule string }
	var rules []rule
	results := map[string][]string{}
	for _, r := range got.Rules {
		rules = append(rules, rule{r.Document, r.Version, r.Path, r.Rule})
		for _, res := range r.Results {
			results[r.Rule] = append(results[r.Rule], res.short())
		}
	}
	wantRules := []rule{
		{"CustomResourceDefinition widgets.example.com", "v1", "spec.ports[]", "self.name.size() <= 3"},
		{"CustomResourceDefinition widgets.example.com", "v1", "spec", "self.size <= 10"},
		{"CustomResourceDefinition widgets.example.com", "v1", "spec", "self.size == oldSelf.size"},
		{"CustomResourceDefinition widgets.example.com", "v1", "spec", "self.nope > 1"},
	}
	if !reflect.DeepEqual(rules, wantRules) {
		t.Errorf("rules = %q, want %q", rules, wantRules)
	}
	wantResults := map[string][]string{
		"self.name.size() <= 3": {
			"samples/ports.yaml:1 | create | refused | spec.ports[0]: Invalid value: port name http is too long | spec.ports[2]: Invalid value: port name https is too long | wrong at rule",
			"samples/widget.yaml:1 | create | allowed | expected",
			"samples/widget.yaml:8 | update | not run | the rule's field is absent from the example | expected",
			"testdata/invalid/big.yaml:1 | create | not run | the rule's field is absent from the example | expected",
			"testdata/small.yaml:2 | create | not run | the rule's field is absent from the example | wrong at example",
		},
		"self.size <= 10": {
			"samples/ports.yaml:1 | create | allowed | expected",
			"samples/widget.yaml:1 | create | allowed | expected",
			"samples/widget.yaml:8 | update | allowed | expected",
			"testdata/invalid/big.yaml:1 | create | refused | spec.size: Invalid value: at most 10 | expected",
			"testdata/small.yaml:2 | create | allowed | wrong at example",
		},
		"self.size == oldSelf.size": {
			"samples/ports.yaml:1 | create | not run | the example is not an update and the rule needs oldSelf | expected",
			"samples/widget.yaml:1 | create | not run | the example is not an update and the rule needs oldSelf | expected",
			"samples/widget.yaml:8 | update | refused | spec: Invalid value: size is immutable | wrong at rule",
			"testdata/invalid/big.yaml:1 | create | not run | the example is not an update and the rule needs oldSelf | expected",
			"testdata/small.yaml:2 | create | not run | the example is not an update and the rule needs oldSelf | wrong at example",
		},
	}
	for rule, want := range wantResults {
		if !reflect.DeepEqual(results[rule], want) {
			t.Errorf("results of %s =\n%s\nwant\n%s", rule, strings.Join(results[rule], "\n"), strings.Join(want, "\n"))
		}
	}
	for _, res := range results["self.nope > 1"] {
		if !strings.Contains(res, "not run | the API server refuses the CustomResourceDefinition for this rule, so it validates no object with it: undefined field 'nope'") {
			t.Errorf("result of self.nope > 1 = %s, want not run as the API server refuses the CRD for it", res)
		}
	}
	ports := got.Rules[0].Results[0]
	if want := []string{"spec.ports[0]", "spec.ports[1]", "spec.ports[2]"}; !reflect.DeepEqual(ports.Ran, want) {
		t.Errorf("ran = %q, want %q", ports.Ran, want)
	}
	if ports.Refusals[1].Path != "spec.ports[2]" {
		t.Errorf("second refusal's path = %q, want spec.ports[2]", ports.Refusals[1].Path)
	}
	if e := ports.Example; e.URI != fileURI(filepath.Join(dir, "samples", "ports.yaml")) || e.APIVersion != "example.com/v1" || e.Kind != "Widget" || e.Name != "p" || e.Expect != "allowed" {
		t.Errorf("example = %+v, want samples/ports.yaml's p, expecting to be allowed", e)
	}

	// The location of the immutable rule, as the line above it gives it,
	// asks for that rule alone.
	at := got.Rules[2].Location
	one := c.exampleResults(map[string]any{"uri": at.URI, "range": map[string]any{"start": at.Range.Start, "end": at.Range.Start}})
	if len(one.Rules) != 1 || one.Rules[0].Rule != "self.size == oldSelf.size" || len(one.Rules[0].Results) != 5 {
		t.Errorf("exampleResults at %+v = %+v, want the immutable rule's 5 results", at, one.Rules)
	}
}
