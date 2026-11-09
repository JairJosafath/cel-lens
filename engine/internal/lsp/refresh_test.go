package lsp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// limitExample is the example of the workspace #24 was found in: a
// ReplicaLimit, of the kind the params of replicaLimitPolicy are.
const limitExample = `apiVersion: rules.example.com/v1
kind: ReplicaLimit
metadata:
  name: team-a
maxReplicas: 5
team: team-a
`

// replay drives a session as VS Code does while the extension gives params
// of replicaLimitPolicy a type from limitExample, one message at a time, and
// records what the server sends back.
type replay struct {
	c                   *client
	policy, lens, entry string
	path                string // the .cellens on disk
	editID              json.RawMessage
	version, seq        int
	// typed is true once the policy's diagnostics with params typed came.
	typed bool
}

// step sends msg, then reads what the server sends for it, up to the answer
// to a codeLens request for a document that is not open, which changes
// nothing. It reports whether the server asked for the code lenses again
// after the policy's diagnostics with params typed came.
func (r *replay) step(msg map[string]any) (refreshed bool) {
	r.c.t.Helper()
	r.c.send(msg)
	r.seq++
	id := `"settled-` + strconv.Itoa(r.seq) + `"`
	r.c.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": "textDocument/codeLens", "params": map[string]any{"textDocument": map[string]any{"uri": "file:///none.yaml"}}})
	for {
		m := r.c.read("the messages for a step")
		switch {
		case m.Method == "textDocument/publishDiagnostics":
			var p struct {
				URI         string       `json:"uri"`
				Diagnostics []diagnostic `json:"diagnostics"`
			}
			if err := json.Unmarshal(m.Params, &p); err != nil {
				r.c.t.Fatal(err)
			}
			if p.URI == r.policy {
				r.typed = slices.ContainsFunc(p.Diagnostics, func(d diagnostic) bool { return strings.Contains(d.Message, "teem") })
			}
		case m.Method == "workspace/codeLens/refresh":
			refreshed = r.typed
		case m.Method == "" && string(m.ID) == id:
			return refreshed
		case m.Method == "" && m.Error != nil:
			r.c.t.Fatalf("a request of the step failed: %+v", m.Error)
		}
	}
}

// disk writes text to the .cellens, as the editor does when it creates the
// file and when it saves it.
func (r *replay) disk(text string) {
	r.c.t.Helper()
	if err := os.WriteFile(r.path, []byte(text), 0o600); err != nil {
		r.c.t.Fatal(err)
	}
}

// do takes one step of the editor, by name, and reports whether it is a
// message about the .cellens, and whether the server asked for the code
// lenses again after the policy's diagnostics with params typed came.
func (r *replay) do(name string) (aboutLens, refreshed bool) {
	r.c.t.Helper()
	notify := func(method string, params any) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	}
	open := func(text string) map[string]any {
		r.version++
		return notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": r.lens, "languageId": "yaml", "version": r.version, "text": text}})
	}
	watched := func(types ...int) map[string]any {
		var changes []map[string]any
		for _, typ := range types {
			changes = append(changes, map[string]any{"uri": r.lens, "type": typ})
		}
		return notify("workspace/didChangeWatchedFiles", map[string]any{"changes": changes})
	}
	switch name {
	case "applied": // the edit creates the file, empty, and its text waits in the editor
		r.disk("")
		return false, r.step(map[string]any{"jsonrpc": "2.0", "id": r.editID, "result": map[string]any{"applied": true}})
	case "open empty":
		return true, r.step(open(""))
	case "open entry":
		return true, r.step(open(r.entry))
	case "change entry":
		r.version++
		return true, r.step(notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": r.lens, "version": r.version}, "contentChanges": []map[string]any{{"text": r.entry}}}))
	case "save":
		r.disk(r.entry)
		return false, false
	case "close":
		return true, r.step(notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": r.lens}}))
	case "watched created":
		return true, r.step(watched(1))
	case "watched changed":
		return true, r.step(watched(2))
	case "watched created, created, changed":
		return true, r.step(watched(1, 1, 2))
	case "snippet": // where the extension shows the .cellens
		args := map[string]any{"cellens": r.lens, "file": "policy.yaml", "document": "ValidatingAdmissionPolicy replica-limit", "variable": "params", "value": "params", "snippet": "schema"}
		return false, r.step(map[string]any{"jsonrpc": "2.0", "id": "snippet", "method": "workspace/executeCommand", "params": executeCommand("cel-lens.setType", args)})
	case "lenses": // the client asks for the policy's lines, as a refresh makes it
		return false, r.step(map[string]any{"jsonrpc": "2.0", "id": "lenses", "method": "textDocument/codeLens", "params": map[string]any{"textDocument": map[string]any{"uri": r.policy}}})
	}
	r.c.t.Fatalf("no step %q", name)
	return false, false
}

// TestCodeLensRefreshAfterExample is #24: once the extension has written the
// schema an example gives params in a .cellens, the lines above the policy's
// rules say params: {apiVersion, …} without an edit. VS Code drops a
// workspace/codeLens/refresh whose request the editor of the policy has not
// made when it loses the focus, as it does when the extension shows the
// .cellens beside it, a moment after the edit; the messages that follow
// about the .cellens carry the text the server already has. So while the
// client has not asked for the lines that changed, each message about the
// .cellens asks again, whatever the order the editor sends them in, and once
// it has asked, the same text asks for nothing. A document the client never
// asked lines for, such as the example the extension read, does not keep
// asking. No workspace folder: no search runs in the background meanwhile.
func TestCodeLensRefreshAfterExample(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		steps []string
	}{
		{
			// The order VS Code 1.105 sent them in, the .cellens shown with
			// the second open.
			"as VS Code sends them",
			[]string{"applied", "open empty", "change entry", "save", "close", "snippet", "open entry", "watched created, created, changed", "close", "open entry"},
		},
		{
			"the entry opened before the edit is answered, the save seen late",
			[]string{"open entry", "applied", "save", "snippet", "watched changed"},
		},
		{
			"the file seen on disk, empty, before it opens",
			[]string{"applied", "watched created", "open entry", "save", "snippet", "watched changed"},
		},
		{
			"the save seen once closed, then shown",
			[]string{"applied", "open entry", "save", "close", "snippet", "watched created", "open entry"},
		},
		{
			// The client asks for the lines on the first refresh, and gets
			// those of the empty .cellens, the entry coming after.
			"the lines asked for before the entry came",
			[]string{"applied", "open empty", "lenses", "change entry", "save", "close", "snippet", "open entry"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "policy.yaml"), replicaLimitPolicy)
			if err := os.MkdirAll(filepath.Join(dir, "config", "samples"), 0o750); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, "config", "samples", "limit.yaml"), limitExample)
			r := &replay{c: start(t), policy: fileURI(filepath.Join(dir, "policy.yaml")), lens: fileURI(filepath.Join(dir, ".cellens")), path: filepath.Join(dir, ".cellens")}
			c := r.c
			initializeIn(t, c, "", map[string]any{"workspace": map[string]any{
				"applyEdit":     true,
				"workspaceEdit": map[string]any{"documentChanges": true, "resourceOperations": []string{"create"}},
				"codeLens":      map[string]any{"refreshSupport": true},
			}})
			c.openURI(r.policy, replicaLimitPolicy)
			if lines := c.codeLenses(r.policy); len(lines) == 0 || !slices.Contains(lines[0].titles, "params: dyn · Give it a type") {
				t.Fatalf("code lenses = %+v, want params: dyn · Give it a type", lines)
			}
			// The extension reads the example, and never shows it.
			c.openURI(fileURI(filepath.Join(dir, "config", "samples", "limit.yaml")), limitExample)
			res, rerr := c.call("workspace/executeCommand", executeCommand("cel-lens.inferType", map[string]any{"text": limitExample, "line": 1}))
			if rerr != nil {
				t.Fatalf("inferType error = %+v", rerr)
			}
			var inferred struct{ Schema json.RawMessage }
			if err := json.Unmarshal(res, &inferred); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"cellens": r.lens, "file": "policy.yaml", "document": "ValidatingAdmissionPolicy replica-limit", "variable": "params", "value": "params", "schema": inferred.Schema}
			c.send(map[string]any{"jsonrpc": "2.0", "id": "set", "method": "workspace/executeCommand", "params": executeCommand("cel-lens.setType", args)})
			m := c.until("workspace/applyEdit", isApplyEdit)
			var edit appliedEdit
			if err := json.Unmarshal(m.Params, &edit); err != nil {
				t.Fatal(err)
			}
			if changes := edit.Edit.DocumentChanges; len(changes) != 2 || changes[0].Kind != "create" {
				t.Fatalf("setType changes = %+v, want the creation of .cellens and its text", changes)
			}
			r.editID, r.entry = m.ID, apply(t, "", edit.Edit.DocumentChanges[1].Edits)

			last := ""
			for _, name := range tt.steps {
				aboutLens, refreshed := r.do(name)
				if !aboutLens {
					continue
				}
				last = name
				if r.typed && !refreshed {
					t.Errorf("after %q, with the policy's lines not asked for since they changed: no workspace/codeLens/refresh, want one", name)
				}
			}
			if !r.typed {
				t.Fatal("the policy's diagnostics never had params typed")
			}
			lines := c.codeLenses(r.policy)
			if len(lines) == 0 || !slices.Contains(lines[0].titles, "params: {apiVersion, …} · Edit") {
				t.Errorf("code lenses after %q = %+v, want params: {apiVersion, …} · Edit", last, lines)
			}
			for _, name := range []string{"watched changed", "close", "open entry"} {
				if _, refreshed := r.do(name); refreshed {
					t.Errorf("after %q, with the policy's lines asked for: workspace/codeLens/refresh, want none", name)
				}
			}
		})
	}
}
