package main

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
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
	if check := dry("check"); !strings.Contains(check, "go test") || !strings.Contains(check, "-race -count=1") {
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

func TestInnerTimeoutBelowMakefileTimeout(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	out, err := exec.Command("make", "-n", "test").CombinedOutput()
	if err != nil {
		t.Fatalf("make -n test: %v\n%s", err, out)
	}
	m := regexp.MustCompile(`go test\b[^\n]*? -timeout (\S+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("make -n test = %q, want a go test with -timeout", out)
	}
	outer, err := time.ParseDuration(m[1])
	if err != nil {
		t.Fatalf("Makefile test -timeout %q: %v", m[1], err)
	}
	if innerTimeout >= outer {
		t.Errorf("innerTimeout = %v, want below the Makefile's -timeout %v", innerTimeout, outer)
	}
}

func TestMakefileParallelKnob(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, "-p 2 "},
		{"raised", []string{"GO_PARALLEL=8"}, "-p 8 "},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command("make", append([]string{"-n", "test"}, c.args...)...) //nolint:gosec // fixed test arguments
			// make check GO_PARALLEL=8 passes the override to this test as an exported variable and
			// inside MAKEFLAGS; the default case must see neither.
			cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
				return strings.HasPrefix(kv, "GO_PARALLEL=") || strings.HasPrefix(kv, "MAKEFLAGS=") ||
					strings.HasPrefix(kv, "MFLAGS=") || strings.HasPrefix(kv, "MAKELEVEL=")
			})
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make -n test: %v\n%s", err, out)
			}
			for _, line := range strings.Split(string(out), "\n") {
				if (strings.Contains(line, "go build") || strings.Contains(line, "go test")) && !strings.Contains(line, c.want) {
					t.Errorf("make -n test %v: %q lacks %q", c.args, line, c.want)
				}
			}
			if !strings.Contains(string(out), "-race -count=1") {
				t.Errorf("make -n test = %q, want -race -count=1 kept", out)
			}
		})
	}
}

func TestTestDistTargetRunsGatedTests(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	out, err := exec.Command("make", "-n", "test-dist").CombinedOutput()
	if err != nil {
		t.Fatalf("make -n test-dist: %v\n%s", err, out)
	}
	m := regexp.MustCompile(`-run\s+'([^']*)'|-run\s+(\S+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("make -n test-dist = %q, want a go test with -run", out)
	}
	pattern := m[1] + m[2]
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("make test-dist -run %q: %v", pattern, err)
	}
	for _, name := range []string{"TestDist", "TestGoInstallFromProxy"} {
		if !re.MatchString(name) {
			t.Errorf("make test-dist -run %q does not select %s", pattern, name)
		}
	}
}
