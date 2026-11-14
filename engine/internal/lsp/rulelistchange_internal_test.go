package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// The documents of the tests of the list kept current.
const (
	// semverPolicy compiles from Kubernetes 1.34 on, where isSemver is.
	semverPolicy = "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: semver}\nspec:\n  validations:\n    - expression: \"isSemver('1.2.3')\"\n"
	// configCRD has a rule on spec.config, which is dyn until a .cellens
	// types it int: then startsWith does not apply.
	configCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: configs.example.com}
spec:
  group: example.com
  scope: Namespaced
  names: {plural: configs, kind: Config}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: "self.spec.config.startsWith('a')"
          properties:
            spec:
              type: object
              properties:
                config:
                  x-kubernetes-int-or-string: true
`
	configLens = "types:\n  - file: config.yaml\n    document: CustomResourceDefinition configs.example.com\n    version: v1\n    path: spec.config\n    type: int\n"
	// limitCRD defines Limit, whose spec.max is limitType; limitPolicy reads
	// params.spec.max as a string, which an integer is not.
	limitCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: limits.example.com}
spec:
  group: example.com
  scope: Namespaced
  names: {plural: limits, kind: Limit}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                max: {type: limitType}
`
	limitPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: limit}
spec:
  paramKind: {apiVersion: example.com/v1, kind: Limit}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], resources: [deployments]}
  validations:
    - expression: "params.spec.max.startsWith('a')"
`
)

// followClient talks to a session served over pipes as an editor that
// follows the list does: what it sends goes out in order without waiting
// for the server, so neither waits for the other; the answers it reads are
// kept by id, and the notifications cel-lens/ruleListChanged in order.
type followClient struct {
	t       *testing.T
	out     chan any
	in      chan jsonrpc.Message
	answers map[string]jsonrpc.Message
	notes   []ruleNote
	seq     int
}

// ruleNote is a notification cel-lens/ruleListChanged.
type ruleNote struct {
	Generation int64     `json:"generation"`
	Scan       ScanState `json:"scan"`
}

// serveFollowing serves ss, set up as the tests of the list kept current
// want it, initialized with folders and the initializationOptions opts.
func serveFollowing(t *testing.T, ss *session, folders []string, opts map[string]any) *followClient {
	t.Helper()
	if ss.ruleTimer == nil {
		ss.ruleTimer = func(time.Duration) (<-chan time.Time, func() bool) { return fired(), func() bool { return true } }
	}
	if ss.exampleTimer == nil {
		ss.exampleTimer = func(time.Duration) (<-chan time.Time, func() bool) {
			return make(chan time.Time), func() bool { return true } // no check of the examples
		}
	}
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ss.conn = jsonrpc.NewConn(serverIn, serverOut)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- ss.serve(ctx, serverIn) }()
	fc := &followClient{t: t, out: make(chan any, 1000), in: make(chan jsonrpc.Message), answers: map[string]jsonrpc.Message{}}
	conn := jsonrpc.NewConn(clientIn, clientOut)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case v := <-fc.out:
				body, _ := json.Marshal(v)
				if _, err := fmt.Fprintf(clientOut, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	})
	wg.Go(func() {
		for {
			m, err := conn.Read()
			if err != nil {
				return
			}
			select {
			case fc.in <- m:
			case <-done:
				return
			}
		}
	})
	t.Cleanup(func() {
		cancel()
		close(done)
		_ = clientOut.Close()
		_ = serverOut.Close()
		_ = clientIn.Close()
		<-served
		wg.Wait()
	})
	list := []map[string]any{}
	for _, f := range folders {
		list = append(list, map[string]any{"uri": pathURI(f)})
	}
	fc.request("initialize", map[string]any{"capabilities": map[string]any{}, "workspaceFolders": list, "initializationOptions": opts})
	return fc
}

// fired is a wait that is over.
func fired() <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return ch
}

func (fc *followClient) notify(method string, params any) {
	fc.out <- map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
}

// request sends a request and returns its answer.
func (fc *followClient) request(method string, params any) jsonrpc.Message {
	fc.t.Helper()
	fc.seq++
	id := fmt.Sprintf("r%d", fc.seq)
	fc.out <- map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	for {
		if m, ok := fc.answers[id]; ok {
			delete(fc.answers, id)
			return m
		}
		fc.read()
	}
}

// read reads the next message: an answer is kept by id, a notification of
// the list in notes.
func (fc *followClient) read() {
	fc.t.Helper()
	select {
	case m := <-fc.in:
		switch {
		case m.Method == notifyRuleList:
			var n ruleNote
			if err := json.Unmarshal(m.Params, &n); err != nil {
				fc.t.Fatal(err)
			}
			fc.notes = append(fc.notes, n)
		case m.Method == "" && len(m.ID) > 0:
			fc.answers[strings.Trim(string(m.ID), `"`)] = m
		}
	case <-time.After(30 * time.Second): // a hang fails here, not at the package's timeout
		fc.t.Fatal("no message from the server within 30s")
	}
}

// note returns the first notification that match accepts, dropping those
// before it.
func (fc *followClient) note(match func(ruleNote) bool) ruleNote {
	fc.t.Helper()
	for {
		for i, n := range fc.notes {
			if match(n) {
				fc.notes = fc.notes[i+1:]
				return n
			}
		}
		fc.notes = nil
		fc.read()
	}
}

// doneAfter accepts a notification of a list newer than generation gen,
// once no scan runs.
func doneAfter(gen int64) func(ruleNote) bool {
	return func(n ruleNote) bool { return n.Generation > gen && n.Scan.State == scanDone }
}

// list asks for the list, since a generation or whole.
func (fc *followClient) list(since *int64) RuleList {
	fc.t.Helper()
	args := []any{}
	if since != nil {
		args = append(args, map[string]any{"since": *since})
	}
	m := fc.request("workspace/executeCommand", map[string]any{"command": commandRuleList, "arguments": args})
	var l RuleList
	if err := json.Unmarshal(m.Result, &l); err != nil || m.Error != nil {
		fc.t.Fatalf("ruleList = %s, %+v (%v)", m.Result, m.Error, err)
	}
	return l
}

// ruleModel is a client's copy of the list: the documents of each file, by
// URI.
type ruleModel map[string][]RuleDocument

// apply applies an answer of cel-lens.ruleList to m.
func (m ruleModel) apply(l RuleList) {
	if l.Full {
		clear(m)
	}
	byFile := map[string][]RuleDocument{}
	for _, d := range l.Documents {
		byFile[d.URI] = append(byFile[d.URI], d)
	}
	maps.Copy(m, byFile)
	for _, uri := range l.Removed {
		delete(m, uri)
	}
}

// files are the URIs of the files of the documents of l, sorted.
func files(l RuleList) []string {
	var out []string
	for _, d := range l.Documents {
		if !slices.Contains(out, d.URI) {
			out = append(out, d.URI)
		}
	}
	slices.Sort(out)
	return out
}

// stateOf is the compile state of the first rule of the file at uri in m.
func (m ruleModel) stateOf(uri string) string {
	if docs := m[uri]; len(docs) > 0 && len(docs[0].Rules) > 0 {
		return docs[0].Rules[0].Compile.State
	}
	return "absent"
}

// TestRuleListKeptCurrent checks that the list is kept current at the cost
// of what changed (cel-lens-core#36): for a client that follows it, the
// index runs from initialize and follows each change that changes a rule's
// entry, then tells the client the new generation; the answer since the
// generation before holds the files that changed, each whole, and the URIs
// of those that left the list, and applying the answers in turn to an empty
// list gives the whole list. A change that changes no entry raises no
// generation and checks nothing; an unknown generation gets the whole list.
func TestRuleListKeptCurrent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ws, ws2 := filepath.Join(root, "ws"), filepath.Join(root, "ws2")
	at := func(rel string) (string, string) {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		return p, pathURI(p)
	}
	write := func(rel, text string) string {
		p, u := at(rel)
		writeRuleFile(t, p, text)
		return u
	}
	aURI := write("a.yaml", listVAP)
	semverURI := write("semver.yaml", semverPolicy)
	kyvernoURI := write("kyverno.yaml", listKyverno)
	configURI := write("config.yaml", configCRD)
	write("limit-crd.yaml", strings.Replace(limitCRD, "limitType", "integer", 1))
	limitURI := write("limit-policy.yaml", limitPolicy)
	writeRuleFile(t, filepath.Join(ws2, "e.yaml"), listCRD)
	eURI := pathURI(filepath.Join(ws2, "e.yaml"))
	ss := newSession(t)
	counter := &checkCounter{}
	ss.checker = counter.check
	fc := serveFollowing(t, ss, []string{ws}, map[string]any{"ruleList": true, "experimental": []string{"cellens-types"}})

	// The index starts at initialize: the first scan is told without a request.
	first := fc.note(func(n ruleNote) bool { return n.Scan.State == scanDone })
	model := ruleModel{}
	before := int64(-1)
	model.apply(fc.list(&before)) // older than the session's first: the whole list
	gen := first.Generation
	watched := func(uri string, typ int) {
		fc.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": uri, "type": typ}}})
	}
	editorText := strings.Replace(listVAP, "variables.replicas <= 5", "variables.replicas <=", 1)
	steps := []struct {
		name    string
		change  func()
		files   []string // the files the answer since holds
		removed []string
		check   func(m ruleModel) string // what is wrong with the model, "" for nothing
	}{
		{"an open file", func() {
			fc.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": aURI, "languageId": "yaml", "version": 1, "text": editorText}})
		}, []string{aURI}, nil, func(m ruleModel) string {
			if m[aURI][0].Rules[2].Compile.State != compileError {
				return "the editor's broken validation is not an error"
			}
			return ""
		}},
		{"an open file edited", func() {
			fc.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": aURI, "version": 2}, "contentChanges": []map[string]any{{"text": strings.Replace(listVAP, "at most 5 replicas", "at most five", 1)}}})
		}, []string{aURI}, nil, func(m ruleModel) string {
			if r := m[aURI][0].Rules[2]; r.Compile.State != compileOK || r.Label != "at most five" {
				return "the fixed validation is not ok and labelled as the editor has it"
			}
			return ""
		}},
		{"an open file closed, back to the disk's text", func() {
			fc.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": aURI}})
		}, []string{aURI}, nil, func(m ruleModel) string {
			if r := m[aURI][0].Rules[2]; r.Compile.State != compileOK || r.Label != "at most 5 replicas" {
				return "the validation is not as the disk has it"
			}
			return ""
		}},
		{"a file created", func() { watched(write("b.yaml", listCRD), fileCreated) }, []string{pathURI(filepath.Join(ws, "b.yaml"))}, nil, nil},
		{"a file changed", func() {
			watched(write("b.yaml", strings.Replace(listCRD, "has(self.spec)", "self.nope", 1)), fileChanged)
		}, []string{pathURI(filepath.Join(ws, "b.yaml"))}, nil, nil},
		{"a file with no rule left", func() {
			watched(write("b.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: b}\n"), fileChanged)
		}, nil, []string{pathURI(filepath.Join(ws, "b.yaml"))}, nil},
		{"a file deleted", func() {
			p, u := at("a.yaml")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			watched(u, fileDeleted)
		}, nil, []string{aURI}, nil},
		{"a folder created", func() {
			write("sub/d.yaml", listCRD)
			_, u := at("sub")
			watched(u, fileCreated)
		}, []string{pathURI(filepath.Join(ws, "sub", "d.yaml"))}, nil, nil},
		{"a folder deleted", func() {
			p, u := at("sub")
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			watched(u, fileDeleted)
		}, nil, []string{pathURI(filepath.Join(ws, "sub", "d.yaml"))}, nil},
		{"a .cellens that types a rule's value", func() {
			watched(write(".cellens", configLens), fileCreated)
		}, []string{configURI}, nil, func(m ruleModel) string {
			if m.stateOf(configURI) != compileWarning {
				return "the rule on the typed config is " + m.stateOf(configURI) + ", want warning"
			}
			return ""
		}},
		{"kubernetesVersion", func() {
			fc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"kubernetesVersion": "1.33"}})
		}, []string{semverURI}, nil, func(m ruleModel) string {
			if m.stateOf(semverURI) != compileError {
				return "isSemver compiles in 1.33"
			}
			return ""
		}},
		{"kyverno on", func() {
			fc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": []string{"cellens-types", "kyverno"}}})
		}, []string{kyvernoURI}, nil, nil},
		{"kyverno off", func() {
			fc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": []string{"cellens-types"}}})
		}, nil, []string{kyvernoURI}, nil},
		{"crd-typed-policies on", func() {
			fc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": []string{"cellens-types", "crd-typed-policies"}}})
		}, []string{limitURI}, nil, func(m ruleModel) string {
			if m.stateOf(limitURI) == compileOK {
				return "params.spec.max, an integer, takes startsWith"
			}
			return ""
		}},
		{"the CRDs known", func() {
			watched(write("limit-crd.yaml", strings.Replace(limitCRD, "limitType", "string", 1)), fileChanged)
		}, []string{limitURI}, nil, func(m ruleModel) string {
			if m.stateOf(limitURI) != compileOK {
				return "params.spec.max, a string, does not take startsWith"
			}
			return ""
		}},
		{"a folder added", func() {
			fc.notify("workspace/didChangeWorkspaceFolders", map[string]any{"event": map[string]any{"added": []map[string]any{{"uri": pathURI(ws2)}}, "removed": []any{}}})
		}, []string{eURI}, nil, nil},
		{"a folder removed", func() {
			fc.notify("workspace/didChangeWorkspaceFolders", map[string]any{"event": map[string]any{"added": []any{}, "removed": []map[string]any{{"uri": pathURI(ws2)}}}})
		}, nil, []string{eURI}, nil},
	}
	for _, s := range steps {
		t.Logf("step: %s", s.name)
		s.change()
		n := fc.note(doneAfter(gen))
		since := fc.list(&gen)
		if since.Full || since.Generation < n.Generation || !slices.Equal(files(since), s.files) || !slices.Equal(since.Removed, s.removed) {
			t.Errorf("%s: ruleList since %d = generation %d, full %t, files %q, removed %q; want generation %d or newer, files %q, removed %q",
				s.name, gen, since.Generation, since.Full, files(since), since.Removed, n.Generation, s.files, s.removed)
		}
		model.apply(since)
		if s.check != nil {
			if msg := s.check(model); msg != "" {
				t.Errorf("%s: %s", s.name, msg)
			}
		}
		gen = since.Generation
	}

	// A file with no CEL changes nothing: no generation, no check.
	counter.take()
	watched(write("plain.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: p}\n"), fileCreated)
	if l := fc.list(&gen); l.Generation != gen || len(l.Documents) > 0 || len(l.Removed) > 0 {
		t.Errorf("after a file with no CEL, ruleList since %d = %d, %q, %q; want nothing new", gen, l.Generation, files(l), l.Removed)
	}
	if got := counter.take(); len(got) > 0 {
		t.Errorf("after a file with no CEL, the index checked %v, want nothing", got)
	}

	whole := fc.list(nil)
	if !whole.Full || whole.Generation != gen {
		t.Errorf("ruleList = full %t, generation %d, want the whole list at %d", whole.Full, whole.Generation, gen)
	}
	want := ruleModel{}
	want.apply(whole)
	if !reflect.DeepEqual(model, want) {
		t.Errorf("the list made of the answers since = %v, want the whole list %v", slices.Sorted(maps.Keys(model)), slices.Sorted(maps.Keys(want)))
	}
	future := gen + 1000
	if l := fc.list(&future); !l.Full || len(l.Documents) != len(whole.Documents) {
		t.Errorf("ruleList since a generation the engine never had = full %t, %d documents; want the whole list", l.Full, len(l.Documents))
	}
}

// TestRuleListNotFollowed checks that a client that does not ask for the
// list at initialize gets no notification, and that the index does not run
// before its first cel-lens.ruleList, which answers the whole list with a
// generation (cel-lens-core#36).
func TestRuleListNotFollowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRuleFile(t, filepath.Join(dir, "a.yaml"), listVAP)
	ss := newSession(t)
	counter := &checkCounter{}
	ss.checker = counter.check
	fc := serveFollowing(t, ss, []string{dir}, map[string]any{})
	u := pathURI(filepath.Join(dir, "b.yaml"))
	fc.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": listCRD}})
	fc.request("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": 0, "character": 0}})
	if got := counter.take(); got["a.yaml"] > 0 {
		t.Errorf("before any ruleList the index checked %v, want nothing of a.yaml", got)
	}
	l := fc.list(nil)
	if !l.Full || l.Generation <= 0 || len(files(l)) != 2 {
		t.Errorf("ruleList = full %t, generation %d, files %q; want the whole list of a.yaml and b.yaml", l.Full, l.Generation, files(l))
	}
	fc.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": u, "version": 2}, "contentChanges": []map[string]any{{"text": listVAP}}})
	again := fc.list(&l.Generation)
	if len(fc.notes) > 0 {
		t.Errorf("a client that did not ask got notifications %+v", fc.notes)
	}
	if again.Full || again.Generation <= l.Generation || !slices.Equal(files(again), []string{u}) {
		t.Errorf("ruleList since %d = full %t, generation %d, files %q; want b.yaml at a newer generation", l.Generation, again.Full, again.Generation, files(again))
	}
}

// heldTimer is a ruleTimer whose waits of a notification the test ends,
// and whose waits before a run are over at once.
type heldTimer struct {
	mu    sync.Mutex
	waits []chan time.Time
}

func (h *heldTimer) after(d time.Duration) (<-chan time.Time, func() bool) {
	if d != scanNotifyEvery {
		return fired(), func() bool { return true }
	}
	ch := make(chan time.Time, 1)
	h.mu.Lock()
	h.waits = append(h.waits, ch)
	h.mu.Unlock()
	return ch, func() bool { return true }
}

// fire ends the waits so far, and reports how many there were.
func (h *heldTimer) fire() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.waits)
	for _, ch := range h.waits {
		ch <- time.Time{}
	}
	h.waits = nil
	return n
}

// TestRuleListScan checks scan (cel-lens-core#36): scanning while the index
// walks the workspace, told at once and then at most once each 500 ms, not
// at every file, and done once the walk ends, with the YAML files walked,
// those with rules and those the limits left out, past the cap and over
// 1 MiB, as cel-lens-core examples counts them; scanning again while a
// folder added is walked (#38); a change that changes an entry is told
// once, without scanning.
func TestRuleListScan(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	one, two, three := filepath.Join(root, "one"), filepath.Join(root, "two"), filepath.Join(root, "three")
	for i := range 3 {
		writeRuleFile(t, filepath.Join(one, fmt.Sprintf("crd%d.yaml", i)), listCRD)
		writeRuleFile(t, filepath.Join(two, fmt.Sprintf("plain%d.yaml", i)), "kind: ConfigMap\n")
	}
	writeRuleFile(t, filepath.Join(two, "big.yaml"), listCRD+"# "+strings.Repeat("x", maxWorkspaceFileSize)+"\n")
	writeRuleFile(t, filepath.Join(three, "policy.yaml"), listVAP)
	ss := newSession(t)
	// Three CRDs, then big.yaml, counted and left out for its size, and three
	// ConfigMaps; then the cap, which leaves the last file of two out.
	ss.fileCap = 7
	writeRuleFile(t, filepath.Join(two, "zz-past-the-cap.yaml"), listVAP)
	timer := &heldTimer{}
	ss.ruleTimer = timer.after
	// The walk of folder two waits until release is closed.
	release := make(chan struct{})
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	ss.fsys = func(dir string) fs.FS {
		if dir == two {
			<-release
		}
		return os.DirFS(dir)
	}
	fc := serveFollowing(t, ss, []string{one, two}, map[string]any{"ruleList": true})
	start := fc.note(func(ruleNote) bool { return true })
	if start.Scan.State != scanScanning {
		t.Errorf("first notification = %+v, want scanning", start)
	}
	// The walk of folder one counts its files; folder two waits. Nothing is
	// told until the wait of 500 ms ends.
	for timer.fire() == 0 {
		fc.request("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": "file:///none.yaml"}, "position": map[string]any{"line": 0, "character": 0}})
	}
	mid := fc.note(func(ruleNote) bool { return true })
	if mid.Scan.State != scanScanning || mid.Scan.YAMLFiles == 0 || mid.Scan.YAMLFiles > 3 || mid.Generation != start.Generation {
		t.Errorf("notification during the walk = %+v, want scanning, files of folder one only, generation %d", mid, start.Generation)
	}
	released.Do(func() { close(release) })
	end := fc.note(func(n ruleNote) bool { return n.Scan.State == scanDone })
	want := ScanState{State: scanDone, YAMLFiles: 6, FilesWithRules: 3, FilesLeftOut: 2}
	if mid.Scan.FilesLeftOut != 0 {
		t.Errorf("notification during the walk of folder one = %+v, want no file left out yet", mid)
	}
	if end.Scan != want || end.Generation <= start.Generation {
		t.Errorf("notification at the end of the walk = %+v, want %+v at a newer generation than %d", end, want, start.Generation)
	}
	for _, n := range fc.notes {
		if n.Scan.State == scanScanning {
			t.Errorf("notification %+v after the walk ended, want none scanning", n)
		}
	}

	// A folder added is walked by a scan (#38).
	fc.notify("workspace/didChangeWorkspaceFolders", map[string]any{"event": map[string]any{"added": []map[string]any{{"uri": pathURI(three)}}, "removed": []any{}}})
	if n := fc.note(func(ruleNote) bool { return true }); n.Scan.State != scanScanning {
		t.Errorf("notification after a folder added = %+v, want scanning", n)
	}
	added := fc.note(func(n ruleNote) bool { return n.Scan.State == scanDone })
	// The cap is reached in the folders before it: its policy is left out.
	if want := (ScanState{State: scanDone, YAMLFiles: 6, FilesWithRules: 3, FilesLeftOut: 3}); added.Scan != want {
		t.Errorf("notification after the added folder's walk = %+v, want %+v", added, want)
	}

	// A change is told once, without scanning.
	u := pathURI(filepath.Join(one, "crd0.yaml"))
	fc.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": strings.Replace(listCRD, "has(self.spec)", "self.nope", 1)}})
	changed := fc.note(func(n ruleNote) bool { return n.Generation > added.Generation })
	if changed.Scan.State != scanDone {
		t.Errorf("notification after an edit = %+v, want done, no scan", changed)
	}
}

// TestBusyCountsTheRuleIndex checks that a batch whose change the rule index
// is to run for, or runs for, is busy, so the check of the open documents it
// owes waits for the index too (#38, cel-lens-core#36).
func TestBusyCountsTheRuleIndex(t *testing.T) {
	t.Parallel()
	ss := newSession(t)
	ss.rules.follow, ss.batch = true, 3
	ss.forBatch()
	if !ss.busy(3) || ss.busy(2) {
		t.Errorf("busy(3), busy(2) with batch 3 due = %t, %t; want true, false", ss.busy(3), ss.busy(2))
	}
	ss.rules.dueBatches = nil
	ss.rules.run = &ruleRun{batches: []int{3}}
	if !ss.busy(3) {
		t.Error("busy(3) while the index runs for batch 3 = false, want true")
	}
	ss.rules.run = nil
	if ss.busy(3) {
		t.Error("busy(3) once the index ran = true, want false")
	}
	ss.rules.follow = false
	ss.forBatch()
	if ss.busy(3) {
		t.Error("busy(3) for a client that does not follow the list = true, want false: no run is due")
	}
}

// TestRuleListLeftTwice checks that a file a client had leaves its copy
// when it left the list, came back and left again after the generation the
// client asks since (cel-lens-core#36): removed names it, as it names every
// file that left after that generation and is not listed now. Two ways:
// a file deleted, created and deleted again on disk; and a file opened
// under a URI spelled otherwise than its file's, which takes it out of the
// list under the file's URI, then closed, which brings it back, then
// deleted.
func TestRuleListLeftTwice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		steps func(fc *followClient, path, uri string) []func()
	}{
		{"deleted, created, deleted", func(fc *followClient, path, uri string) []func() {
			del := func() {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				fc.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": uri, "type": fileDeleted}}})
			}
			return []func(){del, func() {
				writeRuleFile(t, path, listVAP)
				fc.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": uri, "type": fileCreated}}})
			}, del}
		}},
		{"opened under another spelling, closed, deleted", func(fc *followClient, path, uri string) []func() {
			other := strings.Replace(uri, "/f.yaml", "/%66.yaml", 1) // the same file
			return []func(){func() {
				fc.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": other, "languageId": "yaml", "version": 1, "text": listVAP}})
			}, func() {
				fc.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": other}})
			}, func() {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				fc.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": uri, "type": fileDeleted}}})
			}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			path := filepath.Join(ws, "f.yaml")
			writeRuleFile(t, filepath.Join(ws, "keep.yaml"), listCRD)
			writeRuleFile(t, path, listVAP)
			fc := serveFollowing(t, newSession(t), []string{ws}, map[string]any{"ruleList": true})
			gen := fc.note(func(n ruleNote) bool { return n.Scan.State == scanDone }).Generation
			model := ruleModel{}
			whole := fc.list(nil)
			model.apply(whole)
			n := whole.Generation
			for _, step := range tc.steps(fc, path, pathURI(path)) {
				step()
				gen = fc.note(doneAfter(gen)).Generation
			}
			since := fc.list(&n)
			model.apply(since)
			fresh := ruleModel{}
			fresh.apply(fc.list(nil))
			if !reflect.DeepEqual(model, fresh) {
				t.Errorf("the list since %d (files %q, removed %q) applied = %q, want the whole list %q",
					n, files(since), since.Removed, slices.Sorted(maps.Keys(model)), slices.Sorted(maps.Keys(fresh)))
			}
		})
	}
}

// TestRuleListRandomChanges checks, on sequences of changes drawn at random
// with fixed seeds, that the answers since applied in order, at random
// points of each sequence, always give the whole list (cel-lens-core#36,
// #37). The changes are of every kind the list follows: files written,
// created and deleted on disk; files opened under their own URI or under
// another spelling of it, with a text other than the disk's, edited and
// closed; kyverno turned on and off; the Kubernetes version 1.33 and 1.34,
// in which a policy compiles or not; and examples of a CRD edited, deleted
// and created, which change what its rules did to them.
func TestRuleListRandomChanges(t *testing.T) {
	t.Parallel()
	for _, seed := range []uint64{36, 37, 45} {
		t.Run(strconv.FormatUint(seed, 10), func(t *testing.T) {
			t.Parallel()
			randomChanges(t, seed)
		})
	}
}

// randomChanges runs the sequence of TestRuleListRandomChanges drawn from
// the seed.
func randomChanges(t *testing.T, seed uint64) {
	t.Helper()
	ws := t.TempDir()
	rng := rand.New(rand.NewPCG(seed, 45)) //nolint:gosec // a fixed sequence for a test, not for security
	const names, samples, steps = 4, 3, 50
	path := func(i int) string { return filepath.Join(ws, fmt.Sprintf("f%d.yaml", i)) }
	sample := func(i int) string { return filepath.Join(ws, "samples", fmt.Sprintf("s%d.yaml", i)) }
	// text is the i-th file's text with the marker in a message, a policy
	// or a CRD, so each new marker changes its entry.
	text := func(i int, marker string) string {
		if i%2 == 0 {
			return strings.Replace(listVAP, "at most 5 replicas", marker, 1)
		}
		return strings.Replace(listCRD, "at most 10", marker, 1)
	}
	// The CRD of the examples, of a group of its own, and its examples,
	// which its rule "at most 10" refuses or not by their size.
	writeRuleFile(t, filepath.Join(ws, "examples-crd.yaml"), strings.ReplaceAll(exampleCRD, "example.com", "ex.example.com"))
	exampleOf := func(i, size int) string {
		return strings.Replace(sizedWidget(fmt.Sprintf("s%d", i), size), "example.com/v1", "ex.example.com/v1", 1)
	}
	sizes := map[int]int{}
	for i := range samples {
		sizes[i] = 3
		writeRuleFile(t, sample(i), exampleOf(i, 3))
	}
	writeRuleFile(t, filepath.Join(ws, "semver.yaml"), semverPolicy)
	writeRuleFile(t, filepath.Join(ws, "kyverno.yaml"), listKyverno)
	onDisk := map[int]int{} // the version of the text of each file on disk
	for i := range names {
		onDisk[i] = 1
		writeRuleFile(t, path(i), text(i, "version 1"))
	}
	ss := newSession(t)
	ss.exampleTimer = func(time.Duration) (<-chan time.Time, func() bool) { return fired(), func() bool { return true } }
	fc := serveFollowing(t, ss, []string{ws}, map[string]any{"ruleList": true})
	fc.note(func(n ruleNote) bool { return n.Scan.State == scanDone })
	openAs := map[int]string{} // the URI each open file was opened under
	edits := 0
	kyverno, version := false, "1.37"
	watched := func(p string, typ int) {
		fc.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": pathURI(p), "type": typ}}})
	}
	settings := func() {
		on := []string{}
		if kyverno {
			on = append(on, "kyverno")
		}
		fc.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": on, "kubernetesVersion": version}})
	}
	model := ruleModel{}
	held := int64(-1) // the generation of the client's copy; none yet
	for step := range steps {
		// The generation before the change: asking for the list waits for
		// the run of any change before, so the change of this step is told
		// in a later one.
		gen := fc.list(nil).Generation
		i := rng.IntN(names)
		uri := openAs[i]
		op := rng.IntN(10)
		exampleStep := op == 2
		switch {
		case op == 0:
			kyverno = !kyverno
			settings()
		case op == 1:
			version = map[string]string{"1.37": "1.33", "1.33": "1.34", "1.34": "1.33"}[version]
			settings()
		case op == 2:
			s := rng.IntN(samples)
			if _, ok := sizes[s]; ok && rng.IntN(2) == 0 {
				delete(sizes, s)
				if err := os.Remove(sample(s)); err != nil {
					t.Fatal(err)
				}
				watched(sample(s), fileDeleted)
				break
			}
			size, created := 20, true
			if old, ok := sizes[s]; ok {
				size, created = 23-old, false // 3 and 20 in turn
			}
			sizes[s] = size
			writeRuleFile(t, sample(s), exampleOf(s, size))
			typ := fileChanged
			if created {
				typ = fileCreated
			}
			watched(sample(s), typ)
		case uri != "" && op < 7:
			edits++
			fc.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": edits}, "contentChanges": []map[string]any{{"text": text(i, fmt.Sprintf("editor %d", edits))}}})
		case uri != "":
			delete(openAs, i)
			fc.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
		case op < 5:
			edits++
			uri = pathURI(path(i))
			if rng.IntN(2) == 0 {
				uri = strings.Replace(uri, fmt.Sprintf("/f%d.yaml", i), fmt.Sprintf("/%%66%d.yaml", i), 1) // the same file
			}
			openAs[i] = uri
			fc.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "yaml", "version": edits, "text": text(i, fmt.Sprintf("editor %d", edits))}})
		default:
			if _, ok := onDisk[i]; ok && rng.IntN(3) == 0 {
				delete(onDisk, i)
				if err := os.Remove(path(i)); err != nil {
					t.Fatal(err)
				}
				watched(path(i), fileDeleted)
				break
			}
			typ := fileChanged
			if _, ok := onDisk[i]; !ok {
				typ = fileCreated
			}
			onDisk[i]++
			writeRuleFile(t, path(i), text(i, fmt.Sprintf("version %d", onDisk[i])))
			watched(path(i), typ)
		}
		// Each change but an example's changes an entry; what an example
		// changes shows only once the check of the examples ends, after the
		// changes pause, and may be nothing.
		if !exampleStep {
			fc.note(doneAfter(gen))
		}
		if rng.IntN(3) != 0 && step != steps-1 {
			continue // the client asks after some steps only
		}
		// The check of the examples may change the list between two
		// requests: the client asks again until the two meet.
		for try := 0; ; try++ {
			l := fc.list(&held)
			model.apply(l)
			held = l.Generation
			whole := fc.list(nil)
			if whole.Generation != held {
				if try == 10 {
					t.Fatalf("step %d: the list kept changing", step)
				}
				continue
			}
			fresh := ruleModel{}
			fresh.apply(whole)
			if !reflect.DeepEqual(model, fresh) {
				t.Fatalf("step %d: the answers since applied = %q, want the whole list %q", step, slices.Sorted(maps.Keys(model)), slices.Sorted(maps.Keys(fresh)))
			}
			break
		}
	}
}

// TestRuleListAnsweredAtOnce checks that a client that follows the list,
// asking when no change came since the list was made, is answered from it
// at once, with no run of the index; after a change, the request waits for
// the run (cel-lens-core#36).
func TestRuleListAnsweredAtOnce(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeRuleFile(t, filepath.Join(ws, "a.yaml"), listVAP)
	ss := newSession(t)
	var mu sync.Mutex
	runs := 0
	ss.ruleWork = func(ctx context.Context, c *ruleCheck) (map[string]indexedFile, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		return c.run(ctx)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return runs
	}
	fc := serveFollowing(t, ss, []string{ws}, map[string]any{"ruleList": true})
	fc.note(func(n ruleNote) bool { return n.Scan.State == scanDone })
	// What initialize starts besides the index, such as the search for
	// .cellens files, may make a run due as it ends; once none is, two
	// requests in a row run nothing.
	var before int
	var gen int64
	answered := false
	for range 5 {
		before = count()
		whole := fc.list(nil)
		gen = whole.Generation
		since := fc.list(&gen)
		if count() == before {
			answered = true
			if !whole.Full || len(whole.Documents) != 1 || since.Full || len(since.Documents) > 0 {
				t.Errorf("ruleList with nothing changed = whole %t with %d documents, since %d: %d documents; want the whole list, nothing since",
					whole.Full, len(whole.Documents), gen, len(since.Documents))
			}
			break
		}
	}
	if !answered {
		t.Error("ruleList with nothing changed ran the index each time, want it answered at once")
	}
	before = count()
	writeRuleFile(t, filepath.Join(ws, "b.yaml"), listCRD)
	fc.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []map[string]any{{"uri": pathURI(filepath.Join(ws, "b.yaml")), "type": fileCreated}}})
	if l := fc.list(&gen); len(l.Documents) != 1 || count() == before {
		t.Errorf("ruleList after a change = %d documents after %d runs, want b.yaml from a run", len(l.Documents), count()-before)
	}
}
