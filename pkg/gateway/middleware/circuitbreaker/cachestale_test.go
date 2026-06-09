// SPDX-License-Identifier: MIT

package circuitbreaker_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cb "github.com/lennylabs/lenny/pkg/circuitbreaker"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
)

// staleHarness wires a breaker middleware with a controllable cache age
// and a recording appender/metrics so a test can assert the §16.7
// `admission.circuit_breaker_cache_stale` serve behavior.
func staleHarness(t *testing.T, open []cb.Breaker, age time.Duration) (http.Handler, *fakeAppender, *fakeMetrics) {
	t.Helper()
	reg := cbmw.NewMemoryRegistry()
	reg.Set(open)
	app := &fakeAppender{}
	met := &fakeMetrics{}
	rep := cbmw.NewAuditReporter(app, met, "replica-1", func() time.Time { return time.Now().UTC() })
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := cbmw.Wrap(inner, reg, cbmw.Options{
		Audit:    rep,
		CacheAge: func() time.Duration { return age },
		Extract:  func(_ *http.Request) cb.Request { return cb.Request{OperationType: cb.OpSessionCreation} },
		Snapshot: func(_ *http.Request) cbmw.RejectionSnapshot {
			return cbmw.RejectionSnapshot{CallerSub: "alice", CallerTenantID: "acme"}
		},
	})
	return h, app, met
}

func cacheStaleRows(app *fakeAppender) []capturedRow {
	var out []capturedRow
	for _, r := range app.rows {
		if r.eventType == cbmw.EventAdmissionCircuitBreakerCacheStale {
			out = append(out, r)
		}
	}
	return out
}

// spec: §16.7 line 679 — a fresh cache (age within the 5s budget) serves
// no cache-stale audit event.
func TestCacheStaleFreshCacheNoEvent_spec_16_7(t *testing.T) {
	h, app, met := staleHarness(t, nil, 1*time.Second)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if got := len(cacheStaleRows(app)); got != 0 {
		t.Errorf("cache-stale rows = %d, want 0 for a fresh cache", got)
	}
	if met.staleServes["admitted"] != 0 {
		t.Errorf("stale-serve counter incremented for a fresh cache")
	}
}

// spec: §16.7 line 679 — a decision admitted against a stale cache emits
// the security-salient outcome="admitted" cache-stale event and still
// admits the request (the cache could not verify a breaker, so the
// request was not blocked).
func TestCacheStaleAdmittedServeEmits_spec_16_7(t *testing.T) {
	h, app, met := staleHarness(t, nil, 30*time.Second)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (admitted)", rec.Code)
	}
	rows := cacheStaleRows(app)
	if len(rows) != 1 {
		t.Fatalf("cache-stale rows = %d, want 1", len(rows))
	}
	if rows[0].payload["outcome"] != "admitted" {
		t.Errorf("outcome = %v, want admitted", rows[0].payload["outcome"])
	}
	if rows[0].payload["replica_service_instance_id"] != "replica-1" {
		t.Errorf("missing replica id on cache-stale row")
	}
	if met.staleServes["admitted"] != 1 {
		t.Errorf("admitted stale-serve counter = %d, want 1", met.staleServes["admitted"])
	}
}

// spec: §16.7 — a decision rejected against a stale cache emits the
// cache-stale event with outcome="rejected" alongside the rejection event.
func TestCacheStaleRejectedServeEmits_spec_16_7(t *testing.T) {
	h, app, met := staleHarness(t, []cb.Breaker{openBreaker()}, 30*time.Second)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (rejected)", rec.Code)
	}
	rows := cacheStaleRows(app)
	if len(rows) != 1 || rows[0].payload["outcome"] != "rejected" {
		t.Fatalf("cache-stale rows = %+v, want one outcome=rejected", rows)
	}
	if met.staleServes["rejected"] != 1 {
		t.Errorf("rejected stale-serve counter = %d, want 1", met.staleServes["rejected"])
	}
}

// spec: §11.6 — the cache-stale event is sampled per (replica, outcome):
// a storm of stale serves produces one audit row per window but counts
// every serve.
func TestCacheStaleServeIsSampled_spec_11_6(t *testing.T) {
	h, app, met := staleHarness(t, nil, 30*time.Second)
	for i := 0; i < 5; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	}
	if got := len(cacheStaleRows(app)); got != 1 {
		t.Errorf("cache-stale rows = %d, want 1 (sampled)", got)
	}
	if met.staleServes["admitted"] != 5 {
		t.Errorf("admitted stale-serve counter = %d, want 5 (every serve counted)", met.staleServes["admitted"])
	}
}

// spec: §16.7 — with no CacheAge source (the in-memory non-Redis path)
// stale-serve detection is off and no event is ever emitted.
func TestCacheStaleDisabledWithoutCacheAge_spec_16_7(t *testing.T) {
	reg := cbmw.NewMemoryRegistry()
	app := &fakeAppender{}
	rep := cbmw.NewAuditReporter(app, &fakeMetrics{}, "replica-1", nil)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := cbmw.Wrap(inner, reg, cbmw.Options{Audit: rep}) // no CacheAge
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if got := len(cacheStaleRows(app)); got != 0 {
		t.Errorf("cache-stale rows = %d, want 0 when CacheAge is nil", got)
	}
}
