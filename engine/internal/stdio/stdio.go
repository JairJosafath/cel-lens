// Package stdio makes the program's standard input stop reading when a
// context ends, where the platform allows it.
package stdio

import (
	"context"
	"io"
	"os"
	"sync"
	"time"
)

// deadliner is what a reader needs to be released from a blocked Read.
type deadliner interface {
	SetReadDeadline(t time.Time) error
}

// retryEvery is how often a release that can miss a Read about to start is
// repeated.
const retryEvery = 10 * time.Millisecond

// Reader is a reader whose Read returns once the context given to Cancellable
// ends, even when it is blocked. Use it from one goroutine at a time.
type Reader struct {
	ctx context.Context //nolint:containedctx // the reader lives for one call and reports this context's error
	r   io.Reader
	// stop unregisters the release; callbackDone is closed when a release that
	// has started has finished; quit ends a release that repeats.
	stop         func() bool
	callbackDone chan struct{}
	quit         chan struct{}
	quitOnce     sync.Once
}

// Cancellable returns a Reader of r that releases a blocked Read when ctx
// ends, and true. It returns nil and false when r cannot be released: r must
// be a pollable *os.File (SetReadDeadline works on it) or, on Windows, an
// *os.File with a handle whose pending read can be cancelled. The caller must
// call Close once it no longer reads, after which r must not be released.
func Cancellable(ctx context.Context, r io.Reader) (*Reader, bool) {
	release := releaser(r)
	if release == nil {
		return nil, false
	}
	rd := &Reader{ctx: ctx, r: r, callbackDone: make(chan struct{}), quit: make(chan struct{})}
	rd.stop = context.AfterFunc(ctx, func() {
		defer close(rd.callbackDone)
		for !release() {
			select {
			case <-rd.quit:
				return
			case <-time.After(retryEvery):
			}
		}
	})
	return rd, true
}

// releaser returns a function that releases a blocked Read of r, or nil. The
// function reports whether the release is final; when it is not, it is called
// again until the Reader is closed.
func releaser(r io.Reader) func() bool {
	if d, ok := r.(deadliner); ok && d.SetReadDeadline(time.Time{}) == nil {
		return func() bool { return d.SetReadDeadline(time.Unix(1, 0)) == nil }
	}
	if f, ok := r.(*os.File); ok {
		return cancelRead(f)
	}
	return nil
}

// Read reads from the underlying reader. It returns the context's error once
// the context has ended.
func (r *Reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if err != nil {
		if cerr := r.ctx.Err(); cerr != nil {
			err = cerr
		}
	}
	return n, err
}

// Close stops releasing and waits for a release that is running, so that none
// touches the underlying reader afterwards. It is safe to call more than once.
func (r *Reader) Close() {
	r.quitOnce.Do(func() { close(r.quit) })
	if !r.stop() {
		<-r.callbackDone
	}
}
