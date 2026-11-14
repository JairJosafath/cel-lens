// Package hover writes the Markdown of the hover on a CEL expression: its
// sections, always in one order, and the escaping every text taken from a
// file goes through before it reaches the hover.
//
// The hover's layout is fixed: the part under the cursor, then the problems
// there with their fixes, then the rule's paragraphs, each led by its bold
// label: Checks: (or Returns:), Uses:, Cost:, Examples: and Docs:. Blocks
// are separated by a thematic break (---), and what has nothing is left out.
//
// The text a hover takes from a file (a schema's description, a name, a kind,
// a field path, a message) is Markdown to the client, which may render HTML
// and run command links. Escape writes such text so it shows as written and
// makes no link, HTML or structure; Code and Block put it in a code span or a
// fenced block whose backticks it cannot close.
package hover

import "strings"

// Sections are the parts of a hover, each in Markdown without its label: ""
// for a section with nothing to say. The zero value renders as "".
type Sections struct {
	// Part describes what the cursor is on: a variable, a field or a
	// function. It is "" where the cursor is on no name.
	Part string
	// Fix holds the problems at the cursor and their fixes.
	Fix string
	// Checks says what the rule checks; Returns what an expression that is
	// no check gives. Only one is shown: Checks when both are set.
	Checks, Returns string
	// Uses lists the variables the expression reads.
	Uses string
	// Cost is the expression's estimated cost.
	Cost string
	// Examples says what the rule does to its examples.
	Examples string
	// Docs links to the documentation.
	Docs string
}

// The labels of the rule's paragraphs, as the hover shows them.
const (
	labelChecks   = "**Checks:**"
	labelReturns  = "**Returns:**"
	labelUses     = "**Uses:**"
	labelCost     = "**Cost:**"
	labelExamples = "**Examples:**"
	labelDocs     = "**Docs:**"
)

// separator is the Markdown between two blocks of a hover.
const separator = "\n\n---\n\n"

// Render returns the hover of s in Markdown: the part, the fix, then the
// rule's paragraphs, in that order and no other. Blocks are separated by
// ---, paragraphs by an empty line, each paragraph led by its bold label,
// on the same line unless the body starts with a line break, as a list
// does. A section that is empty, or only white space, is left out with its
// separator. It is "" when every section is.
func Render(s Sections) string {
	var rule []string
	add := func(label, body string) {
		body = strings.TrimRight(body, " \t\r\n")
		switch trimmed := strings.TrimLeft(body, " \t\r\n"); {
		case trimmed == "":
		case strings.HasPrefix(strings.TrimLeft(body, " \t"), "\n"):
			// A body that starts on a line of its own, such as a list,
			// stays below its label.
			rule = append(rule, label+"\n\n"+trimmed)
		default:
			rule = append(rule, label+" "+trimmed)
		}
	}
	if strings.TrimSpace(s.Checks) != "" {
		add(labelChecks, s.Checks)
	} else {
		add(labelReturns, s.Returns)
	}
	add(labelUses, s.Uses)
	add(labelCost, s.Cost)
	add(labelExamples, s.Examples)
	add(labelDocs, s.Docs)
	var blocks []string
	for _, b := range []string{s.Part, s.Fix, strings.Join(rule, "\n\n")} {
		if b = strings.TrimSpace(b); b != "" {
			blocks = append(blocks, b)
		}
	}
	return strings.Join(blocks, separator)
}

// escaped are the characters Escape writes with a backslash: every one that
// can start Markdown structure, a link, an autolink, HTML or an entity, and
// = and -, whose line under another makes that one a setext heading.
const escaped = "\\`*_{}[]<>()#+-=!|~&"

// Escape writes s, text taken from a file, so that Markdown shows it as it
// is: each character that could start emphasis, a code span, a link, an
// autolink, raw HTML, an entity, a heading, a list or a table gets a
// backslash. A carriage return, alone or before a line feed, becomes a line
// feed first, so that every client splits the lines of s where Markdown does.
// The result makes no link, no HTML and no heading, whatever s holds.
func Escape(s string) string {
	if strings.ContainsRune(s, '\r') {
		s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	}
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(escaped, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Code returns s as one Markdown code span, delimited by a run of backticks
// longer than any s holds, so nothing in s can close it. A line break in s
// becomes a space, as a code span shows it, so that s cannot end the
// paragraph the span is in. A space pads s when it starts or ends with a
// backtick, or with a space at both ends, which the span then strips. An
// empty s gives a span of one space.
func Code(s string) string {
	if strings.ContainsAny(s, "\r\n") {
		s = strings.ReplaceAll(s, "\r\n", " ")
		s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
	}
	if s == "" {
		s = " "
	}
	fence := strings.Repeat("`", longestRun(s)+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") ||
		strings.HasPrefix(s, " ") && strings.HasSuffix(s, " ") && strings.Trim(s, " ") != "" {
		s = " " + s + " "
	}
	return fence + s + fence
}

// Block returns s as a fenced code block in the language lang, delimited by
// at least three backticks and more than any run s holds, so no line of s
// can close it. A final line break of s is dropped.
func Block(lang, s string) string {
	fence := strings.Repeat("`", max(3, longestRun(s)+1))
	return fence + lang + "\n" + strings.TrimSuffix(s, "\n") + "\n" + fence
}

// longestRun is the length of the longest run of backticks in s.
func longestRun(s string) int {
	if !strings.Contains(s, "`") {
		return 0
	}
	longest, run := 0, 0
	for _, r := range s {
		if r != '`' {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return longest
}
