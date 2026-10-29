package assist_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

const (
	celSpec     = "(https://github.com/google/cel-spec/blob/master/doc/langdef.md)"
	celRegex    = "(https://github.com/google/cel-spec/blob/master/doc/langdef.md#regular-expressions)"
	k8sQuantity = "(https://kubernetes.io/docs/reference/using-api/cel/#kubernetes-quantity-library)"
	k8sCEL      = "(https://kubernetes.io/docs/reference/using-api/cel/)"
	k8sList     = "(https://kubernetes.io/docs/reference/using-api/cel/#kubernetes-list-library)"
	celSize     = "(https://github.com/google/cel-spec/blob/master/doc/langdef.md#list-operators)"
	celString   = "(https://github.com/google/cel-spec/blob/master/doc/langdef.md#string-functions)"
	celMacros   = "(https://github.com/google/cel-spec/blob/master/doc/langdef.md#presence-and-comprehension-macros)"
	celGoPage   = "https://pkg.go.dev/github.com/google/cel-go/cel#hdr-"
)

// celGoLink is the Markdown link a hover has to the cel-go section called name.
func celGoLink(name string) string {
	return "[CEL optional values](" + celGoPage + name + "-OptionalTypes)"
}

// hoverIn hovers on the first rune of word in a policy whose only validation
// is expr, with the CEL environment of Kubernetes version v.
func hoverIn(t *testing.T, v kubeversion.Version, expr, word string) (string, bool) {
	t.Helper()
	a, err := assist.New(v, allOn())
	if err != nil {
		t.Fatal(err)
	}
	const head = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  name: p\nspec:\n  validations:\n    - expression: "
	data := head + expr + "\n"
	col := len("    - expression: ") + strings.Index(expr, word) + 1
	got, ok := a.Hover(assist.Parse(context.Background(), "p.yaml", []byte(data)), extract.Position{Line: 7, Column: col})
	return got.Markdown, ok
}

func TestHoverFunctionDocs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, expr, word string
		want             []string
	}{
		{"size", "size(object.items) > 0", "size", []string{"size(list(<A>)) -> int", "```cel\nsize([1, 2, 3]) // -> 3\n```", celSize}},
		{"matches", "object.name.matches('^a')", "matches", []string{"string.matches(string) -> bool", "'app-1'.matches('^app-[0-9]+$') // -> true", "[CEL language definition]" + celRegex}},
		{"startsWith", "object.name.startsWith('a')", "startsWith", []string{"string.startsWith(string) -> bool", "'kubernetes'.startsWith('kube') // -> true", celString}},
		{"has", "has(object.spec)", "has", []string{"has(e.f) -> bool", "has({'a': 1}.a) // -> true", celSpec}},
		{"all", "object.items.all(i, i > 0)", "all", []string{"range.all(x, predicate) -> bool", "[1, 2, 3].all(x, x > 0) // -> true", celMacros}},
		{"exists", "object.items.exists(i, i > 0)", "exists", []string{"range.exists(x, predicate) -> bool", "// -> true", celSpec}},
		{"exists_one", "object.items.exists_one(i, i > 0)", "exists_one", []string{"range.exists_one(x, predicate) -> bool", "// -> true", celSpec}},
		{"map", "object.items.map(i, i)", "map", []string{"range.map(x, transform) -> list", "range.map(x, filter, transform) -> list", "[1, 2, 3].map(x, x * 2) // -> [2, 4, 6]", celSpec}},
		{"filter", "object.items.filter(i, i > 0)", "filter", []string{"range.filter(x, predicate) -> list", "// -> [2, 3]", celSpec}},
		{"all two variables", "object.items.all(i, v, v > 0)", "all", []string{"range.all(x, predicate) -> bool", "range.all(i, v, predicate) -> bool", celMacros}},
		{"existsOne", "object.items.existsOne(i, v, v > 0)", "existsOne", []string{"range.existsOne(i, v, predicate) -> bool", "[1, 2, 3].existsOne(i, v, v > 2) // -> true", k8sCEL}},
		{"transformList", "object.items.transformList(i, v, v)", "transformList", []string{"range.transformList(i, v, transform) -> list", "range.transformList(i, v, filter, transform) -> list", "// -> [2, 4, 6]", k8sCEL}},
		{"optMap", "object.opt.optMap(x, x)", "optMap", []string{"opt.optMap(x, transform) -> optional", "// -> 4", celGoLink("OptMap")}},
		{"optFlatMap", "object.opt.optFlatMap(x, optional.of(x))", "optFlatMap", []string{"opt.optFlatMap(x, transform) -> optional", celGoLink("OptFlatMap")}},
		{"hasValue", "object.opt.hasValue()", "hasValue", []string{"optional_type(<V>).hasValue() -> bool", celGoLink("HasValue")}},
		{"value", "object.opt.value()", "value", []string{"optional_type(<V>).value() -> <V>", celGoLink("Value")}},
		{"or", "object.opt.or(optional.none())", "or", []string{"optional_type(<V>).or(optional_type(<V>)) -> optional_type(<V>)", celGoLink("Or")}},
		{"optional.of", "optional.of(1).hasValue()", "of", []string{"optional.of(<V>) -> optional_type(<V>)", celGoLink("Optional_Of")}},
		{"optional.none", "optional.none().hasValue()", "none", []string{"optional.none() -> optional_type(<V>)", celGoLink("Optional_None")}},
		{"isSorted", "object.items.isSorted()", "isSorted", []string{"list(int).isSorted() -> bool", "[1, 2, 3].isSorted() // -> true", k8sCEL}},
		{"sum", "object.items.sum()", "sum", []string{"[1, 2, 3].sum() // -> 6", k8sList}},
		{"isLessThan", "quantity(object.a).isLessThan(quantity(object.b))", "isLessThan", []string{"isLessThan", k8sQuantity}},
		{"orValue", "object.opt.orValue(0)", "orValue", []string{"optional.none().orValue(0) // -> 0", celGoLink("OrValue")}},
		{"quantity", "quantity(object.mem).isInteger()", "quantity", []string{"quantity(string) -> kubernetes.Quantity", "quantity('500Mi').asInteger() // -> 524288000", "[Kubernetes CEL reference]" + k8sQuantity}},
		{"isURL", "isURL(object.url)", "isURL", []string{"isURL(string) -> bool", "isURL('https://example.com') // -> true", k8sCEL}},
		{"ip", "ip(object.addr).isLoopback()", "ip", []string{"ip(string) -> net.IP", "ip('::1').family() // -> 6", k8sCEL}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := hoverIn(t, kubeversion.Default(), tt.expr, tt.word)
			if !ok {
				t.Fatalf("Hover(%q) = none, want a hover", tt.expr)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("Hover(%q) = %q, want it to contain %q", tt.expr, got, want)
				}
			}
		})
	}
}

func TestHoverFunctionByVersion(t *testing.T) {
	t.Parallel()
	v133, err := kubeversion.Parse("1.33")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := hoverIn(t, v133, "semver(object.v).major() > 1", "semver"); ok {
		t.Errorf("Hover(semver) in Kubernetes 1.33 = %q, want none: 1.33 lacks semver", got)
	}
	if got, ok := hoverIn(t, v133, "object.items.sortBy(x, x)", "sortBy"); ok {
		t.Errorf("Hover(sortBy) in Kubernetes 1.33 = %q, want none: 1.33 lacks sortBy", got)
	}
	if got, ok := hoverIn(t, kubeversion.Default(), "object.items.sortBy(x, x)", "sortBy"); !ok || !strings.Contains(got, "list.sortBy(x, key) -> list") {
		t.Errorf("Hover(sortBy) in Kubernetes %s = %q, %v, want its form", kubeversion.Default(), got, ok)
	}
	if got, ok := hoverIn(t, kubeversion.Default(), "semver(object.v).major() > 1", "semver"); !ok || !strings.Contains(got, "semver(string)") {
		t.Errorf("Hover(semver) in Kubernetes %s = %q, %v, want its signature", kubeversion.Default(), got, ok)
	}
}

func TestHoverMarksCalledOverload(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	data := load(t, "crd.yaml")
	got, ok := a.Hover(assist.Parse(context.Background(), "crd.yaml", []byte(data)), posIn(t, data, "startsWith('w')", 2))
	if want := "string.startsWith(string) -> bool // this call"; !ok || !strings.Contains(got.Markdown, want) {
		t.Errorf("Hover(self.name.startsWith) = %q, %v, want it to contain %q", got.Markdown, ok, want)
	}
	// With the receiver's type unknown no overload is marked.
	if got, ok := hoverIn(t, kubeversion.Default(), "object.name.startsWith('a')", "startsWith"); !ok || strings.Contains(got, "this call") {
		t.Errorf("Hover(object.name.startsWith) = %q, %v, want a hover with no overload marked", got, ok)
	}
}

func TestHoverMarksCallAfterRawString(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	data := load(t, "macros.yaml")
	got, ok := a.Hover(assist.Parse(context.Background(), "macros.yaml", []byte(data)), posIn(t, data, `string(r'\').startsWith('a')`, 14))
	if want := "string.startsWith(string) -> bool // this call"; !ok || !strings.Contains(got.Markdown, want) {
		t.Errorf("Hover(string(r'\\').startsWith) = %q, %v, want it to contain %q", got.Markdown, ok, want)
	}
}

func TestHoverMarksCallAfterTripleQuotedString(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	data := load(t, "macros.yaml")
	got, ok := a.Hover(assist.Parse(context.Background(), "macros.yaml", []byte(data)), posIn(t, data, `string(r"""\""").startsWith('a')`, 20))
	if want := "string.startsWith(string) -> bool // this call"; !ok || !strings.Contains(got.Markdown, want) {
		t.Errorf("Hover(string(r\"\"\"\\\"\"\").startsWith) = %q, %v, want it to contain %q", got.Markdown, ok, want)
	}
}

func TestHoverMarksOverloadByTypes(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
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
                name:
                  type: string
                sizes:
                  type: array
                  items:
                    type: integer
                weights:
                  type: array
                  items:
                    type: number
                a:
                  type: string
                b:
                  type: string
                labels:
                  type: object
                  additionalProperties:
                    type: string
              x-kubernetes-validations:
                - rule: "self.sizes.sum() > 0 && size(self.name) > 0 && self.weights.sum() > 0.0"
                - rule: "quantity(self.a).isLessThan(quantity(self.b))"
                - rule: "self.?name.orValue('d') == 'd'"
                - rule: "self.labels.all(k, k.startsWith('a'))"
                - rule: "self.name.matches('^a')"
                - rule: "size(string('é')) > 0"
                - rule: "size(self.labels['éé']) > 0 && self.labels['éé'].startsWith('a')"
                - rule: "self.labels[')'].startsWith('a')"
                - rule: "string('(').startsWith('a')"
`
	tests := []struct {
		name, needle string
		offset       int
		marked       []string
		unmarked     []string
	}{
		{"sum of ints", "sizes.sum", 7, []string{"list(int).sum() -> int // this call"}, []string{"list(double).sum() -> double\n", "list(duration).sum()"}},
		{"sum of doubles", "weights.sum", 9, []string{"list(double).sum() -> double // this call"}, []string{"list(int).sum() -> int\n"}},
		{"size of a string", "size(self.name", 1, []string{"size(string) -> int // this call"}, []string{"size(bytes) -> int\n", "size(list(<A>)) -> int\n"}},
		{"size of a non-ASCII argument", "size(string('é'))", 1, []string{"size(string) -> int // this call"}, nil},
		{"size of a non-ASCII index key", "size(self.labels['éé'])", 1, []string{"size(string) -> int // this call"}, nil},
		{"startsWith on a non-ASCII index key", "['éé'].startsWith('a')", 9, []string{"string.startsWith(string) -> bool // this call"}, nil},
		{"isLessThan on a quantity", "isLessThan", 0, []string{"kubernetes.Quantity.isLessThan(kubernetes.Quantity) -> bool // this call"}, []string{"kubernetes.Semver.isLessThan(kubernetes.Semver) -> bool\n"}},
		{"orValue on an optional", "orValue", 0, []string{"optional_type(<V>).orValue(<V>) -> <V> // this call"}, nil},
		{"startsWith on an index key with a bracket", "[')'].startsWith('a')", 6, []string{"string.startsWith(string) -> bool // this call"}, nil},
		{"startsWith on a call with a quoted parenthesis", "string('(').startsWith('a')", 13, []string{"string.startsWith(string) -> bool // this call"}, nil},
		{"startsWith in a macro", "k.startsWith('a')", 2, []string{"string.startsWith(string) -> bool // this call"}, nil},
		{"matches", "matches('^a", 0, []string{"string.matches(string) -> bool // this call"}, nil},
	}
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	doc := assist.Parse(context.Background(), "crd.yaml", []byte(crd))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := a.Hover(doc, posIn(t, crd, tt.needle, tt.offset))
			if !ok {
				t.Fatalf("Hover(%q) = none, want a hover", tt.needle)
			}
			for _, want := range tt.marked {
				if !strings.Contains(got.Markdown, want) {
					t.Errorf("Hover(%q) = %q, want it to contain %q", tt.needle, got.Markdown, want)
				}
			}
			for _, bad := range tt.unmarked {
				if strings.Contains(got.Markdown, strings.TrimSuffix(bad, "\n")+" // this call") {
					t.Errorf("Hover(%q) = %q, want %q not marked", tt.needle, got.Markdown, bad)
				}
			}
			if n := strings.Count(got.Markdown, "// this call"); n != 1 {
				t.Errorf("Hover(%q) marks %d overloads, want 1: %q", tt.needle, n, got.Markdown)
			}
		})
	}
}

func TestHoverMarksNamespacedCalls(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
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
                name:
                  type: string
                ids:
                  type: array
                  items:
                    type: integer
              x-kubernetes-validations:
                - rule: "sets.contains(self.ids, [1, 2])"
                - rule: "sets.intersects(self.ids, [3]) || sets.equivalent(self.ids, [3])"
                - rule: "strings.quote(self.name) != ''"
`
	tests := []struct {
		name, needle string
		offset       int
		want         string
	}{
		{"sets.contains", "sets.contains", 6, "sets.contains(list(<T>), list(<T>)) -> bool // this call"},
		{"sets.intersects", "sets.intersects", 6, "sets.intersects(list(<T>), list(<T>)) -> bool // this call"},
		{"sets.equivalent", "sets.equivalent", 6, "sets.equivalent(list(<T>), list(<T>)) -> bool // this call"},
		{"strings.quote", "strings.quote", 8, "strings.quote(string) -> string // this call"},
	}
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	doc := assist.Parse(context.Background(), "crd.yaml", []byte(crd))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := a.Hover(doc, posIn(t, crd, tt.needle, tt.offset))
			if !ok {
				t.Fatalf("Hover(%q) = none, want a hover", tt.needle)
			}
			if !strings.Contains(got.Markdown, tt.want) {
				t.Errorf("Hover(%q) = %q, want it to contain %q", tt.needle, got.Markdown, tt.want)
			}
			if n := strings.Count(got.Markdown, "// this call"); n != 1 {
				t.Errorf("Hover(%q) marks %d overloads, want 1: %q", tt.needle, n, got.Markdown)
			}
		})
	}
}

func TestHoverMarksOptionalCalls(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
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
                name:
                  type: string
                nickname:
                  type: string
                title:
                  type: string
              x-kubernetes-validations:
                - rule: "self.?nickname.optMap(n, size(n)).orValue(0) <= 20"
                - rule: "!self.?nickname.optFlatMap(n, n == '' ? optional.of(n) : optional.none()).hasValue()"
                - rule: "!self.?title.hasValue() || self.?title.value() != 'admin'"
                - rule: "self.?nickname.or(optional.of(self.name)).value().startsWith('p')"
`
	tests := []struct {
		name, needle string
		offset       int
		want         string
	}{
		{"orValue after optMap", "orValue", 0, "optional_type(<V>).orValue(<V>) -> <V> // this call"},
		{"size in optMap", "size(n)", 1, "size(string) -> int // this call"},
		{"hasValue after optFlatMap", "hasValue()\"", 0, "optional_type(<V>).hasValue() -> bool // this call"},
		{"optional.of in optFlatMap", "optional.of(n)", 9, "optional.of(<V>) -> optional_type(<V>) // this call"},
		{"optional.none in optFlatMap", "optional.none", 9, "optional.none() -> optional_type(<V>) // this call"},
		{"hasValue after title", "hasValue() ||", 0, "optional_type(<V>).hasValue() -> bool // this call"},
		{"value after title", "value() !=", 0, "optional_type(<V>).value() -> <V> // this call"},
		{"or after nickname", "or(optional", 0, "optional_type(<V>).or(optional_type(<V>)) -> optional_type(<V>) // this call"},
		{"optional.of in or", "optional.of(self", 9, "optional.of(<V>) -> optional_type(<V>) // this call"},
		{"value after or", "value().startsWith", 0, "optional_type(<V>).value() -> <V> // this call"},
		{"startsWith after value", "startsWith('p", 0, "string.startsWith(string) -> bool // this call"},
	}
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	doc := assist.Parse(context.Background(), "crd.yaml", []byte(crd))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := a.Hover(doc, posIn(t, crd, tt.needle, tt.offset))
			if !ok {
				t.Fatalf("Hover(%q) = none, want a hover", tt.needle)
			}
			if !strings.Contains(got.Markdown, tt.want) {
				t.Errorf("Hover(%q) = %q, want it to contain %q", tt.needle, got.Markdown, tt.want)
			}
			if n := strings.Count(got.Markdown, "// this call"); n != 1 {
				t.Errorf("Hover(%q) marks %d overloads, want 1: %q", tt.needle, n, got.Markdown)
			}
		})
	}
}
