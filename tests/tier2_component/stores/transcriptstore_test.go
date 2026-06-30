//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §15.1 transcript registry, exercising the
// Postgres-backed pkg/gateway/transcriptstore/pgstore against a real
// container. Covers append + monotonic seq assignment, ordered
// readback, paging, the ErrNotFound sentinel, and cross-tenant
// isolation.
package stores_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/transcriptstore/pgstore"
)

// seedSession creates a session row so a transcript can reference it
// (session_messages.session_id is a foreign key to sessions.id).
func seedSession(t *testing.T, ctx context.Context, sess *sessionpg.Store, tenant string) string {
	t.Helper()
	id := newUUID(t)
	if err := sess.Create(ctx, sessionstore.Session{
		ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return id
}

func entry(role, content string) transcriptstore.Entry {
	return transcriptstore.Entry{Role: role, Content: content}
}

// spec: 15.1
// diagnosis: the Postgres-backed transcript registry in
// pkg/gateway/transcriptstore/pgstore did not behave as specified.
// Append must assign continuing monotonic sequence numbers, Get must
// return entries in order, an empty append must be a no-op, Page must
// walk the transcript, and cross-tenant Get must return ErrNotFound.
func TestTranscriptStoreContract(t *testing.T) {
	t.Parallel()
	sess, pg := startStore(t)
	store := transcriptpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("append and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sid := seedSession(t, ctx, sess, tenant)
		if err := store.Append(
			ctx, tenant, sid,
			entry("user", "hello"),
			entry("assistant", "hi there"),
			entry("user", "bye"),
		); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := store.Get(ctx, tenant, sid)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("Get returned %d entries, want 3", len(got))
		}
		for i, want := range []transcriptstore.Entry{
			{Seq: 1, Role: "user", Content: "hello"},
			{Seq: 2, Role: "assistant", Content: "hi there"},
			{Seq: 3, Role: "user", Content: "bye"},
		} {
			if got[i].Seq != want.Seq || got[i].Role != want.Role || got[i].Content != want.Content {
				t.Errorf("entry %d = %+v, want %+v", i, got[i], want)
			}
			if got[i].Timestamp.IsZero() {
				t.Errorf("entry %d has zero Timestamp", i)
			}
		}
	})

	t.Run("append assigns continuing monotonic seq", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sid := seedSession(t, ctx, sess, tenant)
		if err := store.Append(ctx, tenant, sid, entry("user", "a"), entry("user", "b")); err != nil {
			t.Fatalf("first Append: %v", err)
		}
		if err := store.Append(ctx, tenant, sid, entry("user", "c"), entry("user", "d")); err != nil {
			t.Fatalf("second Append: %v", err)
		}
		got, err := store.Get(ctx, tenant, sid)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		for i, e := range got {
			if e.Seq != uint64(i+1) {
				t.Errorf("entry %d has seq %d, want %d", i, e.Seq, i+1)
			}
		}
	})

	t.Run("empty append is a no-op", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sid := seedSession(t, ctx, sess, tenant)
		if err := store.Append(ctx, tenant, sid); err != nil {
			t.Fatalf("empty Append: %v", err)
		}
		if _, err := store.Get(ctx, tenant, sid); !errors.Is(err, transcriptstore.ErrNotFound) {
			t.Errorf("Get after empty Append: got %v, want ErrNotFound", err)
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, tenant, newUUID(t)); !errors.Is(err, transcriptstore.ErrNotFound) {
			t.Errorf("Get unknown session: got %v, want ErrNotFound", err)
		}
	})

	t.Run("page walks the transcript", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sid := seedSession(t, ctx, sess, tenant)
		for i := 0; i < 5; i++ {
			if err := store.Append(ctx, tenant, sid, entry("user", "msg")); err != nil {
				t.Fatalf("Append %d: %v", i, err)
			}
		}
		first, err := store.Page(ctx, tenant, sid, 0, 2)
		if err != nil || len(first) != 2 || first[0].Seq != 1 || first[1].Seq != 2 {
			t.Fatalf("Page(0,2): %v %+v", err, first)
		}
		next, err := store.Page(ctx, tenant, sid, 2, 2)
		if err != nil || len(next) != 2 || next[0].Seq != 3 || next[1].Seq != 4 {
			t.Fatalf("Page(2,2): %v %+v", err, next)
		}
		last, err := store.Page(ctx, tenant, sid, 4, 10)
		if err != nil || len(last) != 1 || last[0].Seq != 5 {
			t.Fatalf("Page(4,10): %v %+v", err, last)
		}
		// afterSeq past the end: the transcript exists, so an empty
		// page with no error.
		past, err := store.Page(ctx, tenant, sid, 5, 10)
		if err != nil || len(past) != 0 {
			t.Errorf("Page(5,10): got %d entries, err %v; want empty, nil", len(past), err)
		}
		// A session with no transcript at all is ErrNotFound.
		if _, err := store.Page(ctx, tenant, newUUID(t), 0, 10); !errors.Is(err, transcriptstore.ErrNotFound) {
			t.Errorf("Page of unknown session: got %v, want ErrNotFound", err)
		}
	})

	t.Run("cross-tenant get is isolated", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		sid := seedSession(t, ctx, sess, owner)
		if err := store.Append(ctx, owner, sid, entry("user", "secret")); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if _, err := store.Get(ctx, intruder, sid); !errors.Is(err, transcriptstore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
	})

	// spec: §15.4.1 line 1694 — every MessageEnvelope persisted to
	// session_messages carries the gateway-stamped schema_version; §15.5
	// item 7 — integer starting at 1. The migration 0131 column must
	// round-trip through INSERT and SELECT: a zero-value caller field is
	// normalized to the v1 baseline, an explicit version is preserved.
	t.Run("schema_version round-trips and defaults to v1", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sid := seedSession(t, ctx, sess, tenant)
		if err := store.Append(
			ctx, tenant, sid,
			entry("user", "defaulted"),
			transcriptstore.Entry{Role: "assistant", Content: "explicit", SchemaVersion: 2},
		); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := store.Get(ctx, tenant, sid)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("Get returned %d entries, want 2", len(got))
		}
		if got[0].SchemaVersion != transcriptstore.SchemaVersion {
			t.Errorf("entry[0].SchemaVersion = %d, want %d", got[0].SchemaVersion, transcriptstore.SchemaVersion)
		}
		if got[1].SchemaVersion != 2 {
			t.Errorf("entry[1].SchemaVersion = %d, want 2", got[1].SchemaVersion)
		}
	})
}
