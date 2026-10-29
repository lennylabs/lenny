// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
)

// transcriptFixture is a fenced memstore whose CreateWithTranscript copies
// through transcriptstore.Memory, as the memory posture wires it. The copier
// is wrapped to count calls, so a case can assert that no transcript write
// follows the insert.
type transcriptFixture struct {
	*fenceFixture
	transcripts *transcriptstore.Memory
	copies      int
}

func newTranscriptFixture(t *testing.T, copyErr error) *transcriptFixture {
	t.Helper()
	tf := &transcriptFixture{transcripts: transcriptstore.NewMemory()}
	copier := func(ctx context.Context, tenantID, src, dst string) error {
		tf.copies++
		if copyErr != nil {
			// A copier that wrote part of the transcript before failing
			// must still leave nothing visible under dst: the store stores
			// no row, and the test asserts no entry remains either.
			return copyErr
		}
		return tf.transcripts.CopySession(ctx, tenantID, src, dst)
	}
	tf.fenceFixture = newFenceFixture(t, memstore.WithTranscriptCopier(copier))
	return tf
}

var sourceEntries = []transcriptstore.Entry{
	{Role: "user", Content: "first", Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	{Role: "assistant", Content: "second", Timestamp: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), SchemaVersion: 1},
	{Role: "system", Content: "third", Timestamp: time.Date(2026, 1, 2, 3, 4, 7, 0, time.UTC)},
}

// seedSource inserts a source session for owner with sourceEntries.
func (tf *transcriptFixture) seedSource(t *testing.T, owner string) sessionstore.Session {
	t.Helper()
	src := liveRow(owner)
	if err := tf.sessions.Create(context.Background(), src); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := tf.transcripts.Append(context.Background(), fenceTenant, src.ID, sourceEntries...); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	return src
}

func (tf *transcriptFixture) requireNoEntries(t *testing.T, id string) {
	t.Helper()
	if got, err := tf.transcripts.Get(context.Background(), fenceTenant, id); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Fatalf("transcript of %s = %v, %v; want none", id, got, err)
	}
}

func replayOf(src sessionstore.Session) sessionstore.Session {
	r := liveRow(src.UserID)
	r.ParentSessionID = src.ID
	return r
}

// reservedReplay is replayOf with the replay's ID reserved for its owner,
// as the replay handler reserves it before it copies the source snapshot.
func (tf *transcriptFixture) reservedReplay(t *testing.T, src sessionstore.Session) sessionstore.Session {
	t.Helper()
	r := replayOf(src)
	if err := tf.sessions.Reserve(context.Background(), r.TenantID, r.UserID, r.ID, false); err != nil {
		t.Fatalf("reserve replay ID: %v", err)
	}
	return r
}

// TestMemstoreCreateWithTranscript_spec_12_8 pins the replay transcript copy
// inside the insert on the in-memory store: the copy keeps order, roles,
// contents, timestamps, and schema versions; an empty source inserts the
// row with no entries; and a fence refusal, a reservation that is gone, a
// copier error, and a store with no copier each leave neither the row nor
// an entry under the new ID.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API)
func TestMemstoreCreateWithTranscript_spec_12_8(t *testing.T) {
	ctx := context.Background()

	t.Run("copies the source transcript in order", func(t *testing.T) {
		tf := newTranscriptFixture(t, nil)
		src := tf.seedSource(t, fenceOwner)
		replay := tf.reservedReplay(t, src)
		if err := tf.sessions.CreateWithTranscript(ctx, replay, src.ID); err != nil {
			t.Fatalf("CreateWithTranscript: %v", err)
		}
		want, _ := tf.transcripts.Get(ctx, fenceTenant, src.ID)
		got, err := tf.transcripts.Get(ctx, fenceTenant, replay.ID)
		if err != nil {
			t.Fatalf("Get copy: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("copied %d entries, want %d", len(got), len(want))
		}
		for i := range got {
			if got[i].Seq != uint64(i+1) || got[i].Role != want[i].Role || got[i].Content != want[i].Content ||
				!got[i].Timestamp.Equal(want[i].Timestamp) || got[i].SchemaVersion != want[i].SchemaVersion {
				t.Fatalf("entry %d = %+v, want a copy of %+v at seq %d", i, got[i], want[i], i+1)
			}
			if got[i].ID == want[i].ID || got[i].ID == "" {
				t.Fatalf("entry %d kept the source id %q", i, got[i].ID)
			}
		}
		if _, err := tf.sessions.Get(ctx, fenceTenant, replay.ID); err != nil {
			t.Fatalf("replay row: %v", err)
		}
		if tf.copies != 1 {
			t.Fatalf("copier calls = %d, want 1", tf.copies)
		}
	})

	t.Run("empty source inserts the row and copies nothing", func(t *testing.T) {
		tf := newTranscriptFixture(t, nil)
		src := liveRow(fenceOwner)
		if err := tf.sessions.Create(ctx, src); err != nil {
			t.Fatalf("seed: %v", err)
		}
		replay := tf.reservedReplay(t, src)
		if err := tf.sessions.CreateWithTranscript(ctx, replay, src.ID); err != nil {
			t.Fatalf("CreateWithTranscript: %v", err)
		}
		tf.requireNoEntries(t, replay.ID)
	})

	t.Run("fence refusal leaves no row and no entry", func(t *testing.T) {
		tf := newTranscriptFixture(t, nil)
		src := tf.seedSource(t, fenceOwner)
		replay := tf.reservedReplay(t, src)
		setUser(func(u *userstore.User) { u.ProcessingRestricted = true })(t, tf.fenceFixture)
		requireRefusal(t, tf.sessions.CreateWithTranscript(ctx, replay, src.ID), sessionstore.RefusalErasureInProgress)
		requireNoRow(t, tf.sessions, replay.ID)
		tf.requireNoEntries(t, replay.ID)
		if tf.copies != 0 {
			t.Fatalf("copier ran %d times after a refusal", tf.copies)
		}
	})

	// spec: §12.8 (Lifecycle fence and drain) — the replay reserves its ID
	// before it copies the source snapshot, so an insert whose reservation
	// the owner's erasure already removed is refused as CreateReserved
	// refuses it, and copies no transcript.
	t.Run("a row whose reservation is gone is refused", func(t *testing.T) {
		tf := newTranscriptFixture(t, nil)
		src := tf.seedSource(t, fenceOwner)
		replay := replayOf(src)
		err := tf.sessions.CreateWithTranscript(ctx, replay, src.ID)
		ref := refusalOf(t, err, sessionstore.RefusalErasureInProgress)
		if ref.Details["userId"] != fenceOwner {
			t.Fatalf("refusal details = %v, want userId %s", ref.Details, fenceOwner)
		}
		requireNoRow(t, tf.sessions, replay.ID)
		tf.requireNoEntries(t, replay.ID)
		if tf.copies != 0 {
			t.Fatalf("copier ran %d times for a row with no reservation", tf.copies)
		}
	})

	t.Run("copier error leaves no row and no entry", func(t *testing.T) {
		boom := errors.New("copy failed")
		tf := newTranscriptFixture(t, boom)
		src := tf.seedSource(t, fenceOwner)
		replay := tf.reservedReplay(t, src)
		if err := tf.sessions.CreateWithTranscript(ctx, replay, src.ID); !errors.Is(err, boom) {
			t.Fatalf("CreateWithTranscript = %v, want the copier error", err)
		}
		requireNoRow(t, tf.sessions, replay.ID)
		tf.requireNoEntries(t, replay.ID)
	})

	t.Run("store with no copier inserts nothing", func(t *testing.T) {
		f := newFenceFixture(t)
		replay := liveRow(fenceOwner)
		if err := f.sessions.CreateWithTranscript(ctx, replay, "src"); !errors.Is(err, sessionstore.ErrTranscriptCopierUnwired) {
			t.Fatalf("CreateWithTranscript = %v, want ErrTranscriptCopierUnwired", err)
		}
		requireNoRow(t, f.sessions, replay.ID)
	})

	t.Run("CopySession refuses a destination that holds entries", func(t *testing.T) {
		ts := transcriptstore.NewMemory()
		if err := ts.Append(ctx, fenceTenant, "src", sourceEntries...); err != nil {
			t.Fatalf("Append src: %v", err)
		}
		if err := ts.Append(ctx, fenceTenant, "dst", sourceEntries[0]); err != nil {
			t.Fatalf("Append dst: %v", err)
		}
		if err := ts.CopySession(ctx, fenceTenant, "src", "dst"); err == nil {
			t.Fatal("CopySession into a non-empty destination succeeded")
		}
		if got, _ := ts.Get(ctx, fenceTenant, "dst"); len(got) != 1 {
			t.Fatalf("destination changed: %d entries", len(got))
		}
	})
}

// TestMemstoreReplayTranscriptAgainstErasure_spec_12_8 pins the two orders
// of a replay against its owner's erasure: a replay admitted before the
// owner's flag whose CreateWithTranscript runs after the flag commits is
// refused and leaves no entry, and a replay that committed before the flag
// leaves no entry once the erasure orchestrator ran over the same stores.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestMemstoreReplayTranscriptAgainstErasure_spec_12_8(t *testing.T) {
	ctx := context.Background()

	t.Run("insert after the flag is refused", func(t *testing.T) {
		tf := newTranscriptFixture(t, nil)
		src := tf.seedSource(t, fenceOwner)
		replay := tf.reservedReplay(t, src) // admitted: the caller and source-owner checks ran before the flag
		setUser(func(u *userstore.User) { u.ProcessingRestricted, u.ErasureJobID = true, "job-1" })(t, tf.fenceFixture)
		requireRefusal(t, tf.sessions.CreateWithTranscript(ctx, replay, src.ID), sessionstore.RefusalErasureInProgress)
		tf.requireNoEntries(t, replay.ID)
	})

	t.Run("insert before the flag is erased", func(t *testing.T) {
		tf := newTranscriptFixture(t, nil)
		src := tf.seedSource(t, fenceOwner)
		replay := tf.reservedReplay(t, src)
		if err := tf.sessions.CreateWithTranscript(ctx, replay, src.ID); err != nil {
			t.Fatalf("CreateWithTranscript: %v", err)
		}
		setUser(func(u *userstore.User) { u.ProcessingRestricted = true })(t, tf.fenceFixture)
		orch := erasureOver(tf.sessions, tf.transcripts)
		if _, err := orch.DeleteByUser(ctx, fenceTenant, fenceOwner); err != nil {
			t.Fatalf("erasure: %v", err)
		}
		tf.requireNoEntries(t, replay.ID)
		tf.requireNoEntries(t, src.ID)
		if tf.copies != 1 {
			t.Fatalf("copier calls = %d, want exactly the one in the insert", tf.copies)
		}
	})
}

// erasureOver builds the erasure orchestrator the memory posture wires for
// these stores: a lister over the user's session rows and reserved IDs, the
// session-scoped transcripts eraser, and the sessions user-scoped eraser.
func erasureOver(sessions *memstore.Store, transcripts *transcriptstore.Memory) *erasure.Orchestrator {
	return erasure.New(erasure.Config{
		Sessions: func(ctx context.Context, tenantID, userID string) ([]string, error) {
			rows, err := sessions.List(ctx, tenantID, sessionstore.ListFilter{UserID: userID, Primary: true})
			if err != nil {
				return nil, err
			}
			ids, err := sessions.ReservedSessionIDs(ctx, tenantID, userID)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				ids = append(ids, r.ID)
			}
			return ids, nil
		},
		SessionScoped: []erasure.SessionEraser{{Name: "transcripts", DeleteBySession: transcripts.DeleteBySession}},
		UserScoped:    []erasure.Eraser{{Name: "sessions", DeleteByUser: sessions.DeleteByUser}},
	})
}
