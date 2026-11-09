package lsp

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/goschema"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

func writeGo(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func choiceNames(l goListed) []string {
	var out []string
	for _, c := range l.types {
		out = append(out, c.Name)
	}
	return out
}

// TestGoChoicesReuse checks that a listing of Go types reuses what the last
// one read of a file whose size and time are the same, and reads one that
// changed again (the review of 6f82d2a).
func TestGoChoicesReuse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "api", "a.go")
	writeGo(t, p, "package api\n\ntype A struct {\n\tX int `json:\"x\"`\n}\n")
	first := goChoices(t.Context(), os.DirFS, []string{dir}, nil)
	if got := choiceNames(first); !slices.Equal(got, []string{"A"}) {
		t.Fatalf("goChoices = %v, want A", got)
	}
	// What the cache holds is what the next listing gives, unread.
	f := first.files[p]
	f.structs = []goschema.Struct{{Name: "Cached"}}
	first.files[p] = f
	if got := choiceNames(goChoices(t.Context(), os.DirFS, []string{dir}, first.files)); !slices.Equal(got, []string{"Cached"}) {
		t.Errorf("goChoices with the file unchanged = %v, want the cached Cached", got)
	}
	writeGo(t, p, "package api\n\ntype B struct {\n\tX int `json:\"x\"`\n\tY int `json:\"y\"`\n}\n")
	if got := choiceNames(goChoices(t.Context(), os.DirFS, []string{dir}, first.files)); !slices.Equal(got, []string{"B"}) {
		t.Errorf("goChoices with the file changed = %v, want B, read again", got)
	}
}

// TestGoChoicesCap checks that typeChoices lists at most maxGoChoices Go
// types, the first by folder and name, and says there are more.
func TestGoChoicesCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package api\n")
	for i := range maxGoChoices + 1 {
		fmt.Fprintf(&b, "type T%04d struct {\n\tX int `json:\"x\"`\n}\n", i)
	}
	writeGo(t, filepath.Join(dir, "api", "a.go"), b.String())
	got := goChoices(t.Context(), os.DirFS, []string{dir}, nil)
	if len(got.types) != maxGoChoices || !got.more || got.types[0].Name != "T0000" {
		t.Errorf("goChoices = %d types, more %t, want %d from T0000 and more", len(got.types), got.more, maxGoChoices)
	}
}

// TestGoReadsPruned checks that the session forgets a Go type read for a
// go: entry once no entry names it (the review of 6f82d2a).
func TestGoReadsPruned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGo(t, filepath.Join(dir, "api", "a.go"), "package api\n\ntype A struct {\n\tX int `json:\"x\"`\n}\n")
	ss := &session{conn: jsonrpc.NewConn(strings.NewReader(""), io.Discard), docs: map[string]*document{}, folders: []string{dir}}
	lens := pathURI(filepath.Join(dir, cellens.FileName))
	ss.putLens(t.Context(), lens, []byte("types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    go: ./api#A\n"), true)
	g := ss.lenses[crdKey(lens)].entries[0].Go
	if typ, msg := ss.goType(t.Context(), crdKey(lens), g); typ == nil || msg != "" || len(ss.goReads) != 1 {
		t.Fatalf("goType = %v, %q, %d kept, want A, kept", typ, msg, len(ss.goReads))
	}
	ss.putLens(t.Context(), lens, []byte("types:\n  - file: p.yaml\n    document: ValidatingAdmissionPolicy p\n    variable: params\n    go: ./api#A\n    # still named\n"), true)
	if len(ss.goReads) != 1 {
		t.Errorf("goReads = %v after an edit that keeps the entry, want A kept", ss.goReads)
	}
	ss.putLens(t.Context(), lens, []byte("types:\n"), true)
	if len(ss.goReads) != 0 {
		t.Errorf("goReads = %v after the entry went, want none", ss.goReads)
	}
}

// gatedFS is a folder whose listing waits until release is closed.
type gatedFS struct {
	fs.FS
	release <-chan struct{}
}

func (g gatedFS) Open(name string) (fs.File, error) {
	if name == "." {
		<-g.release
	}
	return g.FS.Open(name)
}

// TestTypeChoicesInBackground checks that typeChoices lists the Go types off
// the message loop: a request sent after it is answered while the listing
// waits, and typeChoices is answered once it ends (the review of 6f82d2a).
func TestTypeChoicesInBackground(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGo(t, filepath.Join(dir, "api", "a.go"), "package api\n\ntype A struct {\n\tX int `json:\"x\"`\n}\n")
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	t.Cleanup(open) // a failure before the release still ends the listing
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ss := newSession(t)
	ss.conn = jsonrpc.NewConn(serverIn, serverOut)
	ss.fsys = func(d string) fs.FS { return gatedFS{os.DirFS(d), release} }
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = ss.serve(ctx, serverIn) }()
	t.Cleanup(func() { _ = clientOut.Close(); _ = serverOut.Close() })

	c := jsonrpc.NewConn(clientIn, clientOut)
	if err := c.Request("1", "initialize", map[string]any{"workspaceFolders": []map[string]any{{"uri": pathURI(dir)}}}); err != nil {
		t.Fatal(err)
	}
	readMessage(t, c) // the initialize result
	go func() {
		_ = c.Request("2", "workspace/executeCommand", map[string]any{"command": commandTypeChoices})
		_ = c.Request("3", "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": pathURI(filepath.Join(dir, "x.yaml"))}, "position": map[string]any{}})
	}()
	for {
		m := readMessage(t, c)
		if string(m.ID) == `"2"` {
			t.Fatal("typeChoices answered while the listing waits")
		}
		if string(m.ID) == `"3"` {
			break
		}
	}
	open()
	for {
		m := readMessage(t, c)
		if string(m.ID) != `"2"` {
			continue
		}
		if !strings.Contains(string(m.Result), `"goTypes":[{"folder":"`+pathURI(filepath.Join(dir, "api"))+`","name":"A","doc":""}]`) {
			t.Errorf("typeChoices = %s, want the Go type A", m.Result)
		}
		return
	}
}

// listingSession serves a session whose listing of Go types is list, and
// returns the client's end of its connection, initialized.
func listingSession(t *testing.T, list func(ctx context.Context, dirFS func(string) fs.FS, folders []string, known map[string]goFile) goListed) *jsonrpc.Conn {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ss := newSession(t)
	ss.conn = jsonrpc.NewConn(serverIn, serverOut)
	ss.goLister = list
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = ss.serve(ctx, serverIn)
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = serverOut.Close()
		<-served
	})
	c := jsonrpc.NewConn(clientIn, clientOut)
	go func() { _ = c.Request("1", "initialize", map[string]any{}) }()
	readUntil(t, c, func(m jsonrpc.Message) bool { return string(m.ID) == `"1"` })
	return c
}

// TestTypeChoicesReleasesListing checks that the context of a listing of Go
// types is released once it ends, not kept until the session ends (the
// review of 12c9469).
func TestTypeChoicesReleasesListing(t *testing.T) {
	t.Parallel()
	listed := make(chan context.Context, 1)
	c := listingSession(t, func(ctx context.Context, _ func(string) fs.FS, _ []string, _ map[string]goFile) goListed {
		listed <- ctx
		return goListed{types: []goChoice{}}
	})
	go func() { _ = c.Request("2", "workspace/executeCommand", map[string]any{"command": commandTypeChoices}) }()
	readUntil(t, c, func(m jsonrpc.Message) bool { return string(m.ID) == `"2"` })
	if ctx := <-listed; ctx.Err() == nil {
		t.Error("the listing's context is live after typeChoices was answered, want it released")
	}
}

// TestTypeChoicesCancelled checks that $/cancelRequest answers a typeChoices
// that waits for the listing of Go types with RequestCancelled at once.
func TestTypeChoicesCancelled(t *testing.T) {
	t.Parallel()
	c := listingSession(t, func(ctx context.Context, _ func(string) fs.FS, _ []string, _ map[string]goFile) goListed {
		<-ctx.Done()
		return goListed{}
	})
	go func() {
		_ = c.Request("2", "workspace/executeCommand", map[string]any{"command": commandTypeChoices})
		_ = c.Notify("$/cancelRequest", map[string]any{"id": "2"})
	}()
	for {
		m := readMessage(t, c)
		if string(m.ID) != `"2"` {
			continue
		}
		if m.Error == nil || m.Error.Code != jsonrpc.CodeRequestCancelled {
			t.Errorf("typeChoices after $/cancelRequest = %s, %+v, want RequestCancelled", m.Result, m.Error)
		}
		return
	}
}

// TestTypeChoicesAtShutdown checks that shutdown stops the listing of Go
// types and answers the typeChoices waiting for it with an error.
func TestTypeChoicesAtShutdown(t *testing.T) {
	t.Parallel()
	c := listingSession(t, func(ctx context.Context, _ func(string) fs.FS, _ []string, _ map[string]goFile) goListed {
		<-ctx.Done()
		return goListed{}
	})
	go func() {
		_ = c.Request("2", "workspace/executeCommand", map[string]any{"command": commandTypeChoices})
		_ = c.Request("3", "shutdown", nil)
	}()
	var choices, shutdown *jsonrpc.Message
	for choices == nil || shutdown == nil {
		m := readMessage(t, c)
		switch string(m.ID) {
		case `"2"`:
			choices = &m
		case `"3"`:
			shutdown = &m
		}
	}
	if choices.Error == nil || choices.Error.Code != jsonrpc.CodeRequestFailed || !strings.Contains(choices.Error.Message, "shut down") {
		t.Errorf("typeChoices at shutdown = %s, %+v, want RequestFailed saying the server shut down", choices.Result, choices.Error)
	}
	if shutdown.Error != nil {
		t.Errorf("shutdown error = %+v, want none", shutdown.Error)
	}
}
