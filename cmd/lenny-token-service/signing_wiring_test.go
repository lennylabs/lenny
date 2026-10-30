// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// signingWiringFlags returns the flags the KMS, store, and server build
// steps read, on the local KMS provider over masterKeyFile, without
// Postgres or Redis.
func signingWiringFlags(masterKeyFile string) *tokenServiceFlags {
	empty := ""
	issuer := "https://lenny.acme.com/token"
	ttl := 900
	perSec, perMin, tenantPerSec := 10, 100, 50
	window := time.Minute
	return &tokenServiceFlags{
		postgresDSN:        &empty,
		redisURL:           &empty,
		issuer:             &issuer,
		capabilityTokenTTL: &ttl,
		rlCallerPerSec:     &perSec,
		rlCallerPerMin:     &perMin,
		rlTenantPerSec:     &tenantPerSec,
		rlSampleWindow:     &window,
		kmsOpts: &providerflags.Options{
			Provider:      providerflags.ProviderLocal,
			MasterKeyFile: masterKeyFile,
			AliasToKey:    map[string]string{kms.AliasJWTSigning: "local-key-1"},
		},
	}
}

// TestTokenServiceSignsWithTheSharedKeySet_spec_10_2 drives the Token
// Service startup steps from the KMS flags through the token-exchange
// server. The signer they build signs with the platform:jwt-signing key
// of the loaded key set, a token it signs verifies through that key set,
// and a gateway process loading the key set from the same master key file
// verifies it too, so a Token Service token is accepted on every replica.
//
// spec: 10.2 (Authentication) "Signing topology", 10.2 (Authentication) "Verifier key set and rotation", 4.3 (Token Service)
func TestTokenServiceSignsWithTheSharedKeySet_spec_10_2(t *testing.T) {
	f := signingWiringFlags(filepath.Join(t.TempDir(), "master.key"))
	w := &tokenServiceWiring{f: f}
	w.buildMetrics()
	w.buildKMSAndSigner()
	w.buildStores()
	w.buildDriftMonitor()
	w.buildServer()
	if w.keySet == nil || w.signer == nil || w.srv == nil || w.auditor == nil {
		t.Fatalf("startup left the key set, signer, server, or auditor unset: %+v", w)
	}

	now := time.Now()
	tok, err := w.signer.Sign(context.Background(), jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer, JWTID: "jti-ts",
		IssuedAt: now.Unix(), Expiry: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := w.keySet.Verify(tok); err != nil {
		t.Errorf("the Token Service key set refuses its own signer's token: %v", err)
	}

	provider, err := providerflags.Resolve(context.Background(), *f.kmsOpts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	gatewayKeySet, err := jwt.LoadKeySet(context.Background(), provider, f.kmsOpts.AliasToKey)
	if err != nil {
		t.Fatalf("gateway key set: %v", err)
	}
	if claims, err := gatewayKeySet.Verify(tok); err != nil || claims.TenantID != "acme" {
		t.Errorf("a gateway key set over the same master key file: claims %+v err %v, want the token admitted", claims, err)
	}
}
