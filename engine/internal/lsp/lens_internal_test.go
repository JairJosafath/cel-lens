package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// TestFindLenses checks that the search reads every .cellens below the
// folders except below those the workspace scan skips, and names the folders
// it cannot read.
func TestFindLenses(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		".cellens":                 {Data: []byte("types: []\n")},
		"a/b/.cellens":             {Data: []byte("types:\n")},
		"a/other.yaml":             {Data: []byte("x: 1\n")},
		".git/.cellens":            {Data: []byte("hidden\n")},
		"node_modules/x/.cellens":  {Data: []byte("vendored\n")},
		"locked/.cellens":          {Data: []byte("unread\n")},
		"a/not-a-lens.cellens":     {Data: []byte("other\n")},
		"a/b/c/.cellens/inner.txt": {Data: []byte("a folder named .cellens\n")},
	}
	found := findLenses(context.Background(), func(string) fs.FS { return failDirFS{fsys, "locked"} }, []string{"/ws"})
	keys := slices.Sorted(maps.Keys(found.files))
	if want := []string{"/ws/.cellens", "/ws/a/b/.cellens"}; !slices.Equal(keys, want) {
		t.Errorf("findLenses found %q, want %q", keys, want)
	}
	if want := []string{"/ws/locked"}; !slices.Equal(found.partial, want) {
		t.Errorf("findLenses partial = %q, want %q", found.partial, want)
	}
}

// TestLensSearchKeepsEdits checks that the end of a search keeps a .cellens
// the session changed or opened meanwhile, forgets one gone from the searched
// folders, and keeps those below a folder it could not read or outside them.
func TestLensSearchKeepsEdits(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	ss.putLens(t.Context(), "file:///ws/gone/.cellens", []byte("types:\n"), false)
	ss.putLens(t.Context(), "file:///ws/locked/.cellens", []byte("types:\n"), false)
	ss.putLens(t.Context(), "file:///other/.cellens", []byte("types:\n"), false)
	ss.putLens(t.Context(), "file:///ws/open/.cellens", []byte("types: # editor\n"), true)
	release := make(chan struct{})
	ss.fsys = func(string) fs.FS { <-release; return fstest.MapFS{} }
	ss.searchLenses(context.Background(), []string{"/ws"})
	ss.putLens(t.Context(), "file:///ws/edited/.cellens", []byte("types: # edited\n"), false)
	close(release)
	found := <-ss.lensSearch.done
	found.files["/ws/edited/.cellens"] = []byte("types: # disk\n")
	found.files["/ws/open/.cellens"] = []byte("types: # disk\n")
	found.files["/ws/new/.cellens"] = []byte("types: # new\n")
	found.partial = []string{"/ws/locked"}
	if err := ss.finishLensSearch(context.Background(), found); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"/ws/locked/.cellens": "types:\n",
		"/other/.cellens":     "types:\n",
		"/ws/open/.cellens":   "types: # editor\n",
		"/ws/edited/.cellens": "types: # edited\n",
		"/ws/new/.cellens":    "types: # new\n",
	}
	got := map[string]string{}
	for k, l := range ss.lenses {
		got[k] = string(l.text)
	}
	if !maps.Equal(got, want) {
		t.Errorf("lenses after the search = %q, want %q", got, want)
	}
}

// publishCounter is a session whose messages go to a buffer, to count the
// diagnostics it publishes.
type publishCounter struct{ out *bytes.Buffer }

func countPublishes(ss *session) publishCounter {
	var out bytes.Buffer
	ss.conn = jsonrpc.NewConn(strings.NewReader(""), &out)
	return publishCounter{&out}
}

func (p publishCounter) n() int {
	return strings.Count(p.out.String(), "textDocument/publishDiagnostics")
}

// TestBatchQueuedDuringScanWaitsForItsScan checks that a batch that changes
// the CRDs and creates a folder while another scan runs is published once, at
// the end of the scan of that folder: the end of the scan in progress must not
// publish the state without it. Before the fix the scan in progress published
// it, when its result arrived after the batch (the flake of
// TestWatchedBatchWithFolderPublishesOnce).
func TestBatchQueuedDuringScanWaitsForItsScan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	created := filepath.Join(dir, "new")
	if err := os.Mkdir(created, 0o700); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(dir, "old", "crd.yaml")
	ss := newSession(t)
	published := countPublishes(ss)
	ss.features = feature.Of(feature.CRDTypedPolicies)
	ss.folders = []string{filepath.Join(dir, "ws")}
	ss.crds.Set(ctx, filepath.ToSlash(oldFile), []byte(scanCRD))
	ss.docs[cacheURI] = &document{version: 1, text: []byte(widget(t)), crds: &ss.crds}
	release := make(chan struct{})
	ss.scanner = func(_ context.Context, folders []string, _ *check.CRDs) []message {
		if !slices.Contains(folders, created) {
			<-release // the workspace scan
		}
		return nil
	}
	ss.startScan(ctx)
	params, _ := json.Marshal(map[string]any{"changes": []map[string]any{
		{"uri": pathURI(oldFile), "type": fileDeleted},
		{"uri": pathURI(created), "type": fileCreated},
	}})
	if err := ss.didChangeWatchedFiles(ctx, params); err != nil {
		t.Fatal(err)
	}
	if err := ss.finishLensSearch(ctx, <-ss.lensSearch.done); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := ss.endScan(ctx, <-ss.scan.done); err != nil {
		t.Fatal(err)
	}
	if n := published.n(); n != 0 {
		t.Fatalf("the end of the workspace scan published %d times, want none before the scan of the created folder", n)
	}
	if ss.scan == nil {
		t.Fatal("the created folder is not scanned after the workspace scan")
	}
	if err := ss.endScan(ctx, <-ss.scan.done); err != nil {
		t.Fatal(err)
	}
	if n := published.n(); n != 1 {
		t.Errorf("publishes after the scan of the created folder = %d, want 1", n)
	}
}

// The CRD and .cellens of the batch tests: spec.config, an
// x-kubernetes-int-or-string value, typed int, which a rule then misuses.
const (
	batchCRD  = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: widgets.example.com}\nspec:\n  group: example.com\n  scope: Namespaced\n  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}\n  versions:\n    - name: v1\n      served: true\n      storage: true\n      schema:\n        openAPIV3Schema:\n          type: object\n          x-kubernetes-validations:\n            - rule: \"self.spec.config.startsWith('a')\"\n          properties:\n            spec:\n              type: object\n              properties:\n                config: {x-kubernetes-int-or-string: true}\n"
	batchLens = "types:\n  - file: crd.yaml\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    path: spec.config\n    type: int\n"
)

// TestBatchWithLensAndScanPublishesOnce checks that a batch that creates a
// folder holding a .cellens publishes what its search found once, when the
// last of that search and the batch's CRD scan ends: the search's change waits
// for the scan, which publishes it with what it found itself; a scan that
// finds nothing leaves the check to the search.
func TestBatchWithLensAndScanPublishesOnce(t *testing.T) {
	t.Parallel()
	for _, lensFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("search ends first=%t", lensFirst), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			created := filepath.Join(t.TempDir(), "new")
			if err := os.Mkdir(created, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(created, ".cellens"), []byte(batchLens), 0o600); err != nil {
				t.Fatal(err)
			}
			ss := newSession(t)
			published := countPublishes(ss)
			ss.features = feature.Of(feature.CRDTypedPolicies)
			docURI := pathURI(filepath.Join(created, "crd.yaml"))
			ss.docs[docURI] = &document{version: 1, text: []byte(batchCRD), crds: &ss.crds}
			release := make(chan struct{})
			ss.scanner = func(ctx context.Context, _ []string, dst *check.CRDs) []message {
				<-release
				if lensFirst {
					dst.Set(ctx, filepath.ToSlash(filepath.Join(created, "other.yaml")), []byte(scanCRD))
				}
				return nil
			}
			params, _ := json.Marshal(map[string]any{"changes": []map[string]any{{"uri": pathURI(created), "type": fileCreated}}})
			if err := ss.didChangeWatchedFiles(ctx, params); err != nil {
				t.Fatal(err)
			}
			endSearch := func() {
				if err := ss.finishLensSearch(ctx, <-ss.lensSearch.done); err != nil {
					t.Fatal(err)
				}
			}
			endScan := func() {
				close(release)
				if err := ss.endScan(ctx, <-ss.scan.done); err != nil {
					t.Fatal(err)
				}
			}
			if lensFirst {
				endSearch()
			} else {
				endScan()
			}
			if n := published.n(); n != 0 {
				t.Fatalf("publishes after the first end = %d, want none before the last", n)
			}
			if lensFirst {
				endScan()
			} else {
				endSearch()
			}
			if n := published.n(); n != 1 || !strings.Contains(published.out.String(), "startsWith") {
				t.Errorf("publishes after both ended = %d (%s), want 1 with the .cellens warning", n, published.out)
			}
		})
	}
}

// TestScanPublishesWhileAnotherBatchSearches is the case of the #2 review: a
// folder created while the scan of another one runs is searched for .cellens
// files slowly. The scan of the first folder publishes what it found when it
// ends, without waiting for the other batch's search; that search's end, with
// its batch's scan still to come, publishes nothing; that scan, finding
// nothing for a batch that owes nothing, neither. Before the fix the first
// scan's check waited for the search, then for the second scan.
func TestScanPublishesWhileAnotherBatchSearches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, d := range []string{first, second} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ss := newSession(t)
	published := countPublishes(ss)
	ss.features = feature.Of(feature.CRDTypedPolicies)
	ss.docs[cacheURI] = &document{version: 1, text: []byte(widget(t)), crds: &ss.crds}
	scanFirst, searchSecond := make(chan struct{}), make(chan struct{})
	ss.scanner = func(ctx context.Context, folders []string, dst *check.CRDs) []message {
		if slices.Contains(folders, first) {
			<-scanFirst
			dst.Set(ctx, filepath.ToSlash(filepath.Join(first, "crd.yaml")), []byte(scanCRD))
		}
		return nil
	}
	ss.fsys = func(dir string) fs.FS {
		if dir == second {
			<-searchSecond // a slow search
		}
		return os.DirFS(dir)
	}
	create := func(dir string) {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"changes": []map[string]any{{"uri": pathURI(dir), "type": fileCreated}}})
		if err := ss.didChangeWatchedFiles(ctx, params); err != nil {
			t.Fatal(err)
		}
	}
	create(first)
	if err := ss.finishLensSearch(ctx, <-ss.lensSearch.done); err != nil {
		t.Fatal(err)
	}
	create(second) // its scan waits for that of first; its search is slow
	close(scanFirst)
	if err := ss.endScan(ctx, <-ss.scan.done); err != nil {
		t.Fatal(err)
	}
	if n := published.n(); n != 1 {
		t.Fatalf("publishes when the scan of the first folder ended = %d, want 1: what it found", n)
	}
	close(searchSecond)
	if err := ss.finishLensSearch(ctx, <-ss.lensSearch.done); err != nil {
		t.Fatal(err)
	}
	if err := ss.endScan(ctx, <-ss.scan.done); err != nil {
		t.Fatal(err)
	}
	if n := published.n(); n != 1 || ss.scan != nil || ss.lensSearch != nil {
		t.Errorf("publishes after all ended = %d (scan %v, search %v), want still 1 and nothing left", n, ss.scan, ss.lensSearch)
	}
}

// TestFilesAreReadSafely checks the #2 review's reads: a .cellens or YAML file
// that is a symbolic link out of its folder, or to a device such as
// /dev/zero, is not read, and neither is more than the size limit, whether
// the workspace search, the CRD scan or a watched-file event reads it. Before
// the fix a link to a 4 MiB file outside was read whole, and one to /dev/zero
// filled the memory.
func TestFilesAreReadSafely(t *testing.T) {
	t.Parallel()
	outside := t.TempDir()
	big := filepath.Join(outside, "big")
	if err := os.WriteFile(big, []byte("types:\n#"+strings.Repeat("x", 2*maxWorkspaceFileSize)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, target string
	}{
		{"link out of the folder", big},
		{"link to a device", "/dev/zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := os.Stat(tc.target); err != nil {
				t.Skipf("%s: %v", tc.target, err)
			}
			ws := t.TempDir()
			lens, crd := filepath.Join(ws, "a", ".cellens"), filepath.Join(ws, "a", "crd.yaml")
			if err := os.Mkdir(filepath.Dir(lens), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, link := range []string{lens, crd} {
				if err := os.Symlink(tc.target, link); err != nil {
					t.Skipf("symbolic links: %v", err)
				}
			}
			if err := os.WriteFile(filepath.Join(ws, ".cellens"), []byte("types:\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			found := findLenses(context.Background(), os.DirFS, []string{ws})
			if keys := slices.Sorted(maps.Keys(found.files)); !slices.Equal(keys, []string{crdKey(pathURI(filepath.Join(ws, ".cellens")))}) {
				t.Errorf("findLenses found %q, want only the regular .cellens", keys)
			}
			ss := newSession(t)
			ss.folders = []string{ws}
			if ss.readLens(t.Context(), pathURI(lens)) || len(ss.lenses) != 0 {
				t.Errorf("readLens(%s) recorded it, want it refused", lens)
			}
			if _, err := ss.readFile(crd); err == nil {
				t.Errorf("readFile(%s) = nil error, want it refused", crd)
			}
			var crds check.CRDs
			scanWorkspace(context.Background(), os.DirFS, []string{ws}, maxWorkspaceFiles, &crds)
			if ss.readFromDisk(context.Background(), pathURI(crd)) {
				t.Errorf("readFromDisk(%s) changed the CRDs, want the file refused", crd)
			}
		})
	}
	t.Run("over the limit", func(t *testing.T) {
		t.Parallel()
		ss := newSession(t)
		if _, err := ss.readFile(big); !errors.Is(err, errTooBig) {
			t.Errorf("readFile(big) error = %v, want errTooBig", err)
		}
	})
}

// BenchmarkLensKeystroke measures a didChange of a document that a .cellens
// names, when that .cellens names targets files in all: each keystroke checks
// the document and the .cellens entries for it, not those for the other files.
func BenchmarkLensKeystroke(b *testing.B) {
	for _, targets := range []int{1, 10, 50} {
		b.Run(fmt.Sprintf("targets=%d", targets), func(b *testing.B) {
			dir := b.TempDir()
			var lens strings.Builder
			lens.WriteString("types:\n")
			for i := range targets {
				name := fmt.Sprintf("crd%d.yaml", i)
				if err := os.WriteFile(filepath.Join(dir, name), []byte(batchCRD), 0o600); err != nil {
					b.Fatal(err)
				}
				fmt.Fprintf(&lens, "  - file: %s\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    path: spec.config\n    type: int\n", name)
			}
			ctx := context.Background()
			a, err := assist.New(kubeversion.Default())
			if err != nil {
				b.Fatal(err)
			}
			ss := &session{conn: jsonrpc.NewConn(strings.NewReader(""), io.Discard), assist: a, kube: kubeversion.Default(), docs: map[string]*document{}, folders: []string{dir}}
			ss.putLens(ctx, pathURI(filepath.Join(dir, ".cellens")), []byte(lens.String()), true)
			doc := pathURI(filepath.Join(dir, "crd0.yaml"))
			open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": doc, "version": 1, "text": batchCRD}})
			if err := ss.didOpen(ctx, open); err != nil {
				b.Fatal(err)
			}
			version := 1
			for b.Loop() {
				version++
				params, _ := json.Marshal(map[string]any{
					"textDocument":   map[string]any{"uri": doc, "version": version},
					"contentChanges": []map[string]any{{"text": batchCRD + "# " + strconv.Itoa(version) + "\n"}},
				})
				if err := ss.didChange(ctx, params); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestBatchScanFindsBeforeSearchEnds is the order the load run of
// TestWatchedBatchWithFolderPublishesOnce met: a batch that forgets a CRD and
// creates a folder whose scan finds it again, and whose search for .cellens
// files ends after that scan. The scan publishes what it found, and with it
// what the batch changed, so the search, which finds nothing, publishes
// nothing more.
func TestBatchScanFindsBeforeSearchEnds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	created := filepath.Join(dir, "new")
	if err := os.Mkdir(created, 0o700); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(dir, "old", "crd.yaml")
	ss := newSession(t)
	published := countPublishes(ss)
	ss.features = feature.Of(feature.CRDTypedPolicies)
	ss.crds.Set(ctx, filepath.ToSlash(oldFile), []byte(scanCRD))
	ss.docs[cacheURI] = &document{version: 1, text: []byte(widget(t)), crds: &ss.crds}
	ss.scanner = func(ctx context.Context, _ []string, dst *check.CRDs) []message {
		dst.Set(ctx, filepath.ToSlash(filepath.Join(created, "crd.yaml")), []byte(scanCRD))
		return nil
	}
	release := make(chan struct{})
	ss.fsys = func(dir string) fs.FS {
		<-release // the search ends after the scan
		return os.DirFS(dir)
	}
	params, _ := json.Marshal(map[string]any{"changes": []map[string]any{
		{"uri": pathURI(oldFile), "type": fileDeleted},
		{"uri": pathURI(created), "type": fileCreated},
	}})
	if err := ss.didChangeWatchedFiles(ctx, params); err != nil {
		t.Fatal(err)
	}
	if err := ss.endScan(ctx, <-ss.scan.done); err != nil {
		t.Fatal(err)
	}
	if n := published.n(); n != 1 {
		t.Fatalf("publishes when the scan ended = %d, want 1: what it found", n)
	}
	close(release)
	if err := ss.finishLensSearch(ctx, <-ss.lensSearch.done); err != nil {
		t.Fatal(err)
	}
	if n := published.n(); n != 1 {
		t.Errorf("publishes after the search ended = %d, want still 1: the scan's publish had the batch's change", n)
	}
}

// manyFieldsCRD is a CustomResourceDefinition whose spec has n
// x-kubernetes-int-or-string fields f0, f1, ..., each read by a rule.
func manyFieldsCRD(n int) string {
	var b strings.Builder
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: widgets.example.com}\nspec:\n  group: example.com\n  scope: Namespaced\n  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}\n  versions:\n    - name: v1\n      served: true\n      storage: true\n      schema:\n        openAPIV3Schema:\n          type: object\n          x-kubernetes-validations:\n")
	for i := range n {
		fmt.Fprintf(&b, "            - rule: \"self.spec.f%d == 1\"\n", i)
	}
	b.WriteString("          properties:\n            spec:\n              type: object\n              properties:\n")
	for i := range n {
		fmt.Fprintf(&b, "                f%d: {x-kubernetes-int-or-string: true}\n", i)
	}
	return b.String()
}

// BenchmarkCellensKeystroke measures a didChange of an open .cellens of 200
// entries, for 20 files of 10 values each, three of them open (#16): a
// keystroke in the type of one entry, one that leaves that entry a line YAML
// cannot read, and a new line above every entry, which moves them all and so
// the lines the documents' warnings name.
func BenchmarkCellensKeystroke(b *testing.B) {
	const files, fields = 20, 10
	dir := b.TempDir()
	crd := manyFieldsCRD(fields)
	var lens strings.Builder
	lens.WriteString("types:\n")
	for i := range files {
		name := fmt.Sprintf("crd%02d.yaml", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(crd), 0o600); err != nil {
			b.Fatal(err)
		}
		for j := range fields {
			fmt.Fprintf(&lens, "  - file: %s\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    path: spec.f%d\n    type: int\n", name, j)
		}
	}
	text := lens.String()
	edits := map[string][2]string{
		"type":     {text, strings.Replace(text, "type: int", "type: string", 1)},
		"broken":   {text, strings.Replace(text, "type: int", `type: "{in`, 1)},
		"new line": {text, strings.Replace(text, "types:\n", "types:\n\n", 1)},
	}
	for _, name := range []string{"type", "broken", "new line"} {
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			a, err := assist.New(kubeversion.Default())
			if err != nil {
				b.Fatal(err)
			}
			ss := &session{conn: jsonrpc.NewConn(strings.NewReader(""), io.Discard), assist: a, kube: kubeversion.Default(), docs: map[string]*document{}, folders: []string{dir}}
			lensURI := pathURI(filepath.Join(dir, ".cellens"))
			for i := range 3 {
				open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": pathURI(filepath.Join(dir, fmt.Sprintf("crd%02d.yaml", i))), "version": 1, "text": crd}})
				if err := ss.didOpen(ctx, open); err != nil {
					b.Fatal(err)
				}
			}
			open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": lensURI, "version": 1, "text": text}})
			if err := ss.didOpen(ctx, open); err != nil {
				b.Fatal(err)
			}
			version := 1
			for b.Loop() {
				version++
				params, _ := json.Marshal(map[string]any{
					"textDocument":   map[string]any{"uri": lensURI, "version": version},
					"contentChanges": []map[string]any{{"text": edits[name][version%2]}},
				})
				if err := ss.didChange(ctx, params); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
