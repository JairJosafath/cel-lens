package lsp

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// TestExampleResultsWait checks that cel-lens.exampleResults runs off the
// message loop: while it waits for the walk of the workspace, a hover on
// the rule is answered; cancelling the request, or a new Kubernetes
// version, answers it as cancelled at once; and a request made after waits
// for its results and gets them.
func TestExampleResultsWait(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const crd = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
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
              properties: {size: {type: integer}}
              x-kubernetes-validations: [{rule: self.size <= 10, message: at most 10}]
`
	for name, text := range map[string]string{
		"crd.yaml":    crd,
		"widget.yaml": "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec: {size: 11}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ss := newSession(t)
	ss.conn = jsonrpc.NewConn(serverIn, serverOut)
	// Every walk of a workspace folder waits until release is closed.
	release := make(chan struct{})
	var released sync.Once
	ss.fsys = func(dir string) fs.FS { <-release; return os.DirFS(dir) }
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- ss.serve(ctx, serverIn) }()
	t.Cleanup(func() {
		released.Do(func() { close(release) })
		cancel()
		_ = clientOut.Close()
		_ = serverOut.Close()
		<-served
	})
	c := jsonrpc.NewConn(clientIn, clientOut)
	request := func(id, method string, params any) {
		t.Helper()
		if err := c.Request(id, method, params); err != nil {
			t.Fatal(err)
		}
	}
	isAnswer := func(m jsonrpc.Message, id string) bool { return m.Method == "" && string(m.ID) == `"`+id+`"` }
	// next reads the messages up to the answer to one of ids, and returns it.
	next := func(ids ...string) jsonrpc.Message {
		t.Helper()
		for {
			m := readMessage(t, c)
			for _, id := range ids {
				if isAnswer(m, id) {
					return m
				}
			}
		}
	}

	request("init", "initialize", map[string]any{"capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": pathURI(dir)}}})
	next("init")
	crdURI := pathURI(filepath.Join(dir, "crd.yaml"))
	results := map[string]any{"command": commandExampleResults, "arguments": []any{map[string]any{"uri": crdURI}}}
	request("first", "workspace/executeCommand", results)
	if err := c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": crdURI, "languageId": "yaml", "version": 1, "text": crd}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(crd, "\n")
	line := len(lines) - 2
	request("hover", "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": crdURI}, "position": map[string]any{"line": line, "character": strings.Index(lines[line], "self.size") + 6}})
	if m := next("first", "hover"); !isAnswer(m, "hover") || m.Error != nil || !strings.Contains(string(m.Result), "size") {
		t.Fatalf("the server sent %+v, want the hover answered while exampleResults waits", m)
	}
	if err := c.Notify("$/cancelRequest", map[string]any{"id": "first"}); err != nil {
		t.Fatal(err)
	}
	if m := next("first"); m.Error == nil || m.Error.Code != jsonrpc.CodeRequestCancelled || !strings.Contains(m.Error.Message, "the client cancelled it") {
		t.Fatalf("exampleResults after $/cancelRequest = %s, %+v, want RequestCancelled", m.Result, m.Error)
	}
	// A new Kubernetes version stops it: its answer would be for the old
	// one, and a CRD it validates for the old one would make the loop's
	// checks for the new one wait (#14).
	request("old version", "workspace/executeCommand", results)
	if err := c.Notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"kubernetesVersion": "1.33"}}); err != nil {
		t.Fatal(err)
	}
	if m := next("old version"); m.Error == nil || m.Error.Code != jsonrpc.CodeRequestCancelled || !strings.Contains(m.Error.Message, "the Kubernetes version changed to 1.33") {
		t.Fatalf("exampleResults after a new kubernetesVersion = %s, %+v, want RequestCancelled", m.Result, m.Error)
	}
	request("second", "workspace/executeCommand", results)
	released.Do(func() { close(release) })
	m := next("second")
	var got ExampleResults
	if err := json.Unmarshal(m.Result, &got); err != nil || m.Error != nil {
		t.Fatalf("exampleResults = %s, %+v (%v), want an answer", m.Result, m.Error, err)
	}
	if len(got.Rules) != 1 || len(got.Rules[0].Results) != 1 || got.Rules[0].Results[0].Result != "refused" || got.Rules[0].Results[0].Example.File != "widget.yaml" {
		t.Errorf("exampleResults = %s, want widget.yaml refused by the one rule", m.Result)
	}
}

// TestExampleResultsUseLoopCheck checks that exampleResults, for a CRD the
// editor has open, takes the message loop's check of its text instead of
// checking it again in its job (the review of #9); a CRD that is not open
// is checked in the job.
func TestExampleResultsUseLoopCheck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const crd = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
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
              properties: {size: {type: integer}}
              x-kubernetes-validations: [{rule: self.size <= 10, message: at most 10}]
`
	for name, text := range map[string]string{
		"crd.yaml":    crd,
		"widget.yaml": "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec: {size: 11}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ss := newSession(t)
	ss.conn = jsonrpc.NewConn(serverIn, serverOut)
	// The loop's check finds a problem on the rule that the job's own
	// check would not.
	above, _, _ := strings.Cut(crd, "self.size <= 10")
	ruleLine := strings.Count(above, "\n") + 1
	ss.checker = func(_ context.Context, file string, _ []byte, _ ...check.Option) ([]check.Diagnostic, error) {
		at := extract.Position{Line: ruleLine, Column: 47}
		return []check.Diagnostic{{File: file, Message: "found by the loop", Severity: check.SeverityError, Start: at, End: at}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- ss.serve(ctx, serverIn) }()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = serverOut.Close()
		<-served
	})
	c := jsonrpc.NewConn(clientIn, clientOut)
	answer := func(id string) ExampleResults {
		t.Helper()
		if err := c.Request(id, "workspace/executeCommand", map[string]any{"command": commandExampleResults, "arguments": []any{map[string]any{"uri": pathURI(filepath.Join(dir, "crd.yaml"))}}}); err != nil {
			t.Fatal(err)
		}
		for {
			m := readMessage(t, c)
			if m.Method != "" || string(m.ID) != `"`+id+`"` {
				continue
			}
			var got ExampleResults
			if err := json.Unmarshal(m.Result, &got); err != nil || m.Error != nil || len(got.Rules) != 1 || len(got.Rules[0].Results) != 1 {
				t.Fatalf("exampleResults = %s, %+v (%v), want one rule's one result", m.Result, m.Error, err)
			}
			return got
		}
	}
	if err := c.Request("init", "initialize", map[string]any{"capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": pathURI(dir)}}}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, func(m jsonrpc.Message) bool { return string(m.ID) == `"init"` })
	if got := answer("closed").Rules[0].Results[0]; got.Result != "refused" {
		t.Errorf("exampleResults of the closed CRD = %+v, want refused, by the job's own check", got)
	}
	if err := c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": pathURI(filepath.Join(dir, "crd.yaml")), "languageId": "yaml", "version": 1, "text": crd}}); err != nil {
		t.Fatal(err)
	}
	if got := answer("open").Rules[0].Results[0]; got.Result != "not run" || !strings.HasSuffix(got.Reason, ": found by the loop") {
		t.Errorf("exampleResults of the open CRD = %+v, want not run with the loop's problem", got)
	}
}
