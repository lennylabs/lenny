// SPDX-License-Identifier: MIT

package main

import (
	"log"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
)

// buildPlaygroundOIDC returns the §27.3.1 authorization-code exchanger of
// the playground's oidc mode, or nil in another mode. The playground signs
// users in at the deployer's IdP as the auth.oidc.clientId client: the
// exchanger reads the authorization and token endpoints from the
// --oidc-issuer-url discovery document on first use, sends a PKCE S256
// challenge, and validates the returned ID token with idp, the §10.2
// jwt.OIDCVerifier buildIdPVerifier builds, before reading the tenant from
// --tenant-id-claim. The playground is a public client, so no client
// secret is sent; PKCE protects the code. With no IdP configured the
// exchanger is nil and login and callback answer
// 503 LENNY_PLAYGROUND_OIDC_UNAVAILABLE, which the gateway logs at startup.
//
// spec: §27.3 (Authentication) the `playground.authMode=oidc` bullet;
// §27.3.1 `GET /playground/auth/login` and `GET /playground/auth/callback`.
func buildPlaygroundOIDC(f *gatewayFlags, idp jwt.Verifier, mode playground.AuthMode) playground.OIDCExchanger {
	if mode != playground.AuthModeOIDC {
		return nil
	}
	if idp == nil {
		log.Printf("lenny-gateway: §27.3.1 playground.authMode=oidc without --oidc-issuer-url and --oidc-client-id; /playground/auth/login answers 503 LENNY_PLAYGROUND_OIDC_UNAVAILABLE")
		return nil
	}
	return playground.NewHTTPOIDCExchanger(playground.HTTPOIDCConfig{
		IssuerURL:        strings.TrimSpace(*f.oidcIssuerURL),
		ClientID:         strings.TrimSpace(*f.oidcClientID),
		TenantIDClaim:    *f.tenantIDClaim,
		IDTokenValidator: playground.IDTokenValidatorFromVerifier(idp),
		HTTPClient:       &http.Client{Timeout: *f.oidcFetchTimeout},
	})
}
