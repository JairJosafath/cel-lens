package lsp

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// checkCounter is a checker that counts the checks of each file, by its
// key, and runs check.Check. It is safe for concurrent use.
type checkCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *checkCounter) check(ctx context.Context, file string, data []byte, opts ...check.Option) ([]check.Diagnostic, error) {
	c.mu.Lock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	c.counts[crdKey(file)]++
	c.mu.Unlock()
	return check.Check(ctx, file, data, opts...)
}

// take returns the counts since the last take, by the file's base name.
func (c *checkCounter) take() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]int{}
	for k, n := range c.counts {
		out[filepath.Base(k)] = n
	}
	c.counts = nil
	return out
}

func writeRuleFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRuleIndexRun checks a run of the rule index (cel-lens-core#35): it
// walks the folders with the workspace scan's limits, reads and parses only
// the files with the words of a kind it lists, checks the files with rules
// that are not open, and, given what the last run kept, reads a file again
// only when its size or time changed and checks it again only when its
// text, the Kubernetes version, the features, the CRDs its policies may be
// typed from or the .cellens types of it changed.
func TestRuleIndexRun(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "ws")
	writeRuleFile(t, filepath.Join(dir, "a-crd.yaml"), listCRD)
	writeRuleFile(t, filepath.Join(dir, "b-policy.yaml"), listVAP)
	writeRuleFile(t, filepath.Join(dir, "c-kyverno.yaml"), listKyverno)
	writeRuleFile(t, filepath.Join(dir, "d-plain.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\n")
	writeRuleFile(t, filepath.Join(dir, "e-binding.yaml"), "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicyBinding\nmetadata: {name: b}\nspec: {policyName: limits}\n")
	writeRuleFile(t, filepath.Join(dir, "f-open.yaml"), listCRD)
	writeRuleFile(t, filepath.Join(dir, ".git", "g.yaml"), listCRD)
	writeRuleFile(t, filepath.Join(dir, "node_modules", "n.yaml"), listCRD)
	writeRuleFile(t, filepath.Join(dir, "z-big.yaml"), listCRD+"# "+strings.Repeat("x", maxWorkspaceFileSize)+"\n")
	writeRuleFile(t, filepath.Join(root, "out.yaml"), listCRD)
	if err := os.Symlink(filepath.Join(root, "out.yaml"), filepath.Join(dir, "y-link.yaml")); err != nil {
		t.Logf("no symbolic link: %v", err)
	}
	counter := &checkCounter{}
	inputs := ruleInputs{kube: kubeversion.Default(), crds: &check.CRDs{}, open: map[string]bool{crdKey(pathURI(filepath.Join(dir, "f-open.yaml"))): true}}
	var memo map[string]indexedFile
	run := func(in ruleInputs, limit int) map[string]indexedFile {
		t.Helper()
		c := &ruleCheck{folders: []string{dir}, dirFS: os.DirFS, limit: limit, inputs: in, memo: memo, checker: counter.check}
		files, err := c.run(context.Background())
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
		memo = files
		return files
	}
	listedFiles := func(files map[string]indexedFile) []string {
		var out []string
		for key, f := range files {
			if len(f.docs) > 0 {
				out = append(out, filepath.Base(key))
			}
		}
		slices.Sort(out)
		return out
	}
	files := run(inputs, maxWorkspaceFiles)
	if got, want := listedFiles(files), []string{"a-crd.yaml", "b-policy.yaml"}; !slices.Equal(got, want) {
		t.Errorf("run() lists %v, want %v", got, want)
	}
	if got, want := counter.take(), map[string]int{"a-crd.yaml": 1, "b-policy.yaml": 1}; !maps.Equal(got, want) {
		t.Errorf("first run checked %v, want %v", got, want)
	}
	for _, name := range []string{"d-plain.yaml", "e-binding.yaml"} {
		f := files[crdKey(pathURI(filepath.Join(dir, name)))]
		if name == "d-plain.yaml" && (f.text != nil || f.listed) {
			t.Errorf("%s kept %q, listed %t, want no text, as it has none of the words, and not parsed", name, f.text, f.listed)
		}
		if f.checked {
			t.Errorf("%s checked, want no check of a file with no rules", name)
		}
	}
	for _, name := range []string{"f-open.yaml", "g.yaml", "n.yaml", "z-big.yaml", "y-link.yaml"} {
		for key := range files {
			if filepath.Base(key) == name {
				t.Errorf("run() kept %s, want it left out", name)
			}
		}
	}

	steps := []struct {
		name   string
		change func(in *ruleInputs)
		want   map[string]int
	}{
		{"nothing changed", func(*ruleInputs) {}, map[string]int{}},
		{"a file changed on disk", func(*ruleInputs) {
			writeRuleFile(t, filepath.Join(dir, "a-crd.yaml"), listCRD+"# changed\n")
		}, map[string]int{"a-crd.yaml": 1}},
		{"the Kubernetes version", func(in *ruleInputs) { in.kube, _ = kubeversion.Parse("1.33") }, map[string]int{"a-crd.yaml": 1, "b-policy.yaml": 1}},
		{"kyverno on", func(in *ruleInputs) { in.features = feature.Of(feature.Kyverno) }, map[string]int{"a-crd.yaml": 1, "b-policy.yaml": 1, "c-kyverno.yaml": 1}},
		{"crd-typed-policies on", func(in *ruleInputs) { in.features = feature.Of(feature.Kyverno, feature.CRDTypedPolicies) }, map[string]int{"a-crd.yaml": 1, "b-policy.yaml": 1, "c-kyverno.yaml": 1}},
		{"the CRDs changed", func(in *ruleInputs) {
			var crds check.CRDs
			crds.Set(context.Background(), "/elsewhere/crd.yaml", []byte(listCRD))
			in.crds = crds.Snapshot()
		}, map[string]int{"b-policy.yaml": 1, "c-kyverno.yaml": 1}},
		{"the .cellens types of a file", func(in *ruleInputs) {
			in.assumed = map[string][]check.AssumedType{crdKey(pathURI(filepath.Join(dir, "b-policy.yaml"))): {{Kind: "ValidatingAdmissionPolicy", Name: "limits", Variable: "params", Source: ".cellens", Line: 3}}}
		}, map[string]int{"b-policy.yaml": 1}},
		{"nothing changed again", func(*ruleInputs) {}, map[string]int{}},
	}
	for _, s := range steps {
		s.change(&inputs)
		run(inputs, maxWorkspaceFiles)
		if got := counter.take(); !maps.Equal(got, s.want) {
			t.Errorf("after %s, the run checked %v, want %v", s.name, got, s.want)
		}
	}
	if got, want := listedFiles(memo), []string{"a-crd.yaml", "b-policy.yaml", "c-kyverno.yaml"}; !slices.Equal(got, want) {
		t.Errorf("with kyverno on, run() lists %v, want %v", got, want)
	}
	// The cap counts YAML files in the order of the walk: two of them.
	if got, want := listedFiles(run(inputs, 2)), []string{"a-crd.yaml", "b-policy.yaml"}; !slices.Equal(got, want) {
		t.Errorf("with a cap of 2 files, run() lists %v, want %v", got, want)
	}
	if got := listedFiles(run(inputs, 1)); !slices.Equal(got, []string{"a-crd.yaml"}) {
		t.Errorf("with a cap of 1 file, run() lists %v, want [a-crd.yaml]", got)
	}
}

// TestRuleIndexRunCancelled checks that a run stopped by its context
// returns ctx's error with what the last run kept of the files it did not
// walk, so the next run need not check them again.
func TestRuleIndexRunCancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRuleFile(t, filepath.Join(dir, "crd.yaml"), listCRD)
	in := ruleInputs{kube: kubeversion.Default(), crds: &check.CRDs{}}
	first, err := (&ruleCheck{folders: []string{dir}, dirFS: os.DirFS, limit: maxWorkspaceFiles, inputs: in}).run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	kept, err := (&ruleCheck{folders: []string{dir}, dirFS: os.DirFS, limit: maxWorkspaceFiles, inputs: in, memo: first}).run(ctx)
	if err == nil || len(kept) != 1 {
		t.Errorf("cancelled run() = %d files, %v, want the 1 file kept and ctx's error", len(kept), err)
	}
}

// ruleClient is a session served over pipes for the tests of the loop.
type ruleClient struct {
	t *testing.T
	c *jsonrpc.Conn
	// answers are the answers read before they were waited for, by id.
	answers map[string]jsonrpc.Message
}

func (rc ruleClient) request(id, method string, params any) {
	rc.t.Helper()
	if err := rc.c.Request(id, method, params); err != nil {
		rc.t.Fatal(err)
	}
}

func (rc ruleClient) notify(method string, params any) {
	rc.t.Helper()
	if err := rc.c.Notify(method, params); err != nil {
		rc.t.Fatal(err)
	}
}

// open opens the document at u with text and reads the messages up to its
// diagnostics, so the server never waits to send them.
func (rc ruleClient) open(u, text string) {
	rc.t.Helper()
	rc.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": u, "languageId": "yaml", "version": 1, "text": text}})
	readUntil(rc.t, rc.c, func(m jsonrpc.Message) bool {
		return m.Method == "textDocument/publishDiagnostics" && strings.Contains(string(m.Params), u)
	})
}

// answer reads the messages up to the answer to id.
func (rc ruleClient) answer(id string) jsonrpc.Message {
	rc.t.Helper()
	if m, ok := rc.answers[id]; ok {
		delete(rc.answers, id)
		return m
	}
	for {
		m := readMessage(rc.t, rc.c)
		if m.Method != "" || len(m.ID) == 0 {
			continue
		}
		if string(m.ID) == `"`+id+`"` {
			return m
		}
		rc.answers[strings.Trim(string(m.ID), `"`)] = m
	}
}

func serveRules(t *testing.T, ss *session, folder string) ruleClient {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	ss.conn = jsonrpc.NewConn(serverIn, serverOut)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- ss.serve(ctx, serverIn) }()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = serverOut.Close()
		<-served
	})
	rc := ruleClient{t, jsonrpc.NewConn(clientIn, clientOut), map[string]jsonrpc.Message{}}
	rc.request("init", "initialize", map[string]any{"capabilities": map[string]any{}, "workspaceFolders": []map[string]any{{"uri": pathURI(folder)}}})
	rc.answer("init")
	return rc
}

func ruleListParams() map[string]any { return map[string]any{"command": commandRuleList} }

// TestRuleIndexLeavesTheLoopFree checks that the rule index runs off the
// message loop and is no command job (cel-lens-core#35): while it runs, the
// loop answers a hover, the four commands' slots stay free for four
// exampleResults, a fifth command is refused for those four, cancelling the
// one ruleList waiting answers it as cancelled and stops the run, the next
// ruleList starts another, and shutdown answers it as other commands are.
func TestRuleIndexLeavesTheLoopFree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a.yaml", "b.yaml", "c.yaml", "d.yaml"} {
		writeRuleFile(t, filepath.Join(dir, name), listCRD)
	}
	ss := newSession(t)
	// Every walk of the workspace by a job waits until release is closed.
	release := make(chan struct{})
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	ss.fsys = func(dir string) fs.FS { <-release; return os.DirFS(dir) }
	// The runs of the rule index wait for their context.
	started := make(chan struct{}, 2)
	ss.ruleWork = func(ctx context.Context, c *ruleCheck) (map[string]indexedFile, error) {
		started <- struct{}{}
		<-ctx.Done()
		return c.memo, ctx.Err()
	}
	rc := serveRules(t, ss, dir)
	u := pathURI(filepath.Join(dir, "a.yaml"))
	rc.open(u, listCRD)

	rc.request("list1", "workspace/executeCommand", ruleListParams())
	<-started
	rc.request("hover", "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": 19, "character": 30}})
	if m := rc.answer("hover"); m.Error != nil || !strings.Contains(string(m.Result), "size") {
		t.Errorf("hover while the index runs = %s, %+v, want an answer", m.Result, m.Error)
	}
	for _, name := range []string{"a.yaml", "b.yaml", "c.yaml", "d.yaml"} {
		rc.request("results-"+name, "workspace/executeCommand", map[string]any{"command": commandExampleResults, "arguments": []any{map[string]any{"uri": pathURI(filepath.Join(dir, name))}}})
	}
	rc.request("fifth", "workspace/executeCommand", map[string]any{"command": commandTypeChoices})
	if m := rc.answer("fifth"); m.Error == nil || m.Error.Code != jsonrpc.CodeRequestFailed || !strings.Contains(m.Error.Message, "at most 4 commands") || !strings.Contains(m.Error.Message, "and 4 run") {
		t.Errorf("a fifth command = %+v, want it refused for the four exampleResults running", m.Error)
	}
	rc.notify("$/cancelRequest", map[string]any{"id": "list1"})
	if m := rc.answer("list1"); m.Error == nil || m.Error.Code != jsonrpc.CodeRequestCancelled || !strings.Contains(m.Error.Message, "cel-lens.ruleList was cancelled") {
		t.Errorf("cancelled ruleList = %s, %+v, want it answered as cancelled", m.Result, m.Error)
	}
	rc.request("list2", "workspace/executeCommand", ruleListParams())
	<-started // the cancelled run ended, and list2 started the next
	released.Do(func() { close(release) })
	for _, name := range []string{"a.yaml", "b.yaml", "c.yaml", "d.yaml"} {
		if m := rc.answer("results-" + name); m.Error != nil {
			t.Errorf("exampleResults of %s = %+v, want an answer", name, m.Error)
		}
	}
	rc.request("down", "shutdown", nil)
	if m := rc.answer("list2"); m.Error == nil || m.Error.Code != jsonrpc.CodeRequestFailed || m.Error.Message != "the server shut down before the command ended" {
		t.Errorf("ruleList at shutdown = %s, %+v, want RequestFailed as for other commands", m.Result, m.Error)
	}
	rc.answer("down")
}

// TestRuleIndexRunsAgain checks that a ruleList is answered from a run
// whose inputs are still the session's: a document open when the run
// started and closed while it ran, which the run did not walk, makes the
// next run start for the request, which lists the file from disk; a
// ruleList with an argument that is no object is refused.
func TestRuleIndexRunsAgain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "crd.yaml")
	writeRuleFile(t, path, listCRD)
	ss := newSession(t)
	runs := make(chan struct{})
	ss.ruleWork = func(ctx context.Context, c *ruleCheck) (map[string]indexedFile, error) {
		runs <- struct{}{}
		<-runs
		return c.run(ctx)
	}
	rc := serveRules(t, ss, dir)
	u := pathURI(path)
	rc.open(u, listVAP)
	rc.request("bad", "workspace/executeCommand", map[string]any{"command": commandRuleList, "arguments": []any{"since"}})
	if m := rc.answer("bad"); m.Error == nil || m.Error.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("ruleList with an argument that is no object = %+v, want InvalidParams", m.Error)
	}
	rc.request("list", "workspace/executeCommand", ruleListParams())
	<-runs
	rc.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": u}})
	// The loop has handled the close once it answers what comes after.
	rc.request("hover", "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": u}, "position": map[string]any{"line": 0, "character": 0}})
	rc.answer("hover")
	runs <- struct{}{} // the first run ends, with the file left to the loop
	<-runs             // and the next starts
	runs <- struct{}{}
	m := rc.answer("list")
	var got RuleList
	if err := json.Unmarshal(m.Result, &got); err != nil || len(got.Documents) != 1 || got.Documents[0].Kind != "CustomResourceDefinition" {
		t.Errorf("ruleList = %s, %+v, want the CRD on disk", m.Result, m.Error)
	}
}

// TestRuleIndexAskedAgain checks that a ruleList sent again with nothing
// changed gets the same answer from a run that checks nothing again.
func TestRuleIndexAskedAgain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRuleFile(t, filepath.Join(dir, "crd.yaml"), listCRD)
	ss := newSession(t)
	counter := &checkCounter{}
	ss.checker = counter.check
	rc := serveRules(t, ss, dir)
	answers := make([]json.RawMessage, 2)
	checks := make([]map[string]int, 2)
	for i, id := range []string{"one", "two"} {
		rc.request(id, "workspace/executeCommand", ruleListParams())
		answers[i] = rc.answer(id).Result
		checks[i] = counter.take()
	}
	if string(answers[0]) != string(answers[1]) || !maps.Equal(checks[0], map[string]int{"crd.yaml": 1}) || len(checks[1]) != 0 {
		t.Errorf("ruleList twice = %s then %s, checks %v then %v, want the same answer, one check, then none", answers[0], answers[1], checks[0], checks[1])
	}
}

// TestForgetRulesUnder checks that what the rule index kept of the files
// below a folder that left the workspace is dropped, but of those below a
// folder still walked, such as one inside the folder removed (#35, #38).
func TestForgetRulesUnder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	key := func(rel string) string { return crdKey(pathURI(filepath.Join(root, filepath.FromSlash(rel)))) }
	ss := newSession(t)
	ss.rules.files = map[string]indexedFile{key("a/x.yaml"): {}, key("a/inner/y.yaml"): {}, key("b/z.yaml"): {}, key("ab/w.yaml"): {}}
	ss.forgetRulesUnder([]string{key("a")}, []string{filepath.Join(root, "a", "inner"), filepath.Join(root, "b")})
	got := slices.Sorted(maps.Keys(ss.rules.files))
	if want := []string{key("a/inner/y.yaml"), key("ab/w.yaml"), key("b/z.yaml")}; !slices.Equal(got, want) {
		t.Errorf("forgetRulesUnder(a) kept %q, want %q", got, want)
	}
}

// TestRuleIndexUTF16 checks that a CRD written in UTF-16, which check.Check
// reads, is listed as it is in UTF-8, with the same rules, places and
// compile states (#35): the index reads the file in UTF-8, as Check does.
func TestRuleIndexUTF16(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	text := strings.Replace(listCRD, "rule: has(self.spec)", "rule: self.nope > 0", 1)
	data := []byte{0xff, 0xfe}
	for _, u := range utf16.Encode([]rune(text)) {
		data = binary.LittleEndian.AppendUint16(data, u)
	}
	writeRuleFile(t, filepath.Join(dir, "utf16.yaml"), string(data))
	writeRuleFile(t, filepath.Join(dir, "utf8.yaml"), text)
	files, err := (&ruleCheck{folders: []string{dir}, dirFS: os.DirFS, limit: maxWorkspaceFiles, inputs: ruleInputs{kube: kubeversion.Default(), crds: &check.CRDs{}}}).run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	short := func(name string) []string {
		var out []string
		for _, d := range files[crdKey(pathURI(filepath.Join(dir, name)))].docs {
			for _, r := range d.Rules {
				out = append(out, fmt.Sprintf("%s %v %v %s %d", r.Label, r.Location.Range, r.Span, r.Compile.State, r.Compile.Problems))
			}
		}
		return out
	}
	got, want := short("utf16.yaml"), short("utf8.yaml")
	if len(want) != 3 || !slices.Equal(got, want) {
		t.Errorf("rules of the UTF-16 CRD = %q, want those of the UTF-8 one, %q", got, want)
	}
}
