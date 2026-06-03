// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// spec: §17.4 line 182 — "the embedded OIDC provider refuses any audience
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
	v := embeddedHMACVerifier(embedded)

	if _, err := v.Verify(signFor(t, embedded, embeddedOIDCAudience)); err != nil {
		t.Fatalf("dev.local token rejected: %v", err)
	}
	if _, err := v.Verify(signFor(t, embedded, "lenny-gateway")); err == nil {
		t.Error("foreign-audience token accepted; want rejection")
	}
}

// TestEmbeddedAudienceCompositionMatchesGateway mirrors the gateway's
// MultiVerifier(primary, embedded) wiring: a Token Service token verifies
// through the primary member regardless of its audience, while an
// embedded-key token is accepted only when it carries the dev.local
// audience. F-17.4.16.
func TestEmbeddedAudienceCompositionMatchesGateway_spec_17_4_182(t *testing.T) {
	primary := jwt.NewHMACSigner("token-service-kid", []byte("token-service-secret-abcdefghijklmnop"))
	embedded := jwt.NewHMACSigner("embedded-kid", []byte("embedded-signing-secret-0123456789"))
	bearer := jwt.NewMultiVerifier(primary, embeddedHMACVerifier(embedded))

	cases := []struct {
		name   string
		token  string
		accept bool
	}{
		{"token-service token (any aud) accepted via primary", signFor(t, primary, "lenny-gateway"), true},
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
