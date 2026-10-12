package extract

import (
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// splitLines splits data into lines of runes without their line endings.
func splitLines(data []byte) [][]rune {
	text := strings.ReplaceAll(string(Text(data)), "\r\n", "\n")
	parts := strings.Split(text, "\n")
	lines := make([][]rune, len(parts))
	for i, p := range parts {
		lines[i] = []rune(p)
	}
	return lines
}

// span finds the source range of the text of scalar n, a mapping value whose
// key starts at column keyCol (from 0). The YAML parser only
// reports where a scalar starts (for a block scalar, its header), so the end,
// and the start of block scalars, are read from the source lines.
func (f *finder) span(n *yaml.Node, keyCol int) (start, end Position, ok bool) {
	line, col := n.Line-1, n.Column-1
	if line < 0 || line >= len(f.lines) {
		return start, end, false
	}
	var sl, sc, el, ec int
	switch {
	case n.Style&yaml.DoubleQuotedStyle != 0:
		sl, sc, el, ec, ok = f.quoted(line, col+1, '"')
	case n.Style&yaml.SingleQuotedStyle != 0:
		sl, sc, el, ec, ok = f.quoted(line, col+1, '\'')
	case n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		sl, sc, el, ec, ok = f.block(line, col, keyCol)
	default:
		sl, sc, el, ec, ok = f.plain(line, col, n.Value)
	}
	return Position{sl + 1, sc + 1}, Position{el + 1, ec + 1}, ok
}

// quoted scans from the first character after the opening quote to the
// closing quote, which may be on a later line. Positions count from 0.
func (f *finder) quoted(line, col int, quote rune) (sl, sc, el, ec int, ok bool) {
	for l := line; l < len(f.lines); l++ {
		runes := f.lines[l]
		start := 0
		if l == line {
			start = col
		}
		for i := start; i < len(runes); i++ {
			switch {
			case quote == '"' && runes[i] == '\\':
				i++
			case runes[i] != quote:
			case quote == '\'' && i+1 < len(runes) && runes[i+1] == '\'':
				i++
			default:
				return line, col, l, i, true
			}
		}
	}
	return 0, 0, 0, 0, false
}

// plain finds the end of a plain scalar by matching its words in order, which
// also covers plain scalars folded over several lines.
func (f *finder) plain(line, col int, value string) (sl, sc, el, ec int, ok bool) {
	l, c := line, col
	for _, w := range strings.Fields(value) {
		word := []rune(w)
		for {
			i := indexRunes(f.lines[l], c, word)
			if i >= 0 {
				c = i + len(word)
				break
			}
			l++
			if l >= len(f.lines) {
				return 0, 0, 0, 0, false
			}
			c = 0
		}
	}
	return line, col, l, c, true
}

func indexRunes(line []rune, from int, word []rune) int {
	for i := from; i+len(word) <= len(line); i++ {
		if string(line[i:i+len(word)]) == string(word) {
			return i
		}
	}
	return -1
}

// block finds the content lines of the block scalar whose header is on line.
// keyCol is the column of the mapping key, which the content must be indented
// beyond, unless the header has an indentation indicator such as |2, which
// puts the content indent that many columns right of keyCol. col is the column
// of the scalar's first token, which is an anchor or tag when the header has
// one before its | or > character.
func (f *finder) block(line, col, keyCol int) (sl, sc, el, ec int, ok bool) {
	indent := -1
	line, col = f.header(line, col)
	if d := indicator(f.lines[line][col:]); d > 0 {
		indent = keyCol + d
	}
	first, last := -1, -1
	for l := line + 1; l < len(f.lines); l++ {
		runes := f.lines[l]
		if strings.TrimSpace(string(runes)) == "" {
			continue
		}
		n := leadingSpaces(runes)
		if indent < 0 {
			if n <= keyCol {
				break
			}
			indent = n
		}
		if n < indent {
			break
		}
		if first < 0 {
			first = l
		}
		last = l
	}
	if first < 0 {
		return 0, 0, 0, 0, false
	}
	trimmed := strings.TrimRightFunc(string(f.lines[last]), unicode.IsSpace)
	return first, indent, last, len([]rune(trimmed)), true
}

// header skips the anchors and tags, and the spaces, tabs, comments and line
// breaks around them, that precede a block scalar's | or > character, and returns
// the position of that character.
func (f *finder) header(line, col int) (hl, hc int) {
	for line < len(f.lines) {
		runes := f.lines[line]
		switch {
		case col >= len(runes):
			line++
			col = 0
		case runes[col] == ' ' || runes[col] == '\t':
			col++
		case runes[col] == '#':
			col = len(runes)
		case runes[col] == '&' || runes[col] == '!':
			for col < len(runes) && runes[col] != ' ' && runes[col] != '\t' {
				col++
			}
		default:
			return line, col
		}
	}
	return len(f.lines) - 1, 0
}

// indicator returns the indentation indicator digit in a block scalar header
// (the runes from | or >), or 0 when there is none.
func indicator(header []rune) int {
	if len(header) == 0 {
		return 0
	}
	for _, r := range header[1:] {
		switch {
		case r >= '1' && r <= '9':
			return int(r - '0')
		case r != '-' && r != '+':
			return 0
		}
	}
	return 0
}

func leadingSpaces(runes []rune) int {
	n := 0
	for n < len(runes) && runes[n] == ' ' {
		n++
	}
	return n
}

// Span finds the source range of the text of scalar n, a mapping value whose
// key starts at column keyCol (from 0), in lines, the file split into lines of
// runes. The range follows the rules of Expression.Start and Expression.End,
// except that an empty quoted scalar covers its quotes, so the range is never
// empty. It reports false when the source cannot be read that way.
func Span(lines [][]rune, n *yaml.Node, keyCol int) (start, end Position, ok bool) {
	f := finder{lines: lines}
	start, end, ok = f.span(n, keyCol)
	if ok && start == end && n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		start.Column--
		end.Column++
	}
	return start, end, ok
}
