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
	"errors"
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

// DefaultMaxExtensions bounds consecutive breaker-open lease extensions
// per §4.9 when a lease carries no LeaseTTL from which to derive the
// cumulative-extension cap. It is the fallback for maxExtensions.
// spec: §4.9 line 1470.
const DefaultMaxExtensions = 3

// ErrRenewInfraUnavailable signals that a renewal could not proceed
// because the renewal infrastructure (the §4.3 Token Service circuit
// breaker) is transiently open, not because the lease's credential is
// bad. Per the §4.9 Token Service unavailability guard, a lease failing
// with this error while it has not yet expired is held and rescheduled,
// not exhausted into the Fallback Flow. The gateway wiring maps
// credassign.ErrTokenServiceUnavailable to this sentinel at the package
// boundary so credrenewal need not import credassign.
// spec: §4.9 line 1470.
var ErrRenewInfraUnavailable = errors.New("credrenewal: renewal infrastructure transiently unavailable")

// maxExtensions returns the number of renewBeforeBuffer intervals that
// fit in one leaseTTLSeconds, the §4.9 cumulative-extension cap. Because
// each extension sets RenewBefore = old ExpiresAt and ExpiresAt = old +
// buffer, buffer is invariant across extensions, so the cap is stable
// for the life of the lease. When either input is non-positive (a lease
// carrying no TTL), it falls back to DefaultMaxExtensions.
// spec: §4.9 line 1470.
func maxExtensions(ttl, buffer time.Duration) int {
	if ttl <= 0 || buffer <= 0 {
		return DefaultMaxExtensions
	}
	n := int(ttl / buffer)
	if n < 1 {
		return 1
	}
	return n
}

// DefaultExpiryWarningLead is the §11.3 line 215
// `credentials.expiryWarningLeadSeconds` default (1h). When a lease is
// within this window of its ExpiresAt, the worker fires OnExpiryWarning
// once so a deployer-facing observer (log, metric, audit hook) can
// surface impending lease expiry before the §4.9 proactive renewal
// fully runs out of retries.
// spec: §11.3 line 215.
const DefaultExpiryWarningLead = 3600 * time.Second

// Lease is the subset of a §4.9 CredentialLease the renewal worker
// tracks.
type Lease struct {
	// LeaseID identifies the lease.
	LeaseID string
	// SessionID is the session the lease was assigned to.
	SessionID string
	// CredentialID identifies the underlying pool credential the lease
	// is bound to. A §4.9 emergency revocation targets this id.
	CredentialID string
	// RenewBefore is the §4.9 deadline at which proactive renewal
	// should issue a replacement lease.
	RenewBefore time.Time
	// ExpiresAt is the instant the lease's credential stops working.
	ExpiresAt time.Time
	// LeaseTTL is the lease's original leaseTTLSeconds, populated by the
	// wiring from the pool configuration. It bounds cumulative
	// breaker-open extension under the §4.9 Token Service unavailability
	// guard: total extension may not exceed one LeaseTTL. A zero value
	// selects DefaultMaxExtensions.
	// spec: §4.9 line 1470.
	LeaseTTL time.Duration
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

	// OnRenewed, when set, is called with the replacement lease each
	// time proactive renewal rotates a lease onto a fresh credential.
	// It is the §25.3 credential_rotated signal.
	OnRenewed func(renewed Lease)

	// OnExtend, when set, is called under the §4.9 Token Service
	// unavailability guard to extend a still-valid lease's enforced
	// deadline to newExpiresAt through the delivery mode's enforcement
	// point (the adapter expiry timer for direct mode, the gateway lease
	// store for proxy mode). It returns an error when the enforcement
	// point could not be reached, in which case the worker does not
	// advance its own view of the deadline and the lease falls through to
	// the retry and Fallback path. spec: §4.9 line 1470.
	OnExtend func(lease Lease, newExpiresAt time.Time) error

	// OnExtensionCapReached, when set, is called under the §4.9 Token
	// Service unavailability guard when a lease's cumulative breaker-open
	// extension has reached its original leaseTTLSeconds and the breaker
	// is still open. The gateway wires it to a terminal session teardown
	// that drives the session to the §8.8 expired state (surfaced to
	// clients as expired:lease) and does NOT enter the Fallback Flow,
	// because a re-mint against the still-open breaker would re-enter the
	// restart loop the guard prevents. The worker drops the lease from
	// tracking before invoking it. spec: §4.9 line 1470.
	OnExtensionCapReached func(lease Lease)

	// ExpiryWarningLead overrides DefaultExpiryWarningLead. The §11.3
	// line 215 contract: when a tracked lease is within this window of
	// its ExpiresAt, the worker fires OnExpiryWarning exactly once. Set
	// to a negative value to disable warnings outright.
	ExpiryWarningLead time.Duration

	// OnExpiryWarning, when set, is called the first time a tracked
	// lease enters the warning window (now >= expiresAt -
	// ExpiryWarningLead). It is the §11.3 deployer-tunable lead-time
	// hook used to surface impending lease expiry to logs / metrics /
	// audit before the §4.9 fault-rotation path is consumed.
	OnExpiryWarning func(lease Lease)
}

// Worker is the §4.9 CredentialRenewalWorker. It is goroutine-safe.
type Worker struct {
	renewer               Renewer
	interval              time.Duration
	clock                 func() time.Time
	onExhausted           func(Lease)
	onRenewed             func(Lease)
	onExtend              func(Lease, time.Time) error
	onExtensionCapReached func(Lease)
	expiryWarningLead     time.Duration
	onExpiryWarning       func(Lease)

	mu      sync.Mutex
	tracked map[string]*trackedLease
	revoked map[string]bool
}

// trackedLease pairs a lease with its proactive-renewal retry count.
type trackedLease struct {
	lease              Lease
	retries            int
	expiryWarningFired bool
	// extensions counts consecutive breaker-open extensions of this
	// lease under the §4.9 Token Service unavailability guard. It bounds
	// cumulative extension at the lease's original TTL and resets when a
	// normal renewal replaces the tracked lease.
	extensions int
}

// New returns a Worker that renews via renewer.
func New(renewer Renewer, opts Options) *Worker {
	w := &Worker{
		renewer:               renewer,
		interval:              opts.Interval,
		clock:                 opts.Clock,
		onExhausted:           opts.OnExhausted,
		onRenewed:             opts.OnRenewed,
		onExtend:              opts.OnExtend,
		onExtensionCapReached: opts.OnExtensionCapReached,
		expiryWarningLead:     opts.ExpiryWarningLead,
		onExpiryWarning:       opts.OnExpiryWarning,
		tracked:               map[string]*trackedLease{},
		revoked:               map[string]bool{},
	}
	if w.interval <= 0 {
		w.interval = DefaultInterval
	}
	if w.clock == nil {
		w.clock = func() time.Time { return time.Now() }
	}
	// spec: §11.3 line 215 — zero selects the platform default; a
	// negative override disables warning emission outright.
	if w.expiryWarningLead == 0 {
		w.expiryWarningLead = DefaultExpiryWarningLead
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

// Revoke marks a pool credential as revoked per §4.9 emergency
// revocation. On the next sweep every tracked lease bound to the
// credential is dropped and signals OnExhausted, so the affected
// sessions fall through to fault rotation onto a fresh credential.
func (w *Worker) Revoke(credentialID string) {
	w.mu.Lock()
	w.revoked[credentialID] = true
	w.mu.Unlock()
}

// Tick runs one renewal sweep at now. A tracked lease bound to a
// revoked credential is dropped immediately (the §4.9 emergency
// revocation path) and signals OnExhausted. Otherwise, every lease
// whose renewBefore has passed is processed: an already-expired lease
// cannot be renewed (the §4.9 expiresAt guard) — it is dropped and
// signals OnExhausted; a live lease is renewed via the Renewer, and on
// failure is retried on later ticks up to MaxRenewalRetries before
// being dropped with an OnExhausted signal. Tick returns the count of
// leases successfully renewed.
func (w *Worker) Tick(ctx context.Context, now time.Time) int {
	w.mu.Lock()
	due := make([]*trackedLease, 0)
	revokedLeases := make([]Lease, 0)
	warnings := make([]Lease, 0)
	// spec: §11.3 line 215 — fire the expiry warning once per lease
	// when now is inside the deployer-tunable warning window. The
	// warning fires regardless of whether the lease is due for renewal,
	// so a deployer sees impending expiry even when the proactive-
	// renewal retry path is in flight.
	for _, tl := range w.tracked {
		if w.revoked[tl.lease.CredentialID] {
			revokedLeases = append(revokedLeases, tl.lease)
			continue
		}
		if w.expiryWarningLead > 0 && !tl.expiryWarningFired {
			warningAt := tl.lease.ExpiresAt.Add(-w.expiryWarningLead)
			if !now.Before(warningAt) {
				tl.expiryWarningFired = true
				warnings = append(warnings, tl.lease)
			}
		}
		if !now.Before(tl.lease.RenewBefore) {
			due = append(due, tl)
		}
	}
	w.mu.Unlock()

	for _, lease := range warnings {
		if w.onExpiryWarning != nil {
			w.onExpiryWarning(lease)
		}
	}

	for _, lease := range revokedLeases {
		w.exhaust(lease)
	}

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
			// spec: §4.9 line 1470 — Token Service unavailability guard.
			// The breaker is open and the lease is still valid (the
			// now >= ExpiresAt guard above already handled the expired
			// case). Extend the enforced deadline by one renewBeforeBuffer
			// and reschedule instead of exhausting into the Fallback Flow.
			if errors.Is(err, ErrRenewInfraUnavailable) && w.onExtend != nil {
				buffer := tl.lease.ExpiresAt.Sub(tl.lease.RenewBefore)
				// spec: §4.9 line 1470 — cumulative-extension cap. Once
				// total extension reaches the lease's original TTL, stop
				// extending; a permanently-open breaker must not keep the
				// key alive forever. At the cap, terminate the session
				// without re-minting against the still-open breaker (which
				// would loop) rather than dropping into the Fallback Flow.
				if tl.extensions >= maxExtensions(tl.lease.LeaseTTL, buffer) {
					if w.onExtensionCapReached != nil {
						w.mu.Lock()
						delete(w.tracked, tl.lease.LeaseID)
						w.mu.Unlock()
						w.onExtensionCapReached(tl.lease)
						continue
					}
					// No terminal callback wired (a narrow unit-test
					// worker): fall through to recordFailure/exhaust rather
					// than extend past the cap.
				} else {
					newExpiresAt := tl.lease.ExpiresAt.Add(buffer)
					if extErr := w.onExtend(tl.lease, newExpiresAt); extErr == nil {
						w.mu.Lock()
						tl.lease.RenewBefore = tl.lease.ExpiresAt
						tl.lease.ExpiresAt = newExpiresAt
						tl.extensions++
						tl.retries = 0
						w.mu.Unlock()
						continue
					}
					// The enforcement point was unreachable; fall through
					// to recordFailure/exhaust.
				}
			}
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
		if w.onRenewed != nil {
			w.onRenewed(next)
		}
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
