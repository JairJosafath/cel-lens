package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

func widget(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/widget.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const cacheURI = "file:///cache.yaml"

func newSession(t *testing.T) *session {
	t.Helper()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	return &session{
		conn:   jsonrpc.NewConn(strings.NewReader(""), io.Discard),
		assist: a,
		kube:   kubeversion.Default(),
		docs:   map[string]*document{},
	}
}

func change(t *testing.T, ss *session, version int, text string) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": cacheURI, "version": version},
		"contentChanges": []map[string]any{{"text": text}},
	})
	if err := ss.didChange(context.Background(), params); err != nil {
		t.Fatal(err)
	}
}

// TestRequestsReuseExtraction checks that hover and completion on an unchanged
// document share one extraction, and that a didChange makes a new one.
func TestRequestsReuseExtraction(t *testing.T) {
	t.Parallel()
	text := widget(t)
	ss := newSession(t)
	open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": cacheURI, "version": 1, "text": text}})
	if err := ss.didOpen(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	line := 0
	for i, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "rule:") {
			line = i
			break
		}
	}
	at := map[string]any{"textDocument": map[string]any{"uri": cacheURI}, "position": map[string]any{"line": line, "character": strings.Index(strings.Split(text, "\n")[line], "self") + 2}}
	params, _ := json.Marshal(at)

	if ss.docs[cacheURI].parsed != nil {
		t.Fatal("document parsed before any request, want it on demand")
	}
	if res, err := ss.hover(context.Background(), params); err != nil || res == nil {
		t.Fatalf("hover = %v, %v, want a result", res, err)
	}
	first := ss.docs[cacheURI].parsed
	if first == nil {
		t.Fatal("hover kept no extraction")
	}
	if _, err := ss.completion(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.hover(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if ss.docs[cacheURI].parsed != first {
		t.Error("completion or a second hover re-extracted an unchanged document")
	}

	change(t, ss, 2, text+"# edited\n")
	if ss.docs[cacheURI].parsed != nil {
		t.Error("didChange kept the extraction of the old text")
	}
	if _, err := ss.hover(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if ss.docs[cacheURI].parsed == first || ss.docs[cacheURI].parsed == nil {
		t.Error("hover after didChange did not extract the new text")
	}
}

// TestCancelledExtractionNotKept checks that a request cancelled before it
// extracted does not leave an empty extraction for the next one.
func TestCancelledExtractionNotKept(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	ss.docs[cacheURI] = &document{version: 1, text: []byte(widget(t))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ss.docs[cacheURI].parse(ctx, cacheURI)
	if ss.docs[cacheURI].parsed != nil {
		t.Error("an extraction under a cancelled context was kept")
	}
}

// codeActions asks ss for the quick fixes of the whole document at cacheURI.
func codeActions(t *testing.T, ss *session) []any {
	t.Helper()
	params, _ := json.Marshal(map[string]any{
		"textDocument": map[string]any{"uri": cacheURI},
		"range":        map[string]any{"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 10000, "character": 0}},
		"context":      map[string]any{},
	})
	res, err := ss.codeAction(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	actions, ok := res.([]any)
	if !ok {
		t.Fatalf("codeAction result = %T, want []any", res)
	}
	return actions
}

// TestCodeActionReusesDiagnostics checks that codeAction on an unchanged
// document version does not check it again, and that after a didChange the
// fixes are those of the new text.
func TestCodeActionReusesDiagnostics(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	checks := 0
	ss.checker = func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Diagnostic, error) {
		checks++
		return check.Check(ctx, file, data, opts...)
	}
	action := func() []any {
		t.Helper()
		return codeActions(t, ss)
	}
	text := widget(t)
	open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": cacheURI, "version": 1, "text": text}})
	if err := ss.didOpen(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	first := action()
	if len(first) == 0 {
		t.Fatal("codeAction on the widget offers no fix, want at least one")
	}
	action()
	if checks != 1 {
		t.Errorf("check.Check calls after open and 2 codeActions = %d, want 1", checks)
	}
	change(t, ss, 2, "apiVersion: v1\nkind: ConfigMap\n")
	if got := action(); len(got) != 0 || checks != 2 {
		t.Errorf("codeAction after didChange = %d actions, %d checks, want 0 actions, 2 checks (was %d actions)", len(got), checks, len(first))
	}

	// A different misspelling, two lines further down, gets its own fix.
	moved := "# one\n# two\n" + strings.Replace(text, "self.replcas", "self.nmae", 1)
	change(t, ss, 3, moved)
	got := action()
	if len(got) != 1 || checks != 3 {
		t.Fatalf("codeAction after didChange to a new typo = %d actions, %d checks, want 1 action, 3 checks", len(got), checks)
	}
	before, _, _ := strings.Cut(moved, "self.nmae")
	line := strings.Count(before, "\n")
	column := len(utf16.Encode([]rune(before[strings.LastIndex(before, "\n")+1:]))) + len("self.")
	checkFix(t, got[0], "Change to `name`", "name", line, column)
}

// checkFix checks that action is a quick fix titled title that replaces the
// text at line, starting at character, with name.
func checkFix(t *testing.T, action any, title, name string, line, character int) {
	t.Helper()
	b, _ := json.Marshal(action)
	var a struct {
		Title string `json:"title"`
		Edit  struct {
			Changes map[string][]struct {
				Range   lspRange `json:"range"`
				NewText string   `json:"newText"`
			} `json:"changes"`
		} `json:"edit"`
	}
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	edits := a.Edit.Changes[cacheURI]
	if a.Title != title || len(edits) != 1 || edits[0].NewText != name {
		t.Fatalf("codeAction = %s, want %q editing to %q", b, title, name)
	}
	if r := edits[0].Range; r.Start.Line != line || r.Start.Character != character {
		t.Errorf("fix range starts at %d:%d, want %d:%d in the new text", r.Start.Line, r.Start.Character, line, character)
	}
}

// TestCodeActionAfterCancelledPublish checks that a publish cancelled before
// its check completed keeps nothing, so the next codeAction checks the
// document again and returns its fixes.
func TestCodeActionAfterCancelledPublish(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	checks := 0
	ss.checker = func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Diagnostic, error) {
		checks++
		return check.Check(ctx, file, data, opts...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": cacheURI, "version": 1, "text": widget(t)}})
	if err := ss.didOpen(ctx, open); !errors.Is(err, context.Canceled) {
		t.Fatalf("didOpen under a cancelled context = %v, want context.Canceled", err)
	}
	if checks != 1 || ss.docs[cacheURI].checked {
		t.Fatalf("after the cancelled publish: %d checks, checked = %v, want 1 check and nothing kept", checks, ss.docs[cacheURI].checked)
	}
	if got := codeActions(t, ss); len(got) == 0 || checks != 2 {
		t.Errorf("codeAction after a cancelled publish = %d actions, %d checks, want fixes and 2 checks", len(got), checks)
	}
}

// TestScanDoesNotBlockSession checks that a request sent while the workspace
// scan runs is answered before the scan ends, that the open documents are
// checked again when it ends, and that cancelling the session stops the scan.
func TestScanDoesNotBlockSession(t *testing.T) {
	t.Parallel()
	const crd = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  group: g\n  names: {kind: K}\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema: {type: object}\n"
	for _, tc := range []struct {
		name    string
		release bool // let the scan end instead of cancelling the session
	}{
		{"scan ends", true},
		{"session cancelled", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			started, release := make(chan struct{}), make(chan struct{})
			serverIn, clientOut := io.Pipe()
			clientIn, serverOut := io.Pipe()
			ss := newSession(t)
			ss.conn = jsonrpc.NewConn(serverIn, serverOut)
			ss.scanner = func(ctx context.Context, _ []string, dst *check.CRDs) []message {
				close(started)
				select {
				case <-release:
					dst.Set(ctx, "file:///ws/crd.yaml", []byte(crd))
				case <-ctx.Done():
				}
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			served := make(chan error, 1)
			go func() { served <- ss.serve(ctx, serverIn) }()
			t.Cleanup(func() { _ = clientOut.Close(); _ = serverOut.Close() })

			c := jsonrpc.NewConn(clientIn, clientOut)
			if err := c.Request("1", "initialize", map[string]any{
				"initializationOptions": map[string]any{"experimental": []string{"crd-typed-policies"}},
				"workspaceFolders":      []map[string]any{{"uri": "file:///ws"}},
			}); err != nil {
				t.Fatal(err)
			}
			readMessage(t, c) // the initialize result
			<-started
			if err := c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": cacheURI, "version": 1, "text": widget(t)}}); err != nil {
				t.Fatal(err)
			}
			if err := c.Request("2", "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": cacheURI}, "position": map[string]any{}}); err != nil {
				t.Fatal(err)
			}
			// The hover is answered while the scan is blocked, after the
			// diagnostics of the open document.
			readUntil(t, c, func(m jsonrpc.Message) bool { return string(m.ID) == `"2"` })
			if !tc.release {
				cancel()
				if err := <-served; !errors.Is(err, context.Canceled) {
					t.Errorf("serve = %v, want context.Canceled", err)
				}
				return
			}
			close(release)
			// The scan found a CRD, so the open document is checked again.
			readUntil(t, c, func(m jsonrpc.Message) bool { return m.Method == "textDocument/publishDiagnostics" })
		})
	}
}

func readMessage(t *testing.T, c *jsonrpc.Conn) jsonrpc.Message {
	t.Helper()
	m, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// readUntil reads messages until one satisfies match.
func readUntil(t *testing.T, c *jsonrpc.Conn, match func(jsonrpc.Message) bool) {
	t.Helper()
	for {
		if match(readMessage(t, c)) {
			return
		}
	}
}

const scanCRD = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  group: g\n  names: {kind: K}\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema: {type: object}\n"

// TestScanSkipsFilesChangedDuringScan checks that a CRD file deleted or
// edited while the scan ran is not brought back by the scan's result.
func TestScanSkipsFilesChangedDuringScan(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		typ  int
	}{
		{"deleted", fileDeleted},
		{"edited", fileChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "crd.yaml")
			if err := os.WriteFile(path, []byte("kind: ConfigMap\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			ss := newSession(t)
			ss.features = feature.Of(feature.CRDTypedPolicies)
			release := make(chan struct{})
			ss.scanner = func(ctx context.Context, _ []string, dst *check.CRDs) []message {
				<-release
				dst.Set(ctx, path, []byte(scanCRD))
				return nil
			}
			ss.startScan(context.Background())
			params, _ := json.Marshal(map[string]any{"changes": []map[string]any{{"uri": "file://" + path, "type": tc.typ}}})
			if err := ss.didChangeWatchedFiles(context.Background(), params); err != nil {
				t.Fatal(err)
			}
			close(release)
			found := <-ss.scan.done
			ss.scan = nil
			if err := ss.finishScan(context.Background(), found); err != nil {
				t.Fatal(err)
			}
			if _, ok := ss.crds.Schema("g/v1", "K"); ok {
				t.Error("Schema(g/v1, K) after the file changed during the scan = found, want not found")
			}
		})
	}
}

// TestRescanRefreshesEditedCRD checks that a second scan replaces the schema
// an earlier scan recorded for a file edited on disk in between, and that an
// open document keeps the text of the editor.
func TestRescanRefreshesEditedCRD(t *testing.T) {
	t.Parallel()
	const edited = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  group: g\n  names: {kind: K2}\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema: {type: object}\n"
	ss := newSession(t)
	ss.features = feature.Of(feature.CRDTypedPolicies)
	disk := scanCRD
	ss.scanner = func(ctx context.Context, _ []string, dst *check.CRDs) []message {
		dst.Set(ctx, "/ws/crd.yaml", []byte(disk))
		dst.Set(ctx, "/ws/open.yaml", []byte(disk))
		return nil
	}
	ctx := context.Background()
	ss.docs["file:///ws/open.yaml"] = &document{text: []byte(scanCRD), crds: &ss.crds}
	ss.crds.Set(ctx, "/ws/open.yaml", []byte(scanCRD))
	for range 2 {
		ss.startScan(ctx)
		found := <-ss.scan.done
		ss.scan = nil
		if err := ss.finishScan(ctx, found); err != nil {
			t.Fatal(err)
		}
		disk = edited
	}
	if _, ok := ss.crds.Schema("g/v1", "K2"); !ok {
		t.Error("Schema(g/v1, K2) after the rescan = not found, want the edited file")
	}
	if _, ok := ss.crds.Schema("g/v1", "K"); !ok {
		t.Error("Schema(g/v1, K) after the rescan = not found, want the open document's text")
	}
}

// TestFinishScanAfterFeatureOff checks that a scan ending after the feature
// was turned off does not check the open documents again.
func TestFinishScanAfterFeatureOff(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	var found check.CRDs
	found.Set(context.Background(), "/ws/crd.yaml", []byte(scanCRD))
	if err := ss.finishScan(context.Background(), &found); err != nil {
		t.Fatal(err)
	}
	if _, ok := ss.crds.Schema("g/v1", "K"); !ok {
		t.Error("Schema(g/v1, K) = not found, want the scan's CRD recorded")
	}
}

// TestShutdownStopsScan checks that the shutdown request cancels the scan and
// waits for it, so nothing runs or publishes after shutdown.
func TestShutdownStopsScan(t *testing.T) {
	t.Parallel()
	started, stopped := make(chan struct{}), make(chan struct{})
	ss := newSession(t)
	ss.init = true
	ss.scanner = func(ctx context.Context, _ []string, _ *check.CRDs) []message {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil
	}
	ss.startScan(context.Background())
	<-started
	if err := ss.handle(context.Background(), jsonrpc.Message{ID: json.RawMessage(`1`), Method: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Error("scan still running after shutdown, want it stopped")
	}
	if ss.scan != nil {
		t.Error("session keeps the scan after shutdown, want none")
	}
}

// TestRescanForgetsDeletedCRDFile checks that a rescan drops the CRD of a file
// that left the scanned folder, and keeps a file outside the folders.
// The cap case (a scan that leaves files out) is TestCRDsFillScanForgets.
func TestRescanForgetsDeletedCRDFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone.yaml")
	kept := filepath.Join(dir, "kept.yaml")
	for _, f := range []string{gone, kept} {
		if err := os.WriteFile(f, []byte(scanCRD), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ss := newSession(t)
	ss.features = feature.Of(feature.CRDTypedPolicies)
	ss.folders = []string{dir}
	ctx := context.Background()
	ss.crds.Set(ctx, "/elsewhere/other.yaml", []byte(scanCRD))
	rescan := func() {
		ss.startScan(ctx)
		found := <-ss.scan.done
		ss.scan = nil
		if err := ss.finishScan(ctx, found); err != nil {
			t.Fatal(err)
		}
	}
	rescan()
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	rescan()
	if ss.crds.Forget(crdKey(pathURI(gone))) {
		t.Error("a rescan kept the CRD of a deleted file")
	}
	if !ss.crds.Forget(crdKey(pathURI(kept))) {
		t.Error("a rescan forgot a file that is still there")
	}
	if !ss.crds.Forget("/elsewhere/other.yaml") {
		t.Error("a rescan forgot a file outside the scanned folders")
	}
}

// TestRescanKeepsCRDUnderSkippedFolder checks that a rescan keeps the CRD of a
// file under a folder the scan skips while the file is on disk, and forgets it
// once the file is deleted.
func TestRescanKeepsCRDUnderSkippedFolder(t *testing.T) {
	t.Parallel()
	for _, folder := range []string{".hidden", "node_modules"} {
		t.Run(folder, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			sub := filepath.Join(dir, folder)
			if err := os.Mkdir(sub, 0o700); err != nil {
				t.Fatal(err)
			}
			crd := filepath.Join(sub, "crd.yaml")
			if err := os.WriteFile(crd, []byte(scanCRD), 0o600); err != nil {
				t.Fatal(err)
			}
			ss := newSession(t)
			ss.features = feature.Of(feature.CRDTypedPolicies)
			ss.folders = []string{dir}
			ctx := context.Background()
			ss.crds.Set(ctx, crdKey(pathURI(crd)), []byte(scanCRD))
			rescan := func() {
				ss.startScan(ctx)
				found := <-ss.scan.done
				ss.scan = nil
				if err := ss.finishScan(ctx, found); err != nil {
					t.Fatal(err)
				}
			}
			rescan()
			if _, ok := ss.crds.Schema("g/v1", "K"); !ok {
				t.Fatal("Schema(g/v1, K) after a rescan with the file on disk = not found, want kept")
			}
			if err := os.Remove(crd); err != nil {
				t.Fatal(err)
			}
			rescan()
			if _, ok := ss.crds.Schema("g/v1", "K"); ok {
				t.Error("Schema(g/v1, K) after a rescan with the file deleted = found, want forgotten")
			}
		})
	}
}

// TestRescanKeepsFilesPastCap checks that a rescan which hits the file cap
// does not forget a CRD of a file it left out.
func TestRescanKeepsFilesPastCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scanned := filepath.Join(dir, "a.yaml")
	pastCap := filepath.Join(dir, "b.yaml")
	for _, f := range []string{scanned, pastCap} {
		if err := os.WriteFile(f, []byte(scanCRD), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ss := newSession(t)
	ss.features = feature.Of(feature.CRDTypedPolicies)
	ss.folders = []string{dir}
	ss.fileCap = 1
	ctx := context.Background()
	ss.crds.Set(ctx, crdKey(pathURI(pastCap)), []byte(scanCRD))
	ss.startScan(ctx)
	found := <-ss.scan.done
	ss.scan = nil
	if err := ss.finishScan(ctx, found); err != nil {
		t.Fatal(err)
	}
	if !ss.crds.Forget(crdKey(pathURI(pastCap))) {
		t.Error("a rescan over the file cap forgot a file it did not scan")
	}
	if !ss.crds.Forget(crdKey(pathURI(scanned))) {
		t.Error("a rescan did not record the file within the cap")
	}
}

// TestFolderDeletedDuringScanStaysForgotten checks over the wire that the CRDs
// below a folder deleted while the workspace scan ran, which the scan had
// already read, do not come back when it ends: the open policy gets no
// warning from them.
func TestFolderDeletedDuringScanStaysForgotten(t *testing.T) {
	t.Parallel()
	const crd = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: widgets.example.com}\nspec:\n  group: example.com\n  names: {kind: Widget}\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          type: object\n          properties:\n            spec:\n              type: object\n              properties:\n                maxReplicas: {type: integer}\n"
	const policy = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: demo}\nspec:\n  paramKind: {apiVersion: example.com/v1, kind: Widget}\n  matchConstraints:\n    resourceRules:\n      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}\n  validations:\n    - expression: \"object.spec.replicas <= params.spec.maxReplica\"\n"
	dir := t.TempDir()
	sub := filepath.Join(dir, "crds")
	started, release := make(chan struct{}), make(chan struct{})
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ss := newSession(t)
	ss.conn = jsonrpc.NewConn(serverIn, serverOut)
	ss.scanner = func(ctx context.Context, _ []string, dst *check.CRDs) []message {
		dst.Set(ctx, filepath.ToSlash(filepath.Join(sub, "crd.yaml")), []byte(crd)) // read before the folder is deleted
		close(started)
		<-release
		return []message{{typ: 4, text: "scan ended", log: true}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ss.serve(ctx, serverIn) }()
	t.Cleanup(func() { _ = clientOut.Close(); _ = serverOut.Close() })

	c := jsonrpc.NewConn(clientIn, clientOut)
	if err := c.Request("1", "initialize", map[string]any{
		"initializationOptions": map[string]any{"experimental": []string{"crd-typed-policies"}},
		"workspaceFolders":      []map[string]any{{"uri": pathURI(dir)}},
	}); err != nil {
		t.Fatal(err)
	}
	readMessage(t, c) // the initialize result
	<-started
	if err := c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": pathURI(filepath.Join(dir, "policy.yaml")), "version": 1, "text": policy}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": pathURI(sub), "type": fileDeleted}}}); err != nil {
		t.Fatal(err)
	}
	// A reply to a request sent after the notifications says they are handled.
	hover := func(id string) {
		_ = c.Request(id, "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": pathURI(filepath.Join(dir, "policy.yaml"))}, "position": map[string]any{}})
	}
	go hover("2")
	readUntil(t, c, func(m jsonrpc.Message) bool { return string(m.ID) == `"2"` })
	close(release)
	// The scan's note comes before its result is filled in, and a hover sent
	// after the note is answered after that; a publish in between is the CRD
	// coming back. The pipes do not buffer: the request is written while the
	// replies are read.
	readUntil(t, c, func(m jsonrpc.Message) bool { return m.Method == "window/logMessage" })
	go hover("3")
	for {
		m := readMessage(t, c)
		if m.Method == "textDocument/publishDiagnostics" {
			t.Fatalf("diagnostics after the scan ended = %s, want none: the CRD's folder was deleted", m.Params)
		}
		if string(m.ID) == `"3"` {
			return
		}
	}
}

// TestRescanWithUnreadableFolder checks that a missing workspace folder keeps
// the CRDs known below it, does not stop the rescan from forgetting a CRD
// deleted below a readable folder, and is named in the scan's notes.
func TestRescanWithUnreadableFolder(t *testing.T) {
	t.Parallel()
	good := t.TempDir()
	stale := filepath.Join(t.TempDir(), "removed")
	gone := filepath.Join(good, "gone.yaml")
	kept := filepath.Join(good, "kept.yaml")
	for _, f := range []string{gone, kept} {
		if err := os.WriteFile(f, []byte(scanCRD), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ss := newSession(t)
	ss.features = feature.Of(feature.CRDTypedPolicies)
	ss.folders = []string{good, stale}
	ctx := context.Background()
	staleFile := crdKey(pathURI(filepath.Join(stale, "widget.yaml")))
	ss.crds.Set(ctx, staleFile, []byte(scanCRD))
	rescan := func() {
		ss.startScan(ctx)
		found := <-ss.scan.done
		notes := ss.scan.notes
		ss.scan = nil
		if err := ss.finishScan(ctx, found); err != nil {
			t.Fatal(err)
		}
		if len(notes) != 1 || !strings.Contains(notes[0].text, "incomplete") || !strings.Contains(notes[0].text, stale) {
			t.Fatalf("scan notes = %v, want one naming %s as incomplete", notes, stale)
		}
	}
	rescan()
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	rescan()
	if ss.crds.Forget(crdKey(pathURI(gone))) {
		t.Error("a rescan kept the CRD deleted below the readable folder")
	}
	if !ss.crds.Forget(crdKey(pathURI(kept))) {
		t.Error("a rescan forgot a CRD that is still there")
	}
	if !ss.crds.Forget(staleFile) {
		t.Error("a rescan forgot a CRD below the folder that could not be read")
	}
}

// TestCreatedSkippedFolderRecordsNoCRDs checks that a created folder the
// workspace scan would skip, a hidden one or node_modules, records no CRDs.
func TestCreatedSkippedFolderRecordsNoCRDs(t *testing.T) {
	t.Parallel()
	for _, folder := range []string{".git", "node_modules"} {
		t.Run(folder, func(t *testing.T) {
			t.Parallel()
			sub := filepath.Join(t.TempDir(), folder)
			if err := os.Mkdir(sub, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, "crd.yaml"), []byte(scanCRD), 0o600); err != nil {
				t.Fatal(err)
			}
			ss := newSession(t)
			ss.features = feature.Of(feature.CRDTypedPolicies)
			params, _ := json.Marshal(map[string]any{"changes": []map[string]any{{"uri": pathURI(sub), "type": fileCreated}}})
			if err := ss.didChangeWatchedFiles(context.Background(), params); err != nil {
				t.Fatal(err)
			}
			if _, ok := ss.crds.Schema("g/v1", "K"); ok {
				t.Errorf("Schema(g/v1, K) after creating %s = found, want not found", folder)
			}
		})
	}
}
