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
	"sync"
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
	commandInferType   = "cel-lens.inferType"
)

// commandNames are the commands the server runs, as initialize declares them.
func commandNames() []string {
	return []string{commandTypeChoices, commandCheckType, commandSetType, commandRemoveType, commandInferType}
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
	// Selection is, for setType with a snippet, where the editor puts the
	// cursor in the .cellens once the edit is made: an empty range, or the
	// placeholder to type over.
	Selection *lspRange `json:"selection,omitempty"`
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
	// Schema is an OpenAPI schema, a JSON object; null counts as left out.
	Schema json.RawMessage `json:"schema"`
	// Go names a Go type as go: does, relative to the folder of Cellens.
	Go *string `json:"go"`
	// Snippet asks setType for a new entry to fill in, of type or schema.
	Snippet *string `json:"snippet"`
}

// namesValue reports whether a names a value, not only a type.
func (a valueArgs) namesValue() bool { return a.Cellens != "" || a.File != "" || a.Document != "" }

// hasSchema reports whether a gives a schema.
func (a valueArgs) hasSchema() bool {
	return len(a.Schema) > 0 && !bytes.Equal(bytes.TrimSpace(a.Schema), []byte("null"))
}

// types are the keys of a that give a type: type, kind, schema and go, in
// that order.
func (a valueArgs) types() []string {
	var out []string
	for i, given := range []bool{a.Type != nil, a.Kind != nil, a.hasSchema(), a.Go != nil} {
		if given {
			out = append(out, []string{"type", "kind", "schema", "go"}[i])
		}
	}
	return out
}

func invalid(msg string) error { return &rpcError{jsonrpc.CodeInvalidParams, msg} }

func failed(msg string) error { return &rpcError{jsonrpc.CodeRequestFailed, msg} }

// executeCommand runs a command of workspace/executeCommand. checkType
// answers at once. typeChoices answers once the Go types of the workspace
// are listed and, with a value, its examples are found, and inferType once
// its work ends: both run in a goroutine (see startJob). setType and removeType send the client a workspace/applyEdit
// and answer when it answers (see answered): nothing is written to disk but
// by the client.
func (ss *session) executeCommand(ctx context.Context, msg jsonrpc.Message, reply func(any, error) error) error {
	var p struct {
		Command   string            `json:"command"`
		Arguments []json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return reply(nil, badParams("workspace/executeCommand", err))
	}
	var a valueArgs
	if len(p.Arguments) > 0 && p.Command != commandInferType {
		if err := json.Unmarshal(p.Arguments[0], &a); err != nil {
			return reply(nil, badParams(p.Command, err))
		}
	}
	var edit *workspaceEdit
	var err error
	switch p.Command {
	case commandTypeChoices:
		return ss.answerTypeChoices(ctx, msg, a, reply)
	case commandInferType:
		return ss.startJob(ctx, msg, jobSpec{command: commandInferType, key: string(msg.Params), work: func(ctx context.Context) (any, error) {
			res, err := inferType(ctx, p.Arguments)
			if err != nil {
				return nil, err
			}
			return res, nil
		}})
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
	if edit.edit == nil {
		return reply(edit.result, nil) // nothing to change
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

// answerTypeChoices answers the request msg with the kinds a kind: entry can
// name, the Go types of the workspace a go: entry can name (see goChoices)
// and, when a names a value, the documents of the workspace of the value's
// kind, as examples for inferType (see exampleSearch). A job lists the Go
// types and searches for the examples, the two at once, off the message
// loop; a request for the same value while it runs waits for it. Requests
// without a value and with one are in two slots (see jobSpec), so the editor
// can ask for both at once.
func (ss *session) answerTypeChoices(ctx context.Context, msg jsonrpc.Message, a valueArgs, reply func(any, error) error) error {
	answer := ss.typeChoices()
	var search *exampleSearch
	slot, key := "kinds", ""
	if a.namesValue() {
		v, err := ss.readValue(commandTypeChoices, a)
		if err != nil {
			return reply(nil, err)
		}
		s := ss.exampleSearch(ctx, v)
		search = &s
		e := v.entry
		slot, key = "examples", strings.Join([]string{v.file, e.Document.Kind, e.Document.Name, e.Version, e.Variable, e.Path.String()}, "\x00")
	}
	list := ss.goLister
	if list == nil {
		list = goChoices
	}
	dirFS, folders, known, gen := ss.dirFS(), slices.Clone(ss.folders), ss.goFiles, ss.goGen
	return ss.startJob(ctx, msg, jobSpec{
		command: commandTypeChoices,
		slot:    slot,
		key:     key,
		work: func(ctx context.Context) (any, error) {
			var found choicesFound
			var searchErr error
			var wg sync.WaitGroup
			if search != nil {
				wg.Go(func() {
					examples, err := search.run(ctx)
					found.examples, searchErr = &examples, err
				})
			}
			found.listed = list(ctx, dirFS, folders, known)
			wg.Wait()
			if searchErr != nil {
				return nil, searchErr
			}
			return found, ctx.Err()
		},
		settle: func(v any) (any, error) {
			found, _ := v.(choicesFound) // what work returns
			ss.goFiles, ss.goListedAt = found.listed.files, gen
			answer.GoTypes, answer.Examples = found.listed.types, found.examples
			if found.listed.more {
				answer.GoTypesNote = fmt.Sprintf("cel-lens lists the first %d Go types of the workspace, by folder and name", maxGoChoices)
			}
			return answer, nil
		},
	})
}

// choicesFound is what the job of typeChoices finds: the Go types, and the
// examples of the value, nil without one.
type choicesFound struct {
	listed   goListed
	examples *[]example
}

// typeChoices is the answer of typeChoices: the kinds a kind: entry can name
// and the Go types a go: entry can name.
type typeChoices struct {
	KubernetesVersion string       `json:"kubernetesVersion"`
	BuiltIn           []typeChoice `json:"builtIn"`
	CRDs              []typeChoice `json:"crds"`
	CRDsNote          string       `json:"crdsNote,omitempty"`
	GoTypes           []goChoice   `json:"goTypes"`
	GoTypesNote       string       `json:"goTypesNote,omitempty"`
	// Examples are the examples of the value typeChoices was given; nil
	// without one.
	Examples *[]example `json:"examples,omitempty"`
}

// typeChoices are the kinds a kind: entry can name: the built-in kinds of the
// Kubernetes version in use, and the kinds of the CRDs the session knows,
// which it reads only while crd-typed-policies is on. answerTypeChoices adds
// the Go types and the examples.
func (ss *session) typeChoices() typeChoices {
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
	return typeChoices{KubernetesVersion: ss.kube.String(), BuiltIn: builtIn, CRDs: crds, CRDsNote: note, GoTypes: []goChoice{}}
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
	// Warning is true for what an entry would get on its line while it
	// still applies: the fields a Go type leaves out.
	Warning bool `json:"warning,omitempty"`
}

// checkType says what is wrong with the type, schema or Go type of a, or nil
// when nothing is: a type that does not parse, a schema the API server would
// refuse, a Go type that cannot be read, and, when a names a value, what its
// entry would get on the .cellens. A Go type is read relative to the
// .cellens of a, which is all a needs to name to check it alone.
func (ss *session) checkType(ctx context.Context, a valueArgs) (*typeProblem, error) {
	types := slices.DeleteFunc(a.types(), func(k string) bool { return k == "kind" })
	switch {
	case len(types) == 0:
		return nil, invalid(commandCheckType + ` takes {type: "..."}, {schema: {...}} or {go: "./api/v1#QuotaSpec"}, and the keys of a value to check the type for it`)
	case len(types) > 1:
		return nil, invalid(fmt.Sprintf("%s: give one of type, schema and go, not %s", commandCheckType, strings.Join(types, " and ")))
	}
	var e cellens.Entry
	switch types[0] {
	case "type":
		t, err := cellens.ParseType(*a.Type)
		if te := (*cellens.TypeError)(nil); errors.As(err, &te) {
			return &typeProblem{Message: te.Message, Column: te.Offset + 1}, nil
		}
		e.Type = t
	case "schema":
		s, problems := cellens.ReadSchema(ctx, a.Schema)
		if len(problems) > 0 {
			return &typeProblem{Message: "schema: " + strings.Join(problems, "; schema: ")}, nil
		}
		e.Schema = s
	case "go":
		g, err := cellens.ParseGo(*a.Go)
		if p := parseProblem(err); p != nil {
			return p, nil
		}
		e.Go = g
		if a.File == "" && a.Document == "" {
			return ss.checkGo(ctx, a, g)
		}
	}
	if !a.namesValue() {
		return nil, nil
	}
	v, err := ss.readValue(commandCheckType, a)
	if err != nil {
		return nil, err
	}
	v.setType(e)
	msg, notes, err := ss.valueProblem(ctx, v)
	switch {
	case err != nil || msg != "":
		if err != nil {
			return nil, err
		}
		return &typeProblem{Message: msg}, nil
	case len(notes) > 0:
		return &typeProblem{Message: strings.Join(notes, "; "), Warning: true}, nil
	}
	return nil, nil
}

// parseProblem is the problem of a type that err says does not parse, nil
// for none.
func parseProblem(err error) *typeProblem {
	if err == nil {
		return nil
	}
	return &typeProblem{Message: err.Error()}
}

// checkGo says what is wrong with the Go type g, relative to the .cellens of
// a, alone: why it cannot be read, or, as a warning, the fields it leaves out.
func (ss *session) checkGo(ctx context.Context, a valueArgs, g *cellens.GoType) (*typeProblem, error) {
	key, err := ss.lensArg(commandCheckType, a.Cellens)
	if err != nil {
		return nil, err
	}
	typ, msg := ss.goType(ctx, key, g)
	switch {
	case msg != "":
		return &typeProblem{Message: msg}, nil
	case len(typ.Problems) > 0:
		return &typeProblem{Message: "go: " + strings.Join(typ.Problems, "; go: "), Warning: true}, nil
	}
	return nil, nil
}

// valueRef is a value a command names: the key of the .cellens file its keys
// are relative to, the key of the YAML file it is in, its entry, and its name
// for messages; for an entry with a Go type, also the key of that type's
// folder, which in writes relative to the .cellens it goes in.
type valueRef struct {
	lens, file string
	entry      cellens.Entry
	name       string
	goFolder   string
}

// setType gives the entry of v the type of e, one of its type, kind, schema
// and Go type.
func (v *valueRef) setType(e cellens.Entry) {
	v.entry.Type, v.entry.Kind, v.entry.Schema, v.entry.Go = e.Type, e.Kind, e.Schema, e.Go
	if e.Go != nil {
		v.goFolder = path.Join(path.Dir(v.lens), e.Go.Folder)
	}
}

// readValue reads the value a names, as an entry of the .cellens it names
// would name it. That .cellens must be in a workspace folder, or beside a file
// the editor has open, so a command reads and edits files of the workspace
// only.
func (ss *session) readValue(command string, a valueArgs) (valueRef, error) {
	key, err := ss.lensArg(command, a.Cellens)
	if err != nil {
		return valueRef{}, err
	}
	e, problems := cellens.ReadValue(map[string]string{"file": a.File, "document": a.Document, "version": a.Version, "variable": a.Variable, "path": a.Path})
	if len(problems) > 0 {
		msgs := make([]string, len(problems))
		for i, p := range problems {
			msgs[i] = p.Message
		}
		return valueRef{}, invalid(command + ": " + strings.Join(msgs, "; "))
	}
	v := valueRef{lens: key, file: path.Join(path.Dir(key), e.File), entry: e, name: a.Value}
	if v.name == "" {
		v.name = strings.TrimSuffix(e.Variable+"."+e.Path.String(), ".")
		if e.Document.Kind == cellens.CRDKind {
			v.name = strings.TrimSuffix("self."+e.Path.String(), ".")
		}
	}
	return v, nil
}

// lensArg returns the key of the .cellens file with the URI uri, the
// argument cellens of command, which must be in a workspace folder or beside
// a file the editor has open.
func (ss *session) lensArg(command, uri string) (string, error) {
	if u, err := url.Parse(uri); err != nil || u.Scheme != "file" || !isLens(uri) {
		return "", invalid(fmt.Sprintf("%s: cellens %q is not the file URI of a .cellens, such as file:///repo/.cellens", command, uri))
	}
	if !ss.inWorkspace(crdKey(uri)) {
		return "", invalid(fmt.Sprintf("%s: cellens %q is in no workspace folder and beside no file open in the editor", command, uri))
	}
	return crdKey(uri), nil
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
// file relative to that file's folder, which is at or above it, and its Go
// type's folder relative to that folder.
func (v valueRef) in(lensKey string) cellens.Entry {
	e := v.entry
	dir := path.Dir(lensKey)
	prefix := dir
	if prefix != "/" {
		prefix += "/"
	}
	e.File = strings.TrimPrefix(v.file, prefix)
	if e.Go != nil {
		g := *e.Go
		if rel, err := filepath.Rel(filepath.FromSlash(dir), filepath.FromSlash(v.goFolder)); err == nil {
			g.Folder = filepath.ToSlash(rel)
		}
		e.Go = &g
	}
	return e
}

// valueProblem says why the entry of v would not apply to its value, as the
// warning on its line of the .cellens would: its file cannot be read or has
// no such document or value, the value is not dyn, its kind or Go type is not
// known, or its type does not fit the value. It is "" when the entry would
// apply. notes are what the entry would get on its line all the same: the
// fields its Go type leaves out.
func (ss *session) valueProblem(ctx context.Context, v valueRef) (msg string, notes []string, err error) {
	l := &lens{key: v.lens}
	e := v.in(v.lens)
	data, err := ss.targetText(v.file)
	if err != nil {
		return fmt.Sprintf("file %s %s", e.File, unreadable(err)), nil, nil
	}
	t, notes, msg := ss.assumedType(ctx, l, e)
	if msg != "" {
		return msg, nil, nil
	}
	problems, err := check.AssumedTypeProblems(ctx, data, check.WithAssumedTypes([]check.AssumedType{t}), check.WithVersion(ss.kube), check.WithFeatures(ss.features), check.WithCRDs(&ss.crds))
	if err != nil || len(problems) == 0 {
		return "", notes, err
	}
	return problems[0].Message, nil, nil
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
	types := a.types()
	if a.Snippet != nil && len(types) == 0 {
		return ss.setSnippet(ctx, a)
	}
	switch {
	case a.Snippet != nil:
		return nil, invalid(fmt.Sprintf("%s: give snippet or the type, not both", commandSetType))
	case len(types) > 1:
		return nil, invalid(fmt.Sprintf("%s: give one of type, kind, schema and go, not %s", commandSetType, strings.Join(types, " and ")))
	case len(types) == 0:
		return nil, invalid(commandSetType + `: give the type, such as type: "{name: string}", the kind, such as kind: "apps/v1 Deployment", the schema, such as schema: {"type": "object", "properties": {...}}, or the Go type, such as go: "./api/v1#QuotaSpec"`)
	case !ss.edits.apply:
		return nil, failed("the editor cannot apply an edit for cel-lens (workspace/applyEdit), which is how cel-lens writes .cellens: write the entry by hand")
	}
	v, err := ss.readValue(commandSetType, a)
	if err != nil {
		return nil, err
	}
	var e cellens.Entry
	switch types[0] {
	case "type":
		if e.Type, err = cellens.ParseType(*a.Type); err != nil {
			return nil, invalid(commandSetType + ": type: " + err.Error())
		}
	case "kind":
		if e.Kind, err = cellens.ParseKind(*a.Kind); err != nil {
			return nil, invalid(commandSetType + ": " + err.Error())
		}
	case "schema":
		var problems []string
		if e.Schema, problems = cellens.ReadSchema(ctx, a.Schema); len(problems) > 0 {
			return nil, invalid(commandSetType + ": schema: " + strings.Join(problems, "; schema: "))
		}
	case "go":
		if e.Go, err = cellens.ParseGo(*a.Go); err != nil {
			return nil, invalid(commandSetType + ": " + err.Error())
		}
	}
	v.setType(e)
	if msg, _, err := ss.valueProblem(ctx, v); err != nil || msg != "" {
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
	return &workspaceEdit{label: "Give " + v.name + " a type", key: f.key, path: ss.lensSource(&lens{key: f.key}), edit: edit, result: commandResult{Cellens: f.uri, Change: change}}, nil
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
		return &workspaceEdit{label: label, key: f.key, path: source, edit: edit, result: commandResult{Cellens: f.uri, Change: "deleted"}}, nil
	}
	edit, err := ss.textEdit(f, ed)
	if err != nil {
		return nil, err
	}
	return &workspaceEdit{label: label, key: f.key, path: source, edit: edit, result: commandResult{Cellens: f.uri, Change: "removed"}}, nil
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
	if found, err := ss.cancelJob(p.ID); found || err != nil {
		return err
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

// snippet is a placeholder type setType writes for a snippet: the entry's
// type and where the cursor goes in the text of its type: after the first
// cursor characters, selecting the next selected.
type snippet struct {
	entry            cellens.Entry
	text             string // the type's text as the entry writes it
	cursor, selected int
}

// snippetFor returns the placeholder of the snippet kind, type or schema,
// for a value of a document of the kind: in a CustomResourceDefinition, whose
// x-kubernetes-int-or-string values take an int or a string, int and
// integer, selected; in a policy an empty struct, with the cursor in it.
func snippetFor(ctx context.Context, kind, document string) (snippet, error) {
	crd := document == cellens.CRDKind
	switch kind {
	case "type":
		if crd {
			t, _ := cellens.ParseType("int")
			return snippet{cellens.Entry{Type: t}, "type: int", len("type: "), len("int")}, nil
		}
		t, _ := cellens.ParseType("{}")
		return snippet{cellens.Entry{Type: t}, `type: "{}"`, len(`type: "{`), 0}, nil
	case "schema":
		js, text, cursor, selected := `{"type": "object", "properties": {}}`, "properties: {}", len("properties: {"), 0
		if crd {
			js, text, cursor, selected = `{"type": "integer"}`, "type: integer", len("type: "), len("integer")
		}
		s, problems := cellens.ReadSchema(ctx, []byte(js))
		if len(problems) > 0 {
			return snippet{}, fmt.Errorf("the schema of the snippet: %s", strings.Join(problems, "; "))
		}
		return snippet{cellens.Entry{Schema: s}, text, cursor, selected}, nil
	}
	return snippet{}, invalid(fmt.Sprintf("%s: snippet %q: use type or schema", commandSetType, kind))
}

// setSnippet adds an entry for the value a names with a placeholder type of
// the kind a.Snippet names, for the person to fill in, where setType would
// add it, and answers where the editor puts the cursor. A value that has an
// entry keeps it: the answer is then where that entry's type starts.
func (ss *session) setSnippet(ctx context.Context, a valueArgs) (*workspaceEdit, error) {
	v, err := ss.readValue(commandSetType, a)
	if err != nil {
		return nil, err
	}
	sn, err := snippetFor(ctx, *a.Snippet, v.entry.Document.Kind)
	if err != nil {
		return nil, err
	}
	files, root, err := ss.lensFiles(v.file)
	if err != nil {
		return nil, failed(err.Error())
	}
	if err := ss.waiting(append(files, lensFile{key: path.Join(root, cellens.FileName)})); err != nil {
		return nil, err
	}
	if f, ok := owner(files, v); ok {
		off, _ := cellens.TypeAt(f.text, v.in(f.key))
		at := offsetPosition(f.text, off)
		return &workspaceEdit{result: commandResult{Cellens: f.uri, Change: "unchanged", Selection: &lspRange{at, at}}}, nil
	}
	if !ss.edits.apply {
		return nil, failed("the editor cannot apply an edit for cel-lens (workspace/applyEdit), which is how cel-lens writes .cellens: write the entry by hand")
	}
	v.setType(sn.entry)
	if msg, _, err := ss.valueProblem(ctx, v); err != nil || msg != "" {
		if err != nil {
			return nil, err
		}
		return nil, invalid(fmt.Sprintf("%s: an entry for %s would not apply: %s", commandSetType, v.name, msg))
	}
	var f lensFile
	change := "added"
	if len(files) > 0 {
		f = files[0]
	} else {
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
	text := ed.Apply(f.text)
	at := ed.Start + strings.LastIndex(ed.Text, sn.text) + sn.cursor
	sel := lspRange{offsetPosition(text, at), offsetPosition(text, at+sn.selected)}
	src := ss.lensSource(&lens{key: f.key})
	return &workspaceEdit{label: "Give " + v.name + " a type", key: f.key, path: src, edit: edit, result: commandResult{Cellens: f.uri, Change: change, Selection: &sel}}, nil
}
