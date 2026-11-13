package crdschema_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/JairJosafath/cel-lens/engine/internal/crdschema"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// node returns the mapping of text, a YAML document.
func node(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

// problemsOf reads and validates the schema text, and returns its problems
// as "line:column: message".
func problemsOf(t *testing.T, text string) []string {
	t.Helper()
	n := node(t, text)
	s, problems := crdschema.Read(n)
	if len(problems) == 0 {
		problems = crdschema.Validate(t.Context(), kubeversion.Default(), n, s)
	}
	out := make([]string, len(problems))
	for i, p := range problems {
		out[i] = fmt.Sprintf("%d:%d: %s", p.Node.Line, p.Node.Column, p.Message)
	}
	return out
}

func TestRead(t *testing.T) {
	t.Parallel()
	const text = `type: object
description: The limits.
properties:
  maxReplicas: {type: integer, minimum: 0, maximum: 10}
  team: {type: string, maxLength: 63, enum: [a, b]}
  surge: {x-kubernetes-int-or-string: true}
  extra: {type: object, x-kubernetes-preserve-unknown-fields: true}
  ports:
    type: array
    x-kubernetes-list-type: map
    x-kubernetes-list-map-keys: [port]
    items: {type: object, required: [port], properties: {port: {type: integer}}}
  labels: {type: object, additionalProperties: {type: string}, x-kubernetes-map-type: granular}
  when: {type: string, format: date-time, nullable: true}
required: [team]
`
	s, problems := crdschema.Read(node(t, text))
	if len(problems) > 0 {
		t.Fatalf("Read problems = %v, want none", problems)
	}
	props, _ := s["properties"].(map[string]any)
	maxReplicas, _ := props["maxReplicas"].(map[string]any)
	if s["type"] != "object" || maxReplicas["minimum"] != float64(0) || !reflect.DeepEqual(s["required"], []any{"team"}) {
		t.Errorf("Read = %v, want the schema as JSON values", s)
	}
	if got := crdschema.Validate(t.Context(), kubeversion.Default(), node(t, text), s); len(got) > 0 {
		t.Errorf("Validate = %v, want no problems: the API server takes this schema", got)
	}
}

// TestProblems checks that what the API server refuses in a CRD's schema is a
// problem at the key it is about, in the API server's words (#15).
func TestProblems(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"unknown keyword", "type: object\nproperties:\n  max: {type: integer, minimun: 0}\n", []string{`3:24: properties[max]: unknown field "minimun"`}},
		{"unknown keyword under items", "type: array\nitems:\n  type: string\n  maxLenght: 3\n", []string{`4:3: items: unknown field "maxLenght"`}},
		{"a keyword twice", "type: object\ntype: string\n", []string{`2:1: type: duplicate field "type" (first at line 1)`}},
		{"a property twice", "type: object\nproperties:\n  a: {type: string}\n  a: {type: integer}\n", []string{`4:3: properties[a]: duplicate field "a" (first at line 3)`}},
		{"a string for an integer", "type: string\nmaxLength: ten\n", []string{`2:1: maxLength: expected an integer, got the string "ten"`}},
		{"a schema that is a string", "type: object\nproperties:\n  a: string\n", []string{`3:6: properties[a]: expected an object, got the string "string"`}},
		{"a wrong type", "type: object\nproperties:\n  max: {type: int}\n", []string{`3:9: properties[max].type: Unsupported value: "int": supported values: "array", "boolean", "integer", "number", "object", "string"`}},
		{"no type", "type: object\nproperties:\n  max: {minimum: 0}\n", []string{`3:3: properties[max].type: Required value: must not be empty for specified object fields`}},
		{"properties and additionalProperties", "type: object\nproperties:\n  a: {type: string}\nadditionalProperties: {type: string}\n", []string{`4:1: additionalProperties: Forbidden: additionalProperties and properties are mutual exclusive`}},
		{"items as a list", "type: array\nitems:\n  - type: string\n", []string{"2:1: items: Forbidden: items must be a schema object and not an array"}},
		{"uniqueItems", "type: array\nuniqueItems: true\nitems: {type: string}\n", []string{"2:1: uniqueItems: Forbidden: uniqueItems cannot be set to true"}},
		{"a ref", "type: object\nproperties:\n  a: {$ref: '#/definitions/x'}\n", []string{"3:7: properties[a].$ref: Forbidden: $ref is not supported"}},
		{"a list map without keys", "type: array\nx-kubernetes-list-type: map\nitems: {type: object, properties: {a: {type: string}}}\n", []string{"2:1: x-kubernetes-list-map-keys: Required value: must not be empty if x-kubernetes-list-type is map"}},
		{"a bad default", "type: integer\ndefault: abc\n", []string{`2:1: default: Invalid value: "string"`}},
		{"a scalar root is fine", "type: integer\nnullable: true\n", nil},
		{"rules", "type: object\nx-kubernetes-validations:\n  - rule: self.a > 0\n", []string{"2:1: x-kubernetes-validations: a .cellens schema gives the value a type"}},
		// The review of 6f82d2a: aliases are never followed, so a schema that
		// contains itself or aliases that multiply cannot hold the server.
		{"a schema that contains itself", "&a\ntype: object\nproperties:\n  x: *a\n", []string{"4:6: properties[x]: a YAML alias (*a) is not taken in a schema"}},
		{"properties that contain themselves", "type: object\nproperties: &p\n  b: {type: object, properties: *p}\n", []string{"3:33: properties[b].properties: a YAML alias (*p) is not taken"}},
		{"an alias in an enum", "type: string\nenum: [&v a, *v]\n", []string{"2:14: enum: a YAML alias (*v) is not taken"}},
		{"a merge key", "type: object\n<<: {description: x}\n", []string{"2:1: a merge key (<<) is not taken in a schema"}},
		{"a timestamp is a string", "type: string\ndescription: 2020-01-01\nenum: [2020-01-02]\n", nil},
		{"a value of another tag", "type: string\ndescription: !!binary aGk=\nmaxLength: !foo 3\n", []string{`2:1: description: expected a string, got "aGk=", of the YAML tag !!binary`, `3:1: maxLength: expected an integer, got "3", of the YAML tag !foo`}},
		{"an embedded resource without properties", "type: object\nx-kubernetes-embedded-resource: true\n", []string{"2:1: properties: Required value: must not be empty if x-kubernetes-embedded-resource is true"}},
		{"a property with ] in its name", "type: object\nproperties:\n  'a]':\n    type: int\n", []string{`4:5: properties[a]].type: Unsupported value: "int"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := problemsOf(t, tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("problems of %q = %q, want %q", tc.text, got, tc.want)
			}
			for i := range got {
				if !strings.HasPrefix(got[i], tc.want[i]) {
					t.Errorf("problem %d of %q = %q, want %q", i, tc.text, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestKeywords checks that Read knows every keyword of a CRD's schema: the
// JSON names of the fields of JSONSchemaProps.
func TestKeywords(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[apiextensionsv1.JSONSchemaProps]()
	for f := range typ.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		text := "type: object\n" + name + ": null\n"
		if name == "type" {
			continue
		}
		n := node(t, text)
		if _, problems := crdschema.Read(n); slices.ContainsFunc(problems, func(p crdschema.Problem) bool { return strings.Contains(p.Message, "unknown field") }) {
			t.Errorf("Read(%q) = %v, want %s known", text, problems, name)
		}
	}
}

// FuzzRead checks that no YAML, however written, makes Read or Validate fail
// other than with problems, or hold them: aliases are never followed.
func FuzzRead(f *testing.F) {
	for _, s := range []string{
		"type: object\nproperties:\n  a: {type: string}\n", "&a\ntype: object\nproperties:\n  x: *a\n",
		"type: object\nproperties: &p\n  b: {type: object, properties: *p}\n", "type: string\ndescription: 2020-01-01\n",
		"type: object\n<<: {description: x}\n", "type: array\nitems: [{type: string}]\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		var doc yaml.Node
		if yaml.Unmarshal([]byte(text), &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			return
		}
		if s, problems := crdschema.Read(doc.Content[0]); len(problems) == 0 {
			_ = crdschema.Validate(t.Context(), kubeversion.Default(), doc.Content[0], s)
		}
	})
}

// TestArrange is #23: a schema the engine writes has the keys of each schema
// in it in the order JSONSchemaProps declares them, as the API server writes
// one, type before properties, required before properties, x-kubernetes-
// last; the names of properties and the values of default, enum and example
// keep the order written, and so does a key that is no keyword, after the
// keywords. JSON writes it in that order.
func TestArrange(t *testing.T) {
	t.Parallel()
	const in = `x-kubernetes-preserve-unknown-fields: false
properties:
  zeta: {x-kubernetes-int-or-string: true, description: z}
  alpha:
    items: {minimum: 1, type: integer}
    type: array
  beta:
    additionalProperties: {format: date-time, type: string}
    type: object
dependencies: {zeta: [alpha, beta], alpha: {required: [beta], type: object}}
required: [zeta, alpha]
default: {b: 1, a: 2}
enum: [{b: 1, a: 2}]
type: object
description: d
`
	want := `{"description":"d","type":"object","default":{"b":1,"a":2},"enum":[{"b":1,"a":2}],"required":["zeta","alpha"],` +
		`"properties":{"zeta":{"description":"z","x-kubernetes-int-or-string":true},` +
		`"alpha":{"type":"array","items":{"type":"integer","minimum":1}},` +
		`"beta":{"type":"object","additionalProperties":{"type":"string","format":"date-time"}}},` +
		`"dependencies":{"zeta":["alpha","beta"],"alpha":{"type":"object","required":["beta"]}},` +
		`"x-kubernetes-preserve-unknown-fields":false}`
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(in), &doc); err != nil {
		t.Fatal(err)
	}
	n := doc.Content[0]
	got, err := crdschema.JSON(crdschema.Arrange(n))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("JSON(Arrange(schema)) =\n%s\nwant\n%s", got, want)
	}
	if first := n.Content[0].Value; first != "x-kubernetes-preserve-unknown-fields" {
		t.Errorf("Arrange changed its input: its first key is %s", first)
	}
	unknown := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "x-other"},
		{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "type"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "string"},
	}}
	if got, _ := crdschema.JSON(crdschema.Arrange(unknown)); string(got) != `{"type":"string","x-other":1}` {
		t.Errorf("JSON(Arrange(a key that is no keyword)) = %s, want it after the keywords", got)
	}
}

// semverRule is a schema with a rule only Kubernetes 1.34 on compiles.
// Read refuses rules in a schema, so it is given to Validate as it is: the
// problems of a schema without rules are the same for every version, and
// only a rule shows which version the validation ran for.
var semverRule = map[string]any{
	"type":                     "string",
	"x-kubernetes-validations": []any{map[string]any{"rule": "isSemver(self)"}},
}

// rulesCRD is a CustomResourceDefinition with rules, which check.Check
// validates for its version.
const rulesCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            a: {type: string, maxLength: 10}
            b: {type: array, maxItems: 10, items: {type: integer}}
          x-kubernetes-validations:
            - rule: self.a.size() < 5 && self.b.all(x, x > 0)
            - rule: self.b.exists(x, x == 1) || self.a == 'b'
`

// TestValidateBesideChecks validates a schema for one Kubernetes version
// while CRDs are checked for another, as the language server does (#14):
// the Kubernetes libraries read the version from a setting of the whole
// process, which each check sets to its own, so a validation that did not
// run under check.UnderVersion would see theirs.
func TestValidateBesideChecks(t *testing.T) {
	t.Parallel()
	old, err := kubeversion.Parse("1.33")
	if err != nil {
		t.Fatal(err)
	}
	newer := kubeversion.Default()
	for _, tt := range []struct {
		name          string
		validate, ran kubeversion.Version
		want          int
	}{
		{"old beside new", old, newer, 1},
		{"new beside old", newer, old, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			n := node(t, "type: string")
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
						if _, err := check.Check(t.Context(), "crd.yaml", []byte(rulesCRD), check.WithVersion(tt.ran)); err != nil {
							t.Error(err)
							return
						}
					}
				})
			}
			for range 50 {
				if got := crdschema.Validate(t.Context(), tt.validate, n, semverRule); len(got) != tt.want {
					t.Errorf("Validate(%s, isSemver) beside checks for %s = %v, want %d problems", tt.validate, tt.ran, got, tt.want)
					break
				}
			}
			close(done)
			wg.Wait()
		})
	}
}
