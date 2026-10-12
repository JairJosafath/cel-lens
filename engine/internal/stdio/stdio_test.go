package stdio_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/stdio"
)

func TestCancellableRelease(t *testing.T) {
	t.Parallel()
	pr, pw, err := os.Pipe() // pipes made by os.Pipe are pollable
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

func TestCancellableNotReleasable(t *testing.T) {
	t.Parallel()
	in := strings.NewReader("x")
	r, ok := stdio.Cancellable(t.Context(), in)
	if ok || r != nil {
		t.Errorf("Cancellable(strings.Reader) = %v, %v, want nil and false", r, ok)
	}
}
