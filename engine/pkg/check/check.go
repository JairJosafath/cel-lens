// Package check compiles and type-checks the CEL expressions of Kubernetes
// YAML files the way the Kubernetes API server does, and reports the problems
// as diagnostics positioned in the file.
//
// It builds on the libraries the API server itself uses: the apiextensions
// validation for the x-kubernetes-validations rules of a CustomResourceDefinition
// (the schema node's type for self and oldSelf, the library functions and the
// cost limits) and the admission CEL compiler for ValidatingAdmissionPolicy
// (object, oldObject, request, params, namespaceObject, authorizer and
// variables). The libraries and cost rules follow the Kubernetes version chosen with
// WithVersion.
package check

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/cel-go/cel"
	"go.yaml.in/yaml/v3"
	plugincel "k8s.io/apiserver/pkg/admission/plugin/cel"

	"github.com/JairJosafath/cel-lens/engine/internal/yamlfield"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// Severity says how serious a diagnostic is.
type Severity string

// The severities.
const (
	// SeverityError is a problem the API server would reject the file for.
	SeverityError Severity = "error"
	// SeverityWarning is a problem the API server accepts but lists as a
	// warning, such as a field a policy uses that its resource does not have.
	SeverityWarning Severity = "warning"
)

// Diagnostic is one problem found in a CEL expression.
type Diagnostic struct {
	// File is the file name passed to Check.
	File string
	// Message says what is wrong, in the words of the API server.
	Message string
	// Severity is the seriousness of the problem.
	Severity Severity
	// Start is the position of the first character the problem refers to.
	Start extract.Position
	// End is the position just after the last character the problem refers
	// to. When the problem concerns the whole expression, such as an
	// expression that is too expensive, Start and End cover all of it.
	End extract.Position
	// Fixes are the limits that would solve a problem about cost: where to add
	// which schema keyword, and with what number. Empty for other problems.
	Fixes []Fix
	// FixesTogether is true when the fixes solve the problem only all at once,
	// and false when each of them does alone.
	FixesTogether bool
}

// Check finds the CEL expressions in data, a YAML file with any number of
// documents, and returns the problems the API server would report for them,
// ordered by position. file is only recorded in the diagnostics. A file
// without problems, and documents that are not CustomResourceDefinitions or
// ValidatingAdmissionPolicies, give no diagnostics.
//
// CustomResourceDefinitions are checked when their apiVersion is
// apiextensions.k8s.io/v1, the only version current API servers accept; older
// ones are ignored. A CustomResourceDefinition is validated even when it has no
// CEL, so a schema the API server rejects is reported. ValidatingAdmissionPolicies
// without CEL are still skipped. Rules whose schema is itself invalid are not
// checked, because the API server does not check them either.
//
// A field of the wrong type, such as failurePolicy: 3, gets a diagnostic on
// the field that names its path and the type it should have; the CEL of the
// rest of the document is still checked. Every such field is reported, up to
// 64 a document; schema problems not tied to an expression are not reported in
// such a CRD, since its schema is only partly read. A document that cannot be
// read as its Kubernetes type in any other way gets one diagnostic on its first
// line and its CEL is not checked; the following documents are still checked.
//
// A document whose YAML does not parse gets one diagnostic on the line where
// parsing fails, and the documents after it are still checked. Check then
// also returns an error, naming the file and line, for each such document
// (see extract.SyntaxError). When ctx is cancelled, Check returns ctx's error.
// A diagnostic is never an error: the returned error is for input that cannot be read.
func Check(ctx context.Context, file string, data []byte, opts ...Option) ([]Diagnostic, error) {
	var set settings
	for _, o := range opts {
		o(&set)
	}
	// A well-formed UTF-16 file is decoded here, once, and the rest reads the
	// UTF-8 text; the data kept in the reporter is the file as read.
	text := extract.UTF8(data)
	docs, broken, err := extract.Documents(ctx, file, text)
	if err != nil {
		return nil, err
	}
	exprs, findErr := extract.FindIn(ctx, file, text, docs, broken)
	if errors.Is(findErr, context.Canceled) || errors.Is(findErr, context.DeadlineExceeded) {
		return nil, findErr
	}
	byDoc := map[int]map[string]extract.Expression{}
	for _, e := range exprs {
		if byDoc[e.Document] == nil {
			byDoc[e.Document] = map[string]extract.Expression{}
		}
		byDoc[e.Document][e.FieldPath] = e
	}
	parser, err := cel.NewEnv(cel.OptionalTypes())
	if err != nil {
		return nil, fmt.Errorf("build syntax environment for %s: %w", file, err)
	}
	r := reporter{version: set.version, crds: set.typingCRDs(), kyverno: set.on.Has(feature.Kyverno), file: file, lines: extract.SplitLines(text), data: data, eol: "\n", spans: spans{env: parser}}
	if bytes.Contains(extract.Text(data), []byte("\r\n")) {
		r.eol = "\r\n"
	}
	for _, se := range broken {
		r.syntax(se)
	}
	for _, d := range docs {
		found := byDoc[d.Index]
		if err := ctx.Err(); err != nil {
			return r.sorted(), err
		}
		if err := r.document(ctx, d.Node, found); err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return r.sorted(), cerr
			}
			r.unreadable(d.Node, err)
		}
	}
	return r.sorted(), findErr
}

// reporter collects the diagnostics of one file.
type reporter struct {
	version kubeversion.Version
	crds    *CRDs // the custom resources params may be typed from; may be nil
	kyverno bool  // whether Kyverno ValidatingPolicy documents are checked
	file    string
	lines   [][]rune
	data    []byte // the file as read, for bytes that lines cannot show
	eol     string // the line ending of the file, for text to insert
	diags   []Diagnostic
	guessed []string // field paths of schemas whose wrong-typed type read replaced with object

	plain plugincel.Compiler            // see plainCompiler
	bare  *plugincel.CompositedCompiler // see compositeCompiler

	spans spans // finds where the nodes of an expression lie
}

func (r *reporter) sorted() []Diagnostic {
	sort.SliceStable(r.diags, func(i, j int) bool {
		a, b := r.diags[i].Start, r.diags[j].Start
		return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
	})
	return r.diags
}

// document checks one YAML document with the expressions found in it, by
// field path.
func (r *reporter) document(ctx context.Context, node *yaml.Node, exprs map[string]extract.Expression) error {
	head, ok := typeOf(node)
	if !ok {
		return nil
	}
	group, version, _ := strings.Cut(head.APIVersion, "/")
	switch {
	case group == "apiextensions.k8s.io" && version == "v1" && head.Kind == "CustomResourceDefinition":
		return r.crd(ctx, node, exprs)
	case group == "admissionregistration.k8s.io" && head.Kind == "ValidatingAdmissionPolicy" && len(exprs) > 0:
		return r.policy(ctx, node, exprs)
	case group == "admissionregistration.k8s.io" && head.Kind == "MutatingAdmissionPolicy" && len(exprs) > 0:
		return r.mutatingPolicy(ctx, node, exprs)
	case group == "policies.kyverno.io" && version == "v1" && head.Kind == "ValidatingPolicy" && len(exprs) > 0 && r.kyverno:
		return r.kyvernoPolicy(ctx, node, exprs)
	}
	return nil
}

// typeOf returns the apiVersion and kind of a document. It is false unless the
// document is a mapping whose apiVersion and kind are strings, so a list, a
// scalar or any other YAML that is not a Kubernetes object is left alone.
func typeOf(node *yaml.Node) (head struct{ APIVersion, Kind string }, ok bool) {
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return head, false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		if val.Kind != yaml.ScalarNode || val.Tag != "!!str" {
			continue
		}
		switch key.Value {
		case "apiVersion":
			head.APIVersion = val.Value
		case "kind":
			head.Kind = val.Value
		}
	}
	return head, true
}

// unreadable reports a document that cannot be read as the Kubernetes type its
// apiVersion and kind name, such as a field of the wrong type. The problem is
// placed on the first line of the document, and the CEL in it is not checked.
func (r *reporter) unreadable(node *yaml.Node, err error) {
	start := extract.Position{Line: node.Line, Column: node.Column}
	if len(node.Content) > 0 {
		start = extract.Position{Line: node.Content[0].Line, Column: node.Content[0].Column}
	}
	end := start
	if start.Line >= 1 && start.Line <= len(r.lines) {
		end.Column = len(r.lines[start.Line-1]) + 1
	}
	r.diags = append(r.diags, Diagnostic{File: r.file, Message: err.Error(), Severity: SeverityError, Start: start, End: end})
}

// syntax reports a document that does not parse, on the first token of the line
// where the parser stops through the end of that line, or the whole line when
// it holds only blanks, so the range is never empty. The parser reports no
// column. At the end of the file the line may be empty, so the problem goes to
// the last line that is not; a line of blanks is where the parser stopped, and
// is kept. When that line is empty and a later one is not, as when the parser
// blames an empty first line for a character further down, the problem goes to
// the next line that is not.
func (r *reporter) syntax(se *extract.SyntaxError) {
	if line, col, ok := r.badChar(se); ok {
		d := Diagnostic{
			File:     r.file,
			Message:  "YAML is broken here: " + se.Reason,
			Severity: SeverityError,
			Start:    extract.Position{Line: line, Column: col},
			End:      extract.Position{Line: line, Column: col + 1},
		}
		// The parser reads ahead, so every document before the bad character
		// fails with it; the character is reported once.
		if !slices.ContainsFunc(r.diags, func(x Diagnostic) bool { return reflect.DeepEqual(x, d) }) {
			r.diags = append(r.diags, d)
		}
		return
	}
	line := min(se.Line, len(r.lines))
	for line > 1 && string(r.lines[line-1]) == "" {
		line--
	}
	for line < len(r.lines) && string(r.lines[line-1]) == "" {
		line++
	}
	text := r.lines[line-1]
	start := 1
	for start <= len(text) && (text[start-1] == ' ' || text[start-1] == '\t') {
		start++
	}
	if start > len(text) {
		start = 1
	}
	r.diags = append(r.diags, Diagnostic{
		File:     r.file,
		Message:  "YAML is broken here: " + se.Reason,
		Severity: SeverityError,
		Start:    extract.Position{Line: line, Column: min(start, len(text)+1)},
		End:      extract.Position{Line: line, Column: len(text) + 1},
	})
}

// badChar finds the first character a YAML stream may not hold, at or after the
// line where the parser blamed se, when se is about such a character. The parser
// blames the first line of the document for these, wherever the character is.
// The column counts characters from 1, an invalid byte or UTF-16 unit being one.
// A stream that starts with a UTF-16 byte order mark is read as UTF-16.
func (r *reporter) badChar(se *extract.SyntaxError) (line, col int, ok bool) {
	if !extract.IsBadCharacter(se.Reason) {
		return 0, 0, false
	}
	next := nextUTF8
	var order binary.ByteOrder
	switch {
	case bytes.HasPrefix(r.data, []byte{0xff, 0xfe}):
		order = binary.LittleEndian
	case bytes.HasPrefix(r.data, []byte{0xfe, 0xff}):
		order = binary.BigEndian
	}
	i := 0
	if order != nil {
		i = 2
		next = func(b []byte) (rune, int, bool) { return nextUTF16(order, b) }
	}
	line, col = 1, 1
	for i < len(r.data) {
		c, n, bad := next(r.data[i:])
		switch {
		case !bad && extract.IsBreak(c):
			if c != '\r' || !r.crlfAt(i, order) {
				line, col = line+1, 1
			}
			i += n
			continue
		case line >= se.Line && (bad || !printable(c)):
			return line, col, true
		}
		i += n
		col++
	}
	return 0, 0, false
}

// crlfAt reports whether the carriage return at r.data[i] is followed by a line
// feed, in UTF-16 of the given byte order, or in UTF-8 when order is nil.
func (r *reporter) crlfAt(i int, order binary.ByteOrder) bool {
	if order == nil {
		return i+1 < len(r.data) && r.data[i+1] == '\n'
	}
	return i+3 < len(r.data) && order.Uint16(r.data[i+2:]) == '\n'
}

// nextUTF8 decodes the character at the start of b and its size in bytes; bad
// is true for a byte that is not valid UTF-8.
func nextUTF8(b []byte) (c rune, n int, bad bool) {
	c, n = utf8.DecodeRune(b)
	return c, n, c == utf8.RuneError && n == 1
}

// nextUTF16 decodes the character at the start of b, in the byte order given,
// and its size in bytes; bad is true for a truncated unit or an unpaired
// surrogate, which take one unit, or the rest of b when it is a single byte.
func nextUTF16(order binary.ByteOrder, b []byte) (c rune, n int, bad bool) {
	if len(b) < 2 {
		return 0, len(b), true
	}
	u := rune(order.Uint16(b))
	switch {
	case u >= 0xdc00 && u < 0xe000:
		return u, 2, true
	case u >= 0xd800 && u < 0xdc00:
		if len(b) < 4 {
			return u, len(b), true
		}
		low := rune(order.Uint16(b[2:]))
		if low < 0xdc00 || low >= 0xe000 {
			return u, 2, true
		}
		return 0x10000 + (u-0xd800)<<10 + (low - 0xdc00), 4, false
	}
	return u, 2, false
}

// printable reports whether YAML allows c: tab, line breaks, and the printable
// characters of the YAML specification.
func printable(c rune) bool {
	switch {
	case c == '\t' || c == '\n' || c == '\r' || c == 0x85:
		return true
	case c < 0x20 || c >= 0x7f && c < 0xa0:
		return false
	}
	return c < 0xd800 || c >= 0xe000 && c <= 0xfffd || c >= 0x10000 && c <= 0x10ffff
}

// report adds the problems that detail, an API server error message, names for
// expression e. The compiler messages carry their own position within the
// expression; the others concern the expression as a whole.
func (r *reporter) report(e extract.Expression, detail string) {
	r.reportAs(e, SeverityError, "", detail)
}

// reportAs is report with the severity of the diagnostics and a prefix for
// their messages, such as the kind a warning is about.
func (r *reporter) reportAs(e extract.Expression, sev Severity, prefix, detail string) {
	issues := issuePattern.FindAllStringSubmatch(detail, -1)
	if len(issues) == 0 {
		r.diags = append(r.diags, Diagnostic{File: r.file, Message: prefix + detail, Severity: sev, Start: e.Start, End: e.End})
		return
	}
	for _, m := range issues {
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		d := Diagnostic{File: r.file, Message: prefix + m[3], Severity: sev, Start: e.Start, End: e.End}
		if start, end, ok := locate(r.spans.parse(e.Text), r.lines, e, line, col-1); ok {
			d.Start, d.End = start, end
		}
		r.diags = append(r.diags, d)
	}
}

// issuePattern matches the first line of a CEL compiler issue, as cel-go
// prints it: "ERROR: <input>:line:column: message". The column counts from 1.
var issuePattern = regexp.MustCompile(`(?m)^(?:.*: )?ERROR: <input>:(\d+):(\d+): (.*)$`)

// decode reads a YAML node into out, a Kubernetes API type, which only knows
// JSON field names, so the node goes through JSON.
func decode(node *yaml.Node, out any) error {
	var raw any
	if err := node.Decode(&raw); err != nil {
		return err
	}
	js, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(js, out)
}

// maxPruned is the most wrong-typed fields read reports in one document.
const maxPruned = 64

// read decodes node into out, a pointer to a Kubernetes API type. A field of
// the wrong type is reported on that field, and out holds the rest of the
// document, so that its CEL can be checked; partial is then true. Because
// encoding/json stops at the first wrong type inside a type that decodes
// itself, the field is removed from a copy of the document and the decoding is
// repeated until the document reads, so every wrong-typed field is reported,
// up to maxPruned. Any other failure is returned, and ctx's error if ctx is
// cancelled while a field is being located. The path of a schema whose type was
// replaced is added to r.guessed.
func (r *reporter) read(ctx context.Context, node *yaml.Node, out any) (partial bool, err error) {
	err = decode(node, out)
	var tree *yamlfield.Tree
	for range maxPruned {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			break
		}
		if tree == nil {
			tree = yamlfield.Copy(node)
			node = tree.Root
		}
		f, ok, ferr := yamlfield.Find(ctx, node, out, typeErr)
		if ferr != nil {
			return false, ferr
		}
		if !ok || !tree.Remove(f) {
			break
		}
		r.wrongType(f)
		if yamlfield.IsSchemaType(f) {
			r.guessed = append(r.guessed, strings.TrimSuffix(f.Path, ".type"))
		}
		partial = true
		fresh := reflect.New(reflect.TypeOf(out).Elem())
		err = decode(node, fresh.Interface())
		reflect.ValueOf(out).Elem().Set(fresh.Elem())
	}
	var typeErr *json.UnmarshalTypeError
	if err == nil || partial && errors.As(err, &typeErr) {
		return partial, nil
	}
	return false, err
}

// wrongType reports field f, which cannot be decoded into its Go type. The
// problem is on the value: all of a scalar, the first line of a mapping or list.
func (r *reporter) wrongType(f yamlfield.Field) {
	v := f.Value
	start := extract.Position{Line: v.Line, Column: v.Column}
	end := start
	if start.Line >= 1 && start.Line <= len(r.lines) {
		line := r.lines[start.Line-1]
		end.Column = len(line) + 1
		flow := v.Style&yaml.FlowStyle != 0 && (v.Kind == yaml.SequenceNode || v.Kind == yaml.MappingNode)
		if v.Kind == yaml.ScalarNode || v.Kind == yaml.AliasNode || flow {
			start.Column += propsLen(line[min(start.Column-1, len(line)):])
			if flow {
				if n := flowLen(line[min(start.Column-1, len(line)):]); n > 0 {
					end.Column = start.Column + n
				}
			} else {
				end.Column = start.Column + scalarLen(v, line, start.Column-1)
			}
		}
	}
	r.diags = append(r.diags, Diagnostic{
		File: r.file, Message: fmt.Sprintf("%s: expected %s, got %s", f.Path, f.Expected, f.Got),
		Severity: SeverityError, Start: start, End: end,
	})
}

// propsLen is the number of columns of the anchor and tag that start s, with
// the spaces after them.
func propsLen(s []rune) int {
	n := 0
	for n < len(s) && (s[n] == '&' || s[n] == '!') {
		for n < len(s) && s[n] != ' ' {
			n++
		}
		for n < len(s) && s[n] == ' ' {
			n++
		}
	}
	return n
}

// scalarLen is the number of columns of the scalar v in line, which starts at
// offset from. A scalar that goes on after the line, such as a block scalar,
// is measured to the end of the line.
func scalarLen(v *yaml.Node, line []rune, from int) int {
	if from < 0 || from > len(line) {
		return 0
	}
	rest := string(line[from:])
	switch {
	case v.Kind == yaml.AliasNode:
		return 1 + utf8.RuneCountInString(v.Value)
	case v.Style&yaml.DoubleQuotedStyle != 0:
		return quotedLen([]rune(rest), '"')
	case v.Style&yaml.SingleQuotedStyle != 0:
		return quotedLen([]rune(rest), '\'')
	case v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		return utf8.RuneCountInString(strings.TrimRight(rest, " \t"))
	}
	// A plain scalar folds its lines into v.Value, so the text of the first
	// line is the part of rest that v.Value starts with.
	runes, val := []rune(rest), []rune(v.Value)
	n := 0
	for n < len(runes) && n < len(val) && runes[n] == val[n] {
		n++
	}
	return utf8.RuneCountInString(strings.TrimRight(string(runes[:n]), " \t"))
}

// flowLen is the number of columns of the flow list or mapping that starts s,
// up to and with its closing bracket. It is 0 when the collection does not
// close on the line.
func flowLen(s []rune) int {
	depth := 0
	// last is the last non-blank rune before i (0 at the start), blank whether
	// a blank followed it, and afterQuoted whether it was a colon that follows a
	// quoted key. prop says the word before the blanks is an anchor or tag. They
	// let a quote be classified in constant time.
	var last rune
	var blank, afterQuoted, quoted, prop bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t':
			blank = true
			continue
		}
		starts := startsNode(last, blank, afterQuoted) || prop && blank
		if starts {
			prop = c == '&' || c == '!'
		} else if blank {
			prop = false
		}
		switch c {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '"', '\'':
			// A quote opens a quoted scalar only where a node starts; in the
			// middle of a plain scalar, such as it's, it is an ordinary character.
			if starts {
				i += quotedLen(s[i:], c) - 1
				last, blank, afterQuoted, quoted, prop = c, false, false, true, false
				continue
			}
		case '!':
			// A verbatim tag such as !<tag:yaml.org,2002:str> runs to its '>';
			// a comma inside it does not start a node.
			if starts && i+1 < len(s) && s[i+1] == '<' {
				for i < len(s) && s[i] != '>' {
					i++
				}
				if i == len(s) {
					return 0
				}
				c = '>'
			}
		case '#':
			if blank {
				return 0
			}
		}
		afterQuoted = c == ':' && quoted && !blank
		quoted = false
		last, blank = c, false
	}
	return 0
}

// startsNode reports whether a node can start after the last non-blank rune
// last, which blank says a blank followed: at the start, or after an opening
// bracket, a comma, a question mark, or a colon that a blank follows or that
// ends a quoted key.
func startsNode(last rune, blank, afterQuoted bool) bool {
	switch last {
	case 0, '[', '{', ',', '?':
		return true
	case ':':
		return blank || afterQuoted
	}
	return false
}

// quotedLen is the length of the quoted scalar at the start of s, which is
// quoted with q, up to and including the closing quote; the whole of s when
// the scalar goes on past the line.
func quotedLen(s []rune, q rune) int {
	for i := 1; i < len(s); i++ {
		switch {
		case q == '"' && s[i] == '\\':
			i++
		case s[i] != q:
		case q == '\'' && i+1 < len(s) && s[i+1] == q:
			i++
		default:
			return i + 1
		}
	}
	return len(s)
}
