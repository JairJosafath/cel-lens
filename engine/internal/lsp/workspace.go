package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

const (
	// maxWorkspaceFiles and maxWorkspaceFileSize bound what the server reads of
	// a workspace to find CustomResourceDefinitions, so a huge folder never
	// holds up the first answers.
	maxWorkspaceFiles    = 20000
	maxWorkspaceFileSize = 1 << 20

	registerWatchersID = "cel-lens-watch-yaml"
)

// File change types of workspace/didChangeWatchedFiles, and the watch kinds
// that select creations and deletions.
const (
	fileCreated = 1
	fileChanged = 2
	fileDeleted = 3
	watchCreate = 1
	watchDelete = 4
)

var windowsDrive = regexp.MustCompile(`^/[A-Za-z]:`)

// crdKey names the file at uri in the session's CRDs. File URIs are named by
// their path, so the open document and the file on disk, which a client may
// spell differently (percent-encoding, drive letter), are one entry; any other
// URI is named by itself.
func crdKey(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	if windowsDrive.MatchString(u.Path) {
		return u.Path[1:]
	}
	return u.Path
}

// diskPath is the path of the file at uri, false for a URI that is no file.
func diskPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	return filepath.FromSlash(crdKey(uri)), true
}

// initializeParams are the parts of initialize that locate the workspace.
type initializeParams struct {
	RootURI          string `json:"rootUri"`
	WorkspaceFolders []struct {
		URI string `json:"uri"`
	} `json:"workspaceFolders"`
	Capabilities struct {
		Workspace struct {
			DidChangeWatchedFiles struct {
				DynamicRegistration bool `json:"dynamicRegistration"`
			} `json:"didChangeWatchedFiles"`
			CodeLens struct {
				RefreshSupport bool `json:"refreshSupport"`
			} `json:"codeLens"`
			ApplyEdit     bool `json:"applyEdit"`
			WorkspaceEdit struct {
				DocumentChanges    bool     `json:"documentChanges"`
				ResourceOperations []string `json:"resourceOperations"`
			} `json:"workspaceEdit"`
		} `json:"workspace"`
	} `json:"capabilities"`
}

// editCaps are what the client says it can do with a workspace/applyEdit.
func (p initializeParams) editCaps() editCaps {
	w := p.Capabilities.Workspace
	ops := w.WorkspaceEdit.ResourceOperations
	return editCaps{
		apply: w.ApplyEdit, documentChanges: w.WorkspaceEdit.DocumentChanges,
		create: slices.Contains(ops, "create"), delete: slices.Contains(ops, "delete"),
	}
}

// folders returns the folders of the workspace as paths.
func (p initializeParams) folders() []string {
	var uris []string
	for _, f := range p.WorkspaceFolders {
		uris = append(uris, f.URI)
	}
	if len(uris) == 0 && p.RootURI != "" {
		uris = append(uris, p.RootURI)
	}
	var out []string
	for _, uri := range uris {
		if path, ok := diskPath(uri); ok {
			out = append(out, path)
		}
	}
	return out
}

// scan is a workspace scan running in its own goroutine.
type scan struct {
	cancel context.CancelFunc
	// folders are the folders the scan reads.
	folders []string
	// done delivers the CRDs found, once, when the goroutine ends.
	done chan *check.CRDs
	// notes are what the scan has to tell the user; set before done delivers.
	notes []message
	// batches are the batches it scans for (see session.batch).
	batches []int
}

// startScan scans the workspace folders in a goroutine, for the batch in
// hand, unless a scan is already running. The message loop receives its
// result on ss.scan.done, and stopScan stops it.
func (ss *session) startScan(ctx context.Context) {
	if ss.scan != nil {
		return
	}
	ss.runScan(ctx, slices.Clone(ss.folders), []int{ss.batch})
}

// runScan starts the scan of folders in a goroutine, for batches; no scan may
// be running.
func (ss *session) runScan(ctx context.Context, folders []string, batches []int) {
	run := ss.scanFunc()
	ctx, cancel := context.WithCancel(ctx)
	sc := &scan{cancel: cancel, folders: folders, batches: batches, done: make(chan *check.CRDs, 1)}
	ss.scan = sc
	ss.crds.Track()
	for uri := range ss.docs {
		ss.crds.Keep(crdKey(uri))
	}
	go func() {
		var found check.CRDs
		sc.notes = run(ctx, folders, &found)
		sc.done <- &found
	}()
}

// dirFS is the function that gives the file system of a workspace folder: the
// injected one, or os.DirFS.
func (ss *session) dirFS() func(dir string) fs.FS {
	if ss.fsys != nil {
		return ss.fsys
	}
	return os.DirFS
}

// scanFunc is the function that scans folders: the injected scanner, or
// scanWorkspace with the file cap.
func (ss *session) scanFunc() func(ctx context.Context, folders []string, dst *check.CRDs) []message {
	if ss.scanner != nil {
		return ss.scanner
	}
	limit := ss.fileCap
	if limit <= 0 {
		limit = maxWorkspaceFiles
	}
	return func(ctx context.Context, folders []string, dst *check.CRDs) []message {
		return scanWorkspace(ctx, ss.dirFS(), folders, limit, dst)
	}
}

// scanFolders reads the folders, which were created or renamed in. A scan in
// progress is not restarted, so the files it has read are not read again and
// it ends however many notifications arrive meanwhile: the folders wait in
// ss.queued and are scanned together when it ends. A folder below another of
// the list is read with that one. The message loop receives the result like
// that of the workspace scan.
func (ss *session) scanFolders(ctx context.Context, folders []string) {
	if ss.scan != nil {
		ss.queued = outermost(append(ss.queued, folders...))
		if !slices.Contains(ss.queuedBatches, ss.batch) {
			ss.queuedBatches = append(ss.queuedBatches, ss.batch)
		}
		return
	}
	ss.runScan(ctx, outermost(folders), []int{ss.batch})
}

// scanQueued starts the scan of the folders that were created during the scan
// that just ended and still exist, if there are any and crd-typed-policies is
// still on, for the batches that queued them.
func (ss *session) scanQueued(ctx context.Context) {
	folders := slices.DeleteFunc(ss.queued, func(f string) bool { return !isDir(f) })
	batches := ss.queuedBatches
	ss.queued, ss.queuedBatches = nil, nil
	if len(folders) > 0 && ss.features.Has(feature.CRDTypedPolicies) {
		ss.runScan(ctx, folders, batches)
	}
}

// endScan handles the end of the scan in progress, which found found: it
// tells the user what the scan has to say, records the CRDs (see finishScan)
// and starts the scan of the folders queued meanwhile. When those are gone,
// it settles the batches that queued them.
func (ss *session) endScan(ctx context.Context, found *check.CRDs) error {
	sc := ss.scan
	ss.scan = nil
	if err := ss.warn(sc.notes); err != nil {
		return err
	}
	if err := ss.finishScan(ctx, found, sc.batches); err != nil {
		return err
	}
	queued := ss.queuedBatches
	ss.scanQueued(ctx)
	if ss.scan == nil && len(queued) > 0 {
		return ss.settle(ctx, queued, false, false)
	}
	return nil
}

// busy reports whether a scan or search for batch b runs or waits.
func (ss *session) busy(b int) bool {
	return ss.scan != nil && slices.Contains(ss.scan.batches, b) || slices.Contains(ss.queuedBatches, b) ||
		ss.lensSearch != nil && slices.Contains(ss.lensSearch.batches, b) || slices.Contains(ss.lensQueuedBatches, b)
}

// owe records that batch b owes a check of the open documents.
func (ss *session) owe(b int) {
	if ss.owed == nil {
		ss.owed = map[int]bool{}
	}
	ss.owed[b] = true
}

// settle checks the open documents again after a scan or search for batches
// ended: at once when own, what the ended work changed itself, is true, and
// for what a batch owes once none of its work is left. With hold, own waits
// instead for the batches whose work is left, so a batch checks once, after
// its scan and its search; a scan does not hold, so what it found is checked
// when it ends, whatever other batches wait for. Any check pays what all
// batches owe until then (see publishAll).
func (ss *session) settle(ctx context.Context, batches []int, own, hold bool) error {
	publish := false
	var busy []int
	for _, b := range batches {
		if ss.busy(b) {
			busy = append(busy, b)
			continue
		}
		publish = publish || ss.owed[b]
		delete(ss.owed, b)
	}
	if own && hold && len(busy) > 0 {
		for _, b := range busy {
			ss.owe(b)
		}
		own = false
	}
	if !own && !publish {
		return ctx.Err()
	}
	return ss.publishAll(ctx, "")
}

// outermost returns the folders without those below another of them and
// without duplicates, in their order.
func outermost(folders []string) []string {
	var out []string
	for _, f := range folders {
		below := slices.ContainsFunc(folders, func(o string) bool {
			rel, err := filepath.Rel(o, f)
			return o != f && err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
		})
		if !below && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// stopScan cancels the scan in progress and waits for it to end.
func (ss *session) stopScan() {
	if ss.scan == nil {
		return
	}
	ss.scan.cancel()
	<-ss.scan.done
	ss.scan = nil
	ss.queued, ss.queuedBatches = nil, nil
	ss.crds.Untrack()
}

// finishScan records the CRDs the scan for batches found, replacing those it
// had for the same files, except those of open documents and of files that
// changed during the scan, which win, and settles the batches (see settle):
// it checks the open documents again when that changed anything and
// crd-typed-policies is still on, and for what a batch owes. A file the
// session read, changed or forgot during the scan is not taken from the scan.
// It also reads again the known CRD files under the folders the scan skips,
// which it never read.
func (ss *session) finishScan(ctx context.Context, found *check.CRDs, batches []int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	reread := ss.rereadSkipped(ctx, found)
	own := ss.crds.Fill(found) || reread
	return ss.settle(ctx, batches, own && ss.features.Has(feature.CRDTypedPolicies), false)
}

// rereadSkipped reads again the known CRD files under the folders the scan
// skipped, which it never read, so an edit made on disk while they were closed
// is followed. An open document keeps its text. It reports whether the CRDs
// changed.
func (ss *session) rereadSkipped(ctx context.Context, found *check.CRDs) bool {
	changed := false
	for _, file := range ss.crds.UnderSkipped(found) {
		if ctx.Err() != nil {
			break
		}
		if !ss.isOpenKey(file) {
			changed = ss.readFromDisk(ctx, pathURI(filepath.FromSlash(file))) || changed
		}
	}
	return changed
}

// scanWorkspace reads the YAML files below the workspace folders into dst,
// skipping hidden folders and node_modules, files over
// maxWorkspaceFileSize and files past limit. A folder or file that
// cannot be read is skipped. It lists the files it found in dst, which tells
// Fill to forget the others below the folders, except below a folder it could
// not read or left files out of, whose CRDs are kept; a
// file below a folder it skipped is kept while it is on disk.
// dirFS gives the file system of a folder; os.DirFS reads the disk.
// It stops when ctx is cancelled. It returns what the user should be told: one
// warning when the scan stopped at limit, and a log message for each folder
// that could not be read and one for the files skipped for their size: one
// file is named with its size, many files are counted in one summary line.
func scanWorkspace(ctx context.Context, dirFS func(dir string) fs.FS, folders []string, limit int, dst *check.CRDs) []message {
	var notes []message
	capped := false
	capAt := -1              // where the file cap warning sits in notes, -1 when none
	var oversized []string   // files skipped for their size, in scan order
	var oversizedBytes int64 // the size of the last one, for the one-file message
	roots := make([]string, len(folders))
	for i, dir := range folders {
		roots[i] = crdKey(pathURI(dir))
	}
	dst.ScanRoots(roots)
	walkYAML(ctx, dirFS, folders, limit, yamlWalk{
		unreadable: func(path string, err error) {
			dst.Incomplete(crdKey(pathURI(path)))
			notes = append(notes, message{typ: messageWarning, log: true, text: fmt.Sprintf(
				"cel-lens left the scan of %s incomplete: it could not be read (%v); the CRDs known below it are kept as they were, those below the other folders are rescanned", path, err)})
		},
		skipped: func(path string) { dst.SkipDir(crdKey(pathURI(path)), fileExists) },
		capped: func() {
			for _, r := range roots {
				dst.Incomplete(r)
			}
			if !capped {
				capped = true
				capAt = len(notes)
				notes = append(notes, message{typ: messageWarning, text: fmt.Sprintf(
					"cel-lens stopped scanning the workspace for CustomResourceDefinitions after %d YAML files; CRDs in the files left out are not known, so a params type may stay dyn", limit)})
			}
		},
		file: func(dir, rel string, d fs.DirEntry) bool {
			path := filepath.Join(dir, filepath.FromSlash(rel))
			dst.List(crdKey(pathURI(path)))
			info, err := d.Info()
			if err == nil && info.Size() > maxWorkspaceFileSize {
				oversized = append(oversized, path)
				oversizedBytes = info.Size()
				return true
			}
			if err != nil {
				return true // a file that cannot be read is skipped
			}
			// readIn refuses what a symbolic link leads to outside dir, a
			// device or pipe, and more than the size limit.
			if data, err := readIn(dir, filepath.FromSlash(rel)); err == nil {
				dst.Set(ctx, crdKey(pathURI(path)), data)
			}
			return true
		},
	})
	// The size note goes before the cap warning, which ends the client's wait.
	if capAt < 0 {
		capAt = len(notes)
	}
	return slices.Insert(notes, capAt, oversizeNote(oversized, oversizedBytes)...)
}

// yamlWalk is what walkYAML calls as it walks; a nil function is not called.
type yamlWalk struct {
	// unreadable is called for a folder or file that cannot be read, which
	// the walk skips.
	unreadable func(path string, err error)
	// skipped is called for a folder the walk skips by its name (see
	// skippedFolder).
	skipped func(path string)
	// capped is called, in each folder, at the first YAML file past the
	// limit, where the walk of the folder stops.
	capped func()
	// file is called for each YAML file, with the folder of folders it is
	// below and its slash-separated path relative to it. It returns false to
	// stop the walk.
	file func(dir, rel string, d fs.DirEntry) bool
}

// walkYAML walks the YAML files below folders as the workspace scan reads
// them, in the order of folders and then of their paths: it skips hidden
// folders and node_modules, and stops after limit files. dirFS gives the
// file system of a folder; os.DirFS reads the disk. It stops when ctx is
// cancelled.
func walkYAML(ctx context.Context, dirFS func(dir string) fs.FS, folders []string, limit int, w yamlWalk) {
	seen := 0
	stopped := false
	for _, dir := range folders {
		if stopped {
			return
		}
		_ = fs.WalkDir(dirFS(dir), ".", func(rel string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				if w.unreadable != nil {
					w.unreadable(filepath.Join(dir, filepath.FromSlash(rel)), err)
				}
				return fs.SkipDir // skip what cannot be read
			}
			if d.IsDir() {
				if rel != "." && skippedFolder(d.Name()) {
					if w.skipped != nil {
						w.skipped(filepath.Join(dir, filepath.FromSlash(rel)))
					}
					return fs.SkipDir
				}
				return nil
			}
			if !isYAML(rel) {
				return nil
			}
			seen++
			if seen > limit {
				if w.capped != nil {
					w.capped()
				}
				return fs.SkipAll
			}
			if w.file != nil && !w.file(dir, rel, d) {
				stopped = true
				return fs.SkipAll
			}
			return nil
		})
	}
}

// maxNamedOversized is how many skipped file names the summary line gives.
const maxNamedOversized = 3

// oversizeNote tells of the files skipped for their size: one file gets its
// own message with its size, many files one summary line. It returns no
// message for no files, so the caller drops it.
func oversizeNote(paths []string, size int64) []message {
	switch len(paths) {
	case 0:
		return nil
	case 1:
		return []message{{typ: messageWarning, log: true, text: fmt.Sprintf(
			"cel-lens skipped %s while scanning for CustomResourceDefinitions: it is %d bytes, over the limit of %d bytes", paths[0], size, maxWorkspaceFileSize)}}
	}
	named := paths[:min(len(paths), maxNamedOversized)]
	text := fmt.Sprintf("cel-lens skipped %d files while scanning for CustomResourceDefinitions: each is over the limit of %d bytes (%s",
		len(paths), maxWorkspaceFileSize, strings.Join(named, ", "))
	if more := len(paths) - len(named); more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return []message{{typ: messageWarning, log: true, text: text + ")"}}
}

// fileExists reports whether the file with the slash-separated key is on disk.
func fileExists(key string) bool {
	_, err := os.Stat(filepath.FromSlash(key))
	return err == nil
}

func isYAML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

// pathURI is the file URI of an absolute path.
func pathURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // a Windows drive
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// registerWatchers asks the client, if it can, to send
// workspace/didChangeWatchedFiles for YAML and .cellens files, and for every
// creation and deletion, so CRDs and .cellens files that change on disk
// without being open are followed. A client reports a removed folder, and a
// renamed one as a deletion and a creation of the folder, which only a
// pattern that matches folders sees.
func (ss *session) registerWatchers() error {
	if !ss.watchFiles {
		return nil
	}
	return ss.conn.Request(registerWatchersID, "client/registerCapability", map[string]any{
		"registrations": []map[string]any{{
			"id":     registerWatchersID,
			"method": "workspace/didChangeWatchedFiles",
			"registerOptions": map[string]any{
				"watchers": []map[string]any{
					{"globPattern": "**/*.{yaml,yml}"},
					{"globPattern": "**/" + cellens.FileName},
					{"globPattern": "**", "kind": watchCreate | watchDelete},
				},
			},
		}},
	})
}

// didChangeWatchedFiles reads the changed files again, or forgets the deleted
// ones, starts a background scan of the YAML files below the created folders, and checks the open documents again when the CRDs changed. A file that
// is open keeps the text of the editor. While crd-typed-policies is off it reads
// no CRD: the CRDs are not used, and turning the feature
// on scans the whole workspace, which finds what changed meanwhile. It still
// forgets deleted files and every file below a deleted folder, a cheap step, so
// the scan never sees a CRD whose file is gone.
//
// .cellens files are followed whatever the features: a changed or created one
// is read, a deleted one, and those below a deleted folder, forgotten, and a
// created folder is searched for them in the background; the open documents
// are checked again when one changed. The diagnostics of the .cellens files
// are sent again when they changed, as a YAML file an entry names may have
// come or gone.
func (ss *session) didChangeWatchedFiles(ctx context.Context, params json.RawMessage) error {
	on := ss.features.Has(feature.CRDTypedPolicies)
	var p struct {
		Changes []struct {
			URI  string `json:"uri"`
			Type int    `json:"type"`
		} `json:"changes"`
	}
	if !decode(params, &p) {
		return nil
	}
	lensChanged := false
	var lensFolders []string
	for _, c := range p.Changes {
		key := crdKey(c.URI)
		switch l := ss.lenses[key]; {
		case isLens(c.URI) && l != nil && l.open:
		case isLens(c.URI) && c.Type == fileDeleted:
			lensChanged = ss.dropLens(key) || lensChanged
		case isLens(c.URI):
			lensChanged = ss.readLens(ctx, c.URI) || lensChanged
		case c.Type == fileDeleted:
			lensChanged = ss.dropLensesUnder(key) || lensChanged
		case c.Type == fileCreated:
			if path, ok := diskPath(c.URI); ok && !ss.skippedCreated(path) && isDir(path) {
				lensFolders = append(lensFolders, path)
			}
		}
	}
	changed := false
	var created []string
	for _, c := range p.Changes {
		if _, open := ss.docs[c.URI]; open || isLens(c.URI) {
			continue
		}
		switch {
		case c.Type == fileDeleted:
			// A client sends one event for a removed folder, not one per file.
			if _, file := diskPath(c.URI); file {
				changed = ss.crds.ForgetUnder(crdKey(c.URI), ss.isOpenKey) || changed
			}
			if isYAML(crdKey(c.URI)) {
				changed = ss.crds.Forget(crdKey(c.URI)) || changed
			}
		case on && isYAML(crdKey(c.URI)):
			changed = ss.readFromDisk(ctx, c.URI) || changed
		case on && c.Type == fileCreated:
			// A client sends one event for a created folder, not one per file.
			if path, ok := diskPath(c.URI); ok && !ss.skippedCreated(path) && isDir(path) {
				created = append(created, path)
			}
		}
	}
	if len(created) == 0 && len(lensFolders) == 0 {
		if lensChanged || changed && on {
			return ss.publishAll(ctx, "")
		}
		// A YAML file an entry of a .cellens names may have come or gone.
		return ss.publishLenses(ctx, "")
	}
	// The batch starts or queues a scan of the created folders, a search of
	// them for .cellens files, or both; the end of its last one checks the
	// open documents for it once, so no state without what they find is
	// published in between, not even at the end of a scan or search in
	// progress, behind which they wait. The diagnostics of the .cellens files
	// may change with any file.
	ss.batch++
	if changed && on || lensChanged || len(ss.lenses) > 0 {
		ss.owe(ss.batch)
	}
	ss.searchLenses(ctx, lensFolders)
	if len(created) > 0 {
		ss.scanFolders(ctx, created)
	}
	return nil
}

// skippedFolder reports whether the workspace scan skips a folder of that
// name: a hidden folder or node_modules.
func skippedFolder(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// skippedCreated reports whether the workspace scan would skip the created
// folder: every workspace folder that contains it has a hidden or node_modules
// element between itself and the folder. A folder outside every workspace
// folder is judged by its own name.
func (ss *session) skippedCreated(path string) bool {
	inside := false
	for _, root := range ss.folders {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		inside = true
		if rel == "." || !slices.ContainsFunc(strings.Split(rel, string(filepath.Separator)), skippedFolder) {
			return false
		}
	}
	return inside || skippedFolder(filepath.Base(path))
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isOpenKey reports whether the CRDs entry file is an open document, whose
// text the editor owns.
func (ss *session) isOpenKey(file string) bool {
	for uri := range ss.docs {
		if crdKey(uri) == file {
			return true
		}
	}
	return false
}

// readFromDisk records the CRDs of the file at uri as it is on disk, and
// forgets the file when it cannot be read. It reports whether the CRDs changed.
func (ss *session) readFromDisk(ctx context.Context, uri string) bool {
	path, ok := diskPath(uri)
	if !ok {
		return ss.crds.Forget(crdKey(uri))
	}
	data, err := ss.readFile(path)
	if err != nil {
		return ss.crds.Forget(crdKey(uri))
	}
	return ss.crds.Set(ctx, crdKey(uri), data)
}

// publishAll checks the open documents again and sends their diagnostics,
// except the one at skip, in the order of their URIs, and then those of the
// .cellens files that changed. That pays what every batch owes (see settle):
// what a batch's scan or search still finds, it publishes itself.
// It then asks the client to request the code lenses again, whose lines
// may have changed with the documents' types.
func (ss *session) publishAll(ctx context.Context, skip string) error {
	clear(ss.owed)
	uris := make([]string, 0, len(ss.docs))
	for uri := range ss.docs {
		if uri != skip {
			uris = append(uris, uri)
		}
	}
	slices.Sort(uris)
	for _, uri := range uris {
		if err := ss.publish(ctx, uri); err != nil {
			return err
		}
	}
	if err := ss.publishLenses(ctx, ""); err != nil {
		return err
	}
	return ss.refreshCodeLenses()
}
