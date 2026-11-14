package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// TestScanWorkspaceNotes checks what a scan tells the user about the caps it
// hit, and that it tells nothing when it hit none.
func TestScanWorkspaceNotes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]int // file name to size in bytes
		limit int
		want  []string // substrings of the notes, in order
		log   []bool
	}{
		{"no cap hit", map[string]int{"a.yaml": 10, "b.yml": 10}, 2, nil, nil},
		{"more files than the cap", map[string]int{"a.yaml": 10, "b.yaml": 10, "c.yaml": 10}, 2, []string{"after 2 YAML files"}, []bool{false}},
		{"file over the size limit", map[string]int{"big.yaml": maxWorkspaceFileSize + 1}, 5, []string{"big.yaml", "1048576 bytes"}, []bool{true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, size := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("#", size)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var dst check.CRDs
			notes := scanWorkspace(context.Background(), os.DirFS, []string{dir}, tc.limit, &dst)
			if len(tc.want) == 0 {
				if len(notes) != 0 {
					t.Fatalf("scanWorkspace notes = %v, want none", notes)
				}
				return
			}
			if len(notes) != 1 {
				t.Fatalf("scanWorkspace notes = %v, want one", notes)
			}
			for _, w := range tc.want {
				if !strings.Contains(notes[0].text, w) {
					t.Errorf("note %q lacks %q", notes[0].text, w)
				}
			}
			if notes[0].log != tc.log[0] {
				t.Errorf("note log = %v, want %v", notes[0].log, tc.log[0])
			}
		})
	}
}

// TestStartScanDeliversNotes checks that a scan hands its notes to the session
// with its result.
func TestStartScanDeliversNotes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, n := range []string{"a.yaml", "b.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("a: 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ss := newSession(t)
	ss.folders = []string{dir}
	ss.fileCap = 1
	ss.startScan(context.Background())
	<-ss.scan.done
	if n := ss.scan.notes; len(n) != 1 || !strings.Contains(n[0].text, "after 1 YAML files") {
		t.Errorf("scan notes = %v, want the cap warning", n)
	}
	ss.scan = nil
}

// TestScanNotesReachClient checks, through the session's message loop, that
// the client gets one showMessage for the file cap and one logMessage for a
// file over the size limit, and no message when the scan hits no cap.
func TestScanNotesReachClient(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		cap      int
		big      bool
		wantShow []string
		wantLog  []string
	}{
		{"caps hit", 1, true, []string{"after 1 YAML files"}, []string{"0big.yaml"}},
		{"no cap hit", 5, false, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			files := map[string]int{"a.yaml": 10, "b.yaml": 10}
			if tc.big {
				files["0big.yaml"] = maxWorkspaceFileSize + 1
			}
			for name, size := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("#", size)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			serverIn, clientOut := io.Pipe()
			clientIn, serverOut := io.Pipe()
			ss := newSession(t)
			ss.conn = jsonrpc.NewConn(serverIn, serverOut)
			release := make(chan struct{})
			ss.scanner = func(ctx context.Context, folders []string, dst *check.CRDs) []message {
				<-release
				dst.Set(ctx, "/ws/crd.yaml", []byte(scanCRD)) // a found CRD makes the session publish again
				return scanWorkspace(ctx, os.DirFS, folders, tc.cap, dst)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _ = ss.serve(ctx, serverIn) }()
			t.Cleanup(func() { _ = clientOut.Close(); _ = serverOut.Close() })

			c := jsonrpc.NewConn(clientIn, clientOut)
			if err := c.Request("1", "initialize", map[string]any{
				"initializationOptions": map[string]any{"experimental": []string{"crd-typed-policies", "cellens-types"}},
				"workspaceFolders":      []map[string]any{{"uri": pathURI(dir)}},
			}); err != nil {
				t.Fatal(err)
			}
			readMessage(t, c) // the initialize result
			if err := c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": cacheURI, "version": 1, "text": widget(t)}}); err != nil {
				t.Fatal(err)
			}
			// Once the open document is handled, the scan ends. Its messages
			// come first; a scan that hit no cap then publishes the document.
			if err := c.Request("2", "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": cacheURI}, "position": map[string]any{}}); err != nil {
				t.Fatal(err)
			}
			var show, logs []string
			for done := false; !done; {
				done = string(readMessage(t, c).ID) == `"2"`
			}
			close(release)
			for done := false; !done; {
				m := readMessage(t, c)
				var p struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(m.Params, &p)
				switch m.Method {
				case "textDocument/publishDiagnostics":
					done = true
				case "window/showMessage":
					show = append(show, p.Message)
					done = true // a scan that hit the file cap publishes nothing
				case "window/logMessage":
					logs = append(logs, p.Message)
				}
			}
			expect := func(kind string, got, want []string) {
				if len(got) != len(want) {
					t.Fatalf("%s messages = %q, want %d", kind, got, len(want))
				}
				for i, w := range want {
					if !strings.Contains(got[i], w) {
						t.Errorf("%s message %q lacks %q", kind, got[i], w)
					}
				}
			}
			expect("showMessage", show, tc.wantShow)
			expect("logMessage", logs, tc.wantLog)
		})
	}
}

// TestScanWorkspaceOversizedSummary checks that many oversized files make one
// summary log line, and that a rescan sends one line again.
func TestScanWorkspaceOversizedSummary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i := range 50 {
		name := filepath.Join(dir, fmt.Sprintf("big%02d.yaml", i))
		if err := os.WriteFile(name, []byte(strings.Repeat("#", maxWorkspaceFileSize+1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		var dst check.CRDs
		notes := scanWorkspace(context.Background(), os.DirFS, []string{dir}, 100, &dst)
		if len(notes) != 1 {
			t.Fatalf("scanWorkspace notes = %d, want 1 for 50 files", len(notes))
		}
		for _, w := range []string{"50 files", "1048576 bytes", "big00.yaml", "big02.yaml", "and 47 more"} {
			if !strings.Contains(notes[0].text, w) {
				t.Errorf("note %q lacks %q", notes[0].text, w)
			}
		}
		if strings.Contains(notes[0].text, "big03.yaml") {
			t.Errorf("note %q names more than three files", notes[0].text)
		}
	}
}
