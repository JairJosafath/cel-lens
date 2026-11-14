package hover_test

import (
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/hover"
)

// TestRender holds the hover's order: the part, the fix, then the rule's
// paragraphs Checks: (or Returns:), Uses:, Cost:, Examples: and Docs:, with
// what is empty left out and no empty line or stray separator for it (#52).
func TestRender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   hover.Sections
		want string
	}{
		{"nothing", hover.Sections{}, ""},
		{"only white space", hover.Sections{Part: " \n", Uses: "\n\n", Docs: "\t"}, ""},
		{"the part alone", hover.Sections{Part: "```cel\n(variable) self: object\n```"}, "```cel\n(variable) self: object\n```"},
		{"one paragraph", hover.Sections{Uses: "`self`"}, "**Uses:** `self`"},
		{
			"the part and a paragraph",
			hover.Sections{Part: "part", Uses: "`self`"},
			"part\n\n---\n\n**Uses:** `self`",
		},
		{
			"the fix without the part",
			hover.Sections{Fix: "fix", Cost: "cost"},
			"fix\n\n---\n\n**Cost:** cost",
		},
		{
			"every section",
			hover.Sections{Part: "part", Fix: "fix", Checks: "checks", Returns: "returns", Uses: "uses", Cost: "cost", Examples: "examples", Docs: "docs"},
			"part\n\n---\n\nfix\n\n---\n\n**Checks:** checks\n\n**Uses:** uses\n\n**Cost:** cost\n\n**Examples:** examples\n\n**Docs:** docs",
		},
		{
			"a body that starts on a line of its own",
			hover.Sections{Part: "part", Uses: "\n\n- `self`\n- `oldSelf`\n"},
			"part\n\n---\n\n**Uses:**\n\n- `self`\n- `oldSelf`",
		},
		{"returns without checks", hover.Sections{Returns: "the message", Docs: "docs"}, "**Returns:** the message\n\n**Docs:** docs"},
		{
			"a body's own line breaks are trimmed at its ends only",
			hover.Sections{Part: "\npart\n", Examples: "3 pass\n\n- a\n- b\n", Docs: "docs"},
			"part\n\n---\n\n**Examples:** 3 pass\n\n- a\n- b\n\n**Docs:** docs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hover.Render(tt.in); got != tt.want {
				t.Errorf("Render(%+v) =\n%q\nwant\n%q", tt.in, got, tt.want)
			}
		})
	}
}

// TestRenderEveryCombination renders every combination of present and
// absent sections and checks that each present one is there once, in the
// order, with nothing for an absent one (#52).
func TestRenderEveryCombination(t *testing.T) {
	t.Parallel()
	// The sections in the order the hover shows them, with how each appears.
	type section struct {
		set  func(*hover.Sections, string)
		show string
	}
	order := []section{
		{func(s *hover.Sections, v string) { s.Part = v }, "PART"},
		{func(s *hover.Sections, v string) { s.Fix = v }, "FIX"},
		{func(s *hover.Sections, v string) { s.Checks = v }, "**Checks:** CHECKS"},
		{func(s *hover.Sections, v string) { s.Returns = v }, "**Returns:** RETURNS"},
		{func(s *hover.Sections, v string) { s.Uses = v }, "**Uses:** USES"},
		{func(s *hover.Sections, v string) { s.Cost = v }, "**Cost:** COST"},
		{func(s *hover.Sections, v string) { s.Examples = v }, "**Examples:** EXAMPLES"},
		{func(s *hover.Sections, v string) { s.Docs = v }, "**Docs:** DOCS"},
	}
	for mask := range 1 << len(order) {
		var in hover.Sections
		var blocks, rule []string
		checks := mask&(1<<2) != 0
		for i, sec := range order {
			if mask&(1<<i) == 0 {
				continue
			}
			body := sec.show[strings.LastIndex(sec.show, " ")+1:]
			sec.set(&in, body)
			switch {
			case i < 2:
				blocks = append(blocks, sec.show)
			case i == 3 && checks: // Returns gives way to Checks
			default:
				rule = append(rule, sec.show)
			}
		}
		if len(rule) > 0 {
			blocks = append(blocks, strings.Join(rule, "\n\n"))
		}
		want := strings.Join(blocks, "\n\n---\n\n")
		got := hover.Render(in)
		if got != want {
			t.Errorf("Render(%+v) =\n%q\nwant\n%q", in, got, want)
		}
		if strings.Contains(got, "\n\n\n") || strings.HasPrefix(got, "\n") || strings.HasSuffix(got, "\n") ||
			strings.HasPrefix(got, "---") || strings.HasSuffix(got, "---") || strings.Contains(got, "---\n\n---") {
			t.Errorf("Render(%+v) = %q, want no empty line or stray separator", in, got)
		}
	}
}

// TestEscape checks that text from a file shows as written, with no
// character that starts a link, HTML, an entity or structure left bare.
func TestEscape(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"", ""},
		{"The port to listen on.", "The port to listen on."},
		{"[x](command:workbench.action.terminal.new)", `\[x\]\(command:workbench.action.terminal.new\)`},
		{`<img src="https://example.invalid/a.png">`, `\<img src\="https://example.invalid/a.png"\>`},
		{`<span style="color:#f00;">`, `\<span style\="color:\#f00;"\>`},
		{"```", "\\`\\`\\`"},
		{"&lt;b&gt; *em* _em_ # h", `\&lt;b\&gt; \*em\* \_em\_ \# h`},
		{"<command:x.y>", `\<command:x.y\>`},
		{"a\\b", `a\\b`},
		{"é - + ! | ~ { }", `é \- \+ \! \| \~ \{ \}`},
		// The review of cel-lens-vscode#57 (comment 731): a line of = or -
		// under a line of text makes it a setext heading, and a carriage
		// return splits lines where a client may not.
		{"Title\n===", "Title\n\\=\\=\\="},
		{"Title\n---", "Title\n\\-\\-\\-"},
		{"a = b", `a \= b`},
		{"one\rtwo", "one\ntwo"},
		{"one\r\ntwo\r\n===", "one\ntwo\n\\=\\=\\="},
	}
	for _, tt := range tests {
		if got := hover.Escape(tt.in); got != tt.want {
			t.Errorf("Escape(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestCode checks that a code span is delimited by a run of backticks
// longer than any it holds, and that nothing in it ends its paragraph.
func TestCode(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"self", "`self`"},
		{"", "` `"},
		{"a`b", "``a`b``"},
		{"'```'", "````'```'````"},
		{"`x", "`` `x ``"},
		{"x`", "`` x` ``"},
		{" x ", "`  x  `"},
		{"  ", "`  `"},
		{"a\nb\r\nc", "`a b c`"},
		{"[x](command:a.b)", "`[x](command:a.b)`"},
	}
	for _, tt := range tests {
		if got := hover.Code(tt.in); got != tt.want {
			t.Errorf("Code(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestBlock checks that a fenced block is delimited by at least three
// backticks and more than any run it holds.
func TestBlock(t *testing.T) {
	t.Parallel()
	tests := []struct{ lang, in, want string }{
		{"cel", "(variable) self: object", "```cel\n(variable) self: object\n```"},
		{"cel", "(field) a```b: string\n", "````cel\n(field) a```b: string\n````"},
		{"", "````", "`````\n````\n`````"},
	}
	for _, tt := range tests {
		if got := hover.Block(tt.lang, tt.in); got != tt.want {
			t.Errorf("Block(%q, %q) = %q, want %q", tt.lang, tt.in, got, tt.want)
		}
	}
}
