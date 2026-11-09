package cellens

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/internal/crdschema"
)

// Header is the comment that starts a .cellens file cel-lens writes into an
// empty text.
const Header = "# .cellens: types cel-lens assumes where Kubernetes gives dyn. Commit it."

// ErrNoEntry is the error of Remove for a text with no entry for the value.
var ErrNoEntry = errors.New("no entry for the value")

// Edit is a change to the text of a .cellens file: the bytes from Start to
// End, offsets from 0, become Text.
type Edit struct {
	Start, End int
	Text       string
}

// Apply returns a copy of text with the edit made.
func (ed Edit) Apply(text []byte) []byte {
	out := make([]byte, 0, len(text)-(ed.End-ed.Start)+len(ed.Text))
	out = append(out, text[:ed.Start]...)
	out = append(out, ed.Text...)
	return append(out, text[ed.End:]...)
}

// Has reports whether text, the text of a .cellens file, has an entry for the
// value e names (its file, document, version or variable, and path), even an
// entry with a problem elsewhere, such as a type that does not parse.
func Has(text []byte, e Entry) bool {
	l, err := readLayout(text)
	return err == nil && l.find(e) >= 0
}

// Set returns the edit that gives the value e names the type of e (its type,
// kind, schema or Go type) in text, the text of a .cellens file. When text has
// an entry for the value, only the key type, kind, schema or go of that entry
// changes, and its value: a schema is written as a block of YAML below its
// key, in the order of its keys, quoting only what YAML would read otherwise. Otherwise e is added as a new
// entry before the first that sorts after it, by file, document, version or
// variable, and path, or after the last one, in the indentation the entries
// have; an empty text starts with Header. Every other line stays as it is, its
// comments too. The error says why the text cannot be edited in place: it is
// not YAML, or types is no block list.
func Set(text []byte, e Entry) (Edit, error) {
	l, err := readLayout(text)
	if err != nil {
		return Edit{}, err
	}
	var ed Edit
	if i := l.find(e); i >= 0 {
		ed, err = l.replaceType(l.items[i], e)
	} else {
		ed, err = l.insert(e)
	}
	if err != nil {
		return Edit{}, err
	}
	return ed, l.check(ed, e, false)
}

// Remove returns the edit that removes the entry for the value e names from
// text, the text of a .cellens file, with the comment lines directly above it,
// the comments inside it and directly after it, and the blank lines that
// would be left twice; a comment after a blank line stays. Every other line stays as it is. The error
// is ErrNoEntry when text has no entry for the value, and otherwise says why
// the text cannot be edited in place.
func Remove(text []byte, e Entry) (Edit, error) {
	l, err := readLayout(text)
	if err != nil {
		return Edit{}, err
	}
	i := l.find(e)
	if i < 0 {
		return Edit{}, ErrNoEntry
	}
	start, end := l.items[i].head, l.items[i].end
	if i+1 < len(l.items) {
		for end < l.items[i+1].head && l.blank(end) {
			end++
		}
	} else if i > 0 {
		for start > l.items[i-1].end && l.blank(start-1) {
			start--
		}
	}
	ed := Edit{Start: l.lineStart(start), End: l.lineStart(end)}
	return ed, l.check(ed, e, true)
}

// TypeAt returns the offset in text, the text of a .cellens file, where the
// type of the entry for the value e names starts: the value of its type, kind
// or go, inside its quotes, or the first key of its schema. It is false when
// text is no YAML, has no entry for the value, or the entry has no type.
func TypeAt(text []byte, e Entry) (int, bool) {
	l, err := readLayout(text)
	if err != nil {
		return 0, false
	}
	i := l.find(e)
	if i < 0 {
		return 0, false
	}
	n := l.items[i].node
	for j := 0; j+1 < len(n.Content); j += 2 {
		v := n.Content[j+1]
		if !slices.Contains(typeKeys, n.Content[j].Value) {
			continue
		}
		if v.Kind == yaml.MappingNode && len(v.Content) > 0 {
			v = v.Content[0]
		}
		off := l.offset(v.Line, v.Column)
		if v.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
			off++
		}
		return off, true
	}
	return 0, false
}

// Bare reports whether text holds nothing but Header, the key types without
// entries and blank lines: nothing a person wrote.
func Bare(text []byte) bool {
	for line := range strings.FieldsFuncSeq(string(text), func(r rune) bool { return r == '\n' || r == '\r' }) {
		switch strings.TrimSpace(line) {
		case "", Header, "types:", "types: []":
		default:
			return false
		}
	}
	return true
}

// layout is where the parts of a .cellens text are, to edit it in place.
// Lines and columns count from 0 here; yaml.Node counts from 1.
type layout struct {
	text   []byte
	starts []int  // the offset each line starts at
	eol    string // the line ending the text uses
	// root is the root mapping, nil for a text without one; key and list are
	// the key types and its value, nil when it has none.
	root, key, list *yaml.Node
	// dash is the column of the dashes of a block list; items are its entries.
	dash  int
	items []span
}

// span is where one entry of the list is, by line.
type span struct {
	node *yaml.Node
	// head is its first line, with the comments directly above it; first is
	// the line of its dash; end is the line after its last one, before the
	// blank lines that follow it.
	head, first, end int
}

// errLayout is why Set or Remove refuse a text laid out in a way their edit
// does not fit, such as a list after a tag on a line of its own.
var errLayout = errors.New("cel-lens cannot edit this .cellens in place, as it is laid out: write it like the example of docs/cellens.md")

// check returns nil when the text ed makes of l's reads as l's does but for
// the entry for e's value: the same other root keys, types still a list of
// entries on their own lines (or empty, once remove took its last entry),
// and the other entries with the same content. With remove, one entry for e's
// value is gone; else the entry for it has e's type or kind and, when l had
// one, its other keys as they were. Otherwise it returns errLayout.
func (l *layout) check(ed Edit, e Entry, remove bool) error {
	after, err := readLayout(ed.Apply(l.text))
	if err != nil || !slices.Equal(l.otherKeys(), after.otherKeys()) {
		return errLayout
	}
	own := valueOfEntry(e)
	split := func(l *layout) (others, mine []string) {
		for _, it := range l.items {
			if v, _ := valueOfNode(it.node); v == own {
				mine = append(mine, content(it.node, true))
			} else {
				others = append(others, content(it.node, false))
			}
		}
		slices.Sort(others)
		return others, mine
	}
	others, mine := split(l)
	othersAfter, mineAfter := split(after)
	switch list := after.list; {
	case !slices.Equal(others, othersAfter), list == nil:
		return errLayout
	case remove && len(mineAfter) == 0 && len(others) == 0 && list.Kind == yaml.ScalarNode && list.Tag == "!!null" && list.Value == "":
		return nil // the last entry is gone
	case list.Kind != yaml.SequenceNode || list.Style&yaml.FlowStyle != 0:
		return errLayout
	case remove:
		if len(mineAfter) != len(mine)-1 {
			return errLayout
		}
		return nil
	}
	i := after.find(e)
	switch {
	case i < 0 || typeKey(e) != typeKey(entryType(after.items[i].node)):
		return errLayout
	case len(mine) == 0 && len(mineAfter) != 1, len(mine) > 0 && !slices.Equal(mine, mineAfter):
		return errLayout
	}
	return nil
}

// otherKeys are the keys of the root other than types, in their order.
func (l *layout) otherKeys() []string {
	if l.root == nil {
		return nil
	}
	var out []string
	for i := 0; i < len(l.root.Content); i += 2 {
		if k := l.root.Content[i].Value; k != "types" {
			out = append(out, k)
		}
	}
	return out
}

// content is what the entry n holds, as YAML reads it, without its type,
// kind, schema and go when bare is true.
func content(n *yaml.Node, bare bool) string {
	var v any
	if err := n.Decode(&v); err != nil {
		return "not read: " + err.Error()
	}
	if m, ok := v.(map[string]any); ok && bare {
		for _, k := range typeKeys {
			delete(m, k)
		}
	}
	return fmt.Sprintf("%#v", v)
}

// entryType returns an Entry with the type, kind, schema or Go type the entry
// n has, as written, or none.
func entryType(n *yaml.Node) Entry {
	for i := 0; i+1 < len(n.Content); i += 2 {
		switch k, v := n.Content[i].Value, n.Content[i+1]; {
		case k == "type" && v.Kind == yaml.ScalarNode:
			if t, err := ParseType(v.Value); err == nil {
				return Entry{Type: t}
			}
		case k == "kind" && v.Kind == yaml.ScalarNode:
			if kind, err := ParseKind(v.Value); err == nil {
				return Entry{Kind: kind}
			}
		case k == "go" && v.Kind == yaml.ScalarNode:
			if g, err := ParseGo(v.Value); err == nil {
				return Entry{Go: g}
			}
		case k == "schema" && v.Kind == yaml.MappingNode:
			if value, problems := crdschema.Read(v); len(problems) == 0 {
				return Entry{Schema: &Schema{Value: value}}
			}
		}
	}
	return Entry{Kind: &KindRef{}}
}

// typeKey is the type of e, to compare: the line typeText writes, with a
// schema as its canonical JSON.
func typeKey(e Entry) string {
	if e.Schema == nil {
		return typeText(e, 0, "\n")
	}
	js, err := json.Marshal(e.Schema.Value)
	if err != nil {
		return "schema not read: " + err.Error()
	}
	return "schema: " + string(js)
}

// readLayout finds the parts of text.
func readLayout(text []byte) (*layout, error) {
	l := &layout{text: text, eol: "\n", starts: []int{0}}
	if bytes.Contains(text, []byte("\r\n")) {
		l.eol = "\r\n"
	}
	// YAML ends a line at a line feed, a carriage return, or both.
	for i, b := range text {
		if b == '\n' || b == '\r' && (i+1 == len(text) || text[i+1] != '\n') {
			l.starts = append(l.starts, i+1)
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(text))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return l, nil
		}
		return nil, fmt.Errorf("%s: fix it first", notYAML(err))
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("the .cellens holds more than one YAML document: fix it first")
	}
	if len(doc.Content) == 0 {
		return l, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || root.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("%d:%d: the .cellens is no mapping of lines with the key types: fix it first", root.Line, root.Column)
	}
	l.root = root
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "types" {
			l.key, l.list = root.Content[i], root.Content[i+1]
			break
		}
	}
	if l.list == nil || l.list.Kind != yaml.SequenceNode || l.list.Style&yaml.FlowStyle != 0 {
		return l, nil
	}
	err := l.readItems()
	return l, err
}

// readItems finds where the entries of the block list are.
func (l *layout) readItems() error {
	floor := l.key.Line - 1 // the line before the first one an entry can take
	// The list starts at its first dash, unless a tag or an anchor comes
	// first: only spaces come before a block list's dash.
	if n := l.list.Content[0]; n.Line > l.list.Line {
		l.dash = -1
		for i := n.Line - 1; i > floor && l.dash < 0; i-- {
			if s := strings.TrimLeft(l.line(i), " "); s == "-" || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "-\t") {
				l.dash = len(l.line(i)) - len(s)
			}
		}
	} else {
		l.dash = l.list.Column - 1
	}
	if l.dash < 0 {
		return fmt.Errorf("%d:%d: cel-lens cannot find the dash of the first entry: write - before its first key", l.list.Line, l.list.Column)
	}
	for _, n := range l.list.Content {
		first := n.Line - 1
		for first > floor && !l.dashAt(first) {
			first--
		}
		if first <= floor {
			return fmt.Errorf("%d:%d: cel-lens cannot find the dash of this entry: write - before its first key", n.Line, n.Column)
		}
		l.items = append(l.items, span{node: n, first: first})
		floor = first
	}
	floor = l.key.Line - 1
	for i := range l.items {
		it := &l.items[i]
		limit := len(l.starts)
		if i+1 < len(l.items) {
			limit = l.items[i+1].first
		}
		it.end = l.entryEnd(it, limit)
		// The comments right above the dash are the entry's.
		it.head = it.first
		for it.head-1 > floor && l.comment(it.head-1) {
			it.head--
		}
		floor = it.end - 1
	}
	return nil
}

// entryEnd returns the line after the last line of the entry it, before the
// line limit: the last line a node of the entry starts at, the lines of a
// block scalar it ends with, and then the lines right after it indented more
// than the dash, such as a comment. A blank line ends the entry: a comment
// after it is not the entry's.
func (l *layout) entryEnd(it *span, limit int) int {
	last := max(it.first, maxLine(it.node)-1)
	j := last + 1
	if val := lastNode(it.node); val.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		// Its lines are indented as its first one, more than the line of
		// its | or >, and may be blank.
		for j < limit && l.blank(j) {
			j++
		}
		if in := l.indent(j); j < limit && in > l.indent(val.Line-1) {
			for ; j < limit && (l.blank(j) || l.indent(j) >= in); j++ {
				if !l.blank(j) {
					last = j
				}
			}
		}
		j = last + 1
	}
	for ; j < limit && !l.blank(j) && l.indent(j) > l.dash; j++ {
		last = j
	}
	return last + 1
}

// lastNode returns the last node in n, at any depth: the last value of a
// mapping or item of a list, down to a scalar or an empty collection.
func lastNode(n *yaml.Node) *yaml.Node {
	for len(n.Content) > 0 {
		n = n.Content[len(n.Content)-1]
	}
	return n
}

// maxLine is the greatest line any node in n starts at.
func maxLine(n *yaml.Node) int {
	m := n.Line
	for _, c := range n.Content {
		m = max(m, maxLine(c))
	}
	return m
}

// line is the text of line i without its line ending, "" past the end.
func (l *layout) line(i int) string {
	if i < 0 || i >= len(l.starts) {
		return ""
	}
	end := len(l.text)
	if i+1 < len(l.starts) {
		end = l.starts[i+1]
	}
	return strings.TrimRight(string(l.text[l.starts[i]:end]), "\r\n")
}

// lineStart is the offset line i starts at, the end of the text past it.
func (l *layout) lineStart(i int) int {
	if i < len(l.starts) {
		return l.starts[i]
	}
	return len(l.text)
}

func (l *layout) blank(i int) bool { return strings.TrimSpace(l.line(i)) == "" }

func (l *layout) comment(i int) bool { return strings.HasPrefix(strings.TrimSpace(l.line(i)), "#") }

// indent is how many spaces and tabs line i starts with.
func (l *layout) indent(i int) int {
	s := l.line(i)
	return len(s) - len(strings.TrimLeft(s, " \t"))
}

// dashAt reports whether line i has a list's dash at the list's column.
func (l *layout) dashAt(i int) bool {
	s := l.line(i)
	return len(s) > l.dash && s[l.dash] == '-' && strings.TrimSpace(s[:l.dash]) == "" &&
		(len(s) == l.dash+1 || s[l.dash+1] == ' ' || s[l.dash+1] == '\t')
}

// offset is the offset of the character at line and column, from 1, as
// yaml.Node counts them.
func (l *layout) offset(line, column int) int {
	s := l.line(line - 1)
	return l.lineStart(line-1) + byteColumn(s, column-1)
}

// byteColumn is the byte offset in s of its character at col, from 0.
func byteColumn(s string, col int) int {
	off := 0
	for range col {
		if off >= len(s) {
			return off
		}
		_, size := utf8.DecodeRuneInString(s[off:])
		off += size
	}
	return off
}

// value identifies the value an entry names, in the order entries sort.
type value struct{ file, kind, name, version, variable, path string }

func (v value) compare(w value) int {
	return cmp.Or(cmp.Compare(v.file, w.file), cmp.Compare(v.kind, w.kind), cmp.Compare(v.name, w.name),
		cmp.Compare(v.version, w.version), cmp.Compare(v.variable, w.variable), cmp.Compare(v.path, w.path))
}

func valueOfEntry(e Entry) value {
	return value{e.File, e.Document.Kind, e.Document.Name, e.Version, e.Variable, e.Path.String()}
}

// valueOfNode reads the value the entry n names as written, and reports
// whether it names one: it has a file and a document.
func valueOfNode(n *yaml.Node) (value, bool) {
	if n.Kind != yaml.MappingNode {
		return value{}, false
	}
	fields := map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i].Value, n.Content[i+1]
		if _, seen := fields[k]; !seen && v.Kind == yaml.ScalarNode {
			fields[k] = strings.TrimSpace(v.Value)
		}
	}
	if fields["file"] == "" || fields["document"] == "" {
		return value{}, false
	}
	v := value{file: path.Clean(fields["file"]), version: fields["version"], variable: fields["variable"], path: fields["path"]}
	words := strings.Fields(fields["document"])
	v.kind = strings.Join(words, " ")
	if len(words) == 2 {
		v.kind, v.name = words[0], words[1]
	}
	if p, err := ParsePath(v.path); err == nil {
		v.path = p.String()
	}
	return v, true
}

// find returns the index of the first entry for the value e names, or -1.
func (l *layout) find(e Entry) int {
	want := valueOfEntry(e)
	for i, it := range l.items {
		if v, ok := valueOfNode(it.node); ok && v == want {
			return i
		}
	}
	return -1
}

// insert returns the edit that adds e as a new entry.
func (l *layout) insert(e Entry) (Edit, error) {
	if l.root == nil {
		if len(bytes.TrimSpace(l.text)) == 0 {
			return Edit{Start: 0, End: len(l.text), Text: Header + l.eol + "types:" + l.eol + entryText(e, 2, 4, l.eol)}, nil
		}
		return l.append("types:" + l.eol + entryText(e, 2, 4, l.eol)), nil
	}
	indent := 0 // of the root's keys; a tag or anchor may come before the first
	if len(l.root.Content) > 0 {
		indent = l.root.Content[0].Column - 1
	}
	if l.key == nil {
		return l.append(spaces(indent) + "types:" + l.eol + entryText(e, indent+2, indent+4, l.eol)), nil
	}
	switch {
	case l.list.Kind == yaml.SequenceNode && l.list.Style&yaml.FlowStyle == 0:
		return l.insertItem(e), nil
	case l.list.Kind == yaml.ScalarNode && l.list.Tag == "!!null" && l.list.Value == "":
		return l.insertAt(l.key.Line, entryText(e, indent+2, indent+4, l.eol)), nil
	case l.list.Line == l.key.Line && (l.list.Kind == yaml.ScalarNode && l.list.Tag == "!!null" ||
		l.list.Kind == yaml.SequenceNode && len(l.list.Content) == 0):
		return l.replaceEmpty(entryText(e, indent+2, indent+4, l.eol))
	}
	return Edit{}, fmt.Errorf("%d:%d: types is no list of entries on their own lines, which cel-lens edits: write each entry as - file: <path>, with its other keys below", l.list.Line, l.list.Column)
}

// insertItem returns the edit that adds e to the block list, in order.
func (l *layout) insertItem(e Entry) Edit {
	key := l.dash + 2
	for _, it := range l.items {
		if it.node.Kind == yaml.MappingNode && it.node.Style&yaml.FlowStyle == 0 {
			key = max(it.node.Column-1, l.dash+2)
			break
		}
	}
	text := entryText(e, l.dash, key, l.eol)
	want := valueOfEntry(e)
	for _, it := range l.items {
		if v, ok := valueOfNode(it.node); ok && v.compare(want) > 0 {
			at := l.lineStart(it.head)
			return Edit{Start: at, End: at, Text: text}
		}
	}
	return l.insertAt(l.items[len(l.items)-1].end, text)
}

// insertAt returns the edit that puts text, whole lines, at the start of line
// i, ending the line before it when the text does not.
func (l *layout) insertAt(i int, text string) Edit {
	at := l.lineStart(i)
	if at == len(l.text) && len(l.text) > 0 && !strings.ContainsRune("\r\n", rune(l.text[len(l.text)-1])) {
		text = l.eol + text
	}
	return Edit{Start: at, End: at, Text: text}
}

// append returns the edit that adds text, whole lines, at the end.
func (l *layout) append(text string) Edit { return l.insertAt(len(l.starts), text) }

// replaceEmpty returns the edit that puts the lines text in place of an empty
// value of types on its line, such as ~ or [], keeping a comment after it.
func (l *layout) replaceEmpty(text string) (Edit, error) {
	s := l.line(l.list.Line - 1)
	col := byteColumn(s, l.list.Column-1)
	end, ok := tokenEnd(s, col, l.list)
	if !ok {
		return Edit{}, fmt.Errorf("%d:%d: cel-lens cannot tell where the value of types ends: write it as types: with the entries below", l.list.Line, l.list.Column)
	}
	lineAt := l.lineStart(l.list.Line - 1)
	lines := strings.TrimSuffix(text, l.eol)
	if rest := strings.TrimSpace(s[end:]); rest != "" {
		return Edit{Start: lineAt + col, End: lineAt + len(s), Text: rest + l.eol + lines}, nil
	}
	colon := strings.LastIndexByte(s[:col], ':') + 1
	return Edit{Start: lineAt + colon, End: lineAt + len(s), Text: l.eol + lines}, nil
}

// replaceType returns the edit that gives the entry it the type or kind of e.
func (l *layout) replaceType(it span, e Entry) (Edit, error) {
	n := it.node
	if n.Kind != yaml.MappingNode || n.Style&yaml.FlowStyle != 0 {
		return Edit{}, fmt.Errorf("%d:%d: the entry is written on one line in {...}, which cel-lens does not edit: write one key per line", n.Line, n.Column)
	}
	var key, val *yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i]; slices.Contains(typeKeys, k.Value) {
			key, val = k, n.Content[i+1]
			break
		}
	}
	if key == nil {
		return l.insertAt(it.end, spaces(n.Column-1)+typeText(e, n.Column-1, l.eol)+l.eol), nil
	}
	ed := Edit{Start: l.offset(key.Line, key.Column), End: l.valueEnd(key, val, it), Text: typeText(e, key.Column-1, l.eol)}
	// A comment after a value on the key's line stays on that line, after the
	// key, when the new value takes lines of its own below it.
	first, below, multi := strings.Cut(ed.Text, l.eol)
	lineEnd := l.lineStart(key.Line-1) + len(l.line(key.Line-1))
	if rest := string(l.text[min(ed.End, lineEnd):lineEnd]); multi && ed.End <= lineEnd && strings.TrimSpace(rest) != "" {
		ed.Text, ed.End = first+rest+l.eol+below, lineEnd
	}
	return ed, nil
}

// valueEnd returns the offset where val, the value of key in the entry it,
// ends.
func (l *layout) valueEnd(key, val *yaml.Node, it span) int {
	s := l.line(key.Line - 1)
	lineAt := l.lineStart(key.Line - 1)
	if val.Kind == yaml.ScalarNode && val.Tag == "!!null" && val.Value == "" && val.Line == key.Line {
		colon := strings.IndexByte(s[byteColumn(s, key.Column-1):], ':')
		if colon >= 0 {
			return lineAt + byteColumn(s, key.Column-1) + colon + 1
		}
	}
	block := val.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0
	if val.Line == key.Line && !block {
		if end, ok := tokenEnd(s, byteColumn(s, val.Column-1), val); ok {
			return lineAt + end
		}
	}
	// The value goes on over the lines below the key that are indented more:
	// a comment among them is the value's in a block scalar, and in a block
	// mapping or list up to the line its last node starts at.
	last := key.Line - 1
	inside := maxLine(val) - 1
	if val.Kind != yaml.MappingNode && val.Kind != yaml.SequenceNode {
		inside = -1
	}
	for j := key.Line; j < it.end && (l.blank(j) || l.indent(j) >= key.Column && (block || j <= inside || !l.comment(j))); j++ {
		if !l.blank(j) {
			last = j
		}
	}
	return l.lineStart(last) + len(l.line(last))
}

// tokenEnd returns the offset in s where the value n, which starts at byte
// col, ends, and false when it does not end on the line: a quoted scalar at
// its closing quote, a collection in brackets at its closing bracket, a plain
// scalar before a comment.
func tokenEnd(s string, col int, n *yaml.Node) (int, bool) {
	switch {
	case n.Kind == yaml.ScalarNode && n.Style&yaml.DoubleQuotedStyle != 0:
		return quotedEnd(s, col, '"')
	case n.Kind == yaml.ScalarNode && n.Style&yaml.SingleQuotedStyle != 0:
		return quotedEnd(s, col, '\'')
	case n.Kind == yaml.ScalarNode:
		rest := s[col:]
		if i := strings.Index(rest, " #"); i >= 0 {
			rest = rest[:i]
		}
		rest = strings.TrimRight(rest, " \t")
		return col + len(rest), rest == n.Value
	case n.Style&yaml.FlowStyle != 0:
		return flowEnd(s, col)
	}
	return 0, false
}

// quotedEnd returns the offset after the closing quote of the scalar quoted
// with q that starts at col.
func quotedEnd(s string, col int, q byte) (int, bool) {
	for i := col + 1; i < len(s); i++ {
		switch {
		case q == '"' && s[i] == '\\':
			i++
		case s[i] == q && q == '\'' && i+1 < len(s) && s[i+1] == '\'':
			i++
		case s[i] == q:
			return i + 1, true
		}
	}
	return 0, false
}

// flowEnd returns the offset after the bracket that closes the one at col.
func flowEnd(s string, col int) (int, bool) {
	depth := 0
	for i := col; i < len(s); i++ {
		switch c := s[i]; {
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		case (c == '"' || c == '\'') && i > col && strings.ContainsRune("[{,: ", rune(s[i-1])):
			end, ok := quotedEnd(s, i, c)
			if !ok {
				return 0, false
			}
			i = end - 1
		}
	}
	return 0, false
}

// entryText writes e as an entry with its dash at column dash and its keys at
// column key, from 0.
func entryText(e Entry, dash, key int, eol string) string {
	key = max(key, dash+2)
	lines := []string{"file: " + Scalar(e.File), "document: " + Scalar(e.Document.Kind+" "+e.Document.Name)}
	if e.Version != "" {
		lines = append(lines, "version: "+Scalar(e.Version))
	}
	if e.Variable != "" {
		lines = append(lines, "variable: "+Scalar(e.Variable))
	}
	if len(e.Path) > 0 {
		lines = append(lines, "path: "+Scalar(e.Path.String()))
	}
	lines = append(lines, typeText(e, key, eol))
	var b strings.Builder
	for i, line := range lines {
		if i == 0 {
			b.WriteString(spaces(dash) + "-" + spaces(key-dash-1))
		} else {
			b.WriteString(spaces(key))
		}
		b.WriteString(line + eol)
	}
	return b.String()
}

// typeText writes the type of e as its key and value, for a key at column
// indent, from 0: one line, without its indent, but for a schema, whose lines
// below the key are indented two more than it, and end with eol but the last.
func typeText(e Entry, indent int, eol string) string {
	switch {
	case e.Type != nil:
		return "type: " + Scalar(e.Type.String())
	case e.Go != nil:
		return "go: " + Scalar(e.Go.String())
	case e.Schema != nil:
		lines := schemaLines(e.Schema)
		for i, line := range lines {
			lines[i] = spaces(indent+2) + line
		}
		return strings.Join(append([]string{"schema:"}, lines...), eol)
	}
	return "kind: " + Scalar(e.Kind.APIVersion+" "+e.Kind.Kind)
}

// schemaLines writes the schema s as a block of YAML, one line each, without
// indent: its keys in the order written, or by name for a schema without
// its YAML, and each value plain when YAML reads it back as it is.
func schemaLines(s *Schema) []string {
	n := s.node
	if n == nil {
		n = new(yaml.Node)
		if err := n.Encode(s.Value); err != nil {
			return []string{"# not written: " + err.Error()}
		}
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(blockStyle(n)); err != nil {
		return []string{"# not written: " + err.Error()}
	}
	return strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
}

// blockStyle returns a copy of n, a schema crdschema.Read took, so without
// aliases, written in block style, without quotes or comments, so the encoder
// quotes only what it must.
func blockStyle(n *yaml.Node) *yaml.Node {
	c := *n
	c.Style &^= yaml.FlowStyle | yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle | yaml.TaggedStyle
	c.HeadComment, c.LineComment, c.FootComment = "", "", ""
	c.Anchor = ""
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = blockStyle(ch)
	}
	return &c
}

func spaces(n int) string { return strings.Repeat(" ", max(n, 0)) }

// plainWords are the values written without quotes: words of letters, digits
// and _$/.()[]*-, and # inside a word, as in ./api/v1#QuotaSpec, separated by
// single spaces. Every type with a struct or a comma in it is quoted, so a
// person reads it as written.
var plainWords = regexp.MustCompile(`^[A-Za-z0-9_$/.()\[\]*-][A-Za-z0-9_$/.()\[\]*#-]*( [A-Za-z0-9_$/.()\[\]*-][A-Za-z0-9_$/.()\[\]*#-]*)*$`)

// Scalar writes s as a .cellens writes a value in block style: plain when it
// is a word, or words, of letters, digits and _$/.()[]*#- that YAML reads
// back as that string, double-quoted otherwise.
func Scalar(s string) string {
	if plainWords.MatchString(s) {
		var m map[string]any
		if yaml.Unmarshal([]byte("k: "+s), &m) == nil && m["k"] == s {
			return s
		}
	}
	return strconv.Quote(s)
}
