// SPDX-License-Identifier: MIT

package credrenewal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credrenewal"
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
