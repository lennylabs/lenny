// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// idpCallerServer builds a Server whose caller credential verifier is the
// key set joined by the upstream IdP member, the way cmd/lenny-token-service
// composes it when --oidc-issuer-url and --oidc-client-id are set. The
// subject_token and actor_token verifier stays the key set. withIdP false
// leaves CallerVerifier unset.
func idpCallerServer(t *testing.T, idp *oidcstub.Stub, withIdP bool) (*Server, jwt.Signer) {
	t.Helper()
	stub := kmsstub.New(t)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	opts := Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             signer,
		Verifier:           ks,
		Issuer:             "https://lenny.dev.test/token",
	}
	if withIdP {
		oidc, err := jwt.NewOIDCVerifier(jwt.OIDCConfig{IssuerURL: idp.Issuer(), Audience: "lenny-gateway"})
		if err != nil {
			t.Fatalf("NewOIDCVerifier: %v", err)
		}
		opts.CallerVerifier = jwt.NewMultiVerifier(ks, jwt.NewTenantClaimVerifier(oidc, "tenant_id"))
	}
	return mustNewServer(t, opts), signer
}

// spec: 13.3 (Credential Flow) "Client authentication on /v1/oauth/token"
//
// The upstream OIDC ID token is the bootstrap caller credential: an
// exchange authenticated by an IdP token for alice in acme receives an
// access_token for her Lenny subject_token. A Lenny-issued caller token
// keeps working beside it. The IdP member authenticates the caller only,
// so an IdP token presented as the subject_token is refused with 400
// invalid_grant, and an IdP token for another audience or from another
// issuer is refused as the caller with 401 invalid_client. A Server built
// without a CallerVerifier authenticates the caller with the key set and
// refuses the IdP token.
func TestIdPTokenIsTheBootstrapCallerCredential_spec_13_3(t *testing.T) {
	idp := oidcstub.New(t)
	foreign := oidcstub.New(t)
	srv, signer := idpCallerServer(t, idp, true)
	alice := oidcstub.MintOptions{Subject: "alice@acme.com", TenantID: "acme", Audience: "lenny-gateway"}
	subject := signKeySetToken(t, signer, "jti-subject", "")

	status, env := decodeExchange(t, doExchange(t, srv, idp.MintToken(alice), narrowRequest(subject)))
	if status != http.StatusOK {
		t.Fatalf("IdP caller: status %d, want 200; body %v", status, env)
	}
	tok, _ := env["access_token"].(string)
	if tok == "" {
		t.Fatalf("IdP caller: no access_token; body %v", env)
	}
	issued, err := srv.verifier.Verify(tok)
	if err != nil || issued.Subject != "alice@acme.com" || issued.TenantID != "acme" {
		t.Errorf("issued token: sub %q tenant %q err %v, want alice@acme.com in acme", issued.Subject, issued.TenantID, err)
	}

	lennyCaller := signKeySetToken(t, signer, "jti-lenny-caller", "")
	if status, env := decodeExchange(t, doExchange(t, srv, lennyCaller, narrowRequest(subject))); status != http.StatusOK {
		t.Errorf("Lenny-issued caller beside the IdP member: status %d, want 200; body %v", status, env)
	}

	if status, env := decodeExchange(t, doExchange(t, srv, lennyCaller, narrowRequest(idp.MintToken(alice)))); status != http.StatusBadRequest || env["error"] != "invalid_grant" {
		t.Errorf("IdP token as subject_token: status %d error %v, want 400 invalid_grant", status, env["error"])
	}

	wrongAud := alice
	wrongAud.Audience = "other-client"
	expired := alice
	expired.Lifetime = -time.Minute
	for _, c := range []struct {
		name, caller string
	}{
		{"wrong audience", idp.MintToken(wrongAud)},
		{"foreign issuer", foreign.MintToken(alice)},
		{"expired", idp.MintToken(expired)},
	} {
		status, env := decodeExchange(t, doExchange(t, srv, c.caller, narrowRequest(subject)))
		if status != http.StatusUnauthorized || env["error"] != "invalid_client" {
			t.Errorf("%s IdP caller: status %d error %v, want 401 invalid_client", c.name, status, env["error"])
		}
		if _, ok := env["access_token"]; ok {
			t.Errorf("%s IdP caller received an access_token", c.name)
		}
	}

	keyOnly, keyOnlySigner := idpCallerServer(t, idp, false)
	keyOnlySubject := signKeySetToken(t, keyOnlySigner, "jti-key-only", "")
	if status, env := decodeExchange(t, doExchange(t, keyOnly, idp.MintToken(alice), narrowRequest(keyOnlySubject))); status != http.StatusUnauthorized || env["error"] != "invalid_client" {
		t.Errorf("IdP caller with no CallerVerifier: status %d error %v, want 401 invalid_client", status, env["error"])
	}
}
