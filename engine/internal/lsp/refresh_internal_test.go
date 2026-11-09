package lsp

import (
	"context"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// refreshClient is a client of a session that takes
// workspace/codeLens/refresh and counts the refreshes it is sent.
type refreshClient struct {
	t    *testing.T
	conn *jsonrpc.Conn
	seq  int
}

// startRefresh serves ss to a client that takes workspace/codeLens/refresh,
// after initialize, until the test ends.
func startRefresh(t *testing.T, ss *session) *refreshClient {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ss.conn = jsonrpc.NewConn(serverIn, serverOut)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- ss.serve(ctx, serverIn) }()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = serverOut.Close()
		<-served
	})
	c := &refreshClient{t: t, conn: jsonrpc.NewConn(clientIn, clientOut)}
	if err := c.conn.Request("init", "initialize", map[string]any{"capabilities": map[string]any{"workspace": map[string]any{"codeLens": map[string]any{"refreshSupport": true}}}}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c.conn, func(m jsonrpc.Message) bool { return m.Method == "" && string(m.ID) == `"init"` })
	return c
}

// send sends a notification, or nothing when method is "", then asks for
// the code lenses of a document that is not open, which changes nothing,
// and returns how many refreshes the server sent before its answer.
func (c *refreshClient) send(method string, params any) int {
	c.t.Helper()
	if method != "" {
		if err := c.conn.Notify(method, params); err != nil {
			c.t.Fatal(err)
		}
	}
	return c.lenses("file:///none.yaml")
}

// lenses asks for the code lenses of the document at uri and returns how
// many refreshes the server sent before its answer.
func (c *refreshClient) lenses(uri string) int {
	c.t.Helper()
	c.seq++
	id := "lenses-" + strconv.Itoa(c.seq)
	if err := c.conn.Request(id, "textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": uri}}); err != nil {
		c.t.Fatal(err)
	}
	n := 0
	for {
		m := readMessage(c.t, c.conn)
		switch {
		case m.Method == "workspace/codeLens/refresh":
			n++
		case m.Method == "" && string(m.ID) == `"`+id+`"`:
			return n
		}
	}
}

// fakeRetry is a timer of retry that fires when the test sends on fire.
type fakeRetry struct {
	d       time.Duration
	fire    chan time.Time
	stopped atomic.Bool
}

// fakeRetries makes ss set its retries with fakeRetry, each of which it
// delivers on the channel returned as it is set. A retry's channel holds
// nothing: firing it is done once the message loop has taken it (see
// fireTaken), never while a message the test sent after may come first.
func fakeRetries(ss *session) <-chan *fakeRetry {
	set := make(chan *fakeRetry, 64)
	ss.retryTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
		r := &fakeRetry{d: d, fire: make(chan time.Time)}
		set <- r
		return r.fire, func() bool { return !r.stopped.Swap(true) }
	}
	return set
}

// fireTaken fires r and returns once the message loop has taken it: what
// the loop does for it, it does before the next message the test sends. A
// loop that never takes it fails the test after loopWait.
func (r *fakeRetry) fireTaken(t *testing.T) {
	t.Helper()
	timer := time.NewTimer(loopWait)
	defer timer.Stop()
	select {
	case r.fire <- time.Now():
	case <-timer.C:
		t.Fatalf("the message loop did not take the retry within %v", loopWait)
	}
}

// fireIgnored fires r and checks that the message loop does not take it,
// as it no longer waits for it.
func (r *fakeRetry) fireIgnored(t *testing.T, what string) {
	t.Helper()
	select {
	case r.fire <- time.Now():
		t.Errorf("the message loop took %s, which it no longer waits for", what)
	default:
	}
}

// loopWait bounds the wait for the message loop to take a retry.
const loopWait = 30 * time.Second

// retries returns the retries set since the last call.
func retries(set <-chan *fakeRetry) []*fakeRetry {
	var out []*fakeRetry
	for {
		select {
		case r := <-set:
			out = append(out, r)
		default:
			return out
		}
	}
}

func openDoc(uri, text string) (string, map[string]any) {
	return "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "yaml", "version": 1, "text": text}}
}

func changeDoc(uri string, version int, text string) (string, map[string]any) {
	return "textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": version}, "contentChanges": []map[string]any{{"text": text}}}
}

// TestRefreshHiddenTab is the review of #24: a document the client asked
// code lenses for once, as a tab shown then hidden, is owed its new lines
// for as long as it stays hidden, as the client never asks for them. Typing
// in the .cellens a comment, or anything that changes no entry, asks for
// nothing; only the .cellens opened, closed or changed on disk asks again,
// once each.
func TestRefreshHiddenTab(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crd, lens := pathURI(filepath.Join(dir, "crd.yaml")), pathURI(filepath.Join(dir, ".cellens"))
	ss := newSession(t)
	set := fakeRetries(ss) // none fires: the client's own messages only
	c := startRefresh(t, ss)
	c.send(openDoc(crd, manyFieldsCRD(1)))
	c.lenses(crd) // the tab is shown, then hidden
	entry := "types:\n  - file: crd.yaml\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    path: spec.f0\n    type: int\n"
	if n := c.send(openDoc(lens, "# note\n"+entry)); n != 1 {
		t.Errorf("refreshes after the .cellens opened, typing a value of crd.yaml = %d, want 1", n)
	}
	refreshes := 0
	for i := range 6 {
		refreshes += c.send(changeDoc(lens, i+2, "# note "+strings.Repeat("x", i+1)+"\n"+entry))
	}
	if refreshes != 0 {
		t.Errorf("refreshes after 6 edits of a comment of the .cellens = %d, want none", refreshes)
	}
	if got := retries(set); len(got) != 1 {
		t.Errorf("retries set after the .cellens opened and 6 edits of a comment = %d, want the one of its opening", len(got))
	}
	if n := c.send("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": lens, "type": 2}}}); n != 1 {
		t.Errorf("refreshes after the .cellens was saved = %d, want 1", n)
	}
}

// TestRefreshRetry is the review of #24: when the client dropped the
// refresh and sends nothing about the .cellens after it, as an extension
// that does not save it or a client that does not watch files, the server
// sends it again once, refreshRetry after the last one, unless the client
// has asked for the lines by then. The refresh sent again sets no retry of
// its own, so a client that never asks is not asked in a loop, and shutdown
// stops a retry set, so none follows it (review of #24).
func TestRefreshRetry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy, lens := pathURI(filepath.Join(dir, "policy.yaml")), pathURI(filepath.Join(dir, ".cellens"))
	ss := newSession(t)
	set := fakeRetries(ss)
	c := startRefresh(t, ss)
	c.send(openDoc(policy, variablesPolicy))
	c.lenses(policy)
	entry := func(typ string) string {
		return "types:\n  - file: policy.yaml\n    document: ValidatingAdmissionPolicy replica-limit\n    variable: params\n    type: \"{maxReplicas: " + typ + "}\"\n"
	}
	if n := c.send(openDoc(lens, entry("int"))); n != 1 {
		t.Fatalf("refreshes after the .cellens opened = %d, want 1", n)
	}
	got := retries(set)
	if len(got) != 1 || got[0].d != refreshRetry {
		t.Fatalf("retries set after the refresh = %d, want one of %v", len(got), refreshRetry)
	}
	// The client dropped the refresh and sends nothing more.
	got[0].fireTaken(t)
	if n := c.send("", nil); n != 1 {
		t.Errorf("refreshes when the retry fires with the lines owed = %d, want 1", n)
	}
	if again := retries(set); len(again) != 0 {
		t.Errorf("retries set by the refresh sent again = %d, want none", len(again))
	}
	got[0].fireIgnored(t, "a retry that fired")
	if n := c.send("", nil); n != 0 {
		t.Errorf("refreshes after a retry that fired fires again = %d, want none", n)
	}

	// A change whose refresh the client acts on: the retry is stopped.
	if n := c.send(changeDoc(lens, 2, entry("double"))); n != 1 {
		t.Fatalf("refreshes after the type changed = %d, want 1", n)
	}
	got = retries(set)
	if len(got) != 1 {
		t.Fatalf("retries set after the type changed = %d, want 1", len(got))
	}
	if n := c.lenses(policy); n != 0 || !got[0].stopped.Load() {
		t.Errorf("once the client asked for the lines: %d refreshes, retry stopped %t, want none and stopped", n, got[0].stopped.Load())
	}
	got[0].fireIgnored(t, "a stopped retry")
	if n := c.send("", nil); n != 0 {
		t.Errorf("refreshes after a stopped retry fires = %d, want none", n)
	}

	// Shutdown with a retry set: it is stopped, and no refresh follows it.
	if n := c.send(changeDoc(lens, 3, entry("int"))); n != 1 {
		t.Fatalf("refreshes after the type changed back = %d, want 1", n)
	}
	got = retries(set)
	if len(got) != 1 {
		t.Fatalf("retries set after the type changed back = %d, want 1", len(got))
	}
	if err := c.conn.Request("down", "shutdown", nil); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c.conn, func(m jsonrpc.Message) bool { return m.Method == "" && string(m.ID) == `"down"` })
	if !got[0].stopped.Load() {
		t.Error("the retry set at shutdown was not stopped")
	}
	got[0].fireIgnored(t, "a retry after shutdown")
	if n := c.send("", nil); n != 0 {
		t.Errorf("refreshes after shutdown, with a retry set = %d, want none", n)
	}
}
