// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 HTTP contract tests for the targetPool selection check on
// POST /v1/sessions/{id}/derive and POST /v1/sessions/{id}/replay in both
// replay modes. A named targetPool passes the check POST /v1/sessions runs on
// a create-body pool, so a pool the gateway cannot satisfy answers
// 400 VALIDATION_ERROR and a pool the tenant is not granted answers
// 403 FORBIDDEN. Both statuses are declared by the served OpenAPI document,
// and every body is validated against the declared schema.
//
// spec: §15.1 (REST API: Session Replay Semantics, Error code catalog), §7.1
// (Normal Flow: Derive session semantics), §14.1 (CreateSessionRequest.pool),
// §4 (pool_tenant_access)
package rest_sessions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// targetPoolEndpoints are derive and both replay modes, each naming pool as
// the targetPool.
func targetPoolEndpoints(pool string) []snapCopyEndpoint {
	return []snapCopyEndpoint{
		{"derive", "/v1/sessions/{id}/derive", sessionserver.DeriveRequest{TargetPool: pool}},
		{"replay prompt_history", "/v1/sessions/{id}/replay", sessionserver.ReplayRequest{
			ReplayMode: sessionserver.ReplayPromptHistory, TargetRuntime: "echo", TargetPool: pool,
		}},
		{"replay workspace_derive", "/v1/sessions/{id}/replay", sessionserver.ReplayRequest{
			ReplayMode: sessionserver.ReplayWorkspaceDerive, TargetRuntime: "echo", TargetPool: pool,
		}},
	}
}

// spec: §15.1 (REST API: Session Replay Semantics, Error code catalog), §7.1 (Normal Flow: Derive session semantics), §14.1 (CreateSessionRequest.pool), §4 (pool_tenant_access)
// diagnosis: a failure means derive or replay accepted a targetPool the
// create path would refuse, answered it with a status or code other than the
// create path's 400 VALIDATION_ERROR or 403 FORBIDDEN (a 503 means the check
// is skipped and the claim failed instead), left a row behind, or wrote a
// body or status the served OpenAPI document does not declare. Check the
// requirePoolSelectable calls in pkg/gateway/sessionserver/derive.go and
// replay.go, and the 400 and 403 responses of both operations in
// pkg/gateway/externalapi/openapi/openapi.json.
func TestDeriveAndReplayTargetPoolSelectionContract_spec_15_1(t *testing.T) {
	v := newSnapCopyValidator(t)
	cases := []struct {
		name    string
		pool    string
		granted bool
		status  int
		code    string
		reason  string
	}{
		{name: "absent pool", pool: "no-such-pool", granted: true, status: http.StatusBadRequest, code: "VALIDATION_ERROR", reason: "pool_not_satisfiable"},
		{name: "ungranted pool", pool: "echo-pool", granted: false, status: http.StatusForbidden, code: "FORBIDDEN", reason: "pool_access_denied"},
	}
	for _, tc := range cases {
		for _, ep := range targetPoolEndpoints(tc.pool) {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				f := newSnapCopyFixture(t)
				access := tenantaccessstore.NewMemory()
				if tc.granted {
					if _, err := access.Grant(context.Background(), tenantaccessstore.KindPool, tc.pool, "acme", "alice@acme.com", time.Time{}); err != nil {
						t.Fatalf("grant pool: %v", err)
					}
				}
				h := f.handler(func(o *sessionserver.Options) {
					o.DefaultIsolationProfile = isolation.ProfileSandboxed
					o.PodBinder = &podsession.Binder{Client: claimContractCluster(t, echoPool(false)...), Namespace: claimContractNS}
					o.PodRegistry = podsession.NewRegistry()
					o.AgentNamespace = claimContractNS
					o.TenantAccess = access
				})
				rr := ep.call(t, h)
				if rr.Code != tc.status {
					t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.status, rr.Body.String())
				}
				status := strconv.Itoa(rr.Code)
				if _, ok := servedOperation(t, v.doc, ep.path, "post").Responses[status]; !ok {
					t.Errorf("POST %s does not declare the %s response it answers", ep.path, status)
				}
				v.validate(t, ep.path, status, rr.Body.Bytes())
				var env struct {
					Error struct {
						Code    string         `json:"code"`
						Details map[string]any `json:"details"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
					t.Fatalf("decode error envelope: %v", err)
				}
				if env.Error.Code != tc.code || env.Error.Details["reason"] != tc.reason || env.Error.Details["field"] != "targetPool" {
					t.Errorf("error = %s %v, want %s reason %s field targetPool", env.Error.Code, env.Error.Details, tc.code, tc.reason)
				}
				if _, err := f.store.Get(context.Background(), "acme", "sess_snap_new"); !errors.Is(err, sessionstore.ErrNotFound) {
					t.Errorf("the rejected targetPool left a row behind (err %v)", err)
				}
			})
		}
	}
}
