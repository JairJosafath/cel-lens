package lsp_test

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// fixCRD has a rule with a misspelled field, one near its cost budget and
// one over it, each on its own node.
const fixCRD = `apiVersion: apiextensions.k8s.io/v1
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
                - rule: "self.replcas >= 0"
              properties:
                replicas:
                  type: integer
                ports:
                  type: array
                  x-kubernetes-validations:
                    - rule: "self.all(p, p.port < 65536)"
                  items:
                    type: object
                    properties:
                      port: {type: integer}
                names:
                  type: array
                  x-kubernetes-validations:
                    - rule: "self.all(x, x.matches('^a.*$') && x.matches('^b.*$'))"
                  items:
                    type: string
`

// The rules of fixCRD near and over their budget, as the tests find them.
const (
	nearRule = "self.all(p, p.port < 65536)"
	overRule = "self.all(x, x.matches('^a.*$') && x.matches('^b.*$'))"
)

// fixLink matches a link of a hover to cel-lens.applyFix, capturing its
// argument.
var fixLink = regexp.MustCompile(`\]\(command:cel-lens\.applyFix\?([^ )]+) "`)

// fixClient starts a client that applies edits, sends hover as
// initializationOptions.hover, and has fixCRD open and checked.
func fixClient(t *testing.T, hover map[string]any) *client {
	t.Helper()
	c := start(t)
	c.hover = hover
	initializeIn(t, c, "", editing())
	c.open(fixCRD)
	c.diagnostics()
	return c
}

// hoverOn is the Markdown of the hover on needle's start in fixCRD.
func (c *client) hoverOn(needle string) string {
	c.t.Helper()
	line, char := lineOf(c.t, fixCRD, needle)
	h := c.hoverAtPos(line, char+1)
	if h == nil {
		c.t.Fatalf("hover on %q = null", needle)
	}
	return h.Contents.Value
}

// TestHoverFixBlock checks the Fix block and the near fix (#56), with each
// client: a problem at the cursor with its message when the client does not
// show it, its fixes as links to cel-lens.applyFix when the client runs it,
// else in words; an over-budget rule's fix in Fix, not again in Cost:; a near
// rule's in Cost:, with no Fix block.
func TestHoverFixBlock(t *testing.T) {
	t.Parallel()
	const (
		linked = "](command:cel-lens.applyFix?"
		words  = " (a quick fix)"
	)
	for _, cl := range []struct {
		name     string
		hover    map[string]any
		messages bool
		link     bool
	}{
		{"messages, no commands", nil, true, false},
		{"messages, commands", map[string]any{"commands": []string{"cel-lens.applyFix"}}, true, true},
		{"fixes only, commands", map[string]any{"commands": hoverCommands(), "problems": "fixes"}, false, true},
		{"fixes only, no commands", map[string]any{"problems": "fixes"}, false, false},
	} {
		t.Run(cl.name, func(t *testing.T) {
			t.Parallel()
			c := fixClient(t, cl.hover)
			fixWords := func(md, what string) {
				t.Helper()
				if cl.link && (!strings.Contains(md, "Fix: ["+what) || !strings.Contains(md, linked)) || !cl.link && (!strings.Contains(md, "Fix: "+what) || !strings.Contains(md, words)) {
					t.Errorf("hover =\n%s\nwant the fix %q, linked = %t", md, what, cl.link)
				}
			}
			// The Fix block comes before the rule's paragraphs.
			typo := c.hoverOn("replcas")
			fixBlock, _, _ := strings.Cut(typo, "**Uses:**")
			if hasMessage := strings.Contains(fixBlock, "undefined field 'replcas'"); hasMessage != cl.messages {
				t.Errorf("hover on the typo =\n%s\nwant the message in the Fix block: %t", typo, cl.messages)
			}
			fixWords(fixBlock, "Change to `replicas`")

			over := c.hoverOn(overRule)
			block, rest, _ := strings.Cut(over, "**Uses:**")
			fixWords(block, "Add `maxItems: ")
			_, cost, _ := strings.Cut(rest, "**Cost:**")
			if strings.Contains(cost, "Fix:") || !strings.Contains(cost, "over the per-expression budget") {
				t.Errorf("Cost: of the over-budget rule = %q, want it over budget and without its fix, which is in the Fix block", cost)
			}

			near := c.hoverOn(nearRule)
			_, cost, _ = strings.Cut(near, "**Cost:**")
			if strings.HasPrefix(near, "undefined") || strings.Contains(near, "\n\n---\n\nFix") {
				t.Errorf("hover on the near rule =\n%s\nwant no Fix block: it has no problem", near)
			}
			if !strings.Contains(cost, "half or more") {
				t.Fatalf("Cost: of the near rule = %q, want it near", cost)
			}
			fixWords(cost, "Add `maxItems: ")
			if !strings.Contains(cost, "to `spec.ports`") {
				t.Errorf("Cost: of the near rule = %q, want its fix on spec.ports", cost)
			}
		})
	}
}

// fixArgs decodes the argument of the first link to cel-lens.applyFix in md.
func fixArgs(t *testing.T, md string) map[string]any {
	t.Helper()
	m := fixLink.FindStringSubmatch(md)
	if m == nil {
		t.Fatalf("hover =\n%s\nwant a link to cel-lens.applyFix", md)
	}
	raw, err := url.PathUnescape(m[1])
	if err != nil {
		t.Fatal(err)
	}
	var args []map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil || len(args) != 1 {
		t.Fatalf("the link's argument %s = %v, %v, want a JSON array of one object", raw, args, err)
	}
	return args[0]
}

// TestApplyFix decodes the argument of each kind of fix link and runs
// cel-lens.applyFix with it (#56): the editor gets a workspace/applyEdit of the
// document's version, labelled with the fix's title, which writes the fix
// into the text: the name fix, an over-budget limit and a near limit.
func TestApplyFix(t *testing.T) {
	t.Parallel()
	c := fixClient(t, map[string]any{"commands": hoverCommands()})
	for _, tt := range []struct {
		name, needle, want string
	}{
		{"a name", "replcas", `"self.replicas >= 0"`},
		{"an over-budget limit", overRule, "names:\n                  maxItems: "},
		{"a near limit", nearRule, "ports:\n                  maxItems: "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			args := fixArgs(t, c.hoverOn(tt.needle))
			edit, res, rerr := c.command("cel-lens.applyFix", args, applied)
			if rerr != nil || edit == nil {
				t.Fatalf("applyFix(%v) = %+v, %s, %+v, want an edit", args, edit, res, rerr)
			}
			if edit.Label != args["title"] {
				t.Errorf("the edit's label = %q, want the fix's title %q", edit.Label, args["title"])
			}
			dc := edit.Edit.DocumentChanges
			if len(dc) != 1 || dc[0].TextDocument.URI != uri || dc[0].TextDocument.Version == nil || *dc[0].TextDocument.Version != 1 {
				t.Fatalf("the edit = %+v, want one change of version 1 of %s", edit.Edit, uri)
			}
			if got := apply(t, fixCRD, dc[0].Edits); !strings.Contains(got, tt.want) {
				t.Errorf("the edited text =\n%s\nwant it to hold %q", got, tt.want)
			}
			var answer struct{ Applied string }
			if err := json.Unmarshal(res, &answer); err != nil || answer.Applied != args["title"] {
				t.Errorf("applyFix answered %s, want the title it applied", res)
			}
		})
	}
}

// TestApplyFixRefused checks what cel-lens.applyFix refuses (#56): a version
// of the document that moved since the hover, a title the range has no fix
// of, a document that is not open, and a client that cannot apply an edit.
func TestApplyFixRefused(t *testing.T) {
	t.Parallel()
	c := fixClient(t, map[string]any{"commands": hoverCommands()})
	args := fixArgs(t, c.hoverOn("replcas"))
	with := func(key string, v any) map[string]any {
		out := map[string]any{}
		for k, x := range args {
			out[k] = x
		}
		out[key] = v
		return out
	}
	refused := func(name string, a map[string]any, want string) {
		t.Helper()
		edit, _, rerr := c.command("cel-lens.applyFix", a, applied)
		if edit != nil || rerr == nil || !strings.Contains(rerr.Message, want) {
			t.Errorf("applyFix(%s) = %+v, %+v, want an error with %q and no edit", name, edit, rerr, want)
		}
	}
	refused("an unknown title", with("title", "Change to `nope`"), "has no quick fix \"Change to `nope`\"")
	refused("a document that is not open", with("uri", "file:///other.yaml"), "is not open")
	c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 2}, "contentChanges": []map[string]any{{"text": fixCRD}}})
	c.diagnostics()
	refused("a version that moved", args, "the file changed since this hover; hover again")

	plain := start(t)
	plain.hover = map[string]any{"commands": hoverCommands()}
	initialize(t, plain)
	plain.open(fixCRD)
	plain.diagnostics()
	if edit, _, rerr := plain.command("cel-lens.applyFix", args, applied); edit != nil || rerr == nil || !strings.Contains(rerr.Message, "cannot apply an edit") {
		t.Errorf("applyFix from a client without workspace/applyEdit = %+v, %+v, want an error", edit, rerr)
	}
}

// TestNearQuickFix checks the near fix in the light bulb (#56): a code action
// of kind quickfix, with no diagnostic, for a range that touches a rule at
// half its budget or more, sized when the person asks; none for a rule
// under half; and none for a light bulb the editor asks for by itself before
// the costs are known, so typing pays for no estimate.
func TestNearQuickFix(t *testing.T) {
	t.Parallel()
	c := fixClient(t, nil)
	actions := func(needle string, trigger int) []map[string]any {
		t.Helper()
		line, char := lineOf(t, fixCRD, needle)
		ctx := map[string]any{"diagnostics": []any{}}
		if trigger > 0 {
			ctx["triggerKind"] = trigger
		}
		res, rerr := c.call("textDocument/codeAction", map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"range":        map[string]any{"start": map[string]int{"line": line, "character": char}, "end": map[string]int{"line": line, "character": char + 1}},
			"context":      ctx,
		})
		if rerr != nil {
			t.Fatalf("codeAction error = %+v", rerr)
		}
		var out []map[string]any
		if err := json.Unmarshal(res, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := actions(nearRule, 2); len(got) != 0 {
		t.Errorf("an automatic light bulb before the costs are known = %v, want nothing", got)
	}
	got := actions(nearRule, 1)
	if title, _ := got0(got)["title"].(string); len(got) != 1 || got[0]["kind"] != "quickfix" || got[0]["diagnostics"] != nil || !strings.HasPrefix(title, "Add `maxItems: ") {
		t.Fatalf("codeAction on the near rule = %v, want one quick fix that adds maxItems, with no diagnostic", got)
	}
	if again := actions(nearRule, 2); len(again) != 1 || again[0]["title"] != got[0]["title"] {
		t.Errorf("an automatic light bulb once the fix is sized = %v, want the same fix", again)
	}
	if typo := actions("replcas", 1); len(typo) != 1 || typo[0]["title"] != "Change to `replicas`" {
		t.Errorf("codeAction on the typo = %v, want only its name fix: the rule is under half its budget", typo)
	}
}

// got0 is the first of actions, or nil.
func got0(actions []map[string]any) map[string]any {
	if len(actions) == 0 {
		return nil
	}
	return actions[0]
}
