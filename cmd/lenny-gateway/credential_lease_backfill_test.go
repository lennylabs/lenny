// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
)

// recordingBackfiller is an expiresAtBackfiller test double: it records the
// invocation and returns a configurable filled/deleted count and error.
type recordingBackfiller struct {
	called          chan struct{}
	filled, deleted int
	err             error
}

func newRecordingBackfiller(filled, deleted int, err error) *recordingBackfiller {
	return &recordingBackfiller{called: make(chan struct{}, 1), filled: filled, deleted: deleted, err: err}
}

func (b *recordingBackfiller) BackfillExpiresAt(context.Context) (int, int, error) {
	select {
	case b.called <- struct{}{}:
	default:
	}
	return b.filled, b.deleted, b.err
}

// backfillableLeaseStore is a credleasestore.LeaseStore that also implements
// expiresAtBackfiller, so it can be wired as w.llmLeases (the Postgres backend
// does the same) and drive the type-assertion selection in
// startCredentialLeaseExpiresAtBackfill.
type backfillableLeaseStore struct {
	credleasestore.LeaseStore
	*recordingBackfiller
}

// TestRunCredentialLeaseExpiresAtBackfillInvokesStore pins that the one-time
// §4.9 convergence pass actually calls BackfillExpiresAt on the store. Before
// this wiring the method was implemented but never invoked, so a pre-migration
// NULL-expires_at row lingered past its TTL and kept its deny-list entry.
//
// spec: §4.9 line 1671.
func TestRunCredentialLeaseExpiresAtBackfillInvokesStore(t *testing.T) {
	b := newRecordingBackfiller(3, 1, nil)
	runCredentialLeaseExpiresAtBackfill(context.Background(), b)
	select {
	case <-b.called:
	default:
		t.Fatal("BackfillExpiresAt was not invoked by the startup pass")
	}
}

// TestRunCredentialLeaseExpiresAtBackfillLogsAndReturnsOnError pins that a
// store error is logged and does not panic or block, so a boot-time fault
// leaves the pre-migration rows for the next restart rather than crashing.
//
// spec: §4.9 line 1671.
func TestRunCredentialLeaseExpiresAtBackfillLogsAndReturnsOnError(t *testing.T) {
	b := newRecordingBackfiller(0, 0, errors.New("postgres unavailable"))
	runCredentialLeaseExpiresAtBackfill(context.Background(), b)
	select {
	case <-b.called:
	default:
		t.Fatal("BackfillExpiresAt was not invoked on the error path")
	}
}

// TestStartCredentialLeaseExpiresAtBackfillWiresPostgresBackend pins that
// startBillingAndSecurityWorkers' wiring launches the backfill when the lease
// store is the Postgres backend (which carries BackfillExpiresAt). This is the
// regression against the never-invoked defect: the method existed but no
// startup path called it.
//
// spec: §4.9 line 1671.
func TestStartCredentialLeaseExpiresAtBackfillWiresPostgresBackend(t *testing.T) {
	b := newRecordingBackfiller(2, 0, nil)
	w := &gatewayWiring{}
	w.watchdogCtx = context.Background()
	w.llmLeases = &backfillableLeaseStore{LeaseStore: credleasestore.New(), recordingBackfiller: b}

	if !w.startCredentialLeaseExpiresAtBackfill() {
		t.Fatal("startCredentialLeaseExpiresAtBackfill did not launch the pass for a backend that implements it")
	}
	select {
	case <-b.called:
	case <-time.After(2 * time.Second):
		t.Fatal("the launched backfill goroutine never invoked BackfillExpiresAt")
	}
}

// TestStartCredentialLeaseExpiresAtBackfillSkipsInMemoryBackend pins that the
// in-memory lease store, which keeps ExpiresAt on the struct and carries no
// projection column, is not backfilled.
//
// spec: §4.9 line 1671.
func TestStartCredentialLeaseExpiresAtBackfillSkipsInMemoryBackend(t *testing.T) {
	w := &gatewayWiring{}
	w.watchdogCtx = context.Background()
	w.llmLeases = credleasestore.New()

	if w.startCredentialLeaseExpiresAtBackfill() {
		t.Fatal("the in-memory lease store must not be backfilled")
	}
}
