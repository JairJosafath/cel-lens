//go:build windows

package stdio

import "os"

// Pollable returns f and a no-op function: console and pipe handles cannot be
// polled on Windows; Cancellable cancels their reads instead.
func Pollable(f *os.File) (polled *os.File, restore func()) { return f, func() {} }
