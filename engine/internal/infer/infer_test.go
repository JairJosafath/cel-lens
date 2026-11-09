package infer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
	"github.com/JairJosafath/cel-lens/engine/internal/infer"
)

// crdProblems returns what the API server finds wrong with a
// CustomResourceDefinition whose schema is s: its openAPIV3Schema when root
// is true, else the schema of a field of it.
func crdProblems(t *testing.T, s map[string]any, root bool) field.ErrorList {
	t.Helper()
	if !root {
		s = map[string]any{"type": "object", "properties": map[string]any{"value": s}}
	}
	raw, err := json.Marshal(map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": "widgets.example.com"},
		"spec": map[string]any{
			"group": "example.com", "scope": "Namespaced",
			"names":    map[string]any{"plural": "widgets", "singular": "widget", "kind": "Widget", "listKind": "WidgetList"},
			"versions": []any{map[string]any{"name": "v1", "served": true, "storage": true, "schema": map[string]any{"openAPIV3Schema": s}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var versioned apiextensionsv1.CustomResourceDefinition
	if err := json.Unmarshal(raw, &versioned); err != nil {
		t.Fatal(err)
	}
	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(&versioned)
	var internal apiextensions.CustomResourceDefinition
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&versioned, &internal, nil); err != nil {
		t.Fatal(err)
	}
	return validation.ValidateCustomResourceDefinition(t.Context(), &internal)
}

// accepted fails t unless the API server accepts the schema s of a value in
// a CustomResourceDefinition: as the schema of a field, and, when s is that
// of a whole object, as its root.
func accepted(t *testing.T, s map[string]any, whole bool) {
	t.Helper()
	if p := crdProblems(t, s, false); len(p) > 0 {
		t.Errorf("schema %v as a field: the API server refuses it: %v", s, p)
	}
	if whole && s["type"] == "object" {
		if p := crdProblems(t, s, true); len(p) > 0 {
			t.Errorf("schema %v at the root: the API server refuses it: %v", s, p)
		}
	}
}

// decoded is the schema s, JSON text, as YAML decodes a CRD's schema: maps
// and lists.
func decoded(t *testing.T, s json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(s, &out); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return out
}

// compact is the JSON text s without spaces, its keys in the order written.
func compact(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		t.Fatalf("compact %s: %v", s, err)
	}
	return b.String()
}

// sorted is the JSON text s as json.Marshal writes its value: without
// spaces, with the keys of each object sorted.
func sorted(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestSchema checks the schema and notes Schema works out of examples: the
// kinds of value, lists merged over their items, required only when asked
// (#17), values that disagree merged as Kubernetes accepts both or left
// without a type with a note naming the field and the kinds (#17), and empty
// lists and objects noted (#17). The schemas compare as JSON, which writes
// the keys sorted, the same on every run.
func TestSchema(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		text  string
		opts  infer.Options
		want  string
		notes []string
	}{
		{
			name: "every kind of value",
			text: "name: web\nreplicas: 3\nratio: 0.5\nweight: 2.0\nenabled: true\nports: [{port: 80}, {port: 443, name: https}]\n",
			want: `{"type":"object","properties":{
				"name":{"type":"string"},"enabled":{"type":"boolean"},
				"ports":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"port":{"type":"integer"}}}},
				"ratio":{"type":"number"},"replicas":{"type":"integer"},"weight":{"type":"integer"}}}`,
		},
		{
			name: "JSON",
			text: "{\n\t\"name\": \"web\",\n\t\"sizes\": [1, 2.5]\n}\n",
			want: `{"type":"object","properties":{"name":{"type":"string"},"sizes":{"type":"array","items":{"type":"number"}}}}`,
		},
		{
			name: "documents merge, required off",
			text: "a: 1\nb: x\n---\na: 2\n",
			want: `{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"string"}}}`,
		},
		{
			name: "required on lists the fields of every example",
			text: "a: 1\nb: x\nc: {d: 1, e: 2}\n---\na: 2\nc: {d: 3}\n",
			opts: infer.Options{Required: true},
			want: `{"type":"object","required":["a","c"],"properties":{
				"a":{"type":"integer"},"b":{"type":"string"},
				"c":{"type":"object","required":["d"],"properties":{"d":{"type":"integer"},"e":{"type":"integer"}}}}}`,
		},
		{
			name: "required on, for the items of a list",
			text: "items: [{a: 1, b: 2}, {a: 3}]\n",
			opts: infer.Options{Required: true},
			want: `{"type":"object","required":["items"],"properties":{"items":{"type":"array","items":{"type":"object","required":["a"],"properties":{"a":{"type":"integer"},"b":{"type":"integer"}}}}}}`,
		},
		{
			name: "a list of objects is a list of examples",
			text: "- a: 1\n- a: x\n  b: true\n",
			want: `{"type":"object","properties":{"a":{"x-kubernetes-int-or-string":true},"b":{"type":"boolean"}}}`,
		},
		{
			name: "a list of scalars is one example",
			text: "[1, 2]\n",
			want: `{"type":"array","items":{"type":"integer"}}`,
		},
		{
			name: "a v1 List gives its items",
			text: "apiVersion: v1\nkind: List\nitems:\n- {size: 1}\n- {size: 1.5}\n",
			want: `{"type":"object","properties":{"size":{"type":"number"}}}`,
		},
		{
			name: "integer and number make number",
			text: "count: 1\n---\ncount: 1.5\n",
			want: `{"type":"object","properties":{"count":{"type":"number"}}}`,
		},
		{
			name: "integer and string make int-or-string",
			text: "port: 80\n---\nport: http\n",
			want: `{"type":"object","properties":{"port":{"x-kubernetes-int-or-string":true}}}`,
		},
		{
			name: "null makes the field nullable",
			text: "a: null\nb: ~\n---\na: x\nb: [1]\n",
			want: `{"type":"object","properties":{"a":{"nullable":true,"type":"string"},"b":{"nullable":true,"type":"array","items":{"type":"integer"}}}}`,
		},
		{
			name:  "only null",
			text:  "a: null\n",
			want:  `{"type":"object","properties":{"a":{"nullable":true,"x-kubernetes-preserve-unknown-fields":true}}}`,
			notes: []string{"a: only null was seen, which says nothing about its type"},
		},
		{
			name: "kinds that disagree leave the field without a type, the rest inferred",
			text: "spec: {port: true, name: a}\n---\nspec: {port: x, size: 2}\n",
			want: `{"type":"object","properties":{"spec":{"type":"object","properties":{
				"name":{"type":"string"},"port":{"x-kubernetes-preserve-unknown-fields":true},"size":{"type":"integer"}}}}}`,
			notes: []string{"spec.port: the examples have boolean and string here, which no type Kubernetes accepts holds together: it is left without a type"},
		},
		{
			name:  "number and string do not make int-or-string",
			text:  "a: [1, 1.5, x]\n",
			want:  `{"type":"object","properties":{"a":{"type":"array","items":{"x-kubernetes-preserve-unknown-fields":true}}}}`,
			notes: []string{"a[]: the examples have number and string here, which no type Kubernetes accepts holds together: it is left without a type"},
		},
		{
			name:  "an object and a list",
			text:  "a: {b: 1}\n---\na: [1]\n---\na: null\n",
			want:  `{"type":"object","properties":{"a":{"nullable":true,"x-kubernetes-preserve-unknown-fields":true}}}`,
			notes: []string{"a: the examples have object and list here, which no type Kubernetes accepts holds together: it is left without a type"},
		},
		{
			name: "an empty list or object says nothing",
			text: "spec:\n  items: []\n  config: {}\n  more: []\n",
			want: `{"type":"object","properties":{"spec":{"type":"object","properties":{
				"config":{"type":"object","x-kubernetes-preserve-unknown-fields":true},
				"items":{"type":"array","items":{"x-kubernetes-preserve-unknown-fields":true}},
				"more":{"type":"array","items":{"x-kubernetes-preserve-unknown-fields":true}}}}}}`,
			// #23: the notes come in the order of the schema, the examples'.
			notes: []string{
				"spec.items: an empty list says nothing about its items",
				"spec.config: an empty object says nothing about its fields",
				"spec.more: an empty list says nothing about its items",
			},
		},
		{
			name: "an empty list beside a full one says nothing new",
			text: "items: []\n---\nitems: [a]\n---\nconfig: {}\n---\nconfig: {x: 1}\n",
			want: `{"type":"object","properties":{"config":{"type":"object","properties":{"x":{"type":"integer"}}},"items":{"type":"array","items":{"type":"string"}}}}`,
		},
		{
			name:  "an empty example",
			text:  "{}\n",
			want:  `{"type":"object","x-kubernetes-preserve-unknown-fields":true}`,
			notes: []string{"the value: an empty object says nothing about its fields"},
		},
		{
			name: "timestamps and big numbers",
			text: "at: 2001-12-14\nbig: 99999999999999999999\nhex: 0x1F\n",
			want: `{"type":"object","properties":{"at":{"type":"string"},"big":{"type":"number"},"hex":{"type":"integer"}}}`,
		},
		{
			name: "aliases and merge keys",
			text: "base: &b {a: 1}\nother:\n  <<: *b\n  c: x\n",
			want: `{"type":"object","properties":{"base":{"type":"object","properties":{"a":{"type":"integer"}}},"other":{"type":"object","properties":{"a":{"type":"integer"},"c":{"type":"string"}}}}}`,
		},
		{
			name: "keys that are no string",
			text: "1: x\ntrue: 2\nnull: z\n",
			want: `{"type":"object","properties":{"1":{"type":"string"},"null":{"type":"string"},"true":{"type":"integer"}}}`,
		},
		{
			name: "a merge takes what the mapping and earlier merges lack",
			text: "a: &a {x: 1, w: 1}\nb: &b {x: x, z: 1}\nc:\n  <<: [*a, *b]\n  w: true\n",
			opts: infer.Options{Path: []string{"c"}},
			want: `{"type":"object","properties":{"w":{"type":"boolean"},"x":{"type":"integer"},"z":{"type":"integer"}}}`,
		},
		{
			name: "an unsigned integer past int64 is a number, as Kubernetes decodes it (review of #17)",
			text: "num: 18446744073709551615\nmax: 9223372036854775807\n",
			want: `{"type":"object","properties":{"max":{"type":"integer"},"num":{"type":"number"}}}`,
		},
		{
			name: "strings kubectl reads as booleans get a note (review of #17)",
			text: "enabled: yes\nquoted: \"no\"\nmode: off\ntagged: !!str on\n",
			want: `{"type":"object","properties":{"enabled":{"type":"string"},"mode":{"type":"string"},"quoted":{"type":"string"},"tagged":{"type":"string"}}}`,
			notes: []string{
				"enabled: an example has yes, unquoted, which kubectl reads as a boolean (YAML 1.1): quote it if a string is meant",
				"mode: an example has off, unquoted, which kubectl reads as a boolean (YAML 1.1): quote it if a string is meant",
			},
		},
		{
			name: "keys kubectl reads as booleans get a note (review of #17)",
			text: "on: x\nspec: {y: 1, \"n\": 2, Off: 3}\n",
			want: `{"type":"object","properties":{"on":{"type":"string"},"spec":{"type":"object","properties":{"Off":{"type":"integer"},"n":{"type":"integer"},"y":{"type":"integer"}}}}}`,
			notes: []string{
				`on: the key on, unquoted, is the boolean true to kubectl (YAML 1.1), which sends it as the key "true": quote it`,
				`spec.y: the key y, unquoted, is the boolean true to kubectl (YAML 1.1), which sends it as the key "true": quote it`,
				`spec.Off: the key Off, unquoted, is the boolean false to kubectl (YAML 1.1), which sends it as the key "false": quote it`,
			},
		},
		{
			name: "aliases within ten times the nodes",
			text: "a: &a {x: 1, z: [1, 2]}\nb: [*a, *a, *a, *a, *a]\n",
			opts: infer.Options{Path: []string{"b", assume.Items}},
			want: `{"type":"object","properties":{"x":{"type":"integer"},"z":{"type":"array","items":{"type":"integer"}}}}`,
		},
		{
			name: "line takes its document, whatever the others hold (review of #17)",
			text: "a: [\n---\nb: 1\n---\nc: {\n",
			opts: infer.Options{Line: 3},
			want: `{"type":"object","properties":{"b":{"type":"integer"}}}`,
		},
		{
			name: "line on the separator takes the document it starts",
			text: "a: 1\n---\nb: x\n",
			opts: infer.Options{Line: 2},
			want: `{"type":"object","properties":{"b":{"type":"string"}}}`,
		},
		{
			name: "line takes the document there",
			text: "a: 1\n---\n# second\nb: x\n---\nc: true\n",
			opts: infer.Options{Line: 4},
			want: `{"type":"object","properties":{"b":{"type":"string"}}}`,
		},
		{
			name: "path takes the value there",
			text: "spec:\n  ports:\n  - {config: {a: 1}}\n  - {config: {b: x}}\n---\nspec: {}\n",
			opts: infer.Options{Path: []string{"spec", "ports", assume.Items, "config"}},
			want: `{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"string"}}}`,
		},
		{
			name: "path through a map's values",
			text: "labels: {a: {size: 1}, b: {size: 2.5}}\n",
			opts: infer.Options{Path: []string{"labels", assume.Values}},
			want: `{"type":"object","properties":{"size":{"type":"number"}}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := infer.Schema(t.Context(), []byte(tt.text), tt.opts)
			if err != nil {
				t.Fatalf("Schema(%q) error = %v", tt.text, err)
			}
			if got, want := sorted(t, string(res.Schema)), sorted(t, tt.want); got != want {
				t.Errorf("Schema(%q) = %s, want %s", tt.text, got, want)
			}
			if tt.notes == nil {
				tt.notes = []string{}
			}
			if !slices.Equal(res.Notes, tt.notes) || res.Notes == nil {
				t.Errorf("Schema(%q) notes = %#v, want %#v", tt.text, res.Notes, tt.notes)
			}
			accepted(t, decoded(t, res.Schema), tt.opts.Path == nil)
		})
	}
}

// TestSchemaKubernetesObject checks that apiVersion, kind and metadata of a
// Kubernetes object are typed as a CustomResourceDefinition writes them,
// never from the example's values, so the API server accepts the schema at
// the root of one (review of #17), and that its status is inferred when the
// value is the whole object and left out when the value is a part of it
// (#17).
func TestSchemaKubernetesObject(t *testing.T) {
	t.Parallel()
	const sample = `apiVersion: rules.example.com/v1
kind: ReplicaLimit
metadata:
  name: limit-sample
  labels: {app: web, tier: 3}
  creationTimestamp: null
spec:
  maxReplicas: 3
status:
  ready: true
`
	res, err := infer.Schema(t.Context(), []byte(sample), infer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	props, _ := decoded(t, res.Schema)["properties"].(map[string]any)
	for _, k := range []string{"apiVersion", "kind"} {
		if got, _ := json.Marshal(props[k]); string(got) != `{"type":"string"}` {
			t.Errorf("Schema(sample) %s = %s, want {\"type\":\"string\"}", k, got)
		}
	}
	if got, _ := json.Marshal(props["metadata"]); string(got) != `{"type":"object"}` {
		t.Errorf("Schema(sample) metadata = %s, want {\"type\":\"object\"}, nothing from the example", got)
	}
	accepted(t, decoded(t, res.Schema), true)
	if got, _ := json.Marshal(props["status"]); string(got) != sorted(t, `{"type":"object","properties":{"ready":{"type":"boolean"}}}`) {
		t.Errorf("Schema(sample) status = %s, want it inferred, as the value is the whole object", got)
	}
	if len(res.Notes) != 0 {
		t.Errorf("Schema(sample) notes = %v, want none: metadata's null is not inferred", res.Notes)
	}

	t.Run("a part of it", func(t *testing.T) {
		t.Parallel()
		res, err := infer.Schema(t.Context(), []byte(sample), infer.Options{Path: []string{"spec"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(res.Schema); got != `{"type":"object","properties":{"maxReplicas":{"type":"integer"}}}` {
			t.Errorf("Schema(sample, spec) = %s, want spec alone, without status", got)
		}
	})
	t.Run("the object of the review", func(t *testing.T) {
		t.Parallel()
		res, err := infer.Schema(t.Context(), []byte("apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: a}\n"), infer.Options{})
		if err != nil {
			t.Fatal(err)
		}
		accepted(t, decoded(t, res.Schema), true)
	})
	t.Run("a reference is no object", func(t *testing.T) {
		t.Parallel()
		res, err := infer.Schema(t.Context(), []byte("ref: {apiVersion: v1, kind: Pod, metadata: x}\n"), infer.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(res.Schema); !strings.Contains(got, `"metadata":{"type":"string"}`) {
			t.Errorf("Schema(ref) = %s, want metadata inferred below the root", got)
		}
	})
}

// docSamples are the samples of docs/cellens.md, "A schema from examples".
const docSamples = `apiVersion: rules.example.com/v1
kind: ReplicaLimit
metadata:
  name: small
  labels: {team: a}
spec:
  maxReplicas: 3
  ratio: 0.5
  port: 8080
  owner: null
  teams: []
---
apiVersion: rules.example.com/v1
kind: ReplicaLimit
metadata:
  name: large
spec:
  maxReplicas: 10
  ratio: 1
  port: http
  owner: platform
  teams: []
`

// TestSchemaDocExample checks that the samples of docs/cellens.md give the
// schema and note it shows, and with path spec and required what it says
// (#17), written in that order (#23): the keys of each schema as the API
// server writes them, type first and required before properties, and the
// properties in the order the samples first have them, not by name.
func TestSchemaDocExample(t *testing.T) {
	t.Parallel()
	const spec = `{"type":"object","properties":{
		"maxReplicas":{"type":"integer"},"ratio":{"type":"number"},
		"port":{"x-kubernetes-int-or-string":true},"owner":{"type":"string","nullable":true},
		"teams":{"type":"array","items":{"x-kubernetes-preserve-unknown-fields":true}}}}`
	res, err := infer.Schema(t.Context(), []byte(docSamples), infer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	accepted(t, decoded(t, res.Schema), true)
	want := compact(t, `{"type":"object","properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"type":"object"},"spec":`+spec+`}}`)
	if got := string(res.Schema); got != want {
		t.Errorf("Schema(docSamples) = %s, want %s", got, want)
	}
	if !slices.Equal(res.Notes, []string{"spec.teams: an empty list says nothing about its items"}) {
		t.Errorf("Schema(docSamples) notes = %q, want the one for spec.teams", res.Notes)
	}
	res, err = infer.Schema(t.Context(), []byte(docSamples), infer.Options{Path: []string{"spec"}, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	wantSpec := compact(t, strings.Replace(spec, `{"type":"object",`, `{"type":"object","required":["maxReplicas","ratio","port","owner","teams"],`, 1))
	if got := string(res.Schema); got != wantSpec {
		t.Errorf("Schema(docSamples, spec, required) = %s, want %s", got, wantSpec)
	}
}

// TestSchemaErrors checks that Schema refuses a text it cannot read, too big
// or too deep, with what was wrong and where (#17).
func TestSchemaErrors(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("[", infer.MaxDepth+1) + strings.Repeat("]", infer.MaxDepth+1)
	wide := wideMapping()
	// laughs holds 10^7 values once its aliases are expanded: g holds ten
	// f, each of which holds ten e, and so on.
	var laughs strings.Builder
	laughs.WriteString("a: &a [1, 1, 1, 1, 1, 1, 1, 1, 1, 1]\n")
	for prev := 'a'; prev < 'g'; prev++ {
		fmt.Fprintf(&laughs, "%c: &%c [%s]\n", prev+1, prev+1, strings.TrimSuffix(strings.Repeat("*"+string(prev)+", ", 10), ", "))
	}
	deepObject := strings.Repeat("{a: ", infer.MaxDepth) + "1" + strings.Repeat("}", infer.MaxDepth)
	for _, tt := range []struct {
		name string
		text string
		opts infer.Options
		want error
		msg  string
	}{
		{"too big", strings.Repeat("a", infer.MaxBytes+1), infer.Options{}, infer.ErrTooBig, "it is 1048577 bytes, over the limit of 1048576"},
		{"too deep", deep, infer.Options{}, infer.ErrTooDeep, "over the limit of 100"},
		{"too deep, with its line", "a:\n  " + strings.Repeat("{a: ", infer.MaxDepth) + "1" + strings.Repeat("}", infer.MaxDepth), infer.Options{}, infer.ErrTooDeep, "line 2 is 101 lists and objects deep"},
		{"does not parse", "a: 1\nb: [\n", infer.Options{}, infer.ErrSyntax, "line 2"},
		{"a key twice", "a: 1\na: 2\n", infer.Options{}, infer.ErrSyntax, `the text does not parse: line 2: the key "a" is written twice, at line 1 first`},
		{"a key twice, far apart in a wide mapping (review of #17)", wide + "k0: again\n", infer.Options{}, infer.ErrSyntax, `line 60001: the key "k0" is written twice, at line 1 first`},
		{"two keys that print the same", "1.0: true\n1: x\n", infer.Options{}, infer.ErrSyntax, `line 2: the key "1" is written twice`},
		{"a list as a key", "? [a]\n: 1\n", infer.Options{}, infer.ErrSyntax, "a key that is a list or an object"},
		{"a merge of no object", "<<: 1\n", infer.Options{}, infer.ErrSyntax, "<< merges only an object"},
		{"aliases that expand too far", laughs.String(), infer.Options{}, infer.ErrTooBig, "more than 10000 values once their aliases are expanded"},
		{"four nested maps of 30 aliases, 865,000 values in 1,144 bytes (review of #17)", wideAliases(), infer.Options{}, infer.ErrTooBig, "more than 10000 values"},
		{"empty", "# nothing\n", infer.Options{}, infer.ErrNoExample, "no document with a value"},
		{"nothing at the path", "a: {b: 1}\n", infer.Options{Path: []string{"a", "c", assume.Items}}, infer.ErrNoExample, "a.c[]"},
		{"line in a document of comments", "a: 1\n---\n# nothing\n---\nb: 2\n", infer.Options{Line: 3}, infer.ErrNoExample, "at line 3"},
		{"line in a broken document (review of #17)", "a: [\n---\nb: 1\n", infer.Options{Line: 1}, infer.ErrSyntax, "line 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := infer.Schema(t.Context(), []byte(tt.text), tt.opts)
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("Schema(%.40q) error = %v, want %v with %q", tt.text, err, tt.want, tt.msg)
			}
		})
	}
	if _, err := infer.Schema(t.Context(), []byte(deepObject), infer.Options{}); err != nil {
		t.Errorf("Schema of %d nested objects error = %v, want none at the limit", infer.MaxDepth, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := infer.Schema(ctx, []byte("a: 1\n"), infer.Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Schema with a cancelled context error = %v, want context.Canceled", err)
	}
}

// wideAliases is four nested maps of 30 keys, each map's values aliases of
// the map before: about 865,000 values once the aliases are expanded.
func wideAliases() string {
	var b strings.Builder
	value := "1"
	for _, name := range []string{"a", "b", "c", "d"} {
		fields := make([]string, 30)
		for i := range fields {
			fields[i] = fmt.Sprintf("k%d: %s", i, value)
		}
		fmt.Fprintf(&b, "%s: &%s {%s}\n", name, name, strings.Join(fields, ", "))
		value = "*" + name
	}
	return b.String()
}

// wideMapping is a mapping of 60,000 keys, k0 to k59999, one per line.
func wideMapping() string {
	var b strings.Builder
	for i := range 60000 {
		fmt.Fprintf(&b, "k%d: %d\n", i, i)
	}
	return b.String()
}

// BenchmarkSchemaWideMapping measures Schema on a mapping of 60,000 keys,
// about 0.9 MB: yaml.v3's own decoding checks for keys written twice in
// time quadratic in the keys.
func BenchmarkSchemaWideMapping(b *testing.B) {
	wide := wideMapping()
	for b.Loop() {
		if _, err := infer.Schema(b.Context(), []byte(wide), infer.Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// FuzzSchema checks that no text makes Schema panic, and that a schema it
// returns encodes as JSON.
func FuzzSchema(f *testing.F) {
	for _, s := range []string{"a: 1\n", "- a: [1, x]\n- {}\n", "apiVersion: v1\nkind: List\nitems: [null]\n", "{\"a\": [[], {}]}", "<<: {a: 1}\n1: 2\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		res, err := infer.Schema(t.Context(), []byte(text), infer.Options{Required: true})
		if err != nil {
			return
		}
		if _, err := json.Marshal(res); err != nil {
			t.Errorf("Schema(%q) = %v, which does not encode: %v", text, res.Schema, err)
		}
	})
}

// TestSchemaRootsAndKinds is #25: Schema gives the apiVersion and kind at the
// root of each example, nil for one without both, for the items of a list
// and of a v1 List too; with Kinds and Path, an example of none of the kinds
// is the value itself, with a note naming its line, and one of a kind gives
// the value at the path.
func TestSchemaRootsAndKinds(t *testing.T) {
	t.Parallel()
	const text = "apiVersion: v1\nkind: List\nitems:\n  - {apiVersion: v1, kind: Pod, spec: {a: 1}}\n  - {b: 2}\n---\n- {apiVersion: x/v1, kind: Gadget, spec: {c: 3}}\n---\nd: 4\n"
	res, err := infer.Schema(t.Context(), []byte(text), infer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	roots := make([]string, len(res.Roots))
	for i, r := range res.Roots {
		if r != nil {
			roots[i] = r.String()
		}
	}
	if want := []string{"v1 Pod", "", "x/v1 Gadget", ""}; !slices.Equal(roots, want) {
		t.Errorf("Schema(text) roots = %q, want %q", roots, want)
	}
	res, err = infer.Schema(t.Context(), []byte(text), infer.Options{Path: []string{"spec"}, Kinds: []infer.Kind{{APIVersion: "v1", Kind: "Pod"}, {APIVersion: "x/v1", Kind: "Gadget"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sorted(t, string(res.Schema)), sorted(t, `{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"},"c":{"type":"integer"},"d":{"type":"integer"}}}`); got != want {
		t.Errorf("Schema(text, spec, kinds) = %s, want %s", got, want)
	}
	wantNotes := []string{
		"the value: the example at line 5 has no apiVersion and kind, so it is not of v1 Pod or x/v1 Gadget: it is taken as the value at spec itself",
		"the value: the example at line 9 has no apiVersion and kind, so it is not of v1 Pod or x/v1 Gadget: it is taken as the value at spec itself",
	}
	if !slices.Equal(res.Notes, wantNotes) {
		t.Errorf("Schema(text, spec, kinds) notes = %q, want %q", res.Notes, wantNotes)
	}
	// The review of #25: a kind is one in any version of its group, as the
	// API server converts it, and not in another group; an alias's note
	// names its own line, not its anchor's.
	deployments := []infer.Kind{{APIVersion: "apps/v1", Kind: "Deployment"}}
	for _, tc := range []struct {
		name, text string
		want       string
		notes      []string
	}{
		{
			"another version", "apiVersion: apps/v1beta1\nkind: Deployment\nmetadata: {name: a}\nspec: {replicas: 3}\n",
			`{"type":"object","properties":{"replicas":{"type":"integer"}}}`, nil,
		},
		{
			"another group", "apiVersion: other.example.com/v1\nkind: Deployment\nspec: {replicas: 3}\n",
			`{"type":"object","properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"spec":{"type":"object","properties":{"replicas":{"type":"integer"}}}}}`,
			[]string{"the value: the example at line 1 is of kind other.example.com/v1 Deployment, not of apps/v1 Deployment: it is taken as the value at spec itself"},
		},
		{
			"an alias", "- &a {replicas: 3}\n- *a\n",
			`{"type":"object","properties":{"replicas":{"type":"integer"}}}`,
			[]string{
				"the value: the example at line 1 has no apiVersion and kind, so it is not of apps/v1 Deployment: it is taken as the value at spec itself",
				"the value: the example at line 2 has no apiVersion and kind, so it is not of apps/v1 Deployment: it is taken as the value at spec itself",
			},
		},
	} {
		res, err := infer.Schema(t.Context(), []byte(tc.text), infer.Options{Path: []string{"spec"}, Kinds: deployments})
		if err != nil {
			t.Fatalf("Schema(%s) error = %v", tc.name, err)
		}
		if got, want := sorted(t, string(res.Schema)), sorted(t, tc.want); got != want {
			t.Errorf("Schema(%s) = %s, want %s", tc.name, got, want)
		}
		if tc.notes == nil {
			tc.notes = []string{}
		}
		if !slices.Equal(res.Notes, tc.notes) {
			t.Errorf("Schema(%s) notes = %q, want %q", tc.name, res.Notes, tc.notes)
		}
	}
}
