// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"sync"
)

// opKind identifies a §4.7 serialized adapter operation.
type opKind int

const (
	opCheckpoint opKind = iota
	opInterrupt
)

func (k opKind) String() string {
	if k == opInterrupt {
		return "interrupt"
	}
	return "checkpoint"
}

var (
	// errOpBusy is returned by opLock.Begin when the operation cannot be
	// admitted: an interrupt while another operation is pending, or a
	// checkpoint arriving while an interrupt holds the whole-pod queue.
	// spec: §4.7 (a dropped interrupt returns a BUSY status the gateway
	// retries with backoff).
	errOpBusy = errors.New("adapter operation lock is busy")
	// errOpCoalesced is returned when a checkpoint Begin names a session
	// identifier that is already pending. The request is refused because
	// the already-pending checkpoint for the same session identifier
	// covers it. Server.Checkpoint surfaces the refusal to the gateway as
	// codes.Aborted, the same status a busy lock returns, so the gateway
	// finalises the manifest row partial for this attempt.
	// spec: §4.7 (one pending checkpoint per distinct session identifier).
	errOpCoalesced = errors.New("adapter checkpoint coalesced into the pending checkpoint")
)

// opLock is the §4.7 pod-level operation lock. It serializes the
// Checkpoint and Interrupt adapter RPCs across the sessions the pod
// holds: at most one runs at a time. Waiting operations sit in a pending
// set that holds either a single pending interrupt (which holds the
// whole-pod queue) or a set of pending checkpoints keyed by session
// identifier. The two never mix: an interrupt is admitted only when no
// checkpoint is pending, and a checkpoint is rejected while an interrupt
// is pending. release promotes a pending interrupt first, otherwise the
// pending checkpoint with the lowest session identifier (spec: §5.2).
// Session identifiers are opaque, so that order is a lexicographic
// tie-break chosen so the promotion pick is a pure function of the
// pending set rather than of the iteration order of the pending map. It
// carries no ordinal, arrival-order, or fairness meaning and it
// establishes no liveness property. The pending checkpoint set is keyed
// by session identifier on every pod whatever the pool's concurrency,
// admitting one pending checkpoint per distinct session identifier. A pending
// interrupt is not a member of that set: it lives in interruptPending
// and interruptPromote and never occupies a checkpoint key. The zero
// value is ready to use.
type opLock struct {
	mu      sync.Mutex
	running bool
	// interruptPending records a waiting interrupt, which holds the
	// whole-pod queue. interruptPromote is closed to wake it.
	interruptPending bool
	interruptPromote chan struct{}
	// checkpoints holds the pending checkpoints keyed by session
	// identifier. Each value is a channel closed to promote that
	// session's waiter. It is non-empty only when interruptPending is
	// false.
	checkpoints map[string]chan struct{}
}

// Begin acquires the lock for an operation of kind targeting sessionID.
// An interrupt is pod-scoped, so sessionID is unused for opInterrupt and
// a pending interrupt is recorded outside the pending checkpoint set. It
// blocks until the operation may run or ctx is cancelled. When the
// operation cannot wait it returns immediately: errOpCoalesced for a
// checkpoint whose session identifier is already pending, which
// Server.Checkpoint reports to the gateway as codes.Aborted, or errOpBusy
// for an interrupt behind a pending operation or a checkpoint behind a
// pending interrupt. On success the returned release func must be called
// once the operation completes. spec: §4.7 (Checkpoint/Interrupt mutual
// exclusion, one pending checkpoint per distinct session identifier),
// §5.2 (one session's checkpoint upload at a time, in the lexicographic
// tie-break over session identifiers).
func (l *opLock) Begin(ctx context.Context, kind opKind, sessionID string) (func(), error) {
	l.mu.Lock()
	if !l.running {
		l.running = true
		l.mu.Unlock()
		return l.release, nil
	}
	// An operation is running. A pending interrupt holds the whole-pod
	// queue: any incoming operation is rejected so the pending set never
	// mixes an interrupt with checkpoints.
	if l.interruptPending {
		l.mu.Unlock()
		return nil, errOpBusy
	}
	if kind == opInterrupt {
		// An interrupt never displaces a pending checkpoint; it is
		// admitted only when no checkpoint is pending.
		if len(l.checkpoints) > 0 {
			l.mu.Unlock()
			return nil, errOpBusy
		}
		promote := make(chan struct{})
		l.interruptPending = true
		l.interruptPromote = promote
		l.mu.Unlock()
		return l.wait(ctx, kind, sessionID, promote)
	}
	// A checkpoint whose session identifier is already pending is covered
	// by that pending checkpoint and coalesces; a distinct session
	// identifier is admitted.
	if _, ok := l.checkpoints[sessionID]; ok {
		l.mu.Unlock()
		return nil, errOpCoalesced
	}
	promote := make(chan struct{})
	if l.checkpoints == nil {
		l.checkpoints = make(map[string]chan struct{})
	}
	l.checkpoints[sessionID] = promote
	l.mu.Unlock()
	return l.wait(ctx, kind, sessionID, promote)
}

// wait blocks until the pending operation is promoted or ctx is
// cancelled, withdrawing the operation from the pending set on
// cancellation.
func (l *opLock) wait(ctx context.Context, kind opKind, sessionID string, promote chan struct{}) (func(), error) {
	select {
	case <-promote:
		// release() promoted this operation; it now holds the lock.
		return l.release, nil
	case <-ctx.Done():
		l.mu.Lock()
		if l.withdrawLocked(kind, sessionID, promote) {
			l.mu.Unlock()
			return nil, ctx.Err()
		}
		// Promoted concurrently with the cancellation; give the lock
		// back so the next operation is not stranded.
		l.mu.Unlock()
		l.release()
		return nil, ctx.Err()
	}
}

// withdrawLocked removes a still-pending operation from the pending set,
// matching on the promote channel so a concurrently promoted operation is
// not withdrawn. It returns whether the operation was withdrawn. The
// caller holds l.mu.
func (l *opLock) withdrawLocked(kind opKind, sessionID string, promote chan struct{}) bool {
	if kind == opInterrupt {
		if l.interruptPending && l.interruptPromote == promote {
			l.interruptPending = false
			l.interruptPromote = nil
			return true
		}
		return false
	}
	if ch, ok := l.checkpoints[sessionID]; ok && ch == promote {
		delete(l.checkpoints, sessionID)
		return true
	}
	return false
}

// release ends the running operation. It promotes a pending interrupt
// first, otherwise the pending checkpoint with the lowest session
// identifier (spec: §5.2). Session identifiers are opaque, so the
// promotion order is a lexicographic tie-break chosen so the pick is a
// pure function of the pending set rather than of the iteration order of
// the pending map; it carries no ordinal, arrival-order, or fairness
// meaning and establishes no liveness property. With nothing pending the
// lock returns to idle.
func (l *opLock) release() {
	l.mu.Lock()
	if l.interruptPending {
		promote := l.interruptPromote
		l.interruptPending = false
		l.interruptPromote = nil
		// running stays true: ownership transfers to the interrupt.
		l.mu.Unlock()
		close(promote)
		return
	}
	if len(l.checkpoints) > 0 {
		lowest := lowestKey(l.checkpoints)
		promote := l.checkpoints[lowest]
		delete(l.checkpoints, lowest)
		// running stays true: ownership transfers to the promoted session.
		l.mu.Unlock()
		close(promote)
		return
	}
	l.running = false
	l.mu.Unlock()
}

// lowestKey returns the lexicographically smallest key of a non-empty
// map, which is the promotion pick over the pending session identifiers
// (spec: §5.2).
func lowestKey(m map[string]chan struct{}) string {
	lowest := ""
	first := true
	for k := range m {
		if first || k < lowest {
			lowest = k
			first = false
		}
	}
	return lowest
}
