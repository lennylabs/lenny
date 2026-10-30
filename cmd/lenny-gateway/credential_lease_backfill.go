// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
)

// expiresAtBackfiller converges the plain expires_at projection for
// credential_leases rows written before migration 0175, which carry a NULL
// expires_at that the sweep cannot treat as expired. Only the Postgres-backed
// lease store implements it; the in-memory store keeps ExpiresAt on the struct
// and needs no backfill.
type expiresAtBackfiller interface {
	BackfillExpiresAt(ctx context.Context) (filled, deleted int, err error)
}

// startCredentialLeaseExpiresAtBackfill launches the one-time §4.9 expires_at
// convergence pass in the background when the lease store is the Postgres
// backend, so pre-migration NULL-expires_at rows are filled or, when already
// expired, removed rather than lingering past their TTL and keeping their
// deny-list entry. It is a no-op on the in-memory store, which keeps ExpiresAt
// on the struct. It returns whether the pass was launched.
//
// spec: §4.9 — a deny-list entry expires when the credential's
// natural lease TTL lapses.
func (w *gatewayWiring) startCredentialLeaseExpiresAtBackfill() bool {
	backfiller, ok := w.llmLeases.(expiresAtBackfiller)
	if !ok {
		return false
	}
	go runCredentialLeaseExpiresAtBackfill(w.watchdogCtx, backfiller)
	return true
}

// runCredentialLeaseExpiresAtBackfill runs the one-time §4.9 expires_at
// convergence pass and logs the filled and deleted counts. It is not retried on
// error: active leases re-populate expires_at on their next renewal Put, so a
// transient boot-time fault leaves only pre-migration rows for the next
// restart's pass to converge rather than opening a security gap.
//
// spec: §4.9 — a deny-list entry expires when the credential's
// natural lease TTL lapses.
func runCredentialLeaseExpiresAtBackfill(ctx context.Context, backfiller expiresAtBackfiller) {
	filled, deleted, err := backfiller.BackfillExpiresAt(ctx)
	if err != nil {
		log.Printf("lenny-gateway: §4.9 credential-lease expires_at backfill failed: %v", err)
		return
	}
	if filled > 0 || deleted > 0 {
		log.Printf("lenny-gateway: §4.9 credential-lease expires_at backfill filled %d pre-migration row(s) and deleted %d already-expired row(s)",
			filled, deleted)
	}
}
