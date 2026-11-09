package lsp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"k8s.io/kube-openapi/pkg/validation/spec"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// lens is a .cellens file the session knows: from the workspace, or open in
// the editor.
type lens struct {
	// uri is where its diagnostics go; key is its name in the session, as
	// crdKey makes it.
	uri, key string
	text     []byte
	entries  []cellens.Entry
	problems []cellens.Problem
	// open is true while the editor has it open: its text is the editor's,
	// and version the editor's version of it.
	open    bool
	version int
	// checked are the diagnostics of its entries for each file they name, by
	// key, as lensDiagnostics last made them; a new text starts without.
	checked map[string][]map[string]any
}

// isLens reports whether the document at uri is a .cellens file.
func isLens(uri string) bool { return path.Base(crdKey(uri)) == cellens.FileName }

// putLens records the .cellens file at uri with its text, open when the
// editor has it, and reports whether its text changed. Its schemas are
// checked as the API server checks a CRD's, with ctx.
func (ss *session) putLens(ctx context.Context, uri string, text []byte, open bool) bool {
	key := crdKey(uri)
	ss.touchLens(key)
	if l, ok := ss.lenses[key]; ok && bytes.Equal(l.text, text) {
		if l.uri != uri {
			ss.lensChanges++ // the code lenses name the file by its URI
		}
		l.open, l.uri = open, uri
		return false
	}
	ss.lensChanges++
	entries, problems := cellens.Parse(text)
	entries, refused := cellens.CheckSchemas(ctx, entries)
	if len(refused) > 0 {
		problems = append(problems, refused...)
		slices.SortStableFunc(problems, func(a, b cellens.Problem) int { return cmp.Or(a.Line-b.Line, a.Column-b.Column) })
	}
	if ss.lenses == nil {
		ss.lenses = map[string]*lens{}
	}
	ss.lenses[key] = &lens{uri: uri, key: key, text: text, entries: entries, problems: problems, open: open}
	ss.pruneGoReads()
	return true
}

// readLens records the .cellens file at uri as it is on disk, or forgets it
// when it cannot be read or is no regular file: a symbolic link is not
// followed, as the search does not follow one. It reports whether that
// changed it.
func (ss *session) readLens(ctx context.Context, uri string) bool {
	p, ok := diskPath(uri)
	if !ok {
		return ss.dropLens(crdKey(uri))
	}
	if info, err := os.Lstat(p); err != nil || !info.Mode().IsRegular() {
		return ss.dropLens(crdKey(uri))
	}
	data, err := ss.readFile(p)
	if err != nil {
		return ss.dropLens(crdKey(uri))
	}
	return ss.putLens(ctx, uri, data, false)
}

// dropLens forgets the .cellens file with the key and reports whether the
// session had it.
func (ss *session) dropLens(key string) bool {
	ss.touchLens(key)
	_, had := ss.lenses[key]
	if had {
		ss.lensChanges++
	}
	delete(ss.lenses, key)
	ss.pruneGoReads()
	return had
}

// dropLensesUnder forgets the .cellens files below the folder with the key,
// except open ones, and reports whether there were any.
func (ss *session) dropLensesUnder(key string) bool {
	prefix := strings.TrimSuffix(key, "/") + "/"
	changed := false
	for k, l := range ss.lenses {
		if strings.HasPrefix(k, prefix) && !l.open {
			changed = ss.dropLens(k) || changed
		}
	}
	return changed
}

// touchLens records, for a search in progress, that the session changed the
// .cellens file with the key, so the search does not bring back what it read.
func (ss *session) touchLens(key string) {
	if ss.lensSearch != nil {
		ss.lensSearch.touched[key] = true
	}
}

// lensSearch is a search for .cellens files running in its own goroutine.
type lensSearch struct {
	cancel context.CancelFunc
	// roots are the keys of the folders it searches.
	roots []string
	// touched are the .cellens files the session changed while it ran.
	touched map[string]bool
	// batches are the batches it searches for (see session.batch).
	batches []int
	// done delivers what it found, once, when the goroutine ends.
	done chan lensFound
}

// lensFound is what a search for .cellens files found: their text by key,
// and the keys of the folders it could not read, whose files it does not know.
type lensFound struct {
	files   map[string][]byte
	partial []string
}

// searchLenses searches folders for .cellens files in a goroutine, for the
// batch in hand, or, while a search runs, queues them for the next. The
// message loop receives what it finds on ss.lensSearch.done; stopLensSearch
// stops it.
func (ss *session) searchLenses(ctx context.Context, folders []string) {
	if len(folders) == 0 {
		return
	}
	if ss.lensSearch != nil {
		ss.lensQueued = outermost(append(ss.lensQueued, folders...))
		if !slices.Contains(ss.lensQueuedBatches, ss.batch) {
			ss.lensQueuedBatches = append(ss.lensQueuedBatches, ss.batch)
		}
		return
	}
	ss.runLensSearch(ctx, outermost(folders), []int{ss.batch})
}

// runLensSearch starts the search of folders for batches; none may be running.
func (ss *session) runLensSearch(ctx context.Context, folders []string, batches []int) {
	dirFS := ss.dirFS()
	ctx, cancel := context.WithCancel(ctx)
	sc := &lensSearch{cancel: cancel, touched: map[string]bool{}, batches: batches, done: make(chan lensFound, 1)}
	for _, f := range folders {
		sc.roots = append(sc.roots, crdKey(pathURI(f)))
	}
	ss.lensSearch = sc
	go func() { sc.done <- findLenses(ctx, dirFS, folders) }()
}

// stopLensSearch cancels the search in progress and waits for it to end.
func (ss *session) stopLensSearch() {
	if ss.lensSearch == nil {
		return
	}
	ss.lensSearch.cancel()
	<-ss.lensSearch.done
	ss.lensSearch, ss.lensQueued, ss.lensQueuedBatches = nil, nil, nil
}

// findLenses reads the .cellens files below folders, skipping the folders the
// workspace scan skips and what is no regular file, such as a symbolic link,
// and reading at most maxWorkspaceFileSize bytes of one (see readLimited).
// dirFS gives the file system of a folder. It stops when ctx is cancelled.
func findLenses(ctx context.Context, dirFS func(dir string) fs.FS, folders []string) lensFound {
	found := lensFound{files: map[string][]byte{}}
	for _, dir := range folders {
		root := dirFS(dir)
		_ = fs.WalkDir(root, ".", func(rel string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			key := crdKey(pathURI(filepath.Join(dir, filepath.FromSlash(rel))))
			switch {
			case err != nil:
				found.partial = append(found.partial, key)
				return fs.SkipDir
			case d.IsDir() && rel != "." && skippedFolder(d.Name()):
				return fs.SkipDir
			case d.IsDir() || d.Name() != cellens.FileName || !d.Type().IsRegular():
				return nil
			}
			// A file that cannot be read, or is too big, is skipped.
			if data, rerr := readFS(root, rel); rerr == nil {
				found.files[key] = data
			}
			return nil
		})
	}
	return found
}

// readFS reads the file rel of fsys with readLimited.
func readFS(fsys fs.FS, rel string) ([]byte, error) {
	f, err := fsys.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readLimited(f)
}

// errNotRegular is why what is no regular file, such as a device, a pipe or a
// folder, is not read.
var errNotRegular = errors.New("it is no regular file")

// readLimited reads f, which must be a regular file of at most
// maxWorkspaceFileSize bytes. It reads through a limit, so a file that grows
// meanwhile never takes more memory than that.
func readLimited(f fs.File) ([]byte, error) {
	info, err := f.Stat()
	switch {
	case err != nil:
		return nil, err
	case !info.Mode().IsRegular():
		return nil, errNotRegular
	case info.Size() > maxWorkspaceFileSize:
		return nil, errTooBig
	}
	data, err := io.ReadAll(io.LimitReader(f, maxWorkspaceFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxWorkspaceFileSize {
		return nil, errTooBig
	}
	return data, nil
}

// readIn reads the file rel of the folder dir with readLimited, through an
// os.Root of dir, so a symbolic link cannot lead out of the folder. What is no
// regular file once links are followed, such as a link to /dev/zero or a pipe,
// is refused before it is opened, so it cannot hold the session.
func readIn(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readLimited(f)
}

// readFile reads the file at p with readIn, within the workspace folder it is
// in, or, outside every one, within its own folder.
func (ss *session) readFile(p string) ([]byte, error) {
	for _, dir := range ss.folders {
		if rel, err := filepath.Rel(dir, p); err == nil && filepath.IsLocal(rel) {
			return readIn(dir, rel)
		}
	}
	return readIn(filepath.Dir(p), filepath.Base(p))
}

// finishLensSearch records what the search found, in place of what the
// session had below the folders it searched, except the .cellens files that are
// open or that the session changed meanwhile, starts the search of the folders
// queued meanwhile, and settles its batches (see settle): what it changed
// waits for the CRD scan of a batch it searched for, so that batch checks the
// open documents once, after both.
func (ss *session) finishLensSearch(ctx context.Context, found lensFound) error {
	sc := ss.lensSearch
	ss.lensSearch = nil
	below := func(key string, dirs []string) bool {
		return slices.ContainsFunc(dirs, func(d string) bool { return strings.HasPrefix(key, strings.TrimSuffix(d, "/")+"/") })
	}
	changed := false
	for key, l := range ss.lenses {
		if _, ok := found.files[key]; ok || l.open || sc.touched[key] || !below(key, sc.roots) || below(key, found.partial) {
			continue
		}
		changed = ss.dropLens(key) || changed
	}
	for key, data := range found.files {
		if l := ss.lenses[key]; sc.touched[key] || l != nil && l.open {
			continue
		}
		changed = ss.putLens(ctx, pathURI(filepath.FromSlash(key)), data, false) || changed
	}
	queued := slices.DeleteFunc(ss.lensQueued, func(f string) bool { return !isDir(f) })
	batches, ended := ss.lensQueuedBatches, sc.batches
	ss.lensQueued, ss.lensQueuedBatches = nil, nil
	if len(queued) > 0 {
		ss.runLensSearch(ctx, queued, batches)
	} else {
		ended = append(ended, batches...) // their folders are gone
	}
	return ss.settle(ctx, ended, changed, true)
}

// lensSource is the name messages give the .cellens file l: its path below the
// workspace folder it is in, or its whole path.
func (ss *session) lensSource(l *lens) string {
	for _, f := range ss.folders {
		if rel, ok := strings.CutPrefix(l.key, strings.TrimSuffix(crdKey(pathURI(f)), "/")+"/"); ok {
			return rel
		}
	}
	return l.key
}

// target is the key of the file an entry of l names.
func (l *lens) target(e cellens.Entry) string { return path.Join(path.Dir(l.key), e.File) }

// assumedFor returns the types the .cellens files assume in the document at
// uri, in the order of the files' keys and then of their entries. An entry
// whose kind or Go type is not found is left out.
func (ss *session) assumedFor(ctx context.Context, uri string) []check.AssumedType {
	key := crdKey(uri)
	var out []check.AssumedType
	for _, l := range ss.sortedLenses() {
		for _, e := range l.entries {
			if l.target(e) != key {
				continue
			}
			if _, _, dup := ss.earlier(l, e); dup {
				continue
			}
			if t, _, msg := ss.assumedType(ctx, l, e); msg == "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// valueKey names the value e of l names, across .cellens files: its file's
// key, its document, version or variable, and path.
func (l *lens) valueKey(e cellens.Entry) string {
	return strings.Join([]string{l.target(e), e.Document.Kind, e.Document.Name, e.Version, e.Variable, e.Path.String()}, "\x00")
}

// earlier returns the entry of another .cellens file, before l in the order
// of the keys, for the value e of l names: then that one gives the value its
// type, and e is a problem.
func (ss *session) earlier(l *lens, e cellens.Entry) (*lens, cellens.Entry, bool) {
	key := l.valueKey(e)
	for _, o := range ss.sortedLenses() {
		if o.key >= l.key {
			break
		}
		for _, oe := range o.entries {
			if o.valueKey(oe) == key {
				return o, oe, true
			}
		}
	}
	return nil, cellens.Entry{}, false
}

func (ss *session) sortedLenses() []*lens {
	out := make([]*lens, 0, len(ss.lenses))
	for _, l := range ss.lenses {
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b *lens) int { return strings.Compare(a.key, b.key) })
	return out
}

// assumedType is the entry e of l as check takes it, or why it is not: its
// kind is no built-in kind and no CustomResourceDefinition the session knows,
// or its Go type cannot be read. notes are what the entry gets on its line
// all the same: the fields its Go type leaves out.
func (ss *session) assumedType(ctx context.Context, l *lens, e cellens.Entry) (t check.AssumedType, notes []string, msg string) {
	t = check.AssumedType{
		Kind: e.Document.Kind, Name: e.Document.Name, Version: e.Version, Variable: e.Variable,
		Source: ss.lensSource(l), Line: e.Line,
	}
	for _, s := range e.Path {
		switch s.Kind {
		case cellens.FieldStep:
			t.Path = append(t.Path, s.Name)
		case cellens.ItemsStep:
			t.Path = append(t.Path, assume.Items)
		case cellens.ValuesStep:
			t.Path = append(t.Path, assume.Values)
		}
	}
	switch {
	case e.Type != nil:
		t.Schema, t.Type = e.Type.Schema(), e.Type.String()
		return t, nil, ""
	case e.Schema != nil:
		t.Schema, t.Type = e.Schema.Value, assume.TypeText(e.Schema.Value)
		t.From = fmt.Sprintf("the schema at %s, line %d", t.Source, e.Schema.Line)
		return t, nil, ""
	case e.Go != nil:
		typ, msg := ss.goType(ctx, l.key, e.Go)
		if msg != "" {
			return t, nil, msg
		}
		t.Schema, t.Type = typ.Schema, assume.TypeText(typ.Schema)
		t.From = fmt.Sprintf("the Go type %s in %s:%d, named in %s, line %d", e.Go.Name, typ.File, typ.Line, t.Source, e.Line)
		for _, p := range typ.Problems {
			notes = append(notes, "go: "+p)
		}
		return t, notes, ""
	}
	t.Type = e.Kind.APIVersion + " " + e.Kind.Kind
	if s, ok := ss.builtInKind(e.Kind.APIVersion, e.Kind.Kind); ok {
		t.Schema = s
		return t, nil, ""
	}
	if !ss.features.Has(feature.CRDTypedPolicies) {
		return t, nil, fmt.Sprintf("kind %s is no built-in kind, and typing from a CustomResourceDefinition of the workspace is the experimental feature %s, which is off", t.Type, feature.CRDTypedPolicies)
	}
	if s, ok := ss.crds.Schema(e.Kind.APIVersion, e.Kind.Kind); ok {
		t.Schema = s
		return t, nil, ""
	}
	return t, nil, fmt.Sprintf("kind %s is no built-in kind, and no CustomResourceDefinition of the workspace defines it", t.Type)
}

// builtInKind returns the schema of the built-in kind, as schemaMap makes it,
// made once per session and kind, as the built-in kinds never change. The
// schema is shared: it must not change.
func (ss *session) builtInKind(apiVersion, kind string) (map[string]any, bool) {
	key := apiVersion + " " + kind
	if s, ok := ss.kinds[key]; ok {
		return s, s != nil
	}
	var s map[string]any
	if k, ok := matchkind.Lookup(apiVersion, kind); ok {
		s, _ = schemaMap(k.Schema)
	}
	if ss.kinds == nil {
		ss.kinds = map[string]map[string]any{}
	}
	ss.kinds[key] = s
	return s, s != nil
}

// errTooBig is why a file over maxWorkspaceFileSize is not read.
var errTooBig = errors.New("it is over the size limit")

// targetText returns the text of the file with the key: the editor's when the
// document is open, else the disk's.
func (ss *session) targetText(key string) ([]byte, error) {
	for uri, d := range ss.docs {
		if crdKey(uri) == key {
			return d.text, nil
		}
	}
	return ss.readFile(filepath.FromSlash(key))
}

// lensDiagnostics returns the problems of l as the protocol has them: those
// of its format, as errors, and the entries that apply to nothing, as
// warnings, each on the entry's line. When about is not "", only the entries
// for the file with that key are checked again, and those for the other files
// keep what the last call found, as only that file changed.
func (ss *session) lensDiagnostics(ctx context.Context, l *lens, about string) ([]map[string]any, error) {
	lines := splitLines(l.text)
	out := []map[string]any{}
	for _, p := range l.problems {
		out = append(out, lensDiagnostic(lines, p.Line, p.Column, messageError, p.Message))
	}
	var files []string
	byFile := map[string][]cellens.Entry{}
	for _, e := range l.entries {
		f := l.target(e)
		if _, ok := byFile[f]; !ok {
			files = append(files, f)
		}
		byFile[f] = append(byFile[f], e)
	}
	checked := map[string][]map[string]any{}
	for _, f := range files {
		if prev, ok := l.checked[f]; ok && about != "" && f != about {
			checked[f] = prev
			continue
		}
		diags, err := ss.entryDiagnostics(ctx, l, lines, byFile[f])
		if err != nil {
			return nil, err
		}
		checked[f] = diags
	}
	l.checked = checked
	for _, f := range files {
		out = append(out, checked[f]...)
	}
	slices.SortStableFunc(out, func(a, b map[string]any) int {
		ra, _ := a["range"].(lspRange)
		rb, _ := b["range"].(lspRange)
		return ra.Start.Line - rb.Start.Line
	})
	return out, nil
}

// entryDiagnostics returns the problems of entries, those of l for one file,
// as warnings on their lines of l, whose lines are lines.
func (ss *session) entryDiagnostics(ctx context.Context, l *lens, lines [][]rune, entries []cellens.Entry) ([]map[string]any, error) {
	out := []map[string]any{}
	warn := func(e cellens.Entry, msg string) {
		out = append(out, lensDiagnostic(lines, e.Line, 0, messageWarning, msg))
	}
	data, err := ss.targetText(l.target(entries[0]))
	if err != nil {
		for _, e := range entries {
			warn(e, fmt.Sprintf("file %s %s", e.File, unreadable(err)))
		}
		return out, nil
	}
	var types []check.AssumedType
	var of []cellens.Entry
	for _, e := range entries {
		if o, oe, dup := ss.earlier(l, e); dup {
			warn(e, fmt.Sprintf("a second type for this value: %s:%d gives it one: keep one", ss.lensSource(o), oe.Line))
			continue
		}
		t, notes, msg := ss.assumedType(ctx, l, e)
		for _, n := range notes {
			warn(e, n)
		}
		if msg != "" {
			warn(e, msg)
			continue
		}
		types, of = append(types, t), append(of, e)
	}
	problems, err := check.AssumedTypeProblems(ctx, data, check.WithAssumedTypes(types), check.WithVersion(ss.kube), check.WithFeatures(ss.features), check.WithCRDs(&ss.crds))
	if err != nil {
		return nil, err
	}
	for _, p := range problems {
		warn(of[p.Index], p.Message)
	}
	return out, nil
}

// unreadable says why a file an entry names could not be read, after its
// name.
func unreadable(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "does not exist"
	case errors.Is(err, errTooBig):
		return fmt.Sprintf("is over the limit of %d bytes cel-lens reads", maxWorkspaceFileSize)
	case errors.Is(err, errNotRegular):
		return "is no regular file"
	}
	return "cannot be read: " + err.Error()
}

// lensDiagnostic is a problem of a .cellens at line and column, from 1, as the
// protocol has it: from the column, or the line's first character after the
// indent and a list's dash when column is 0, to the end of the line.
func lensDiagnostic(lines [][]rune, line, column, severity int, message string) map[string]any {
	end := 1
	if line >= 1 && line <= len(lines) {
		text := string(lines[line-1])
		end = utf8.RuneCountInString(strings.TrimRightFunc(text, unicode.IsSpace)) + 1
		if column == 0 {
			rest := strings.TrimLeftFunc(text, func(r rune) bool { return unicode.IsSpace(r) || r == '-' })
			column = utf8.RuneCountInString(text) - utf8.RuneCountInString(rest) + 1
		}
	}
	column = max(column, 1)
	r := toRange(lines, extract.Position{Line: line, Column: column}, extract.Position{Line: line, Column: max(end, column+1)})
	return map[string]any{"range": r, "severity": severity, "source": "cel-lens", "message": message}
}

// publishLenses sends the diagnostics of the .cellens files that changed since
// they were last sent: all of them, or, when about is not "", those with an
// entry for the file with that key. It clears those of a .cellens file the
// session forgot.
func (ss *session) publishLenses(ctx context.Context, about string) error {
	uris := map[string]*lens{}
	for _, l := range ss.lenses {
		if about == "" || slices.ContainsFunc(l.entries, func(e cellens.Entry) bool { return l.target(e) == about }) {
			uris[l.uri] = l
		}
	}
	if about == "" {
		for uri := range ss.lensShown {
			if _, ok := uris[uri]; !ok {
				uris[uri] = nil
			}
		}
	}
	keys := make([]string, 0, len(uris))
	for uri := range uris {
		keys = append(keys, uri)
	}
	slices.Sort(keys)
	for _, uri := range keys {
		diags := []map[string]any{}
		if l := uris[uri]; l != nil {
			var err error
			if diags, err = ss.lensDiagnostics(ctx, l, about); err != nil {
				return err
			}
		}
		shown, was := ss.lensShown[uri]
		if was && reflect.DeepEqual(shown, diags) || !was && len(diags) == 0 {
			if uris[uri] == nil {
				delete(ss.lensShown, uri)
			}
			continue
		}
		if err := ss.conn.Notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": diags}); err != nil {
			return err
		}
		if ss.lensShown == nil {
			ss.lensShown = map[string][]map[string]any{}
		}
		ss.lensShown[uri] = diags
		if uris[uri] == nil {
			delete(ss.lensShown, uri)
		}
	}
	return nil
}

// schemaMap returns the schema of a built-in kind in the form a CRD's schema
// has when YAML decodes it.
func schemaMap(s *spec.Schema) (map[string]any, bool) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, false
	}
	return out, true
}
