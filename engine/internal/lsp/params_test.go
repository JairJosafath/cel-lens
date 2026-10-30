package lsp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const widgetCRDText = `apiVersion: apiextensions.k8s.io/v1
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
                maxReplicas: {type: integer}
`

const widgetPolicyText = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  paramKind: {apiVersion: example.com/v1, kind: Widget}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  validations:
    - expression: "object.spec.replicas <= params.spec.maxReplicas"
`

const (
	crdURI    = "file:///ws/crd.yaml"
	policyURI = "file:///ws/policy.yaml"
)

// initializeIn initializes the server with the folder as the workspace.
func initializeIn(t *testing.T, c *client, folder string, caps map[string]any) {
	t.Helper()
	params := map[string]any{"processId": nil, "capabilities": caps, "rootUri": nil, "initializationOptions": allExperimental()}
	if folder != "" {
		params["workspaceFolders"] = []map[string]any{{"uri": "file://" + filepath.ToSlash(folder), "name": "ws"}}
	}
	if _, rerr := c.call("initialize", params); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
}

func (c *client) openURI(u, text string) {
	c.t.Helper()
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": text}})
}

func (c *client) changeURI(u string, version int, text string) {
	c.t.Helper()
	c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": u, "version": version}, "contentChanges": []map[string]any{{"text": text}}})
}

// nextDiagnostics waits for the next diagnostics published for the policy.
func (c *client) nextDiagnostics() []diagnostic {
	c.t.Helper()
	for {
		m := c.read()
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p struct {
			URI         string       `json:"uri"`
			Diagnostics []diagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			c.t.Fatal(err)
		}
		if p.URI == policyURI {
			return p.Diagnostics
		}
	}
}

// completeLabels returns the labels offered at the end of needle's first n
// characters in the text of the document at u.
func (c *client) completeLabels(text, needle string, n int) []string {
	c.t.Helper()
	line, char := lineOf(c.t, text, needle)
	res, rerr := c.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": policyURI}, "position": map[string]any{"line": line, "character": char + n}})
	if rerr != nil {
		c.t.Fatalf("completion error = %+v", rerr)
	}
	var items []struct{ Label string }
	if err := json.Unmarshal(res, &items); err != nil {
		c.t.Fatalf("completion result %s: %v", res, err)
	}
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.Label
	}
	return labels
}

func (c *client) hoverText(text, needle string, n int) string {
	c.t.Helper()
	line, char := lineOf(c.t, text, needle)
	res, rerr := c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": policyURI}, "position": map[string]any{"line": line, "character": char + n}})
	if rerr != nil {
		c.t.Fatalf("hover error = %+v", rerr)
	}
	var h struct{ Contents struct{ Value string } }
	if err := json.Unmarshal(res, &h); err != nil {
		c.t.Fatalf("hover result %s: %v", res, err)
	}
	return h.Contents.Value
}

func contains(labels []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, l := range labels {
			found = found || l == w
		}
		if !found {
			return false
		}
	}
	return true
}

func TestParamsBuiltInKind(t *testing.T) {
	t.Parallel()
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	policy := strings.Replace(widgetPolicyText, "{apiVersion: example.com/v1, kind: Widget}", "{apiVersion: v1, kind: ConfigMap}", 1)
	policy = strings.Replace(policy, "object.spec.replicas <= params.spec.maxReplicas", "params.data.size() > 0", 1)
	c.openURI(policyURI, policy)
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics = %v, want none", diags)
	}
	if labels := c.completeLabels(policy, "params.data", len("params.")); !contains(labels, "data", "metadata", "binaryData") {
		t.Errorf("completion after params. = %v, want data, metadata and binaryData", labels)
	}
	if got := c.hoverText(policy, "params.data", 2); !strings.Contains(got, "params: v1 ConfigMap") {
		t.Errorf("hover on params = %q, want it to name v1 ConfigMap", got)
	}
}

func TestParamsFromWorkspaceCRD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crd.yaml"), []byte(widgetCRDText), 0o600); err != nil {
		t.Fatal(err)
	}
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	policy := strings.Replace(widgetPolicyText, "params.spec.maxReplicas", "params.spec.maxReplica", 1)
	c.openURI(policyURI, policy)
	diags := c.nextDiagnostics()
	if len(diags) != 1 || diags[0].Severity != 2 || diags[0].Message != "example.com/v1 Widget: undefined field 'maxReplica'" {
		t.Fatalf("diagnostics = %+v, want one warning: example.com/v1 Widget: undefined field 'maxReplica'", diags)
	}
	if labels := c.completeLabels(policy, "params.spec.maxReplica", len("params.spec.")); !contains(labels, "maxReplicas") {
		t.Errorf("completion after params.spec. = %v, want maxReplicas", labels)
	}
	if got := c.hoverText(policy, "params.spec", 2); !strings.Contains(got, "params: example.com/v1 Widget") {
		t.Errorf("hover on params = %q, want it to name example.com/v1 Widget", got)
	}
}

// scanSession initializes a server on dir with crd-typed-policies off and
// returns the client.
func scanSession(t *testing.T, dir string) *client {
	t.Helper()
	c := start(t)
	params := map[string]any{
		"processId": nil, "capabilities": map[string]any{}, "rootUri": nil,
		"workspaceFolders": []map[string]any{{"uri": "file://" + filepath.ToSlash(dir), "name": "ws"}},
	}
	if _, rerr := c.call("initialize", params); rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	c.notify("initialized", map[string]any{})
	return c
}

func enableCRDTypedPolicies(c *client) {
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": []string{"crd-typed-policies"}}})
}

func TestInitializeSkipsWorkspaceScan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "crd.yaml")
	if err := os.WriteFile(path, []byte(widgetCRDText), 0o600); err != nil {
		t.Fatal(err)
	}
	c := scanSession(t, dir)
	policy := strings.Replace(widgetPolicyText, "params.spec.maxReplicas", "params.spec.maxReplica", 1)
	c.openURI(policyURI, policy)
	// The server handles messages in order, so the diagnostics of the open
	// document come after any scan that initialize started.
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics with crd-typed-policies off = %+v, want none", diags)
	}
	// A scan during initialize would have recorded the CRD; remove it so
	// only a scan at the switch could find it.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	enableCRDTypedPolicies(c)
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics after turning the feature on = %+v, want none (initialize did not scan, the CRD is gone)", diags)
	}
}

func TestEnablingFeatureScansAndRechecks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := scanSession(t, dir)
	policy := strings.Replace(widgetPolicyText, "params.spec.maxReplicas", "params.spec.maxReplica", 1)
	c.openURI(policyURI, policy)
	// The server handles messages in order, so any scan that initialize
	// started has finished once these diagnostics arrive; only a scan at the
	// switch can find the CRD written next.
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics with crd-typed-policies off = %+v, want none", diags)
	}
	if err := os.WriteFile(filepath.Join(dir, "crd.yaml"), []byte(widgetCRDText), 0o600); err != nil {
		t.Fatal(err)
	}
	enableCRDTypedPolicies(c)
	diags := c.nextDiagnostics()
	if len(diags) != 1 || diags[0].Message != "example.com/v1 Widget: undefined field 'maxReplica'" {
		t.Errorf("diagnostics after turning the feature on = %+v, want the warning from the CRD written after initialize", diags)
	}
}

func TestParamsFollowCRDChanges(t *testing.T) {
	t.Parallel()
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	c.openURI(policyURI, widgetPolicyText)
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics without the CRD = %v, want none (params is dyn)", diags)
	}
	renamed := strings.Replace(widgetCRDText, "maxReplicas", "maxCount", 1)
	c.openURI(crdURI, renamed)
	diags := c.nextDiagnostics()
	if len(diags) != 1 || diags[0].Message != "example.com/v1 Widget: undefined field 'maxReplicas'" {
		t.Fatalf("diagnostics after the CRD was added = %+v, want a warning about maxReplicas", diags)
	}
	c.changeURI(crdURI, 2, widgetCRDText)
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics after the CRD was fixed = %+v, want none", diags)
	}
	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": crdURI}})
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics after the CRD closed = %+v, want none (params is dyn again)", diags)
	}
}

func TestParamsFollowFilesOnDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "crd.yaml")
	c := start(t)
	initializeIn(t, c, dir, map[string]any{})
	c.openURI(policyURI, widgetPolicyText)
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics without the CRD = %v, want none", diags)
	}
	fileURI := "file://" + filepath.ToSlash(path)
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	changes := func(kind int) {
		c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": fileURI, "type": kind}}})
	}
	write(strings.Replace(widgetCRDText, "maxReplicas", "maxCount", 1))
	changes(1)
	if diags := c.nextDiagnostics(); len(diags) != 1 {
		t.Fatalf("diagnostics after the CRD file was created = %+v, want one warning", diags)
	}
	write(widgetCRDText)
	changes(2)
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics after the CRD file changed = %+v, want none", diags)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	changes(3)
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics after the CRD file was deleted = %+v, want none", diags)
	}
}

func TestParamsKindNotFound(t *testing.T) {
	t.Parallel()
	c := start(t)
	initializeIn(t, c, "", map[string]any{})
	c.openURI(policyURI, widgetPolicyText)
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics = %v, want no field warning for a dyn params", diags)
	}
	if labels := c.completeLabels(widgetPolicyText, "params.spec", len("params.")); contains(labels, "spec") {
		t.Errorf("completion after params. = %v, want no fields", labels)
	}
	got := c.hoverText(widgetPolicyText, "params.spec", 2)
	if !strings.Contains(got, "params: dyn") || !strings.Contains(got, "example.com/v1 Widget was not found") {
		t.Errorf("hover on params = %q, want dyn and that the kind was not found", got)
	}
}

func TestWatchersRegistered(t *testing.T) {
	t.Parallel()
	c := start(t)
	initializeIn(t, c, "", map[string]any{"workspace": map[string]any{"didChangeWatchedFiles": map[string]any{"dynamicRegistration": true}}})
	for {
		m := c.read()
		if m.Method != "client/registerCapability" {
			continue
		}
		if !strings.Contains(string(m.Params), "workspace/didChangeWatchedFiles") || !strings.Contains(string(m.Params), "**/*.{yaml,yml}") {
			t.Errorf("registerCapability params = %s, want watchers for YAML files", m.Params)
		}
		return
	}
}
