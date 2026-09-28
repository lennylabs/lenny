// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
)

// credentialLeaseSweepMetrics records how many expired lease rows the §4.9
// sweep removed each tick. *gatewaymetrics.Metrics satisfies it; a test
// substitutes a counter.
type credentialLeaseSweepMetrics interface {
	AddCredentialLeasesSwept(n int)
}

// runCredentialLeaseSweepLoop runs the §4.9 expired-lease sweep on a ticker
// until ctx is cancelled. Each tick deletes credential_leases rows past
// ExpiresAt and reconciles this replica's deny list, failing closed on a
// store error (a failing tick leaves the deny entry in place and retries
// next interval). It records the swept-row count on metrics.
//
// spec: §4.9 — a deny-list entry expires when the credential's
// natural lease TTL lapses.
func runCredentialLeaseSweepLoop(ctx context.Context, leases leaseSweeper, deny denyReconciler, metrics credentialLeaseSweepMetrics, interval time.Duration, now func() time.Time) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			swept, denyRemoved, err := sweepExpiredCredentialLeases(ctx, leases, deny, now())
			if err != nil {
				log.Printf("lenny-gateway: §4.9 expired-lease sweep error: %v", err)
				continue
			}
			metrics.AddCredentialLeasesSwept(swept)
			if swept > 0 || denyRemoved > 0 {
				log.Printf("lenny-gateway: §4.9 expired-lease sweep removed %d lease row(s) past ExpiresAt and expired %d deny-list entr(ies)",
					swept, denyRemoved)
			}
		}
	}
}

// leaseSweeper bounds the credential_leases table and answers the
// per-credential lease-existence query the §4.9 deny-entry expiry gates on.
// LeasesByCredentialCount distinguishes a definitive zero from an
// unanswerable query so the reconcile can fail closed.
type leaseSweeper interface {
	DeleteExpired(ctx context.Context, cutoff time.Time) (int, error)
	LeasesByCredentialCount(ctx context.Context, key credential.CredentialKey, activeAsOf time.Time) (int, error)
}

// denyReconciler is the per-replica deny list the §4.9 sweep reconciles:
// it snapshots the current entries and removes one once its credential has
// no remaining active lease.
type denyReconciler interface {
	Keys() []credential.CredentialKey
	Remove(key credential.CredentialKey)
}

// sweepExpiredCredentialLeases runs one §4.9 expired-lease sweep tick. It
// first deletes lease rows past ExpiresAt to bound the credential_leases
// table, then reconciles the deny list by dropping a credential's deny
// entry only when the store reports a count of zero with no error (the
// store definitively holds no active lease, so no request can resolve to
// an unexpired lease and the entry is inert). On a store error or a
// positive count the entry stays for this tick and is retried next
// interval, because removing a live deny entry is a fail-closed
// CREDENTIAL_REVOKED bypass. It returns the number of expired lease rows
// removed and the number of deny entries expired.
//
// spec: §4.9 — a deny-list entry expires when the credential's
// natural lease TTL lapses.
func sweepExpiredCredentialLeases(ctx context.Context, leases leaseSweeper, deny denyReconciler, now time.Time) (swept int, denyRemoved int, err error) {
	swept, err = leases.DeleteExpired(ctx, now)
	if err != nil {
		return 0, 0, fmt.Errorf("§4.9 expired-lease sweep (delete): %w", err)
	}
	for _, key := range deny.Keys() {
		n, cerr := leases.LeasesByCredentialCount(ctx, key, now)
		if cerr != nil || n > 0 {
			continue
		}
		deny.Remove(key)
		denyRemoved++
	}
	return swept, denyRemoved, nil
}
