// SPDX-License-Identifier: MIT

package legalholdreconciler_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/storage/legalholdreconciler"
)

// spec: §12.8 line 739 — the reconciler scans for legal-hold sessions
// whose artifact_store rows record a rotated checkpoint and emits a
// `legal_hold.checkpoint_gap_detected` audit event.

// fakeCatalog is a minimal artifactcatalog.Store fake.
type fakeCatalog struct {
	candidates    []artifactcatalog.SessionRef
	candidatesErr error
	rows          map[string][]artifactcatalog.Record
	rowsErr       error
}

func (f *fakeCatalog) Insert(context.Context, artifactcatalog.Record) error { return nil }
func (f *fakeCatalog) Get(context.Context, string) (artifactcatalog.Record, error) {
	return artifactcatalog.Record{}, artifactcatalog.ErrNotFound
}
func (f *fakeCatalog) SoftDelete(context.Context, string, time.Time) error       { return nil }
func (f *fakeCatalog) Tombstone(context.Context, string) error                   { return nil }
func (f *fakeCatalog) HardPruneExpired(context.Context, time.Time) (int, error)  { return 0, nil }
func (f *fakeCatalog) ListPrunable(context.Context, time.Time) ([]string, error) { return nil, nil }
func (f *fakeCatalog) HardPruneURIs(context.Context, []string) (int, error)      { return 0, nil }
func (f *fakeCatalog) ListBySession(_ context.Context, tenantID, sessionID string) ([]artifactcatalog.Record, error) {
	if f.rowsErr != nil {
		return nil, f.rowsErr
	}
	return f.rows[tenantID+"|"+sessionID], nil
}

func (f *fakeCatalog) SetLegalHold(context.Context, string, bool, string, time.Time, string) error {
	return nil
}

func (f *fakeCatalog) ListLegalHeld(context.Context, string) ([]artifactcatalog.Record, error) {
	return nil, nil
}

func (f *fakeCatalog) IsLegalHeldAt(context.Context, string, string) (bool, error) {
	return false, nil
}

func (f *fakeCatalog) SessionsWithLegalHoldAndCheckpoints(context.Context) ([]artifactcatalog.SessionRef, error) {
	if f.candidatesErr != nil {
		return nil, f.candidatesErr
	}
	return f.candidates, nil
}

func (f *fakeCatalog) DeleteByTenant(context.Context, string) (int, error) { return 0, nil }

func (f *fakeCatalog) SumLiveBytes(context.Context, string) (int64, error) { return 0, nil }

// fakeAppender records every Append.
type fakeAppender struct {
	rows []record
	err  error
}

type record struct {
	tenantID  string
	eventType string
	payload   string
}

func (a *fakeAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	if a.err != nil {
		return audit.Row{}, a.err
	}
	a.rows = append(a.rows, record{tenantID: tenantID, eventType: eventType, payload: string(payload)})
	return audit.Row{}, nil
}

// fakeMetrics counts IncLegalHoldCheckpointGap calls.
type fakeMetrics struct{ counts map[string]int }

func (m *fakeMetrics) IncLegalHoldCheckpointGap(tenantID string) {
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.counts[tenantID]++
}

// TestTickEmitsOnRotatedCheckpoint verifies the reconciler emits the
// §16.7 audit event when a held session carries a soft_deleted
// checkpoint row alongside a live one.
//
// spec: §12.8 line 739.
func TestTickEmitsOnRotatedCheckpoint(t *testing.T) {
	cat := &fakeCatalog{
		candidates: []artifactcatalog.SessionRef{{TenantID: "acme", SessionID: "sess-1"}},
		rows: map[string][]artifactcatalog.Record{
			"acme|sess-1": {
				{ArtifactType: artifactcatalog.ArtifactTypeCheckpoint, State: artifactcatalog.StateLive},
				{ArtifactType: artifactcatalog.ArtifactTypeCheckpoint, State: artifactcatalog.StateSoftDeleted},
			},
		},
	}
	app := &fakeAppender{}
	m := &fakeMetrics{}
	r := legalholdreconciler.New(cat, app, m, legalholdreconciler.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC) },
	})
	emitted, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1", emitted)
	}
	if len(app.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(app.rows))
	}
	row := app.rows[0]
	if row.eventType != "legal_hold.checkpoint_gap_detected" {
		t.Errorf("eventType = %q", row.eventType)
	}
	if row.tenantID != "acme" {
		t.Errorf("tenantID = %q", row.tenantID)
	}
	if !strings.Contains(row.payload, `"session_id":"sess-1"`) {
		t.Errorf("payload missing session_id: %q", row.payload)
	}
	if !strings.Contains(row.payload, `"live_checkpoints":1`) {
		t.Errorf("payload live_checkpoints: %q", row.payload)
	}
	if !strings.Contains(row.payload, `"rotated_checkpoints":1`) {
		t.Errorf("payload rotated_checkpoints: %q", row.payload)
	}
	if m.counts["acme"] != 1 {
		t.Errorf("metric counts = %v, want acme=1", m.counts)
	}
}

// TestTickSkipsSessionWithoutRotatedCheckpoint verifies the
// reconciler does not emit when every checkpoint is still live.
func TestTickSkipsSessionWithoutRotatedCheckpoint(t *testing.T) {
	cat := &fakeCatalog{
		candidates: []artifactcatalog.SessionRef{{TenantID: "acme", SessionID: "sess-clean"}},
		rows: map[string][]artifactcatalog.Record{
			"acme|sess-clean": {
				{ArtifactType: artifactcatalog.ArtifactTypeCheckpoint, State: artifactcatalog.StateLive},
				{ArtifactType: artifactcatalog.ArtifactTypeCheckpoint, State: artifactcatalog.StateLive},
			},
		},
	}
	app := &fakeAppender{}
	r := legalholdreconciler.New(cat, app, nil, legalholdreconciler.Options{})
	emitted, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0", emitted)
	}
	if len(app.rows) != 0 {
		t.Errorf("audit rows = %d, want 0", len(app.rows))
	}
}

// TestTickDedupesWithinEmitWindow verifies a chronic gap re-emits at
// most once per EmitWindow.
func TestTickDedupesWithinEmitWindow(t *testing.T) {
	cat := &fakeCatalog{
		candidates: []artifactcatalog.SessionRef{{TenantID: "acme", SessionID: "sess-d"}},
		rows: map[string][]artifactcatalog.Record{
			"acme|sess-d": {
				{ArtifactType: artifactcatalog.ArtifactTypeCheckpoint, State: artifactcatalog.StateLive},
				{ArtifactType: artifactcatalog.ArtifactTypeCheckpoint, State: artifactcatalog.StateTombstoned},
			},
		},
	}
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	app := &fakeAppender{}
	r := legalholdreconciler.New(cat, app, nil, legalholdreconciler.Options{
		Clock:      clock,
		EmitWindow: 24 * time.Hour,
	})
	if _, err := r.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Second sweep within the dedupe window is a no-op.
	if emitted, err := r.Tick(context.Background()); err != nil || emitted != 0 {
		t.Errorf("second Tick: emitted=%d err=%v, want 0 nil", emitted, err)
	}
	if len(app.rows) != 1 {
		t.Errorf("audit rows = %d, want 1 (dedupe)", len(app.rows))
	}

	// Advance past the window — a re-emit lands.
	now = now.Add(25 * time.Hour)
	if emitted, err := r.Tick(context.Background()); err != nil || emitted != 1 {
		t.Errorf("third Tick after window: emitted=%d err=%v", emitted, err)
	}
	if len(app.rows) != 2 {
		t.Errorf("audit rows = %d, want 2 after window elapses", len(app.rows))
	}
}

// TestTickPropagatesAuditError surfaces an audit-append failure so the
// caller can record the §11.7 chain-write fault.
func TestTickPropagatesAuditError(t *testing.T) {
	cat := &fakeCatalog{
		candidates: []artifactcatalog.SessionRef{{TenantID: "acme", SessionID: "sess-e"}},
		rows: map[string][]artifactcatalog.Record{
			"acme|sess-e": {
				{ArtifactType: artifactcatalog.ArtifactTypeCheckpoint, State: artifactcatalog.StateSoftDeleted},
			},
		},
	}
	app := &fakeAppender{err: errors.New("chain unreachable")}
	r := legalholdreconciler.New(cat, app, nil, legalholdreconciler.Options{})
	if _, err := r.Tick(context.Background()); err == nil {
		t.Error("Tick should propagate the audit error")
	}
}

// TestTickPropagatesCatalogError reports a catalog-side failure
// without emitting a partial event.
func TestTickPropagatesCatalogError(t *testing.T) {
	cat := &fakeCatalog{candidatesErr: errors.New("postgres down")}
	app := &fakeAppender{}
	r := legalholdreconciler.New(cat, app, nil, legalholdreconciler.Options{})
	if _, err := r.Tick(context.Background()); err == nil {
		t.Error("Tick should propagate the catalog error")
	}
	if len(app.rows) != 0 {
		t.Errorf("no audit rows must be written when the candidate list fails")
	}
}

// TestDefaultSweepIntervalMatchesSpec11_3_231 pins the §11.3 line 231
// `legalHoldCheckpointReconcilerInterval` value (900s, hard-coded in
// v1). The reconciler is co-located with the §12.8 line 739 GC sweep
// at the same cadence; both lines name the 15-minute value. A change
// here is a spec edit and should not slip in silently. F-11.3.21.
func TestDefaultSweepIntervalMatchesSpec11_3_231(t *testing.T) {
	if legalholdreconciler.DefaultSweepInterval != 900*time.Second {
		t.Errorf("DefaultSweepInterval = %s, want 900s per §11.3 line 231 / §12.8 line 739",
			legalholdreconciler.DefaultSweepInterval)
	}
}
