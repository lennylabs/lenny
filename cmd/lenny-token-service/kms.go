// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// buildKMSAndSigner resolves the §4 / §17.5 KMS provider from the parsed flags
// and constructs the §4-KMS-envelope-backed, circuit-broken JWT signer. The
// in-process kms.Local provider is the no-cloud development KEK; cloud
// deployments swap in an AWS/GCP/Azure provider behind the same kms.Provider
// interface by setting --kms-provider. The KMS-backed signer is wrapped in the
// JWTSigner circuit breaker so KMS outages convert to KMS_SIGNING_UNAVAILABLE
// rather than hanging the request path.
//
// spec: F-4.3.11 / F-17.5.2 — the pluggable KMS provider; §10.2 line 225 /
// F-10.2.6 — the JWTSigner circuit breaker.
func (w *tokenServiceWiring) buildKMSAndSigner() {
	kmsProvider, err := providerflags.Resolve(ctx, *w.f.kmsOpts)
	if err != nil {
		fatalf("kms provider: %v", err)
	}
	w.kmsProvider = kmsProvider
	log.Printf("lenny-token-service: §4 KMS provider = %s (environment=%s)",
		w.f.kmsOpts.Provider, w.f.kmsOpts.Environment)
	kmsBackedSigner, err := jwt.NewKMSSigner(ctx, kmsProvider, jwt.TokenServiceKEKAlias, "dev-1")
	if err != nil {
		fatalf("kms-backed signer: %v", err)
	}
	w.signer = &jwt.BreakerSigner{Inner: kmsBackedSigner}
}
