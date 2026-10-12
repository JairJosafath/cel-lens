package lsp_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

const listsPolicy = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: \"isSemver('1.2.3')\"\n"

// initializeWith sends initialize with options, then initialized, and returns
// the Kubernetes version the server reports.
func initializeWith(t *testing.T, c *client, options any) string {
	t.Helper()
	v := initializeRequest(t, c, options)
	c.notify("initialized", map[string]any{})
	return v
}

// initializeRequest sends initialize with options and returns the Kubernetes
// version the server reports.
func initializeRequest(t *testing.T, c *client, options any) string {
	t.Helper()
	res, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil, "initializationOptions": options})
	if rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	var r struct {
		KubernetesVersion string
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	return r.KubernetesVersion
}

func TestKubernetesVersionDefault(t *testing.T) {
	t.Parallel()
	c := start(t)
	if got := initializeWith(t, c, nil); got != "1.37" {
		t.Errorf("initialize kubernetesVersion = %q, want 1.37", got)
	}
	c.open(listsPolicy)
	if _, diags := c.diagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics with the default version = %v, want none", diags)
	}
}

func TestKubernetesVersionInitializationOptions(t *testing.T) {
	t.Parallel()
	c := start(t)
	if got := initializeWith(t, c, map[string]any{"kubernetesVersion": "1.33"}); got != "1.33" {
		t.Errorf("initialize kubernetesVersion = %q, want 1.33", got)
	}
	c.open(listsPolicy)
	if _, diags := c.diagnostics(); len(diags) != 1 || !strings.Contains(diags[0].Message, "isSemver") {
		t.Errorf("diagnostics with 1.33 = %v, want one about isSemver", diags)
	}
}

func TestKubernetesVersionDidChangeConfiguration(t *testing.T) {
	t.Parallel()
	c := start(t)
	initializeWith(t, c, nil)
	c.open(listsPolicy)
	if _, diags := c.diagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics with 1.37 = %v, want none", diags)
	}
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"kubernetesVersion": "1.33"}})
	if _, diags := c.diagnostics(); len(diags) != 1 {
		t.Errorf("diagnostics after changing to 1.33 = %v, want one about isSemver", diags)
	}
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"kubernetesVersion": ""}})
	if _, diags := c.diagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics after resetting to the default = %v, want none", diags)
	}
}

func TestKubernetesVersionUnsupported(t *testing.T) {
	t.Parallel()
	c := start(t)
	initializeWith(t, c, nil)
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"kubernetesVersion": "1.20"}})
	for {
		m := c.read()
		if m.Method != "window/showMessage" {
			continue
		}
		if !strings.Contains(string(m.Params), "1.33, 1.34, 1.35, 1.36, 1.37") {
			t.Errorf("showMessage = %s, want it to list the supported versions", m.Params)
		}
		return
	}
}

func TestKubernetesVersionUnsupportedInitializationOptions(t *testing.T) {
	t.Parallel()
	c := start(t)
	if got := initializeRequest(t, c, map[string]any{"kubernetesVersion": "1.20"}); got != "1.37" {
		t.Errorf("initialize kubernetesVersion = %q, want the default 1.37", got)
	}
	for {
		m := c.read()
		if m.Method != "window/showMessage" {
			continue
		}
		if !strings.Contains(string(m.Params), "1.33, 1.34, 1.35, 1.36, 1.37") {
			t.Errorf("showMessage = %s, want it to list the supported versions", m.Params)
		}
		return
	}
}

func TestInitializeResultVersions(t *testing.T) {
	t.Parallel()
	c := start(t)
	res, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil, "initializationOptions": map[string]any{"kubernetesVersion": "1.34"}})
	if rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	var r struct {
		KubernetesVersion string `json:"kubernetesVersion"`
		Capabilities      struct {
			Experimental struct {
				CelLens struct {
					KubernetesVersion           string   `json:"kubernetesVersion"`
					SupportedKubernetesVersions []string `json:"supportedKubernetesVersions"`
				} `json:"celLens"`
			} `json:"experimental"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	if r.KubernetesVersion != "1.34" {
		t.Errorf("kubernetesVersion = %q, want 1.34", r.KubernetesVersion)
	}
	var want []string
	for _, v := range slices.Backward(kubeversion.Supported()) {
		want = append(want, v.String())
	}
	celLens := r.Capabilities.Experimental.CelLens
	if !slices.Equal(celLens.SupportedKubernetesVersions, want) {
		t.Errorf("celLens.supportedKubernetesVersions = %v, want %v (newest first)", celLens.SupportedKubernetesVersions, want)
	}
	if celLens.KubernetesVersion != kubeversion.Default().String() {
		t.Errorf("celLens.kubernetesVersion = %q, want the default %s", celLens.KubernetesVersion, kubeversion.Default())
	}
}
