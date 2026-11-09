package lsp

import (
	"context"
	"encoding/json"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/inline"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// codeLens answers textDocument/codeLens with the inline info of the
// document: above each CEL expression the server checks, the line package
// inline writes, one code lens per part, each with its command and that
// command's arguments. A line goes above the line of the expression's key,
// and only the first expression whose key is on a line gets one. There are no
// lenses while the setting inlineInfo is false, nor for a document that is
// not open. The problems counted are those of the last check of the document
// as it is now; it is checked only if no check of this version completed. The
// lenses are kept until the document is checked again or a .cellens file
// changes, so asking again costs nothing.
func (ss *session) codeLens(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, badParams("textDocument/codeLens", err)
	}
	uri := p.TextDocument.URI
	doc, ok := ss.docs[uri]
	if !ok || ss.inlineOff {
		return []any{}, nil
	}
	if doc.codeLenses != nil && doc.codeLensesAt == ss.lensChanges {
		return doc.codeLenses, nil
	}
	diags, err := ss.diagnostics(ctx, uri, doc)
	if err != nil {
		return nil, err
	}
	summaries, err := ss.assist.Summaries(ctx, doc.parse(ctx, uri))
	if err != nil {
		return nil, err
	}
	lines := doc.runes()
	lenses := []any{}
	used := map[int]bool{}
	for _, s := range summaries {
		line := keyLine(lines, s.Start)
		if used[line] {
			continue
		}
		used[line] = true
		rule := inline.Rule{
			Subject: s.Subject, SubjectType: s.SubjectType, Problems: problemsIn(diags, s), Values: ss.inlineValues(uri, s.Values),
			Location: map[string]any{"uri": uri, "range": toRange(lines, s.Start, s.End)},
		}
		r := lineRange(lines, line)
		for _, part := range inline.Line(rule) {
			lenses = append(lenses, map[string]any{"range": r, "command": part})
		}
	}
	doc.codeLenses, doc.codeLensesAt = lenses, ss.lensChanges
	return lenses, nil
}

// problemsIn counts the diagnostics that start inside the expression s.
func problemsIn(diags []check.Diagnostic, s assist.Summary) int {
	n := 0
	for _, d := range diags {
		if !before(d.Start, s.Start) && !before(s.End, d.Start) {
			n++
		}
	}
	return n
}

// keyLine is the line, from 1, of the key of the expression that starts at
// start: its own line, unless the expression starts a line of its own, as a
// block scalar does, and then the nearest line above that is not blank.
func keyLine(lines [][]rune, start extract.Position) int {
	if start.Line < 1 || start.Line > len(lines) {
		return start.Line
	}
	text := lines[start.Line-1]
	before := strings.TrimLeftFunc(string(text[:min(max(start.Column-1, 0), len(text))]), unicode.IsSpace)
	if before != "" && before != `"` && before != "'" {
		return start.Line
	}
	for l := start.Line - 1; l >= 1; l-- {
		if strings.TrimSpace(string(lines[l-1])) != "" {
			return l
		}
	}
	return start.Line
}

// lineRange is the range of line, from 1, from its first character that is
// not a space to its end: a code lens shows above it.
func lineRange(lines [][]rune, line int) lspRange {
	first, end := 1, 1
	if line >= 1 && line <= len(lines) {
		text := lines[line-1]
		end = len(text) + 1
		for first < end && unicode.IsSpace(text[first-1]) {
			first++
		}
	}
	return toRange(lines, extract.Position{Line: line, Column: first}, extract.Position{Line: line, Column: end})
}

// inlineValues are values of the document at uri as the line shows them,
// each with the target its commands take: the .cellens file and the entry
// that name it, or for a dyn value no entry names the .cellens file an entry
// would go in. A value an entry names that is dyn all the same, as the entry
// does not apply, gets Edit and Remove for that entry, not a second entry. A
// policy variable whose type follows from the entries of the values it reads
// shows that type. A document that is no file has none, as no .cellens entry
// can name it.
func (ss *session) inlineValues(uri string, values []assist.Value) []inline.Value {
	if _, ok := diskPath(uri); !ok {
		return nil
	}
	key := crdKey(uri)
	var out []inline.Value
	for _, v := range values {
		iv := inline.Value{Name: v.Name, Type: v.Type, Inferred: v.Inferred, Target: inline.Target{
			Document: v.DocumentKind + " " + v.DocumentName, Version: v.Version, Variable: v.Variable, Path: v.Path, Value: v.Name,
		}}
		l, e, ok := ss.entryOf(key, v)
		switch {
		case ok:
			iv.Entry = true
			iv.Target.Cellens, iv.Target.File = l.uri, e.File
			switch {
			case e.Kind != nil:
				iv.Target.Kind = e.Kind.APIVersion + " " + e.Kind.Kind
			case e.Type != nil:
				iv.Target.Type = e.Type.String()
			case e.Schema != nil:
				iv.Target.Schema = e.Schema.JSON()
			case e.Go != nil:
				iv.Target.Go = e.Go.String()
			}
			// A schema or Go type shows as the type a type: entry would
			// write, which v.Type has; the others as the entry writes them,
			// but for the type of a variable that follows from other
			// entries, as this one does not apply.
			if t := iv.Target.Kind + iv.Target.Type; t != "" && v.Type != "" && !v.Inferred {
				iv.Type = t
			}
		case v.Inferred:
			lensKey, lensURI := ss.lensFor(key)
			iv.Target.Cellens, iv.Target.File = lensURI, relativeTo(lensKey, key)
			if loc, ok := ss.entryLocation(v.FromSource, v.FromLine); ok {
				iv.From = loc
			}
		case v.Type != "":
			continue // typed by no entry the session knows
		default:
			lensKey, lensURI := ss.lensFor(key)
			iv.Target.Cellens, iv.Target.File = lensURI, relativeTo(lensKey, key)
		}
		out = append(out, iv)
	}
	return out
}

// entryLocation returns where the type of the entry at line, from 1, of the
// .cellens file messages name source (see lensSource) starts, as
// cel-lens.showRule takes a place: {uri, range}, the range empty. It is
// false when the session knows no such entry.
func (ss *session) entryLocation(source string, line int) (map[string]any, bool) {
	for _, l := range ss.sortedLenses() {
		if ss.lensSource(l) != source {
			continue
		}
		for _, e := range l.entries {
			if e.Line != line {
				continue
			}
			off, ok := cellens.TypeAt(l.text, e)
			if !ok {
				return nil, false
			}
			at := offsetPosition(l.text, off)
			return map[string]any{"uri": l.uri, "range": lspRange{at, at}}, true
		}
	}
	return nil, false
}

// entryOf returns the .cellens file and the entry that name the value v of
// the file with the key: the first in the order of the files' keys, which
// gives the value its type when it applies (see assumedFor).
func (ss *session) entryOf(key string, v assist.Value) (*lens, cellens.Entry, bool) {
	for _, l := range ss.sortedLenses() {
		for _, e := range l.entries {
			if l.target(e) == key && e.Document.Kind == v.DocumentKind && e.Document.Name == v.DocumentName &&
				e.Version == v.Version && e.Variable == v.Variable && e.Path.String() == v.Path {
				return l, e, true
			}
		}
	}
	return nil, cellens.Entry{}, false
}

// lensFor returns the key and the URI of the .cellens file an entry for a
// value of the file with the key would go in: the nearest .cellens the
// session knows in a folder at or above the file, else the one at the root of
// the workspace folder the file is in, else the one beside the file.
func (ss *session) lensFor(key string) (lensKey, lensURI string) {
	var best *lens
	for _, l := range ss.lenses {
		if dir := path.Dir(l.key); below(key, dir) && (best == nil || len(dir) > len(path.Dir(best.key))) {
			best = l
		}
	}
	if best != nil {
		return best.key, best.uri
	}
	dir := path.Dir(key)
	found := false
	for _, f := range ss.folders {
		if fk := strings.TrimSuffix(crdKey(pathURI(f)), "/"); below(key, fk) && (!found || len(fk) > len(dir)) {
			dir, found = fk, true
		}
	}
	k := path.Join(dir, cellens.FileName)
	return k, pathURI(filepath.FromSlash(k))
}

// below reports whether the file with the key is in the folder dir, or in a
// folder below it.
func below(key, dir string) bool { return strings.HasPrefix(key, strings.TrimSuffix(dir, "/")+"/") }

// relativeTo is the file with the key relative to the folder of the .cellens
// file with the key lensKey, slash-separated.
func relativeTo(lensKey, key string) string {
	return strings.TrimPrefix(key, strings.TrimSuffix(path.Dir(lensKey), "/")+"/")
}

// refreshCodeLenses asks the client to ask again for the code lenses of the
// documents it shows, when it can (workspace/codeLens/refresh): the lines
// change with the settings, the .cellens files and the CRDs, not only with
// the document.
func (ss *session) refreshCodeLenses() error {
	ss.refreshedAt = ss.lensChanges
	if !ss.codeLensRefresh {
		return nil
	}
	ss.refreshes++
	return ss.conn.Request("cel-lens-refresh-lenses-"+strconv.Itoa(ss.refreshes), "workspace/codeLens/refresh", nil)
}
