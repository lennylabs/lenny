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
	// queued: one operation is already running and the single queue slot
	// is taken (§4.7 queue depth is one).
	errOpBusy = errors.New("adapter operation lock is busy")
	// errOpCoalesced is returned when a checkpoint Begin coincides with a
	// checkpoint already queued. The queued checkpoint covers this
	// request, so the caller treats it as a successful no-op.
	errOpCoalesced = errors.New("adapter checkpoint coalesced into the queued checkpoint")
)

// opLock is the §4.7 per-session operation lock. It serializes the
// Checkpoint and Interrupt adapter RPCs: at most one runs at a time,
// and at most one more waits in a single queue slot. A third operation
// is coalesced (a checkpoint behind a queued checkpoint) or rejected
// with errOpBusy. The zero value is ready to use.
type opLock struct {
	mu         sync.Mutex
	running    bool
	queued     bool
	queuedKind opKind
	// promote is closed to wake the single queued waiter when the
	// running operation releases the lock.
	promote chan struct{}
}

// Begin acquires the lock for an operation of kind. It blocks until the
// operation may run or ctx is cancelled; when the queue slot is already
// taken it returns errOpCoalesced (a checkpoint behind a queued
// checkpoint) or errOpBusy immediately. On success the returned release
// func must be called once the operation completes.
func (l *opLock) Begin(ctx context.Context, kind opKind) (func(), error) {
	l.mu.Lock()
	if !l.running {
		l.running = true
		l.mu.Unlock()
		return l.release, nil
	}
	if l.queued {
		// The queue slot is taken. A checkpoint behind a queued
		// checkpoint is coalesced; anything else is rejected.
		coalesce := kind == opCheckpoint && l.queuedKind == opCheckpoint
		l.mu.Unlock()
		if coalesce {
			return nil, errOpCoalesced
		}
		return nil, errOpBusy
	}
	// Take the single queue slot and wait for promotion.
	l.queued = true
	l.queuedKind = kind
	promote := make(chan struct{})
	l.promote = promote
	l.mu.Unlock()

	select {
	case <-promote:
		// release() promoted this operation; it now holds the lock.
		return l.release, nil
	case <-ctx.Done():
		l.mu.Lock()
		if l.queued && l.promote == promote {
			// Still queued: withdraw and free the slot.
			l.queued = false
			l.promote = nil
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

// release ends the running operation. If an operation is queued it is
// promoted to running; otherwise the lock returns to idle.
func (l *opLock) release() {
	l.mu.Lock()
	if l.queued {
		l.queued = false
		promote := l.promote
		l.promote = nil
		// running stays true: ownership transfers to the queued op.
		l.mu.Unlock()
		close(promote)
		return
	}
	l.running = false
	l.mu.Unlock()
}
