package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestCommunityFiles pins the README's section order and that the README,
// CONTRIBUTING.md and SECURITY.md name the public repository, carry no badge
// and do not name the factory.
func TestCommunityFiles(t *testing.T) {
	t.Parallel()
	const repo = "github.com/JairJosafath/cel-lens"
	heading := regexp.MustCompile(`(?m)^## (.+)$`)
	wantOrder := []string{
		"What you see",
		"Install",
		"Flags and settings",
		"Supported Kubernetes versions",
		"Stable and experimental features",
		"Other editors",
		"Not supported yet",
		"Contributing",
	}
	for _, name := range []string{"README.md", "CONTRIBUTING.md", "SECURITY.md"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := readFile(t, name)
			if !strings.Contains(s, repo) {
				t.Errorf("%s does not name %s", name, repo)
			}
			low := strings.ToLower(s)
			for _, bad := range []string{"factory", "gitea", "devcontainer", "![", "shields.io", "engine/v"} {
				if strings.Contains(low, strings.ToLower(bad)) {
					t.Errorf("%s names %q", name, bad)
				}
			}
		})
	}
	t.Run("README section order", func(t *testing.T) {
		t.Parallel()
		var got []string
		for _, m := range heading.FindAllStringSubmatch(readFile(t, "README.md"), -1) {
			got = append(got, m[1])
		}
		if strings.Join(got, "|") != strings.Join(wantOrder, "|") {
			t.Errorf("README sections = %q, want %q", got, wantOrder)
		}
	})
	t.Run("README install line", func(t *testing.T) {
		t.Parallel()
		if !strings.Contains(readFile(t, "README.md"), "go install "+repo+"/engine@latest") {
			t.Error("README does not install with go install .../engine@latest")
		}
	})
	t.Run("SECURITY says when we answer", func(t *testing.T) {
		t.Parallel()
		s := readFile(t, "SECURITY.md")
		for _, want := range []string{"private vulnerability reporting", "within 7 days", "within 14 days", "advisory"} {
			if !strings.Contains(strings.ToLower(s), want) {
				t.Errorf("SECURITY.md lacks %q", want)
			}
		}
	})
	t.Run("CONTRIBUTING says how to build, test and send", func(t *testing.T) {
		t.Parallel()
		s := readFile(t, "CONTRIBUTING.md")
		for _, want := range []string{"go build", "make build", "make check", "fails on any warning", "[Unreleased]", "pull request", "skip"} {
			if !strings.Contains(s, want) {
				t.Errorf("CONTRIBUTING.md lacks %q", want)
			}
		}
	})
}
