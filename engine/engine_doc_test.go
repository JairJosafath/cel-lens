package main

import (
	"fmt"
	"os"
	"regexp"
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
	t.Run("every version is supported and the libraries match the default", func(t *testing.T) {
		for _, p := range versionProblems(text) {
			t.Error(p)
		}
	})
}

var (
	versionRef    = regexp.MustCompile(`\b1\.\d+\b`)
	librariesSent = regexp.MustCompile(`results come from the (\S+) Kubernetes CEL libraries`)
)

// versionProblems lists every 1.NN in text that kubeversion.Parse rejects,
// and a "results come from the X Kubernetes CEL libraries" sentence that
// names a version other than kubeversion.Default (or is missing).
func versionProblems(text string) []string {
	var problems []string
	for _, v := range versionRef.FindAllString(text, -1) {
		if _, err := kubeversion.Parse(v); err != nil {
			problems = append(problems, fmt.Sprintf("version %s: %v", v, err))
		}
	}
	m := librariesSent.FindStringSubmatch(text)
	if m == nil {
		problems = append(problems, "no sentence 'results come from the X Kubernetes CEL libraries'")
	} else if def := kubeversion.Default().String(); m[1] != def {
		problems = append(problems, fmt.Sprintf("libraries sentence names %s, want default %s", m[1], def))
	}
	return problems
}

func TestVersionProblems(t *testing.T) {
	t.Parallel()
	def := kubeversion.Default().String()
	tests := []struct {
		name string
		text string
		want int
	}{
		{"clean", "results come from the " + def + " Kubernetes CEL libraries; --kube-version " + def, 0},
		{"unsupported version", "results come from the " + def + " Kubernetes CEL libraries; use 1.01", 1},
		{"libraries name another version", "results come from the 1.01 Kubernetes CEL libraries", 2},
		{"libraries sentence missing", "nothing here", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := versionProblems(tt.text); len(got) != tt.want {
				t.Errorf("versionProblems(%q) = %q, want %d problems", tt.text, got, tt.want)
			}
		})
	}
}
