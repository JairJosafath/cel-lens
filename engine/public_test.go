package main

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// factoryPaths are the files and directories the published source leaves out.
var factoryPaths = []string{
	".gitea", "AGENTS.md", ".c4" + "factory.yaml", ".devcontainer", "docs/agents",
}

// innerTimeout bounds the inner go test; it is shorter than the Makefile's 30m.
const innerTimeout = 25 * time.Minute

// parallelEnv names the variable through which the Makefile's GO_PARALLEL reaches
// the tests; defaultParallel is used when it is unset or not a positive number.
const (
	parallelEnv     = "GO_PARALLEL"
	defaultParallel = "2"
)

// innerTestArgs returns the arguments of the inner go test. It takes the same -p
// limit and -trimpath as the Makefile's build and test, so the packages the outer
// run compiled serve it from the build cache and it adds little memory beside them.
func innerTestArgs(parallel string) []string {
	if n, err := strconv.Atoi(parallel); err != nil || n < 1 {
		parallel = defaultParallel
	}
	return []string{"test", "-p", parallel, "-trimpath", "-short", "-count=1", "-timeout", innerTimeout.String(), "."}
}

// trackedFiles returns the files git tracks in dir, as slash-separated paths.
func trackedFiles(ctx context.Context, dir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee := new(exec.ExitError); errors.As(err, &ee) {
			if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
				return nil, fmt.Errorf("git ls-files in %s: %w: %s", dir, err, msg)
			}
		}
		return nil, fmt.Errorf("git ls-files in %s: %w", dir, err)
	}
	var files []string
	for f := range strings.SplitSeq(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// isFactoryPath reports whether the tracked path f is one of factoryPaths or inside one.
func isFactoryPath(f string) bool {
	for _, p := range factoryPaths {
		if f == p || strings.HasPrefix(f, p+"/") {
			return true
		}
	}
	return false
}

// copyTracked copies the tracked files of src that are not factory paths into dst.
func copyTracked(files []string, src, dst string) error {
	root, err := os.OpenRoot(dst)
	if err != nil {
		return fmt.Errorf("open %s: %w", dst, err)
	}
	defer func() { _ = root.Close() }()
	for _, f := range files {
		if isFactoryPath(f) {
			continue
		}
		in := filepath.Join(src, filepath.FromSlash(f))
		info, err := os.Lstat(in)
		if errors.Is(err, fs.ErrNotExist) {
			continue // tracked but deleted in the working tree
		}
		if err != nil {
			return fmt.Errorf("stat %s: %w", in, err)
		}
		if err := root.MkdirAll(path.Dir(f), 0o750); err != nil {
			return fmt.Errorf("create directory for %s: %w", f, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(in)
			if err != nil {
				return fmt.Errorf("read link %s: %w", in, err)
			}
			if err := root.Symlink(target, f); err != nil {
				return fmt.Errorf("link %s: %w", f, err)
			}
			continue
		}
		data, err := os.ReadFile(in)
		if err != nil {
			return fmt.Errorf("read %s: %w", in, err)
		}
		if err := root.WriteFile(f, data, info.Mode().Perm()); err != nil {
			return fmt.Errorf("write %s: %w", f, err)
		}
	}
	return nil
}

// distEnv is the variable that turns on the tests that build and cross-compile
// every dependency (make test-dist).
const distEnv = "CEL_LENS_DIST_TEST"

// innerEnv returns env for the inner run: binEnv set to bin, and distEnv
// removed. The dist tests belong to the outer run; in the copy, which is not a
// git work tree, TestDist cannot pass, and TestGoInstallFromProxy would build
// every dependency again.
func innerEnv(env []string, bin string) []string {
	out := slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		return strings.HasPrefix(kv, distEnv+"=") || strings.HasPrefix(kv, binEnv+"=")
	})
	return append(out, binEnv+"="+bin)
}

func TestInnerEnvDropsDistTest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		env  []string
		want []string
	}{
		{"dist set", []string{"A=1", distEnv + "=1"}, []string{"A=1", binEnv + "=/b"}},
		{"dist unset", []string{"A=1"}, []string{"A=1", binEnv + "=/b"}},
		{"bin replaced", []string{binEnv + "=/old", "A=1"}, []string{"A=1", binEnv + "=/b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := innerEnv(tc.env, "/b"); !slices.Equal(got, tc.want) {
				t.Errorf("innerEnv(%q) = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}

// TestPublicSourceTestsPass proves a contributor's clone, which lacks the
// factory's files, has a green go test of the root package, which holds every test that reads
// the files the copy lacks. The copy holds only the files git tracks, so untracked
// or ignored files cannot change the result; the inner run stops with the test's
// context and after innerTimeout. The other packages read none of them, so their
// tests run once, in the outer run. -short leaves out the tests that build every
// dependency again, which the outer run covers.
func TestPublicSourceTestsPass(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(".gitea"); os.IsNotExist(err) {
		t.Skip(".gitea is the factory's and absent from the published source; this is the public copy")
	}
	for _, tool := range []string{"git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH: %v", tool, err)
		}
	}
	if testing.Short() {
		t.Skip("copies the tree and runs every test")
	}
	src, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	files, err := trackedFiles(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := copyTracked(files, src, dst); err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
	cmd := exec.CommandContext(t.Context(), "go", innerTestArgs(os.Getenv(parallelEnv))...) //nolint:gosec // arguments built from a validated number
	cmd.Dir = dst
	cmd.Env = innerEnv(os.Environ(), buildBinary(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test . in the public copy: %v\n%s", err, out)
	}
}

// TestNoFactoryPathOutsideRootTests keeps the promise TestPublicSourceTestsPass
// relies on: only root-package tests may name the factory's files, because only
// the root package's tests run in the copy that lacks them. It checks the string
// literals of every other _test.go file, so a comment may still mention them. It
// folds adjacent literals joined by + and the arguments of filepath.Join and
// path.Join first, so a split or joined name is caught too.
func TestNoFactoryPathOutsideRootTests(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") || filepath.Dir(p) == "." {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		for _, h := range factoryNamings(fset, f) {
			t.Errorf("%s names %q; only root-package tests may, as the public copy runs only those", h.pos, h.name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// naming is a place where a test names a factory path.
type naming struct {
	pos  token.Position
	name string
}

// foldString returns the constant string e stands for: a string literal, a +
// of foldable strings, or a filepath.Join or path.Join call whose arguments all fold.
func foldString(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return foldString(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		l, lok := foldString(e.X)
		r, rok := foldString(e.Y)
		return l + r, lok && rok
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || sel.Sel.Name != "Join" || (pkg.Name != "filepath" && pkg.Name != "path") {
			return "", false
		}
		parts := make([]string, len(e.Args))
		for i, a := range e.Args {
			p, ok := foldString(a)
			if !ok {
				return "", false
			}
			parts[i] = p
		}
		return path.Join(parts...), true
	}
	return "", false
}

// factoryNamings returns where f holds a constant string, folded as foldString
// does, that contains a factory path. Each such expression is reported once.
func factoryNamings(fset *token.FileSet, f *ast.File) []naming {
	var found []naming
	ast.Inspect(f, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		s, ok := foldString(e)
		if !ok {
			return true
		}
		for _, name := range factoryPaths {
			if strings.Contains(s, name) {
				found = append(found, naming{fset.Position(e.Pos()), name})
				return false
			}
		}
		return true
	})
	return found
}

func TestFactoryNamings(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, expr string
		want       int
	}{
		{"whole literal", `".gitea"`, 1},
		{"split literal", `".c4" + "factory.yaml"`, 1},
		{"filepath.Join", `filepath.Join("docs", "agents")`, 1},
		{"path.Join", `path.Join("docs", "agents", "x.md")`, 1},
		{"join of split", `filepath.Join("a", ".c4"+"factory.yaml")`, 1},
		{"unrelated", `filepath.Join("docs", "style")`, 0},
		{"variable part", `filepath.Join("docs", dir)`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x_test.go", "package x\nvar _ = "+c.expr, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(factoryNamings(fset, f)); got != c.want {
				t.Errorf("factoryNamings(%s) = %d namings, want %d", c.expr, got, c.want)
			}
		})
	}
}

func TestTrackedFilesNotARepository(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	_, err := trackedFiles(t.Context(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("trackedFiles(non-repository) error = %v, want it to mention %q", err, "not a git repository")
	}
}

func TestInnerTestArgs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, parallel, want string }{
		{"unset", "", "2"},
		{"eight", "8", "8"},
		{"junk", "x", "2"},
		{"zero", "0", "2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			args := innerTestArgs(c.parallel)
			got := strings.Join(args, " ")
			for _, want := range []string{"-p " + c.want + " ", "-trimpath", "-count=1"} {
				if !strings.Contains(got+" ", want) {
					t.Errorf("innerTestArgs(%q) = %q, want it to hold %q", c.parallel, got, want)
				}
			}
		})
	}
}
