// SPDX-License-Identifier: MIT

package transcriptstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
)

func TestAppendAndGet(t *testing.T) {
	s := transcriptstore.NewMemory()
	if _, err := s.Append(
		context.Background(), "acme", "sess_1",
		transcriptstore.Entry{Role: "user", Content: "hello"},
		transcriptstore.Entry{Role: "assistant", Content: "hi"},
	); err != nil {
		t.Fatalf("Append: %v", err)
	}
	entries, err := s.Get(context.Background(), "acme", "sess_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries: %d", len(entries))
	}
	if entries[0].Seq != 1 || entries[1].Seq != 2 {
		t.Errorf("seq not monotonic: %+v", entries)
	}
	if entries[0].Timestamp.IsZero() {
		t.Error("timestamp should be auto-set")
	}
}

func TestAppendAccumulatesSeq(t *testing.T) {
	s := transcriptstore.NewMemory()
	_, _ = s.Append(context.Background(), "acme", "sess_1", transcriptstore.Entry{Content: "a"})
	_, _ = s.Append(context.Background(), "acme", "sess_1", transcriptstore.Entry{Content: "b"})
	entries, _ := s.Get(context.Background(), "acme", "sess_1")
	if len(entries) != 2 || entries[1].Seq != 2 {
		t.Errorf("seq across appends: %+v", entries)
	}
}

func TestGetMissing(t *testing.T) {
	s := transcriptstore.NewMemory()
	if _, err := s.Get(context.Background(), "acme", "missing"); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	s := transcriptstore.NewMemory()
	_, _ = s.Append(context.Background(), "acme", "sess_1", transcriptstore.Entry{Content: "acme-data"})
	// Different tenant, same session id — must not see acme's data.
	if _, err := s.Get(context.Background(), "globex", "sess_1"); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Errorf("cross-tenant Get should be ErrNotFound: %v", err)
	}
}

func TestPage(t *testing.T) {
	s := transcriptstore.NewMemory()
	for i := 0; i < 5; i++ {
		_, _ = s.Append(context.Background(), "acme", "sess_1", transcriptstore.Entry{Content: "x"})
	}
	page, err := s.Page(context.Background(), "acme", "sess_1", 2, 2)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if len(page) != 2 || page[0].Seq != 3 || page[1].Seq != 4 {
		t.Errorf("page: %+v", page)
	}
}

func TestPageMissing(t *testing.T) {
	s := transcriptstore.NewMemory()
	if _, err := s.Page(context.Background(), "acme", "missing", 0, 10); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Errorf("Page missing: %v", err)
	}
}

func TestAppendEmptyIsNoOp(t *testing.T) {
	s := transcriptstore.NewMemory()
	if last, err := s.Append(context.Background(), "acme", "sess_1"); err != nil || last != 0 {
		t.Errorf("empty append: last=%d err=%v, want 0 and nil", last, err)
	}
	if _, err := s.Get(context.Background(), "acme", "sess_1"); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Error("empty append should not create a transcript")
	}
}

// spec: §12.1 — DeleteByUser on a session-scoped store is a
// no-op that returns 0 erased rows; the §12.8 orchestrator walks the
// user's sessions and calls DeleteBySession per session.
func TestDeleteByUserIsNoOp_spec_12_1(t *testing.T) {
	s := transcriptstore.NewMemory()
	_, _ = s.Append(context.Background(), "acme", "sess_1",
		transcriptstore.Entry{Role: "user", Content: "hi"})
	n, err := s.DeleteByUser(context.Background(), "acme", "alice")
	if err != nil || n != 0 {
		t.Errorf("DeleteByUser: n=%d err=%v", n, err)
	}
	if got, _ := s.Get(context.Background(), "acme", "sess_1"); len(got) != 1 {
		t.Errorf("transcript should survive DeleteByUser: %d", len(got))
	}
}

// spec: §12.1 / §12.8 Phase 4 — DeleteByTenant removes every
// transcript belonging to the tenant; other tenants are unaffected.
func TestDeleteByTenantRemovesAll_spec_12_1(t *testing.T) {
	s := transcriptstore.NewMemory()
	_, _ = s.Append(context.Background(), "acme", "sess_1", transcriptstore.Entry{Content: "a"})
	_, _ = s.Append(context.Background(), "acme", "sess_2", transcriptstore.Entry{Content: "b"})
	_, _ = s.Append(context.Background(), "globex", "sess_1", transcriptstore.Entry{Content: "c"})
	n, err := s.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByTenant should remove 2 acme transcripts, got %d", n)
	}
	if _, err := s.Get(context.Background(), "acme", "sess_1"); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Errorf("acme/sess_1 should be gone: %v", err)
	}
	if _, err := s.Get(context.Background(), "globex", "sess_1"); err != nil {
		t.Errorf("globex/sess_1 should survive: %v", err)
	}
}

// TestAppendStampsSchemaVersion_spec_15_4_1_1694 verifies the gateway-owned
// schema_version is stamped on every persisted MessageEnvelope: a
// zero-value caller field is normalized to the v1 baseline, and an explicit
// version is preserved verbatim.
//
// spec: §15.4 — "Every MessageEnvelope persisted to the
// session_messages table carries this field"; §15.5 item 7 — integer
// "starting at 1".
func TestAppendStampsSchemaVersion_spec_15_4_1_1694(t *testing.T) {
	s := transcriptstore.NewMemory()
	if _, err := s.Append(
		context.Background(), "acme", "sess_sv",
		transcriptstore.Entry{Role: "user", Content: "zero-defaulted"},
		transcriptstore.Entry{Role: "assistant", Content: "explicit", SchemaVersion: 2},
	); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := s.Get(context.Background(), "acme", "sess_sv")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].SchemaVersion != transcriptstore.SchemaVersion {
		t.Errorf("entry[0].SchemaVersion = %d, want %d (v1 baseline)",
			got[0].SchemaVersion, transcriptstore.SchemaVersion)
	}
	if got[1].SchemaVersion != 2 {
		t.Errorf("entry[1].SchemaVersion = %d, want 2 (explicit preserved)", got[1].SchemaVersion)
	}
	if transcriptstore.SchemaVersion != 1 {
		t.Errorf("SchemaVersion const = %d, want 1 per §15.5 item 7", transcriptstore.SchemaVersion)
	}
}

// TestAppendReturnsLastSeq_spec_15 pins the end-sequence contract: each Append
// returns the Seq it assigned to its last entry, successive appends return
// contiguous last-seqs, the returned value equals the highest Seq a Get reads
// back, and an empty append returns 0 without moving the sequence, including
// on a session that already holds entries.
//
// spec: §15.1 (REST API), §15 (Open Responses conversation sessions)
func TestAppendReturnsLastSeq_spec_15(t *testing.T) {
	ctx := context.Background()
	s := transcriptstore.NewMemory()
	entry := func(c string) transcriptstore.Entry { return transcriptstore.Entry{Role: "user", Content: c} }
	cases := []struct {
		name    string
		entries []transcriptstore.Entry
		want    uint64
	}{
		{name: "empty append on an empty session", entries: nil, want: 0},
		{name: "single entry", entries: []transcriptstore.Entry{entry("a")}, want: 1},
		{name: "batch of three", entries: []transcriptstore.Entry{entry("b"), entry("c"), entry("d")}, want: 4},
		{name: "empty append on a populated session", entries: nil, want: 0},
		{name: "single entry after the empty append", entries: []transcriptstore.Entry{entry("e")}, want: 5},
		{name: "batch of two", entries: []transcriptstore.Entry{entry("f"), entry("g")}, want: 7},
	}
	for _, tc := range cases {
		got, err := s.Append(ctx, "acme", "sess_seq", tc.entries...)
		if err != nil {
			t.Fatalf("%s: Append: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: lastSeq = %d, want %d", tc.name, got, tc.want)
		}
		if tc.want == 0 {
			continue
		}
		all, err := s.Get(ctx, "acme", "sess_seq")
		if err != nil {
			t.Fatalf("%s: Get: %v", tc.name, err)
		}
		if top := all[len(all)-1].Seq; top != got {
			t.Fatalf("%s: highest stored Seq = %d, Append returned %d", tc.name, top, got)
		}
	}
	// Another session's sequence is independent of this one.
	if got, err := s.Append(ctx, "acme", "sess_other", entry("x")); err != nil || got != 1 {
		t.Fatalf("independent session: lastSeq=%d err=%v, want 1", got, err)
	}
}
