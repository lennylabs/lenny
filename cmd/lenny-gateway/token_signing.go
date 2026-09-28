// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"log"
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
// observer; the §10.3 rotating verifier; and the §10.2 bearer verifier and
// JWT signer, recording each on the accumulator. It is an extracted
// per-concern step of buildStores.
//
// spec: §4.1 gateway subsystem seams; §7.1 uploadToken; §10.2 / §10.3
// verifiers; §4 KMS provider.
func (w *gatewayWiring) buildTokenSigningStores(tenants tenantstore.Store) {
	f := w.f
	devMode := f.devMode
	kmsOpts := f.kmsOpts
	bearerExpectedIssuer := f.bearerExpectedIssuer
	bearerExpectedAudiences := f.bearerExpectedAudiences
	bearerTrustHMACKeyFile := f.bearerTrustHMACKeyFile
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
	// The §4 / §12.9 envelope-encryption KEK seam. The gateway wraps
	// the §4.9 connector-credential DEKs through this provider; the
	// signing-key concern moved to the Token Service binary in
	// F-4.3.12. --kms-provider selects local | aws | gcp | azure;
	// `local` is rejected when --environment=prod.
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

	// ----- §13.3 Token Service -----
	// §4 KMS-envelope-backed JWT signer: the HMAC-SHA256 signing key is
	// sealed under a KMS KEK rather than being a plaintext per-process
	// dev secret. The token-service handler mounted below serves POST
	// /v1/oauth/token (RFC 8693).
	kmsBackedSigner, err := jwt.NewKMSSigner(context.Background(), kmsProvider, jwt.TokenServiceKEKAlias, "boot")
	if err != nil {
		log.Fatalf("lenny-gateway: kms-backed jwt signer: %v", err)
	}
	// spec: §10.2 — wrap the KMS-backed signer in the
	// JWTSigner circuit breaker. More than 3 consecutive Sign failures
	// inside a 30s window trips the breaker open; subsequent Sign calls
	// short-circuit to ErrSigningUnavailable until the cooldown elapses.
	// The Token Service handler maps the sentinel to 503
	// KMS_SIGNING_UNAVAILABLE with retryable: true. The Observer is
	// wired after gatewaymetrics.New() below so the breaker can push the
	// signing-error counter and circuit-state gauge. F-10.2.6.
	kmsBreakerObs := &kmsBreakerObserver{}
	jwtSigner := &jwt.BreakerSigner{
		Inner:    kmsBackedSigner,
		Observer: kmsBreakerObs,
	}

	// ----- §10.3 RotatingVerifier -----
	// Wrap the Token Service signer in a §10.3 RotatingVerifier so the
	// JWKS publication endpoint, the rotation lifecycle audit event,
	// and a future operator-driven Rotate call all converge on one
	// canonical key holder. The rotating verifier starts with the
	// boot-time KMS signer as its sole current key; until a Rotate
	// lands, JWKSHandler advertises exactly that key and the bearer
	// path verifies against it. The §13.3 24h overlap window is the
	// jwt.DefaultOverlapWindow default.
	// Verifier uses kmsBackedSigner directly: verification is local
	// memory and doesn't reach KMS, so it must not gate on the §10.2
	// signing breaker. F-10.2.6.
	rotatingVerifier := jwt.NewRotatingVerifier(kmsBackedSigner, jwt.DefaultOverlapWindow)

	// ----- §10.2 Bearer verifier -----
	// The Token Service signer verifies tokens it minted itself. A
	// production install runs with that single verifier. §17.4 Embedded
	// Mode additionally trusts the embedded OIDC provider's HMAC key:
	// when --bearer-trust-hmac-key-file points at a key file, the
	// gateway loads it and accepts tokens signed with it alongside
	// Token Service tokens through a jwt.MultiVerifier. The flag is
	// unset in a production install, so the production posture is
	// unchanged. The Token Service signer stays the primary verifier:
	// its rejection reason is surfaced when neither verifier accepts a
	// token. The verifier is the §10.3 RotatingVerifier; once a
	// rotation lands, a token signed by the now-previous key keeps
	// verifying through the overlap window without a code change here.
	var bearerVerifier jwt.Verifier = rotatingVerifier
	if *bearerTrustHMACKeyFile != "" {
		// spec: §10.2. The bare HMAC signer is the dev-mode
		// backend; the spec is explicit that it "must never be used in
		// production deployments". --bearer-trust-hmac-key-file is the
		// §17.4 Embedded Mode hook that trusts the bundled OIDC
		// provider's HMAC key; it has no production use case. Refuse
		// to load it when --dev-mode is off so a misconfigured chart
		// fails closed at startup instead of silently widening the
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
		// embedded OIDC audience (dev.local) so a foreign-audience token —
		// even one validly signed by the trusted key — is refused at the
		// gateway. The Token Service path (rotatingVerifier) is unaffected.
		// F-17.4.16.
		bearerVerifier = jwt.NewMultiVerifier(rotatingVerifier, embeddedHMACVerifier(trusted))
		log.Printf("lenny-gateway: trusting an additional HMAC bearer key from %s (kid %s); embedded tokens must carry aud=dev.local",
			*bearerTrustHMACKeyFile, trusted.KeyID())
	}
	// spec: §10.2 — wrap the verifier so the standard auth
	// chain enforces iss / aud alongside signature / exp / nbf when an
	// operator configures the expected values. An unset flag skips
	// the corresponding check so dev deployments retain their existing
	// posture.
	expectedAuds := splitCSV(*bearerExpectedAudiences)
	if *bearerExpectedIssuer != "" || len(expectedAuds) > 0 {
		bearerVerifier = jwt.NewClaimChecker(bearerVerifier, jwt.ExpectedClaims{
			Issuer:    *bearerExpectedIssuer,
			Audiences: expectedAuds,
		})
		log.Printf("lenny-gateway: §10.2 bearer iss/aud enforced iss=%q audiences=%v",
			*bearerExpectedIssuer, expectedAuds)
	}
	// §13.3 canonical surface: the gateway does NOT mint tokens
	// in-process. The /v1/oauth/* HTTP path is reverse-proxied to
	// lenny-token-service per --token-service-http-url, so the
	// Token Service is the only component holding the signing key
	// and the only component writing token.exchanged audit rows.
	// spec: §4.3 / F-4.3.12.

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
	w.kmsBackedSigner = kmsBackedSigner
	w.jwtSigner = jwtSigner
	w.rotatingVerifier = rotatingVerifier
	w.bearerVerifier = bearerVerifier
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

// embeddedHMACVerifier wraps the trusted embedded OIDC HMAC verifier so
// the gateway refuses any token whose aud claim is not the embedded
// provider's audience, even when the signature is valid. §17.4
// requires the gateway to reject foreign-audience tokens; the embedded
// provider's own Verify enforces this, but the gateway trusts the key
// directly and must apply the same check on its side. F-17.4.16.
func embeddedHMACVerifier(trusted jwt.Verifier) jwt.Verifier {
	return jwt.NewClaimChecker(trusted, jwt.ExpectedClaims{
		Audiences: []string{embeddedOIDCAudience},
	})
}
