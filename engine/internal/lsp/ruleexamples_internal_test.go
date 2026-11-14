package lsp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/inline"
)

// The workspace of the tests of the examples in the list.
const (
	// exampleCRD has a rule its examples refuse wrongly, one that needs
	// an update, one that errors on an example, and one they all pass.
	exampleCRD = `apiVersion: apiextensions.k8s.io/v1
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
              properties:
                size: {type: integer}
              x-kubernetes-validations:
                - rule: self.size <= 10
                  message: at most 10
                - rule: self.size == oldSelf.size
                  message: size is immutable
                - rule: 10 / self.size >= 0
                  message: divides
                - rule: self.size >= 0
                  message: non-negative
`
	// noExampleCRD has a rule that does not compile, and no example.
	noExampleCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: gadgets.example.com}
spec:
  group: example.com
  names: {plural: gadgets, kind: Gadget}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: self.nope > 0
              message: broken
`
	examplePolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: replicas}
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments]}
  variables:
    - name: replicas
      expression: object.spec.replicas
  validations:
    - expression: variables.replicas <= 5
      message: at most 5 replicas
    - expression: has(object.spec.nope) || true
      message: a warning
`
	exampleDeployment = "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: d, namespace: default}\nspec:\n  replicas: 3\n  selector: {matchLabels: {app: d}}\n  template: {metadata: {labels: {app: d}}, spec: {containers: [{name: c, image: nginx}]}}\n"
)

// sizedWidget is an example of exampleCRD of the size.
func sizedWidget(name string, size int) string {
	return "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: " + name + ", namespace: default}\nspec:\n  size: " + itoa(size) + "\n"
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// heldExamples is an exampleTimer whose waits are over at once, but while
// held: then the test ends them.
type heldExamples struct {
	mu    sync.Mutex
	hold  bool
	waits []chan time.Time
}

func (h *heldExamples) after(time.Duration) (<-chan time.Time, func() bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.hold {
		return fired(), func() bool { return true }
	}
	ch := make(chan time.Time, 1)
	h.waits = append(h.waits, ch)
	return ch, func() bool { return true }
}

// release ends the waits and holds no more.
func (h *heldExamples) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hold = false
	for _, ch := range h.waits {
		ch <- time.Time{}
	}
	h.waits = nil
}

// examplesOf are the examples of each rule of l, by its label, with the
// rule's problem flag; a rule with none has "-".
func examplesOf(l RuleList) map[string]string {
	out := map[string]string{}
	for _, d := range l.Documents {
		for _, r := range d.Rules {
			s := "-"
			if r.Examples != nil {
				s = r.Examples.State + " | " + r.Examples.Words
			}
			if r.Problem {
				s += " | problem"
			}
			out[r.Label] = s
		}
	}
	return out
}

// TestRuleListExamples checks what the examples of each rule say in the
// list (cel-lens-core#37): the state and the line's own words for a CRD's
// rules and a ValidatingAdmissionPolicy's validations, from the last check
// of the examples; none for a rule with no example, a policy's variable, a
// MutatingAdmissionPolicy, a policy of another version, a Kyverno policy,
// while exampleResults is off and while no check has run for the version in
// use; the problem flag; and that the list follows the check of the
// examples, as for an edit, with one generation that clears them all when
// exampleResults is turned off.
func TestRuleListExamples(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	write := func(rel, text string) { writeRuleFile(t, filepath.Join(ws, filepath.FromSlash(rel)), text) }
	write("crd.yaml", exampleCRD)
	write("gadgets.yaml", noExampleCRD)
	write("policy.yaml", examplePolicy)
	write("beta.yaml", strings.Replace(strings.Replace(examplePolicy, "/v1\n", "/v1beta1\n", 1), "name: replicas", "name: beta", 1))
	write("mutating.yaml", strings.Replace(listMAP, "add-label", "mutating", 1))
	write("kyverno.yaml", listKyverno)
	write("samples/small.yaml", sizedWidget("small", 3))
	write("samples/big.yaml", sizedWidget("big", 20))
	write("samples/zero.yaml", sizedWidget("zero", 0))
	write("samples/deployment.yaml", exampleDeployment)
	ss := newSession(t)
	held := &heldExamples{}
	ss.exampleTimer = held.after
	fc := serveFollowing(t, ss, []string{ws}, map[string]any{"ruleList": true, "experimental": []string{"kyverno"}})

	// until waits for the notifications until the list holds what want
	// accepts, and returns it.
	gen := int64(0)
	until := func(what string, want func(map[string]string) bool) RuleList {
		t.Helper()
		for range 20 {
			gen = fc.note(func(n ruleNote) bool { return n.Generation > gen && n.Scan.State == scanDone }).Generation
			if l := fc.list(nil); want(examplesOf(l)) {
				return l
			}
		}
		t.Fatalf("the list never held %s", what)
		return RuleList{}
	}
	hasExamples := func(m map[string]string) bool { return m["at most 10"] != "-" && m["at most 5 replicas"] != "-" }
	l := until("the examples", hasExamples)
	want := map[string]string{
		"at most 10":         inline.ExamplesWrong + " | refuses 1 example it should allow · 2 pass | problem",
		"size is immutable":  inline.ExamplesNotRun + " | not run: needs an update example",
		"divides":            inline.ExamplesError + " | 1 example errors · 2 pass | problem",
		"non-negative":       inline.ExamplesPass + " | 3 examples pass",
		"broken":             "- | problem", // no example; does not compile
		"replicas":           "-",           // a variable
		"at most 5 replicas": inline.ExamplesPass + " | 1 example passes · under no binding",
		"a warning":          inline.ExamplesPass + " | 1 example passes · under no binding | problem", // a compile warning
		// A policy of v1beta1, a MutatingAdmissionPolicy and a Kyverno policy.
		"a team label is required": "-",
		"has-labels":               "-",
		"team":                     "-",
	}
	got := examplesOf(l)
	for label, w := range want {
		if got[label] != w {
			t.Errorf("rule %q = %q, want %q", label, got[label], w)
		}
	}
	for _, d := range l.Documents {
		if d.Name == "beta" {
			for _, r := range d.Rules {
				if r.Examples != nil {
					t.Errorf("rule %q of the v1beta1 policy has examples %+v, want none", r.Label, r.Examples)
				}
			}
		}
	}

	// The words are the line's: the part about the examples of the line
	// above each rule of the CRD, open.
	crdURI := pathURI(filepath.Join(ws, "crd.yaml"))
	fc.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": crdURI, "languageId": "yaml", "version": 1, "text": exampleCRD}})
	m := fc.request("textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": crdURI}})
	var lenses []struct {
		Command inline.Part `json:"command"`
	}
	if err := json.Unmarshal(m.Result, &lenses); err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, ln := range lenses {
		if ln.Command.Command == inline.ShowExampleResults {
			parts = append(parts, ln.Command.Title)
		}
	}
	wantParts := []string{"refuses 1 example it should allow · 2 pass", "not run: needs an update example", "1 example errors · 2 pass", "3 examples pass"}
	if strings.Join(parts, "\n") != strings.Join(wantParts, "\n") {
		t.Errorf("the lines' parts = %q, want the list's words %q", parts, wantParts)
	}

	// An example changed: the check of the examples ends, and the file of
	// the CRD gets a new generation.
	before := gen
	write("samples/big.yaml", sizedWidget("big", 5))
	fc.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": pathURI(filepath.Join(ws, "samples", "big.yaml")), "type": fileChanged}}})
	until("the example changed", func(m map[string]string) bool { return strings.HasPrefix(m["at most 10"], inline.ExamplesPass) })
	if since := fc.list(&before); !strings.Contains(strings.Join(files(since), " "), "crd.yaml") {
		t.Errorf("ruleList since %d = files %q, want crd.yaml, whose examples changed", before, files(since))
	}

	// exampleResults off clears every examples in one generation.
	before = gen
	fc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"exampleResults": false}})
	off := fc.note(func(n ruleNote) bool { return n.Generation > before })
	l = fc.list(&before)
	if off.Generation != before+1 || l.Generation != off.Generation {
		t.Errorf("exampleResults off: generation %d, then %d, want one: %d", off.Generation, l.Generation, before+1)
	}
	for label, s := range examplesOf(fc.list(nil)) {
		if !strings.HasPrefix(s, "-") {
			t.Errorf("with exampleResults off, rule %q = %q, want no examples", label, s)
		}
	}
	gen = off.Generation

	// On again, they come back when the check ends.
	fc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"exampleResults": true}})
	until("the examples again", hasExamples)

	// Another Kubernetes version: none until the check of the examples for
	// it ends, never those of the version before.
	held.mu.Lock()
	held.hold = true
	held.mu.Unlock()
	fc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"kubernetesVersion": "1.33"}})
	l = until("no examples for 1.33", func(m map[string]string) bool {
		return m["at most 10"] == "-" || strings.HasPrefix(m["at most 10"], "- ")
	})
	if l.KubernetesVersion != "1.33" {
		t.Errorf("list for version %s, want 1.33", l.KubernetesVersion)
	}
	held.release()
	until("the examples for 1.33", hasExamples)
}

// TestRuleListCellensTypesOff checks that the rule index types no value from
// a .cellens while cellens-types is off (#44): a rule whose value a .cellens
// would type is listed with the compile state of the value as dyn, and
// turning the feature on, then off, changes the state, each with a new
// generation.
func TestRuleListCellensTypesOff(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRuleFile(t, filepath.Join(ws, "config.yaml"), configCRD)
	writeRuleFile(t, filepath.Join(ws, ".cellens"), configLens)
	// newSession turns cellens-types on, as the tests before it was a
	// feature: initialize turns it off, as a client's default does.
	fc := serveFollowing(t, newSession(t), []string{ws}, map[string]any{"ruleList": true, "experimental": []string{}})
	gen := fc.note(func(n ruleNote) bool { return n.Scan.State == scanDone }).Generation
	state := func() string {
		m := ruleModel{}
		m.apply(fc.list(nil))
		return m.stateOf(pathURI(filepath.Join(ws, "config.yaml")))
	}
	if got := state(); got != compileOK {
		t.Errorf("with cellens-types off, the rule on the value .cellens would type is %s, want ok: the value is dyn", got)
	}
	for _, step := range []struct {
		on    []string
		state string
	}{{[]string{"cellens-types"}, compileWarning}, {[]string{}, compileOK}} {
		fc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": step.on}})
		n := fc.note(doneAfter(gen))
		if got := state(); got != step.state {
			t.Errorf("with experimental %q, the rule is %s, want %s", step.on, got, step.state)
		}
		gen = n.Generation
	}
}
