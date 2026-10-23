// SPDX-License-Identifier: MIT

package jwt

// TenantClaimVerifier is a Verifier wrapper that resolves the tenant
// identifier of a token an identity-provider key verified from the
// operator-configured tenant claim (`auth.tenantIdClaim`, the gateway's
// --tenant-id-claim flag).
//
// §10.2 reads the tenant from the configured claim on a bearer an IdP
// key verified, and from the `tenant_id` claim on a gateway-issued
// token. The discriminator is the key that verified the token, never a
// payload claim such as `iss` or `typ`, because an IdP-signed token can
// carry any payload. The gateway therefore wraps only its IdP-key
// verifier members with TenantClaimVerifier and leaves the Token Service
// member unwrapped, and the auth middleware reads Claims.TenantID for
// every bearer.
//
// On every token the inner verifier accepts, Verify replaces
// Claims.TenantID with ClaimString(name). When the configured claim is
// absent the result is empty, so the middleware refuses the token with
// TENANT_CLAIM_MISSING even when the token carries its own `tenant_id`:
// an IdP token never keeps a `tenant_id` the operator did not name.
//
// spec: §10.2 (Authentication: `tenant_id` extraction).
type TenantClaimVerifier struct {
	inner Verifier
	claim string
}

// NewTenantClaimVerifier returns a Verifier that delegates to inner and,
// on success, sets Claims.TenantID from the claim named claim. An empty
// claim name reads as the §10.2 default `tenant_id`.
func NewTenantClaimVerifier(inner Verifier, claim string) *TenantClaimVerifier {
	if inner == nil {
		panic("jwt: NewTenantClaimVerifier: inner verifier is nil")
	}
	if claim == "" {
		claim = "tenant_id"
	}
	return &TenantClaimVerifier{inner: inner, claim: claim}
}

// Verify implements the Verifier interface.
func (v *TenantClaimVerifier) Verify(token string) (Claims, error) {
	claims, err := v.inner.Verify(token)
	if err != nil {
		return Claims{}, err
	}
	claims.TenantID = claims.ClaimString(v.claim)
	return claims, nil
}

// Compile-time interface check.
var _ Verifier = (*TenantClaimVerifier)(nil)
