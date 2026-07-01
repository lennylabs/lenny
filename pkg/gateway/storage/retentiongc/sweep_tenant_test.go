// SPDX-License-Identifier: MIT

package retentiongc_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/retentiongc"
)

// seedTenant creates one terminal, retention-expired session for the named
// tenant directly (the package seed helper hard-codes "acme").
func seedTenant(t *testing.T, store sessionstore.Store, tenant, id string) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:                 id,
		TenantID:           tenant,
		State:              session.StateCompleted,
		RetentionExpiresAt: gcClock.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed %s/%s: %v", tenant, id, err)
	}
}

// spec: §12.5 line 317 — SweepTenant collects only the named tenant's
// expired artifacts and never touches another tenant's sessions, so the
// `gcPriority: high` immediate sweep stays scoped to the erased tenant.
// F-12.5.18.
func TestSweepTenantScopesToOneTenant_spec_12_5_317(t *testing.T) {
	store := memstore.New()
	seedTenant(t, store, "acme", "acme_old")
	seedTenant(t, store, "globex", "globex_old")
	arts := &recordingArtifact{name: "artifacts"}
	c := retentiongc.New(store, retentiongc.StaticTenants{"acme", "globex"},
		[]retentiongc.Artifact{arts.artifact()}, retentiongc.Options{})

	n, err := c.SweepTenant(context.Background(), "acme", gcClock)
	if err != nil {
		t.Fatalf("SweepTenant: %v", err)
	}
	if n != 1 {
		t.Errorf("collected = %d, want 1", n)
	}
	if len(arts.deleted) != 1 || arts.deleted[0] != "acme_old" {
		t.Errorf("deleter calls = %v, want [acme_old] (globex untouched)", arts.deleted)
	}
}

// spec: §12.5 line 317 — an empty tenant id is rejected so a misfired hook
// cannot sweep the whole store. F-12.5.18.
func TestSweepTenantRejectsEmptyTenant_spec_12_5_317(t *testing.T) {
	store := memstore.New()
	arts := &recordingArtifact{name: "artifacts"}
	c := retentiongc.New(store, retentiongc.StaticTenants{}, []retentiongc.Artifact{arts.artifact()},
		retentiongc.Options{Metrics: &fakeMetricsSink{}})
	if _, err := c.SweepTenant(context.Background(), "", gcClock); err == nil {
		t.Fatal("SweepTenant(\"\") = nil error, want rejection")
	}
}

// spec: §12.5 line 321 — an out-of-cycle SweepTenant emits the same
// observability signals as a scheduled Tick (one run outcome, per-store
// deleted count, a duration sample). F-12.5.18.
func TestSweepTenantEmitsMetrics_spec_12_5_321(t *testing.T) {
	store := memstore.New()
	seedTenant(t, store, "acme", "acme_old")
	arts := &recordingArtifact{name: "artifacts"}
	sink := &fakeMetricsSink{}
	c := retentiongc.New(store, retentiongc.StaticTenants{"acme"},
		[]retentiongc.Artifact{arts.artifact()}, retentiongc.Options{Metrics: sink})

	if _, err := c.SweepTenant(context.Background(), "acme", gcClock); err != nil {
		t.Fatalf("SweepTenant: %v", err)
	}
	if len(sink.runs) != 1 || sink.runs[0] != "success" {
		t.Errorf("runs = %v, want [success]", sink.runs)
	}
	if sink.deleted["artifacts"] != 1 {
		t.Errorf("deleted[artifacts] = %d, want 1", sink.deleted["artifacts"])
	}
	if len(sink.durations) != 1 {
		t.Errorf("durations = %v, want one sample", sink.durations)
	}
}
