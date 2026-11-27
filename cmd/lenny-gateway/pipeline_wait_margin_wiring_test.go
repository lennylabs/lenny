// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	apisession "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/storage/retentiongc"
)

// TestPipelineWaitMarginFlagDefaultsToThirtySecondsAndLeavesTheBudgetAlone
// pins the composition root's pipeline wait margin: buildPersistenceStores
// reads --terminal-pipeline-wait-margin, 30 s by default, and the margin does
// not change the terminal pipeline budget the session store, the retention
// collector, and the session server read.
//
// spec: §12.8 (Lifecycle fence and drain)
func TestPipelineWaitMarginFlagDefaultsToThirtySecondsAndLeavesTheBudgetAlone(t *testing.T) {
	def := flagPostureWiring(t)
	if def.pipelineWaitMargin != 30*time.Second {
		t.Errorf("default margin = %v, want 30s", def.pipelineWaitMargin)
	}
	set := flagPostureWiring(t, "--terminal-pipeline-wait-margin=45s")
	if set.pipelineWaitMargin != 45*time.Second {
		t.Errorf("configured margin = %v, want 45s", set.pipelineWaitMargin)
	}
	if set.postTerminalCap != def.postTerminalCap {
		t.Errorf("budget with a margin = %v, without = %v; want the margin to leave the budget alone", set.postTerminalCap, def.postTerminalCap)
	}
}

// TestValidatePipelineWaitMarginRefusesANegativeOrOverflowingMargin pins the
// startup check: a negative --terminal-pipeline-wait-margin, and one whose sum
// with the budget overflows, are refused, and zero and a positive margin are
// accepted.
//
// spec: §12.8 (Lifecycle fence and drain)
func TestValidatePipelineWaitMarginRefusesANegativeOrOverflowingMargin(t *testing.T) {
	const budget = 330 * time.Second
	for _, d := range []time.Duration{-time.Nanosecond, time.Duration(math.MaxInt64)} {
		if err := validatePipelineWaitMargin(d, budget); err == nil {
			t.Errorf("validatePipelineWaitMargin(%v) = nil, want an error", d)
		}
	}
	for _, d := range []time.Duration{0, 30 * time.Second, time.Duration(math.MaxInt64) - budget} {
		if err := validatePipelineWaitMargin(d, budget); err != nil {
			t.Errorf("validatePipelineWaitMargin(%v) = %v, want nil", d, err)
		}
	}
}

// TestErasureAndTenantDeletionWaitTheBudgetPlusTheMargin pins where the
// margin applies: a terminal session with no pipeline mark whose budget has
// elapsed but whose budget plus margin has not still holds the erasure
// pipeline gate and the tenant-deletion terminator, while the retention
// collector, which reads the budget alone, already collects it; once the
// budget plus the margin has elapsed neither the gate nor the terminator
// holds it; and a negative margin fails both closed with
// ErrPipelineWaitMarginInvalid.
//
// spec: §12.8 (Lifecycle fence and drain), §7.1 (Pipeline-pending session)
func TestErasureAndTenantDeletionWaitTheBudgetPlusTheMargin(t *testing.T) {
	ctx := context.Background()
	w := memoryPostureWiring(t)
	seedTenantUser(t, w, "acme", "alice@acme.com")
	w.sessionSrv = sessionserver.New(w.sessions, sessionserver.Options{DevTenantHeader: true, DefaultRetention: time.Second})
	t.Cleanup(w.sessionSrv.Close)
	if w.pipelineWaitMargin <= 0 {
		t.Fatalf("margin = %v, want the positive default", w.pipelineWaitMargin)
	}
	now := clockinject.Now()
	ended := now.Add(-w.postTerminalCap - w.pipelineWaitMargin/2)
	if err := w.sessions.Create(ctx, sessionstore.Session{
		ID: "sess-late", TenantID: "acme", UserID: "alice@acme.com", State: apisession.StateCompleted,
		PodAssignment: "pod-a", TerminatedAt: ended, RetentionExpiresAt: now.Add(-time.Second),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	term := func() tenantSessionTerminator {
		return w.newTenantSessionTerminator(&sleepyDrainTerminator{}, nil, time.Second)
	}

	if ids, err := w.erasurePipelineGate()(ctx, "acme", "alice@acme.com"); err != nil || len(ids) != 1 {
		t.Errorf("erasure gate inside the margin = (%v, %v), want the session", ids, err)
	}
	if err := term().TerminateTenantSessions(ctx, "acme"); err == nil {
		t.Error("tenant terminator inside the margin = nil, want the terminal pipeline wait")
	}
	arts := &countingArtifacts{}
	c := retentiongc.New(w.sessions, retentiongc.StaticTenants{"acme"}, []retentiongc.Artifact{arts.artifact()}, w.retentionGCOptions(time.Minute))
	if n, err := c.Tick(ctx, clockinject.Now()); err != nil || n != 1 {
		t.Errorf("retention past the budget = (%d, %v), want the row collected, because the margin extends the erasure and tenant-deletion wait alone", n, err)
	}

	margin := w.pipelineWaitMargin
	w.pipelineWaitMargin = -time.Second
	if _, err := w.erasurePipelineGate()(ctx, "acme", "alice@acme.com"); !errors.Is(err, admin.ErrPipelineWaitMarginInvalid) {
		t.Errorf("erasure gate with a negative margin = %v, want ErrPipelineWaitMarginInvalid", err)
	}
	if err := term().TerminateTenantSessions(ctx, "acme"); !errors.Is(err, admin.ErrPipelineWaitMarginInvalid) {
		t.Errorf("tenant terminator with a negative margin = %v, want ErrPipelineWaitMarginInvalid", err)
	}

	w.pipelineWaitMargin = margin / 4
	if ids, err := w.erasurePipelineGate()(ctx, "acme", "alice@acme.com"); err != nil || len(ids) != 0 {
		t.Errorf("erasure gate past the budget plus the margin = (%v, %v), want none", ids, err)
	}
	if err := term().TerminateTenantSessions(ctx, "acme"); err != nil {
		t.Errorf("tenant terminator past the budget plus the margin = %v, want nil", err)
	}
}
