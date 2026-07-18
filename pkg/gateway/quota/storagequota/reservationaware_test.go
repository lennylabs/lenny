// SPDX-License-Identifier: MIT

package storagequota_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
)

// constSource returns a fixed byte total, standing in for a Postgres-derived
// sum (SumLiveBytes or SumOutstandingReservations).
func constSource(v int64) storagequota.LiveBytesSource {
	return func(context.Context, string) (int64, error) { return v, nil }
}

// errSource returns a fixed error, standing in for a Postgres read fault on
// one of the two component sums.
func errSource(err error) storagequota.LiveBytesSource {
	return func(context.Context, string) (int64, error) { return 0, err }
}

// spec: §11.2 reservation-aware rebuild — the composed seam adds outstanding
// checkpoint reservations to the durable artifact_store byte sum.
func TestReservationAwareLiveBytesFoldsReservations_spec_11_2(t *testing.T) {
	seam := storagequota.ReservationAwareLiveBytes(constSource(400), constSource(500))
	got, err := seam(context.Background(), "acme")
	if err != nil {
		t.Fatalf("composed seam: %v", err)
	}
	if got != 900 {
		t.Errorf("composed sum = %d, want 900 (400 live + 500 reserved)", got)
	}
}

// A nil component degrades the composed seam to the other alone rather than
// panicking, so a deployment without one source keeps a working seam.
func TestReservationAwareLiveBytesNilComponentDegrades(t *testing.T) {
	liveOnly := storagequota.ReservationAwareLiveBytes(constSource(400), nil)
	if got, err := liveOnly(context.Background(), "acme"); err != nil || got != 400 {
		t.Fatalf("nil reservations: got (%d, %v), want (400, nil)", got, err)
	}
	reservedOnly := storagequota.ReservationAwareLiveBytes(nil, constSource(500))
	if got, err := reservedOnly(context.Background(), "acme"); err != nil || got != 500 {
		t.Fatalf("nil liveBytes: got (%d, %v), want (500, nil)", got, err)
	}
	if storagequota.ReservationAwareLiveBytes(nil, nil) != nil {
		t.Error("both-nil composition should yield a nil seam")
	}
}

// A read fault on either component surfaces so the §12.4 enforcement read
// fails closed rather than under-counting a tenant's usage.
func TestReservationAwareLiveBytesSurfacesComponentError(t *testing.T) {
	liveErr := errors.New("postgres: live down")
	if _, err := storagequota.ReservationAwareLiveBytes(errSource(liveErr), constSource(500))(context.Background(), "acme"); !errors.Is(err, liveErr) {
		t.Errorf("live-bytes fault err = %v, want wrapped %v", err, liveErr)
	}
	resErr := errors.New("postgres: reservations down")
	if _, err := storagequota.ReservationAwareLiveBytes(constSource(400), errSource(resErr))(context.Background(), "acme"); !errors.Is(err, resErr) {
		t.Errorf("reservations fault err = %v, want wrapped %v", err, resErr)
	}
}

// spec: §12.4 line 222 — during a Redis outage the storage-quota pre-check
// reads the reservation-aware Postgres-derived total (live artifact bytes plus
// outstanding checkpoint reservations), so a tenant holding checkpoint
// reservations does not recover that reserved headroom invisibly while Redis
// is down. Regression: with the reservation-free seam this Reserve would admit
// an over-quota write (400 live + 200 incoming = 600 ≤ 1000), failing the
// quota gate open; folding the 500 reserved bytes pushes committed-plus-
// reserved-plus-incoming to 1100 > 1000 and the write is rejected.
func TestFailoverReserveCountsOutstandingReservationsDuringOutage_spec_12_4_222(t *testing.T) {
	const (
		live     = int64(400)
		reserved = int64(500)
		incoming = int64(200)
		limit    = int64(1000)
	)

	// The reservation-free seam the pre-fix wiring passed admits the write:
	// it never sees the outstanding reservation, so the quota gate fails open.
	liveOnly := storagequota.NewFailover(&fakePrimary{reserveErr: errRedisDown}, constSource(live), nil)
	if _, err := liveOnly.Reserve(context.Background(), "acme", incoming, limit); err != nil {
		t.Fatalf("reservation-free seam unexpectedly rejected (setup guard): %v", err)
	}

	// The reservation-aware seam folds the outstanding reservation into the
	// during-outage total and rejects the same over-quota write.
	seam := storagequota.ReservationAwareLiveBytes(constSource(live), constSource(reserved))
	f := storagequota.NewFailover(&fakePrimary{reserveErr: errRedisDown}, seam, nil)
	prior, err := f.Reserve(context.Background(), "acme", incoming, limit)
	if !errors.Is(err, storagequota.ErrQuotaExceeded) {
		t.Fatalf("Reserve err = %v, want ErrQuotaExceeded", err)
	}
	if prior != live+reserved {
		t.Errorf("priorUsed = %d, want %d (live + outstanding reservations)", prior, live+reserved)
	}
}

// spec: §11.2 reservation-aware rebuild — the absolute counter rebuild sets a
// tenant's counter to live artifact bytes plus outstanding reservations, so
// the rebuilt counter holds every unreleased reservation the guarded relative
// Adjust will later release. Regression: a reservation-free rebuild would
// leave the counter below the reserved total, after which a release Adjust
// removes bytes belonging to the tenant's other live artifacts.
func TestRehydrateFoldsOutstandingReservations_spec_11_2(t *testing.T) {
	c := storagequota.NewMemory()
	seam := storagequota.ReservationAwareLiveBytes(constSource(400), constSource(500))
	if err := storagequota.Rehydrate(context.Background(), c, []string{"acme"}, seam); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	got, err := c.Used(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Used: %v", err)
	}
	if got != 900 {
		t.Errorf("rebuilt counter = %d, want 900 (400 live + 500 reserved)", got)
	}
}
