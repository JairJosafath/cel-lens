package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
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
