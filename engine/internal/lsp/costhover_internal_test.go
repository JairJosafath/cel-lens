package lsp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// costCRD is a CustomResourceDefinition with two rules, for the hovers of
// the cost tests.
const costCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names: {kind: Widget, plural: widgets}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              x-kubernetes-validations:
                - rule: "self.replicas <= 10"
                - rule: "self.tags.all(t, t.size() < 64)"
              properties:
                replicas: {type: integer}
                tags: {type: array, items: {type: string}}
`

// costClient is a client of a session served in the test, whose coster is
// run, for the cost tests.
type costClient struct {
	t *testing.T
	c *jsonrpc.Conn
}

// serveCosts serves a new session whose costs are estimated by coster and
// returns its client, initialized, with costCRD open and its diagnostics
// read.
func serveCosts(t *testing.T, coster func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Cost, error)) *costClient {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ss := newSession(t)
	ss.conn = jsonrpc.NewConn(serverIn, serverOut)
	ss.coster = coster
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- ss.serve(ctx, serverIn) }()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = serverOut.Close()
		<-served
	})
	cc := &costClient{t: t, c: jsonrpc.NewConn(clientIn, clientOut)}
	cc.request("init", "initialize", map[string]any{"initializationOptions": map[string]any{"experimental": []string{}}, "capabilities": map[string]any{}})
	cc.answer("init")
	cc.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": "file:///w/crd.yaml", "languageId": "yaml", "version": 1, "text": costCRD}})
	cc.diagnostics()
	return cc
}

func (cc *costClient) request(id, method string, params any) {
	cc.t.Helper()
	if err := cc.c.Request(id, method, params); err != nil {
		cc.t.Fatal(err)
	}
}

func (cc *costClient) notify(method string, params any) {
	cc.t.Helper()
	if err := cc.c.Notify(method, params); err != nil {
		cc.t.Fatal(err)
	}
}

// answer reads messages until the answer to the request id, and returns it.
func (cc *costClient) answer(id string) jsonrpc.Message {
	cc.t.Helper()
	for {
		if m := readMessage(cc.t, cc.c); m.Method == "" && string(m.ID) == `"`+id+`"` {
			return m
		}
	}
}

// diagnostics reads messages until the diagnostics of the CRD.
func (cc *costClient) diagnostics() {
	cc.t.Helper()
	readUntil(cc.t, cc.c, func(m jsonrpc.Message) bool { return m.Method == "textDocument/publishDiagnostics" })
}

// hover asks for the hover on the CRD's rule that holds needle, with the id
// given, without reading the answer.
func (cc *costClient) hover(id, text, needle string) {
	cc.t.Helper()
	line, char := lineAndColumn(cc.t, text, needle)
	cc.request(id, "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": "file:///w/crd.yaml"}, "position": map[string]any{"line": line, "character": char}})
}

// hoverText is the Markdown of the hover answered to id.
func (cc *costClient) hoverText(id string) string {
	cc.t.Helper()
	m := cc.answer(id)
	var h *struct{ Contents struct{ Value string } }
	if m.Error != nil || json.Unmarshal(m.Result, &h) != nil || h == nil {
		cc.t.Fatalf("hover %s = %s, %+v, want one", id, m.Result, m.Error)
	}
	return h.Contents.Value
}

// lineAndColumn is the 0-based line and column of the first needle in text.
func lineAndColumn(t *testing.T, text, needle string) (line, char int) {
	t.Helper()
	i := strings.Index(text, needle)
	if i < 0 {
		t.Fatalf("%q is not in the text", needle)
	}
	before := text[:i]
	line = strings.Count(before, "\n")
	return line, len(before) - strings.LastIndex(before, "\n") - 1
}

// counting is check.Costs counting its calls in n.
func counting(n *atomic.Int32) func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Cost, error) {
	return func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Cost, error) {
		n.Add(1)
		return check.Costs(ctx, file, data, opts...)
	}
}

// TestHoverCostsKept checks that the costs of a document are estimated by the
// first hover that needs them and kept: a hover asked again, on the same rule
// or another, estimates nothing; an edit, another Kubernetes version and other
// experimental features each drop them, and the next hover estimates again
// (#55).
func TestHoverCostsKept(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	cc := serveCosts(t, counting(&n))
	estimated := func(id, text, needle string, want int32, words string) {
		t.Helper()
		cc.hover(id, text, needle)
		got := cc.hoverText(id)
		if !strings.Contains(got, "**Cost:** estimated ") || !strings.Contains(got, words) {
			t.Errorf("hover %s =\n%s\nwant a Cost: paragraph with %q", id, got, words)
		}
		if c := n.Load(); c != want {
			t.Errorf("after hover %s the costs were estimated %d times, want %d", id, c, want)
		}
	}
	estimated("first", costCRD, "replicas <= 10", 1, "Kubernetes 1.37")
	estimated("again", costCRD, "replicas <= 10", 1, "Kubernetes 1.37")
	estimated("other rule", costCRD, "t.size()", 1, "(62%), half or more: doubling a limit it depends on can take it over. Why: `spec.tags` is a list with no `maxItems`")
	edited := strings.Replace(costCRD, "<= 10", "<= 20", 1)
	cc.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": "file:///w/crd.yaml", "version": 2}, "contentChanges": []map[string]any{{"text": edited}}})
	cc.diagnostics()
	estimated("edited", edited, "replicas <= 20", 2, "Kubernetes 1.37")
	estimated("edited again", edited, "replicas <= 20", 2, "Kubernetes 1.37")
	settings := func(version string, experimental []string) {
		cc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"kubernetesVersion": version, "experimental": experimental}})
		cc.diagnostics()
	}
	settings("1.34", []string{})
	estimated("version", edited, "replicas <= 20", 3, "Kubernetes 1.34")
	estimated("version again", edited, "replicas <= 20", 3, "Kubernetes 1.34")
	settings("1.34", []string{"kyverno"})
	estimated("features", edited, "replicas <= 20", 4, "Kubernetes 1.34")
	estimated("features again", edited, "replicas <= 20", 4, "Kubernetes 1.34")
}

// TestHoverCostsCancelled checks that a hover cancelled with $/cancelRequest
// while its costs are estimated is answered as cancelled, that the estimate
// stops and nothing is kept: the next hover estimates again and gets its
// Cost: paragraph (#55).
func TestHoverCostsCancelled(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	started, stopped := make(chan struct{}, 1), make(chan error, 1)
	cc := serveCosts(t, func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Cost, error) {
		if n.Add(1) == 1 {
			started <- struct{}{}
			<-ctx.Done() // the first estimate waits until it is stopped
			stopped <- ctx.Err()
			return nil, ctx.Err()
		}
		return check.Costs(ctx, file, data, opts...)
	})
	cc.hover("cancelled", costCRD, "replicas <= 10")
	<-started
	cc.notify("$/cancelRequest", map[string]any{"id": "cancelled"})
	if m := cc.answer("cancelled"); m.Error == nil || m.Error.Code != jsonrpc.CodeRequestCancelled {
		t.Fatalf("the cancelled hover was answered %s, %+v, want RequestCancelled", m.Result, m.Error)
	}
	if err := <-stopped; err == nil {
		t.Error("the estimate of the cancelled hover was not stopped")
	}
	cc.hover("next", costCRD, "replicas <= 10")
	if got := cc.hoverText("next"); !strings.Contains(got, "**Cost:** estimated ") {
		t.Errorf("hover after the cancelled one =\n%s\nwant its Cost: paragraph", got)
	}
	if c := n.Load(); c != 2 {
		t.Errorf("the costs were estimated %d times, want 2: the cancelled hover kept nothing", c)
	}
}

// TestHoverCostsDocumentChanged checks that hovers waiting for the costs of
// a version of a document that an edit replaced are not answered with text
// the client no longer has: the edit's own hover replaces the estimate, and
// the waiting hover is answered as cancelled (#55).
func TestHoverCostsDocumentChanged(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	release := make(chan struct{})
	cc := serveCosts(t, func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Cost, error) {
		if n.Add(1) == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return check.Costs(ctx, file, data, opts...)
	})
	cc.hover("old", costCRD, "replicas <= 10")
	edited := strings.Replace(costCRD, "<= 10", "<= 20", 1)
	cc.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": "file:///w/crd.yaml", "version": 2}, "contentChanges": []map[string]any{{"text": edited}}})
	cc.diagnostics()
	cc.hover("new", edited, "replicas <= 20")
	if m := cc.answer("old"); m.Error == nil || m.Error.Code != jsonrpc.CodeRequestCancelled {
		t.Errorf("the hover on the old text was answered %s, %+v, want it cancelled by the new one", m.Result, m.Error)
	}
	close(release)
	if got := cc.hoverText("new"); !strings.Contains(got, "**Cost:** estimated ") {
		t.Errorf("hover on the edited text =\n%s\nwant its Cost: paragraph", got)
	}
}

// TestHoverCostsContentModified checks that a hover whose estimate ends
// after an edit replaced its version of the document is answered with
// ContentModified, not with a hover of text the client no longer has, and
// that what was estimated is not kept for the new version (#55).
func TestHoverCostsContentModified(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	release := make(chan struct{})
	cc := serveCosts(t, func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Cost, error) {
		if n.Add(1) == 1 {
			<-release
		}
		return check.Costs(ctx, file, data, opts...)
	})
	cc.hover("old", costCRD, "replicas <= 10")
	edited := strings.Replace(costCRD, "<= 10", "<= 20", 1)
	cc.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": "file:///w/crd.yaml", "version": 2}, "contentChanges": []map[string]any{{"text": edited}}})
	cc.diagnostics()
	close(release)
	if m := cc.answer("old"); m.Error == nil || m.Error.Code != jsonrpc.CodeContentModified {
		t.Errorf("the hover on the old text was answered %s, %+v, want ContentModified", m.Result, m.Error)
	}
	cc.hover("new", edited, "replicas <= 20")
	if got := cc.hoverText("new"); !strings.Contains(got, "**Cost:** estimated ") {
		t.Errorf("hover on the edited text =\n%s\nwant its Cost: paragraph", got)
	}
	if c := n.Load(); c != 2 {
		t.Errorf("the costs were estimated %d times, want 2: those of the old text are not the new one's", c)
	}
}

// TestNearFixSizedOnce checks that the fix of a rule near its budget is sized
// only when asked, once per version of the document, and shared by the hover
// and the light bulb (#56): a hover on another rule sizes nothing, the first
// hover on the near rule sizes it, and a hover again and a code action there
// reuse it; an edit drops it.
func TestNearFixSizedOnce(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	var sized int
	ss.limiter = func(ctx context.Context, file string, data []byte, c check.Cost, budget uint64, opts ...check.Option) ([]check.Fix, bool, error) {
		sized++
		return check.Limits(ctx, file, data, c, budget, opts...)
	}
	const uri = "file:///w/crd.yaml"
	open := func(version int) {
		t.Helper()
		p, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri, "version": version, "text": costCRD}})
		if err := ss.didOpen(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	at := func(needle string) map[string]any {
		line, char := lineAndColumn(t, costCRD, needle)
		return map[string]any{"line": line, "character": char}
	}
	hoverOn := func(needle string) string {
		t.Helper()
		p, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri}, "position": at(needle)})
		res, err := ss.hover(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res)
		return string(b)
	}
	codeAction := func(needle string) []any {
		t.Helper()
		p, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri}, "range": map[string]any{"start": at(needle), "end": at(needle)}, "context": map[string]any{"triggerKind": 1}})
		res, err := ss.codeAction(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		return res.([]any) //nolint:forcetypeassert // codeAction answers a list
	}
	open(1)
	hoverOn("replicas <= 10")
	if sized != 0 {
		t.Errorf("a hover on a rule under half its budget sized %d fixes, want none", sized)
	}
	if got := hoverOn("t.size()"); !strings.Contains(got, "Fix: Add `maxItems: ") || sized != 1 {
		t.Errorf("the first hover on the near rule = %s, sized %d, want its fix, sized once", got, sized)
	}
	hoverOn("t.size()")
	if got := codeAction("t.size()"); len(got) != 1 || sized != 1 {
		t.Errorf("a hover again and a code action = %v, sized %d times, want the fix sized once", got, sized)
	}
	open(2)
	codeAction("t.size()")
	if sized != 2 {
		t.Errorf("after an edit the fix was sized %d times, want 2", sized)
	}
}

// TestHoverCostsSlotsBusy checks that a hover that would estimate, while
// every background slot is taken, is answered at once without Cost: and
// estimates nothing on the message loop (review of #62, decision 11).
func TestHoverCostsSlotsBusy(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	var estimated int
	ss.coster = func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Cost, error) {
		estimated++
		return check.Costs(ctx, file, data, opts...)
	}
	const uri = "file:///w/crd.yaml"
	p, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri, "version": 1, "text": costCRD}})
	if err := ss.didOpen(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	ss.jobs = map[int]*job{}
	for i := range maxJobs {
		ss.jobs[i] = &job{spec: jobSpec{command: "busy"}, cancel: func() {}}
	}
	line, char := lineAndColumn(t, costCRD, "replicas <= 10")
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char}})
	var answer any
	replied := false
	reply := func(res any, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		answer, replied = res, true
		return nil
	}
	if err := ss.answerHover(context.Background(), jsonrpc.Message{ID: json.RawMessage(`1`), Method: "textDocument/hover", Params: params}, reply); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(answer)
	if !replied || !strings.Contains(string(got), "**Uses:**") || strings.Contains(string(got), "**Cost:**") {
		t.Errorf("hover with every slot busy = %s, want the hover at once, without Cost:", got)
	}
	if estimated != 0 || len(ss.jobs) != maxJobs {
		t.Errorf("the hover estimated %d times and left %d jobs, want none estimated or started", estimated, len(ss.jobs))
	}
}

// TestHoverFixSlotsBusy checks that a hover on a near rule whose fix is not
// sized, while every background slot is taken, is answered at once with the
// costs that are known and without the fix, and sizes nothing (#56).
func TestHoverFixSlotsBusy(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	var sized int
	ss.limiter = func(ctx context.Context, file string, data []byte, c check.Cost, budget uint64, opts ...check.Option) ([]check.Fix, bool, error) {
		sized++
		return check.Limits(ctx, file, data, c, budget, opts...)
	}
	const uri = "file:///w/crd.yaml"
	p, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri, "version": 1, "text": costCRD}})
	if err := ss.didOpen(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.estimateNow(context.Background(), uri, ss.docs[uri]); err != nil {
		t.Fatal(err)
	}
	ss.jobs = map[int]*job{}
	for i := range maxJobs {
		ss.jobs[i] = &job{spec: jobSpec{command: "busy"}, cancel: func() {}}
	}
	line, char := lineAndColumn(t, costCRD, "t.size()")
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char}})
	var answer any
	if err := ss.answerHover(context.Background(), jsonrpc.Message{ID: json.RawMessage(`1`), Method: "textDocument/hover", Params: params}, func(res any, err error) error {
		answer = res
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(answer)
	if !strings.Contains(string(got), "half or more") || strings.Contains(string(got), "Fix:") || sized != 0 || len(ss.jobs) != maxJobs {
		t.Errorf("hover with every slot busy = %s, sized %d, %d jobs, want the near cost without its fix, nothing sized or started", got, sized, len(ss.jobs))
	}
}
