//go:build windows

package stdio_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/stdio"
)

// TestCancellableBlockingPipe checks that a read blocked on a pipe, which has
// no deadline on Windows, is released when the context ends.
func TestCancellableBlockingPipe(t *testing.T) {
	t.Parallel()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	r, ok := stdio.Cancellable(ctx, pr)
	if !ok {
		t.Fatal("Cancellable(pipe) ok = false, want true")
	}
	defer r.Close()
	got := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 1))
		got <- err
	}()
	cancel()
	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Errorf("Read after cancel = %v, want context.Canceled", err)
	}
}
