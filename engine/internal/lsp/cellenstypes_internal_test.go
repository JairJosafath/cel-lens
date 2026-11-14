package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// TestReadsLensesPaths lists the paths that ask readsLenses whether .cellens
// is read, each once, and checks that readsLenses is the one place that asks
// whether cellens-types is on, in this package and in package assist (#44).
func TestReadsLensesPaths(t *testing.T) {
	t.Parallel()
	asks, has := map[string]int{}, map[string]int{}
	for _, dir := range []string{".", "../assist"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if sel.Sel.Name == "readsLenses" {
						asks[fn.Name.Name]++
					}
					if sel.Sel.Name == "Has" && len(call.Args) == 1 && isCellensTypes(call.Args[0]) {
						has[fn.Name.Name]++
					}
					return true
				})
			}
		}
	}
	// The paths that read a .cellens, or show what one gives.
	want := map[string]int{
		"initialize":             1, // searches the workspace, or probes it for the notice
		"didChangeConfiguration": 1, // with switchLenses, turning the feature on or off
		"switchLenses":           1,
		"didOpen":                1, // an open .cellens: read, or kept unread
		"didChange":              1,
		"didClose":               1,
		"didChangeWatchedFiles":  1, // a .cellens changed on disk, or a created folder
		"publishAssumed":         1, // the types of a document, and its hover (assist.WithoutCellens)
		"inlineValues":           1, // Give it a type and Edit on the line above a rule
		"executeCommand":         1, // the commands behind Give it a type
		"ruleInputs":             1, // the types the rule index checks the files that are not open with
	}
	if !maps.Equal(asks, want) {
		t.Errorf("functions that ask readsLenses = %v, want %v", asks, want)
	}
	if want := map[string]int{"readsLenses": 1}; !maps.Equal(has, want) {
		t.Errorf("functions that ask Has(feature.CellensTypes) = %v, want only readsLenses", has)
	}
}

func isCellensTypes(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "feature" && sel.Sel.Name == "CellensTypes"
}

// openRecorder is a file system that records the names opened.
type openRecorder struct {
	fs.FS
	mu     *sync.Mutex
	opened *[]string
}

func (r openRecorder) Open(name string) (fs.File, error) {
	r.mu.Lock()
	*r.opened = append(*r.opened, name)
	r.mu.Unlock()
	return r.FS.Open(name)
}

// TestLensesOffReadNone checks that, while cellens-types is off, no .cellens
// is searched for or read: initialize starts a probe, which opens none; a
// watched change of one, and the editor opening and closing one, read none;
// and the session knows none (#44).
func TestLensesOffReadNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lensPath := filepath.Join(dir, cellens.FileName)
	for name, text := range map[string]string{
		cellens.FileName:                 "types:\n  - file: policy.yaml\n    document: ValidatingAdmissionPolicy limits\n    variable: params\n    type: int\n",
		filepath.Join("sub", ".cellens"): "types: []\n",
		"policy.yaml":                    "kind: ValidatingAdmissionPolicy\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var opened []string
	var out bytes.Buffer
	ss := &session{conn: jsonrpc.NewConn(strings.NewReader(""), &out), docs: map[string]*document{}, fsys: func(d string) fs.FS {
		return openRecorder{os.DirFS(d), &mu, &opened}
	}}
	t.Cleanup(ss.stopLensSearch)
	ctx := context.Background()
	params, _ := json.Marshal(map[string]any{"workspaceFolders": []map[string]any{{"uri": pathURI(dir)}}})
	if err := ss.initialize(ctx, params, func(any, error) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if ss.lensSearch == nil || !ss.lensSearch.probe {
		t.Fatalf("initialize started %+v, want a probe", ss.lensSearch)
	}
	if err := ss.finishLensSearch(ctx, <-ss.lensSearch.done); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	for _, name := range opened {
		if path.Base(name) == cellens.FileName {
			t.Errorf("the probe opened %s, want no .cellens opened", name)
		}
	}
	mu.Unlock()
	if n := strings.Count(out.String(), `"window/showMessageRequest"`); n != 1 {
		t.Errorf("the probe sent %d notices, want 1:\n%s", n, out.String())
	}

	lensURI := pathURI(lensPath)
	steps := []struct {
		name string
		do   func() error
	}{
		{"a watched change", func() error {
			p, _ := json.Marshal(map[string]any{"changes": []map[string]any{{"uri": lensURI, "type": fileChanged}, {"uri": pathURI(filepath.Join(dir, "new")), "type": fileCreated}}})
			return ss.didChangeWatchedFiles(ctx, p)
		}},
		{"didOpen", func() error {
			p, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": lensURI, "version": 1, "text": "types: []\n"}})
			return ss.didOpen(ctx, p)
		}},
		{"didClose", func() error {
			p, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": lensURI}})
			return ss.didClose(ctx, p)
		}},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if len(ss.lenses) > 0 || ss.lensSearch != nil && !ss.lensSearch.probe {
			t.Errorf("after %s the session knows %v and searches %+v, want no .cellens read or searched for", s.name, slices.Collect(maps.Keys(ss.lenses)), ss.lensSearch)
		}
	}
	if n := strings.Count(out.String(), `"window/showMessageRequest"`); n != 1 {
		t.Errorf("the session sent %d notices, want 1", n)
	}
}
