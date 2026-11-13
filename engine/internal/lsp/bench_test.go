package lsp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
	return benchClientIn(b, "")
}

// benchClientIn is benchClient with the folder, when not "", as the
// workspace.
func benchClientIn(b *testing.B, folder string) (send func(any), read func() jsonrpc.Message) {
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
	params := map[string]any{"capabilities": map[string]any{}}
	if folder != "" {
		params["workspaceFolders"] = []map[string]any{{"uri": fileURI(folder), "name": "ws"}}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": params})
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

// typingPause is how long after a change BenchmarkExamples sends a
// request: a little longer than the server waits after a change before it
// checks the examples, a little shorter than that and the check.
const typingPause = 255 * time.Millisecond

// BenchmarkExamples measures the requests the editor waits for on an open
// 1,000-line CRD, hover, completion and its code lenses, in a workspace
// with no example of it and with 100, one in ten with a problem, through
// the server's whole message loop, as typing pauses: before each request
// the CRD changes, a line added at its end, as when a rule is typed, and
// the request comes 260 ms later. A server that checks the examples does so
// from 250 ms after the last change, in the background, so the request
// comes while that check runs (it takes about 25 ms with 100 examples). The
// timer leaves the change, its diagnostics and the pause out. It needs
// nothing of the server but the protocol, so it runs as well on a server
// that checks no example; BenchmarkExampleCheckRunning measures a request
// during a check without timing. The examples must not make the requests
// slower.
func BenchmarkExamples(b *testing.B) {
	text := bigCRD(1000)
	lines := strings.Split(text, "\n")
	line := 0
	for i, l := range lines {
		if strings.Contains(l, "rule:") && strings.Contains(l, "self.size()") {
			line = i
		}
	}
	char := strings.Index(lines[line], "self.") + len("self.")
	requests := []struct {
		name, method string
		params       func(u string) map[string]any
		ok           func(json.RawMessage) bool
	}{
		{"hover", "textDocument/hover", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char - 2}}
		}, func(r json.RawMessage) bool { return strings.Contains(string(r), "self") }},
		{"completion", "textDocument/completion", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char}}
		}, func(r json.RawMessage) bool {
			return strings.Contains(string(r), "group") || strings.Contains(string(r), `"a`)
		}},
		{"codeLens", "textDocument/codeLens", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}}
		}, func(r json.RawMessage) bool { return strings.Count(string(r), "self: Widget") > 10 }},
	}
	for _, n := range []int{0, 100} {
		for _, rq := range requests {
			b.Run(fmt.Sprintf("%d examples/%s", n, rq.name), func(b *testing.B) {
				dir := b.TempDir()
				crd := filepath.Join(dir, "crd.yaml")
				if err := os.WriteFile(crd, []byte(text), 0o600); err != nil {
					b.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(dir, "samples"), 0o750); err != nil {
					b.Fatal(err)
				}
				for i := range n {
					extra := ""
					if i%10 == 0 {
						extra = "\n  extra: 1"
					}
					example := fmt.Sprintf("apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w%d\nspec:\n  group0:\n    a0: x\n    b0: [1, 2]%s\n", i, extra)
					if err := os.WriteFile(filepath.Join(dir, "samples", fmt.Sprintf("w%03d.yaml", i)), []byte(example), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				b.Logf("input: %d lines, %d examples", strings.Count(text, "\n"), n)
				send, read := benchClientIn(b, dir)
				u := fileURI(crd)
				// until reads the messages up to the response to id, or the
				// diagnostics of u for id 0.
				until := func(id int) jsonrpc.Message {
					for {
						m := read()
						if id == 0 && m.Method == "textDocument/publishDiagnostics" && strings.Contains(string(m.Params), u) {
							return m
						}
						if id != 0 && string(m.ID) == strconv.Itoa(id) {
							return m
						}
					}
				}
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
					"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": text},
				}})
				until(0)
				for id := 2; b.Loop(); id++ {
					b.StopTimer()
					changed := time.Now()
					send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
						"textDocument":   map[string]any{"uri": u, "version": id},
						"contentChanges": []map[string]any{{"text": text + fmt.Sprintf("# %d\n", id)}},
					}})
					until(0)
					// The person stops typing; a pause, not a wait for
					// something the test could see.
					time.Sleep(time.Until(changed.Add(typingPause)))
					b.StartTimer()
					send(map[string]any{"jsonrpc": "2.0", "id": id, "method": rq.method, "params": rq.params(u)})
					if m := until(id); !rq.ok(m.Result) {
						b.Fatalf("%s = %.300s, want an answer", rq.method, m.Result)
					}
				}
			})
		}
	}
}

// benchRules is how many rules rulesCRD has.
const benchRules = 50

// rulesCRD returns a CustomResourceDefinition of widgets with benchRules
// groups of properties under spec, each with a rule, and note at the end of
// the message of the first, so that changing note changes one rule.
func rulesCRD(note string) string {
	var b strings.Builder
	b.WriteString(`apiVersion: apiextensions.k8s.io/v1
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
`)
	for i := range benchRules {
		fmt.Fprintf(&b, `                group%d:
                  type: object
                  properties:
                    a%d: {type: string, maxLength: 20}
                    b%d: {type: array, maxItems: 10, items: {type: integer}}
                  x-kubernetes-validations:
                    - rule: self.a%d.size() < 10 && self.b%d.all(x, x > 0)
                      message: group %d is invalid%s
`, i, i, i, i, i, i, note)
		note = ""
	}
	return b.String()
}

// BenchmarkExampleResults measures cel-lens.exampleResults on a CRD of 50
// rules with 100 examples, each with a value for every rule, one in ten
// refused by one, through the server's whole message loop: "changed
// schema" with the CRD's schema changed on disk before each request, so
// every rule runs on every example again; "one rule changed" with the
// message of one rule changed, so it alone runs again; "unchanged" asking
// again, so every result is kept. Each request also reads the CRD, walks
// the workspace and checks the examples against the schema.
func BenchmarkExampleResults(b *testing.B) {
	const rules, examples = benchRules, 100
	for _, tc := range []struct {
		name string
		crd  func(i int) string
	}{
		{"changed schema", func(i int) string {
			return strings.Replace(rulesCRD(""), "type: object\n          properties:", fmt.Sprintf("type: object\n          description: run %d\n          properties:", i), 1)
		}},
		{"one rule changed", func(i int) string { return rulesCRD(fmt.Sprintf(" (%d)", i)) }},
		{"unchanged", func(int) string { return rulesCRD("") }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			dir := b.TempDir()
			crd := filepath.Join(dir, "crd.yaml")
			if err := os.Mkdir(filepath.Join(dir, "samples"), 0o750); err != nil {
				b.Fatal(err)
			}
			for i := range examples {
				var spec strings.Builder
				for g := range rules {
					a := "x"
					if i%10 == 0 && g == i%rules {
						a = "much too long"
					}
					fmt.Fprintf(&spec, "  group%d: {a%d: %s, b%d: [1, 2]}\n", g, g, a, g)
				}
				example := fmt.Sprintf("apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w%d\nspec:\n%s", i, spec.String())
				if err := os.WriteFile(filepath.Join(dir, "samples", fmt.Sprintf("w%03d.yaml", i)), []byte(example), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			send, read := benchClientIn(b, dir)
			u := fileURI(crd)
			ask := func(id int) json.RawMessage {
				send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "workspace/executeCommand", "params": map[string]any{
					"command": "cel-lens.exampleResults", "arguments": []any{map[string]any{"uri": u}},
				}})
				for {
					if m := read(); string(m.ID) == strconv.Itoa(id) {
						if m.Error != nil {
							b.Fatalf("exampleResults error = %+v", m.Error)
						}
						return m.Result
					}
				}
			}
			if err := os.WriteFile(crd, []byte(tc.crd(0)), 0o600); err != nil {
				b.Fatal(err)
			}
			first := ask(2)
			if got := strings.Count(string(first), `"result":"refused"`); got != examples/10 {
				b.Fatalf("exampleResults = %d refusals, want %d", got, examples/10)
			}
			b.Logf("input: %d rules, %d examples, %d results", rules, examples, strings.Count(string(first), `"result":`))
			for id := 3; b.Loop(); id++ {
				b.StopTimer()
				if err := os.WriteFile(crd, []byte(tc.crd(id)), 0o600); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				ask(id)
			}
		})
	}
}

// BenchmarkExampleResultsRunning measures hover and the code lenses of an
// open CRD of 50 rules, with 100 examples, as it is edited while
// cel-lens.exampleResults runs: the command is sent for the CRD as it is,
// and then, timed, five times the CRD changes, which the server checks on
// its message loop, and the request is sent and answered; ns/op is for the
// five. The command must not make them slower: its job does not check an
// open CRD again, so the loop's check of a change never waits for it.
// Where the server has no such command, it answers it with an error at
// once, which gives the time with nothing running beside.
func BenchmarkExampleResultsRunning(b *testing.B) {
	const rules, examples = benchRules, 100
	text := func(i int) string { return rulesCRD(fmt.Sprintf(" (%d)", i)) }
	lines := strings.Split(text(0), "\n")
	line := 0
	for i, l := range lines {
		if strings.Contains(l, "rule: self.a1.size()") {
			line = i
		}
	}
	char := strings.Index(lines[line], "self.a1") + len("self.")
	requests := []struct {
		name, method string
		params       func(u string) map[string]any
		ok           func(json.RawMessage) bool
	}{
		{"hover", "textDocument/hover", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char}}
		}, func(r json.RawMessage) bool { return strings.Contains(string(r), "a1") }},
		{"codeLens", "textDocument/codeLens", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}}
		}, func(r json.RawMessage) bool { return strings.Count(string(r), "self: Widget") >= rules }},
	}
	for _, rq := range requests {
		b.Run(rq.name, func(b *testing.B) {
			dir := b.TempDir()
			crd := filepath.Join(dir, "crd.yaml")
			if err := os.WriteFile(crd, []byte(text(0)), 0o600); err != nil {
				b.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "samples"), 0o750); err != nil {
				b.Fatal(err)
			}
			for i := range examples {
				var spec strings.Builder
				for g := range rules {
					fmt.Fprintf(&spec, "  group%d: {a%d: x, b%d: [1, 2]}\n", g, g, g)
				}
				example := fmt.Sprintf("apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w%d\nspec:\n%s", i, spec.String())
				if err := os.WriteFile(filepath.Join(dir, "samples", fmt.Sprintf("w%03d.yaml", i)), []byte(example), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			send, messages := benchStream(b, dir)
			u := fileURI(crd)
			answered := map[string]bool{}
			// until reads the messages up to the response to id, keeping
			// which others were answered.
			until := func(id string) jsonrpc.Message {
				for {
					m, ok := <-messages
					if !ok {
						b.Fatal("the server ended")
					}
					if m.Method == "" && len(m.ID) > 0 {
						answered[string(m.ID)] = true
						if string(m.ID) == id {
							return m
						}
					}
				}
			}
			send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
				"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": text(1)},
			}})
			version := 1
			change := func() {
				version++
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
					"textDocument":   map[string]any{"uri": u, "version": version},
					"contentChanges": []map[string]any{{"text": text(version)}},
				}})
			}
			id := 10
			for b.Loop() {
				b.StopTimer()
				id += 2
				command, request := strconv.Itoa(id), "\"request\""
				send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "workspace/executeCommand", "params": map[string]any{
					"command": "cel-lens.exampleResults", "arguments": []any{map[string]any{"uri": u}},
				}})
				b.StartTimer()
				for range 5 {
					change()
					send(map[string]any{"jsonrpc": "2.0", "id": "request", "method": rq.method, "params": rq.params(u)})
					if m := until(request); !rq.ok(m.Result) {
						b.Fatalf("%s = %.300s, want an answer", rq.method, m.Result)
					}
				}
				b.StopTimer()
				if !answered[command] {
					until(command)
				}
				b.StartTimer()
			}
		})
	}
}

// benchStream is benchClientIn whose messages from the server are read as
// they come, into the channel, so the server never waits to write one while
// the benchmark sends; the channel is closed when the server ends.
func benchStream(b *testing.B, folder string) (send func(any), messages <-chan jsonrpc.Message) {
	b.Helper()
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- new(lsp.Server).Serve(context.Background(), serverIn, serverOut)
		_ = serverOut.Close()
	}()
	out := make(chan jsonrpc.Message, 1024)
	read := make(chan struct{})
	go func() {
		defer close(read)
		defer close(out)
		in := jsonrpc.NewConn(clientIn, io.Discard)
		for {
			m, err := in.Read()
			if err != nil {
				return
			}
			out <- m
		}
	}()
	b.Cleanup(func() {
		_ = clientOut.Close()
		<-done
		_ = clientIn.Close()
		// What is left is drained, so the reader ends.
		for range out {
		}
		<-read
	})
	send = func(v any) {
		body, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(clientOut, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
			b.Fatal(err)
		}
	}
	params := map[string]any{"capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": fileURI(folder), "name": "ws"}}}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": params})
	for m := range out {
		if m.Method == "" && string(m.ID) == "1" {
			break
		}
	}
	return send, out
}

// BenchmarkExampleLine measures hover and the code lenses of an open CRD of
// 50 rules with 100 examples, each with a value for every rule, one in ten
// refused by one, through the server's whole message loop, while what the
// rules do to the examples is worked out: before each request the message
// of the first rule changes, and the request comes typingPause later, as
// the check of the examples, and of what the rules do to them, starts after
// a pause in typing. The timer leaves the change, its diagnostics and the
// pause out. Before the first, it waits, up to five seconds, for the lines
// to say what the rules do, which a server that works out nothing of the
// examples never says; the lines then say it from what is known while the
// next results are worked out. It needs nothing of the server but the
// protocol, so it runs on any. The results must not make the requests
// slower.
func BenchmarkExampleLine(b *testing.B) {
	const rules, examples = benchRules, 100
	text := func(i int) string { return rulesCRD(fmt.Sprintf(" (%d)", i)) }
	lines := strings.Split(text(0), "\n")
	line := 0
	for i, l := range lines {
		if strings.Contains(l, "rule: self.a1.size()") {
			line = i
		}
	}
	char := strings.Index(lines[line], "self.a1") + len("self.")
	requests := []struct {
		name, method string
		params       func(u string) map[string]any
		ok           func(json.RawMessage) bool
	}{
		{"hover", "textDocument/hover", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char}}
		}, func(r json.RawMessage) bool { return strings.Contains(string(r), "a1") }},
		{"codeLens", "textDocument/codeLens", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}}
		}, func(r json.RawMessage) bool { return strings.Count(string(r), "self: Widget") >= rules }},
	}
	for _, rq := range requests {
		b.Run(rq.name, func(b *testing.B) {
			dir := b.TempDir()
			crd := filepath.Join(dir, "crd.yaml")
			if err := os.WriteFile(crd, []byte(text(0)), 0o600); err != nil {
				b.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "samples"), 0o750); err != nil {
				b.Fatal(err)
			}
			for i := range examples {
				var spec strings.Builder
				for g := range rules {
					a := "x"
					if i%10 == 0 && g == i%rules {
						a = "much too long"
					}
					fmt.Fprintf(&spec, "  group%d: {a%d: %s, b%d: [1, 2]}\n", g, g, a, g)
				}
				example := fmt.Sprintf("apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w%d\nspec:\n%s", i, spec.String())
				if err := os.WriteFile(filepath.Join(dir, "samples", fmt.Sprintf("w%03d.yaml", i)), []byte(example), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			send, read := benchClientIn(b, dir)
			u := fileURI(crd)
			// until reads the messages up to the response to id, or the
			// diagnostics of u for id 0.
			until := func(id int) jsonrpc.Message {
				for {
					m := read()
					if id == 0 && m.Method == "textDocument/publishDiagnostics" && strings.Contains(string(m.Params), u) {
						return m
					}
					if id != 0 && string(m.ID) == strconv.Itoa(id) {
						return m
					}
				}
			}
			send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
				"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": text(1)},
			}})
			until(0)
			id := 2
			shown := false
			for deadline := time.Now().Add(5 * time.Second); !shown && time.Now().Before(deadline); id++ {
				send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "textDocument/codeLens", "params": map[string]any{"textDocument": map[string]any{"uri": u}}})
				shown = strings.Count(string(until(id).Result), "cel-lens.showExampleResults") >= rules
				if !shown {
					time.Sleep(50 * time.Millisecond) // the results come in the background
				}
			}
			b.Logf("input: %d rules, %d examples; the lines say what the rules do: %t", rules, examples, shown)
			for ; b.Loop(); id++ {
				b.StopTimer()
				changed := time.Now()
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
					"textDocument":   map[string]any{"uri": u, "version": id},
					"contentChanges": []map[string]any{{"text": text(id)}},
				}})
				until(0)
				// The person stops typing; a pause, not a wait for
				// something the test could see.
				time.Sleep(time.Until(changed.Add(typingPause)))
				b.StartTimer()
				send(map[string]any{"jsonrpc": "2.0", "id": id, "method": rq.method, "params": rq.params(u)})
				if m := until(id); !rq.ok(m.Result) {
					b.Fatalf("%s = %.300s, want an answer", rq.method, m.Result)
				}
			}
		})
	}
}

// widgetsCRD returns a CustomResourceDefinition of the kind Widget<k> in
// the group g<k>.example.com, with rules groups of properties under spec,
// each with a rule, and note at the end of the message of the first.
func widgetsCRD(rules, k int, note string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets%[1]d.g%[1]d.example.com
spec:
  group: g%[1]d.example.com
  names: {plural: widgets%[1]d, singular: widget%[1]d, kind: Widget%[1]d, listKind: Widget%[1]dList}
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
`, k)
	for i := range rules {
		fmt.Fprintf(&b, `                group%[1]d:
                  type: object
                  properties:
                    a%[1]d: {type: string, maxLength: 20}
                    b%[1]d: {type: array, maxItems: 10, items: {type: integer}}
                  x-kubernetes-validations:
                    - rule: self.a%[1]d.size() < 10 && self.b%[1]d.all(x, x > 0)
                      message: group %[1]d is invalid%[2]s
`, i, note)
		note = ""
	}
	return b.String()
}

// BenchmarkClosedCRDs measures what a person typing in a small open CRD
// waits for while the CRDs that are not open are checked in the background
// (#14): in a workspace of 20 such CRDs of 50 rules, with or without 5
// examples each, the open CRD of 5 rules is changed 78 times, 260 to 960 ms
// apart, and each change is followed at once by a hover, timed from the
// change to the hover's answer. With examples, the check of the examples
// runs from the start of the session and checks each closed CRD with
// pkg/check, as the API server would, to know which of its rules the API
// server refuses it for; the loop checks each change of the open CRD with
// pkg/check too. An op is one such session, from a server started fresh,
// so ns/op is mostly the pauses; median-ms and worst-ms are what the
// person waits, over the session's 78 changes. The worst with examples must
// be within noise of the worst without.
func BenchmarkClosedCRDs(b *testing.B) {
	const crds, rules, examples, changes = 20, benchRules, 5, 78
	for _, withExamples := range []bool{false, true} {
		b.Run(fmt.Sprintf("examples=%t", withExamples), func(b *testing.B) {
			dir := b.TempDir()
			for k := 1; k <= crds; k++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("crd%02d.yaml", k)), []byte(widgetsCRD(rules, k, "")), 0o600); err != nil {
					b.Fatal(err)
				}
				if !withExamples {
					continue
				}
				var spec strings.Builder
				for g := range rules {
					fmt.Fprintf(&spec, "  group%[1]d: {a%[1]d: x, b%[1]d: [1, 2]}\n", g)
				}
				for i := range examples {
					example := fmt.Sprintf("apiVersion: g%[1]d.example.com/v1\nkind: Widget%[1]d\nmetadata:\n  name: w%[2]d\nspec:\n%[3]s", k, i, spec.String())
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("ex%02d-%d.yaml", k, i)), []byte(example), 0o600); err != nil {
						b.Fatal(err)
					}
				}
			}
			open := filepath.Join(dir, "open.yaml")
			if err := os.WriteFile(open, []byte(widgetsCRD(5, 0, "")), 0o600); err != nil {
				b.Fatal(err)
			}
			u := fileURI(open)
			lines := strings.Split(widgetsCRD(5, 0, ""), "\n")
			line := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "rule: self.a1.") })
			char := strings.Index(lines[line], "self.a1") + len("self.")
			var waits []time.Duration
			for b.Loop() {
				b.StopTimer()
				send, messages := benchStream(b, dir)
				// until reads the messages up to the response to id.
				until := func(id int) {
					for m := range messages {
						if m.Method == "" && string(m.ID) == strconv.Itoa(id) {
							return
						}
					}
					b.Fatal("the server ended")
				}
				b.StartTimer()
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
					"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": widgetsCRD(5, 0, " (1)")},
				}})
				for i := 2; i < changes+2; i++ {
					start := time.Now()
					send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
						"textDocument":   map[string]any{"uri": u, "version": i},
						"contentChanges": []map[string]any{{"text": widgetsCRD(5, 0, fmt.Sprintf(" (%d)", i))}},
					}})
					send(map[string]any{"jsonrpc": "2.0", "id": i, "method": "textDocument/hover", "params": map[string]any{
						"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char},
					}})
					until(i)
					waits = append(waits, time.Since(start))
					// The person types on; a pause, not a wait for
					// something the benchmark could see.
					time.Sleep(time.Until(start.Add(time.Duration(260+(i*37)%700) * time.Millisecond)))
				}
			}
			slices.Sort(waits)
			b.ReportMetric(float64(waits[len(waits)/2])/float64(time.Millisecond), "median-ms")
			b.ReportMetric(float64(waits[len(waits)-1])/float64(time.Millisecond), "worst-ms")
		})
	}
}
