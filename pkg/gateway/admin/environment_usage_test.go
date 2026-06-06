// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §15.1 line 840 GET /v1/admin/environments/{name}/usage.

func envUsageRouter(t *testing.T, billing billingstore.Store, envs ...environmentstore.Environment) *admin.Router {
	t.Helper()
	store := environmentstore.NewMemory()
	for _, e := range envs {
		if err := store.Create(context.Background(), e); err != nil {
			t.Fatalf("seed environment %q: %v", e.Name, err)
		}
	}
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithEnvironments(store)
	if billing != nil {
		r = r.WithBillingCorrections(billing, correctionstore.NewMemory(), 0)
	}
	return r
}

// TestEnvironmentUsage_spec_15_1_840 verifies the rollup sums the
// environment's billing events, applies a §11.2.1 correction, and
// isolates other environments. spec: §15.1 line 840; §11.2.1. F-15.1.3.
func TestEnvironmentUsage_spec_15_1_840(t *testing.T) {
	ctx := context.Background()
	billing := billingstore.NewMemory()
	orig, _ := billing.Append(ctx, billingstore.Event{
		TenantID: "acme", SessionID: "s1", EnvironmentID: "prod",
		EventType: billingstore.EventSessionCreated, TokensInput: 1000, TokensOutput: 200, PodMinutes: 3,
	})
	_, _ = billing.Append(ctx, billingstore.Event{
		TenantID: "acme", SessionID: "s2", EnvironmentID: "prod",
		EventType: billingstore.EventSessionCompleted, TokensInput: 50, TokensOutput: 10, PodMinutes: 1,
	})
	// A correction (no environment_id, as production writes it) supersedes
	// the s1 original's figures.
	_, _ = billing.Append(ctx, billingstore.Event{
		TenantID: "acme", EventType: billingstore.EventBillingCorrection,
		TokensInput: 100, TokensOutput: 20, PodMinutes: 0.5,
		CorrectsSequence: orig.SequenceNumber, CorrectionReasonCode: billingstore.ReasonRetryOvercounting,
	})
	// An unrelated environment must not leak into the prod rollup.
	_, _ = billing.Append(ctx, billingstore.Event{
		TenantID: "acme", SessionID: "s3", EnvironmentID: "staging",
		EventType: billingstore.EventSessionCreated, TokensInput: 999, TokensOutput: 999, PodMinutes: 9,
	})

	router := envUsageRouter(t, billing,
		environmentstore.Environment{Name: "prod", TenantID: "acme"})

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/environments/prod/usage?tenantId=acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.EnvironmentUsagePayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// s1 corrected to 100/20/0.5 + s2 50/10/1 = 150/30/1.5 across 2 sessions.
	if resp.Environment != "prod" || resp.TenantID != "acme" {
		t.Errorf("identity = %+v", resp)
	}
	if resp.TokensInput != 150 || resp.TokensOutput != 30 || resp.PodMinutes != 1.5 || resp.EventCount != 2 {
		t.Fatalf("rollup = %+v, want {150 30 1.5 2}", resp)
	}
}

// TestEnvironmentUsageNotFound_spec_15_1_840 verifies an absent
// environment returns 404. spec: §15.1 line 840. F-15.1.3.
func TestEnvironmentUsageNotFound_spec_15_1_840(t *testing.T) {
	router := envUsageRouter(t, billingstore.NewMemory())
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/environments/ghost/usage?tenantId=acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestEnvironmentUsageZero_spec_15_1_840 verifies an environment with no
// billing events returns a zero-valued rollup, not an error. spec: §15.1
// line 840. F-15.1.3.
func TestEnvironmentUsageZero_spec_15_1_840(t *testing.T) {
	router := envUsageRouter(t, billingstore.NewMemory(),
		environmentstore.Environment{Name: "prod", TenantID: "acme"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/environments/prod/usage?tenantId=acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.EnvironmentUsagePayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TokensInput != 0 || resp.TokensOutput != 0 || resp.PodMinutes != 0 || resp.EventCount != 0 {
		t.Fatalf("zero rollup = %+v, want all zero", resp)
	}
}

// TestEnvironmentUsageRouteAbsentWithoutBilling_spec_15_1_840 verifies the
// usage route is not mounted on a deployment with no billing ledger, so a
// request 404s rather than silently reporting a fabricated zero rollup.
// spec: §15.1 line 840. F-15.1.3.
func TestEnvironmentUsageRouteAbsentWithoutBilling_spec_15_1_840(t *testing.T) {
	router := envUsageRouter(t, nil,
		environmentstore.Environment{Name: "prod", TenantID: "acme"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/environments/prod/usage?tenantId=acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (route unmounted); body=%s", rr.Code, rr.Body.String())
	}
}
