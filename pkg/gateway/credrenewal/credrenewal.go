// SPDX-License-Identifier: MIT

// Package credrenewal implements the §4.9 CredentialRenewalWorker: the
// gateway-side proactive lease-renewal loop. Each active credential
// lease carries a renewBefore timestamp; the worker tracks leases and
// issues a replacement before the original expires, so a long-lived
// session never sees its LLM credential lapse.
//
// A proactive renewal does not consume the session's
// maxRotationsPerSession budget (§4.9). When proactive renewal cannot
// refresh a lease — the lease has already expired, or the retry budget
// is exhausted — the worker drops the lease and signals OnExhausted so
// the caller can fall through to the §4.9 fault-rotation path.
package credrenewal

import (
	"context"
	"sync"
	"time"
)

// DefaultInterval is the renewal sweep interval. It is well under the
// §4.9 renewBeforeBuffer (300s) so a lease is renewed comfortably
// before its renewBefore deadline.
const DefaultInterval = 30 * time.Second

// MaxRenewalRetries bounds proactive renewal attempts per §4.9 before
// the worker gives up and the lease falls through to fault rotation.
const MaxRenewalRetries = 3

// Lease is the subset of a §4.9 CredentialLease the renewal worker
// tracks.
type Lease struct {
	// LeaseID identifies the lease.
	LeaseID string
	// SessionID is the session the lease was assigned to.
	SessionID string
	// RenewBefore is the §4.9 deadline at which proactive renewal
	// should issue a replacement lease.
	RenewBefore time.Time
	// ExpiresAt is the instant the lease's credential stops working.
	ExpiresAt time.Time
}

// Renewer issues a replacement lease for a lease approaching its
// renewBefore deadline — the §4.9 AssignCredentials path.
type Renewer interface {
	Renew(ctx context.Context, lease Lease) (Lease, error)
}

// Options configures a Worker. A zero field selects its default.
type Options struct {
	// Interval overrides DefaultInterval.
	Interval time.Duration
	// Clock overrides time.Now.
	Clock func() time.Time
	// OnExhausted, when set, is called for a lease whose proactive
	// renewal cannot proceed — the lease has expired or its retry
	// budget is exhausted. It is the §4.9 fall-through signal to fault
	// rotation.
	OnExhausted func(lease Lease)
}

// Worker is the §4.9 CredentialRenewalWorker. It is goroutine-safe.
type Worker struct {
	renewer     Renewer
	interval    time.Duration
	clock       func() time.Time
	onExhausted func(Lease)

	mu      sync.Mutex
	tracked map[string]*trackedLease
}

// trackedLease pairs a lease with its proactive-renewal retry count.
type trackedLease struct {
	lease   Lease
	retries int
}

// New returns a Worker that renews via renewer.
func New(renewer Renewer, opts Options) *Worker {
	w := &Worker{
		renewer:     renewer,
		interval:    opts.Interval,
		clock:       opts.Clock,
		onExhausted: opts.OnExhausted,
		tracked:     map[string]*trackedLease{},
	}
	if w.interval <= 0 {
		w.interval = DefaultInterval
	}
	if w.clock == nil {
		w.clock = func() time.Time { return time.Now() }
	}
	return w
}

// Track registers a lease for proactive renewal. A lease already
// tracked under the same LeaseID is replaced.
func (w *Worker) Track(lease Lease) {
	w.mu.Lock()
	w.tracked[lease.LeaseID] = &trackedLease{lease: lease}
	w.mu.Unlock()
}

// Forget stops tracking a lease — the caller invokes it when the
// lease's session ends so a terminated session is not renewed.
func (w *Worker) Forget(leaseID string) {
	w.mu.Lock()
	delete(w.tracked, leaseID)
	w.mu.Unlock()
}

// Tracked reports the number of leases under renewal management.
func (w *Worker) Tracked() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.tracked)
}

// Tick runs one renewal sweep at now: every tracked lease whose
// renewBefore has passed is processed. An already-expired lease cannot
// be renewed (the §4.9 expiresAt guard) — it is dropped and signals
// OnExhausted. A live lease is renewed via the Renewer; on success the
// replacement lease is tracked under its new renewBefore, and on
// failure the lease is retried on later ticks up to MaxRenewalRetries
// before being dropped with an OnExhausted signal. Tick returns the
// count of leases successfully renewed.
func (w *Worker) Tick(ctx context.Context, now time.Time) int {
	w.mu.Lock()
	due := make([]*trackedLease, 0)
	for _, tl := range w.tracked {
		if !now.Before(tl.lease.RenewBefore) {
			due = append(due, tl)
		}
	}
	w.mu.Unlock()

	renewed := 0
	for _, tl := range due {
		// §4.9 expiresAt guard: an already-expired lease is not retried;
		// it falls through to fault rotation.
		if !now.Before(tl.lease.ExpiresAt) {
			w.exhaust(tl.lease)
			continue
		}
		next, err := w.renewer.Renew(ctx, tl.lease)
		if err != nil {
			if w.recordFailure(tl) {
				w.exhaust(tl.lease)
			}
			continue
		}
		w.mu.Lock()
		delete(w.tracked, tl.lease.LeaseID)
		w.tracked[next.LeaseID] = &trackedLease{lease: next}
		w.mu.Unlock()
		renewed++
	}
	return renewed
}

// recordFailure increments a lease's retry count and reports whether
// the §4.9 retry budget is now exhausted, dropping the lease when it
// is.
func (w *Worker) recordFailure(tl *trackedLease) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	tl.retries++
	if tl.retries >= MaxRenewalRetries {
		delete(w.tracked, tl.lease.LeaseID)
		return true
	}
	return false
}

// exhaust drops a lease from tracking and signals OnExhausted.
func (w *Worker) exhaust(lease Lease) {
	w.mu.Lock()
	delete(w.tracked, lease.LeaseID)
	w.mu.Unlock()
	if w.onExhausted != nil {
		w.onExhausted(lease)
	}
}

// Run drives the renewal sweep on the configured interval until ctx is
// done.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Tick(ctx, w.clock())
		}
	}
}
