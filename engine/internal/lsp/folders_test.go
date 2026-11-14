package lsp_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// changeFolders tells the server of workspace folders added and removed, as
// workspace/didChangeWorkspaceFolders does.
func (c *client) changeFolders(added, removed []string) {
	c.t.Helper()
	list := func(paths []string) []map[string]any {
		out := []map[string]any{}
		for _, p := range paths {
			out = append(out, map[string]any{"uri": fileURI(p), "name": filepath.Base(p)})
		}
		return out
	}
	c.notify("workspace/didChangeWorkspaceFolders", map[string]any{"event": map[string]any{"added": list(added), "removed": list(removed)}})
}

// latestDiagnostics reads the server's diagnostics until done accepts the
// last ones published for each URI.
func (c *client) latestDiagnostics(want string, done func(map[string][]diagnostic) bool) {
	c.t.Helper()
	latest := map[string][]diagnostic{}
	for !done(latest) {
		m := c.read("textDocument/publishDiagnostics: " + want)
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
		latest[p.URI] = p.Diagnostics
	}
}

// TestInitializeDeclaresWorkspaceFolders checks that initialize declares
// workspace folders with change notifications, so a client tells the server
// of a folder added or removed (#38).
func TestInitializeDeclaresWorkspaceFolders(t *testing.T) {
	t.Parallel()
	c := start(t)
	res, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil})
	if rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	var r struct {
		Capabilities struct {
			Workspace struct {
				WorkspaceFolders *struct {
					Supported           bool `json:"supported"`
					ChangeNotifications bool `json:"changeNotifications"`
				} `json:"workspaceFolders"`
			} `json:"workspace"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	if f := r.Capabilities.Workspace.WorkspaceFolders; f == nil || !f.Supported || !f.ChangeNotifications {
		t.Errorf("initialize workspace.workspaceFolders = %+v, want supported and changeNotifications true", f)
	}
}

// TestAddedFolderCRDsTypePolicies checks that the CRD scan, while
// crd-typed-policies is on, reads a workspace folder added after initialize
// and forgets one removed, and that the open policy is checked again each
// time (#38).
func TestAddedFolderCRDsTypePolicies(t *testing.T) {
	t.Parallel()
	a, b := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(b, "crd.yaml"), widgetCRDText)
	c := start(t)
	initializeIn(t, c, a, map[string]any{})
	policy := strings.Replace(widgetPolicyText, "params.spec.maxReplicas", "params.spec.maxReplica", 1)
	c.openURI(policyURI, policy)
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics before %s is added = %+v, want none (params is dyn)", b, diags)
	}
	c.changeFolders([]string{b}, nil)
	diags := c.nextDiagnostics()
	for len(diags) == 0 {
		diags = c.nextDiagnostics()
	}
	if len(diags) != 1 || diags[0].Message != "apps/v1 Deployment: undefined field 'maxReplica'" {
		t.Fatalf("diagnostics after %s was added = %+v, want the warning from its CRD", b, diags)
	}
	c.changeFolders(nil, []string{b})
	if diags := c.nextDiagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics after %s was removed = %+v, want none: its CRD no longer counts", b, diags)
	}
}

// TestAddedFolderLenses checks that a workspace folder added after
// initialize is searched for .cellens files, whose types and problems then
// count, and that removing it forgets them: the open document typed from
// one is checked again, and the diagnostics of the .cellens files that are
// not open are cleared, while an open file of the folder stays open (#38).
func TestAddedFolderLenses(t *testing.T) {
	t.Parallel()
	a, b := t.TempDir(), t.TempDir()
	crd := fileURI(filepath.Join(b, "crd.yaml"))
	writeFile(t, filepath.Join(b, "crd.yaml"), lensCRD)
	writeFile(t, filepath.Join(b, ".cellens"), lensText)
	broken := filepath.Join(b, "other", ".cellens")
	writeIn(t, broken, "types:\n  - file: missing.yaml\n    document: CustomResourceDefinition widgets.example.com\n    version: v1\n    type: string\n")
	c := start(t)
	initializeIn(t, c, a, map[string]any{})
	c.openURI(crd, lensCRD)
	c.diagnosticsOf(crd, none)
	c.changeFolders([]string{b}, nil)
	c.latestDiagnostics("the .cellens types of the folder added", func(latest map[string][]diagnostic) bool {
		return hasMessage(configWarning)(latest[crd]) && hasMessage("file missing.yaml does not exist")(latest[fileURI(broken)])
	})
	c.changeFolders(nil, []string{b})
	c.latestDiagnostics("the .cellens types of the folder removed forgotten", func(latest map[string][]diagnostic) bool {
		lens, sent := latest[fileURI(broken)]
		doc, checked := latest[crd]
		return sent && len(lens) == 0 && checked && len(doc) == 0
	})
}

// TestAddedFolderExamples checks that the check of the examples walks a
// workspace folder added after initialize, whose examples and CRDs then
// count, and that removing it clears the diagnostics it sent for the
// folder's files that are not open (#38).
func TestAddedFolderExamples(t *testing.T) {
	t.Parallel()
	a, b := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(b, "crd.yaml"), limitedWidgetCRD)
	sample := filepath.Join(b, "samples", "widget.yaml")
	writeIn(t, sample, sizedWidget("20"))
	const over = "spec.size: Invalid value: 20: spec.size in body should be less than or equal to 10"
	c := start(t)
	initializeIn(t, c, a, map[string]any{})
	c.changeFolders([]string{b}, nil)
	c.diagnosticsFor(fileURI(sample), saying(over))
	c.changeFolders(nil, []string{b})
	c.diagnosticsFor(fileURI(sample), saying())
}
