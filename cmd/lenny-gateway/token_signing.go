// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"log"
	"strings"
	"sync"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// buildTokenSigningStores constructs the §7.1 uploadToken key ring, issuer,
// verifier, and rotator; the §4 / §17.5 KMS provider and its breaker
// observer; the §10.2 JWT key set and the shared remote signer; and the
// §10.2 bearer verifier, recording each on the accumulator. It is an
// extracted per-concern step of buildStores.
//
// spec: §4.1 gateway subsystem seams; §7.1 uploadToken; §10.2
// (Authentication) verifiers and signing topology; §4 KMS provider.
func (w *gatewayWiring) buildTokenSigningStores(tenants tenantstore.Store) {
	f := w.f
	devMode := f.devMode
	kmsOpts := f.kmsOpts
	bearerExpectedIssuer := f.bearerExpectedIssuer
	bearerExpectedAudiences := f.bearerExpectedAudiences
	bearerTrustHMACKeyFile := f.bearerTrustHMACKeyFile
	tenantIDClaim := f.tenantIDClaim
	// ----- §7.1 uploadToken KeyRing + rotator -----
	// The §7.1 contract requires the gateway to rotate signing
	// keys on a deployer-configurable schedule (default 24h) and keep
	// the previous key valid through a 5-minute overlap window so
	// tokens minted just before rotation continue to verify. The boot
	// key seeds the ring; the rotator goroutine (started below under
	// watchdogCtx) drives subsequent rotations and the overlap sweep.
	// spec: §7.1.
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		log.Fatalf("lenny-gateway: rand: %v", err)
	}
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "boot", Secret: seed[:]})
	uploadIssuer := uploadtoken.NewIssuer(ring, nil)
	uploadTracker := uploadtoken.NewMemoryTracker()
	uploadVerifier := uploadtoken.NewVerifier(ring, uploadTracker, nil)
	uploadRotator := uploadtoken.NewRotator(ring, uploadtoken.RotatorOptions{
		OnRotate: func(active, displaced uploadtoken.SigningKey) {
			log.Printf("lenny-gateway: §7.1 uploadToken signing key rotated; active=%s overlap=%s",
				active.KeyID, displaced.KeyID)
		},
		OnExpire: func(expired []string) {
			log.Printf("lenny-gateway: §7.1 uploadToken signing key(s) expired from overlap window: %v", expired)
		},
	})

	// ----- §4 KMS provider -----
	// The §4 / §12.9 envelope-encryption KEK seam and the §10.2 remote
	// signing seam. The gateway wraps the §4.9 connector-credential DEKs
	// through this provider and signs every Lenny-issued JWT it mints
	// with its platform:jwt-signing key. --kms-provider selects local |
	// aws | gcp | azure; `local` is rejected when --environment=prod.
	// spec: F-4.3.11, F-10.2.11, F-17.5.2.
	kmsProvider, err := providerflags.Resolve(context.Background(), *kmsOpts)
	if err != nil {
		log.Fatalf("lenny-gateway: kms provider: %v", err)
	}
	log.Printf("lenny-gateway: §4 KMS provider = %s (environment=%s)",
		kmsOpts.Provider, kmsOpts.Environment)

	// spec: §12.8 — now that the KMS provider is resolved, wire it
	// into the Postgres tenant store so the §12.8 erasure_salt is
	// envelope-encrypted at rest (the store is built before the provider is
	// resolved, so the injection is deferred to here). F-12.8.5.
	if tps, ok := tenants.(*tenantpg.Store); ok {
		tps.SetSaltKMS(kmsProvider)
	}

	// ----- §10.2 Shared signer and key set -----
	// spec: §10.2 (Authentication) "Signing topology", "Verifier key set
	// and rotation"; §4.3 — the gateway mints the impersonation bearers,
	// the §17.6 admin credential, and the playground bearers in process,
	// with the same platform:jwt-signing key the Token Service signs with.
	// The key set is loaded once at startup, and a process that cannot
	// load it does not start.
	//
	// The signer goes through the §10.2 JWTSigner circuit breaker. More
	// than 3 consecutive hard Sign failures inside a 30s window trip it
	// open; later Sign calls short-circuit to ErrSigningUnavailable until
	// the cooldown elapses, and each mint site maps the refusal to 503
	// KMS_SIGNING_UNAVAILABLE. The observer is wired to gatewaymetrics
	// after gatewaymetrics.New() so the breaker can push the
	// signing-error counter and circuit-state gauge. Verification goes
	// through the key set, which holds public keys only and never calls
	// the KMS, so it does not gate on the breaker. F-10.2.6.
	kmsBreakerObs := &kmsBreakerObserver{}
	keySet, jwtSigner, err := jwt.LoadSigning(context.Background(), kmsProvider, kmsOpts.AliasToKey, kmsBreakerObs)
	if err != nil {
		log.Fatalf("lenny-gateway: §10.2 JWT key set: %v", err)
	}
	log.Printf("lenny-gateway: §10.2 JWT key set loaded (%d key(s), signing kid %s)",
		keySet.Len(), keySet.Signing().KeyID)

	// ----- §10.2 Bearer verifier -----
	// The key set verifies every Lenny-issued token, whichever replica
	// or Token Service replica signed it. A production install runs with
	// that single verifier. §17.4 Embedded Mode additionally trusts the
	// embedded OIDC provider's HMAC key: when --bearer-trust-hmac-key-file
	// points at a key file, the gateway loads it and accepts tokens
	// signed with it alongside Lenny-issued tokens through a
	// jwt.MultiVerifier. The key set stays the primary member, so its
	// rejection reason is surfaced when neither member accepts a token.
	var bearerVerifier jwt.Verifier = keySet
	if *bearerTrustHMACKeyFile != "" {
		// spec: §10.2. The HMAC trust member is the dev-mode embedded
		// OIDC hook; the spec is explicit that the dev HMAC backend
		// "must never be used in production deployments".
		// --bearer-trust-hmac-key-file has no production use case.
		// Refuse to load it when --dev-mode is off so a misconfigured
		// chart fails closed at startup instead of silently widening the
		// trust set. F-10.2.13.
		if !*devMode {
			log.Fatalf("lenny-gateway: --bearer-trust-hmac-key-file requires --dev-mode (§10.2: the dev HMAC backend must never be used in production)")
		}
		trusted, err := jwt.LoadHMACKeyFile(*bearerTrustHMACKeyFile)
		if err != nil {
			log.Fatalf("lenny-gateway: --bearer-trust-hmac-key-file: %v", err)
		}
		// spec: §17.4 — "the embedded OIDC provider refuses any
		// audience claim not matching dev.local; the gateway rejects
		// externally-issued tokens." The embedded provider's own Verify
		// enforces the audience, but the gateway accepts the embedded key
		// directly and would otherwise honor any aud claim signed under it.
		// Wrap the embedded-key verifier in a ClaimChecker pinned to the
		// embedded OIDC audience (dev.local) so a foreign-audience token,
		// even one validly signed by the trusted key, is refused at the
		// gateway. The key-set member is unaffected. F-17.4.16.
		bearerVerifier = embeddedBearerVerifier(keySet, trusted, *tenantIDClaim)
		log.Printf("lenny-gateway: trusting an additional HMAC bearer key from %s (kid %s); embedded tokens must carry aud=dev.local",
			*bearerTrustHMACKeyFile, trusted.KeyID())
	}
	// spec: §10.2 — wrap the Lenny-issued member so the standard auth
	// chain enforces iss / aud alongside signature / exp / nbf when an
	// operator configures the expected values. An unset flag skips
	// the corresponding check so dev deployments retain their existing
	// posture. The upstream IdP member joins after this wrap, because it
	// checks its own iss and aud against auth.oidc.issuerUrl and
	// auth.oidc.clientId.
	expectedAuds := splitCSV(*bearerExpectedAudiences)
	if *bearerExpectedIssuer != "" || len(expectedAuds) > 0 {
		log.Printf("lenny-gateway: §10.2 bearer iss/aud enforced iss=%q audiences=%v",
			*bearerExpectedIssuer, expectedAuds)
	}
	idpVerifier := buildIdPVerifier(f)
	bearerVerifier = composeBearerVerifier(bearerVerifier, jwt.ExpectedClaims{
		Issuer:    *bearerExpectedIssuer,
		Audiences: expectedAuds,
	}, idpVerifier, *tenantIDClaim)
	// spec: §4.1 — record the §7.1 uploadToken, §4 KMS, and §10.2 / §10.3
	// signing and verification surfaces on the accumulator for the
	// credential-assignment step and the run loop.
	w.ring = ring
	w.uploadIssuer = uploadIssuer
	w.uploadVerifier = uploadVerifier
	w.uploadTracker = uploadTracker
	w.uploadRotator = uploadRotator
	w.kmsProvider = kmsProvider
	w.kmsBreakerObs = kmsBreakerObs
	w.jwtKeySet = keySet
	w.jwtSigner = jwtSigner
	w.bearerVerifier = bearerVerifier
	w.idpVerifier = idpVerifier
	w.expectedAuds = expectedAuds
}

// kmsBreakerObserver routes the §10.2 JWTSigner breaker
// transitions and signing failures onto gatewaymetrics so the §16.5
// KMSSigningUnavailable alert reads them. The metrics pointer is wired
// in after gatewaymetrics.New() returns; pre-wire calls are no-ops.
// spec: §10.2. F-10.2.6.
type kmsBreakerObserver struct {
	mu sync.Mutex
	m  *gatewaymetrics.Metrics
}

func (o *kmsBreakerObserver) SetMetrics(m *gatewaymetrics.Metrics) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.m = m
}

func (o *kmsBreakerObserver) metrics() *gatewaymetrics.Metrics {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.m
}

func (o *kmsBreakerObserver) OnSigningFailure() {
	if m := o.metrics(); m != nil {
		m.RecordKMSSigningError("inner")
	}
}

func (o *kmsBreakerObserver) OnRejected() {
	if m := o.metrics(); m != nil {
		m.RecordKMSSigningError("rejected")
	}
}

// OnOutcome counts the sign request on
// lenny_jwt_sign_requests_total{component="gateway",outcome}.
// spec: §16.1 (JWT sign requests).
func (o *kmsBreakerObserver) OnOutcome(outcome string) {
	if m := o.metrics(); m != nil {
		m.RecordJWTSignRequest(outcome)
	}
}

func (o *kmsBreakerObserver) OnCircuitOpen() {
	if m := o.metrics(); m != nil {
		m.SetKMSSigningCircuitState(2)
	}
}

func (o *kmsBreakerObserver) OnCircuitClosed() {
	if m := o.metrics(); m != nil {
		m.SetKMSSigningCircuitState(0)
	}
}

// splitCSV splits a comma-separated flag value into a trimmed,
// non-empty slice. An empty input yields a nil slice.
// embeddedOIDCAudience is the only audience the §17.4 embedded OIDC
// provider issues. It mirrors pkg/embedded/oidc.Audience; the gateway
// keeps the literal local so the production binary does not link the
// embedded dev-only provider. spec: §17.4.
const embeddedOIDCAudience = "dev.local"

// embeddedBearerVerifier composes the §17.4 Embedded Mode bearer
// verifier: the key-set member (primary) first, unwrapped, so a
// Lenny-issued token keeps its own `tenant_id`, then the embedded OIDC
// member, which reads the configured tenant claim. The key-set member's
// rejection reason is surfaced when neither accepts a token.
// spec: §10.2 (Authentication: `tenant_id` extraction), §17.4.
func embeddedBearerVerifier(primary, trusted jwt.Verifier, tenantClaim string) jwt.Verifier {
	return jwt.NewMultiVerifier(primary, embeddedHMACVerifier(trusted, tenantClaim))
}

// embeddedHMACVerifier wraps the trusted embedded OIDC HMAC verifier so
// the gateway refuses any token whose aud claim is not the embedded
// provider's audience, even when the signature is valid. §17.4
// requires the gateway to reject foreign-audience tokens; the embedded
// provider's own Verify enforces this, but the gateway trusts the key
// directly and must apply the same check on its side. F-17.4.16.
//
// The embedded provider is an identity provider, so a token its key
// verified takes its tenant from the configured tenant claim
// (--tenant-id-claim) through jwt.TenantClaimVerifier. The key-set
// member of the bearer verifier is not wrapped: a Lenny-issued token
// resolves from its own `tenant_id`. spec: §10.2 (Authentication:
// `tenant_id` extraction).
func embeddedHMACVerifier(trusted jwt.Verifier, tenantClaim string) jwt.Verifier {
	return jwt.NewTenantClaimVerifier(jwt.NewClaimChecker(trusted, jwt.ExpectedClaims{
		Audiences: []string{embeddedOIDCAudience},
	}), tenantClaim)
}

// buildIdPVerifier returns the §10.2 verifier of bearers the deployer's
// upstream OIDC identity provider issues, or nil when the gateway has no
// IdP to trust. It needs both §10.3 OIDC keys: the issuer names the
// discovery document and the expected `iss`, and the client id is the
// audience every IdP token must carry. Outside dev mode the startup
// validation already requires both; in dev mode an issuer without a
// client id cannot be audience-checked, so the gateway trusts no IdP and
// says so.
//
// spec: §10.2 (Authentication) the Client → Gateway boundary and the
// `user_bearer` type; §10.3 (Gateway startup configuration validation)
// `auth.oidc.issuerUrl` and `auth.oidc.clientId`.
func buildIdPVerifier(f *gatewayFlags) jwt.Verifier {
	issuer := strings.TrimSpace(*f.oidcIssuerURL)
	clientID := strings.TrimSpace(*f.oidcClientID)
	if issuer == "" {
		return nil
	}
	if clientID == "" {
		log.Printf("lenny-gateway: §10.2 --oidc-issuer-url is set without --oidc-client-id; IdP-issued bearers are refused because their audience cannot be checked")
		return nil
	}
	v, err := jwt.NewOIDCVerifier(jwt.OIDCConfig{
		IssuerURL:          issuer,
		Audience:           clientID,
		KeyRefreshInterval: *f.oidcJWKSRefreshInterval,
		MinRefetchInterval: *f.oidcJWKSMinRefetchInterval,
		FetchTimeout:       *f.oidcFetchTimeout,
	})
	if err != nil {
		log.Fatalf("lenny-gateway: §10.2 OIDC verifier: %v", err)
	}
	log.Printf("lenny-gateway: §10.2 trusting IdP-issued bearers from %s (aud %s)", issuer, clientID)
	return v
}

// composeBearerVerifier assembles the §10.2 bearer verifier from the
// Lenny-issued member (the key set, joined by the §17.4 embedded key when
// the dev trust hook is set), the optional operator iss / aud check on
// that member, and the optional upstream IdP member. The Lenny-issued
// member is tried first, so its rejection reason is the one surfaced when
// no member accepts a token. The IdP member takes its tenant from
// auth.tenantIdClaim, because a token an IdP key verified resolves its
// tenant from the configured claim.
//
// spec: §10.2 (Authentication) the Client → Gateway boundary, "`tenant_id`
// extraction", and "Verifier key set and rotation".
func composeBearerVerifier(lenny jwt.Verifier, expected jwt.ExpectedClaims, idp jwt.Verifier, tenantClaim string) jwt.Verifier {
	if expected.Issuer != "" || len(expected.Audiences) > 0 {
		lenny = jwt.NewClaimChecker(lenny, expected)
	}
	if idp == nil {
		return lenny
	}
	return jwt.NewMultiVerifier(lenny, jwt.NewTenantClaimVerifier(idp, tenantClaim))
}
