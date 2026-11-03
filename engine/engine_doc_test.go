package main

import (
	"os"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// TestEngineDocKubeVersions requires docs/engine.md's Kubernetes version
// section to state the default, the range and the supportedKubernetesVersions
// list of pkg/kubeversion.
func TestEngineDocKubeVersions(t *testing.T) {
	data, err := os.ReadFile("docs/engine.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "## Kubernetes version\n")
	if !ok {
		t.Fatal("docs/engine.md has no Kubernetes version section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	text := strings.Join(strings.Fields(section), " ")

	supported := kubeversion.Supported()
	def := kubeversion.Default().String()
	rng := supported[0].String() + " to " + supported[len(supported)-1].String()
	var list []string
	for i := len(supported) - 1; i >= 0; i-- {
		list = append(list, `"`+supported[i].String()+`"`)
	}

	tests := []struct {
		name string
		want string
	}{
		{"default", "matches **Kubernetes " + def + "**"},
		{"range", "from " + rng + " ("},
		{"supported list", `"supportedKubernetesVersions": [` + strings.Join(list, ", ") + `]`},
		{"version in use", `"kubernetesVersion": "` + def + `"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(text, tt.want) {
				t.Errorf("docs/engine.md Kubernetes version section = %q, want it to contain %q", text, tt.want)
			}
		})
	}
}
