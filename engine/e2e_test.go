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
	"strconv"
	"strings"
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

// buildBinary returns the path of the real program that make test built. It
// fails the test when go test runs without make test.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv(binEnv)
	if bin == "" {
		t.Fatalf("%s is not set; run the tests with make test, which builds the program once", binEnv)
	}
	return bin
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
