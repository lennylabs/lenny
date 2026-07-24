// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// spec: §27.8 ("Metrics") — the playground metric-catalog rows for
// lenny_playground_page_views_total ("Playground index loads"),
// lenny_playground_session_revocations_total ("Incremented exactly once
// per DEL t:{tenant_id}:pg:sess:{session_id} performed by the
// revocation path"; reason ∈ {user_logout, idle_timeout, admin_revoke,
// oidc_session_ended, user_invalidated}),
// lenny_playground_dev_tenant_not_seeded_total ("/playground/*
// requests rejected with 503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED"),
// lenny_playground_sessions_created_total ("Sessions initiated from
// the playground"), lenny_playground_ws_connect_total ("MCP WebSocket
// connections opened from the playground"), and the
// lenny_playground_session_revocation_propagation_seconds histogram
// ("End-to-end propagation latency ... to when peer replicas observe
// it on their auth hot path").
//
// TestMetricCatalogMatchesSpec278 (metrics_catalog_test.go) pins the
// catalog's names, types, and label sets but drives every counter
// through NewMetrics, which currently fails registration on the
// unrelated §16.1.1 snake_case check on the "authMode" label (tracked
// in BUILD-GAPS.md §16.1 Metrics Finding 8) before any §27.8 metric
// registers, so that test skips. idle_sweep_test.go and
// user_invalidation_test.go each cover one revocation reason
// (idle_timeout, user_invalidated) by building the Metrics collectors
// directly, bypassing NewMetrics, and driving the real handler methods.
// These tests extend that pattern to the remaining reasons
// (user_logout, admin_revoke, oidc_session_ended) and the remaining
// counters/histogram, so every §27.8 row has increment coverage
// independent of the NewMetrics registration defect.

// newSpec278Metrics builds every §27.8 collector with the exact name,
// type, and label set NewMetrics uses, without routing through
// NewMetrics itself (see the file comment above for why). Each test
// gets its own collector set so counter deltas from one test never
// leak into another.
func newSpec278Metrics(t *testing.T) *Metrics {
	t.Helper()
	return &Metrics{
		pageViews: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_playground_page_views_total", Help: "test",
		}, []string{"authMode"}),
		sessionsCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_playground_sessions_created_total", Help: "test",
		}, []string{"runtime"}),
		wsConnect: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_playground_ws_connect_total", Help: "test",
		}, []string{"outcome"}),
		revocations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_playground_session_revocations_total", Help: "test",
		}, []string{"reason"}),
		revocationProp: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "lenny_playground_session_revocation_propagation_seconds", Help: "test",
		}, []string{"outcome"}),
		devTenantNotSeededC: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_playground_dev_tenant_not_seeded_total", Help: "test",
		}, nil).WithLabelValues(),
	}
}

// diagnosis: a failure means GET /playground/ (serveIndex, the real
// asset-serving runtime path) no longer increments
// lenny_playground_page_views_total for the serving auth mode.
func TestServeIndexIncrementsPageViews_spec_27_8(t *testing.T) {
	m := newSpec278Metrics(t)
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{Signer: devSigner(), Metrics: m})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/playground/")
	if err != nil {
		t.Fatalf("GET /playground/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := testutil.ToFloat64(m.pageViews.WithLabelValues(string(AuthModeOIDC))); got != 1 {
		t.Errorf("page_views_total{authMode=oidc} = %v, want 1", got)
	}

	// A second load (the SPA-fallback path for an unrecognized asset
	// path) increments the same series again.
	if _, err := http.Get(srv.URL + "/playground/some/client/route"); err != nil {
		t.Fatalf("GET /playground/some/client/route: %v", err)
	}
	if got := testutil.ToFloat64(m.pageViews.WithLabelValues(string(AuthModeOIDC))); got != 2 {
		t.Errorf("page_views_total{authMode=oidc} after fallback load = %v, want 2", got)
	}
}

// diagnosis: a failure means the §27.2 layer-4 Ready-gate's 503
// rejection path (readyGate) no longer increments
// lenny_playground_dev_tenant_not_seeded_total.
func TestReadyGateRejectionIncrementsDevTenantNotSeeded_spec_27_8(t *testing.T) {
	m := newSpec278Metrics(t)
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme"}, Options{
		Signer:  devSigner(),
		Tenants: fakeTenants{registered: map[string]bool{}}, // acme not seeded
		Metrics: m,
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/playground/")
	if err != nil {
		t.Fatalf("GET /playground/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got := testutil.ToFloat64(m.devTenantNotSeededC); got != 1 {
		t.Errorf("dev_tenant_not_seeded_total = %v, want 1", got)
	}
	// The index load never happened: the gate rejected before serveIndex.
	if got := testutil.ToFloat64(m.pageViews.WithLabelValues(string(AuthModeDev))); got != 0 {
		t.Errorf("page_views_total{authMode=dev} = %v, want 0 (blocked by the gate)", got)
	}

	// A second rejected request increments the same unlabelled counter
	// again, matching the spec's "absent label set" row.
	if _, err := http.Get(srv.URL + "/playground/"); err != nil {
		t.Fatalf("GET /playground/ (second): %v", err)
	}
	if got := testutil.ToFloat64(m.devTenantNotSeededC); got != 2 {
		t.Errorf("dev_tenant_not_seeded_total after second rejection = %v, want 2", got)
	}
}

// diagnosis: a failure means POST /playground/auth/logout (the real
// OIDC logout runtime path, driven end to end through login → mint →
// logout) no longer attributes its revocation to
// lenny_playground_session_revocations_total{reason="user_logout"}.
func TestHandleLogoutIncrementsUserLogoutRevocation_spec_27_8(t *testing.T) {
	m := newSpec278Metrics(t)
	store := NewMemorySessionStore()
	oidc := &fakeOIDC{subject: OIDCSubject{UserID: "alice", TenantID: "acme", Scope: "tools:sessions:read"}}
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC, OIDCSessionTTL: time.Hour, BearerTTL: 900 * time.Second}, Options{
		Signer:   devSigner(),
		Sessions: store,
		OIDC:     oidc,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
		Metrics:  m,
	})
	pgSrv := httptest.NewServer(h.PlaygroundRoutes())
	defer pgSrv.Close()
	tokenSrv := httptest.NewServer(h.TokenRoutes())
	defer tokenSrv.Close()

	cookie := completeOIDCLogin(t, h, pgSrv, oidc)
	_ = mintWithCookie(t, tokenSrv, cookie)

	if got := testutil.ToFloat64(m.revocations.WithLabelValues(string(RevokeUserLogout))); got != 0 {
		t.Fatalf("revocations{reason=user_logout} before logout = %v, want 0", got)
	}

	logoutReq, _ := http.NewRequest(http.MethodPost, pgSrv.URL+"/playground/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	logoutResp, err := http.DefaultClient.Do(logoutReq)
	if err != nil {
		t.Fatalf("POST logout: %v", err)
	}
	defer logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", logoutResp.StatusCode)
	}

	if got := testutil.ToFloat64(m.revocations.WithLabelValues(string(RevokeUserLogout))); got != 1 {
		t.Errorf("revocations{reason=user_logout} = %v, want 1", got)
	}
}

// TestRevokeSessionAdminAndOIDCSessionEndedIncrementReasons drives
// Handler.RevokeSession — "the §27.6 admin/idle-timeout entry point
// into the revocation primitive" per its doc comment, and the same
// entry point idle_sweep_test.go drives for RevokeIdleTimeout — for
// the two remaining §27.8 reasons, admin_revoke and
// oidc_session_ended.
//
// diagnosis: a failure means the shared revocation primitive
// (revokeSessionRecord) no longer attributes an admin- or
// OIDC-session-end-triggered revocation to its §27.8 reason label.
func TestRevokeSessionAdminAndOIDCSessionEndedIncrementReasons_spec_27_8(t *testing.T) {
	store := NewMemorySessionStore()
	m := newSpec278Metrics(t)
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC, BearerTTL: 15 * time.Minute, OIDCSessionTTL: time.Hour},
		Options{Signer: devSigner(), Sessions: store, Metrics: m})
	ctx := context.Background()

	mustPut(t, store, "acme", "admin-sess", SessionRecord{
		TenantID: "acme", UserID: "bob@acme.com", BearerJTIs: []string{"jti-admin"},
	})
	mustPut(t, store, "acme", "oidc-end-sess", SessionRecord{
		TenantID: "acme", UserID: "carol@acme.com", BearerJTIs: []string{"jti-oidc-end"},
	})

	if err := h.RevokeSession(ctx, "acme", "admin-sess", RevokeAdmin); err != nil {
		t.Fatalf("RevokeSession(admin_revoke): %v", err)
	}
	if err := h.RevokeSession(ctx, "acme", "oidc-end-sess", RevokeOIDCSessionEnded); err != nil {
		t.Fatalf("RevokeSession(oidc_session_ended): %v", err)
	}

	if got := testutil.ToFloat64(m.revocations.WithLabelValues(string(RevokeAdmin))); got != 1 {
		t.Errorf("revocations{reason=admin_revoke} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.revocations.WithLabelValues(string(RevokeOIDCSessionEnded))); got != 1 {
		t.Errorf("revocations{reason=oidc_session_ended} = %v, want 1", got)
	}
	// Each revocation deleted its own record and marked its own bearer,
	// through the same DEL + SET path §27.8 attributes the counter to.
	if _, err := store.GetSession(ctx, "acme", "admin-sess"); err == nil {
		t.Error("admin-sess record survived RevokeSession(admin_revoke)")
	}
	if revoked, _ := store.IsBearerRevoked(ctx, "acme", "jti-admin"); !revoked {
		t.Error("jti-admin not on the deny list after RevokeSession(admin_revoke)")
	}
	if _, err := store.GetSession(ctx, "acme", "oidc-end-sess"); err == nil {
		t.Error("oidc-end-sess record survived RevokeSession(oidc_session_ended)")
	}
	if revoked, _ := store.IsBearerRevoked(ctx, "acme", "jti-oidc-end"); !revoked {
		t.Error("jti-oidc-end not on the deny list after RevokeSession(oidc_session_ended)")
	}
}

// diagnosis: a failure means the exported §27.8 SessionCreated hook —
// the callback the sessionserver package drives once it reads the
// §27.3 origin=playground claim on the session-create path — no
// longer increments lenny_playground_sessions_created_total for the
// creating runtime.
func TestSessionCreatedIncrementsSessionsCreatedTotal_spec_27_8(t *testing.T) {
	m := newSpec278Metrics(t)
	m.SessionCreated("go")
	m.SessionCreated("go")
	m.SessionCreated("node")

	if got := testutil.ToFloat64(m.sessionsCreated.WithLabelValues("go")); got != 2 {
		t.Errorf("sessions_created_total{runtime=go} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.sessionsCreated.WithLabelValues("node")); got != 1 {
		t.Errorf("sessions_created_total{runtime=node} = %v, want 1", got)
	}
}

// diagnosis: a failure means the ws-connect-outcome increment path no
// longer records lenny_playground_ws_connect_total per outcome label.
// Nothing in the gateway's WebSocket accept path currently calls
// wsConnectOutcome (see the discovered-issue note filed alongside this
// finding); this test pins the counter's own increment contract so a
// future caller wiring it to the real MCP WebSocket accept path has a
// regression guard already in place.
func TestWSConnectOutcomeIncrementsWSConnectTotal_spec_27_8(t *testing.T) {
	m := newSpec278Metrics(t)
	m.wsConnectOutcome("success")
	m.wsConnectOutcome("success")
	m.wsConnectOutcome("failure")

	if got := testutil.ToFloat64(m.wsConnect.WithLabelValues("success")); got != 2 {
		t.Errorf("ws_connect_total{outcome=success} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.wsConnect.WithLabelValues("failure")); got != 1 {
		t.Errorf("ws_connect_total{outcome=failure} = %v, want 1", got)
	}
}

// histogramSampleCount reads the cumulative observation count for one
// label series of a HistogramVec via the wire-format Write, the same
// technique TestFillMeterRecordsOnceReachesMinWarm
// (pkg/controller/warmpool/fill_meter_test.go) uses.
func histogramSampleCount(t *testing.T, hv *prometheus.HistogramVec, labelValue string) uint64 {
	t.Helper()
	obs, err := hv.GetMetricWithLabelValues(labelValue)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%s): %v", labelValue, err)
	}
	pb := &dto.Metric{}
	if err := obs.(prometheus.Histogram).Write(pb); err != nil {
		t.Fatalf("write histogram: %v", err)
	}
	return pb.GetHistogram().GetSampleCount()
}

// TestRevocationPropagationHistogramRecordsOnRuntimePath drives the
// real subscribe-loop runtime path — RedisSessionStore.WithMetrics
// wiring propObserver to the registered Metrics histogram, then
// handleRevocationMessage delivering a peer-published revocation — so
// the sample lands on the actual §27.8
// lenny_playground_session_revocation_propagation_seconds collector.
// revocation_propagation_test.go's existing tests all substitute a
// bare func(outcome string, seconds float64) for propObserver, which
// pins handleRevocationMessage's own arithmetic but never exercises
// WithMetrics's wiring into the real histogram.
//
// diagnosis: a failure means WithMetrics no longer connects a peer
// replica's observed revocation latency to the registered §27.8
// propagation histogram, or the sample lands under the wrong outcome
// label.
func TestRevocationPropagationHistogramRecordsOnRuntimePath_spec_27_8(t *testing.T) {
	m := newSpec278Metrics(t)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store := (&RedisSessionStore{
		now:       func() time.Time { return now },
		replicaID: "replica-self",
		cache:     map[string]time.Time{},
	}).WithMetrics(m)

	publishNano := now.Add(-75 * time.Millisecond).UnixNano()
	store.handleRevocationMessage("t:acme:pg:revocations",
		encodeRevocationMsg("replica-peer", publishNano, "jti-runtime-path"))

	if got := histogramSampleCount(t, m.revocationProp, "pubsub_delivered"); got != 1 {
		t.Errorf("revocation_propagation_seconds{outcome=pubsub_delivered} sample count = %d, want 1", got)
	}

	// A self-published message is not a cross-replica observation
	// (§27.8's histogram measures peer-observed latency): it must not
	// add a sample under any outcome.
	store.handleRevocationMessage("t:acme:pg:revocations",
		encodeRevocationMsg("replica-self", now.UnixNano(), "jti-own"))
	if got := histogramSampleCount(t, m.revocationProp, "pubsub_delivered"); got != 1 {
		t.Errorf("revocation_propagation_seconds{outcome=pubsub_delivered} sample count after self-publish = %d, want still 1", got)
	}
}
