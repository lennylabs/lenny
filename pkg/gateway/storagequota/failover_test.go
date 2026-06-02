// SPDX-License-Identifier: MIT

package storagequota_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
)

// errRedisDown stands in for the infrastructure error a Redis-backed
// counter returns during an outage (a connection error, a closed client,
// a timeout) — anything that is not a storage-quota domain outcome.
var errRedisDown = errors.New("redis: connection refused")

// fakePrimary is a configurable storagequota.Counter standing in for the
// Redis primary. Each method returns its programmed error so a test can
// drive the §12.4 line 210 failover branches deterministically.
type fakePrimary struct {
	reserveErr error
	reservePr  int64
	usedVal    int64
	usedErr    error
	adjustErr  error
	setErr     error
	setCalls   []setCall
}

type setCall struct {
	tenant string
	value  int64
}

func (f *fakePrimary) Reserve(_ context.Context, _ string, _, _ int64) (int64, error) {
	return f.reservePr, f.reserveErr
}
func (f *fakePrimary) Adjust(_ context.Context, _ string, _ int64) error { return f.adjustErr }
func (f *fakePrimary) Used(_ context.Context, _ string) (int64, error)   { return f.usedVal, f.usedErr }
func (f *fakePrimary) Set(_ context.Context, tenantID string, value int64) error {
	f.setCalls = append(f.setCalls, setCall{tenant: tenantID, value: value})
	return f.setErr
}

// spec: §12.4 line 210 — a healthy Redis reserve is forwarded unchanged
// and never consults the Postgres fallback.
func TestFailoverReserveForwardsPrimarySuccess_spec_12_4_210(t *testing.T) {
	fallbackCalls := 0
	sizeOf := func(context.Context, string) (int64, error) {
		t.Fatal("sizeOf must not be called when the primary succeeds")
		return 0, nil
	}
	f := storagequota.NewFailover(&fakePrimary{reservePr: 42}, sizeOf, func() { fallbackCalls++ })
	prior, err := f.Reserve(context.Background(), "acme", 10, 1000)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if prior != 42 {
		t.Errorf("priorUsed = %d, want 42 (the primary's value)", prior)
	}
	if fallbackCalls != 0 {
		t.Errorf("onFallback fired %d times on a primary success, want 0", fallbackCalls)
	}
}

// spec: §12.4 line 210 — ErrQuotaExceeded is a domain outcome forwarded
// without consulting the fallback (a healthy Redis reject, not an outage).
func TestFailoverReserveForwardsQuotaExceeded_spec_12_4_210(t *testing.T) {
	fallbackCalls := 0
	f := storagequota.NewFailover(
		&fakePrimary{reserveErr: storagequota.ErrQuotaExceeded, reservePr: 900},
		func(context.Context, string) (int64, error) {
			t.Fatal("sizeOf must not be called on a domain ErrQuotaExceeded")
			return 0, nil
		},
		func() { fallbackCalls++ },
	)
	prior, err := f.Reserve(context.Background(), "acme", 200, 1000)
	if !errors.Is(err, storagequota.ErrQuotaExceeded) {
		t.Fatalf("Reserve err = %v, want ErrQuotaExceeded", err)
	}
	if prior != 900 {
		t.Errorf("priorUsed = %d, want 900", prior)
	}
	if fallbackCalls != 0 {
		t.Errorf("onFallback fired on a domain reject, want 0")
	}
}

// spec: §12.4 line 210 — on a Redis outage the pre-check reads the
// Postgres SUM(artifact_size_bytes) and admits when sum+incoming fits.
func TestFailoverReserveAdmitsAgainstPostgresSum_spec_12_4_210(t *testing.T) {
	fallbackCalls := 0
	sizeOf := func(context.Context, string) (int64, error) { return 600, nil }
	f := storagequota.NewFailover(&fakePrimary{reserveErr: errRedisDown}, sizeOf, func() { fallbackCalls++ })
	prior, err := f.Reserve(context.Background(), "acme", 300, 1000) // 600+300 ≤ 1000
	if err != nil {
		t.Fatalf("Reserve via fallback: %v", err)
	}
	if prior != 600 {
		t.Errorf("priorUsed = %d, want 600 (the Postgres sum)", prior)
	}
	if fallbackCalls != 1 {
		t.Errorf("onFallback fired %d times, want 1", fallbackCalls)
	}
}

// spec: §12.4 line 210 — the Postgres-derived pre-check still rejects an
// over-quota upload during the outage window.
func TestFailoverReserveRejectsAgainstPostgresSum_spec_12_4_210(t *testing.T) {
	sizeOf := func(context.Context, string) (int64, error) { return 800, nil }
	f := storagequota.NewFailover(&fakePrimary{reserveErr: errRedisDown}, sizeOf, nil)
	prior, err := f.Reserve(context.Background(), "acme", 300, 1000) // 800+300 > 1000
	if !errors.Is(err, storagequota.ErrQuotaExceeded) {
		t.Fatalf("Reserve err = %v, want ErrQuotaExceeded", err)
	}
	if prior != 800 {
		t.Errorf("priorUsed = %d, want 800", prior)
	}
}

// spec: §12.4 line 210 — dual-store outage (Redis down AND Postgres down)
// fails closed with ErrUnavailable so the upload handler returns 503.
func TestFailoverReserveDualOutageFailsClosed_spec_12_4_210(t *testing.T) {
	sizeOf := func(context.Context, string) (int64, error) { return 0, errors.New("postgres: down") }
	f := storagequota.NewFailover(&fakePrimary{reserveErr: errRedisDown}, sizeOf, nil)
	_, err := f.Reserve(context.Background(), "acme", 300, 1000)
	if !errors.Is(err, storagequota.ErrUnavailable) {
		t.Fatalf("dual-store outage err = %v, want ErrUnavailable", err)
	}
}

// A nil sizeOf disables the fallback so the deployment without a Postgres
// catalog keeps the prior fail-on-Redis-error behavior (the raw infra
// error surfaces, not ErrUnavailable).
func TestFailoverReserveNilSizeOfSurfacesPrimaryError(t *testing.T) {
	f := storagequota.NewFailover(&fakePrimary{reserveErr: errRedisDown}, nil, nil)
	_, err := f.Reserve(context.Background(), "acme", 300, 1000)
	if !errors.Is(err, errRedisDown) {
		t.Fatalf("err = %v, want the raw primary error when sizeOf is nil", err)
	}
}

// spec: §12.4 line 210 — Used reads the Postgres sum during the outage.
func TestFailoverUsedFallsBackToPostgresSum_spec_12_4_210(t *testing.T) {
	sizeOf := func(context.Context, string) (int64, error) { return 777, nil }
	f := storagequota.NewFailover(&fakePrimary{usedErr: errRedisDown}, sizeOf, nil)
	v, err := f.Used(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Used via fallback: %v", err)
	}
	if v != 777 {
		t.Errorf("Used = %d, want 777", v)
	}
}

func TestFailoverUsedDualOutageFailsClosed_spec_12_4_210(t *testing.T) {
	sizeOf := func(context.Context, string) (int64, error) { return 0, errors.New("down") }
	f := storagequota.NewFailover(&fakePrimary{usedErr: errRedisDown}, sizeOf, nil)
	if _, err := f.Used(context.Background(), "acme"); !errors.Is(err, storagequota.ErrUnavailable) {
		t.Fatalf("Used dual outage err = %v, want ErrUnavailable", err)
	}
}

// Adjust swallows an infrastructure error: there is no reachable counter
// to shift during the outage, and the Postgres-derived sum already
// excludes the uncommitted bytes, so a dropped release does not over-count
// once Redis recovers.
func TestFailoverAdjustSwallowsInfraError(t *testing.T) {
	fallbackCalls := 0
	f := storagequota.NewFailover(&fakePrimary{adjustErr: errRedisDown}, nil, func() { fallbackCalls++ })
	if err := f.Adjust(context.Background(), "acme", -100); err != nil {
		t.Fatalf("Adjust must swallow the infra error, got %v", err)
	}
	if fallbackCalls != 1 {
		t.Errorf("onFallback fired %d times, want 1", fallbackCalls)
	}
}

func TestFailoverAdjustForwardsSuccess(t *testing.T) {
	fallbackCalls := 0
	f := storagequota.NewFailover(&fakePrimary{}, nil, func() { fallbackCalls++ })
	if err := f.Adjust(context.Background(), "acme", -100); err != nil {
		t.Fatalf("Adjust: %v", err)
	}
	if fallbackCalls != 0 {
		t.Errorf("onFallback fired on a successful Adjust, want 0")
	}
}

// spec: §12.4 line 210 — Set is the recovery write-back target; it
// delegates to the Redis primary and surfaces a still-down error.
func TestFailoverSetDelegatesToPrimary_spec_12_4_210(t *testing.T) {
	fp := &fakePrimary{}
	f := storagequota.NewFailover(fp, nil, nil)
	if err := f.Set(context.Background(), "acme", 555); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(fp.setCalls) != 1 || fp.setCalls[0].tenant != "acme" || fp.setCalls[0].value != 555 {
		t.Fatalf("Set did not delegate to the primary: %+v", fp.setCalls)
	}
	fp.setErr = errRedisDown
	if err := f.Set(context.Background(), "acme", 1); !errors.Is(err, errRedisDown) {
		t.Errorf("Set must surface a still-down Redis error, got %v", err)
	}
}
