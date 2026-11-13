package lsp

import (
	"bytes"
	"cmp"
	"context"
	"io/fs"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/examples"
	"github.com/JairJosafath/cel-lens/engine/internal/inline"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// The examples of the CustomResourceDefinitions the session knows are
// found and checked against their schemas in the background, never on the
// message loop: a change of a YAML file, open or on disk, of the CRDs or of
// the Kubernetes version makes the check due (examplesChanged), which stops
// the one running; the loop starts the next once nothing changed for
// exampleDelay and none runs (startExamples), and publishes what it found
// when nothing changed meanwhile (endExamples). So typing waits for
// nothing, a check takes no time from it, and the problems of the examples
// come once it pauses.

// exampleDelay is how long the check of the examples waits after the last
// change, so that it runs once typing pauses.
const exampleDelay = 250 * time.Millisecond

// exampleRun is the check of the examples in progress.
type exampleRun struct {
	cancel context.CancelFunc
	// done delivers what it found, once, when its goroutine ends.
	done chan exampleResult
}

// exampleResult is what a check of the examples found: the diagnostics of
// each file with any, by key, nil for a check that was stopped, and what it
// kept for the next.
type exampleResult struct {
	files map[string]exampleFile
	memo  *exampleMemo
}

// exampleFile is what an example file, or a file of CRDs, shows: its
// diagnostics, in the text they were found in, the editor's or the disk's,
// and, for a file of CRDs with examples, what each of its rules does to
// them, found for the Kubernetes version kube (see runRules).
type exampleFile struct {
	text  []byte
	diags []check.Diagnostic
	kube  kubeversion.Version
	rules map[ruleID][]inline.Example
}

// exampleShown is what was last sent for an example file that is not open:
// the URI it was sent to and the diagnostics.
type exampleShown struct {
	uri   string
	diags []map[string]any
}

// exampleMemo is what a check of the examples keeps for the next one, which
// owns it while it runs: the CRDs read from each file, what was read of
// each file on disk, and the examples found and checked in each file.
type exampleMemo struct {
	crds    map[string]crdsRead
	files   map[string]fileRead
	checked map[string]checkedFile
}

// checkedFile are the examples found in a text, of the CRDs looked for, all
// of them, and which were checked: the next check takes them as they are
// for the same text and the same CRDs, which a CRD read again is not.
type checkedFile struct {
	text  []byte
	crds  []*examples.CRD
	found []*examples.Object
	done  map[*examples.Object]bool
}

// crdsRead are the CRDs read from a text for a Kubernetes version.
type crdsRead struct {
	text []byte
	kube kubeversion.Version
	crds []*examples.CRD
}

// fileRead is what a check read of a file on disk of a size and time:
// whether it may hold a CRD, its text when it does or had one of the words,
// the kinds of the CRDs then, and nil when it had none, which a check with
// the same words need not read again.
type fileRead struct {
	size  int64
	mod   time.Time
	crd   bool
	words string
	text  []byte
}

// exampleCheck is a check of the examples: what it needs of the session,
// made on the message loop, so it runs off it.
type exampleCheck struct {
	// crdFiles are the keys of the files with CRDs the session knows, the
	// open ones among them; the check adds those of its walk.
	crdFiles []string
	// open are the texts of the open documents, by key.
	open    map[string][]byte
	folders []string
	dirFS   func(dir string) fs.FS
	limit   int
	kube    kubeversion.Version
	memo    *exampleMemo
	// loop is held while the message loop handles a message; nil never is.
	loop *busyGate
	// results runs the rules of the CRDs on their examples and keeps what
	// they did; nil runs no rule. checks are the message loop's checks of
	// the open files of CRDs, by key, which it takes rather than check
	// them again (see loopChecks).
	results *examples.Cache
	checks  map[string][]check.Diagnostic
}

// examplesChanged records that what the examples are, or their problems,
// may have changed, and stops the check in progress, whose result is then
// dropped. The message loop starts the next exampleDelay after the last
// change (see startExamples); exampleTimer makes the wait.
func (ss *session) examplesChanged() {
	ss.examplesDue = true
	if ss.exampleRun != nil {
		ss.exampleRun.cancel()
	}
	ss.stopExampleWait()
	if ss.examplesOff {
		return // turning exampleResults on starts the wait
	}
	after := ss.exampleTimer
	if after == nil {
		after = newTimer
	}
	ss.exampleWait, ss.cancelExampleWait = after(exampleDelay)
}

// stopExampleWait stops the wait before the next check, if any.
func (ss *session) stopExampleWait() {
	if ss.cancelExampleWait != nil {
		ss.cancelExampleWait()
	}
	ss.exampleWait, ss.cancelExampleWait = nil, nil
}

// forgetExample makes the next check read the file with the key again,
// which changed on disk.
func (ss *session) forgetExample(key string) {
	ss.exampleStale = append(ss.exampleStale, key)
	ss.examplesChanged()
}

// startExamples starts a check of the examples in a goroutine when one is
// due, its wait is over, none runs and the setting exampleResults is on.
func (ss *session) startExamples(ctx context.Context) {
	if !ss.examplesDue || ss.exampleWait != nil || ss.exampleRun != nil || !ss.init || ss.shutdown || ss.examplesOff {
		return
	}
	checks, err := ss.loopChecks(ctx)
	if err != nil {
		return // ctx is done
	}
	ss.examplesDue = false
	memo := ss.exampleMemo
	if memo == nil {
		memo = &exampleMemo{}
	}
	for _, key := range ss.exampleStale {
		delete(memo.files, key)
	}
	ss.exampleMemo, ss.exampleStale = nil, nil
	limit := ss.fileCap
	if limit <= 0 {
		limit = maxWorkspaceFiles
	}
	open := make(map[string][]byte, len(ss.docs))
	for uri, d := range ss.docs {
		open[crdKey(uri)] = d.text // a document's text is replaced, never changed
	}
	c := exampleCheck{
		crdFiles: ss.crds.Files(), open: open, folders: outermost(ss.folders), dirFS: ss.dirFS(), limit: limit, kube: ss.kube, memo: memo, loop: &ss.loopBusy,
		results: &ss.results, checks: checks,
	}
	ctx, cancel := context.WithCancel(ctx)
	run := &exampleRun{cancel: cancel, done: make(chan exampleResult, 1)}
	ss.exampleRun = run
	work := ss.exampleWork
	if work == nil {
		work = func(ctx context.Context, c *exampleCheck) (map[string]exampleFile, error) { return c.run(ctx) }
	}
	go func() {
		files, err := work(ctx, &c)
		if err != nil {
			files = nil // stopped: what it found is not all there is
		}
		run.done <- exampleResult{files: files, memo: c.memo}
	}()
}

// stopExamples cancels the check in progress and waits for it to end, and
// stops the wait for the next.
func (ss *session) stopExamples() {
	ss.stopExampleWait()
	if ss.exampleRun == nil {
		return
	}
	ss.exampleRun.cancel()
	r := <-ss.exampleRun.done
	ss.exampleRun, ss.exampleMemo = nil, r.memo
}

// endExamples handles the end of the check in progress: unless something
// changed meanwhile, it records what the check found, sends the
// diagnostics of each file whose example problems changed, and asks the
// client for the code lenses again when what a rule does to its examples
// changed.
func (ss *session) endExamples(ctx context.Context, r exampleResult) error {
	ss.exampleRun.cancel()
	ss.exampleRun, ss.exampleMemo = nil, r.memo
	if r.files == nil || ss.examplesDue {
		return nil // the next check runs
	}
	return ss.showExamples(ctx, r.files)
}

// showExamples makes files what the examples show, nil for nothing, and
// sends what changed: the diagnostics of each file whose example problems
// changed, and a code lens refresh when what a rule does to its examples
// did.
func (ss *session) showExamples(ctx context.Context, files map[string]exampleFile) error {
	old := ss.exampleFound
	ss.exampleFound = files
	keys := slices.Collect(maps.Keys(old))
	for k := range files {
		if _, ok := old[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	lenses := false
	for _, key := range keys {
		o, n := old[key], files[key]
		lenses = lenses || o.kube != n.kube || !reflect.DeepEqual(o.rules, n.rules)
		if reflect.DeepEqual(o.diags, n.diags) && (len(n.diags) == 0 || bytes.Equal(o.text, n.text)) {
			continue
		}
		if err := ss.sendExamples(ctx, key); err != nil {
			return err
		}
	}
	if !lenses {
		return nil
	}
	ss.lensChanges++
	return ss.refreshCodeLenses()
}

// exampleDiagnostics returns the example problems of the file with the key
// that were found in text, none when they were found in another text.
func (ss *session) exampleDiagnostics(key string, text []byte) []check.Diagnostic {
	if f, ok := ss.exampleFound[key]; ok && bytes.Equal(f.text, text) {
		return f.diags
	}
	return nil
}

// sendExamples sends the diagnostics of the file with the key, whose example
// problems changed: with those of its document when the editor has it open,
// else alone, to the file on disk.
func (ss *session) sendExamples(ctx context.Context, key string) error {
	var uris []string
	for uri := range ss.docs {
		if crdKey(uri) == key {
			uris = append(uris, uri)
		}
	}
	slices.Sort(uris)
	for _, uri := range uris {
		if doc := ss.docs[uri]; doc.checked {
			if err := ss.sendDiagnostics(uri, doc, doc.diags); err != nil {
				return err
			}
		} else if err := ss.publish(ctx, uri); err != nil {
			return err
		}
	}
	if len(uris) > 0 {
		return nil
	}
	return ss.sendClosedExamples(key, pathURI(filepath.FromSlash(key)), ss.exampleFound[key])
}

// sendClosedExamples sends f, the example problems of the file with the key,
// which is not open, to uri, unless the client has them: the URI the last
// ones went to, if any, takes them, and no problems clear them.
func (ss *session) sendClosedExamples(key, uri string, f exampleFile) error {
	diags := make([]map[string]any, 0, len(f.diags))
	lines := splitLines(f.text)
	for _, d := range f.diags {
		diags = append(diags, diagnostic(lines, d))
	}
	shown, was := ss.exampleShown[key]
	if was {
		uri = shown.uri
	}
	if was && reflect.DeepEqual(shown.diags, diags) || !was && len(diags) == 0 {
		return nil
	}
	if err := ss.conn.Notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": diags}); err != nil {
		return err
	}
	if len(diags) == 0 {
		delete(ss.exampleShown, key)
		return nil
	}
	if ss.exampleShown == nil {
		ss.exampleShown = map[string]exampleShown{}
	}
	ss.exampleShown[key] = exampleShown{uri: uri, diags: diags}
	return nil
}

// closeExamples sends, for the document at uri the editor closed, the
// example problems of the file on disk, as far as the last check found them
// in the same text, and none otherwise: the next check finds them.
func (ss *session) closeExamples(uri string) error {
	key := crdKey(uri)
	var f exampleFile
	if path, ok := diskPath(uri); ok {
		if data, err := ss.readFile(path); err == nil {
			f = ss.exampleFound[key]
			if !bytes.Equal(f.text, data) {
				f = exampleFile{}
			}
		}
	}
	delete(ss.exampleShown, key)
	if len(f.diags) == 0 {
		return ss.conn.Notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": []any{}})
	}
	return ss.sendClosedExamples(key, uri, f)
}

// run finds the examples of the CRDs the check knows and checks them (see
// examples), runs the CRDs' rules on them when it has results (see
// runRules), and returns, by key, what each file with anything to show
// shows: the diagnostics of the examples, the warnings of wrong verdicts,
// and what each rule of a file of CRDs does to its examples.
func (c *exampleCheck) run(ctx context.Context) (map[string]exampleFile, error) {
	var found []fileFound
	crds, err := c.examples(ctx, func(f walked, text []byte, objs []*examples.Object) {
		found = append(found, fileFound{f: f, text: text, objs: objs})
	})
	if err != nil {
		return nil, err
	}
	var rules ruleFindings
	if c.results != nil {
		if rules, err = c.runRules(ctx, crds, found); err != nil {
			return nil, err
		}
	}
	out := map[string]exampleFile{}
	for _, ff := range found {
		var diags []check.Diagnostic
		for _, o := range ff.objs {
			diags = append(diags, o.Diagnostics()...)
		}
		diags = append(diags, rules.warnings[ff.f.key]...)
		if len(diags) > 0 {
			out[ff.f.key] = exampleFile{text: ff.text, diags: diags}
		}
	}
	for key, cf := range rules.crds {
		f, ok := out[key]
		if !ok {
			f.text = cf.text
		}
		f.diags = append(f.diags, cf.warnings...)
		f.kube, f.rules = c.kube, cf.rules
		out[key] = f
	}
	for _, f := range out {
		slices.SortStableFunc(f.diags, func(a, b check.Diagnostic) int {
			return cmp.Or(a.Start.Line-b.Start.Line, a.Start.Column-b.Start.Column)
		})
	}
	return out, nil
}

// examples finds the examples of the CRDs the check knows and checks them,
// calls each with the text and the checked examples of each file with
// any, and returns the CRDs. The CRDs are those of the check's files and
// those of the YAML files of the folders, whatever the experimental
// features: it walks them as the search for the examples of typeChoices
// does, with the limits of the workspace scan, an open file with the
// editor's text. It takes at most maxExamples examples of each CRD, in the
// order of the walk. The examples of a file whose text and CRDs are those
// of the last check are taken as that check left them. The error is ctx's.
func (c *exampleCheck) examples(ctx context.Context, each func(f walked, text []byte, objs []*examples.Object)) ([]*examples.CRD, error) {
	files := map[string]fileRead{}
	checked := map[string]checkedFile{}
	var listed []walked
	crdFiles := slices.Clone(c.crdFiles)
	walkYAML(ctx, c.dirFS, c.folders, c.limit, yamlWalk{file: func(dir, rel string, d fs.DirEntry) bool {
		c.wait(ctx)
		f := walked{dir: dir, rel: rel, d: d, key: crdKey(pathURI(filepath.Join(dir, filepath.FromSlash(rel))))}
		listed = append(listed, f)
		if c.hasCRD(f, files) && !slices.Contains(crdFiles, f.key) {
			crdFiles = append(crdFiles, f.key)
		}
		return true
	}})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	crds := c.crds(ctx, crdFiles, files)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(crds) == 0 {
		c.memo.files, c.memo.checked = files, checked
		return nil, nil
	}
	kinds := make([]string, 0, len(crds))
	for _, crd := range crds {
		kinds = append(kinds, crd.Kind)
	}
	slices.Sort(kinds)
	words := strings.Join(slices.Compact(kinds), "\x00")
	taken := map[*examples.CRD]int{}
	for _, f := range listed {
		c.wait(ctx)
		text, ok := c.open[f.key]
		if !ok {
			if text, ok = c.read(f, words, files); !ok {
				continue
			}
		}
		open := slices.DeleteFunc(slices.Clone(crds), func(crd *examples.CRD) bool { return taken[crd] >= maxExamples })
		cf, ok := c.memo.checked[f.key]
		if !ok || !bytes.Equal(cf.text, text) || !slices.Equal(cf.crds, open) {
			found, err := examples.Find(ctx, open, f.key, f.rel, text)
			if err != nil {
				return nil, err
			}
			cf = checkedFile{text: text, crds: open, found: found, done: map[*examples.Object]bool{}}
		}
		checked[f.key] = cf
		objs := slices.DeleteFunc(slices.Clone(cf.found), func(o *examples.Object) bool {
			taken[o.CRD]++
			return taken[o.CRD] > maxExamples
		})
		for _, o := range objs {
			if cf.done[o] {
				continue
			}
			c.wait(ctx)
			if err := examples.Check(ctx, []*examples.Object{o}); err != nil {
				return nil, err
			}
			if ctx.Err() == nil {
				cf.done[o] = true
			}
		}
		if len(objs) > 0 {
			each(f, text, objs)
		}
		if !slices.ContainsFunc(crds, func(crd *examples.CRD) bool { return taken[crd] < maxExamples }) {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.memo.files, c.memo.checked = files, checked
	return crds, nil
}

// wait returns once the message loop handles no message, or ctx is done,
// so the check takes no time from a message.
func (c *exampleCheck) wait(ctx context.Context) {
	if c.loop != nil {
		c.loop.wait(ctx)
	}
}

// busyGate says whether its owner is busy, for others to wait until it is
// not. The zero value is not busy. It is safe for concurrent use.
type busyGate struct {
	mu sync.Mutex
	// busy is closed when the owner is done, nil while it is not busy.
	busy chan struct{}
}

// enter makes g busy until leave.
func (g *busyGate) enter() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.busy = make(chan struct{})
}

// leave ends what enter began.
func (g *busyGate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	close(g.busy)
	g.busy = nil
}

// wait returns once g is not busy, or ctx is done.
func (g *busyGate) wait(ctx context.Context) {
	g.mu.Lock()
	busy := g.busy
	g.mu.Unlock()
	if busy == nil {
		return
	}
	select {
	case <-busy:
	case <-ctx.Done():
	}
}

// walked is a YAML file the walk of a check listed: the folder it is
// below, its slash-separated path there, its entry and its key.
type walked struct {
	dir, rel, key string
	d             fs.DirEntry
}

// crdWord is in the text of every file with a CustomResourceDefinition.
const crdWord = "CustomResourceDefinition"

// crds reads the CRDs of the files with the keys, an open one with the
// editor's text, one the walk read with that text, again only when its text
// or the Kubernetes version changed since the last check.
func (c *exampleCheck) crds(ctx context.Context, keys []string, files map[string]fileRead) []*examples.CRD {
	var out []*examples.CRD
	read := map[string]crdsRead{}
	for _, key := range keys {
		c.wait(ctx)
		text, ok := c.open[key]
		if !ok {
			text = files[key].text
		}
		if text == nil {
			var err error
			if text, err = readUnder(c.folders, filepath.FromSlash(key)); err != nil {
				continue
			}
		}
		r, ok := c.memo.crds[key]
		if !ok || r.kube != c.kube || !bytes.Equal(r.text, text) {
			r = crdsRead{text: text, kube: c.kube, crds: examples.Read(ctx, key, text, c.kube)}
		}
		if ctx.Err() != nil {
			return nil
		}
		read[key] = r
		out = append(out, r.crds...)
	}
	c.memo.crds = read
	return out
}

// hasCRD reports whether the file f of the walk may hold a
// CustomResourceDefinition: whether its text, the editor's when it is open,
// has its kind. It records what it read of the disk in files.
func (c *exampleCheck) hasCRD(f walked, files map[string]fileRead) bool {
	if text, ok := c.open[f.key]; ok {
		return bytes.Contains(text, []byte(crdWord))
	}
	r, ok := c.load(f, "", files)
	return ok && r.crd
}

// read returns the text of the file f of the walk when it has one of the
// words, the CRDs' kinds, or may hold a CRD. It records what it read of the
// disk in files.
func (c *exampleCheck) read(f walked, words string, files map[string]fileRead) ([]byte, bool) {
	r, ok := c.load(f, words, files)
	return r.text, ok && r.text != nil
}

// load returns what there is to know of the file f of the walk for the
// words: whether it may hold a CRD, and its text when it does or has one of
// the words. It is what this check or the last read of the file, when its
// size and time are the same and it knew the text or looked for the same
// words, else it reads the disk, refusing what readIn refuses, and records
// what it read in files.
func (c *exampleCheck) load(f walked, words string, files map[string]fileRead) (fileRead, bool) {
	info, err := f.d.Info()
	if err != nil {
		return fileRead{}, false
	}
	fresh := func(r fileRead, ok bool) bool {
		return ok && r.size == info.Size() && r.mod.Equal(info.ModTime()) && (r.text != nil || r.words == words || words == "")
	}
	if r, ok := files[f.key]; fresh(r, ok) {
		return r, true
	}
	if r, ok := c.memo.files[f.key]; fresh(r, ok) {
		files[f.key] = r
		return r, true
	}
	data, err := readIn(f.dir, filepath.FromSlash(f.rel))
	if err != nil {
		return fileRead{}, false
	}
	r := fileRead{size: info.Size(), mod: info.ModTime(), words: words, crd: bytes.Contains(data, []byte(crdWord))}
	if r.crd || words != "" && slices.ContainsFunc(strings.Split(words, "\x00"), func(w string) bool { return bytes.Contains(data, []byte(w)) }) {
		r.text = data
	}
	files[f.key] = r
	return r, true
}
