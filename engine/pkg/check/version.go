package check

import (
	"errors"
	"slices"
	"sync"

	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apiserver/pkg/util/compatibility"
	basecompatibility "k8s.io/component-base/compatibility"

	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// Option changes how Check works.
type Option func(*settings)

type settings struct {
	version kubeversion.Version
	crds    *CRDs
	on      feature.Set
	assumed []AssumedType
}

// typingCRDs returns the CRDs that type params, none unless the experimental
// feature crd-typed-policies is on. They type object and oldObject too.
func (s settings) typingCRDs() *CRDs {
	if !s.on.Has(feature.CRDTypedPolicies) {
		return nil
	}
	return s.crds
}

// WithVersion makes Check match the API server of version v, with its CEL
// libraries and cost rules. Without it Check matches kubeversion.Default.
func WithVersion(v kubeversion.Version) Option {
	return func(s *settings) { s.version = v }
}

// compat admits the calls of UnderVersion. The setting it guards is the
// Kubernetes libraries' own, process-wide, so there is one gate for the
// process.
var compat compatGate

// UnderVersion calls fn with the process-wide compatibility version of the
// Kubernetes libraries set to v's, as the API server of version v has it,
// then puts the old one back. The libraries read that setting where they
// take no version, such as validation.ValidateCustomResourceDefinition and
// apiservervalidation.NewSchemaValidator, so code that calls them for a
// version calls them in fn: Check does. Calls for the same version run
// together, as the setting is the same for all of them; a call for another
// one waits until those running end, and the calls after it wait for it.
// Code that reads the setting outside UnderVersion may see any version. fn
// must not call UnderVersion itself: once a call for another version waits,
// that inner call waits behind it, for ever. The error says the libraries'
// version cannot be set, and fn did not run.
func UnderVersion(v kubeversion.Version, fn func()) error {
	eff, ok := compatibility.DefaultComponentGlobalsRegistry.EffectiveVersionFor(basecompatibility.DefaultKubeComponent).(basecompatibility.MutableEffectiveVersion)
	if !ok {
		return errors.New("set Kubernetes compatibility version: the libraries' version is not settable")
	}
	compat.do(eff, v.Compat(), fn)
	return nil
}

// compatGate lets calls that need a compatibility version set in a
// MutableEffectiveVersion share it: the Kubernetes libraries read that
// setting at several points of a CRD's validation, so it must stay the
// same from the start of a call to its end, but any number of calls that
// need the same version can run at once. A call that needs another version
// waits until the calls running end, and the calls that come after it wait
// for it, whatever version they need, so no call waits for ever. The zero
// value is ready to use; a gate is safe for concurrent use, with one
// MutableEffectiveVersion.
type compatGate struct {
	mu      sync.Mutex
	running int              // calls running, all with the version cur
	cur     *version.Version // what the setting is while calls run
	old     *version.Version // what it was before them, put back after
	queue   []*compatTurn    // the calls waiting, in order
}

// compatTurn is a call waiting at a compatGate for version, admitted when
// in is closed.
type compatTurn struct {
	version *version.Version
	in      chan struct{}
}

// do calls fn with the compatibility version of eff set to v, then puts
// the old one back once no other call needs v.
func (g *compatGate) do(eff basecompatibility.MutableEffectiveVersion, v *version.Version, fn func()) {
	g.enter(eff, v)
	defer g.leave(eff)
	fn()
}

// enter returns once the compatibility version of eff is v for the caller,
// at once when calls with v are running and no call is waiting.
func (g *compatGate) enter(eff basecompatibility.MutableEffectiveVersion, v *version.Version) {
	g.mu.Lock()
	if len(g.queue) == 0 && (g.running == 0 || g.cur.EqualTo(v)) {
		if g.running == 0 {
			g.old, g.cur = eff.MinCompatibilityVersion(), v
			eff.SetMinCompatibilityVersion(v)
		}
		g.running++
		g.mu.Unlock()
		return
	}
	t := &compatTurn{version: v, in: make(chan struct{})}
	g.queue = append(g.queue, t)
	g.mu.Unlock()
	<-t.in // leave counted the call in and set its version
}

// leave ends a call. The last call running puts the old compatibility
// version back, and then admits the first call waiting together with every
// other waiting call that needs the same version.
func (g *compatGate) leave(eff basecompatibility.MutableEffectiveVersion) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.running--
	if g.running > 0 {
		return
	}
	eff.SetMinCompatibilityVersion(g.old)
	if len(g.queue) == 0 {
		return
	}
	g.cur = g.queue[0].version
	eff.SetMinCompatibilityVersion(g.cur)
	g.queue = slices.DeleteFunc(g.queue, func(t *compatTurn) bool {
		if !t.version.EqualTo(g.cur) {
			return false
		}
		g.running++
		close(t.in)
		return true
	})
}

// WithFeatures switches on the experimental features in f: Check reports the
// problems of Kyverno ValidatingPolicy documents with feature.Kyverno, and
// types params from WithCRDs with feature.CRDTypedPolicies. Without it every
// experimental feature is off.
func WithFeatures(f feature.Set) Option {
	return func(s *settings) { s.on = f }
}

// WithCRDs makes Check type object and oldObject from the CRD of a custom
// resource a ValidatingAdmissionPolicy matches, and params from the CustomResourceDefinitions in c,
// when a ValidatingAdmissionPolicy's spec.paramKind names a custom resource
// they define and the experimental feature crd-typed-policies is on (see
// WithFeatures). Without it, or when the kind is in no CRD of c, params of such a
// policy stays dyn. A built-in kind needs no CRDs.
func WithCRDs(c *CRDs) Option {
	return func(s *settings) { s.crds = c }
}
