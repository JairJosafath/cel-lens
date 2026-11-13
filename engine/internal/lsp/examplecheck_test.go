package lsp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// limitedWidgetCRD is a CRD of widgets whose spec.size is at most 10.
const limitedWidgetCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
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
              properties:
                size: {type: integer, maximum: 10}
              x-kubernetes-validations:
                - rule: self.size >= 0
`

// sizedWidget is an example of limitedWidgetCRD with a spec.size of size.
func sizedWidget(size string) string {
	return "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\nspec:\n  size: " + size + "\n"
}

// published is one publishDiagnostics notification.
type published struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

// diagnosticsFor reads the server's messages until diagnostics for u that
// done accepts, and returns them with every publishDiagnostics read before.
func (c *client) diagnosticsFor(u string, done func(published) bool) (published, []published) {
	c.t.Helper()
	var before []published
	for {
		m := c.read("textDocument/publishDiagnostics for " + u)
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p published
		if err := json.Unmarshal(m.Params, &p); err != nil {
			c.t.Fatal(err)
		}
		if p.URI == u && done(p) {
			return p, before
		}
		before = append(before, p)
	}
}

// saying accepts diagnostics whose messages are want, in order.
func saying(want ...string) func(published) bool {
	return func(p published) bool {
		got := make([]string, len(p.Diagnostics))
		for i, d := range p.Diagnostics {
			got[i] = d.Message
		}
		return slices.Equal(got, want)
	}
}

// writeIn writes text to the file at path, making its folders.
func writeIn(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, text)
}

// TestExampleDiagnostics checks that the schema's problems show on the
// examples of a CRD, at the field, unless the example expects to be refused,
// and that they follow the examples and the CRD as they are opened, edited,
// closed, created, changed and deleted, the editor's text winning over the
// disk's.
func TestExampleDiagnostics(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	at := func(rel string) (path, uri string) {
		path = filepath.Join(dir, filepath.FromSlash(rel))
		return path, fileURI(path)
	}
	crdPath, crdURI := at("crd.yaml")
	samplePath, sampleURI := at("config/samples/widget.yaml")
	refusedPath, refusedURI := at("testdata/invalid/big.yaml")
	commentPath, commentURI := at("zz/refused.yaml")
	witnessPath, witnessURI := at("zz/witness.yaml")
	newPath, newURI := at("zz/new.yaml")
	writeIn(t, crdPath, limitedWidgetCRD)
	writeIn(t, samplePath, sizedWidget("20"))
	writeIn(t, refusedPath, sizedWidget("20"))
	writeIn(t, commentPath, "# cel-lens: refused \"less than or equal to 10\"\n"+sizedWidget("20"))
	writeIn(t, witnessPath, sizedWidget("30"))
	over := func(n string) string {
		return "spec.size: Invalid value: " + n + ": spec.size in body should be less than or equal to 10"
	}

	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	_, before := c.diagnosticsFor(witnessURI, saying(over("30")))
	var uris []string
	for _, p := range before {
		uris = append(uris, p.URI)
	}
	if !slices.Equal(uris, []string{sampleURI}) {
		t.Errorf("diagnostics sent before the last example's = %q, want only %q: the examples that expect to be refused (%s, %s) get none", uris, sampleURI, refusedURI, commentURI)
	}
	if d := before[0].Diagnostics; len(d) != 1 || d[0].Message != over("20") || d[0].Severity != 1 || d[0].Range.Start.Line != 5 || d[0].Range.Start.Character != 2 || d[0].Range.End.Character != 6 {
		t.Errorf("diagnostics of %s = %+v, want the schema's error on the key size, line 6", sampleURI, d)
	}

	t.Run("open, with the text on disk", func(t *testing.T) {
		c.use(t)
		c.openURI(sampleURI, sizedWidget("20"))
		c.diagnosticsFor(sampleURI, func(p published) bool { return p.Version == 1 && saying(over("20"))(p) })
	})
	t.Run("edited", func(t *testing.T) {
		c.use(t)
		c.changeURI(sampleURI, 2, sizedWidget("1"))
		c.diagnosticsFor(sampleURI, func(p published) bool { return p.Version == 2 && saying()(p) })
		c.changeURI(sampleURI, 3, sizedWidget("40"))
		c.diagnosticsFor(sampleURI, func(p published) bool { return p.Version == 3 && saying(over("40"))(p) })
	})
	t.Run("closed, the disk's", func(t *testing.T) {
		c.use(t)
		c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": sampleURI}})
		c.diagnosticsFor(sampleURI, saying(over("20")))
	})
	t.Run("the CRD edited", func(t *testing.T) {
		c.use(t)
		c.openURI(crdURI, strings.Replace(limitedWidgetCRD, "maximum: 10", "maximum: 100", 1))
		c.diagnosticsFor(sampleURI, saying())
	})
	t.Run("the CRD closed", func(t *testing.T) {
		c.use(t)
		c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": crdURI}})
		c.diagnosticsFor(sampleURI, saying(over("20")))
	})
	t.Run("a .cellens, no example", func(t *testing.T) {
		c.use(t)
		lensPath, lensURI := at(".cellens")
		writeIn(t, lensPath, sizedWidget("70"))
		c.openURI(lensURI, sizedWidget("70"))
		p, _ := c.diagnosticsFor(lensURI, func(published) bool { return true })
		for _, d := range p.Diagnostics {
			if strings.Contains(d.Message, "spec.size") {
				t.Errorf("diagnostic of the .cellens %q, want none of the schema's", d.Message)
			}
		}
		c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": lensURI}})
	})
	watched := func(u string, typ int) {
		c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": u, "type": typ}}})
	}
	t.Run("created", func(t *testing.T) {
		c.use(t)
		writeIn(t, newPath, sizedWidget("50"))
		watched(newURI, 1)
		c.diagnosticsFor(newURI, saying(over("50")))
	})
	t.Run("changed on disk", func(t *testing.T) {
		c.use(t)
		writeIn(t, newPath, sizedWidget(" 5")) // the same size
		watched(newURI, 2)
		c.diagnosticsFor(newURI, saying())
		writeIn(t, newPath, sizedWidget("60"))
		watched(newURI, 2)
		c.diagnosticsFor(newURI, saying(over("60")))
	})
	t.Run("deleted", func(t *testing.T) {
		c.use(t)
		if err := os.Remove(newPath); err != nil {
			t.Fatal(err)
		}
		watched(newURI, 3)
		c.diagnosticsFor(newURI, saying())
	})
	t.Run("the CRD deleted", func(t *testing.T) {
		c.use(t)
		if err := os.Remove(crdPath); err != nil {
			t.Fatal(err)
		}
		watched(crdURI, 3)
		_, before := c.diagnosticsFor(witnessURI, saying())
		if len(before) != 1 || before[0].URI != sampleURI || len(before[0].Diagnostics) != 0 {
			t.Errorf("diagnostics sent before the last example's = %+v, want none for %s", before, sampleURI)
		}
	})
	shutdown(t, c)
}

// TestExampleOfOpenCRD checks that a CRD the editor has open has its
// examples checked with crd-typed-policies off, and that a document in the
// CRD's own file is no example of it.
func TestExampleOfOpenCRD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crdPath := filepath.Join(dir, "crd.yaml")
	crdURI := fileURI(crdPath)
	samplePath := filepath.Join(dir, "sample.yaml")
	sampleURI := fileURI(samplePath)
	writeIn(t, samplePath, sizedWidget("20"))
	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": "file://" + filepath.ToSlash(dir), "name": "ws"}}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	c.openURI(crdURI, limitedWidgetCRD+"---\n"+sizedWidget("30"))
	if p, _ := c.diagnosticsFor(crdURI, func(published) bool { return true }); len(p.Diagnostics) != 0 {
		t.Errorf("diagnostics of the CRD = %+v, want none: the widget in its file is no example", p.Diagnostics)
	}
	c.diagnosticsFor(sampleURI, saying("spec.size: Invalid value: 20: spec.size in body should be less than or equal to 10"))
	shutdown(t, c)
}

// TestExampleOfClosedCRD checks that, with every experimental feature off,
// the examples of a CRD of the workspace that is not open are checked, and
// follow its file as it is created, changed and deleted on disk.
func TestExampleOfClosedCRD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crdPath := filepath.Join(dir, "config", "crd", "widgets.yaml")
	samplePath := filepath.Join(dir, "config", "samples", "widget.yaml")
	crdURI, sampleURI := fileURI(crdPath), fileURI(samplePath)
	writeIn(t, samplePath, sizedWidget("20"))
	writeIn(t, filepath.Join(dir, "other.yaml"), "apiVersion: v1\nkind: ConfigMap\n")
	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "initializationOptions": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": fileURI(dir), "name": "ws"}}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	watched := func(u string, typ int) {
		c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": u, "type": typ}}})
	}
	over := "spec.size: Invalid value: 20: spec.size in body should be less than or equal to 10"
	t.Run("created", func(t *testing.T) {
		c.use(t)
		writeIn(t, crdPath, limitedWidgetCRD)
		watched(crdURI, 1)
		c.diagnosticsFor(sampleURI, saying(over))
	})
	t.Run("changed", func(t *testing.T) {
		c.use(t)
		writeIn(t, crdPath, strings.Replace(limitedWidgetCRD, "maximum: 10", "maximum: 100", 1))
		watched(crdURI, 2)
		c.diagnosticsFor(sampleURI, saying())
		writeIn(t, crdPath, limitedWidgetCRD)
		watched(crdURI, 2)
		c.diagnosticsFor(sampleURI, saying(over))
	})
	t.Run("deleted", func(t *testing.T) {
		c.use(t)
		if err := os.Remove(crdPath); err != nil {
			t.Fatal(err)
		}
		watched(crdURI, 3)
		c.diagnosticsFor(sampleURI, saying())
	})
	shutdown(t, c)
}

// TestExampleOfClosedCRDAtStart checks that, with every experimental
// feature off, the examples of a CRD of the workspace that was never open
// are checked from the start.
func TestExampleOfClosedCRDAtStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	samplePath := filepath.Join(dir, "config", "samples", "widget.yaml")
	writeIn(t, filepath.Join(dir, "config", "crd", "widgets.yaml"), limitedWidgetCRD)
	writeIn(t, samplePath, sizedWidget("20"))
	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": fileURI(dir), "name": "ws"}}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	c.diagnosticsFor(fileURI(samplePath), saying("spec.size: Invalid value: 20: spec.size in body should be less than or equal to 10"))
	shutdown(t, c)
}
