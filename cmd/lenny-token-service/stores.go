// SPDX-License-Identifier: MIT

package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/tokenservice/promemit"
)

// buildMetrics constructs the §16.1 Prometheus metric vectors. The §16.5
// TokenServiceUnavailable alert reads lenny_token_service_circuit_state from
// the gateway-side breaker (set in cmd/lenny-gateway/main.go); the metrics
// emitted here are the §16.1 request_duration, errors, secret_reloads, and the
// §13.3 rate-limit counters.
//
// spec: §16.1.
func (w *tokenServiceWiring) buildMetrics() {
	metricsEmitter, err := promemit.New()
	if err != nil {
		fatalf("metrics emitter: %v", err)
	}
	w.metricsEmitter = metricsEmitter
}

// buildStores wires the §13.3 line 589 durable write-before-issue state. With
// Postgres, every minted token is recorded in issued_tokens and every
// accepted/rejected exchange writes a token.exchanged audit row in the same
// transaction under the §11.7 advisory lock. With no Postgres, the Token
// Service runs without durable issued-token or audit state and keeps an
// in-memory audit chain so the rate-limit and revocation emits still produce
// rows (lost on restart). The Postgres pool is recorded on the accumulator so
// runTokenService can defer its close.
//
// spec: §13.3 line 589, §12.3 R-03 / F-12.3.4.
func (w *tokenServiceWiring) buildStores() {
	if *w.f.postgresDSN == "" {
		// Dev path: keep an in-memory chain so the rate-limit /
		// revocation audit emits still produce rows (lost on restart).
		// The /v1/oauth/token success path falls through to
		// IssuedTokenStore.Record + Auditor.Append (no Postgres tx).
		chains := audit.NewChainSet()
		w.auditor = policy.NewChainSetAppender(chains, nil)
		return
	}

	pool, err := pgxpool.New(ctx, *w.f.postgresDSN)
	if err != nil {
		fatalf("postgres: %v", err)
	}
	w.pgPool = pool
	// Postgres-backed issuedtokenstore.Store also satisfies
	// IssuedTokenAuditStore, so the handler uses the combined
	// write-before-issue tx. The in-memory auditor is wired
	// alongside to cover the rate-limit and revocation paths.
	w.issuedTokens = issuedtokenstore.New(pool)
	// §12.3 R-03: the audit chain routes its writes through the
	// StoreRouter rather than holding a raw pool. The Token Service
	// audit path is Postgres-only (it never touches Redis), so the
	// single-shard router is built in Postgres-only mode. F-12.3.4.
	auditRouter, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pool})
	if err != nil {
		fatalf("store router: %v", err)
	}
	w.auditor = auditstore.New(auditRouter)
}
