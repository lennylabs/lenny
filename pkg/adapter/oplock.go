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
	// errOpCoalesced is returned when a checkpoint Begin names a slotId
	// that is already pending. The pending checkpoint for that slot covers
	// this request, so the caller treats it as a successful no-op.
	errOpCoalesced = errors.New("adapter checkpoint coalesced into the pending checkpoint")
)

// opLock is the §4.7 pod-level operation lock. It serializes the
// Checkpoint and Interrupt adapter RPCs across the pod's slots: at most
// one runs at a time. Waiting operations sit in a pending set that holds
// either a single pending interrupt (which holds the whole-pod queue) or
// an ordered set of pending per-slot checkpoints keyed by slotID. The two
// never mix: an interrupt is admitted only when no checkpoint is pending,
// and a checkpoint is rejected while an interrupt is pending. release
// promotes a pending interrupt first, otherwise the pending checkpoint
// with the lowest slotID, so per-slot checkpoints run one at a time in
// slot-ID order (spec: §5.2). On a maxConcurrentSessions: 1 pod every
// slotID is the empty whole-pod id, so the pending set holds at most one
// entry and the depth-one whole-pod behavior is preserved. The zero value
// is ready to use.
type opLock struct {
	mu      sync.Mutex
	running bool
	// interruptPending records a waiting interrupt, which holds the
	// whole-pod queue. interruptPromote is closed to wake it.
	interruptPending bool
	interruptPromote chan struct{}
	// checkpoints holds the pending per-slot checkpoints keyed by slotID.
	// Each value is a channel closed to promote that slot's waiter. It is
	// non-empty only when interruptPending is false.
	checkpoints map[string]chan struct{}
}

// Begin acquires the lock for an operation of kind targeting slotID (the
// empty whole-pod id for interrupts and single-session checkpoints). It
// blocks until the operation may run or ctx is cancelled. When the
// operation cannot wait it returns immediately: errOpCoalesced for a
// checkpoint whose slotID is already pending, or errOpBusy for an
// interrupt behind a pending operation or a checkpoint behind a pending
// interrupt. On success the returned release func must be called once the
// operation completes. spec: §4.7 (Checkpoint/Interrupt mutual exclusion),
// §5.2 (one slot's checkpoint upload at a time, in slot-ID order).
func (l *opLock) Begin(ctx context.Context, kind opKind, slotID string) (func(), error) {
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
		return l.wait(ctx, kind, slotID, promote)
	}
	// A checkpoint whose slotID is already pending is covered by that
	// pending checkpoint and coalesces; a distinct slotID is admitted.
	if _, ok := l.checkpoints[slotID]; ok {
		l.mu.Unlock()
		return nil, errOpCoalesced
	}
	promote := make(chan struct{})
	if l.checkpoints == nil {
		l.checkpoints = make(map[string]chan struct{})
	}
	l.checkpoints[slotID] = promote
	l.mu.Unlock()
	return l.wait(ctx, kind, slotID, promote)
}

// wait blocks until the pending operation is promoted or ctx is
// cancelled, withdrawing the operation from the pending set on
// cancellation.
func (l *opLock) wait(ctx context.Context, kind opKind, slotID string, promote chan struct{}) (func(), error) {
	select {
	case <-promote:
		// release() promoted this operation; it now holds the lock.
		return l.release, nil
	case <-ctx.Done():
		l.mu.Lock()
		if l.withdrawLocked(kind, slotID, promote) {
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
func (l *opLock) withdrawLocked(kind opKind, slotID string, promote chan struct{}) bool {
	if kind == opInterrupt {
		if l.interruptPending && l.interruptPromote == promote {
			l.interruptPending = false
			l.interruptPromote = nil
			return true
		}
		return false
	}
	if ch, ok := l.checkpoints[slotID]; ok && ch == promote {
		delete(l.checkpoints, slotID)
		return true
	}
	return false
}

// release ends the running operation. It promotes a pending interrupt
// first, otherwise the pending checkpoint with the lowest slotID, so
// per-slot checkpoints run in slot-ID order (spec: §5.2). With nothing
// pending the lock returns to idle.
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
		// running stays true: ownership transfers to the promoted slot.
		l.mu.Unlock()
		close(promote)
		return
	}
	l.running = false
	l.mu.Unlock()
}

// lowestKey returns the lexicographically smallest key of a non-empty
// map, the slot-ID-order promotion pick (spec: §5.2).
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
