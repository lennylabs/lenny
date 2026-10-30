// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §10.2 gateway bearer verifier. It runs the
// real cmd/lenny-gateway binary on the local KMS provider with a
// file-backed master key, derives the same platform:jwt-signing key in the
// test from that master key, and checks that the gateway admits an ES256
// token signed by that key and refuses every token outside the key set:
// an HS256 token keyed with the public key (the algorithm-confusion
// forgery), an alg none token, and an ES256 token signed by a key the
// gateway does not hold. A second gateway with a different master key
// refuses the first gateway's token.
package tier9_security_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// keySetSigningRef is the platform:jwt-signing reference every process in
// this test maps, so the local provider derives one key from one master
// key.
const keySetSigningRef = "local-key-1"

// masterKeySigner derives the platform:jwt-signing key from the master key
// file at path the way the gateway's local provider does, and returns the
// key set and remote signer over it.
func masterKeySigner(t *testing.T, path string) (*jwt.KeySet, *jwt.RemoteSigner) {
	t.Helper()
	local, err := kms.NewLocalFromKeyFile(path)
	if err != nil {
		t.Fatalf("load master key: %v", err)
	}
	local.SetSigningReference(kms.AliasJWTSigning, keySetSigningRef)
	ks, err := jwt.LoadKeySet(context.Background(), local, map[string]string{kms.AliasJWTSigning: keySetSigningRef})
	if err != nil {
		t.Fatalf("load key set: %v", err)
	}
	signer, err := jwt.NewRemoteSigner(local, ks.Signing())
	if err != nil {
		t.Fatalf("remote signer: %v", err)
	}
	return ks, signer
}

// startKeySetGateway runs a gateway on the local provider over the master
// key file at path.
func startKeySetGateway(t *testing.T, path string) *gateway.Process {
	t.Helper()
	return gateway.StartWith(t, "--dev-mode",
		"--kms-provider=local",
		"--kms-master-key-file="+path,
		"--kms-alias="+kms.AliasJWTSigning+"="+keySetSigningRef)
}

// keySetUserClaims returns the payload of a default-tenant user bearer.
func keySetUserClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"sub":       "alice@acme.com",
		"tenant_id": auth.DefaultTenantID,
		"roles":     []string{string(auth.RoleUser)},
		"typ":       string(auth.TokenUserBearer),
		"iat":       now.Unix(),
		"exp":       now.Add(10 * time.Minute).Unix(),
		"jti":       "keyset-" + now.Format(time.RFC3339Nano),
	}
}

// signKeySetToken signs the default-tenant user bearer with signer.
func signKeySetToken(t *testing.T, signer jwt.Signer) string {
	t.Helper()
	now := time.Now()
	tok, err := signer.Sign(context.Background(), jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: auth.DefaultTenantID,
		Roles:    []auth.Role{auth.RoleUser},
		Typ:      auth.TokenUserBearer,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(10 * time.Minute).Unix(),
		JWTID:    "keyset-" + now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// encodeForged encodes a token with the given header, signing the input
// with HMAC-SHA256 under secret when secret is non-nil.
func encodeForged(t *testing.T, header, payload map[string]any, secret []byte) string {
	t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	if secret == nil {
		return signing + "."
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// spec: 10.2 (Authentication) "Verifier key set and rotation", 10.2 (Authentication) "Session capability context"
// diagnosis: a failure on the admitted case means the gateway's bearer path
// does not verify through the platform:jwt-signing key set, so a token
// another replica or the Token Service signs with the shared key is
// refused. A failure on a refused case means the gateway accepts a token
// outside its key set: an HS256 or alg none token (algorithm confusion
// opens a forgery path to anyone holding the public key), or an ES256
// token signed by a key it does not hold, including the token of a
// gateway that runs on another master key.
func TestGatewayBearerPathVerifiesOnlyThroughKeySet_spec_10_2(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	dir := t.TempDir()
	sharedKey := filepath.Join(dir, "master.key")
	otherKey := filepath.Join(dir, "other-master.key")
	// Create both master keys before any process starts, so the gateway
	// and the test load the same file contents.
	if _, err := kms.LoadOrCreateMasterKey(sharedKey); err != nil {
		t.Fatalf("create master key: %v", err)
	}
	if _, err := kms.LoadOrCreateMasterKey(otherKey); err != nil {
		t.Fatalf("create other master key: %v", err)
	}
	ks, signer := masterKeySigner(t, sharedKey)
	_, foreignSigner := masterKeySigner(t, otherKey)

	gw := startKeySetGateway(t, sharedKey)
	other := startKeySetGateway(t, otherKey)
	me := gw.BaseURL() + "/v1/admin/me"

	genuine := signKeySetToken(t, signer)
	if code, body := spoofCall(t, http.MethodGet, me, genuine, nil, nil); code != http.StatusOK || body["tenantId"] != auth.DefaultTenantID {
		t.Fatalf("key-set token: status %d (%v), want 200 as %s\n%s", code, body, auth.DefaultTenantID, gw.Logs())
	}

	pub, err := jwt.ECPublicJWK(ks.Signing().PublicKey)
	if err != nil {
		t.Fatalf("public jwk: %v", err)
	}
	pubBytes, err := json.Marshal(pub)
	if err != nil {
		t.Fatalf("marshal jwk: %v", err)
	}
	kid := ks.Signing().KeyID
	refused := map[string]string{
		"HS256 keyed with the public key": encodeForged(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid}, keySetUserClaims(), pubBytes),
		"alg none":                        encodeForged(t, map[string]any{"alg": "none", "typ": "JWT", "kid": kid}, keySetUserClaims(), nil),
		"ES256 from a foreign key":        signKeySetToken(t, foreignSigner),
	}
	for name, tok := range refused {
		if code, body := spoofCall(t, http.MethodGet, me, tok, nil, nil); code != http.StatusUnauthorized {
			t.Errorf("%s: status %d (%v), want 401", name, code, body)
		}
	}
	if code, body := spoofCall(t, http.MethodGet, other.BaseURL()+"/v1/admin/me", genuine, nil, nil); code != http.StatusUnauthorized {
		t.Errorf("gateway on another master key: status %d (%v), want 401", code, body)
	}
}
