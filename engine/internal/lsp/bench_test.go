package lsp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/internal/lsp"
)

// bigCRD returns a CustomResourceDefinition of about lines lines, with a rule on every group of properties, about every 35 lines, and a
// wrong one in every tenth group.
func bigCRD(lines int) string {
	var b strings.Builder
	b.WriteString(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
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
`)
	for i := 0; strings.Count(b.String(), "\n") < lines-10; i++ {
		rule := fmt.Sprintf("self.size() >= 0 && self.a%d.size() < 10 && self.b%d.all(x, x > 0)", i, i)
		if i%10 == 7 {
			rule = fmt.Sprintf("self.a%d.nope > 0", i)
		}
		fmt.Fprintf(&b, `                group%d:
                  type: object
                  description: Group number %d.
                  properties:
                    a%d:
                      type: string
                      maxLength: 20
                    b%d:
                      type: array
                      maxItems: 10
                      items:
                        type: integer
                    c:
                      type: string
                      description: A free-form note.
                    d:
                      type: integer
                      minimum: 0
                      maximum: 100
                    e:
                      type: object
                      properties:
                        f:
                          type: string
                        g:
                          type: boolean
                  x-kubernetes-validations:
                    - rule: "%s"
                      message: group %d is invalid
`, i, i, i, i, rule, i)
	}
	return b.String()
}

// benchClient starts a server and initializes it. send writes one message to
// it, read returns the next one.
func benchClient(b *testing.B) (send func(any), read func() jsonrpc.Message) {
	b.Helper()
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- new(lsp.Server).Serve(context.Background(), serverIn, serverOut)
		_ = serverOut.Close()
	}()
	b.Cleanup(func() {
		_ = clientOut.Close()
		_ = clientIn.Close()
		<-done
	})
	in := jsonrpc.NewConn(clientIn, io.Discard)
	send = func(v any) {
		body, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(clientOut, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
			b.Fatal(err)
		}
	}
	read = func() jsonrpc.Message {
		m, err := in.Read()
		if err != nil {
			b.Fatal(err)
		}
		return m
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"capabilities": map[string]any{}}})
	read()
	return send, read
}

func didOpen(text string, version int) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "yaml", "version": version, "text": text},
	}}
}

// BenchmarkDiagnostics measures a didOpen of a 1,000-line CRD, from the
// notification to the published diagnostics, through the server's whole
// message loop. The budget is 300 ms per op.
func BenchmarkDiagnostics(b *testing.B) {
	text := bigCRD(1000)
	b.Logf("input: %d lines", strings.Count(text, "\n"))
	send, read := benchClient(b)
	for b.Loop() {
		send(didOpen(text, 1))
		var p struct{ Diagnostics []json.RawMessage }
		m := read()
		if err := json.Unmarshal(m.Params, &p); err != nil || len(p.Diagnostics) == 0 {
			b.Fatalf("diagnostics = %s, %v, want some", m.Params, err)
		}
	}
}

// BenchmarkHover measures a hover request on an open, unchanged 1,000-line
// CRD, from the request to the response, through the server's whole message
// loop. The extraction is made by the first hover and reused by the others.
func BenchmarkHover(b *testing.B) {
	text := bigCRD(1000)
	b.Logf("input: %d lines", strings.Count(text, "\n"))
	send, read := benchClient(b)
	send(didOpen(text, 1))
	read()
	// The last rule of the file, as far from the start as a request gets.
	lines := strings.Split(text, "\n")
	line := 0
	for i, l := range lines {
		if strings.Contains(l, "rule:") && strings.Contains(l, "self.") {
			line = i
		}
	}
	char := strings.Index(lines[line], "self.") + 2
	for id := 2; b.Loop(); id++ {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "textDocument/hover", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char},
		}})
		if m := read(); !strings.Contains(string(m.Result), "self") {
			b.Fatalf("hover = %s, want a description of self", m.Result)
		}
	}
}
