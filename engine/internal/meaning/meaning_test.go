package meaning_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/parser"

	"github.com/JairJosafath/cel-lens/engine/internal/meaning"
)

func translator(tb testing.TB) *meaning.Parser {
	tb.Helper()
	p, err := meaning.NewParser()
	if err != nil {
		tb.Fatal(err)
	}
	return p
}

// say parses text with p and returns its sentence; false when text does not
// parse or has none.
func say(p *meaning.Parser, text string, kind meaning.Kind) (string, bool) {
	e, ok := p.Parse(text)
	if !ok {
		return "", false
	}
	return meaning.Sentence(e, kind)
}

// TestSentence holds each row of the plan's table to its exact sentence
// (#53).
func TestSentence(t *testing.T) {
	t.Parallel()
	tr := translator(t)
	tests := []struct {
		name, text string
		kind       meaning.Kind
		want       string
	}{
		{"&& chain", "self.a < 1 && self.b > 2 && self.c == 3", meaning.Check, "`self.a` is less than `1`, and `self.b` is greater than `2`, and `self.c` equals `3`."},
		{"|| chain", "self.a < 1 || self.b > 2", meaning.Check, "`self.a` is less than `1`, or `self.b` is greater than `2`."},
		{"!has(x) || B", "!has(self.port) || self.port > 0", meaning.Check, "when `self.port` is set, `self.port` is greater than `0`."},
		{"!has(x) || B || C", "!has(self.port) || self.port > 0 || self.any", meaning.Check, "when `self.port` is set, `self.port` is greater than `0`, or `self.any` is true."},
		{"ternary", "self.tls ? has(self.cert) : !has(self.cert)", meaning.Check, "if `self.tls` is true, then `self.cert` is set; otherwise `self.cert` is not set."},
		{"<", "self.a < self.b", meaning.Check, "`self.a` is less than `self.b`."},
		{"<=", "self.replicas <= 10", meaning.Check, "`self.replicas` is at most `10`."},
		{">", "self.port > 0", meaning.Check, "`self.port` is greater than `0`."},
		{">=", "self.port >= 1", meaning.Check, "`self.port` is at least `1`."},
		{"==", "self.mode == 'on'", meaning.Check, "`self.mode` equals `'on'`."},
		{"!=", "self.name != ''", meaning.Check, "`self.name` does not equal `''`."},
		{"size(x) compared", "size(self.name) <= 63", meaning.Check, "the size of `self.name` is at most `63`."},
		{"x.size() compared", "self.items.size() < 10", meaning.Check, "the size of `self.items` is less than `10`."},
		{"has", "has(self.spec.port)", meaning.Check, "`self.spec.port` is set."},
		{"!has", "!has(self.spec.port)", meaning.Check, "`self.spec.port` is not set."},
		{"in a list literal", "self.mode in ['on', 'off']", meaning.Check, "`self.mode` is one of `'on'`, `'off'`."},
		{"in a value", "self.mode in self.allowed", meaning.Check, "`self.mode` is in `self.allowed`."},
		{"all", "self.ports.all(p, p.port < 65536)", meaning.Check, "for every `p` in `self.ports`, `p.port` is less than `65536`."},
		{"exists", "self.ports.exists(p, p.name == 'http')", meaning.Check, "for at least one `p` in `self.ports`, `p.name` equals `'http'`."},
		{"exists_one", "self.ports.exists_one(p, p.primary)", meaning.Check, "for exactly one `p` in `self.ports`, `p.primary` is true."},
		{"existsOne", "self.ports.existsOne(p, p.primary)", meaning.Check, "for exactly one `p` in `self.ports`, `p.primary` is true."},
		{"all, two variables", "self.labels.all(k, v, v.size() < 64)", meaning.Check, "for every `k`, `v` in `self.labels`, the size of `v` is less than `64`."},
		{"exists, two variables", "self.list.exists(i, v, i > 0 && v == 'x')", meaning.Check, "for at least one `i`, `v` in `self.list`, `i` is greater than `0`, and `v` equals `'x'`."},
		{"existsOne, two variables", "self.labels.existsOne(k, v, k == 'app')", meaning.Check, "for exactly one `k`, `v` in `self.labels`, `k` equals `'app'`."},
		{"nested comprehensions", "self.groups.all(g, g.ports.exists(p, p > 0))", meaning.Check, "for every `g` in `self.groups`, for at least one `p` in `g.ports`, `p` is greater than `0`."},
		{"startsWith", "self.image.startsWith('registry.example.com/')", meaning.Check, "`self.image` starts with `'registry.example.com/'`."},
		{"!startsWith", "!self.image.startsWith('docker.io/')", meaning.Check, "`self.image` does not start with `'docker.io/'`."},
		{"endsWith", "self.host.endsWith('.example.com')", meaning.Check, "`self.host` ends with `'.example.com'`."},
		{"!endsWith", "!self.host.endsWith('.local')", meaning.Check, "`self.host` does not end with `'.local'`."},
		{"contains", "self.name.contains('-')", meaning.Check, "`self.name` contains `'-'`."},
		{"!contains", "!self.name.contains('..')", meaning.Check, "`self.name` does not contain `'..'`."},
		{"matches", "self.name.matches('^[a-z]+$')", meaning.Check, "`self.name` matches the regular expression `'^[a-z]+$'`."},
		{"!matches", "!self.name.matches('^kube-')", meaning.Check, "`self.name` does not match the regular expression `'^kube-'`."},
		{"isQuantity", "isQuantity(self.memory)", meaning.Check, "`self.memory` is a quantity."},
		{"isURL", "isURL(self.endpoint)", meaning.Check, "`self.endpoint` is a URL."},
		{"isIP", "isIP(self.address)", meaning.Check, "`self.address` is an IP address."},
		{"isCIDR", "isCIDR(self.range)", meaning.Check, "`self.range` is a CIDR."},
		{"isSemver", "isSemver(self.version)", meaning.Check, "`self.version` is a semantic version."},
		{"a field read as the result", "self.enabled", meaning.Check, "`self.enabled` is true."},
		{"a scope goes last", "self.ports.all(p, p > 0) && self.name != ''", meaning.Check, "`self.name` does not equal `''`, and for every `p` in `self.ports`, `p` is greater than `0`."},
		{"a match condition", "request.namespace != 'kube-system'", meaning.MatchCondition, "the policy applies only when `request.namespace` does not equal `'kube-system'`."},
		{"a match condition's !has(x) || B", "!has(object.spec.x) || object.spec.x", meaning.MatchCondition, "the policy applies only when `object.spec.x` is not set, or `object.spec.x` is true."},
		{"four clauses", "self.a > 1 && self.b > 2 && self.c > 3 && self.d > 4", meaning.Check, "`self.a` is greater than `1`, and `self.b` is greater than `2`, and `self.c` is greater than `3`, and `self.d` is greater than `4`."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := say(tr, tt.text, tt.kind)
			if !ok || got != tt.want {
				t.Errorf("Sentence(%q) = %q, %t, want %q", tt.text, got, ok, tt.want)
			}
		})
	}
}

// TestNoSentence gives no sentence for each case the plan names, and for
// the neighbours of each row that the table does not cover (#53).
func TestNoSentence(t *testing.T) {
	t.Parallel()
	tr := translator(t)
	long := "self.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa == 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' && " +
		"self.cccccccccccccccccccccccccccccccccccccccccccc == 'dddddddddddddddddddddddddddddddddddddddddddddddddddd'"
	tests := []struct {
		name, text string
		kind       meaning.Kind
	}{
		{"! of a comparison", "!(self.a < self.b)", meaning.Check},
		{"! of a field read", "!self.enabled", meaning.Check},
		{"! of a format function", "!isURL(self.endpoint)", meaning.Check},
		{"! of a comprehension", "!self.ports.all(p, p > 0)", meaning.Check},
		{"a call not in the table", "self.list.isSorted()", meaning.Check},
		{"a global call not in the table", "isSorted(self.list)", meaning.Check},
		{"a member string function with two arguments", "self.name.startsWith('a', 'b')", meaning.Check},
		{"true", "true", meaning.Check},
		{"false in a chain", "self.a > 0 && false", meaning.Check},
		{"an unknown macro", "self.ports.map(p, p.port).size() > 0 && self.ports.filter(p, p > 1)", meaning.Check},
		{"a macro not in the table as the result", "self.ports.filter(p, p > 1)", meaning.Check},
		{"a comprehension over a name that is no name", "self.ports.all(p.x, p > 0)", meaning.Check},
		{"a comprehension whose body matches no row", "self.ports.all(p, p.isSorted())", meaning.Check},
		{"&& and || mixed", "self.a > 0 && (self.b > 0 || self.c > 0)", meaning.Check},
		{"|| and && mixed", "self.a > 0 || self.b > 0 && self.c > 0", meaning.Check},
		{"!has(x) || an && chain", "!has(self.a) || self.a > 0 && self.a < 9", meaning.Check},
		{"two scopes in one chain", "self.a.all(x, x > 0) && self.b.all(y, y > 0)", meaning.Check},
		{"a ternary that is no check", "self.tls ? 1 : 2", meaning.Check},
		{"a match condition's ternary", "self.tls ? has(self.cert) : true", meaning.MatchCondition},
		{"more than four clauses", "self.a > 1 && self.b > 2 && self.c > 3 && self.d > 4 && self.e > 5", meaning.Check},
		{"more than four clauses across a scope", "self.a > 1 && self.b > 2 && self.l.all(x, x > 3 && x < 4 && x != 5)", meaning.Check},
		{"more than 200 characters", long, meaning.Check},
		{"text that does not parse", "self.a >", meaning.Check},
		{"no text", "", meaning.Check},
		{"an empty list", "self.mode in []", meaning.Check},
		{"an index read as the result", "self.flags[0]", meaning.Check},
		{"a name as the result", "self", meaning.Check},
		{"a value", "self.a + 1", meaning.Check},
		{"a comment inside a value", "self.a == [1, // one\n2]", meaning.Check},
		{"a line break inside a quoted string", "self.a == '''x\ny'''", meaning.Check},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := say(tr, tt.text, tt.kind); ok {
				t.Errorf("Sentence(%q) = %q, want none", tt.text, got)
			}
		})
	}
	if _, ok := say(tr, long[:len(long)/2-4], meaning.Check); !ok {
		t.Error("Sentence(half of the long one) = none, want a sentence: the limit is the length")
	}
}

// TestQuoting checks that a value is quoted from the source, on one line:
// line breaks and indentation outside strings are one space, a string keeps
// its own, and a value holding a backtick gets a longer delimiter (#53).
func TestQuoting(t *testing.T) {
	t.Parallel()
	tr := translator(t)
	tests := []struct{ name, text, want string }{
		{"a line break and indentation", "self.spec\n    .replicas <=\n    10", "`self.spec .replicas` is at most `10`."},
		{"two spaces in a string", "self.name == 'a  b'", "`self.name` equals `'a  b'`."},
		{"tabs and runs of spaces outside", "self.a    ==\t\t[1,   2]", "`self.a` equals `[1, 2]`."},
		{"a backtick in a string", "self.quote == '```'", "`self.quote` equals ````'```'````."},
		{"an escaped field name", "self.`my-port` > 0", "`` self.`my-port` `` is greater than `0`."},
		{"a call", "size(self.name) == size( self.other )", "the size of `self.name` equals `size( self.other )`."},
		{"a call in parentheses", "(self.a + self.b).size() > 2", "the size of `self.a + self.b` is greater than `2`."},
		{"an index and a message", "self.list[0].name == Object{spec: 1}.spec", "`self.list[0].name` equals `Object{spec: 1}.spec`."},
		{"a map", "self.m == {'a': 1, 'b': [2]}", "`self.m` equals `{'a': 1, 'b': [2]}`."},
		{"an optional field", "self.?a.orValue(1) > 0", "`self.?a.orValue(1)` is greater than `0`."},
		{"a raw string", `self.re == r'\d+'`, "`self.re` equals `r'\\d+'`."},
		{"an escaped quote", `self.s == "a\"b"`, "`self.s` equals `\"a\\\"b\"`."},
		{"characters of several bytes", "self.name == 'héllo' && self.n > 1", "`self.name` equals `'héllo'`, and `self.n` is greater than `1`."},
		{"a ternary value", "self.a == (self.b ? 1 : 2)", "`self.a` equals `self.b ? 1 : 2`."},
		{"a name from the root, with its dot", ".self.a > 0", "`.self.a` is greater than `0`."},
		{"a function from the root, with its dot", ".f() > 0", "`.f()` is greater than `0`."},
		// Review comment 683 on #61: a token's end is its start in
		// characters plus its length in bytes.
		{"a list of non-ASCII strings (comment 683)", "self.x in ['é', 'b']", "`self.x` is one of `'é'`, `'b'`."},
		{"a longer non-ASCII string in a list (comment 683)", "self.x in ['éé', 'b'] && self.z", "`self.x` is one of `'éé'`, `'b'`, and `self.z` is true."},
		{"endsWith a non-ASCII string (comment 683)", "self.x.endsWith('é')", "`self.x` ends with `'é'`."},
		{"the size of a non-ASCII string (comment 683)", "size('é') == self.n", "the size of `'é'` equals `self.n`."},
		{"startsWith a non-ASCII string, or (comment 683)", "self.a.startsWith('ü') || self.c == 12345", "`self.a` starts with `'ü'`, or `self.c` equals `12345`."},
		{"a longer non-ASCII literal (comment 683)", "self.a == '日本語テキスト' && self.bbbbbb > 100", "`self.a` equals `'日本語テキスト'`, and `self.bbbbbb` is greater than `100`."},
		{"a comprehension over a non-ASCII string (comment 683)", "self.list.all(x, x != 'ñ') && self.size() <= 3", "the size of `self` is at most `3`, and for every `x` in `self.list`, `x` does not equal `'ñ'`."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := say(tr, tt.text, meaning.Check)
			if !ok || got != tt.want {
				t.Errorf("Sentence(%q) = %q, %t, want %q", tt.text, got, ok, tt.want)
			}
		})
	}
}

// FuzzSentence checks that Sentence never panics, and that every value a
// sentence quotes is part of the expression, with its white space outside
// strings made one space (#53).
func FuzzSentence(f *testing.F) {
	for _, seed := range []string{
		"self.size() >= 0 && self.a1.size() < 10 && self.b1.all(x, x > 0)",
		"!has(self.port) || self.port > 0",
		"self.tls ? has(self.cert) : !has(self.cert)",
		"self.mode in ['on', 'a  b']",
		"self.groups.all(g, g.ports.exists(p, p > 0))",
		"self.spec\n  .replicas <= 10",
		"self.quote == '```'",
		"self.`my-port` > 0",
		"(self.a + self.b).size() > 2",
		"self.m == {'a': 1}",
		"self.s == \"a\\\"b\" && self.r == r'\\d'",
		"self.x == '''a\nb'''",
		"self.a == // c\n 1",
		"self.list[0].name == Object{spec: 1}.spec",
		"self.x in ['é', 'b'] && size('日本語') == self.n && self.y.endsWith('ü')",
		"",
		"(((",
	} {
		f.Add(seed)
	}
	tr := translator(f)
	f.Fuzz(func(t *testing.T, text string) {
		for _, kind := range []meaning.Kind{meaning.Check, meaning.MatchCondition} {
			got, ok := say(tr, text, kind)
			if !ok {
				continue
			}
			flat := flatten(text)
			nodes := nodeSources(t, tr, text)
			for _, v := range codeSpans(got) {
				if !strings.Contains(flat, v) {
					t.Errorf("Sentence(%q) = %q quotes %q, which is not in %q", text, got, v, flat)
				}
				// Each value is the source of one node of the tree, which
				// a substring holding a token too many or too few is not
				// (review comment 683).
				if src, ok := unparsed(tr, v); !ok || !nodes[src] {
					t.Errorf("Sentence(%q) = %q quotes %q, which is the source of no node of it", text, got, v)
				}
			}
		}
	})
}

// nodeSources returns the source of every node of text, parsed by p, as
// cel-go's unparser writes it.
func nodeSources(t *testing.T, p *meaning.Parser, text string) map[string]bool {
	t.Helper()
	e, ok := p.Parse(text)
	if !ok {
		t.Fatalf("Parse(%q) = none, but it has a sentence", text)
	}
	out := map[string]bool{}
	var walk func(x ast.Expr)
	walk = func(x ast.Expr) {
		if src, err := parser.Unparse(x, e.AST.SourceInfo()); err == nil {
			out[src] = true
		}
		switch x.Kind() {
		case ast.CallKind:
			c := x.AsCall()
			if c.IsMemberFunction() {
				walk(c.Target())
			}
			for _, a := range c.Args() {
				walk(a)
			}
		case ast.SelectKind:
			walk(x.AsSelect().Operand())
		case ast.ListKind:
			for _, el := range x.AsList().Elements() {
				walk(el)
			}
		case ast.MapKind:
			for _, en := range x.AsMap().Entries() {
				walk(en.AsMapEntry().Key())
				walk(en.AsMapEntry().Value())
			}
		case ast.StructKind:
			for _, f := range x.AsStruct().Fields() {
				walk(f.AsStructField().Value())
			}
		case ast.ComprehensionKind, ast.IdentKind, ast.LiteralKind, ast.UnspecifiedExprKind:
		}
	}
	walk(e.AST.Expr())
	return out
}

// unparsed returns v, a value a sentence quotes, parsed by p alone and
// written by cel-go's unparser; false when v does not parse alone.
func unparsed(p *meaning.Parser, v string) (string, bool) {
	e, ok := p.Parse(v)
	if !ok {
		return "", false
	}
	src, err := parser.Unparse(e.AST.Expr(), e.AST.SourceInfo())
	return src, err == nil
}

// flatten is text with each run of white space outside its strings made
// one space, written again here so the test does not trust the code it
// tests: strings are '…' and "…", their triple forms, with \ escaping the
// next character unless r or R comes first.
func flatten(text string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\'' || c == '"' {
			raw := i > 0 && (text[i-1] == 'r' || text[i-1] == 'R' || i > 1 && (text[i-2] == 'r' || text[i-2] == 'R') && (text[i-1] == 'b' || text[i-1] == 'B'))
			q := string(c)
			if strings.HasPrefix(text[i:], q+q+q) {
				q += q + q
			}
			j := i + len(q)
			for j < len(text) && !strings.HasPrefix(text[j:], q) {
				if text[j] == '\\' && !raw {
					j++
				}
				j++
			}
			end := min(j+len(q), len(text))
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteString(text[i:end])
			i = end - 1
			continue
		}
		if strings.IndexByte(" \t\n\r\f\v", c) >= 0 {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteByte(c)
	}
	return b.String()
}

// codeSpans returns the contents of the code spans of md, as Markdown
// reads them: a run of backticks to the next run of the same length, one
// space stripped from each end when both have one.
func codeSpans(md string) []string {
	var out []string
	for i := 0; i < len(md); {
		if md[i] != '`' {
			i++
			continue
		}
		n := 0
		for i+n < len(md) && md[i+n] == '`' {
			n++
		}
		rest := md[i+n:]
		end := -1
		for k := 0; k < len(rest); {
			if rest[k] != '`' {
				k++
				continue
			}
			m := 0
			for k+m < len(rest) && rest[k+m] == '`' {
				m++
			}
			if m == n {
				end = k
				break
			}
			k += m
		}
		if end < 0 {
			i += n
			continue
		}
		s := rest[:end]
		if len(s) > 1 && s[0] == ' ' && s[len(s)-1] == ' ' && strings.Trim(s, " ") != "" {
			s = s[1 : len(s)-1]
		}
		out = append(out, s)
		i += n + end + n
	}
	return out
}

// benchRules are the rules of BenchmarkHover's CRD (internal/lsp): the 34
// rules of a 1,000-line CRD, one in ten with a misspelled field.
func benchRules() []string {
	var rules []string
	for i := range 34 {
		rule := fmt.Sprintf("self.size() >= 0 && self.a%d.size() < 10 && self.b%d.all(x, x > 0)", i, i)
		if i%10 == 7 {
			rule = fmt.Sprintf("self.a%d.nope > 0", i)
		}
		rules = append(rules, rule)
	}
	return rules
}

// BenchmarkSentence makes the sentence of each rule of BenchmarkHover's CRD
// from the rule already parsed, as the hover does: it shares the parse with
// Uses:. The budget is 50 µs a rule.
func BenchmarkSentence(b *testing.B) {
	p := translator(b)
	var parsed []meaning.Expression
	for _, r := range benchRules() {
		e, ok := p.Parse(r)
		if !ok {
			b.Fatalf("Parse(%q) = none", r)
		}
		parsed = append(parsed, e)
	}
	for b.Loop() {
		for _, e := range parsed {
			if _, ok := meaning.Sentence(e, meaning.Check); !ok {
				b.Fatalf("Sentence(%q) = none", e.Text)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(parsed))/1000, "µs/rule")
}

// BenchmarkParseAndSentence is BenchmarkSentence with the parse, which the
// hover does once per version of a document for Uses: and the sentence
// together.
func BenchmarkParseAndSentence(b *testing.B) {
	p := translator(b)
	rules := benchRules()
	for b.Loop() {
		for _, r := range rules {
			if _, ok := say(p, r, meaning.Check); !ok {
				b.Fatalf("Sentence(%q) = none", r)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(rules))/1000, "µs/rule")
}
