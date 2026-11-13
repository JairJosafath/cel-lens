package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// maxJobs is how many jobs run in the background at once, those cancelled
// but still ending included.
const maxJobs = 4

// jobSpec is the work of a command that startJob runs in the background.
type jobSpec struct {
	command string
	// slot and key say what the work does, such as whether it searches for
	// examples, and the value whose examples: a request of the same
	// command, slot and key while the work runs waits for its answer, and
	// one of the same command and slot with another key replaces it. Jobs
	// in different slots leave each other be.
	slot, key string
	// work runs in a goroutine. It must not touch the session, which the
	// message loop goes on changing: what it needs is made on the loop
	// before.
	work func(context.Context) (any, error)
	// settle, when not nil, runs on the message loop with what work
	// returned, and returns the answer; it may change the session.
	settle func(any) (any, error)
}

// job is the work of a command that runs in its own goroutine, so the
// message loop goes on with other messages, such as a didChange and its
// diagnostics, until it ends; the loop answers the requests that wait for it
// then.
type job struct {
	spec jobSpec
	// ids are the requests that wait for the answer.
	ids    []json.RawMessage
	cancel context.CancelFunc
	// cancelled is true once no request waits any more: what the work
	// returns then is dropped.
	cancelled bool
}

// jobResult is what the work of the job seq returned.
type jobResult struct {
	seq    int
	result any
	err    error
}

// startJob runs the work of spec for the request msg in a goroutine; the
// message loop answers the request with what it returns (see finishJob). A
// job of the same command, slot and key that still runs takes the request
// too; one of the same command and slot and another key is replaced, its
// requests answered as cancelled, as the editor asks again when what it asks
// for changed. With maxJobs jobs running, cancelled ones still ending
// included, the request is answered with RequestFailed. A notification
// starts nothing, as nothing would get the answer. stopJobs cancels and
// waits for the jobs.
func (ss *session) startJob(ctx context.Context, msg jsonrpc.Message, spec jobSpec) error {
	if msg.IsNotification() {
		return nil
	}
	same := func(j *job) bool {
		return !j.cancelled && j.spec.command == spec.command && j.spec.slot == spec.slot
	}
	for _, seq := range slices.Sorted(maps.Keys(ss.jobs)) {
		if j := ss.jobs[seq]; same(j) && j.spec.key == spec.key {
			j.ids = append(j.ids, msg.ID)
			return nil
		}
	}
	if len(ss.jobs) >= maxJobs {
		return ss.conn.ReplyError(msg.ID, jsonrpc.CodeRequestFailed, fmt.Sprintf("cel-lens runs at most %d commands in the background, those cancelled but still ending included, and %d run: run %s again once one has ended", maxJobs, len(ss.jobs), spec.command))
	}
	for _, seq := range slices.Sorted(maps.Keys(ss.jobs)) {
		if j := ss.jobs[seq]; same(j) {
			if err := ss.cancelOne(j, "a newer "+spec.command+" replaced it"); err != nil {
				return err
			}
		}
	}
	ss.runJob(ctx, spec, []json.RawMessage{msg.ID})
	return nil
}

// runJob starts the work of spec in a goroutine for the requests ids, which
// may be none.
func (ss *session) runJob(ctx context.Context, spec jobSpec, ids []json.RawMessage) {
	if ss.jobs == nil {
		ss.jobs = map[int]*job{}
		ss.jobDone = make(chan jobResult)
	}
	ss.jobSeq++
	seq, done := ss.jobSeq, ss.jobDone
	ctx, cancel := context.WithCancel(ctx)
	ss.jobs[seq] = &job{spec: spec, ids: ids, cancel: cancel}
	go func() {
		result, err := spec.work(ctx)
		done <- jobResult{seq, result, err}
	}()
}

// startBackground runs the work of spec in a goroutine for no request, so
// its settle keeps what it found for the requests that come after, unless a
// job of the same command and slot runs, or maxJobs do: it reports whether
// it started one. stopJobs cancels and waits for it as for any job.
func (ss *session) startBackground(ctx context.Context, spec jobSpec) bool {
	for _, j := range ss.jobs {
		if !j.cancelled && j.spec.command == spec.command && j.spec.slot == spec.slot {
			return false
		}
	}
	if len(ss.jobs) >= maxJobs {
		return false
	}
	ss.runJob(ctx, spec, nil)
	return true
}

// finishJob answers the requests that wait for the job that ended with r,
// with what its settle makes of it: an rpcError as itself, another error as
// RequestFailed. A job no request waits for is dropped.
func (ss *session) finishJob(r jobResult) error {
	j := ss.jobs[r.seq]
	delete(ss.jobs, r.seq)
	j.cancel()
	if j.cancelled {
		return nil
	}
	result, err := r.result, r.err
	if err == nil && j.spec.settle != nil {
		result, err = j.spec.settle(result)
	}
	for _, id := range j.ids {
		var re *rpcError
		var werr error
		switch {
		case errors.As(err, &re):
			werr = ss.conn.ReplyError(id, re.code, re.message)
		case err != nil:
			werr = ss.conn.ReplyError(id, jsonrpc.CodeRequestFailed, err.Error())
		default:
			werr = ss.conn.Reply(id, result)
		}
		if werr != nil {
			return werr
		}
	}
	return nil
}

// cancelJob answers the request id, when it waits for a job, as cancelled,
// and reports whether it did. The job goes on while another request waits
// for it, and is cancelled when none does.
func (ss *session) cancelJob(id json.RawMessage) (bool, error) {
	for _, seq := range slices.Sorted(maps.Keys(ss.jobs)) {
		j := ss.jobs[seq]
		i := slices.IndexFunc(j.ids, func(w json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(w), bytes.TrimSpace(id)) })
		if j.cancelled || i < 0 {
			continue
		}
		j.ids = slices.Delete(j.ids, i, i+1)
		if len(j.ids) == 0 {
			j.cancelled = true
			j.cancel()
		}
		return true, ss.conn.ReplyError(id, jsonrpc.CodeRequestCancelled, j.spec.command+" was cancelled: the client cancelled it")
	}
	return false, nil
}

// cancelOne cancels the work of j and answers the requests that wait for it
// as cancelled, for the reason why.
func (ss *session) cancelOne(j *job, why string) error {
	j.cancelled = true
	j.cancel()
	for _, id := range j.ids {
		if err := ss.conn.ReplyError(id, jsonrpc.CodeRequestCancelled, j.spec.command+" was cancelled: "+why); err != nil {
			return err
		}
	}
	return nil
}

// cancelJobs cancels every job of command that a request still waits for,
// as cancelOne does, for the reason why.
func (ss *session) cancelJobs(command, why string) error {
	for _, seq := range slices.Sorted(maps.Keys(ss.jobs)) {
		if j := ss.jobs[seq]; !j.cancelled && j.spec.command == command {
			if err := ss.cancelOne(j, why); err != nil {
				return err
			}
		}
	}
	return nil
}

// stopJobs cancels the jobs and waits for them to end. It returns the
// requests still waiting for an answer, in the order the jobs started.
func (ss *session) stopJobs() []json.RawMessage {
	var waiting []json.RawMessage
	for _, seq := range slices.Sorted(maps.Keys(ss.jobs)) {
		j := ss.jobs[seq]
		j.cancel()
		if !j.cancelled {
			waiting = append(waiting, j.ids...)
		}
	}
	for range ss.jobs {
		<-ss.jobDone
	}
	clear(ss.jobs)
	return waiting
}

// dropJobs answers the requests whose jobs still run with an error, as the
// session shuts down, and stops the jobs.
func (ss *session) dropJobs() error {
	for _, id := range ss.stopJobs() {
		if err := ss.conn.ReplyError(id, jsonrpc.CodeRequestFailed, "the server shut down before the command ended"); err != nil {
			return err
		}
	}
	return nil
}
