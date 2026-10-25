// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// buildKMSAndSigner resolves the §4 / §17.5 KMS provider from the parsed
// flags, loads the §10.2 key set once, and builds the shared remote
// ES256 signer from the key set's signing entry, wrapped in the JWTSigner
// circuit breaker. The Token Service signs with the same
// platform:jwt-signing key as the gateway, and the key set verifies the
// caller, subject, and actor tokens whichever process signed them. A
// process that cannot load its key set exits before it serves.
//
// spec: §10.2 (Authentication) "Signing topology", "Verifier key set and
// rotation"; §4.3 item 3 — the Token Service mints through the shared
// signer; F-4.3.11 / F-17.5.2 — the pluggable KMS provider.
func (w *tokenServiceWiring) buildKMSAndSigner() {
	kmsProvider, err := providerflags.Resolve(ctx, *w.f.kmsOpts)
	if err != nil {
		fatalf("kms provider: %v", err)
	}
	w.kmsProvider = kmsProvider
	log.Printf("lenny-token-service: §4 KMS provider = %s (environment=%s)",
		w.f.kmsOpts.Provider, w.f.kmsOpts.Environment)
	keySet, signer, err := jwt.LoadSigning(ctx, kmsProvider, w.f.kmsOpts.AliasToKey, nil)
	if err != nil {
		fatalf("§10.2 JWT key set: %v", err)
	}
	log.Printf("lenny-token-service: §10.2 JWT key set loaded (%d key(s), signing kid %s)",
		keySet.Len(), keySet.Signing().KeyID)
	w.keySet = keySet
	w.signer = signer
}
