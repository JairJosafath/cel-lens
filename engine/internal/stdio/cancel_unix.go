//go:build !windows

package stdio

import "os"

// cancelRead returns nil: where a read cannot be released by a deadline, it
// cannot be released at all.
func cancelRead(*os.File) func() bool { return nil }
