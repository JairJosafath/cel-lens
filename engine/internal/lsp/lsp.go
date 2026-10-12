// Package lsp serves CEL language support for Kubernetes YAML files over the
// Language Server Protocol: diagnostics for every CEL expression of an open
// document, hover and completion inside them.
//
// Documents are synchronised in full. Positions use the protocol's default
// encoding, UTF-16 code units. A YAML document that does not parse is reported
// as an error on the line where parsing fails, and the CEL in the other
// documents of the file is still checked.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode/utf16"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// ErrNoShutdown is returned by Serve when the client ends the session, with
// exit or by closing the stream, without asking for shutdown first. The
// protocol makes that a failure.
var ErrNoShutdown = errors.New("client ended the session without shutdown")

// Server is a language server. The zero value is ready to use and matches
// kubeversion.Default until the client chooses a Kubernetes version with
// kubernetesVersion in initializationOptions or didChangeConfiguration.
type Server struct {
	// Version is reported to the client in the initialize result.
	Version string
}

// session is the state of one client connection.
type session struct {
	conn    *jsonrpc.Conn
	assist  *assist.Assistant
	version string
	kube    kubeversion.Version // the Kubernetes version results match
	docs    map[string]*document
	// crds are the custom resources of the workspace files and the open
	// documents, which type the params of policies.
	crds check.CRDs
	// watchFiles is true when the client lets the server ask for file changes.
	watchFiles bool
	// checker finds the problems of a document; nil means check.Check.
	checker  func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Diagnostic, error)
	init     bool
	shutdown bool
}

// document is an open file. The server is single-threaded, so the cached
// fields need no lock.
type document struct {
	version int
	text    []byte
	// crds are the session's custom resources, which type params.
	crds *check.CRDs
	// lines and parsed are made by the first request that needs them and
	// kept until the next didChange replaces the document.
	lines  [][]rune
	parsed *assist.Document
	// diags are the problems found by the last check of this version, kept
	// for codeAction; checked tells whether there was one.
	diags   []check.Diagnostic
	checked bool
}

// diagnostics returns the problems of the document as it is, checking it only
// when no check of this version completed. A YAML error leaves the diagnostics
// found before it. A cancelled ctx gives ctx.Err() and keeps nothing.
func (ss *session) diagnostics(ctx context.Context, uri string, doc *document) ([]check.Diagnostic, error) {
	if doc.checked {
		return doc.diags, nil
	}
	run := check.Check
	if ss.checker != nil {
		run = ss.checker
	}
	// Check's error is for YAML that does not parse, which it also returns as
	// diagnostics, or for ctx.
	diags, _ := run(ctx, uri, doc.text, check.WithVersion(ss.kube), check.WithCRDs(&ss.crds))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	doc.diags, doc.checked = diags, true
	return diags, nil
}

// runes returns the lines of the text, split once per version.
func (d *document) runes() [][]rune {
	if d.lines == nil {
		d.lines = splitLines(d.text)
	}
	return d.lines
}

// parse returns what assist reads from the text, extracted once per version.
// A cancelled ctx gives an incomplete result, which is not kept.
func (d *document) parse(ctx context.Context, uri string) *assist.Document {
	if d.parsed != nil {
		return d.parsed
	}
	p := assist.Parse(ctx, uri, d.text, assist.WithCRDs(d.crds))
	if ctx.Err() == nil {
		d.parsed = p
	}
	return p
}

// Serve talks to one client, reading requests from in and writing responses
// to out, until the client exits or in ends. It handles one message at a
// time. It returns nil after a shutdown request followed by exit,
// ErrNoShutdown when the client leaves without shutdown, and the error that
// broke the stream otherwise. When ctx is cancelled Serve returns ctx.Err() at
// once, even while waiting for a message. Messages are read by a goroutine
// that Serve releases before it returns by closing in, if in is an io.Closer.
// The goroutine ends once its blocked read returns, which closing in makes
// happen for pipes and sockets; Serve does not wait for it, because a read
// that Close cannot release, such as one on a terminal, would block Serve.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	a, err := assist.New(kubeversion.Default())
	if err != nil {
		return err
	}
	ss := &session{conn: jsonrpc.NewConn(in, out), assist: a, kube: kubeversion.Default(), version: s.Version, docs: map[string]*document{}}
	msgs, stop := readMessages(ss.conn, in)
	defer stop()
	for {
		var r readResult
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r = <-msgs:
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg, err := r.msg, r.err
		if errors.Is(err, io.EOF) {
			return ErrNoShutdown
		}
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}
		if msg.Method == "exit" {
			if ss.shutdown {
				return nil
			}
			return ErrNoShutdown
		}
		if err := ss.handle(ctx, msg); err != nil {
			return err
		}
	}
}

type readResult struct {
	msg jsonrpc.Message
	err error
}

// readMessages reads conn in a goroutine and delivers each message on the
// returned channel, which stops after the first error. stop ends the
// goroutine: it closes in when it can, to release a blocked read, and does not
// wait for the goroutine.
func readMessages(conn *jsonrpc.Conn, in io.Reader) (msgs <-chan readResult, stop func()) {
	ch := make(chan readResult)
	quit := make(chan struct{})
	go func() {
		for {
			msg, err := conn.Read()
			select {
			case ch <- readResult{msg, err}:
			case <-quit:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return ch, func() {
		close(quit)
		if c, ok := in.(io.Closer); ok {
			_ = c.Close()
		}
	}
}

// handle dispatches one message. The error it returns is for a broken
// connection; a bad request is answered with an error response.
func (ss *session) handle(ctx context.Context, msg jsonrpc.Message) error {
	reply := func(result any, err error) error {
		if msg.IsNotification() {
			return nil
		}
		var re *rpcError
		switch {
		case errors.As(err, &re):
			return ss.conn.ReplyError(msg.ID, re.code, re.message)
		case err != nil:
			return err
		}
		return ss.conn.Reply(msg.ID, result)
	}
	switch {
	case msg.Method == "" && !msg.IsNotification():
		return nil // a response from the client; nothing to answer
	case msg.Method == "initialize":
		ss.init = true
		return ss.initialize(ctx, msg.Params, reply)
	case msg.Method == "workspace/didChangeConfiguration":
		return ss.didChangeConfiguration(ctx, msg.Params)
	case msg.Method == "initialized":
		return ss.registerWatchers()
	case msg.Method == "$/cancelRequest" || msg.Method == "$/setTrace":
		return nil
	case !ss.init:
		return reply(nil, &rpcError{jsonrpc.CodeNotInitialized, "the server is not initialized"})
	case msg.Method == "shutdown":
		ss.shutdown = true
		return reply(nil, nil)
	case ss.shutdown:
		return reply(nil, &rpcError{jsonrpc.CodeInvalidRequest, "the server is shut down"})
	}
	switch msg.Method {
	case "textDocument/didOpen":
		return ss.didOpen(ctx, msg.Params)
	case "textDocument/didChange":
		return ss.didChange(ctx, msg.Params)
	case "textDocument/didClose":
		return ss.didClose(ctx, msg.Params)
	case "workspace/didChangeWatchedFiles":
		return ss.didChangeWatchedFiles(ctx, msg.Params)
	case "textDocument/hover":
		res, err := ss.hover(ctx, msg.Params)
		return reply(res, err)
	case "textDocument/completion":
		res, err := ss.completion(ctx, msg.Params)
		return reply(res, err)
	case "textDocument/codeAction":
		res, err := ss.codeAction(ctx, msg.Params)
		return reply(res, err)
	}
	return reply(nil, &rpcError{jsonrpc.CodeMethodNotFound, "method not supported: " + msg.Method})
}

// rpcError is an error that goes back to the client as a response.
type rpcError struct {
	code    int
	message string
}

func (e *rpcError) Error() string { return e.message }

// decode reads params into v and reports whether they fit.
func decode(params json.RawMessage, v any) bool { return json.Unmarshal(params, v) == nil }

func badParams(method string, err error) error {
	return &rpcError{jsonrpc.CodeInvalidParams, fmt.Sprintf("%s: %v", method, err)}
}

// initialize answers the initialize request, after reading the Kubernetes
// version from initializationOptions.
func (ss *session) initialize(ctx context.Context, params json.RawMessage, reply func(any, error) error) error {
	var p struct {
		initializeParams
		InitializationOptions *kubeOptions `json:"initializationOptions"`
	}
	var problem string
	if decode(params, &p) && p.InitializationOptions != nil {
		_, problem, _ = ss.chooseVersion(p.InitializationOptions)
	}
	if err := reply(ss.initializeResult(), nil); err != nil {
		return err
	}
	ss.watchFiles = p.Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration
	ss.scanWorkspace(ctx, p.folders())
	return ss.warn(problem)
}

// kubeOptions are the settings of the client that the server reads.
type kubeOptions struct {
	// KubernetesVersion is the Kubernetes version to match, such as "1.34".
	// Empty means the default; absent means no change.
	KubernetesVersion *string `json:"kubernetesVersion"`
}

// chooseVersion applies the Kubernetes version in o. It reports whether the
// version in use changed, and when the value is not supported, why it was
// ignored.
func (ss *session) chooseVersion(o *kubeOptions) (changed bool, problem string, err error) {
	if o.KubernetesVersion == nil {
		return false, "", nil
	}
	v := kubeversion.Default()
	if *o.KubernetesVersion != "" {
		var perr error
		if v, perr = kubeversion.Parse(*o.KubernetesVersion); perr != nil {
			return false, fmt.Sprintf("kubernetesVersion: %v; keeping %s", perr, ss.kube), nil
		}
	}
	if v == ss.kube {
		return false, "", nil
	}
	a, err := assist.New(v)
	if err != nil {
		return false, "", err
	}
	ss.kube, ss.assist = v, a
	return true, "", nil
}

// warn shows problem, when there is one, to the user.
func (ss *session) warn(problem string) error {
	if problem == "" {
		return nil
	}
	return ss.conn.Notify("window/showMessage", map[string]any{"type": 1, "message": problem})
}

// didChangeConfiguration applies a new kubernetesVersion and checks the open
// documents again when it changed.
func (ss *session) didChangeConfiguration(ctx context.Context, params json.RawMessage) error {
	var p struct {
		Settings *kubeOptions `json:"settings"`
	}
	if !decode(params, &p) || p.Settings == nil {
		return nil
	}
	changed, problem, err := ss.chooseVersion(p.Settings)
	if err != nil {
		return err
	}
	if err := ss.warn(problem); err != nil || !changed {
		return err
	}
	return ss.publishAll(ctx, "")
}

func (ss *session) initializeResult() any {
	type info struct {
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
	}
	// Newest first, the order the VS Code extension documents for its picker.
	supported := make([]string, 0, len(kubeversion.Supported()))
	for _, v := range slices.Backward(kubeversion.Supported()) {
		supported = append(supported, v.String())
	}
	return map[string]any{
		"kubernetesVersion": ss.kube.String(),
		"capabilities": map[string]any{
			"textDocumentSync":   map[string]any{"openClose": true, "change": 1},
			"hoverProvider":      true,
			"codeActionProvider": map[string]any{"codeActionKinds": []string{quickFix}},
			"completionProvider": map[string]any{
				"triggerCharacters": []string{"."},
			},
			"experimental": map[string]any{"celLens": map[string]any{
				"kubernetesVersion":           kubeversion.Default().String(),
				"supportedKubernetesVersions": supported,
			}},
		},
		"serverInfo": info{"cel-lens-core", ss.version},
	}
}

type textDocumentItem struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
	Text    string `json:"text"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type positionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position position `json:"position"`
}

func (ss *session) didOpen(ctx context.Context, params json.RawMessage) error {
	var p struct {
		TextDocument textDocumentItem `json:"textDocument"`
	}
	if !decode(params, &p) {
		return nil // a notification has nobody to tell
	}
	d := p.TextDocument
	ss.docs[d.URI] = &document{version: d.Version, text: []byte(d.Text), crds: &ss.crds}
	return ss.publishWithCRDs(ctx, d.URI, ss.crds.Set(ctx, crdKey(d.URI), []byte(d.Text)))
}

func (ss *session) didChange(ctx context.Context, params json.RawMessage) error {
	var p struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if !decode(params, &p) || len(p.ContentChanges) == 0 {
		return nil
	}
	uri := p.TextDocument.URI
	text := []byte(p.ContentChanges[len(p.ContentChanges)-1].Text)
	ss.docs[uri] = &document{version: p.TextDocument.Version, text: text, crds: &ss.crds}
	return ss.publishWithCRDs(ctx, uri, ss.crds.Set(ctx, crdKey(uri), text))
}

func (ss *session) didClose(ctx context.Context, params json.RawMessage) error {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if !decode(params, &p) {
		return nil
	}
	uri := p.TextDocument.URI
	delete(ss.docs, uri)
	// The editor's text is gone: the file on disk is what the workspace has.
	changed := ss.readFromDisk(ctx, uri)
	if err := ss.conn.Notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": []any{}}); err != nil || !changed {
		return err
	}
	return ss.publishAll(ctx, "")
}

// publishWithCRDs sends the diagnostics of the document at uri and, when the
// custom resources changed, those of the other open documents, whose params
// may be typed from them.
func (ss *session) publishWithCRDs(ctx context.Context, uri string, crdsChanged bool) error {
	if err := ss.publish(ctx, uri); err != nil || !crdsChanged {
		return err
	}
	return ss.publishAll(ctx, uri)
}

// publish checks the document at uri and sends its diagnostics.
func (ss *session) publish(ctx context.Context, uri string) error {
	doc := ss.docs[uri]
	doc.checked = false // the Kubernetes version may have changed
	diags, err := ss.diagnostics(ctx, uri, doc)
	if err != nil {
		return err
	}
	lines := doc.runes()
	out := make([]map[string]any, 0, len(diags))
	for _, d := range diags {
		out = append(out, diagnostic(lines, d))
	}
	return ss.conn.Notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "version": doc.version, "diagnostics": out})
}

// diagnostic is d as the protocol has it.
func diagnostic(lines [][]rune, d check.Diagnostic) map[string]any {
	diag := map[string]any{
		"range":    toRange(lines, d.Start, d.End),
		"severity": severity(d.Severity),
		"source":   "cel-lens",
		"message":  d.Message,
	}
	if len(d.Fixes) > 0 {
		fixes := make([]map[string]any, len(d.Fixes))
		for i, f := range d.Fixes {
			fixes[i] = map[string]any{"fieldPath": f.FieldPath, "limit": f.Limit}
			if f.Value > 0 {
				fixes[i]["value"] = f.Value
			}
		}
		diag["data"] = map[string]any{"fixes": fixes}
	}
	return diag
}

// severity is s as the protocol numbers it: 1 for an error, 2 for a warning.
func severity(s check.Severity) int {
	if s == check.SeverityWarning {
		return 2
	}
	return 1
}

func (ss *session) hover(ctx context.Context, params json.RawMessage) (any, error) {
	var p positionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, badParams("textDocument/hover", err)
	}
	doc, ok := ss.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	lines := doc.runes()
	h, ok := ss.assist.Hover(doc.parse(ctx, p.TextDocument.URI), fromPosition(lines, p.Position))
	if !ok {
		return nil, nil
	}
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": h.Markdown},
		"range":    toRange(lines, h.Start, h.End),
	}, nil
}

func (ss *session) completion(ctx context.Context, params json.RawMessage) (any, error) {
	var p positionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, badParams("textDocument/completion", err)
	}
	doc, ok := ss.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	items, ok := ss.assist.Complete(doc.parse(ctx, p.TextDocument.URI), fromPosition(doc.runes(), p.Position))
	if !ok {
		return nil, nil
	}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = map[string]any{"label": it.Label, "kind": completionKind(it.Kind), "detail": it.Detail}
		if it.SortKey != "" {
			out[i]["sortText"] = it.SortKey
		}
		if it.Documentation != "" {
			out[i]["documentation"] = map[string]any{"kind": "markdown", "value": it.Documentation}
		}
	}
	return out, nil
}

// completionKind maps an item kind to the protocol's CompletionItemKind.
func completionKind(k assist.ItemKind) int {
	switch k {
	case assist.KindField:
		return 5
	case assist.KindFunction:
		return 3
	case assist.KindMethod:
		return 2
	case assist.KindVariable:
		return 6
	}
	return 1 // text
}

// splitLines splits text at line feeds; a carriage return stays at the end of
// its line, which no position refers to.
func splitLines(text []byte) [][]rune {
	var lines [][]rune
	start := 0
	for i, b := range text {
		if b == '\n' {
			lines = append(lines, []rune(string(text[start:i])))
			start = i + 1
		}
	}
	return append(lines, []rune(string(text[start:])))
}

// toRange converts a range of the extractor, in runes counting from 1, to the
// protocol's, in UTF-16 units counting from 0.
func toRange(lines [][]rune, start, end extract.Position) lspRange {
	return lspRange{toPosition(lines, start), toPosition(lines, end)}
}

func toPosition(lines [][]rune, p extract.Position) position {
	n := 0
	if p.Line >= 1 && p.Line <= len(lines) {
		line := lines[p.Line-1]
		for _, r := range line[:max(min(p.Column-1, len(line)), 0)] {
			n += utf16.RuneLen(r)
		}
		n += max(p.Column-1-len(line), 0)
	}
	return position{Line: p.Line - 1, Character: n}
}

// fromPosition converts a protocol position to the extractor's. A position
// inside a surrogate pair counts as before the character.
func fromPosition(lines [][]rune, p position) extract.Position {
	col := 0
	if p.Line >= 0 && p.Line < len(lines) {
		units := 0
		for _, r := range lines[p.Line] {
			if units >= p.Character {
				break
			}
			units += utf16.RuneLen(r)
			col++
		}
		col += max(p.Character-units, 0)
	}
	return extract.Position{Line: p.Line + 1, Column: col + 1}
}
