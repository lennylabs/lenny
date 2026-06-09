// SPDX-License-Identifier: MIT

package legalholdescrow

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeDeleter struct {
	deleted []string
	err     error
}

func (f *fakeDeleter) Delete(_ context.Context, _, _, key string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, key)
	return nil
}

type fakeReleaseLedger struct {
	events []Released
	err    error
}

func (f *fakeReleaseLedger) EscrowReleased(_ context.Context, ev Released) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, ev)
	return nil
}

func seedRecords(t *testing.T, store RecordStore, recs ...Record) {
	t.Helper()
	for _, r := range recs {
		if err := store.Save(context.Background(), r); err != nil {
			t.Fatalf("seed record: %v", err)
		}
	}
}

func artifactRec(tenant, session, uri, key string) Record {
	return Record{
		TenantID:        tenant,
		ResourceType:    "artifact",
		ResourceID:      "rid-" + key,
		EscrowObjectKey: key,
		EscrowRegion:    "eu-west-1",
		EscrowKEKID:     "platform:legal_hold_escrow:eu-west-1",
		TenantDeleteJob: "job-1",
		SessionID:       session,
		ArtifactURI:     uri,
		MigratedAt:      time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC),
	}
}

// spec: §12.8 line 884 — clearing a session hold releases every escrow
// object escrowed under it: each object is deleted, marked released, and a
// legal_hold.escrow_released event is emitted.
func TestReleaseForSession_spec_12_8_line884(t *testing.T) {
	t.Parallel()
	store := NewMemRecordStore()
	seedRecords(t, store,
		artifactRec("acme", "sess-1", "blob://acme/sess-1/a", "k1"),
		artifactRec("acme", "sess-1", "blob://acme/sess-1/b", "k2"),
		artifactRec("acme", "sess-2", "blob://acme/sess-2/c", "k3"), // other session
	)
	del := &fakeDeleter{}
	led := &fakeReleaseLedger{}
	r := &Releaser{Records: store, Deleter: del, Ledger: led, Clock: func() time.Time { return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) }}

	n, err := r.ReleaseForSession(context.Background(), "acme", "sess-1", "alice@acme.com")
	if err != nil {
		t.Fatalf("ReleaseForSession: %v", err)
	}
	if n != 2 {
		t.Fatalf("released = %d, want 2", n)
	}
	if len(del.deleted) != 2 || len(led.events) != 2 {
		t.Fatalf("deleted=%d events=%d, want 2/2", len(del.deleted), len(led.events))
	}
	for _, ev := range led.events {
		if ev.ClearedBy != "alice@acme.com" || ev.EscrowRegion != "eu-west-1" || ev.TenantID != "acme" {
			t.Errorf("escrow_released event fields wrong: %+v", ev)
		}
	}
	// sess-2's object is untouched.
	for _, k := range del.deleted {
		if k == "k3" {
			t.Error("released sess-2 object k3 on a sess-1 clear")
		}
	}
	// Idempotent: a re-clear finds nothing (records marked released).
	n2, err := r.ReleaseForSession(context.Background(), "acme", "sess-1", "alice@acme.com")
	if err != nil || n2 != 0 {
		t.Errorf("re-release = (%d, %v), want (0, nil)", n2, err)
	}
}

// spec: §12.8 line 884 — clearing an artifact's own hold releases exactly it.
func TestReleaseForArtifact_spec_12_8_line884(t *testing.T) {
	t.Parallel()
	store := NewMemRecordStore()
	seedRecords(t, store,
		artifactRec("acme", "sess-1", "blob://acme/sess-1/a", "k1"),
		artifactRec("acme", "sess-1", "blob://acme/sess-1/b", "k2"),
	)
	del := &fakeDeleter{}
	led := &fakeReleaseLedger{}
	r := &Releaser{Records: store, Deleter: del, Ledger: led}
	n, err := r.ReleaseForArtifact(context.Background(), "acme", "blob://acme/sess-1/a", "bob@acme.com")
	if err != nil {
		t.Fatalf("ReleaseForArtifact: %v", err)
	}
	if n != 1 || len(del.deleted) != 1 || del.deleted[0] != "k1" {
		t.Fatalf("released = %d, deleted = %v, want 1 / [k1]", n, del.deleted)
	}
}

// A delete failure aborts the release with the event not emitted for the
// failed object: an escrow_released row is written only for an object that
// is actually gone.
func TestRelease_deleteError_failsClosed(t *testing.T) {
	t.Parallel()
	store := NewMemRecordStore()
	seedRecords(t, store, artifactRec("acme", "sess-1", "blob://acme/sess-1/a", "k1"))
	led := &fakeReleaseLedger{}
	r := &Releaser{Records: store, Deleter: &fakeDeleter{err: errors.New("bucket down")}, Ledger: led}
	n, err := r.ReleaseForSession(context.Background(), "acme", "sess-1", "alice@acme.com")
	if err == nil {
		t.Fatal("expected delete error")
	}
	if n != 0 || len(led.events) != 0 {
		t.Errorf("released=%d events=%d, want 0/0 on delete failure", n, len(led.events))
	}
}

// No escrow records for the cleared resource: a clean no-op.
func TestRelease_noRecords(t *testing.T) {
	t.Parallel()
	r := &Releaser{Records: NewMemRecordStore(), Deleter: &fakeDeleter{}, Ledger: &fakeReleaseLedger{}}
	n, err := r.ReleaseForSession(context.Background(), "acme", "sess-x", "alice@acme.com")
	if err != nil || n != 0 {
		t.Errorf("release with no records = (%d, %v), want (0, nil)", n, err)
	}
}

// MemRecordStore filters active records by session/artifact and excludes
// released rows.
func TestMemRecordStore_activeFiltering(t *testing.T) {
	t.Parallel()
	store := NewMemRecordStore()
	seedRecords(t, store,
		artifactRec("acme", "sess-1", "blob://acme/sess-1/a", "k1"),
		artifactRec("globex", "sess-1", "blob://globex/sess-1/a", "k9"), // other tenant
	)
	ctx := context.Background()
	got, _ := store.ActiveForSession(ctx, "acme", "sess-1")
	if len(got) != 1 || got[0].EscrowObjectKey != "k1" {
		t.Fatalf("ActiveForSession(acme) = %v, want [k1] (tenant-scoped)", got)
	}
	if err := store.MarkReleased(ctx, "acme", "k1", "alice@acme.com", time.Now()); err != nil {
		t.Fatalf("MarkReleased: %v", err)
	}
	got, _ = store.ActiveForSession(ctx, "acme", "sess-1")
	if len(got) != 0 {
		t.Errorf("ActiveForSession after release = %v, want empty", got)
	}
}
