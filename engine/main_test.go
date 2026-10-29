package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // a substring of stdout; "" means stdout stays empty
		wantStderr string // a substring of stderr; "" means stderr stays empty
	}{
		{
			name:       "no arguments print the help",
			wantCode:   exitOK,
			wantStdout: "Usage: " + name + " [flags] [command]",
		},
		{
			name:       "help",
			args:       []string{"--help"},
			wantCode:   exitOK,
			wantStdout: "-version",
		},
		{
			name:       "version",
			args:       []string{"--version"},
			wantCode:   exitOK,
			wantStdout: name + " (devel) (matches Kubernetes 1.37; supports 1.33–1.37)\n",
		},
		{
			name:       "unknown flag",
			args:       []string{"--nope"},
			wantCode:   exitUsage,
			wantStderr: "flag provided but not defined: -nope",
		},
		{
			name:       "unexpected argument",
			args:       []string{"extra"},
			wantCode:   exitUsage,
			wantStderr: `unexpected argument "extra"`,
		},
		{
			name:       "extra argument after lsp",
			args:       []string{"lsp", "extra"},
			wantCode:   exitUsage,
			wantStderr: `unexpected argument "extra"`,
		},
		{
			name:       "lsp without a client ends with a failure",
			args:       []string{"lsp"},
			wantCode:   exitFail,
			wantStderr: "without shutdown",
		},
		{
			name:       "check help",
			args:       []string{"check", "--help"},
			wantCode:   exitOK,
			wantStdout: "Example:",
		},
		{
			name:       "check without paths",
			args:       []string{"check"},
			wantCode:   exitUsage,
			wantStderr: "no paths given",
		},
		{
			name:       "check with a bad format",
			args:       []string{"check", "--format", "xml", "x"},
			wantCode:   exitUsage,
			wantStderr: "unknown format",
		},
		{
			name:       "check with a missing path",
			args:       []string{"check", "/nonexistent/x.yaml"},
			wantCode:   exitUsage,
			wantStderr: "no such file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), tt.args, strings.NewReader(""), &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d", tt.args, got, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

// checkOutput fails the test when got lacks want, or when want is empty and got
// is not.
func checkOutput(t *testing.T, stream, got, want string) {
	t.Helper()
	if want == "" && got != "" {
		t.Errorf("%s = %q, want it empty", stream, got)
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}

func TestRunCheckExitCodes(t *testing.T) {
	t.Parallel()
	const crd = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec:\n  versions:\n    - name: v1\n      schema:\n        openAPIV3Schema:\n          type: object\n          properties:\n            a:\n              type: integer\n          x-kubernetes-validations:\n            - rule: %q\n"
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	for path, rule := range map[string]string{good: "self.a > 0", bad: "self.b > 0"} {
		if err := os.WriteFile(path, fmt.Appendf(nil, crd, rule), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const kyverno = "apiVersion: policies.kyverno.io/v1\nkind: ValidatingPolicy\nspec:\n  validations:\n    - expression: nope\n"
	badKyverno := filepath.Join(dir, "kyverno", "policy.yaml")
	if err := os.MkdirAll(filepath.Dir(badKyverno), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badKyverno, []byte(kyverno), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
	}{
		{"clean file", []string{"check", good}, exitOK, ""},
		{"Kyverno policy with errors", []string{"check", "--experimental", "kyverno", badKyverno}, exitFail, badKyverno + ":5:19: error: undeclared reference to 'nope'"},
		{"file with errors", []string{"check", bad}, exitFail, bad + ":13:"},
		{"folder with mixed files", []string{"check", dir}, exitFail, "bad.yaml:13:"},
		{"json", []string{"check", "--format", "json", bad}, exitFail, `"severity": "error"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), tt.args, strings.NewReader(""), &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d", tt.args, got, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantOut)
			checkOutput(t, "stderr", stderr.String(), "")
		})
	}
}

func TestRunCheckExperimental(t *testing.T) {
	t.Parallel()
	const kyverno = "apiVersion: policies.kyverno.io/v1\nkind: ValidatingPolicy\nspec:\n  validations:\n    - expression: nope\n"
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(kyverno), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"flag absent", []string{"check", path}, exitOK, "", ""},
		{"kyverno on", []string{"check", "--experimental", "kyverno", path}, exitFail, "undeclared reference to 'nope'", ""},
		{"other feature on", []string{"check", "--experimental", "fitting-completion", path}, exitOK, "", ""},
		{"kyverno among several", []string{"check", "--experimental=fitting-completion,kyverno", path}, exitFail, "undeclared reference to 'nope'", ""},
		{"unknown name", []string{"check", "--experimental", "nope", path}, exitOK, "", `unknown experimental feature "nope", ignored; the known ones are crd-typed-policies, fitting-completion, kyverno`},
		{"unknown name next to a known one", []string{"check", "--experimental", "kyverno,nope", path}, exitFail, "undeclared reference to 'nope'", `"nope"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), tt.args, strings.NewReader(""), &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d", tt.args, got, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
			if n := strings.Count(stderr.String(), "unknown experimental feature"); n > 1 {
				t.Errorf("stderr has %d warnings, want at most one: %q", n, stderr.String())
			}
		})
	}
}

func TestRunLSPFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"stdio", []string{"--stdio"}, exitFail, "", "without shutdown"},
		{"stdio with pid after =", []string{"--stdio", "--clientProcessId=1234"}, exitFail, "", "without shutdown"},
		{"stdio with pid as next argument", []string{"--stdio", "--clientProcessId", "1234"}, exitFail, "", "without shutdown"},
		{"pid missing", []string{"--clientProcessId"}, exitFail, "", "ignoring --clientProcessId: no value"},
		{"pid invalid", []string{"--clientProcessId=abc"}, exitFail, "", `invalid value "abc"`},
		{"stdio with value", []string{"--stdio=x"}, exitUsage, "", `unexpected argument "--stdio=x"`},
		{"pid repeated first reported", []string{"--clientProcessId=abc", "--clientProcessId=-1"}, exitFail, "", `invalid value "abc"`},
		{"pid repeated second reported", []string{"--clientProcessId=abc", "--clientProcessId=-1"}, exitFail, "", `invalid value "-1"`},
		{"socket", []string{"--socket=3000"}, exitUsage, "", "only stdio is supported"},
		{"pipe", []string{"--pipe", "x"}, exitUsage, "", "only stdio is supported"},
		{"node-ipc", []string{"--node-ipc"}, exitUsage, "", "only stdio is supported"},
		{"unknown flag", []string{"--nope"}, exitUsage, "", `unexpected argument "--nope"`},
		{"help", []string{"--help"}, exitOK, "--clientProcessId=<pid>", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			env := lspEnv{alive: func(int) bool { return true }, interval: time.Millisecond}
			if got := runLSP(t.Context(), tt.args, strings.NewReader(""), &stdout, &stderr, env); got != tt.wantCode {
				t.Errorf("runLSP(%q) = %d, want %d", tt.args, got, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

func TestRunLSPClientGone(t *testing.T) {
	t.Parallel()
	in, out, err := os.Pipe() // the client never writes, so Serve blocks reading until released
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close(); _ = out.Close() }()
	var stdout, stderr bytes.Buffer
	env := lspEnv{alive: func(pid int) bool { return pid != 4321 }, interval: time.Millisecond}
	got := runLSP(t.Context(), []string{"--stdio", "--clientProcessId=4321"}, in, &stdout, &stderr, env)
	if got != exitOK {
		t.Errorf("runLSP(client gone) = %d, want %d", got, exitOK)
	}
	checkOutput(t, "stderr", stderr.String(), "client process 4321 is gone")
}

// TestRunLSPWaitsForServe checks that runLSP does not return while Serve is
// still running, when the client goes away and when Serve ends by itself. The
// stand-in for Serve keeps running until the test releases it, so a runLSP
// that does not wait returns first.
func TestRunLSPWaitsForServe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		// serve returns the stand-in for Serve. It signals started, waits for
		// release, then sets returned and returns.
		serve func(started chan<- struct{}, release <-chan struct{}, returned *atomic.Bool) serveFunc
	}{
		{"client gone", []string{"--clientProcessId=4321"}, func(started chan<- struct{}, release <-chan struct{}, returned *atomic.Bool) serveFunc {
			return func(ctx context.Context, _ io.Reader, _ io.Writer) error {
				close(started)
				<-ctx.Done() // runLSP cancels it because the client is gone
				<-release
				returned.Store(true)
				return ctx.Err()
			}
		}},
		{"serve ends", nil, func(started chan<- struct{}, release <-chan struct{}, returned *atomic.Bool) serveFunc {
			return func(context.Context, io.Reader, io.Writer) error {
				close(started)
				<-release
				returned.Store(true)
				return nil
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			release := make(chan struct{})
			var returned atomic.Bool
			alive := func(int) bool {
				select {
				case <-started:
					return false // the client is gone once Serve runs
				default:
					return true
				}
			}
			env := lspEnv{alive: alive, interval: time.Millisecond, serve: tt.serve(started, release, &returned)}
			exited := make(chan struct{})
			go func() {
				defer close(exited)
				runLSP(t.Context(), tt.args, strings.NewReader(""), io.Discard, io.Discard, env)
			}()
			<-started
			select {
			case <-exited:
				t.Fatal("runLSP returned while Serve was still running")
			case <-time.After(50 * time.Millisecond): // runLSP is given the chance to return early; the check is negative
			}
			close(release)
			<-exited
			if !returned.Load() {
				t.Error("runLSP returned before Serve did")
			}
		})
	}
}

func TestRunCheckKubeVersion(t *testing.T) {
	t.Parallel()
	const policy = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: \"isSemver('1.2.3')\"\n"
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"default is 1.37", []string{"check", path}, exitOK, "", ""},
		{"1.37", []string{"check", "--kube-version", "1.37", path}, exitOK, "", ""},
		{"1.34", []string{"check", "--kube-version=1.34", path}, exitOK, "", ""},
		{"1.33 lacks semver", []string{"check", "--kube-version=1.33", path}, exitFail, "isSemver", ""},
		{"unsupported", []string{"check", "--kube-version=1.20", path}, exitUsage, "", "1.33, 1.34, 1.35, 1.36, 1.37"},
		{"not a version", []string{"check", "--kube-version=new", path}, exitUsage, "", "1.33, 1.34, 1.35, 1.36, 1.37"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), tt.args, strings.NewReader(""), &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d", tt.args, got, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

func TestRunCheckFlagsAfterPaths(t *testing.T) {
	t.Parallel()
	const bad = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nspec:\n  validations:\n    - expression: \"isSemver('1.2.3')\"\n"
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"format after path", []string{"check", path, "--format", "json", "--kube-version", "1.33"}, exitFail, `"severity"`, ""},
		{"format between paths", []string{"check", path, "--format=json", "--kube-version=1.33", path}, exitFail, `"severity"`, ""},
		{"kube version after path", []string{"check", path, "--kube-version=1.33"}, exitFail, "isSemver", ""},
		{"unknown flag after path", []string{"check", path, "--nope"}, exitUsage, "", "-nope"},
		{"format without value", []string{"check", path, "--format"}, exitUsage, "", "-format"},
		{"after terminator", []string{"check", "--", "--format"}, exitUsage, "", "--format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), tt.args, strings.NewReader(""), &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d", tt.args, got, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

func TestRunCheckErrorFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing1, missing2 := filepath.Join(dir, "one.yaml"), filepath.Join(dir, "two.yaml")
	var stdout, stderr bytes.Buffer
	got := run(t.Context(), []string{"check", missing1, missing2}, strings.NewReader(""), &stdout, &stderr)
	if got != exitUsage {
		t.Errorf("run(missing paths) = %d, want %d", got, exitUsage)
	}
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stderr = %q, want 2 lines", stderr.String())
	}
	for i, path := range []string{missing1, missing2} {
		if !strings.HasPrefix(lines[i], name+" check: ") {
			t.Errorf("line %d = %q, want prefix %q", i, lines[i], name+" check: ")
		}
		if n := strings.Count(lines[i], path); n != 1 {
			t.Errorf("line %d = %q names %s %d times, want once", i, lines[i], path, n)
		}
	}
}

func TestRunCheckInterrupted(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stdout, stderr bytes.Buffer
	got := run(ctx, []string{"check", t.TempDir()}, strings.NewReader(""), &stdout, &stderr)
	if got != exitInterrupted {
		t.Errorf("run(cancelled) = %d, want %d", got, exitInterrupted)
	}
	checkOutput(t, "stderr", stderr.String(), name+" check: interrupted\n")
}

func TestRunCheckBadCharacterOnce(t *testing.T) {
	t.Parallel()
	var second strings.Builder
	second.WriteString("a: 1\n")
	for i := 2; i < 26; i++ {
		fmt.Fprintf(&second, "k%d: v\n", i)
	}
	second.WriteString("---\nx: 1\ny: 1\nz: 1\nw: 1\nv: 1\nu: 1\nt: \x01\n")
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"invalid Unicode character on line 2", "a: 1\nb: \xed\xa0\x80\n", "bad.yaml:2:4: error: YAML is broken here: invalid Unicode character\n"},
		{"control character on line 33 of the second document", second.String(), "bad.yaml:33:4: error: YAML is broken here: control characters are not allowed\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "bad.yaml")
			if err := os.WriteFile(path, []byte(tt.src), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			got := run(t.Context(), []string{"check", path}, strings.NewReader(""), &stdout, &stderr)
			if got != exitUsage {
				t.Errorf("run(check) = %d, want %d", got, exitUsage)
			}
			if want := strings.ReplaceAll(tt.want, "bad.yaml", path); stdout.String() != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestBuildVersion(t *testing.T) {
	t.Parallel()
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: v}}, true }
	}
	tests := []struct {
		name    string
		stamped string
		read    func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"stamped wins", "v1.2.3", info("v9.9.9"), "v1.2.3"},
		{"module version", "", info("v0.13.0"), "v0.13.0"},
		{"checkout", "", info("(devel)"), "(devel)"},
		{"empty module version", "", info(""), "(devel)"},
		{"no build info", "", func() (*debug.BuildInfo, bool) { return nil, false }, "(devel)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := buildVersion(tt.stamped, tt.read); got != tt.want {
				t.Errorf("buildVersion(%q) = %q, want %q", tt.stamped, got, tt.want)
			}
		})
	}
}

// TestRunCheckParamsFromCRD checks that the check command types params from a
// CRD among the files it checks.
func TestRunCheckParamsFromCRD(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
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
	const policy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: demo
spec:
  paramKind: {apiVersion: example.com/v1, kind: Widget}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  validations:
    - expression: "object.spec.replicas <= params.spec.maxReplica"
`
	dir := t.TempDir()
	for name, text := range map[string]string{"crd.yaml": crd, "policy.yaml": policy} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if got := run(t.Context(), []string{"check", "--experimental", "crd-typed-policies", dir}, strings.NewReader(""), &stdout, &stderr); got != exitOK {
		t.Errorf("run(check) = %d, want %d (warnings do not fail)", got, exitOK)
	}
	checkOutput(t, "stdout", stdout.String(), "warning: apps/v1 Deployment: undefined field 'maxReplica'")
	checkOutput(t, "stderr", stderr.String(), "")
}

func TestRunCheckUnicodeLineEnds(t *testing.T) {
	t.Parallel()
	const crd = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            replicas: {type: integer}
            ps: {type: array, items: {type: string}}
          x-kubernetes-validations:
                - rule: self.replicas > 0 && self.maxReplica > 1
                  messageExpression: "'replicas is ' + string(self.replicas)"
                - rule: self.replicas > 0
                  messageExpression: "self.ps.all(a,self.ps.all(b,a<b)) ? 'x' :'y'"
`
	want := []string{"22:51-22:61 undefined field", "25:39-25:83 estimated cost"}
	for _, br := range []struct{ name, end string }{{"line feed", "\n"}, {"NEL", "\u0085"}, {"LS", "\u2028"}, {"PS", "\u2029"}} {
		t.Run(br.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "crd.yaml")
			if err := os.WriteFile(path, []byte("# c"+br.end+"# d\n"+crd), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), []string{"check", "--format", "json", path}, strings.NewReader(""), &stdout, &stderr); got != exitFail {
				t.Errorf("run(check) = %d, want %d", got, exitFail)
			}
			var diags []struct {
				Start, End struct{ Line, Column int }
				Message    string
			}
			if err := json.Unmarshal(stdout.Bytes(), &diags); err != nil {
				t.Fatalf("stdout = %q, not JSON: %v", stdout.String(), err)
			}
			if len(diags) != len(want) {
				t.Fatalf("run(check) gave %d problems %+v, want %d", len(diags), diags, len(want))
			}
			for i, d := range diags {
				got := fmt.Sprintf("%d:%d-%d:%d %s", d.Start.Line, d.Start.Column, d.End.Line, d.End.Column, d.Message)
				if !strings.HasPrefix(got, want[i]) {
					t.Errorf("problem %d = %.60q, want prefix %q", i, got, want[i])
				}
			}
		})
	}
}
