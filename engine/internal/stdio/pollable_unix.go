//go:build !windows

package stdio

import (
	"os"
	"syscall"
)

// Pollable returns a file the runtime can poll, so that SetReadDeadline works
// on it, for the same open file as f, and a function that undoes the change.
// It returns f and a no-op function when that is not possible (a regular
// file, say).
//
// Pollable puts the open file in non-blocking mode, and that mode is shared
// with every process that holds the same file, such as a parent shell. Call
// the restore function, once, when the file is no longer read; until then no
// other process should read it. The result is a duplicate descriptor:
// closing it does not close f.
func Pollable(f *os.File) (polled *os.File, restore func()) {
	fd := int(f.Fd())
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	if errno != 0 || flags&syscall.O_NONBLOCK != 0 {
		return f, func() {} // unknown mode, or already non-blocking and so pollable
	}
	dup, err := syscall.Dup(fd)
	if err != nil {
		return f, func() {}
	}
	syscall.CloseOnExec(dup)
	if err := syscall.SetNonblock(dup, true); err != nil {
		_ = syscall.Close(dup)
		return f, func() {}
	}
	p := os.NewFile(uintptr(dup), f.Name())
	return p, func() {
		_ = syscall.SetNonblock(fd, false)
		_ = p.Close()
	}
}
