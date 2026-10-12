//go:build windows

package stdio

import (
	"os"

	"golang.org/x/sys/windows"
)

// cancelRead returns a function that cancels the read pending on f from any
// thread. Go opens the standard input blocking on Windows, so a deadline is
// not available and CancelIoEx is the way to release it. A cancel that comes
// before the read starts is lost, so the function is not final.
func cancelRead(f *os.File) func() bool {
	c, err := f.SyscallConn()
	if err != nil {
		return nil
	}
	return func() bool {
		_ = c.Control(func(fd uintptr) { _ = windows.CancelIoEx(windows.Handle(fd), nil) })
		return false
	}
}
