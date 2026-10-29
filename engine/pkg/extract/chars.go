package extract

import (
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// Range is the part of a file that one character of an expression's text comes
// from. End is just after the last source character, so an escape such as \u00e9
// spans all its source characters. A line break that YAML folds into a space or
// into line breaks has an empty range where the break was.
type Range struct {
	Start, End Position
}

// chars maps each character of the text of the plain, double-quoted or
// single-quoted scalar n to its place in the source. The scalar's text starts at
// line l and column c (from 0), just after the opening quote of a quoted one.
// It returns nil when the source shows the text as written on one line, and when
// the source does not decode to n.Value, so that callers fall back to the whole
// scalar.
//
// It follows the YAML folding rules: a line break between two lines becomes a
// space, k empty lines become k line breaks, and the indentation of the next
// line and the spaces before a break are dropped; a line separator (U+2028) or
// paragraph separator (U+2029) is kept as it is instead of folding to a space; in double quotes a \ at the
// end of a line joins the lines, and an escape is one character.
func (f *finder) chars(n *yaml.Node, l, c int) []Range {
	var quote rune
	switch {
	case n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		return nil
	case n.Style&yaml.DoubleQuotedStyle != 0:
		quote = '"'
	case n.Style&yaml.SingleQuotedStyle != 0:
		quote = '\''
	}
	want := []rune(n.Value)
	out := make([]Range, 0, len(want))
	got := make([]rune, 0, len(want))
	at := func(l, c int) Position { return Position{l + 1, c + 1} }
	for {
		if l >= len(f.lines) {
			return nil
		}
		runes := f.lines[l]
		keep, closed, joined := len(out), false, false
	line:
		for c < len(runes) {
			r := runes[c]
			switch {
			case quote == '"' && r == '\\':
				if c+1 == len(runes) {
					joined = true
					break line
				}
				v, size, ok := escape(runes[c:])
				if !ok {
					return nil
				}
				out, got = append(out, Range{at(l, c), at(l, c+size)}), append(got, v)
				c += size
				keep = len(out)
			case quote != 0 && r == quote:
				if quote == '\'' && c+1 < len(runes) && runes[c+1] == '\'' {
					out, got = append(out, Range{at(l, c), at(l, c+2)}), append(got, r)
					c += 2
					keep = len(out)
					continue
				}
				closed = true
				break line
			case quote == 0 && r == '#' && c > 0 && (runes[c-1] == ' ' || runes[c-1] == '\t'):
				break line
			default:
				out, got = append(out, Range{at(l, c), at(l, c+1)}), append(got, r)
				c++
				if r != ' ' && r != '\t' {
					keep = len(out)
				}
			}
		}
		end := at(l, 0)
		if joined {
			keep = len(out)
		} else {
			out, got = out[:keep], got[:keep]
		}
		if keep > 0 && out[keep-1].End.Line == l+1 {
			end = out[keep-1].End
		}
		if closed || quote == 0 && len(got) >= len(want) {
			break
		}
		next := l + 1
		for next < len(f.lines) && isBlank(f.lines[next]) {
			next++
		}
		if next >= len(f.lines) {
			return nil
		}
		blanks := next - l - 1
		if first := f.breakAt(l); first != '\n' && !joined {
			out, got = append(out, Range{end, end}), append(got, first)
		} else if blanks == 0 && !joined {
			out, got = append(out, Range{end, end}), append(got, ' ')
		}
		for i := range blanks {
			out, got = append(out, Range{at(l+1+i, 0), at(l+1+i, 0)}), append(got, f.breakAt(l+1+i))
		}
		l, c = next, leadingBlanks(f.lines[next])
	}
	if !slices.Equal(got, want) {
		return nil
	}
	if len(out) > 0 && out[0].Start.Line == out[len(out)-1].End.Line && len(out) == out[len(out)-1].End.Column-out[0].Start.Column {
		return nil
	}
	return out
}

// breakAt returns the character the line break ending line l folds to: U+2028
// and U+2029 are kept, any other break is a line feed.
func (f *finder) breakAt(l int) rune {
	if l < len(f.breaks) {
		return f.breaks[l]
	}
	return '\n'
}

func isBlank(runes []rune) bool {
	return leadingBlanks(runes) == len(runes)
}

func leadingBlanks(runes []rune) int {
	n := 0
	for n < len(runes) && (runes[n] == ' ' || runes[n] == '\t') {
		n++
	}
	return n
}

// escape decodes the double-quoted escape at the start of s, which begins with
// a backslash, and returns the character and the number of source characters.
func escape(s []rune) (r rune, size int, ok bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	switch s[1] {
	case '0':
		return 0, 2, true
	case 'a':
		return '\a', 2, true
	case 'b':
		return '\b', 2, true
	case 'n':
		return '\n', 2, true
	case 'v':
		return '\v', 2, true
	case 'f':
		return '\f', 2, true
	case 'r':
		return '\r', 2, true
	case 'e':
		return 0x1b, 2, true
	case 'N':
		return 0x85, 2, true
	case '_':
		return 0xa0, 2, true
	case 'L':
		return 0x2028, 2, true
	case 'P':
		return 0x2029, 2, true
	case 't', '\t':
		return '\t', 2, true
	case ' ', '"', '/', '\\':
		return s[1], 2, true
	}
	digits := map[rune]int{'x': 2, 'u': 4, 'U': 8}[s[1]]
	if digits == 0 || len(s) < 2+digits {
		return 0, 0, false
	}
	v, err := strconv.ParseInt(string(s[2:2+digits]), 16, 32)
	if err != nil {
		return 0, 0, false
	}
	return rune(v), 2 + digits, true
}
