package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const brokenCRD = `apiVersion: apiextensions.k8s.io/v1
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
          x-kubernetes-validations:
            - rule: "has(self.a) &&"
`

// sessionDeadline only ends a session that hangs. It is far above what a
// session takes, so a loaded CI runner running many packages at once never
// reaches it.
const sessionDeadline = 5 * time.Minute

// binEnv names the environment variable that holds the path of the program the
// e2e tests run. make test builds it once, before go test, and sets it.
const binEnv = "CEL_LENS_BIN"

// built is the program this test process builds itself when binEnv is unset.
// It is built at most once, by the first test that needs it.
var built struct {
	once sync.Once
	dir  string
	bin  string
	err  error
}

// TestMain removes the directory of the program the tests built themselves.
func TestMain(m *testing.M) {
	m.Run()
	if built.dir != "" {
		_ = os.RemoveAll(built.dir)
	}
}

// buildBinary returns the path of the real program the e2e tests run: the one
// binEnv names (make test builds it once), or else one built here, once per
// test process, into a temporary directory that TestMain removes. The build has
// no deadline of its own; the test's timeout bounds it.
func buildBinary(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv(binEnv); bin != "" {
		return bin
	}
	built.once.Do(func() {
		built.dir, built.err = os.MkdirTemp("", "cel-lens-core-test-") //nolint:usetesting // t.TempDir ends with the first test; the program serves them all
		if built.err != nil {
			return
		}
		built.bin = filepath.Join(built.dir, name)
		cmd := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-o", built.bin, ".") //nolint:gosec // fixed arguments
		if out, err := cmd.CombinedOutput(); err != nil {
			built.err = fmt.Errorf("go build -trimpath -o %s .: %w\n%s", built.bin, err, out)
		}
	})
	if built.err != nil {
		t.Fatal(built.err)
	}
	return built.bin
}

// TestBinaryLSPClientGone checks that the built program exits by itself when
// the client process is gone, even though its stdin stays open.
func TestBinaryLSPClientGone(t *testing.T) {
	bin := buildBinary(t)
	ctx, cancel := context.WithTimeout(t.Context(), sessionDeadline)
	defer cancel()
	child := exec.CommandContext(ctx, bin, "--version") //nolint:gosec // bin is the binary make test built
	if err := child.Run(); err != nil {
		t.Fatalf("%s --version: %v", name, err)
	}
	cmd := exec.CommandContext(ctx, bin, "lsp", "--stdio", "--clientProcessId="+strconv.Itoa(child.Process.Pid)) //nolint:gosec // bin is the binary make test built
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("%s lsp with a dead client exited with %v, want exit code 0", name, err)
	}
}

// TestBinaryLSP launches the built program the way VS Code's language client
// does and runs a session against it.
func TestBinaryLSP(t *testing.T) {
	bin := buildBinary(t)
	ctx, cancel := context.WithTimeout(t.Context(), sessionDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "lsp", "--stdio", "--clientProcessId="+strconv.Itoa(os.Getpid())) //nolint:gosec // bin is the binary make test built
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(stdout)
	send := func(msg map[string]any) {
		t.Helper()
		msg["jsonrpc"] = "2.0"
		body, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
			t.Fatal(err)
		}
	}
	// next reads messages until one has the wanted method or id.
	next := func(method string, id int) map[string]any {
		t.Helper()
		for {
			hdr, err := textproto.NewReader(r).ReadMIMEHeader()
			if err != nil {
				t.Fatalf("waiting for %q: %v", method, err)
			}
			n, err := strconv.Atoi(hdr.Get("Content-Length"))
			if err != nil {
				t.Fatalf("Content-Length %q: %v", hdr.Get("Content-Length"), err)
			}
			body := make([]byte, n)
			if _, err := io.ReadFull(r, body); err != nil {
				t.Fatal(err)
			}
			var msg map[string]any
			if err := json.Unmarshal(body, &msg); err != nil {
				t.Fatal(err)
			}
			if msg["method"] == method || (method == "" && msg["id"] == float64(id)) {
				return msg
			}
		}
	}

	send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"processId": os.Getpid(), "capabilities": map[string]any{}}})
	next("", 1)
	send(map[string]any{"method": "initialized", "params": map[string]any{}})
	uri := "file:///widget-crd.yaml"
	send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "yaml", "version": 1, "text": brokenCRD},
	}})
	diag := next("textDocument/publishDiagnostics", 0)
	params, _ := diag["params"].(map[string]any)
	list, _ := params["diagnostics"].([]any)
	if params["uri"] != uri || len(list) == 0 {
		t.Fatalf("publishDiagnostics = %v, want diagnostics for %s", diag, uri)
	}
	if first, _ := list[0].(map[string]any); !strings.Contains(fmt.Sprint(first["message"]), "Syntax error") {
		t.Errorf("first diagnostic = %v, want a syntax error", first)
	}
	send(map[string]any{"id": 2, "method": "shutdown"})
	next("", 2)
	send(map[string]any{"method": "exit"})
	if err := cmd.Wait(); err != nil {
		t.Errorf("%s lsp exited with %v, want exit code 0", name, err)
	}
}

// TestBinaryCheck runs the built program on a sample CRD
// and expects its CEL error.
func TestBinaryCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget-crd.yaml")
	if err := os.WriteFile(path, []byte(brokenCRD), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), buildBinary(t), "check", path).CombinedOutput() //nolint:gosec // bin is the binary make test built
	if exit := (&exec.ExitError{}); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Errorf("%s check = %v, want exit code 1", name, err)
	}
	if !strings.Contains(string(out), "Syntax error") {
		t.Errorf("%s check printed %q, want the CEL syntax error", name, out)
	}
}

// TestBinaryVersion checks that the built program reports a version after its
// name rather than an empty one.
func TestBinaryVersion(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), buildBinary(t), "--version").Output() //nolint:gosec // bin is the binary make test built
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != name || fields[1] == "" {
		t.Errorf("%s --version = %q, want a version after the name", name, out)
	}
}

// fallbackEnv returns environ for the inner run of TestFallbackBuildLeavesNoDirectory:
// binEnv removed, and the temporary directory set to tmp for every platform
// (os.TempDir reads TMPDIR on Unix and TMP or TEMP on Windows). Names are
// compared without case, as Windows does.
func fallbackEnv(environ []string, tmp string) []string {
	var env []string
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.EqualFold(key, binEnv), strings.EqualFold(key, "TMPDIR"),
			strings.EqualFold(key, "TMP"), strings.EqualFold(key, "TEMP"):
		default:
			env = append(env, kv)
		}
	}
	return append(env, "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
}

// TestFallbackEnv checks that the inner run's environment replaces every
// temporary-directory variable and drops binEnv.
func TestFallbackEnv(t *testing.T) {
	t.Parallel()
	in := []string{"PATH=/bin", binEnv + "=/x/bin", "TMPDIR=/old", "TMP=/old", "TEMP=/old", "Temp=/old", "HOME=/h"}
	got := fallbackEnv(in, "/new")
	want := []string{"PATH=/bin", "HOME=/h", "TMPDIR=/new", "TMP=/new", "TEMP=/new"}
	if !slices.Equal(got, want) {
		t.Errorf("fallbackEnv(%q, /new) = %q, want %q", in, got, want)
	}
}

// TestFallbackBuildLeavesNoDirectory runs TestBinaryVersion in an inner go test
// with binEnv removed, so the test builds the program itself, and checks that
// the run passes and TestMain leaves no cel-lens-core-test-* directory in the
// temporary directory. make test always sets binEnv, so only this test guards
// that fallback. -short leaves it out: the inner run of
// TestPublicSourceTestsPass is short and must not nest another build.
func TestFallbackBuildLeavesNoDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program again")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go is not on PATH: %v", err)
	}
	tmp := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "go", "test", "-count=1", "-run", "^TestBinaryVersion$", ".")
	cmd.Env = fallbackEnv(os.Environ(), tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test -run '^TestBinaryVersion$' . without %s: %v\n%s", binEnv, err, out)
	}
	left, err := filepath.Glob(filepath.Join(tmp, "cel-lens-core-test-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) > 0 {
		t.Errorf("go test left %q in %s, want none", left, tmp)
	}
}

// lspProcess is the built program serving the language server over stdio,
// driven as an editor's language client drives it.
type lspProcess struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser
	r     *bufio.Reader
}

// startLSP starts the built program's language server; ctx ends it.
func startLSP(ctx context.Context, t *testing.T) *lspProcess {
	t.Helper()
	cmd := exec.CommandContext(ctx, buildBinary(t), "lsp", "--stdio", "--clientProcessId="+strconv.Itoa(os.Getpid())) //nolint:gosec // the binary make test built
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &lspProcess{t: t, cmd: cmd, stdin: stdin, r: bufio.NewReader(stdout)}
}

func (p *lspProcess) send(msg map[string]any) {
	p.t.Helper()
	msg["jsonrpc"] = "2.0"
	body, err := json.Marshal(msg)
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err := fmt.Fprintf(p.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		p.t.Fatal(err)
	}
}

// next reads messages until one satisfies match, and returns it.
func (p *lspProcess) next(what string, match func(map[string]any) bool) map[string]any {
	p.t.Helper()
	for {
		hdr, err := textproto.NewReader(p.r).ReadMIMEHeader()
		if err != nil {
			p.t.Fatalf("waiting for %s: %v", what, err)
		}
		n, err := strconv.Atoi(hdr.Get("Content-Length"))
		if err != nil {
			p.t.Fatalf("Content-Length %q: %v", hdr.Get("Content-Length"), err)
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(p.r, body); err != nil {
			p.t.Fatal(err)
		}
		var msg map[string]any
		if err := json.Unmarshal(body, &msg); err != nil {
			p.t.Fatal(err)
		}
		if match(msg) {
			return msg
		}
	}
}

// call sends a request and returns its result.
func (p *lspProcess) call(id int, method string, params map[string]any) any {
	p.t.Helper()
	p.send(map[string]any{"id": id, "method": method, "params": params})
	return p.next("the response to "+method, func(m map[string]any) bool { return m["id"] == float64(id) })["result"]
}

// positionOf is the protocol position of the end of needle's first n
// characters in text, which is ASCII.
func positionOf(t *testing.T, text, needle string, n int) map[string]any {
	t.Helper()
	i := strings.Index(text, needle)
	if i < 0 {
		t.Fatalf("%q not in the text", needle)
	}
	before := text[:i+n]
	return map[string]any{"line": strings.Count(before, "\n"), "character": len(before) - strings.LastIndex(before, "\n") - 1}
}

// TestBinaryLSPCellens runs the built program on the workspace
// testdata/cellens, whose .cellens types values Kubernetes leaves dyn, and
// checks the warnings, completion and hover those types bring (#2).
func TestBinaryLSPCellens(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("testdata", "cellens"))
	if err != nil {
		t.Fatal(err)
	}
	uri := func(name string) string {
		p := filepath.ToSlash(filepath.Join(dir, name))
		if !strings.HasPrefix(p, "/") {
			p = "/" + p // a Windows drive
		}
		return "file://" + p
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	ctx, cancel := context.WithTimeout(t.Context(), sessionDeadline)
	defer cancel()
	p := startLSP(ctx, t)
	p.call(1, "initialize", map[string]any{
		"processId": os.Getpid(), "capabilities": map[string]any{},
		"workspaceFolders": []map[string]any{{"uri": uri(""), "name": "cellens"}},
	})
	p.send(map[string]any{"method": "initialized", "params": map[string]any{}})
	crd, policy := read("crd.yaml"), read("policy.yaml")
	for name, text := range map[string]string{"crd.yaml": crd, "policy.yaml": policy} {
		p.send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri(name), "languageId": "yaml", "version": 1, "text": text},
		}})
	}
	const note = ": the API server would not report this)"
	want := map[string][]string{
		uri("crd.yaml"): {"found no matching overload for 'startsWith' applied to 'int.(string)' (the type comes from .cellens:3" + note},
		uri("policy.yaml"): {
			"undefined field 'nosuch' (the type comes from .cellens:8" + note,
			"undefined field 'nosuch' (the type comes from .cellens:12" + note,
		},
	}
	// The search for .cellens files runs in the background, so the first
	// diagnostics may come before it ends; the check when it ends follows.
	got := map[string][]string{}
	p.next("the diagnostics that use .cellens", func(m map[string]any) bool {
		if m["method"] != "textDocument/publishDiagnostics" {
			return false
		}
		params, _ := m["params"].(map[string]any)
		list, _ := params["diagnostics"].([]any)
		var messages []string
		for _, d := range list {
			d, _ := d.(map[string]any)
			if d["severity"] != float64(2) {
				t.Errorf("diagnostic %v is no warning", d)
			}
			messages = append(messages, fmt.Sprint(d["message"]))
		}
		got[fmt.Sprint(params["uri"])] = messages
		for u, w := range want {
			if !slices.Equal(got[u], w) {
				return false
			}
		}
		return true
	})

	items, _ := p.call(2, "textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": uri("policy.yaml")}, "position": positionOf(t, policy, "params.spec.max", len("params.spec.")),
	}).([]any)
	var labels []string
	for _, it := range items {
		it, _ := it.(map[string]any)
		labels = append(labels, fmt.Sprint(it["label"]))
	}
	if !slices.Contains(labels, "max") {
		t.Errorf("completion after params.spec. = %v, want max", labels)
	}
	hover, _ := p.call(3, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri("policy.yaml")}, "position": positionOf(t, policy, "params.spec.max", 2),
	}).(map[string]any)
	contents, _ := hover["contents"].(map[string]any)
	if md := fmt.Sprint(contents["value"]); !strings.Contains(md, "(variable) params: {spec: {max: int}}") || !strings.Contains(md, "from .cellens, line 8") {
		t.Errorf("hover on params = %q, want its type and that it is from .cellens, line 8", md)
	}
	p.call(4, "shutdown", nil)
	p.send(map[string]any{"method": "exit"})
	if err := p.cmd.Wait(); err != nil {
		t.Errorf("%s lsp exited with %v, want exit code 0", name, err)
	}
}
