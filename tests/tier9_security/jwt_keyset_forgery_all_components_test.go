// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §10.2 verifier key set on every component
// that verifies Lenny-issued tokens. The real cmd/lenny-gateway,
// cmd/lenny-token-service, and cmd/lenny-ops processes run over the local
// KMS master key of the test's stack, and the test derives the same
// platform:jwt-signing key from it. Each component admits a token that
// key signed and refuses every forgery: an HS256 token keyed with the
// public key (algorithm confusion), an alg none token, a genuine signature
// under a kid that names no key, and tokens signed by a P-256 key outside
// the set, under its own kid and under the genuine kid.
package tier9_security_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// forgeryClaims returns the claims of a default-tenant platform-admin
// bearer for jti, so the token passes lenny-ops's role requirement.
func forgeryClaims(jti string) jwt.Claims {
	now := time.Now()
	return jwt.Claims{
		Subject:  "carol@acme.com",
		TenantID: auth.DefaultTenantID,
		Roles:    []auth.Role{auth.RolePlatformAdmin},
		Typ:      auth.TokenUserBearer,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(10 * time.Minute).Unix(),
		JWTID:    jti,
	}
}

// forgeryPayload returns c as the JSON object a forged token carries.
func forgeryPayload(t *testing.T, c jwt.Claims) map[string]any {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return out
}

// signForgery signs c with signer.
func signForgery(t *testing.T, signer jwt.Signer, c jwt.Claims) string {
	t.Helper()
	tok, err := signer.Sign(context.Background(), c)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// withKID returns tok with its JOSE header's kid replaced by kid and its
// signature kept, so the signature no longer matches the header the
// verifier reads the key from.
func withKID(t *testing.T, tok, kid string) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var h map[string]any
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("parse header: %v", err)
	}
	h["kid"] = kid
	enc, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	parts[0] = base64.RawURLEncoding.EncodeToString(enc)
	return strings.Join(parts, ".")
}

// forgeries returns the tokens no component may accept, keyed by name.
func forgeries(t *testing.T, ks *jwt.KeySet, genuine jwt.Signer, foreign jwt.Signer) map[string]string {
	t.Helper()
	kid := ks.Signing().KeyID
	jwk, err := jwt.ECPublicJWK(ks.Signing().PublicKey)
	if err != nil {
		t.Fatalf("public jwk: %v", err)
	}
	pub, err := json.Marshal(jwk)
	if err != nil {
		t.Fatalf("marshal jwk: %v", err)
	}
	payload := forgeryPayload(t, forgeryClaims("forged-payload"))
	foreignTok := signForgery(t, foreign, forgeryClaims("forged-foreign"))
	return map[string]string{
		"HS256 keyed with the public key":       encodeForged(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid}, payload, pub),
		"alg none":                              encodeForged(t, map[string]any{"alg": "none", "typ": "JWT", "kid": kid}, payload, nil),
		"genuine signature under a forged kid":  withKID(t, signForgery(t, genuine, forgeryClaims("forged-kid")), "forged-kid-matches-no-key"),
		"foreign P-256 key under its own kid":   foreignTok,
		"foreign P-256 key under the set's kid": withKID(t, foreignTok, kid),
	}
}

// forgeryExchange posts a token exchange to the Token Service at base and
// returns the status and decoded body.
func forgeryExchange(t *testing.T, base, caller, subject string) (int, map[string]any) {
	t.Helper()
	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subject},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+caller)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/oauth/token: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// spec: 10.2 (Authentication) "Verifier key set and rotation", 4.3 (Token Service), 25.4 (The lenny-ops Service)
// diagnosis: an admitted forgery means that component accepts a token
// outside its key set: an HS256 or alg none token (algorithm confusion
// gives anyone holding the public key a forgery path), a token whose kid
// names no key, or a token signed by a key the component does not hold,
// including one that claims the set's kid. A refused genuine token means
// the component does not verify through the shared platform:jwt-signing
// key set.
func TestEveryVerifierRefusesForgedLennyTokens_spec_10_2(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	ks, genuine := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	_, foreign := stackkey.Signer(t, filepath.Join(t.TempDir(), "foreign-master.key"), refs)
	alias := "--kms-alias=" + kms.AliasJWTSigning + "=local-key-1"

	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	gw := gateway.StartWith(t, "--dev-mode", "--kms-provider=local", alias)
	ts := tokensvc.StartWith(t, "--kms-provider=local", alias)
	ops := opsprocess.StartWith(t, append([]string{"--postgres-dsn=" + pg.DSN, "--kms-provider=local", alias}, opsprocess.AuthArgs(t)...)...)

	caller := signForgery(t, genuine, forgeryClaims("genuine-caller"))
	subject := signForgery(t, genuine, forgeryClaims("genuine-subject"))
	if code, body := spoofCall(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", caller, nil, nil); code != http.StatusOK {
		t.Fatalf("gateway refused a genuine key-set token: %d %v\n%s", code, body, gw.Logs())
	}
	if code, body := forgeryExchange(t, ts.BaseURL(), caller, subject); code != http.StatusOK {
		t.Fatalf("Token Service refused a genuine caller and subject: %d %v\n%s", code, body, ts.Logs())
	}
	ops.WaitAccepted(t, caller, 30*time.Second)

	for name, forged := range forgeries(t, ks, genuine, foreign) {
		t.Run(name, func(t *testing.T) {
			if code, body := spoofCall(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", forged, nil, nil); code != http.StatusUnauthorized {
				t.Errorf("gateway: status %d (%v), want 401", code, body)
			}
			if code, body := forgeryExchange(t, ts.BaseURL(), forged, subject); code != http.StatusUnauthorized || body["error"] != "invalid_client" {
				t.Errorf("Token Service, forgery as the caller: status %d (%v), want 401 invalid_client", code, body)
			}
			if code, body := forgeryExchange(t, ts.BaseURL(), caller, forged); code != http.StatusBadRequest || body["error"] != "invalid_grant" {
				t.Errorf("Token Service, forgery as the subject: status %d (%v), want 400 invalid_grant", code, body)
			}
			if got := ops.Me(t, forged); got.Status != http.StatusUnauthorized || got.Code != opsprocess.CodeInvalid {
				t.Errorf("lenny-ops: %d %s, want 401 %s", got.Status, got.Body, opsprocess.CodeInvalid)
			}
		})
	}
}
