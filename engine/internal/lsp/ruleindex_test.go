package lsp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ruleList is the answer of cel-lens.ruleList, as a client reads it.
type ruleList struct {
	KubernetesVersion string `json:"kubernetesVersion"`
	Documents         []struct {
		URI, Folder, File, Document, Kind, Name string
		Range                                   lspRange
		Rules                                   []listedRule
	}
}

type lspRange struct{ Start, End struct{ Line, Character int } }

// listedRule is a rule of the answer of cel-lens.ruleList.
type listedRule struct {
	ID, Document, Field, Name, Label, LabelFrom string
	Version, Path                               *string
	Index                                       int
	Location                                    struct {
		URI   string
		Range lspRange
	}
	Span    lspRange
	Compile struct {
		State    string
		Problems int
		Messages []struct{ Message, Severity string }
	}
}

// ruleList runs cel-lens.ruleList and returns its answer.
func (c *client) ruleList() ruleList {
	c.t.Helper()
	raw, rerr := c.call("workspace/executeCommand", map[string]any{"command": "cel-lens.ruleList"})
	if rerr != nil {
		c.t.Fatalf("cel-lens.ruleList error = %+v", rerr)
	}
	var l ruleList
	if err := json.Unmarshal(raw, &l); err != nil {
		c.t.Fatalf("cel-lens.ruleList = %s: %v", raw, err)
	}
	return l
}

// shortRules are the rules of l as the tests compare them: the document's
// folder and file, the rule's label, and its compile state and first
// message.
func (l ruleList) shortRules() []string {
	var out []string
	for _, d := range l.Documents {
		for _, r := range d.Rules {
			s := d.Folder + " | " + d.File + " | " + r.Label + " | " + r.Compile.State
			if len(r.Compile.Messages) > 0 {
				s += " | " + r.Compile.Messages[0].Message
			}
			out = append(out, s)
		}
	}
	return out
}

const listPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: replicas
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE], resources: [deployments]}
  validations:
    - expression: object.spec.replicas <= 5
      message: at most 5 replicas
    - expression: object.spec.nope > 0
`

// TestRuleList checks the answer of cel-lens.ruleList for a workspace of two
// folders, one inside the other, and open files (cel-lens-core#35): the
// documents with rules of the closed YAML files, read and checked in the
// background, each under the deepest folder that holds it, and of the open
// files, with the editor's text and the loop's check, one outside every
// folder last; in the order of the folders, then of the files' paths, then
// of the file; each rule with its compile state, an error, a warning (from
// the types a .cellens gives a closed file too) or ok. Files under hidden
// folders and node_modules, over 1 MiB, a symbolic link out of the folder
// and files with no rule are not listed.
func TestRuleList(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ws, inner, outside := filepath.Join(root, "ws"), filepath.Join(root, "ws", "inner"), filepath.Join(root, "outside")
	writeIn(t, filepath.Join(ws, "crd.yaml"), lensCRD)
	writeIn(t, filepath.Join(ws, ".cellens"), lensText)
	writeIn(t, filepath.Join(ws, "policies", "replicas.yaml"), listPolicy)
	writeIn(t, filepath.Join(ws, "edited.yaml"), strings.Replace(listPolicy, "name: replicas", "name: edited", 1))
	writeIn(t, filepath.Join(ws, "plain.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\n")
	writeIn(t, filepath.Join(inner, "x.yaml"), ruleCRD)
	writeIn(t, filepath.Join(ws, ".hidden", "h.yaml"), ruleCRD)
	writeIn(t, filepath.Join(ws, "node_modules", "n.yaml"), ruleCRD)
	writeIn(t, filepath.Join(ws, "big.yaml"), ruleCRD+"# "+strings.Repeat("x", 1<<20)+"\n")
	writeIn(t, filepath.Join(outside, "o.yaml"), ruleCRD)
	linked := os.Symlink(filepath.Join(outside, "o.yaml"), filepath.Join(ws, "link.yaml")) == nil
	c := start(t)
	if _, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "workspaceFolders": []map[string]any{
		{"uri": fileURI(ws), "name": "ws"}, {"uri": fileURI(inner), "name": "inner"},
	}}); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	// The search for .cellens files runs in the background: crd.yaml, open
	// until the type shows, is then closed.
	crdURI := fileURI(filepath.Join(ws, "crd.yaml"))
	c.openURI(crdURI, lensCRD)
	c.diagnosticsOf(crdURI, hasMessage(configWarning))
	// Each message the server sends is read before the next is written, so
	// neither waits for the other.
	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": crdURI}})
	c.diagnosticsOf(crdURI, none)
	edited := strings.Replace(strings.Replace(listPolicy, "name: replicas", "name: edited", 1), "object.spec.nope > 0", "object.spec.replicas > 0 &&", 1)
	anyDiagnostics := func([]diagnostic) bool { return true }
	c.openURI(fileURI(filepath.Join(ws, "edited.yaml")), edited)
	c.diagnosticsOf(fileURI(filepath.Join(ws, "edited.yaml")), anyDiagnostics)
	c.openURI(fileURI(filepath.Join(outside, "open.yaml")), strings.Replace(listPolicy, "name: replicas", "name: open", 1))
	c.diagnosticsOf(fileURI(filepath.Join(outside, "open.yaml")), anyDiagnostics)

	l := c.ruleList()
	wsURI, innerURI := fileURI(ws), fileURI(inner)
	const nope = "apps/v1 Deployment: undefined field 'nope'"
	want := []string{
		wsURI + " | crd.yaml | self.spec.config > 0 | ok",
		wsURI + " | crd.yaml | self.spec.config.startsWith('a') | warning | " + configWarning,
		wsURI + " | edited.yaml | at most 5 replicas | ok",
		wsURI + " | edited.yaml | object.spec.replicas > 0 && | error | Syntax error: mismatched input '<EOF>' expecting {'[', '{', '(', '.', '-', '!', 'true', 'false', 'null', NUM_FLOAT, NUM_INT, NUM_UINT, STRING, BYTES, IDENTIFIER}",
		wsURI + " | policies/replicas.yaml | at most 5 replicas | ok",
		wsURI + " | policies/replicas.yaml | object.spec.nope > 0 | warning | " + nope,
		innerURI + " | x.yaml | self.name.size() <= 3 | ok",
		innerURI + " | x.yaml | at most 10 | ok",
		innerURI + " | x.yaml | size is immutable | ok",
		innerURI + " | x.yaml | self.nope > 1 | error | undefined field 'nope'",
		" | " + filepath.ToSlash(filepath.Join(outside, "open.yaml")) + " | at most 5 replicas | ok",
		" | " + filepath.ToSlash(filepath.Join(outside, "open.yaml")) + " | object.spec.nope > 0 | warning | " + nope,
	}
	if got := l.shortRules(); !slices.Equal(got, want) {
		t.Errorf("cel-lens.ruleList rules =\n%s\nwant\n%s (symbolic link made: %t)", strings.Join(got, "\n"), strings.Join(want, "\n"), linked)
	}
	for _, d := range l.Documents {
		switch {
		case d.File == "edited.yaml" && d.URI != fileURI(filepath.Join(ws, "edited.yaml")),
			d.File == "x.yaml" && (d.URI != fileURI(filepath.Join(inner, "x.yaml")) || d.Document != "CustomResourceDefinition widgets.example.com" || d.Kind != "CustomResourceDefinition" || d.Name != "widgets.example.com"):
			t.Errorf("document %+v, want its file's URI, its kind and name", d)
		}
		for _, r := range d.Rules {
			if r.Location.URI != d.URI || !strings.HasPrefix(r.ID, d.URI+"#"+d.Document+"#") {
				t.Errorf("rule %+v of %s, want its location in the file and its id from the file's URI and the document", r, d.URI)
			}
		}
	}
	if l.KubernetesVersion == "" {
		t.Error("cel-lens.ruleList kubernetesVersion is empty")
	}
}

// TestRuleListMatchesDiagnostics checks that the compile states of the
// rules of an open file are what the diagnostics the server publishes for it
// say of them, without the warnings the check of the examples puts on a rule
// (cel-lens-core#35): for each rule, the state, the count and the first
// messages of the diagnostics that start in its entry.
func TestRuleListMatchesDiagnostics(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crdPath := filepath.Join(dir, "crd.yaml")
	text := ruleCRD + "---\n" + listPolicy + `    - expression: "object.spec.replicas +"
    - expression: object.spec.replicas > 0
      messageExpression: "1 + "
`
	writeIn(t, crdPath, text)
	// size 20 is refused by the rule "self.size <= 10": a warning on the rule.
	writeIn(t, filepath.Join(dir, "samples", "widget.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec:\n  size: 20\n")
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	u := fileURI(crdPath)
	c.openURI(u, text)
	const exampleWarning = "which expects to be allowed"
	diags := c.diagnosticsOf(u, func(ds []diagnostic) bool {
		return slices.ContainsFunc(ds, func(d diagnostic) bool { return strings.Contains(d.Message, exampleWarning) })
	})
	l := c.ruleList()
	in := func(d diagnostic, r lspRange) bool {
		after := func(a, b struct{ Line, Character int }) bool {
			return a.Line > b.Line || a.Line == b.Line && a.Character >= b.Character
		}
		return after(d.Range.Start, r.Start) && after(r.End, d.Range.Start)
	}
	rules := 0
	for _, doc := range l.Documents {
		for _, r := range doc.Rules {
			rules++
			state, problems := "ok", 0
			var messages []string
			for _, d := range diags {
				if strings.Contains(d.Message, exampleWarning) || !in(d, r.Span) {
					continue
				}
				problems++
				switch {
				case d.Severity == 1:
					state = "error"
				case state == "ok":
					state = "warning"
				}
				if len(messages) < 3 {
					messages = append(messages, d.Message)
				}
			}
			var got []string
			for _, m := range r.Compile.Messages {
				got = append(got, m.Message)
			}
			if r.Compile.State != state || r.Compile.Problems != problems || !slices.Equal(got, messages) {
				t.Errorf("rule %q compile = %s, %d, %q, want %s, %d, %q from the diagnostics", r.Label, r.Compile.State, r.Compile.Problems, got, state, problems, messages)
			}
		}
	}
	if rules != 8 {
		t.Errorf("cel-lens.ruleList = %d rules, want 8", rules)
	}
	if !slices.ContainsFunc(diags, func(d diagnostic) bool { return d.Severity == 1 }) || !slices.ContainsFunc(diags, func(d diagnostic) bool { return d.Severity == 2 && !strings.Contains(d.Message, exampleWarning) }) {
		t.Errorf("diagnostics = %+v, want errors and warnings of the check besides the example's", diags)
	}
}

// TestRuleListFollowsFolders checks that cel-lens.ruleList follows the
// workspace folders as they change (cel-lens-core#35, with #38): a folder
// added is listed, under its own folder, and a folder removed is gone.
func TestRuleListFollowsFolders(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	writeIn(t, filepath.Join(a, "crd.yaml"), ruleCRD)
	writeIn(t, filepath.Join(b, "policy.yaml"), listPolicy)
	c := start(t)
	initializeIn(t, c, a, map[string]any{})
	files := func() []string {
		var out []string
		for _, d := range c.ruleList().Documents {
			out = append(out, d.Folder+" "+d.File)
		}
		return out
	}
	if got, want := files(), []string{fileURI(a) + " crd.yaml"}; !slices.Equal(got, want) {
		t.Errorf("ruleList of folder a = %q, want %q", got, want)
	}
	c.changeFolders([]string{b}, nil)
	if got, want := files(), []string{fileURI(a) + " crd.yaml", fileURI(b) + " policy.yaml"}; !slices.Equal(got, want) {
		t.Errorf("ruleList once b is added = %q, want %q", got, want)
	}
	c.changeFolders(nil, []string{a})
	if got, want := files(), []string{fileURI(b) + " policy.yaml"}; !slices.Equal(got, want) {
		t.Errorf("ruleList once a is removed = %q, want %q", got, want)
	}
}

// TestRuleListUncompiled checks that a rule whose CEL the check did not
// compile is never listed ok (cel-lens-core#35): every rule of a CRD whose
// schema is not structural, which the API server compiles none of, and of a
// document the API server cannot read is an error, with the problem that
// stopped the check first in its messages, in a closed file and in an open
// one alike. With the schema fixed, the CRD's rules are compiled again.
func TestRuleListUncompiled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	notStructural := strings.Replace(ruleCRD, "                  items:\n                    type: object\n", "                  items:\n", 1)
	if notStructural == ruleCRD {
		t.Fatal("ruleCRD has no type: object under the ports' items")
	}
	unreadable := strings.Replace(listPolicy, "  name: replicas\n", "  name: replicas\n  creationTimestamp: notatime\n", 1)
	writeIn(t, filepath.Join(dir, "closed-crd.yaml"), notStructural)
	writeIn(t, filepath.Join(dir, "closed-policy.yaml"), unreadable)
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	for name, text := range map[string]string{"open-crd.yaml": notStructural, "open-policy.yaml": unreadable} {
		u := fileURI(filepath.Join(dir, name))
		c.openURI(u, text)
		c.diagnosticsOf(u, func([]diagnostic) bool { return true })
	}
	stoppers := map[string]string{"crd": "must not be empty for specified array items", "policy": "notatime"}
	rules := 0
	for _, d := range c.ruleList().Documents {
		stopper := stoppers[strings.TrimSuffix(strings.SplitN(d.File, "-", 2)[1], ".yaml")]
		for _, r := range d.Rules {
			rules++
			if r.Compile.State != "error" || r.Compile.Problems < 1 || len(r.Compile.Messages) == 0 || !strings.Contains(r.Compile.Messages[0].Message, stopper) {
				t.Errorf("%s: rule %q compile = %+v, want an error with %q first", d.File, r.Label, r.Compile, stopper)
			}
		}
	}
	if rules != 2*(4+2) {
		t.Errorf("cel-lens.ruleList = %d rules, want %d", rules, 2*(4+2))
	}
	writeIn(t, filepath.Join(dir, "closed-crd.yaml"), ruleCRD)
	for _, d := range c.ruleList().Documents {
		if d.File != "closed-crd.yaml" {
			continue
		}
		var states []string
		for _, r := range d.Rules {
			states = append(states, r.Compile.State)
		}
		if want := []string{"ok", "ok", "ok", "error"}; !slices.Equal(states, want) {
			t.Errorf("closed-crd.yaml with its schema fixed: states %q, want %q", states, want)
		}
	}
}
