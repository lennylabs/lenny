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
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/serviceidentity"
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
	svc.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))
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

// opsBufferReplicaBehindRealAuth serves the genuine gateway admin Router
// behind the genuine auth middleware, wired with the §25.4 service-identity
// resolver the gateway binary wires. No principal is injected: the only way in
// is the credential the caller presents, resolved through a Kubernetes
// TokenReview exactly as a deployed cluster resolves it.
//
// grantedTo is the ServiceAccount username the deployment grants
// platform-admin to; authenticatedAs is the username the apiserver reports for
// the presented token. The two differ when the test presents a token for an
// account the deployment did not grant.
func opsBufferReplicaBehindRealAuth(t *testing.T, buffered []gwevents.OperationalEvent, grantedTo, authenticatedAs string) *httptest.Server {
	t.Helper()
	buf := eventbuffer.NewEventBuffer(64)
	for _, ev := range buffered {
		buf.Append(ev)
	}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithEventBuffer(buf)

	// The apiserver stand-in: it authenticates any token minted for the
	// deployment audience as authenticatedAs, which is what a TokenReview
	// against a real cluster returns for the projected token lenny-ops mounts.
	cs := k8sfake.NewSimpleClientset()
	cs.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		tr := action.(k8stesting.CreateAction).GetObject().(*authnv1.TokenReview)
		tr.Status = authnv1.TokenReviewStatus{
			Authenticated: true,
			Audiences:     tr.Spec.Audiences,
			User:          authnv1.UserInfo{Username: authenticatedAs},
		}
		return true, tr, nil
	})

	handler := authmw.Wrap(router.Handler(), authmw.Options{
		// The gateway's own JWT verifier, which a projected ServiceAccount
		// token never satisfies: it is signed by the cluster's service-account
		// issuer rather than by the platform token service.
		Verifier: jwt.NewHMACSigner("gateway", []byte("not-the-cluster-issuer-key")),
		ServiceIdentity: serviceidentity.New(serviceidentity.Config{
			Verifier: leasecontrol.TokenReviewVerifier{Reviews: cs.AuthenticationV1().TokenReviews()},
			Audience: opsTokenAudience,
			Roles:    map[string][]pkgauth.Role{grantedTo: {pkgauth.RolePlatformAdmin}},
			TenantID: "platform",
		}),
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// opsTokenAudience is the audience the chart mints the lenny-ops projected
// ServiceAccount token for, and the audience the gateway binds its admin-API
// TokenReview to. spec: §25.4 ("Calling the Gateway").
const opsTokenAudience = "lenny-gateway"

// opsSAUsername is the fully-qualified ServiceAccount username lenny-ops runs
// under. spec: §25.4 ("It uses a dedicated service account (`lenny-ops-sa`)").
const opsSAUsername = "system:serviceaccount:lenny-system:lenny-ops-sa"

// deployedOpsGatewayBearer returns the credential lenny-ops actually presents
// to the gateway admin API: the projected ServiceAccount token the chart
// mounts, minted for the lenny-gateway audience. It is JWT-shaped and signed
// by the cluster issuer, which is why the gateway's own JWT verifier rejects
// it and the service-identity path is what must admit it.
func deployedOpsGatewayBearer(t *testing.T) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"aud": []string{opsTokenAudience},
		"sub": opsSAUsername,
		"iss": "https://kubernetes.default.svc.cluster.local",
	})
	if err != nil {
		t.Fatalf("marshal projected token payload: %v", err)
	}
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".cluster-issuer-signature"
}

// spec: 25.4 (lenny-ops calls the gateway admin API as a dedicated service
// account holding platform-admin, through the gateway's standard RBAC), 25.5
// (Redis-down gateway-buffer fall-back)
// diagnosis: a failure means the §25.5 case-1 fall-back is unreachable in a
// deployed cluster. The credential lenny-ops presents is refused by the admin
// gate on GET /v1/admin/events/buffer, so a Redis outage yields the dual-outage
// classification and gateway-originated events have no data path at all,
// whatever the in-process wiring does with an injected principal.
func TestOpsDeployedServiceAccountIsAdmittedToTheGatewayEventBuffer_spec_25_4_25_5(t *testing.T) {
	const gwKey = "gw-1:2000:1"
	buffered := []gwevents.OperationalEvent{{
		ID:          gwKey,
		Type:        "dev.lenny.alert_fired",
		SpecVersion: gwevents.CloudEventsSpecVersion,
		Severity:    "warning",
		Time:        time.Unix(2000, 0).UTC(),
	}}

	// The deployment grants lenny-ops-sa platform-admin, and the credential
	// lenny-ops presents authenticates as that account. The whole chain runs:
	// the JWT verifier rejects the projected token, the service-identity
	// resolver validates it through a TokenReview bound to the deployment
	// audience, and the admin role gate admits the resulting principal.
	replica := opsBufferReplicaBehindRealAuth(t, buffered, opsSAUsername, opsSAUsername)
	status, keys := opsFallbackPoll(t, replica.URL, deployedOpsGatewayBearer(t))
	if status != http.StatusOK {
		t.Fatalf("the credential lenny-ops presents was refused the buffer query: poll status %d; "+
			"the §25.5 Redis-down fall-back cannot serve gateway-originated events in a deployed cluster", status)
	}
	if len(keys) != 1 || keys[0] != gwKey {
		t.Fatalf("the fall-back served %v, want the gateway-originated %s", keys, gwKey)
	}

	// The grant is per account rather than per authentic token: a different
	// service account presenting an equally authentic token for the same
	// audience is refused, and the refusal reaches the read caller as the
	// §25.5 unavailable classification rather than a healthy degraded page.
	other := opsBufferReplicaBehindRealAuth(t, buffered, opsSAUsername, "system:serviceaccount:lenny-system:agent-sa")
	status, keys = opsFallbackPoll(t, other.URL, deployedOpsGatewayBearer(t))
	if status != http.StatusServiceUnavailable {
		t.Fatalf("an ungranted service account produced poll status %d serving %v, want 503: only the "+
			"account §25.4 grants platform-admin may read the gateway event buffer", status, keys)
	}
}
