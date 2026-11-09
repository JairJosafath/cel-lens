package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// The commands of workspace/executeCommand, which the VS Code extension calls
// with the arguments the lines above a rule give its own commands (see
// docs/engine.md, "Commands").
const (
	commandTypeChoices = "cel-lens.typeChoices"
	commandCheckType   = "cel-lens.checkType"
	commandSetType     = "cel-lens.setType"
	commandRemoveType  = "cel-lens.removeType"
)

// commandNames are the commands the server runs, as initialize declares them.
func commandNames() []string {
	return []string{commandTypeChoices, commandCheckType, commandSetType, commandRemoveType}
}

// editCaps are what the client can do with a workspace/applyEdit.
type editCaps struct {
	apply, documentChanges, create, delete bool
}

// pendingEdit is a workspace/applyEdit the server sent for a command, which
// answers once the client answers it.
type pendingEdit struct {
	id     json.RawMessage // the workspace/executeCommand request
	key    string          // the key of the .cellens file it edits
	path   string          // that file, as messages name it
	result commandResult
	// cancelled is true once the command was answered as cancelled: the
	// edit waits for the client's answer all the same, as the client may
	// still apply it.
	cancelled bool
}

// commandResult is what setType and removeType answer: the URI of the
// .cellens file they changed and how: added, replaced or created for setType,
// removed or deleted for removeType.
type commandResult struct {
	Cellens string `json:"cellens"`
	Change  string `json:"change"`
}

// valueArgs are the arguments of checkType, setType and removeType: the keys
// the lines above a rule give cel-lens.giveType, editType and removeType, and
// the type or kind the person chose.
type valueArgs struct {
	// Cellens is the URI of the .cellens file File is relative to.
	Cellens string `json:"cellens"`
	// File, Document, Version, Variable and Path name the value as .cellens
	// keys it.
	File     string `json:"file"`
	Document string `json:"document"`
	Version  string `json:"version"`
	Variable string `json:"variable"`
	Path     string `json:"path"`
	// Value is the value's CEL, such as self.spec.config, for messages.
	Value string  `json:"value"`
	Type  *string `json:"type"`
	Kind  *string `json:"kind"`
}

// namesValue reports whether a names a value, not only a type.
func (a valueArgs) namesValue() bool { return a.Cellens != "" || a.File != "" || a.Document != "" }

func invalid(msg string) error { return &rpcError{jsonrpc.CodeInvalidParams, msg} }

func failed(msg string) error { return &rpcError{jsonrpc.CodeRequestFailed, msg} }

// executeCommand runs a command of workspace/executeCommand. typeChoices and
// checkType answer at once. setType and removeType send the client a
// workspace/applyEdit and answer when it answers (see answered): nothing is
// written to disk but by the client.
func (ss *session) executeCommand(ctx context.Context, msg jsonrpc.Message, reply func(any, error) error) error {
	var p struct {
		Command   string            `json:"command"`
		Arguments []json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return reply(nil, badParams("workspace/executeCommand", err))
	}
	var a valueArgs
	if len(p.Arguments) > 0 && p.Command != commandTypeChoices {
		if err := json.Unmarshal(p.Arguments[0], &a); err != nil {
			return reply(nil, badParams(p.Command, err))
		}
	}
	var edit *workspaceEdit
	var err error
	switch p.Command {
	case commandTypeChoices:
		return reply(ss.typeChoices(), nil)
	case commandCheckType:
		problem, err := ss.checkType(ctx, a)
		return reply(problem, err)
	case commandSetType:
		edit, err = ss.setType(ctx, a)
	case commandRemoveType:
		edit, err = ss.removeType(a)
	default:
		return reply(nil, invalid(fmt.Sprintf("unknown command %q: cel-lens runs %s", p.Command, strings.Join(commandNames(), ", "))))
	}
	if err != nil || msg.IsNotification() {
		return reply(nil, err)
	}
	return ss.applyEdit(msg.ID, edit)
}

// typeChoice is a kind typeChoices offers.
type typeChoice struct {
	APIVersion  string `json:"apiVersion"`
	Group       string `json:"group"`
	Version     string `json:"version"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	// Stability is that of a built-in kind's API version: ga, beta or alpha.
	Stability string `json:"stability,omitempty"`
}

// stability is ga, beta or alpha for an API version such as v1, v1beta2 or
// v1alpha1.
func stability(version string) string {
	switch {
	case strings.Contains(version, "alpha"):
		return "alpha"
	case strings.Contains(version, "beta"):
		return "beta"
	}
	return "ga"
}

// typeChoices are the kinds a kind: entry can name: the built-in kinds of the
// Kubernetes version in use, and the kinds of the CRDs the session knows,
// which it reads only while crd-typed-policies is on.
func (ss *session) typeChoices() any {
	builtIn := []typeChoice{}
	for _, k := range matchkind.Kinds(ss.kube.Minor()) {
		gv := k.GVK.GroupVersion().String()
		builtIn = append(builtIn, typeChoice{gv, k.GVK.Group, k.GVK.Version, k.GVK.Kind, oneLine(k.Description, "The built-in kind "+gv+" "+k.GVK.Kind+"."), stability(k.GVK.Version)})
	}
	crds := []typeChoice{}
	note := ""
	if ss.features.Has(feature.CRDTypedPolicies) {
		for _, k := range ss.crds.Kinds() {
			gv := k.Version
			if k.Group != "" {
				gv = k.Group + "/" + k.Version
			}
			crds = append(crds, typeChoice{APIVersion: gv, Group: k.Group, Version: k.Version, Kind: k.Kind, Description: oneLine(k.Description, "Defined by CustomResourceDefinition "+k.CRD+".")})
		}
	} else {
		note = fmt.Sprintf("cel-lens reads the CustomResourceDefinitions of the workspace, and a kind: entry can name one, only with the experimental feature %s on", feature.CRDTypedPolicies)
	}
	return struct {
		KubernetesVersion string       `json:"kubernetesVersion"`
		BuiltIn           []typeChoice `json:"builtIn"`
		CRDs              []typeChoice `json:"crds"`
		CRDsNote          string       `json:"crdsNote,omitempty"`
	}{ss.kube.String(), builtIn, crds, note}
}

// oneLine is the first sentence of the first line of desc, or fallback when
// desc is empty.
func oneLine(desc, fallback string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(desc), "\n")
	if i := strings.Index(line, ". "); i >= 0 {
		line = line[:i+1]
	}
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return fallback
}

// typeProblem is what checkType finds wrong with a type.
type typeProblem struct {
	Message string `json:"message"`
	// Column is the character of the type the problem is at, from 1, in
	// Unicode code points; 0, and left out, for a type that is wrong as a
	// whole for the value.
	Column int `json:"column,omitempty"`
}

// checkType says what is wrong with the type of a, or nil when nothing is: a
// type that does not parse, and, when a names a value, what its entry would
// get on the .cellens.
func (ss *session) checkType(ctx context.Context, a valueArgs) (*typeProblem, error) {
	if a.Type == nil {
		return nil, invalid(commandCheckType + ` takes {type: "..."}, and the keys of a value to check the type for it`)
	}
	t, err := cellens.ParseType(*a.Type)
	if te := (*cellens.TypeError)(nil); errors.As(err, &te) {
		return &typeProblem{Message: te.Message, Column: te.Offset + 1}, nil
	}
	if !a.namesValue() {
		return nil, nil
	}
	v, err := ss.readValue(commandCheckType, a)
	if err != nil {
		return nil, err
	}
	v.entry.Type = t
	msg, err := ss.valueProblem(ctx, v)
	if err != nil || msg == "" {
		return nil, err
	}
	return &typeProblem{Message: msg}, nil
}

// valueRef is a value a command names: the key of the .cellens file its keys
// are relative to, the key of the YAML file it is in, its entry, and its name
// for messages.
type valueRef struct {
	lens, file string
	entry      cellens.Entry
	name       string
}

// readValue reads the value a names, as an entry of the .cellens it names
// would name it. That .cellens must be in a workspace folder, or beside a file
// the editor has open, so a command reads and edits files of the workspace
// only.
func (ss *session) readValue(command string, a valueArgs) (valueRef, error) {
	if u, err := url.Parse(a.Cellens); err != nil || u.Scheme != "file" || !isLens(a.Cellens) {
		return valueRef{}, invalid(fmt.Sprintf("%s: cellens %q is not the file URI of a .cellens, such as file:///repo/.cellens", command, a.Cellens))
	}
	if !ss.inWorkspace(crdKey(a.Cellens)) {
		return valueRef{}, invalid(fmt.Sprintf("%s: cellens %q is in no workspace folder and beside no file open in the editor", command, a.Cellens))
	}
	e, problems := cellens.ReadValue(map[string]string{"file": a.File, "document": a.Document, "version": a.Version, "variable": a.Variable, "path": a.Path})
	if len(problems) > 0 {
		msgs := make([]string, len(problems))
		for i, p := range problems {
			msgs[i] = p.Message
		}
		return valueRef{}, invalid(command + ": " + strings.Join(msgs, "; "))
	}
	key := crdKey(a.Cellens)
	v := valueRef{lens: key, file: path.Join(path.Dir(key), e.File), entry: e, name: a.Value}
	if v.name == "" {
		v.name = strings.TrimSuffix(e.Variable+"."+e.Path.String(), ".")
		if e.Document.Kind == cellens.CRDKind {
			v.name = strings.TrimSuffix("self."+e.Path.String(), ".")
		}
	}
	return v, nil
}

// inWorkspace reports whether the .cellens file with the key is below a
// workspace folder, or in the folder of a document the editor has open.
func (ss *session) inWorkspace(key string) bool {
	for _, f := range ss.folders {
		k := crdKey(pathURI(f))
		if k == "/" || strings.HasPrefix(key, strings.TrimSuffix(k, "/")+"/") {
			return true
		}
	}
	dir := path.Dir(key)
	for uri := range ss.docs {
		if path.Dir(crdKey(uri)) == dir {
			return true
		}
	}
	for _, l := range ss.lenses {
		if l.open && l.key == key {
			return true
		}
	}
	return false
}

// in returns the entry of v as the .cellens file with the key writes it: its
// file relative to that file's folder, which is at or above it.
func (v valueRef) in(lensKey string) cellens.Entry {
	e := v.entry
	dir := path.Dir(lensKey)
	if dir != "/" {
		dir += "/"
	}
	e.File = strings.TrimPrefix(v.file, dir)
	return e
}

// valueProblem says why the entry of v would not apply to its value, as the
// warning on its line of the .cellens would: its file cannot be read or has
// no such document or value, the value is not dyn, its kind is not known, or
// its type does not fit the value. It is "" when the entry would apply.
func (ss *session) valueProblem(ctx context.Context, v valueRef) (string, error) {
	l := &lens{key: v.lens}
	e := v.in(v.lens)
	data, err := ss.targetText(v.file)
	if err != nil {
		return fmt.Sprintf("file %s %s", e.File, unreadable(err)), nil
	}
	t, msg := ss.assumedType(l, e)
	if msg != "" {
		return msg, nil
	}
	problems, err := check.AssumedTypeProblems(ctx, data, check.WithAssumedTypes([]check.AssumedType{t}), check.WithVersion(ss.kube), check.WithFeatures(ss.features), check.WithCRDs(&ss.crds))
	if err != nil || len(problems) == 0 {
		return "", err
	}
	return problems[0].Message, nil
}

// workspaceEdit is a workspace/applyEdit a command asks of the client: its
// label, the edit, the .cellens file it changes as messages name it, and what
// the command answers once the client applied it.
type workspaceEdit struct {
	label, key, path string
	edit             map[string]any
	result           commandResult
}

// setType gives the value a names the type or kind of a in a .cellens: in the
// entry of the .cellens that gives it a type, or else as a new entry of the
// nearest .cellens at or above its file, or else of a new .cellens at the root
// of the workspace folder, or of the file's own folder outside every one. It
// refuses what an entry would get a problem for.
func (ss *session) setType(ctx context.Context, a valueArgs) (*workspaceEdit, error) {
	switch {
	case a.Type != nil && a.Kind != nil:
		return nil, invalid(commandSetType + ": give type or kind, not both")
	case a.Type == nil && a.Kind == nil:
		return nil, invalid(commandSetType + `: give the type, such as type: "{name: string}", or the kind, such as kind: "apps/v1 Deployment"`)
	case !ss.edits.apply:
		return nil, failed("the editor cannot apply an edit for cel-lens (workspace/applyEdit), which is how cel-lens writes .cellens: write the entry by hand")
	}
	v, err := ss.readValue(commandSetType, a)
	if err != nil {
		return nil, err
	}
	if a.Type != nil {
		t, err := cellens.ParseType(*a.Type)
		if err != nil {
			return nil, invalid(commandSetType + ": type: " + err.Error())
		}
		v.entry.Type = t
	} else {
		k, err := cellens.ParseKind(*a.Kind)
		if err != nil {
			return nil, invalid(commandSetType + ": " + err.Error())
		}
		v.entry.Kind = k
	}
	if msg, err := ss.valueProblem(ctx, v); err != nil || msg != "" {
		if err != nil {
			return nil, err
		}
		return nil, invalid(fmt.Sprintf("%s: an entry for %s would not apply: %s", commandSetType, v.name, msg))
	}
	files, root, err := ss.lensFiles(v.file)
	if err != nil {
		return nil, failed(err.Error())
	}
	if err := ss.waiting(append(files, lensFile{key: path.Join(root, cellens.FileName)})); err != nil {
		return nil, err
	}
	f, ok := owner(files, v)
	change := "replaced"
	switch {
	case !ok && len(files) > 0:
		f, change = files[0], "added"
	case !ok:
		key := path.Join(root, cellens.FileName)
		f, change = lensFile{key: key, uri: pathURI(filepath.FromSlash(key))}, "created"
	}
	ed, err := cellens.Set(f.text, v.in(f.key))
	if err != nil {
		return nil, failed(fmt.Sprintf("%s: %v", ss.lensSource(&lens{key: f.key}), err))
	}
	edit, err := ss.textEdit(f, ed)
	if err != nil {
		return nil, err
	}
	return &workspaceEdit{label: "Give " + v.name + " a type", key: f.key, path: ss.lensSource(&lens{key: f.key}), edit: edit, result: commandResult{f.uri, change}}, nil
}

// removeType removes the entry of the .cellens that gives the value a names
// its type, and that .cellens when nothing but the header would be left and
// the client can delete files.
func (ss *session) removeType(a valueArgs) (*workspaceEdit, error) {
	if !ss.edits.apply {
		return nil, failed("the editor cannot apply an edit for cel-lens (workspace/applyEdit), which is how cel-lens writes .cellens: remove the entry by hand")
	}
	v, err := ss.readValue(commandRemoveType, a)
	if err != nil {
		return nil, err
	}
	files, _, err := ss.lensFiles(v.file)
	if err != nil {
		return nil, failed(err.Error())
	}
	if err := ss.waiting(files); err != nil {
		return nil, err
	}
	f, ok := owner(files, v)
	if !ok {
		return nil, failed(fmt.Sprintf("no .cellens gives %s a type: there is no entry to remove", v.name))
	}
	ed, err := cellens.Remove(f.text, v.in(f.key))
	source := ss.lensSource(&lens{key: f.key})
	if err != nil {
		return nil, failed(fmt.Sprintf("%s: %v", source, err))
	}
	label := "Remove the type of " + v.name
	if cellens.Bare(ed.Apply(f.text)) && ss.edits.documentChanges && ss.edits.delete {
		edit := map[string]any{"documentChanges": []any{map[string]any{"kind": "delete", "uri": f.uri}}}
		return &workspaceEdit{label: label, key: f.key, path: source, edit: edit, result: commandResult{f.uri, "deleted"}}, nil
	}
	edit, err := ss.textEdit(f, ed)
	if err != nil {
		return nil, err
	}
	return &workspaceEdit{label: label, key: f.key, path: source, edit: edit, result: commandResult{f.uri, "removed"}}, nil
}

// waiting refuses a command whose edit would be made on the text of one of
// files while the editor has not answered an edit of it: the text the
// server has does not have that edit, so a second edit made on it would
// land on the wrong lines.
func (ss *session) waiting(files []lensFile) error {
	for _, p := range ss.pending {
		if slices.ContainsFunc(files, func(f lensFile) bool { return f.key == p.key }) {
			return failed(fmt.Sprintf("an edit of %s is waiting for the editor: run the command again once it is applied", p.path))
		}
	}
	return nil
}

// cancel answers a command that waits for the editor's answer to its edit,
// when the client cancels it. The edit still waits for that answer, as the
// client may apply it all the same: until it comes, the .cellens stays
// reserved (see waiting), and answered then only forgets the edit.
func (ss *session) cancel(params json.RawMessage) error {
	var p struct {
		ID json.RawMessage `json:"id"`
	}
	if !decode(params, &p) {
		return nil
	}
	for reqID, pe := range ss.pending {
		if !pe.cancelled && bytes.Equal(bytes.TrimSpace(pe.id), bytes.TrimSpace(p.ID)) {
			pe.cancelled = true
			ss.pending[reqID] = pe
			return ss.conn.ReplyError(pe.id, jsonrpc.CodeRequestCancelled, "the command was cancelled before the editor applied the edit to "+pe.path)
		}
	}
	return nil
}

// textEdit is ed, an edit of the text of f, as the client takes it: on the
// version of f the editor has open, and, for a file that does not exist,
// after creating it, which the client must support.
func (ss *session) textEdit(f lensFile, ed cellens.Edit) (map[string]any, error) {
	te := textEdit{lspRange{offsetPosition(f.text, ed.Start), offsetPosition(f.text, ed.End)}, ed.Text}
	doc := map[string]any{"uri": f.uri, "version": f.version}
	switch {
	case !f.exists && (!ss.edits.documentChanges || !ss.edits.create):
		return nil, failed(fmt.Sprintf("the entry goes in a new %s, and the editor cannot create files for cel-lens (workspace/applyEdit): create it, empty, and run the command again", f.key))
	case !f.exists:
		return map[string]any{"documentChanges": []any{
			map[string]any{"kind": "create", "uri": f.uri},
			map[string]any{"textDocument": doc, "edits": []textEdit{te}},
		}}, nil
	case ss.edits.documentChanges:
		return map[string]any{"documentChanges": []any{map[string]any{"textDocument": doc, "edits": []textEdit{te}}}}, nil
	}
	return map[string]any{"changes": map[string][]textEdit{f.uri: {te}}}, nil
}

// applyEdit asks the client to apply edit for the command request id, which
// answered answers.
func (ss *session) applyEdit(id json.RawMessage, edit *workspaceEdit) error {
	ss.editSeq++
	reqID := "cel-lens-edit-" + strconv.Itoa(ss.editSeq)
	if ss.pending == nil {
		ss.pending = map[string]pendingEdit{}
	}
	ss.pending[reqID] = pendingEdit{id: id, key: edit.key, path: edit.path, result: edit.result}
	return ss.conn.Request(reqID, "workspace/applyEdit", map[string]any{"label": edit.label, "edit": edit.edit})
}

// answered handles the client's response to a request of the server: the
// command waiting for a workspace/applyEdit gets its result when the client
// applied the edit, and an error saying so when it did not.
func (ss *session) answered(msg jsonrpc.Message) error {
	var id string
	_ = json.Unmarshal(msg.ID, &id) // a number is no id the server sent: id stays ""
	p, ok := ss.pending[id]
	if !ok {
		return nil
	}
	delete(ss.pending, id)
	if p.cancelled {
		return nil // the command was answered
	}
	var r struct {
		Applied       bool   `json:"applied"`
		FailureReason string `json:"failureReason"`
	}
	switch {
	case msg.Error != nil:
		return ss.conn.ReplyError(p.id, jsonrpc.CodeRequestFailed, fmt.Sprintf("the editor did not apply the edit to %s (%s): nothing was written", p.path, msg.Error.Message))
	case json.Unmarshal(msg.Result, &r) != nil || !r.Applied:
		why := ""
		if r.FailureReason != "" {
			why = " (" + r.FailureReason + ")"
		}
		return ss.conn.ReplyError(p.id, jsonrpc.CodeRequestFailed, fmt.Sprintf("the editor refused the edit to %s%s: nothing was written", p.path, why))
	}
	return ss.conn.Reply(p.id, p.result)
}

// dropPending answers the commands that wait for the client with an error,
// as the session shuts down.
func (ss *session) dropPending() error {
	for _, id := range slices.Sorted(maps.Keys(ss.pending)) {
		p := ss.pending[id]
		delete(ss.pending, id)
		if p.cancelled {
			continue
		}
		if err := ss.conn.ReplyError(p.id, jsonrpc.CodeRequestFailed, fmt.Sprintf("the server shut down before the editor applied the edit to %s", p.path)); err != nil {
			return err
		}
	}
	return nil
}

// lensFile is a .cellens file a command reads and edits: its key and URI, its
// text now (the editor's when it has it open, else the disk's), the editor's
// version of it when open, and whether it exists.
type lensFile struct {
	key, uri string
	text     []byte
	version  *int
	exists   bool
}

// lensFiles returns the .cellens files that may have an entry for the YAML
// file with the key: those in its folder and in each folder above it, up to
// the workspace folder that holds it, nearest first. A .cellens below a folder
// the search skips counts only while the editor has it open, as the search
// does not find it. It also returns the key of that workspace folder, or of
// the file's own folder outside every one, where a new .cellens goes.
func (ss *session) lensFiles(file string) ([]lensFile, string, error) {
	root := path.Dir(file)
	best := -1
	for _, f := range ss.folders {
		k := crdKey(pathURI(f))
		if k != "/" {
			k = strings.TrimSuffix(k, "/")
		}
		if (k == "/" || strings.HasPrefix(file, k+"/")) && len(k) > best {
			root, best = k, len(k)
		}
	}
	var out []lensFile
	for dir := path.Dir(file); ; dir = path.Dir(dir) {
		rel := strings.TrimPrefix(strings.TrimPrefix(dir, root), "/")
		hidden := rel != "" && slices.ContainsFunc(strings.Split(rel, "/"), skippedFolder)
		f, ok, err := ss.lensFileAt(path.Join(dir, cellens.FileName), hidden)
		if err != nil {
			return nil, "", err
		}
		if ok {
			out = append(out, f)
		}
		if dir == root || path.Dir(dir) == dir {
			return out, root, nil
		}
	}
}

// lensFileAt returns the .cellens file with the key, and whether there is
// one: open in the editor, or, unless skipped, a regular file on disk.
func (ss *session) lensFileAt(key string, skipped bool) (lensFile, bool, error) {
	l := ss.lenses[key]
	if l != nil && l.open {
		version := l.version
		return lensFile{key: key, uri: l.uri, text: l.text, version: &version, exists: true}, true, nil
	}
	if skipped {
		return lensFile{}, false, nil
	}
	p := filepath.FromSlash(key)
	info, err := os.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return lensFile{}, false, nil
	case err != nil:
		return lensFile{}, false, fmt.Errorf("read %s: %w", p, err)
	case !info.Mode().IsRegular():
		return lensFile{}, false, nil // not followed, as the search does not
	}
	data, err := ss.readFile(p)
	if err != nil {
		return lensFile{}, false, fmt.Errorf("read %s: %w", p, err)
	}
	f := lensFile{key: key, uri: pathURI(p), text: data, exists: true}
	if l != nil {
		f.uri = l.uri
	}
	return f, true, nil
}

// owner returns the file of files with an entry for the value of v, the one
// first by key, as that one gives the value its type.
func owner(files []lensFile, v valueRef) (lensFile, bool) {
	byKey := slices.Clone(files)
	slices.SortFunc(byKey, func(a, b lensFile) int { return strings.Compare(a.key, b.key) })
	for _, f := range byKey {
		if cellens.Has(f.text, v.in(f.key)) {
			return f, true
		}
	}
	return lensFile{}, false
}

// offsetPosition is the protocol's position of the byte offset off in text:
// a line ends at a line feed, a carriage return or both, and characters count
// in UTF-16 units.
func offsetPosition(text []byte, off int) position {
	var p position
	for i := 0; i < off && i < len(text); {
		r, size := utf8.DecodeRune(text[i:])
		switch {
		case r == '\n' || r == '\r' && (i+1 == len(text) || text[i+1] != '\n'):
			p.Line++
			p.Character = 0
		case r != '\r':
			p.Character += utf16.RuneLen(r)
		}
		i += size
	}
	return p
}
