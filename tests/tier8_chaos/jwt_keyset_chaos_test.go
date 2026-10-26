// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for the §10.2 shared signing key under dependency
// loss, against real cmd/lenny-gateway, cmd/lenny-token-service, and
// cmd/lenny-ops processes.
//
// The lenny-ops case injects a Postgres outage through an in-process TCP
// proxy (tests/testinfra/tcpfault) between lenny-ops and a migrated
// Postgres, so the outage breaks lenny-ops's connections and refuses new
// ones while the data survives for the restore. It checks the §25.4
// revocation set rules: after a successful read the set stays in force
// through failed reads, a lenny-ops that starts during the outage refuses
// every Lenny-issued token, and acceptance resumes after the next read.
//
// The KMS case runs the gateway and the Token Service on the AWS KMS
// provider against a local AWS KMS protocol server
// (tests/testinfra/stubs/kms.AWSServer) and makes it fail every request.
// It checks the §10.2 KMS signing failure mode: mints fail with 503
// KMS_SIGNING_UNAVAILABLE, the breaker opens with Retry-After equal to the
// remaining cooldown and stops calling the KMS, pre-fault tokens still
// verify, and signing recovers through the half-open probe.
package tier8_chaos_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// keySetChaosReadBound bounds the wait for one lenny-ops revocation set
// read: the 30 s interval plus margin.
const keySetChaosReadBound = 35 * time.Second

// keySetChaosStaleWindow exceeds the 90 s after which the gateway's
// revocation cache reports stale. A lenny-ops that gated on that signal
// would start refusing every Lenny-issued token inside the window.
const keySetChaosStaleWindow = 95 * time.Second

// keySetChaosStartPostgres starts a migrated Postgres.
func keySetChaosStartPostgres(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
}

// keySetChaosMint signs a default-tenant platform-admin bearer for jti with
// signer and records its issued_tokens row through store.
func keySetChaosMint(t *testing.T, signer jwt.Signer, store *issuedtokenstore.Store, jti string) string {
	t.Helper()
	now := time.Now()
	claims := jwt.Claims{
		Subject:  "carol@acme.com",
		TenantID: auth.DefaultTenantID,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(time.Hour).Unix(),
		JWTID:    jti,
		Typ:      auth.TokenUserBearer,
		Roles:    []auth.Role{auth.RolePlatformAdmin},
	}
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("sign %s: %v", jti, err)
	}
	if err := store.Record(context.Background(), issuedtokenstore.IssuedToken{
		JTI: jti, TenantID: auth.DefaultTenantID, Subject: claims.Subject,
		TokenHash: []byte(jti), IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("record %s: %v", jti, err)
	}
	return tok
}

// spec: 25.4 (The lenny-ops Service), 25.15 (Failure Mode Analysis), 13.3 (Credential Flow)
// diagnosis: the non-revoked token refused during the outage means
// lenny-ops refuses Lenny-issued tokens once a read fails, or once the
// cache reports stale after 90 s, which is the rejected all-refuse posture;
// the revoked token admitted means the revocation set does not stay in
// force through failed reads. A token admitted after the restart with
// Postgres down means lenny-ops accepts a Lenny-issued token before any
// read succeeded. A refusal after Postgres returns means the read loop
// stopped on the failed reads.
func TestOpsRevocationSetUnderPostgresLoss(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)
	pg := keySetChaosStartPostgres(t)
	pgProxy, proxiedDSN := tcpfault.StartURL(t, pg.DSN)
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	_, signer := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	store := issuedtokenstore.New(pg.Pool)
	kept := keySetChaosMint(t, signer, store, "chaos-kept")
	revoked := keySetChaosMint(t, signer, store, "chaos-revoked")
	if err := store.Revoke(context.Background(), auth.DefaultTenantID, "chaos-revoked", "chaos", time.Now()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	opts := opsprocess.Options{
		Binary: opsprocess.Build(t),
		Args: append([]string{
			"--postgres-dsn=" + proxiedDSN, "--kms-provider=local",
			"--kms-alias=" + kms.AliasJWTSigning + "=local-key-1",
		}, opsprocess.AuthArgs(t)...),
	}
	ops := opsprocess.StartOpts(t, opts)
	ops.WaitAccepted(t, kept, keySetChaosReadBound)
	ops.WaitRefused(t, revoked, http.StatusUnauthorized, opsprocess.CodeRevoked, keySetChaosReadBound)

	pgProxy.Cut()
	for end := time.Now().Add(keySetChaosStaleWindow); time.Now().Before(end); time.Sleep(5 * time.Second) {
		if got := ops.Me(t, kept); got.Status != http.StatusOK {
			t.Fatalf("non-revoked token during the outage: %d %s, want 200", got.Status, got.Body)
		}
		if got := ops.Me(t, revoked); got.Status != http.StatusUnauthorized || got.Code != opsprocess.CodeRevoked {
			t.Fatalf("revoked token during the outage: %d %s, want 401 %s", got.Status, got.Body, opsprocess.CodeRevoked)
		}
	}

	ops.Stop(t)
	ops = opsprocess.StartOpts(t, opts)
	ops.RequireRefusedThroughout(t, kept, http.StatusServiceUnavailable, opsprocess.CodeStale, 10*time.Second)
	ops.RequireRefusedThroughout(t, revoked, http.StatusServiceUnavailable, opsprocess.CodeStale, 5*time.Second)

	pgProxy.Restore(t)
	ops.WaitAccepted(t, kept, keySetChaosReadBound)
	if got := ops.Me(t, revoked); got.Status != http.StatusUnauthorized || got.Code != opsprocess.CodeRevoked {
		t.Errorf("revoked token after Postgres returned: %d %s, want 401 %s", got.Status, got.Body, opsprocess.CodeRevoked)
	}
}

// keySetChaosResponse is one HTTP answer.
type keySetChaosResponse struct {
	status     int
	retryAfter string
	body       map[string]any
}

// code returns error.code of an error envelope.
func (r keySetChaosResponse) code() string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// keySetChaosDo issues one request. A non-empty bearer is sent as the
// Authorization header; otherwise the dev headers name a platform-admin
// on the default tenant. A non-nil form is sent form-encoded, otherwise
// body as JSON.
func keySetChaosDo(t *testing.T, method, target, bearer string, body any, form url.Values) keySetChaosResponse {
	t.Helper()
	var reader io.Reader
	contentType := "application/json"
	if form != nil {
		reader = strings.NewReader(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	} else if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else {
		req.Header.Set("X-Lenny-Tenant-ID", auth.DefaultTenantID)
		req.Header.Set("X-Lenny-User-ID", "carol@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return keySetChaosResponse{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"), body: out}
}

// keySetChaosImpersonate asks gw to mint an impersonation bearer for
// alice@acme.com.
func keySetChaosImpersonate(t *testing.T, gw *gateway.Process) keySetChaosResponse {
	t.Helper()
	return keySetChaosDo(t, http.MethodPost, gw.BaseURL()+"/v1/admin/impersonation", "", map[string]any{
		"targetTenantId": "acme", "targetUserId": "alice@acme.com",
		"reason": "kms outage chaos probe", "ticketId": "kms-chaos", "durationSeconds": 600,
	}, nil)
}

// keySetChaosExchange asks the Token Service at base for a token exchange
// with caller as the caller token.
func keySetChaosExchange(t *testing.T, base, caller, subject string) keySetChaosResponse {
	t.Helper()
	return keySetChaosDo(t, http.MethodPost, base+"/v1/oauth/token", caller, nil, url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subject},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
	})
}

// keySetChaosKID returns the kid of a compact JWS.
func keySetChaosKID(t *testing.T, tok string) string {
	t.Helper()
	header, _, _ := strings.Cut(tok, ".")
	raw, err := base64.RawURLEncoding.DecodeString(header)
	if err != nil {
		t.Fatalf("decode JOSE header: %v", err)
	}
	var h struct {
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("parse JOSE header: %v", err)
	}
	return h.Kid
}

// requireSigningUnavailable asserts r is 503 KMS_SIGNING_UNAVAILABLE with
// no token, and returns its Retry-After in seconds.
func requireSigningUnavailable(t *testing.T, what string, r keySetChaosResponse) int {
	t.Helper()
	if r.status != http.StatusServiceUnavailable || r.code() != "KMS_SIGNING_UNAVAILABLE" {
		t.Fatalf("%s: status %d body %v, want 503 KMS_SIGNING_UNAVAILABLE", what, r.status, r.body)
	}
	if _, ok := r.body["bearerToken"]; ok {
		t.Fatalf("%s: a failed mint returned a bearer: %v", what, r.body)
	}
	if _, ok := r.body["access_token"]; ok {
		t.Fatalf("%s: a failed mint returned an access_token: %v", what, r.body)
	}
	secs, err := strconv.Atoi(r.retryAfter)
	if err != nil || secs < 1 {
		t.Fatalf("%s: Retry-After %q, want whole seconds of at least 1", what, r.retryAfter)
	}
	return secs
}

// tripSigningBreaker sends mints through mint while the KMS fails until
// the breaker is open, and checks the open-breaker refusal: Retry-After
// is the remaining cooldown, falling as time passes, and no Sign request
// reaches the KMS. It returns the last Retry-After.
func tripSigningBreaker(t *testing.T, who string, stub *kmsstub.AWSServer, mint func() keySetChaosResponse) int {
	t.Helper()
	// More than SigningBreakerThreshold consecutive hard failures inside
	// the window trip the breaker; each is answered 503.
	for i := 0; i <= jwt.SigningBreakerThreshold; i++ {
		requireSigningUnavailable(t, who+" mint during the KMS outage", mint())
	}
	before := stub.SignRequests()
	first := requireSigningUnavailable(t, who+" mint with the breaker open", mint())
	if first <= 1 || first > int(jwt.SigningBreakerCooldown.Seconds()) {
		t.Fatalf("%s: open-breaker Retry-After %d, want the remaining cooldown in (1, %d]", who, first, int(jwt.SigningBreakerCooldown.Seconds()))
	}
	time.Sleep(3 * time.Second)
	later := requireSigningUnavailable(t, who+" mint with the breaker open, 3 s later", mint())
	if later >= first {
		t.Errorf("%s: Retry-After went from %d to %d over 3 s, want the remaining cooldown to fall", who, first, later)
	}
	if n := stub.SignRequests() - before; n != 0 {
		t.Errorf("%s: %d Sign requests reached the KMS while the breaker was open, want 0", who, n)
	}
	return later
}

// spec: 10.2 (Authentication) "KMS signing failure mode", 11.6 (Circuit Breakers), 15.1 (REST API)
// diagnosis: a mint other than 503 KMS_SIGNING_UNAVAILABLE during the
// outage means a signing failure surfaces as another error or issues a
// token. An open-breaker Retry-After that is not the remaining cooldown, or
// a Sign request reaching the KMS while the breaker is open, means the
// breaker does not short-circuit. A pre-fault token refused means
// verification depends on the KMS. A mint still failing after the cooldown,
// or a token whose kid is not the KMS key's thumbprint, means the half-open
// probe does not close the breaker or a token was signed under another key.
func TestJWTSigningKMSOutageBreakerAndRecovery(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	pg := keySetChaosStartPostgres(t)
	stub := kmsstub.NewAWSServer(t)
	ts := tokensvc.StartOpts(t, tokensvc.Options{
		Args: append([]string{"--postgres-dsn=" + pg.DSN}, stub.Args("acme")...),
		Env:  stub.Env(),
	})
	gw := gateway.StartOpts(t, gateway.Options{
		Args: append([]string{
			"--no-environment-policy", "allow-all", "--dev-mode",
			"--postgres-dsn=" + pg.DSN, "--postgres-billing-audit-ddl-dsn=" + pg.DSN,
			"--tenancy-mode=multi", "--multi-tenant=true",
			"--token-service-http-url=" + ts.BaseURL(),
		}, stub.Args("acme")...),
		Env: stub.Env(),
	})
	if r := keySetChaosDo(t, http.MethodPost, gw.BaseURL()+"/v1/admin/bootstrap", "", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
	}, nil); r.status != http.StatusOK {
		t.Fatalf("bootstrap: %d %v\n%s", r.status, r.body, gw.Logs())
	}
	if r := keySetChaosDo(t, http.MethodPost, gw.BaseURL()+"/v1/admin/users", "", map[string]any{
		"tenantId": "acme", "subject": "alice@acme.com", "roles": []string{"user"},
	}, nil); r.status != http.StatusCreated {
		t.Fatalf("create alice: %d %v", r.status, r.body)
	}
	bearerOf := func(r keySetChaosResponse) string {
		tok, _ := r.body["bearerToken"].(string)
		if r.status != http.StatusOK || tok == "" {
			t.Fatalf("impersonation mint: %d %v\n%s", r.status, r.body, gw.Logs())
		}
		return tok
	}
	preA := bearerOf(keySetChaosImpersonate(t, gw))
	preB := bearerOf(keySetChaosImpersonate(t, gw))
	if r := keySetChaosExchange(t, ts.BaseURL(), preA, preB); r.status != http.StatusOK {
		t.Fatalf("pre-fault exchange: %d %v\n%s", r.status, r.body, ts.Logs())
	}

	stub.SetFailing(true)
	gwWait := tripSigningBreaker(t, "gateway", stub, func() keySetChaosResponse { return keySetChaosImpersonate(t, gw) })
	tsWait := tripSigningBreaker(t, "Token Service", stub, func() keySetChaosResponse {
		return keySetChaosExchange(t, ts.BaseURL(), preA, preB)
	})
	if r := keySetChaosDo(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", preA, nil, nil); r.status != http.StatusOK {
		t.Errorf("pre-fault bearer during the KMS outage: %d %v, want 200", r.status, r.body)
	}

	stub.SetFailing(false)
	time.Sleep(time.Duration(max(gwWait, tsWait)+1) * time.Second)
	jwk, err := jwt.ECPublicJWK(stub.PublicKey())
	if err != nil {
		t.Fatalf("stub jwk: %v", err)
	}
	wantKID, err := jwk.Thumbprint()
	if err != nil {
		t.Fatalf("stub thumbprint: %v", err)
	}
	for i := range 2 {
		minted := bearerOf(keySetChaosImpersonate(t, gw))
		if kid := keySetChaosKID(t, minted); kid != wantKID {
			t.Errorf("gateway mint %d after recovery has kid %q, want the KMS key's %q", i, kid, wantKID)
		}
		if r := keySetChaosDo(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", minted, nil, nil); r.status != http.StatusOK {
			t.Errorf("gateway mint %d after recovery does not verify: %d %v", i, r.status, r.body)
		}
		r := keySetChaosExchange(t, ts.BaseURL(), preA, preB)
		tok, _ := r.body["access_token"].(string)
		if r.status != http.StatusOK || tok == "" {
			t.Fatalf("Token Service mint %d after recovery: %d %v", i, r.status, r.body)
		}
		if kid := keySetChaosKID(t, tok); kid != wantKID {
			t.Errorf("Token Service mint %d after recovery has kid %q, want the KMS key's %q", i, kid, wantKID)
		}
	}
}
