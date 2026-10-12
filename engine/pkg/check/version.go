package check

import (
	"errors"
	"sync"

	"k8s.io/apiserver/pkg/util/compatibility"
	basecompatibility "k8s.io/component-base/compatibility"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// Option changes how Check works.
type Option func(*settings)

type settings struct {
	version kubeversion.Version
}

// WithVersion makes Check match the API server of version v, with its CEL
// libraries and cost rules. Without it Check matches kubeversion.Default.
func WithVersion(v kubeversion.Version) Option {
	return func(s *settings) { s.version = v }
}

// emulation serialises the use of the Kubernetes libraries' compatibility
// version, which their CRD validation reads from a process-wide setting.
var emulation sync.Mutex

// withCompatibility calls fn with the process-wide compatibility version of
// the Kubernetes libraries set to v.Compat(), then puts the old one back. Calls
// do not overlap with each other, but other code in the process that reads that
// setting meanwhile sees v's.
func withCompatibility(v kubeversion.Version, fn func()) error {
	eff, ok := compatibility.DefaultComponentGlobalsRegistry.EffectiveVersionFor(basecompatibility.DefaultKubeComponent).(basecompatibility.MutableEffectiveVersion)
	if !ok {
		return errors.New("set Kubernetes compatibility version: the libraries' version is not settable")
	}
	emulation.Lock()
	defer emulation.Unlock()
	old := eff.MinCompatibilityVersion()
	eff.SetMinCompatibilityVersion(v.Compat())
	defer eff.SetMinCompatibilityVersion(old)
	fn()
	return nil
}
