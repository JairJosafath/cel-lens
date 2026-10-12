package assist_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// posIn returns the position of the first rune of offset-th byte in
// data, where needle must occur once.
func posIn(t *testing.T, data, needle string, offset int) extract.Position {
	t.Helper()
	if strings.Count(data, needle) != 1 {
		t.Fatalf("needle %q occurs %d times, want 1", needle, strings.Count(data, needle))
	}
	idx := strings.Index(data, needle)
	before := data[:idx] + needle[:offset]
	line := strings.Count(before, "\n") + 1
	col := len([]rune(before[strings.LastIndex(before, "\n")+1:])) + 1
	return extract.Position{Line: line, Column: col}
}

func load(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHover(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		file   string
		needle string // where the cursor is: offset bytes into needle
		offset int
		want   string // a substring of the hover; "" means no hover
	}{
		{name: "self", file: "crd.yaml", needle: "self.replicas >= 0", offset: 1, want: "(variable) self: object"},
		{name: "integer field", file: "crd.yaml", needle: "self.replicas >= 0", offset: 8, want: "(field) replicas: int\n```\n\nHow many copies to run."},
		{name: "string field", file: "crd.yaml", needle: "self.name.starts", offset: 6, want: "(field) name: string"},
		{name: "method", file: "crd.yaml", needle: "startsWith('w')", offset: 2, want: "string.startsWith(string) -> bool"},
		{name: "method description", file: "crd.yaml", needle: "startsWith('w')", offset: 2, want: "starts with a substring"},
		{name: "macro", file: "crd.yaml", needle: "self.ports.all(p", offset: 12, want: "range.all(x, predicate)"},
		{name: "field through list", file: "crd.yaml", needle: "self.ports.all(p", offset: 8, want: "(field) ports: list(object)"},
		{name: "second line of block scalar", file: "crd.yaml", needle: "ports.all(p, p.port > 0)", offset: 0, want: "(field) ports"},
		{name: "loop variable after a raw string ending in a backslash", file: "macros.yaml", needle: `r'\' != '' && p.size`, offset: 14, want: "(variable) p: string"},
		{name: "loop variable after a triple-quoted string", file: "macros.yaml", needle: `b''' != '' && p.size`, offset: 14, want: "(variable) p: string"},
		{name: "unknown name", file: "crd.yaml", needle: "self.ports.all(p, p.port", offset: 16, want: ""},
		{name: "operator", file: "crd.yaml", needle: "self.replicas >= 0", offset: 15, want: ""},
		{name: "number", file: "crd.yaml", needle: ">= 0 &&", offset: 3, want: ""},
		{name: "inside string literal", file: "crd.yaml", needle: "startsWith('w')", offset: 13, want: ""},
		{name: "outside CEL: description", file: "crd.yaml", needle: "The desired state.", offset: 5, want: ""},
		{name: "outside CEL: key", file: "crd.yaml", needle: "replicas:\n", offset: 2, want: ""},
		{name: "outside CEL: message", file: "crd.yaml", needle: "message: bad", offset: 10, want: ""},
		{name: "policy variable", file: "policy.yaml", needle: "request.operation !=", offset: 3, want: "(variable) request: kubernetes.AdmissionRequest"},
		{name: "policy field", file: "policy.yaml", needle: "request.operation !=", offset: 10, want: "(field) operation: string"},
		{name: "policy nested field", file: "policy.yaml", needle: "userInfo.username", offset: 11, want: "(field) username: string"},
		{name: "policy variables", file: "policy.yaml", needle: "variables.replicas <=", offset: 3, want: "(variable) variables: kubernetes.variables"},
		{name: "policy declared variable", file: "policy.yaml", needle: "variables.replicas <=", offset: 12, want: "(field) replicas: dyn"},
		{name: "policy object", file: "policy.yaml", needle: "object.spec.replicas", offset: 2, want: "(variable) object: dyn"},
		{name: "matchConditions variable", file: "policy.yaml", needle: "expression: request.operation", offset: 15, want: "(variable) request"},
		{name: "policy outside CEL", file: "policy.yaml", needle: "failurePolicy", offset: 3, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := load(t, tt.file)
			pos := posIn(t, data, tt.needle, tt.offset)
			got, ok := a.Hover(assist.Parse(context.Background(), tt.file, []byte(data)), pos)
			if tt.want == "" {
				if ok {
					t.Errorf("Hover(%s %v) = %q, want none", tt.file, pos, got.Markdown)
				}
				return
			}
			if !ok || !strings.Contains(got.Markdown, tt.want) {
				t.Errorf("Hover(%s %v) = %q, %v, want it to contain %q", tt.file, pos, got.Markdown, ok, tt.want)
			}
		})
	}
}

func TestHoverRange(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	data := load(t, "crd.yaml")
	pos := posIn(t, data, "self.replicas >= 0", 8)
	got, ok := a.Hover(assist.Parse(context.Background(), "crd.yaml", []byte(data)), pos)
	start := posIn(t, data, "replicas >= 0", 0)
	end := posIn(t, data, "replicas >= 0", len("replicas"))
	if !ok || got.Start != start || got.End != end {
		t.Errorf("Hover(%v) range = %v-%v, %v, want %v-%v", pos, got.Start, got.End, ok, start, end)
	}
}

func TestComplete(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		file    string
		needle  string
		offset  int
		want    []string // labels that must be offered
		notWant []string // labels that must not be
		none    bool     // no completion at all
	}{
		{name: "variables and functions", file: "crd.yaml", needle: "self.replicas >= 0", offset: 0, want: []string{"self", "oldSelf", "size", "has", "int", "quantity"}, notWant: []string{"replicas", "startsWith"}},
		{name: "fields and methods of an object", file: "crd.yaml", needle: "self.replicas >= 0", offset: 5, want: []string{"replicas", "name", "ports"}, notWant: []string{"startsWith", "all"}},
		{name: "methods of a string", file: "crd.yaml", needle: "self.name.starts", offset: 10, want: []string{"startsWith", "lowerAscii", "size"}, notWant: []string{"replicas", "all", "isSorted"}},
		{name: "methods of a list", file: "crd.yaml", needle: "self.ports.all(p", offset: 11, want: []string{"all", "exists", "filter", "size", "isSorted"}, notWant: []string{"startsWith", "port"}},
		{name: "policy variables", file: "policy.yaml", needle: "request.userInfo.username", offset: 0, want: []string{"object", "oldObject", "request", "namespaceObject", "authorizer", "variables"}, notWant: []string{"params", "self"}},
		{name: "policy request fields", file: "policy.yaml", needle: "request.userInfo.username", offset: 8, want: []string{"operation", "userInfo", "namespace", "kind"}},
		{name: "policy nested fields", file: "policy.yaml", needle: "userInfo.username", offset: 9, want: []string{"username", "groups"}},
		{name: "declared policy variables", file: "policy.yaml", needle: "variables.replicas <=", offset: 10, want: []string{"replicas"}},
		{name: "no variables in matchConditions", file: "policy.yaml", needle: "request.operation !=", offset: 0, want: []string{"request"}, notWant: []string{"variables"}},
		{name: "after a group receiver", file: "groups.yaml", needle: "(self).replicas", offset: len("(self)."), want: []string{"replicas"}, notWant: []string{"all"}},
		{name: "after a group receiver in an expression", file: "groups.yaml", needle: "'a' in (self).name", offset: len("'a' in (self)."), want: []string{"replicas"}, notWant: []string{"all"}},
		{name: "outside CEL: key", file: "crd.yaml", needle: "message: bad", offset: 3, none: true},
		{name: "outside CEL: description", file: "crd.yaml", needle: "The desired state.", offset: 5, none: true},
		{name: "inside string literal", file: "crd.yaml", needle: "startsWith('w')", offset: 13, none: true},
		{name: "outside CEL: policy", file: "policy.yaml", needle: "failurePolicy", offset: 3, none: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := load(t, tt.file)
			pos := posIn(t, data, tt.needle, tt.offset)
			items, ok := a.Complete(assist.Parse(context.Background(), tt.file, []byte(data)), pos)
			if tt.none {
				if ok || len(items) > 0 {
					t.Errorf("Complete(%s %v) = %d items, want none", tt.file, pos, len(items))
				}
				return
			}
			if !ok {
				t.Fatalf("Complete(%s %v) = none, want items", tt.file, pos)
			}
			have := map[string]bool{}
			for _, it := range items {
				have[it.Label] = true
			}
			for _, w := range tt.want {
				if !have[w] {
					t.Errorf("Complete(%s %v) lacks %q", tt.file, pos, w)
				}
			}
			for _, w := range tt.notWant {
				if have[w] {
					t.Errorf("Complete(%s %v) offers %q, want it absent", tt.file, pos, w)
				}
			}
		})
	}
}

// isInternal reports whether name is one a developer cannot type: an operator
// overload such as `_+_` or `!_`, an `@` operator, or a reserved word.
func isInternal(name string) bool {
	return strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_") || strings.HasPrefix(name, "@") ||
		slices.Contains(strings.Fields("as break const continue else for function if import let loop package namespace return var void while in"), name)
}

func TestCompleteNoInternalNames(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	data := load(t, "crd.yaml")
	tests := []struct {
		name   string
		needle string // where the cursor is: offset bytes into needle
		offset int
		want   string // a name that must still be offered
	}{
		{name: "empty position", needle: "self.replicas >= 0", offset: 0, want: "has"},
		{name: "after self.", needle: "self.replicas >= 0", offset: 5, want: "replicas"},
		{name: "after self.name.", needle: "self.name.starts", offset: len("self.name."), want: "startsWith"},
		{name: "after self.ports.", needle: "self.ports.all(p", offset: len("self.ports."), want: "all"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pos := posIn(t, data, tt.needle, tt.offset)
			items, ok := a.Complete(assist.Parse(context.Background(), "crd.yaml", []byte(data)), pos)
			if !ok {
				t.Fatalf("Complete(%v) = none, want items", pos)
			}
			found := false
			for _, it := range items {
				l := it.Label
				if isInternal(l) {
					t.Errorf("Complete(%v) offers internal name %q", pos, l)
				}
				found = found || l == tt.want
			}
			if !found {
				t.Errorf("Complete(%v) lacks %q", pos, tt.want)
			}
		})
	}
}

func TestCompleteNamespaced(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	data := strings.Replace(load(t, "crd.yaml"), "self.replicas >= 0", "sets.contains([1], [1]) && self.replicas >= 0", 1)
	pos := posIn(t, data, "sets.contains", len("sets."))
	items, ok := a.Complete(assist.Parse(context.Background(), "crd.yaml", []byte(data)), pos)
	if !ok || len(items) == 0 {
		t.Fatalf("Complete(%v) = %v, %v, want sets functions", pos, items, ok)
	}
	for _, it := range items {
		if !strings.HasSuffix(it.Detail, "bool") || it.Kind != assist.KindFunction {
			t.Errorf("Complete(%v) item %+v, want sets functions returning bool", pos, it)
		}
		if isInternal(it.Label) {
			t.Errorf("Complete(%v) offers internal name %q", pos, it.Label)
		}
	}
}

func TestCancelled(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := load(t, "crd.yaml")
	pos := posIn(t, data, "self.replicas >= 0", 1)
	if h, ok := a.Hover(assist.Parse(ctx, "crd.yaml", []byte(data)), pos); ok {
		t.Errorf("Hover with a cancelled context = %q, want none", h.Markdown)
	}
	if _, ok := a.Complete(assist.Parse(ctx, "crd.yaml", []byte(data)), pos); ok {
		t.Error("Complete with a cancelled context found items, want none")
	}
}

// FuzzAssist checks that no file and no position makes Hover or Complete panic.
func FuzzAssist(f *testing.F) {
	for _, name := range []string{"crd.yaml", "policy.yaml"} {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b), 30, 20)
	}
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(_ *testing.T, data string, line, col int) {
		pos := extract.Position{Line: line, Column: col}
		a.Hover(assist.Parse(context.Background(), "f.yaml", []byte(data)), pos)
		a.Complete(assist.Parse(context.Background(), "f.yaml", []byte(data)), pos)
	})
}

func TestSuggest(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	data := load(t, "typos.yaml")
	tests := []struct {
		name   string
		needle string // the misspelled name, which must occur once
		want   string // the suggestion; "" means none
	}{
		{"field one letter short", "maxReplica ", "maxReplicas"},
		{"method", "startWith", "startsWith"},
		{"global function", "sizee", "size"},
		{"two fields as near", "replicaC", ""},
		{"nothing near a field", "zzzzzz >= 0", ""},
		{"nothing near a method", "zzzzzz()", ""},
		{"a name that exists", "maxReplicas > 0", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			word, _, _ := strings.Cut(tt.needle, " ")
			word, _, _ = strings.Cut(word, "(")
			start := posIn(t, data, tt.needle, 0)
			end := extract.Position{Line: start.Line, Column: start.Column + len(word)}
			got, ok := a.Suggest(assist.Parse(context.Background(), "typos.yaml", []byte(data)), start, end)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("Suggest(%q) = %q, %v, want %q", word, got, ok, tt.want)
			}
		})
	}
}

func TestCompleteMacros(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		needle  string
		offset  int
		want    []string
		notWant []string
	}{
		{name: "list", needle: "self.ports.size", offset: 11, want: []string{"all", "map", "sortBy"}, notWant: []string{"optMap", "optFlatMap"}},
		{name: "map", needle: "self.labels.size", offset: 12, want: []string{"all", "map"}, notWant: []string{"sortBy", "optMap", "optFlatMap"}},
		{name: "optional field", needle: "self.?name.hasValue", offset: 12, want: []string{"optMap", "optFlatMap", "hasValue"}, notWant: []string{"all", "map", "sortBy", "startsWith", "isLoopback", "containsCIDR"}},
		{name: "optional of index", needle: "self.?ports[0].size", offset: 16, want: []string{"optMap", "hasValue", "orValue"}, notWant: []string{"startsWith", "isLoopback"}},
		{name: "optional index", needle: "self.labels[?'a'].hasValue", offset: 18, want: []string{"optMap", "optFlatMap"}, notWant: []string{"all", "sortBy"}},
		{name: "optional call", needle: "optional.of(self.name).hasValue", offset: 23, want: []string{"optMap", "optFlatMap"}, notWant: []string{"all", "sortBy"}},
		{name: "parenthesised optional field", needle: "(self.?name).hasValue", offset: 14, want: []string{"optMap", "hasValue"}, notWant: []string{"startsWith"}},
		{name: "optional call with paren in string", needle: "optional.of(')').hasValue", offset: 18, want: []string{"optMap"}, notWant: []string{"startsWith", "all"}},
		{name: "parenthesised with spaces", needle: "( self.?name ).hasValue", offset: 16, want: []string{"optMap", "hasValue"}, notWant: []string{"startsWith"}},
		{name: "index with bracket in string", needle: "self.labels[']'].size", offset: 17, want: []string{"startsWith"}, notWant: []string{"all", "sortBy"}},
		{name: "after a stray closer", needle: "a) + ( self.?name).hasValue", offset: 19, want: []string{"optMap", "hasValue"}, notWant: []string{"startsWith"}},
		{name: "index with escaped quote in string", needle: `self.labels['\']'].size`, offset: 19, want: []string{"startsWith"}, notWant: []string{"all", "sortBy"}},
	}
	data := load(t, "macros.yaml")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pos := posIn(t, data, tt.needle, tt.offset)
			items, ok := a.Complete(assist.Parse(context.Background(), "macros.yaml", []byte(data)), pos)
			if !ok {
				t.Fatalf("Complete(%v) = none, want items", pos)
			}
			have := map[string]bool{}
			for _, it := range items {
				have[it.Label] = true
			}
			for _, w := range tt.want {
				if !have[w] {
					t.Errorf("Complete(%v) lacks %q", pos, w)
				}
			}
			for _, w := range tt.notWant {
				if have[w] {
					t.Errorf("Complete(%v) offers %q, want it absent", pos, w)
				}
			}
		})
	}
}

// TestBeforeBadDocument checks that a CRD followed by a document with a
// control character gets the same answers as the CRD alone.
func TestBeforeBadDocument(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	clean := load(t, "typos.yaml")
	broken := clean + "\n---\nkey: \x01\n"
	ctx := context.Background()
	cleanDoc := assist.Parse(ctx, "typos.yaml", []byte(clean))
	brokenDoc := assist.Parse(ctx, "typos.yaml", []byte(broken))

	start := posIn(t, clean, "maxReplica ", 0)
	end := extract.Position{Line: start.Line, Column: start.Column + len("maxReplica")}
	t.Run("suggest", func(t *testing.T) {
		t.Parallel()
		want, wantOK := a.Suggest(cleanDoc, start, end)
		got, ok := a.Suggest(brokenDoc, start, end)
		if want != "maxReplicas" || got != want || ok != wantOK {
			t.Errorf("Suggest(maxReplica) = %q, %v, want %q, %v", got, ok, want, wantOK)
		}
	})
	t.Run("hover", func(t *testing.T) {
		t.Parallel()
		pos := posIn(t, clean, "maxReplicas > 0", 6)
		want, wantOK := a.Hover(cleanDoc, pos)
		got, ok := a.Hover(brokenDoc, pos)
		if !wantOK || !ok || got != want {
			t.Errorf("Hover(maxReplicas) = %v, %v, want %v, %v", got, ok, want, wantOK)
		}
	})
	t.Run("complete", func(t *testing.T) {
		t.Parallel()
		pos := posIn(t, clean, "maxReplicas > 0", 5)
		want, wantOK := a.Complete(cleanDoc, pos)
		got, ok := a.Complete(brokenDoc, pos)
		if !wantOK || !ok || !slices.Equal(got, want) {
			t.Errorf("Complete(self.) = %d items, %v, want %d items, %v", len(got), ok, len(want), wantOK)
		}
		if !slices.ContainsFunc(got, func(i assist.Item) bool { return i.Label == "maxReplicas" }) {
			t.Error("Complete(self.) lacks the field maxReplicas")
		}
	})
}

// TestCompleteDeeplyNested checks that completion after a receiver wrapped in
// thousands of parentheses answers without recursing through every level.
func TestCompleteDeeplyNested(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		t.Fatal(err)
	}
	const depth = 10000
	rule := strings.Repeat("(", depth) + "self.name" + strings.Repeat(")", depth) + ".x()"
	data := strings.Replace(load(t, "macros.yaml"), "self.ports.size() > 0", rule, 1)
	pos := posIn(t, data, rule, depth+len("self.name")+depth+1)
	if _, ok := a.Complete(assist.Parse(context.Background(), "macros.yaml", []byte(data)), pos); !ok {
		t.Errorf("Complete(%v) = none, want items", pos)
	}
}
