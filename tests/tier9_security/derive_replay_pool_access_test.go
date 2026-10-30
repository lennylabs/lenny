// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §4 pool_tenant_access enforcement on the targetPool of
// POST /v1/sessions/{id}/derive and POST /v1/sessions/{id}/replay. Derive
// and replay create a session whose create-time claim is pinned to
// targetPool, so the grant that gates a create-body pool pin gates them
// too: a tenant that names a platform-global pool it was never granted is
// refused 403 FORBIDDEN before any snapshot copy or pod claim. Without the
// gate a tenant could place a derived or replayed session on any pool by
// naming it.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// diagnosis: the §4 pool_tenant_access grant is not enforced on the
// targetPool of derive or replay. The test seeds an envtest warm pool backed
// by the echo runtime and a terminal acme source session, then names the
// pool as targetPool from acme, which holds no grant. Any status other than
// 403 pool_access_denied means a tenant can place a derived or replayed
// session on a pool it was never granted. A server that grants acme the
// pool must not answer pool_access_denied, ruling out a blanket rejection.
// Check the requirePoolSelectable calls in
// pkg/gateway/sessionserver/derive.go and replay.go.
// spec: 4 (pool_tenant_access), 15.1 (REST API: Session Replay Semantics, FORBIDDEN), 7.1 (Normal Flow: Derive session semantics), 14.1 (CreateSessionRequest.pool)
func TestDeriveAndReplayTargetPoolEnforcesTenantAccess_spec_4(t *testing.T) {
	cluster := poolAccessCluster(t)
	endpoints := []struct {
		name string
		path string
		body any
	}{
		{"derive", "/v1/sessions/sess_src/derive", sessionserver.DeriveRequest{TargetPool: "echo-pool"}},
		{"replay prompt_history", "/v1/sessions/sess_src/replay", sessionserver.ReplayRequest{
			ReplayMode: sessionserver.ReplayPromptHistory, TargetRuntime: "echo", TargetPool: "echo-pool",
		}},
		{"replay workspace_derive", "/v1/sessions/sess_src/replay", sessionserver.ReplayRequest{
			ReplayMode: sessionserver.ReplayWorkspaceDerive, TargetRuntime: "echo", TargetPool: "echo-pool",
		}},
	}
	serve := func(t *testing.T, grantee string, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		store := memstore.New()
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: "sess_src", TenantID: "acme", UserID: "alice@acme.com", State: session.StateCompleted,
			RuntimeRef: "echo", PoolRef: "echo-pool", IsolationProfile: isolation.ProfileSandboxed,
			WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
				Ref: "/acme/checkpoints/sess_src/ckpt_src", Source: sessionstore.WorkspaceSnapshotSealed,
			},
		}); err != nil {
			t.Fatalf("seed source: %v", err)
		}
		access := tenantaccessstore.NewMemory()
		if _, err := access.Grant(context.Background(), tenantaccessstore.KindPool, "echo-pool",
			grantee, "platform-admin@"+grantee+".com", time.Time{}); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
		srv := sessionserver.New(store, sessionserver.Options{
			DevTenantHeader:         true,
			IDFunc:                  func() string { return "sess_new" },
			DefaultIsolationProfile: isolation.ProfileSandboxed,
			PodBinder:               &podsession.Binder{Client: cluster, Namespace: poolAccessNS},
			PodRegistry:             podsession.NewRegistry(),
			AgentNamespace:          poolAccessNS,
			TenantAccess:            access,
		})
		buf, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}
	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			// globex holds the grant and acme does not: acme's pin is
			// satisfiable yet unauthorized.
			rr := serve(t, "globex", ep.path, ep.body)
			if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "pool_access_denied") {
				t.Fatalf("§4: an ungranted tenant named echo-pool as targetPool and got %d, want 403 pool_access_denied (body=%s)",
					rr.Code, rr.Body.String())
			}
			// acme holds the grant: the pin clears the §4 gate. The request
			// fails later, at the snapshot copy this server does not wire.
			rr = serve(t, "acme", ep.path, ep.body)
			if strings.Contains(rr.Body.String(), "pool_access_denied") {
				t.Errorf("§4: a granted tenant was rejected with pool_access_denied (status %d, body=%s)", rr.Code, rr.Body.String())
			}
		})
	}
}
