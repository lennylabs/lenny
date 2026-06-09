// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quotafailopen"
	"github.com/lennylabs/lenny/pkg/quota"
)

// spec: §12.8 step 6 — with no quota backend wired the gateway skips the
// quota erasure step entirely rather than wiring a no-op eraser.
func TestBuildQuotaEraser_NoBackendsReturnsNil(t *testing.T) {
	if got := buildQuotaEraser(nil, nil, nil); got != nil {
		t.Errorf("buildQuotaEraser(nil,nil,nil) = %v, want nil", got)
	}
}

// spec: §12.8 step 6 — a no-Redis / no-Postgres posture still erases the
// in-memory fail-open accumulator so a recovery reconcile cannot resurrect
// the erased user's usage.
func TestBuildQuotaEraser_AccumulatorOnlyErases(t *testing.T) {
	accum := quotafailopen.New()
	at := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	accum.Record("acme", "alice", quota.ResetHourly, at, 100)
	accum.Record("acme", "bob", quota.ResetHourly, at, 40)

	eraser := buildQuotaEraser(nil, nil, accum)
	if eraser == nil {
		t.Fatal("buildQuotaEraser with accumulator returned nil")
	}
	if _, err := eraser.DeleteByUser(context.Background(), "acme", "alice"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if got := accum.UserWindow("acme", "alice", quota.ResetHourly, at); got != 0 {
		t.Errorf("alice window after composite erase = %d, want 0", got)
	}
	if got := accum.UserWindow("acme", "bob", quota.ResetHourly, at); got != 40 {
		t.Errorf("bob window after alice erase = %d, want 40", got)
	}

	// Tenant erasure drops the whole tenant including the rollup.
	if _, err := eraser.DeleteByTenant(context.Background(), "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if accum.Len() != 0 {
		t.Errorf("accumulator not empty after tenant erase: %d entries", accum.Len())
	}
}
