package lsp

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/goschema"
	"github.com/JairJosafath/cel-lens/engine/internal/inline"
	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// The protocol's CompletionItemKind values the items of a .cellens take.
const (
	itemField       = 5
	itemVariable    = 6
	itemClass       = 7
	itemProperty    = 10
	itemValue       = 12
	itemFile        = 17
	itemReference   = 18
	itemFolder      = 19
	itemEnumMember  = 20
	itemStruct      = 22
	itemTypeParam   = 25
	insertSnippet   = 2 // InsertTextFormat.Snippet
	insertAsIs      = 1 // InsertTextMode.asIs
	maxDynItems     = 200
	maxGoTypeDetail = 40
)

// entryKeys are the keys of an entry, in the order docs/cellens.md lists
// them, with what each says.
var entryKeys = []cellens.Word{
	{Name: "file", Doc: "The YAML file the value is in, at or below this .cellens."},
	{Name: "document", Doc: "The document the value is in: its kind and metadata.name."},
	{Name: "version", Doc: "The version of the CustomResourceDefinition the value is in."},
	{Name: "variable", Doc: "The variable of the policy: params, object, oldObject or variables.<name>."},
	{Name: "path", Doc: "The value below the root or the variable, such as spec.config."},
	{Name: "type", Doc: "The type in CEL's names, such as {name: string}."},
	{Name: "kind", Doc: "The type of a kind, such as apps/v1 Deployment."},
	{Name: "schema", Doc: "The type as an OpenAPI schema, written as a CustomResourceDefinition's."},
	{Name: "go", Doc: "The type of a Go struct of the workspace, such as ./api/v1#QuotaSpec."},
}

// celTypes are the CEL type names a type: offers, with what each is.
var celTypes = []cellens.Word{
	{Name: "string", Doc: "A string."},
	{Name: "int", Doc: "An integer."},
	{Name: "uint", Doc: "An integer of at least 0."},
	{Name: "double", Doc: "A number."},
	{Name: "bool", Doc: "true or false."},
	{Name: "bytes", Doc: "Bytes, written in base64."},
	{Name: "timestamp", Doc: "A date and time, written as RFC 3339."},
	{Name: "duration", Doc: "A duration, such as 1h30m."},
}

// lensItem is a completion item in a .cellens.
type lensItem struct {
	label, detail, text string
	kind                int
	// snippet is true when text is a snippet; asIs when its lines keep the
	// indentation they have.
	snippet, asIs bool
	// suggest is true when the client should ask for completion again once
	// the item is in, as for the types of a Go folder after its #.
	suggest bool
	// start and end are the characters the item replaces on the spot's line,
	// when they are not the spot's word: set is true then.
	start, end int
	set        bool
}

// lensCompletion answers textDocument/completion in the open .cellens at
// uri: the keys of an entry, the values of its keys, the CEL type names
// where type: writes one, and the keywords and their values of a schema, as
// far as the line at the position can be read (see cellens.Locate). trigger
// is the character the client asks after, "" when it asks for no character.
// After a space it answers only where the space follows a key's colon, as
// in "go: ", with what it answers there when asked: a space elsewhere, as
// between the words of a value, asks for nothing.
func (ss *session) lensCompletion(ctx context.Context, uri string, pos position, trigger string) (any, error) {
	l := ss.lenses[crdKey(uri)]
	if l == nil || !l.open {
		return nil, nil
	}
	lines := splitLines(l.text)
	at := fromPosition(lines, pos)
	s, ok := cellens.Locate(l.text, at.Line-1, at.Column-1)
	if !ok {
		return nil, nil
	}
	if trigger == " " && !afterColon(lines[s.Line], at.Column-1, s) {
		return nil, nil
	}
	items, incomplete, err := ss.lensItems(ctx, l, s, lines[s.Line], at.Column-1, eolOf(l.text))
	if err != nil || len(items) == 0 && !incomplete {
		return nil, err
	}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		start, end := s.Start, s.End
		if it.set {
			start, end = it.start, it.end
		}
		r := toRange(lines, extract.Position{Line: s.Line + 1, Column: start + 1}, extract.Position{Line: s.Line + 1, Column: end + 1})
		out[i] = map[string]any{"label": it.label, "kind": it.kind, "textEdit": textEdit{r, it.text}, "sortText": fmt.Sprintf("%04d", i)}
		if it.detail != "" {
			out[i]["detail"] = it.detail
		}
		if it.snippet {
			out[i]["insertTextFormat"] = insertSnippet
		}
		if it.asIs {
			out[i]["insertTextMode"] = insertAsIs
		}
		if it.suggest {
			out[i]["command"] = map[string]any{"title": "", "command": "editor.action.triggerSuggest"}
		}
	}
	if incomplete {
		return map[string]any{"isIncomplete": true, "items": out}, nil
	}
	return out, nil
}

// afterColon reports whether the character col of line, the line of the spot
// s, comes right after the colon of the key s is the value of and one space:
// where a value starts.
func afterColon(line []rune, col int, s cellens.Spot) bool {
	return s.Key != "" && col >= 2 && col <= len(line) && line[col-1] == ' ' && line[col-2] == ':'
}

// eolOf is the line ending of text: CR LF when it has one, else LF.
func eolOf(text []byte) string {
	if bytes.Contains(text, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// lensItems are the completion items at the spot s of the .cellens l, whose
// line there is line, with the position at character col; eol is the line
// ending of l. incomplete is true when more may come, as the Go types are
// being listed: the client then asks again as the person types. In an entry
// for a CustomResourceDefinition, whose dyn values are
// x-kubernetes-int-or-string and take only an int or a string, only what
// such an entry takes is offered.
func (ss *session) lensItems(ctx context.Context, l *lens, s cellens.Spot, line []rune, col int, eol string) (items []lensItem, incomplete bool, err error) {
	inEntry := len(s.Path) >= 2 && s.Path[0] == "types" && s.Path[1] == "-"
	docKind, _, _ := strings.Cut(strings.TrimSpace(s.Entry["document"]), " ")
	crd := docKind == cellens.CRDKind
	block := ""
	if ss.asIs {
		block = eol
	}
	switch {
	case len(s.Path) == 0 && s.Key == "":
		if _, ok := s.Keys["types"]; ok {
			return nil, false, nil
		}
		return []lensItem{keyItem(s, cellens.Word{Name: "types", Doc: "The entries: each names a value Kubernetes leaves dyn, and its type."})}, false, nil
	case !inEntry:
		return nil, false, nil
	case len(s.Path) > 2:
		return schemaItems(s, crd, block), false, nil
	case s.Key == "":
		return entryKeyItems(s, block), false, nil
	case crd && (s.Key == "kind" || s.Key == "go"):
		return nil, false, nil // a kind or a Go type is no int or string
	}
	switch s.Key {
	case "file":
		return ss.fileItems(ctx, l, s), false, nil
	case "document", "version", "variable", "path":
		items, err := ss.valueItems(ctx, l, s, line, col)
		return items, false, err
	case "type":
		return typeItems(s, line, col, ss.snippets, crd), false, nil
	case "kind":
		return ss.kindItems(s), false, nil
	case "go":
		items, incomplete := ss.goItems(ctx, l, s, line, col)
		return items, incomplete, nil
	}
	return nil, false, nil
}

// keyItem is the item that writes the key w at the spot s.
func keyItem(s cellens.Spot, w cellens.Word) lensItem {
	text := w.Name
	if !s.Colon {
		text += ": "
	}
	return lensItem{label: w.Name, detail: w.Doc, text: text, kind: itemProperty}
}

// valueItem is the item that writes the value v at the spot s, quoted as it
// must be there.
func valueItem(s cellens.Spot, v, detail string, kind int) lensItem {
	return lensItem{label: v, detail: detail, text: valueText(s, v), kind: kind}
}

// valueText is v as the value at the spot s writes it: escaped in quotes,
// quoted when YAML would read it otherwise, after a space when the spot
// follows its colon at once.
func valueText(s cellens.Spot, v string) string {
	switch {
	case s.Quote == '"':
		v = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v)
	case s.Quote == '\'':
		v = strings.ReplaceAll(v, "'", "''")
	case s.Flow && strings.ContainsAny(v, ",[]{}"):
		v = strconv.Quote(v)
	default:
		v = cellens.Scalar(v)
	}
	if s.Pad {
		return " " + v
	}
	return v
}

// entryKeyItems are the keys the entry at s can take next: those it does not
// have, but version for a policy and variable for a CustomResourceDefinition,
// and the keys of a type while it has none: type and schema for a
// CustomResourceDefinition, as no kind or Go type is an int or a string, and
// also kind and go for a policy. eol ends the line of schema:, which brings
// the next line, indented, when it is not "" (see lensItems).
func entryKeyItems(s cellens.Spot, eol string) []lensItem {
	has := func(k string) bool { _, ok := s.Keys[k]; return ok && k != s.KeyAt }
	kind, _, _ := strings.Cut(s.Keys["document"], " ")
	typed := slices.ContainsFunc([]string{"type", "kind", "schema", "go"}, has)
	var out []lensItem
	for _, w := range entryKeys {
		switch k := w.Name; {
		case has(k),
			k == "version" && (has("variable") || kind != "" && kind != cellens.CRDKind),
			k == "variable" && (has("version") || kind == cellens.CRDKind),
			typed && slices.Contains([]string{"type", "kind", "schema", "go"}, k),
			kind == cellens.CRDKind && (k == "kind" || k == "go"):
			continue
		}
		it := keyItem(s, w)
		if w.Name == "schema" && !s.Colon && !s.Flow {
			it.text = "schema:"
			if eol != "" {
				it.text, it.asIs = "schema:"+eol+strings.Repeat(" ", s.Indent+2), true
			}
		}
		out = append(out, it)
	}
	return out
}

// schemaItems are the keywords a schema can take at s, or the values of the
// keyword s is in. In an entry for a CustomResourceDefinition, crd, the
// schema is that of an integer or a string without a format, the types an
// x-kubernetes-int-or-string value takes. eol is as for entryKeyItems.
func schemaItems(s cellens.Spot, crd bool, eol string) []lensItem {
	if s.Path[2] != "schema" || !cellens.SchemaAt(s.Path[3:]) || s.Item {
		return nil
	}
	refused := func(w string) bool {
		return crd && (slices.Contains([]string{"object", "array", "number", "boolean", "x-kubernetes-int-or-string", "x-kubernetes-preserve-unknown-fields"}, w) ||
			w == "format" && s.Keys["type"] != "integer")
	}
	if s.Key != "" {
		var out []lensItem
		for _, w := range cellens.SchemaValues(s.Key, s.Keys) {
			if s.Key != "type" || !refused(w.Name) {
				out = append(out, valueItem(s, w.Name, w.Doc, itemEnumMember))
			}
		}
		return out
	}
	keys := s.Keys
	if s.Colon {
		keys = make(map[string]string, len(s.Keys))
		for k, v := range s.Keys {
			if k != s.KeyAt {
				keys[k] = v
			}
		}
	}
	var out []lensItem
	for _, w := range cellens.SchemaKeywords(keys) {
		if refused(w.Name) {
			continue
		}
		it := keyItem(s, w)
		if !s.Colon && !s.Flow && slices.Contains([]string{"properties", "items", "additionalProperties"}, w.Name) {
			it.text = w.Name + ":"
			if eol != "" {
				it.text, it.asIs = w.Name+":"+eol+strings.Repeat(" ", s.Indent+2), true
			}
		}
		out = append(out, it)
	}
	return out
}

// fileItems are the YAML files at or below the folder of l, relative to it,
// skipping the folders the workspace scan skips, at most maxWorkspaceFiles.
func (ss *session) fileItems(ctx context.Context, l *lens, s cellens.Spot) []lensItem {
	dir := filepath.FromSlash(path.Dir(l.key))
	var out []lensItem
	_ = fs.WalkDir(ss.dirFS()(dir), ".", func(rel string, d fs.DirEntry, err error) error {
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			return fs.SkipDir
		case d.IsDir() && rel != "." && skippedFolder(d.Name()):
			return fs.SkipDir
		case len(out) == maxWorkspaceFiles:
			return fs.SkipAll
		case !d.IsDir() && d.Type().IsRegular() && isYAML(rel):
			out = append(out, valueItem(s, rel, "", itemFile))
		}
		return nil
	})
	return out
}

// targetOf returns the key of the YAML file the entry at s names, and false
// when its file is not one a .cellens may name.
func (l *lens) targetOf(s cellens.Spot) (string, bool) {
	f := path.Clean(strings.TrimSpace(s.Entry["file"]))
	if f == "." || !filepath.IsLocal(filepath.FromSlash(f)) || strings.Contains(f, "\\") {
		return "", false
	}
	return path.Join(path.Dir(l.key), f), true
}

// roots returns the roots of the values of the file with the key, as
// check.Roots finds them with the session's settings; none for a file that
// cannot be read. The error is for a cancelled ctx.
func (ss *session) roots(ctx context.Context, key string) ([]check.Root, error) {
	data, readErr := ss.targetText(key)
	if readErr != nil {
		data = nil
	}
	return check.Roots(ctx, data, check.WithVersion(ss.kube), check.WithFeatures(ss.features), check.WithCRDs(&ss.crds))
}

// mode is the mode in which the values of r are dyn.
func mode(r check.Root) assume.Mode {
	if r.Kind == cellens.CRDKind {
		return assume.Rules
	}
	return assume.Policies
}

// dynPaths returns the paths, as a .cellens writes them, from r to the
// values below it that are dyn, at most limit of them: "" for r itself.
func dynPaths(r check.Root, limit int) []string {
	if len(r.Schemas) == 0 {
		return []string{""}
	}
	var out []string
	for _, s := range r.Schemas {
		for _, p := range assume.DynPaths(s, mode(r), limit) {
			if w := pathText(p); !slices.Contains(out, w) && len(out) < limit {
				out = append(out, w)
			}
		}
	}
	return out
}

// pathText writes the steps of a path as a .cellens path does.
func pathText(steps []string) string {
	var b strings.Builder
	for _, step := range steps {
		if step != assume.Items && step != assume.Values && b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(step)
	}
	return b.String()
}

// dynCount says how many dyn values there are: "2 dyn values".
func dynCount(n int) string {
	switch n {
	case 0:
		return "no dyn value"
	case 1:
		return "1 dyn value"
	}
	return strconv.Itoa(n) + " dyn values"
}

// valueItems are the documents, versions, variables or paths the key of the
// entry at s can name in the file its entry names, those with dyn values
// first.
func (ss *session) valueItems(ctx context.Context, l *lens, s cellens.Spot, line []rune, col int) ([]lensItem, error) {
	key, ok := l.targetOf(s)
	if !ok {
		return nil, nil
	}
	roots, err := ss.roots(ctx, key)
	if err != nil {
		return nil, err
	}
	docKind, docName, _ := strings.Cut(strings.TrimSpace(s.Entry["document"]), " ")
	var mine []check.Root
	for _, r := range roots {
		if r.Kind == docKind && r.Name == strings.TrimSpace(docName) {
			mine = append(mine, r)
		}
	}
	type ranked struct {
		item lensItem
		rank int
	}
	var out []ranked
	switch s.Key {
	case "document":
		counts := map[string]int{}
		var docs []string
		for _, r := range roots {
			doc := r.Kind + " " + r.Name
			if _, seen := counts[doc]; !seen {
				docs = append(docs, doc)
			}
			counts[doc] += len(dynPaths(r, maxDynItems))
		}
		for _, d := range docs {
			out = append(out, ranked{valueItem(s, d, dynCount(counts[d]), itemReference), min(counts[d], 1)})
		}
	case "version", "variable":
		for _, r := range mine {
			n := len(dynPaths(r, maxDynItems))
			name, detail := r.Version, dynCount(n)
			if r.Variable != "" {
				name, detail = r.Variable, r.Type
				if len(r.Schemas) > 0 && n > 0 {
					detail += " · " + dynCount(n)
				}
			}
			rank := min(n, 1)
			if len(r.Schemas) == 0 {
				rank = 2
			}
			icon := itemValue
			if r.Variable != "" {
				icon = itemVariable
			}
			out = append(out, ranked{valueItem(s, name, detail, icon), rank})
		}
	case "path":
		return ss.pathItems(l, s, mine, line, col), nil
	}
	slices.SortStableFunc(out, func(a, b ranked) int { return b.rank - a.rank })
	items := make([]lensItem, len(out))
	for i, r := range out {
		items[i] = r.item
	}
	return items, nil
}

// pathItems are the paths the entry at s can name below its root, among
// mine: the dyn values first, but those another entry of l types, then the
// values one step below the path written so far that have dyn values below
// them, the way to those values. A typed value, or one no rule reads, is
// none an entry can name.
func (ss *session) pathItems(l *lens, s cellens.Spot, mine []check.Root, line []rune, col int) []lensItem {
	var root check.Root
	found := false
	for _, r := range mine {
		if r.Version == strings.TrimSpace(s.Entry["version"]) && r.Variable == strings.TrimSpace(s.Entry["variable"]) {
			root, found = r, true
		}
	}
	if !found {
		return nil
	}
	typed := map[string]bool{}
	for _, e := range l.entries {
		if e.Line != s.EntryLine+1 && l.target(e) == path.Join(path.Dir(l.key), path.Clean(s.Entry["file"])) &&
			e.Document.Kind+" "+e.Document.Name == strings.Join(strings.Fields(s.Entry["document"]), " ") &&
			e.Version == root.Version && e.Variable == root.Variable {
			typed[e.Path.String()] = true
		}
	}
	var out []lensItem
	offered := map[string]bool{}
	for _, p := range dynPaths(root, maxDynItems) {
		if p != "" && !typed[p] {
			offered[p] = true
			out = append(out, valueItem(s, p, "dyn", itemField))
		}
	}
	written := string(line[s.Start:min(max(col, s.Start), s.End)])
	parent := written[:max(strings.LastIndexAny(written, ".["), 0)]
	steps, err := cellens.ParsePath(parent)
	if err != nil {
		return out
	}
	var at []string
	for _, st := range steps {
		switch st.Kind {
		case cellens.FieldStep:
			at = append(at, st.Name)
		case cellens.ItemsStep:
			at = append(at, assume.Items)
		case cellens.ValuesStep:
			at = append(at, assume.Values)
		}
	}
	for _, sch := range root.Schemas {
		below, ok := assume.Below(sch, at)
		if !ok {
			continue
		}
		for _, c := range assume.Children(below) {
			if strings.ContainsAny(c.Step, ".[]") && c.Step != assume.Items && c.Step != assume.Values {
				continue // no path can name it
			}
			p := pathText(append(slices.Clone(at), c.Step))
			if offered[p] {
				continue
			}
			n := len(assume.DynPaths(c.Schema, mode(root), maxDynItems))
			if n == 0 {
				continue
			}
			offered[p] = true
			out = append(out, valueItem(s, p, assume.TypeName(c.Schema)+" · "+dynCount(n), itemField))
		}
	}
	return out
}

// typeItems are the CEL type names where the type at s writes one: none in
// a struct where a field's name goes, and only string for a map's key.
// snippets is true when the client takes snippets: list(), map(string, ) and
// {} then put the cursor inside. In an entry for a CustomResourceDefinition,
// crd, they are int and string, what an x-kubernetes-int-or-string value
// takes.
func typeItems(s cellens.Spot, line []rune, col int, snippets, crd bool) []lensItem {
	value := []rune(string(line[s.Start:s.End]))
	ws, we, ok := cellens.TypeNameAt(string(value), col-s.Start)
	if !ok {
		return nil
	}
	mapKey := strings.HasSuffix(strings.TrimRight(string(value[:ws]), " "), "map(")
	pad := ""
	if s.Pad && ws == 0 {
		pad = " "
	}
	at := func(it lensItem) lensItem {
		it.start, it.end, it.set = s.Start+ws, s.Start+we, true
		it.text = pad + it.text
		return it
	}
	var out []lensItem
	for _, w := range celTypes {
		if (crd || mapKey) && w.Name != "string" && (mapKey || w.Name != "int") {
			continue
		}
		out = append(out, at(lensItem{label: w.Name, detail: w.Doc, text: w.Name, kind: itemTypeParam}))
	}
	if crd || mapKey {
		return out
	}
	list, mapOf, object := "list()", "map(string, )", "{}"
	if snippets {
		list, mapOf, object = "list($1)", "map(string, $1)", "{$1}"
	}
	out = append(out,
		at(lensItem{label: "list(T)", detail: "A list of T.", text: list, kind: itemTypeParam, snippet: snippets}),
		at(lensItem{label: "map(string, T)", detail: "A map from strings to T.", text: mapOf, kind: itemTypeParam, snippet: snippets}))
	switch {
	case s.Quote != 0:
		out = append(out, at(lensItem{label: "{field: T}", detail: "An object with these fields.", text: object, kind: itemStruct, snippet: snippets}))
	case strings.TrimSpace(string(value)) == "":
		// Unquoted, YAML reads {...} as a mapping: the struct comes quoted.
		out = append(out, at(lensItem{label: "{field: T}", detail: "An object with these fields.", text: `"` + object + `"`, kind: itemStruct, snippet: snippets}))
	}
	return out
}

// kindItems are the kinds a kind: entry can name: the CustomResourceDefinitions'
// first, then the built-in ones, those of alpha and beta API versions last.
func (ss *session) kindItems(s cellens.Spot) []lensItem {
	type ranked struct {
		item lensItem
		rank int
	}
	var out []ranked
	if ss.features.Has(feature.CRDTypedPolicies) {
		for _, k := range ss.crds.Kinds() {
			gv := k.Version
			if k.Group != "" {
				gv = k.Group + "/" + k.Version
			}
			out = append(out, ranked{valueItem(s, gv+" "+k.Kind, oneLine(k.Description, "Defined by CustomResourceDefinition "+k.CRD+"."), itemClass), 0})
		}
	}
	for _, k := range matchkind.Kinds(ss.kube.Minor()) {
		gv := k.GVK.GroupVersion().String()
		rank := map[string]int{"ga": 1, "beta": 2, "alpha": 3}[stability(k.GVK.Version)]
		out = append(out, ranked{valueItem(s, gv+" "+k.GVK.Kind, oneLine(k.Description, "The built-in kind "+gv+" "+k.GVK.Kind+"."), itemClass), rank})
	}
	slices.SortStableFunc(out, func(a, b ranked) int { return a.rank - b.rank })
	items := make([]lensItem, len(out))
	for i, r := range out {
		items[i] = r.item
	}
	return items
}

// goItems are, before the # of the go: at s, the folders of the Go packages
// of the workspace with struct types a go: can name, relative to the folder
// of l, and after it the struct types of the folder written before it. The
// folders come from the last listing of the workspace's Go types: when there
// is none, or a .go file or folder changed since, listGo starts one in the
// background and incomplete is true, so the client asks again.
func (ss *session) goItems(ctx context.Context, l *lens, s cellens.Spot, line []rune, col int) (items []lensItem, incomplete bool) {
	written := string(line[s.Start:min(max(col, s.Start), s.End)])
	dir := path.Dir(l.key)
	rootDir, rootKey := ss.goRoot(l.key)
	if folder, _, ok := strings.Cut(written, "#"); ok {
		g, err := cellens.ParseGo(folder + "#T")
		if err != nil {
			return nil, false
		}
		hash := s.Start + len([]rune(folder)) + 1
		var out []lensItem
		for _, st := range goStructs(path.Join(dir, g.Folder), rootDir, rootKey) {
			it := lensItem{label: st.Name, detail: oneLine(st.Doc, ""), text: st.Name, kind: itemStruct}
			it.start, it.end, it.set = hash, s.End, true
			out = append(out, it)
		}
		return out, false
	}
	if !ss.goFresh() {
		ss.listGo(ctx)
		incomplete = true
	}
	byFolder := map[string][]string{} // the struct types, by the URI of their folder
	for _, f := range ss.goFiles {
		for _, st := range f.structs {
			byFolder[f.folder] = append(byFolder[f.folder], st.Name)
		}
	}
	types := map[string][]string{} // by the folder as a go: writes it
	for folder, names := range byFolder {
		key := crdKey(folder)
		if key != rootKey && !below(key, rootKey) {
			continue
		}
		rel, err := filepath.Rel(filepath.FromSlash(dir), filepath.FromSlash(key))
		if err != nil {
			continue
		}
		name := strings.TrimSuffix((&cellens.GoType{Folder: filepath.ToSlash(rel)}).String(), "#")
		types[name] = append(types[name], names...)
	}
	var out []lensItem
	for _, f := range slices.Sorted(maps.Keys(types)) {
		slices.Sort(types[f])
		out = append(out, lensItem{label: f, detail: cut(strings.Join(types[f], ", "), maxGoTypeDetail), text: valueText(s, f+"#"), kind: itemFolder, suggest: true})
	}
	return out, incomplete
}

// goStructs returns the exported struct types with a json-tagged field that
// the .go files of the folder with the key declare, but _test.go files, read
// as goType reads them, through the workspace folder rootDir, whose key is
// rootKey.
func goStructs(folderKey, rootDir, rootKey string) []goschema.Struct {
	if folderKey != rootKey && !below(folderKey, rootKey) {
		return nil
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	rel := strings.TrimPrefix(strings.TrimPrefix(folderKey, rootKey), "/")
	if rel == "" {
		rel = "."
	}
	fsys := root.FS()
	names, _, err := goFiles(fsys, rel)
	if err != nil {
		return nil
	}
	var out []goschema.Struct
	for _, n := range names {
		if data, err := readFS(fsys, path.Join(rel, n)); err == nil {
			out = append(out, goschema.Structs(n, data)...)
		}
	}
	slices.SortFunc(out, func(a, b goschema.Struct) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// lensHover answers textDocument/hover in the .cellens at uri: on the file,
// document, version, variable or path of an entry, the value it names and
// whether Kubernetes leaves it dyn; on its kind, go or schema, the type that
// gives, in the short form of the line above a rule.
func (ss *session) lensHover(ctx context.Context, uri string, pos position) (any, error) {
	l := ss.lenses[crdKey(uri)]
	if l == nil {
		return nil, nil
	}
	lines := splitLines(l.text)
	at := fromPosition(lines, pos)
	s, ok := cellens.Locate(l.text, at.Line-1, at.Column-1)
	if !ok || s.Entry == nil || len(s.Path) != 2 || s.Item {
		return nil, nil
	}
	var md string
	var err error
	switch s.KeyAt {
	case "file", "document", "version", "variable", "path":
		md, err = ss.valueHover(ctx, l, s)
	case "kind", "go", "schema":
		md = ss.typeHover(ctx, l, s)
	}
	if err != nil || md == "" {
		return nil, err
	}
	out := map[string]any{"contents": map[string]any{"kind": "markdown", "value": md}}
	if s.Start < s.End {
		out["range"] = toRange(lines, extract.Position{Line: s.Line + 1, Column: s.Start + 1}, extract.Position{Line: s.Line + 1, Column: s.End + 1})
	}
	return out, nil
}

// parsed returns the entry of l that starts at the line of the entry at s,
// as the reader read it, and false when it has a problem.
func (l *lens) parsed(s cellens.Spot) (cellens.Entry, bool) {
	for _, e := range l.entries {
		if e.Line == s.EntryLine+1 {
			return e, true
		}
	}
	return cellens.Entry{}, false
}

// valueName is the value e names as CEL reads it, as the line above a rule
// names it: self.spec.config, params.max.
func valueName(e cellens.Entry) string {
	root := e.Variable
	if e.Document.Kind == cellens.CRDKind {
		root = "self"
	}
	if len(e.Path) == 0 {
		return root
	}
	if e.Path[0].Kind != cellens.FieldStep {
		return root + e.Path.String()
	}
	return root + "." + e.Path.String()
}

// valueHover describes the value the entry at s names: its type as
// Kubernetes gives it, and where it is. It is "" when the entry does not name
// a value yet.
func (ss *session) valueHover(ctx context.Context, l *lens, s cellens.Spot) (string, error) {
	e, problems := cellens.ReadValue(s.Entry)
	if len(problems) > 0 {
		return "", nil
	}
	where := e.Document.Kind + " " + e.Document.Name
	if e.Version != "" {
		where += ", version " + e.Version
	}
	where += ", in " + e.File + "."
	var typ, why string
	data, err := ss.targetText(l.target(e))
	if err != nil {
		why = fmt.Sprintf("The file %s %s.", e.File, unreadable(err))
	} else {
		roots, err := check.Roots(ctx, data, check.WithVersion(ss.kube), check.WithFeatures(ss.features), check.WithCRDs(&ss.crds))
		if err != nil {
			return "", err
		}
		i := slices.IndexFunc(roots, func(r check.Root) bool {
			return r.Kind == e.Document.Kind && r.Name == e.Document.Name && r.Version == e.Version && r.Variable == e.Variable
		})
		if i < 0 {
			why = "The file has no such value: see the problem on the entry."
		} else {
			typ, why = kubeType(roots[i], e.Path)
		}
	}
	md := "```cel\n(value) " + valueName(e)
	if typ != "" {
		md += ": " + typ
	}
	md += "\n```\n" + where + "\n\n" + why
	if typ == "dyn" {
		if pe, ok := l.parsed(s); ok {
			if t, _, msg := ss.assumedType(ctx, l, pe); msg == "" {
				md += " This entry gives it `" + inline.ShortType(t.Type) + "`."
			}
		}
	}
	return md, nil
}

// kubeType returns the CEL type Kubernetes gives the value p leads to from
// the root r, and a sentence saying why it is dyn, or that an entry does
// not apply to it.
func kubeType(r check.Root, p cellens.Path) (typ, why string) {
	var steps []string
	for _, st := range p {
		switch st.Kind {
		case cellens.FieldStep:
			steps = append(steps, st.Name)
		case cellens.ItemsStep:
			steps = append(steps, assume.Items)
		case cellens.ValuesStep:
			steps = append(steps, assume.Values)
		}
	}
	if len(r.Schemas) == 0 {
		if len(steps) > 0 {
			return "", "It is below " + r.Variable + ", which is dyn: nothing below it is known, so type " + r.Variable + " itself."
		}
		why := map[string]string{
			"params":    "the kind of spec.paramKind is not known",
			"object":    "the kinds the policy matches are not all known",
			"oldObject": "the kinds the policy matches are not all known",
		}[r.Variable]
		return "dyn", "Kubernetes leaves it dyn: " + cmp.Or(why, "the API server gives this variable no type") + "."
	}
	for _, sch := range r.Schemas {
		s, ok := assume.Below(sch, steps)
		if !ok {
			continue
		}
		switch {
		case mode(r).IsDyn(s) && flag(s, "x-kubernetes-int-or-string"):
			return "dyn", "Kubernetes leaves it dyn: it is x-kubernetes-int-or-string, an int or a string."
		case mode(r).IsDyn(s) && flag(s, "x-kubernetes-preserve-unknown-fields"):
			return "dyn", "Kubernetes leaves it dyn: it keeps unknown fields (x-kubernetes-preserve-unknown-fields)."
		case mode(r).IsDyn(s):
			return "dyn", "Kubernetes leaves it dyn: its schema gives it no type."
		case assume.IsDyn(s):
			return "", "No rule may read it: it keeps unknown fields, which the API server gives a rule none of."
		}
		return assume.TypeName(s), "Kubernetes types it: an entry for it does not apply."
	}
	return "", "There is no such value: see the problem on the entry."
}

// typeHover describes the type the kind, go or schema of the entry at s
// gives, in the short form of the line above a rule, with where it comes
// from. It is "" when that type cannot be read.
func (ss *session) typeHover(ctx context.Context, l *lens, s cellens.Spot) string {
	var e cellens.Entry
	switch s.KeyAt {
	case "kind":
		k, err := cellens.ParseKind(s.Entry["kind"])
		if err != nil {
			return ""
		}
		e.Kind = k
	case "go":
		g, err := cellens.ParseGo(s.Entry["go"])
		if err != nil {
			return ""
		}
		e.Go = g
	case "schema":
		pe, ok := l.parsed(s)
		if !ok || pe.Schema == nil {
			return ""
		}
		e.Schema = pe.Schema
	}
	name := s.KeyAt
	if v, problems := cellens.ReadValue(s.Entry); len(problems) == 0 {
		v.Type, v.Kind, v.Schema, v.Go = e.Type, e.Kind, e.Schema, e.Go
		e, name = v, valueName(v)
	}
	t, notes, msg := ss.assumedType(ctx, l, e)
	if msg == "" && e.File != "" {
		// What the type gives the value, if the entry applies to it.
		v := valueRef{lens: l.key, file: l.target(e), entry: e}
		if e.Go != nil {
			v.goFolder = path.Join(path.Dir(l.key), e.Go.Folder)
		}
		problem, _, err := ss.valueProblem(ctx, v)
		if err != nil {
			return ""
		}
		if problem != "" {
			msg = "The entry does not apply: " + problem + "."
		}
	}
	short := t.Type
	if e.Kind == nil {
		short = inline.ShortType(t.Type)
	}
	md := "```cel\n(" + s.KeyAt + ") " + name
	if msg == "" {
		md += ": " + short
	}
	md += "\n```\n"
	var parts []string
	switch {
	case msg != "":
		parts = append(parts, msg)
	case e.Kind != nil:
		parts = append(parts, ss.kindDescription(e.Kind))
	case e.Go != nil:
		if typ, _ := ss.goType(ctx, l.key, e.Go); typ != nil {
			desc, _ := typ.Schema["description"].(string)
			parts = append(parts, strings.TrimSpace(fmt.Sprintf("The Go type %s in %s:%d. %s", e.Go.Name, typ.File, typ.Line, oneLine(desc, ""))))
		}
	}
	if msg == "" && e.Kind == nil && short != t.Type {
		parts = append(parts, "`"+t.Type+"`")
	}
	parts = append(parts, notes...)
	return md + strings.Join(parts, "\n\n")
}

// kindDescription is the first sentence of the description of the kind k,
// a built-in kind or a CustomResourceDefinition's, or where it comes from.
func (ss *session) kindDescription(k *cellens.KindRef) string {
	if bk, ok := matchkind.Lookup(k.APIVersion, k.Kind); ok {
		return oneLine(bk.Schema.Description, "The built-in kind "+k.APIVersion+" "+k.Kind+".")
	}
	for _, c := range ss.crds.Kinds() {
		if gv := strings.TrimPrefix(c.Group+"/"+c.Version, "/"); gv == k.APIVersion && c.Kind == k.Kind {
			return oneLine(c.Description, "Defined by CustomResourceDefinition "+c.CRD+".")
		}
	}
	return ""
}

// cut returns s, or its first limit-1 characters and "…" when it is longer.
func cut(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

// flag reports whether the schema s has the boolean key set to true.
func flag(s map[string]any, key string) bool {
	b, _ := s[key].(bool)
	return b
}
