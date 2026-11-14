package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// The workspace of the rule index's benchmarks: 2,000 YAML files, 200 of
// them CRDs of 25 rules, 5,000 rules in all, and 1,800 custom resources.
const (
	indexFiles     = 2000
	indexCRDs      = 200
	indexCRDRules  = 25
	indexAllRules  = indexCRDs * indexCRDRules
	indexResources = indexFiles - indexCRDs
)

// indexCRD returns the k-th CustomResourceDefinition of the benchmarks'
// workspace, of gadgets of its own group, with rules rules, one on each
// group of properties of spec, one in ten of which does not compile.
func indexCRD(k, rules int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.g%[1]d.example.com
spec:
  group: g%[1]d.example.com
  names: {plural: gadgets, singular: gadget, kind: Gadget, listKind: GadgetList}
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
		rule := fmt.Sprintf("self.a%[1]d.size() < 10 && self.b%[1]d.all(x, x > 0)", i)
		if i%10 == 7 {
			rule = fmt.Sprintf("self.a%d.nope > 0", i)
		}
		fmt.Fprintf(&b, `                group%[1]d:
                  type: object
                  properties:
                    a%[1]d: {type: string, maxLength: 20}
                    b%[1]d: {type: array, maxItems: 10, items: {type: integer}}
                  x-kubernetes-validations:
                    - rule: %[2]s
                      message: group %[1]d is invalid
`, i, rule)
	}
	return b.String()
}

// writeIndexWorkspace writes the benchmarks' workspace into dir.
func writeIndexWorkspace(b *testing.B, dir string) {
	b.Helper()
	write := func(rel, text string) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	for k := range indexCRDs {
		write(fmt.Sprintf("crds/crd%03d.yaml", k), indexCRD(k, indexCRDRules))
	}
	for i := range indexResources {
		write(fmt.Sprintf("samples/s%02d/gadget%04d.yaml", i/100, i), fmt.Sprintf(
			"apiVersion: g%d.example.com/v1\nkind: Gadget\nmetadata:\n  name: gadget%d\nspec:\n  group0: {a0: x, b0: [1, 2]}\n", i%indexCRDs, i))
	}
}

// noExampleCheck keeps the check of the examples from starting, so the
// benchmarks measure the rule index alone.
func noExampleCheck(ss *session) {
	ss.exampleTimer = func(time.Duration) (<-chan time.Time, func() bool) {
		return make(chan time.Time), func() bool { return true }
	}
}

// BenchmarkRuleIndex measures cel-lens.ruleList on a workspace of 2,000
// YAML files, 200 of them CRDs of 25 rules (5,000 rules, one in ten that
// does not compile), and 1,800 custom resources, from the request to the
// response through the session's whole message loop: "empty cache" with
// every run from an empty cache, as the first answer is, which reads and
// checks every CRD; "unchanged" asked again with nothing changed, which
// walks the workspace, finds each file as it was and checks nothing. The
// budget of the second is 100 ms.
func BenchmarkRuleIndex(b *testing.B) {
	for _, empty := range []bool{true, false} {
		name := "unchanged"
		if empty {
			name = "empty cache"
		}
		b.Run(name, func(b *testing.B) {
			dir := b.TempDir()
			writeIndexWorkspace(b, dir)
			send, until := benchSession(b, dir, func(ss *session) {
				noExampleCheck(ss)
				if empty {
					ss.ruleWork = func(ctx context.Context, c *ruleCheck) (map[string]indexedFile, error) {
						c.memo = nil
						return c.run(ctx)
					}
				}
			})
			ask := func(id int) {
				send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "workspace/executeCommand", "params": map[string]any{"command": commandRuleList}})
				m := until(id, "")
				b.StopTimer() // what the client makes of the answer is not timed
				defer b.StartTimer()
				var l RuleList
				if err := json.Unmarshal(m.Result, &l); err != nil || m.Error != nil {
					b.Fatalf("ruleList = %+v, %v", m.Error, err)
				}
				rules, broken := 0, 0
				for _, d := range l.Documents {
					rules += len(d.Rules)
					for _, r := range d.Rules {
						if r.Compile.State == compileError {
							broken++
						}
					}
				}
				// Rules 7 and 17 of each CRD do not compile.
				if rules != indexAllRules || broken != indexCRDs*2 {
					b.Fatalf("ruleList = %d rules, %d that do not compile, want %d and %d", rules, broken, indexAllRules, indexCRDs*2)
				}
			}
			if !empty {
				b.StopTimer()
				ask(2) // the first answer fills the cache
				b.StartTimer()
			}
			b.Logf("input: %d YAML files, %d CRDs, %d rules", indexFiles, indexCRDs, indexAllRules)
			for id := 3; b.Loop(); id++ {
				ask(id)
			}
		})
	}
}

// BenchmarkRuleIndexRunning measures the requests the editor waits for on
// an open 1,000-line CRD, hover, completion and its code lenses, from the
// request to the response through the session's whole message loop, while
// the rule index runs on the workspace of BenchmarkRuleIndex from an empty
// cache, again and again, and, for comparison, while none runs. Before
// each request the CRD changes, a line added at its end, as when a rule is
// typed; the timer leaves out the change and its diagnostics. The index
// waits while the loop handles a message (busyGate) and checks one CRD at a
// time; its checks of CRDs share the Kubernetes version guard with the
// loop's. median-ms and worst-ms are what the person waits; the worst
// while the index runs must be within noise of the worst while none does,
// as BenchmarkClosedCRDs shows for the check of the examples.
func BenchmarkRuleIndexRunning(b *testing.B) {
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
			name := "no index/" + strings.TrimPrefix(rq.method, "textDocument/")
			if running {
				name = "index running/" + strings.TrimPrefix(rq.method, "textDocument/")
			}
			b.Run(name, func(b *testing.B) {
				dir := b.TempDir()
				writeIndexWorkspace(b, dir)
				crd := filepath.Join(dir, "open.yaml")
				if err := os.WriteFile(crd, []byte(text), 0o600); err != nil {
					b.Fatal(err)
				}
				started := make(chan struct{}, 1)
				send, until := benchSession(b, dir, func(ss *session) {
					noExampleCheck(ss)
					ss.ruleWork = func(ctx context.Context, c *ruleCheck) (map[string]indexedFile, error) {
						for {
							c.memo = nil // every run from an empty cache
							select {
							case started <- struct{}{}:
							default:
							}
							if _, err := c.run(ctx); err != nil {
								return nil, err
							}
						}
					}
				})
				u := pathURI(crd)
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
					"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": text},
				}})
				until(0, u)
				if running {
					send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "workspace/executeCommand", "params": map[string]any{"command": commandRuleList}})
					<-started
				}
				b.Logf("input: %d lines, the index running %t on %d YAML files, %d CRDs", strings.Count(text, "\n"), running, indexFiles, indexCRDs)
				var waits []time.Duration
				for id := 3; b.Loop(); id++ {
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
					b.StartTimer()
					start := time.Now()
					send(map[string]any{"jsonrpc": "2.0", "id": id, "method": rq.method, "params": rq.params(u)})
					m := until(id, "")
					waits = append(waits, time.Since(start))
					if !rq.ok(m.Result) {
						b.Fatalf("%s = %.300s, want an answer", rq.method, m.Result)
					}
				}
				slices.Sort(waits)
				b.ReportMetric(float64(waits[len(waits)/2])/float64(time.Millisecond), "median-ms")
				b.ReportMetric(float64(waits[len(waits)-1])/float64(time.Millisecond), "worst-ms")
			})
		}
	}
}

// BenchmarkRuleListChange measures how soon a client that follows the list
// (initializationOptions.ruleList) learns of an edit, on the workspace of
// BenchmarkRuleIndex (5,000 rules): one CRD file of 25 rules, open, is
// edited, its last rule broken and fixed in turn, and the time runs from the
// end of the pause after it, when the index runs again, to the notification
// cel-lens/ruleListChanged of the new generation, through the session's
// whole message loop. The budget is the diagnostics', 300 ms. The answer
// since the generation before then holds that one file; since-bytes is its
// size.
func BenchmarkRuleListChange(b *testing.B) {
	dir := b.TempDir()
	writeIndexWorkspace(b, dir)
	pauses := make(chan chan time.Time, 100)
	send, read := benchSessionWith(b, dir, map[string]any{"ruleList": true}, func(ss *session) {
		noExampleCheck(ss)
		ss.ruleTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
			ch := make(chan time.Time, 1)
			if d == ruleDelay {
				pauses <- ch // the benchmark ends the pause
			} else {
				ch <- time.Time{}
			}
			return ch, func() bool { return true }
		}
	})
	// notified reads the messages up to the notification of a generation
	// after gen, once no scan runs.
	notified := func(gen int64) int64 {
		for {
			m := read()
			if m.Method != notifyRuleList {
				continue
			}
			var n ruleNote
			if err := json.Unmarshal(m.Params, &n); err != nil {
				b.Fatal(err)
			}
			if n.Generation > gen && n.Scan.State == scanDone {
				return n.Generation
			}
		}
	}
	// change sends the change v after dropping the pauses begun before it,
	// and returns the pause it begins.
	change := func(v any) chan time.Time {
		for len(pauses) > 0 {
			<-pauses
		}
		send(v)
		return <-pauses
	}
	gen := notified(0) // the first scan
	path := filepath.Join(dir, "crds", "crd000.yaml")
	text, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	u := pathURI(path)
	broken := strings.Replace(string(text), "self.a24.size() < 10", "self.a24.nope < 10", 1)
	if broken == string(text) {
		b.Fatal("the CRD has no rule on a24 to break")
	}
	change(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": string(text)},
	}}) <- time.Time{}
	if l := askSince(b, send, read, 1, gen); len(l.Documents) > 0 {
		gen = l.Generation // opening it with its own text changes nothing
	}
	sinceBytes := 0
	for id := 2; b.Loop(); id++ {
		b.StopTimer()
		edit := string(text)
		if id%2 == 0 {
			edit = broken
		}
		pause := change(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{
			"textDocument": map[string]any{"uri": u, "version": id}, "contentChanges": []map[string]any{{"text": edit}},
		}})
		b.StartTimer()
		pause <- time.Time{}
		next := notified(gen)
		b.StopTimer()
		l := askSince(b, send, read, -id, gen)
		if l.Generation != next || len(l.Documents) != 1 || l.Documents[0].URI != u || len(l.Documents[0].Rules) != indexCRDRules {
			b.Fatalf("ruleList since %d = generation %d, %d documents, want %d, crd000.yaml alone", gen, l.Generation, len(l.Documents), next)
		}
		body, _ := json.Marshal(l)
		sinceBytes = len(body)
		gen = next
		b.StartTimer()
	}
	b.ReportMetric(float64(sinceBytes), "since-bytes")
}

// askSince sends cel-lens.ruleList since gen as request id and returns the
// answer.
func askSince(b *testing.B, send func(any), read func() jsonrpc.Message, id int, gen int64) RuleList {
	b.Helper()
	send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "workspace/executeCommand", "params": map[string]any{
		"command": commandRuleList, "arguments": []any{map[string]any{"since": gen}},
	}})
	for {
		m := read()
		if string(m.ID) != strconv.Itoa(id) {
			continue
		}
		var l RuleList
		if err := json.Unmarshal(m.Result, &l); err != nil || m.Error != nil {
			b.Fatalf("ruleList = %+v, %v", m.Error, err)
		}
		return l
	}
}
