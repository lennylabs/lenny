// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §11.1 (concurrency, admission-rate), §10.6 (environment-admission),
// §15.2.1 rule 1 — the combined create-and-start path (POST /v1/sessions/start)
// runs the same §11.1 concurrency and admission-rate gates and the §10.6
// environment-admission gate the two-step create path runs. Before the fix
// this path ran only the active-user, tenant-state, tenant-classification,
// session-quota, and policy-chain gates, so it failed open on these three.
// Each test below drives POST /v1/sessions/start and asserts a rejection that
// would not fire against the pre-fix handler, which returned 201.

// createAndStartRequest drives POST /v1/sessions/start with the tenant header
// stamped, mirroring createRequest for the two-step create path.
func createAndStartRequest(t *testing.T, h http.Handler, body sessionserver.CreateAndStartRequest) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// createAndStartRequestAs drives POST /v1/sessions/start with an authenticated
// principal on the context, the §10.2 path the §11.1 line 13 / §10.6 gate
// resolves environment membership against.
func createAndStartRequestAs(t *testing.T, h http.Handler, body sessionserver.CreateAndStartRequest, principal authmw.Principal) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), principal))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestSessionStartRejectedAtPerRuntimeConcurrencyLimit_spec_11_1 pins that the
// §11.1 line 8 per-runtime concurrent-session cap now rejects a create-and-start
// at the limit. The pre-fix create-and-start path never ran requireConcurrencyLimits,
// so this create returned 201 running; the gate now returns 429 QUOTA_EXCEEDED.
// spec: §11.1 line 8; §15.2.1 rule 1.
func TestSessionStartRejectedAtPerRuntimeConcurrencyLimit_spec_11_1(t *testing.T) {
	srv := concurrencyServer(t,
		sessionserver.Options{MaxConcurrentSessionsPerRuntime: 2},
		runningRows("acme", "alice", "claude-code", 2))
	rr := createAndStartRequest(t, srv.Handler(),
		sessionserver.CreateAndStartRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("at per-runtime limit: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "QUOTA_EXCEEDED") || !strings.Contains(rr.Body.String(), `"scope":"runtime"`) {
		t.Errorf("rejection should carry QUOTA_EXCEEDED + runtime scope: %s", rr.Body.String())
	}
}

// TestSessionStartRejectedAtPerUserConcurrencyLimit_spec_11_1 pins the §11.1
// per-user concurrent-session cap on the create-and-start path, keyed off the
// authenticated principal's subject. spec: §11.1 line 8; §15.2.1 rule 1.
func TestSessionStartRejectedAtPerUserConcurrencyLimit_spec_11_1(t *testing.T) {
	srv := concurrencyServer(t,
		sessionserver.Options{MaxConcurrentSessionsPerUser: 2},
		runningRows("acme", "alice@acme.com", "claude-code", 2))
	rr := createAndStartRequestAs(t, srv.Handler(),
		sessionserver.CreateAndStartRequest{RuntimeRef: "claude-code", UserID: "alice@acme.com"},
		authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("at per-user limit: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"scope":"user"`) {
		t.Errorf("rejection should carry user scope: %s", rr.Body.String())
	}
}

// TestSessionStartRejectedAtAdmissionRateLimit_spec_11_1 pins that the §11.1
// line 7 per-runtime requests-per-minute admission limit now rejects on the
// create-and-start path. The pre-fix path never ran requireAdmissionRateLimit,
// so a caller could create-and-start past the per-minute cap unthrottled.
// spec: §11.1 line 7; §15.2.1 rule 1.
func TestSessionStartRejectedAtAdmissionRateLimit_spec_11_1(t *testing.T) {
	srv := admissionServer(sessionserver.Options{
		AdmissionRateLimitCounter: rlcounter.NewMemory(),
		PerRuntimePerMinute:       2,
	})
	h := srv.Handler()
	for i := 1; i <= 2; i++ {
		rr := createAndStartRequest(t, h, sessionserver.CreateAndStartRequest{RuntimeRef: "claude-code"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create-and-start %d under limit: status %d, want 201; body %s", i, rr.Code, rr.Body.String())
		}
	}
	rr := createAndStartRequest(t, h, sessionserver.CreateAndStartRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd create-and-start: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "RATE_LIMITED") {
		t.Errorf("rejection should carry RATE_LIMITED: %s", rr.Body.String())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("a 429 admission rejection must carry a Retry-After header")
	}
}

// TestSessionStartRejectedByEnvironmentAdmission_spec_10_6 pins that the §11.1
// line 13 / §10.6 environment-admission gate now rejects a create-and-start
// that names no environment when the caller holds no environment membership
// and the tenant's noEnvironmentPolicy resolves to deny-all. The pre-fix
// create-and-start path never ran requireEnvironmentAdmission, so this create
// returned 201; the gate now returns 403 FORBIDDEN.
// spec: §11.1 line 13; §10.6; §15.2.1 rule 1.
func TestSessionStartRejectedByEnvironmentAdmission_spec_10_6(t *testing.T) {
	srv, _ := seedNoEnvServer(t, "sess_start_deny",
		tenantstore.NoEnvPolicyDenyAll, tenantstore.NoEnvPolicyDenyAll)
	rr := createAndStartRequestAs(t, srv.Handler(),
		sessionserver.CreateAndStartRequest{RuntimeRef: "claude-code"},
		authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("no-environment deny-all: status %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "FORBIDDEN") ||
		!strings.Contains(rr.Body.String(), "no_environment_policy_deny_all") {
		t.Errorf("rejection should carry FORBIDDEN + deny-all reason: %s", rr.Body.String())
	}
}

// countingRLCounter wraps a rate-limit counter and records how many times Incr
// is called, so a test can assert whether the admission-rate gate ran.
type countingRLCounter struct {
	inner rlcounter.Counter
	calls int
}

func (c *countingRLCounter) Incr(ctx context.Context, key string, now time.Time) (int, error) {
	c.calls++
	return c.inner.Incr(ctx, key, now)
}

// TestSessionStartConcurrencyGateRunsBeforeRateGate_spec_11_1 pins the §11.1
// concurrency-and-rate-before-policy ordering on the create-and-start path: a
// create rejected by the concurrency cap reserves no rate budget, so the
// admission-rate counter is never incremented. A regression that placed the
// rate gate before the concurrency gate would bump the counter here.
// spec: §11.1 line 8 (over-limit reserves no rate budget); §15.2.1 rule 1.
func TestSessionStartConcurrencyGateRunsBeforeRateGate_spec_11_1(t *testing.T) {
	spy := &countingRLCounter{inner: rlcounter.NewMemory()}
	store := memstore.New()
	now := time.Now().UTC()
	for _, row := range runningRows("acme", "alice", "claude-code", 1) {
		row.CreatedAt, row.UpdatedAt = now, now
		if err := store.Create(context.Background(), row); err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                           func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) },
		MaxConcurrentSessionsPerRuntime: 1,
		AdmissionRateLimitCounter:       spy,
		PerRuntimePerMinute:             5,
	})
	rr := createAndStartRequest(t, srv.Handler(),
		sessionserver.CreateAndStartRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("at concurrency cap: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "QUOTA_EXCEEDED") {
		t.Errorf("rejection should be the concurrency QUOTA_EXCEEDED, not the rate RATE_LIMITED: %s", rr.Body.String())
	}
	if spy.calls != 0 {
		t.Errorf("admission-rate counter incremented %d times on a concurrency-rejected create; the concurrency gate must short-circuit before the rate gate", spy.calls)
	}
}
