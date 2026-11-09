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

// manyRulesCRD returns a CustomResourceDefinition with rules rules, one on
// each group of properties as in bigCRD, every tenth wrong. Every dynEvery-th
// group has a dyn value (x-kubernetes-int-or-string), which its rule reads.
func manyRulesCRD(rules, dynEvery int) string {
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
	for i := range rules {
		rule := fmt.Sprintf("self.size() >= 0 && self.a%d.size() < 10 && self.b%d.all(x, x > 0)", i, i)
		port := ""
		switch {
		case i%dynEvery == 0:
			rule = fmt.Sprintf("self.a%d.size() < 10 && self.port == 80", i)
			port = "\n                    port:\n                      x-kubernetes-int-or-string: true"
		case i%10 == 7:
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
                        type: integer%s
                  x-kubernetes-validations:
                    - rule: "%s"
                      message: group %d is invalid
`, i, i, i, i, port, rule, i)
	}
	return b.String()
}

// manyPolicies returns n ValidatingAdmissionPolicies, ---separated, each
// matching deployments with vars variables and validations validations. Their
// params are dyn, as no CRD defines their paramKind, and each validation reads
// them.
func manyPolicies(n, vars, validations int) string {
	var b strings.Builder
	for p := range n {
		fmt.Fprintf(&b, `---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: limits-%d
spec:
  paramKind: {apiVersion: example.com/v1, kind: Limit}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments], operations: [CREATE, UPDATE]}
`, p)
		if vars > 0 {
			b.WriteString("  variables:\n")
		}
		for v := range vars {
			fmt.Fprintf(&b, "    - name: v%d\n      expression: \"object.spec.replicas + %d\"\n", v, v)
		}
		b.WriteString("  validations:\n")
		for v := range validations {
			fmt.Fprintf(&b, "    - expression: \"object.spec.replicas <= params.max%d && object.metadata.name != ''\"\n", v)
		}
	}
	return b.String()
}

// BenchmarkCodeLens measures a codeLens request, from the request to the
// response, through the server's whole message loop: on a CRD of 500 rules,
// every rule reading a dyn value or every fifth, and on policies matching
// deployments. "after a change" is the first request for a version of the
// document, after didChange and its check, which the timer leaves out;
// "unchanged" asks again for the same version.
func BenchmarkCodeLens(b *testing.B) {
	inputs := []struct {
		name  string
		text  string
		rules int // the expressions
		dyn   int // the values they read that are dyn
	}{
		{"CRD 500 rules dyn in every 1", manyRulesCRD(500, 1), 500, 500},
		{"CRD 500 rules dyn in every 5", manyRulesCRD(500, 5), 500, 100},
		{"policy 10 variables 2 validations", manyPolicies(1, 10, 2), 12, 2},
		{"50 policies 10 validations", manyPolicies(50, 0, 10), 500, 500},
		{"50 policies 2 variables 8 validations", manyPolicies(50, 2, 8), 500, 400},
	}
	for _, in := range inputs {
		for _, changed := range []bool{true, false} {
			name := in.name + "/unchanged"
			if changed {
				name = in.name + "/after a change"
			}
			b.Run(name, func(b *testing.B) {
				b.Logf("input: %d lines, %d expressions", strings.Count(in.text, "\n"), in.rules)
				send, read := benchClient(b)
				send(didOpen(in.text, 1))
				read()
				for id := 2; b.Loop(); id++ {
					if changed {
						b.StopTimer()
						send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
							"textDocument":   map[string]any{"uri": uri, "version": id},
							"contentChanges": []map[string]any{{"text": in.text}},
						}})
						read()
						b.StartTimer()
					}
					send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "textDocument/codeLens", "params": map[string]any{
						"textDocument": map[string]any{"uri": uri},
					}})
					var lenses []json.RawMessage
					if m := read(); json.Unmarshal(m.Result, &lenses) != nil || len(lenses) < in.rules+in.dyn {
						b.Fatalf("codeLens = %d lenses (%.300s), want one for each of %d expressions and one for each of %d dyn values", len(lenses), m.Result, in.rules, in.dyn)
					}
				}
			})
		}
	}
}
