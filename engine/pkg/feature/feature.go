// Package feature names the experimental features of the engine and holds the
// set a user has switched on.
//
// Every feature is stable or experimental. Stable features are always on and
// have no name here. An experimental feature is off until the user lists its
// name: with --experimental on the command line, or experimental in the
// language server's initializationOptions and settings.
package feature

import (
	"fmt"
	"slices"
	"strings"
)

// Feature is the name of an experimental feature, as the user lists it.
type Feature string

// The experimental features.
const (
	// Kyverno is the checks, hover and completion for Kyverno ValidatingPolicy.
	Kyverno Feature = "kyverno"
	// CRDTypedPolicies types object, oldObject and params of a custom resource
	// from a CustomResourceDefinition found in the workspace.
	CRDTypedPolicies Feature = "crd-typed-policies"
	// FittingCompletion is completion that fits the spot, and hover that says
	// why a value is dyn.
	FittingCompletion Feature = "fitting-completion"
)

// Known returns the names of every experimental feature, sorted. The caller
// may change the result.
func Known() []Feature {
	return []Feature{CRDTypedPolicies, FittingCompletion, Kyverno}
}

// Set is the experimental features switched on. The zero value has every
// experimental feature off. A Set is a plain value: compare it with ==.
type Set uint8

// bit returns the bit of f in a Set, 0 for a name that is no feature.
func bit(f Feature) Set {
	switch f {
	case Kyverno:
		return 1 << 0
	case CRDTypedPolicies:
		return 1 << 1
	case FittingCompletion:
		return 1 << 2
	}
	return 0
}

// Of returns the Set with the given features on. A name that is no feature is
// left out.
func Of(features ...Feature) Set {
	var s Set
	for _, f := range features {
		s |= bit(f)
	}
	return s
}

// Has reports whether f is on.
func (s Set) Has(f Feature) bool {
	b := bit(f)
	return b != 0 && s&b != 0
}

// Parse returns the Set that the names switch on. Names that are no feature are
// ignored and returned in unknown, in the order given, once each. Surrounding
// spaces are trimmed and an empty name is skipped.
func Parse(names []string) (set Set, unknown []string) {
	for _, n := range names {
		n = strings.TrimSpace(n)
		switch b := bit(Feature(n)); {
		case n == "":
		case b == 0:
			if !slices.Contains(unknown, n) {
				unknown = append(unknown, n)
			}
		default:
			set |= b
		}
	}
	return set, unknown
}

// Warning returns the sentence that tells the user the names are no experimental
// feature and which names are, or "" when unknown is empty.
func Warning(unknown []string) string {
	if len(unknown) == 0 {
		return ""
	}
	known := make([]string, 0, len(Known()))
	for _, f := range Known() {
		known = append(known, string(f))
	}
	return fmt.Sprintf("unknown experimental feature %s, ignored; the known ones are %s",
		strings.Join(quoted(unknown), ", "), strings.Join(known, ", "))
}

func quoted(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}
