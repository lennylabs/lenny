// SPDX-License-Identifier: MIT

package retentiongc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/retentiongc"
)

// spec: §7.1 artifact retention GC + §12.8 legal-hold exemption.

var gcClock = time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)

func seed(t *testing.T, store sessionstore.Store, s sessionstore.Session) {
	t.Helper()
	s.TenantID = "acme"
	if s.State == "" {
		s.State = session.StateCompleted
	}
	if err := store.Create(context.Background(), s); err != nil {
		t.Fatalf("seed session %q: %v", s.ID, err)
	}
}

// recordingArtifact is an ArtifactDeleter that records the session ids
// it was asked to delete.
type recordingArtifact struct {
	name    string
	deleted []string
}

func (a *recordingArtifact) artifact() retentiongc.Artifact {
	return retentiongc.Artifact{
		Name: a.name,
		Delete: func(_ context.Context, _, sessionID string) (int, error) {
			a.deleted = append(a.deleted, sessionID)
			return 1, nil
		},
	}
}

func collector(store sessionstore.Store, arts ...retentiongc.Artifact) *retentiongc.Collector {
	return retentiongc.New(store, retentiongc.StaticTenants{"acme"}, arts, retentiongc.Options{})
}

func TestTickCollectsExpiredSession(t *testing.T) {
	store := memstore.New()
	seed(t, store, sessionstore.Session{ID: "sess_old", RetentionExpiresAt: gcClock.Add(-time.Hour)})
	transcripts := &recordingArtifact{name: "transcripts"}
	blobs := &recordingArtifact{name: "artifacts"}
	c := collector(store, transcripts.artifact(), blobs.artifact())

	n, err := c.Tick(context.Background(), gcClock)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Errorf("collected = %d, want 1", n)
	}
	if len(transcripts.deleted) != 1 || transcripts.deleted[0] != "sess_old" {
		t.Errorf("transcript deleter calls = %v, want [sess_old]", transcripts.deleted)
	}
	if len(blobs.deleted) != 1 || blobs.deleted[0] != "sess_old" {
		t.Errorf("blob deleter calls = %v, want [sess_old]", blobs.deleted)
	}
}

func TestTickSkipsLegalHold(t *testing.T) {
	store := memstore.New()
	seed(t, store, sessionstore.Session{
		ID: "sess_held", RetentionExpiresAt: gcClock.Add(-time.Hour), LegalHold: true,
	})
	transcripts := &recordingArtifact{name: "transcripts"}
	c := collector(store, transcripts.artifact())

	n, err := c.Tick(context.Background(), gcClock)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Errorf("collected = %d, want 0 — a §12.8 legal hold exempts the session", n)
	}
	if len(transcripts.deleted) != 0 {
		t.Errorf("a held session's artifacts must not be deleted: %v", transcripts.deleted)
	}
}

func TestTickSkipsLiveSession(t *testing.T) {
	store := memstore.New()
	seed(t, store, sessionstore.Session{
		ID: "sess_live", State: session.StateRunning, RetentionExpiresAt: gcClock.Add(-time.Hour),
	})
	transcripts := &recordingArtifact{name: "transcripts"}
	c := collector(store, transcripts.artifact())

	n, _ := c.Tick(context.Background(), gcClock)
	if n != 0 || len(transcripts.deleted) != 0 {
		t.Errorf("a non-terminal session must not be collected: n=%d deleted=%v", n, transcripts.deleted)
	}
}

func TestTickSkipsUnexpiredSession(t *testing.T) {
	store := memstore.New()
	seed(t, store, sessionstore.Session{ID: "sess_future", RetentionExpiresAt: gcClock.Add(time.Hour)})
	transcripts := &recordingArtifact{name: "transcripts"}
	c := collector(store, transcripts.artifact())

	n, _ := c.Tick(context.Background(), gcClock)
	if n != 0 || len(transcripts.deleted) != 0 {
		t.Errorf("a session before its retention deadline must not be collected: n=%d", n)
	}
}

func TestTickSkipsSessionWithNoRetentionDeadline(t *testing.T) {
	store := memstore.New()
	seed(t, store, sessionstore.Session{ID: "sess_nort"}) // zero RetentionExpiresAt
	transcripts := &recordingArtifact{name: "transcripts"}
	c := collector(store, transcripts.artifact())

	n, _ := c.Tick(context.Background(), gcClock)
	if n != 0 || len(transcripts.deleted) != 0 {
		t.Errorf("a session with no retention deadline must not be collected: n=%d", n)
	}
}

func TestTickClearsSnapshotAndIsIdempotent(t *testing.T) {
	store := memstore.New()
	seed(t, store, sessionstore.Session{
		ID: "sess_x", RetentionExpiresAt: gcClock.Add(-time.Hour),
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: "lenny-blob://acme/sess_x/snap"},
	})
	transcripts := &recordingArtifact{name: "transcripts"}
	c := collector(store, transcripts.artifact())

	if _, err := c.Tick(context.Background(), gcClock); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "sess_x")
	if got.WorkspaceSnapshot != nil {
		t.Error("collection must clear the WorkspaceSnapshot reference")
	}
	if !got.RetentionExpiresAt.IsZero() {
		t.Error("collection must clear RetentionExpiresAt")
	}
	// A later sweep must not re-collect the already-collected session.
	n, _ := c.Tick(context.Background(), gcClock.Add(time.Hour))
	if n != 0 || len(transcripts.deleted) != 1 {
		t.Errorf("re-sweep collected %d (deleter calls %v) — collection must be idempotent",
			n, transcripts.deleted)
	}
}

func TestTickPropagatesDeleterError(t *testing.T) {
	store := memstore.New()
	seed(t, store, sessionstore.Session{ID: "sess_e", RetentionExpiresAt: gcClock.Add(-time.Hour)})
	failing := retentiongc.Artifact{
		Name: "broken",
		Delete: func(context.Context, string, string) (int, error) {
			return 0, errors.New("store down")
		},
	}
	c := collector(store, failing)

	if _, err := c.Tick(context.Background(), gcClock); err == nil {
		t.Error("Tick should propagate an artifact-deleter error")
	}
}

// fakeMetricsSink records every §12.5 line 321 retention-GC metric
// signal the Collector emits so tests can assert wiring.
type fakeMetricsSink struct {
	runs      []string
	deleted   map[string]int
	errors    []string
	durations []float64
}

func (s *fakeMetricsSink) IncGCRun(outcome string) {
	s.runs = append(s.runs, outcome)
}

func (s *fakeMetricsSink) AddGCArtifactsDeleted(store string, n int) {
	if s.deleted == nil {
		s.deleted = map[string]int{}
	}
	s.deleted[store] += n
}

func (s *fakeMetricsSink) IncGCError(store string) {
	s.errors = append(s.errors, store)
}

func (s *fakeMetricsSink) ObserveGCDuration(seconds float64) {
	s.durations = append(s.durations, seconds)
}

// TestTickEmitsSuccessMetrics asserts a clean sweep increments
// `lenny_gc_runs_total{outcome="success"}` once, attributes deleted
// artifacts to each per-store adapter, and observes a non-negative
// duration.
//
// spec: §12.5 line 321.
func TestTickEmitsSuccessMetrics(t *testing.T) {
	store := memstore.New()
	seed(t, store, sessionstore.Session{ID: "sess_old", RetentionExpiresAt: gcClock.Add(-time.Hour)})
	transcripts := &recordingArtifact{name: "transcripts"}
	blobs := &recordingArtifact{name: "artifacts"}
	sink := &fakeMetricsSink{}
	c := retentiongc.New(store, retentiongc.StaticTenants{"acme"},
		[]retentiongc.Artifact{transcripts.artifact(), blobs.artifact()},
		retentiongc.Options{Metrics: sink})

	if _, err := c.Tick(context.Background(), gcClock); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := sink.runs; len(got) != 1 || got[0] != "success" {
		t.Errorf("runs = %v, want [success]", got)
	}
	if got := sink.deleted["transcripts"]; got != 1 {
		t.Errorf("transcripts deleted = %d, want 1", got)
	}
	if got := sink.deleted["artifacts"]; got != 1 {
		t.Errorf("artifacts deleted = %d, want 1", got)
	}
	if len(sink.errors) != 0 {
		t.Errorf("errors = %v, want []", sink.errors)
	}
	if len(sink.durations) != 1 {
		t.Errorf("duration observations = %d, want 1", len(sink.durations))
	}
}

// TestTickEmitsErrorMetrics asserts a per-store deleter failure flips
// the sweep outcome to `error` and increments
// `lenny_gc_errors_total{store=...}` for the failing adapter.
//
// spec: §12.5 line 321.
func TestTickEmitsErrorMetrics(t *testing.T) {
	store := memstore.New()
	seed(t, store, sessionstore.Session{ID: "sess_e", RetentionExpiresAt: gcClock.Add(-time.Hour)})
	failing := retentiongc.Artifact{
		Name: "broken",
		Delete: func(context.Context, string, string) (int, error) {
			return 0, errors.New("store down")
		},
	}
	sink := &fakeMetricsSink{}
	c := retentiongc.New(store, retentiongc.StaticTenants{"acme"},
		[]retentiongc.Artifact{failing}, retentiongc.Options{Metrics: sink})

	if _, err := c.Tick(context.Background(), gcClock); err == nil {
		t.Fatal("Tick should propagate an artifact-deleter error")
	}
	if got := sink.runs; len(got) != 1 || got[0] != "error" {
		t.Errorf("runs = %v, want [error]", got)
	}
	if got := sink.errors; len(got) != 1 || got[0] != "broken" {
		t.Errorf("errors = %v, want [broken]", got)
	}
	if len(sink.durations) != 1 {
		t.Errorf("duration observations = %d, want 1", len(sink.durations))
	}
}

// spec: §12.5 line 317 — gc.cycleIntervalSeconds default 900, minimum 60;
// §12.5 line 341 — gc.tombstoneRetentionSeconds default 86400.
func TestGCConfigDefaultsMatchSpec_spec_12_5_317_341(t *testing.T) {
	if got := retentiongc.DefaultSweepInterval; got != 900*time.Second {
		t.Errorf("DefaultSweepInterval = %s, want 900s (§12.5 line 317)", got)
	}
	if got := retentiongc.MinSweepInterval; got != 60*time.Second {
		t.Errorf("MinSweepInterval = %s, want 60s (§12.5 line 317)", got)
	}
	if got := retentiongc.DefaultTombstoneRetention; got != 86400*time.Second {
		t.Errorf("DefaultTombstoneRetention = %s, want 86400s/24h (§12.5 line 341)", got)
	}
	// The tombstone-retention window is the §12.5 line 341 default, not the
	// §7.1 7-day artifact TTL the gateway previously conflated it with.
	if retentiongc.DefaultTombstoneRetention == 7*24*time.Hour {
		t.Error("DefaultTombstoneRetention must not equal the §7.1 7-day artifact TTL")
	}
}

// spec: §12.5 line 317 — the configured sweep interval is clamped to the
// 60s floor; a non-positive value selects the default.
func TestClampSweepInterval_spec_12_5_317(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero selects default", 0, retentiongc.DefaultSweepInterval},
		{"negative selects default", -5 * time.Second, retentiongc.DefaultSweepInterval},
		{"below floor clamps up", 30 * time.Second, retentiongc.MinSweepInterval},
		{"at floor is unchanged", 60 * time.Second, 60 * time.Second},
		{"above floor is unchanged", 1800 * time.Second, 1800 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retentiongc.ClampSweepInterval(tc.in); got != tc.want {
				t.Errorf("ClampSweepInterval(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}
