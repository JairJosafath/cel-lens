package assist

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoMutablePackageTables checks the rule that no package-level
// variable of the package holds a map or a slice.
func TestNoMutablePackageTables(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if isTable(vs.Type) || i < len(vs.Values) && isTable(vs.Values[i]) {
						t.Errorf("%s: package variable %s holds a map or slice; use a function or a switch", file, name.Name)
					}
				}
			}
		}
	}
}

// isTable reports whether e is a map or slice type or a literal of one.
func isTable(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.MapType, *ast.ArrayType:
		return true
	case *ast.CompositeLit:
		return isTable(e.Type)
	}
	return false
}
