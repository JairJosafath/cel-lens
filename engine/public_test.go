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

// trackedFiles returns the files git tracks in dir, as slash-separated paths.
func trackedFiles(ctx context.Context, dir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
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
	cmd := exec.CommandContext(t.Context(), "go", "test", "-short", "-count=1", "-timeout", innerTimeout.String(), ".") //nolint:gosec // fixed arguments
	cmd.Dir = dst
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test . in the public copy: %v\n%s", err, out)
	}
}

// TestNoFactoryPathOutsideRootTests keeps the promise TestPublicSourceTestsPass
// relies on: only root-package tests may name the factory's files, because only
// the root package's tests run in the copy that lacks them. It checks the string
// literals of every other _test.go file, so a comment may still mention them.
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
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			for _, name := range factoryPaths {
				if strings.Contains(lit.Value, name) {
					t.Errorf("%s names %q; only root-package tests may, as the public copy runs only those",
						fset.Position(lit.Pos()), name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
