package goschema_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/goschema"
)

// quota is a package in the style of kubebuilder: a spec with json tags,
// pointers, inline fields and Kubernetes types.
const quota = `package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"example.com/other"
)

// QuotaSpec is the quota of a team.
// +kubebuilder:object:root=true
type QuotaSpec struct {
	metav1.TypeMeta ` + "`json:\",inline\"`" + `
	// Metadata is the object's metadata.
	Metadata metav1.ObjectMeta ` + "`json:\"metadata,omitempty\"`" + `

	// MaxReplicas is the most replicas.
	// +kubebuilder:validation:Minimum=0
	MaxReplicas int32 ` + "`json:\"maxReplicas\"`" + `
	Team        *string ` + "`json:\"team\"`" + `
	Labels      map[string]string ` + "`json:\"labels,omitempty\"`" + `
	Ports       []Port ` + "`json:\"ports,omitempty\"`" + `
	Phase       Phase ` + "`json:\"phase\"`" + `
	Surge       intstr.IntOrString ` + "`json:\"surge,omitempty\"`" + `
	CPU         resource.Quantity ` + "`json:\"cpu,omitempty\"`" + `
	Since       metav1.Time ` + "`json:\"since,omitempty\"`" + `
	Every       metav1.Duration ` + "`json:\"every,omitempty\"`" + `
	Resources   corev1.ResourceRequirements ` + "`json:\"resources,omitempty\"`" + `
	Data        []byte ` + "`json:\"data,omitempty\"`" + `
	Common      ` + "`json:\",inline\"`" + `
	Internal    string ` + "`json:\"-\"`" + `
	hidden      string
	Owner       other.Owner ` + "`json:\"owner,omitempty\"`" + `
	ByID        map[int]string ` + "`json:\"byID,omitempty\"`" + `
	Untagged    string
	Any         interface{} ` + "`json:\"any,omitempty\"`" + `
	Next        *QuotaSpec ` + "`json:\"next,omitempty\"`" + `
}

// Port is a port.
type Port struct {
	Port int ` + "`json:\"port\"`" + `
}

// Phase is a phase.
type Phase string

type Common struct {
	Note string ` + "`json:\"note,omitempty\"`" + `
}

func (q QuotaSpec) Total() int { return 0 }
`

func TestRead(t *testing.T) {
	t.Parallel()
	got, err := goschema.Read([]goschema.File{{Name: "api/v1/quota_types.go", Src: []byte(quota)}}, "QuotaSpec")
	if err != nil {
		t.Fatalf("Read(QuotaSpec) error = %v", err)
	}
	if got.File != "api/v1/quota_types.go" || got.Line != 13 {
		t.Errorf("Read(QuotaSpec) at %s:%d, want api/v1/quota_types.go:13", got.File, got.Line)
	}
	s := got.Schema
	if s["description"] != "QuotaSpec is the quota of a team." || s["type"] != "object" {
		t.Errorf("Read(QuotaSpec) = %v, want an object with the type's doc", s)
	}
	props, _ := s["properties"].(map[string]any)
	want := map[string]string{
		"apiVersion":  `{"description":`,
		"metadata":    `{"description":"Metadata is the object's metadata.","type":"object"}`,
		"maxReplicas": `{"description":"MaxReplicas is the most replicas.","format":"int32","type":"integer"}`,
		"team":        `{"type":"string"}`,
		"labels":      `{"additionalProperties":{"type":"string"},"type":"object"}`,
		"ports":       `{"items":{"properties":{"port":{"type":"integer"}},"required":["port"],"type":"object"},"type":"array"}`,
		"phase":       `{"type":"string"}`,
		"surge":       `{"anyOf":[{"type":"integer"},{"type":"string"}],"x-kubernetes-int-or-string":true}`,
		"cpu":         `{"anyOf":[{"type":"integer"},{"type":"string"}],"pattern":`,
		"since":       `{"format":"date-time","type":"string"}`,
		"every":       `{"type":"string"}`,
		"resources":   `{"description":"ResourceRequirements describes`,
		"data":        `{"format":"byte","type":"string"}`,
		"note":        `{"type":"string"}`,
	}
	for name, w := range want {
		js, _ := json.Marshal(props[name])
		if !strings.HasPrefix(string(js), w) {
			t.Errorf("Read(QuotaSpec) property %s = %s, want %s", name, js, w)
		}
	}
	for _, name := range []string{"Internal", "internal", "hidden", "owner", "byID", "Untagged", "any", "next"} {
		if _, ok := props[name]; ok {
			t.Errorf("Read(QuotaSpec) has property %s, want it left out", name)
		}
	}
	if !reflect.DeepEqual(s["required"], []any{"maxReplicas", "phase"}) {
		t.Errorf("Read(QuotaSpec) required = %v, want maxReplicas and phase: omitempty and pointers are optional", s["required"])
	}
	wantProblems := []string{
		"QuotaSpec.Owner: type other.Owner is from package example.com/other, which cel-lens does not read: the field is left out",
		"QuotaSpec.ByID: a map's keys are strings in Kubernetes objects, and controller-gen refuses others: the field is left out",
		"QuotaSpec.Untagged: it has no json tag, which controller-gen needs: the field is left out",
		"QuotaSpec.Any: an interface has no schema, and controller-gen refuses it: the field is left out",
		"QuotaSpec.Next: QuotaSpec contains itself, which a schema cannot say: the field is left out",
	}
	if !reflect.DeepEqual(got.Problems, wantProblems) {
		t.Errorf("Read(QuotaSpec) problems =\n%s\nwant\n%s", strings.Join(got.Problems, "\n"), strings.Join(wantProblems, "\n"))
	}
}

func TestReadErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, src, typ, want string
	}{
		{"no such type", quota, "Nope", "the package declares no type Nope"},
		{"no schema", "package p\ntype F func()\n", "F", "F has no schema: F: a func or chan has no schema"},
		{"a file that does not parse", "package p\ntype A struct {\n", "B", "the package declares no type B, and"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := goschema.Read([]goschema.File{{Name: "a.go", Src: []byte(tc.src)}}, tc.typ)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Read(%s) error = %v, want one saying %q", tc.typ, err, tc.want)
			}
		})
	}
}

func TestReadAcrossFiles(t *testing.T) {
	t.Parallel()
	files := []goschema.File{
		{Name: "b.go", Src: []byte("package p\ntype Spec struct {\n\tItem Item `json:\"item\"`\n}\n")},
		{Name: "a.go", Src: []byte("package p\ntype Item struct {\n\tName string `json:\"name\"`\n}\n")},
	}
	got, err := goschema.Read(files, "Spec")
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(got.Schema)
	if want := `{"properties":{"item":{"properties":{"name":{"type":"string"}},"required":["name"],"type":"object"}},"required":["item"],"type":"object"}`; string(js) != want {
		t.Errorf("Read(Spec) = %s, want %s", js, want)
	}
}

func TestStructs(t *testing.T) {
	t.Parallel()
	src := quota + "\n// Bare has no json tags.\ntype Bare struct{ A int }\n\n// List is generic.\ntype List[T any] struct {\n\tItems []T `json:\"items\"`\n}\n\ntype (\n\t// Grouped is in a group. It has a second sentence.\n\tGrouped struct {\n\t\tA int `json:\"a\"`\n\t}\n\tlower struct {\n\t\tA int `json:\"a\"`\n\t}\n)\n"
	got := goschema.Structs("a.go", []byte(src))
	want := []goschema.Struct{
		{Name: "QuotaSpec", Doc: "QuotaSpec is the quota of a team."},
		{Name: "Port", Doc: "Port is a port."},
		{Name: "Common"},
		{Name: "Grouped", Doc: "Grouped is in a group."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Structs = %+v, want %+v", got, want)
	}
	if got := goschema.Structs("a.go", []byte("not go")); got != nil {
		t.Errorf("Structs(not go) = %v, want none", got)
	}
}

// FuzzRead checks that no Go source, however broken, makes Read or Structs
// fail other than with an error.
func FuzzRead(f *testing.F) {
	for _, s := range []string{quota, doubling(25), "package p\ntype T struct{ A T `json:\"a\"` }\n", "package p\ntype T [][][]map[string]*T\n", "package p\ntype T = U\ntype U struct{}\n", "\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, src string) {
		_, _ = goschema.Read([]goschema.File{{Name: "a.go", Src: []byte(src)}}, "T")
		_ = goschema.Structs("a.go", []byte(src))
	})
}

// doubling is a package whose types each use the next twice, n deep: written
// out, the schema of T0 has 2^n of Tn.
func doubling(n int) string {
	var b strings.Builder
	b.WriteString("package p\n")
	for i := range n {
		fmt.Fprintf(&b, "type T%d struct {\n\tA T%d `json:\"a\"`\n\tB T%d `json:\"b\"`\n}\n", i, i+1, i+1)
	}
	fmt.Fprintf(&b, "type T%d struct {\n\tX string `json:\"x\"`\n}\n", n)
	return b.String()
}

// TestReadBudget checks that a schema that would double with each type stops
// at 20000 schemas, with a problem naming the field, by its Go name and its
// path, and that the fields past it are dyn, not left out, so CEL reports no
// field of them missing (the reviews of 6f82d2a and 12c9469).
func TestReadBudget(t *testing.T) {
	t.Parallel()
	got, err := goschema.Read([]goschema.File{{Name: "a.go", Src: []byte(doubling(30))}}, "T0")
	if err != nil {
		t.Fatalf("Read(T0) error = %v", err)
	}
	const want = ", at a.a.a.a.a.a.a.a.a.a.a.a.a.a.a.a.a.a.b.b.b.b.b.a.a.b.b of T0: the schema grows past 20000 schemas here, as its types are used again and again: it and the fields after it are dyn, which CEL does not check"
	if len(got.Problems) != 1 || !strings.HasPrefix(got.Problems[0], "T26.B") || !strings.HasSuffix(got.Problems[0], want) {
		t.Errorf("Read(T0) problems = %q, want one naming the field T26.B%s", got.Problems, want)
	}
	js, _ := json.Marshal(got.Schema)
	if n := strings.Count(string(js), `"type"`); n > 20000 {
		t.Errorf("Read(T0) has %d schemas, want at most 20000", n)
	}
	props, _ := got.Schema["properties"].(map[string]any)
	b, _ := props["b"].(map[string]any)
	if dyn, _ := b["x-kubernetes-int-or-string"].(bool); !dyn || props["a"] == nil {
		t.Errorf("Read(T0) b = %v, want a dyn value past the budget, and a typed", b)
	}
	small, err := goschema.Read([]goschema.File{{Name: "a.go", Src: []byte(doubling(10))}}, "T0")
	if err != nil || len(small.Problems) > 0 {
		t.Errorf("Read(T0 of 10 types) = %v, %v, want all 2^10 written out", small.Problems, err)
	}
}

// TestReadBuiltFiles checks that Read reads only the files a go build on this
// machine compiles, of the folder's package: not those its build constraints
// or file name leave out, nor those of another package (the review of
// 6f82d2a).
func TestReadBuiltFiles(t *testing.T) {
	t.Parallel()
	spec := func(field string) string {
		return "type Spec struct {\n\t" + field + " string `json:\"" + strings.ToLower(field) + "\"`\n}\n"
	}
	other := "plan9"
	if runtime.GOOS == other {
		other = "linux"
	}
	files := []goschema.File{
		{Name: "a_ignored.go", Src: []byte("//go:build ignore\n\npackage p\n" + spec("Ignored"))},
		{Name: "b_other.go", Src: []byte("//go:build " + other + "\n\npackage p\n" + spec("Other"))},
		{Name: "c_" + other + ".go", Src: []byte("package p\n" + spec("Suffix"))},
		{Name: "d_tool.go", Src: []byte("package main\n" + spec("Tool"))},
		{Name: "e_main.go", Src: []byte("package p\n" + spec("Mine"))},
		{Name: "f_more.go", Src: []byte("package p\n")},
	}
	got, err := goschema.Read(files, "Spec")
	if err != nil {
		t.Fatalf("Read(Spec) error = %v", err)
	}
	if props, _ := got.Schema["properties"].(map[string]any); got.File != "e_main.go" || len(props) != 1 || props["mine"] == nil {
		t.Errorf("Read(Spec) = %s: %v, want the Spec of e_main.go, the one a build compiles", got.File, got.Schema)
	}
	if !goschema.Builds("x.go", []byte("package p\n")) || goschema.Builds("x.go", []byte("//go:build ignore\n\npackage p\n")) {
		t.Error("Builds = wrong, want a plain file built and an ignored one not")
	}
}
