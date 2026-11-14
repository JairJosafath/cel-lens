package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/internal/examples"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// ruledCRD returns a CustomResourceDefinition of widgets of about lines
// lines, with a rule on every group of properties.
func ruledCRD(lines int) string {
	var b strings.Builder
	b.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: example.com\n  names:\n    kind: Widget\n    plural: widgets\n  scope: Namespaced\n  versions:\n    - name: v1\n      served: true\n      storage: true\n      schema:\n        openAPIV3Schema:\n          type: object\n          properties:\n            spec:\n              type: object\n              properties:\n")
	for i := 0; strings.Count(b.String(), "\n") < lines-14; i++ {
		fmt.Fprintf(&b, `                group%d:
                  type: object
                  properties:
                    a%d:
                      type: string
                      maxLength: 20
                    b%d:
                      type: array
                      maxItems: 10
                      items:
                        type: integer
                  x-kubernetes-validations:
                    - rule: "self.size() >= 0 && self.a%d.size() < 10 && self.b%d.all(x, x > 0)"
                      message: group %d is invalid
`, i, i, i, i, i, i)
	}
	return b.String()
}

// BenchmarkExampleCheckRunning measures the requests the editor waits for
// on an open 1,000-line CRD, hover, completion and its code lenses, from the
// request to the response through the session's whole message loop, while
// a check of 100 examples of the CRD, one in ten with a problem, is in
// progress, and, for comparison, while none is. Before each request the
// CRD changes, a line added at its end, as when a rule is typed. With a
// check, the session starts it at once instead of exampleDelay after the
// change, and the check does its work again and again from before the
// request is sent until the response is read, so every request timed comes
// in the middle of a check; the check waits while the loop handles the
// request (busyGate). The timer leaves out the change, its diagnostics and
// the end of the check. Without one, no check starts. The check must not
// make the requests slower.
func BenchmarkExampleCheckRunning(b *testing.B) {
	text := ruledCRD(1000)
	lines := strings.Split(text, "\n")
	line := 0
	for i, l := range lines {
		if strings.Contains(l, "rule:") {
			line = i
		}
	}
	char := strings.Index(lines[line], "self.a") + len("self.")
	requests := []struct {
		method string
		params func(u string) map[string]any
		ok     func(json.RawMessage) bool
	}{
		{"textDocument/hover", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char - 2}}
		}, func(r json.RawMessage) bool { return strings.Contains(string(r), "self") }},
		{"textDocument/completion", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char}}
		}, func(r json.RawMessage) bool { return strings.Contains(string(r), `"a`) }},
		{"textDocument/codeLens", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}}
		}, func(r json.RawMessage) bool { return strings.Count(string(r), "self: Widget") > 10 }},
	}
	for _, running := range []bool{false, true} {
		for _, rq := range requests {
			name := "no check/" + strings.TrimPrefix(rq.method, "textDocument/")
			if running {
				name = "check of 100 examples running/" + strings.TrimPrefix(rq.method, "textDocument/")
			}
			b.Run(name, func(b *testing.B) {
				dir := b.TempDir()
				crd := filepath.Join(dir, "crd.yaml")
				if err := os.WriteFile(crd, []byte(text), 0o600); err != nil {
					b.Fatal(err)
				}
				for i := range 100 {
					extra := ""
					if i%10 == 0 {
						extra = "\n  extra: 1"
					}
					example := fmt.Sprintf("apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w%d\nspec:\n  group0:\n    a0: x\n    b0: [1, 2]%s\n", i, extra)
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("w%03d.yaml", i)), []byte(example), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				b.Logf("input: %d lines, 100 examples", strings.Count(text, "\n"))
				started, release := make(chan struct{}), make(chan struct{})
				send, until := benchSession(b, dir, func(ss *session) {
					if !running {
						ss.exampleTimer = func(time.Duration) (<-chan time.Time, func() bool) {
							return make(chan time.Time), func() bool { return true }
						}
						return
					}
					ss.exampleTimer = func(time.Duration) (<-chan time.Time, func() bool) { return nil, func() bool { return true } }
					ss.exampleWork = func(ctx context.Context, c *exampleCheck) (map[string]exampleFile, error) {
						select {
						case started <- struct{}{}:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
						for {
							files, err := c.run(ctx)
							if err != nil {
								return nil, err
							}
							select {
							case <-release:
								return files, nil
							default:
							}
						}
					}
				})
				u := pathURI(crd)
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
					"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": text},
				}})
				until(0, u)
				for id := 2; b.Loop(); id++ {
					b.StopTimer()
					send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
						"textDocument":   map[string]any{"uri": u, "version": id},
						"contentChanges": []map[string]any{{"text": text + fmt.Sprintf("# %d\n", id)}},
					}})
					until(0, u)
					if running {
						<-started
					}
					b.StartTimer()
					send(map[string]any{"jsonrpc": "2.0", "id": id, "method": rq.method, "params": rq.params(u)})
					m := until(id, "")
					b.StopTimer()
					if !rq.ok(m.Result) {
						b.Fatalf("%s = %.300s, want an answer", rq.method, m.Result)
					}
					if running {
						release <- struct{}{}
					}
					b.StartTimer()
				}
			})
		}
	}
}

// BenchmarkExampleRulesRunning measures hover and the code lenses of an
// open CRD of 50 rules, with 100 examples that have a value for each rule,
// one in ten refused by one, from the request to the response through the
// session's whole message loop, while the check of the examples runs every
// rule on every example, and, for comparison, while no check runs. As in
// BenchmarkExampleCheckRunning, the CRD changes before each request, and
// the check starts at once and does its work again and again until the
// response is read, each time with an empty cache of results, so the
// rules run while the request is handled (#15); without one, the check
// that starts does nothing. Before the first, whole checks run until the
// lines say what the rules do, and the requests that ask for them read
// what the server sent before. The results must not make the requests
// slower.
func BenchmarkExampleRulesRunning(b *testing.B) {
	const rules = 50
	// The rules of ruledCRD without self.size(), which a rule on an object
	// cannot call: each compiles.
	text := strings.ReplaceAll(ruledCRD(rules*14+32), "self.size() >= 0 && ", "")
	if n := strings.Count(text, "rule:"); n != rules {
		b.Fatalf("CRD with %d rules, want %d", n, rules)
	}
	lines := strings.Split(text, "\n")
	line := 0
	for i, l := range lines {
		if strings.Contains(l, "rule:") {
			line = i
		}
	}
	char := strings.Index(lines[line], "self.a") + len("self.")
	requests := []struct {
		method string
		params func(u string) map[string]any
		ok     func(json.RawMessage) bool
	}{
		{"textDocument/hover", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char - 2}}
		}, func(r json.RawMessage) bool { return strings.Contains(string(r), "self") }},
		{"textDocument/codeLens", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}}
		}, func(r json.RawMessage) bool { return strings.Count(string(r), "self: Widget") >= rules }},
	}
	for _, running := range []bool{false, true} {
		for _, rq := range requests {
			name := "no check/" + strings.TrimPrefix(rq.method, "textDocument/")
			if running {
				name = "rules running on 100 examples/" + strings.TrimPrefix(rq.method, "textDocument/")
			}
			b.Run(name, func(b *testing.B) {
				dir := b.TempDir()
				crd := filepath.Join(dir, "crd.yaml")
				if err := os.WriteFile(crd, []byte(text), 0o600); err != nil {
					b.Fatal(err)
				}
				for i := range 100 {
					var spec strings.Builder
					for g := range rules {
						a := "x"
						if i%10 == 0 && g == i%rules {
							a = "much too long"
						}
						fmt.Fprintf(&spec, "  group%d: {a%d: %s, b%d: [1, 2]}\n", g, g, a, g)
					}
					example := fmt.Sprintf("apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w%d\nspec:\n%s", i, spec.String())
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("w%03d.yaml", i)), []byte(example), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				b.Logf("input: %d rules, 100 examples", rules)
				started, release, ready := make(chan struct{}), make(chan struct{}), make(chan struct{})
				send, until := benchSession(b, dir, func(ss *session) {
					ss.exampleTimer = func(time.Duration) (<-chan time.Time, func() bool) { return nil, func() bool { return true } }
					ss.exampleWork = func(ctx context.Context, c *exampleCheck) (map[string]exampleFile, error) {
						select {
						case <-ready:
						default:
							return c.run(ctx) // until the lines say what the rules do
						}
						if !running {
							<-ctx.Done() // a check started, that does nothing
							return nil, ctx.Err()
						}
						select {
						case started <- struct{}{}:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
						for {
							c.results = &examples.Cache{} // every rule runs again
							files, err := c.run(ctx)
							if err != nil {
								return nil, err
							}
							select {
							case <-release:
								return files, nil
							default:
							}
						}
					}
				})
				u := pathURI(crd)
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
					"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": text},
				}})
				// Whole checks run until the lines say what the rules do; each
				// request reads what the server sent before its answer.
				until(0, u)
				for id := -1; ; id-- {
					send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "textDocument/codeLens", "params": map[string]any{"textDocument": map[string]any{"uri": u}}})
					if strings.Count(string(until(id, "").Result), "cel-lens.showExampleResults") >= rules {
						break
					}
					time.Sleep(50 * time.Millisecond) // the results come in the background
				}
				close(ready)
				for id := 2; b.Loop(); id++ {
					b.StopTimer()
					send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
						"textDocument":   map[string]any{"uri": u, "version": id},
						"contentChanges": []map[string]any{{"text": text + fmt.Sprintf("# %d\n", id)}},
					}})
					// The diagnostics of the change, not those of a check
					// that ended before it.
					for {
						if m := until(0, u); strings.Contains(string(m.Params), fmt.Sprintf(`"version":%d}`, id)) {
							break
						}
					}
					if running {
						<-started
					}
					b.StartTimer()
					send(map[string]any{"jsonrpc": "2.0", "id": id, "method": rq.method, "params": rq.params(u)})
					m := until(id, "")
					b.StopTimer()
					if !rq.ok(m.Result) || rq.method == "textDocument/codeLens" && !strings.Contains(string(m.Result), "showExampleResults") {
						b.Fatalf("%s = %.300s, want an answer, with what the rules do", rq.method, m.Result)
					}
					if running {
						release <- struct{}{}
					}
					b.StartTimer()
				}
			})
		}
	}
}

// benchSession serves a session, set up by setup, over pipes, initialized
// with the folder as its workspace, until the benchmark ends. send writes
// a message to it; until reads its messages up to the response to id, or,
// for id 0, the diagnostics of u.
func benchSession(b *testing.B, folder string, setup func(*session)) (send func(any), until func(id int, u string) jsonrpc.Message) {
	b.Helper()
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		b.Fatal(err)
	}
	ss := &session{conn: jsonrpc.NewConn(serverIn, serverOut), assist: a, kube: kubeversion.Default(), docs: map[string]*document{}, features: feature.Of(feature.CellensTypes)}
	setup(ss)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ss.serve(ctx, serverIn)
		_ = serverOut.Close()
	}()
	b.Cleanup(func() {
		cancel()
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
	until = func(id int, u string) jsonrpc.Message {
		for {
			m, err := in.Read()
			if err != nil {
				b.Fatal(err)
			}
			if id == 0 && m.Method == "textDocument/publishDiagnostics" && strings.Contains(string(m.Params), `"uri":"`+u+`"`) {
				return m
			}
			if id != 0 && string(m.ID) == strconv.Itoa(id) {
				return m
			}
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": pathURI(folder), "name": "ws"}},
		"initializationOptions": map[string]any{"experimental": []string{"cellens-types"}},
	}})
	until(1, "")
	return send, until
}
