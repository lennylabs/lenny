// SPDX-License-Identifier: MIT

package azure

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/kmstest"
)

// signFake scripts the fake client's Sign and GetKey-for-signing answers.
type signFake struct {
	sig    []byte
	err    error
	jwk    *azkeys.JSONWebKey
	calls  int
	lastIn azkeys.SignParameters
}

func (f *fakeKMS) Sign(ctx context.Context, _, _ string, params azkeys.SignParameters, _ *azkeys.SignOptions) (azkeys.SignResponse, error) {
	f.signing.calls++
	f.signing.lastIn = params
	if err := ctx.Err(); err != nil {
		return azkeys.SignResponse{}, err
	}
	if f.signing.err != nil {
		return azkeys.SignResponse{}, f.signing.err
	}
	return azkeys.SignResponse{KeyOperationResult: azkeys.KeyOperationResult{Result: f.signing.sig}}, nil
}

const signKeyName, signKeyVersion = "jwt", "v1"

// newSignProvider returns a provider whose GetKey answers come from the
// signing fake rather than the KEK fake.
func newSignProvider(f *fakeKMS) *Provider {
	return newWithClient(signKeyClient{f}, map[string]KeyRef{kms.AliasJWTSigning: {Name: signKeyName, Version: signKeyVersion}})
}

// signKeyClient overrides GetKey with the signing fake's JWK.
type signKeyClient struct{ *fakeKMS }

func (c signKeyClient) GetKey(ctx context.Context, _, _ string, _ *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error) {
	if err := ctx.Err(); err != nil {
		return azkeys.GetKeyResponse{}, err
	}
	if c.signing.err != nil {
		return azkeys.GetKeyResponse{}, c.signing.err
	}
	return azkeys.GetKeyResponse{KeyBundle: azkeys.KeyBundle{Key: c.signing.jwk}}, nil
}

func ecJWK(t *testing.T, pub *ecdsa.PublicKey, kty azkeys.KeyType, crv azkeys.CurveName) *azkeys.JSONWebKey {
	t.Helper()
	raw, err := pub.Bytes()
	if err != nil {
		t.Fatalf("encode public key: %v", err)
	}
	return &azkeys.JSONWebKey{Kty: &kty, Crv: &crv, X: raw[1:33], Y: raw[33:]}
}

// spec: 10.2 (Authentication)
// Key Vault returns r||s already, so the adapter passes it through
// unchanged and rejects any other length.
func TestAzureSignReturnsRawRS_spec_10_2(t *testing.T) {
	key := kmstest.NewP256Key(t)
	digest := kmstest.Digest("header.payload")
	rs, err := kms.SignatureFromASN1(kmstest.ShortRSignature(t, key, digest))
	if err != nil {
		t.Fatalf("convert fixture: %v", err)
	}
	f := newFakeKMS()
	f.signing.sig = rs
	sig, err := newSignProvider(f).Sign(context.Background(), kms.AliasJWTSigning, digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !kmstest.VerifyRS(&key.PublicKey, digest, sig) {
		t.Fatal("signature does not verify")
	}
	if *f.signing.lastIn.Algorithm != azkeys.SignatureAlgorithmES256 || !bytes.Equal(f.signing.lastIn.Value, digest) {
		t.Fatalf("Sign parameters = %+v, want ES256 over the digest", f.signing.lastIn)
	}
	f.signing.sig = rs[:63]
	if _, err := newSignProvider(f).Sign(context.Background(), kms.AliasJWTSigning, digest); !errors.Is(err, kms.ErrNotP256) {
		t.Errorf("63-byte signature err=%v, want ErrNotP256", err)
	}
}

// spec: 10.2 (Authentication)
func TestAzureSignMaps429ToErrThrottled_spec_10_2(t *testing.T) {
	f := newFakeKMS()
	f.signing.err = &azcore.ResponseError{StatusCode: http.StatusTooManyRequests, ErrorCode: "Throttled"}
	if _, err := newSignProvider(f).Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x")); !errors.Is(err, kms.ErrThrottled) {
		t.Errorf("429 err=%v, want ErrThrottled", err)
	}
	f.signing.err = &azcore.ResponseError{StatusCode: http.StatusInternalServerError}
	if _, err := newSignProvider(f).Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x")); err == nil || errors.Is(err, kms.ErrThrottled) {
		t.Errorf("500 err=%v, want a non-throttle error", err)
	}
	f.signing.err = &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "KeyNotFound"}
	if _, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("404 err=%v, want ErrUnknownKEK", err)
	}
}

// spec: 10.2 (Authentication)
func TestAzureSignRejectsBadDigestUnmappedAliasAndCancelledContext_spec_10_2(t *testing.T) {
	f := newFakeKMS()
	p := newSignProvider(f)
	if _, err := p.Sign(context.Background(), kms.AliasJWTSigning, make([]byte, 31)); !errors.Is(err, kms.ErrInvalidDigest) {
		t.Errorf("31-byte digest err=%v, want ErrInvalidDigest", err)
	}
	if f.signing.calls != 0 {
		t.Errorf("a rejected digest reached the provider %d times", f.signing.calls)
	}
	if _, err := p.Sign(context.Background(), "platform:other", kmstest.Digest("x")); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("unmapped alias err=%v, want ErrUnknownKEK", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Sign(ctx, kms.AliasJWTSigning, kmstest.Digest("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Sign err=%v, want context.Canceled", err)
	}
	if _, err := p.PublicKey(ctx, kms.AliasJWTSigning); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled PublicKey err=%v, want context.Canceled", err)
	}
}

// spec: 10.2 (Authentication)
func TestAzurePublicKeyRequiresP256_spec_10_2(t *testing.T) {
	key := kmstest.NewP256Key(t)
	f := newFakeKMS()
	for _, kty := range []azkeys.KeyType{azkeys.KeyTypeEC, azkeys.KeyTypeECHSM} {
		f.signing.jwk = ecJWK(t, &key.PublicKey, kty, azkeys.CurveNameP256)
		pub, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning)
		if err != nil || !pub.Equal(&key.PublicKey) {
			t.Fatalf("%s PublicKey: pub=%v err=%v", kty, pub, err)
		}
	}
	rsa, crv := azkeys.KeyTypeRSA, azkeys.CurveNameP256
	bad := map[string]*azkeys.JSONWebKey{
		"nil key":     nil,
		"RSA key":     {Kty: &rsa, Crv: &crv},
		"P-384 curve": ecJWK(t, &key.PublicKey, azkeys.KeyTypeEC, azkeys.CurveNameP384),
		"off-curve":   {Kty: ecJWK(t, &key.PublicKey, azkeys.KeyTypeEC, crv).Kty, Crv: &crv, X: []byte{1}, Y: []byte{2}},
		"oversized X": {Kty: ecJWK(t, &key.PublicKey, azkeys.KeyTypeEC, crv).Kty, Crv: &crv, X: make([]byte, 33), Y: []byte{2}},
	}
	for name, jwk := range bad {
		f.signing.jwk = jwk
		if _, err := newSignProvider(f).PublicKey(context.Background(), kms.AliasJWTSigning); !errors.Is(err, kms.ErrNotP256) {
			t.Errorf("%s err=%v, want ErrNotP256", name, err)
		}
	}
}

// staticCredential is a token credential for the local Key Vault server.
type staticCredential struct{}

func (staticCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// throttleVault answers the Key Vault authentication challenge and then
// throttles every authorized sign request with HTTP 429, which the SDK's
// default retry policy retries. It counts the authorized sign requests.
func throttleVault(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Bearer authorization="https://login.microsoftonline.com/tenant", resource="https://vault.azure.net"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/sign") {
			n.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"Throttled","message":"slow down"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// spec: 10.2 (Authentication)
// Sign through the real SDK client must reach the provider once: the
// fake-client tests bypass the SDK retry policy, so the request count
// against a local server is the discriminating assertion.
func TestAzureSignRealClientMakesOneRequestOnThrottle_spec_10_2(t *testing.T) {
	srv, n := throttleVault(t)
	client, err := azkeys.NewClient(srv.URL, staticCredential{}, &azkeys.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: srv.Client(),
			Retry:     policy.RetryOptions{RetryDelay: time.Millisecond, MaxRetryDelay: time.Millisecond},
		},
		DisableChallengeResourceVerification: true,
	})
	if err != nil {
		t.Fatalf("azkeys.NewClient: %v", err)
	}
	alg := azkeys.SignatureAlgorithmES256
	_, _ = client.Sign(context.Background(), signKeyName, signKeyVersion, azkeys.SignParameters{Algorithm: &alg, Value: kmstest.Digest("x")}, nil)
	if got := n.Load(); got < 2 {
		t.Fatalf("control: default client made %d sign requests, want a retry", got)
	}

	n.Store(0)
	p := newWithClient(clientAdapter{inner: client}, map[string]KeyRef{kms.AliasJWTSigning: {Name: signKeyName, Version: signKeyVersion}})
	if _, err := p.Sign(context.Background(), kms.AliasJWTSigning, kmstest.Digest("x")); !errors.Is(err, kms.ErrThrottled) {
		t.Fatalf("Sign err=%v, want ErrThrottled", err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("Sign made %d provider sign requests, want exactly 1", got)
	}
}
