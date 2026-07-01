// SPDX-License-Identifier: MIT

package evictionfallback_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/storage/evictionfallback"
)

// spec: §4.4 line 291 — eviction-context storage accounting.

// recordingCatalog is a minimal artifactcatalog.Store fake used by
// the bridge tests. Only Insert records anything; the other methods
// are present to satisfy the interface and are no-ops.
type recordingCatalog struct {
	inserts []artifactcatalog.Record
	err     error
}

func (r *recordingCatalog) Insert(_ context.Context, rec artifactcatalog.Record) error {
	if r.err != nil {
		return r.err
	}
	r.inserts = append(r.inserts, rec)
	return nil
}

func (r *recordingCatalog) Get(context.Context, string) (artifactcatalog.Record, error) {
	return artifactcatalog.Record{}, artifactcatalog.ErrNotFound
}
func (r *recordingCatalog) SoftDelete(context.Context, string, time.Time) error { return nil }
func (r *recordingCatalog) Tombstone(context.Context, string) error             { return nil }
func (r *recordingCatalog) HardPruneExpired(context.Context, time.Time) (int, error) {
	return 0, nil
}

func (r *recordingCatalog) ListPrunable(context.Context, time.Time) ([]string, error) {
	return nil, nil
}

func (r *recordingCatalog) HardPruneURIs(context.Context, []string) (int, error) {
	return 0, nil
}

func (r *recordingCatalog) ListBySession(context.Context, string, string) ([]artifactcatalog.Record, error) {
	return nil, nil
}

func (r *recordingCatalog) SetLegalHold(context.Context, string, bool, string, time.Time, string) error {
	return nil
}

func (r *recordingCatalog) ListLegalHeld(context.Context, string) ([]artifactcatalog.Record, error) {
	return nil, nil
}

func (r *recordingCatalog) IsLegalHeldAt(context.Context, string, string) (bool, error) {
	return false, nil
}

func (r *recordingCatalog) SessionsWithLegalHoldAndCheckpoints(context.Context) ([]artifactcatalog.SessionRef, error) {
	return nil, nil
}
func (r *recordingCatalog) DeleteByTenant(context.Context, string) (int, error) { return 0, nil }
func (r *recordingCatalog) SumLiveBytes(context.Context, string) (int64, error) { return 0, nil }

// TestCatalogBridgeRecordsEvictionContextRow verifies the bridge
// stamps `artifact_type = eviction_context` and forwards the URI /
// size verbatim.
//
// spec: §4.4 line 291.
func TestCatalogBridgeRecordsEvictionContextRow(t *testing.T) {
	cat := &recordingCatalog{}
	b := &evictionfallback.CatalogBridge{Catalog: cat}
	if err := b.RecordEvictionContext(context.Background(), "acme", "sess-7",
		"/acme/eviction/sess-7/context", 4096); err != nil {
		t.Fatalf("RecordEvictionContext: %v", err)
	}
	if len(cat.inserts) != 1 {
		t.Fatalf("inserts = %d, want 1", len(cat.inserts))
	}
	r := cat.inserts[0]
	if r.ArtifactType != artifactcatalog.ArtifactTypeEvictionContext {
		t.Errorf("ArtifactType = %q, want %q", r.ArtifactType, artifactcatalog.ArtifactTypeEvictionContext)
	}
	if r.URI != "/acme/eviction/sess-7/context" {
		t.Errorf("URI = %q", r.URI)
	}
	if r.SizeBytes != 4096 {
		t.Errorf("SizeBytes = %d, want 4096", r.SizeBytes)
	}
	if r.TenantID != "acme" || r.SessionID != "sess-7" {
		t.Errorf("tenant/session = (%q, %q)", r.TenantID, r.SessionID)
	}
	if r.State != artifactcatalog.StateLive {
		t.Errorf("State = %q, want live", r.State)
	}
}

// TestCatalogBridgeNilIsNoOp confirms the bridge tolerates a nil
// underlying catalog so dev-mode wiring still produces a row.
func TestCatalogBridgeNilIsNoOp(t *testing.T) {
	b := &evictionfallback.CatalogBridge{Catalog: nil}
	if err := b.RecordEvictionContext(context.Background(), "acme", "sess", "/uri", 100); err != nil {
		t.Errorf("RecordEvictionContext with nil catalog = %v, want nil", err)
	}
}

// TestQuotaBridgeForwardsDelta confirms the bridge passes the delta
// straight through to the underlying counter.
func TestQuotaBridgeForwardsDelta(t *testing.T) {
	c := &recordingQuotaCounter{}
	b := &evictionfallback.QuotaBridge{Counter: c}
	if err := b.Adjust(context.Background(), "acme", 4096); err != nil {
		t.Fatalf("Adjust: %v", err)
	}
	if len(c.deltas) != 1 || c.deltas[0] != 4096 {
		t.Errorf("deltas = %v, want [4096]", c.deltas)
	}
}

// TestQuotaBridgeNilCounterIsNoOp confirms a nil counter does not
// panic and returns nil.
func TestQuotaBridgeNilCounterIsNoOp(t *testing.T) {
	b := &evictionfallback.QuotaBridge{Counter: nil}
	if err := b.Adjust(context.Background(), "acme", 4096); err != nil {
		t.Errorf("nil counter Adjust = %v, want nil", err)
	}
}

// TestQuotaBridgePropagatesError surfaces the underlying counter's
// failure so the writer can log it (the writer treats this as
// non-fatal and continues).
func TestQuotaBridgePropagatesError(t *testing.T) {
	c := &recordingQuotaCounter{err: errors.New("redis unreachable")}
	b := &evictionfallback.QuotaBridge{Counter: c}
	if err := b.Adjust(context.Background(), "acme", 4096); err == nil {
		t.Error("Adjust returned nil; want underlying error")
	}
}

// recordingQuotaCounter records every Adjust call.
type recordingQuotaCounter struct {
	deltas []int64
	err    error
}

func (r *recordingQuotaCounter) Adjust(_ context.Context, _ string, delta int64) error {
	if r.err != nil {
		return r.err
	}
	r.deltas = append(r.deltas, delta)
	return nil
}
