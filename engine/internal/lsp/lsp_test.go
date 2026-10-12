package lsp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/internal/lsp"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

const uri = "file:///widget.yaml"

// client drives a Server over pipes, as an editor does.
type client struct {
	t      *testing.T
	in     *jsonrpc.Conn // the server's messages
	out    io.WriteCloser
	nextID int
	done   chan error // Serve's result
}

// start runs a Server and returns the client talking to it. The server's
// goroutine ends, and is waited for, when the test ends.
func start(t *testing.T) *client {
	t.Helper()
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	c := &client{t: t, in: jsonrpc.NewConn(clientIn, io.Discard), out: clientOut, done: make(chan error, 1)}
	go func() {
		c.done <- (&lsp.Server{Version: "test"}).Serve(context.Background(), serverIn, serverOut)
		_ = serverOut.Close()
	}()
	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
		<-c.done // Serve returns once its input ends or it has exited
	})
	return c
}

// use makes the client report failures on t, a subtest of the test that
// started it, until t ends. Subtests that share a client must not run in
// parallel.
func (c *client) use(t *testing.T) {
	t.Helper()
	parent := c.t
	c.t = t
	t.Cleanup(func() { c.t = parent })
}

// call sends a request and returns the response's result, or its error.
func (c *client) call(method string, params any) (json.RawMessage, *jsonrpc.Error) {
	c.t.Helper()
	c.nextID++
	id := strconv.Itoa(c.nextID)
	c.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params})
	for {
		m := c.read()
		if string(m.ID) == id {
			return m.Result, m.Error
		}
	}
}

func (c *client) notify(method string, params any) {
	c.t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *client) send(v any) {
	c.t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c.out, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		c.t.Fatalf("write to server: %v", err)
	}
}

func (c *client) read() jsonrpc.Message {
	c.t.Helper()
	m, err := c.in.Read()
	if err != nil {
		c.t.Fatalf("read from server: %v", err)
	}
	return m
}

// diagnostics waits for the next publishDiagnostics notification.
func (c *client) diagnostics() (version int, diags []diagnostic) {
	c.t.Helper()
	for {
		m := c.read()
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p struct {
			Version     int          `json:"version"`
			Diagnostics []diagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			c.t.Fatal(err)
		}
		return p.Version, p.Diagnostics
	}
}

type diagnostic struct {
	Range struct {
		Start, End struct{ Line, Character int }
	}
	Severity int
	Message  string
}

func (c *client) open(text string) {
	c.t.Helper()
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "yaml", "version": 1, "text": text}})
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lineOf returns the 0-based line of the first line of text that contains s,
// and the UTF-16 offset of s in it.
func lineOf(t *testing.T, text, s string) (line, char int) {
	t.Helper()
	for i, l := range strings.Split(text, "\n") {
		if before, _, ok := strings.Cut(l, s); ok {
			n := 0
			for _, r := range before {
				n += 1 + int(r>>16&1) // astral runes take two units
			}
			return i, n
		}
	}
	t.Fatalf("%q not in text", s)
	return 0, 0
}

func initialize(t *testing.T, c *client) {
	t.Helper()
	res, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil})
	if rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	var r struct {
		Capabilities struct {
			HoverProvider      bool
			CodeActionProvider struct{ CodeActionKinds []string }
			CompletionProvider struct{ TriggerCharacters []string }
			TextDocumentSync   struct {
				OpenClose bool
				Change    int
			}
		}
		ServerInfo struct{ Name, Version string }
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	caps := r.Capabilities
	if !caps.HoverProvider || len(caps.CompletionProvider.TriggerCharacters) == 0 || !caps.TextDocumentSync.OpenClose || caps.TextDocumentSync.Change != 1 {
		t.Errorf("initialize capabilities = %+v, want hover, completion, open/close and full sync", caps)
	}
	if r.ServerInfo.Name != "cel-lens-core" || r.ServerInfo.Version != "test" {
		t.Errorf("initialize serverInfo = %+v, want cel-lens-core test", r.ServerInfo)
	}
	if kinds := caps.CodeActionProvider.CodeActionKinds; len(kinds) != 1 || kinds[0] != "quickfix" {
		t.Errorf("initialize codeActionProvider kinds = %v, want [quickfix]", kinds)
	}
	c.notify("initialized", map[string]any{})
}

func shutdown(t *testing.T, c *client) {
	t.Helper()
	if _, rerr := c.call("shutdown", nil); rerr != nil {
		t.Fatalf("shutdown error = %+v", rerr)
	}
	c.notify("exit", nil)
	if err := <-c.done; err != nil {
		t.Errorf("Serve() = %v, want nil after shutdown and exit", err)
	}
	c.done <- nil
}

// TestSession drives the server through initialize, didOpen, didChange, hover,
// completion, shutdown and exit.
func TestSession(t *testing.T) {
	c := start(t)
	text := readFile(t, "widget.yaml")
	initialize(t, c)

	c.open(text)
	version, diags := c.diagnostics()
	line, char := lineOf(t, text, "self.replcas")
	if version != 1 || len(diags) != 1 {
		t.Fatalf("diagnostics after didOpen = v%d %+v, want v1 and one", version, diags)
	}
	d := diags[0]
	if d.Severity != 1 || !strings.Contains(d.Message, "replcas") || d.Range.Start.Line != line || d.Range.Start.Character != char+len("self.") ||
		d.Range.End.Line != line || d.Range.End.Character != char+len("self.replcas") {
		t.Errorf("diagnostic = %+v, want an error about replcas at %d:%d-%d", d, line, char+len("self."), char+len("self.replcas"))
	}

	fixed := strings.Replace(text, "replcas", "replicas", 1)
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{"text": fixed}},
	})
	if version, diags = c.diagnostics(); version != 2 || len(diags) != 0 {
		t.Errorf("diagnostics after fixing = v%d %+v, want v2 and none", version, diags)
	}

	line, char = lineOf(t, fixed, "self.replicas")
	res, rerr := c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char + len("self.rep")}})
	if rerr != nil {
		t.Fatalf("hover error = %+v", rerr)
	}
	var hover struct {
		Contents struct{ Kind, Value string }
		Range    struct{ Start, End struct{ Line, Character int } }
	}
	if err := json.Unmarshal(res, &hover); err != nil {
		t.Fatalf("hover result %s: %v", res, err)
	}
	if hover.Contents.Kind != "markdown" || !strings.Contains(hover.Contents.Value, "(field) replicas: int") ||
		hover.Range.Start.Character != char+len("self.") || hover.Range.End.Character != char+len("self.replicas") {
		t.Errorf("hover = %+v, want the int field replicas over its name", hover)
	}

	res, rerr = c.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char + len("self.")}})
	if rerr != nil {
		t.Fatalf("completion error = %+v", rerr)
	}
	var items []struct {
		Label string
		Kind  int
	}
	if err := json.Unmarshal(res, &items); err != nil {
		t.Fatalf("completion result %s: %v", res, err)
	}
	have := map[string]int{}
	for _, it := range items {
		have[it.Label] = it.Kind
	}
	if have["replicas"] != 5 || have["name"] != 5 || have["ports"] != 5 {
		t.Errorf("completion after self. = %v, want the fields replicas, name and ports (kind 5)", have)
	}

	line, char = lineOf(t, fixed, "self.replicas")
	res, _ = c.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char}})
	if err := json.Unmarshal(res, &items); err != nil {
		t.Fatalf("completion result %s: %v", res, err)
	}
	have = map[string]int{}
	for _, it := range items {
		have[it.Label] = it.Kind
	}
	if have["self"] != 6 || have["size"] != 3 {
		t.Errorf("completion at the start of an expression = %d items, want the variable self and the function size", len(items))
	}

	shutdown(t, c)
}

func TestOutsideCEL(t *testing.T) {
	c := start(t)
	text := readFile(t, "widget.yaml")
	initialize(t, c)
	c.open(text)
	c.diagnostics()
	line, char := lineOf(t, text, "message: bad")
	for _, method := range []string{"textDocument/hover", "textDocument/completion"} {
		res, rerr := c.call(method, map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char + 3}})
		if rerr != nil || string(res) != "null" {
			t.Errorf("%s outside CEL = %s, %+v, want null", method, res, rerr)
		}
	}
	res, rerr := c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": "file:///unknown.yaml"}, "position": map[string]any{"line": 0, "character": 0}})
	if rerr != nil || string(res) != "null" {
		t.Errorf("hover in an unopened document = %s, %+v, want null", res, rerr)
	}
	shutdown(t, c)
}

func TestClose(t *testing.T) {
	c := start(t)
	initialize(t, c)
	c.open(readFile(t, "widget.yaml"))
	c.diagnostics()
	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
	if _, diags := c.diagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics after didClose = %+v, want none", diags)
	}
	shutdown(t, c)
}

func TestProtocolErrors(t *testing.T) {
	c := start(t)
	if _, rerr := c.call("textDocument/hover", map[string]any{}); rerr == nil || rerr.Code != jsonrpc.CodeNotInitialized {
		t.Errorf("hover before initialize error = %+v, want code %d", rerr, jsonrpc.CodeNotInitialized)
	}
	initialize(t, c)
	if _, rerr := c.call("textDocument/rename", map[string]any{}); rerr == nil || rerr.Code != jsonrpc.CodeMethodNotFound {
		t.Errorf("unknown method error = %+v, want code %d", rerr, jsonrpc.CodeMethodNotFound)
	}
	if _, rerr := c.call("textDocument/hover", "not an object"); rerr == nil || rerr.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("hover with bad params error = %+v, want code %d", rerr, jsonrpc.CodeInvalidParams)
	}
	if _, rerr := c.call("shutdown", nil); rerr != nil {
		t.Fatalf("shutdown error = %+v", rerr)
	}
	if _, rerr := c.call("textDocument/hover", map[string]any{}); rerr == nil || rerr.Code != jsonrpc.CodeInvalidRequest {
		t.Errorf("hover after shutdown error = %+v, want code %d", rerr, jsonrpc.CodeInvalidRequest)
	}
	c.notify("exit", nil)
	if err := <-c.done; err != nil {
		t.Errorf("Serve() = %v, want nil", err)
	}
	c.done <- nil
}

func TestEndWithoutShutdown(t *testing.T) {
	for _, tt := range []struct {
		name string
		end  func(c *client)
	}{
		{"exit", func(c *client) { c.notify("exit", nil) }},
		{"closed stream", func(c *client) { _ = c.out.Close() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := start(t)
			initialize(t, c)
			tt.end(c)
			err := <-c.done
			c.done <- err
			if !errors.Is(err, lsp.ErrNoShutdown) {
				t.Errorf("Serve() = %v, want ErrNoShutdown", err)
			}
		})
	}
}

func TestCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := new(lsp.Server).Serve(ctx, strings.NewReader(""), io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Serve(cancelled) = %v, want context.Canceled", err)
	}
}

// TestCostFixes checks that a cost diagnostic carries the field path and the
// limit a quick fix needs in its data.
func TestCostFixes(t *testing.T) {
	c := start(t)
	initialize(t, c)
	b, err := os.ReadFile("../../pkg/check/testdata/crd-cost-name.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c.open(string(b))
	var p struct {
		Diagnostics []struct {
			Data struct {
				Fixes []struct{ FieldPath, Limit string }
			}
		}
	}
	for {
		m := c.read()
		if m.Method == "textDocument/publishDiagnostics" {
			if err := json.Unmarshal(m.Params, &p); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if len(p.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want one", p.Diagnostics)
	}
	want := "[{spec.entries maxItems} {spec.entries[*].name maxLength}]"
	if got := fmt.Sprint(p.Diagnostics[0].Data.Fixes); got != want {
		t.Errorf("diagnostic data fixes = %s, want %s", got, want)
	}
	shutdown(t, c)
}

type codeAction struct {
	Title string
	Kind  string
	Edit  struct {
		Changes map[string][]struct {
			Range struct {
				Start, End struct{ Line, Character int }
			}
			NewText string
		}
	}
}

// codeActions asks for the quick fixes at the range of d.
func (c *client) codeActions(d diagnostic) []codeAction {
	c.t.Helper()
	res, rerr := c.call("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range":        d.Range,
		"context":      map[string]any{"diagnostics": []any{}},
	})
	if rerr != nil {
		c.t.Fatalf("codeAction error = %+v", rerr)
	}
	var actions []codeAction
	if err := json.Unmarshal(res, &actions); err != nil {
		c.t.Fatal(err)
	}
	return actions
}

// applyAction writes the edits of a into text, which must be ASCII.
func applyAction(t *testing.T, text string, a codeAction) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	edits := a.Edit.Changes[uri]
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		if e.Range.Start.Line != e.Range.End.Line {
			t.Fatalf("edit %+v spans lines", e)
		}
		l := lines[e.Range.Start.Line]
		lines[e.Range.Start.Line] = l[:e.Range.Start.Character] + e.NewText + l[e.Range.End.Character:]
	}
	return strings.Join(lines, "\n")
}

// recheck replaces the document with text and returns the diagnostics for it.
func (c *client) recheck(text string, version int) []diagnostic {
	c.t.Helper()
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []any{map[string]any{"text": text}},
	})
	_, diags := c.diagnostics()
	return diags
}

// TestQuickFixes offers the fixes for the misspelled names of a document and
// applies each, then checks that the problem it was offered for is gone.
func TestQuickFixes(t *testing.T) {
	c := start(t)
	initialize(t, c)
	text := readFile(t, "../../assist/testdata/typos.yaml")
	c.open(text)
	_, diags := c.diagnostics()
	offered := map[string]string{} // problem -> title
	for i, d := range diags {
		actions := c.codeActions(d)
		if len(actions) == 0 {
			continue
		}
		if len(actions) != 1 || actions[0].Kind != "quickfix" {
			t.Fatalf("codeAction(%q) = %+v, want one quickfix", d.Message, actions)
		}
		offered[d.Message] = actions[0].Title
		for _, after := range c.recheck(applyAction(t, text, actions[0]), 2+i) {
			if after.Message == d.Message {
				t.Errorf("after %q the problem %q remains", actions[0].Title, d.Message)
			}
		}
		c.recheck(text, 100+i)
	}
	want := map[string]string{
		"undefined field 'maxReplica'":                         "Change to `maxReplicas`",
		"undeclared reference to 'startWith' (in container ''": "Change to `startsWith`",
		"undeclared reference to 'sizee' (in container ''":     "Change to `size`",
	}
	for msg, title := range want {
		found := false
		for got, gotTitle := range offered {
			if strings.HasPrefix(got, msg) && gotTitle == title {
				found = true
			}
		}
		if !found {
			t.Errorf("no quick fix %q for %q, offered %v", title, msg, offered)
		}
	}
	if len(offered) != len(want) {
		t.Errorf("quick fixes = %v, want only %d: none for names with no clear match", offered, len(want))
	}
	shutdown(t, c)
}

// TestQuickFixLimits adds the limits a cost problem asks for, and the document
// has no problem left; a cost problem without limits has no quick fix.
func TestQuickFixLimits(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		title string // "" means no quick fix
	}{
		{"limits that work together", "crd-cost-name.yaml", "Add `maxItems: 1000` to `spec.entries` and `maxLength: 1000` to `spec.entries[*].name`"},
		{"limits with the same number", "crd-cost.yaml", "Add `maxItems: 200` to `items` and `maxLength: 200` to `items[*]`"},
		{"a limit alone", "crd-cost-string.yaml", "Add `maxLength: 200000` to `name`"},
		{"no limit to add", "crd-cost-message-expression.yaml", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := start(t)
			initialize(t, c)
			b, err := os.ReadFile("../../pkg/check/testdata/" + tt.file)
			if err != nil {
				t.Fatal(err)
			}
			text := string(b)
			c.open(text)
			_, diags := c.diagnostics()
			if len(diags) == 0 {
				t.Fatalf("%s has no diagnostics", tt.file)
			}
			var actions []codeAction
			for _, d := range diags {
				actions = append(actions, c.codeActions(d)...)
			}
			if tt.title == "" {
				if len(actions) != 0 {
					t.Errorf("codeAction = %+v, want none", actions)
				}
				return
			}
			var titles []string
			for _, a := range actions {
				titles = append(titles, a.Title)
			}
			if len(actions) == 0 || actions[0].Title != tt.title {
				t.Fatalf("codeAction titles = %q, want %q first", titles, tt.title)
			}
			if after := c.recheck(applyAction(t, text, actions[0]), 2); len(after) != 0 {
				t.Errorf("after %q diagnostics = %+v, want none", tt.title, after)
			}
			shutdown(t, c)
		})
	}
}

// TestBrokenYAML opens a CRD whose YAML does not parse, as a single document
// and among others, and then fixes it.
func TestBrokenYAML(t *testing.T) {
	c := start(t)
	initialize(t, c)
	widget := readFile(t, "widget.yaml")

	fixed := strings.Replace(widget, "replcas", "replicas", 1)
	broken := strings.Replace(fixed, "type: object", "type: object: x", 1)
	c.open(broken)
	_, diags := c.diagnostics()
	line, char := lineOf(t, broken, "type: object: x")
	if len(diags) != 1 {
		t.Fatalf("diagnostics for broken YAML = %+v, want one", diags)
	}
	d := diags[0]
	wantEnd := char + len("type: object: x")
	if d.Severity != 1 || d.Message != "YAML is broken here: mapping values are not allowed in this context" ||
		d.Range.Start.Line != line || d.Range.Start.Character != char || d.Range.End.Line != line || d.Range.End.Character != wantEnd {
		t.Errorf("diagnostic = %+v, want an error with the parser's message over %d:%d-%d", d, line, char, wantEnd)
	}

	if diags = c.recheck(fixed, 2); len(diags) != 0 {
		t.Errorf("diagnostics after the YAML parses = %+v, want none", diags)
	}

	// The same broken line as in check's TestCheckMalformedYAML: the language
	// server and check report the same place, counted in characters.
	diags = c.recheck("kind: X\nspec:\n description: Größe — 日本語: x\n", 3)
	if len(diags) != 1 {
		t.Fatalf("diagnostics for a broken line with non-ASCII text = %+v, want one", diags)
	}
	if r := diags[0].Range; r.Start.Line != 2 || r.Start.Character != 1 || r.End.Line != 2 || r.End.Character != 28 {
		t.Errorf("range = %+v, want line 2, characters 1-28", r)
	}

	multi := "a: b: c\n---\n" + widget
	diags = c.recheck(multi, 4)
	if len(diags) != 2 {
		t.Fatalf("diagnostics for a broken and a CEL-broken document = %+v, want two", diags)
	}
	if diags[0].Range.Start.Line != 0 || !strings.HasPrefix(diags[0].Message, "YAML is broken here: ") || !strings.Contains(diags[1].Message, "replcas") {
		t.Errorf("diagnostics = %+v, want the YAML problem on line 0, then the CEL problem", diags)
	}
	shutdown(t, c)
}

// TestBadCharacter opens documents with a character YAML refuses and expects
// one diagnostic, on the line and column of the character, as check reports it.
func TestBadCharacter(t *testing.T) {
	var second strings.Builder
	second.WriteString("a: 1\n")
	for i := 2; i < 26; i++ {
		fmt.Fprintf(&second, "k%d: v\n", i)
	}
	second.WriteString("---\nx: 1\ny: 1\nz: 1\nw: 1\nv: 1\nu: 1\nt: \x01\n")
	tests := []struct {
		name      string
		src       string
		line      int
		character int
		message   string
	}{
		{"invalid Unicode character (U+FFFE) on line 2", "a: 1\nb: \ufffe\n", 1, 3, "YAML is broken here: control characters are not allowed"},
		{"control character on line 33 of the second document", second.String(), 32, 3, "YAML is broken here: control characters are not allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := start(t)
			initialize(t, c)
			c.use(t)
			c.open(tt.src)
			_, diags := c.diagnostics()
			if len(diags) != 1 {
				t.Fatalf("diagnostics = %+v, want one", diags)
			}
			d := diags[0]
			if d.Range.Start.Line != tt.line || d.Range.Start.Character != tt.character || d.Message != tt.message {
				t.Errorf("diagnostic = %+v, want %q at line %d character %d", d, tt.message, tt.line, tt.character)
			}
			shutdown(t, c)
		})
	}
}

// cancelWriter cancels a context on its first write.
type cancelWriter struct {
	cancel context.CancelFunc
}

func (w cancelWriter) Write(p []byte) (int, error) {
	w.cancel()
	return len(p), nil
}

// TestServeCancelWithMessageReady cancels the context while the server answers
// one message and another is already queued. Serve must return Canceled
// instead of handling the queued "exit". Which select case wins is random, so
// the run repeats; without the ctx.Err check after the receive it fails.
func TestServeCancelWithMessageReady(t *testing.T) {
	t.Parallel()
	frame := func(v any) string {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	input := frame(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil}}) +
		frame(map[string]any{"jsonrpc": "2.0", "method": "exit"})
	for i := range 200 {
		ctx, cancel := context.WithCancel(context.Background())
		err := new(lsp.Server).Serve(ctx, strings.NewReader(input), cancelWriter{cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run %d: Serve(cancelled with a message queued) = %v, want %v", i, err, context.Canceled)
		}
	}
}

// signalReader is a pipe reader that reports when Read starts and returns.
type signalReader struct {
	*io.PipeReader
	entered  chan struct{}
	returned chan struct{}
	enter    *sync.Once
	once     *sync.Once
}

func (r signalReader) Read(p []byte) (int, error) {
	r.enter.Do(func() { close(r.entered) })
	defer r.once.Do(func() { close(r.returned) })
	return r.PipeReader.Read(p)
}

func TestServeCancelWhileReading(t *testing.T) {
	serverIn, clientOut := io.Pipe()
	t.Cleanup(func() { _ = clientOut.Close() })
	in := signalReader{serverIn, make(chan struct{}), make(chan struct{}), new(sync.Once), new(sync.Once)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- new(lsp.Server).Serve(ctx, in, io.Discard) }()
	<-in.entered // Serve is blocked reading
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Serve(cancelled while reading) = %v, want %v", err, context.Canceled)
	}
	<-in.returned // the reading goroutine is released by closing the input
}

// TestClientResponseIgnored checks that a response from the client gets no
// reply, even before initialize.
func TestClientResponseIgnored(t *testing.T) {
	c := start(t)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 900, "result": nil})
	initialize(t, c)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 901, "result": map[string]any{}})
	c.send(map[string]any{"jsonrpc": "2.0", "id": 902, "method": "shutdown"})
	for {
		m := c.read()
		if string(m.ID) == "900" || string(m.ID) == "901" {
			t.Fatalf("server replied to a client response: %+v", m)
		}
		if string(m.ID) == "902" {
			break
		}
	}
	c.notify("exit", nil)
	if err := <-c.done; err != nil {
		t.Errorf("Serve() = %v, want nil", err)
	}
	c.done <- nil
}

// TestAssistsBeforeBadDocument opens a CRD followed by a document with a
// control character and checks that the fix, hover and completion for the CRD
// still answer.
func TestAssistsBeforeBadDocument(t *testing.T) {
	c := start(t)
	initialize(t, c)
	text := readFile(t, "../../assist/testdata/typos.yaml") + "\n---\nkey: \x01\n"
	c.open(text)
	_, diags := c.diagnostics()

	badLine, _ := lineOf(t, text, "key: \x01")
	broken := false
	var typo *diagnostic
	for i, d := range diags {
		if strings.Contains(d.Message, "control characters are not allowed") && d.Range.Start.Line == badLine {
			broken = true
		}
		if strings.Contains(d.Message, "undefined field 'maxReplica'") {
			typo = &diags[i]
		}
	}
	if !broken {
		t.Errorf("diagnostics = %+v, want one about control characters on line %d", diags, badLine)
	}
	if typo == nil {
		t.Fatalf("diagnostics = %+v, want one about maxReplica", diags)
	}
	found := false
	for _, a := range c.codeActions(*typo) {
		found = found || a.Title == "Change to `maxReplicas`"
	}
	if !found {
		t.Errorf("codeAction(maxReplica) = %+v, want Change to `maxReplicas`", c.codeActions(*typo))
	}

	line, char := lineOf(t, text, "self.maxReplicas > 0")
	at := func(n int) map[string]any {
		return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char + n}}
	}
	res, rerr := c.call("textDocument/hover", at(len("self.max")))
	if rerr != nil || string(res) == "null" || !strings.Contains(string(res), "maxReplicas") {
		t.Errorf("hover(self.maxReplicas) = %s, %+v, want a description of maxReplicas", res, rerr)
	}
	res, rerr = c.call("textDocument/completion", at(len("self.")))
	var items []struct{ Label string }
	if err := json.Unmarshal(res, &items); rerr != nil || err != nil {
		t.Fatalf("completion = %s, %+v, %v", res, rerr, err)
	}
	have := map[string]bool{}
	for _, it := range items {
		have[it.Label] = true
	}
	if !have["maxReplicas"] || !have["name"] {
		t.Errorf("completion after self. = %v, want the fields maxReplicas and name", have)
	}
	shutdown(t, c)
}

// TestTypedObject checks that a policy matching one built-in kind gets a
// warning, not an error, for a field the kind lacks, and that completion and
// hover use the kind's fields.
func TestTypedObject(t *testing.T) {
	c := start(t)
	text := readFile(t, "deployment-policy.yaml")
	initialize(t, c)
	c.open(text)
	_, diags := c.diagnostics()
	line, char := lineOf(t, text, "object.additionalContainers")
	const wantMessage = "apps/v1 Deployment: undefined field 'additionalContainers'"
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %+v, want one warning", diags)
	}
	d := diags[0]
	if d.Severity != 2 || d.Message != wantMessage || d.Range.Start.Line != line || d.Range.Start.Character != char+len("object.") ||
		d.Range.End.Character != char+len("object.additionalContainers") {
		t.Errorf("diagnostic = %+v, want warning %q over additionalContainers at line %d", d, wantMessage, line)
	}

	pos := map[string]any{"line": line, "character": char + len("object.")}
	res, rerr := c.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos})
	if rerr != nil {
		t.Fatalf("completion error = %+v", rerr)
	}
	var items []struct{ Label string }
	if err := json.Unmarshal(res, &items); err != nil {
		t.Fatalf("completion result %s: %v", res, err)
	}
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	slices.Sort(labels)
	if want := []string{"apiVersion", "kind", "metadata", "spec", "status"}; !slices.Equal(labels, want) {
		t.Errorf("completion after object. = %v, want %v", labels, want)
	}

	res, rerr = c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char + 2}})
	if rerr != nil {
		t.Fatalf("hover error = %+v", rerr)
	}
	var hover struct{ Contents struct{ Value string } }
	if err := json.Unmarshal(res, &hover); err != nil || !strings.Contains(hover.Contents.Value, "object: apps/v1 Deployment") {
		t.Errorf("hover on object = %s, %v, want it to name apps/v1 Deployment", res, err)
	}
	shutdown(t, c)
}

// TestSeveralKinds checks that a policy matching two kinds gets one warning per
// kind that lacks a field, and that completion lists the fields both kinds have
// before the others, each item naming its kinds.
func TestSeveralKinds(t *testing.T) {
	c := start(t)
	text := readFile(t, "workload-policy.yaml")
	initialize(t, c)
	c.open(text)
	_, diags := c.diagnostics()
	line, char := lineOf(t, text, "object.spec.serviceName")
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %+v, want one warning", diags)
	}
	const wantMessage = "apps/v1 Deployment: undefined field 'serviceName'"
	if d := diags[0]; d.Severity != 2 || d.Message != wantMessage || d.Range.Start.Line != line || d.Range.Start.Character != char+len("object.spec.") {
		t.Errorf("diagnostic = %+v, want warning %q over serviceName at line %d", d, wantMessage, line)
	}

	pos := map[string]any{"line": line, "character": char + len("object.spec.")}
	res, rerr := c.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos})
	if rerr != nil {
		t.Fatalf("completion error = %+v", rerr)
	}
	var items []struct {
		Label    string
		Detail   string
		SortText string
		Kind     int
	}
	if err := json.Unmarshal(res, &items); err != nil {
		t.Fatalf("completion result %s: %v", res, err)
	}
	var fields []string
	details := map[string]string{}
	for _, it := range items {
		if it.Kind == 5 {
			fields = append(fields, it.Label)
			details[it.Label] = it.Detail
		}
	}
	// The client orders the items by sortText.
	sortText := map[string]string{}
	for _, it := range items {
		sortText[it.Label] = it.SortText
	}
	slices.SortFunc(fields, func(a, b string) int { return strings.Compare(sortText[a], sortText[b]) })
	shared, others := []string{"minReadySeconds", "replicas", "revisionHistoryLimit", "selector", "template"}, "paused"
	if !slices.Equal(fields[:len(shared)], shared) || !slices.Contains(fields[len(shared):], others) {
		t.Errorf("fields after object.spec. by sortText = %v, want %v first, then the others such as %s", fields, shared, others)
	}
	if want := "int · Deployment, StatefulSet"; details["replicas"] != want {
		t.Errorf("detail of replicas = %q, want %q", details["replicas"], want)
	}
	if want := "string · StatefulSet"; details["serviceName"] != want {
		t.Errorf("detail of serviceName = %q, want %q", details["serviceName"], want)
	}
	shutdown(t, c)
}

// TestKyvernoPolicy checks that the server reports what Kyverno's CEL
// environment rejects in a ValidatingPolicy, on the exact part that is wrong.
func TestKyvernoPolicy(t *testing.T) {
	c := start(t)
	text := readFile(t, "kyverno-policy.yaml")
	initialize(t, c)
	c.open(text)
	_, diags := c.diagnostics()
	line, char := lineOf(t, text, "nope")
	const wantMessage = "undeclared reference to 'nope' (in container '')"
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %+v, want one error", diags)
	}
	if d := diags[0]; d.Severity != 1 || d.Message != wantMessage || d.Range.Start.Line != line || d.Range.Start.Character != char || d.Range.End.Character != char+len("nope") {
		t.Errorf("diagnostic = %+v, want error %q over nope at line %d", d, wantMessage, line)
	}
	shutdown(t, c)
}

// TestKyvernoAssist checks completion and hover in a ValidatingPolicy: Kyverno's
// variables and functions and no others, and object typed from the kinds the
// policy matches.
func TestKyvernoAssist(t *testing.T) {
	c := start(t)
	text := readFile(t, "kyverno-assist.yaml")
	initialize(t, c)
	c.open(text)
	if _, diags := c.diagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics = %+v, want none", diags)
	}
	at := func(s string, offset int) map[string]any {
		line, char := lineOf(t, text, s)
		return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char + offset}}
	}
	complete := func(s string, offset int) map[string]int {
		t.Helper()
		res, rerr := c.call("textDocument/completion", at(s, offset))
		if rerr != nil {
			t.Fatalf("completion error = %+v", rerr)
		}
		var items []struct {
			Label string
			Kind  int
		}
		if err := json.Unmarshal(res, &items); err != nil {
			t.Fatalf("completion result %s: %v", res, err)
		}
		have := map[string]int{}
		for _, it := range items {
			have[it.Label] = it.Kind
		}
		return have
	}
	hover := func(s string, offset int) string {
		t.Helper()
		res, rerr := c.call("textDocument/hover", at(s, offset))
		if rerr != nil {
			t.Fatalf("hover error = %+v", rerr)
		}
		var h struct{ Contents struct{ Value string } }
		if err := json.Unmarshal(res, &h); err != nil {
			t.Fatalf("hover result %s: %v", res, err)
		}
		return h.Contents.Value
	}

	top := complete("isImage('a')", 0)
	for _, label := range []string{"object", "request", "variables", "resource", "http", "image", "isImage"} {
		if _, ok := top[label]; !ok {
			t.Errorf("completion at the start of an expression lacks %q: %v", label, top)
		}
	}
	for _, label := range []string{"authorizer", "params", "self"} {
		if _, ok := top[label]; ok {
			t.Errorf("completion at the start of an expression offers %q, which Kyverno lacks", label)
		}
	}
	if members := complete("resource.Get", len("resource.")); members["Get"] == 0 || members["List"] == 0 || members["startsWith"] != 0 {
		t.Errorf("completion after resource. = %v, want Get and List and no string methods", members)
	}
	if names := complete("variables.replicas >", len("variables.")); len(names) != 1 || names["replicas"] != 5 {
		t.Errorf("completion after variables. = %v, want the policy's variable replicas", names)
	}
	if fields := complete("object.spec.replicas >", len("object.")); fields["spec"] != 5 || fields["metadata"] != 5 {
		t.Errorf("completion after object. = %v, want the fields of a Deployment", fields)
	}
	if got := hover("isImage('a')", 2); !strings.Contains(got, "(function) isImage(string) -> bool") || !strings.Contains(got, "valid image reference") {
		t.Errorf("hover(isImage) = %q, want its signature and description", got)
	}
	if got := hover("resource.Get", 2); !strings.Contains(got, "(variable) resource: resource.Context") {
		t.Errorf("hover(resource) = %q, want its type", got)
	}
	if got := hover("object.spec.replicas >", 2); !strings.Contains(got, "(variable) object: apps/v1 Deployment") {
		t.Errorf("hover(object) = %q, want the matched kind", got)
	}
	shutdown(t, c)
}

// TestPolicyVariables checks that the server reports the problems check.Check
// reports for the variables of a policy, and completes the chain of authorizer
// calls with the next method.
func TestPolicyVariables(t *testing.T) {
	const chain = "authorizer.group('apps').resource('deployments').namespace('ns')."
	text := `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  validations:
    - expression: request.usrInfo.username == '' && self == 1
    - expression: ` + chain + `check('update').allowed()
`
	want, err := check.Check(context.Background(), "widget.yaml", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	c := start(t)
	initialize(t, c)
	c.open(text)
	_, diags := c.diagnostics()
	if len(diags) != len(want) || len(want) != 2 {
		t.Fatalf("diagnostics = %+v, want the %d problems of check.Check: %+v", diags, len(want), want)
	}
	for i, d := range diags {
		w := want[i]
		if d.Message != w.Message || d.Range.Start.Line != w.Start.Line-1 || d.Range.Start.Character != w.Start.Column-1 || d.Range.End.Character != w.End.Column-1 {
			t.Errorf("diagnostic %d = %+v, want the problem of check.Check: %+v", i, d, w)
		}
	}

	line, char := lineOf(t, text, chain)
	res, rerr := c.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char + len(chain)}})
	if rerr != nil {
		t.Fatalf("completion error = %+v", rerr)
	}
	var items []struct{ Label string }
	if err := json.Unmarshal(res, &items); err != nil {
		t.Fatalf("completion result %s: %v", res, err)
	}
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	for _, w := range []string{"check", "name", "subresource"} {
		if !slices.Contains(labels, w) {
			t.Errorf("completion after %q = %v, want it to offer %q", chain, labels, w)
		}
	}
	if slices.Contains(labels, "allowed") {
		t.Errorf("completion after %q = %v, want it not to offer allowed", chain, labels)
	}
	shutdown(t, c)
}

// TestOldObjectCompletion checks that the server offers after "oldObject."
// exactly what it offers after "object.", at the top and down the tree.
func TestOldObjectCompletion(t *testing.T) {
	text := `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  validations:
    - expression: object.spec.replicas > 0
    - expression: oldObject.spec.replicas > 0
`
	c := start(t)
	initialize(t, c)
	c.open(text)
	complete := func(after string) []string {
		t.Helper()
		line, char := lineOf(t, text, after)
		res, rerr := c.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char + len(after)}})
		if rerr != nil {
			t.Fatalf("completion after %q error = %+v", after, rerr)
		}
		var items []struct{ Label string }
		if err := json.Unmarshal(res, &items); err != nil {
			t.Fatalf("completion result %s: %v", res, err)
		}
		labels := make([]string, len(items))
		for i, it := range items {
			labels[i] = it.Label
		}
		slices.Sort(labels)
		return labels
	}
	for _, tt := range []struct{ path, field string }{{"", "metadata"}, {"spec.", "replicas"}} {
		path := tt.path
		obj, old := complete("expression: object."+path), complete("expression: oldObject."+path)
		if !slices.Contains(obj, tt.field) {
			t.Errorf("completion after object.%s = %v, want it to offer %q", path, obj, tt.field)
		}
		if !slices.Equal(obj, old) {
			t.Errorf("completion after oldObject.%s = %v, want that after object.%s: %v", path, old, path, obj)
		}
	}
	shutdown(t, c)
}
