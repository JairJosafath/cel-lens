package main

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// fencedBlock returns the body of the first code block of readme whose opening
// fence is "```" followed by lang and whose body contains contains.
func fencedBlock(t *testing.T, readme, lang, contains string) string {
	t.Helper()
	parts := strings.Split(readme, "```"+lang+"\n")
	for _, part := range parts[1:] {
		body, _, ok := strings.Cut(part, "```")
		if ok && strings.Contains(body, contains) {
			return body
		}
	}
	t.Fatalf("README.md has no %s block containing %q", lang, contains)
	return ""
}

// TestReadmeSample runs the built check command on the CRD the README quotes
// and expects the output and exit code the README's shell block shows.
func TestReadmeSample(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	crd := fencedBlock(t, readme, "yaml", "kind: CustomResourceDefinition")
	session := fencedBlock(t, readme, "sh", "$ "+name+" check ")

	var args []string
	var want strings.Builder
	var wantExit string
	for line := range strings.SplitSeq(strings.TrimSuffix(session, "\n"), "\n") {
		switch cmd, isCmd := strings.CutPrefix(line, "$ "); {
		case isCmd && cmd == "echo $?":
			wantExit = "next"
		case isCmd:
			args = strings.Fields(cmd)[1:]
		case wantExit == "next":
			wantExit = line
		default:
			want.WriteString(line + "\n")
		}
	}
	if len(args) < 2 || args[0] != "check" {
		t.Fatalf("README.md shell block runs %q, want a check command", args)
	}
	wantCode, err := strconv.Atoi(wantExit)
	if err != nil {
		t.Fatalf("README.md exit code %q is not a number", wantExit)
	}

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile(args[len(args)-1], []byte(crd), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), buildBinary(t), args...) //nolint:gosec // bin is the binary make test built
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if exit := (&exec.ExitError{}); errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if string(out) != want.String() {
		t.Errorf("%s %s printed %q, README.md quotes %q", name, strings.Join(args, " "), out, want.String())
	}
	if code != wantCode {
		t.Errorf("%s %s exit code = %d, README.md quotes %d", name, strings.Join(args, " "), code, wantCode)
	}
}

// TestReadmeKubeVersions requires the README's settings table and its
// Supported Kubernetes versions section to state the range and default of
// pkg/kubeversion.
func TestReadmeKubeVersions(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	supported := kubeversion.Supported()
	def := kubeversion.Default().String()
	rng := supported[0].String() + " to " + supported[len(supported)-1].String()

	_, section, ok := strings.Cut(readme, "## Supported Kubernetes versions\n")
	if !ok {
		t.Fatal("README.md has no Supported Kubernetes versions section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	var row string
	for line := range strings.SplitSeq(readme, "\n") {
		if strings.HasPrefix(line, "| `--kube-version ") {
			row = line
		}
	}

	tests := []struct {
		name string
		text string
		want string
	}{
		{"table default", row, "the default is " + def},
		{"section range", section, rng + "; the default is **" + def + "**"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.text, tt.want) {
				t.Errorf("README.md = %q, want it to contain %q", tt.text, tt.want)
			}
		})
	}
}
