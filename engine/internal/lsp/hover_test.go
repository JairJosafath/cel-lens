package lsp_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// hoverCommands are the command ids a client that runs command links lists,
// as the VS Code extension does.
func hoverCommands() []string {
	return []string{"cel-lens.applyFix", "cel-lens.pickKubernetesVersion"}
}

// markdownText splits md into the text outside its code spans and fenced
// code blocks, with each backslash-escaped character replaced by a middle
// dot, and the contents of each code span or block. In the text, what is
// left is what Markdown can read as a link, HTML or structure.
func markdownText(md string) (text string, code []string) {
	var out strings.Builder
	lines := strings.SplitAfter(md, "\n")
	for i := 0; i < len(lines); i++ {
		if fence := fenceOf(lines[i]); fence > 0 {
			var block strings.Builder
			for i++; i < len(lines) && fenceOf(lines[i]) < fence; i++ {
				block.WriteString(lines[i])
			}
			code = append(code, block.String())
			continue
		}
		line := []rune(lines[i])
		for j := 0; j < len(line); j++ {
			switch {
			case line[j] == '\\' && j+1 < len(line) && line[j+1] != '\n':
				out.WriteRune('·')
				j++
			case line[j] == '`':
				n := run(line, j)
				end := -1
				for k := j + n; k < len(line); k++ {
					if line[k] == '`' {
						m := run(line, k)
						if m == n {
							end = k
							break
						}
						k += m - 1
					}
				}
				if end < 0 {
					out.WriteString(string(line[j : j+n]))
					j += n - 1
					continue
				}
				code = append(code, string(line[j+n:end]))
				j = end + n - 1
			default:
				out.WriteRune(line[j])
			}
		}
	}
	return out.String(), code
}

// fenceOf is the number of backticks that open or close a fenced code block
// on line, 0 when it does neither.
func fenceOf(line string) int {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return 0
	}
	if n := run([]rune(trimmed), 0); n >= 3 {
		return n
	}
	return 0
}

// run is the number of backticks in line from i on.
func run(line []rune, i int) int {
	n := 0
	for i+n < len(line) && line[i+n] == '`' {
		n++
	}
	return n
}

// commandLink finds a command: link in the text outside code: a link's or
// a reference's destination, an autolink or an href.
var commandLink = regexp.MustCompile(`(?:\]\(\s*<?|<|\]:\s*<?|href\s*=\s*["']?)command:([^\s)?"'>]+)`)

// commandLinks returns the id of every command a link in md runs, in order:
// what Markdown reads as a link, outside code spans and blocks and not
// escaped.
func commandLinks(md string) []string {
	text, _ := markdownText(md)
	var ids []string
	for _, m := range commandLink.FindAllStringSubmatch(text, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// unlistedLinks returns the ids of the commands md links to that listed
// does not have.
func unlistedLinks(md string, listed []string) []string {
	var bad []string
	for _, id := range commandLinks(md) {
		if !slices.Contains(listed, id) {
			bad = append(bad, id)
		}
	}
	return bad
}

// TestCommandLinks checks the helper that finds the command links of a
// hover: links, autolinks and references count, escaped text and code do
// not.
func TestCommandLinks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		md   string
		want []string
	}{
		{"[Fix](command:cel-lens.applyFix?%5B%5D \"Apply\")", []string{"cel-lens.applyFix"}},
		{"[a](<command:x.y>) and <command:z> and [r]: command:w", []string{"x.y", "z", "w"}},
		{`<a href="command:x">x</a>`, []string{"x"}},
		{`\[x\]\(command:workbench.action.terminal.new\)`, nil},
		{`\<command:x\>`, nil},
		{"`[x](command:in.code)`", nil},
		{"````'```' [x](command:in.code)````", nil},
		{"```cel\n[x](command:in.block)\n```", nil},
		{"[x](https://example.invalid/command:not)", nil},
	}
	for _, tt := range tests {
		if got := commandLinks(tt.md); !slices.Equal(got, tt.want) {
			t.Errorf("commandLinks(%q) = %q, want %q", tt.md, got, tt.want)
		}
	}
	md := "[a](command:cel-lens.applyFix) [b](command:workbench.action.terminal.new)"
	if got := unlistedLinks(md, hoverCommands()); !slices.Equal(got, []string{"workbench.action.terminal.new"}) {
		t.Errorf("unlistedLinks(%q) = %q, want the terminal command", md, got)
	}
	if got := unlistedLinks(md, nil); len(got) != 2 {
		t.Errorf("unlistedLinks(%q, none) = %q, want both", md, got)
	}
}

// TestHoverCommandLinks runs every hover test table with a client that
// lists the commands it runs from a hover and with one that lists none: a
// hover that links to a command the client does not list fails the table
// (see client.checkLinks) (#52).
func TestHoverCommandLinks(t *testing.T) {
	t.Parallel()
	// The hover test tables of the package, each run by a client that sends
	// hover as initializationOptions.hover, nil for none.
	tables := []struct {
		name string
		run  func(t *testing.T, hover map[string]any)
	}{
		{"rule hover", ruleHoverTable},
		{"hover escapes", hoverEscapesTable},
		{"hover escapes cel-lens's own key", hoverEscapesOwnKeyTable},
		{"types off hover", typesOffHoverTable},
		{"lens hover", lensHoverTable},
		{"checks left out", checksLeftOutTable},
	}
	for _, table := range tables {
		for _, cl := range []struct {
			name  string
			hover map[string]any
		}{
			{"a client listing the commands", map[string]any{"commands": hoverCommands(), "problems": "fixes"}},
			{"a client listing none", nil},
		} {
			t.Run(table.name+"/"+cl.name, func(t *testing.T) {
				t.Parallel()
				table.run(t, cl.hover)
			})
		}
	}
}

// hoverDocs has an expression of every kind the check checks: a CRD's rule
// and messageExpression; a ValidatingAdmissionPolicy's match condition,
// variable, validation, messageExpression and audit annotation; a
// MutatingAdmissionPolicy's match condition, variable and two mutations;
// and a Kyverno ValidatingPolicy's validation.
const hoverDocs = `apiVersion: apiextensions.k8s.io/v1
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
                - rule: "self.replicas <= 10 && self.name != 'a b'"
                  messageExpression: "'too many: ' + string(self.replicas)"
              properties:
                replicas: {type: integer}
                name: {type: string}
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: vap
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments], operations: [CREATE]}
  matchConditions:
    - name: not-system
      expression: "request.namespace != 'kube system'"
  variables:
    - name: many
      expression: "object.spec.replicas > 3"
  validations:
    - expression: "object.spec.replicas < 100 || variables.many"
      messageExpression: "'replicas: ' + string(object.spec.replicas)"
  auditAnnotations:
    - key: replicas
      valueExpression: "'seen ' + string(object.spec.paused)"
---
apiVersion: admissionregistration.k8s.io/v1beta1
kind: MutatingAdmissionPolicy
metadata:
  name: map
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments], operations: [CREATE]}
  matchConditions:
    - name: named
      expression: "object.metadata.name != 'x y'"
  variables:
    - name: want
      expression: "object.spec.minReadySeconds + 1"
  mutations:
    - patchType: ApplyConfiguration
      applyConfiguration:
        expression: "Object{spec: Object.spec{replicas: variables.want}}"
    - patchType: JSONPatch
      jsonPatch:
        expression: "[JSONPatch{op: 'add', path: '/spec/replicas', value: variables.want * 2}]"
---
apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: vpol
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments], operations: [CREATE]}
  validations:
    - expression: "object.spec.revisionHistoryLimit >= 1 && 'k v' != ''"
`

// hoverResult is a hover as the client reads it, nil for null.
type hoverResult *struct {
	Contents struct{ Kind, Value string }
	Range    struct{ Start, End struct{ Line, Character int } }
}

// hoverAtPos asks for the hover at line and char of the open document.
func (c *client) hoverAtPos(line, char int) hoverResult {
	c.t.Helper()
	res, rerr := c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char}})
	if rerr != nil {
		c.t.Fatalf("hover error = %+v", rerr)
	}
	var h hoverResult
	if err := json.Unmarshal(res, &h); err != nil {
		c.t.Fatalf("hover result %s: %v", res, err)
	}
	return h
}

// TestRuleHover is the first criterion of #52: every position inside an
// expression the check checks gets a hover, names, operators, literals,
// strings and spaces included, in each kind; the YAML key and a Kyverno
// policy with kyverno off get null.
func TestRuleHover(t *testing.T) {
	t.Parallel()
	ruleHoverTable(t, nil)
}

func ruleHoverTable(t *testing.T, hover map[string]any) {
	t.Helper()
	c := start(t)
	c.hover = hover
	initialize(t, c)
	c.open(hoverDocs)
	c.diagnostics()
	// Each expression, with its key, a name it reads, an operator, a
	// literal, the text inside a string, its Checks: or Returns: paragraph
	// and the line of Uses that follows (#52, #53).
	const (
		message = "**Returns:** the message the API server gives when the rule is false."
		object  = "`object`: `apps/v1 Deployment`"
	)
	exprs := []struct {
		kind, key, expr, name, op, literal, str, rule, uses string
	}{
		{
			"CRD rule", "rule", "self.replicas <= 10 && self.name != 'a b'", "replicas", "<=", "10", "a b",
			"**Checks:** `self.replicas` is at most `10`, and `self.name` does not equal `'a b'`.", "`self`: `Widget.spec` (object)",
		},
		{"CRD messageExpression", "messageExpression", "'too many: ' + string(self.replicas)", "string", "+", "", "too many", message, "`self`: `Widget.spec` (object)"},
		{
			"VAP match condition", "expression", "request.namespace != 'kube system'", "request", "!=", "", "kube system",
			"**Checks:** the policy applies only when `request.namespace` does not equal `'kube system'`.", "`request`: `kubernetes.AdmissionRequest`",
		},
		{"VAP variable", "expression", "object.spec.replicas > 3", "spec", ">", "3", "", "**Returns:** the value of `variables.many`.", object},
		{
			"VAP validation", "expression", "object.spec.replicas < 100 || variables.many", "many", "||", "100", "",
			"**Checks:** `object.spec.replicas` is less than `100`, or `variables.many` is true.", "\n\n- " + object,
		},
		{"VAP messageExpression", "messageExpression", "'replicas: ' + string(object.spec.replicas)", "object", "+", "", "replicas: ", message, object},
		{"VAP audit annotation", "valueExpression", "'seen ' + string(object.spec.paused)", "paused", "+", "", "seen", "**Returns:** the value of the audit annotation `replicas`.", object},
		{
			"MAP match condition", "expression", "object.metadata.name != 'x y'", "metadata", "!=", "", "x y",
			"**Checks:** the policy applies only when `object.metadata.name` does not equal `'x y'`.", object,
		},
		{"MAP variable", "expression", "object.spec.minReadySeconds + 1", "minReadySeconds", "+", "1", "", "**Returns:** the value of `variables.want`.", object},
		{
			"MAP apply configuration", "expression", "Object{spec: Object.spec{replicas: variables.want}}", "variables", ":", "", "",
			"**Returns:** the apply configuration the policy applies.", "`variables.want`: `int`",
		},
		{
			"MAP JSON patch", "expression", "[JSONPatch{op: 'add', path: '/spec/replicas', value: variables.want * 2}]", "want", "*", "2", "/spec/replicas",
			"**Returns:** the JSON patch the policy applies.", "`variables.want`: `int`",
		},
		{
			"Kyverno validation", "expression", "object.spec.revisionHistoryLimit >= 1 && 'k v' != ''", "revisionHistoryLimit", ">=", "1", "k v",
			"**Checks:** `object.spec.revisionHistoryLimit` is at least `1`, and `'k v'` does not equal `''`.", object,
		},
	}
	for _, ex := range exprs {
		line, start := lineOf(t, hoverDocs, ex.expr)
		end := start + len(ex.expr)
		at := func(s string) int {
			t.Helper()
			i := strings.Index(ex.expr, s)
			if i < 0 {
				t.Fatalf("%q not in %q", s, ex.expr)
			}
			return start + i
		}
		type spot struct {
			sort string
			char int
			name bool // on a name the part describes
		}
		// The space after the operator: the one before it ends the name
		// before, which the part describes, as it does today.
		space := at(ex.op+" ") + len(ex.op)
		spots := []spot{
			{"name", at(ex.name) + 1, true},
			{"operator", at(ex.op + " "), false},
			{"space", space, false},
		}
		if ex.literal != "" {
			spots = append(spots, spot{"literal", at(" "+ex.literal) + 1, false})
		}
		if ex.str != "" {
			spots = append(spots, spot{"inside a string", at(ex.str) + 1, false})
		}
		for _, sp := range spots {
			t.Run(ex.kind+"/"+sp.sort, func(t *testing.T) {
				c.use(t)
				h := c.hoverAtPos(line, sp.char)
				if h == nil {
					t.Fatalf("hover at %d:%d = null, want the rule's hover", line, sp.char)
				}
				md := h.Contents.Value
				uses := ex.rule + "\n\n**Uses:** " + ex.uses
				if strings.HasPrefix(ex.uses, "\n") {
					uses = ex.rule + "\n\n**Uses:**" + ex.uses
				}
				wantStart, wantEnd := start, end
				if sp.name {
					wantStart = at(ex.name)
					wantEnd = wantStart + len(ex.name)
					if !strings.HasPrefix(md, "```cel\n(") || !strings.Contains(md, "\n\n---\n\n"+uses) {
						t.Errorf("hover = %q, want the part, ---, then %q", md, uses)
					}
				} else if !strings.HasPrefix(md, uses) && (ex.kind != "CRD messageExpression" || !strings.Contains(md, "\n\n---\n\n"+uses)) {
					// The CRD's messageExpression is over its cost budget:
					// its hover starts with that problem, in the Fix block
					// (#56).
					t.Errorf("hover = %q, want it to start with %q", md, uses)
				}
				if r := h.Range; r.Start.Line != line || r.End.Line != line || r.Start.Character != wantStart || r.End.Character != wantEnd {
					t.Errorf("hover range = %+v, want %d:%d-%d", r, line, wantStart, wantEnd)
				}
				// Every hover ends with the pages that document it (#57).
				if i := strings.LastIndex(md, "\n\n**Docs:** ["); i < 0 || strings.Contains(md[i+2:], "\n") {
					t.Errorf("hover = %q, want it to end with the Docs: paragraph", md)
				}
			})
		}
		t.Run(ex.kind+"/the YAML key", func(t *testing.T) {
			c.use(t)
			keyAt := strings.LastIndex(strings.Split(hoverDocs, "\n")[line][:start], ex.key+":")
			if h := c.hoverAtPos(line, keyAt+1); h != nil {
				t.Errorf("hover on %s: = %q, want null", ex.key, h.Contents.Value)
			}
		})
	}
	t.Run("Kyverno with kyverno off", func(t *testing.T) {
		off := start(t)
		off.hover = hover
		initializeTypesOff(t, off, "", map[string]any{})
		off.open(hoverDocs)
		off.diagnostics()
		line, char := lineOf(t, hoverDocs, "object.spec.revisionHistoryLimit")
		if h := off.hoverAtPos(line, char+2); h != nil {
			t.Errorf("hover in a Kyverno policy with kyverno off = %q, want null", h.Contents.Value)
		}
	})
}

// escapesCRD has a field whose description holds a command link, an image
// and a coloured span, and a rule on a field whose name holds three
// backticks.
const escapesCRD = `apiVersion: apiextensions.k8s.io/v1
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
              properties:
                a` + "```" + `b:
                  type: object
                  x-kubernetes-validations:
                    - rule: "self.port > 0"
                  properties:
                    port:
                      type: integer
                      description: 'Port [x](command:workbench.action.terminal.new) <img src="https://example.invalid/a.png"> <span style="color:#f00;">red</span>'
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: quotes
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments], operations: [CREATE]}
  variables:
    - name: quote
      expression: "'` + "```" + `'"
  validations:
    - expression: "variables.quote != '` + "```" + `'"
`

// TestHoverEscapes is the escaping criterion of #52: what the hover takes
// from a file shows only escaped, or whole inside one code span or block:
// a description's command link, image and span, a field name and a CEL
// string holding three backticks.
func TestHoverEscapes(t *testing.T) {
	t.Parallel()
	hoverEscapesTable(t, nil)
}

func hoverEscapesTable(t *testing.T, hover map[string]any) {
	t.Helper()
	c := start(t)
	c.hover = hover
	initialize(t, c)
	c.open(escapesCRD)
	c.diagnostics()
	const (
		link  = "[x](command:workbench.action.terminal.new)"
		img   = `<img src="https://example.invalid/a.png">`
		span  = `<span style="color:#f00;">`
		ticks = "```"
	)
	tests := []struct {
		name, needle string
		n            int
		want         string
		// whole must each be inside one code span or block, escaped each
		// escaped in the text.
		whole, escaped []string
	}{
		{
			"a description, and a field name in Uses", "self.port > 0", len("self.po"),
			"```cel\n(field) port: int\n```\n\n" +
				`Port \[x\]\(command:workbench.action.terminal.new\) \<img src\="https://example.invalid/a.png"\> \<span style\="color:\#f00;"\>red\</span\>` +
				"\n\n---\n\n**Checks:** `self.port` is greater than `0`.\n\n**Uses:** `self`: ````Widget.spec.a```b```` (object), the value the rule is on." +
				"\n\n**Cost:** estimated 3 of the per-expression budget of 10M (under 1%). All rules of `v1`'s schema: 3 of 100M (under 1%). " + versionWords(hover) +
				"\n\n**Docs:** [Validation rules](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules) (Kubernetes 1.37)",
			[]string{"Widget.spec.a```b"},
			[]string{link, img, span},
		},
		{
			"a CEL string in a variable's description and in Uses", "variables.quote", len("variables.qu"),
			"```cel\n(field) quote: string\n```\n\nPolicy variable quote, defined in spec.variables by ````'```'````.\n\n---\n\n" +
				"**Checks:** `variables.quote` does not equal ````'```'````.\n\n" +
				"**Uses:** `variables.quote`: `string`, the policy variable defined by ````'```'````." +
				"\n\n**Cost:** the API server estimates no cost when a policy is applied; it stops an evaluation past 1,000,000. " + versionWords(hover) +
				"\n\n**Docs:** [Validation expressions](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/#validation-expression) (Kubernetes 1.37)",
			[]string{"'```'"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			line, char := lineOf(t, escapesCRD, tt.needle)
			h := c.hoverAtPos(line, char+tt.n)
			if h == nil {
				t.Fatal("hover = null")
			}
			md := h.Contents.Value
			if md != tt.want {
				t.Errorf("hover =\n%s\nwant\n%s", md, tt.want)
			}
			text, code := markdownText(withoutVersionLink(md))
			for _, bare := range []string{link, img, span, ticks, "<", "](command:"} {
				if strings.Contains(text, bare) {
					t.Errorf("hover = %q has %q outside code, unescaped", md, bare)
				}
			}
			for _, w := range tt.whole {
				if !slices.ContainsFunc(code, func(c string) bool { return strings.Contains(c, w) }) {
					t.Errorf("hover = %q, want %q whole inside a code span or block (code: %q)", md, w, code)
				}
			}
			for _, e := range tt.escaped {
				if escaped := escapeMarkdown(e); !strings.Contains(md, escaped) {
					t.Errorf("hover = %q, want %q escaped as %q", md, e, escaped)
				}
			}
		})
	}
}

// TestHoverEscapesOwnKey is the review of #52: a CRD file that sets the key
// cel-lens keeps its own Markdown descriptions under, x-cel-lens-markdown,
// puts nothing raw into the hover; the field's description shows, escaped.
func TestHoverEscapesOwnKey(t *testing.T) {
	t.Parallel()
	hoverEscapesOwnKeyTable(t, nil)
}

func hoverEscapesOwnKeyTable(t *testing.T, hover map[string]any) {
	t.Helper()
	crd := strings.Replace(escapesCRD, "                      description: 'Port",
		"                      x-cel-lens-markdown: '[run](command:workbench.action.terminal.new) <img src=\"https://example.invalid/t.png\">'\n                      description: 'Port", 1)
	c := start(t)
	c.hover = hover
	initialize(t, c)
	c.open(crd)
	c.diagnostics()
	line, char := lineOf(t, crd, "self.port > 0")
	h := c.hoverAtPos(line, char+len("self.po"))
	if h == nil {
		t.Fatal("hover = null")
	}
	md := h.Contents.Value
	want := "```cel\n(field) port: int\n```\n\n" +
		`Port \[x\]\(command:workbench.action.terminal.new\) \<img src\="https://example.invalid/a.png"\> \<span style\="color:\#f00;"\>red\</span\>` +
		"\n\n---\n\n"
	if !strings.HasPrefix(md, want) {
		t.Errorf("hover =\n%s\nwant it to start with\n%s", md, want)
	}
	if text, _ := markdownText(withoutVersionLink(md)); strings.Contains(text, "](command:") || strings.Contains(text, "<img") || strings.Contains(text, "t.png") {
		t.Errorf("hover = %q holds what the file wrote under x-cel-lens-markdown", md)
	}
}

// versionLink is the end of the Cost: paragraph for a client that runs
// cel-lens.pickKubernetesVersion from a hover.
const versionLink = `[Kubernetes 1.37](command:cel-lens.pickKubernetesVersion "Pick the Kubernetes version")`

// versionWords is how the Cost: paragraph ends for a client that sends hover
// as initializationOptions.hover: the version linked when it lists
// cel-lens.pickKubernetesVersion, plain otherwise.
func versionWords(hover map[string]any) string {
	if cmds, _ := hover["commands"].([]string); slices.Contains(cmds, "cel-lens.pickKubernetesVersion") {
		return versionLink
	}
	return "Kubernetes 1.37"
}

// withoutVersionLink is md without the link of the Cost: paragraph to the
// command that picks the Kubernetes version, cel-lens's own, so a test that
// no text from a file makes a link sees the others.
func withoutVersionLink(md string) string {
	return strings.ReplaceAll(md, versionLink, "Kubernetes 1.37")
}

// escapeMarkdown is s with a backslash before each character hover.Escape
// escapes, written out again so the test does not trust the code it tests.
func escapeMarkdown(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("\\`*_{}[]<>()#+-=!|~&", r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestHoverOptions checks that initializationOptions.hover is read: a list
// of commands and problems message or fixes are taken without a word, and
// each value of another form is ignored with one window/logMessage line.
func TestHoverOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		hover any
		logs  []string
	}{
		{"absent", nil, nil},
		{"commands and fixes", map[string]any{"commands": hoverCommands(), "problems": "fixes"}, nil},
		{"no commands and message", map[string]any{"commands": []string{}, "problems": "message"}, nil},
		{"wrong values", map[string]any{"commands": "cel-lens.applyFix", "problems": "both"}, []string{
			`hover: commands "cel-lens.applyFix" is not a list of command ids, ignored`,
			`hover: problems "both" is neither "message" nor "fixes", ignored`,
		}},
		{"not an object", 3, []string{"hover: 3 is not an object with commands and problems, ignored"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := start(t)
			opts := allExperimental()
			if tt.hover != nil {
				opts["hover"] = tt.hover
			}
			if _, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil, "initializationOptions": opts}); rerr != nil {
				t.Fatalf("initialize error = %+v", rerr)
			}
			_, _, seen := c.callSeeing("shutdown", nil)
			var logs []string
			for _, m := range seen {
				var p struct{ Message string }
				if m.Method == "window/logMessage" && json.Unmarshal(m.Params, &p) == nil && strings.HasPrefix(p.Message, "hover") {
					logs = append(logs, p.Message)
				}
				if m.Method == "window/showMessage" && strings.Contains(string(m.Params), "hover") {
					t.Errorf("the server showed %s, want only a log line", m.Params)
				}
			}
			if !slices.Equal(logs, tt.logs) {
				t.Errorf("log lines = %q, want %q", logs, tt.logs)
			}
		})
	}
}

// checksCRD has a rule with a misspelled field, which the check reports,
// and one whose && and || mix, which has no sentence.
const checksCRD = `apiVersion: apiextensions.k8s.io/v1
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
                - rule: "self.replicaz <= 10"
                - rule: "self.replicas >= 0 && (self.replicas < 10 || self.replicas == 100)"
                - rule: "self.replicas != 7"
              properties:
                replicas: {type: integer}
`

// TestChecksLeftOut is the Checks: criterion of #53: what a rule checks is
// left out when the file's check has an error inside the rule, and when
// there is no sentence for it; the other paragraphs stay.
func TestChecksLeftOut(t *testing.T) {
	t.Parallel()
	checksLeftOutTable(t, nil)
}

func checksLeftOutTable(t *testing.T, hover map[string]any) {
	t.Helper()
	c := start(t)
	c.hover = hover
	initialize(t, c)
	c.open(checksCRD)
	if _, diags := c.diagnostics(); len(diags) != 1 || !strings.Contains(diags[0].Message, "replicaz") {
		t.Fatalf("diagnostics = %+v, want one error about replicaz", diags)
	}
	const uses = "**Uses:** `self`: `Widget.spec` (object), the value the rule is on."
	tests := []struct{ name, rule, want string }{
		{"an error in the rule", "self.replicaz <= 10", uses},
		{"no sentence", "self.replicas >= 0 && (", uses},
		{"a sentence", "self.replicas != 7", "**Checks:** `self.replicas` does not equal `7`.\n\n" + uses},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			line, char := lineOf(t, checksCRD, tt.rule)
			// On the operator: no part, the rule's paragraphs alone.
			at := char + strings.IndexAny(tt.rule, "<>!")
			h := c.hoverAtPos(line, at)
			// The Cost: paragraph follows (#55).
			if h == nil || !strings.HasPrefix(h.Contents.Value, tt.want) || !strings.HasPrefix(h.Contents.Value[len(tt.want):]+"\n\n**Cost:**", "\n\n**Cost:**") {
				t.Errorf("hover on %q = %+v, want %q, then the Cost: paragraph", tt.rule, h, tt.want)
			}
		})
	}
}

// versionsCRD is a CustomResourceDefinition with the versions given, each a
// name and the rule on its root.
func versionsCRD(versions ...[2]string) string {
	var b strings.Builder
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: example.com\n  names: {kind: Widget, plural: widgets}\n  scope: Namespaced\n  versions:\n")
	for i, v := range versions {
		fmt.Fprintf(&b, "    - name: %s\n      served: true\n      storage: %t\n      schema:\n        openAPIV3Schema:\n          type: object\n          x-kubernetes-validations:\n            - rule: %q\n          properties:\n            size: {type: integer}\n", v[0], i == 0, v[1])
	}
	return b.String()
}

// TestHoverCostSchemaNames checks how Cost: names the schema whose total it
// gives, through the server (review of #62): a CRD with one version names
// it, though the API server moves its schema to spec.validation as a shared
// one; versions that share one schema are named together; a version with a
// schema of its own is named alone.
func TestHoverCostSchemaNames(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, text, needle, want string
	}{
		{"one version", versionsCRD([2]string{"v1", "self.size > 0"}), "self.size > 0", "All rules of `v1`'s schema: "},
		{"two versions, one schema", versionsCRD([2]string{"v1", "self.size > 0"}, [2]string{"v2", "self.size > 0"}), "self.size > 0", "All rules of the schema `v1` and `v2` share: "},
		{"two versions, two schemas", versionsCRD([2]string{"v1", "self.size > 0"}, [2]string{"v2", "self.size > 1"}), "self.size > 1", "All rules of `v2`'s schema: "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := start(t)
			initialize(t, c)
			c.open(tt.text)
			c.diagnostics()
			line, char := lineOf(t, tt.text, tt.needle)
			h := c.hoverAtPos(line, char+len("self.si"))
			if h == nil || !strings.Contains(h.Contents.Value, tt.want) {
				t.Errorf("hover = %+v, want Cost: with %q", h, tt.want)
			}
		})
	}
}

// TestHoverDocs is the Docs: paragraph of #57 in the hover: an expression
// that reads no variable gets a hover, the page of its rule, and on a
// function the rule's page then the function's.
func TestHoverDocs(t *testing.T) {
	t.Parallel()
	text := strings.Replace(checksDocsCRD, "%s", "1 < 2", 1) + strings.Replace(checksDocsRule, "%s", "size(self.name) > 0", 1)
	c := start(t)
	initialize(t, c)
	c.open(text)
	c.diagnostics()
	const rule = "[Validation rules](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules) (Kubernetes 1.37)"
	tests := []struct {
		name, needle string
		n            int
		want         string
	}{
		{"an expression that reads no variable", "1 < 2", len("1 "), "**Docs:** " + rule},
		{
			"a function", "size(self.name)", len("si"),
			"**Docs:** " + rule + " · [`size`, CEL list operators](https://github.com/google/cel-spec/blob/v0.25.2/doc/langdef.md#list-operators) (cel-spec v0.25.2)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			line, char := lineOf(t, text, tt.needle)
			h := c.hoverAtPos(line, char+tt.n)
			if h == nil || !strings.HasSuffix(h.Contents.Value, tt.want) {
				t.Errorf("hover on %q = %+v, want it to end with %q", tt.needle, h, tt.want)
			}
		})
	}
}

// checksDocsCRD is a CRD whose root has the rule %s; checksDocsRule adds
// another rule %s on the same root.
const (
	checksDocsCRD = `apiVersion: apiextensions.k8s.io/v1
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
            name: {type: string}
          x-kubernetes-validations:
            - rule: "%s"
`
	checksDocsRule = `            - rule: "%s"
`
)

// TestHoverDocsCost checks that Docs: adds the page about the cost of
// rules, Resource use by validation functions, to the hover of a rule at
// half its budget or more, over it included, and not to one under half
// (the review of #63, with #62's estimates).
func TestHoverDocsCost(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
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
                - rule: "self.tags.all(t, t.matches('^a.*$') && t.matches('^b.*$'))"
              properties:
                replicas: {type: integer}
                tags: {type: array, items: {type: string}}
`
	const page = "[Resource use by validation functions](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#resource-use-by-validation-functions)"
	c := start(t)
	initialize(t, c)
	c.open(crd)
	c.diagnostics()
	for _, tt := range []struct {
		name, rule, cost string
		heavy            bool
	}{
		{"under half", "self.replicas <= 10", "(under 1%)", false},
		{"near", "self.tags.all(t, t.size() < 64)", "half or more", true},
		{"over", "self.tags.all(t, t.matches(", "over the per-expression budget", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c.use(t)
			line, char := lineOf(t, crd, tt.rule)
			h := c.hoverAtPos(line, char+len("self.")) // off a name, the rule's paragraphs alone
			if h == nil || !strings.Contains(h.Contents.Value, tt.cost) {
				t.Fatalf("hover on %q = %+v, want a Cost: with %q", tt.rule, h, tt.cost)
			}
			_, docs, _ := strings.Cut(h.Contents.Value, "**Docs:** ")
			if got := strings.Contains(docs, page); got != tt.heavy {
				t.Errorf("Docs: of %q = %q, want the page about cost: %t", tt.rule, docs, tt.heavy)
			}
		})
	}
}

// TestHoverOptionEcho checks that initialize says what it honours of the
// hover option (review of cel-lens-vscode#57): with the option sent, as
// capabilities.experimental.celLens.hover, the ids of hover.commands the
// engine writes links to, once each in the client's order, and how it writes
// problems; without it, nothing, so a client trusts no link of an engine that
// does not escape file text.
func TestHoverOptionEcho(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		hover any // nil sends no hover option
		want  string
	}{
		{"no option", nil, ""},
		{
			"known and unknown ids",
			map[string]any{"commands": []string{"workbench.action.terminal.new", "cel-lens.applyFix", "cel-lens.pickKubernetesVersion", "cel-lens.applyFix", "cel-lens.setType"}, "problems": "fixes"},
			`{"commands":["cel-lens.applyFix","cel-lens.pickKubernetesVersion"],"problems":"fixes"}`,
		},
		{"no commands, message", map[string]any{"commands": []string{}, "problems": "message"}, `{"commands":[],"problems":"message"}`},
		{"only problems", map[string]any{"problems": "fixes"}, `{"commands":[],"problems":"fixes"}`},
		{"problems of another form", map[string]any{"commands": []string{"cel-lens.applyFix"}, "problems": "both"}, `{"commands":["cel-lens.applyFix"],"problems":"message"}`},
		{"an option that is no object", "yes", `{"commands":[],"problems":"message"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := start(t)
			opts := map[string]any{"experimental": []string{}}
			if tt.hover != nil {
				opts["hover"] = tt.hover
			}
			res, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil, "initializationOptions": opts})
			if rerr != nil {
				t.Fatalf("initialize error = %+v", rerr)
			}
			var r struct {
				Capabilities struct {
					Experimental struct {
						CelLens map[string]json.RawMessage
					}
				}
			}
			if err := json.Unmarshal(res, &r); err != nil {
				t.Fatal(err)
			}
			got, ok := r.Capabilities.Experimental.CelLens["hover"]
			if tt.want == "" {
				if ok {
					t.Errorf("celLens.hover = %s, want none without the option", got)
				}
				if _, has := r.Capabilities.Experimental.CelLens["kubernetesVersion"]; !has {
					t.Errorf("celLens = %v, want its other fields", r.Capabilities.Experimental.CelLens)
				}
				return
			}
			if string(got) != tt.want {
				t.Errorf("celLens.hover = %s, want %s", got, tt.want)
			}
		})
	}
}
