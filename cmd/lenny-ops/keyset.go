// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// kmsResolver resolves the KMS provider from the parsed flags.
// providerflags.Resolve is the production resolver.
type kmsResolver func(context.Context, providerflags.Options) (kms.Provider, error)

// loadOpsKeySet resolves the KMS provider and loads the §10.2 verifier key
// set from it. In production mode (--environment=prod) providerflags.Resolve
// refuses a provider that resolves to local, including an empty one, with
// providerflags.ErrLocalForbidden. Every error is fatal to the caller.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation"; §25.4
// (The lenny-ops Service: Startup and Readiness); §17.4 "Dev mode guard
// rails"; §17.9.1 (Composition Dimensions) "Production mode".
func loadOpsKeySet(ctx context.Context, opts providerflags.Options, resolve kmsResolver) (*jwt.KeySet, error) {
	provider, err := resolve(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("kms provider: %w", err)
	}
	keySet, err := jwt.LoadKeySet(ctx, provider, opts.AliasToKey)
	if err != nil {
		return nil, fmt.Errorf("§10.2 JWT key set: %w", err)
	}
	return keySet, nil
}

// buildKeySet loads the §10.2 verifier key set once at startup, before the
// HTTP listener starts, and exits the process when it cannot. It first
// refuses the dev trust hook in production mode (--environment=prod) or
// without --dev-mode, so
// that refusal does not depend on the KMS being reachable. spec: §25.4
// (The lenny-ops Service: Startup and Readiness).
func (w *opsWiring) buildKeySet() {
	if err := checkDevTrustPosture(*w.f.devMode, w.f.productionMode(), *w.f.bearerTrustHMACKeyFile); err != nil {
		log.Fatalf("lenny-ops: %v", err)
	}
	keySet, err := loadOpsKeySet(w.ctx, *w.f.kmsOpts, providerflags.Resolve)
	if err != nil {
		log.Fatalf("lenny-ops: %v", err)
	}
	log.Printf("lenny-ops: §10.2 JWT key set loaded (%d key(s), provider %s)", keySet.Len(), w.f.kmsOpts.Provider)
	w.jwtKeySet = keySet
}
