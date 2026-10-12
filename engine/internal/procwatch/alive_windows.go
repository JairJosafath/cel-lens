//go:build windows

package procwatch

import (
	"errors"
	"math"
	"syscall"
)

func alive(pid int) bool { return aliveWith(pid, syscall.OpenProcess) }

// aliveWith is alive with the OpenProcess call injected. A process that
// OpenProcess refuses with ERROR_ACCESS_DENIED exists but belongs to another
// user or runs elevated, so it counts as alive.
func aliveWith(pid int, open func(access uint32, inherit bool, pid uint32) (syscall.Handle, error)) bool {
	if pid <= 0 || pid > math.MaxUint32 {
		return false
	}
	h, err := open(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	ev, err := syscall.WaitForSingleObject(h, 0)
	alive := err == nil && ev == syscall.WAIT_TIMEOUT
	// A failed close leaks one handle but does not change whether the
	// process is alive, so the answer stays the same.
	_ = syscall.CloseHandle(h)
	return alive
}
