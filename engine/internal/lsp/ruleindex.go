package lsp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io/fs"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// The rule index lists the CEL rules of the workspace for cel-lens.ruleList
// (see answerRuleList). It runs in the background, never on the message
// loop: a run walks the workspace folders with walkYAML and its limits,
// reads a file again only when its size or time changed, parses only a file
// with the words of a kind it lists, and checks each file that is not open
// with check.Check and the options an open document gets, again only when
// its text or those options changed. It waits while the loop handles a
// message, as the check of the examples does. An open file is listed on the
// loop, from the editor's text and the loop's own check of it.

// ruleIndex is the state of the rule index in the session.
type ruleIndex struct {
	// files is what the last run kept of each file it walked, by key, which
	// the next run takes; nil while a run holds it.
	files map[string]indexedFile
	// run is the run in progress, nil when none runs.
	run *ruleRun
	// waiting are the cel-lens.ruleList requests the run is for, which are
	// answered when it ends.
	waiting []ruleRequest
	// open is what was listed of each open document, by URI, which is made
	// again only for another version of it or another kyverno.
	open map[string]openListing
	// follow is true for a client that asked for the list at initialize
	// (initializationOptions.ruleList): the index runs from initialize on,
	// follows every change, and the client is told of what it found (see
	// rulesChanged and notifyRules).
	follow bool
	// due is true when what the list holds may have changed since the run in
	// progress, or the last one, started; wait fires ruleDelay after the
	// last change, when the next may start, and cancelWait stops it; both
	// nil while none waits. dueBatches are the batches (see session.batch)
	// whose change the next run is for.
	due        bool
	wait       <-chan time.Time
	cancelWait func() bool
	dueBatches []int
	// last are the inputs of the last run that walked the whole workspace to
	// its end, nil before the first: a run with another version, other
	// features or other folders is a scan (see ScanState).
	last *ruleInputs
	// The list as last answered or told: the base of the generations of the
	// session, from the clock at initialize, the generation now, the entry
	// of each file listed by its URI, the URIs in the order of the list, and
	// the files listed once and not now, with the generation they last left
	// the list at (see publishRules).
	base, gen int64
	listed    map[string]listedFile
	order     []string
	gone      map[string]int64
	// scan is how far the walk is, what the notification says;
	// throttle fires when the next notification of a scan may be sent, nil
	// while none waits (see progressRules), and stopThrottle stops it;
	// progressed is true when the scan went on since the last notification.
	scan         ScanState
	throttle     <-chan time.Time
	stopThrottle func() bool
	progressed   bool
}

// ruleRequest is a cel-lens.ruleList request waiting for a run: its id, and
// the generation it asks for what changed since, nil for the whole list.
type ruleRequest struct {
	id    json.RawMessage
	since *int64
}

// openListing is what listRules found in an open document.
type openListing struct {
	doc     *document
	kyverno bool
	docs    []listedDoc
}

// ruleRun is a run of the rule index in its own goroutine.
type ruleRun struct {
	cancel context.CancelFunc
	// done delivers what the run kept, once, when its goroutine ends.
	done chan ruleRunDone
	// inputs are what the run was started with; the requests are answered
	// with what it found only while they are still the session's.
	inputs ruleInputs
	// scan is true for a run that is a scan (see ScanState); counter is how
	// far its walk is, which the loop reads when counter.signal says so.
	scan    bool
	counter *scanCounter
	// batches are the batches (see session.batch) it runs for.
	batches []int
}

// ruleRunDone is what a run of the rule index returns: what it kept of
// each file, and whether it was stopped before its end, when files holds
// what it kept until then and what it was given of the files it did not
// walk.
type ruleRunDone struct {
	files   map[string]indexedFile
	stopped bool
}

// ruleInputs are what the checks of a run of the rule index depend on,
// made on the message loop: the Kubernetes version, the features, a
// snapshot of the CRDs the session knows, the types the .cellens files
// assume in each file, by key, the keys of the open documents, which the
// run leaves to the loop, and the workspace folders.
type ruleInputs struct {
	kube     kubeversion.Version
	features feature.Set
	crds     *check.CRDs
	assumed  map[string][]check.AssumedType
	open     map[string]bool
	folders  []string
}

// current reports whether what a run started with in was still the
// session's in now: the same version, features, types and folders, the
// same CRDs while crd-typed-policies is on, and every document open then
// still open, as the run did not list it.
func (in ruleInputs) current(now ruleInputs) bool {
	if in.kube != now.kube || in.features != now.features ||
		!reflect.DeepEqual(in.assumed, now.assumed) || !slices.Equal(in.folders, now.folders) {
		return false
	}
	if in.features.Has(feature.CRDTypedPolicies) && !in.crds.Equal(now.crds) {
		return false // the checks of policies read them
	}
	for key := range in.open {
		if !now.open[key] {
			return false
		}
	}
	return true
}

// fileInputs are what the check of one file depends on: the Kubernetes
// version, the features, the CRDs when the check may read them (a file with
// a policy while crd-typed-policies is on), nil otherwise, and the types
// the .cellens files assume in it.
type fileInputs struct {
	kube     kubeversion.Version
	features feature.Set
	crds     *check.CRDs
	assumed  []check.AssumedType
}

// equal reports whether a check with f would find what one with o did.
func (f fileInputs) equal(o fileInputs) bool {
	return f.kube == o.kube && f.features == o.features && f.crds.Equal(o.crds) && reflect.DeepEqual(f.assumed, o.assumed)
}

// indexedFile is what the rule index keeps of a file it walked: its size
// and time when it read it, its text when it has the words of a kind the
// index lists, the documents with rules found in that text with or without
// Kyverno's, and the check made of it, with what that check depended on,
// and the documents with the compile states of their rules.
type indexedFile struct {
	size int64
	mod  time.Time
	text []byte
	// listed is true once found was made, for kyverno.
	listed  bool
	kyverno bool
	found   []listedDoc
	// checked is true once diags were found, with with.
	checked bool
	with    fileInputs
	diags   []check.Diagnostic
	docs    []RuleDocument
}

// ruleCheck is a run of the rule index: what it needs of the session,
// made on the message loop, so it runs off it.
type ruleCheck struct {
	folders []string
	dirFS   func(dir string) fs.FS
	limit   int
	inputs  ruleInputs
	// memo is what the last run kept, which this one owns while it runs.
	memo map[string]indexedFile
	// checker finds the problems of a file; nil means check.Check.
	checker func(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Diagnostic, error)
	// loop is held while the message loop handles a message; nil never is.
	loop *busyGate
	// counter, when not nil, counts what the walk has done.
	counter *scanCounter
}

// answerRuleList answers the request msg, cel-lens.ruleList, which takes no
// argument or {since: n}, with the documents with rules of the workspace
// folders and of the open files (see RuleList): all of them, or those of the
// files whose entry changed after generation n (see answerSince). For a
// client that follows the list, with no change since the list was made, it
// answers at once from it. Otherwise it starts a run of the rule index
// unless one runs, which the request then waits for, and is answered when
// it ends; the run checks again only what changed since the last. The run is no job: it takes none of the commands'
// slots (see maxJobs). A notification starts nothing.
func (ss *session) answerRuleList(ctx context.Context, msg jsonrpc.Message, raw []json.RawMessage, reply func(any, error) error) error {
	usage := commandRuleList + " takes no argument, or one: {since: n}, a generation of an answer, for what changed after it"
	var args struct {
		Since *int64 `json:"since"`
	}
	if len(raw) > 1 || len(raw) == 1 && (json.Unmarshal(raw[0], &args) != nil || !bytes.HasPrefix(bytes.TrimSpace(raw[0]), []byte("{"))) {
		return reply(nil, invalid(usage))
	}
	if msg.IsNotification() {
		return nil
	}
	if ss.rules.follow && !ss.rules.due && ss.rules.run == nil && ss.rules.listed != nil {
		// The index follows every change, and none came since the list was
		// made: it is current.
		return reply(ss.answerSince(args.Since), nil)
	}
	ss.rules.waiting = append(ss.rules.waiting, ruleRequest{id: msg.ID, since: args.Since})
	if ss.rules.run != nil {
		return nil
	}
	inputs, err := ss.ruleInputs(ctx)
	if err != nil {
		return err
	}
	return ss.runRules(ctx, inputs, nil)
}

// ruleInputs makes the inputs of a run of the rule index (see ruleInputs).
// The error is ctx's.
func (ss *session) ruleInputs(ctx context.Context) (ruleInputs, error) {
	in := ruleInputs{
		kube: ss.kube, features: ss.features, crds: ss.crds.Snapshot(),
		assumed: map[string][]check.AssumedType{}, open: map[string]bool{}, folders: slices.Clone(ss.folders),
	}
	for uri := range ss.docs {
		in.open[crdKey(uri)] = true
	}
	// The types of .cellens, only while the session reads them; the index
	// reads no .cellens itself.
	lenses := []*lens(nil)
	if ss.readsLenses() {
		lenses = ss.sortedLenses()
	}
	for _, l := range lenses {
		for _, e := range l.entries {
			key := l.target(e)
			if _, done := in.assumed[key]; done {
				continue
			}
			if assumed := ss.assumedFor(ctx, pathURI(filepath.FromSlash(key))); len(assumed) > 0 {
				in.assumed[key] = assumed
			}
		}
	}
	return in, ctx.Err()
}

// startRuleRun starts a run of the rule index with inputs in a goroutine,
// for batches; none may be running. The message loop receives what it kept
// on ss.rules.run.done, and stopRuleRun stops it. A run with another
// Kubernetes version, other features or other folders than the last that
// walked to its end, and the first, is a scan: the scan's state is then
// scanning, and a client that follows the list is told so at once.
func (ss *session) startRuleRun(ctx context.Context, inputs ruleInputs, batches []int) {
	limit := ss.fileCap
	if limit <= 0 {
		limit = maxWorkspaceFiles
	}
	counter := &scanCounter{signal: make(chan struct{}, 1)}
	c := &ruleCheck{
		folders: outermost(ss.folders), dirFS: ss.dirFS(), limit: limit, inputs: inputs,
		memo: ss.rules.files, checker: ss.checker, loop: &ss.loopBusy, counter: counter,
	}
	ss.rules.files = nil
	last := ss.rules.last
	scan := last == nil || last.kube != inputs.kube || last.features != inputs.features || !slices.Equal(last.folders, inputs.folders)
	ctx, cancel := context.WithCancel(ctx)
	run := &ruleRun{cancel: cancel, done: make(chan ruleRunDone, 1), inputs: inputs, scan: scan, counter: counter, batches: batches}
	ss.rules.run = run
	work := ss.ruleWork
	if work == nil {
		work = func(ctx context.Context, c *ruleCheck) (map[string]indexedFile, error) { return c.run(ctx) }
	}
	go func() {
		files, err := work(ctx, c)
		run.done <- ruleRunDone{files: files, stopped: err != nil}
	}()
}

// runRules starts a run of the index (see startRuleRun) and, for a scan,
// tells a client that follows the list that it scans.
func (ss *session) runRules(ctx context.Context, inputs ruleInputs, batches []int) error {
	ss.startRuleRun(ctx, inputs, batches)
	return ss.progressRules()
}

// stopRuleRun cancels the run in progress, if any, waits for it to end and
// keeps what it kept, and stops the waits of the index.
func (ss *session) stopRuleRun() {
	ss.stopRuleWait()
	ss.stopRuleThrottle()
	if ss.rules.run == nil {
		return
	}
	ss.rules.run.cancel()
	r := <-ss.rules.run.done
	ss.rules.run, ss.rules.files = nil, r.files
}

// endRuleRun handles the end of the run in progress, which returned r: it
// keeps what the run kept, makes it the list (see publishRules), answers the
// requests that wait for it and tells a client that follows the list of what
// changed, unless the run was stopped or something it depends on changed
// meanwhile, such as the Kubernetes version or a document open when it
// started and closed since: then the next run starts, for the requests and
// the batches it was for, and checks again only what changed. A change that
// came meanwhile starts the next run once its wait is over.
func (ss *session) endRuleRun(ctx context.Context, r ruleRunDone) error {
	run := ss.rules.run
	run.cancel()
	ss.rules.run, ss.rules.files = nil, r.files
	if len(ss.rules.waiting) == 0 && !ss.rules.follow {
		return ss.settle(ctx, run.batches, false, false)
	}
	now, err := ss.ruleInputs(ctx)
	if err != nil {
		return err
	}
	if r.stopped || !run.inputs.current(now) {
		return ss.runRules(ctx, now, run.batches)
	}
	ss.rules.last = &run.inputs
	changed, err := ss.publishRules(ctx, r.files)
	if err != nil {
		return err
	}
	if run.scan || ss.rules.scan.State == scanScanning {
		ss.stopRuleThrottle()
		ss.rules.scan = run.counter.state(scanDone)
		ss.rules.scan.FilesWithRules = ss.filesWithRules()
		changed = true // the scan's end is news
	}
	requests := ss.rules.waiting
	ss.rules.waiting = nil
	for _, rq := range requests {
		if err := ss.conn.Reply(rq.id, ss.answerSince(rq.since)); err != nil {
			return err
		}
	}
	if changed {
		if err := ss.notifyRules(); err != nil {
			return err
		}
	}
	if err := ss.settle(ctx, run.batches, false, false); err != nil {
		return err
	}
	if ss.rules.wait == nil {
		return ss.startDueRuleRun(ctx)
	}
	return nil
}

// cancelRuleList answers the request id, when it waits for the rule index,
// as cancelled, and reports whether it did. The run goes on while another
// request waits for it, or the client follows the list, and is stopped
// otherwise; what it kept until then is kept.
func (ss *session) cancelRuleList(id json.RawMessage) (bool, error) {
	i := slices.IndexFunc(ss.rules.waiting, func(w ruleRequest) bool { return sameID(w.id, id) })
	if i < 0 {
		return false, nil
	}
	ss.rules.waiting = slices.Delete(ss.rules.waiting, i, i+1)
	if len(ss.rules.waiting) == 0 && ss.rules.run != nil && !ss.rules.follow {
		ss.rules.run.cancel()
	}
	return true, ss.conn.ReplyError(id, jsonrpc.CodeRequestCancelled, commandRuleList+" was cancelled: the client cancelled it")
}

// sameID reports whether two request ids are the same.
func sameID(a, b json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b)) }

// dropRuleList stops the run of the rule index and answers the requests
// that wait for it with an error, as the session shuts down.
func (ss *session) dropRuleList() error {
	ss.stopRuleRun()
	ss.rules.due, ss.rules.dueBatches = false, nil
	requests := ss.rules.waiting
	ss.rules.waiting = nil
	for _, rq := range requests {
		if err := ss.conn.ReplyError(rq.id, jsonrpc.CodeRequestFailed, "the server shut down before the command ended"); err != nil {
			return err
		}
	}
	return nil
}

// forgetRulesUnder drops what the rule index kept of the files below the
// folders with the keys gone, which left the workspace, but of those below a
// folder of walked, the folders walked now. A run in progress holds what it
// kept, and runs again for the folders as they are now (see endRuleRun),
// which keeps only the files it walks.
func (ss *session) forgetRulesUnder(gone, walked []string) {
	for key := range ss.rules.files {
		if slices.ContainsFunc(gone, func(dir string) bool { return below(key, dir) }) && !inFolders(filepath.FromSlash(key), walked) {
			delete(ss.rules.files, key)
		}
	}
}

// ruleFiles are the files with rules from files, what the last run kept of
// the files it walked, for those not open, and from the open documents, with
// the loop's own check of each, in the order of the list: of the workspace
// folders, then of the files' paths below them, then of the file; the open
// files outside every folder come last, by path. The error is ctx's.
func (ss *session) ruleFiles(ctx context.Context, files map[string]indexedFile) ([]ruleFile, error) {
	var all []ruleFile
	add := func(key, uri string, docs []RuleDocument) {
		folder, folderURI, file := ss.ruleFolder(key, uri)
		for i := range docs {
			docs[i].Folder, docs[i].File = folderURI, file
		}
		all = append(all, ruleFile{uri: uri, folder: folder, file: file, docs: docs})
	}
	open := map[string]bool{}
	for uri := range ss.docs {
		open[crdKey(uri)] = true
	}
	for key, f := range files {
		if !open[key] && len(f.docs) > 0 {
			add(key, pathURI(filepath.FromSlash(key)), ss.withExamples(key, f.found, slices.Clone(f.docs)))
		}
	}
	kyverno := ss.features.Has(feature.Kyverno)
	listings := map[string]openListing{}
	for _, uri := range slices.Sorted(maps.Keys(ss.docs)) {
		doc := ss.docs[uri]
		l, ok := ss.rules.open[uri]
		if !ok || l.doc != doc || l.kyverno != kyverno {
			found, err := listRules(ctx, uri, doc.text, kyverno)
			if err != nil {
				return nil, err
			}
			l = openListing{doc: doc, kyverno: kyverno, docs: found}
		}
		listings[uri] = l
		if len(l.docs) == 0 {
			continue
		}
		diags, err := ss.diagnostics(ctx, uri, doc)
		if err != nil {
			return nil, err
		}
		add(crdKey(uri), uri, ss.withExamples(crdKey(uri), l.docs, withCompile(l.docs, diags)))
	}
	ss.rules.open = listings
	slices.SortStableFunc(all, func(a, b ruleFile) int { return cmp.Or(a.folder-b.folder, strings.Compare(a.file, b.file)) })
	return all, nil
}

// ruleFile is a file of the list: its URI, the place of its workspace folder
// among the session's (their number for none), its path there, and its
// documents with rules.
type ruleFile struct {
	uri    string
	folder int
	file   string
	docs   []RuleDocument
}

// ruleFolder returns, for the file with the key at uri, the deepest
// workspace folder that holds it, by its place in the session's folders and
// as a file URI, and the file's slash-separated path below it; for a file
// outside every folder, the number of folders, "", and the file's path, or
// its URI when it is no file.
func (ss *session) ruleFolder(key, uri string) (index int, folderURI, file string) {
	index, best := len(ss.folders), -1
	file = key
	if _, ok := diskPath(uri); !ok {
		return index, "", uri
	}
	for i, f := range ss.folders {
		k := crdKey(pathURI(f))
		prefix := strings.TrimSuffix(k, "/") + "/"
		if rel, ok := strings.CutPrefix(key, prefix); ok && len(k) > best {
			index, best, folderURI, file = i, len(k), pathURI(f), rel
		}
	}
	return index, folderURI, file
}

// run walks the folders as the workspace scan walks them, with its limits,
// and returns what it keeps of each YAML file that is not open (see
// index), by key: what the last run kept when the file's size and time are
// the same, else what it reads now. It waits while the message loop handles
// a message before each file. It counts on c.counter, when there is one, the
// YAML files it walks, those with rules, and those it leaves out, past the
// limit or over the size limit; it walks on past the limit to count them.
// When ctx is cancelled it returns ctx's error, with what it kept until then
// and what it was given of the other files.
func (c *ruleCheck) run(ctx context.Context) (map[string]indexedFile, error) {
	out := make(map[string]indexedFile, len(c.memo))
	walkYAML(ctx, c.dirFS, c.folders, c.limit, yamlWalk{over: func() { c.counter.add(0, 0, 1) }, file: func(dir, rel string, d fs.DirEntry) bool {
		if c.loop != nil {
			c.loop.wait(ctx)
		}
		if ctx.Err() != nil {
			return false
		}
		path := filepath.Join(dir, filepath.FromSlash(rel))
		key := crdKey(pathURI(path))
		f, had := c.memo[key]
		if c.inputs.open[key] {
			if had {
				out[key] = f // the loop lists it; this is for when it is closed
			}
			c.counter.add(1, len(f.docs), 0)
			return true
		}
		info, err := d.Info()
		if err != nil {
			c.counter.add(1, 0, 0)
			return true // a file that cannot be read is skipped
		}
		if info.Size() > maxWorkspaceFileSize {
			c.counter.add(0, 0, 1)
			return true
		}
		if !had || f.size != info.Size() || !f.mod.Equal(info.ModTime()) {
			// readIn refuses what a symbolic link leads to outside dir, a
			// device or pipe, and more than the size limit.
			data, err := readIn(dir, filepath.FromSlash(rel))
			if err != nil {
				c.counter.add(1, 0, 0)
				return true
			}
			f = indexedFile{size: info.Size(), mod: info.ModTime()}
			// A UTF-16 file is read as check.Check reads it, in UTF-8.
			if data = extract.UTF8(data); hasRuleWords(data, true) {
				f.text = data
			}
		}
		if f, err = c.index(ctx, key, pathURI(path), f); err != nil {
			return false
		}
		out[key] = f
		c.counter.add(1, len(f.docs), 0)
		return true
	}})
	if err := ctx.Err(); err != nil {
		for key, f := range c.memo {
			if _, ok := out[key]; !ok {
				out[key] = f
			}
		}
		return out, err
	}
	return out, nil
}

// index lists the documents with rules of f, the file with the key at uri,
// unless they were listed for the same features, and checks it unless it
// was checked with the inputs it needs now, and returns f with them. The
// error is ctx's.
func (c *ruleCheck) index(ctx context.Context, key, uri string, f indexedFile) (indexedFile, error) {
	if f.text == nil {
		return f, nil
	}
	kyverno := c.inputs.features.Has(feature.Kyverno)
	if !f.listed || f.kyverno != kyverno {
		found, err := listRules(ctx, uri, f.text, kyverno)
		if err != nil {
			return f, err
		}
		f.listed, f.kyverno, f.found, f.checked, f.docs = true, kyverno, found, false, nil
	}
	if len(f.found) == 0 {
		return f, nil
	}
	want := fileInputs{kube: c.inputs.kube, features: c.inputs.features, assumed: c.inputs.assumed[key]}
	if c.inputs.features.Has(feature.CRDTypedPolicies) && slices.ContainsFunc(f.found, func(d listedDoc) bool { return d.policy }) {
		want.crds = c.inputs.crds
	}
	if f.checked && f.with.equal(want) {
		return f, nil
	}
	run := check.Check
	if c.checker != nil {
		run = c.checker
	}
	// Check's error is for YAML that does not parse, which it also returns
	// as diagnostics, or for ctx.
	diags, _ := run(ctx, uri, f.text, check.WithVersion(want.kube), check.WithFeatures(want.features), check.WithCRDs(c.inputs.crds), check.WithAssumedTypes(want.assumed))
	if err := ctx.Err(); err != nil {
		return f, err
	}
	f.checked, f.with, f.diags, f.docs = true, want, diags, withCompile(f.found, diags)
	return f, nil
}
