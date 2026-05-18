// SPDX-License-Identifier: MIT

package tenantdeletion_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// spec: §12.8 — the tenant-deletion controller is a timer-driven
// manager.Runnable (the Postgres-backed tenant registry cannot be
// watched), running only on the elected leader.

func TestRunnableNeedsLeaderElection(t *testing.T) {
	rn := &tenantdeletion.Runnable{}
	if !rn.NeedLeaderElection() {
		t.Error("the tenant-deletion controller must run only on the elected leader")
	}
}

func TestRunnableReconcilesOnStartAndStopsOnContextCancel(t *testing.T) {
	seed := bytes.Repeat([]byte{0x5a}, kms.DEKSize)
	local, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	action := &fakeAction{}
	r := &tenantdeletion.Reconciler{
		Jobs:       tenantdeletion.NewMemory(),
		KMS:        tenantkms.New(tenantkms.NewLocalManager(local)),
		Disabler:   action,
		Terminator: action,
		Revoker:    action,
		Eraser:     &fakeEraser{counts: map[string]int{}},
		Cleaner:    action,
		Receipts:   &fakeReceipts{},
	}
	if err := r.Start(context.Background(), "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rn := &tenantdeletion.Runnable{Reconciler: r, Interval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rn.Start(ctx) }()

	// Start reconciles once immediately. Give it a moment, then cancel.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Runnable.Start returned %v, want nil on context cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Runnable.Start did not return after context cancel")
	}

	// The immediate initial reconcile advanced the job one phase.
	j, _ := r.Jobs.Get(context.Background(), "acme")
	if j.Phase == tenantdeletion.PhaseSoftDisable {
		t.Error("the initial reconcile pass should have advanced the job past phase 1")
	}
}
