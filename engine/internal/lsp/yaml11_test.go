package lsp_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// yaml11CRD is a CRD of widgets with a string spec.mode, whose enum is a and b.
const yaml11CRD = `apiVersion: apiextensions.k8s.io/v1
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
                mode: {type: string, enum: [a, b]}
`

// yaml11Policy is a ValidatingAdmissionPolicy with a mistake in its CEL, an
// audit annotation whose key is key and a label whose value is label.
func yaml11Policy(key, label string) string {
	return "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  name: p\n  labels:\n    tier: " + label + "\n" +
		"spec:\n  validations:\n    - expression: object.spec.replicas > 1 &&\n  auditAnnotations:\n    - key: " + key + "\n      valueExpression: \"'x'\"\n"
}

// yaml11CRDWithEnum is a CRD of quirks whose string spec.answer takes a and word.
func yaml11CRDWithEnum(word string) string {
	return strings.NewReplacer("widgets", "quirks", "widget", "quirk", "Widget", "Quirk", "example.com", "yaml11.example.com", "mode: {type: string, enum: [a, b]}", "mode: {type: string, enum: [a, "+word+"]}").Replace(yaml11CRD)
}

// latest reads the server's messages until each of the URIs in want has had
// diagnostics that its function accepts, and returns the last of each.
func (c *client) latest(want map[string]func(published) bool) map[string]published {
	c.t.Helper()
	got := map[string]published{}
	done := map[string]bool{}
	for len(done) < len(want) {
		m := c.read("diagnostics for the files not yet seen")
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p published
		if err := json.Unmarshal(m.Params, &p); err != nil {
			c.t.Fatal(err)
		}
		if accept, ok := want[p.URI]; ok && !done[p.URI] {
			got[p.URI] = p
			done[p.URI] = accept(p)
			if !done[p.URI] {
				delete(done, p.URI)
			}
		}
	}
	return got
}

// TestYAML11Diagnostics is the test of #27 in the editor: a plain y, n, yes,
// no, on or off where the API server wants a string is the boolean kubectl
// sends, and the editor says so, at the word, where it said nothing of a
// policy whose audit annotation key was n. The words are an audit
// annotation's key and a label's value of a policy, an entry of the enum of a
// CRD, and a label's value and an enum value of an example of a CRD.
func TestYAML11Diagnostics(t *testing.T) {
	t.Parallel()
	words := []string{"y", "n", "yes", "no", "on", "off", "Yes", "NO", "Off"}
	dir := t.TempDir()
	writeIn(t, filepath.Join(dir, "crd.yaml"), yaml11CRD)
	for _, w := range words {
		writeIn(t, filepath.Join(dir, "samples", w+"-label.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\n  labels:\n    tier: "+w+"\nspec:\n  mode: a\n")
		writeIn(t, filepath.Join(dir, "samples", w+"-enum.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\nspec:\n  mode: "+w+"\n")
	}
	writeIn(t, filepath.Join(dir, "samples", "quoted.yaml"), "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\n  labels:\n    tier: \"n\"\nspec:\n  mode: a\n")
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})

	wrongType := func(path, w string) string {
		return fmt.Sprintf("%s: expected a string, got a boolean: kubectl reads an unquoted %s as one (YAML 1.1), so quote it if a string is meant", path, w)
	}
	truth := func(w string) bool { return slices.Contains([]string{"y", "yes", "on", "Yes"}, w) }
	hint := func(line int, w string) string {
		return fmt.Sprintf("(line %d: kubectl reads the unquoted %s as a boolean, as YAML 1.1 does; quote it if a string is meant)", line, w)
	}
	nonEmpty := func(p published) bool { return len(p.Diagnostics) > 0 }
	any1 := func(published) bool { return true }
	type check struct {
		name string
		want []string // the messages, in order
		cel  bool     // whether a CEL problem of the policy comes with them
	}
	// verify reads the diagnostics of the URIs of checks, which the server
	// publishes as it goes, and holds each to what it wants.
	verify := func(checks map[string]check, accept func(published) bool) {
		wants := map[string]func(published) bool{}
		for u := range checks {
			wants[u] = accept
		}
		got := c.latest(wants)
		for u, ck := range checks {
			var msgs []string
			for _, d := range got[u].Diagnostics {
				msgs = append(msgs, d.Message)
			}
			// The CEL mistake of a policy is reported too, so the policy is
			// not left out: only that the diagnostic of the word is there,
			// with one more, is held to.
			if ck.cel {
				if len(msgs) != 2 || !slices.Contains(msgs, ck.want[0]) {
					t.Errorf("%s: diagnostics = %q, want %q and the policy's CEL problem", ck.name, msgs, ck.want[0])
				}
				continue
			}
			if !slices.Equal(msgs, ck.want) {
				t.Errorf("%s: diagnostics = %q, want %q", ck.name, msgs, ck.want)
			}
		}
	}
	examples := map[string]check{}
	for _, w := range words {
		labelEx := fileURI(filepath.Join(dir, "samples", w+"-label.yaml"))
		examples[labelEx] = check{"example label value " + w, []string{"json: cannot unmarshal bool into Go struct field ObjectMeta.labels of type string " + hint(6, w)}, false}
		enumEx := fileURI(filepath.Join(dir, "samples", w+"-enum.yaml"))
		examples[enumEx] = check{"example enum value " + w, []string{
			`spec.mode: Invalid value: "boolean": spec.mode in body must be of type string: "boolean" ` + hint(6, w),
			fmt.Sprintf(`spec.mode: Unsupported value: %t: supported values: "a", "b"`, truth(w)),
		}, false}
	}
	verify(examples, nonEmpty)
	for _, w := range words {
		policyURI := fileURI(filepath.Join(dir, "policy-"+w+".yaml"))
		labelURI := fileURI(filepath.Join(dir, "label-"+w+".yaml"))
		crdURI := fileURI(filepath.Join(dir, "quirk-"+w+".yaml"))
		// One at a time: the server publishes as it reads, and the client
		// reads only when it is waiting for a publication.
		c.openURI(policyURI, yaml11Policy(w, "x"))
		verify(map[string]check{policyURI: {"audit annotation key " + w, []string{wrongType("spec.auditAnnotations[0].key", w)}, true}}, any1)
		c.openURI(labelURI, yaml11Policy("k", w))
		verify(map[string]check{labelURI: {"policy label value " + w, []string{wrongType("metadata.labels[tier]", w)}, true}}, any1)
		c.openURI(crdURI, yaml11CRDWithEnum(w))
		verify(map[string]check{crdURI: {"CRD enum value " + w, []string{fmt.Sprintf("spec.versions[0].schema.openAPIV3Schema.properties[spec].properties[mode].enum[1]: kubectl reads the unquoted %s as the boolean %t, not the string %q (YAML 1.1): quote it", w, truth(w), w)}, false}}, any1)
	}
	shutdown(t, c)
}
