// SPDX-License-Identifier: MIT

package leasestore

import (
	"context"
	"errors"
	"testing"
	"time"
)

// spec: §12.4 line 206 — "Distributed session leases | Fall back to
// Postgres advisory locks (higher latency)". The Failover wrapper
// forwards lease-domain outcomes from the Redis primary unchanged and
// routes only infrastructure failures to the Postgres fallback.

// fakeLeaseStore is a configurable LeaseStore that records how many
// times each method was called and returns a preset (lease, err).
type fakeLeaseStore struct {
	name      string
	lease     Lease
	count     int
	err       error
	delCount  int
	delErr    error
	calls     map[string]int
}

func newFake(name string) *fakeLeaseStore {
	return &fakeLeaseStore{name: name, calls: map[string]int{}}
}

func (f *fakeLeaseStore) Acquire(_ context.Context, t, s, h string, _ time.Duration) (Lease, error) {
	f.calls["Acquire"]++
	return f.lease, f.err
}
func (f *fakeLeaseStore) Renew(_ context.Context, t, s, h string, _ time.Duration) (Lease, error) {
	f.calls["Renew"]++
	return f.lease, f.err
}
func (f *fakeLeaseStore) Release(_ context.Context, t, s, h string) error {
	f.calls["Release"]++
	return f.err
}
func (f *fakeLeaseStore) Get(_ context.Context, t, s string) (Lease, error) {
	f.calls["Get"]++
	return f.lease, f.err
}
func (f *fakeLeaseStore) DeleteByUser(_ context.Context, t, u string) (int, error) {
	f.calls["DeleteByUser"]++
	return f.delCount, f.delErr
}
func (f *fakeLeaseStore) DeleteByTenant(_ context.Context, t string) (int, error) {
	f.calls["DeleteByTenant"]++
	return f.delCount, f.delErr
}

func TestFailover_DomainOutcomesDoNotFallBack_spec_12_4(t *testing.T) {
	ctx := context.Background()
	ttl := time.Minute

	// Each domain outcome the primary can return: nil, and every lease
	// sentinel. None should engage the fallback.
	for _, primErr := range []error{nil, ErrHeld, ErrNotHeld, ErrNotFound, ErrEmptyScope} {
		primErr := primErr
		t.Run(name(primErr), func(t *testing.T) {
			prim := newFake("redis")
			prim.err = primErr
			prim.delErr = primErr
			fb := newFake("postgres")
			fellBack := 0
			f := NewFailover(prim, fb, func() { fellBack++ })

			_, _ = f.Acquire(ctx, "acme", "s1", "r1", ttl)
			_, _ = f.Renew(ctx, "acme", "s1", "r1", ttl)
			_ = f.Release(ctx, "acme", "s1", "r1")
			_, _ = f.Get(ctx, "acme", "s1")
			_, _ = f.DeleteByUser(ctx, "acme", "alice")
			_, _ = f.DeleteByTenant(ctx, "acme")

			if fellBack != 0 {
				t.Fatalf("primary returned domain outcome %v; fallback engaged %d times, want 0", primErr, fellBack)
			}
			for _, m := range []string{"Acquire", "Renew", "Release", "Get", "DeleteByUser", "DeleteByTenant"} {
				if prim.calls[m] != 1 {
					t.Errorf("primary %s called %d times, want 1", m, prim.calls[m])
				}
				if fb.calls[m] != 0 {
					t.Errorf("fallback %s called %d times, want 0 (no outage)", m, fb.calls[m])
				}
			}
		})
	}
}

func TestFailover_InfraErrorRoutesToFallback_spec_12_4(t *testing.T) {
	ctx := context.Background()
	ttl := time.Minute
	infra := errors.New("dial tcp 127.0.0.1:6379: connect: connection refused")

	prim := newFake("redis")
	prim.err = infra
	prim.delErr = infra
	fb := newFake("postgres")
	want := Lease{TenantID: "acme", SessionID: "s1", Holder: "r1", ExpiresAt: time.Unix(100, 0)}
	fb.lease = want
	fb.delCount = 7
	fellBack := 0
	f := NewFailover(prim, fb, func() { fellBack++ })

	got, err := f.Acquire(ctx, "acme", "s1", "r1", ttl)
	if err != nil {
		t.Fatalf("Acquire via fallback: %v", err)
	}
	if got != want {
		t.Fatalf("Acquire returned %+v, want fallback lease %+v", got, want)
	}
	_, _ = f.Renew(ctx, "acme", "s1", "r1", ttl)
	_ = f.Release(ctx, "acme", "s1", "r1")
	_, _ = f.Get(ctx, "acme", "s1")
	// Both erasure primitives forward the fallback's count unchanged.
	n, _ := f.DeleteByUser(ctx, "acme", "alice")
	if n != 7 {
		t.Errorf("DeleteByUser via fallback returned %d, want fallback count 7", n)
	}
	n, _ = f.DeleteByTenant(ctx, "acme")
	if n != 7 {
		t.Errorf("DeleteByTenant via fallback returned %d, want fallback count 7", n)
	}

	for _, m := range []string{"Acquire", "Renew", "Release", "Get", "DeleteByUser", "DeleteByTenant"} {
		if prim.calls[m] != 1 {
			t.Errorf("primary %s called %d times, want 1 (tried first)", m, prim.calls[m])
		}
		if fb.calls[m] != 1 {
			t.Errorf("fallback %s called %d times, want 1 (primary unavailable)", m, fb.calls[m])
		}
	}
	if fellBack != 6 {
		t.Fatalf("onFallback fired %d times, want 6 (one per routed op)", fellBack)
	}
}

// TestFailover_GetNotFoundIsHealthy pins that a primary ErrNotFound — a
// healthy "no lease" answer — is forwarded without consulting the
// fallback, so a routine miss does not pay the Postgres round-trip.
func TestFailover_GetNotFoundIsHealthy_spec_12_4(t *testing.T) {
	ctx := context.Background()
	prim := newFake("redis")
	prim.err = ErrNotFound
	fb := newFake("postgres")
	fellBack := 0
	f := NewFailover(prim, fb, func() { fellBack++ })

	_, err := f.Get(ctx, "acme", "s1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get returned %v, want ErrNotFound passed through", err)
	}
	if fb.calls["Get"] != 0 || fellBack != 0 {
		t.Fatalf("a healthy ErrNotFound engaged the fallback (calls=%d, fellBack=%d)", fb.calls["Get"], fellBack)
	}
}

func name(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
