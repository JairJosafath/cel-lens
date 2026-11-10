// Package goschema turns a Go type into the OpenAPI v3 schema controller-gen
// writes for it in a CustomResourceDefinition, from the Go source alone: it
// parses the files of the type's package with go/parser, and builds, loads or
// runs nothing.
//
// The rules are controller-gen's, but for one. A field's json tag gives its
// name; a field without one is left out, as controller-gen refuses it, and so
// is one tagged json:"-", and an unexported one, which encoding/json leaves
// out. json:",inline", or a tag without a name, puts the fields of the
// field's struct in the struct's own. omitempty and omitzero make a field
// optional, and so does a pointer here, which controller-gen does not (it
// needs omitempty or +optional); every other field is required. Only the
// files a go build on this machine compiles, of the package most of them are
// of, are read. Go's basic types, slices, arrays,
// maps with string keys, structs and the named types of the package map to
// their schemas, and so do the common Kubernetes types: ObjectMeta, Time,
// MicroTime and Duration as controller-gen writes them, IntOrString and
// Quantity as x-kubernetes-int-or-string, and the other types of
// k8s.io/apimachinery/pkg/apis/meta/v1 and k8s.io/api/core/v1 the engine has
// schemas for (see matchkind.GoType). A doc comment is the description, without
// its marker lines; markers such as +kubebuilder:validation:Maximum are not
// read.
package goschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/token"
	"io"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
)

// File is a Go source file of a package: its name, as Type and messages give
// it, and its text.
type File struct {
	Name string
	Src  []byte
}

// Type is a Go type turned into a schema.
type Type struct {
	// Schema is the type's schema, in the form YAML decodes a
	// CustomResourceDefinition's schema to: maps, lists and JSON values.
	Schema map[string]any
	// File is the Name of the file that declares the type, and Line the line
	// of its name there, from 1.
	File string
	Line int
	// Problems say which fields the schema leaves out, and why, each naming
	// the field as Struct.Field, such as
	// "QuotaSpec.Owner: type other.Owner is from package example.com/other,
	// which cel-lens does not read: the field is left out", and where it
	// grows past 20000 schemas and its fields become dyn.
	Problems []string
}

// maxDepth is how deep types may nest; deeper ones are left out rather than
// risking the stack.
const maxDepth = 100

// maxSchemas is how many schemas, one per type or field, the schema of a type
// may have; core/v1 PodTemplateSpec has about 1400. A named type is written
// out each time it is used, so a few types that each use the next twice make
// a schema that doubles with each one; past this the fields are dyn rather
// than holding the reader.
const maxSchemas = 20000

// Read turns the type name, declared in files, the .go files of one folder,
// into its schema, as controller-gen does. Of those, it reads the files a go
// build on this machine compiles (see Builds) that are of the package most of
// them are of. Files that do not parse count with what they declare before
// their first error. The error says why there is no
// schema: the package declares no type name, or the type has none, such as a
// func type.
func Read(files []File, name string) (*Type, error) {
	c := &converter{fset: token.NewFileSet(), decls: map[string]decl{}, active: map[string]bool{}, known: map[string][]byte{}}
	type parsed struct {
		name string
		file *ast.File
		err  error
	}
	var all []parsed
	count := map[string]int{}
	for _, f := range slices.SortedFunc(slices.Values(files), func(a, b File) int { return strings.Compare(a.Name, b.Name) }) {
		if !Builds(f.Name, f.Src) {
			continue
		}
		af, err := parser.ParseFile(c.fset, f.Name, f.Src, parser.ParseComments|parser.SkipObjectResolution)
		if af != nil {
			all = append(all, parsed{f.Name, af, err})
			count[af.Name.Name]++
		}
	}
	// The package is the one most files are of, the first file's on a tie.
	pkg := ""
	for _, p := range all {
		if count[p.file.Name.Name] > count[pkg] {
			pkg = p.file.Name.Name
		}
	}
	var parseErr error
	for _, p := range all {
		if p.file.Name.Name != pkg {
			continue
		}
		if parseErr == nil {
			parseErr = p.err
		}
		c.add(p.name, p.file)
	}
	d, ok := c.decls[name]
	if !ok {
		if parseErr != nil {
			return nil, fmt.Errorf("the package declares no type %s, and %w", name, parseErr)
		}
		return nil, fmt.Errorf("the package declares no type %s", name)
	}
	c.root = name
	s := c.named(name, name, 0)
	if s == nil {
		return nil, fmt.Errorf("%s has no schema: %s", name, strings.Join(c.problems, "; "))
	}
	if d.doc != "" {
		s["description"] = d.doc
	}
	return &Type{Schema: s, File: d.fileName, Line: c.fset.Position(d.spec.Name.Pos()).Line, Problems: c.problems}, nil
}

// decl is a type the package declares.
type decl struct {
	spec     *ast.TypeSpec
	file     *ast.File
	fileName string
	doc      string
}

// converter turns the types of one package into schemas.
type converter struct {
	fset  *token.FileSet
	decls map[string]decl
	// active are the named types being converted, to stop at one that
	// contains itself.
	active   map[string]bool
	problems []string
	// known are the schemas of the types of other packages, as JSON, by
	// import path and name.
	known map[string][]byte
	// schemas counts the schemas made, up to maxSchemas; full is true once
	// that is reached.
	schemas int
	full    bool
	// root is the type Read turns into a schema; fields are the fields
	// being turned into one, from root down, each as Struct.Field and by its
	// JSON name, "" for an inline one.
	root   string
	fields []fieldAt
}

// fieldAt is a field being turned into a schema.
type fieldAt struct{ goName, jsonName string }

// grow counts n more schemas and reports whether they fit within maxSchemas.
// The first time they do not, it records which field that is: it and the
// fields after it are dyn.
func (c *converter) grow(n int) bool {
	if c.full {
		return false
	}
	c.schemas += n
	if c.schemas <= maxSchemas {
		return true
	}
	c.full = true
	where, names := c.root, []string{}
	for _, f := range c.fields {
		where = f.goName
		if f.jsonName != "" {
			names = append(names, f.jsonName)
		}
	}
	c.problems = append(c.problems, fmt.Sprintf("%s, at %s of %s: the schema grows past %d schemas here, as its types are used again and again: it and the fields after it are dyn, which CEL does not check",
		where, strings.Join(names, "."), c.root, maxSchemas))
	return false
}

// dyn is the schema of a value past maxSchemas: one CEL types as dyn, as the
// API server does an x-kubernetes-int-or-string value, so a field below it is
// no error.
func dyn() map[string]any {
	return map[string]any{"x-kubernetes-int-or-string": true, "description": fmt.Sprintf("cel-lens leaves this value dyn: the schema of its Go type grows past %d schemas.", maxSchemas)}
}

// countSchemas is how many schemas s has: itself and those below it.
func countSchemas(v any) int {
	n := 0
	switch v := v.(type) {
	case map[string]any:
		n = 1
		for _, x := range v {
			n += countSchemas(x)
		}
	case []any:
		for _, x := range v {
			n += countSchemas(x)
		}
	}
	return n
}

// add records the types file f declares, unless an earlier file declares one
// of the same name.
func (c *converter) add(name string, f *ast.File) {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, sp := range gd.Specs {
			ts, ok := sp.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, dup := c.decls[ts.Name.Name]; dup {
				continue
			}
			comment := ts.Doc
			if comment == nil && len(gd.Specs) == 1 {
				comment = gd.Doc
			}
			c.decls[ts.Name.Name] = decl{spec: ts, file: f, fileName: name, doc: docText(comment)}
		}
	}
}

// leftOut records that the field where is left out, and why.
func (c *converter) leftOut(where, why string) {
	c.problems = append(c.problems, where+": "+why+": the field is left out")
}

// named returns the schema of the type name the package declares, for the
// field where, or nil when it has none.
func (c *converter) named(name, where string, depth int) map[string]any {
	d, ok := c.decls[name]
	switch {
	case !ok:
		c.leftOut(where, fmt.Sprintf("type %s is not declared in the package", name))
		return nil
	case c.active[name]:
		c.leftOut(where, fmt.Sprintf("%s contains itself, which a schema cannot say", name))
		return nil
	case d.spec.TypeParams != nil:
		c.leftOut(where, fmt.Sprintf("%s is generic, which controller-gen does not take", name))
		return nil
	}
	c.active[name] = true
	defer delete(c.active, name)
	owner := where
	if _, isStruct := d.spec.Type.(*ast.StructType); isStruct {
		owner = name
	}
	return c.schema(d.spec.Type, d.file, owner, depth+1)
}

// basic are the schemas of Go's basic types, as controller-gen writes them.
var basic = map[string]func() map[string]any{
	"string":  func() map[string]any { return map[string]any{"type": "string"} },
	"bool":    func() map[string]any { return map[string]any{"type": "boolean"} },
	"int":     func() map[string]any { return map[string]any{"type": "integer"} },
	"int8":    func() map[string]any { return map[string]any{"type": "integer"} },
	"int16":   func() map[string]any { return map[string]any{"type": "integer"} },
	"int32":   func() map[string]any { return map[string]any{"type": "integer", "format": "int32"} },
	"int64":   func() map[string]any { return map[string]any{"type": "integer", "format": "int64"} },
	"uint":    func() map[string]any { return map[string]any{"type": "integer"} },
	"uint8":   func() map[string]any { return map[string]any{"type": "integer"} },
	"uint16":  func() map[string]any { return map[string]any{"type": "integer"} },
	"uint32":  func() map[string]any { return map[string]any{"type": "integer", "format": "int32"} },
	"uint64":  func() map[string]any { return map[string]any{"type": "integer", "format": "int64"} },
	"byte":    func() map[string]any { return map[string]any{"type": "integer"} },
	"rune":    func() map[string]any { return map[string]any{"type": "integer", "format": "int32"} },
	"float32": func() map[string]any { return map[string]any{"type": "number"} },
	"float64": func() map[string]any { return map[string]any{"type": "number"} },
}

// schema returns the schema of the type expression x, written in file f, for
// the field where, or nil when it has none.
func (c *converter) schema(x ast.Expr, f *ast.File, where string, depth int) map[string]any {
	if !c.grow(1) {
		return dyn()
	}
	if depth > maxDepth {
		c.leftOut(where, fmt.Sprintf("the type nests deeper than %d levels", maxDepth))
		return nil
	}
	switch x := x.(type) {
	case *ast.Ident:
		if b, ok := basic[x.Name]; ok {
			return b()
		}
		switch x.Name {
		case "any", "error", "uintptr", "complex64", "complex128":
			c.leftOut(where, fmt.Sprintf("%s has no schema, and controller-gen refuses it", x.Name))
			return nil
		}
		return c.named(x.Name, where, depth)
	case *ast.ParenExpr:
		return c.schema(x.X, f, where, depth+1)
	case *ast.StarExpr:
		return c.schema(x.X, f, where, depth+1)
	case *ast.ArrayType:
		if id, ok := x.Elt.(*ast.Ident); ok && x.Len == nil && (id.Name == "byte" || id.Name == "uint8") {
			return map[string]any{"type": "string", "format": "byte"}
		}
		items := c.schema(x.Elt, f, where, depth+1)
		if items == nil {
			return nil
		}
		return map[string]any{"type": "array", "items": items}
	case *ast.MapType:
		if !c.stringKind(x.Key) {
			c.leftOut(where, "a map's keys are strings in Kubernetes objects, and controller-gen refuses others")
			return nil
		}
		values := c.schema(x.Value, f, where, depth+1)
		if values == nil {
			return nil
		}
		return map[string]any{"type": "object", "additionalProperties": values}
	case *ast.StructType:
		return c.structSchema(x, f, where, depth)
	case *ast.SelectorExpr:
		return c.foreign(x, f, where)
	case *ast.InterfaceType:
		c.leftOut(where, "an interface has no schema, and controller-gen refuses it")
	case *ast.FuncType, *ast.ChanType:
		c.leftOut(where, "a func or chan has no schema, and controller-gen refuses it")
	case *ast.IndexExpr, *ast.IndexListExpr:
		c.leftOut(where, "a generic type is not taken by controller-gen")
	default:
		c.leftOut(where, "its type has no schema")
	}
	return nil
}

// stringKind reports whether x is string or a type of the package whose
// underlying type is string, which may key a map.
func (c *converter) stringKind(x ast.Expr) bool {
	for range maxDepth {
		id, ok := x.(*ast.Ident)
		if !ok {
			return false
		}
		if id.Name == "string" {
			return true
		}
		d, ok := c.decls[id.Name]
		if !ok {
			return false
		}
		x = d.spec.Type
	}
	return false
}

// structSchema returns the schema of the struct st, written in file f, whose
// fields are named owner.Field in problems.
func (c *converter) structSchema(st *ast.StructType, f *ast.File, owner string, depth int) map[string]any {
	props := map[string]any{}
	var required []any
	for _, field := range st.Fields.List {
		for _, goName := range fieldNames(field) {
			name, s, opt := c.field(field, goName, f, owner, depth)
			if s == nil {
				continue
			}
			if name != "" {
				props[name] = s
				if !opt {
					required = append(required, name)
				}
				continue
			}
			// An inline field: its struct's fields are this struct's. Past
			// maxSchemas it has none.
			inner, _ := s["properties"].(map[string]any)
			if c.full && s["type"] == nil {
				continue
			}
			if s["type"] != "object" || s["additionalProperties"] != nil {
				c.leftOut(owner+"."+goName, "only a struct can be inline")
				continue
			}
			for k, v := range inner {
				props[k] = v
			}
			if r, ok := s["required"].([]any); ok {
				required = append(required, r...)
			}
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// fieldNames are the Go names of the fields field declares: its names, or
// for an embedded field the name of its type.
func fieldNames(field *ast.Field) []string {
	if len(field.Names) == 0 {
		x := field.Type
		if star, ok := x.(*ast.StarExpr); ok {
			x = star.X
		}
		switch x := x.(type) {
		case *ast.Ident:
			return []string{x.Name}
		case *ast.SelectorExpr:
			return []string{x.Sel.Name}
		}
		return []string{"embedded"}
	}
	out := make([]string, 0, len(field.Names))
	for _, n := range field.Names {
		if ast.IsExported(n.Name) {
			out = append(out, n.Name)
		}
	}
	return out
}

// field returns the JSON name of the field goName of field, "" when it is
// inline, its schema, nil when it is left out, and whether it is optional.
func (c *converter) field(field *ast.Field, goName string, f *ast.File, owner string, depth int) (string, map[string]any, bool) {
	where := owner + "." + goName
	tag, ok := jsonTag(field)
	if !ok {
		c.leftOut(where, "it has no json tag, which controller-gen needs")
		return "", nil, false
	}
	opts := strings.Split(tag, ",")
	if len(opts) == 1 && opts[0] == "-" {
		return "", nil, false
	}
	name := opts[0]
	if slices.Contains(opts[1:], "inline") {
		name = ""
	}
	c.fields = append(c.fields, fieldAt{where, name})
	s := c.schema(field.Type, f, where, depth+1)
	c.fields = c.fields[:len(c.fields)-1]
	if s == nil {
		return "", nil, false
	}
	if d := docText(field.Doc); d != "" && name != "" {
		s["description"] = d
	}
	_, pointer := field.Type.(*ast.StarExpr)
	optional := pointer || slices.Contains(opts[1:], "omitempty") || slices.Contains(opts[1:], "omitzero")
	return name, s, optional
}

// jsonTag returns the json tag of field, and whether it has one.
func jsonTag(field *ast.Field) (string, bool) {
	if field.Tag == nil {
		return "", false
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return "", false
	}
	return reflect.StructTag(raw).Lookup("json")
}

// The import paths of the Kubernetes packages whose types have schemas.
const (
	metaV1   = "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreV1   = "k8s.io/api/core/v1"
	intstr   = "k8s.io/apimachinery/pkg/util/intstr"
	resource = "k8s.io/apimachinery/pkg/api/resource"
)

// quantityPattern is the pattern controller-gen gives resource.Quantity.
const quantityPattern = `^(\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))(([KMGTPE]i)|[numkMGTPE]|([eE](\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))))?$`

// written returns the schema controller-gen writes itself for the type name of
// the package with the import path pkg, and false for one it reads from Go.
func written(pkg, name string) (map[string]any, bool) {
	intOrString := func() map[string]any {
		return map[string]any{"x-kubernetes-int-or-string": true, "anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}}
	}
	switch pkg + "." + name {
	case metaV1 + ".ObjectMeta":
		return map[string]any{"type": "object"}, true
	case metaV1 + ".Time", metaV1 + ".MicroTime":
		return map[string]any{"type": "string", "format": "date-time"}, true
	case metaV1 + ".Duration":
		return map[string]any{"type": "string"}, true
	case intstr + ".IntOrString":
		return intOrString(), true
	case resource + ".Quantity":
		s := intOrString()
		s["pattern"] = quantityPattern
		return s, true
	}
	return nil, false
}

// foreign returns the schema of x, a type of another package, written in file
// f, for the field where, or nil for a package or type it does not know.
func (c *converter) foreign(x *ast.SelectorExpr, f *ast.File, where string) map[string]any {
	pkgName, ok := x.X.(*ast.Ident)
	if !ok {
		c.leftOut(where, "its type has no schema")
		return nil
	}
	pkg := importPath(f, pkgName.Name)
	typ := pkgName.Name + "." + x.Sel.Name
	if pkg == "" {
		c.leftOut(where, fmt.Sprintf("type %s is from a package the file does not import", typ))
		return nil
	}
	if s, ok := written(pkg, x.Sel.Name); ok {
		return s
	}
	if pkg != metaV1 && pkg != coreV1 {
		c.leftOut(where, fmt.Sprintf("type %s is from package %s, which cel-lens does not read", typ, pkg))
		return nil
	}
	key := pkg + "." + x.Sel.Name
	js, ok := c.known[key]
	if !ok {
		if s, found := matchkind.GoType(pkg, x.Sel.Name); found {
			js, _ = json.Marshal(s)
		}
		c.known[key] = js
	}
	var s map[string]any
	if js == nil || json.Unmarshal(js, &s) != nil {
		c.leftOut(where, fmt.Sprintf("type %s of %s has no schema cel-lens knows", typ, pkg))
		return nil
	}
	if !c.grow(countSchemas(s)) {
		return dyn()
	}
	return s
}

// defaultNames are the names of the Kubernetes packages whose import path
// does not end in their name.
var defaultNames = map[string]string{metaV1: "v1", coreV1: "v1"}

// majorVersion is the last element of an import path that is a major
// version, as in example.com/lib/v2, whose package is named for the one before.
var majorVersion = regexp.MustCompile(`^v\d+$`)

// importPath returns the import path of the package file f names name, or "".
func importPath(f *ast.File, name string) string {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == name {
				return p
			}
			continue
		}
		if packageName(p) == name {
			return p
		}
	}
	return ""
}

// packageName is the name a package with the import path p has by default.
func packageName(p string) string {
	if n, ok := defaultNames[p]; ok {
		return n
	}
	base := path.Base(p)
	if majorVersion.MatchString(base) && path.Dir(p) != "." {
		base = path.Base(path.Dir(p))
	}
	return strings.TrimPrefix(base, "go-")
}

// docText is the text of the doc comment g, without its marker lines (those
// that start with +), trimmed; "" for none.
func docText(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	var lines []string
	for line := range strings.SplitSeq(g.Text(), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "+") {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Struct is an exported struct type of a file, which Structs lists.
type Struct struct {
	Name string
	// Doc is the first sentence of its doc comment, "" without one.
	Doc string
}

// Structs returns the exported struct types the Go file name, of text src,
// declares that have at least one field with a json tag, and no type
// parameters, in the order declared: those a go: entry can name to type a
// value with. A file Builds says a go build skips has none, and one that does
// not parse those declared before its first error.
func Structs(name string, src []byte) []Struct {
	if !Builds(name, src) {
		return nil
	}
	f, _ := parser.ParseFile(token.NewFileSet(), "", src, parser.ParseComments|parser.SkipObjectResolution)
	if f == nil {
		return nil
	}
	var out []Struct
	var synopsis doc.Package
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, sp := range gd.Specs {
			ts, ok := sp.(*ast.TypeSpec)
			if !ok || !ts.Name.IsExported() || ts.TypeParams != nil {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || !slices.ContainsFunc(st.Fields.List, func(field *ast.Field) bool { _, ok := jsonTag(field); return ok }) {
				continue
			}
			comment := ts.Doc
			if comment == nil && len(gd.Specs) == 1 {
				comment = gd.Doc
			}
			out = append(out, Struct{Name: ts.Name.Name, Doc: synopsis.Synopsis(docText(comment))})
		}
	}
	return out
}

// Builds reports whether a go build on this machine compiles the Go file
// name, of text src, as go/build decides it from those alone: a name with a
// _GOOS or _GOARCH suffix of another system, or that starts with _ or ., and
// build constraints that exclude this system, such as //go:build ignore or
// //go:build windows on Linux, leave it out. Nothing else is read or run. A
// file whose constraints cannot be read is compiled, so its error shows.
func Builds(name string, src []byte) bool {
	ctxt := build.Default
	ctxt.JoinPath = path.Join
	ctxt.OpenFile = func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(src)), nil }
	ok, err := ctxt.MatchFile(".", path.Base(name))
	return ok || err != nil
}
