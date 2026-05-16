// SPDX-License-Identifier: MIT

package retentiongc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/retentiongc"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
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
