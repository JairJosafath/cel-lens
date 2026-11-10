package cellens

import (
	"slices"
	"strings"
)

// Spot is where a position in the text of a .cellens file is, as completion
// and hover need it: in which mapping, at a key or in the value of which key,
// and the word there.
type Spot struct {
	// Path leads from the root of the file to the mapping the position is
	// in: its keys, and "-" for an item of a list. It is empty at the root,
	// ["types", "-"] in an entry, and ["types", "-", "schema", "properties",
	// "max"] in the schema of the property max.
	Path []string
	// Key is the key whose value the position is in; "" where a key of the
	// mapping at Path is written.
	Key string
	// Item is true in an item of a list that is the value of Key, as in
	// required: [a, b].
	Item bool
	// Line is the position's line; Start and End are the characters of the
	// word the position is in on that line: the key written so far, or the
	// value without its quotes. All count from 0.
	Line, Start, End int
	// Quote is the quote around the value, 0 for a plain one.
	Quote rune
	// Colon is true at a key that has its colon already.
	Colon bool
	// Pad is true at a value that starts right after its key's colon: a
	// value written there needs a space before it.
	Pad bool
	// Flow is true in a mapping or list written in brackets.
	Flow bool
	// Indent is the column the keys of the mapping at Path start at, in a
	// block mapping.
	Indent int
	// Keys are the keys of the mapping at Path with their values as written
	// on one line, without quotes; a value written otherwise is "".
	Keys map[string]string
	// KeyAt is the key of the line or the flow mapping the position is in,
	// whether the position is on that key or in its value; "" for none.
	KeyAt string
	// Entry are the keys of the entry the position is in, as Keys has them,
	// and EntryLine the line of its first key, from 0; nil and 0 outside
	// every entry.
	Entry     map[string]string
	EntryLine int
}

// scalarLists are the keys of a schema whose value is a list of strings: a
// plain word below their dash is an item, not a key.
var scalarLists = []string{"required", "enum", "x-kubernetes-list-map-keys"}

// Locate reads where the position at line and character col, both from 0,
// is in text, a .cellens file. It reads line by line, by indentation and the
// brackets of a line, so a text YAML cannot read, as one being typed, still
// has the spots of the lines it can. It is false for a position in a
// comment, in the indentation before a line's content, in a value written
// over several lines, or past the end of the text.
func Locate(text []byte, line, col int) (Spot, bool) {
	r := newLineReader(text)
	if line < 0 || line >= len(r.lines) || col < 0 {
		return Spot{}, false
	}
	t := r.lines[line]
	col = min(col, len(t.text))
	if t.comment >= 0 && col > t.comment {
		return Spot{}, false
	}
	var c, dash int
	switch {
	case t.blank:
		c, dash = col, -1
	case col < t.col:
		return Spot{}, false
	default:
		c, dash = t.col, t.dash
	}
	path, start, items, ok := r.chain(line, c, dash)
	if !ok {
		return Spot{}, false
	}
	s := Spot{Path: path, Line: line, Start: col, End: col, Indent: c}
	switch {
	case t.blank:
	case t.val.flow && col > t.val.start && (t.colon < 0 || col > t.colon):
		if !r.inFlow(&s, t, col) {
			return Spot{}, false
		}
	case t.colon >= 0 && col > t.colon:
		if t.val.block {
			return Spot{}, false
		}
		s.Key, s.KeyAt = t.key, t.key
		s.Start, s.End, s.Quote = t.val.start, max(t.val.end, col), t.val.quote
		switch {
		case t.val.start == t.val.end && col <= t.val.start:
			s.Start, s.End = col, col
		case col < t.val.start && t.val.quote == 0:
			s.Start = col // in the spaces before the value, which it replaces
		}
		s.Pad = col == t.colon+1
	case t.colon >= 0:
		s.Start, s.End, s.Colon, s.KeyAt = t.keyAt, t.keyEnd, true, t.key
	default:
		s.Start, s.End = t.val.start, max(t.val.end, col)
		if n := len(path); n >= 2 && path[n-1] == "-" && slices.Contains(scalarLists, path[n-2]) {
			s.Path, s.Key, s.Item = path[:n-2], path[n-2], true
		}
	}
	if col < s.Start || col > s.End {
		return Spot{}, false // before an opening quote
	}
	if s.Keys == nil {
		s.Keys = r.keys(start, c)
	}
	if len(path) >= 2 && path[0] == "types" && path[1] == "-" {
		s.Entry, s.EntryLine = r.entry(items[1], line, s)
	}
	return s, true
}

// tline is one line of a .cellens text, read as a line of YAML in block
// style. Columns count characters from 0.
type tline struct {
	text []rune
	// blank is true for a line of spaces or a comment.
	blank bool
	// dash is the column of the last "- " before the content, -1 for none;
	// col is the column the content starts at.
	dash, col int
	// comment is the column of the # that starts a comment, -1 for none.
	comment int
	// key is the key of the content, from keyAt to keyEnd, and colon the
	// column of its colon; colon is -1 for content without a key.
	key           string
	keyAt, keyEnd int
	colon         int
	// val is the value after the colon, or the content without a key.
	val vspan
}

// vspan is where a value is on its line.
type vspan struct {
	// start and end are its characters, without quotes.
	start, end int
	quote      rune
	// flow is true for a value in brackets, block for a | or > scalar.
	flow, block bool
}

// lineReader reads the lines of a .cellens text.
type lineReader struct{ lines []tline }

func newLineReader(text []byte) *lineReader {
	raw := strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n")
	r := &lineReader{lines: make([]tline, len(raw))}
	for i, l := range raw {
		r.lines[i] = readLine([]rune(strings.TrimSuffix(l, "\r")))
	}
	return r
}

// readLine reads one line of block YAML.
func readLine(text []rune) tline {
	t := tline{text: text, dash: -1, comment: -1, colon: -1}
	i := 0
	for i < len(text) && text[i] == ' ' {
		i++
	}
	for i < len(text) && text[i] == '-' && (i+1 == len(text) || text[i+1] == ' ') {
		t.dash = i
		i++
		for i < len(text) && text[i] == ' ' {
			i++
		}
	}
	t.col = i
	if i == len(text) || text[i] == '#' {
		t.blank = t.dash < 0
		if i < len(text) {
			t.comment = i
		}
		t.val = vspan{start: i, end: i}
		return t
	}
	if q := text[i]; q == '"' || q == '\'' {
		end, closed := quoteEnd(text, i)
		if closed && end < len(text) && text[end] == ':' && (end+1 == len(text) || text[end+1] == ' ') {
			t.key, t.keyAt, t.keyEnd, t.colon = string(text[i+1:end-1]), i+1, end-1, end
			t.val, t.comment = readValue(text, end+1)
			return t
		}
		t.val, t.comment = readValue(text, i)
		return t
	}
	if text[i] != '{' && text[i] != '[' {
		for j := i; j < len(text); j++ {
			if j > i && text[j] == '#' && text[j-1] == ' ' {
				break
			}
			if text[j] == ':' && (j+1 == len(text) || text[j+1] == ' ') {
				end := j
				for end > i && text[end-1] == ' ' {
					end--
				}
				t.key, t.keyAt, t.keyEnd, t.colon = string(text[i:end]), i, end, j
				t.val, t.comment = readValue(text, j+1)
				return t
			}
		}
	}
	t.val, t.comment = readValue(text, i)
	return t
}

// readValue reads the value that starts at or after column i of text, after
// spaces, and returns it with the column of a comment after it, -1 for none.
func readValue(text []rune, i int) (vspan, int) {
	for i < len(text) && text[i] == ' ' {
		i++
	}
	if i == len(text) {
		return vspan{start: i, end: i}, -1
	}
	switch c := text[i]; c {
	case '#':
		return vspan{start: i, end: i}, i
	case '"', '\'':
		end, closed := quoteEnd(text, i)
		v := vspan{start: i + 1, end: end, quote: c}
		if closed {
			v.end = end - 1
		}
		return v, commentAfter(text, end)
	case '{', '[':
		return vspan{start: i, end: len(text), flow: true}, -1
	case '|', '>':
		return vspan{start: i, end: i, block: true}, commentAfter(text, i+1)
	}
	end := len(text)
	comment := commentAfter(text, i)
	if comment >= 0 {
		end = comment
	}
	for end > i && text[end-1] == ' ' {
		end--
	}
	return vspan{start: i, end: end}, comment
}

// quoteEnd returns the column after the quote that closes the one at i, and
// whether there is one; the end of the line otherwise.
func quoteEnd(text []rune, i int) (int, bool) {
	q := text[i]
	for j := i + 1; j < len(text); j++ {
		switch {
		case q == '"' && text[j] == '\\':
			j++
		case q == '\'' && text[j] == '\'' && j+1 < len(text) && text[j+1] == '\'':
			j++
		case text[j] == q:
			return j + 1, true
		}
	}
	return len(text), false
}

// commentAfter returns the column of a comment at or after column i, -1 for
// none: a # after a space, or at i.
func commentAfter(text []rune, i int) int {
	for j := i; j < len(text); j++ {
		if text[j] == '#' && (j == i || text[j-1] == ' ') {
			return j
		}
	}
	return -1
}

// chain returns the path from the root to the mapping whose keys start at
// column c of line at, which starts an item of a list when dash, the column
// of its dash, is not -1. It also returns the line that mapping starts at,
// and, for each "-" of the path, the line its item starts at. It is false
// when the lines above do not read as block mappings and lists, such as for
// a line in a value of several lines.
func (r *lineReader) chain(at, c, dash int) ([]string, int, []int, bool) {
	var rev []string
	var revItems []int
	start := at
	inMap := true // still in the mapping at column c
	seq, d := false, 0
	if dash >= 0 {
		rev, revItems = append(rev, "-"), append(revItems, at)
		seq, d, inMap = true, dash, false
	}
	parent := func(t tline) bool {
		return t.colon >= 0 && t.val.start == t.val.end && t.val.quote == 0 && !t.val.block
	}
	for i := at - 1; i >= 0; i-- {
		t := r.lines[i]
		if t.blank {
			continue
		}
		if seq {
			switch {
			case t.dash == d, t.col > d:
				continue // another item, or inside one
			case t.col < d || t.dash < 0 && parent(t):
				if !parent(t) {
					return nil, 0, nil, false
				}
				rev, revItems = append(rev, t.key), append(revItems, -1)
				seq, c = false, t.col
				if t.dash >= 0 {
					rev, revItems = append(rev, "-"), append(revItems, i)
					seq, d = true, t.dash
				}
				continue
			}
			return nil, 0, nil, false
		}
		switch {
		case t.col > c:
			continue // inside the value of a key above
		case t.col == c && t.dash < 0:
			if inMap {
				start = i
			}
			continue
		case t.col == c, t.dash >= 0 && t.dash < c && t.bare():
			// The first line of the item the mapping is: its first key, or
			// its dash alone, with the keys below.
			if inMap && !t.bare() {
				start = i
			}
			inMap = false
			rev, revItems = append(rev, "-"), append(revItems, i)
			seq, d = true, t.dash
			continue
		}
		if !parent(t) {
			return nil, 0, nil, false
		}
		inMap = false
		rev, revItems = append(rev, t.key), append(revItems, -1)
		c = t.col
		if t.dash >= 0 {
			rev, revItems = append(rev, "-"), append(revItems, i)
			seq, d = true, t.dash
		}
	}
	slices.Reverse(rev)
	slices.Reverse(revItems)
	return rev, start, revItems, true
}

// keys returns the keys of the block mapping whose keys start at column c,
// from line start on, with their values as written on one line.
func (r *lineReader) keys(start, c int) map[string]string {
	out := map[string]string{}
	for i := start; i < len(r.lines); i++ {
		t := r.lines[i]
		switch {
		case t.blank || t.col > c:
			continue
		case i > start && (t.col < c || t.dash >= 0):
			return out
		case t.col == c && t.colon >= 0:
			if _, seen := out[t.key]; !seen {
				out[t.key] = t.scalar()
			}
		}
	}
	return out
}

// bare reports whether t has nothing after its dashes but a comment.
func (t tline) bare() bool { return t.col == len(t.text) || t.col == t.comment }

// scalar is the value of t when it is a scalar on its line, without quotes,
// else "".
func (t tline) scalar() string {
	if t.val.flow || t.val.block {
		return ""
	}
	return string(t.text[t.val.start:t.val.end])
}

// entry returns the keys of the entry whose item starts at line item, and
// the line of its first key; s is the spot already read on line at.
func (r *lineReader) entry(item, at int, s Spot) (map[string]string, int) {
	if item < 0 || item >= len(r.lines) {
		return nil, 0
	}
	t := r.lines[item]
	if t.val.flow && t.colon < 0 {
		f := flowReader{text: t.text}
		f.read(t.val.start, -1)
		return f.top, item
	}
	if item == at && len(s.Path) == 2 {
		return s.Keys, item
	}
	if t.col < len(t.text) && t.comment != t.col {
		return r.keys(item, t.col), item
	}
	for i := item + 1; i < len(r.lines); i++ {
		if n := r.lines[i]; !n.blank {
			if n.col <= t.dash || n.dash >= 0 {
				return map[string]string{}, item
			}
			return r.keys(i, n.col), i
		}
	}
	return map[string]string{}, item
}

// inFlow fills s for the position col in the brackets of line t: the path,
// key and word there, and the keys of the mapping it is in.
func (r *lineReader) inFlow(s *Spot, t tline, col int) bool {
	f := flowReader{text: t.text}
	if !f.read(t.val.start, col) {
		return false
	}
	if t.colon >= 0 {
		s.Path = append(s.Path, t.key)
	}
	s.Flow = true
	s.Path = append(s.Path, f.path...)
	s.Key, s.Item, s.Start, s.End, s.Quote, s.Keys, s.Pad = f.key, f.item, f.start, f.end, f.quote, f.keys, f.pad
	switch {
	case f.item && (len(s.Path) == 0 || s.Path[len(s.Path)-1] == "-"):
		return false // a list that is no key's value
	case f.item:
		// The list is the value of the last key of the path.
		s.Key, s.Path = s.Path[len(s.Path)-1], s.Path[:len(s.Path)-1]
		s.KeyAt = s.Key
	case f.key != "":
		s.KeyAt = f.key
	default:
		s.KeyAt = string(t.text[f.start:f.end])
		rest := strings.TrimLeft(string(t.text[min(f.end+1, len(t.text)):]), " ")
		s.Colon = f.end < len(t.text) && (t.text[f.end] == ':' || f.quote != 0 && strings.HasPrefix(rest, ":"))
	}
	return true
}

// flowReader reads the brackets of a line: a flow mapping or list, which may
// not be closed yet.
type flowReader struct {
	text []rune
	// What read found at the position: the keys and "-" from the outer
	// brackets to the innermost ones, the key whose value it is in ("" at a
	// key), whether it is in an item of a list (then the list is the last of
	// path), the word there, its quote, the keys of its mapping, and whether
	// it follows a colon at once.
	path       []string
	key        string
	item       bool
	start, end int
	quote      rune
	keys       map[string]string
	pad        bool
	// top are the keys of the outer mapping.
	top map[string]string
}

// frame is one pair of brackets read so far.
type frame struct {
	list  bool
	name  string // the key it is the value of in its parent, or "-"
	key   string // in a mapping, the key read last
	atKey bool   // in a mapping, before the colon of an entry
	keys  map[string]string
}

// read reads the brackets that start at column from and reports whether the
// position at, -1 for none, is inside them; it fills in what is there.
func (f *flowReader) read(from, at int) bool {
	var stack []*frame
	found := at < 0
	mark := func(start, end int, quote rune) {
		if found {
			return
		}
		found = true
		top := stack[len(stack)-1]
		for _, fr := range stack[1:] {
			f.path = append(f.path, fr.name)
		}
		f.start, f.end, f.quote, f.keys = start, end, quote, top.keys
		f.pad = start > 0 && f.text[start-1] == ':'
		switch {
		case top.list:
			f.item, f.key = true, top.name
		case !top.atKey:
			f.key = top.key
		}
	}
	scalar := func(start, end int, quote rune) {
		top := stack[len(stack)-1]
		value := string(f.text[start:end])
		switch {
		case top.list:
		case top.atKey:
			top.key = value
		default:
			if v, seen := top.keys[top.key]; !seen || v == "" {
				top.keys[top.key] = value
			}
		}
		if !found && at >= start && at <= end {
			mark(start, end, quote)
		}
	}
	i := from
	for i < len(f.text) {
		c := f.text[i]
		// The position is in the gap before what starts at i, unless it is
		// at the start of a word, which then holds it.
		if !found && len(stack) > 0 && c != ' ' && (at < i || at == i && strings.ContainsRune(",[]{}:", c)) {
			mark(at, at, 0)
		}
		switch {
		case c == ' ':
			i++
		case c == '#' && i > from && f.text[i-1] == ' ':
			i = len(f.text)
		case c == '{' || c == '[':
			name := "-"
			if len(stack) > 0 {
				if p := stack[len(stack)-1]; !p.list {
					name = p.key
					p.keys[p.key] = ""
				}
			}
			stack = append(stack, &frame{list: c == '[', name: name, atKey: c == '{', keys: map[string]string{}})
			i++
		case c == '}' || c == ']':
			if len(stack) == 1 {
				f.top = stack[0].keys
				return found
			}
			stack = stack[:len(stack)-1]
			i++
		case len(stack) == 0:
			return false
		case c == ',':
			if top := stack[len(stack)-1]; !top.list {
				top.key, top.atKey = "", true
			}
			i++
		case c == ':':
			top := stack[len(stack)-1]
			if _, seen := top.keys[top.key]; !seen && top.atKey && !top.list {
				top.keys[top.key] = ""
			}
			top.atKey = false
			i++
		case c == '"' || c == '\'':
			end, closed := quoteEnd(f.text, i)
			last := end
			if closed {
				last = end - 1
			}
			scalar(i+1, last, c)
			i = end
		default:
			j := i
			for j < len(f.text) && !strings.ContainsRune(",[]{}", f.text[j]) &&
				(f.text[j] != ':' || j+1 < len(f.text) && !strings.ContainsRune(" ,]}", f.text[j+1])) &&
				(f.text[j] != '#' || f.text[j-1] != ' ') {
				j++
			}
			end := j
			for end > i && f.text[end-1] == ' ' {
				end--
			}
			scalar(i, end, 0)
			i = j
		}
	}
	if !found && len(stack) > 0 && at >= i {
		mark(at, at, 0)
	}
	if len(stack) > 0 {
		f.top = stack[0].keys
	}
	return found
}
