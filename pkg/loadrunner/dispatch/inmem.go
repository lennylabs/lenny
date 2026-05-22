// SPDX-License-Identifier: MIT

package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// InMem is an in-memory Dispatcher used by tests and by local
// `lenny-loadrunner --dispatcher=inmem` runs against a Lenny stack
// without cloud queue infrastructure. It supports the full surface
// (Receive, Ack, Nack, Heartbeat) with a configurable visibility
// timeout.
type InMem struct {
	mu                 sync.Mutex
	queue              []*Job
	inFlight           map[string]*inFlightEntry
	visibilityTimeout  time.Duration
	closed             bool
}

type inFlightEntry struct {
	job       *Job
	expiresAt time.Time
}

// NewInMem returns an InMem Dispatcher with the supplied visibility
// timeout. A Job not Ack'd within this window is automatically
// returned to the queue.
func NewInMem(visibilityTimeout time.Duration) *InMem {
	return &InMem{
		inFlight:          make(map[string]*inFlightEntry),
		visibilityTimeout: visibilityTimeout,
	}
}

// Submit enqueues a Job for delivery to the next Receive caller.
// Used by the control plane equivalent in tests; in production, the
// equivalent path is `Dispatcher.Push` on the cloud impls.
func (d *InMem) Submit(j *Job) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	// Generate a receipt token so the same Job can be re-enqueued
	// after Nack and tracked separately.
	d.queue = append(d.queue, withFreshReceipt(j))
}

func (d *InMem) Receive(ctx context.Context) (*Job, error) {
	deadline, ok := ctx.Deadline()
	for {
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			return nil, errors.New("dispatch: closed")
		}
		d.reapExpired()
		if len(d.queue) > 0 {
			j := d.queue[0]
			d.queue = d.queue[1:]
			d.inFlight[receiptKey(j.ReceiptToken)] = &inFlightEntry{
				job:       j,
				expiresAt: time.Now().Add(d.visibilityTimeout),
			}
			d.mu.Unlock()
			return j, nil
		}
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			if ok && time.Now().After(deadline) {
				return nil, ErrNoJob
			}
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (d *InMem) Ack(ctx context.Context, j *Job) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := receiptKey(j.ReceiptToken)
	if _, ok := d.inFlight[key]; !ok {
		return ErrJobNotInFlight
	}
	delete(d.inFlight, key)
	return nil
}

func (d *InMem) Nack(ctx context.Context, j *Job, reason string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := receiptKey(j.ReceiptToken)
	if _, ok := d.inFlight[key]; !ok {
		return ErrJobNotInFlight
	}
	delete(d.inFlight, key)
	d.queue = append(d.queue, withFreshReceipt(j))
	return nil
}

func (d *InMem) Heartbeat(ctx context.Context, j *Job) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := receiptKey(j.ReceiptToken)
	entry, ok := d.inFlight[key]
	if !ok {
		return ErrJobNotInFlight
	}
	entry.expiresAt = time.Now().Add(d.visibilityTimeout)
	return nil
}

func (d *InMem) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	d.queue = nil
	d.inFlight = nil
	return nil
}

// QueueDepth returns the count of unclaimed jobs. Used by tests.
func (d *InMem) QueueDepth() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reapExpired()
	return len(d.queue)
}

// InFlight returns the count of claimed-but-unacked jobs.
func (d *InMem) InFlight() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reapExpired()
	return len(d.inFlight)
}

// reapExpired returns every in-flight job whose visibility window
// has expired back to the head of the queue.
func (d *InMem) reapExpired() {
	now := time.Now()
	for key, entry := range d.inFlight {
		if now.After(entry.expiresAt) {
			d.queue = append([]*Job{withFreshReceipt(entry.job)}, d.queue...)
			delete(d.inFlight, key)
		}
	}
}

func withFreshReceipt(j *Job) *Job {
	copy := *j
	t := make([]byte, 16)
	_, _ = rand.Read(t)
	copy.ReceiptToken = t
	return &copy
}

func receiptKey(t []byte) string { return hex.EncodeToString(t) }
