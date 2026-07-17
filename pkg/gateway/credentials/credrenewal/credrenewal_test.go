// SPDX-License-Identifier: MIT

package credrenewal_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal"
)

// fakeRenewer issues replacement leases. When err is set every Renew
// fails; otherwise it returns a fresh lease with the renew/expiry
// windows advanced by ttl.
type fakeRenewer struct {
	ttl   time.Duration
	err   error
	calls int
}

func (r *fakeRenewer) Renew(_ context.Context, lease credrenewal.Lease) (credrenewal.Lease, error) {
	r.calls++
	if r.err != nil {
		return credrenewal.Lease{}, r.err
	}
	now := lease.RenewBefore
	return credrenewal.Lease{
		LeaseID:     lease.LeaseID + "-r",
		SessionID:   lease.SessionID,
		RenewBefore: now.Add(r.ttl),
		ExpiresAt:   now.Add(r.ttl).Add(5 * time.Minute),
	}, nil
}

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestTickRenewsDueLease(t *testing.T) {
	r := &fakeRenewer{ttl: time.Hour}
	w := credrenewal.New(r, credrenewal.Options{})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", SessionID: "sess-1",
		RenewBefore: base, ExpiresAt: base.Add(5 * time.Minute),
	})

	renewed := w.Tick(context.Background(), base.Add(time.Second))
	if renewed != 1 {
		t.Fatalf("renewed = %d, want 1", renewed)
	}
	if r.calls != 1 {
		t.Errorf("Renewer called %d times, want 1", r.calls)
	}
	// The replacement lease is tracked under its new id.
	if w.Tracked() != 1 {
		t.Errorf("tracked = %d, want 1 (the replacement lease)", w.Tracked())
	}
}

func TestTickFiresOnRenewed(t *testing.T) {
	// §25.3: a proactive renewal that rotates a lease onto a fresh
	// credential fires the OnRenewed hook with the replacement lease.
	r := &fakeRenewer{ttl: time.Hour}
	var renewedLeases []credrenewal.Lease
	w := credrenewal.New(r, credrenewal.Options{
		OnRenewed: func(l credrenewal.Lease) { renewedLeases = append(renewedLeases, l) },
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", SessionID: "sess-1",
		RenewBefore: base, ExpiresAt: base.Add(5 * time.Minute),
	})
	w.Tick(context.Background(), base.Add(time.Second))
	if len(renewedLeases) != 1 || renewedLeases[0].LeaseID != "lease-1-r" {
		t.Errorf("OnRenewed: got %+v, want one replacement lease lease-1-r", renewedLeases)
	}
}

func TestTickFutureLeaseDoesNotFireOnRenewed(t *testing.T) {
	r := &fakeRenewer{ttl: time.Hour}
	fired := false
	w := credrenewal.New(r, credrenewal.Options{
		OnRenewed: func(credrenewal.Lease) { fired = true },
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", RenewBefore: base.Add(time.Hour), ExpiresAt: base.Add(2 * time.Hour),
	})
	w.Tick(context.Background(), base)
	if fired {
		t.Error("OnRenewed must not fire when no lease was renewed")
	}
}

func TestTickLeavesFutureLease(t *testing.T) {
	r := &fakeRenewer{ttl: time.Hour}
	w := credrenewal.New(r, credrenewal.Options{})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", RenewBefore: base.Add(time.Hour), ExpiresAt: base.Add(2 * time.Hour),
	})

	if renewed := w.Tick(context.Background(), base); renewed != 0 {
		t.Errorf("renewed = %d, want 0 — the lease's renewBefore is in the future", renewed)
	}
	if r.calls != 0 {
		t.Errorf("Renewer called %d times, want 0", r.calls)
	}
}

func TestTickSkipsExpiredLease(t *testing.T) {
	r := &fakeRenewer{ttl: time.Hour}
	var exhausted []string
	w := credrenewal.New(r, credrenewal.Options{
		OnExhausted: func(l credrenewal.Lease) { exhausted = append(exhausted, l.LeaseID) },
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", RenewBefore: base, ExpiresAt: base.Add(time.Minute),
	})

	// Sweep after the lease has already expired.
	if renewed := w.Tick(context.Background(), base.Add(2*time.Minute)); renewed != 0 {
		t.Errorf("renewed = %d, want 0 — an expired lease is not renewed", renewed)
	}
	if r.calls != 0 {
		t.Error("the Renewer was called for an already-expired lease")
	}
	if len(exhausted) != 1 || exhausted[0] != "lease-1" {
		t.Errorf("exhausted = %v, want [lease-1]", exhausted)
	}
	if w.Tracked() != 0 {
		t.Errorf("tracked = %d, want 0 — the expired lease was dropped", w.Tracked())
	}
}

func TestTickRetriesFailedRenewalThenExhausts(t *testing.T) {
	r := &fakeRenewer{err: errors.New("pool exhausted")}
	var exhausted []string
	w := credrenewal.New(r, credrenewal.Options{
		OnExhausted: func(l credrenewal.Lease) { exhausted = append(exhausted, l.LeaseID) },
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", RenewBefore: base, ExpiresAt: base.Add(time.Hour),
	})

	// Each tick retries; after MaxRenewalRetries the lease is dropped.
	for i := 0; i < credrenewal.MaxRenewalRetries; i++ {
		w.Tick(context.Background(), base.Add(time.Second))
	}
	if len(exhausted) != 1 || exhausted[0] != "lease-1" {
		t.Errorf("exhausted = %v, want [lease-1] after %d failed retries", exhausted, credrenewal.MaxRenewalRetries)
	}
	if w.Tracked() != 0 {
		t.Errorf("tracked = %d, want 0 — the exhausted lease was dropped", w.Tracked())
	}
}

func TestTickRenewalSucceedsAfterTransientFailure(t *testing.T) {
	r := &flakyRenewer{failFirst: 1, ttl: time.Hour}
	w := credrenewal.New(r, credrenewal.Options{})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", RenewBefore: base, ExpiresAt: base.Add(time.Hour),
	})

	w.Tick(context.Background(), base.Add(time.Second)) // fails
	w.Tick(context.Background(), base.Add(time.Second)) // succeeds
	if w.Tracked() != 1 {
		t.Errorf("tracked = %d, want 1 — the lease renewed on the retry", w.Tracked())
	}
}

func TestRevokeDropsBoundLeases(t *testing.T) {
	r := &fakeRenewer{ttl: time.Hour}
	var exhausted []string
	w := credrenewal.New(r, credrenewal.Options{
		OnExhausted: func(l credrenewal.Lease) { exhausted = append(exhausted, l.LeaseID) },
	})
	// Two leases on the revoked credential, one on a healthy credential.
	w.Track(credrenewal.Lease{
		LeaseID: "lease-a", CredentialID: "key-1",
		RenewBefore: base.Add(time.Hour), ExpiresAt: base.Add(2 * time.Hour),
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-b", CredentialID: "key-1",
		RenewBefore: base.Add(time.Hour), ExpiresAt: base.Add(2 * time.Hour),
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-c", CredentialID: "key-2",
		RenewBefore: base.Add(time.Hour), ExpiresAt: base.Add(2 * time.Hour),
	})

	w.Revoke("key-1")
	w.Tick(context.Background(), base)

	// Both key-1 leases dropped even though their renewBefore is far off.
	if len(exhausted) != 2 {
		t.Fatalf("exhausted %d leases, want 2 (both bound to the revoked credential)", len(exhausted))
	}
	if w.Tracked() != 1 {
		t.Errorf("tracked = %d, want 1 — only the healthy lease remains", w.Tracked())
	}
	// The Renewer is not invoked for a revoked credential's leases.
	if r.calls != 0 {
		t.Errorf("Renewer called %d times, want 0 — a revoked lease is not renewed", r.calls)
	}
}

func TestForgetStopsTracking(t *testing.T) {
	w := credrenewal.New(&fakeRenewer{ttl: time.Hour}, credrenewal.Options{})
	w.Track(credrenewal.Lease{LeaseID: "lease-1", RenewBefore: base, ExpiresAt: base.Add(time.Hour)})
	w.Forget("lease-1")
	if w.Tracked() != 0 {
		t.Errorf("tracked = %d, want 0 after Forget", w.Tracked())
	}
	if renewed := w.Tick(context.Background(), base.Add(time.Second)); renewed != 0 {
		t.Errorf("renewed = %d, want 0 — the forgotten lease is not renewed", renewed)
	}
}

// errTokenServiceBreakerOpen stands in for the signal the §4.3 Token
// Service circuit breaker surfaces to the renewal worker when it is open.
// The gateway wiring maps a tripped breaker to the
// credrenewal.ErrRenewInfraUnavailable sentinel at the package boundary;
// the test models that mapping by wrapping the sentinel so
// errors.Is(err, credrenewal.ErrRenewInfraUnavailable) holds.
var errTokenServiceBreakerOpen = fmt.Errorf(
	"credrenewal_test: Token Service circuit breaker open: %w",
	credrenewal.ErrRenewInfraUnavailable,
)

// TestTickHoldsLeaseWhenTokenServiceBreakerOpen_spec_4_9 pins the §4.9
// "Token Service unavailability guard": when the Token Service circuit
// breaker is open and the existing lease has not yet expired
// (now < expiresAt), the proactive renewal worker MUST NOT trigger the
// standard Fallback Flow. Instead it extends the lease deadline by one
// renewBeforeBuffer interval through OnExtend and reschedules the renewal,
// keeping the session alive on its current, still-valid credential until
// the Token Service recovers.
//
// The extension sets the new ExpiresAt to old ExpiresAt + renewBeforeBuffer
// (buffer = ExpiresAt - RenewBefore) and the new RenewBefore to the old
// ExpiresAt, so the lease is no longer due until one buffer later. A single
// extension therefore covers every subsequent tick at the same instant, and
// no tick reaches the Fallback Flow while the breaker stays open.
//
// spec: §4.9 line 1470 (Token Service unavailability guard)
func TestTickHoldsLeaseWhenTokenServiceBreakerOpen_spec_4_9(t *testing.T) {
	// Breaker-open is modeled as the Renewer failing with the mapped
	// sentinel; the lease's expiresAt is well in the future.
	r := &fakeRenewer{err: errTokenServiceBreakerOpen}
	var exhausted []string
	type extension struct {
		lease        credrenewal.Lease
		newExpiresAt time.Time
	}
	var extensions []extension
	w := credrenewal.New(r, credrenewal.Options{
		OnExhausted: func(l credrenewal.Lease) { exhausted = append(exhausted, l.LeaseID) },
		OnExtend: func(l credrenewal.Lease, newExpiresAt time.Time) error {
			extensions = append(extensions, extension{lease: l, newExpiresAt: newExpiresAt})
			return nil
		},
		// A generous TTL keeps the cumulative-extension cap out of play
		// for this transient-outage scenario.
		OnExtensionCapReached: func(credrenewal.Lease) {
			t.Errorf("OnExtensionCapReached fired for a transient outage well under one TTL")
		},
	})
	// buffer = ExpiresAt - RenewBefore = 1h; LeaseTTL = 24h so the cap
	// (24 extensions) is never approached.
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", SessionID: "sess-1",
		RenewBefore: base, ExpiresAt: base.Add(time.Hour),
		LeaseTTL: 24 * time.Hour,
	})

	// More ticks than MaxRenewalRetries: under the guard none may exhaust
	// the lease, because the credential is still valid and the breaker is
	// only transiently open. The Fallback Flow is reserved for an actually
	// expired credential (now >= expiresAt) or an upstream rejection.
	for i := 0; i < credrenewal.MaxRenewalRetries+2; i++ {
		w.Tick(context.Background(), base.Add(time.Second))
	}
	if len(exhausted) != 0 {
		t.Fatalf("lease exhausted %v while the Token Service breaker was open and the lease had not expired; §4.9 forbids the Fallback Flow here", exhausted)
	}
	// Exactly one extension: after it, RenewBefore advances to base+1h, so
	// the lease is no longer due at base+1s on later ticks.
	if len(extensions) != 1 {
		t.Fatalf("OnExtend fired %d times, want exactly 1 — the first extension moves RenewBefore past the tick instant", len(extensions))
	}
	if got, want := extensions[0].newExpiresAt, base.Add(2*time.Hour); !got.Equal(want) {
		t.Errorf("extension newExpiresAt = %s, want %s (old ExpiresAt + one buffer)", got, want)
	}
	if got, want := extensions[0].lease.ExpiresAt, base.Add(time.Hour); !got.Equal(want) {
		t.Errorf("extension called with ExpiresAt = %s, want the pre-extension %s", got, want)
	}
	if w.Tracked() != 1 {
		t.Errorf("tracked = %d, want 1 — the lease stays tracked and is rescheduled while the breaker is open", w.Tracked())
	}
}

// TestTickExhaustsWhenExtensionEnforcementFails_spec_4_9 pins the §4.9
// failure-fall-through: when the enforcement point is genuinely
// unreachable (OnExtend returns an error), the worker does not advance its
// own view of the deadline and the lease falls through to the retry and
// Fallback path, exhausting after MaxRenewalRetries. The deadline never
// advances, so every attempt targets the same newExpiresAt.
//
// spec: §4.9 line 1470 (Token Service unavailability guard)
func TestTickExhaustsWhenExtensionEnforcementFails_spec_4_9(t *testing.T) {
	r := &fakeRenewer{err: errTokenServiceBreakerOpen}
	var exhausted []string
	var attempts []time.Time
	w := credrenewal.New(r, credrenewal.Options{
		OnExhausted: func(l credrenewal.Lease) { exhausted = append(exhausted, l.LeaseID) },
		OnExtend: func(_ credrenewal.Lease, newExpiresAt time.Time) error {
			attempts = append(attempts, newExpiresAt)
			return errors.New("adapter unreachable")
		},
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", SessionID: "sess-1",
		RenewBefore: base, ExpiresAt: base.Add(time.Hour),
		LeaseTTL: 24 * time.Hour,
	})

	for i := 0; i < credrenewal.MaxRenewalRetries; i++ {
		w.Tick(context.Background(), base.Add(time.Second))
	}
	if len(exhausted) != 1 || exhausted[0] != "lease-1" {
		t.Fatalf("exhausted = %v, want [lease-1] — a failed extension falls through to fault rotation", exhausted)
	}
	if len(attempts) != credrenewal.MaxRenewalRetries {
		t.Errorf("OnExtend attempted %d times, want %d", len(attempts), credrenewal.MaxRenewalRetries)
	}
	// The deadline never advanced: every attempt targeted the same
	// newExpiresAt (old ExpiresAt + one buffer).
	for i, at := range attempts {
		if want := base.Add(2 * time.Hour); !at.Equal(want) {
			t.Errorf("attempt %d newExpiresAt = %s, want %s — the deadline must not advance on a failed extension", i, at, want)
		}
	}
	if w.Tracked() != 0 {
		t.Errorf("tracked = %d, want 0 — the lease was exhausted", w.Tracked())
	}
}

// TestTickCapsCumulativeExtensionAtLeaseTTL_spec_4_9 pins the §4.9
// bounded-extension cap: cumulative breaker-open extension may not exceed
// the lease's original leaseTTLSeconds. With buffer = 1h and LeaseTTL = 3h,
// the worker extends exactly three times, then at the cap drops the lease
// and fires OnExtensionCapReached without ever entering the Fallback Flow
// (no OnExhausted, no re-mint).
//
// spec: §4.9 line 1470 (Token Service unavailability guard — Bounded extension)
func TestTickCapsCumulativeExtensionAtLeaseTTL_spec_4_9(t *testing.T) {
	r := &fakeRenewer{err: errTokenServiceBreakerOpen}
	var exhausted []string
	var extended int
	var capped []credrenewal.Lease
	w := credrenewal.New(r, credrenewal.Options{
		OnExhausted:           func(l credrenewal.Lease) { exhausted = append(exhausted, l.LeaseID) },
		OnExtend:              func(credrenewal.Lease, time.Time) error { extended++; return nil },
		OnExtensionCapReached: func(l credrenewal.Lease) { capped = append(capped, l) },
	})
	// buffer = 1h, LeaseTTL = 3h → maxExtensions = 3.
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", SessionID: "sess-1",
		RenewBefore: base, ExpiresAt: base.Add(time.Hour),
		LeaseTTL: 3 * time.Hour,
	})

	// Each extension advances RenewBefore by one buffer, so the lease is
	// due again only one buffer later. Tick at the advancing due instants.
	w.Tick(context.Background(), base.Add(time.Second))             // extend #1
	w.Tick(context.Background(), base.Add(time.Hour+time.Second))   // extend #2
	w.Tick(context.Background(), base.Add(2*time.Hour+time.Second)) // extend #3
	w.Tick(context.Background(), base.Add(3*time.Hour+time.Second)) // cap

	if extended != 3 {
		t.Errorf("OnExtend fired %d times, want exactly 3 (one per renewBeforeBuffer in the TTL)", extended)
	}
	if len(capped) != 1 || capped[0].LeaseID != "lease-1" {
		t.Fatalf("OnExtensionCapReached = %v, want a single call for lease-1", capped)
	}
	if len(exhausted) != 0 {
		t.Errorf("exhausted = %v, want none — the cap terminates the session without the Fallback Flow", exhausted)
	}
	if w.Tracked() != 0 {
		t.Errorf("tracked = %d, want 0 — the capped lease is dropped from tracking", w.Tracked())
	}
}

// TestTickFallsThroughWhenExtensionCapCallbackUnset_spec_4_9 pins the §4.9
// cap behavior for a worker constructed without OnExtensionCapReached (a
// narrow unit-test worker): at the cap the branch is skipped and the lease
// follows the existing recordFailure/exhaust path rather than hanging or
// extending past the cap.
//
// spec: §4.9 line 1470 (Token Service unavailability guard — Bounded extension)
func TestTickFallsThroughWhenExtensionCapCallbackUnset_spec_4_9(t *testing.T) {
	r := &fakeRenewer{err: errTokenServiceBreakerOpen}
	var exhausted []string
	var extended int
	w := credrenewal.New(r, credrenewal.Options{
		OnExhausted: func(l credrenewal.Lease) { exhausted = append(exhausted, l.LeaseID) },
		OnExtend:    func(credrenewal.Lease, time.Time) error { extended++; return nil },
		// OnExtensionCapReached deliberately unset.
	})
	// buffer = 1h, LeaseTTL = 3h → maxExtensions = 3.
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", SessionID: "sess-1",
		RenewBefore: base, ExpiresAt: base.Add(time.Hour),
		LeaseTTL: 3 * time.Hour,
	})

	w.Tick(context.Background(), base.Add(time.Second))             // extend #1
	w.Tick(context.Background(), base.Add(time.Hour+time.Second))   // extend #2
	w.Tick(context.Background(), base.Add(2*time.Hour+time.Second)) // extend #3
	// At the cap with no callback, the lease falls through to
	// recordFailure/exhaust. MaxRenewalRetries ticks at the capped due
	// instant exhaust it.
	for i := 0; i < credrenewal.MaxRenewalRetries; i++ {
		w.Tick(context.Background(), base.Add(3*time.Hour+time.Second))
	}
	if extended != 3 {
		t.Errorf("OnExtend fired %d times, want exactly 3 — no extension past the cap", extended)
	}
	if len(exhausted) != 1 || exhausted[0] != "lease-1" {
		t.Fatalf("exhausted = %v, want [lease-1] — with no cap callback the lease falls through to fault rotation", exhausted)
	}
	if w.Tracked() != 0 {
		t.Errorf("tracked = %d, want 0", w.Tracked())
	}
}

// TestTickResetsExtensionCountOnSuccessfulRenewal_spec_4_9 pins the §4.9
// reset: a successful renewal after the breaker recovers replaces the
// tracked lease with a fresh count, so a transient outage shorter than one
// TTL never accumulates toward the cap. Two extensions, then a recovery
// renewal, then a full run of extensions on the fresh lease reaches the cap
// only after another maxExtensions extensions rather than sooner.
//
// spec: §4.9 line 1470 (Token Service unavailability guard — Bounded extension)
func TestTickResetsExtensionCountOnSuccessfulRenewal_spec_4_9(t *testing.T) {
	r := &togglingRenewer{buffer: time.Hour, leaseTTL: 3 * time.Hour, open: true}
	var extended int
	var capped []credrenewal.Lease
	var exhausted []string
	w := credrenewal.New(r, credrenewal.Options{
		OnExhausted:           func(l credrenewal.Lease) { exhausted = append(exhausted, l.LeaseID) },
		OnExtend:              func(credrenewal.Lease, time.Time) error { extended++; return nil },
		OnExtensionCapReached: func(l credrenewal.Lease) { capped = append(capped, l) },
	})
	// buffer = 1h, LeaseTTL = 3h → maxExtensions = 3.
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", SessionID: "sess-1",
		RenewBefore: base, ExpiresAt: base.Add(time.Hour),
		LeaseTTL: 3 * time.Hour,
	})

	w.Tick(context.Background(), base.Add(time.Second))           // extend #1 (count 1)
	w.Tick(context.Background(), base.Add(time.Hour+time.Second)) // extend #2 (count 2)

	// Breaker recovers: the next due tick renews onto a fresh lease with a
	// zero extension count. The replacement (lease-1-r) becomes due at
	// base+4h.
	r.open = false
	w.Tick(context.Background(), base.Add(2*time.Hour+time.Second)) // renew → lease-1-r
	if w.Tracked() != 1 {
		t.Fatalf("tracked = %d, want 1 after the recovery renewal", w.Tracked())
	}

	// Breaker reopens. If the count had persisted (2), the cap (3) would be
	// reached after one more extension. Because the renewal reset it, three
	// full extensions occur before the cap.
	r.open = true
	w.Tick(context.Background(), base.Add(4*time.Hour+time.Second)) // extend #3 overall, count 1
	w.Tick(context.Background(), base.Add(5*time.Hour+time.Second)) // extend #4 overall, count 2
	w.Tick(context.Background(), base.Add(6*time.Hour+time.Second)) // extend #5 overall, count 3
	if len(capped) != 0 {
		t.Fatalf("cap reached after %d extensions on the fresh lease; the count did not reset on renewal", extended)
	}
	w.Tick(context.Background(), base.Add(7*time.Hour+time.Second)) // cap on the fresh lease
	if extended != 5 {
		t.Errorf("OnExtend fired %d times, want 5 (2 before + 3 after the reset)", extended)
	}
	if len(capped) != 1 || capped[0].LeaseID != "lease-1-r" {
		t.Fatalf("OnExtensionCapReached = %v, want a single call for the renewed lease lease-1-r", capped)
	}
	if len(exhausted) != 0 {
		t.Errorf("exhausted = %v, want none", exhausted)
	}
}

// togglingRenewer fails with the mapped breaker sentinel while open, and
// otherwise returns a fresh lease whose RenewBefore/ExpiresAt advance by
// one buffer and whose LeaseTTL is preserved, so cap arithmetic is stable
// across a recovery renewal.
type togglingRenewer struct {
	open     bool
	buffer   time.Duration
	leaseTTL time.Duration
	calls    int
}

func (r *togglingRenewer) Renew(_ context.Context, lease credrenewal.Lease) (credrenewal.Lease, error) {
	r.calls++
	if r.open {
		return credrenewal.Lease{}, errTokenServiceBreakerOpen
	}
	// The replacement lease becomes due one buffer after the current
	// ExpiresAt, with the same buffer and TTL.
	renewBefore := lease.ExpiresAt.Add(r.buffer)
	return credrenewal.Lease{
		LeaseID:     lease.LeaseID + "-r",
		SessionID:   lease.SessionID,
		RenewBefore: renewBefore,
		ExpiresAt:   renewBefore.Add(r.buffer),
		LeaseTTL:    r.leaseTTL,
	}, nil
}

// flakyRenewer fails its first failFirst calls, then succeeds.
type flakyRenewer struct {
	failFirst int
	ttl       time.Duration
	calls     int
}

func (r *flakyRenewer) Renew(_ context.Context, lease credrenewal.Lease) (credrenewal.Lease, error) {
	r.calls++
	if r.calls <= r.failFirst {
		return credrenewal.Lease{}, errors.New("transient")
	}
	return credrenewal.Lease{
		LeaseID:     lease.LeaseID + "-r",
		RenewBefore: lease.RenewBefore.Add(r.ttl),
		ExpiresAt:   lease.RenewBefore.Add(r.ttl).Add(5 * time.Minute),
	}, nil
}

// TestDefaultExpiryWarningLeadIsOneHour_spec_11_3_215 pins the §11.3
// line 215 default (3600s) at the package layer so any future drift
// breaks the test rather than silently shrinking the lead time.
// F-11.3.20.
func TestDefaultExpiryWarningLeadIsOneHour_spec_11_3_215(t *testing.T) {
	if credrenewal.DefaultExpiryWarningLead != 3600*time.Second {
		t.Errorf("DefaultExpiryWarningLead = %s, want 3600s per §11.3 line 215",
			credrenewal.DefaultExpiryWarningLead)
	}
}

// TestTickFiresOnExpiryWarningOncePerLease_spec_11_3_215 proves the
// warning hook fires exactly once when a tracked lease crosses into
// the expiry-warning window. A second tick after the warning has fired
// does not re-emit. F-11.3.20.
func TestTickFiresOnExpiryWarningOncePerLease_spec_11_3_215(t *testing.T) {
	r := &fakeRenewer{ttl: time.Hour}
	var warned []credrenewal.Lease
	w := credrenewal.New(r, credrenewal.Options{
		ExpiryWarningLead: 30 * time.Minute,
		OnExpiryWarning:   func(l credrenewal.Lease) { warned = append(warned, l) },
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-warn", SessionID: "sess-1",
		RenewBefore: base.Add(2 * time.Hour),
		ExpiresAt:   base.Add(time.Hour),
	})
	// 31 minutes past base — exactly inside the 30m warning window
	// (expiresAt - lead = base + 30m). The warning fires.
	w.Tick(context.Background(), base.Add(31*time.Minute))
	if len(warned) != 1 || warned[0].LeaseID != "lease-warn" {
		t.Fatalf("OnExpiryWarning: got %+v, want a single warning for lease-warn", warned)
	}
	// A second tick still inside the same window must not re-fire.
	w.Tick(context.Background(), base.Add(45*time.Minute))
	if len(warned) != 1 {
		t.Errorf("OnExpiryWarning fired %d times, want exactly 1 per lease lifetime", len(warned))
	}
}

// TestTickHoldsBackExpiryWarningOutsideWindow_spec_11_3_215 proves the
// warning hook is silent when now is before expiresAt-lead. F-11.3.20.
func TestTickHoldsBackExpiryWarningOutsideWindow_spec_11_3_215(t *testing.T) {
	r := &fakeRenewer{ttl: time.Hour}
	fired := false
	w := credrenewal.New(r, credrenewal.Options{
		ExpiryWarningLead: 30 * time.Minute,
		OnExpiryWarning:   func(credrenewal.Lease) { fired = true },
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-far", SessionID: "sess-1",
		RenewBefore: base.Add(2 * time.Hour),
		ExpiresAt:   base.Add(time.Hour),
	})
	// 25 minutes past base — 35 minutes until expiry, outside the 30m
	// warning window (warningAt = base + 30m).
	w.Tick(context.Background(), base.Add(25*time.Minute))
	if fired {
		t.Error("OnExpiryWarning fired before the lease crossed into its warning window")
	}
}

// TestTickHonorsZeroExpiryWarningLead_spec_11_3_215 proves a negative
// lead value disables warning emission entirely (an operator opt-out).
// The package-default normalization happens only when the option is
// exactly zero. F-11.3.20.
func TestTickHonorsZeroExpiryWarningLead_spec_11_3_215(t *testing.T) {
	r := &fakeRenewer{ttl: time.Hour}
	fired := false
	w := credrenewal.New(r, credrenewal.Options{
		ExpiryWarningLead: -1,
		OnExpiryWarning:   func(credrenewal.Lease) { fired = true },
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-off", SessionID: "sess-1",
		RenewBefore: base.Add(time.Hour),
		ExpiresAt:   base.Add(time.Minute),
	})
	w.Tick(context.Background(), base.Add(time.Second))
	if fired {
		t.Error("OnExpiryWarning fired with a negative ExpiryWarningLead override (warnings disabled)")
	}
}

// TestTickFiresExpiryWarningEvenWhenRenewalSucceeds_spec_11_3_215 proves
// the warning is a separate signal from the renewal lifecycle: a lease
// in the warning window with a non-due renewBefore still fires the
// warning, so operators get the heads-up before the renewal worker
// completes. F-11.3.20.
func TestTickFiresExpiryWarningEvenWhenRenewalSucceeds_spec_11_3_215(t *testing.T) {
	r := &fakeRenewer{ttl: time.Hour}
	var warned []credrenewal.Lease
	var renewed []credrenewal.Lease
	w := credrenewal.New(r, credrenewal.Options{
		ExpiryWarningLead: 30 * time.Minute,
		OnExpiryWarning:   func(l credrenewal.Lease) { warned = append(warned, l) },
		OnRenewed:         func(l credrenewal.Lease) { renewed = append(renewed, l) },
	})
	w.Track(credrenewal.Lease{
		LeaseID: "lease-1", SessionID: "sess-1",
		RenewBefore: base.Add(2 * time.Hour),    // not due
		ExpiresAt:   base.Add(45 * time.Minute), // inside the warning window
	})
	w.Tick(context.Background(), base.Add(20*time.Minute))
	if len(warned) != 1 {
		t.Errorf("OnExpiryWarning: got %d warnings, want 1", len(warned))
	}
	if len(renewed) != 0 {
		t.Errorf("OnRenewed fired for a lease whose renewBefore is in the future")
	}
}
