//go:build windows

package procwatch

import (
	"fmt"
	"math"
	"os"
	"syscall"
	"testing"
)

func TestAliveWindows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		pid  int
		want bool
	}{
		{"self", os.Getpid(), true},
		{"zero", 0, false},
		{"negative", -1, false},
		{"above uint32", math.MaxUint32 + 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Alive(tt.pid); got != tt.want {
				t.Errorf("Alive(%d) = %v, want %v", tt.pid, got, tt.want)
			}
		})
	}
}

func TestAliveWithOpenError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"access denied", syscall.ERROR_ACCESS_DENIED, true},
		{"wrapped access denied", fmt.Errorf("open: %w", syscall.ERROR_ACCESS_DENIED), true},
		{"invalid parameter", syscall.Errno(87), false}, // 87 is ERROR_INVALID_PARAMETER, returned for an unknown pid
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			open := func(uint32, bool, uint32) (syscall.Handle, error) { return 0, tt.err }
			if got := aliveWith(1234, open); got != tt.want {
				t.Errorf("aliveWith(1234, open fails with %v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
