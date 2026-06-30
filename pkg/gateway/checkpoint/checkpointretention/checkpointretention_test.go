// SPDX-License-Identifier: MIT

package checkpointretention

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fixedClock advances by 1ms per call so successive Insert /
// Rotate calls see a monotonic timeline.
type fixedClock struct {
	t time.Time
}

func (f *fixedClock) now() time.Time {
	f.t = f.t.Add(time.Millisecond)
	return f.t
}

func newStore() (*MemoryStore, *fixedClock) {
	c := &fixedClock{t: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)}
	return NewMemoryStore(c.now), c
}

// spec: §4.4 line 234 — Insert validates tenant + session + ref.
func TestInsert_Validates(t *testing.T) {
	store, _ := newStore()
	cases := []Record{
		{TenantID: "", SessionID: "s", Ref: "r"},
		{TenantID: "t", SessionID: "", Ref: "r"},
		{TenantID: "t", SessionID: "s", Ref: ""},
	}
	for _, r := range cases {
		if err := store.Insert(context.Background(), r); err == nil {
			t.Fatalf("Insert(%+v): want error, got nil", r)
		}
	}
}

// spec: §4.4 line 234 — Insert is idempotent at the composite key.
func TestInsert_RejectsDuplicate(t *testing.T) {
	store, _ := newStore()
	r := Record{TenantID: "acme", SessionID: "s1", Ref: "ref-1"}
	if err := store.Insert(context.Background(), r); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := store.Insert(context.Background(), r)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second insert: want ErrDuplicate, got %v", err)
	}
}

// spec: §12.5 — Rotate retains the latest 2 checkpoints.
func TestRotate_RetainsLatestTwo(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	for _, ref := range []string{"r1", "r2", "r3", "r4"} {
		if err := store.Insert(ctx, Record{TenantID: "acme", SessionID: "sess", Ref: ref}); err != nil {
			t.Fatalf("insert %s: %v", ref, err)
		}
	}
	transitioned, err := store.Rotate(ctx, "acme", "sess", "")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	// r4 and r3 are the latest two (inserted last); r1 and r2 are the
	// older two that must transition to retained=false.
	wantRefs := []string{"r1", "r2"}
	if len(transitioned) != len(wantRefs) {
		t.Fatalf("transitioned: got %d rows, want %d", len(transitioned), len(wantRefs))
	}
	for i, want := range wantRefs {
		if transitioned[i].Ref != want {
			t.Fatalf("transitioned[%d].Ref: got %q want %q", i, transitioned[i].Ref, want)
		}
		if transitioned[i].Retained {
			t.Fatalf("transitioned[%d].Retained: want false", i)
		}
		if transitioned[i].DeletedAt.IsZero() {
			t.Fatalf("transitioned[%d].DeletedAt: must be set", i)
		}
	}
	// Cross-check via List: the two retained rows are r4 and r3.
	rows, err := store.List(ctx, "acme", "sess", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("List: want 4 rows, got %d", len(rows))
	}
	retained := 0
	for _, row := range rows {
		if row.Retained {
			retained++
		}
	}
	if retained != RetainedCount {
		t.Fatalf("retained count: got %d want %d", retained, RetainedCount)
	}
}

// spec: §4.4 line 236 — Rotate is idempotent (second call is a no-op).
func TestRotate_Idempotent(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	for _, ref := range []string{"r1", "r2", "r3"} {
		_ = store.Insert(ctx, Record{TenantID: "t", SessionID: "s", Ref: ref})
	}
	t1, err := store.Rotate(ctx, "t", "s", "")
	if err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	t2, err := store.Rotate(ctx, "t", "s", "")
	if err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}
	if len(t1) != 1 {
		t.Fatalf("first rotate: want 1 transition, got %d", len(t1))
	}
	if len(t2) != 0 {
		t.Fatalf("second rotate: want 0 transitions, got %d", len(t2))
	}
}

// spec: §4.4 line 234 — Rotate handles fewer than RetainedCount rows.
func TestRotate_FewerThanCap(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	_ = store.Insert(ctx, Record{TenantID: "t", SessionID: "s", Ref: "only"})
	transitioned, err := store.Rotate(ctx, "t", "s", "")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if len(transitioned) != 0 {
		t.Fatalf("transitioned: want 0, got %d", len(transitioned))
	}
}

// spec: §4.4 line 234 — Rotate scopes by (tenant, session).
func TestRotate_ScopedBySession(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	// Two sessions of the same tenant, each with three checkpoints.
	for _, sess := range []string{"sA", "sB"} {
		for _, ref := range []string{"r1", "r2", "r3"} {
			_ = store.Insert(ctx, Record{TenantID: "t", SessionID: sess, Ref: ref})
		}
	}
	if _, err := store.Rotate(ctx, "t", "sA", ""); err != nil {
		t.Fatalf("Rotate sA: %v", err)
	}
	// sB must be untouched: rotating sA's catalog must not retire any
	// of sB's checkpoints.
	rows, _ := store.List(ctx, "t", "sB", "")
	for _, row := range rows {
		if !row.Retained || !row.DeletedAt.IsZero() {
			t.Fatalf("sB row %s should not have been rotated: %+v", row.Ref, row)
		}
	}
}

// spec: §12.5 — ListSoftDeletedBefore returns rows whose deleted_at
// is older than cutoff.
func TestListSoftDeletedBefore(t *testing.T) {
	c := &fixedClock{t: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)}
	store := NewMemoryStore(c.now)
	ctx := context.Background()
	for _, ref := range []string{"r1", "r2", "r3"} {
		_ = store.Insert(ctx, Record{TenantID: "t", SessionID: "s", Ref: ref})
	}
	if _, err := store.Rotate(ctx, "t", "s", ""); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	// All rows so far are stamped around 2026-05-24. A cutoff one year
	// later returns the soft-deleted row.
	cutoff := time.Date(2027, 5, 24, 0, 0, 0, 0, time.UTC)
	rows, err := store.ListSoftDeletedBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("ListSoftDeletedBefore: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	// A cutoff before the deleted_at returns nothing.
	earlier := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rows, _ = store.ListSoftDeletedBefore(ctx, earlier)
	if len(rows) != 0 {
		t.Fatalf("early cutoff: want 0 rows, got %d", len(rows))
	}
}

// spec: §12.5 — HardDelete removes the row.
func TestHardDelete(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	_ = store.Insert(ctx, Record{TenantID: "t", SessionID: "s", Ref: "r"})
	if err := store.HardDelete(ctx, "t", "s", "", "r"); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	rows, _ := store.List(ctx, "t", "s", "")
	if len(rows) != 0 {
		t.Fatalf("List after HardDelete: want 0, got %d", len(rows))
	}
}

// spec: §12.8 — DeleteByUser removes rows for the supplied sessions.
func TestDeleteByUser(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	for _, sess := range []string{"s1", "s2", "s3"} {
		_ = store.Insert(ctx, Record{TenantID: "t", SessionID: sess, Ref: "r"})
	}
	if err := store.DeleteByUser(ctx, "t", "alice", []string{"s1", "s3"}); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	for _, sess := range []string{"s1", "s3"} {
		rows, _ := store.List(ctx, "t", sess, "")
		if len(rows) != 0 {
			t.Fatalf("session %s: want 0 rows, got %d", sess, len(rows))
		}
	}
	rows, _ := store.List(ctx, "t", "s2", "")
	if len(rows) != 1 {
		t.Fatalf("session s2: want 1 row, got %d", len(rows))
	}
}

// spec: §12.8 — DeleteByTenant is idempotent.
func TestDeleteByTenant(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	_ = store.Insert(ctx, Record{TenantID: "t1", SessionID: "s", Ref: "r"})
	_ = store.Insert(ctx, Record{TenantID: "t2", SessionID: "s", Ref: "r"})
	if err := store.DeleteByTenant(ctx, "t1"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	rows, _ := store.List(ctx, "t1", "s", "")
	if len(rows) != 0 {
		t.Fatalf("t1: want 0 rows, got %d", len(rows))
	}
	rows, _ = store.List(ctx, "t2", "s", "")
	if len(rows) != 1 {
		t.Fatalf("t2: want 1 row, got %d", len(rows))
	}
	// Re-running is a no-op.
	if err := store.DeleteByTenant(ctx, "t1"); err != nil {
		t.Fatalf("DeleteByTenant idempotent: %v", err)
	}
}

// spec: §12.5 lines 313, 326 — in concurrent-workspace mode the
// "latest 2" cap applies independently per slot. A session with two
// slots each carrying three checkpoints retains two per slot (four
// total), not two for the whole session.
func TestRotate_PerSlotIndependent(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	for _, slot := range []string{"slot-a", "slot-b"} {
		for _, ref := range []string{"r1", "r2", "r3"} {
			if err := store.Insert(ctx, Record{TenantID: "t", SessionID: "s", SlotID: slot, Ref: slot + "-" + ref}); err != nil {
				t.Fatalf("insert %s/%s: %v", slot, ref, err)
			}
		}
	}
	// Rotating slot-a must not touch slot-b.
	transA, err := store.Rotate(ctx, "t", "s", "slot-a")
	if err != nil {
		t.Fatalf("Rotate slot-a: %v", err)
	}
	if len(transA) != 1 {
		t.Fatalf("slot-a rotate: want 1 transition, got %d", len(transA))
	}
	// slot-b is untouched: all three rows retained, none soft-deleted.
	rowsB, _ := store.List(ctx, "t", "s", "slot-b")
	if len(rowsB) != 3 {
		t.Fatalf("slot-b: want 3 rows, got %d", len(rowsB))
	}
	for _, row := range rowsB {
		if !row.Retained || !row.DeletedAt.IsZero() {
			t.Fatalf("slot-b row %s rotated unexpectedly: %+v", row.Ref, row)
		}
	}
	// slot-a now has two retained rows.
	rowsA, _ := store.List(ctx, "t", "s", "slot-a")
	retainedA := 0
	for _, row := range rowsA {
		if row.Retained {
			retainedA++
		}
	}
	if retainedA != RetainedCount {
		t.Fatalf("slot-a retained: got %d want %d", retainedA, RetainedCount)
	}
}

// spec: §4.4 line 236 — Rotate stamps deleted_at on transitioned rows.
func TestRotate_StampsDeletedAt(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	for _, ref := range []string{"r1", "r2", "r3"} {
		_ = store.Insert(ctx, Record{TenantID: "t", SessionID: "s", Ref: ref})
	}
	transitioned, err := store.Rotate(ctx, "t", "s", "")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if len(transitioned) != 1 {
		t.Fatalf("transitioned: want 1, got %d", len(transitioned))
	}
	if transitioned[0].DeletedAt.IsZero() {
		t.Fatal("transitioned row must have DeletedAt set")
	}
}

// TestInsert_StampsSchemaVersion_spec_15_5_item7 verifies the gateway-owned
// checkpoint-metadata schema_version is stamped on Insert: a zero-value
// caller field defaults to the v1 baseline and an explicit value is kept.
//
// spec: §15.5 item 7 — "checkpoint metadata" carries a schemaVersion
// integer "starting at 1", set at write time by the gateway.
func TestInsert_StampsSchemaVersion_spec_15_5_item7(t *testing.T) {
	store, _ := newStore()
	ctx := context.Background()
	if err := store.Insert(ctx, Record{TenantID: "acme", SessionID: "s", Ref: "a"}); err != nil {
		t.Fatalf("Insert default: %v", err)
	}
	if err := store.Insert(ctx, Record{TenantID: "acme", SessionID: "s", Ref: "b", SchemaVersion: 3}); err != nil {
		t.Fatalf("Insert explicit: %v", err)
	}
	rows, err := store.List(ctx, "acme", "s", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.Ref] = r.SchemaVersion
	}
	if got["a"] != SchemaVersion {
		t.Errorf("ref a SchemaVersion = %d, want %d (v1 baseline)", got["a"], SchemaVersion)
	}
	if got["b"] != 3 {
		t.Errorf("ref b SchemaVersion = %d, want 3 (explicit preserved)", got["b"])
	}
	if SchemaVersion != 1 {
		t.Errorf("SchemaVersion const = %d, want 1 per §15.5 item 7", SchemaVersion)
	}
}
