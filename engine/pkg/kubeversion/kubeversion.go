// Package kubeversion defines the Kubernetes versions cel-lens can match: the
// supported range and the default are here and nowhere else.
//
// A Version selects the API server's own versioned CEL environment, the base
// environment set for that compatibility version, so the libraries, language
// settings and cost rules are those of that Kubernetes minor version.
package kubeversion

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apiserver/pkg/cel/environment"
)

// The supported range of Kubernetes 1.x minor versions: the five newest. The
// newest is the default, and the one the vendored Kubernetes libraries are.
const (
	oldest = 33
	newest = 37
)

// ErrUnsupported is wrapped by the error Parse returns for a version cel-lens
// does not match.
var ErrUnsupported = errors.New("unsupported Kubernetes version")

// Version is a supported Kubernetes minor version, such as 1.37. The zero
// value is the default version.
type Version struct {
	minor int
}

// Default returns the version cel-lens matches when none is chosen.
func Default() Version { return Version{newest} }

// Supported returns the supported versions, oldest first.
func Supported() []Version {
	out := make([]Version, 0, newest-oldest+1)
	for m := oldest; m <= newest; m++ {
		out = append(out, Version{m})
	}
	return out
}

// Parse reads a version written as 1.<minor>, such as "1.35". A leading "v" is
// accepted. The error wraps ErrUnsupported and lists the supported versions.
func Parse(s string) (Version, error) {
	minor, ok := strings.CutPrefix(strings.TrimPrefix(s, "v"), "1.")
	if n, err := strconv.Atoi(minor); ok && err == nil && n >= oldest && n <= newest && strconv.Itoa(n) == minor {
		return Version{n}, nil
	}
	return Version{}, fmt.Errorf("%w %q: supported versions are %s", ErrUnsupported, s, Range())
}

// String returns the version as 1.<minor>.
func (v Version) String() string { return "1." + strconv.Itoa(v.get().minor) }

func (v Version) get() Version {
	if v.minor == 0 {
		return Default()
	}
	return v
}

// EnvSet returns the API server's base CEL environment set for v. The sets are
// built once per version and shared, so it is cheap and safe for concurrent
// use.
func (v Version) EnvSet() *environment.EnvSet {
	return environment.MustBaseEnvSet(v.Compat())
}

// Compat returns the compatibility version of the Kubernetes libraries with
// which an API server of version v compiles new expressions: its minimum
// compatibility version, one minor below its binary version. Functions added in
// v itself are not yet available to new expressions.
func (v Version) Compat() *version.Version {
	return version.MajorMinor(1, uint(v.get().minor-1))
}

// Range returns the supported range, such as "1.33, 1.34, 1.35, 1.36, 1.37".
func Range() string {
	var names []string
	for _, v := range Supported() {
		names = append(names, v.String())
	}
	return strings.Join(names, ", ")
}

// Summary describes the default and the range, for --version, such as
// "matches Kubernetes 1.37; supports 1.33–1.37".
func Summary() string {
	return fmt.Sprintf("matches Kubernetes %s; supports 1.%d–1.%d", Default(), oldest, newest)
}
