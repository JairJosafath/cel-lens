package check

import (
	"testing"

	"github.com/google/cel-go/cel"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

func TestReportParsesExpressionOnce(t *testing.T) {
	t.Parallel()
	env, err := cel.NewEnv(cel.OptionalTypes())
	if err != nil {
		t.Fatal(err)
	}
	e := extract.Expression{
		Text:  "a + b + c",
		Start: extract.Position{Line: 1, Column: 1},
		End:   extract.Position{Line: 1, Column: 10},
	}
	detail := `ERROR: <input>:1:1: undeclared reference to 'a'
ERROR: <input>:1:5: undeclared reference to 'b'
ERROR: <input>:1:9: undeclared reference to 'c'`
	r := &reporter{file: "f.yaml", lines: extract.SplitLines([]byte("a + b + c")), spans: spans{env: env}}
	r.report(e, detail)
	r.report(e, detail)
	if got := len(r.diags); got != 6 {
		t.Fatalf("report produced %d diagnostics, want 6", got)
	}
	if r.spans.parses != 1 {
		t.Errorf("report parsed the expression %d times, want 1", r.spans.parses)
	}
	if got, want := r.diags[1].Start.Column, 5; got != want {
		t.Errorf("second diagnostic starts at column %d, want %d", got, want)
	}
}

func TestSpansKeepsOneTree(t *testing.T) {
	t.Parallel()
	env, err := cel.NewEnv()
	if err != nil {
		t.Fatal(err)
	}
	s := spans{env: env}
	for _, text := range []string{"a", "a", "b", "a"} {
		if s.parse(text) == nil {
			t.Fatalf("parse(%q) = nil, want a tree", text)
		}
	}
	if s.parses != 3 {
		t.Errorf("parsed %d times for a, a, b, a, want 3: only the last tree is kept", s.parses)
	}
}
