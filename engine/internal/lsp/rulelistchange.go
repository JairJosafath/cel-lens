package lsp

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/inline"
)

// A client keeps its copy of the list of rules current at the cost of what
// changed: each answer of cel-lens.ruleList has a generation, which grows
// each time the entry of a file changes, and {since: n} answers only the
// files whose entry changed after n, and those that left the list. A client
// that asks for it at initialize (initializationOptions.ruleList) is told by
// cel-lens/ruleListChanged when there is something new, and how far the
// walk of the workspace is; for it, the index follows every change of what a
// rule's entry depends on, in the background, once changes pause for
// ruleDelay, and the notification is made on the message loop from what
// the run found.

// notifyRuleList is the notification a client that follows the list gets.
const notifyRuleList = "cel-lens/ruleListChanged"

const (
	// ruleDelay is how long the index waits after the last change before it
	// runs again, so that it runs once typing pauses, as the check of the
	// examples does.
	ruleDelay = exampleDelay
	// scanNotifyEvery is the least time between two notifications during a
	// scan.
	scanNotifyEvery = 500 * time.Millisecond
)

// The states of a scan.
const (
	scanScanning = "scanning"
	scanDone     = "done"
)

// ScanState is how far the rule index's walk of the workspace is: State is
// scanning while it walks the whole workspace (the first walk, and the one
// after the Kubernetes version, the features or the folders changed), and
// done otherwise; YAMLFiles are the YAML files it walked, FilesWithRules
// those with rules, and FilesLeftOut the files the limits left out (past the
// first 20,000, or over 1 MiB), so far while it scans, else in the last
// walk.
type ScanState struct {
	State          string `json:"state"`
	YAMLFiles      int    `json:"yamlFiles"`
	FilesWithRules int    `json:"filesWithRules"`
	FilesLeftOut   int    `json:"filesLeftOut"`
}

// scanCounter counts what a walk of the index has done; signal gets a value,
// when it has room, each time the counts change. A nil counter counts
// nothing. It is safe for concurrent use.
type scanCounter struct {
	mu                         sync.Mutex
	walked, withRules, leftOut int
	signal                     chan struct{}
}

// add counts walked files walked, of which withRules have rules (a number of
// documents, counted as one file), and leftOut files left out.
func (c *scanCounter) add(walked, withRules, leftOut int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.walked += walked
	c.leftOut += leftOut
	if withRules > 0 {
		c.withRules++
	}
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

// state is the counts as a ScanState in the state s.
func (c *scanCounter) state(s string) ScanState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ScanState{State: s, YAMLFiles: c.walked, FilesWithRules: c.withRules, FilesLeftOut: c.leftOut}
}

// listedFile is the entry of a file in the list: the generation it last
// changed at, and its documents.
type listedFile struct {
	gen  int64
	docs []RuleDocument
}

// publishRules makes the list what files, the files a run kept, and the open
// documents hold (see ruleFiles), and reports whether it changed: each file
// whose entry changed gets the next generation, as does each file that left
// the list, and the generation grows only when one did. The error is ctx's.
func (ss *session) publishRules(ctx context.Context, files map[string]indexedFile) (bool, error) {
	list, err := ss.ruleFiles(ctx, files)
	if err != nil {
		return false, err
	}
	if ss.rules.listed == nil {
		ss.rules.listed, ss.rules.gone = map[string]listedFile{}, map[string]int64{}
	}
	next := ss.rules.gen + 1
	changed := false
	order := make([]string, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, f := range list {
		order = append(order, f.uri)
		seen[f.uri] = true
		old, ok := ss.rules.listed[f.uri]
		if ok && reflect.DeepEqual(old.docs, f.docs) {
			continue
		}
		ss.rules.listed[f.uri] = listedFile{gen: next, docs: f.docs}
		delete(ss.rules.gone, f.uri)
		changed = true
	}
	for uri := range ss.rules.listed {
		if !seen[uri] {
			delete(ss.rules.listed, uri)
			ss.rules.gone[uri] = next
			changed = true
		}
	}
	ss.rules.order = order
	if changed {
		ss.rules.gen = next
	}
	return changed, nil
}

// answerSince is the answer of cel-lens.ruleList for the list as it is: with
// since, the files whose entry changed after that generation, each whole, and
// in Removed the URIs of the files that left the list after it and are not
// listed now, which may name a file the client never had, as one that came
// and left after it (the client ignores those); without, or for a
// generation before the session's first or after its last, as a client of
// an engine that restarted has, the whole list, Full.
func (ss *session) answerSince(since *int64) RuleList {
	full := since == nil || *since < ss.rules.base || *since > ss.rules.gen
	out := RuleList{KubernetesVersion: ss.kube.String(), Generation: ss.rules.gen, Full: full, Documents: []RuleDocument{}}
	for _, uri := range ss.rules.order {
		if f := ss.rules.listed[uri]; full || f.gen > *since {
			out.Documents = append(out.Documents, f.docs...)
		}
	}
	if full {
		return out
	}
	out.Removed = []string{}
	for uri, at := range ss.rules.gone {
		if at > *since {
			out.Removed = append(out.Removed, uri)
		}
	}
	slices.Sort(out.Removed)
	return out
}

// withExamples gives the rules of docs, the documents of the file with the
// key as listRules found them in found, what their examples say, as the line
// above each rule says it: what the last check of the examples found the
// rule did to them, for the Kubernetes version in use, by the rule's
// identity the line finds it by (see ruleExamples). A rule with no example,
// one no example runs on (a policy's variable, a MutatingAdmissionPolicy, a
// Kyverno policy, a ValidatingAdmissionPolicy of another version than v1),
// and every rule while exampleResults is off or before a check of the
// examples for the version in use, has none. A rule whose examples are wrong
// or error is a problem. It returns docs, whose rules it replaces, not
// changes.
func (ss *session) withExamples(key string, found []listedDoc, docs []RuleDocument) []RuleDocument {
	if !ss.knowsRules(key) {
		return docs
	}
	rules := ss.exampleFound[key].rules
	for i := range docs {
		listed := slices.Clone(docs[i].Rules)
		for j := range listed {
			state, words, ok := inline.ExamplesState(rules[found[i].ids[j]])
			if !ok {
				continue
			}
			listed[j].Examples = &RuleExamples{State: state, Words: words}
			listed[j].Problem = listed[j].Problem || state == inline.ExamplesWrong || state == inline.ExamplesError
		}
		docs[i].Rules = listed
	}
	return docs
}

// republishRules makes the list again from what the last run of the index
// kept, when what it holds besides the run's checks may have changed: what
// the rules did to their examples. A client that follows the list is told
// when it changed. While a run is in progress, its end makes the list.
func (ss *session) republishRules(ctx context.Context) error {
	if ss.rules.run != nil || ss.rules.listed == nil || ss.rules.files == nil {
		return nil
	}
	changed, err := ss.publishRules(ctx, ss.rules.files)
	if err != nil || !changed {
		return err
	}
	return ss.notifyRules()
}

// filesWithRules is how many files of the list are in a workspace folder.
func (ss *session) filesWithRules() int {
	n := 0
	for _, f := range ss.rules.listed {
		if len(f.docs) > 0 && f.docs[0].Folder != "" {
			n++
		}
	}
	return n
}

// rulesChanged records that what the entry of a rule depends on may have
// changed, for a client that follows the list: the index runs again
// ruleDelay after the last change, or after the run in progress ends. A
// change in a batch that waits for the index (see busy) is recorded with
// forBatch. For any other client, the next cel-lens.ruleList runs it.
func (ss *session) rulesChanged() {
	if !ss.rules.follow || !ss.init || ss.shutdown {
		return
	}
	ss.rules.due = true
	ss.stopRuleWait()
	after := ss.ruleTimer
	if after == nil {
		after = newTimer
	}
	ss.rules.wait, ss.rules.cancelWait = after(ruleDelay)
}

// forBatch makes the next run of the index, for a client that follows the
// list, part of the batch in hand: the batch is busy until it ends (see
// busy and settle).
func (ss *session) forBatch() {
	if ss.rules.follow && !slices.Contains(ss.rules.dueBatches, ss.batch) {
		ss.rules.dueBatches = append(ss.rules.dueBatches, ss.batch)
	}
}

// stopRuleWait stops the wait before the next run, if any.
func (ss *session) stopRuleWait() {
	if ss.rules.cancelWait != nil {
		ss.rules.cancelWait()
	}
	ss.rules.wait, ss.rules.cancelWait = nil, nil
}

// startDueRuleRun starts the next run of the index when one is due, its wait
// is over and none runs. The error is ctx's.
func (ss *session) startDueRuleRun(ctx context.Context) error {
	if !ss.rules.due || ss.rules.wait != nil || ss.rules.run != nil || ss.shutdown {
		return nil
	}
	inputs, err := ss.ruleInputs(ctx)
	if err != nil {
		return err
	}
	batches := ss.rules.dueBatches
	ss.rules.due, ss.rules.dueBatches = false, nil
	return ss.runRules(ctx, inputs, batches)
}

// progressRules follows the walk of a scan, which counted more: a client
// that follows the list is told how far it is at once, and then at most
// every scanNotifyEvery (see throttleRules).
func (ss *session) progressRules() error {
	run := ss.rules.run
	if run == nil || !run.scan {
		return nil
	}
	ss.rules.scan = run.counter.state(scanScanning)
	ss.rules.progressed = true
	if ss.rules.throttle != nil {
		return nil
	}
	return ss.throttleRules()
}

// throttleRules sends the notification of a scan when it went on since the
// last, and waits scanNotifyEvery for the next; with nothing new it waits
// no more.
func (ss *session) throttleRules() error {
	ss.rules.throttle, ss.rules.stopThrottle = nil, nil
	if !ss.rules.progressed || ss.rules.run == nil || !ss.rules.run.scan {
		return nil
	}
	after := ss.ruleTimer
	if after == nil {
		after = newTimer
	}
	ss.rules.throttle, ss.rules.stopThrottle = after(scanNotifyEvery)
	return ss.notifyRules()
}

// stopRuleThrottle stops the wait for the next notification of a scan.
func (ss *session) stopRuleThrottle() {
	if ss.rules.stopThrottle != nil {
		ss.rules.stopThrottle()
	}
	ss.rules.throttle, ss.rules.stopThrottle = nil, nil
}

// notifyRules tells a client that follows the list the generation and how
// far the scan is, never the list itself.
func (ss *session) notifyRules() error {
	ss.rules.progressed = false
	if !ss.rules.follow {
		return nil
	}
	return ss.conn.Notify(notifyRuleList, map[string]any{"generation": ss.rules.gen, "scan": ss.rules.scan})
}
