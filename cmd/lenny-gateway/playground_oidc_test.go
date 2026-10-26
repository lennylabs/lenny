// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// TestBuildPlaygroundOIDCWiresTheDiscoveredExchanger_spec_27_3_1 drives
// the gateway startup steps with the §10.3 OIDC keys pointed at an OIDC
// stub. In oidc mode the playground exchanger redirects to the issuer's
// discovered authorization endpoint for auth.oidc.clientId and redeems the
// code for the subject and the tenant from --tenant-id-claim. Another
// playground mode, or a gateway that trusts no IdP, gets no exchanger.
//
// spec: 27.3.1 (OIDC cookie-to-MCP-bearer exchange) `GET /playground/auth/login`
// and `GET /playground/auth/callback`, 27.2 (Placement and gating) the
// `playground.authMode` row
func TestBuildPlaygroundOIDCWiresTheDiscoveredExchanger_spec_27_3_1(t *testing.T) {
	ctx := context.Background()
	idp := oidcstub.New(t)
	const tenantClaim = "https://acme.example/tenant"
	idp.SetLoginIdentity(oidcstub.MintOptions{Subject: "alice@acme.com", Extra: map[string]any{tenantClaim: "acme"}})
	master := filepath.Join(t.TempDir(), "master.key")
	build := func(issuer string) *gatewayWiring {
		f := tokenSigningFlags(master, "", "", "")
		claim, client := tenantClaim, "lenny-gateway"
		f.tenantIDClaim = &claim
		f.oidcIssuerURL = &issuer
		f.oidcClientID = &client
		w := &gatewayWiring{f: f}
		w.buildTokenSigningStores(nil)
		return w
	}

	w := build(idp.Issuer())
	x := buildPlaygroundOIDC(w.f, w.idpVerifier, playground.AuthModeOIDC)
	if x == nil {
		t.Fatal("oidc mode with a configured IdP built no exchanger")
	}
	const redirect = "https://gw.acme.com/playground/auth/callback"
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk-playground-test"
	authURL, err := x.AuthorizationURL(ctx, "state-1", pkceS256(verifier), redirect)
	if err != nil {
		t.Fatalf("AuthorizationURL: %v", err)
	}
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, idp.Issuer()+"/authorize?") || u.Query().Get("client_id") != "lenny-gateway" {
		t.Fatalf("AuthorizationURL = %s, want the discovered endpoint for client lenny-gateway", authURL)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatalf("GET authorization URL: %v", err)
	}
	_ = resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	subject, err := x.Exchange(ctx, loc.Query().Get("code"), verifier, redirect)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if subject.UserID != "alice@acme.com" || subject.TenantID != "acme" {
		t.Errorf("subject = %+v, want alice@acme.com in acme", subject)
	}

	if x := buildPlaygroundOIDC(w.f, w.idpVerifier, playground.AuthModeAPIKey); x != nil {
		t.Error("apiKey mode built an OIDC exchanger")
	}
	noIdP := build("")
	if x := buildPlaygroundOIDC(noIdP.f, noIdP.idpVerifier, playground.AuthModeOIDC); x != nil {
		t.Error("oidc mode with no IdP built an exchanger")
	}
}

// pkceS256 is the RFC 7636 S256 code_challenge of verifier.
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
