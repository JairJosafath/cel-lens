package lsp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// inferred is what inferType answers.
type inferred struct {
	Schema map[string]any
	Notes  []string
}

// TestInferType checks that inferType answers the schema of the examples in
// its text, with required only when asked and the notes on what the examples
// do not settle, takes one document by its line and the value at a path,
// and refuses what it cannot read with InvalidParams (#17).
func TestInferType(t *testing.T) {
	t.Parallel()
	c := start(t)
	initialize(t, c)
	const sample = "apiVersion: rules.example.com/v1\nkind: ReplicaLimit\nmetadata: {name: a}\nspec:\n  max: 3\n  teams: []\n---\napiVersion: rules.example.com/v1\nkind: ReplicaLimit\nmetadata: {name: b}\nspec:\n  max: 1.5\n"
	for _, tt := range []struct {
		name  string
		args  map[string]any
		want  string
		notes []string
	}{
		{
			name: "YAML documents",
			args: map[string]any{"text": "a: 1\nb: [x]\n---\na: 2\n"},
			want: `{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"array","items":{"type":"string"}}}}`,
		},
		{
			name: "required when asked",
			args: map[string]any{"text": "a: 1\nb: [x]\n---\na: 2\n", "required": true},
			want: `{"type":"object","required":["a"],"properties":{"a":{"type":"integer"},"b":{"type":"array","items":{"type":"string"}}}}`,
		},
		{
			name: "a JSON list of objects",
			args: map[string]any{"text": `[{"port": 80}, {"port": "http"}, {"port": null}]`},
			want: `{"type":"object","properties":{"port":{"nullable":true,"x-kubernetes-int-or-string":true}}}`,
		},
		{
			name:  "kinds that disagree",
			args:  map[string]any{"text": "a: true\nb: 1\n---\na: x\nb: 2.5\n"},
			want:  `{"type":"object","properties":{"a":{"x-kubernetes-preserve-unknown-fields":true},"b":{"type":"number"}}}`,
			notes: []string{"a: the examples have boolean and string here, which no type Kubernetes accepts holds together: it is left without a type"},
		},
		{
			name:  "the spec of the first document, by its line",
			args:  map[string]any{"text": sample, "line": 1, "path": "spec"},
			want:  `{"type":"object","properties":{"max":{"type":"integer"},"teams":{"type":"array","items":{"x-kubernetes-preserve-unknown-fields":true}}}}`,
			notes: []string{"teams: an empty list says nothing about its items"},
		},
		{
			name: "the second document, whole",
			args: map[string]any{"text": sample, "line": 8},
			want: `{"type":"object","properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"type":"object"},"spec":{"type":"object","properties":{"max":{"type":"number"}}}}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			_, res, rerr := c.command("cel-lens.inferType", tt.args, nil)
			if rerr != nil {
				t.Fatalf("inferType(%v) error = %+v", tt.args, rerr)
			}
			var got inferred
			if err := json.Unmarshal(res, &got); err != nil {
				t.Fatal(err)
			}
			if g, w := canonical(t, got.Schema), canonical(t, json.RawMessage(tt.want)); g != w {
				t.Errorf("inferType(%v) schema = %s, want %s", tt.args, g, w)
			}
			if tt.notes == nil {
				tt.notes = []string{}
			}
			if !slices.Equal(got.Notes, tt.notes) || got.Notes == nil {
				t.Errorf("inferType(%v) notes = %#v, want %#v", tt.args, got.Notes, tt.notes)
			}
		})
	}
	for _, tt := range []struct {
		name string
		args map[string]any
		msg  string
	}{
		{"no text", map[string]any{}, `cel-lens.inferType takes {text: "..."}`},
		{"no YAML", map[string]any{"text": "a: [\n"}, "cel-lens.inferType: the text does not parse: line 1"},
		{"no example", map[string]any{"text": "# only a comment\n"}, "cel-lens.inferType: no example"},
		{"a bad path", map[string]any{"text": "a: 1\n", "path": "a..b"}, "cel-lens.inferType: path: "},
		{"too deep", map[string]any{"text": strings.Repeat("[", 101) + strings.Repeat("]", 101)}, "over the limit of 100"},
		{"too big", map[string]any{"text": strings.Repeat("#", 1<<20+1)}, "over the limit of 1048576"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			_, _, rerr := c.command("cel-lens.inferType", tt.args, nil)
			if rerr == nil || rerr.Code != jsonrpc.CodeInvalidParams || !strings.Contains(rerr.Message, tt.msg) {
				t.Errorf("inferType(%.60v) error = %+v, want invalid params with %q", tt.args, rerr, tt.msg)
			}
		})
	}
}

// canonical is v as JSON with the keys of each object sorted.
func canonical(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// examplesPolicy is a policy whose params are ReplicaLimits and which matches
// Widgets, which no CustomResourceDefinition of the workspace defines, and
// Deployments.
const examplesPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits
spec:
  paramKind: {apiVersion: rules.example.com/v1, kind: ReplicaLimit}
  matchConstraints:
    resourceRules:
      - {apiGroups: [example.com], apiVersions: [v1], resources: [widgets]}
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments, deployments/scale]}
  validations:
    - expression: "object.spec.replicas <= params.spec.max"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: endpoints
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [""], apiVersions: ["*"], resources: [endpoints]}
  validations:
    - expression: "true"
`

const limitText = "apiVersion: rules.example.com/v1\nkind: ReplicaLimit\nmetadata: {name: a}\nspec: {max: 3}\n"

type exampleDoc struct {
	URI, File        string
	Line             int
	APIVersion, Kind string
}

// TestTypeChoicesExamples checks that typeChoices, given the arguments of a
// line, lists the workspace documents of the kind of the value: a policy's
// paramKind for params, the kinds it matches for object, with file, line,
// apiVersion and kind; that it walks the workspace as the CRD scan does,
// skipping hidden folders, node_modules and files over the size limit, and
// reads an open file as the editor has it; and that a value with no kind
// gets none (#17).
func TestTypeChoicesExamples(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, text := range map[string]string{
		"policy.yaml":                  examplesPolicy,
		"config/samples/limit.yaml":    limitText + "---\n# another\n" + limitText + "---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\n---\napiVersion: rules.example.com/v1beta1\nkind: ReplicaLimit\nmetadata: {name: old}\n---\napiVersion: other.example.com/v1\nkind: ReplicaLimit\nmetadata: {name: o}\n",
		"config/samples/widget.yaml":   "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\n",
		"config/samples/widgetv2.yaml": "apiVersion: example.com/v2\nkind: Widget\nmetadata: {name: w}\n",
		"deploy.yml":                   "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: d}\n",
		"endpoints.yaml":               "apiVersion: v1\nkind: Endpoints\nmetadata: {name: e}\n",
		"open.yaml":                    "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: o}\n",
		"notes.txt":                    limitText,
		".hidden/limit.yaml":           limitText,
		"node_modules/pkg/limit.yaml":  limitText,
		"big.yaml":                     limitText + strings.Repeat("#", 1<<20),
		"broken.yaml":                  "a: [\n---\n" + limitText,
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, text)
	}
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	// The editor's text of open.yaml, not the disk's, counts.
	c.openURI(fileURI(filepath.Join(dir, "open.yaml")), "# edited\n"+limitText)
	args := func(variable string) map[string]any {
		return map[string]any{"cellens": fileURI(filepath.Join(dir, ".cellens")), "file": "policy.yaml", "document": "ValidatingAdmissionPolicy limits", "variable": variable, "value": variable}
	}
	limit := func(file string, line int) exampleDoc {
		return exampleDoc{fileURI(filepath.Join(dir, filepath.FromSlash(file))), file, line, "rules.example.com/v1", "ReplicaLimit"}
	}
	for _, tt := range []struct {
		name string
		args map[string]any
		want []exampleDoc
	}{
		{"params", args("params"), []exampleDoc{
			limit("broken.yaml", 3), limit("config/samples/limit.yaml", 1), limit("config/samples/limit.yaml", 7),
			{fileURI(filepath.Join(dir, "config", "samples", "limit.yaml")), "config/samples/limit.yaml", 16, "rules.example.com/v1beta1", "ReplicaLimit"},
			limit("open.yaml", 2),
		}},
		// The review of #25: a kind in another version of its group is one,
		// as the API server converts it to the rule's version.
		{"object", args("object"), []exampleDoc{
			{fileURI(filepath.Join(dir, "config", "samples", "widget.yaml")), "config/samples/widget.yaml", 1, "example.com/v1", "Widget"},
			{fileURI(filepath.Join(dir, "config", "samples", "widgetv2.yaml")), "config/samples/widgetv2.yaml", 1, "example.com/v2", "Widget"},
			{fileURI(filepath.Join(dir, "deploy.yml")), "deploy.yml", 1, "apps/v1", "Deployment"},
		}},
		{"oldObject, with a path", with(args("oldObject"), "path", "spec"), []exampleDoc{
			{fileURI(filepath.Join(dir, "config", "samples", "widget.yaml")), "config/samples/widget.yaml", 1, "example.com/v1", "Widget"},
			{fileURI(filepath.Join(dir, "config", "samples", "widgetv2.yaml")), "config/samples/widgetv2.yaml", 1, "example.com/v2", "Widget"},
			{fileURI(filepath.Join(dir, "deploy.yml")), "deploy.yml", 1, "apps/v1", "Deployment"},
		}},
		{"a built-in kind with an irregular plural, in any version (review of #17)", with(args("object"), "document", "ValidatingAdmissionPolicy endpoints"), []exampleDoc{
			{fileURI(filepath.Join(dir, "endpoints.yaml")), "endpoints.yaml", 1, "v1", "Endpoints"},
		}},
		{"a variable has no kind", args("variables.max"), []exampleDoc{}},
		{"no such policy", with(args("params"), "document", "ValidatingAdmissionPolicy other"), []exampleDoc{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			_, res, rerr := c.command("cel-lens.typeChoices", tt.args, nil)
			if rerr != nil {
				t.Fatalf("typeChoices(%v) error = %+v", tt.args, rerr)
			}
			var got struct {
				BuiltIn  []choice
				Examples *[]exampleDoc
			}
			if err := json.Unmarshal(res, &got); err != nil {
				t.Fatal(err)
			}
			if got.Examples == nil || !slices.Equal(*got.Examples, tt.want) || len(got.BuiltIn) == 0 {
				t.Errorf("typeChoices(%v) examples = %+v, want %+v, beside the kinds", tt.args, got.Examples, tt.want)
			}
		})
	}
	t.Run("no arguments, no examples", func(t *testing.T) {
		c.use(t)
		_, res, rerr := c.command("cel-lens.typeChoices", nil, nil)
		if rerr != nil || strings.Contains(string(res), `"examples"`) {
			t.Errorf("typeChoices() = %s, %+v, want no examples", res, rerr)
		}
	})
	t.Run("a cellens outside the workspace", func(t *testing.T) {
		c.use(t)
		bad := with(args("params"), "cellens", "file:///elsewhere/.cellens")
		if _, _, rerr := c.command("cel-lens.typeChoices", bad, nil); rerr == nil || rerr.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("typeChoices(%v) error = %+v, want invalid params", bad, rerr)
		}
	})
}

// valueKindsWidget is a CustomResourceDefinition of example.com/v1 Widget,
// served as widgets, which examplesPolicy matches.
const valueKindsWidget = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema: {type: object, properties: {spec: {type: object, properties: {replicas: {type: integer}}}}}
`

// kindsWorkspace starts a server on a workspace with examplesPolicy and the
// CustomResourceDefinition of its widgets, open, and returns the client and
// the arguments of a line for a value of the policy named name.
func kindsWorkspace(t *testing.T) (*client, func(name, variable string) map[string]any) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "policy.yaml"), examplesPolicy)
	writeFile(t, filepath.Join(dir, "crd.yaml"), valueKindsWidget)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	c.openAll(fileURI(filepath.Join(dir, "crd.yaml")), valueKindsWidget)
	return c, func(name, variable string) map[string]any {
		return map[string]any{"cellens": fileURI(filepath.Join(dir, ".cellens")), "file": "policy.yaml", "document": "ValidatingAdmissionPolicy " + name, "variable": variable, "value": variable}
	}
}

// TestTypeChoicesValueKinds is #25: typeChoices, given a value, answers the
// kinds its root can be: the paramKind for params, the kinds the policy
// matches for object and oldObject, with a path too, and none when they are
// not all known, as with a wildcard, or the value has no kind; without a
// value it answers none.
func TestTypeChoicesValueKinds(t *testing.T) {
	t.Parallel()
	c, args := kindsWorkspace(t)
	type kind struct{ APIVersion, Kind string }
	limit := []kind{{"rules.example.com/v1", "ReplicaLimit"}}
	matched := []kind{{"example.com/v1", "Widget"}, {"apps/v1", "Deployment"}}
	for _, tt := range []struct {
		name string
		args map[string]any
		want []kind
	}{
		{"params", args("limits", "params"), limit},
		{"params, with a path", with(args("limits", "params"), "path", "spec"), limit},
		{"object", args("limits", "object"), matched},
		{"oldObject, with a path", with(args("limits", "oldObject"), "path", "spec.replicas"), matched},
		{"a wildcard version", args("endpoints", "object"), []kind{}},
		{"no paramKind", args("endpoints", "params"), []kind{}},
		{"a variable", args("limits", "variables.max"), []kind{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			_, res, rerr := c.command("cel-lens.typeChoices", tt.args, nil)
			if rerr != nil {
				t.Fatalf("typeChoices(%v) error = %+v", tt.args, rerr)
			}
			var got struct{ ValueKinds *[]kind }
			if err := json.Unmarshal(res, &got); err != nil {
				t.Fatal(err)
			}
			if got.ValueKinds == nil || !slices.Equal(*got.ValueKinds, tt.want) {
				t.Errorf("typeChoices(%v) valueKinds = %+v, want %+v", tt.args, got.ValueKinds, tt.want)
			}
		})
	}
	t.Run("no value", func(t *testing.T) {
		c.use(t)
		if _, res, rerr := c.command("cel-lens.typeChoices", nil, nil); rerr != nil || strings.Contains(string(res), `"valueKinds"`) {
			t.Errorf("typeChoices() = %s, %+v, want no valueKinds", res, rerr)
		}
	})
}

// TestInferTypeRoots is #25: inferType answers the apiVersion and kind at
// the root of each example, null for one without; given the value, as
// typeChoices takes it, and a path, an example whose root is none of the
// value's kinds, such as a pasted value or another kind, is the value
// itself, and a note says so, while one of its kind gives the value at the
// path; when the value's kinds are not known, every example gives the value
// at the path, as before.
func TestInferTypeRoots(t *testing.T) {
	t.Parallel()
	c, args := kindsWorkspace(t)
	type kind struct{ APIVersion, Kind string }
	for _, tt := range []struct {
		name  string
		args  map[string]any
		roots []*kind
		want  string
		notes []string
	}{
		{
			name:  "roots without a value",
			args:  map[string]any{"text": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\n---\na: 1\n---\n[{kind: X}, {apiVersion: v1, kind: Secret}]\n"},
			roots: []*kind{{"v1", "ConfigMap"}, nil, nil, {"v1", "Secret"}},
			want:  `{"type":"object","properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"type":"object"},"a":{"type":"integer"}}}`,
		},
		{
			name:  "a whole resource and a pasted value",
			args:  map[string]any{"text": limitText + "---\nmax: 4\nteam: a\n", "path": "spec", "value": args("limits", "params")},
			roots: []*kind{{"rules.example.com/v1", "ReplicaLimit"}, nil},
			want:  `{"type":"object","properties":{"max":{"type":"integer"},"team":{"type":"string"}}}`,
			notes: []string{"the value: the example at line 6 has no apiVersion and kind, so it is not of rules.example.com/v1 ReplicaLimit: it is taken as the value at spec itself"},
		},
		{
			name:  "another kind",
			args:  map[string]any{"text": "apiVersion: v1\nkind: ConfigMap\ndata: {a: b}\n", "path": "spec", "value": args("limits", "object")},
			roots: []*kind{{"v1", "ConfigMap"}},
			want:  `{"type":"object","properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"data":{"type":"object","properties":{"a":{"type":"string"}}}}}`,
			notes: []string{"the value: the example at line 1 is of kind v1 ConfigMap, not of example.com/v1 Widget or apps/v1 Deployment: it is taken as the value at spec itself"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			_, res, rerr := c.command("cel-lens.inferType", tt.args, nil)
			if rerr != nil {
				t.Fatalf("inferType(%v) error = %+v", tt.args, rerr)
			}
			var got struct {
				Schema map[string]any
				Notes  []string
				Roots  []*kind
			}
			if err := json.Unmarshal(res, &got); err != nil {
				t.Fatal(err)
			}
			if g, w := canonical(t, got.Schema), canonical(t, json.RawMessage(tt.want)); g != w {
				t.Errorf("inferType(%v) schema = %s, want %s", tt.args, g, w)
			}
			if !reflect.DeepEqual(got.Roots, tt.roots) {
				t.Errorf("inferType(%v) roots = %s, want %+v", tt.args, res, tt.roots)
			}
			if tt.notes == nil {
				tt.notes = []string{}
			}
			if !slices.Equal(got.Notes, tt.notes) {
				t.Errorf("inferType(%v) notes = %q, want %q", tt.args, got.Notes, tt.notes)
			}
		})
	}
	t.Run("kinds not known: the value at the path, as before", func(t *testing.T) {
		c.use(t)
		a := map[string]any{"text": "max: 4\n", "path": "spec", "value": args("endpoints", "object")}
		if _, _, rerr := c.command("cel-lens.inferType", a, nil); rerr == nil || !strings.Contains(rerr.Message, "no example has a value at spec") {
			t.Errorf("inferType(%v) error = %+v, want no example with a value at spec", a, rerr)
		}
	})
	t.Run("a value that cannot be read", func(t *testing.T) {
		c.use(t)
		a := map[string]any{"text": "max: 4\n", "value": with(args("limits", "params"), "cellens", "file:///elsewhere/.cellens")}
		if _, _, rerr := c.command("cel-lens.inferType", a, nil); rerr == nil || rerr.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("inferType(%v) error = %+v, want invalid params", a, rerr)
		}
	})
}
