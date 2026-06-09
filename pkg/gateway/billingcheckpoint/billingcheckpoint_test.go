// SPDX-License-Identifier: MIT

package billingcheckpoint_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingcheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage"
)

type fakeLister struct{ sessions []billingcheckpoint.Session }

func (f fakeLister) ListActiveSessions(context.Context) ([]billingcheckpoint.Session, error) {
	return f.sessions, nil
}

// spec: §11.2.1 — token_usage.checkpoint reports the delta since the
// previous checkpoint, so two passes over a growing session produce two
// events whose token counts sum to the cumulative usage (no double
// count), and a pass with no new usage emits nothing.
func TestCheckpointEmitsWindowDelta_spec_11_2_1(t *testing.T) {
	usage := sessionusage.NewMemory()
	ctx := context.Background()
	_ = usage.Add(ctx, "acme", "sess-1", 100, 40)

	lister := fakeLister{sessions: []billingcheckpoint.Session{{TenantID: "acme", SessionID: "sess-1", UserID: "alice"}}}
	billing := billingstore.NewMemory()
	cp := billingcheckpoint.New(billingfanout.NewEmitter(billing), lister, usage)

	// First pass: the full cumulative usage is the first window.
	cp.Checkpoint(ctx)
	evs := since(t, billing, "acme")
	if len(evs) != 1 {
		t.Fatalf("first pass: want 1 checkpoint, got %d: %+v", len(evs), evs)
	}
	if evs[0].EventType != billingstore.EventTokenUsageCheckpoint ||
		evs[0].TokensInput != 100 || evs[0].TokensOutput != 40 ||
		evs[0].SessionID != "sess-1" || evs[0].UserID != "alice" {
		t.Fatalf("first checkpoint = %+v", evs[0])
	}

	// No new usage: the second pass emits nothing.
	cp.Checkpoint(ctx)
	if evs := since(t, billing, "acme"); len(evs) != 1 {
		t.Fatalf("no-new-usage pass must emit nothing, got %d events", len(evs))
	}

	// More usage: the next checkpoint reports only the delta (50, 10).
	_ = usage.Add(ctx, "acme", "sess-1", 50, 10)
	cp.Checkpoint(ctx)
	evs = since(t, billing, "acme")
	if len(evs) != 2 {
		t.Fatalf("after more usage: want 2 checkpoints, got %d", len(evs))
	}
	if evs[1].TokensInput != 50 || evs[1].TokensOutput != 10 {
		t.Fatalf("second checkpoint must carry the window delta, got %+v", evs[1])
	}
	// The two windows sum to the cumulative total.
	if evs[0].TokensInput+evs[1].TokensInput != 150 || evs[0].TokensOutput+evs[1].TokensOutput != 50 {
		t.Fatalf("checkpoint windows do not sum to cumulative usage: %+v", evs)
	}
}

// A session with zero recorded usage produces no checkpoint.
func TestCheckpointSkipsZeroUsage_spec_11_2_1(t *testing.T) {
	usage := sessionusage.NewMemory()
	lister := fakeLister{sessions: []billingcheckpoint.Session{{TenantID: "acme", SessionID: "idle", UserID: "bob"}}}
	billing := billingstore.NewMemory()
	cp := billingcheckpoint.New(billingfanout.NewEmitter(billing), lister, usage)
	cp.Checkpoint(context.Background())
	if evs := since(t, billing, "acme"); len(evs) != 0 {
		t.Fatalf("idle session must emit no checkpoint, got %+v", evs)
	}
}

// New returns nil when any dependency is absent, and a nil Checkpointer's
// Checkpoint/Run are safe no-ops.
func TestNewNilSafe(t *testing.T) {
	if billingcheckpoint.New(nil, fakeLister{}, sessionusage.NewMemory()) != nil {
		t.Fatal("nil emitter must yield nil Checkpointer")
	}
	var nilCP *billingcheckpoint.Checkpointer
	nilCP.Checkpoint(context.Background())
}

func since(t *testing.T, store *billingstore.Memory, tenant string) []billingstore.Event {
	t.Helper()
	evs, err := store.Since(context.Background(), tenant, 0, 100)
	if err != nil {
		t.Fatalf("billing Since: %v", err)
	}
	return evs
}
