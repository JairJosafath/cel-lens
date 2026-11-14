package check_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	yamlv3 "go.yaml.in/yaml/v3"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// crdMarker is how a test's YAML names a CustomResourceDefinition.
const crdMarker = "kind: CustomResourceDefinition"

// testdataCRDs returns the files of testdata, at any depth, that hold a
// CustomResourceDefinition, by path.
func testdataCRDs(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	fsys := os.DirFS("testdata")
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(crdMarker)) {
			out["testdata/"+path] = data
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sourceCRDs returns the CustomResourceDefinitions written in the test files of
// this package: every string that a literal, a constant or a sum of them
// spells out, by the file and line it starts at. A string built at run time,
// such as by a function or fmt.Sprintf, is not found; the generators the
// tests use are called by generatedCRDs instead.
func sourceCRDs(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	type scope map[string]ast.Expr
	pkgScopes := map[string]scope{} // package name -> its constants
	var parsed []*ast.File
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, f)
		s := pkgScopes[f.Name.Name]
		if s == nil {
			s = scope{}
			pkgScopes[f.Name.Name] = s
		}
		for _, d := range f.Decls {
			if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.CONST {
				addConsts(g, s)
			}
		}
	}
	out := map[string][]byte{}
	for _, f := range parsed {
		pkg := pkgScopes[f.Name.Name]
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			local := scope{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if g, ok := n.(*ast.GenDecl); ok && g.Tok == token.CONST {
					addConsts(g, local)
				}
				return true
			})
			lookup := func(name string) (ast.Expr, bool) {
				if e, ok := local[name]; ok {
					return e, true
				}
				e, ok := pkg[name]
				return e, ok
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				e, ok := n.(ast.Expr)
				if !ok {
					return true
				}
				if s, ok := evalString(e, lookup, 0); ok && strings.Contains(s, crdMarker) {
					out[fset.Position(e.Pos()).String()] = []byte(s)
					return false
				}
				return true
			})
			return false
		})
		for name, e := range pkg {
			if s, ok := evalString(e, func(n string) (ast.Expr, bool) { x, ok := pkg[n]; return x, ok }, 0); ok && strings.Contains(s, crdMarker) {
				out[fset.Position(e.Pos()).String()+" "+name] = []byte(s)
			}
		}
	}
	return out
}

// addConsts adds the constants of g that have one value each to s.
func addConsts(g *ast.GenDecl, s map[string]ast.Expr) {
	for _, spec := range g.Specs {
		v, ok := spec.(*ast.ValueSpec)
		if !ok || len(v.Names) != len(v.Values) {
			continue
		}
		for i, n := range v.Names {
			s[n.Name] = v.Values[i]
		}
	}
}

// evalString spells out e when it is a string literal, a constant lookup
// finds, or a sum or parenthesis of them.
func evalString(e ast.Expr, lookup func(string) (ast.Expr, bool), depth int) (string, bool) {
	if depth > 32 {
		return "", false
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return evalString(x.X, lookup, depth+1)
	case *ast.Ident:
		v, ok := lookup(x.Name)
		if !ok {
			return "", false
		}
		return evalString(v, lookup, depth+1)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok := evalString(x.X, lookup, depth+1)
		if !ok {
			return "", false
		}
		b, ok := evalString(x.Y, lookup, depth+1)
		return a + b, ok
	}
	return "", false
}

// generatedCRDs are the CustomResourceDefinitions the tests of this package
// build at run time, with the sizes they use, and those of the Costs bench.
func generatedCRDs() map[string][]byte {
	return map[string][]byte{
		"costlyCRD":             costlyCRD("", "", 20),
		"costlyCRD with limits": costlyCRD("              maxItems: 100000\n", "                maxLength: 100000\n", 20),
		"manyRules(20)":         manyRules(20),
		"twoLists":              twoLists("string", "integer"),
		"defaultedCRD":          []byte(defaultedCRD(20)),
		"hoverCRD":              []byte(hoverCRD(1000)),
		"unboundedListsCRD":     unboundedListsCRD(),
		"transitionCRD":         []byte(transitionCRD),
	}
}

// transitionCRD has transition rules (oldSelf) on the items of a list map
// with maxItems and on a map with maxProperties inside a list, whose cost the
// API server estimates as any rule's (review of #60).
const transitionCRD = crdHead + `    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            ports:
              type: array
              maxItems: 1000
              x-kubernetes-list-type: map
              x-kubernetes-list-map-keys: [name]
              items:
                type: object
                required: [name]
                properties:
                  name: {type: string, maxLength: 63}
                  note: {type: string}
                x-kubernetes-validations:
                  - rule: "self.note == oldSelf.note || self.note.startsWith(oldSelf.note)"
                    message: the note only grows
            groups:
              type: array
              x-kubernetes-list-type: map
              x-kubernetes-list-map-keys: [id]
              items:
                type: object
                required: [id]
                properties:
                  id: {type: string, maxLength: 63}
                  labels:
                    type: object
                    maxProperties: 50
                    additionalProperties: {type: string, maxLength: 1000}
                    x-kubernetes-validations:
                      - rule: "oldSelf.all(k, k in self)"
                        message: no label is removed
`

// apiDoc is a CustomResourceDefinition document of a file and the cost errors
// the API server's validation of it gives.
type apiDoc struct {
	line int // of its first line
	// over and totals are the per-expression and per-schema cost errors, by
	// field path; contributed are the costs of the rules the total names.
	over, totals map[string]string
	contributed  []string
	skip         bool // the document does not read as a CustomResourceDefinition of v1
}

// apiCostErrors runs the API server's validation, for version v, of every
// CustomResourceDefinition document of data.
func apiCostErrors(t *testing.T, data []byte, v kubeversion.Version) []apiDoc {
	t.Helper()
	dec := yamlv3.NewDecoder(bytes.NewReader(data))
	var out []apiDoc
	for {
		var node yamlv3.Node
		err := dec.Decode(&node)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			return out // the rest does not parse; Costs answers nothing for it either
		}
		doc := apiDoc{line: node.Line, over: map[string]string{}, totals: map[string]string{}}
		if len(node.Content) > 0 {
			doc.line = node.Content[0].Line
		}
		raw, err := yamlv3.Marshal(&node)
		var versioned apiextensionsv1.CustomResourceDefinition
		if err != nil || yaml.Unmarshal(raw, &versioned) != nil || versioned.APIVersion != "apiextensions.k8s.io/v1" || versioned.Kind != "CustomResourceDefinition" {
			doc.skip = true
			out = append(out, doc)
			continue
		}
		apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(&versioned)
		var crd apiextensions.CustomResourceDefinition
		if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&versioned, &crd, nil); err != nil {
			t.Fatal(err)
		}
		var errs field.ErrorList
		if err := check.UnderVersion(v, func() { errs = validation.ValidateCustomResourceDefinition(context.Background(), &crd) }); err != nil {
			t.Fatal(err)
		}
		for _, fe := range errs {
			switch {
			case fe.Type != field.ErrorTypeForbidden:
			case strings.HasPrefix(fe.Detail, "contributed to estimated rule cost total"):
				doc.contributed = append(doc.contributed, fe.Field)
			case strings.Contains(fe.Detail, "estimated rule cost total"):
				doc.totals[fe.Field] = fe.Detail
			case strings.Contains(fe.Detail, "exceeds budget by factor"):
				doc.over[fe.Field] = fe.Detail
			}
		}
		out = append(out, doc)
	}
}

// factor is how the API server writes a cost against its limit in a cost
// error (getCostErrorMessage).
func factor(cost, limit uint64) string {
	f := float64(cost) / float64(limit)
	switch {
	case f > 100:
		return "more than 100x"
	case f < 1.5:
		return fmt.Sprintf("%fx", f)
	}
	return fmt.Sprintf("%.1fx", f)
}

// printed writes a cost as the cost diagnostics of Check print it.
func printed(n uint64) string {
	trim := func(f float64) string { return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0") }
	f := float64(n)
	switch {
	case n >= 1e15:
		return fmt.Sprintf("%.1e", f)
	case n >= 1e12:
		return trim(f/1e12) + "T"
	case n >= 1e9:
		return trim(f/1e9) + "B"
	case n >= 1e6:
		return trim(f/1e6) + "M"
	case n >= 1e4:
		return trim(f/1e3) + "K"
	}
	return strconv.FormatUint(n, 10)
}

// schemaOf is the field path of the schema an expression's field path is in.
func schemaOf(path string) string {
	i := strings.Index(path, "openAPIV3Schema")
	if i < 0 {
		return ""
	}
	return path[:i+len("openAPIV3Schema")]
}

// TestCostsTruth holds Costs to the API server's own cost errors. For every
// CustomResourceDefinition of this package's tests and testdata, at every
// supported version: an expression is over its budget exactly when the API
// server's validation reports its cost error, by the factor that error
// prints, and the cost diagnostic of Check prints the same number; a schema's
// total is over exactly when the validation reports the total's, and the
// rules the total names cost what MostExpensive says.
func TestCostsTruth(t *testing.T) {
	t.Parallel()
	inputs := testdataCRDs(t)
	for name, data := range sourceCRDs(t) {
		inputs[name] = data
	}
	for name, data := range generatedCRDs() {
		inputs[name] = data
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for n := range inputs {
			if !yield(n) {
				return
			}
		}
	})
	t.Logf("%d files with CustomResourceDefinitions", len(names))
	var (
		mu                          sync.Mutex
		estimated, over, totalsOver int
	)
	t.Run("versions", func(t *testing.T) {
		for _, v := range kubeversion.Supported() {
			t.Run(v.String(), func(t *testing.T) {
				t.Parallel()
				for _, name := range names {
					e, o, to := costTruth(t, name, inputs[name], v)
					mu.Lock()
					estimated, over, totalsOver = estimated+e, over+o, totalsOver+to
					mu.Unlock()
				}
			})
		}
	})
	t.Logf("%d expressions estimated, %d over their budget, %d schema totals over theirs", estimated, over, totalsOver)
	if over == 0 || totalsOver == 0 {
		t.Errorf("the inputs have %d expressions and %d totals over their budgets, want some of each", over, totalsOver)
	}
}

// costTruth compares Costs with the API server for one file at version v and
// says how many expressions it estimated, how many were over their budget and
// how many schema totals were over theirs.
func costTruth(t *testing.T, name string, data []byte, v kubeversion.Version) (estimated, over, totalsOver int) {
	t.Helper()
	ctx := context.Background()
	costs, err := check.Costs(ctx, name, data, check.WithVersion(v))
	var syntax *extract.SyntaxError
	if err != nil && !errors.As(err, &syntax) {
		t.Errorf("Costs(%s, %s) error = %v", name, v, err)
		return 0, 0, 0
	}
	docs := apiCostErrors(t, data, v)
	var diags []check.Diagnostic // Check's, made when a number has to be compared
	diagnostics := func() []check.Diagnostic {
		if diags == nil {
			diags, _ = check.Check(ctx, name, data, check.WithVersion(v))
		}
		return diags
	}
	docOf := func(line int) *apiDoc {
		var d *apiDoc
		for i := range docs {
			if docs[i].line <= line {
				d = &docs[i]
			}
		}
		return d
	}
	seenOver := map[*apiDoc]map[string]bool{}
	seenTotal := map[*apiDoc]map[string]bool{}
	for _, c := range costs {
		doc := docOf(c.Start.Line)
		if doc == nil || doc.skip {
			continue
		}
		if seenOver[doc] == nil {
			seenOver[doc], seenTotal[doc] = map[string]bool{}, map[string]bool{}
		}
		apiOver, isOver := doc.over[c.FieldPath]
		if c.Kind != check.CostEstimated {
			if isOver {
				t.Errorf("%s at %s: Costs %s:%d = %s, but the API server reports its cost error %q", v, name, c.FieldPath, c.Start.Line, c.Kind, apiOver)
			}
			continue
		}
		estimated++
		if got := c.Cost > c.Budget; got != isOver {
			t.Errorf("%s at %s: Costs %s:%d over its budget = %t (%d of %d), API server's cost error = %t", v, name, c.FieldPath, c.Start.Line, got, c.Cost, c.Budget, isOver)
		}
		if isOver {
			over++
			seenOver[doc][c.FieldPath] = true
			if want := "by factor of " + factor(c.Cost, c.Budget); !strings.Contains(apiOver, want) {
				t.Errorf("%s at %s: Costs %s cost %d; the API server's error %q, want it to say %q", v, name, c.FieldPath, c.Cost, apiOver, want)
			}
			want := "estimated cost " + printed(c.Cost) + " is over"
			if !slices.ContainsFunc(diagnostics(), func(d check.Diagnostic) bool {
				return d.Start == c.Start && d.End == c.End && strings.Contains(d.Message, want)
			}) {
				t.Errorf("%s at %s: Costs %s:%d cost %d, want a diagnostic there with %q: %v", v, name, c.FieldPath, c.Start.Line, c.Cost, want, diagnostics())
			}
		}
		schema := schemaOf(c.FieldPath)
		apiTotal, totalOver := doc.totals[schema]
		if got := c.Total > c.TotalBudget; got != totalOver {
			t.Errorf("%s at %s: Costs %s:%d total over its budget = %t (%d of %d), API server's total error = %t", v, name, c.FieldPath, c.Start.Line, got, c.Total, c.TotalBudget, totalOver)
		}
		if !totalOver || seenTotal[doc][schema] {
			continue
		}
		seenTotal[doc][schema] = true
		totalsOver++
		if want := "by factor of " + factor(c.Total, c.TotalBudget); !strings.Contains(apiTotal, want) {
			t.Errorf("%s at %s: Costs total of %s = %d; the API server's error %q, want it to say %q", v, name, schema, c.Total, apiTotal, want)
		}
		var gotCosts, wantCosts []uint64
		for _, m := range c.MostExpensive {
			gotCosts = append(gotCosts, m.Cost)
		}
		for _, p := range doc.contributed {
			if schemaOf(p) != schema {
				continue
			}
			i := slices.IndexFunc(costs, func(o check.Cost) bool { return o.FieldPath == p && o.Kind == check.CostEstimated })
			if i < 0 {
				t.Errorf("%s at %s: the API server names %s in the total of %s, which Costs does not estimate", v, name, p, schema)
				continue
			}
			wantCosts = append(wantCosts, costs[i].Cost)
		}
		slices.Sort(gotCosts)
		slices.Sort(wantCosts)
		if !slices.Equal(gotCosts, wantCosts) {
			t.Errorf("%s at %s: Costs MostExpensive of %s cost %v, the rules the API server names %v", v, name, schema, gotCosts, wantCosts)
		}
		want := []string{"estimated cost " + printed(c.Total) + " of all rules together", "All rules together cost an estimated " + printed(c.Total) + ","}
		if !slices.ContainsFunc(diagnostics(), func(d check.Diagnostic) bool {
			return d.CostTotal && (strings.Contains(d.Message, want[0]) || strings.Contains(d.Message, want[1]))
		}) {
			t.Errorf("%s at %s: Costs total of %s = %d, want a diagnostic that prints %s: %v", v, name, schema, c.Total, printed(c.Total), diagnostics())
		}
	}
	for i := range docs {
		doc := &docs[i]
		for p := range doc.over {
			if !seenOver[doc][p] {
				t.Errorf("%s at %s: the API server reports the cost error of %s, which Costs has under its budget or not at all", v, name, p)
			}
		}
		for p := range doc.totals {
			if !seenTotal[doc][schemaOf(p)] {
				t.Errorf("%s at %s: the API server reports the total of %s over its budget, which Costs has under it or not at all", v, name, p)
			}
		}
	}
	return estimated, over, totalsOver
}
