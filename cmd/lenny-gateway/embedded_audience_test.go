// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// spec: §17.4 — "the embedded OIDC provider refuses any audience
// claim not matching dev.local; the gateway rejects externally-issued
// tokens." The gateway trusts the embedded OIDC HMAC key directly via
// --bearer-trust-hmac-key-file; embeddedHMACVerifier pins that path to the
// dev.local audience so a foreign-audience token is refused at the gateway
// even when its signature is valid. F-17.4.16.

// signFor mints an HMAC token with the given audience and a short expiry.
func signFor(t *testing.T, s *jwt.HMACSigner, aud string) string {
	t.Helper()
	now := time.Now()
	tok, err := s.Sign(jwt.Claims{
		Subject:   "alice@dev.local",
		Audience:  []string{aud},
		IssuedAt:  now.Unix(),
		NotBefore: now.Add(-time.Minute).Unix(),
		Expiry:    now.Add(time.Hour).Unix(),
		TenantID:  "default",
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func TestEmbeddedHMACVerifierRejectsForeignAudience_spec_17_4_182(t *testing.T) {
	embedded := jwt.NewHMACSigner("embedded-kid", []byte("embedded-signing-secret-0123456789"))
	v := embeddedHMACVerifier(embedded, "tenant_id")

	if _, err := v.Verify(signFor(t, embedded, embeddedOIDCAudience)); err != nil {
		t.Fatalf("dev.local token rejected: %v", err)
	}
	if _, err := v.Verify(signFor(t, embedded, "lenny-gateway")); err == nil {
		t.Error("foreign-audience token accepted; want rejection")
	}
}

// TestEmbeddedAudienceCompositionMatchesGateway mirrors the gateway's
// MultiVerifier(keySet, embedded) wiring: a Lenny-issued token verifies
// through the key-set member regardless of its audience, while an
// embedded-key token is accepted only when it carries the dev.local
// audience. F-17.4.16.
func TestEmbeddedAudienceCompositionMatchesGateway_spec_17_4_182(t *testing.T) {
	keySet, signer := gatewaySigning(t, localSigningProvider(t, 0x66, "local-key-1"))
	embedded := jwt.NewHMACSigner("embedded-kid", []byte("embedded-signing-secret-0123456789"))
	bearer := embeddedBearerVerifier(keySet, embedded, "tenant_id")
	now := time.Now()
	lennyIssued, err := signer.Sign(context.Background(), jwt.Claims{
		Subject:  "alice@dev.local",
		Audience: []string{"lenny-gateway"},
		IssuedAt: now.Unix(),
		Expiry:   now.Add(time.Hour).Unix(),
		TenantID: "default",
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	cases := []struct {
		name   string
		token  string
		accept bool
	}{
		{"Lenny-issued token (any aud) accepted via the key set", lennyIssued, true},
		{"embedded token with dev.local accepted", signFor(t, embedded, embeddedOIDCAudience), true},
		{"embedded token with foreign aud rejected", signFor(t, embedded, "lenny-gateway"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bearer.Verify(tc.token)
			if tc.accept && err != nil {
				t.Errorf("token rejected, want accepted: %v", err)
			}
			if !tc.accept && err == nil {
				t.Error("token accepted, want rejected")
			}
		})
	}
}
