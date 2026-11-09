package lsp_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// limitPolicy is a ValidatingAdmissionPolicy whose params are dyn: no CRD
// defines its paramKind.
const limitPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: replica-limit
spec:
  paramKind: {apiVersion: example.com/v1, kind: Limit}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  validations:
    - expression: "object.spec.replicas <= params.maxReplicas"
    - expression: "params.team.startsWith('a') && params.extra == 'x'"
    - expression: "params.surge == 1 || params.surge == 'x'"
`

// limitSchema types the params of limitPolicy, in policy.yaml beside it,
// with a schema as a CRD's openAPIV3Schema: one that keeps unknown fields
// and has an x-kubernetes-int-or-string field.
const limitSchema = `types:
  - file: policy.yaml
    document: ValidatingAdmissionPolicy replica-limit
    variable: params
    schema:
      type: object
      x-kubernetes-preserve-unknown-fields: true
      properties:
        maxReplicas: {type: integer, minimum: 0}
        team: {type: string}
        surge: {x-kubernetes-int-or-string: true}
`

// TestLensSchema checks that a schema: entry types the value as the API
// server types a CRD's schema for CEL, unknown fields it keeps included, and
// that hover says where the type comes from (#15).
func TestLensSchema(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "policy.yaml"), limitPolicy)
	writeFile(t, filepath.Join(dir, ".cellens"), limitSchema)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	policy := fileURI(filepath.Join(dir, "policy.yaml"))
	c.openURI(policy, limitPolicy)
	// The API server gives CEL no field a schema does not declare, even
	// with x-kubernetes-preserve-unknown-fields, and an int-or-string is dyn.
	const extra = "apps/v1 Deployment: undefined field 'extra' (the type comes from .cellens:2: the API server would not report this)"
	diags := c.diagnosticsOf(policy, hasMessage(extra))
	if len(diags) != 1 || diags[0].Severity != 2 {
		t.Errorf("diagnostics = %+v, want only the warning %q", diags, extra)
	}
	var hover struct{ Contents struct{ Value string } }
	c.request("textDocument/hover", policy, limitPolicy, "params.maxReplicas", len("params.max"), &hover)
	if got := hover.Contents.Value; !strings.Contains(got, "(field) maxReplicas: int") || !strings.Contains(got, "Typed from the schema at .cellens, line 5") {
		t.Errorf("hover on maxReplicas = %q, want int, typed from the schema at .cellens, line 5", got)
	}
	c.request("textDocument/hover", policy, limitPolicy, "params.maxReplicas", len("par"), &hover)
	if got := hover.Contents.Value; !strings.Contains(got, "(variable) params: {maxReplicas: int, surge: dyn, team: string}") {
		t.Errorf("hover on params = %q, want the schema's type as a type: entry writes it", got)
	}
	lines := c.codeLenses(policy)
	if len(lines) != 3 {
		t.Fatalf("code lenses = %+v, want three lines", lines)
	}
	l := lines[0]
	if !reflect.DeepEqual(l.titles, []string{"object: apps/v1 Deployment · compiles", "params: {maxReplicas, …} · Edit", "Remove"}) {
		t.Errorf("code lenses = %+v, want params with the schema's fields, cut as for a type:", l)
	}
	if s, _ := l.target["schema"].(map[string]any); s["type"] != "object" || l.target["type"] != nil {
		t.Errorf("code lens target = %v, want the entry's schema", l.target)
	}
}

// TestLensSchemaInCRD checks that in a CustomResourceDefinition a schema:
// entry, like type:, types only an x-kubernetes-int-or-string value, as an
// integer or a string (#15).
func TestLensSchemaInCRD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crd.yaml"), lensCRD)
	text := strings.Replace(lensText, "    type: int\n", "    schema: {type: integer, minimum: 0}\n", 1)
	writeFile(t, filepath.Join(dir, ".cellens"), text)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	crd := fileURI(filepath.Join(dir, "crd.yaml"))
	c.openURI(crd, lensCRD)
	c.diagnosticsOf(crd, hasMessage(configWarning))
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c.openURI(lens, strings.Replace(text, "schema: {type: integer, minimum: 0}", "schema: {type: object}", 1)+`  - file: crd.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.name
    schema: {type: string}
`)
	diags := c.diagnosticsOf(lens, func(d []diagnostic) bool { return len(d) == 2 })
	want := []string{
		"spec.config is x-kubernetes-int-or-string, an int or a string to a rule: give it int or string",
		"spec.name is already typed as string: remove this entry",
	}
	for i, w := range want {
		if diags[i].Message != w || diags[i].Severity != 2 {
			t.Errorf("diagnostic %d of .cellens = %+v, want the warning %q", i, diags[i], w)
		}
	}
}

// TestLensSchemaProblems checks that a schema the API server would refuse as
// a CRD's schema is an error on the .cellens, at the line of the offending
// key, in the API server's words, and its entry types nothing (#15).
func TestLensSchemaProblems(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "policy.yaml"), limitPolicy)
	text := strings.NewReplacer("{type: integer, minimum: 0}", "{type: int}", "team: {type: string}", "team: {type: string, maxLenght: 3}").Replace(limitSchema)
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c.openURI(lens, text)
	diags := c.diagnosticsOf(lens, func([]diagnostic) bool { return true })
	if len(diags) != 1 || diags[0].Severity != 1 || diags[0].Range.Start.Line != 9 ||
		diags[0].Message != `schema: properties[team]: unknown field "maxLenght"` {
		t.Errorf("diagnostics of .cellens = %+v, want the unknown field as an error on line 10", diags)
	}
	c.changeURI(lens, 2, strings.Replace(text, ", maxLenght: 3", "", 1))
	diags = c.diagnosticsOf(lens, func(d []diagnostic) bool { return len(d) > 0 && d[0].Severity == 1 })
	if len(diags) != 1 || diags[0].Range.Start.Line != 8 || diags[0].Range.Start.Character != 22 ||
		diags[0].Message != `schema: properties[maxReplicas].type: Unsupported value: "int": supported values: "array", "boolean", "integer", "number", "object", "string"` {
		t.Errorf("diagnostics of .cellens = %+v, want the API server's error on the type of maxReplicas", diags)
	}
	policy := fileURI(filepath.Join(dir, "policy.yaml"))
	c.openURI(policy, limitPolicy)
	if diags := c.diagnosticsOf(policy, func([]diagnostic) bool { return true }); len(diags) != 0 {
		t.Errorf("diagnostics of the policy = %+v, want none: the refused schema types nothing", diags)
	}
}

// quotaTypes is a Go package of the workspace, api/v1, as kubebuilder lays
// one out.
const quotaTypes = `package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"example.com/other"
)

// QuotaSpec is the quota of a team.
// +kubebuilder:object:root=true
type QuotaSpec struct {
	// MaxReplicas is the most replicas a team may run.
	MaxReplicas int32       ` + "`json:\"maxReplicas\"`" + `
	Team        string      ` + "`json:\"team,omitempty\"`" + `
	Since       metav1.Time ` + "`json:\"since,omitempty\"`" + `
	Owner       other.Owner ` + "`json:\"owner,omitempty\"`" + `
}
`

// quotaPolicy reads params typed from QuotaSpec.
const quotaPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: quota
spec:
  paramKind: {apiVersion: example.com/v1, kind: Quota}
  validations:
    - expression: "params.maxReplicas > 0 && params.team.size() > 0 && params.since > timestamp('2020-01-01T00:00:00Z')"
    - expression: "params.owner == 'me'"
`

// quotaLens types the params of quotaPolicy with QuotaSpec.
const quotaLens = `types:
  - file: deploy/quota.yaml
    document: ValidatingAdmissionPolicy quota
    variable: params
    go: ./api/v1#QuotaSpec
`

// goWorkspace writes a workspace with api/v1 and deploy/quota.yaml, and the
// .cellens text at its root, and returns its folder.
func goWorkspace(t *testing.T, lens string) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"api/v1", "deploy", "api/v1/testdata", "vendor/x"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, "api", "v1", "quota_types.go"), quotaTypes)
	writeFile(t, filepath.Join(dir, "api", "v1", "quota_test.go"), "package v1\n\ntype TestSpec struct {\n\tA int `json:\"a\"`\n}\n")
	writeFile(t, filepath.Join(dir, "api", "v1", "testdata", "x.go"), "package x\n\ntype Fixture struct {\n\tA int `json:\"a\"`\n}\n")
	writeFile(t, filepath.Join(dir, "vendor", "x", "x.go"), "package x\n\ntype Vendored struct {\n\tA int `json:\"a\"`\n}\n")
	writeFile(t, filepath.Join(dir, "deploy", "quota.yaml"), quotaPolicy)
	writeFile(t, filepath.Join(dir, ".cellens"), lens)
	return dir
}

// TestLensGo checks that a go: entry types the value with the Go struct, read
// from the package's files as controller-gen reads them, that a field it
// cannot read is a warning on the entry naming the field while the others are
// used, and that hover says which Go type and file the type comes from (#15).
func TestLensGo(t *testing.T) {
	t.Parallel()
	dir := goWorkspace(t, quotaLens)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	// The search for .cellens files finds it, and its problems are published.
	lens := fileURI(filepath.Join(dir, ".cellens"))
	got := c.diagnosticsOf(lens, func(d []diagnostic) bool { return len(d) > 0 })
	const left = "go: QuotaSpec.Owner: type other.Owner is from package example.com/other, which cel-lens does not read: the field is left out"
	if len(got) != 1 || got[0].Message != left || got[0].Severity != 2 || got[0].Range.Start.Line != 1 {
		t.Errorf("diagnostics of .cellens = %+v, want the warning %q on the entry", got, left)
	}
	policy := fileURI(filepath.Join(dir, "deploy", "quota.yaml"))
	c.openURI(policy, quotaPolicy)
	const owner = "undefined field 'owner' (the type comes from .cellens:2: the API server would not report this)"
	diags := c.diagnosticsOf(policy, hasMessage(owner))
	if len(diags) != 1 {
		t.Errorf("diagnostics = %+v, want only the warning %q: maxReplicas, team and since are typed", diags, owner)
	}
	var hover struct{ Contents struct{ Value string } }
	c.request("textDocument/hover", policy, quotaPolicy, "params.maxReplicas", len("params.max"), &hover)
	if got := hover.Contents.Value; !strings.Contains(got, "(field) maxReplicas: int") || !strings.Contains(got, "MaxReplicas is the most replicas a team may run.") ||
		!strings.Contains(got, "Typed from the Go type QuotaSpec in api/v1/quota_types.go:10, named in .cellens, line 2") {
		t.Errorf("hover on maxReplicas = %q, want int, its doc, and the Go type and file it is from", got)
	}
	lines := c.codeLenses(policy)
	if len(lines) != 2 {
		t.Fatalf("code lenses = %+v, want two lines", lines)
	}
	if l := lines[0]; !reflect.DeepEqual(l.titles, []string{"object: dyn · compiles", "params: {maxReplicas, since, team} · Edit", "Remove"}) || l.target["go"] != "./api/v1#QuotaSpec" {
		t.Errorf("code lenses = %+v, want params with the struct's fields and the entry's go", l)
	}
}

// TestLensGoProblems checks that a Go type that cannot be read is a warning
// on its entry saying why, and the entry types nothing (#15).
func TestLensGoProblems(t *testing.T) {
	t.Parallel()
	entry := func(g string) string {
		return "  - file: deploy/quota.yaml\n    document: ValidatingAdmissionPolicy quota\n    variable: " + g + "\n"
	}
	text := "types:\n" + entry("params\n    go: ./nosuch#QuotaSpec") + entry("object\n    go: ./api/v1#Nope") +
		entry("oldObject\n    go: ../outside#QuotaSpec") + entry("variables.x\n    go: ./deploy#QuotaSpec")
	dir := goWorkspace(t, text)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c.openURI(lens, text)
	got := c.diagnosticsOf(lens, func(d []diagnostic) bool { return len(d) == 4 })
	want := []string{
		"go: ./nosuch#QuotaSpec: the folder does not exist",
		"go: ./api/v1#Nope: the package declares no type Nope",
		"go: ../outside#QuotaSpec: the folder is outside the workspace folder, and cel-lens reads Go only there",
		"go: ./deploy#QuotaSpec: the folder has no .go files but tests",
	}
	for i, w := range want {
		if got[i].Message != w || got[i].Severity != 2 {
			t.Errorf("diagnostic %d of .cellens = %+v, want the warning %q", i, got[i], w)
		}
	}
}

// TestGoTypeChoices checks that typeChoices lists the Go struct types of the
// workspace a go: entry can name, with their doc's first sentence, and none
// of tests, testdata or vendor (#15).
func TestGoTypeChoices(t *testing.T) {
	t.Parallel()
	dir := goWorkspace(t, quotaLens)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	_, res, rerr := c.command("cel-lens.typeChoices", nil, nil)
	if rerr != nil {
		t.Fatalf("typeChoices error = %+v", rerr)
	}
	var got struct{ GoTypes []map[string]string }
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatal(err)
	}
	want := []map[string]string{{"folder": fileURI(filepath.Join(dir, "api", "v1")), "name": "QuotaSpec", "doc": "QuotaSpec is the quota of a team."}}
	if !reflect.DeepEqual(got.GoTypes, want) {
		t.Errorf("typeChoices goTypes = %v, want %v", got.GoTypes, want)
	}
}

// TestCheckTypeSchemaAndGo checks that checkType takes a schema and a Go
// type, alone or for a value (#15).
func TestCheckTypeSchemaAndGo(t *testing.T) {
	t.Parallel()
	dir := goWorkspace(t, "")
	lens := fileURI(filepath.Join(dir, ".cellens"))
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	value := map[string]any{"cellens": lens, "file": "deploy/quota.yaml", "document": "ValidatingAdmissionPolicy quota", "variable": "params", "value": "params"}
	for _, tt := range []struct {
		name    string
		args    map[string]any
		message string
		warning bool
	}{
		{"a schema", map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"max": map[string]any{"type": "integer"}}}}, "", false},
		{"a schema the API server refuses", map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"max": map[string]any{"type": "int"}}}}, `schema: properties[max].type: Unsupported value: "int": supported values: "array", "boolean", "integer", "number", "object", "string"`, false},
		{"a schema for a value", with(value, "schema", map[string]any{"type": "object"}), "", false},
		{"a Go type alone", map[string]any{"cellens": lens, "go": "./api/v1#QuotaSpec"}, "go: QuotaSpec.Owner: type other.Owner is from package example.com/other, which cel-lens does not read: the field is left out", true},
		{"no such Go type", map[string]any{"cellens": lens, "go": "./api/v1#Nope"}, "go: ./api/v1#Nope: the package declares no type Nope", false},
		{"a Go type for a value", with(value, "go", "./api/v1#QuotaSpec"), "go: QuotaSpec.Owner: type other.Owner is from package example.com/other, which cel-lens does not read: the field is left out", true},
		{"a Go type for a value that is not there", with(with(value, "variable", "variables.x"), "go", "./api/v1#QuotaSpec"), "ValidatingAdmissionPolicy quota has no variable x in spec.variables", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			_, res, rerr := c.command("cel-lens.checkType", tt.args, nil)
			if rerr != nil {
				t.Fatalf("checkType(%v) error = %+v", tt.args, rerr)
			}
			if tt.message == "" {
				if string(res) != "null" {
					t.Errorf("checkType(%v) = %s, want null", tt.args, res)
				}
				return
			}
			var p struct {
				Message string
				Warning bool
			}
			if err := json.Unmarshal(res, &p); err != nil || p.Message != tt.message || p.Warning != tt.warning {
				t.Errorf("checkType(%v) = %s, want message %q, warning %t", tt.args, res, tt.message, tt.warning)
			}
		})
	}
	t.Run("a Go type without its .cellens", func(t *testing.T) {
		c.use(t)
		if _, _, rerr := c.command("cel-lens.checkType", map[string]any{"go": "./api/v1#QuotaSpec"}, nil); rerr == nil {
			t.Error("checkType(go without cellens) error = nil, want one: the folder is relative to the .cellens")
		}
	})
}

// TestSetTypeSchemaAndGo checks that setType writes a schema as a block of
// YAML below schema:, and a Go type's folder relative to the .cellens the
// entry goes in (#15).
func TestSetTypeSchemaAndGo(t *testing.T) {
	t.Parallel()
	const subText = "# deploy's types.\ntypes:\n  # mine\n  - file: other.yaml\n    document: ValidatingAdmissionPolicy other\n    variable: params\n    type: int\n"
	dir := goWorkspace(t, "types:\n")
	writeFile(t, filepath.Join(dir, "deploy", ".cellens"), subText)
	root, sub := fileURI(filepath.Join(dir, ".cellens")), fileURI(filepath.Join(dir, "deploy", ".cellens"))
	c := start(t)
	initializeIn(t, c, dir, editing("create", "delete"))
	// The arguments name the value from the root's .cellens: deploy's is nearer.
	value := map[string]any{"cellens": root, "file": "deploy/quota.yaml", "document": "ValidatingAdmissionPolicy quota", "variable": "params", "value": "params"}
	edit, _, rerr := c.command("cel-lens.setType", with(value, "go", "./api/v1#QuotaSpec"), applied)
	if rerr != nil {
		t.Fatalf("setType error = %+v", rerr)
	}
	dc := edit.Edit.DocumentChanges[0]
	entry := "  - file: quota.yaml\n    document: ValidatingAdmissionPolicy quota\n    variable: params\n"
	if got, want := apply(t, subText, dc.Edits), subText+entry+"    go: ../api/v1#QuotaSpec\n"; dc.TextDocument.URI != sub || got != want {
		t.Errorf("setType(go) edits %s to\n%s\nwant %s to\n%s", dc.TextDocument.URI, got, sub, want)
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{"maxReplicas": map[string]any{"type": "integer", "minimum": 0}}}
	edit, _, rerr = c.command("cel-lens.setType", with(value, "schema", schema), applied)
	if rerr != nil {
		t.Fatalf("setType error = %+v", rerr)
	}
	want := subText + entry + "    schema:\n      properties:\n        maxReplicas:\n          minimum: 0\n          type: integer\n      type: object\n"
	if got := apply(t, subText, edit.Edit.DocumentChanges[0].Edits); got != want {
		t.Errorf("setType(schema) gives\n%s\nwant\n%s", got, want)
	}
	if _, _, rerr := c.command("cel-lens.setType", with(with(value, "go", "./api/v1#QuotaSpec"), "type", "int"), applied); rerr == nil || !strings.Contains(rerr.Message, "not type and go") {
		t.Errorf("setType with type and go error = %+v, want one: give one", rerr)
	}
	if _, _, rerr := c.command("cel-lens.setType", with(value, "schema", map[string]any{"type": "int"}), applied); rerr == nil || !strings.Contains(rerr.Message, `schema: type: Unsupported value: "int"`) {
		t.Errorf("setType with a refused schema error = %+v, want the API server's words", rerr)
	}
}

// TestLensGoBudget checks that a Go type whose schema would double with each
// of its types types the fields past 20000 schemas as dyn: a policy that
// reads one gets no false undefined-field warning, and the entry's warning
// names the field (the review of 12c9469).
func TestLensGoBudget(t *testing.T) {
	t.Parallel()
	var src strings.Builder
	src.WriteString("package v1\n")
	for i := range 26 {
		fmt.Fprintf(&src, "type T%d struct {\n\tA T%d `json:\"a\"`\n\tB T%d `json:\"b\"`\n}\n", i, i+1, i+1)
	}
	src.WriteString("type T26 struct {\n\tX string `json:\"x\"`\n}\n")
	dir := goWorkspace(t, strings.Replace(quotaLens, "QuotaSpec", "T0", 1))
	writeFile(t, filepath.Join(dir, "api", "v1", "t.go"), src.String())
	policy := strings.Replace(quotaPolicy, `"params.maxReplicas > 0 && params.team.size() > 0 && params.since > timestamp('2020-01-01T00:00:00Z')"`, `"params.b.anything == 1 && params.a.a.b.a.x == 'y'"`, 1)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	lens := fileURI(filepath.Join(dir, ".cellens"))
	got := c.diagnosticsOf(lens, func(d []diagnostic) bool { return len(d) > 0 })
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, "go: T25.B, at a.a.") || !strings.Contains(got[0].Message, "of T0: the schema grows past 20000 schemas here") {
		t.Errorf("diagnostics of .cellens = %+v, want the field where the schema stops growing", got)
	}
	uri := fileURI(filepath.Join(dir, "deploy", "quota.yaml"))
	c.openURI(uri, policy)
	const owner = "undefined field 'owner' (the type comes from .cellens:2: the API server would not report this)"
	if diags := c.diagnosticsOf(uri, hasMessage(owner)); len(diags) != 1 {
		t.Errorf("diagnostics = %+v, want only the warning about owner: b is dyn and a.a.b.a.x a string", diags)
	}
}
