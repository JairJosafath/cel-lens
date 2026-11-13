package examples

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// widgetCRD is a CustomResourceDefinition of widgets with a status
// subresource, a default, a list map and a rule that does not compile.
const widgetCRD = `apiVersion: apiextensions.k8s.io/v1
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
      subresources: {status: {}}
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              required: [size]
              properties:
                size: {type: integer, maximum: 10}
                mode: {type: string, enum: [a, b], default: a}
                ports:
                  type: array
                  x-kubernetes-list-type: map
                  x-kubernetes-list-map-keys: [name]
                  items:
                    type: object
                    required: [name]
                    properties:
                      name: {type: string, maxLength: 3}
                labels: {type: object, additionalProperties: {type: string, maxLength: 2}}
              x-kubernetes-validations:
                - rule: self.size > nope
            status:
              type: object
              properties:
                ready: {type: boolean}
    - name: v2
      served: false
      storage: false
      schema:
        openAPIV3Schema: {type: object}
`

// gadgetCRD is a cluster-scoped CustomResourceDefinition of gadgets.
const gadgetCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.example.com
spec:
  group: example.com
  names: {plural: gadgets, singular: gadget, kind: Gadget, listKind: GadgetList}
  scope: Cluster
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec: {type: object, properties: {size: {type: integer}}}
`

const crdFile = "/ws/crd.yaml"

// readCRDs reads widgetCRD and gadgetCRD.
func readCRDs(t *testing.T) []*CRD {
	t.Helper()
	crds := Read(t.Context(), crdFile, []byte(widgetCRD+"---\n"+gadgetCRD), kubeversion.Default())
	if len(crds) != 2 {
		t.Fatalf("Read(widgets and gadgets) = %d CRDs, want 2", len(crds))
	}
	return crds
}

func TestRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		text string
		want []string // Kind and versions served
	}{
		{"served versions", widgetCRD, []string{"Widget v1"}},
		{"two CRDs", widgetCRD + "---\n" + gadgetCRD, []string{"Widget v1", "Gadget v1"}},
		{"a rule that does not compile", strings.Replace(widgetCRD, "self.size > nope", "self.size >", 1), []string{"Widget v1"}},
		{"a schema the API server refuses", strings.Replace(widgetCRD, "maximum: 10", "maximum: ten", 1), nil},
		{"a default the schema refuses", strings.Replace(widgetCRD, "default: a", "default: c", 1), nil},
		{"a name the API server refuses", strings.Replace(widgetCRD, "name: widgets.example.com", "name: widgets", 1), nil},
		{"no version served", strings.Replace(gadgetCRD, "served: true", "served: false", 1), nil},
		{"another apiVersion", strings.Replace(gadgetCRD, "apiextensions.k8s.io/v1", "apiextensions.k8s.io/v1beta1", 1), nil},
		{"no CRD", "apiVersion: v1\nkind: ConfigMap\n", nil},
		{"broken YAML", "kind: CustomResourceDefinition\n  : [\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, c := range Read(t.Context(), crdFile, []byte(tc.text), kubeversion.Default()) {
				versions := slices.Sorted(func(yield func(string) bool) {
					for v := range c.versions {
						if !yield(v) {
							return
						}
					}
				})
				got = append(got, c.Kind+" "+strings.Join(versions, " "))
				if c.File != crdFile {
					t.Errorf("Read(%s) file = %q, want %q", tc.name, c.File, crdFile)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Read(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if got := Read(ctx, crdFile, []byte(widgetCRD), kubeversion.Default()); len(got) != 0 {
			t.Errorf("Read(cancelled) = %d CRDs, want none", len(got))
		}
	})
}

// TestReadBesideChecks reads CRDs for one Kubernetes version while CRDs are
// checked for another, as the language server does (#14), and gets what it
// gets alone; -race finds unguarded shared state. Read validates a CRD
// with the Kubernetes libraries, which read the version from a setting of
// the whole process, under check.UnderVersion. What that validation says
// of a CRD without its rules, as Read validates it, is the same for every
// version, so this test cannot see which version it ran for: it shows that
// running beside the checks changes nothing.
func TestReadBesideChecks(t *testing.T) {
	t.Parallel()
	old, err := kubeversion.Parse("1.33")
	if err != nil {
		t.Fatal(err)
	}
	text := []byte(widgetCRD + "---\n" + gadgetCRD + "---\n" + strings.Replace(widgetCRD, "default: a", "default: c", 1))
	alone := Read(t.Context(), crdFile, text, old)
	if len(alone) != 2 {
		t.Fatalf("Read(widgets, gadgets and a refused widget) = %d CRDs, want 2", len(alone))
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				if _, err := check.Check(t.Context(), crdFile, []byte(widgetCRD), check.WithVersion(kubeversion.Default())); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	for range 20 {
		got := Read(t.Context(), crdFile, text, old)
		if len(got) != len(alone) || got[0].Kind != alone[0].Kind || got[1].Kind != alone[1].Kind {
			t.Errorf("Read beside checks = %d CRDs, want %d as alone", len(got), len(alone))
			break
		}
	}
	close(done)
	wg.Wait()
}

// widget is a valid example of widgetCRD named name.
func widget(name string) string {
	return "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: " + name + "}\nspec: {size: 1}\n"
}

func TestFind(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	for _, tc := range []struct {
		name, file, text string
		want             []string // Kind and Line of each example
	}{
		{"a served version", "/ws/a.yaml", widget("a"), []string{"Widget 1"}},
		{"documents of several kinds", "/ws/a.yaml", widget("a") + "---\napiVersion: v1\nkind: ConfigMap\n---\napiVersion: example.com/v1\nkind: Gadget\n", []string{"Widget 1", "Gadget 9"}},
		{"a version not served", "/ws/a.yaml", strings.Replace(widget("a"), "/v1", "/v2", 1), nil},
		{"a version not defined", "/ws/a.yaml", strings.Replace(widget("a"), "/v1", "/v9", 1), nil},
		{"another group", "/ws/a.yaml", strings.Replace(widget("a"), "example.com", "other.com", 1), nil},
		{"the CRD's own file", crdFile, widget("a"), nil},
		{"a kind not a string", "/ws/a.yaml", "apiVersion: example.com/v1\nkind: [Widget]\n", nil},
		{"no mapping", "/ws/a.yaml", "- Widget\n", nil},
		{"after a broken document", "/ws/a.yaml", "a: [\n---\n" + widget("a"), []string{"Widget 3"}},
		{"after comments", "/ws/a.yaml", "# one\n# two\n" + widget("a"), []string{"Widget 3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			objs, err := Find(t.Context(), crds, tc.file, "a.yaml", []byte(tc.text))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, o := range objs {
				got = append(got, fmt.Sprintf("%s %d", o.Kind, o.Line))
				// #15: a problem of the whole example shows at its first key.
				if start, end := o.Head(); start.Line != o.Line || start.Column != 1 || end.Line != o.Line || end.Column != len("apiVersion")+1 {
					t.Errorf("Find(%s), Head() of the example at line %d = %v-%v, want its first key", tc.name, o.Line, start, end)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Find(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := Find(ctx, crds, "/ws/a.yaml", "a.yaml", []byte(widget("a"))); err == nil {
			t.Error("Find(cancelled) error = nil, want the context's")
		}
	})
}

func TestExpectByPath(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	for _, tc := range []struct {
		rel  string
		want Expectation
	}{
		{"config/samples/widget.yaml", Allowed},
		{"testdata/invalid/big.yaml", Refused},
		{"test/Invalid/big.yaml", Refused},
		{"widget-invalid.yaml", Refused},
		{"widget_bad.yaml", Refused},
		{"widget.refused.yaml", Refused},
		{"rejected/widget.yaml", Refused},
		{"denied-widget.yml", Refused},
		{"badge.yaml", Allowed},
		{"invalidated/widget.yaml", Allowed},
		{"samples/widget-valid.yaml", Allowed},
	} {
		t.Run(tc.rel, func(t *testing.T) {
			t.Parallel()
			objs, err := Find(t.Context(), crds, "/ws/"+tc.rel, tc.rel, []byte(widget("a")))
			if err != nil || len(objs) != 1 {
				t.Fatalf("Find(%s) = %d examples, %v, want 1", tc.rel, len(objs), err)
			}
			if got := objs[0].Expect; got != tc.want {
				t.Errorf("Find(%s) expects %v, want %v", tc.rel, got, tc.want)
			}
		})
	}
}

func TestExpectByComment(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	body := widget("a")
	const problem = `this # cel-lens: comment is none of # cel-lens: allowed, # cel-lens: refused and # cel-lens: refused "part of the message"`
	for _, tc := range []struct {
		name, rel, text string
		want            Expectation
		message         string
		problems        []string // line:column-column of each problem
	}{
		{"refused", "a.yaml", "# cel-lens: refused\n" + body, Refused, "", nil},
		{"refused with a message", "a.yaml", `# cel-lens: refused "at most 10"` + "\n" + body, Refused, "at most 10", nil},
		{"an escaped message", "a.yaml", `# cel-lens: refused "say \"no\""` + "\n" + body, Refused, `say "no"`, nil},
		{"allowed over the path", "invalid/a.yaml", "# cel-lens: allowed\n" + body, Allowed, "", nil},
		{"refused over the path", "a.yaml", "#cel-lens:refused\n" + body, Refused, "", nil},
		{"among other comments", "a.yaml", "# A widget.\n# cel-lens: refused\n# More.\n" + body, Refused, "", nil},
		{"indented", "a.yaml", "  # cel-lens: refused\n" + body, Refused, "", nil},
		{"on the --- line", "a.yaml", widget("z") + "--- # cel-lens: refused\n" + body, Refused, "", nil},
		{"after ---, apart from the key", "a.yaml", widget("z") + "---\n# cel-lens: refused\n\n# A widget.\n" + body, Refused, "", nil},
		{"apart from the key, no ---", "a.yaml", "# cel-lens: refused\n\n" + body, Allowed, "", nil},
		{"in the document before", "a.yaml", widget("z") + "# cel-lens: refused\n---\n" + body, Allowed, "", nil},
		{"the first one", "a.yaml", "# cel-lens: refused\n# cel-lens: allowed\n" + body, Refused, "", nil},
		{"unknown", "a.yaml", "# cel-lens: forbidden\n" + body, Allowed, "", []string{"1:1-22"}},
		{"unknown, indented", "a.yaml", "  # cel-lens: Refused   \n" + body, Allowed, "", []string{"1:3-22"}},
		{"unknown on the --- line", "a.yaml", widget("z") + "--- # cel-lens: no\n" + body, Allowed, "", []string{"5:5-19"}},
		{"no space before the message", "a.yaml", `# cel-lens: refused"x"` + "\n" + body, Allowed, "", []string{"1:1-23"}},
		{"an unquoted message", "a.yaml", "# cel-lens: refused at most\n" + body, Allowed, "", []string{"1:1-28"}},
		{"an empty message", "a.yaml", `# cel-lens: refused ""` + "\n" + body, Allowed, "", []string{"1:1-23"}},
		{"empty", "a.yaml", "# cel-lens:\n" + body, Allowed, "", []string{"1:1-12"}},
		{"unknown, then one that reads", "a.yaml", "# cel-lens: no\n# cel-lens: refused\n" + body, Refused, "", []string{"1:1-15"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			objs, err := Find(t.Context(), crds, "/ws/"+tc.rel, tc.rel, []byte(tc.text))
			if err != nil || len(objs) == 0 {
				t.Fatalf("Find(%s) = %d examples, %v, want some", tc.name, len(objs), err)
			}
			o := objs[len(objs)-1]
			if o.Expect != tc.want || o.Message != tc.message {
				t.Errorf("Find(%s) expects %v %q, want %v %q", tc.name, o.Expect, o.Message, tc.want, tc.message)
			}
			var got []string
			for _, d := range o.Diagnostics() {
				got = append(got, fmt.Sprintf("%d:%d-%d", d.Start.Line, d.Start.Column, d.End.Column))
				if d.Message != problem || d.Severity != "error" {
					t.Errorf("Find(%s) problem = %s %q, want an error %q", tc.name, d.Severity, d.Message, problem)
				}
			}
			if !slices.Equal(got, tc.problems) {
				t.Errorf("Find(%s) problems at %q, want %q", tc.name, got, tc.problems)
			}
		})
	}
}

func TestUpdates(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	gadget := func(ns, name string) string {
		return fmt.Sprintf("apiVersion: example.com/v1\nkind: Gadget\nmetadata: {namespace: %q, name: %q}\n", ns, name)
	}
	named := func(ns, name string) string {
		return fmt.Sprintf("apiVersion: example.com/v1\nkind: Widget\nmetadata: {namespace: %q, name: %q}\nspec: {size: 1}\n", ns, name)
	}
	for _, tc := range []struct {
		name string
		docs []string
		want []int // the index of each example's Old, -1 for none
	}{
		{"one", []string{widget("a")}, []int{-1}},
		{"the same twice", []string{widget("a"), widget("a")}, []int{-1, 0}},
		{"three times", []string{widget("a"), widget("a"), widget("a")}, []int{-1, 0, 1}},
		{"another name between", []string{widget("a"), widget("b"), widget("a"), widget("b")}, []int{-1, -1, 0, 1}},
		{"another namespace", []string{named("x", "a"), named("y", "a")}, []int{-1, -1}},
		{"no namespace is default", []string{named("", "a"), named("default", "a")}, []int{-1, 0}},
		{"cluster-scoped, any namespace", []string{gadget("x", "a"), gadget("y", "a")}, []int{-1, 0}},
		{"another kind", []string{widget("a"), gadget("", "a")}, []int{-1, -1}},
		{"another version, the same kind", []string{widget("a"), strings.Replace(widget("a"), "/v1", "/v2", 1), widget("a")}, []int{-1, 0}},
		{"no name", []string{"apiVersion: example.com/v1\nkind: Widget\n", "apiVersion: example.com/v1\nkind: Widget\n"}, []int{-1, -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(strings.Join(tc.docs, "---\n")))
			if err != nil {
				t.Fatal(err)
			}
			got := make([]int, len(objs))
			for i, o := range objs {
				got[i] = slices.Index(objs, o.Old)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Find(%s) updates from %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	const head = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\n"
	for _, tc := range []struct {
		name, text string
		want       []string // line:column-column message of each problem
	}{
		{"valid", head + "spec:\n  size: 1\n", nil},
		{"over the maximum", head + "spec:\n  size: 20\n", []string{"6:3-7 spec.size: Invalid value: 20: spec.size in body should be less than or equal to 10"}},
		{"of the wrong type", head + "spec:\n  size: \"1\"\n", []string{`6:3-7 spec.size: Invalid value: "string": spec.size in body must be of type integer: "string"`}},
		{"required, at the nearest key", head + "spec:\n  mode: a\n", []string{"5:1-5 spec.size: Required value"}},
		{"required, in a flow mapping", head + "spec: {mode: a}\n", []string{"5:1-5 spec.size: Required value"}},
		{"not in the enum", head + "spec:\n  size: 1\n  mode: c\n", []string{`7:3-7 spec.mode: Unsupported value: "c": supported values: "a", "b"`}},
		{"an unknown field", head + "spec:\n  size: 1\n  extra: x\n", []string{`7:3-8 unknown field "spec.extra"`}},
		{"an unknown metadata field", head + "  bogus: 1\nspec:\n  size: 1\n", []string{`5:3-8 unknown field "metadata.bogus"`}},
		{"in a list", head + "spec:\n  size: 1\n  ports:\n    - name: abcd\n", []string{"8:7-11 spec.ports[0].name: Too long: may not be more than 3 bytes"}},
		{"a list map with a key twice", head + "spec:\n  size: 1\n  ports:\n    - name: a\n    - name: a\n", []string{`9:7-11 spec.ports[1]: Duplicate value: {"name":"a"}`}},
		{"a key with dots", head + "spec:\n  size: 1\n  labels:\n    example.com/team: abc\n", []string{"8:5-21 spec.labels.example.com/team: Too long: may not be more than 2 bytes"}},
		{"a quoted key", head + "spec:\n  \"size\": 20\n", []string{"6:3-9 spec.size: Invalid value: 20: spec.size in body should be less than or equal to 10"}},
		{"through a merge key", head + "spec:\n  <<: &base {size: 20}\n", []string{"6:14-18 spec.size: Invalid value: 20: spec.size in body should be less than or equal to 10"}},
		{"through an alias", head + "  annotations: &a {x: abc}\nspec:\n  size: 1\n  labels: *a\n", []string{"5:20-21 spec.labels.x: Too long: may not be more than 2 bytes"}},
		{"a bad name", "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: A\nspec: {size: 1}\n", []string{`4:3-7 metadata.name: Invalid value: "A": a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters, '-' or '.', and must start and end with an alphanumeric character (e.g. 'example.com', regex used for validation is '[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*')`}},
		{"no name", "apiVersion: example.com/v1\nkind: Widget\nspec: {size: 1}\n", []string{"1:1-11 metadata.name: Required value: name or generateName is required"}},
		{"a generateName", "apiVersion: example.com/v1\nkind: Widget\nmetadata: {generateName: w-}\nspec: {size: 1}\n", nil},
		{"metadata no mapping", "apiVersion: example.com/v1\nkind: Widget\nmetadata: [a]\nspec: {size: 1}\n", []string{"3:1-9 json: cannot unmarshal array into Go value of type v1.ObjectMeta"}},
		{"a status, ignored on create", head + "spec: {size: 1}\nstatus: {ready: 3}\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(tc.text))
			if err != nil || len(objs) == 0 {
				t.Fatalf("Find(%s) = %d examples, %v, want some", tc.name, len(objs), err)
			}
			if err := Check(t.Context(), objs); err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, d := range objs[len(objs)-1].Diagnostics() {
				if d.Start.Line != d.End.Line || d.Severity != "error" {
					t.Errorf("Check(%s) problem %+v, want an error on one line", tc.name, d)
				}
				got = append(got, fmt.Sprintf("%d:%d-%d %s", d.Start.Line, d.Start.Column, d.End.Column, d.Message))
			}
			if tc.want == nil {
				tc.want = []string{}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Check(%s) =\n%s\nwant\n%s", tc.name, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

func TestCheckObject(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	text := "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: a}\nspec: {size: 1, extra: x}\nstatus: {ready: true}\n" +
		"---\napiVersion: example.com/v1\nkind: Gadget\nmetadata: {name: g, namespace: x}\n"
	objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(text))
	if err != nil || len(objs) != 2 {
		t.Fatalf("Find = %d examples, %v, want 2", len(objs), err)
	}
	if err := Check(t.Context(), objs); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{
		`map[apiVersion:example.com/v1 kind:Widget metadata:map[name:a namespace:default] spec:map[mode:a size:1]]`,
		`map[apiVersion:example.com/v1 kind:Gadget metadata:map[name:g]]`,
	} {
		if got := fmt.Sprint(objs[i].Object); got != want {
			t.Errorf("Check() object %d = %s, want %s: pruned, defaulted, in its namespace, without the status", i, got, want)
		}
	}
}

// TestCheckOutcome checks what Check keeps for the step that runs the CEL
// rules: the API server's errors, whether it refuses the example while
// decoding it, and whether it would skip the rules.
func TestCheckOutcome(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	const head = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\n"
	for _, tc := range []struct {
		name, text   string
		errors       []string // the type of each error
		decodeFailed bool
		rulesSkipped bool
		object       bool
	}{
		{"valid", head + "spec:\n  size: 1\n", nil, false, false, true},
		{"over the maximum, rules run", head + "spec:\n  size: 20\n", []string{"FieldValueInvalid"}, false, false, true},
		{"not in the enum, rules skipped", head + "spec:\n  size: 1\n  mode: c\n", []string{"FieldValueNotSupported"}, false, true, true},
		{"required, rules skipped", head + "spec:\n  mode: a\n", []string{"FieldValueRequired"}, false, true, true},
		{"of the wrong type, rules skipped", head + "spec:\n  size: \"1\"\n", []string{"FieldValueTypeInvalid"}, false, true, true},
		{"too long, rules skipped", head + "spec:\n  size: 1\n  ports:\n    - name: abcd\n", []string{"FieldValueTooLong"}, false, true, true},
		{"an unknown field, refused while decoding", head + "spec:\n  size: 1\n  extra: x\n", nil, true, false, true},
		{"metadata that does not read", "apiVersion: example.com/v1\nkind: Widget\nmetadata: [a]\nspec: {size: 1}\n", nil, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(tc.text))
			if err != nil || len(objs) != 1 {
				t.Fatalf("Find(%s) = %d examples, %v, want 1", tc.name, len(objs), err)
			}
			if err := Check(t.Context(), objs); err != nil {
				t.Fatal(err)
			}
			o := objs[0]
			var types []string
			for _, fe := range o.Errors {
				types = append(types, string(fe.Type))
			}
			if !slices.Equal(types, tc.errors) || o.DecodeFailed != tc.decodeFailed || o.RulesSkipped != tc.rulesSkipped || (o.Object != nil) != tc.object {
				t.Errorf("Check(%s) errors %q, decode failed %v, rules skipped %v, object %v; want %q, %v, %v, %v",
					tc.name, types, o.DecodeFailed, o.RulesSkipped, o.Object != nil, tc.errors, tc.decodeFailed, tc.rulesSkipped, tc.object)
			}
		})
	}
}

// TestCheckUpdate checks updates. The API server's ratcheting, which lets a
// field left as it was through though the schema now refuses it, cannot
// show here: the object updated is checked against the same schema, and
// when the schema refuses it the update is a create, so no field it
// refuses is ever left as it was. Check passes the API server's ratcheting
// option all the same, as the API server does.
func TestCheckUpdate(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	const head = "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: a}\n"
	for _, tc := range []struct {
		name string
		docs []string
		want []int // the problems of each example
	}{
		{"an update", []string{head + "spec: {size: 1}\n", head + "spec: {size: 2}\n"}, []int{0, 0}},
		{"an update the schema refuses", []string{head + "spec: {size: 1}\n", head + "spec: {size: 20}\n"}, []int{0, 1}},
		// The first is not created, so the second is a create, refused
		// again, with no ratcheting from an object that is not there.
		{"after a refused create", []string{head + "spec: {size: 20}\n", head + "spec: {size: 20}\n"}, []int{1, 1}},
		{"the status kept from the old one", []string{head + "spec: {size: 1}\n", head + "spec: {size: 1}\nstatus: {ready: 3}\n"}, []int{0, 0}},
		{"metadata.name changed", []string{head + "spec: {size: 1}\n", "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: a, namespace: default}\nspec: {size: 1}\n"}, []int{0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(strings.Join(tc.docs, "---\n")))
			if err != nil {
				t.Fatal(err)
			}
			if err := Check(t.Context(), objs); err != nil {
				t.Fatal(err)
			}
			got := make([]int, len(objs))
			for i, o := range objs {
				got[i] = len(o.Problems)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Check(%s) problems = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(widget("a")))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := Check(ctx, objs); err == nil {
			t.Error("Check(cancelled) error = nil, want the context's")
		}
	})
}

func TestExpectedRefusal(t *testing.T) {
	t.Parallel()
	const blockWidget = "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: a}\nspec:\n  size: 1\n  extra: 1\n"
	crds := readCRDs(t)
	for _, tc := range []struct {
		name, rel, text string
		want            int  // diagnostics
		bySchema        bool // RefusedBySchema
	}{
		{"allowed, refused by the schema", "a.yaml", blockWidget, 1, true},
		{"refused by its path, refused by the schema", "invalid/a.yaml", blockWidget, 0, true},
		{"refused by its comment, refused by the schema", "a.yaml", "# cel-lens: refused \"other\"\n" + blockWidget, 0, false},
		{"refused by its comment, with the schema's message", "a.yaml", "# cel-lens: refused \"unknown field\"\n" + blockWidget, 0, true},
		// The schema lets it through: the rules, which a later step runs,
		// are what should refuse it.
		{"refused, let through by the schema", "invalid/a.yaml", widget("a"), 0, false},
		{"a bad comment on one refused", "invalid/a.yaml", "# cel-lens: nope\n" + blockWidget, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			objs, err := Find(t.Context(), crds, "/ws/"+tc.rel, tc.rel, []byte(tc.text))
			if err != nil || len(objs) != 1 {
				t.Fatalf("Find(%s) = %d examples, %v, want 1", tc.name, len(objs), err)
			}
			if err := Check(t.Context(), objs); err != nil {
				t.Fatal(err)
			}
			if got := objs[0].Diagnostics(); len(got) != tc.want {
				t.Errorf("Diagnostics(%s) = %+v, want %d", tc.name, got, tc.want)
			}
			if got := objs[0].RefusedBySchema(); got != tc.bySchema {
				t.Errorf("RefusedBySchema(%s) = %t, want %t", tc.name, got, tc.bySchema)
			}
		})
	}
}

// FuzzFind checks that no text makes Find or Check fail but on ctx.
func FuzzFind(f *testing.F) {
	crds := Read(context.Background(), crdFile, []byte(widgetCRD), kubeversion.Default())
	for _, seed := range []string{
		widget("a"),
		"# cel-lens: refused \"x\"\n" + widget("a"),
		"--- # cel-lens: allowed\n" + widget("a") + "---\n" + widget("a"),
		"apiVersion: example.com/v1\nkind: Widget\nmetadata: &m {name: a}\nspec: {<<: *m}\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(text))
		if err != nil {
			t.Fatalf("Find(%q) error = %v, want nil", text, err)
		}
		if err := Check(t.Context(), objs); err != nil {
			t.Fatalf("Check(%q) error = %v, want nil", text, err)
		}
	})
}
