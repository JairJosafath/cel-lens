package lsp

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// changeFolders sends ss a workspace/didChangeWorkspaceFolders with the
// folders added and removed.
func changeFolders(t *testing.T, ss *session, added, removed []string) {
	t.Helper()
	list := func(paths []string) []map[string]any {
		out := []map[string]any{}
		for _, p := range paths {
			out = append(out, map[string]any{"uri": pathURI(p), "name": filepath.Base(p)})
		}
		return out
	}
	params, _ := json.Marshal(map[string]any{"event": map[string]any{"added": list(added), "removed": list(removed)}})
	if err := ss.didChangeWorkspaceFolders(context.Background(), params); err != nil {
		t.Fatal(err)
	}
}

// TestFolderChangeApply checks how a change of the workspace folders applies
// to the folders, each given as the file URI a client sends; a folder spelled
// with a trailing slash or a drive letter in another case is the same folder
// (#38).
func TestFolderChangeApply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                    string
		folders, added, removed []string
		want                    []string
	}{
		{name: "added after the others", folders: []string{"file:///a", "file:///b"}, added: []string{"file:///c"}, want: []string{"file:///a", "file:///b", "file:///c"}},
		{name: "removed", folders: []string{"file:///a", "file:///b", "file:///c"}, removed: []string{"file:///b"}, want: []string{"file:///a", "file:///c"}},
		{name: "added twice once", folders: []string{"file:///a"}, added: []string{"file:///b", "file:///a", "file:///b"}, want: []string{"file:///a", "file:///b"}},
		{name: "unknown removed", folders: []string{"file:///a"}, removed: []string{"file:///z"}, want: []string{"file:///a"}},
		{name: "removed and added stays", folders: []string{"file:///a", "file:///b"}, added: []string{"file:///a"}, removed: []string{"file:///a"}, want: []string{"file:///b", "file:///a"}},
		{name: "all removed", folders: []string{"file:///a"}, removed: []string{"file:///a"}, want: []string{}},
		{name: "#38 removed with a trailing slash", folders: []string{"file:///ws/a", "file:///ws/b"}, removed: []string{"file:///ws/a/"}, want: []string{"file:///ws/b"}},
		{name: "#38 given with a trailing slash, removed without", folders: []string{"file:///ws/a/", "file:///ws/b"}, removed: []string{"file:///ws/a"}, want: []string{"file:///ws/b"}},
		{name: "#38 added again with a trailing slash", folders: []string{"file:///ws/a"}, added: []string{"file:///ws/a/"}, want: []string{"file:///ws/a"}},
		{name: "#38 removed with the drive letter in upper case", folders: []string{"file:///c%3A/ws/a", "file:///c%3A/ws/b"}, removed: []string{"file:///C:/ws/a"}, want: []string{"file:///c%3A/ws/b"}},
		{name: "#38 removed with the drive letter in lower case", folders: []string{"file:///D:/ws/a"}, removed: []string{"file:///d%3A/ws/a/"}, want: []string{}},
		{name: "#38 added again with the drive letter in another case", folders: []string{"file:///c%3A/ws"}, added: []string{"file:///C:/ws"}, want: []string{"file:///c%3A/ws"}},
		{name: "#38 another folder of the same drive", folders: []string{"file:///c%3A/ws/a"}, removed: []string{"file:///C:/ws/ab"}, want: []string{"file:///c%3A/ws/a"}},
	}
	paths := func(t *testing.T, uris []string) []string {
		t.Helper()
		out := []string{}
		for _, u := range uris {
			p, ok := folderPath(u)
			if !ok {
				t.Fatalf("folderPath(%q) = false, want a path", u)
			}
			out = append(out, p)
		}
		return out
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := folderChange{added: paths(t, tt.added), removed: paths(t, tt.removed)}.apply(paths(t, tt.folders))
			if want := paths(t, tt.want); !slices.Equal(got, want) {
				t.Errorf("apply(%q), added %q, removed %q = %q, want %q", tt.folders, tt.added, tt.removed, got, want)
			}
		})
	}
}

func TestUncovered(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	at := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	tests := []struct {
		name string
		a, b []string
		want []string
	}{
		{name: "same", a: []string{at("a")}, b: []string{at("a")}},
		{name: "below", a: []string{at("a/b")}, b: []string{at("a")}},
		{name: "above", a: []string{at("a")}, b: []string{at("a/b")}, want: []string{at("a")}},
		{name: "sibling with a common prefix", a: []string{at("ab")}, b: []string{at("a")}, want: []string{at("ab")}},
		{name: "none in b", a: []string{at("a"), at("c")}, want: []string{at("a"), at("c")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := uncovered(tt.a, tt.b); !slices.Equal(got, tt.want) {
				t.Errorf("uncovered(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestFolderInsideAnotherWalkedOnce checks that a workspace folder inside
// another is walked once, with the outer one, by the CRD scan of initialize
// and of a change of the folders, and by the search for .cellens files, and
// that a folder added inside another starts no walk (#38).
func TestFolderInsideAnotherWalkedOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	a, ab, ac := filepath.Join(root, "a"), filepath.Join(root, "a", "b"), filepath.Join(root, "a", "c")
	for _, dir := range []string{ab, ac} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ss := newSession(t)
	ss.features = feature.Of(feature.CRDTypedPolicies)
	var scans [][]string
	ss.scanner = func(_ context.Context, folders []string, _ *check.CRDs) []message {
		scans = append(scans, folders) // read after the scan ends
		return nil
	}
	finish := func() {
		t.Helper()
		if ss.scan != nil {
			<-ss.scan.done
			ss.scan = nil
			ss.crds.Untrack()
		}
		if ss.lensSearch != nil {
			<-ss.lensSearch.done
			ss.lensSearch = nil
		}
	}
	ss.folders = []string{ab, a}
	ss.startScan(context.Background())
	finish()
	changeFolders(t, ss, []string{ac}, nil)
	if ss.scan != nil || ss.lensSearch != nil {
		t.Errorf("adding %s inside %s started a scan or a search, want none", ac, a)
	}
	finish()
	changeFolders(t, ss, []string{root}, nil)
	var searched []string
	if ss.lensSearch != nil {
		searched = ss.lensSearch.roots
	}
	finish()
	changeFolders(t, ss, nil, []string{root})
	if ss.lensSearch != nil {
		t.Errorf("removing %s searched again, want no search: %s was searched with it", root, a)
	}
	finish()
	if want := [][]string{{a}, {root}, {a}}; !slices.EqualFunc(scans, want, slices.Equal) {
		t.Errorf("scans = %q, want %q", scans, want)
	}
	if want := []string{crdKey(pathURI(root))}; !slices.Equal(searched, want) {
		t.Errorf("search after adding %s = %q, want %q", root, searched, want)
	}
}

// TestAddedFolderScanCountsTheCapAcrossFolders checks that the CRD scan of a
// folder added walks every folder again, so the file cap counts across them
// all, as at initialize (#38).
func TestAddedFolderScanCountsTheCapAcrossFolders(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(a, "a.yaml"), []byte("kind: ConfigMap\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "crd.yaml"), []byte(scanCRD), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		cap  int
		want bool
	}{{cap: 1, want: false}, {cap: 2, want: true}} {
		ss := newSession(t)
		ss.features = feature.Of(feature.CRDTypedPolicies)
		ss.fileCap = tt.cap
		ss.folders = []string{a}
		changeFolders(t, ss, []string{b}, nil)
		if ss.scan == nil {
			t.Fatalf("cap %d: adding %s started no scan", tt.cap, b)
		}
		if want := []string{a, b}; !slices.Equal(ss.scan.folders, want) {
			t.Errorf("cap %d: scan of %q, want %q", tt.cap, ss.scan.folders, want)
		}
		finishCreatedScan(t, ss)
		if ss.lensSearch != nil {
			<-ss.lensSearch.done
		}
		if _, got := ss.crds.Schema("g/v1", "K"); got != tt.want {
			t.Errorf("cap %d: Schema(g/v1, K) found = %t, want %t", tt.cap, got, tt.want)
		}
	}
}

// TestRemovedFolderStopsTheRunsOverIt checks that a folder removed while a
// CRD scan and a search for .cellens files run over it is forgotten. The
// search is still walking, held at the removed folder until after the
// removal, and nothing it then finds below that folder is taken. The scan,
// whose result the message loop has not received, gives way to one of the
// folders left (#38).
func TestRemovedFolderStopsTheRunsOverIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(b, "crd.yaml"), []byte(scanCRD), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".cellens"), []byte("types: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ss := newSession(t)
	ss.features = feature.Of(feature.CRDTypedPolicies)
	ss.folders = []string{a, b}
	// The search cannot list b until release is closed, after the removal,
	// so it is still walking when b is removed.
	release := make(chan struct{})
	ss.fsys = func(dir string) fs.FS {
		if dir == b {
			return gatedFS{FS: os.DirFS(dir), release: release}
		}
		return os.DirFS(dir)
	}
	ss.searchLenses(context.Background(), ss.folders)
	ss.fsys = nil // the search took its file system; the scans read the disk
	ss.startScan(context.Background())
	changeFolders(t, ss, nil, []string{b})
	close(release)
	if want := []string{a}; ss.scan == nil || !slices.Equal(ss.scan.folders, want) {
		t.Fatalf("scan after removing %s = %+v, want one of %q", b, ss.scan, want)
	}
	finishCreatedScan(t, ss)
	if _, ok := ss.crds.Schema("g/v1", "K"); ok {
		t.Errorf("Schema(g/v1, K) after removing %s = found, want not found", b)
	}
	found := <-ss.lensSearch.done
	if key := crdKey(pathURI(filepath.Join(b, ".cellens"))); found.files[key] == nil {
		t.Fatalf("the search found %v, want %s, which it read after the removal", found.files, key)
	}
	if err := ss.finishLensSearch(context.Background(), found); err != nil {
		t.Fatal(err)
	}
	if key := crdKey(pathURI(filepath.Join(b, ".cellens"))); ss.lenses[key] != nil {
		t.Errorf("lenses after removing %s hold %s, want it forgotten", b, key)
	}
}
