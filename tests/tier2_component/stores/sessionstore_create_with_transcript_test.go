//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
)

// TestSessionStoreCreateWithTranscript_spec_12_8 pins the replay transcript
// copy inside the replayed session's insert transaction against real
// Postgres: the session row and the copied session_messages rows commit
// together with seq renumbered from 1 in the source order; a copier that
// fails after inserting some rows leaves neither; a replay admitted before
// its owner's flag whose insert waits on the user Update that sets the flag
// is refused and leaves no copied row; and a replay committed before the
// flag loses its copied rows to the session row's cascade on DeleteByUser.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API)
// diagnosis: a failure means the replay transcript copy is not atomic with
// the fenced insert: copied rows can exist without their session row, or a
// replay racing its owner's erasure can write transcript rows the erasure
// never reaches.
func TestSessionStoreCreateWithTranscript_spec_12_8(t *testing.T) {
	ctx := context.Background()
	var failAfterCopy bool
	errCopy := errors.New("copy failed after a partial write")
	e := newFenceEnv(t, pgstore.WithTranscriptCopier(func(ctx context.Context, tx pgx.Tx, tenantID, src, dst string) error {
		if err := transcriptpg.CopySessionTx(ctx, tx, tenantID, src, dst); err != nil {
			return err
		}
		if failAfterCopy {
			return errCopy
		}
		return nil
	}))
	transcripts := transcriptpg.New(e.pg.Pool)
	entries := []transcriptstore.Entry{
		{Role: "user", Content: "first", Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{Role: "assistant", Content: "second", Timestamp: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC)},
		{Role: "system", Content: "third", Timestamp: time.Date(2026, 1, 2, 3, 4, 7, 0, time.UTC)},
	}
	seedSource := func(t *testing.T) sessionstore.Session {
		t.Helper()
		src := e.row("alice")
		if err := e.sessions.Create(ctx, src); err != nil {
			t.Fatalf("seed source: %v", err)
		}
		if err := transcripts.Append(ctx, e.tenant, src.ID, entries...); err != nil {
			t.Fatalf("seed transcript: %v", err)
		}
		return src
	}
	replayOf := func(src sessionstore.Session) sessionstore.Session {
		r := e.row(src.UserID)
		r.ParentSessionID = src.ID
		return r
	}

	t.Run("row and copied rows commit together in source order", func(t *testing.T) {
		e.addTenant(t)
		src := seedSource(t)
		replay := replayOf(src)
		if err := e.sessions.CreateWithTranscript(ctx, replay, src.ID); err != nil {
			t.Fatalf("CreateWithTranscript: %v", err)
		}
		want, err := transcripts.Get(ctx, e.tenant, src.ID)
		if err != nil {
			t.Fatalf("Get source: %v", err)
		}
		got, err := transcripts.Get(ctx, e.tenant, replay.ID)
		if err != nil {
			t.Fatalf("Get copy: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("copied %d rows, want %d", len(got), len(want))
		}
		for i := range got {
			if got[i].Seq != uint64(i+1) || got[i].Role != want[i].Role || got[i].Content != want[i].Content ||
				!got[i].Timestamp.Equal(want[i].Timestamp) || got[i].SchemaVersion != want[i].SchemaVersion || got[i].ID == want[i].ID {
				t.Fatalf("row %d = %+v, want a copy of %+v at seq %d with a new id", i, got[i], want[i], i+1)
			}
		}
		if n := e.rowCount(t, replay.ID); n != 1 {
			t.Fatalf("replay rows = %d", n)
		}
	})

	t.Run("source with no transcript copies nothing", func(t *testing.T) {
		e.addTenant(t)
		src := e.row("alice")
		if err := e.sessions.Create(ctx, src); err != nil {
			t.Fatalf("seed: %v", err)
		}
		replay := replayOf(src)
		if err := e.sessions.CreateWithTranscript(ctx, replay, src.ID); err != nil {
			t.Fatalf("CreateWithTranscript: %v", err)
		}
		if n := messageCount(t, ctx, e.pg, replay.ID); n != 0 {
			t.Fatalf("copied %d rows from an empty source", n)
		}
	})

	t.Run("a copier failing after a partial write leaves neither", func(t *testing.T) {
		e.addTenant(t)
		src := seedSource(t)
		replay := replayOf(src)
		failAfterCopy = true
		defer func() { failAfterCopy = false }()
		if err := e.sessions.CreateWithTranscript(ctx, replay, src.ID); !errors.Is(err, errCopy) {
			t.Fatalf("CreateWithTranscript = %v, want the copier error", err)
		}
		if n := e.rowCount(t, replay.ID); n != 0 {
			t.Fatalf("failed copy left %d session rows", n)
		}
		if n := messageCount(t, ctx, e.pg, replay.ID); n != 0 {
			t.Fatalf("failed copy left %d message rows", n)
		}
	})

	t.Run("a store with no copier inserts nothing", func(t *testing.T) {
		e.addTenant(t)
		bare := pgstore.New(e.pg.Pool)
		replay := e.row("alice")
		if err := bare.CreateWithTranscript(ctx, replay, replay.ID); !errors.Is(err, sessionstore.ErrTranscriptCopierUnwired) {
			t.Fatalf("CreateWithTranscript = %v, want ErrTranscriptCopierUnwired", err)
		}
		if n := e.rowCount(t, replay.ID); n != 0 {
			t.Fatalf("unwired copier left %d rows", n)
		}
	})

	t.Run("a replay waiting on the flag write is refused and copies nothing", func(t *testing.T) {
		e.addTenant(t)
		src := seedSource(t)
		replay := replayOf(src) // admitted: its request-time checks ran before the flag
		hold, entered := make(chan struct{}), make(chan struct{})
		writeDone := make(chan error, 1)
		go func() {
			_, err := e.users.Update(ctx, e.tenant, "alice", func(u *userstore.User) error {
				close(entered)
				<-hold
				u.ProcessingRestricted, u.ErasureJobID = true, "job-1"
				return nil
			})
			writeDone <- err
		}()
		<-entered
		createDone := make(chan error, 1)
		go func() { createDone <- e.sessions.CreateWithTranscript(ctx, replay, src.ID) }()
		awaitLockWaiter(t, ctx, e.pg, createDone)
		close(hold)
		if err := awaitUpdateResult(t, "user Update", writeDone); err != nil {
			t.Fatalf("flag write: %v", err)
		}
		requirePGRefusal(t, awaitUpdateResult(t, "CreateWithTranscript", createDone), sessionstore.RefusalErasureInProgress)
		if n := messageCount(t, ctx, e.pg, replay.ID); n != 0 {
			t.Fatalf("refused replay left %d message rows", n)
		}
	})

	t.Run("a replay committed before the flag is erased by DeleteByUser", func(t *testing.T) {
		e.addTenant(t)
		src := seedSource(t)
		replay := replayOf(src)
		if err := e.sessions.CreateWithTranscript(ctx, replay, src.ID); err != nil {
			t.Fatalf("CreateWithTranscript: %v", err)
		}
		e.setUser(t, "alice", func(u *userstore.User) { u.ProcessingRestricted = true })
		if _, err := e.sessions.DeleteByUser(ctx, e.tenant, "alice"); err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		if n := messageCount(t, ctx, e.pg, replay.ID); n != 0 {
			t.Fatalf("erasure left %d message rows under the replayed id", n)
		}
	})
}
