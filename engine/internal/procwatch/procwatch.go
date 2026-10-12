// Package procwatch notices when another process, such as the editor that
// started the program, has gone.
package procwatch

import (
	"context"
	"time"
)

// Alive reports whether the process with the given ID still exists. A process
// that exists but belongs to another user counts as alive.
func Alive(pid int) bool { return alive(pid) }

// Wait polls alive every interval and returns true once it reports false. It
// returns false when ctx ends first. alive is called from the calling
// goroutine only.
func Wait(ctx context.Context, interval time.Duration, alive func() bool) bool {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if !alive() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}
