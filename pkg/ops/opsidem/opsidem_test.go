// SPDX-License-Identifier: MIT

package opsidem_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/opsidem"
)

// callerFromHeader extracts caller_id from a dev header for the tests.
func callerFromHeader(r *http.Request) string {
	if v := r.Header.Get("X-Lenny-Caller"); v != "" {
		return v
	}
	return "operator"
}

// countingHandler records how many times it executed and returns a
// 201 with a body so replay can be distinguished from re-execution.
type countingHandler struct{ n int }

func (h *countingHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.n++
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"execution":%d}`, h.n)
}

func newMW(t *testing.T, store opsidem.Store, production bool) *opsidem.Middleware {
	t.Helper()
	return opsidem.New(store, opsidem.Config{CallerID: callerFromHeader, Production: production})
}

func do(h http.Handler, method, path, key, caller, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(opsidem.HeaderName, key)
	}
	if caller != "" {
		req.Header.Set("X-Lenny-Caller", caller)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// spec: §25.4 lines 2031-2035 — required-key endpoints reject a missing
// key with 400 IDEMPOTENCY_KEY_REQUIRED at Tier 2/3.
func TestRequiredKeyMissingReturns400_spec_25_4(t *testing.T) {
	inner := &countingHandler{}
	mw := newMW(t, opsidem.NewMemoryStore(), true).Wrap(inner)
	rec := do(mw, http.MethodPost, "/v1/admin/restore/execute", "", "alice", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "IDEMPOTENCY_KEY_REQUIRED") {
		t.Errorf("body missing IDEMPOTENCY_KEY_REQUIRED: %s", rec.Body.String())
	}
	if inner.n != 0 {
		t.Errorf("inner executed %d times on a rejected request, want 0", inner.n)
	}
}

// At Tier 1 (dev), a required-key endpoint allows a missing key so
// interactive testing is not blocked.
func TestRequiredKeyMissingDevPassesThrough_spec_25_4(t *testing.T) {
	inner := &countingHandler{}
	mw := newMW(t, opsidem.NewMemoryStore(), false).Wrap(inner)
	rec := do(mw, http.MethodPost, "/v1/admin/restore/execute", "", "alice", `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (dev pass-through)", rec.Code)
	}
	if inner.n != 1 {
		t.Errorf("inner executed %d times, want 1", inner.n)
	}
}

// A full backup requires the key (Tier 2/3); an incremental does not.
func TestBackupFullRequiresKey_spec_25_4(t *testing.T) {
	inner := &countingHandler{}
	mw := newMW(t, opsidem.NewMemoryStore(), true).Wrap(inner)
	full := do(mw, http.MethodPost, "/v1/admin/backups", "", "alice", `{"type":"full"}`)
	if full.Code != http.StatusBadRequest {
		t.Errorf("full backup without key: status = %d, want 400", full.Code)
	}
	inc := do(mw, http.MethodPost, "/v1/admin/backups", "", "alice", `{"type":"incremental"}`)
	if inc.Code != http.StatusCreated {
		t.Errorf("incremental backup without key: status = %d, want 201 (optional)", inc.Code)
	}
}

// spec: §25.4 lines 2013-2014 — a completed key replays the stored
// response without re-executing.
func TestReplayDoesNotReExecute_spec_25_4(t *testing.T) {
	inner := &countingHandler{}
	mw := newMW(t, opsidem.NewMemoryStore(), true).Wrap(inner)
	first := do(mw, http.MethodPost, "/v1/admin/backups", "k1", "alice", `{"type":"full"}`)
	if first.Code != http.StatusCreated || inner.n != 1 {
		t.Fatalf("first: status=%d n=%d, want 201/1", first.Code, inner.n)
	}
	second := do(mw, http.MethodPost, "/v1/admin/backups", "k1", "alice", `{"type":"full"}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want 201", second.Code)
	}
	if inner.n != 1 {
		t.Errorf("inner re-executed on replay (n=%d), want 1", inner.n)
	}
	if second.Header().Get("X-Lenny-Idempotent-Replay") != "true" {
		t.Errorf("replay missing X-Lenny-Idempotent-Replay header")
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("replay body %q != original %q", second.Body.String(), first.Body.String())
	}
}

// spec: §25.4 line 2015 — a different caller with the same key gets
// independent behavior (the PK is (key, caller_id)).
func TestCallerScopingIsIndependent_spec_25_4(t *testing.T) {
	inner := &countingHandler{}
	mw := newMW(t, opsidem.NewMemoryStore(), true).Wrap(inner)
	do(mw, http.MethodPost, "/v1/admin/backups", "shared", "alice", `{"type":"full"}`)
	// bob's same key collides with alice's live row -> owned-by-other (403).
	rec := do(mw, http.MethodPost, "/v1/admin/backups", "shared", "bob", `{"type":"full"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bob status = %d, want 403 OWNED_BY_OTHER; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER") {
		t.Errorf("body missing OWNED_BY_OTHER code: %s", rec.Body.String())
	}
}

// spec: §25.4 line 2013 (status='in_progress') — a concurrent in-flight
// claim returns 409 OPERATION_IN_PROGRESS with an elapsed detail.
func TestInProgressReturns409_spec_25_4(t *testing.T) {
	store := opsidem.NewMemoryStore()
	// Pre-claim the key as in-progress and never complete it.
	if _, res, _ := store.Claim(context.Background(), "k2", "alice", "POST /v1/admin/backups", time.Hour, time.Now()); res != opsidem.ClaimInserted {
		t.Fatalf("pre-claim result = %v, want inserted", res)
	}
	inner := &countingHandler{}
	mw := newMW(t, store, true).Wrap(inner)
	rec := do(mw, http.MethodPost, "/v1/admin/backups", "k2", "alice", `{"type":"full"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "OPERATION_IN_PROGRESS") || !strings.Contains(rec.Body.String(), "elapsed") {
		t.Errorf("body missing OPERATION_IN_PROGRESS/elapsed: %s", rec.Body.String())
	}
	if inner.n != 0 {
		t.Errorf("inner executed during in-progress, want 0")
	}
}

// outageStore returns ErrStoreUnavailable from Claim.
type outageStore struct{ opsidem.Store }

func (outageStore) Claim(context.Context, string, string, string, time.Duration, time.Time) (opsidem.Record, opsidem.ClaimResult, error) {
	return opsidem.Record{}, 0, opsidem.ErrStoreUnavailable
}

// spec: §25.4 lines 2042-2058 — required endpoints return 503
// IDEMPOTENCY_STORE_UNAVAILABLE during a store outage.
func TestStoreOutageRequiredFailsClosed_spec_25_4(t *testing.T) {
	inner := &countingHandler{}
	mw := newMW(t, outageStore{}, true).Wrap(inner)
	rec := do(mw, http.MethodPost, "/v1/admin/restore/execute", "k3", "alice", `{}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "IDEMPOTENCY_STORE_UNAVAILABLE") {
		t.Errorf("body missing IDEMPOTENCY_STORE_UNAVAILABLE: %s", rec.Body.String())
	}
	if inner.n != 0 {
		t.Errorf("inner executed during required-endpoint outage, want 0")
	}
}

// spec: §25.4 line 2057 — optional endpoints proceed during an
// outage but the response carries a degradation warning.
func TestStoreOutageOptionalProceedsWithDegradation_spec_25_4(t *testing.T) {
	inner := &countingHandler{}
	mw := newMW(t, outageStore{}, true).Wrap(inner)
	rec := do(mw, http.MethodPost, "/v1/admin/pools/p/warm-count", "k4", "alice", `{"minWarm":5}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (optional proceeds)", rec.Code)
	}
	if inner.n != 1 {
		t.Errorf("inner executed %d times, want 1", inner.n)
	}
	if rec.Header().Get("X-Lenny-Idempotency-Degraded") != "true" {
		t.Errorf("missing X-Lenny-Idempotency-Degraded header")
	}
	if !strings.Contains(rec.Body.String(), "degradation") || !strings.Contains(rec.Body.String(), "retry-safety") {
		t.Errorf("body missing degradation warning: %s", rec.Body.String())
	}
}

// A 5xx response is not cached: a retry re-executes rather than replaying
// the error for the TTL window.
func TestServerErrorNotCached_spec_25_4(t *testing.T) {
	var calls int
	failing := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "boom", http.StatusBadGateway)
	})
	mw := newMW(t, opsidem.NewMemoryStore(), true).Wrap(failing)
	do(mw, http.MethodPost, "/v1/admin/backups", "k5", "alice", `{"type":"full"}`)
	rec := do(mw, http.MethodPost, "/v1/admin/backups", "k5", "alice", `{"type":"full"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("retry status = %d, want 502 (re-executed)", rec.Code)
	}
	if calls != 2 {
		t.Errorf("handler called %d times, want 2 (5xx not cached)", calls)
	}
}

// A GET carrying an Idempotency-Key passes through untouched (only
// POST/PUT mutate).
func TestNonMutatingMethodPassesThrough_spec_25_4(t *testing.T) {
	inner := &countingHandler{}
	mw := newMW(t, opsidem.NewMemoryStore(), true).Wrap(inner)
	rec := do(mw, http.MethodGet, "/v1/admin/backups", "k6", "alice", "")
	if rec.Code != http.StatusCreated || inner.n != 1 {
		t.Fatalf("GET passthrough: status=%d n=%d", rec.Code, inner.n)
	}
}

// Long-running endpoints are classified into the 7d TTL class; the
// classification is exercised indirectly by confirming a long-running
// claim outlives the standard window. Here we assert the MemoryStore
// honors the longer TTL the middleware selects for restore/execute.
func TestLongRunningTTLOutlivesStandard_spec_25_4(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := opsidem.NewMemoryStore()
	mw := opsidem.New(store, opsidem.Config{
		CallerID:       callerFromHeader,
		Production:     true,
		StandardTTL:    time.Hour,
		LongRunningTTL: 48 * time.Hour,
		Now:            func() time.Time { return now },
	}).Wrap(&countingHandler{})
	do(mw, http.MethodPost, "/v1/admin/restore/execute", "lr", "alice", `{}`)
	// 24h later the long-running key is still live (replays), so a fresh
	// execution does not occur.
	rec, res, err := store.Claim(context.Background(), "lr", "alice", "x", time.Hour, now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if res != opsidem.ClaimReplay && res != opsidem.ClaimInProgress {
		t.Errorf("after 24h, long-running key result = %v, want still-live (replay/in-progress); rec=%+v", res, rec)
	}
}

func TestErrStoreUnavailableIsSentinel(t *testing.T) {
	if !errors.Is(opsidem.ErrStoreUnavailable, opsidem.ErrStoreUnavailable) {
		t.Fatal("sentinel identity broken")
	}
}
