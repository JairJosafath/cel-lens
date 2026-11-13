// Package lsp serves CEL language support for Kubernetes YAML files over the
// Language Server Protocol: diagnostics for every CEL expression of an open
// document, hover and completion inside them, and above each the inline info
// of package inline as code lenses.
//
// Documents are synchronised in full. Positions use the protocol's default
// encoding, UTF-16 code units. A YAML document that does not parse is reported
// as an error on the line where parsing fails, and the CEL in the other
// documents of the file is still checked.
package lsp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"slices"
	"time"
	"unicode/utf16"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/internal/examples"
	"github.com/JairJosafath/cel-lens/engine/internal/inline"
	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// ErrNoShutdown is returned by Serve when the client ends the session, with
// exit or by closing the stream, without asking for shutdown first. The
// protocol makes that a failure.
var ErrNoShutdown = errors.New("client ended the session without shutdown")

// Server is a language server. The zero value is ready to use and matches
// kubeversion.Default until the client chooses a Kubernetes version with
// kubernetesVersion in initializationOptions or didChangeConfiguration. Every
// experimental feature is off until the client lists it in experimental, in
// the same two places, and the inline info and the example results are on
// until inlineInfo or exampleResults is false there.
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
	// features are the experimental features the client switched on.
	features feature.Set
	docs     map[string]*document
	// crds are the custom resources of the workspace files and the open
	// documents, which type the params of policies.
	crds check.CRDs
	// watchFiles is true when the client lets the server ask for file changes.
	watchFiles bool
	// folders are the workspace folders, which are scanned for CRDs once
	// crd-typed-policies is on.
	folders []string
	// checker finds the problems of a document; nil means check.Check.
	checker func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Diagnostic, error)
	// scanner reads the CRDs of the folders into dst and returns what to tell the
	// user; nil means scanWorkspace.
	scanner func(ctx context.Context, folders []string, dst *check.CRDs) []message
	// fsys gives the file system of a workspace folder for the scan; nil means
	// os.DirFS.
	fsys func(dir string) fs.FS
	// fileCap is the most files a scan reads; 0 means maxWorkspaceFiles.
	fileCap int
	// scan is the workspace scan in progress, nil when there is none.
	scan *scan
	// batch numbers the message in hand, such as a didChangeWatchedFiles
	// batch: a scan and a search it starts or queues work for that batch, and
	// the check of the open documents the batch owes waits for both (see
	// settle).
	batch int
	// owed are the batches owed a check of the open documents once none of
	// their scans and searches is left.
	owed map[int]bool
	// queued are the created folders to scan once the scan in progress ends,
	// for queuedBatches.
	queued        []string
	queuedBatches []int
	// lenses are the .cellens files of the workspace folders and the open
	// ones, by key (see crdKey), which give types to assume where values are
	// dyn. They are found by a search of the folders at initialize, which
	// runs whatever the experimental features.
	lenses map[string]*lens
	// lensSearch is the search for .cellens files in progress, nil when none.
	lensSearch *lensSearch
	// lensQueued are the created folders to search once lensSearch ends, for
	// lensQueuedBatches.
	lensQueued        []string
	lensQueuedBatches []int
	// kinds are the schemas of the built-in kinds kind: entries name, made
	// once, by apiVersion and kind; nil for one that is no built-in kind.
	kinds map[string]map[string]any
	// goReads are the Go types go: entries name, as last read, by the key of
	// their folder, # and their name (see goType).
	goReads map[string]goRead
	// goFiles is what the last listing of the workspace's Go types for
	// typeChoices read of each .go file, which the next reuses while the
	// file stays as it was.
	goFiles map[string]goFile
	// goGen counts the changes to .go files and folders the client told of;
	// goListedAt is goGen when the listing in goFiles started, so goFiles is
	// as the workspace is while the two are equal (see goFresh).
	goGen, goListedAt int
	// goLister lists the Go types for the job of typeChoices (see
	// answerTypeChoices) and for completion (see listGo); nil means
	// goChoices.
	goLister func(ctx context.Context, dirFS func(dir string) fs.FS, folders []string, known map[string]goFile) goListed
	// lensShown are the diagnostics last sent for each .cellens file, by URI.
	lensShown map[string][]map[string]any
	// lensGen counts the changes, other than to the .cellens files, that may
	// change the problems of their entries: the settings, the CRDs and the
	// files on disk. The problems found at one count are kept until another
	// (see lensDiagnostics).
	lensGen int
	// inlineOff is true while the client's setting inlineInfo is false: the
	// server then answers no code lenses.
	inlineOff bool
	// examplesOff is true while the client's setting exampleResults is
	// false: the server then finds, checks and runs nothing on the
	// examples, and shows nothing of them.
	examplesOff bool
	// completionRegistration is true when the client registers completion
	// itself, a provider for YAML files and one for .cellens files;
	// completionRegistered once the server asked it to.
	completionRegistration, completionRegistered bool
	// snippets is true when the client takes snippets in completion items,
	// asIs when it takes their text with the indentation it has
	// (insertTextMode asIs).
	snippets, asIs bool
	// codeLensRefresh is true when the client lets the server ask it to request
	// the code lenses again; refreshes counts the times it did, for the ids.
	codeLensRefresh bool
	refreshes       int
	// refreshedAt is lensChanges when the client was last asked to refresh
	// the code lenses, or would have been.
	refreshedAt int
	// lensChanges counts the changes to lenses; the code lenses a document
	// keeps were made at one count, and are made again at another.
	lensChanges int
	// retry fires once to send the last refresh again while the code lenses
	// are owed, and stopRetry stops it; both nil while none is set (see
	// armRetry). retryTimer makes them; nil means newTimer.
	retry      <-chan time.Time
	stopRetry  func() bool
	retryTimer func(d time.Duration) (<-chan time.Time, func() bool)
	init       bool
	shutdown   bool
	// edits are what the client can do with a workspace/applyEdit; pending
	// are the ones sent for a command and not answered yet, by the id of
	// the request, the editSeq-th one.
	edits   editCaps
	pending map[string]pendingEdit
	editSeq int
	// jobs are the commands whose work runs in a goroutine, by number, the
	// jobSeq-th one last; each sends what it returns on jobDone (see
	// startJob).
	jobs    map[int]*job
	jobSeq  int
	jobDone chan jobResult
	// exampleRun is the check of the examples of the CRDs in progress, nil
	// when none runs; examplesDue is true when what they are or their
	// problems may have changed since the last one started (see
	// examplesChanged), and exampleStale are the files that changed on disk
	// since, which it reads again. exampleMemo is what the last check kept
	// for the next, nil while one runs. exampleWait fires when the next may
	// start, and cancelExampleWait stops it, both nil while none waits;
	// exampleTimer makes them, nil meaning newTimer.
	exampleRun        *exampleRun
	examplesDue       bool
	exampleStale      []string
	exampleMemo       *exampleMemo
	exampleWait       <-chan time.Time
	cancelExampleWait func() bool
	exampleTimer      func(d time.Duration) (<-chan time.Time, func() bool)
	// exampleWork does the work of a check of the examples in its
	// goroutine; nil means exampleCheck.run.
	exampleWork func(ctx context.Context, c *exampleCheck) (map[string]exampleFile, error)
	// loopBusy is busy while the message loop handles a message: a check
	// of the examples waits for it between two steps of its work, so it
	// takes no time from the message.
	loopBusy busyGate
	// exampleFound are the problems of the examples the last check found,
	// by the key of their file; exampleShown what was sent for each file
	// with some that is not open.
	exampleFound map[string]exampleFile
	exampleShown map[string]exampleShown
	// results keeps what the rules of each file of CRDs exampleResults was
	// asked about did to their examples; its jobs use it, under its own
	// lock.
	results examples.Cache
}

// document is an open file. Only the message loop uses it, so the cached
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
	// assumed are the types the .cellens files assume in the document, as of
	// its last check; parsed is made with them.
	assumed []check.AssumedType
	// codeLenses are the code lenses of this version as of its last check,
	// made when the session's lensChanges was codeLensesAt; nil until a
	// codeLens request makes them.
	codeLenses   []any
	codeLensesAt int
	// shown is true once the client asked for the code lenses of the
	// document, at any version: an editor shows them, and asks again on a
	// refresh (see lensesOwed).
	shown bool
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
	diags, _ := run(ctx, uri, doc.text, check.WithVersion(ss.kube), check.WithFeatures(ss.features), check.WithCRDs(&ss.crds), check.WithAssumedTypes(doc.assumed))
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
	p := assist.Parse(ctx, uri, d.text, assist.WithCRDs(d.crds), assist.WithAssumedTypes(d.assumed))
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
	return ss.serve(ctx, in)
}

// serve is the message loop of Serve.
func (ss *session) serve(ctx context.Context, in io.Reader) error {
	msgs, stop := readMessages(ss.conn, in)
	defer stop()
	defer ss.stopScan()
	defer ss.stopLensSearch()
	defer ss.stopJobs()
	defer ss.cancelRetry()
	defer ss.stopExamples()
	for {
		ss.startExamples(ctx)
		var r readResult
		var scanned <-chan *check.CRDs
		if ss.scan != nil {
			scanned = ss.scan.done
		}
		var lensesFound <-chan lensFound
		if ss.lensSearch != nil {
			lensesFound = ss.lensSearch.done
		}
		var jobDone <-chan jobResult
		if len(ss.jobs) > 0 {
			jobDone = ss.jobDone
		}
		var examplesDone <-chan exampleResult
		if ss.exampleRun != nil {
			examplesDone = ss.exampleRun.done
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r := <-examplesDone:
			if err := ss.endExamples(ctx, r); err != nil {
				return err
			}
			continue
		case <-ss.exampleWait:
			ss.exampleWait, ss.cancelExampleWait = nil, nil
			continue
		case r := <-jobDone:
			if err := ss.finishJob(r); err != nil {
				return err
			}
			continue
		case <-ss.retry:
			if err := ss.retryRefresh(); err != nil {
				return err
			}
			continue
		case found := <-lensesFound:
			if err := ss.finishLensSearch(ctx, found); err != nil {
				return err
			}
			continue
		case found := <-scanned:
			if err := ss.endScan(ctx, found); err != nil {
				return err
			}
			continue
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
		ss.loopBusy.enter()
		err = ss.handle(ctx, msg)
		ss.loopBusy.leave()
		if err != nil {
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
		return ss.answered(msg) // a response from the client
	case msg.Method == "initialize":
		ss.init = true
		return ss.initialize(ctx, msg.Params, reply)
	case msg.Method == "workspace/didChangeConfiguration":
		return ss.didChangeConfiguration(ctx, msg.Params)
	case msg.Method == "initialized":
		if err := ss.registerWatchers(); err != nil {
			return err
		}
		return ss.registerCompletion()
	case msg.Method == "$/cancelRequest":
		return ss.cancel(msg.Params)
	case msg.Method == "$/setTrace":
		return nil
	case !ss.init:
		return reply(nil, &rpcError{jsonrpc.CodeNotInitialized, "the server is not initialized"})
	case msg.Method == "shutdown":
		ss.shutdown = true
		ss.cancelRetry()
		ss.stopScan()
		ss.stopLensSearch()
		ss.stopExamples()
		if err := ss.dropPending(); err != nil {
			return err
		}
		if err := ss.dropJobs(); err != nil {
			return err
		}
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
	case "textDocument/codeLens":
		res, err := ss.codeLens(ctx, msg.Params)
		return reply(res, err)
	case "workspace/executeCommand":
		return ss.executeCommand(ctx, msg, reply)
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
	var problems []message
	if decode(params, &p) && p.InitializationOptions != nil {
		_, problems, _ = ss.configure(p.InitializationOptions)
	}
	ss.completionRegistration = p.Capabilities.TextDocument.Completion.DynamicRegistration
	if err := reply(ss.initializeResult(), nil); err != nil {
		return err
	}
	ss.watchFiles = p.Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration
	ss.codeLensRefresh = p.Capabilities.Workspace.CodeLens.RefreshSupport
	item := p.Capabilities.TextDocument.Completion.CompletionItem
	ss.snippets, ss.asIs = item.SnippetSupport, slices.Contains(item.InsertTextModeSupport.ValueSet, insertAsIs)
	ss.edits = p.editCaps()
	ss.folders = p.folders()
	ss.batch++ // the workspace scan and search work for one batch
	if ss.features.Has(feature.CRDTypedPolicies) {
		ss.startScan(ctx)
	}
	ss.examplesChanged()
	ss.searchLenses(ctx, ss.folders)
	return ss.warn(problems)
}

// kubeOptions are the settings of the client that the server reads.
type kubeOptions struct {
	// KubernetesVersion is the Kubernetes version to match, such as "1.34".
	// Empty means the default; absent means no change.
	KubernetesVersion *string `json:"kubernetesVersion"`
	// Experimental lists the experimental features to switch on. Empty means
	// none; absent means no change.
	Experimental *[]string `json:"experimental"`
	// InlineInfo turns the code lenses on or off; absent means no change.
	InlineInfo *bool `json:"inlineInfo"`
	// ExampleResults turns on or off what the server says of the examples
	// of the CRDs: what the rules do to them, and the schema's problems in
	// them; absent means no change.
	ExampleResults *bool `json:"exampleResults"`
}

// configure applies the Kubernetes version, the experimental features,
// inlineInfo and exampleResults in o. It reports whether the version or the
// features in use changed, and why each value that is not supported was
// ignored.
func (ss *session) configure(o *kubeOptions) (changed bool, problems []message, err error) {
	if o.InlineInfo != nil {
		ss.inlineOff = !*o.InlineInfo
	}
	if o.ExampleResults != nil {
		ss.examplesOff = !*o.ExampleResults
	}
	v, on := ss.kube, ss.features
	if o.KubernetesVersion != nil {
		v = kubeversion.Default()
		if *o.KubernetesVersion != "" {
			var perr error
			if v, perr = kubeversion.Parse(*o.KubernetesVersion); perr != nil {
				problems = append(problems, message{typ: messageError, text: fmt.Sprintf("kubernetesVersion: %v; keeping %s", perr, ss.kube)})
				v = ss.kube
			}
		}
	}
	if o.Experimental != nil {
		var unknown []string
		on, unknown = feature.Parse(*o.Experimental)
		if w := feature.Warning(unknown); w != "" {
			problems = append(problems, message{typ: messageWarning, text: "experimental: " + w, log: true})
		}
	}
	if v == ss.kube && on == ss.features {
		return false, problems, nil
	}
	a, err := assist.New(v, assist.WithFeatures(on))
	if err != nil {
		return false, problems, err
	}
	ss.kube, ss.features, ss.assist = v, on, a
	return true, problems, nil
}

// message is something to show the user, with the protocol's MessageType.
type message struct {
	typ  int
	text string
	// log sends the message to the client's log, not as a notification.
	log bool
}

// The protocol's MessageType values the server sends.
const (
	messageError   = 1
	messageWarning = 2
)

// warn shows each of problems to the user, or writes it to the client's log
// when it is marked log.
func (ss *session) warn(problems []message) error {
	for _, p := range problems {
		method := "window/showMessage"
		if p.log {
			method = "window/logMessage"
		}
		if err := ss.conn.Notify(method, map[string]any{"type": p.typ, "message": p.text}); err != nil {
			return err
		}
	}
	return nil
}

// didChangeConfiguration applies a new kubernetesVersion, experimental list,
// inlineInfo and exampleResults, checks the open documents again when the
// version or the features changed, and asks the client to request the code
// lenses again when they may have changed. A new version also stops each
// cel-lens.exampleResults still running, whose answer would be for the old
// version, answering it as cancelled. Turning on crd-typed-policies
// starts a scan of the workspace for CRDs in the background; the open
// documents are checked again when it ends. Turning exampleResults off
// stops the check of the examples and takes back what they showed; turning
// it on starts one.
func (ss *session) didChangeConfiguration(ctx context.Context, params json.RawMessage) error {
	var p struct {
		Settings *kubeOptions `json:"settings"`
	}
	if !decode(params, &p) || p.Settings == nil {
		return nil
	}
	wasOn, wasOff, examplesWereOff, kube := ss.features.Has(feature.CRDTypedPolicies), ss.inlineOff, ss.examplesOff, ss.kube
	changed, problems, err := ss.configure(p.Settings)
	if err != nil {
		return err
	}
	if ss.kube != kube {
		if err := ss.cancelJobs(commandExampleResults, "the Kubernetes version changed to "+ss.kube.String()); err != nil {
			return err
		}
	}
	if ss.examplesOff != examplesWereOff {
		if err := ss.switchExamples(ctx); err != nil {
			return err
		}
	}
	if !wasOn && ss.features.Has(feature.CRDTypedPolicies) {
		ss.batch++
		ss.startScan(ctx)
	}
	if err := ss.warn(problems); err != nil {
		return err
	}
	if changed {
		ss.examplesChanged()          // the Kubernetes version their check matches may have changed
		return ss.publishAll(ctx, "") // which refreshes the lenses
	}
	if ss.inlineOff != wasOff {
		return ss.refreshCodeLenses()
	}
	return nil
}

// initializeInfo is initializeResult without completion.
func (ss *session) initializeInfo() map[string]any {
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
			"textDocumentSync":       map[string]any{"openClose": true, "change": 1},
			"hoverProvider":          true,
			"codeActionProvider":     map[string]any{"codeActionKinds": []string{quickFix}},
			"codeLensProvider":       map[string]any{},
			"executeCommandProvider": map[string]any{"commands": commandNames()},
			"experimental": map[string]any{"celLens": map[string]any{
				"kubernetesVersion":           kubeversion.Default().String(),
				"supportedKubernetesVersions": supported,
				"experimentalFeatures":        feature.Known(),
				"workspaceScanMaxFiles":       maxWorkspaceFiles,
				"workspaceScanMaxFileBytes":   maxWorkspaceFileSize,
			}},
		},
		"serverInfo": info{"cel-lens-core", ss.version},
	}
}

// initializeResult is the answer to initialize: the server's capabilities,
// completion among them unless the client registers it (registerCompletion),
// the Kubernetes version and the server's name.
func (ss *session) initializeResult() any {
	res := ss.initializeInfo()
	if !ss.completionRegistration {
		caps, _ := res["capabilities"].(map[string]any)
		caps["completionProvider"] = map[string]any{"triggerCharacters": lensTriggerCharacters}
	}
	return res
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
	if isLens(d.URI) {
		ss.putLens(ctx, d.URI, []byte(d.Text), true)
		ss.lenses[crdKey(d.URI)].version = d.Version
		return ss.publishTyped(ctx, true)
	}
	ss.docs[d.URI] = &document{version: d.Version, text: []byte(d.Text), crds: &ss.crds}
	ss.examplesChanged()
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
	if isLens(uri) {
		changed := ss.putLens(ctx, uri, text, true)
		ss.lenses[crdKey(uri)].version = p.TextDocument.Version
		if !changed {
			return nil
		}
		return ss.publishTyped(ctx, false)
	}
	shown := ss.docs[uri] != nil && ss.docs[uri].shown
	ss.docs[uri] = &document{version: p.TextDocument.Version, text: text, crds: &ss.crds, shown: shown}
	ss.examplesChanged()
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
	if isLens(uri) {
		// The editor's text is gone: the file on disk, if any, is what the
		// workspace has, and its problems still show.
		if l := ss.lenses[crdKey(uri)]; l != nil {
			l.open = false
		}
		if !ss.readLens(ctx, uri) {
			return ss.refreshOwed()
		}
		return ss.publishTyped(ctx, true)
	}
	delete(ss.docs, uri)
	// The editor's text is gone: the file on disk is what the workspace has.
	changed := ss.readFromDisk(ctx, uri)
	ss.examplesChanged()
	if err := ss.closeExamples(uri); err != nil {
		return err
	}
	if !changed {
		return ss.publishLenses(ctx, crdKey(uri))
	}
	return ss.publishAll(ctx, "")
}

// publishWithCRDs sends the diagnostics of the document at uri and, when the
// custom resources changed, those of the other open documents, whose params
// may be typed from them. It also sends those of the .cellens files that name
// the document, which may now have what they name, or not.
func (ss *session) publishWithCRDs(ctx context.Context, uri string, crdsChanged bool) error {
	if err := ss.publish(ctx, uri); err != nil {
		return err
	}
	if !crdsChanged {
		return ss.publishLenses(ctx, crdKey(uri))
	}
	return ss.publishAll(ctx, uri)
}

// publish checks the document at uri, with the types the .cellens files
// assume in it, and sends its diagnostics.
func (ss *session) publish(ctx context.Context, uri string) error {
	return ss.publishAssumed(ctx, uri, ss.assumedFor(ctx, uri))
}

// publishAssumed is publish with assumed, the types the .cellens files
// assume in the document at uri.
func (ss *session) publishAssumed(ctx context.Context, uri string, assumed []check.AssumedType) error {
	doc := ss.docs[uri]
	doc.checked = false // the Kubernetes version may have changed
	doc.codeLenses = nil
	if !reflect.DeepEqual(assumed, doc.assumed) {
		doc.assumed, doc.parsed = assumed, nil
	}
	diags, err := ss.diagnostics(ctx, uri, doc)
	if err != nil {
		return err
	}
	return ss.sendDiagnostics(uri, doc, diags)
}

// sendDiagnostics sends diags, the problems of the open document at uri,
// with those of its examples found in its text, after them where they are
// at the same place. The client has no other diagnostics for the file then.
func (ss *session) sendDiagnostics(uri string, doc *document, diags []check.Diagnostic) error {
	key := crdKey(uri)
	if found := ss.exampleDiagnostics(key, doc.text); len(found) > 0 {
		diags = append(slices.Clone(diags), found...)
		slices.SortStableFunc(diags, func(a, b check.Diagnostic) int {
			return cmp.Or(a.Start.Line-b.Start.Line, a.Start.Column-b.Start.Column)
		})
	}
	if shown, ok := ss.exampleShown[key]; ok {
		delete(ss.exampleShown, key)
		if shown.uri != uri {
			if err := ss.conn.Notify("textDocument/publishDiagnostics", map[string]any{"uri": shown.uri, "diagnostics": []any{}}); err != nil {
				return err
			}
		}
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
	if isLens(p.TextDocument.URI) {
		return ss.lensHover(ctx, p.TextDocument.URI, p.Position)
	}
	doc, ok := ss.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	lines := doc.runes()
	parsed := doc.parse(ctx, p.TextDocument.URI)
	pos := fromPosition(lines, p.Position)
	h, ok := ss.assist.Hover(parsed, pos)
	// A CRD's rule says what it does to the examples, as far as the last
	// check of the examples found it, below what the place says, or alone
	// where the place says nothing.
	section := ""
	if key := crdKey(p.TextDocument.URI); ss.knowsRules(key) {
		if e, found := parsed.Expression(pos); found {
			if section = inline.ExamplesHover(ss.ruleExamples(key, e)); section != "" && !ok {
				h = assist.Hover{Start: e.Start, End: e.End}
			}
		}
	}
	switch {
	case !ok && section == "":
		return nil, nil
	case h.Markdown == "":
		h.Markdown = section
	case section != "":
		h.Markdown += "\n\n---\n\n" + section
	}
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": h.Markdown},
		"range":    toRange(lines, h.Start, h.End),
	}, nil
}

// triggerCharacters are the characters after which the client asks for
// completion in a YAML file: a dot in CEL, and the characters of a .cellens
// but the space, which a client that cannot register completion per kind of
// file (registerCompletion) has for every file. In a .cellens they are a dot
// in a path, the / of a file or folder, the # of a Go type, the ( of a list
// or map type and the colon after a key; lensTriggerCharacters add the space
// after that colon. The server answers null at once where one gives nothing
// (see completion).
var (
	triggerCharacters     = []string{".", "/", "#", "(", ":"}
	lensTriggerCharacters = []string{".", "/", "#", "(", ":", " "}
)

// triggerCharacter is the protocol's CompletionTriggerKind for a character.
const triggerCharacter = 2

func (ss *session) completion(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		positionParams
		Context *struct {
			TriggerKind      int    `json:"triggerKind"`
			TriggerCharacter string `json:"triggerCharacter"`
		} `json:"context"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, badParams("textDocument/completion", err)
	}
	trigger := ""
	if c := p.Context; c != nil && c.TriggerKind == triggerCharacter {
		trigger = c.TriggerCharacter
	}
	if isLens(p.TextDocument.URI) {
		return ss.lensCompletion(ctx, p.TextDocument.URI, p.Position, trigger)
	}
	if trigger != "" && trigger != "." {
		return nil, nil // the other characters are for .cellens files
	}
	doc, ok := ss.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	lines := doc.runes()
	items, ok := ss.assist.Complete(doc.parse(ctx, p.TextDocument.URI), fromPosition(lines, p.Position))
	if !ok {
		return nil, nil
	}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = map[string]any{"label": it.Label, "kind": completionKind(it.Kind), "detail": it.Detail}
		if it.NewText != "" {
			out[i]["textEdit"] = textEdit{toRange(lines, it.Start, it.End), it.NewText}
		}
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
