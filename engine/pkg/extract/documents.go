package extract

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SyntaxError is a YAML document that does not parse.
type SyntaxError struct {
	// File is the file name passed to Documents.
	File string
	// Line is the line, from 1, where parsing fails. The parser reports no
	// column.
	Line int
	// Reason is the parser's message, such as "mapping values are not
	// allowed in this context".
	Reason string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("parse %s: line %d: %s", e.File, e.Line, e.Reason)
}

// Document is one parsed YAML document of a file.
type Document struct {
	// Index is the position of the document in the file, from 0. A document
	// that does not parse still has an index, so the others keep theirs.
	Index int
	// Node is the document, with positions counted from the start of the file.
	Node *yaml.Node
}

// Documents parses data, a YAML file with any number of documents. A document
// that does not parse is reported in the syntax errors, and parsing goes on at
// the next "---" line, so the documents after a broken one are still returned.
// The error is only ctx's error, when ctx is cancelled; the documents and
// syntax errors found so far are returned with it. A well-formed UTF-16 stream
// with a byte order mark is read as UTF-8 (see UTF8).
func Documents(ctx context.Context, file string, data []byte) ([]Document, []*SyntaxError, error) {
	src := newSource(data)
	data = src.data
	var (
		docs   []Document
		broken []*SyntaxError
		seps   = src.separators()
		offset int // where in data the current parse started
		shift  int // the lines before offset
		index  int
		end    = len(data) // where the current parse stops
	)
	for {
		dec := yaml.NewDecoder(bytes.NewReader(src.part(offset, end)))
		startDocs, startIndex := len(docs), index
		for {
			if err := ctx.Err(); err != nil {
				return docs, broken, err
			}
			var node yaml.Node
			err := dec.Decode(&node)
			if errors.Is(err, io.EOF) {
				if end == len(data) {
					return docs, broken, nil
				}
				// The part before the cut parsed; go on at the cut.
				next, _ := nextSeparator(seps, src.linesBefore(end)+1)
				offset, shift, end = next.offset, next.line-1, len(data)
				break
			}
			if err == nil {
				if shift > 0 {
					shiftLines(&node, shift)
				}
				docs = append(docs, Document{Index: index, Node: &node})
				index++
				continue
			}
			se := syntaxError(file, err, src, offset, end, shift)
			// The parser reads ahead, so a bad character in a later document
			// fails this one, with a line that does not tell where. Parse again
			// up to the last "---" that still parses, to keep the documents
			// before the bad one.
			if cut, ok := lastParsing(src, seps, offset, end, shift); ok {
				docs, index, end = docs[:startDocs], startIndex, cut.offset
				break
			}
			// The same read-ahead hides the syntax error of a broken first
			// document behind the bad character of a later one. Parse the
			// first document alone, up to the next "---", to report its own.
			if cut, ok := firstBroken(src, seps, offset, end, shift, se); ok {
				docs, index, end = docs[:startDocs], startIndex, cut.offset
				break
			}
			broken = append(broken, se)
			index++
			next, ok := nextSeparator(seps, max(se.Line, shift+2))
			if !ok {
				return docs, broken, nil
			}
			offset, shift, end = next.offset, next.line-1, len(data)
			break
		}
	}
}

// source is the data of a file and how it is encoded. A well-formed UTF-16
// stream is converted to UTF-8. A malformed one is kept as it is, for the
// parser to report, and its lines are found in its UTF-16 units.
type source struct {
	data  []byte
	order binary.ByteOrder // of a UTF-16 stream kept as it is, else nil
}

// newSource prepares data, a file, for parsing in parts.
func newSource(data []byte) source {
	text := UTF8(data)
	if len(text) == len(data) && len(data) >= 2 {
		switch {
		case data[0] == 0xff && data[1] == 0xfe:
			return source{data, binary.LittleEndian}
		case data[0] == 0xfe && data[1] == 0xff:
			return source{data, binary.BigEndian}
		}
	}
	return source{data: text}
}

// part returns data[offset:end] as a stream the parser reads: a part of a
// UTF-16 stream starts with the byte order mark.
func (s source) part(offset, end int) []byte {
	if s.order == nil || offset == 0 {
		return s.data[offset:end]
	}
	return append(slices.Clip(s.data[:2]), s.data[offset:end]...)
}

// unit returns the code unit at byte offset i, and its width in bytes.
func (s source) unit(i int) (rune, int) {
	if s.order == nil {
		return rune(s.data[i]), 1
	}
	return rune(s.order.Uint16(s.data[i:])), 2
}

// start is the offset of the first line, after the byte order mark.
func (s source) start() int {
	if s.order == nil {
		return 0
	}
	return 2
}

// size is the length of the whole units of data.
func (s source) size() int {
	if s.order == nil {
		return len(s.data)
	}
	return len(s.data) &^ 1
}

// lineBreak returns the width in bytes of the line break at byte offset i, or 0
// when there is none. It reads no further than limit. The breaks are the ones
// the YAML reader of yaml.v3 counts: a line feed, a carriage return, both
// together, and the next line (U+0085), line separator (U+2028) and paragraph
// separator (U+2029) characters.
func (s source) lineBreak(i, limit int) int {
	if s.order == nil {
		d := s.data
		switch {
		case d[i] == '\n':
			return 1
		case d[i] == '\r':
			if i+1 < limit && d[i+1] == '\n' {
				return 2
			}
			return 1
		case d[i] == 0xc2 && i+1 < limit && d[i+1] == 0x85,
			d[i] == 0xe2 && i+2 < limit && d[i+1] == 0x80 && (d[i+2] == 0xa8 || d[i+2] == 0xa9):
			if d[i] == 0xc2 {
				return 2
			}
			return 3
		}
		return 0
	}
	u, w := s.unit(i)
	switch u {
	case '\n', 0x85, 0x2028, 0x2029:
		return w
	case '\r':
		if i+w < limit {
			if next, _ := s.unit(i + w); next == '\n' {
				return 2 * w
			}
		}
		return w
	}
	return 0
}

// linesBefore returns the number of line ends of data before offset, counted
// as separators does: see lineBreak.
func (s source) linesBefore(offset int) int {
	n := 0
	for i := s.start(); i < offset; {
		if w := s.lineBreak(i, offset); w > 0 {
			n++
			i += w
			continue
		}
		_, w := s.unit(i)
		i += w
	}
	return n
}

// separators finds the lines of data that start with a document marker. A line
// ends at a line break, see lineBreak.
func (s source) separators() []separator {
	var seps []separator
	end := s.size()
	line, offset := 1, s.start()
	for offset <= end {
		var text []rune // the first four units of the line
		next, brk := offset, 0
		for next < end {
			if brk = s.lineBreak(next, end); brk > 0 {
				break
			}
			u, w := s.unit(next)
			if len(text) < 4 {
				text = append(text, u)
			}
			next += w
		}
		if len(text) >= 3 && text[0] == '-' && text[1] == '-' && text[2] == '-' &&
			(len(text) == 3 || text[3] == ' ' || text[3] == '\t') {
			seps = append(seps, separator{line, offset})
		}
		if brk == 0 {
			break
		}
		line, offset = line+1, next+brk
	}
	return seps
}

// separator is a line that starts a document with "---".
type separator struct {
	line   int // from 1
	offset int // of the line in the file, in bytes
}

// nextSeparator returns the first separator at or after line.
func nextSeparator(seps []separator, line int) (separator, bool) {
	for _, s := range seps {
		if s.line >= line {
			return s, true
		}
	}
	return separator{}, false
}

// lastParsing returns the last separator of seps in data[offset:end], past the
// first line shift+1, up to which data[offset:] parses. It reports false when
// there is none, so the first document is the broken one.
func lastParsing(src source, seps []separator, offset, end, shift int) (separator, bool) {
	var inside []separator
	for _, s := range seps {
		if s.line > shift+1 && s.offset > offset && s.offset < end {
			inside = append(inside, s)
		}
	}
	i := sort.Search(len(inside), func(i int) bool { return !parses(src.part(offset, inside[i].offset)) })
	if i == 0 {
		return separator{}, false
	}
	return inside[i-1], true
}

// firstBroken returns the first separator of seps in data[offset:end], past the
// first line shift+1, when se is a reader error, a bad character the parser
// found by reading ahead, and the part of data[offset:] before the separator
// fails with an error of its own. The first document is then broken apart from
// the bad character.
func firstBroken(src source, seps []separator, offset, end, shift int, se *SyntaxError) (separator, bool) {
	if !IsBadCharacter(se.Reason) {
		return separator{}, false
	}
	for _, s := range seps {
		if s.line <= shift+1 || s.offset <= offset || s.offset >= end {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(src.part(offset, s.offset)))
		for {
			var node yaml.Node
			err := dec.Decode(&node)
			if errors.Is(err, io.EOF) {
				return separator{}, false
			}
			if err != nil {
				m := problemPattern.FindStringSubmatch(err.Error())
				return s, !IsBadCharacter(m[2])
			}
		}
	}
	return separator{}, false
}

// IsBadCharacter reports whether reason, the Reason of a SyntaxError, is one of
// those yaml.v3's reader gives for a character or byte a YAML stream may not
// hold, rather than about its structure. The reader reports them on the first
// line of the document, wherever the character is.
func IsBadCharacter(reason string) bool {
	switch reason {
	case "control characters are not allowed",
		"invalid leading UTF-8 octet",
		"incomplete UTF-8 octet sequence",
		"invalid trailing UTF-8 octet",
		"invalid length of a UTF-8 sequence",
		"invalid Unicode character",
		"incomplete UTF-16 character",
		"unexpected low surrogate area",
		"incomplete UTF-16 surrogate pair",
		"expected low surrogate area":
		return true
	}
	return false
}

// parses reports whether text is YAML without syntax errors.
func parses(text []byte) bool {
	dec := yaml.NewDecoder(bytes.NewReader(text))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			return errors.Is(err, io.EOF)
		}
	}
}

// problemPattern matches the parser's message: "yaml: line 3: reason". The
// line is missing when the problem is on the first line.
var problemPattern = regexp.MustCompile(`^(?:yaml: )?(?:line (\d+): )?(.*)$`)

// parserProblems start the messages of the YAML parser's own errors, as opposed
// to those of its scanner.
var parserProblems = []string{
	"did not find expected <",
	"did not find expected node content",
	"did not find expected '-' indicator",
	"did not find expected key",
	"did not find expected ',' or ",
	"found duplicate %",
	"found incompatible YAML document",
	"found undefined tag handle",
}

// syntaxError turns the parser's err into a SyntaxError. text is what the
// parser read, src[offset:end], and shift is the number of lines before it.
//
// The parser leaves out the line when it is the first. Its scanner counts lines
// from 1 and reports where it fails. For its own errors the line is where the
// enclosing block starts, so the line is found by cutting text short: the first
// line whose end makes the parser fail with the same message is where it breaks.
func syntaxError(file string, err error, src source, offset, end, shift int) *SyntaxError {
	m := problemPattern.FindStringSubmatch(err.Error())
	line, convErr := strconv.Atoi(m[1])
	if convErr != nil || line < 1 {
		line = 1
	}
	if slices.ContainsFunc(parserProblems, func(p string) bool { return strings.HasPrefix(m[2], p) }) {
		text := src.part(offset, end)
		ends := source{text, src.order}.lineEnds()
		k := sort.Search(len(ends), func(i int) bool { return failsWith(text[:ends[i]], m[2]) })
		line = min(k+1, len(ends))
	}
	return &SyntaxError{File: file, Line: line + shift, Reason: m[2]}
}

// lineEnds returns the offset just after each line of data, the last being
// len(data). A line ends at a line break, see lineBreak.
func (s source) lineEnds() []int {
	var ends []int
	size := s.size()
	for i := s.start(); i < size; {
		if w := s.lineBreak(i, size); w > 0 {
			i += w
			ends = append(ends, i)
			continue
		}
		_, w := s.unit(i)
		i += w
	}
	if len(ends) == 0 || ends[len(ends)-1] != len(s.data) {
		ends = append(ends, len(s.data))
	}
	return ends
}

// failsWith reports whether parsing text fails with the parser message reason.
func failsWith(text []byte, reason string) bool {
	dec := yaml.NewDecoder(bytes.NewReader(text))
	for {
		var node yaml.Node
		err := dec.Decode(&node)
		if err != nil {
			return !errors.Is(err, io.EOF) && problemPattern.FindStringSubmatch(err.Error())[2] == reason
		}
	}
}

// shiftLines moves the positions of n and everything below it down by lines.
// Aliases point into the tree, which is shifted through its own parent.
func shiftLines(n *yaml.Node, lines int) {
	n.Line += lines
	for _, c := range n.Content {
		shiftLines(c, lines)
	}
}
