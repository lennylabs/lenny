// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSessionRecordCarriesLabelsThroughJSON exercises the §27.2
// line 41 SessionRecord.Labels round-trip: an operator-supplied
// label survives Marshal+Unmarshal so a Redis-stored record carries
// the audit/accounting labels back on every reread. F-27.2.1.
func TestSessionRecordCarriesLabelsThroughJSON_spec_27_2_41(t *testing.T) {
	original := SessionRecord{
		UserID:   "alice",
		TenantID: "acme",
		Origin:   PlaygroundOrigin,
		Labels:   map[string]string{"origin": "playground", "team": "platform"},
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal SessionRecord: %v", err)
	}
	var decoded SessionRecord
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal SessionRecord: %v", err)
	}
	if decoded.Labels["origin"] != PlaygroundOrigin {
		t.Fatalf("decoded labels[origin] = %q, want %q", decoded.Labels["origin"], PlaygroundOrigin)
	}
	if decoded.Labels["team"] != "platform" {
		t.Fatalf("decoded labels[team] = %q, want platform", decoded.Labels["team"])
	}
}

func TestMemorySessionStorePerTenantIsolation(t *testing.T) {
	store := NewMemorySessionStore()
	ctx := context.Background()
	rec := SessionRecord{UserID: "alice", TenantID: "acme", Origin: PlaygroundOrigin}
	if err := store.PutSession(ctx, "acme", "sess1", rec, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	// A read scoped to a different tenant must not see the record.
	if _, err := store.GetSession(ctx, "globex", "sess1"); err == nil {
		t.Fatal("tenant globex read a tenant acme session record")
	}
	got, err := store.GetSession(ctx, "acme", "sess1")
	if err != nil {
		t.Fatalf("GetSession(acme): %v", err)
	}
	if got.UserID != "alice" {
		t.Fatalf("record UserID = %q, want alice", got.UserID)
	}
}

func TestMemorySessionStoreRevocationKeyIsolation(t *testing.T) {
	store := NewMemorySessionStore()
	ctx := context.Background()
	// A revocation marker for tenant acme's jti must not cause a tenant
	// globex request reusing the same jti value to be rejected.
	if err := store.MarkBearerRevoked(ctx, "acme", "jti-shared", time.Hour); err != nil {
		t.Fatalf("MarkBearerRevoked: %v", err)
	}
	revoked, err := store.IsBearerRevoked(ctx, "acme", "jti-shared")
	if err != nil || !revoked {
		t.Fatalf("acme jti-shared revoked = %v, %v; want true, nil", revoked, err)
	}
	revoked, err = store.IsBearerRevoked(ctx, "globex", "jti-shared")
	if err != nil || revoked {
		t.Fatalf("globex jti-shared revoked = %v, %v; want false, nil", revoked, err)
	}
}

func TestRevokeSessionDeletesRecordAndMarksBearers(t *testing.T) {
	store := NewMemorySessionStore()
	ctx := context.Background()
	rec := SessionRecord{
		UserID:     "carol",
		TenantID:   "acme",
		Origin:     PlaygroundOrigin,
		BearerJTIs: []string{"jti-a", "jti-b"},
	}
	if err := store.PutSession(ctx, "acme", "sess9", rec, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	if err := store.RevokeSession(ctx, "acme", "sess9", rec.BearerJTIs, time.Hour); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := store.GetSession(ctx, "acme", "sess9"); err == nil {
		t.Fatal("RevokeSession did not delete the session record")
	}
	for _, jti := range rec.BearerJTIs {
		revoked, err := store.IsBearerRevoked(ctx, "acme", jti)
		if err != nil || !revoked {
			t.Fatalf("after RevokeSession, %s revoked = %v, %v; want true, nil", jti, revoked, err)
		}
	}
}

func TestLogoutRevokesSessionBearer(t *testing.T) {
	signer := devSigner()
	store := NewMemorySessionStore()
	audit := NewMemoryAuditEmitter()
	oidc := &fakeOIDC{subject: OIDCSubject{
		UserID:   "dave",
		TenantID: "acme",
		Scope:    "tools:sessions:read",
	}}
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC, OIDCSessionTTL: time.Hour, BearerTTL: 900 * time.Second}, Options{
		Signer:   signer,
		Sessions: store,
		OIDC:     oidc,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
		Metrics:  nil,
	}).WithAuditEmitter(audit)

	// Drive the full login → mint → logout flow.
	pgSrv := httptest.NewServer(h.PlaygroundRoutes())
	defer pgSrv.Close()
	tokenSrv := httptest.NewServer(h.TokenRoutes())
	defer tokenSrv.Close()

	cookie := completeOIDCLogin(t, h, pgSrv, oidc)

	// Mint a bearer carrying the session cookie.
	bearerJTI := mintWithCookie(t, tokenSrv, cookie)

	// The minted bearer is not revoked before logout.
	id, tenant := idTenantForCookie(t, store, cookie)
	revoked, _ := store.IsBearerRevoked(context.Background(), tenant, bearerJTI)
	if revoked {
		t.Fatal("bearer revoked before logout")
	}

	// Logout.
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

	// After logout the session record is gone and the bearer is
	// revoked — §27.3.1 guarantees the revocation write committed
	// before the 200 response.
	if _, err := store.GetSession(context.Background(), tenant, id); err == nil {
		t.Fatal("session record survived logout")
	}
	revoked, err = store.IsBearerRevoked(context.Background(), tenant, bearerJTI)
	if err != nil || !revoked {
		t.Fatalf("after logout, bearer revoked = %v, %v; want true, nil", revoked, err)
	}

	// The §27.3.1 step-6 audit events were emitted.
	var sawMint, sawRevoke bool
	for _, ev := range audit.Events() {
		if ev.Type == "playground.bearer_minted" {
			sawMint = true
		}
		if ev.Type == "playground.bearer_revoked" {
			sawRevoke = true
		}
		// spec: §27.2 line 41 — every playground audit event carries
		// the EffectiveLabels map (origin: "playground" plus any
		// operator-configured entries). F-27.2.1.
		if ev.Labels["origin"] != PlaygroundOrigin {
			t.Fatalf("audit event %q labels[origin] = %q, want %q",
				ev.Type, ev.Labels["origin"], PlaygroundOrigin)
		}
	}
	if !sawMint || !sawRevoke {
		t.Fatalf("audit events: mint=%v revoke=%v, want both true", sawMint, sawRevoke)
	}
}

func TestRevokedBearerCheckSurvivesAcrossHandlers(t *testing.T) {
	// A logout against one Handler instance revokes a bearer that a
	// second Handler instance — sharing the same SessionStore, the
	// way two gateway replicas share Redis — must observe as revoked.
	signer := devSigner()
	store := NewMemorySessionStore()
	oidc := &fakeOIDC{subject: OIDCSubject{UserID: "eve", TenantID: "acme", Scope: "tools:sessions:read"}}

	opts := Options{
		Signer:   signer,
		Sessions: store,
		OIDC:     oidc,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
	}
	cfg := Config{Enabled: true, AuthMode: AuthModeOIDC, OIDCSessionTTL: time.Hour, BearerTTL: 900 * time.Second}
	replicaA := New(cfg, opts)
	replicaB := New(cfg, opts)

	aPg := httptest.NewServer(replicaA.PlaygroundRoutes())
	defer aPg.Close()
	aTok := httptest.NewServer(replicaA.TokenRoutes())
	defer aTok.Close()

	cookie := completeOIDCLogin(t, replicaA, aPg, oidc)
	bearerJTI := mintWithCookie(t, aTok, cookie)
	id, tenant := idTenantForCookie(t, store, cookie)

	// Logout on replica A.
	logoutReq, _ := http.NewRequest(http.MethodPost, aPg.URL+"/playground/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	logoutResp, err := http.DefaultClient.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout on replica A: %v", err)
	}
	_ = logoutResp.Body.Close()

	// Replica B's per-request revocation check observes the revocation.
	revoked, err := replicaB.IsBearerRevoked(context.Background(), tenant, bearerJTI)
	if err != nil || !revoked {
		t.Fatalf("replica B revocation check = %v, %v; want true, nil", revoked, err)
	}
	// Replica B's mint endpoint rejects the now-deleted session.
	if _, err := store.GetSession(context.Background(), tenant, id); err == nil {
		t.Fatal("session record visible to replica B after logout")
	}
}

// completeOIDCLogin drives GET /playground/auth/login then
// /playground/auth/callback and returns the lenny_playground_session
// cookie value the gateway set.
func completeOIDCLogin(t *testing.T, h *Handler, srv *httptest.Server, oidc *fakeOIDC) string {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	loginResp, err := client.Get(srv.URL + "/playground/auth/login")
	if err != nil {
		t.Fatalf("GET login: %v", err)
	}
	_ = loginResp.Body.Close()
	var stateCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == oidcStateCookie {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("login set no state cookie")
	}
	// Recover the state value the gateway minted from the signed cookie.
	cv, err := h.openState(stateCookie.Value)
	if err != nil {
		t.Fatalf("openState: %v", err)
	}
	cbReq, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/playground/auth/callback?code=auth-code&state="+cv.State, nil)
	cbReq.AddCookie(stateCookie)
	cbResp, err := client.Do(cbReq)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", cbResp.StatusCode)
	}
	for _, c := range cbResp.Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c.Value
		}
	}
	t.Fatal("callback set no session cookie")
	return ""
}

// mintWithCookie posts to /v1/playground/token with the session
// cookie and returns the minted bearer's jti.
func mintWithCookie(t *testing.T, tokenSrv *httptest.Server, cookie string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, tokenSrv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mint status = %d, want 200", resp.StatusCode)
	}
	var body tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode mint: %v", err)
	}
	claims := decodeJWTPayload(t, body.BearerToken)
	jti, _ := claims["jti"].(string)
	if jti == "" {
		t.Fatal("minted JWT carried no jti")
	}
	return jti
}

// idTenantForCookie returns the opaque session id (the whole cookie
// value per §27.3.1 line 81) together with the tenant the store indexed
// for it. The cookie no longer embeds the tenant, so the tenant is
// recovered through SessionStore.TenantForSession. F-27.3.8.
func idTenantForCookie(t *testing.T, store SessionStore, cookie string) (id, tenant string) {
	t.Helper()
	id = cookie
	tenant, ok, err := store.TenantForSession(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("TenantForSession(%q) = (%q, ok=%v, err=%v); want a resolved tenant", id, tenant, ok, err)
	}
	return id, tenant
}

// TestEstablishedSessionRecordCarriesOperatorLabels drives a real
// §27.3.1 OIDC login (GET /playground/auth/login then
// /playground/auth/callback) against a Config whose
// playground.sessionLabels carries operator-supplied keys, then reads
// the persisted SessionRecord back out of the SessionStore. The §27.2
// line 41 "Labels applied to playground sessions for audit/accounting"
// contract requires the created record to carry both the load-bearing
// origin=playground entry and every operator-configured key: unlike
// TestSessionRecordCarriesLabelsThroughJSON (a JSON round-trip of a
// hand-built struct literal), this exercises the actual
// establishSession mint path so a regression that drops
// Config.EffectiveLabels() from the write path is caught. F-27.2.1.
func TestEstablishedSessionRecordCarriesOperatorLabels_spec_27_2_41(t *testing.T) {
	signer := devSigner()
	store := NewMemorySessionStore()
	oidc := &fakeOIDC{subject: OIDCSubject{
		UserID:   "carol",
		TenantID: "acme",
		Scope:    "tools:sessions:read",
	}}
	operatorLabels := map[string]string{"environment": "stage", "team": "platform"}
	h := New(Config{
		Enabled:        true,
		AuthMode:       AuthModeOIDC,
		OIDCSessionTTL: time.Hour,
		BearerTTL:      900 * time.Second,
		SessionLabels:  operatorLabels,
	}, Options{
		Signer:   signer,
		Sessions: store,
		OIDC:     oidc,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
	})

	pgSrv := httptest.NewServer(h.PlaygroundRoutes())
	defer pgSrv.Close()

	cookie := completeOIDCLogin(t, h, pgSrv, oidc)
	id, tenant := idTenantForCookie(t, store, cookie)

	rec, err := store.GetSession(context.Background(), tenant, id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if rec.Labels["origin"] != PlaygroundOrigin {
		t.Fatalf("persisted record labels[origin] = %q, want %q", rec.Labels["origin"], PlaygroundOrigin)
	}
	if rec.Labels["environment"] != "stage" {
		t.Fatalf("persisted record labels[environment] = %q, want stage", rec.Labels["environment"])
	}
	if rec.Labels["team"] != "platform" {
		t.Fatalf("persisted record labels[team] = %q, want platform", rec.Labels["team"])
	}
}

// TestBearerMintedAuditEventCarriesOperatorLabels drives a real dev-mode
// mint (POST /v1/playground/token) against a Config whose
// playground.sessionLabels carries operator-supplied keys and asserts
// the emitted playground.bearer_minted audit event's Labels field
// carries both the load-bearing origin=playground entry and the
// operator overrides. TestLogoutRevokesSessionBearer already checks
// every emitted event's origin label; this test additionally exercises
// a Config with multi-key operator labels so a regression that emits
// only the origin entry (dropping the rest of EffectiveLabels()) on
// the mint path is caught. F-27.2.1.
func TestBearerMintedAuditEventCarriesOperatorLabels_spec_27_2_41(t *testing.T) {
	audit := NewMemoryAuditEmitter()
	operatorLabels := map[string]string{"environment": "stage", "team": "platform"}
	h := New(Config{
		Enabled:       true,
		AuthMode:      AuthModeDev,
		DevTenantID:   "acme",
		BearerTTL:     900 * time.Second,
		SessionLabels: operatorLabels,
	}, Options{Signer: devSigner()}).WithAuditEmitter(audit)

	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/playground/token", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mint status = %d, want 200", resp.StatusCode)
	}

	events := audit.Events()
	var minted *AuditEvent
	for i := range events {
		if events[i].Type == "playground.bearer_minted" {
			minted = &events[i]
		}
	}
	if minted == nil {
		t.Fatalf("no playground.bearer_minted event emitted; events=%+v", events)
	}
	if minted.Labels["origin"] != PlaygroundOrigin {
		t.Fatalf("audit event labels[origin] = %q, want %q", minted.Labels["origin"], PlaygroundOrigin)
	}
	if minted.Labels["environment"] != "stage" {
		t.Fatalf("audit event labels[environment] = %q, want stage", minted.Labels["environment"])
	}
	if minted.Labels["team"] != "platform" {
		t.Fatalf("audit event labels[team] = %q, want platform", minted.Labels["team"])
	}
}
