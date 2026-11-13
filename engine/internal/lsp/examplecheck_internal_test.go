package lsp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/cel-lens/engine/internal/examples"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

const gadgetCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: gadgets.example.com}
spec:
  group: example.com
  names: {plural: gadgets, singular: gadget, kind: Gadget, listKind: GadgetList}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema: {type: object, properties: {spec: {type: object, properties: {size: {type: integer}}}}}
`

// badGadget is an example of gadgetCRD with one problem, an unknown field.
const badGadget = "apiVersion: example.com/v1\nkind: Gadget\nmetadata: {name: g}\nspec: {size: 1, extra: 1}\n"

// TestExampleCheckLimits checks that the check of the examples finds them
// as typeChoices does: it stops at the file cap of the workspace scan,
// skips a file over the size limit, a .cellens and the CRD's own file,
// takes an open file's text over the disk's, and at most maxExamples
// examples of a CRD.
func TestExampleCheckLimits(t *testing.T) {
	t.Parallel()
	big := badGadget + "# " + strings.Repeat("x", maxWorkspaceFileSize) + "\n"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		open    map[string]string
		fileCap int
		want    []string // the files with problems, and how many
	}{
		{"file cap", map[string]string{"a.yaml": badGadget, "b.yaml": badGadget, "c.yaml": badGadget}, nil, 3, []string{"a.yaml 1", "b.yaml 1"}},
		{"example cap", map[string]string{"a.yaml": strings.Repeat(badGadget+"---\n", maxExamples-1), "b.yaml": badGadget + "---\n" + badGadget}, nil, 0, []string{"a.yaml 99", "b.yaml 1"}},
		{"size limit", map[string]string{"a.yaml": big, "b.yml": badGadget}, nil, 0, []string{"b.yml 1"}},
		{"no .cellens", map[string]string{".cellens": badGadget, "x/.cellens": badGadget}, nil, 0, nil},
		{"the CRD's own file", map[string]string{"a.yaml": badGadget}, map[string]string{"a-crd.yaml": gadgetCRD + "---\n" + badGadget}, 0, []string{"a.yaml 1"}},
		{"an open file", map[string]string{"a.yaml": badGadget, "b.yaml": "kind: Gadget\n"}, map[string]string{"a.yaml": "kind: Gadget\n", "b.yaml": badGadget}, 0, []string{"b.yaml 1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, text := range tc.files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			crdFile := filepath.Join(dir, "a-crd.yaml")
			if err := os.WriteFile(crdFile, []byte(gadgetCRD), 0o600); err != nil {
				t.Fatal(err)
			}
			open := map[string][]byte{}
			for name, text := range tc.open {
				open[crdKey(pathURI(filepath.Join(dir, name)))] = []byte(text)
			}
			limit := tc.fileCap
			if limit == 0 {
				limit = maxWorkspaceFiles
			}
			c := exampleCheck{
				crdFiles: []string{crdKey(pathURI(crdFile))}, open: open, folders: []string{dir},
				dirFS: os.DirFS, limit: limit, kube: kubeversion.Default(), memo: &exampleMemo{},
			}
			found, err := c.run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for key, f := range found {
				rel, _ := filepath.Rel(dir, filepath.FromSlash(key))
				got = append(got, filepath.ToSlash(rel)+" "+strconv.Itoa(len(f.diags)))
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("examples with problems = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		crdFile := filepath.Join(dir, "crd.yaml")
		if err := os.WriteFile(crdFile, []byte(gadgetCRD), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := exampleCheck{crdFiles: []string{crdKey(pathURI(crdFile))}, folders: []string{dir}, dirFS: os.DirFS, limit: 10, kube: kubeversion.Default(), memo: &exampleMemo{}}
		if _, err := c.run(ctx); err == nil {
			t.Error("run(cancelled) error = nil, want the context's")
		}
	})
}

// TestExampleCheckMemo checks that a check reads again only the CRDs whose
// text or Kubernetes version changed.
func TestExampleCheckMemo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crdFile := filepath.Join(dir, "crd.yaml")
	if err := os.WriteFile(crdFile, []byte(gadgetCRD), 0o600); err != nil {
		t.Fatal(err)
	}
	key := crdKey(pathURI(crdFile))
	memo := &exampleMemo{}
	read := func(kube kubeversion.Version) *exampleMemo {
		c := exampleCheck{crdFiles: []string{key}, folders: []string{dir}, dirFS: os.DirFS, limit: 10, kube: kube, memo: memo}
		if _, err := c.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		return c.memo
	}
	first := read(kubeversion.Default())
	crds := first.crds[key].crds
	if len(crds) != 1 {
		t.Fatalf("CRDs read = %d, want 1", len(crds))
	}
	if again := read(kubeversion.Default()); again.crds[key].crds[0] != crds[0] {
		t.Error("a second check read the CRD again, want it kept")
	}
	older, err := kubeversion.Parse("1.33")
	if err != nil {
		t.Fatal(err)
	}
	if other := read(older); other.crds[key].crds[0] == crds[0] {
		t.Error("a check for another Kubernetes version kept the CRD, want it read again")
	}
}

// TestExampleCheckWaits checks that a check of the examples starts only
// exampleDelay after the last change, that a change stops the check
// running and drops what it found, and that one check runs at a time.
func TestExampleCheckWaits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	crdFile := filepath.Join(dir, "crd.yaml")
	for name, text := range map[string]string{crdFile: gadgetCRD, filepath.Join(dir, "a.yaml"): badGadget} {
		if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	ss := newSession(t)
	ss.init, ss.folders = true, []string{dir}
	ss.crds.Set(ctx, crdKey(pathURI(crdFile)), []byte(gadgetCRD))
	waits, stopped := 0, 0
	ss.exampleTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
		if d != exampleDelay {
			t.Errorf("wait before a check = %v, want %v", d, exampleDelay)
		}
		waits++
		return make(chan time.Time), func() bool { stopped++; return true }
	}
	ss.examplesChanged()
	ss.startExamples(ctx)
	if ss.exampleRun != nil {
		t.Fatal("a check started before its wait was over")
	}
	ss.examplesChanged()
	if waits != 2 || stopped != 1 {
		t.Errorf("after two changes, waits = %d, stopped = %d, want 2 and 1: a change waits again", waits, stopped)
	}
	ss.exampleWait, ss.cancelExampleWait = nil, nil // the wait is over, as the message loop takes it
	ss.startExamples(ctx)
	run := ss.exampleRun
	if run == nil {
		t.Fatal("no check started once its wait was over")
	}
	ss.examplesChanged()
	ss.exampleWait, ss.cancelExampleWait = nil, nil
	ss.startExamples(ctx)
	if ss.exampleRun != run {
		t.Error("a second check started while one ran")
	}
	if err := ss.endExamples(ctx, <-run.done); err != nil {
		t.Fatal(err)
	}
	if ss.exampleFound != nil {
		t.Errorf("what the check a change stopped found was kept: %v", ss.exampleFound)
	}
	ss.startExamples(ctx)
	next := ss.exampleRun
	if next == nil || next == run {
		t.Fatal("no new check started once the stopped one ended")
	}
	if err := ss.endExamples(ctx, <-next.done); err != nil {
		t.Fatal(err)
	}
	// The file of the CRD is there too, with what its rules (none) do
	// (#15).
	withProblems := 0
	for _, f := range ss.exampleFound {
		if len(f.diags) > 0 {
			withProblems++
		}
	}
	if withProblems != 1 || len(ss.exampleFound) != 2 {
		t.Errorf("the check found problems in %d files of %d, want 1 of 2", withProblems, len(ss.exampleFound))
	}
}

// TestBusyGate checks that a check waits for the message loop only while
// it handles a message, or until its context is done.
func TestBusyGate(t *testing.T) {
	t.Parallel()
	var g busyGate
	g.wait(context.Background()) // not busy: returns at once
	g.enter()
	done := make(chan struct{})
	go func() {
		g.wait(context.Background())
		close(done)
	}()
	g.leave()
	<-done
	g.enter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g.wait(ctx) // busy, but ctx is done: returns
	g.leave()
}

// TestExampleCheckKeepsChecked checks that a check of the examples takes
// the examples of a file whose text and CRDs did not change as the last
// check left them, checked, and finds and checks again those of a file
// that changed, and all of them when the CRD did.
func TestExampleCheckKeepsChecked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("crd.yaml", gadgetCRD)
	write("a.yaml", badGadget)
	write("b.yaml", badGadget)
	memo := &exampleMemo{}
	// found returns the examples of each file a check found, by name.
	found := func() map[string]*examples.Object {
		t.Helper()
		c := exampleCheck{folders: []string{dir}, dirFS: os.DirFS, limit: 10, kube: kubeversion.Default(), memo: memo}
		out := map[string]*examples.Object{}
		if _, err := c.examples(context.Background(), func(f walked, _ []byte, objs []*examples.Object) {
			if len(objs) != 1 || len(objs[0].Problems) != 1 {
				t.Errorf("%s: %d examples, want 1 with its one problem", f.rel, len(objs))
				return
			}
			out[f.rel] = objs[0]
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := found()
	write("b.yaml", strings.Replace(badGadget, "size: 1", "size: 22", 1))
	second := found()
	if second["a.yaml"] != first["a.yaml"] {
		t.Error("the example of the unchanged a.yaml was found and checked again, want it kept")
	}
	if second["b.yaml"] == first["b.yaml"] {
		t.Error("the example of the changed b.yaml was kept, want it found and checked again")
	}
	write("crd.yaml", strings.Replace(gadgetCRD, "type: integer", "type: integer, minimum: 0", 1))
	if third := found(); third["a.yaml"] == second["a.yaml"] {
		t.Error("the example of a.yaml was kept after its CRD changed, want it checked again")
	}
}

// TestExampleResultsOff checks that while the setting exampleResults is
// false nothing about the examples is evaluated: a change sets no wait and
// starts no check, even once one is due; turning it on starts the wait,
// and the check after it (#15).
func TestExampleResultsOff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, text := range map[string]string{filepath.Join(dir, "crd.yaml"): gadgetCRD, filepath.Join(dir, "a.yaml"): badGadget} {
		if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	ss := newSession(t)
	ss.init, ss.folders = true, []string{dir}
	off := false
	if _, _, err := ss.configure(&kubeOptions{ExampleResults: &off}); err != nil {
		t.Fatal(err)
	}
	waits := 0
	ss.exampleTimer = func(time.Duration) (<-chan time.Time, func() bool) {
		waits++
		return make(chan time.Time), func() bool { return true }
	}
	ss.examplesChanged()
	ss.startExamples(ctx)
	if waits != 0 || ss.exampleRun != nil {
		t.Fatalf("with exampleResults false, a change set %d waits and started a check: %t, want none", waits, ss.exampleRun != nil)
	}
	on := true
	if _, _, err := ss.configure(&kubeOptions{ExampleResults: &on}); err != nil {
		t.Fatal(err)
	}
	if err := ss.switchExamples(ctx); err != nil {
		t.Fatal(err)
	}
	if waits != 1 {
		t.Fatalf("turning exampleResults on set %d waits, want 1", waits)
	}
	ss.exampleWait, ss.cancelExampleWait = nil, nil // the wait is over
	ss.startExamples(ctx)
	if ss.exampleRun == nil {
		t.Fatal("no check started once exampleResults was on and the wait over")
	}
	if err := ss.endExamples(ctx, <-ss.exampleRun.done); err != nil {
		t.Fatal(err)
	}
	if len(ss.exampleFound) == 0 {
		t.Error("the check with exampleResults true found nothing, want the problem of a.yaml")
	}
}
