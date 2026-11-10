package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// TestJobsLeaveTheLoopFree checks that while typeChoices lists the Go types
// and searches the workspace for examples, the message loop goes on: a
// document opened meanwhile gets its diagnostics before the search ends, and
// its answer comes when it does (review of #17: the search held the loop for
// seconds). A typeChoices for another value replaces the one running, which
// is answered as cancelled at once (review of #17: each started a walk of
// its own); one for the same value waits for the same job and gets its
// answer, and cancelling it leaves the job to the others. One without a
// value, for the kinds and Go types, runs beside them, and both are answered
// (review of #17).
func TestJobsLeaveTheLoopFree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, text := range map[string]string{
		"policy.yaml": "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: limits}\nspec:\n  paramKind: {apiVersion: example.com/v1, kind: Limit}\n",
		"limit.yaml":  "apiVersion: example.com/v1\nkind: Limit\n",
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

	request("init", "initialize", map[string]any{"capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": pathURI(dir)}}})
	readUntil(t, c, func(m jsonrpc.Message) bool { return isAnswer(m, "init") })
	choices := func(variable string) map[string]any {
		return map[string]any{"command": commandTypeChoices, "arguments": []any{map[string]any{
			"cellens": pathURI(filepath.Join(dir, ".cellens")), "file": "policy.yaml", "document": "ValidatingAdmissionPolicy limits", "variable": variable,
		}}}
	}
	cancelled := func(id, why string) {
		t.Helper()
		m := readMessage(t, c)
		if !isAnswer(m, id) || m.Error == nil || m.Error.Code != jsonrpc.CodeRequestCancelled || !strings.Contains(m.Error.Message, why) {
			t.Fatalf("the server sent %+v, want %s answered as cancelled: %s", m, id, why)
		}
	}
	request("replaced", "workspace/executeCommand", choices("object"))
	request("search", "workspace/executeCommand", choices("params"))
	cancelled("replaced", "a newer cel-lens.typeChoices replaced it")
	request("twin", "workspace/executeCommand", choices("params"))
	request("kinds", "workspace/executeCommand", map[string]any{"command": commandTypeChoices})
	request("dropped", "workspace/executeCommand", choices("params"))
	if err := c.Notify("$/cancelRequest", map[string]any{"id": "dropped"}); err != nil {
		t.Fatal(err)
	}
	cancelled("dropped", "the client cancelled it")
	const opened = "file:///elsewhere/policy.yaml"
	if err := c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": opened, "languageId": "yaml", "version": 1,
		"text": "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: p}\nspec:\n  validations:\n    - expression: \"1 +\"\n",
	}}); err != nil {
		t.Fatal(err)
	}
	for {
		m := readMessage(t, c)
		if isAnswer(m, "search") {
			t.Fatal("typeChoices answered before its search could read the workspace")
		}
		if m.Method == "textDocument/publishDiagnostics" && strings.Contains(string(m.Params), opened) {
			break
		}
	}
	released.Do(func() { close(release) })
	answered := map[string]bool{}
	for !answered["search"] || !answered["twin"] || !answered["kinds"] {
		m := readMessage(t, c)
		if isAnswer(m, "dropped") || isAnswer(m, "replaced") {
			t.Fatalf("a cancelled request was answered twice: %+v", m)
		}
		if isAnswer(m, "kinds") {
			answered["kinds"] = true
			var got typeChoices
			if err := json.Unmarshal(m.Result, &got); err != nil || m.Error != nil || got.Examples != nil || got.GoTypes == nil || len(got.BuiltIn) == 0 {
				t.Errorf("typeChoices without a value = %s, %+v (%v), want the kinds and Go types, no examples", m.Result, m.Error, err)
			}
		}
		for _, id := range []string{"search", "twin"} {
			if !isAnswer(m, id) {
				continue
			}
			answered[id] = true
			var got typeChoices
			if err := json.Unmarshal(m.Result, &got); err != nil || got.Examples == nil || len(*got.Examples) != 1 || (*got.Examples)[0].File != "limit.yaml" || got.GoTypes == nil {
				t.Errorf("typeChoices %s = %s (%v), want limit.yaml as the one example, and the Go types", id, m.Result, err)
			}
		}
	}
}

// TestJobLimits checks that at most maxJobs jobs run in the background,
// those replaced or cancelled but still ending included, so quick
// replacements cannot pile up goroutines (review of #17), a request past them
// answered with RequestFailed, and that $/cancelRequest answers a job's
// request as cancelled.
func TestJobLimits(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	var out bytes.Buffer
	ss.conn = jsonrpc.NewConn(strings.NewReader(""), &out)
	ctx := context.Background()
	wait := func(ctx context.Context) (any, error) { <-ctx.Done(); return nil, ctx.Err() }
	start := func(id, command string) {
		t.Helper()
		if err := ss.startJob(ctx, jsonrpc.Message{ID: json.RawMessage(id), Method: "workspace/executeCommand"}, jobSpec{command: command, key: id, work: wait}); err != nil {
			t.Fatal(err)
		}
	}
	start("1", commandInferType)
	start("2", commandInferType) // replaces 1, which still ends
	start("3", commandTypeChoices)
	start("4", "cel-lens.third")
	start("5", "cel-lens.fourth")
	got := out.String()
	if !strings.Contains(got, `"id":1,"error":{"code":-32800,"message":"cel-lens.inferType was cancelled: a newer cel-lens.inferType replaced it"`) ||
		!strings.Contains(got, `"id":5,"error":{"code":-32803,"message":"cel-lens runs at most 4 commands in the background, those cancelled but still ending included, and 4 run`) || len(ss.jobs) != 4 {
		t.Errorf("five jobs wrote %s with %d jobs, want 1 replaced, 5 refused with RequestFailed and 4 jobs", got, len(ss.jobs))
	}
	out.Reset()
	if err := ss.finishJob(<-ss.jobDone); err != nil { // the replaced one ends
		t.Fatal(err)
	}
	start("6", "cel-lens.fourth")
	params, _ := json.Marshal(map[string]any{"id": 3})
	if err := ss.cancel(params); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, `"id":6`) || !strings.Contains(got, `"id":3,"error":{"code":-32800,"message":"cel-lens.typeChoices was cancelled: the client cancelled it"`) || len(ss.jobs) != 4 {
		t.Errorf("after the replaced job ended the server wrote %s with %d jobs, want 6 running and 3 answered as cancelled", got, len(ss.jobs))
	}
	out.Reset()
	ss.stopJobs()
	if out.Len() != 0 || len(ss.jobs) != 0 {
		t.Errorf("stopJobs wrote %s and left %d jobs, want nothing written and none left", out.String(), len(ss.jobs))
	}
}

// TestTypeChoicesListsAndSearchesAtOnce checks that the job of typeChoices
// with a value lists the Go types and searches for the examples at once, not
// one after the other (review of #17): the listing waits for the search to
// start.
func TestTypeChoicesListsAndSearchesAtOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, text := range map[string]string{
		"policy.yaml": "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: limits}\nspec:\n  paramKind: {apiVersion: example.com/v1, kind: Limit}\n",
		"limit.yaml":  "apiVersion: example.com/v1\nkind: Limit\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ss := newSession(t)
	var out bytes.Buffer
	ss.conn = jsonrpc.NewConn(strings.NewReader(""), &out)
	ss.folders = []string{dir}
	searching := make(chan struct{})
	var started sync.Once
	ss.fsys = func(d string) fs.FS { started.Do(func() { close(searching) }); return os.DirFS(d) }
	overlapped := false
	ss.goLister = func(ctx context.Context, _ func(string) fs.FS, _ []string, _ map[string]goFile) goListed {
		select {
		case <-searching:
			overlapped = true
		case <-ctx.Done():
		case <-time.After(time.Minute): // ends a failing run: the search waits for the listing
		}
		return goListed{types: []goChoice{}}
	}
	params, _ := json.Marshal(map[string]any{"command": commandTypeChoices, "arguments": []any{map[string]any{
		"cellens": pathURI(filepath.Join(dir, ".cellens")), "file": "policy.yaml", "document": "ValidatingAdmissionPolicy limits", "variable": "params",
	}}})
	msg := jsonrpc.Message{ID: json.RawMessage(`1`), Method: "workspace/executeCommand", Params: params}
	if err := ss.executeCommand(t.Context(), msg, func(_ any, err error) error { return err }); err != nil {
		t.Fatal(err)
	}
	if err := ss.finishJob(<-ss.jobDone); err != nil {
		t.Fatal(err)
	}
	if !overlapped || !strings.Contains(out.String(), `"file":"limit.yaml"`) {
		t.Errorf("typeChoices: overlapped %t, answer %s; want the listing to run while the search runs, and limit.yaml", overlapped, out.String())
	}
}

// TestShutdownAnswersJobs checks that shutdown stops the commands that still
// run, and answers them with an error, before it answers itself (#17).
func TestShutdownAnswersJobs(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	var out bytes.Buffer
	ss.conn = jsonrpc.NewConn(strings.NewReader(""), &out)
	ss.init = true
	ctx := context.Background()
	wait := func(ctx context.Context) (any, error) { <-ctx.Done(); return nil, ctx.Err() }
	if err := ss.startJob(ctx, jsonrpc.Message{ID: json.RawMessage(`7`), Method: "workspace/executeCommand"}, jobSpec{command: commandInferType, work: wait}); err != nil {
		t.Fatal(err)
	}
	if err := ss.handle(ctx, jsonrpc.Message{ID: json.RawMessage(`8`), Method: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	stopped, answered := strings.Index(got, `"id":7,"error"`), strings.Index(got, `"id":8,"result":null`)
	if stopped < 0 || answered < stopped || !strings.Contains(got, "the server shut down before the command ended") || len(ss.jobs) != 0 {
		t.Errorf("shutdown wrote %s with %d jobs left, want the command answered with an error, then shutdown", got, len(ss.jobs))
	}
}
