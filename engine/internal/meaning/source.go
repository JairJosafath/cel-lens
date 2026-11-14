package meaning

import "strings"

// literals marks the bytes of s that are inside a CEL string or bytes
// literal, its quotes and prefix included: '…', "…", ”'…”', """…""",
// raw (r, R) and bytes (b, B) ones. A literal not closed runs to the end.
func literals(s string) []bool {
	in := make([]bool, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\'' && s[i] != '"' {
			continue
		}
		// The prefix: up to two of r, R, b, B right before the quote, not
		// part of a longer name.
		from, raw := i, false
		for k := 0; k < 2 && from > 0 && strings.IndexByte("rRbB", s[from-1]) >= 0; k++ {
			from--
			raw = raw || s[from] == 'r' || s[from] == 'R'
		}
		if from > 0 && nameByte(s[from-1]) {
			from, raw = i, false
		}
		quote := s[i : i+1]
		if strings.HasPrefix(s[i:], strings.Repeat(quote, 3)) {
			quote = strings.Repeat(quote, 3)
		}
		end := len(s)
		for j := i + len(quote); j < len(s); j++ {
			if !raw && s[j] == '\\' {
				j++
				continue
			}
			if strings.HasPrefix(s[j:], quote) {
				end = j + len(quote)
				break
			}
		}
		for k := from; k < end; k++ {
			in[k] = true
		}
		i = end - 1
	}
	return in
}

// nameByte reports whether b can be part of a name.
func nameByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// brackets pairs each bracket of text outside its strings with its partner,
// by position. It is false when one has no partner.
func brackets(text string) (map[int]int, bool) {
	in := literals(text)
	partner := map[int]int{}
	var open []int
	for i := range len(text) {
		if in[i] {
			continue
		}
		switch text[i] {
		case '(', '[', '{':
			open = append(open, i)
		case ')', ']', '}':
			if len(open) == 0 || text[open[len(open)-1]] != opener(text[i]) {
				return nil, false
			}
			o := open[len(open)-1]
			open = open[:len(open)-1]
			partner[o], partner[i] = i, o
		}
	}
	return partner, len(open) == 0
}

// widen widens start and end until every bracket between them has its
// partner, as brackets pairs them, between them too.
func widen(partner map[int]int, start, end int) (wideStart, wideEnd int) {
	for changed := true; changed; {
		changed = false
		for i := start; i < end; i++ {
			p, ok := partner[i]
			switch {
			case !ok:
			case p < start:
				start, changed = p, true
			case p >= end:
				end, changed = p+1, true
			}
		}
	}
	return start, end
}

// opener is the bracket that closer closes.
func opener(closer byte) byte {
	switch closer {
	case ')':
		return '('
	case ']':
		return '['
	}
	return '{'
}

// oneLine returns s, CEL source, with each run of white space outside its
// strings made one space; what is inside a string stays as it is. It is
// false when s holds a comment, or a line break inside a string, which one
// line cannot show.
func oneLine(s string) (string, bool) {
	in := literals(s)
	var b strings.Builder
	space := false
	for i := range len(s) {
		c := s[i]
		switch {
		case in[i]:
			if c == '\n' || c == '\r' {
				return "", false
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			space = true
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			return "", false
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteByte(c)
	}
	return b.String(), true
}
