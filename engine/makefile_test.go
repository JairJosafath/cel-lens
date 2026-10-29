package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCheckLightTarget(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	dry := func(target string) string {
		out, err := exec.Command("make", "-n", target).CombinedOutput() //nolint:gosec // target is a constant of this test
		if err != nil {
			t.Fatalf("make -n %s: %v\n%s", target, err, out)
		}
		return string(out)
	}
	light := dry("check-light")
	for _, want := range []string{"go vet ./...", "golangci-lint run", "go build"} {
		if !strings.Contains(light, want) {
			t.Errorf("make check-light does not run %q", want)
		}
	}
	if strings.Contains(light, "go test") {
		t.Error("make check-light runs tests")
	}
	if !strings.Contains(dry("check"), "go test -race") {
		t.Error("make check no longer runs the tests")
	}
	help, err := exec.Command("make", "help").CombinedOutput()
	if err != nil {
		t.Fatalf("make help: %v\n%s", err, help)
	}
	if !strings.Contains(string(help), "check-light") {
		t.Error("make help does not name check-light")
	}
}
