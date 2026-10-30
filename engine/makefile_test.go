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

func TestTestTargetBuildsOnce(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	out, err := exec.Command("make", "-n", "test").CombinedOutput()
	if err != nil {
		t.Fatalf("make -n test: %v\n%s", err, out)
	}
	got := string(out)
	build, run := strings.Index(got, "go build"), strings.Index(got, "go test")
	if build < 0 || run < 0 || build > run {
		t.Errorf("make -n test = %q, want go build once before go test", got)
	}
	if strings.Count(got, "go build") != 1 || !strings.Contains(got, "CEL_LENS_BIN=") {
		t.Errorf("make -n test = %q, want one go build and CEL_LENS_BIN set for go test", got)
	}
}
