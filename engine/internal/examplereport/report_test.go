package examplereport_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/examplereport"
	"github.com/JairJosafath/cel-lens/engine/internal/lsp"
)

// reportOf reads the report in the JSON of cel-lens.exampleResults, with the
// problems beside the rules.
func reportOf(t *testing.T, text string) *lsp.ExamplesReport {
	t.Helper()
	var r lsp.ExamplesReport
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		t.Fatalf("report %s: %v", text, err)
	}
	return &r
}

// result is the JSON of one result of a rule: the example at line of
// samples/file, what it expects, and the rest of the result.
func result(file string, line int, expect, rest string) string {
	return resultAs("create", file, line, expect, rest)
}

// resultAs is result of a create or an update, as operation says.
func resultAs(operation, file string, line int, expect, rest string) string {
	return `{"example": {"uri": "file:///w/config/samples/` + file + `", "file": "` + file + `", "line": ` + itoa(line) + `, "operation": "` + operation + `", "expect": "` + expect + `"}, ` + rest + `}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// rule is the JSON of a CRD's rule at line of its file with the results.
func rule(text string, line int, results ...string) string {
	return `{"document": "CustomResourceDefinition widgets.example.com", "version": "v1", "path": "spec", "index": 0, "rule": ` + quote(text) +
		`, "location": {"uri": "file:///w/config/crd.yaml", "range": {"start": {"line": ` + itoa(line-1) + `, "character": 4}, "end": {"line": ` + itoa(line-1) + `, "character": 9}}}, "results": [` + strings.Join(results, ",") + `]}`
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// every is a report with each kind of result: a rule that refuses what it
// should allow, one that errors, one that passes, one that does not run, an
// example that expects to be refused and that nothing refuses, and a
// problem of an example.
var every = `{"kubernetesVersion": "1.37", "rules": [` +
	rule("self.size <= 10", 22,
		result("ok.yaml", 1, "allowed", `"result": "allowed", "expected": true`),
		result("huge.yaml", 3, "allowed", `"result": "refused", "refusals": [{"path": "spec", "message": "spec: Invalid value: at most 10"}], "expected": false, "wrong": "rule"`),
		result("invalid/big.yaml", 1, "refused", `"result": "refused", "refusals": [{"path": "spec", "message": "spec: Invalid value: at most 10"}], "expected": true`),
	) + `,` +
	rule("self.size == oldSelf.size", 24,
		result("ok.yaml", 1, "allowed", `"result": "not run", "reason": "the example is not an update and the rule needs oldSelf", "expected": true`),
	) + `,` +
	rule("self.a / self.b > 0 && self.some.very.long.rule.that.goes.on.and.on.and.on.past.the.limit", 30,
		result("div.yaml", 1, "allowed", `"result": "error", "error": "spec: Invalid value: division by zero", "expected": false, "wrong": "rule"`),
		result("invalid/zero.yaml", 1, "refused", `"result": "error", "error": "spec: Invalid value: division by zero", "expected": true`),
	) + `,` +
	rule("self.size > 0", 40,
		result("invalid/nothing.yaml", 2, "refused", `"result": "allowed", "expected": false, "wrong": "example"`),
		resultAs("update", "invalid/nothing.yaml", 2, "refused", `"result": "allowed", "binding": "b", "expected": false, "wrong": "example"`),
	) + `,` +
	rule("self.unused", 50) +
	`], "filesLeftOut": 1, "problems": [{"uri": "file:///w/config/samples/bad.yaml", "file": "bad.yaml", "line": 4, "column": 8, "severity": "error", "message": "spec.size: Invalid value:\n \"string\""}]}`

// TestCount checks what a report holds: the rules, the examples any rule
// has a result for, a rule's error as an error and not as wrong, a rule that
// did not run apart, the verdicts for which the example is to blame not for
// each rule, and the problems of the examples as wrong.
func TestCount(t *testing.T) {
	t.Parallel()
	got := examplereport.Count(reportOf(t, every))
	want := examplereport.Counts{Rules: 5, Examples: 6, Wrong: 3, Errors: 1, NotRun: 1, FilesLeftOut: 1}
	if got != want {
		t.Errorf("Count = %+v, want %+v", got, want)
	}
}

// TestFailed checks what each --fail-on value fails a run for.
func TestFailed(t *testing.T) {
	t.Parallel()
	const crdRule = `{"document": "CustomResourceDefinition x", "rule": "r", "location": {"uri": "file:///x.yaml", "range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 1}}}, "results": [`
	one := func(rest string) string {
		return `{"rules": [` + crdRule + result("a.yaml", 1, "allowed", rest) + `]}]}`
	}
	tests := []struct {
		name   string
		report string
		want   map[examplereport.FailOn]bool
	}{
		{"nothing", one(`"result": "allowed", "expected": true`), map[examplereport.FailOn]bool{"wrong": false, "error": false, "none": false}},
		{"no rules", `{"rules": []}`, map[examplereport.FailOn]bool{"wrong": false, "error": false, "none": false}},
		{"a wrong verdict", one(`"result": "refused", "expected": false, "wrong": "rule"`), map[examplereport.FailOn]bool{"wrong": true, "error": true, "none": false}},
		{"an error that refuses an example expected to be allowed", one(`"result": "error", "error": "x", "expected": false, "wrong": "rule"`), map[examplereport.FailOn]bool{"wrong": true, "error": true, "none": false}},
		{"an error that is expected", one(`"result": "error", "error": "x", "expected": true`), map[examplereport.FailOn]bool{"wrong": false, "error": true, "none": false}},
		{"not run", one(`"result": "not run", "reason": "x", "expected": true`), map[examplereport.FailOn]bool{"wrong": false, "error": false, "none": false}},
		{"a problem of an example", `{"rules": [], "problems": [{"uri": "file:///a.yaml", "line": 1, "column": 1, "severity": "error", "message": "x"}]}`, map[examplereport.FailOn]bool{"wrong": true, "error": true, "none": false}},
		{"a refused example nothing refuses, as results only", one(`"result": "allowed", "expected": false, "wrong": "example"`), map[examplereport.FailOn]bool{"wrong": false, "error": false, "none": false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := reportOf(t, tc.report)
			for on, want := range tc.want {
				if got := examplereport.Failed(r, on); got != want {
					t.Errorf("Failed(%s, %q) = %v, want %v", tc.name, on, got, want)
				}
			}
		})
	}
}

// TestWriteText checks the text: quiet but for the wrong verdicts, the
// errors and the problems, which come first, each marked, with the API
// server's message and what the example expected, the paths relative to the
// working folder, and a summary, which says how many files the limits left out; --verbose lists every result.
func TestWriteText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts examplereport.Options
		want string
	}{
		{
			name: "quiet",
			opts: examplereport.Options{Dir: "/w"},
			want: `config/samples/bad.yaml:4:8: error: spec.size: Invalid value: "string"
config/crd.yaml:22: self.size <= 10  (CustomResourceDefinition widgets.example.com v1 spec)
  wrong config/samples/huge.yaml:3: refused, expected to be allowed: spec: Invalid value: at most 10
config/crd.yaml:30: self.a / self.b > 0 && self.some.very.long.rule.that.goes.o…  (CustomResourceDefinition widgets.example.com v1 spec)
  wrong config/samples/div.yaml:1: error, expected to be allowed: spec: Invalid value: division by zero
  error config/samples/invalid/zero.yaml:1: error: spec: Invalid value: division by zero
5 rules, 6 examples: 3 wrong, 1 error, 1 not run · 1 file left out at the limits
`,
		},
		{
			name: "verbose",
			opts: examplereport.Options{Dir: "/w", Verbose: true},
			want: `config/samples/bad.yaml:4:8: error: spec.size: Invalid value: "string"
config/crd.yaml:22: self.size <= 10  (CustomResourceDefinition widgets.example.com v1 spec)
  wrong config/samples/huge.yaml:3: refused, expected to be allowed: spec: Invalid value: at most 10
  ok    config/samples/ok.yaml:1: allowed
  ok    config/samples/invalid/big.yaml:1: refused: spec: Invalid value: at most 10
config/crd.yaml:30: self.a / self.b > 0 && self.some.very.long.rule.that.goes.o…  (CustomResourceDefinition widgets.example.com v1 spec)
  wrong config/samples/div.yaml:1: error, expected to be allowed: spec: Invalid value: division by zero
  error config/samples/invalid/zero.yaml:1: error: spec: Invalid value: division by zero
config/crd.yaml:40: self.size > 0  (CustomResourceDefinition widgets.example.com v1 spec)
  wrong config/samples/invalid/nothing.yaml:2: allowed, expected to be refused
  wrong config/samples/invalid/nothing.yaml:2: allowed as an update under binding "b", expected to be refused
config/crd.yaml:24: self.size == oldSelf.size  (CustomResourceDefinition widgets.example.com v1 spec)
  skip  config/samples/ok.yaml:1: not run: the example is not an update and the rule needs oldSelf
config/crd.yaml:50: self.unused  (CustomResourceDefinition widgets.example.com v1 spec)
  no examples
5 rules, 6 examples: 3 wrong, 1 error, 1 not run · 1 file left out at the limits
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if err := examplereport.Write(&out, examplereport.Text, reportOf(t, every), tc.opts); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != tc.want {
				t.Errorf("Write(text, %+v) =\n%s\nwant\n%s", tc.opts, got, tc.want)
			}
		})
	}
}

// TestWriteTextPaths checks that with no folder to show them relative to, and
// for a path outside it, the paths are shown as they are.
func TestWriteTextPaths(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"", "/elsewhere"} {
		var out bytes.Buffer
		if err := examplereport.Write(&out, examplereport.Text, reportOf(t, every), examplereport.Options{Dir: dir}); err != nil {
			t.Fatal(err)
		}
		want := "/w/config/samples/bad.yaml:4:8: error: spec.size: Invalid value: \"string\"\n/w/config/crd.yaml:22: self.size <= 10  (CustomResourceDefinition widgets.example.com v1 spec)\n  wrong /w/config/samples/huge.yaml:3: "
		if !strings.HasPrefix(out.String(), want) {
			t.Errorf("Write(text, Dir %q) = %q, want it to start %q", dir, out.String(), want)
		}
	}
}

// TestSummary checks the last line: the singular, what is left out when there
// is none, and the files left out at the limits.
func TestSummary(t *testing.T) {
	t.Parallel()
	one := result("ok.yaml", 1, "allowed", `"result": "allowed", "expected": true`)
	for _, tc := range []struct{ left, want string }{
		{"", "1 rule, 1 example: 0 wrong\n"},
		{`, "filesLeftOut": 1`, "1 rule, 1 example: 0 wrong · 1 file left out at the limits\n"},
		{`, "filesLeftOut": 3`, "1 rule, 1 example: 0 wrong · 3 files left out at the limits\n"},
	} {
		var out bytes.Buffer
		if err := examplereport.Write(&out, examplereport.Text, reportOf(t, `{"rules": [`+rule("self.size <= 10", 3, one)+`]`+tc.left+`}`), examplereport.Options{}); err != nil {
			t.Fatal(err)
		}
		if out.String() != tc.want {
			t.Errorf("Write(text) with %q = %q, want %q", tc.left, out.String(), tc.want)
		}
	}
}

// TestWriteTextAllWell checks that a run with nothing wrong prints the
// summary alone, and that it counts one rule and one example in the singular.
func TestWriteTextAllWell(t *testing.T) {
	t.Parallel()
	r := reportOf(t, `{"rules": [`+rule("self.size <= 10", 3, result("ok.yaml", 1, "allowed", `"result": "allowed", "expected": true`))+`], "problems": []}`)
	var out bytes.Buffer
	if err := examplereport.Write(&out, examplereport.Text, r, examplereport.Options{}); err != nil {
		t.Fatal(err)
	}
	if want := "1 rule, 1 example: 0 wrong\n"; out.String() != want {
		t.Errorf("Write(text) = %q, want %q", out.String(), want)
	}
}

// TestWriteJSON checks that JSON is one object: the answer of
// cel-lens.exampleResults, and the problems of the examples beside its rules,
// whatever the options, with no notes.
func TestWriteJSON(t *testing.T) {
	t.Parallel()
	r := reportOf(t, every)
	r.Notes = []string{"a note"}
	var out bytes.Buffer
	if err := examplereport.Write(&out, examplereport.JSON, r, examplereport.Options{}); err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("Write(json) = %s: %v", out.Bytes(), err)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	if len(keys) != 4 || got["kubernetesVersion"] == nil || got["rules"] == nil || got["problems"] == nil || string(got["filesLeftOut"]) != "1" {
		t.Errorf("Write(json) has the keys %q, want kubernetesVersion, rules, problems and filesLeftOut: 1", keys)
	}
	var again lsp.ExamplesReport
	if err := json.Unmarshal(out.Bytes(), &again); err != nil || len(again.Rules) != 5 || len(again.Problems) != 1 || again.Rules[0].Results[1].Wrong != "rule" {
		t.Errorf("Write(json) = %s, want the report back (%v)", out.Bytes(), err)
	}
}

// TestParse checks the format and the fail-on value, and the error for others.
func TestParse(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"text", "json"} {
		if f, err := examplereport.ParseFormat(s); err != nil || string(f) != s {
			t.Errorf("ParseFormat(%q) = %q, %v, want it", s, f, err)
		}
	}
	if _, err := examplereport.ParseFormat("xml"); !errors.Is(err, examplereport.ErrFormat) {
		t.Errorf("ParseFormat(xml) error = %v, want %v", err, examplereport.ErrFormat)
	}
	for _, s := range []string{"wrong", "error", "none"} {
		if f, err := examplereport.ParseFailOn(s); err != nil || string(f) != s {
			t.Errorf("ParseFailOn(%q) = %q, %v, want it", s, f, err)
		}
	}
	if _, err := examplereport.ParseFailOn("warning"); !errors.Is(err, examplereport.ErrFailOn) {
		t.Errorf("ParseFailOn(warning) error = %v, want %v", err, examplereport.ErrFailOn)
	}
	if err := examplereport.Write(&bytes.Buffer{}, "xml", &lsp.ExamplesReport{}, examplereport.Options{}); !errors.Is(err, examplereport.ErrFormat) {
		t.Errorf("Write(xml) error = %v, want %v", err, examplereport.ErrFormat)
	}
}
