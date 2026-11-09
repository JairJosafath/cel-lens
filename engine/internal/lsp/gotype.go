package lsp

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/goschema"
)

// goRead is a Go type read for go: entries, kept while the .go files of its
// folder stay as they were.
type goRead struct {
	// stamp is the names, sizes and times of the folder's .go files when read.
	stamp string
	typ   *goschema.Type
	msg   string
}

// goRoot returns the folder go: entries of the .cellens file with the key may
// read Go from, as a path and as a key: the workspace folder it is in, the
// innermost one, or its own folder outside every one.
func (ss *session) goRoot(lensKey string) (dir, key string) {
	key = path.Dir(lensKey)
	dir = filepath.FromSlash(key)
	best := -1
	for _, f := range ss.folders {
		k := strings.TrimSuffix(crdKey(pathURI(f)), "/")
		if below(lensKey, k) && len(k) > best {
			dir, key, best = f, k, len(k)
		}
	}
	return dir, key
}

// goType reads the Go type g, which an entry of the .cellens file with the
// key names, from the .go files of its folder but its _test.go files, as
// goschema turns it into a schema. The folder must be in the workspace
// folder of the .cellens, which is read through an os.Root, so a symbolic link
// cannot lead out of it; at most maxWorkspaceFiles files of at most
// maxWorkspaceFileSize bytes each are read. What it read is kept until a .go
// file of the folder changes, is added or goes. msg says why the type cannot
// be read, as the entry's warning.
func (ss *session) goType(ctx context.Context, lensKey string, g *cellens.GoType) (*goschema.Type, string) {
	fail := func(why string) (*goschema.Type, string) { return nil, "go: " + g.String() + ": " + why }
	dir, rootKey := ss.goRoot(lensKey)
	folderKey := path.Join(path.Dir(lensKey), g.Folder)
	rel := "."
	if folderKey != rootKey {
		if !below(folderKey, rootKey) {
			return fail("the folder is outside the workspace folder, and cel-lens reads Go only there")
		}
		rel = strings.TrimPrefix(folderKey, strings.TrimSuffix(rootKey, "/")+"/")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fail("the workspace folder cannot be read: " + err.Error())
	}
	defer func() { _ = root.Close() }()
	fsys := root.FS()
	names, stamp, err := goFiles(fsys, rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fail("the folder does not exist")
	case err != nil:
		return fail("the folder cannot be read: " + err.Error())
	case len(names) == 0:
		return fail("the folder has no .go files but tests")
	}
	cacheKey := folderKey + "#" + g.Name
	if r, ok := ss.goReads[cacheKey]; ok && r.stamp == stamp {
		return r.typ, r.msg
	}
	files := make([]goschema.File, 0, len(names))
	for _, name := range names {
		if ctx.Err() != nil {
			return fail("cancelled")
		}
		data, err := readFS(fsys, path.Join(rel, name))
		if err != nil {
			continue // changed since it was listed; the next read sees it
		}
		files = append(files, goschema.File{Name: path.Join(rel, name), Src: data})
	}
	r := goRead{stamp: stamp}
	if r.typ, err = goschema.Read(files, g.Name); err != nil {
		r.typ, r.msg = nil, "go: "+g.String()+": "+err.Error()
	}
	if ss.goReads == nil {
		ss.goReads = map[string]goRead{}
	}
	ss.goReads[cacheKey] = r
	return r.typ, r.msg
}

// goFiles lists the .go files of the folder rel of fsys, but _test.go files,
// those that are no regular file once links are followed and those over
// maxWorkspaceFileSize bytes, at most maxWorkspaceFiles of them, by name. The
// stamp says what they are: their names, sizes and times.
func goFiles(fsys fs.FS, rel string) (names []string, stamp string, err error) {
	entries, err := fs.ReadDir(fsys, rel)
	if err != nil {
		return nil, "", err
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || len(names) == maxWorkspaceFiles {
			continue
		}
		info, err := fs.Stat(fsys, path.Join(rel, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxWorkspaceFileSize {
			continue
		}
		names = append(names, name)
		fmt.Fprintf(&b, "%s\x00%d\x00%d\n", name, info.Size(), info.ModTime().UnixNano())
	}
	return names, b.String(), nil
}

// goChoice is a Go type typeChoices offers for a go: entry.
type goChoice struct {
	// Folder is the file URI of the folder of the type's package.
	Folder string `json:"folder"`
	Name   string `json:"name"`
	// Doc is the first sentence of the type's doc comment, "" without one.
	Doc string `json:"doc"`
}

// maxGoChoices is the most Go types typeChoices lists, the first by folder
// and name, so its answer stays small in a workspace of many packages.
const maxGoChoices = 1000

// goFile is what the listing of Go types found in one .go file: its stamp
// (size and time) when read, the URI of its folder and its struct types.
type goFile struct {
	stamp   string
	folder  string
	structs []goschema.Struct
}

// goListed is what a listing of Go types found: the types, the first
// maxGoChoices, whether there were more, and what it read of each file, by
// path, for the next listing to reuse.
type goListed struct {
	types []goChoice
	more  bool
	files map[string]goFile
}

// goChoices lists the Go struct types a go: entry can name in folders, the
// workspace folders: the exported ones with a json-tagged field that the .go
// files declare, but _test.go files and those a go build on this machine
// skips, below the folders, skipping the folders the workspace scan skips and
// vendor and testdata, and looking at most at maxWorkspaceFiles files, of at
// most maxWorkspaceFileSize bytes each, read through the workspace folder; only
// those with a json tag are parsed. A file whose size and time are those in
// known is not read again. The types are sorted by folder and name, the first
// maxGoChoices of them. dirFS gives the file system of a folder. It stops when
// ctx is cancelled, with what it found so far.
func goChoices(ctx context.Context, dirFS func(dir string) fs.FS, folders []string, known map[string]goFile) goListed {
	listed := goListed{types: []goChoice{}, files: map[string]goFile{}}
	seen := 0
	for _, dir := range folders {
		_ = fs.WalkDir(dirFS(dir), ".", func(rel string, d fs.DirEntry, err error) error {
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case err != nil:
				return fs.SkipDir
			case d.IsDir() && rel != "." && (skippedFolder(d.Name()) || d.Name() == "vendor" || d.Name() == "testdata"):
				return fs.SkipDir
			case d.IsDir() || !d.Type().IsRegular() || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go"):
				return nil
			case seen == maxWorkspaceFiles:
				return fs.SkipAll
			}
			seen++
			info, ierr := d.Info()
			if ierr != nil || info.Size() > maxWorkspaceFileSize {
				return nil //nolint:nilerr // a file that cannot be read is skipped
			}
			p := filepath.Join(dir, filepath.FromSlash(rel))
			f := goFile{stamp: fmt.Sprintf("%d\x00%d", info.Size(), info.ModTime().UnixNano())}
			if k, ok := known[p]; ok && k.stamp == f.stamp {
				f = k
			} else if data, rerr := readIn(dir, filepath.FromSlash(rel)); rerr == nil && bytes.Contains(data, []byte(`json:"`)) {
				f.folder = pathURI(filepath.Dir(p))
				f.structs = goschema.Structs(rel, data)
			}
			listed.files[p] = f
			for _, s := range f.structs {
				listed.types = append(listed.types, goChoice{Folder: f.folder, Name: s.Name, Doc: s.Doc})
			}
			return nil
		})
	}
	slices.SortFunc(listed.types, func(a, b goChoice) int {
		return cmp.Or(strings.Compare(a.Folder, b.Folder), strings.Compare(a.Name, b.Name))
	})
	listed.types = slices.CompactFunc(listed.types, func(a, b goChoice) bool { return a.Folder == b.Folder && a.Name == b.Name })
	if len(listed.types) > maxGoChoices {
		listed.types, listed.more = listed.types[:maxGoChoices], true
	}
	return listed
}

// pruneGoReads forgets the Go types read for go: entries that no entry of a
// .cellens names any more.
func (ss *session) pruneGoReads() {
	if len(ss.goReads) == 0 {
		return
	}
	named := map[string]bool{}
	for _, l := range ss.lenses {
		for _, e := range l.entries {
			if e.Go != nil {
				named[path.Join(path.Dir(l.key), e.Go.Folder)+"#"+e.Go.Name] = true
			}
		}
	}
	maps.DeleteFunc(ss.goReads, func(k string, _ goRead) bool { return !named[k] })
}
