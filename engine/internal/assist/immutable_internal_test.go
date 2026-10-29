package assist

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoMutablePackageTables checks AGENTS.md rule 7: no package-level
// variable of the package holds a map or a slice, however it is initialised.
func TestNoMutablePackageTables(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	got, err := mutableVars(fset, files, exportImporter(t, fset))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range got {
		t.Errorf("package variable %s holds a map or slice; use a function or a switch", name)
	}
}

// TestMutableVars checks that mutableVars sees through call initialisers.
func TestMutableVars(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"literal", `var x = map[string]int{}`, []string{"x"}},
		{"declared type", `var x []int`, []string{"x"}},
		{"call returning map", "var x = mk()\nfunc mk() map[string]any { return nil }", []string{"x"}},
		{"call returning pointer to slice", "var x = mk()\nfunc mk() *[]int { return nil }", []string{"x"}},
		{"scalar", `var x = 1`, nil},
		{"call returning string", "var x = mk()\nfunc mk() string { return \"\" }", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x.go", "package p\n"+tt.src, 0)
			if err != nil {
				t.Fatal(err)
			}
			got, err := mutableVars(fset, []*ast.File{f}, importer.Default())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("mutableVars(%q) = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

// mutableVars type-checks files as one package and returns the package-level
// variables whose type is a map, a slice or a pointer to one.
func mutableVars(fset *token.FileSet, files []*ast.File, imp types.Importer) ([]string, error) {
	conf := types.Config{Importer: imp}
	pkg, err := conf.Check("p", fset, files, nil)
	if err != nil {
		return nil, err
	}
	var out []string
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		v, ok := scope.Lookup(name).(*types.Var)
		if ok && isTable(v.Type()) {
			out = append(out, name)
		}
	}
	return out, nil
}

// isTable reports whether t is a map, a slice or a pointer to one.
func isTable(t types.Type) bool {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	switch t.Underlying().(type) {
	case *types.Map, *types.Slice:
		return true
	}
	return false
}

// exportImporter returns an importer that reads the compiler export data of
// the package's dependencies, as listed by go list.
func exportImporter(t *testing.T, fset *token.FileSet) types.Importer {
	t.Helper()
	out, err := exec.Command("go", "list", "-export", "-deps", "-f", "{{.ImportPath}} {{.Export}}", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	exports := map[string]string{}
	for line := range strings.Lines(string(out)) {
		if path, file, ok := strings.Cut(strings.TrimSpace(line), " "); ok && file != "" {
			exports[path] = file
		}
	}
	return importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})
}
