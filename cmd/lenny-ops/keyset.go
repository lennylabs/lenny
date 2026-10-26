// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// errLocalProviderInProduction refuses the local KMS provider under
// --production. --production is lenny-ops's production signal, so the
// providerflags check on --environment alone would admit local under
// --production without LENNY_ENV=prod.
var errLocalProviderInProduction = errors.New("§17.4: --kms-provider=local is refused when --production is set; select aws, gcp, or azure")

// kmsResolver resolves the KMS provider from the parsed flags.
// providerflags.Resolve is the production resolver.
type kmsResolver func(context.Context, providerflags.Options) (kms.Provider, error)

// loadOpsKeySet resolves the KMS provider and loads the §10.2 verifier key
// set from it. Under --production it refuses a provider that resolves to
// local before resolving anything. Every error is fatal to the caller,
// with or without --production.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation"; §25.4
// (The lenny-ops Service: Startup and Readiness); §17.4 "Dev mode guard
// rails".
func loadOpsKeySet(ctx context.Context, opts providerflags.Options, production bool, resolve kmsResolver) (*jwt.KeySet, error) {
	if production && resolvesToLocal(opts.Provider) {
		return nil, errLocalProviderInProduction
	}
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

// resolvesToLocal reports whether providerflags.Resolve picks the local
// provider for the --kms-provider value, which it does for an empty one.
func resolvesToLocal(provider string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	return p == "" || p == providerflags.ProviderLocal
}

// buildKeySet loads the §10.2 verifier key set once at startup, before the
// HTTP listener starts, and exits the process when it cannot. It first
// refuses the dev trust hook under --production or without --dev-mode, so
// that refusal does not depend on the KMS being reachable. spec: §25.4
// (The lenny-ops Service: Startup and Readiness).
func (w *opsWiring) buildKeySet() {
	if err := checkDevTrustPosture(*w.f.devMode, *w.f.production, *w.f.bearerTrustHMACKeyFile); err != nil {
		log.Fatalf("lenny-ops: %v", err)
	}
	keySet, err := loadOpsKeySet(w.ctx, *w.f.kmsOpts, *w.f.production, providerflags.Resolve)
	if err != nil {
		log.Fatalf("lenny-ops: %v", err)
	}
	log.Printf("lenny-ops: §10.2 JWT key set loaded (%d key(s), provider %s)", keySet.Len(), w.f.kmsOpts.Provider)
	w.jwtKeySet = keySet
}
