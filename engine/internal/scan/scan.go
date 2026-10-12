// Package scan checks the CEL in Kubernetes YAML files and folders and writes
// the problems as text or JSON, for the command line and CI.
//
// It only finds files and formats results; reading CEL out of a file and
// checking it is the job of pkg/check.
package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// Format is how Run writes the diagnostics.
type Format string

// The formats.
const (
	// Text writes one "file:line:col: severity: message" line per diagnostic.
	Text Format = "text"
	// JSON writes one JSON array of diagnostics, "[]" when there are none.
	JSON Format = "json"
)

// ErrFormat is returned, wrapped, by Run for a Format it does not know.
var ErrFormat = errors.New("unknown format")

// ErrSyntax is in the error Run returns when a file holds YAML that does not
// parse. The file's diagnostics already say where, so the error carries no
// position and callers need not print it.
var ErrSyntax = errors.New("YAML does not parse")

// Run checks the files and folders in paths and writes the diagnostics to
// stdout in the given format, ordered by path and then by position. A folder
// is walked for files ending in .yaml or .yml; a file named in paths is
// checked whatever its name. Unreadable and malformed files, and missing
// paths, do not stop the run: the other paths are still checked.
//
// It reports whether any diagnostic was written with the error severity. The
// returned error joins everything that could not be read, each naming its path,
// and is nil when all of paths was checked. opts choose, for one, the
// Kubernetes version to match; see check.WithVersion. When ctx is cancelled, Run stops
// and returns ctx's error.
func Run(ctx context.Context, paths []string, format Format, stdout io.Writer, opts ...check.Option) (found bool, err error) {
	return run(ctx, os.DirFS, paths, format, stdout, opts)
}

// run is Run with the file system a folder is walked in: dirFS returns it for
// the folder's path. A test uses it to make a folder fail to read.
func run(ctx context.Context, dirFS func(dir string) fs.FS, paths []string, format Format, stdout io.Writer, opts []check.Option) (found bool, err error) {
	if format != Text && format != JSON {
		return false, fmt.Errorf("format %q: %w (want text or json)", format, ErrFormat)
	}
	var (
		diags []check.Diagnostic
		errs  []error
	)
	type walked struct {
		files []string
		err   error
	}
	collected := make([]walked, len(paths))
	var crds check.CRDs
	for i, p := range paths {
		files, err := collect(ctx, dirFS, p)
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		collected[i] = walked{files, err}
		for _, f := range files {
			// An unreadable file is reported when it is checked.
			if data, err := os.ReadFile(f); err == nil {
				crds.Set(ctx, f, data)
			}
		}
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	opts = append(slices.Clone(opts), check.WithCRDs(&crds))
	for _, c := range collected {
		if c.err != nil {
			errs = append(errs, c.err)
		}
		for _, f := range c.files {
			d, err := checkFile(ctx, f, opts)
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if err != nil {
				errs = append(errs, err)
			}
			diags = append(diags, d...)
		}
	}
	for _, d := range diags {
		if d.Severity == check.SeverityError {
			found = true
		}
	}
	if werr := write(stdout, format, diags); werr != nil {
		errs = append(errs, werr)
	}
	return found, errors.Join(errs...)
}

// collect returns the files to check under path: path itself when it is a
// file, else the YAML files below it. A subfolder that cannot be read is
// skipped and reported in the returned error, which joins one error per
// subfolder; the other files are still returned.
func collect(ctx context.Context, dirFS func(dir string) fs.FS, path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, pathError("read", path, err)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var (
		files []string
		errs  []error
	)
	err = fs.WalkDir(dirFS(path), ".", func(rel string, d fs.DirEntry, err error) error {
		p := filepath.Join(path, filepath.FromSlash(rel))
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			errs = append(errs, fmt.Errorf("walk %s: %w", p, err))
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if !d.IsDir() && isYAML(p) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return files, pathError("walk", path, err)
	}
	return files, errors.Join(errs...)
}

// pathError describes a failed op on path. When err is a *fs.PathError it
// already names the path it failed on, which may lie below path, so only its
// cause is kept and the path is named once.
func pathError(op, path string, err error) error {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return fmt.Errorf("%s %s: %w", op, pe.Path, pe.Err)
	}
	return fmt.Errorf("%s %s: %w", op, path, err)
}

func isYAML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

func checkFile(ctx context.Context, file string, opts []check.Option) ([]check.Diagnostic, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, pathError("read", file, err)
	}
	diags, err := check.Check(ctx, file, data, opts...)
	if _, ok := errors.AsType[*extract.SyntaxError](err); ok {
		// Check reports each broken document as a diagnostic; printing the
		// parser's error too would say the same thing twice, on another line.
		return diags, ErrSyntax
	}
	return diags, err
}

// jsonPosition and jsonDiagnostic fix the JSON field names, which are the
// command's interface and must not follow the Go types.
type jsonPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type jsonDiagnostic struct {
	File     string       `json:"file"`
	Start    jsonPosition `json:"start"`
	End      jsonPosition `json:"end"`
	Severity string       `json:"severity"`
	Message  string       `json:"message"`
	Fixes    []jsonFix    `json:"fixes,omitempty"`
}

// jsonFix is a limit to add to a field; see check.Fix.
type jsonFix struct {
	FieldPath string `json:"fieldPath"`
	Limit     string `json:"limit"`
	Value     int64  `json:"value,omitempty"`
}

func position(p extract.Position) jsonPosition {
	return jsonPosition{Line: p.Line, Column: p.Column}
}

func write(w io.Writer, format Format, diags []check.Diagnostic) error {
	if format == Text {
		for _, d := range diags {
			if _, err := fmt.Fprintf(w, "%s:%d:%d: %s: %s\n", d.File, d.Start.Line, d.Start.Column, d.Severity, oneLine(d.Message)); err != nil {
				return fmt.Errorf("write results: %w", err)
			}
		}
		return nil
	}
	out := make([]jsonDiagnostic, len(diags))
	for i, d := range diags {
		out[i] = jsonDiagnostic{File: d.File, Start: position(d.Start), End: position(d.End), Severity: string(d.Severity), Message: d.Message}
		for _, f := range d.Fixes {
			out[i].Fixes = append(out[i].Fixes, jsonFix{f.FieldPath, f.Limit, f.Value})
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("write results: %w", err)
	}
	return nil
}

// oneLine joins the lines of a message, so that each diagnostic takes one line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}
