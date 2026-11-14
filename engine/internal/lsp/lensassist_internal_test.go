package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/goschema"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// goEntry is a .cellens whose last line is a go: being written.
const goEntry = "types:\n  - file: policy.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    go: "

// completeGo asks ss for the completion at the end of goEntry in the
// .cellens at uri, and returns the labels and whether the list is
// incomplete.
func completeGo(tb testing.TB, ss *session, uri string) (labels []string, incomplete bool) {
	tb.Helper()
	res, err := ss.lensCompletion(context.Background(), uri, position{Line: 4, Character: len("    go: ")}, "")
	if err != nil {
		tb.Fatal(err)
	}
	items, _ := res.([]map[string]any)
	if list, ok := res.(map[string]any); ok {
		incomplete, _ = list["isIncomplete"].(bool)
		items, _ = list["items"].([]map[string]any)
	}
	for _, it := range items {
		label, _ := it["label"].(string)
		labels = append(labels, label)
	}
	return labels, incomplete
}

// TestGoCompletionFromListing checks that the completion of a go: answers at
// once from the last listing of the workspace's Go types: without one it
// starts one in the background, once, and says the list is incomplete; with
// one it answers from it without listing again; after a .go file changed it
// answers from the listing it has, incomplete, and lists again. Before the
// review of #16 every completion of a go: listed the workspace's Go files on
// the message loop.
func TestGoCompletionFromListing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ss := newSession(t)
	ss.folders = []string{dir}
	uri := pathURI(filepath.Join(dir, ".cellens"))
	ss.putLens(t.Context(), uri, []byte(goEntry), true)
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	ss.goLister = func(context.Context, func(string) fs.FS, []string, map[string]goFile) goListed {
		started <- struct{}{}
		<-release
		return goListed{files: map[string]goFile{
			filepath.Join(dir, "api", "v1", "quota.go"): {folder: pathURI(filepath.Join(dir, "api", "v1")), structs: []goschema.Struct{{Name: "QuotaSpec"}}},
		}}
	}
	none := func(when string) {
		t.Helper()
		select {
		case <-started:
			t.Errorf("%s: a listing started, want none", when)
		default:
		}
	}
	finish := func() {
		t.Helper()
		if err := ss.finishJob(<-ss.jobDone); err != nil {
			t.Fatal(err)
		}
	}
	if labels, incomplete := completeGo(t, ss, uri); len(labels) != 0 || !incomplete {
		t.Fatalf("completion before a listing = %q, incomplete %t, want none, incomplete", labels, incomplete)
	}
	<-started
	if _, incomplete := completeGo(t, ss, uri); !incomplete {
		t.Error("completion while the listing runs is complete, want incomplete")
	}
	none("a second completion while the listing runs")
	close(release)
	finish()
	if labels, incomplete := completeGo(t, ss, uri); !slices.Equal(labels, []string{"./api/v1"}) || incomplete {
		t.Errorf("completion after the listing = %q, incomplete %t, want ./api/v1, complete", labels, incomplete)
	}
	none("a completion with a listing")
	params, _ := json.Marshal(map[string]any{"changes": []map[string]any{{"uri": pathURI(filepath.Join(dir, "api", "v1", "quota.go")), "type": fileChanged}}})
	if err := ss.didChangeWatchedFiles(t.Context(), params); err != nil {
		t.Fatal(err)
	}
	if labels, incomplete := completeGo(t, ss, uri); !slices.Equal(labels, []string{"./api/v1"}) || !incomplete {
		t.Errorf("completion after a .go file changed = %q, incomplete %t, want ./api/v1, incomplete", labels, incomplete)
	}
	<-started
	finish()
	if _, incomplete := completeGo(t, ss, uri); incomplete {
		t.Error("completion after the second listing is incomplete, want complete")
	}
}

// BenchmarkGoCompletion measures, in a workspace of 2,000 Go files in 200
// packages, the completion of a go: once the Go types are listed, which is
// all the message loop does, and the listing that runs in the background:
// the first, and one that finds every file as it was.
func BenchmarkGoCompletion(b *testing.B) {
	dir := b.TempDir()
	for p := range 200 {
		pkg := filepath.Join(dir, "api", fmt.Sprintf("p%03d", p))
		if err := os.MkdirAll(pkg, 0o750); err != nil {
			b.Fatal(err)
		}
		for f := range 10 {
			src := fmt.Sprintf("package p%03d\n\n// T%d is a type.\ntype T%d struct {\n\tA int `json:\"a\"`\n\tB string `json:\"b,omitempty\"`\n}\n", p, f, f)
			if err := os.WriteFile(filepath.Join(pkg, fmt.Sprintf("t%d.go", f)), []byte(src), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	ss := &session{docs: map[string]*document{}, folders: []string{dir}, features: feature.Of(feature.CellensTypes)}
	b.Run("completion", func(b *testing.B) {
		uri := pathURI(filepath.Join(dir, ".cellens"))
		ss.putLens(context.Background(), uri, []byte(goEntry), true)
		ss.goFiles = goChoices(context.Background(), os.DirFS, ss.folders, nil).files
		for b.Loop() {
			if labels, incomplete := completeGo(b, ss, uri); len(labels) != 200 || incomplete {
				b.Fatalf("completion = %d folders, incomplete %t, want 200, complete", len(labels), incomplete)
			}
		}
	})
	b.Run("first listing", func(b *testing.B) {
		for b.Loop() {
			goChoices(context.Background(), os.DirFS, ss.folders, nil)
		}
	})
	b.Run("listing again", func(b *testing.B) {
		known := goChoices(context.Background(), os.DirFS, ss.folders, nil).files
		for b.Loop() {
			goChoices(context.Background(), os.DirFS, ss.folders, known)
		}
	})
}
