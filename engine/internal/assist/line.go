package assist

import (
	"slices"
	"strings"
	"unicode"
)

// step is one link of a chain such as self.ports[0].name: a field or an index.
type step struct {
	name  string
	index bool
	// optional is true for the optional forms .?name and [?key], whose
	// result is an optional value.
	optional bool
}

func identRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// wordAt returns the bounds of the identifier around column c of line.
func wordAt(line []rune, c int) (start, end int) {
	start, end = c, c
	for start > 0 && identRune(line[start-1]) {
		start--
	}
	for end < len(line) && identRune(line[end]) {
		end++
	}
	return start, end
}

// scanLine calls visit for each rune of line[:to] that is outside a string
// literal, in order. Quote characters and the text they enclose are skipped.
// It reports whether line[:to] ends inside a string literal.
func scanLine(line []rune, to int, visit func(i int, r rune)) (inQuote bool) {
	quoted, inQuote := quotedRunes(line, to)
	for i := 0; i < to && i < len(line); i++ {
		if !quoted[i] {
			visit(i, line[i])
		}
	}
	return inQuote
}

// inString reports whether column c of line is inside a string literal that
// starts on the line.
func inString(line []rune, c int) bool {
	return scanLine(line, c, func(int, rune) {})
}

// calledAt reports whether the first character after column c that is not a
// space is an opening parenthesis.
func calledAt(line []rune, c int) bool {
	for ; c < len(line); c++ {
		if line[c] != ' ' && line[c] != '\t' {
			return line[c] == '('
		}
	}
	return false
}

// chainBefore reads the chain of fields and indexes that ends just before the
// dot at line[dot], first link first. It reports false when the receiver is
// anything else, such as a call or a literal.
func chainBefore(line []rune, dot int) ([]step, bool) {
	steps, _, ok := chainFrom(line, dot, 0)
	return steps, ok
}

// maxGroupDepth caps how many parenthesised groups chainFrom looks into, so
// that deeply nested input stays cheap.
const maxGroupDepth = 8

// chainFrom is chainBefore that also returns the column where the chain
// starts. depth counts the parenthesised groups around the chain.
func chainFrom(line []rune, dot, depth int) ([]step, int, bool) {
	var rev []step
	p := dot
	for {
		switch {
		case p > 0 && line[p-1] == ']':
			open := matchOpen(line, p-1, '[', ']')
			if open < 0 {
				return nil, 0, false
			}
			rev = append(rev, step{index: true, optional: line[open+1] == '?'})
			p = open
		case p > 0 && identRune(line[p-1]):
			q := p
			for q > 0 && identRune(line[q-1]) {
				q--
			}
			st := step{name: string(line[q:p])}
			p = q
			if p > 1 && line[p-1] == '?' && line[p-2] == '.' {
				st.optional = true
				p--
			}
			rev = append(rev, st)
			if p > 0 && line[p-1] == '.' {
				p--
				continue
			}
			slices.Reverse(rev)
			return rev, p, true
		case p > 0 && line[p-1] == ')':
			open := parenOpen(line, p-1)
			if depth >= maxGroupDepth || open < 0 || calledFrom(line, open) {
				return nil, 0, false
			}
			end := p - 1
			for end > open+1 && (line[end-1] == ' ' || line[end-1] == '\t') {
				end--
			}
			first := open + 1
			for first < end && (line[first] == ' ' || line[first] == '\t') {
				first++
			}
			inner, start, ok := chainFrom(line[:end], end, depth+1)
			if !ok || start != first {
				return nil, 0, false
			}
			slices.Reverse(rev)
			return append(inner, rev...), open, true
		default:
			return nil, 0, false
		}
	}
}

// parenOpen returns the column of the opening parenthesis that matches the
// closing one at line[closeAt], skipping parentheses inside string literals,
// or -1 when there is none.
func parenOpen(line []rune, closeAt int) int {
	return matchOpen(line, closeAt, '(', ')')
}

// matchOpen returns the column of the open rune that matches the closing rune
// at line[closeAt], skipping any inside string literals, or -1 when there is
// none.
func matchOpen(line []rune, closeAt int, open, closing rune) int {
	var stack []int
	found := -1
	scanLine(line, closeAt+1, func(i int, r rune) {
		switch {
		case r == open:
			stack = append(stack, i)
		case r == closing && len(stack) > 0:
			o := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if i == closeAt {
				found = o
			}
		}
	})
	return found
}

// calledFrom reports whether the parenthesis at line[open] opens the arguments
// of a call rather than a group. The keyword operator in is not a call.
func calledFrom(line []rune, open int) bool {
	i := open
	for i > 0 && (line[i-1] == ' ' || line[i-1] == '\t') {
		i--
	}
	if i == 0 {
		return false
	}
	if !identRune(line[i-1]) {
		return line[i-1] == ')' || line[i-1] == ']'
	}
	j := i
	for j > 0 && identRune(line[j-1]) {
		j--
	}
	return string(line[j:i]) != "in" || j > 0 && line[j-1] == '.'
}

// optionalCallBefore reports whether the receiver that ends just before the
// dot at line[dot] is a call of one of the functions that return an optional
// value: optional.of, optional.ofNonZeroValue or optional.none.
func optionalCallBefore(line []rune, dot int) bool {
	if dot == 0 || line[dot-1] != ')' {
		return false
	}
	i := parenOpen(line, dot-1)
	if i < 0 {
		return false
	}
	return slices.ContainsFunc([]string{"optional.of", "optional.ofNonZeroValue", "optional.none"}, func(c string) bool {
		return strings.HasSuffix(string(line[:i]), c) && (i == len(c) || !identRune(line[i-len(c)-1]) && line[i-len(c)-1] != '.')
	})
}
