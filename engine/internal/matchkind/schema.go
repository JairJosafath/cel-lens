package matchkind

import (
	"reflect"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// isResource reports whether the type of gvk is a stored resource rather than
// a list, an option or a subresource body: it has metadata and a list kind.
func isResource(gvk schema.GroupVersionKind, t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	if _, ok := t.FieldByName("ObjectMeta"); !ok {
		return false
	}
	return scheme.Scheme.Recognizes(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
}

// schemaOf builds the OpenAPI schema of Go type t as kube-openapi's generator
// does: field names from json tags, types from the Go kinds, and the OpenAPI
// type and format that a type such as metav1.Time declares for itself.
func schemaOf(t reflect.Type) *spec.Schema {
	return build(t, map[reflect.Type]bool{})
}

type openAPITyped interface{ OpenAPISchemaType() []string }

type openAPIFormatted interface{ OpenAPISchemaFormat() string }

type documented interface{ SwaggerDoc() map[string]string }

func build(t reflect.Type, active map[reflect.Type]bool) *spec.Schema {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if typed, ok := reflect.New(t).Interface().(openAPITyped); ok {
		s := &spec.Schema{SchemaProps: spec.SchemaProps{Type: typed.OpenAPISchemaType()}}
		if f, ok := reflect.New(t).Interface().(openAPIFormatted); ok {
			s.Format = f.OpenAPISchemaFormat()
		}
		return s
	}
	switch t.Kind() {
	case reflect.String:
		return &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}}
	case reflect.Bool:
		return &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"boolean"}}}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"integer"}}}
	case reflect.Float32, reflect.Float64:
		return &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"number"}}}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}, Format: "byte"}}
		}
		return spec.ArrayProperty(build(t.Elem(), active))
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return &spec.Schema{}
		}
		return spec.MapProperty(build(t.Elem(), active))
	case reflect.Struct:
		return structSchema(t, active)
	case reflect.Invalid, reflect.Uintptr, reflect.Complex64, reflect.Complex128, reflect.Chan,
		reflect.Func, reflect.Interface, reflect.Pointer, reflect.UnsafePointer:
	}
	return &spec.Schema{}
}

// structSchema is the object schema of struct type t. A type that contains
// itself ends there with an object without properties.
func structSchema(t reflect.Type, active map[reflect.Type]bool) *spec.Schema {
	s := &spec.Schema{SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"object"}}}
	if active[t] {
		return s
	}
	active[t] = true
	defer delete(active, t)
	s.Properties = map[string]spec.Schema{}
	s.Description = docs(t)[""]
	addFields(s, t, active)
	return s
}

// addFields adds the properties of the fields of struct type t to s, those of
// inlined structs as if they were t's own.
func addFields(s *spec.Schema, t reflect.Type, active map[reflect.Type]bool) {
	doc := docs(t)
	for f := range t.Fields() {
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" && opts == "" || !f.IsExported() && !f.Anonymous {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if name == "" && (f.Anonymous || strings.Contains(opts, "inline")) && ft.Kind() == reflect.Struct {
			addFields(s, ft, active)
			continue
		}
		if name == "" {
			name = f.Name
		}
		prop := *build(f.Type, active)
		if d, ok := doc[name]; ok {
			prop.Description = d
		}
		s.Properties[name] = prop
	}
}

// docs returns the descriptions of type t from its generated swagger doc, by
// json field name; the type's own is under "".
func docs(t reflect.Type) map[string]string {
	if d, ok := reflect.New(t).Interface().(documented); ok {
		return d.SwaggerDoc()
	}
	if d, ok := reflect.New(t).Elem().Interface().(documented); ok {
		return d.SwaggerDoc()
	}
	return nil
}

// ObjectMeta returns the schema of metav1.ObjectMeta as the API server
// publishes it for every resource, including a custom resource.
func ObjectMeta() *spec.Schema {
	return schemaOf(reflect.TypeFor[metav1.ObjectMeta]())
}
