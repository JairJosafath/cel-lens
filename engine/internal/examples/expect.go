package examples

import (
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// refusedWords are the folder names and file name words that make an example
// expect to be refused.
var refusedWords = []string{"invalid", "bad", "refused", "rejected", "denied"}

// pathExpectation is what an example at rel, a slash-separated path below its
// workspace folder, expects by its path: Refused when a folder of it, or a
// word of its file name split at -, _ and ., is one of refusedWords, in any
// case.
func pathExpectation(rel string) Expectation {
	dir, name := path.Split(strings.ToLower(rel))
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	words = append(words, strings.Split(strings.Trim(dir, "/"), "/")...)
	if slices.ContainsFunc(words, func(w string) bool { return slices.Contains(refusedWords, w) }) {
		return Refused
	}
	return Allowed
}

// directivePrefix starts a comment that says what an example expects.
const directivePrefix = "cel-lens:"

// directiveForms are the forms a # cel-lens: comment takes, for its problem.
const directiveForms = `# cel-lens: allowed, # cel-lens: refused and # cel-lens: refused "part of the message"`

// readComments reads what the # cel-lens: comments at the top of the
// document of o say it expects, which wins over its path: those of the
// comment lines directly above its first key, and of those directly after
// its --- and on that line. The first that reads is taken; one that does not
// read is a problem on it.
func (o *Object) readComments(lines [][]rune) {
	for _, c := range topComments(lines, o.Line) {
		text := strings.TrimSpace(strings.TrimPrefix(c.text, "#"))
		rest, ok := strings.CutPrefix(text, directivePrefix)
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		expect, msg, ok := directive(rest)
		if !ok {
			o.comments = append(o.comments, check.Diagnostic{
				File: o.File, Severity: check.SeverityError, Start: c.start, End: c.end,
				Message: "this # cel-lens: comment is none of " + directiveForms,
			})
			continue
		}
		if !o.said {
			o.Expect, o.Message, o.said = expect, msg, true
		}
	}
}

// directive reads what follows # cel-lens:, false when it is none of the
// forms.
func directive(s string) (Expectation, string, bool) {
	switch s {
	case "allowed":
		return Allowed, "", true
	case "refused":
		return Refused, "", true
	}
	quoted, ok := strings.CutPrefix(s, "refused")
	if !ok || quoted == "" || !unicode.IsSpace(rune(quoted[0])) {
		return Allowed, "", false
	}
	quoted = strings.TrimSpace(quoted)
	if !strings.HasPrefix(quoted, `"`) {
		return Allowed, "", false
	}
	msg, err := strconv.Unquote(quoted)
	if err != nil || msg == "" {
		return Allowed, "", false
	}
	return Refused, msg, true
}

// firstRune is the first character of s, which is not empty.
func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

// comment is a comment of a file: its text from its # to the end of its
// line, without trailing space, and where that is.
type comment struct {
	text       string
	start, end extract.Position
}

// topComments returns the comments at the top of the document whose first
// key is at line, from 1: the comment lines directly above it, and, when the
// document starts with ---, the comment on that line and the comment lines
// directly after it, in the order of the file, each once.
func topComments(lines [][]rune, line int) []comment {
	at := func(i int) (comment, bool) {
		text := string(lines[i])
		trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
		if !strings.HasPrefix(trimmed, "#") {
			return comment{}, false
		}
		col := len([]rune(text)) - len([]rune(trimmed)) + 1
		trimmed = strings.TrimRightFunc(trimmed, unicode.IsSpace)
		return comment{text: trimmed, start: extract.Position{Line: i + 1, Column: col}, end: extract.Position{Line: i + 1, Column: col + len([]rune(trimmed))}}, true
	}
	blank := func(i int) bool { return strings.TrimSpace(string(lines[i])) == "" }
	first := line - 1 // the index of the first key's line
	if first < 0 || first > len(lines) {
		return nil
	}
	var above []comment
	i := first - 1
	for ; i >= 0; i-- {
		c, ok := at(i)
		if !ok {
			break
		}
		above = append(above, c)
	}
	slices.Reverse(above)
	for ; i >= 0; i-- {
		if _, ok := at(i); !ok && !blank(i) {
			break
		}
	}
	var after []comment
	if i >= 0 {
		text := string(lines[i])
		if rest, ok := strings.CutPrefix(text, "---"); ok && (rest == "" || unicode.IsSpace(firstRune(rest))) {
			if trimmed := strings.TrimSpace(rest); strings.HasPrefix(trimmed, "#") {
				col := len([]rune(text)) - len([]rune(strings.TrimLeftFunc(rest, unicode.IsSpace))) + 1
				after = append(after, comment{text: trimmed, start: extract.Position{Line: i + 1, Column: col}, end: extract.Position{Line: i + 1, Column: col + len([]rune(trimmed))}})
			}
			for j := i + 1; j < first; j++ {
				c, ok := at(j)
				if !ok {
					break
				}
				after = append(after, c)
			}
		}
	}
	out := after
	for _, c := range above {
		if !slices.ContainsFunc(out, func(o comment) bool { return o.start.Line == c.start.Line }) {
			out = append(out, c)
		}
	}
	return out
}
