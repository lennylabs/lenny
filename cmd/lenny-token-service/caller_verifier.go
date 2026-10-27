// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// buildCallerVerifier returns the verifier of the §13.3 caller credential
// on /v1/oauth/token: the key set, joined by the upstream OIDC IdP member
// when --oidc-issuer-url and --oidc-client-id are set. The key set is tried
// first, so its rejection reason is the one surfaced when neither member
// accepts a token. The IdP member resolves the tenant from
// --tenant-id-claim and normalizes the token to user_bearer, the
// composition the gateway's composeBearerVerifier uses. An issuer without
// a client id cannot be audience-checked, so the Token Service then trusts
// no IdP and logs why. The subject_token and actor_token stay on the key
// set alone.
//
// spec: §13.3 "Client authentication on /v1/oauth/token"; §10.2
// (Authentication) "`tenant_id` extraction".
func buildCallerVerifier(f *tokenServiceFlags, keySet jwt.Verifier) (jwt.Verifier, error) {
	// A flag set built without registerIdPFlags, as some tests build one,
	// trusts no IdP.
	if f.oidcIssuerURL == nil || f.oidcClientID == nil {
		return keySet, nil
	}
	issuer := strings.TrimSpace(*f.oidcIssuerURL)
	clientID := strings.TrimSpace(*f.oidcClientID)
	if issuer == "" {
		return keySet, nil
	}
	if clientID == "" {
		log.Printf("lenny-token-service: §13.3 --oidc-issuer-url is set without --oidc-client-id; IdP caller tokens are refused because their audience cannot be checked")
		return keySet, nil
	}
	idp, err := jwt.NewOIDCVerifier(jwt.OIDCConfig{
		IssuerURL:          issuer,
		Audience:           clientID,
		KeyRefreshInterval: *f.oidcJWKSRefreshInterval,
		MinRefetchInterval: *f.oidcJWKSMinRefetchInterval,
		FetchTimeout:       *f.oidcFetchTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("§13.3 OIDC caller verifier: %w", err)
	}
	log.Printf("lenny-token-service: §13.3 admitting IdP caller tokens from %s (aud %s)", issuer, clientID)
	return jwt.NewMultiVerifier(keySet, jwt.NewTenantClaimVerifier(idp, *f.tenantIDClaim)), nil
}
