package procwatch

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestAlive(t *testing.T) {
	t.Parallel()
	if !Alive(os.Getpid()) {
		t.Error("Alive(own pid) = false, want true")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // re-runs the test binary to get a process that has exited
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if Alive(cmd.Process.Pid) {
		t.Error("Alive(exited pid) = true, want false")
	}
}

func TestWait(t *testing.T) {
	t.Parallel()
	t.Run("gone", func(t *testing.T) {
		t.Parallel()
		calls := 0
		gone := Wait(t.Context(), time.Millisecond, func() bool { calls++; return calls < 3 })
		if !gone {
			t.Error("Wait = false, want true")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if Wait(ctx, time.Millisecond, func() bool { return true }) {
			t.Error("Wait(cancelled) = true, want false")
		}
	})
}
