// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §27.10 roll-forward claim: turning
// playground.enabled off is safe against sessions already in flight,
// whether they originated from the playground or not, and only blocks
// new playground traffic.
//
// The suite does not boot the compiled cmd/lenny-gateway binary with
// --playground-enabled: doing so currently crash-loops on an unrelated
// defect (pkg/gateway/mcpfabric/playground/metrics.go registers the
// lenny_playground_page_views_total counter with the camelCase label
// "authMode", which the §16.1.1 snake_case validator rejects fatally at
// startup under every playground.authMode). That defect is already
// tracked (BUILD-GAPS.md §16.1 Metrics Finding 8) and is the documented
// reason tests/tier4_integration/playground_ws_carrier_test.go,
// tests/tier4_integration/playground_idle_override_test.go, and
// tests/tier4_integration/playground_authmode_matrix_test.go compose the
// real middleware and handler types directly instead of the binary. This
// suite follows the same convention, modeling the "before" and "after" a
// reconfigure toggling playground.enabled as two independent
// http.Handler compositions sharing the one real sessionserver.Server
// (and its store) — exactly what cmd/lenny-gateway/httpsurface.go's
// `if *playgroundEnabled { mux.Handle(...) }` gate produces on a rollout
// that flips the flag: the session store is externalized and unaffected,
// only the route table changes.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// rollforwardSession is the slice of the §15.1 session envelope this
// suite inspects.
type rollforwardSession struct {
	ID     string `json:"id"`
	Origin string `json:"origin"`
	State  string `json:"state"`
}

// buildRollforwardMux composes the real production session and (when
// pg is non-nil) playground routes behind the real §10.2 authmw.Wrap
// bearer chain, in the same outer-to-inner order
// cmd/lenny-gateway/httpsurface.go wires them. Passing pg=nil mirrors
// the httpsurface.go `if *playgroundEnabled { ... }` gate evaluating
// false: the playground asset bundle and the POST /v1/playground/token
// mint endpoint are never registered on the mux, matching the §27.2
// "the asset bundle is unmounted" effect of playground.enabled=false.
func buildRollforwardMux(sessionsHandler http.Handler, pg *playground.Handler, signer *jwt.HMACSigner, tenants *tenantstore.Memory) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1/sessions", sessionsHandler)
	mux.Handle("/v1/sessions/", sessionsHandler)
	if pg != nil {
		mux.Handle("/v1/playground/token", pg.TokenRoutes())
		mux.Handle("/playground", pg.PlaygroundRoutes())
		mux.Handle("/playground/", pg.PlaygroundRoutes())
	}
	return authmw.Wrap(mux, authmw.Options{
		MultiTenant: true,
		Verifier:    signer,
		Registry:    tenants,
	})
}

// spec: §27.10 (spec/27_web-playground.md:265) — "The playground is
// additive: disabling playground.enabled is safe at any time and has no
// effect on in-flight non-playground sessions. Playground-initiated
// sessions already in flight continue to run to completion or their
// configured cap; only new playground sessions are blocked."; §27.2
// (spec/27_web-playground.md:33) — "playground.enabled | false | When
// false, /playground/* returns 404 and the asset bundle is unmounted".
//
// diagnosis: a failure here means disabling playground.enabled does
// more than the spec allows: either an already-running playground-
// origin or non-playground session stops being servable (read or
// terminated) once the flag flips off, or the post-toggle gateway
// surface still admits a new playground session (the token-mint route,
// or the playground asset route, stays reachable instead of 404ing).
// Check cmd/lenny-gateway/httpsurface.go's `if *playgroundEnabled`
// gate: the session routes (/v1/sessions, /v1/sessions/) must sit
// outside it while the playground routes (/playground, /playground/,
// /v1/playground/token) must sit inside it, and neither the create nor
// the get/terminate handlers in pkg/gateway/sessionserver may consult
// the playground flag at all.
func TestPlaygroundDisableDoesNotAffectInFlightSessions_spec_27_10(t *testing.T) {
	signer := jwt.NewHMACSigner("pg-rollforward-test", []byte("playground-rollforward-test-secret"))

	// One tenant registry shared by the playground mint path and the
	// authmw bearer chain, the same split newPlaygroundServer's own
	// internal tenant store keeps distinct from this external one (see
	// playground_authmode_matrix_test.go).
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(t.Context(), tenantstore.Tenant{
		ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
	}); err != nil {
		t.Fatalf("seed tenant registry: %v", err)
	}

	rt := runtimestore.NewMemory()
	if err := rt.Create(t.Context(), runtimestore.Runtime{
		Name: "claude-code",
		Type: runtimestore.TypeAgent,
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	// One real sessionserver.Server (and its store) shared by the
	// "before" and "after" handler compositions below — the durable
	// session state a rolling reconfigure does not touch.
	srv, _ := newPlaygroundServer(t, rt, playground.Config{
		MaxIdleTimeSeconds: idlePlaygroundCapDisabled,
		MaxSessionMinutes:  idlePlaygroundCapDisabled,
	})

	pgCfg := playground.Config{
		Enabled:     true,
		AuthMode:    playground.AuthModeDev,
		MultiTenant: true,
		BearerTTL:   900 * time.Second,
		DevTenantID: "acme",
	}
	pg := playground.New(pgCfg, playground.Options{Signer: signer, Tenants: tenants})

	// "Before": playground.enabled=true. Both routes families are live.
	beforeSrv := httptest.NewServer(buildRollforwardMux(srv.Handler(), pg, signer, tenants))
	defer beforeSrv.Close()

	// Mint a playground bearer through the real dev-mode admission path
	// and use it to create a playground-origin session — the §27.3
	// origin: "playground" claim stamped on every playground mint.
	pgBearer := mintRollforwardPlaygroundBearer(t, beforeSrv)
	pgSession := createRollforwardSession(t, beforeSrv, pgBearer, "claude-code")
	if pgSession.Origin != "playground" {
		t.Fatalf("playground-origin session origin = %q, want %q", pgSession.Origin, "playground")
	}
	if pgSession.State == "" {
		t.Fatalf("playground-origin session state is empty")
	}

	// A non-playground session: a directly signed standard bearer, the
	// same credential shape a regular client presents on the §10.2
	// Client -> Gateway path, carrying no origin claim at all.
	directBearer, err := signer.Sign(jwt.Claims{
		Subject:    "bob@acme.com",
		TenantID:   "acme",
		CallerType: "human",
		Typ:        auth.TokenUserBearer,
	})
	if err != nil {
		t.Fatalf("sign direct bearer: %v", err)
	}
	directSession := createRollforwardSession(t, beforeSrv, directBearer, "claude-code")
	if directSession.Origin != "" {
		t.Fatalf("non-playground session origin = %q, want empty", directSession.Origin)
	}

	// "After": playground.enabled=false. A fresh handler composition
	// over the SAME sessionserver.Server/store, with the playground
	// routes never registered — the observable effect of the
	// httpsurface.go conditional mount flipping off, independent of
	// whatever process/rollout mechanism performed the reconfigure.
	afterSrv := httptest.NewServer(buildRollforwardMux(srv.Handler(), nil, signer, tenants))
	defer afterSrv.Close()

	// New playground traffic is blocked: the asset route and the
	// mode-polymorphic mint endpoint both 404 once the flag is off.
	assetResp, err := http.Get(afterSrv.URL + "/playground/")
	if err != nil {
		t.Fatalf("GET /playground/: %v", err)
	}
	defer assetResp.Body.Close()
	if assetResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /playground/ after disabling playground.enabled: status = %d, want 404", assetResp.StatusCode)
	}

	mintResp, err := http.Post(afterSrv.URL+"/v1/playground/token", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	defer mintResp.Body.Close()
	if mintResp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /v1/playground/token after disabling playground.enabled: status = %d, want 404", mintResp.StatusCode)
	}

	// Both sessions already in flight are unaffected: each is still
	// readable, and each can still be driven to completion, through the
	// post-disable gateway surface. A non-playground bearer suffices
	// for both reads/terminations since the minimal gateway's session
	// permission gate admits any authenticated, roleless principal
	// (pkg/gateway/sessionserver/rbac_gate.go) regardless of who
	// created the row.
	for _, id := range []string{pgSession.ID, directSession.ID} {
		got := getRollforwardSession(t, afterSrv, directBearer, id)
		if got.ID != id {
			t.Fatalf("GET session %s after disabling playground.enabled: got id %q", id, got.ID)
		}
		switch got.State {
		case "completed", "failed", "cancelled", "expired":
			t.Fatalf("session %s was already terminal (%s) right after create; the store was not shared across the before/after handlers as intended", id, got.State)
		}

		terminated := terminateRollforwardSession(t, afterSrv, directBearer, id)
		if terminated.State != "completed" {
			t.Fatalf("terminate session %s after disabling playground.enabled: state = %q, want %q (a session already in flight must be able to run to completion)", id, terminated.State, "completed")
		}
	}
}

// mintRollforwardPlaygroundBearer drives POST /v1/playground/token in
// dev mode (no admission material required) and returns the minted
// bearerToken.
func mintRollforwardPlaygroundBearer(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp, err := http.Post(srv.URL+"/v1/playground/token", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST /v1/playground/token: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/playground/token: want 200, got %d (body %s)", resp.StatusCode, raw)
	}
	var out struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode mint response: %v; body %s", err, raw)
	}
	return out.BearerToken
}

// createRollforwardSession issues POST /v1/sessions with bearer and
// returns the parsed session.
func createRollforwardSession(t *testing.T, srv *httptest.Server, bearer, runtimeRef string) rollforwardSession {
	t.Helper()
	body, err := json.Marshal(map[string]string{"runtimeRef": runtimeRef})
	if err != nil {
		t.Fatalf("marshal create-session body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/sessions", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build create-session request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /v1/sessions: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/sessions: want 201, got %d (body %s)", resp.StatusCode, raw)
	}
	var s rollforwardSession
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode create-session response: %v; body %s", err, raw)
	}
	return s
}

// getRollforwardSession issues GET /v1/sessions/{id} with bearer and
// returns the parsed session.
func getRollforwardSession(t *testing.T, srv *httptest.Server, bearer, id string) rollforwardSession {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/sessions/"+id, nil)
	if err != nil {
		t.Fatalf("build get-session request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /v1/sessions/%s: %v", id, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/sessions/%s: want 200, got %d (body %s)", id, resp.StatusCode, raw)
	}
	var s rollforwardSession
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode get-session response: %v; body %s", err, raw)
	}
	return s
}

// terminateRollforwardSession issues POST /v1/sessions/{id}/terminate
// with bearer and returns the parsed session.
func terminateRollforwardSession(t *testing.T, srv *httptest.Server, bearer, id string) rollforwardSession {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/sessions/"+id+"/terminate", nil)
	if err != nil {
		t.Fatalf("build terminate-session request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /v1/sessions/%s/terminate: %v", id, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/sessions/%s/terminate: want 200, got %d (body %s)", id, resp.StatusCode, raw)
	}
	var s rollforwardSession
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode terminate-session response: %v; body %s", err, raw)
	}
	return s
}
