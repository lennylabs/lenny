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
