// Package shortnum writes the large counts of cel-lens's messages, such as
// estimated costs and the sizes the estimator assumes, in a few characters:
// 9999, 12K, 6.2M, 2.4B, 1.3T, then a power of ten.
package shortnum

import (
	"fmt"
	"strconv"
	"strings"
)

// Format writes n with a K, M, B or T suffix and one decimal, without a
// trailing ".0" (12K, 6.2M), or as a power of ten from 10^15 on (7.4e+18).
// Below 10,000 it writes n as it is.
func Format(n uint64) string {
	f := float64(n)
	switch {
	case n >= 1e15:
		return fmt.Sprintf("%.1e", f)
	case n >= 1e12:
		return trim(f/1e12) + "T"
	case n >= 1e9:
		return trim(f/1e9) + "B"
	case n >= 1e6:
		return trim(f/1e6) + "M"
	case n >= 1e4:
		return trim(f/1e3) + "K"
	}
	return strconv.FormatUint(n, 10)
}

// trim writes f with one decimal, without a trailing ".0".
func trim(f float64) string {
	return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0")
}
