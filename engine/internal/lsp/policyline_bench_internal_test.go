package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/examples"
)

// benchPolicy is a ValidatingAdmissionPolicy of Deployments with n
// validations, half of which read its params, bound with Deny to a
// ConfigMap of params.
func benchPolicy(n int) string {
	var b strings.Builder
	b.WriteString(`apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: bench}
spec:
  paramKind: {apiVersion: v1, kind: ConfigMap}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments]}
  variables:
    - {name: max, expression: "int(params.data.max)"}
  validations:
`)
	for i := range n {
		if i%2 == 0 {
			fmt.Fprintf(&b, "    - expression: \"object.spec.replicas <= variables.max + %d\"\n      messageExpression: \"'at most ' + string(variables.max + %d)\"\n", i, i)
		} else {
			fmt.Fprintf(&b, "    - expression: \"object.spec.template.spec.containers.all(c, c.image != 'bad%d')\"\n      message: no bad image\n", i)
		}
	}
	b.WriteString("---\napiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicyBinding\nmetadata: {name: bench}\nspec: {policyName: bench, validationActions: [Deny], paramRef: {name: limits, namespace: default, parameterNotFoundAction: Deny}}\n")
	return b.String()
}

// BenchmarkPolicyLineRunning measures hover and the code lenses of an open
// ValidatingAdmissionPolicy of 20 validations, through the session's whole
// message loop, while the check of the examples runs the policy on 100
// Deployments again and again, each time with an empty cache, and, for
// comparison, while no check runs (#19). The policy changes before each
// request, and the check starts at once, as in BenchmarkExampleRulesRunning.
// The results must not make the requests slower.
func BenchmarkPolicyLineRunning(b *testing.B) {
	const validations = 20
	text := benchPolicy(validations)
	lines := strings.Split(text, "\n")
	line := 0
	for i, l := range lines {
		if strings.Contains(l, "expression: \"object.spec.replicas") {
			line = i
		}
	}
	char := strings.Index(lines[line], "object.spec") + len("object.")
	requests := []struct {
		method string
		params func(u string) map[string]any
		ok     func(json.RawMessage) bool
	}{
		{"textDocument/hover", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": line, "character": char}}
		}, func(r json.RawMessage) bool { return strings.Contains(string(r), "Examples") }},
		{"textDocument/codeLens", func(u string) map[string]any {
			return map[string]any{"textDocument": map[string]any{"uri": u}}
		}, func(r json.RawMessage) bool {
			return strings.Count(string(r), "cel-lens.showExampleResults") >= validations
		}},
	}
	for _, running := range []bool{false, true} {
		for _, rq := range requests {
			name := "no check/" + strings.TrimPrefix(rq.method, "textDocument/")
			if running {
				name = "policy running on 100 examples/" + strings.TrimPrefix(rq.method, "textDocument/")
			}
			b.Run(name, func(b *testing.B) {
				dir := b.TempDir()
				policy := filepath.Join(dir, "policy.yaml")
				write := func(path, text string) {
					if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				write(policy, text)
				write(filepath.Join(dir, "params.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: limits}\ndata: {max: \"5\"}\n")
				for i := range 100 {
					replicas := 2
					if i%10 == 0 {
						replicas = 9
					}
					write(filepath.Join(dir, fmt.Sprintf("d%03d.yaml", i)), fmt.Sprintf("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: d%d}\nspec: {replicas: %d, selector: {matchLabels: {a: b}}, template: {metadata: {labels: {a: b}}, spec: {containers: [{name: c, image: i}]}}}\n", i, replicas))
				}
				b.Logf("input: %d validations, 100 examples", validations)
				started, release, ready := make(chan struct{}), make(chan struct{}), make(chan struct{})
				send, until := benchSession(b, dir, func(ss *session) {
					ss.exampleTimer = func(time.Duration) (<-chan time.Time, func() bool) { return nil, func() bool { return true } }
					ss.exampleWork = func(ctx context.Context, c *exampleCheck) (map[string]exampleFile, error) {
						select {
						case <-ready:
						default:
							return c.run(ctx) // until the lines say what the policy does
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
							c.policies = &examples.PolicyCache{} // every example is admitted again
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
				u := pathURI(policy)
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
					"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": text},
				}})
				until(0, u)
				for id := -1; ; id-- {
					send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "textDocument/codeLens", "params": map[string]any{"textDocument": map[string]any{"uri": u}}})
					if strings.Count(string(until(id, "").Result), "cel-lens.showExampleResults") >= validations {
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
					if !rq.ok(m.Result) {
						b.Fatalf("%s = %.300s, want an answer, with what the policy does", rq.method, m.Result)
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
