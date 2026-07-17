// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
)

// poolCredKey is the source-aware deny-list key for a pool-backed
// credential in the claude-prod pool.
func poolCredKey(credID string) credential.CredentialKey {
	return credential.CredentialKey{Source: credential.SourcePool, PoolID: "claude-prod", CredentialID: credID}
}

// expiredCredLease returns a pool-backed proxy lease against credID whose
// ExpiresAt is expiresAt (so a sweep can drive it past its TTL).
func expiredCredLease(leaseID, credID string, expiresAt time.Time) credential.Lease {
	l := credLease(leaseID, credID)
	l.ExpiresAt = expiresAt
	return l
}

// TestExpiredLeaseSweepExpiresDenyEntryWhenLastLeaseLapses asserts the
// §4.9 sweep deletes an expired lease row and then drops its credential's
// deny entry once the store reports no remaining active lease, realizing
// the "expire when the credential's natural lease TTL lapses" promise.
//
// spec: §4.9 line 1671.
func TestExpiredLeaseSweepExpiresDenyEntryWhenLastLeaseLapses(t *testing.T) {
	now := time.Now()
	leases := credleasestore.New()
	deny := denylist.New()
	_ = leases.Put(expiredCredLease("l1", "key-1", now.Add(-time.Hour)))
	deny.Revoke(poolCredKey("key-1"))

	swept, denyRemoved, err := sweepExpiredCredentialLeases(context.Background(), leases, deny, now)
	if err != nil {
		t.Fatalf("sweep error: %v", err)
	}
	if swept != 1 {
		t.Errorf("swept = %d, want 1 (the expired lease row)", swept)
	}
	if denyRemoved != 1 {
		t.Errorf("denyRemoved = %d, want 1 (the credential's deny entry)", denyRemoved)
	}
	if _, ok := leases.GetByID("l1"); ok {
		t.Error("expired lease l1 must be deleted from the store")
	}
	if deny.Revoked(poolCredKey("key-1")) {
		t.Error("key-1 deny entry must expire once its last lease lapses")
	}
}

// TestExpiredLeaseSweepRetainsDenyEntryWhileLeaseResolves asserts the
// sweep keeps a revoked credential's deny entry (and its retained lease)
// while any active lease still resolves against it, so the proxy keeps
// rejecting it with CREDENTIAL_REVOKED until the lease lapses.
//
// spec: §4.9 line 1671.
func TestExpiredLeaseSweepRetainsDenyEntryWhileLeaseResolves(t *testing.T) {
	now := time.Now()
	leases := credleasestore.New()
	deny := denylist.New()
	_ = leases.Put(expiredCredLease("l1", "key-1", now.Add(time.Hour)))
	deny.Revoke(poolCredKey("key-1"))

	swept, denyRemoved, err := sweepExpiredCredentialLeases(context.Background(), leases, deny, now)
	if err != nil {
		t.Fatalf("sweep error: %v", err)
	}
	if swept != 0 {
		t.Errorf("swept = %d, want 0 (the lease has not lapsed)", swept)
	}
	if denyRemoved != 0 {
		t.Errorf("denyRemoved = %d, want 0 (the active lease keeps the entry)", denyRemoved)
	}
	if _, ok := leases.GetByID("l1"); !ok {
		t.Error("an active retained lease must not be swept")
	}
	if !deny.Revoked(poolCredKey("key-1")) {
		t.Error("key-1 deny entry must stay while an active lease resolves")
	}
}

// TestExpiredLeaseSweepBoundsNonWinningPeerReplica models the shared
// Postgres store: one replica deletes the expired row, but the peer
// replica that deleted nothing still bounds its own in-memory deny list
// by re-checking each key against the shared store via Keys() + count.
// A removal set derived from the deleted rows would leave the peer's
// entry in place until restart, which is defect (C)'s unbounded growth.
//
// spec: §4.9 line 1671.
func TestExpiredLeaseSweepBoundsNonWinningPeerReplica(t *testing.T) {
	now := time.Now()
	shared := credleasestore.New()
	_ = shared.Put(expiredCredLease("l1", "key-1", now.Add(-time.Hour)))
	denyWinner := denylist.New()
	denyPeer := denylist.New()
	denyWinner.Revoke(poolCredKey("key-1"))
	denyPeer.Revoke(poolCredKey("key-1"))

	// The winning replica deletes the shared expired row and bounds its list.
	swept, _, err := sweepExpiredCredentialLeases(context.Background(), shared, denyWinner, now)
	if err != nil {
		t.Fatalf("winner sweep error: %v", err)
	}
	if swept != 1 {
		t.Fatalf("winner swept = %d, want 1", swept)
	}

	// The peer replica deletes nothing (the row is already gone) but must
	// still drop its own deny entry by re-checking the shared store.
	peerSwept, peerRemoved, err := sweepExpiredCredentialLeases(context.Background(), shared, denyPeer, now)
	if err != nil {
		t.Fatalf("peer sweep error: %v", err)
	}
	if peerSwept != 0 {
		t.Errorf("peer swept = %d, want 0 (the winner already deleted the row)", peerSwept)
	}
	if peerRemoved != 1 {
		t.Errorf("peer denyRemoved = %d, want 1 (the peer bounds its own list via Keys())", peerRemoved)
	}
	if denyPeer.Revoked(poolCredKey("key-1")) {
		t.Error("the non-winning peer replica's deny entry must still expire")
	}
}

// errLeaseSweeper is a leaseSweeper whose count query fails, modeling a
// transient Postgres or KMS fault during the sweep's reconcile step.
type errLeaseSweeper struct {
	deleteErr error
	countErr  error
}

func (e errLeaseSweeper) DeleteExpired(context.Context, time.Time) (int, error) {
	return 0, e.deleteErr
}

func (e errLeaseSweeper) LeasesByCredentialCount(context.Context, credential.CredentialKey, time.Time) (int, error) {
	return 0, e.countErr
}

// TestExpiredLeaseSweepFailsClosedOnStoreError asserts the reconcile keeps
// a live deny entry when the store cannot answer the lease-existence
// query, so a store fault never removes a deny entry and opens a
// CREDENTIAL_REVOKED bypass. A count query that returns (0, nil) would
// wrongly drop the entry; the error path must not.
//
// spec: §4.9 line 1671 (fail closed on the security path).
func TestExpiredLeaseSweepFailsClosedOnStoreError(t *testing.T) {
	now := time.Now()
	deny := denylist.New()
	deny.Revoke(poolCredKey("key-1"))

	swept, denyRemoved, err := sweepExpiredCredentialLeases(context.Background(),
		errLeaseSweeper{countErr: errors.New("postgres unavailable")}, deny, now)
	if err != nil {
		t.Fatalf("a count-query error must not abort the sweep: %v", err)
	}
	if swept != 0 || denyRemoved != 0 {
		t.Errorf("swept=%d denyRemoved=%d, want 0/0 on a count-query error", swept, denyRemoved)
	}
	if !deny.Revoked(poolCredKey("key-1")) {
		t.Error("the deny entry must be retained when the store cannot enumerate leases (fail closed)")
	}
}

// TestExpiredLeaseSweepAbortsOnDeleteError asserts a DeleteExpired failure
// surfaces as an error and leaves the deny list untouched for this tick.
//
// spec: §4.9 line 1671.
func TestExpiredLeaseSweepAbortsOnDeleteError(t *testing.T) {
	now := time.Now()
	deny := denylist.New()
	deny.Revoke(poolCredKey("key-1"))

	_, _, err := sweepExpiredCredentialLeases(context.Background(),
		errLeaseSweeper{deleteErr: errors.New("delete failed")}, deny, now)
	if err == nil {
		t.Fatal("a DeleteExpired error must surface from the sweep")
	}
	if !deny.Revoked(poolCredKey("key-1")) {
		t.Error("the deny list must be untouched when the delete step fails")
	}
}

// countingSweepMetrics records the swept counts the §4.9 sweep loop reports,
// standing in for *gatewaymetrics.Metrics so a test can observe the loop
// bumped the counter.
type countingSweepMetrics struct {
	mu    sync.Mutex
	swept int
}

func (c *countingSweepMetrics) AddCredentialLeasesSwept(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.swept += n
}

func (c *countingSweepMetrics) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.swept
}

// TestCredentialLeaseSweepLoopReclaimsThenStops asserts the §4.9 sweep loop
// runs a tick that deletes an expired lease and expires its deny entry,
// records the swept count on metrics, and returns when its context is
// cancelled.
//
// spec: §4.9 line 1671.
func TestCredentialLeaseSweepLoopReclaimsThenStops(t *testing.T) {
	now := time.Now()
	leases := credleasestore.New()
	deny := denylist.New()
	_ = leases.Put(expiredCredLease("l1", "key-1", now.Add(-time.Hour)))
	deny.Revoke(poolCredKey("key-1"))
	metrics := &countingSweepMetrics{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runCredentialLeaseSweepLoop(ctx, leases, deny, metrics, time.Millisecond, time.Now)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for deny.Revoked(poolCredKey("key-1")) {
		select {
		case <-deadline:
			cancel()
			t.Fatal("the sweep loop never expired the deny entry")
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the sweep loop did not return on context cancellation")
	}
	if metrics.total() < 1 {
		t.Errorf("metrics swept total = %d, want at least 1", metrics.total())
	}
	if _, ok := leases.GetByID("l1"); ok {
		t.Error("the expired lease must be deleted by the sweep loop")
	}
}

// TestCredentialLeaseSweepLoopContinuesOnStoreError asserts a per-tick store
// error does not stop the loop: it logs and retries the next tick, and the
// loop still returns cleanly on context cancellation.
//
// spec: §4.9 line 1671 (fail closed without aborting the sweep worker).
func TestCredentialLeaseSweepLoopContinuesOnStoreError(t *testing.T) {
	deny := denylist.New()
	deny.Revoke(poolCredKey("key-1"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runCredentialLeaseSweepLoop(ctx,
			errLeaseSweeper{deleteErr: errors.New("postgres unavailable")},
			deny, &countingSweepMetrics{}, time.Millisecond, time.Now)
		close(done)
	}()

	// Let several failing ticks run, then cancel; the loop must still exit.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the sweep loop did not return after failing ticks and cancellation")
	}
	if !deny.Revoked(poolCredKey("key-1")) {
		t.Error("a failing sweep tick must leave the deny entry in place (fail closed)")
	}
}
