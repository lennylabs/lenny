// SPDX-License-Identifier: MIT

package tenantkms_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// spec: §12.5 — the T4 per-tenant KMS availability probe is an
// admin-time and continuous round-trip against the tenant-scoped key
// that gates the `PUT /v1/admin/tenants/{id}` workspaceTier transition
// and feeds the `lenny_t4_kms_probe_last_success_timestamp` gauge.

// newProbeLifecycle builds a Lifecycle over a LocalManager with a
// fixed clock so the test asserts deterministic last-probe-success
// timestamps.
func newProbeLifecycle(t *testing.T, clock func() time.Time) (*tenantkms.Lifecycle, *tenantkms.LocalManager) {
	t.Helper()
	seed := bytes.Repeat([]byte{0xa5}, kms.DEKSize)
	local, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	mgr := tenantkms.NewLocalManager(local)
	return tenantkms.NewWithClock(mgr, clock), mgr
}

func TestProbeAvailabilitySkipsNonT4(t *testing.T) {
	now := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	lc, _ := newProbeLifecycle(t, func() time.Time { return now })
	// A T3 tenant has no per-tenant key, so the probe is a no-op and
	// the lifecycle records nothing.
	if err := lc.ProbeAvailability(context.Background(), "acme", "T3"); err != nil {
		t.Fatalf("ProbeAvailability T3: %v", err)
	}
	if _, ok := lc.LastProbeSuccess("acme"); ok {
		t.Error("non-T4 probe must not record a success")
	}
}

func TestProbeAvailabilityRejectsEmptyTenant(t *testing.T) {
	lc, _ := newProbeLifecycle(t, time.Now)
	if err := lc.ProbeAvailability(context.Background(), "", tenantkms.WorkspaceTierT4); !errors.Is(err, tenantkms.ErrEmptyTenantID) {
		t.Errorf("ProbeAvailability empty tenant: %v, want ErrEmptyTenantID", err)
	}
}

func TestProbeAvailabilityKeyNotProvisioned(t *testing.T) {
	lc, _ := newProbeLifecycle(t, time.Now)
	err := lc.ProbeAvailability(context.Background(), "acme", tenantkms.WorkspaceTierT4)
	if !errors.Is(err, tenantkms.ErrKeyNotFound) {
		t.Errorf("ProbeAvailability with no key: %v, want ErrKeyNotFound", err)
	}
	if _, ok := lc.LastProbeSuccess("acme"); ok {
		t.Error("failed probe must not record a success")
	}
}

func TestProbeAvailabilitySuccessRecordsTimestamp(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	lc, mgr := newProbeLifecycle(t, func() time.Time { return now })
	ctx := context.Background()
	if _, err := mgr.ProvisionKey(ctx, tenantkms.AliasFor("acme")); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	if err := lc.ProbeAvailability(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("ProbeAvailability: %v", err)
	}
	got, ok := lc.LastProbeSuccess("acme")
	if !ok {
		t.Fatal("LastProbeSuccess missing after a successful probe")
	}
	if !got.Equal(now) {
		t.Errorf("LastProbeSuccess = %s, want %s", got, now)
	}
}

func TestProbeAvailabilityKeyDisabledFailsClosed(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	lc, mgr := newProbeLifecycle(t, func() time.Time { return now })
	ctx := context.Background()
	alias := tenantkms.AliasFor("acme")
	if _, err := mgr.ProvisionKey(ctx, alias); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	// Probe succeeds while the key is active.
	if err := lc.ProbeAvailability(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("ProbeAvailability active: %v", err)
	}
	// §12.8 Phase 4a disables the key — subsequent probes must
	// fail-closed so the admin API rejects a re-assert and the
	// continuous probe drives the alert.
	if _, err := mgr.DisableKey(ctx, alias); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}
	err := lc.ProbeAvailability(ctx, "acme", tenantkms.WorkspaceTierT4)
	if !errors.Is(err, tenantkms.ErrKeyUnavailable) {
		t.Errorf("ProbeAvailability disabled key: %v, want ErrKeyUnavailable", err)
	}
	// Last-success timestamp is the prior success and must not advance.
	got, ok := lc.LastProbeSuccess("acme")
	if !ok || !got.Equal(now) {
		t.Errorf("LastProbeSuccess after failure = %s ok=%v, want pre-failure %s", got, ok, now)
	}
}

func TestProbeAvailabilityAdvancesTimestampOnRepeatedSuccess(t *testing.T) {
	clock := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	advance := func() time.Time {
		out := clock
		clock = clock.Add(time.Minute)
		return out
	}
	lc, mgr := newProbeLifecycle(t, advance)
	ctx := context.Background()
	if _, err := mgr.ProvisionKey(ctx, tenantkms.AliasFor("acme")); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	if err := lc.ProbeAvailability(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("first probe: %v", err)
	}
	first, _ := lc.LastProbeSuccess("acme")
	if err := lc.ProbeAvailability(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("second probe: %v", err)
	}
	second, _ := lc.LastProbeSuccess("acme")
	if !second.After(first) {
		t.Errorf("second LastProbeSuccess = %s must advance past first = %s", second, first)
	}
}

func TestForgetProbeSuccessClearsTimestamp(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	lc, mgr := newProbeLifecycle(t, func() time.Time { return now })
	ctx := context.Background()
	if _, err := mgr.ProvisionKey(ctx, tenantkms.AliasFor("acme")); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	if err := lc.ProbeAvailability(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("ProbeAvailability: %v", err)
	}
	if _, ok := lc.LastProbeSuccess("acme"); !ok {
		t.Fatal("LastProbeSuccess missing")
	}
	lc.ForgetProbeSuccess("acme")
	if _, ok := lc.LastProbeSuccess("acme"); ok {
		t.Error("LastProbeSuccess survived ForgetProbeSuccess")
	}
}

func TestLocalManagerProbeRoundTrip(t *testing.T) {
	seed := bytes.Repeat([]byte{0x12}, kms.DEKSize)
	local, _ := kms.NewLocal(seed)
	mgr := tenantkms.NewLocalManager(local)
	ctx := context.Background()
	if _, err := mgr.ProvisionKey(ctx, "tenant:acme"); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	if err := mgr.Probe(ctx, "tenant:acme"); err != nil {
		t.Errorf("Probe active key: %v", err)
	}
}

func TestLocalManagerProbeRejectsDestroyedKey(t *testing.T) {
	seed := bytes.Repeat([]byte{0x12}, kms.DEKSize)
	local, _ := kms.NewLocal(seed)
	mgr := tenantkms.NewLocalManager(local)
	ctx := context.Background()
	if _, err := mgr.ProvisionKey(ctx, "tenant:acme"); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	if _, err := mgr.DestroyKey(ctx, "tenant:acme"); err != nil {
		t.Fatalf("DestroyKey: %v", err)
	}
	if err := mgr.Probe(ctx, "tenant:acme"); !errors.Is(err, tenantkms.ErrKeyUnavailable) {
		t.Errorf("Probe destroyed key: %v, want ErrKeyUnavailable", err)
	}
}

func TestLocalManagerProbeUnknownAlias(t *testing.T) {
	seed := bytes.Repeat([]byte{0x12}, kms.DEKSize)
	local, _ := kms.NewLocal(seed)
	mgr := tenantkms.NewLocalManager(local)
	if err := mgr.Probe(context.Background(), "tenant:bob"); !errors.Is(err, tenantkms.ErrKeyNotFound) {
		t.Errorf("Probe unknown alias: %v, want ErrKeyNotFound", err)
	}
}
