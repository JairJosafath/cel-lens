package lsp_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/scan"
)

// TestFoldedScalarSamePositions checks that the language server and
// `cel-lens-core check` underline the same place for a rule folded over
// several lines, with an escape and a line-end join before the typo.
func TestFoldedScalarSamePositions(t *testing.T) {
	const doc = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            size: {type: integer}
          x-kubernetes-validations:
            - message: m
              rule: "self.size != 4 && \"\\u00e9\" != \"\" && \
                self.size <=

                self.maxSiz"
`
	file := filepath.Join(t.TempDir(), "widget.yaml")
	if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := scan.Run(t.Context(), []string{file}, scan.JSON, &out); err != nil {
		t.Fatalf("Run(%s) error: %v", file, err)
	}
	var cli []struct{ Start, End struct{ Line, Column int } }
	if err := json.Unmarshal(out.Bytes(), &cli); err != nil || len(cli) != 1 {
		t.Fatalf("Run(%s) wrote %q (%v), want one diagnostic", file, out.String(), err)
	}

	c := start(t)
	initialize(t, c)
	c.open(doc)
	_, diags := c.diagnostics()
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %+v, want one", diags)
	}
	r, got := diags[0].Range, cli[0]
	if r.Start.Line+1 != got.Start.Line || r.Start.Character+1 != got.Start.Column ||
		r.End.Line+1 != got.End.Line || r.End.Character+1 != got.End.Column {
		t.Errorf("language server range = %+v, command line range = %+v, want the same", r, got)
	}
	if line, char := lineOf(t, doc, "self.maxSiz"); r.Start.Line != line || r.Start.Character != char+5 || r.End.Character != char+11 {
		t.Errorf("language server range = %+v, want maxSiz on line %d, from character %d", r, line+1, char+5)
	}
	shutdown(t, c)
}

// TestFoldedScalarCompletionEdit checks that a completion typed on the second
// line of a double-quoted rule edits that line, and that a quote in the
// inserted name is escaped.
func TestFoldedScalarCompletionEdit(t *testing.T) {
	const doc = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            'q"x': {type: string}
          x-kubernetes-validations:
            - message: m
              rule: "true &&
                self.q"
`
	c := start(t)
	initialize(t, c)
	c.open(doc)
	line, char := lineOf(t, doc, "self.q")
	res, rerr := c.call("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": char + len("self.q")},
	})
	if rerr != nil {
		t.Fatalf("completion error = %+v", rerr)
	}
	var items []struct {
		Label    string
		TextEdit struct {
			Range   struct{ Start, End struct{ Line, Character int } }
			NewText string
		}
	}
	if err := json.Unmarshal(res, &items); err != nil {
		t.Fatalf("completion result %s: %v", res, err)
	}
	for _, it := range items {
		if it.Label != `q"x` {
			continue
		}
		r := it.TextEdit.Range
		if it.TextEdit.NewText != `q\"x` || r.Start.Line != line || r.Start.Character != char+len("self.") || r.End.Line != line || r.End.Character != char+len("self.q") {
			t.Errorf("completion of q\"x = %+v, want q\\\"x over the q on line %d", it.TextEdit, line+1)
		}
		shutdown(t, c)
		return
	}
	t.Errorf("completion = %s, want an item q\"x", res)
}
