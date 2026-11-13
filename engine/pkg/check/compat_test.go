package check

import (
	"runtime"
	"slices"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/util/version"
	basecompatibility "k8s.io/component-base/compatibility"
)

// queued returns once n calls wait at g.
func queued(g *compatGate, n int) {
	for {
		g.mu.Lock()
		l := len(g.queue)
		g.mu.Unlock()
		if l == n {
			return
		}
		runtime.Gosched()
	}
}

// TestCompatGateSameVersion: a call that needs the compatibility version a
// running call has set runs at once, beside it (#14: the editor's check of
// an open CRD never waits for the background check of a closed one). Were
// it to wait, the test would never end.
func TestCompatGateSameVersion(t *testing.T) {
	t.Parallel()
	var g compatGate
	eff := basecompatibility.NewEffectiveVersionFromString("1.37", "", "")
	before := eff.MinCompatibilityVersion()
	v := version.MajorMinor(1, 34)
	in, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		g.do(eff, v, func() {
			close(in)
			<-release
		})
	}()
	<-in
	var saw *version.Version
	g.do(eff, v, func() { saw = eff.MinCompatibilityVersion() })
	close(release)
	<-done
	if !saw.EqualTo(v) {
		t.Errorf("the second call saw %v, want %v", saw, v)
	}
	if got := eff.MinCompatibilityVersion(); !got.EqualTo(before) {
		t.Errorf("after both calls the version is %v, want %v back", got, before)
	}
}

// TestCompatGateOtherVersion: a call that needs another version waits until
// the calls running end; calls that come after it wait for it, even with
// the version that is set; and the waiting calls of one version run
// together.
func TestCompatGateOtherVersion(t *testing.T) {
	t.Parallel()
	var g compatGate
	eff := basecompatibility.NewEffectiveVersionFromString("1.37", "", "")
	before := eff.MinCompatibilityVersion()
	v34, v35 := version.MajorMinor(1, 34), version.MajorMinor(1, 35)
	var (
		mu  sync.Mutex
		ran []string
		wg  sync.WaitGroup
	)
	note := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		ran = append(ran, name+" "+eff.MinCompatibilityVersion().String())
	}
	in, release := make(chan struct{}), make(chan struct{})
	wg.Go(func() {
		g.do(eff, v34, func() {
			close(in)
			<-release
			note("first")
		})
	})
	<-in
	// The two calls with 1.35 run together, or neither returns.
	var together sync.WaitGroup
	together.Add(2)
	wg.Go(func() {
		g.do(eff, v35, func() {
			together.Done()
			together.Wait()
			note("other")
		})
	})
	queued(&g, 1)
	wg.Go(func() { g.do(eff, v34, func() { note("later") }) })
	queued(&g, 2)
	wg.Go(func() {
		g.do(eff, v35, func() {
			together.Done()
			together.Wait()
			note("other")
		})
	})
	queued(&g, 3)
	close(release)
	wg.Wait()
	want := []string{"first 1.34", "other 1.35", "other 1.35", "later 1.34"}
	if !slices.Equal(ran, want) {
		t.Errorf("the calls ran as %q, want %q", ran, want)
	}
	if got := eff.MinCompatibilityVersion(); !got.EqualTo(before) {
		t.Errorf("after the calls the version is %v, want %v back", got, before)
	}
}
