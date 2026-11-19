// SPDX-License-Identifier: MIT

package quotaerasure

import (
	"context"
	"errors"
	"testing"
)

// fakeEraser records the calls it received and returns a fixed count/error.
type fakeEraser struct {
	count    int
	err      error
	userHits int
	tenHits  int
}

func (f *fakeEraser) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	f.userHits++
	return f.count, f.err
}

func (f *fakeEraser) DeleteByTenant(_ context.Context, _ string) (int, error) {
	f.tenHits++
	return f.count, f.err
}

// spec: §12.8 step 6 — the composite fans the erasure out to every wired
// backend and sums the deleted-row counts.
func TestComposite_SumsAcrossBackends_spec_12_8_step6(t *testing.T) {
	redis := &fakeEraser{count: 2}
	pg := &fakeEraser{count: 3}
	accum := &fakeEraser{count: 1}
	c := New(
		Backend{Name: "redis_counter", User: redis, Tenant: redis},
		Backend{Name: "postgres_checkpoint", User: pg, Tenant: pg},
		Backend{Name: "failopen_accumulator", User: accum, Tenant: accum},
	)

	got, err := c.DeleteByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if got != 6 {
		t.Errorf("DeleteByUser sum = %d, want 6", got)
	}
	if redis.userHits != 1 || pg.userHits != 1 || accum.userHits != 1 {
		t.Errorf("each backend should be hit once: redis=%d pg=%d accum=%d", redis.userHits, pg.userHits, accum.userHits)
	}

	got, err = c.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if got != 6 {
		t.Errorf("DeleteByTenant sum = %d, want 6", got)
	}
}

// spec: §12.8 step 6 — every backend is attempted even when an earlier one
// errors (each is idempotent and retried), and the first error surfaces
// with the partial count.
func TestComposite_ContinuesPastErrorAndReturnsFirst(t *testing.T) {
	sentinel := errors.New("boom")
	redis := &fakeEraser{count: 2, err: sentinel}
	pg := &fakeEraser{count: 3}
	c := New(
		Backend{Name: "redis_counter", User: redis, Tenant: redis},
		Backend{Name: "postgres_checkpoint", User: pg, Tenant: pg},
	)

	got, err := c.DeleteByUser(context.Background(), "acme", "alice")
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want wrapped sentinel", err)
	}
	// pg still ran despite redis erroring; partial count includes both.
	if got != 5 {
		t.Errorf("partial count = %d, want 5", got)
	}
	if pg.userHits != 1 {
		t.Errorf("postgres backend should run despite earlier error, hits=%d", pg.userHits)
	}
}

// New drops a backend exposing neither eraser so a no-Redis / no-Postgres
// posture carries no dead entry.
func TestNew_DropsEmptyBackends(t *testing.T) {
	pg := &fakeEraser{count: 4}
	c := New(
		Backend{Name: "empty"},
		Backend{Name: "postgres_checkpoint", User: pg, Tenant: pg},
	)
	if len(c.backends) != 1 {
		t.Fatalf("kept %d backends, want 1", len(c.backends))
	}
	got, _ := c.DeleteByUser(context.Background(), "acme", "alice")
	if got != 4 {
		t.Errorf("DeleteByUser = %d, want 4", got)
	}
}

// A backend that exposes only one direction is skipped on the other.
func TestComposite_SkipsBackendMissingDirection(t *testing.T) {
	userOnly := &fakeEraser{count: 7}
	c := New(Backend{Name: "user_only", User: userOnly}) // Tenant is nil

	if got, _ := c.DeleteByUser(context.Background(), "acme", "alice"); got != 7 {
		t.Errorf("DeleteByUser = %d, want 7", got)
	}
	if got, _ := c.DeleteByTenant(context.Background(), "acme"); got != 0 {
		t.Errorf("DeleteByTenant = %d, want 0 (no tenant eraser wired)", got)
	}
	if userOnly.tenHits != 0 {
		t.Errorf("tenant eraser should not be called, hits=%d", userOnly.tenHits)
	}
}
