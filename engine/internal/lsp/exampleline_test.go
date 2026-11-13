package lsp_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// lineCRD is a CRD of widgets with three rules on spec: a limit on the
// size, an immutable size, which needs an update, and a version that must
// be one, with isSemver, which the API server takes from 1.34 on.
const lineCRD = `apiVersion: apiextensions.k8s.io/v1
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
                size: {type: integer}
                count: {type: integer, maximum: 5}
                version: {type: string}
              x-kubernetes-validations:
                - rule: self.size <= 10
                  message: at most 10
                - rule: self.size == oldSelf.size
                  message: size is immutable
                - rule: "!has(self.version) || isSemver(self.version)"
                  message: not a version
`

// lineWidget is an example of lineCRD with spec, a YAML flow mapping.
func lineWidget(name, spec string) string {
	return "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: " + name + "}\nspec: " + spec + "\n"
}

// examplesPart returns the title of the part about examples of the line
// above the rule of the CRD at u whose text has needle, "" for none, and
// checks that it runs cel-lens.showExampleResults with the location
// cel-lens.showRule takes.
func (c *client) examplesPart(u, text, needle string) string {
	c.t.Helper()
	res, rerr := c.call("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": u}})
	if rerr != nil {
		c.t.Fatalf("codeLens error = %+v", rerr)
	}
	var lenses []codeLens
	if err := json.Unmarshal(res, &lenses); err != nil {
		c.t.Fatalf("codeLens result %s: %v", res, err)
	}
	line, _ := lineOf(c.t, text, needle)
	var rule map[string]any
	for _, l := range lenses {
		if l.Range.Start.Line != line {
			continue
		}
		switch l.Command.Command {
		case "cel-lens.showRule":
			rule = l.Command.Arguments[0]
		case "cel-lens.showExampleResults":
			if len(l.Command.Arguments) != 1 || !reflect.DeepEqual(l.Command.Arguments[0], rule) {
				c.t.Errorf("cel-lens.showExampleResults arguments = %v, want the rule's location %v", l.Command.Arguments, rule)
			}
			return l.Command.Title
		}
	}
	return ""
}

// hoverAt returns the Markdown of the hover at needle's start in the text
// of the document at u, "" for none.
func (c *client) hoverAt(u, text, needle string) string {
	c.t.Helper()
	line, char := lineOf(c.t, text, needle)
	res, rerr := c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char}})
	if rerr != nil {
		c.t.Fatalf("hover error = %+v", rerr)
	}
	var h *struct{ Contents struct{ Value string } }
	if err := json.Unmarshal(res, &h); err != nil {
		c.t.Fatalf("hover result %s: %v", res, err)
	}
	if h == nil {
		return ""
	}
	return h.Contents.Value
}

// cleared reads the server's messages until each of uris got an empty list
// of diagnostics.
func (c *client) cleared(uris ...string) {
	c.t.Helper()
	left := slices.Clone(uris)
	for len(left) > 0 {
		m := c.read("no diagnostics for each of " + strings.Join(left, ", "))
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p published
		if err := json.Unmarshal(m.Params, &p); err != nil {
			c.t.Fatal(err)
		}
		if len(p.Diagnostics) == 0 {
			left = slices.DeleteFunc(left, func(u string) bool { return u == p.URI })
		}
	}
}

// TestExampleLine checks what the rules of an open CRD do to its examples
// where a person looks (#15): a part of the line above each rule, which
// runs cel-lens.showExampleResults with the rule's location, a section of
// the rule's hover, and a warning on the rule and on the example for each
// wrong verdict, which follow the examples; the setting exampleResults
// takes them all back, and the schema's problems on the examples, and
// gives them back without a restart; a change of the Kubernetes version
// gives the results of the new one. The experimental features are off.
func TestExampleLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	at := func(rel string) (path, uri string) {
		path = filepath.Join(dir, filepath.FromSlash(rel))
		return path, fileURI(path)
	}
	crdPath, crdURI := at("config/crd/widgets.yaml")
	bigPath, bigURI := at("config/samples/big.yaml")
	_, countURI := at("config/samples/count.yaml")
	_, versionURI := at("config/samples/version.yaml")
	smallPath, smallURI := at("testdata/invalid/small.yaml")
	writeIn(t, crdPath, lineCRD)
	writeIn(t, filepath.Join(dir, "config", "samples", "ok.yaml"), lineWidget("ok", "{size: 3}"))
	writeIn(t, bigPath, lineWidget("big", "{size: 20}"))
	writeIn(t, filepath.Join(dir, "config", "samples", "count.yaml"), lineWidget("count", "{size: 1, count: 9}"))
	writeIn(t, filepath.Join(dir, "config", "samples", "version.yaml"), lineWidget("version", "{size: 2, version: x.y}"))
	writeIn(t, filepath.Join(dir, "testdata", "invalid", "huge.yaml"), lineWidget("huge", "{size: 30}"))

	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"capabilities": refreshCaps, "workspaceFolders": []map[string]any{{"uri": fileURI(dir), "name": "ws"}}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	c.openURI(crdURI, lineCRD)
	const (
		limit     = "self.size <= 10"
		immutable = "self.size == oldSelf.size"
		version   = "isSemver"
	)
	refuses := "refuses config/samples/big.yaml:1, which expects to be allowed: "
	p, _ := c.diagnosticsFor(crdURI, func(p published) bool { return len(p.Diagnostics) == 2 })
	limitLine, _ := lineOf(t, lineCRD, limit)
	for _, d := range p.Diagnostics {
		if d.Severity != 2 {
			t.Errorf("diagnostic of the CRD %+v, want a warning", d)
		}
	}
	if d := p.Diagnostics[0]; !strings.HasPrefix(d.Message, refuses) || !strings.Contains(d.Message, "at most 10") || d.Range.Start.Line != limitLine {
		t.Errorf("first diagnostic of the CRD = %+v, want %q... on the rule %s", d, refuses, limit)
	}
	if d := p.Diagnostics[1]; !strings.HasPrefix(d.Message, "refuses config/samples/version.yaml:1, which expects to be allowed: ") || !strings.Contains(d.Message, "not a version") {
		t.Errorf("second diagnostic of the CRD = %+v, want the version rule's refusal of version.yaml", d)
	}

	t.Run("the line", func(t *testing.T) {
		c.use(t)
		for _, tc := range []struct{ rule, want string }{
			{limit, "refuses 1 example it should allow · 4 pass"},
			{immutable, "not run: needs an update example"},
			{version, "refuses 1 example it should allow · 4 pass"},
		} {
			if got := c.examplesPart(crdURI, lineCRD, tc.rule); got != tc.want {
				t.Errorf("the line above %s ends with %q, want %q", tc.rule, got, tc.want)
			}
		}
	})
	t.Run("the hover", func(t *testing.T) {
		c.use(t)
		got := c.hoverAt(crdURI, lineCRD, limit)
		want := "**Examples:** refuses 1 example it should allow · 4 pass\n\n- [config/samples/big.yaml:1](<" + bigURI + "#L1>) **refused**, expected to be allowed: spec: Invalid value: "
		if !strings.Contains(got, "\n\n---\n\n"+want) || !strings.Contains(got, "testdata/invalid/huge.yaml:1") {
			t.Errorf("hover on %s =\n%s\nwant what self is, then\n%s...", limit, got, want)
		}
	})
	t.Run("the warning on the example", func(t *testing.T) {
		c.use(t)
		c.openURI(bigURI, lineWidget("big", "{size: 20}"))
		p, _ := c.diagnosticsFor(bigURI, func(p published) bool { return len(p.Diagnostics) == 1 })
		d := p.Diagnostics[0]
		if !strings.HasPrefix(d.Message, `expected to be allowed, but the rule "self.size <= 10" refuses it: `) || d.Severity != 2 || d.Range.Start.Line != 0 || d.Range.Start.Character != 0 {
			t.Errorf("diagnostic of big.yaml = %+v, want a warning at its first key that the rule refuses it", d)
		}
	})
	t.Run("an example nothing refuses", func(t *testing.T) {
		c.use(t)
		writeIn(t, smallPath, lineWidget("small", "{size: 1}"))
		c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": smallURI, "type": 1}}})
		c.diagnosticsFor(smallURI, saying("expected to be refused, but nothing refuses it"))
		if got := c.examplesPart(crdURI, lineCRD, limit); got != "2 wrong · 4 pass" {
			t.Errorf("the line above %s ends with %q, want 2 wrong · 4 pass, shortened", limit, got)
		}
	})
	t.Run("edited until right", func(t *testing.T) {
		c.use(t)
		c.changeURI(bigURI, 2, lineWidget("big", "{size: 5}"))
		c.diagnosticsFor(bigURI, func(p published) bool { return p.Version == 2 && len(p.Diagnostics) == 0 })
		c.diagnosticsFor(crdURI, func(p published) bool { return len(p.Diagnostics) == 1 })
		if got := c.examplesPart(crdURI, lineCRD, limit); got != "allows 1 example it should refuse · 5 pass" {
			t.Errorf("the line above %s ends with %q, want allows 1 example it should refuse · 5 pass", limit, got)
		}
	})
	t.Run("the CRD edited elsewhere", func(t *testing.T) {
		c.use(t)
		edited := "# widgets\n" + lineCRD
		c.changeURI(crdURI, 2, edited)
		// What is known of a rule stays above it until the check of the
		// new text ends, whenever this comes.
		if got := c.examplesPart(crdURI, edited, limit); got != "allows 1 example it should refuse · 5 pass" {
			t.Errorf("the line above %s after an edit above it ends with %q, want what was known", limit, got)
		}
		c.changeURI(crdURI, 3, lineCRD)
	})
	set := func(settings map[string]any) {
		c.notify("workspace/didChangeConfiguration", map[string]any{"settings": settings})
	}
	t.Run("off", func(t *testing.T) {
		c.use(t)
		set(map[string]any{"exampleResults": false})
		// The schema's problem on count.yaml goes too, as do the warnings.
		c.cleared(countURI, smallURI, versionURI, crdURI)
		if got := c.examplesPart(crdURI, lineCRD, limit); got != "" {
			t.Errorf("the line above %s with exampleResults false ends with %q, want no part about examples", limit, got)
		}
		if got := c.hoverAt(crdURI, lineCRD, limit); got == "" || strings.Contains(got, "Examples") {
			t.Errorf("hover on %s with exampleResults false =\n%s\nwant what self is alone", limit, got)
		}
		if _, _, rerr := c.command("cel-lens.exampleResults", map[string]any{"uri": crdURI}, nil); rerr == nil || !strings.Contains(rerr.Message, "exampleResults is false") {
			t.Errorf("cel-lens.exampleResults with exampleResults false error = %+v, want one saying the setting is off", rerr)
		}
	})
	t.Run("on again", func(t *testing.T) {
		c.use(t)
		set(map[string]any{"exampleResults": true})
		c.diagnosticsFor(countURI, saying("spec.count: Invalid value: 9: spec.count in body should be less than or equal to 5"))
		if got := c.examplesPart(crdURI, lineCRD, limit); got != "allows 1 example it should refuse · 5 pass" {
			t.Errorf("the line above %s with exampleResults true again ends with %q, want allows 1 example it should refuse · 5 pass", limit, got)
		}
	})
	t.Run("another Kubernetes version", func(t *testing.T) {
		c.use(t)
		set(map[string]any{"kubernetesVersion": "1.33"})
		// isSemver does not compile for 1.33: the API server refuses the
		// CRD for the rule, which then refuses version.yaml no more.
		c.cleared(versionURI)
		if got := c.examplesPart(crdURI, lineCRD, version); got != "not run: CRD refused" {
			t.Errorf("the line above %s with 1.33 ends with %q, want not run: CRD refused", version, got)
		}
		if got := c.examplesPart(crdURI, lineCRD, limit); got != "allows 1 example it should refuse · 5 pass" {
			t.Errorf("the line above %s with 1.33 ends with %q, want allows 1 example it should refuse · 5 pass", limit, got)
		}
	})
	shutdown(t, c)
}
