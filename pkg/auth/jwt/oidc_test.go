// SPDX-License-Identifier: MIT

package jwt_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// testIdP is an in-process OIDC provider: a discovery document, a JWK
// Set, and signing keys a test can rotate, plus switches that make each
// endpoint fail.
type testIdP struct {
	t      *testing.T
	server *httptest.Server
	// issuerOverride, when set, is the issuer the discovery document
	// advertises instead of the server URL.
	issuerOverride string

	mu   sync.Mutex
	keys map[string]crypto.Signer // kid -> private key
	// extraJWKs are published verbatim alongside keys.
	extraJWKs     []map[string]any
	failDiscovery bool
	failJWKS      bool

	discoveryHits atomic.Int64
	jwksHits      atomic.Int64
}

func newTestIdP(t *testing.T) *testIdP {
	t.Helper()
	idp := &testIdP{t: t, keys: map[string]crypto.Signer{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		idp.discoveryHits.Add(1)
		idp.mu.Lock()
		fail, issuer := idp.failDiscovery, idp.issuerOverride
		idp.mu.Unlock()
		if fail {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		if issuer == "" {
			issuer = idp.issuer()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "jwks_uri": idp.issuer() + "/keys"})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		idp.jwksHits.Add(1)
		idp.mu.Lock()
		defer idp.mu.Unlock()
		if idp.failJWKS {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		var keys []map[string]any
		for kid, k := range idp.keys {
			keys = append(keys, publicJWK(kid, k.Public()))
		}
		keys = append(keys, idp.extraJWKs...)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	})
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (i *testIdP) issuer() string { return i.server.URL }

func (i *testIdP) addRSA(kid string) {
	i.t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		i.t.Fatalf("generate RSA key: %v", err)
	}
	i.set(kid, k)
}

func (i *testIdP) addEC(kid string, curve elliptic.Curve) {
	i.t.Helper()
	k, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		i.t.Fatalf("generate EC key: %v", err)
	}
	i.set(kid, k)
}

func (i *testIdP) set(kid string, k crypto.Signer) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.keys[kid] = k
}

func (i *testIdP) remove(kid string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.keys, kid)
}

func (i *testIdP) setFail(discovery, jwks bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.failDiscovery, i.failJWKS = discovery, jwks
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func publicJWK(kid string, pub crypto.PublicKey) map[string]any {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return map[string]any{"kty": "RSA", "use": "sig", "kid": kid, "n": b64u(k.N.Bytes()), "e": b64u(big.NewInt(int64(k.E)).Bytes())}
	case *ecdsa.PublicKey:
		size := (k.Curve.Params().BitSize + 7) / 8
		return map[string]any{
			"kty": "EC", "use": "sig", "kid": kid, "crv": k.Curve.Params().Name,
			"x": b64u(k.X.FillBytes(make([]byte, size))), "y": b64u(k.Y.FillBytes(make([]byte, size))),
		}
	}
	return nil
}

// sign signs payload with the key kid under alg. headerKid overrides
// the header kid ("-" omits it).
func (i *testIdP) sign(alg, kid, headerKid string, payload map[string]any) string {
	i.t.Helper()
	i.mu.Lock()
	key := i.keys[kid]
	i.mu.Unlock()
	header := map[string]any{"alg": alg, "typ": "JWT"}
	switch headerKid {
	case "":
		header["kid"] = kid
	case "-":
	default:
		header["kid"] = headerKid
	}
	return signWith(i.t, alg, key, header, payload)
}

func signWith(t *testing.T, alg string, key crypto.Signer, header, payload map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	input := b64u(hb) + "." + b64u(pb)
	hash := map[string]crypto.Hash{"256": crypto.SHA256, "384": crypto.SHA384, "512": crypto.SHA512}[alg[2:]]
	h := hash.New()
	h.Write([]byte(input))
	digest := h.Sum(nil)
	var sig []byte
	var err error
	switch k := key.(type) {
	case *rsa.PrivateKey:
		if strings.HasPrefix(alg, "PS") {
			sig, err = rsa.SignPSS(rand.Reader, k, hash, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		} else {
			sig, err = rsa.SignPKCS1v15(rand.Reader, k, hash, digest)
		}
	case *ecdsa.PrivateKey:
		var r, s *big.Int
		r, s, err = ecdsa.Sign(rand.Reader, k, digest)
		size := (k.Curve.Params().BitSize + 7) / 8
		sig = append(r.FillBytes(make([]byte, size)), s.FillBytes(make([]byte, size))...)
	}
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return input + "." + b64u(sig)
}

// idTokenClaims returns a valid ID-token payload for alice at idp.
func (i *testIdP) idTokenClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": i.issuer(), "sub": "alice@acme.com", "aud": "lenny-gateway",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "tenant_id": "acme",
	}
}

// oidcClock is a settable clock.
type oidcClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *oidcClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *oidcClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newOIDCVerifier(t *testing.T, idp *testIdP, clock *oidcClock) *jwt.OIDCVerifier {
	t.Helper()
	v, err := jwt.NewOIDCVerifier(jwt.OIDCConfig{
		IssuerURL:          idp.issuer(),
		Audience:           "lenny-gateway",
		KeyRefreshInterval: time.Hour,
		MinRefetchInterval: 30 * time.Second,
		Now:                clock.Now,
	})
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}
	return v
}

func verifyReason(err error) string {
	var ve *jwt.VerifyError
	if errors.As(err, &ve) {
		return ve.Reason
	}
	if err == nil {
		return ""
	}
	return "non-VerifyError: " + err.Error()
}

// spec: 10.2 (Authentication: the Client → Gateway OIDC boundary and the
// `user_bearer` type)
func TestOIDCVerifierAcceptsIdPTokenAcrossAlgorithms_spec_10_2(t *testing.T) {
	idp := newTestIdP(t)
	idp.addRSA("rsa-1")
	idp.addEC("ec-256", elliptic.P256())
	idp.addEC("ec-384", elliptic.P384())
	idp.addEC("ec-521", elliptic.P521())
	clock := &oidcClock{now: time.Now()}
	v := newOIDCVerifier(t, idp, clock)
	for _, c := range []struct{ alg, kid string }{
		{"RS256", "rsa-1"},
		{"RS384", "rsa-1"},
		{"RS512", "rsa-1"},
		{"PS256", "rsa-1"},
		{"PS512", "rsa-1"},
		{"ES256", "ec-256"},
		{"ES384", "ec-384"},
		{"ES512", "ec-521"},
	} {
		tok := idp.sign(c.alg, c.kid, "", idp.idTokenClaims(clock.Now()))
		claims, err := v.Verify(tok)
		if err != nil {
			t.Errorf("%s: Verify: %v", c.alg, err)
			continue
		}
		if claims.Subject != "alice@acme.com" || claims.Typ != auth.TokenUserBearer {
			t.Errorf("%s: claims sub=%q typ=%q, want alice@acme.com user_bearer", c.alg, claims.Subject, claims.Typ)
		}
		if len(claims.Audience) != 1 || claims.Audience[0] != "lenny-gateway" {
			t.Errorf("%s: string aud decoded as %v, want [lenny-gateway]", c.alg, claims.Audience)
		}
		if claims.ClaimString("tenant_id") != "acme" {
			t.Errorf("%s: tenant_id claim %q, want acme", c.alg, claims.ClaimString("tenant_id"))
		}
	}
	if got := idp.discoveryHits.Load(); got != 1 {
		t.Errorf("discovery fetched %d times across verifications, want 1", got)
	}
}

// spec: 10.2 (Authentication: `typ` "boundary-normalization step")
func TestOIDCVerifierNormalizesToUserBearer_spec_10_2(t *testing.T) {
	idp := newTestIdP(t)
	idp.addRSA("k1")
	clock := &oidcClock{now: time.Now()}
	v := newOIDCVerifier(t, idp, clock)
	p := idp.idTokenClaims(clock.Now())
	p["typ"] = "session_capability"
	p["session_id"] = "sess-forged"
	p["delegation_depth"] = 3
	p["origin"] = "playground"
	p["aud"] = []string{"other", "lenny-gateway"}
	claims, err := v.Verify(idp.sign("RS256", "k1", "", p))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Typ != auth.TokenUserBearer || claims.SessionID != "" || claims.DelegationDepth != 0 || claims.Origin != "" {
		t.Fatalf("normalized claims typ=%q session_id=%q depth=%d origin=%q, want user_bearer with no session, depth, or origin",
			claims.Typ, claims.SessionID, claims.DelegationDepth, claims.Origin)
	}
}

// spec: 10.2 (Authentication: "signature, `iss`, `aud`, `exp`, `nbf`")
func TestOIDCVerifierRejectsInvalidTokens_spec_10_2(t *testing.T) {
	idp := newTestIdP(t)
	idp.addRSA("k1")
	idp.addEC("ec", elliptic.P256())
	clock := &oidcClock{now: time.Now()}
	v := newOIDCVerifier(t, idp, clock)
	now := clock.Now()
	mut := func(f func(map[string]any)) map[string]any {
		p := idp.idTokenClaims(now)
		f(p)
		return p
	}
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	forged := signWith(t, "RS256", otherKey, map[string]any{"alg": "RS256", "kid": "k1"}, idp.idTokenClaims(now))
	valid := idp.sign("RS256", "k1", "", idp.idTokenClaims(now))
	parts := strings.Split(valid, ".")
	// An HS256 token keyed with the IdP's public modulus: the classic
	// algorithm-confusion attack.
	hsInput := b64u([]byte(`{"alg":"HS256","kid":"k1"}`)) + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(parts[0]))
	mac.Write([]byte(hsInput))
	hs := hsInput + "." + b64u(mac.Sum(nil))
	none := b64u([]byte(`{"alg":"none"}`)) + "." + parts[1] + "."
	crit := signWith(t, "RS256", idpKey(idp, "k1"), map[string]any{"alg": "RS256", "kid": "k1", "crit": []string{"x"}}, idp.idTokenClaims(now))

	cases := []struct {
		name, tok, reason string
	}{
		{"wrong issuer", idp.sign("RS256", "k1", "", mut(func(p map[string]any) { p["iss"] = "https://idp.globex.test" })), "issuer_mismatch"},
		{"wrong audience", idp.sign("RS256", "k1", "", mut(func(p map[string]any) { p["aud"] = "other-client" })), "audience_mismatch"},
		{"no audience", idp.sign("RS256", "k1", "", mut(func(p map[string]any) { delete(p, "aud") })), "audience_mismatch"},
		{"expired", idp.sign("RS256", "k1", "", mut(func(p map[string]any) { p["exp"] = now.Add(-time.Minute).Unix() })), "expired"},
		{"not yet valid", idp.sign("RS256", "k1", "", mut(func(p map[string]any) { p["nbf"] = now.Add(time.Minute).Unix() })), "not_yet_valid"},
		{"no exp", idp.sign("RS256", "k1", "", mut(func(p map[string]any) { delete(p, "exp") })), "missing_exp"},
		{"foreign key", forged, "signature_mismatch"},
		{"tampered payload", parts[0] + "." + b64u([]byte(`{"iss":"x"}`)) + "." + parts[2], "signature_mismatch"},
		{"HS256 confusion", hs, "unsupported_alg"},
		{"alg none", none, "unsupported_alg"},
		{"alg does not match key type", idp.sign("ES256", "ec", "k1", idp.idTokenClaims(now)), "unsupported_alg"},
		{"critical extension", crit, "malformed"},
		{"two segments", "a.b", "malformed"},
		{"header not base64", "!!." + parts[1] + "." + parts[2], "malformed"},
		{"payload not JSON", parts[0] + "." + b64u([]byte("nope")) + "." + parts[2], "signature_mismatch"},
		{"ambiguous missing kid", idp.sign("RS256", "k1", "-", idp.idTokenClaims(now)), ""},
	}
	for _, c := range cases {
		_, err := v.Verify(c.tok)
		if c.reason == "" {
			// One RSA key matches RS256, so a kid-less token selects it.
			if err != nil {
				t.Errorf("%s: Verify: %v, want accepted (single compatible key)", c.name, err)
			}
			continue
		}
		if got := verifyReason(err); got != c.reason {
			t.Errorf("%s: reason %q (%v), want %q", c.name, got, err, c.reason)
		}
	}
}

func idpKey(idp *testIdP, kid string) crypto.Signer {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	return idp.keys[kid]
}

// spec: 10.2 (Authentication: the Client → Gateway OIDC boundary)
func TestOIDCVerifierMissingKidWithSeveralCompatibleKeys_spec_10_2(t *testing.T) {
	idp := newTestIdP(t)
	idp.addRSA("k1")
	idp.addRSA("k2")
	clock := &oidcClock{now: time.Now()}
	v := newOIDCVerifier(t, idp, clock)
	_, err := v.Verify(idp.sign("RS256", "k1", "-", idp.idTokenClaims(clock.Now())))
	if got := verifyReason(err); got != "missing_kid" {
		t.Fatalf("kid-less token with two RSA keys: reason %q, want missing_kid", got)
	}
}

// spec: 10.2 (Authentication: the Client → Gateway OIDC boundary)
func TestOIDCVerifierFollowsIdPKeyRotation_spec_10_2(t *testing.T) {
	idp := newTestIdP(t)
	idp.addRSA("old")
	clock := &oidcClock{now: time.Now()}
	v := newOIDCVerifier(t, idp, clock)
	if _, err := v.Verify(idp.sign("RS256", "old", "", idp.idTokenClaims(clock.Now()))); err != nil {
		t.Fatalf("old-key token: %v", err)
	}
	// The IdP rotates. A token under the new kid triggers one refetch,
	// but only after the minimum refetch interval.
	idp.addRSA("new")
	newTok := func() string { return idp.sign("RS256", "new", "", idp.idTokenClaims(clock.Now())) }
	if _, err := v.Verify(newTok()); verifyReason(err) != "unknown_kid" {
		t.Fatalf("new kid inside the refetch interval: %v, want unknown_kid", err)
	}
	hits := idp.jwksHits.Load()
	for range 5 {
		_, _ = v.Verify(newTok())
	}
	if idp.jwksHits.Load() != hits {
		t.Fatalf("unknown-kid tokens inside the refetch interval fetched the JWK Set %d more times, want 0", idp.jwksHits.Load()-hits)
	}
	clock.Advance(31 * time.Second)
	if _, err := v.Verify(newTok()); err != nil {
		t.Fatalf("new kid after the refetch interval: %v", err)
	}
	// After the scheduled refresh the retired key no longer verifies.
	later := idp.idTokenClaims(clock.Now())
	later["exp"] = clock.Now().Add(3 * time.Hour).Unix()
	oldTok := idp.sign("RS256", "old", "", later)
	idp.remove("old")
	clock.Advance(time.Hour)
	if _, err := v.Verify(idp.sign("RS256", "new", "", idp.idTokenClaims(clock.Now()))); err != nil {
		t.Fatalf("new kid after scheduled refresh: %v", err)
	}
	if _, err := v.Verify(oldTok); verifyReason(err) != "unknown_kid" {
		t.Fatalf("retired kid after scheduled refresh: %v, want unknown_kid", err)
	}
}

// spec: 10.2 (Authentication: the Client → Gateway OIDC boundary)
func TestOIDCVerifierIdPOutage_spec_10_2(t *testing.T) {
	idp := newTestIdP(t)
	idp.addRSA("k1")
	clock := &oidcClock{now: time.Now()}
	v := newOIDCVerifier(t, idp, clock)
	idp.setFail(true, false)
	tok := func() string { return idp.sign("RS256", "k1", "", idp.idTokenClaims(clock.Now())) }
	if _, err := v.Verify(tok()); verifyReason(err) != "jwks_unavailable" {
		t.Fatalf("discovery down at first use: %v, want jwks_unavailable", err)
	}
	idp.setFail(false, true)
	clock.Advance(31 * time.Second)
	if _, err := v.Verify(tok()); verifyReason(err) != "jwks_unavailable" {
		t.Fatalf("JWK Set down at first use: %v, want jwks_unavailable", err)
	}
	idp.setFail(false, false)
	clock.Advance(31 * time.Second)
	if _, err := v.Verify(tok()); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	// A failed scheduled refresh keeps the cached keys.
	idp.setFail(true, true)
	clock.Advance(2 * time.Hour)
	if _, err := v.Verify(tok()); err != nil {
		t.Fatalf("cached keys during an outage: %v", err)
	}
}

// spec: 10.2 (Authentication: the Client → Gateway OIDC boundary)
func TestOIDCVerifierRejectsDiscoveryIssuerMismatch_spec_10_2(t *testing.T) {
	idp := newTestIdP(t)
	idp.addRSA("k1")
	idp.issuerOverride = "https://idp.globex.test"
	clock := &oidcClock{now: time.Now()}
	v := newOIDCVerifier(t, idp, clock)
	if _, err := v.Verify(idp.sign("RS256", "k1", "", idp.idTokenClaims(clock.Now()))); verifyReason(err) != "jwks_unavailable" {
		t.Fatalf("discovery document naming another issuer: %v, want jwks_unavailable", err)
	}
}

// spec: 10.2 (Authentication: the Client → Gateway OIDC boundary)
func TestOIDCVerifierSkipsUnusableJWKs_spec_10_2(t *testing.T) {
	idp := newTestIdP(t)
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	idp.extraJWKs = []map[string]any{
		publicJWK("small", &small.PublicKey),
		{"kty": "RSA", "use": "enc", "kid": "enc", "n": "AQAB", "e": "AQAB"},
		{"kty": "oct", "kid": "sym", "k": "c2VjcmV0"},
		{"kty": "EC", "kid": "off-curve", "crv": "P-256", "x": b64u([]byte{1}), "y": b64u([]byte{2})},
		{"kty": "EC", "kid": "bad-crv", "crv": "P-192", "x": "AA", "y": "AA"},
	}
	clock := &oidcClock{now: time.Now()}
	v := newOIDCVerifier(t, idp, clock)
	idp.set("small", small)
	if _, err := v.Verify(idp.sign("RS256", "small", "", idp.idTokenClaims(clock.Now()))); verifyReason(err) != "jwks_unavailable" {
		t.Fatalf("JWK Set with no usable key: %v, want jwks_unavailable", err)
	}
}

// spec: 10.2 (Authentication: the Client → Gateway OIDC boundary)
func TestOIDCVerifierConcurrentFirstUseFetchesOnce_spec_10_2(t *testing.T) {
	idp := newTestIdP(t)
	idp.addEC("ec", elliptic.P256())
	clock := &oidcClock{now: time.Now()}
	v := newOIDCVerifier(t, idp, clock)
	tok := idp.sign("ES256", "ec", "", idp.idTokenClaims(clock.Now()))
	var wg sync.WaitGroup
	var failures atomic.Int64
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := v.Verify(tok); err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d concurrent verifications failed", failures.Load())
	}
	if got := idp.jwksHits.Load(); got != 1 {
		t.Fatalf("concurrent first use fetched the JWK Set %d times, want 1", got)
	}
}

// spec: 10.3 (Gateway startup configuration validation: `auth.oidc.issuerUrl`,
// `auth.oidc.clientId`)
func TestNewOIDCVerifierValidatesConfig_spec_10_3(t *testing.T) {
	for _, c := range []jwt.OIDCConfig{
		{IssuerURL: "", Audience: "lenny-gateway"},
		{IssuerURL: "idp.acme.test", Audience: "lenny-gateway"},
		{IssuerURL: "https://idp.acme.test", Audience: " "},
	} {
		if _, err := jwt.NewOIDCVerifier(c); err == nil {
			t.Errorf("NewOIDCVerifier(%+v): nil error, want a configuration error", c)
		}
	}
}
