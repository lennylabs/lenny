// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security coverage for the authorization the §25.5 Redis-down
// gateway-buffer fall-back depends on. The fall-back fetches
// GET /v1/admin/events/buffer from every gateway replica, and that route is
// gated on the §10.2 platform-admin role. §25.4 gives lenny-ops a dedicated
// service account holding that role and states that its calls go through the
// gateway's standard RBAC, so the read path is reachable only when the
// principal lenny-ops presents is admitted by that gate.
//
// The gate is exercised in-process against the genuine admin Router with an
// injected Principal, the same authorization code path a Bearer caller
// exercises, because the deployed identity of the lenny-ops service account is
// resolved by the cluster's OIDC configuration rather than by anything a test
// can reproduce. What is pinned here is the consequence for the read surface:
// a principal the gate admits serves the gateway-originated event, and a
// principal it refuses is reported as unavailable rather than as a healthy
// degraded page.
//
// spec: §25.4 (lenny-ops calls the gateway admin API as a service account
// holding platform-admin, through the gateway's standard RBAC), §25.5
// (Redis-down gateway-buffer fall-back; EVENT_STREAM_UNAVAILABLE when no
// source can serve gateway-originated events).

package tier9_security_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// opsBufferReplica serves the genuine gateway admin Router over one replica's
// §25.3 event buffer, behind an auth step that attaches the principal the
// bearer token names. "platform-admin" is the §25.4 lenny-ops service account
// as the spec defines it; any other token is a principal holding no platform
// role, which is what the admin gate refuses.
func opsBufferReplica(t *testing.T, buffered []gwevents.OperationalEvent) *httptest.Server {
	t.Helper()
	buf := eventbuffer.NewEventBuffer(64)
	for _, ev := range buffered {
		buf.Append(ev)
	}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithEventBuffer(buf)

	handler := router.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := authmw.Principal{Subject: "system:serviceaccount:lenny-system:lenny-ops-sa", TenantID: "platform"}
		if req.Header.Get("Authorization") == "Bearer platform-admin" {
			p.Roles = []pkgauth.Role{pkgauth.RolePlatformAdmin}
		}
		handler.ServeHTTP(w, req.WithContext(authmw.WithPrincipal(req.Context(), p)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// opsFallbackPoll builds the §25.5 read surface with Redis reported down and
// the gateway up, fans the buffer query at replica with the given bearer, and
// returns the poll status and the eventKeys the page served.
func opsFallbackPoll(t *testing.T, replicaURL, bearer string) (int, []string) {
	t.Helper()
	client, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken(bearer),
		Discovery:         gateway.StaticDiscovery{replicaURL},
		PerRequestTimeout: 5 * time.Second,
		FanOutTimeout:     3 * time.Second,
	})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}
	svc := opsstream.New(opsstream.Options{
		SourceHealth: opsstream.StaticSourceHealth{Redis: false, Gateway: true},
		ReplicaID:    "ops-1",
	})
	svc.SetGatewayBufferSource(client)

	rec := httptest.NewRecorder()
	svc.HandlePoll(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil))
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var body struct {
		Items []struct {
			Event struct {
				ID string `json:"id"`
			} `json:"event"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode poll body %q: %v", rec.Body.String(), err)
	}
	keys := make([]string, 0, len(body.Items))
	for _, it := range body.Items {
		keys = append(keys, it.Event.ID)
	}
	return rec.Code, keys
}

// spec: 25.4 (lenny-ops holds platform-admin on the gateway admin API), 25.5
// (Redis-down gateway-buffer fall-back)
// diagnosis: a failure means the §25.5 Redis-down data path is not reachable
// under the authorization the deployed gateway applies. Either the
// platform-admin principal is refused on GET /v1/admin/events/buffer, so no
// gateway-originated event can ever reach a lenny-ops read caller during a
// Redis outage, or a refused principal is reported as a healthy degraded page,
// which hides the refusal behind a gateway-buffer label over an empty result.
func TestOpsGatewayBufferFanOutRequiresPlatformAdmin_spec_25_4_25_5(t *testing.T) {
	const gwKey = "gw-1:1000:1"
	replica := opsBufferReplica(t, []gwevents.OperationalEvent{{
		ID:          gwKey,
		Type:        "dev.lenny.alert_fired",
		SpecVersion: gwevents.CloudEventsSpecVersion,
		Severity:    "warning",
		Time:        time.Unix(1000, 0).UTC(),
	}})

	// The §25.4 service account as the spec defines it: platform-admin. The
	// gate admits it and the gateway-originated event reaches the read caller.
	status, keys := opsFallbackPoll(t, replica.URL, "platform-admin")
	if status != http.StatusOK {
		t.Fatalf("a platform-admin principal was refused the buffer query: poll status %d", status)
	}
	if len(keys) != 1 || keys[0] != gwKey {
		t.Fatalf("the fall-back served %v, want the gateway-originated %s: the admin gate admitted the "+
			"principal but the fan-out did not deliver its page", keys, gwKey)
	}

	// A principal without the role the admin gate requires, which is the state
	// a service account lacking the §25.4 grant is in. The refusal must reach
	// the caller as the §25.5 unavailable classification.
	status, keys = opsFallbackPoll(t, replica.URL, "no-roles")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("a refused principal produced poll status %d serving %v, want 503: an authorization refusal "+
			"must not surface as a healthy gateway-buffer page", status, keys)
	}
}
