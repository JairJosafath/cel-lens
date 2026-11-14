package lsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// The workspace folders are one list, session.folders, in the client's
// order: initialize sets it, and workspace/didChangeWorkspaceFolders changes
// it. Every background run that walks the workspace (the CRD scan, the
// search for .cellens files, the check of the examples, the searches of
// typeChoices and exampleResults) takes it when it starts, through
// outermost, so a folder inside another is walked once, with that one: a
// folder added is walked by the next run, and one removed by none.

// folderChange is a change of the workspace folders: the paths of the folders
// added and removed.
type folderChange struct {
	added, removed []string
}

// folderChangeOf reads the event of workspace/didChangeWorkspaceFolders,
// each folder's path as folderPath makes it. A folder whose URI is no file
// URI is left out, as initialize leaves it out.
func folderChangeOf(params json.RawMessage) (folderChange, bool) {
	type folder struct {
		URI string `json:"uri"`
	}
	var p struct {
		Event struct {
			Added   []folder `json:"added"`
			Removed []folder `json:"removed"`
		} `json:"event"`
	}
	if !decode(params, &p) {
		return folderChange{}, false
	}
	paths := func(fs []folder) []string {
		var out []string
		for _, f := range fs {
			if path, ok := folderPath(f.URI); ok {
				out = append(out, path)
			}
		}
		return out
	}
	return folderChange{added: paths(p.Event.Added), removed: paths(p.Event.Removed)}, true
}

// apply returns folders without those removed and with those added after
// them, in their order, each once, as sameFolder tells folders apart: a
// folder keeps the spelling it was first given. A folder both removed and
// added stays.
func (c folderChange) apply(folders []string) []string {
	has := func(list []string, f string) bool {
		return slices.ContainsFunc(list, func(o string) bool { return sameFolder(o, f) })
	}
	out := slices.DeleteFunc(slices.Clone(folders), func(f string) bool { return has(c.removed, f) })
	for _, f := range c.added {
		if !has(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// folderPath is the path of the workspace folder at uri, as diskPath makes
// it, cleaned, so a trailing slash is not part of it; false for a URI that is
// no file.
func folderPath(uri string) (string, bool) {
	p, ok := diskPath(uri)
	if !ok {
		return "", false
	}
	return filepath.Clean(p), true
}

// sameFolder reports whether the folder paths a and b name one folder, as
// clients spell it in a file URI: cleaned, and with the drive letter of a
// Windows path in either case, which crdKey keeps as the URI has it, so
// file:///c%3A/repo and file:///C:/repo/ are one folder.
func sameFolder(a, b string) bool { return folderID(a) == folderID(b) }

// folderID is the folder path p cleaned, slash-separated, with the drive
// letter of a Windows path in upper case.
func folderID(p string) string {
	k := filepath.ToSlash(filepath.Clean(p))
	if windowsDrive.MatchString("/" + k) {
		k = strings.ToUpper(k[:1]) + k[1:]
	}
	return k
}

// uncovered returns the folders of a that are neither one of b nor below one.
func uncovered(a, b []string) []string {
	return slices.DeleteFunc(slices.Clone(a), func(f string) bool { return inFolders(f, b) })
}

// inFolders reports whether the path p is one of folders or below one.
func inFolders(p string, folders []string) bool {
	return slices.ContainsFunc(folders, func(dir string) bool {
		rel, err := filepath.Rel(dir, p)
		return err == nil && (rel == "." || filepath.IsLocal(rel))
	})
}

// didChangeWorkspaceFolders follows a change of the workspace folders: the
// folders walked are then those of the new list, outermost. What was walked
// below a folder no longer walked is forgotten at once, but for the open
// files: the CRDs of its files, which a running CRD scan cannot bring back,
// its .cellens files, whose diagnostics are cleared, and the rules of its
// files the rule index kept (see forgetRulesUnder). A folder
// walked now and not before is searched for .cellens files in the
// background. While crd-typed-policies is on, the CRD scan walks the new
// folders again from the start, as at initialize, so the cap of
// maxWorkspaceFiles counts across all of them; a scan in progress gives way
// to it. The check of the examples is due again, and walks the new folders.
// The open documents are checked again when what they are typed from
// changed, once the searches and the scan of the change end (see settle).
// The Go types typeChoices lists are listed again. A folder added inside a
// folder of the workspace changes no walk.
func (ss *session) didChangeWorkspaceFolders(ctx context.Context, params json.RawMessage) error {
	change, ok := folderChangeOf(params)
	if !ok {
		return nil
	}
	folders := change.apply(ss.folders)
	if slices.Equal(folders, ss.folders) {
		return nil
	}
	before := outermost(ss.folders)
	ss.folders = folders
	ss.rulesChanged() // the files listed and their folders change
	walked := outermost(folders)
	ss.goGen++ // the Go types are those of the folders (see goFresh)
	if slices.Equal(before, walked) {
		return nil
	}
	ss.examplesChanged()
	gone, came := uncovered(before, walked), uncovered(walked, before)
	var goneKeys []string
	for _, g := range gone {
		goneKeys = append(goneKeys, crdKey(pathURI(g)))
	}
	// A file below a folder gone stays when it is open, or below a folder
	// walked now, such as one that was inside the folder removed.
	stays := func(key string) bool {
		return ss.isOpenKey(key) || inFolders(filepath.FromSlash(key), walked)
	}
	changed, lensChanged := false, false
	for _, key := range goneKeys {
		changed = ss.crds.ForgetUnder(key, stays) || changed
	}
	ss.forgetRulesUnder(goneKeys, walked)
	for key, l := range ss.lenses {
		if !l.open && slices.ContainsFunc(goneKeys, func(dir string) bool { return below(key, dir) }) && !inFolders(filepath.FromSlash(key), walked) {
			lensChanged = ss.dropLens(key) || lensChanged
		}
	}
	if sc := ss.lensSearch; sc != nil {
		sc.gone = append(sc.gone, goneKeys...)
		ss.lensQueued = slices.DeleteFunc(ss.lensQueued, func(f string) bool { return inFolders(f, gone) && !inFolders(f, walked) })
	}
	on := ss.features.Has(feature.CRDTypedPolicies)
	if len(came) == 0 && !on {
		if lensChanged {
			return ss.publishAll(ctx, "")
		}
		return nil
	}
	// As for a change on disk (see didChangeWatchedFiles), the change is a
	// batch: the end of its last search or scan checks the open documents
	// for it once.
	ss.batch++
	ss.forBatch()
	if changed && on || lensChanged || len(ss.lenses) > 0 {
		ss.owe(ss.batch)
	}
	ss.searchLenses(ctx, came)
	if on {
		ss.rescan(ctx, gone)
	}
	return nil
}

// rescan scans the workspace folders again from the start, for the batch in
// hand, in place of the scan in progress, which it stops: that one's batches
// and the folders queued after it, but those below a folder gone, join the
// new scan.
func (ss *session) rescan(ctx context.Context, gone []string) {
	folders := outermost(ss.folders)
	batches := []int{ss.batch}
	if ss.scan != nil {
		queued := slices.DeleteFunc(slices.Clone(ss.queued), func(f string) bool { return inFolders(f, gone) && !inFolders(f, folders) })
		batches = append(slices.Concat(ss.scan.batches, ss.queuedBatches), ss.batch)
		folders = outermost(append(folders, queued...))
		ss.stopScan()
	}
	ss.runScan(ctx, folders, batches)
}
