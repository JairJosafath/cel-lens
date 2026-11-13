package lsp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/JairJosafath/cel-lens/engine/internal/examples"
	"github.com/JairJosafath/cel-lens/engine/internal/inline"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// The command line runs the examples with the code the language server
// runs them with, over the workspace that its paths make up: the folders are
// walked as the workspace scan walks them, with its limits, and a file
// given alone is a workspace of its own.

// ExamplesReport is what RunExamples found: the answer of
// cel-lens.exampleResults for every file with rules, in one object, in the
// order of the files' paths, and the problems the editor shows on the
// examples.
type ExamplesReport struct {
	ExampleResults
	// Problems are what the API server's schema refuses in an example that
	// expects to be allowed, what is wrong with a # cel-lens: comment, and
	// an example that expects to be refused and that nothing refuses so, once
	// however many rules allow it.
	Problems []ExampleProblem `json:"problems"`
	// FilesLeftOut is how many YAML files the run did not read for the limits
	// of the workspace scan: those over 1 MiB and those past the first 20000;
	// left out of the JSON when none.
	FilesLeftOut int `json:"filesLeftOut,omitempty"`
	// Notes are what the run leaves out, in words. They are not part of the
	// report's JSON.
	Notes []string `json:"-"`
}

// ExampleProblem is a problem of an example: where it is, by the example's
// file URI and by its path below its workspace folder, and what it is.
type ExampleProblem struct {
	URI      string `json:"uri"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// FilePath returns the path of the file at uri, which is a file URI, or uri
// itself when it is none.
func FilePath(uri string) string {
	if path, ok := diskPath(uri); ok {
		return path
	}
	return uri
}

// Start returns the path of the file r is in and the line, from 1, its text
// starts at; "" and 0 for a rule with no location.
func (r RuleResults) Start() (path string, line int) {
	if r.Location.URI == "" {
		return "", 0
	}
	return FilePath(r.Location.URI), r.Location.Range.Start.Line + 1
}

// RunExamples finds the CustomResourceDefinitions and ValidatingAdmissionPolicies
// of paths, files and folders, runs their rules on the examples of the
// workspace they make up as cel-lens.exampleResults does, for the Kubernetes
// version kube, and returns what each rule does to each example.
//
// A folder is walked for .yaml and .yml files as the workspace scan walks it:
// hidden folders and node_modules are left out, as are files over 1 MiB and the
// files after the first 20000, which are counted (FilesLeftOut) and said in a
// note; the cap of 100 examples of a rule, which is for the editor's latency,
// does not apply. A file is the workspace's alone,
// unless it is below a folder of paths. A path that does not exist, or a file
// that is no .yaml or .yml file, runs nothing: the report is nil and the error
// names each such path. A folder or file that cannot be read is skipped and
// named in the error, which joins one error for each, beside the report of the
// rest. When ctx is cancelled it stops and returns ctx's error.
func RunExamples(ctx context.Context, paths []string, kube kubeversion.Version) (*ExamplesReport, error) {
	return runExamples(ctx, os.DirFS, maxWorkspaceFiles, paths, kube)
}

// runExamples is RunExamples with the file system a folder is walked in, which
// dirFS gives, and the most files a walk reads, limit.
func runExamples(ctx context.Context, dirFS func(dir string) fs.FS, limit int, paths []string, kube kubeversion.Version) (*ExamplesReport, error) {
	ws, err := workspaceOf(dirFS, paths)
	if err != nil {
		return nil, err
	}
	var (
		unread []error
		big    []string // the files over the size limit
		over   int      // the files past the file limit
	)
	c := exampleCheck{
		folders: ws.folders, dirFS: ws.dirFS, limit: limit, kube: kube, memo: &exampleMemo{},
		results: &examples.Cache{}, policies: &examples.PolicyCache{},
		unread: func(path string, err error) {
			if pe, ok := errors.AsType[*fs.PathError](err); ok {
				err = pe.Err
			}
			unread = append(unread, fmt.Errorf("read %s: %w", path, err))
		},
		big: func(path string) {
			if !slices.Contains(big, path) {
				big = append(big, path)
			}
		},
		over: func() { over++ },
		// The cap on the examples of a rule is for the editor's latency.
		maxExamples: math.MaxInt,
	}
	var found []fileFound
	crds, err := c.examples(ctx, func(f walked, text []byte, objs []*examples.Object) {
		found = append(found, fileFound{f: f, text: text, objs: objs})
	})
	if err != nil {
		return nil, err
	}
	ev, err := c.evaluate(ctx, crds, found)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(ev.rules)+len(ev.policies))
	for _, crd := range crds {
		keys = append(keys, crd.File)
	}
	for key := range ev.policies {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	report := &ExamplesReport{ExampleResults: ExampleResults{KubernetesVersion: kube.String(), Rules: []RuleResults{}}, Problems: []ExampleProblem{}}
	for _, key := range slices.Compact(keys) {
		text := c.memo.crds[key].text
		if text == nil {
			text = c.admission.texts[key]
		}
		rules, ran := ev.rules[key]
		if !ran {
			// A file of CRDs without examples has its rules, with no results.
			for _, crd := range crds {
				if crd.File != key {
					continue
				}
				more, err := crd.Rules(ctx)
				if err != nil {
					return nil, err
				}
				rules = append(rules, more...)
			}
		}
		rc := resultsCheck{exampleCheck: c, uri: pathURI(filepath.FromSlash(key)), key: key}
		answer := rc.answer(text, rules, ev.crds[key], ev.rels)
		rc.answerPolicies(&answer, text, ev.policies[key], ev.rels)
		report.Rules = append(report.Rules, answer.Rules...)
	}
	problem := func(o *examples.Object, line, column int, severity, message string) {
		report.Problems = append(report.Problems, ExampleProblem{
			URI: pathURI(filepath.FromSlash(o.File)), File: ev.rels[o.File], Line: line, Column: column, Severity: severity, Message: message,
		})
	}
	for _, ff := range found {
		for _, o := range ff.objs {
			for _, d := range o.Diagnostics() {
				problem(o, d.Start.Line, d.Start.Column, string(d.Severity), d.Message)
			}
		}
	}
	// An example that expects to be refused and that nothing refuses so is
	// one problem, however many rules allow it: the editor warns on it once.
	nothing := map[exampleAt]bool{}
	unmet := func(o *examples.Object) {
		if k := at(o); o.Expect == examples.Refused && !ev.right[k] && !o.RefusedBySchema() && !nothing[k] {
			nothing[k] = true
			start, _ := o.Head()
			problem(o, start.Line, start.Column, string(check.SeverityError), inline.NothingRefuses(o.Message))
		}
	}
	for _, results := range ev.crds {
		for _, r := range results {
			unmet(r.Example)
		}
	}
	for _, runs := range ev.policies {
		for _, pr := range runs {
			for _, r := range pr.run.Results {
				unmet(r.Example)
			}
		}
	}
	for key, u := range c.unreadable {
		for _, d := range u.diags {
			rel, ok := ev.rels[key]
			if !ok {
				rel = filepath.Base(filepath.FromSlash(key))
			}
			report.Problems = append(report.Problems, ExampleProblem{
				URI: pathURI(filepath.FromSlash(key)), File: rel, Line: d.Start.Line, Column: d.Start.Column, Severity: string(d.Severity), Message: d.Message,
			})
		}
	}
	slices.SortStableFunc(report.Problems, func(a, b ExampleProblem) int {
		return cmp.Or(cmp.Compare(a.URI, b.URI), a.Line-b.Line, a.Column-b.Column)
	})
	for _, path := range big {
		report.Notes = append(report.Notes, fmt.Sprintf("%s is over the limit of %d bytes, so it is not read", path, maxWorkspaceFileSize))
	}
	if over > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf("%d YAML %s past the first %d %s not read", over, plural(over, "file", "files"), limit, plural(over, "is", "are")))
	}
	report.FilesLeftOut = len(big) + over
	return report, errors.Join(unread...)
}

// workspace is the folders a run of the examples walks, and the file system
// each is walked in.
type workspace struct {
	folders []string
	dirFS   func(dir string) fs.FS
}

// workspaceOf makes the workspace of paths: the folders among them, without
// those below another, then the folder of each file that is not below one, in
// which the file is all there is. An error names each path that does not exist
// or is a file other than .yaml or .yml.
func workspaceOf(dirFS func(dir string) fs.FS, paths []string) (workspace, error) {
	var (
		dirs, files []string
		errs        []error
	)
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", p, err))
			continue
		}
		info, err := os.Stat(abs)
		switch {
		case err != nil:
			if pe, ok := errors.AsType[*fs.PathError](err); ok {
				err = pe.Err
			}
			errs = append(errs, fmt.Errorf("read %s: %w", p, err))
		case info.IsDir():
			dirs = append(dirs, abs)
		case !isYAML(abs):
			errs = append(errs, fmt.Errorf("read %s: not a .yaml or .yml file", p))
		default:
			files = append(files, abs)
		}
	}
	if len(errs) > 0 {
		return workspace{}, errors.Join(errs...)
	}
	given := outermost(dirs)
	ws := workspace{folders: slices.Clone(given)}
	alone := map[string][]string{}
	for _, f := range files {
		below := slices.ContainsFunc(given, func(dir string) bool {
			rel, err := filepath.Rel(dir, f)
			return err == nil && filepath.IsLocal(rel)
		})
		if dir := filepath.Dir(f); !below && !slices.Contains(alone[dir], filepath.Base(f)) {
			if _, ok := alone[dir]; !ok {
				ws.folders = append(ws.folders, dir)
			}
			alone[dir] = append(alone[dir], filepath.Base(f))
		}
	}
	ws.dirFS = func(dir string) fs.FS {
		if names, ok := alone[dir]; ok {
			return fileSet{FS: dirFS(dir), names: names}
		}
		return dirFS(dir)
	}
	return ws, nil
}

// fileSet is a folder's file system with only the files named in it, which
// are in the folder itself.
type fileSet struct {
	fs.FS
	names []string
}

// Open opens the folder, or one of the files.
func (s fileSet) Open(name string) (fs.File, error) {
	if name != "." && !slices.Contains(s.names, name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return s.FS.Open(name)
}

// Stat describes the folder, or one of the files.
func (s fileSet) Stat(name string) (fs.FileInfo, error) {
	if name != "." && !slices.Contains(s.names, name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return fs.Stat(s.FS, name)
}

// ReadDir lists the files of the set, which only the folder has.
func (s fileSet) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries, err := fs.ReadDir(s.FS, name)
	return slices.DeleteFunc(entries, func(e fs.DirEntry) bool { return !slices.Contains(s.names, e.Name()) }), err
}

// plural is one when n is 1, else many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
