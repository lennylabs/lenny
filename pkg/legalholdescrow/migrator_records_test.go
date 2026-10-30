// SPDX-License-Identifier: MIT

package legalholdescrow

import (
	"context"
	"errors"
	"testing"
	"time"
)

// countingSource records every original read, so a test can assert that a
// skipped hold never touches the original.
type countingSource struct {
	fakeSource
	reads int
}

func (s *countingSource) Read(ctx context.Context, a HeldArtifact) ([]byte, error) {
	s.reads++
	return s.fakeSource.Read(ctx, a)
}

// failingRecords is a RecordStore whose artifact lookup fails.
type failingRecords struct{ *MemRecordStore }

func (failingRecords) ActiveForArtifact(context.Context, string, string) ([]Record, error) {
	return nil, errors.New("escrow records unreachable")
}

// TestMigrateSkipsHoldWithActiveEscrowRecord_spec_12_8 pins the Phase 3.5
// re-entry skip: once a hold has an active escrow record, a re-entered
// migration returns the recorded escrow key without reading the original,
// which Phase 4 may already have deleted.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3.5), 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func TestMigrateSkipsHoldWithActiveEscrowRecord_spec_12_8(t *testing.T) {
	ctx := context.Background()
	recs := NewMemRecordStore()
	hold := HeldArtifact{ResourceType: "artifact", ResourceID: "r1", BlobURI: "u1", ArtifactURI: "u1", SessionID: "sess-1"}
	key := EscrowObjectKey("acme", "artifact", "r1")
	if err := recs.Save(ctx, Record{TenantID: "acme", ResourceType: "artifact", ResourceID: "r1", EscrowObjectKey: key, ArtifactURI: "u1"}); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	// The original is gone (Phase 4 deleted it), so a read would fail.
	src := &countingSource{fakeSource: fakeSource{data: map[string][]byte{}}}
	esc := &fakeEscrow{}
	led := &fakeLedger{}
	m := newMigrator(src, esc, led, defaultCfg())
	m.Records = recs

	res, err := m.Migrate(ctx, Input{TenantID: "acme", Holds: []HeldArtifact{hold}})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if src.reads != 0 || len(esc.written) != 0 || len(led.escrowed) != 0 {
		t.Fatalf("skipped hold touched the pipeline: reads=%d writes=%d escrowed=%d", src.reads, len(esc.written), len(led.escrowed))
	}
	if len(res.EscrowObjectKeys) != 1 || res.EscrowObjectKeys[0] != key {
		t.Fatalf("keys = %v, want [%s]", res.EscrowObjectKeys, key)
	}

	// A released record is not active, so the hold is escrowed again.
	if err := recs.MarkReleased(ctx, "acme", key, "alice", time.Unix(1, 0)); err != nil {
		t.Fatalf("MarkReleased: %v", err)
	}
	src.data["u1"] = []byte("held")
	if _, err := m.Migrate(ctx, Input{TenantID: "acme", Holds: []HeldArtifact{hold}}); err != nil {
		t.Fatalf("Migrate after release: %v", err)
	}
	if src.reads != 1 || len(esc.written) != 1 {
		t.Fatalf("released hold not re-escrowed: reads=%d writes=%d", src.reads, len(esc.written))
	}
}

// TestMigrateAbortsOnEscrowRecordLookupError_spec_12_8 pins the fail-closed
// re-entry lookup: an ActiveForArtifact error aborts the migration with no
// original read, escrow write, or escrowed ledger row for the hold.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3.5)
func TestMigrateAbortsOnEscrowRecordLookupError_spec_12_8(t *testing.T) {
	src := &countingSource{fakeSource: fakeSource{data: map[string][]byte{"u1": []byte("held")}}}
	esc := &fakeEscrow{}
	led := &fakeLedger{}
	m := newMigrator(src, esc, led, defaultCfg())
	m.Records = failingRecords{NewMemRecordStore()}

	_, err := m.Migrate(context.Background(), Input{TenantID: "acme", Holds: []HeldArtifact{{
		ResourceType: "artifact", ResourceID: "r1", BlobURI: "u1", ArtifactURI: "u1",
	}}})
	if err == nil {
		t.Fatal("Migrate succeeded with an unreachable escrow record store")
	}
	if src.reads != 0 || len(esc.written) != 0 || len(led.escrowed) != 0 {
		t.Fatalf("lookup error did not abort before the hold: reads=%d writes=%d escrowed=%d", src.reads, len(esc.written), len(led.escrowed))
	}
}

// TestMigrateNilRecordsSkipsLookup_spec_12_8 pins that a migrator with no
// record store escrows every hold.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3.5)
func TestMigrateNilRecordsSkipsLookup_spec_12_8(t *testing.T) {
	src := &countingSource{fakeSource: fakeSource{data: map[string][]byte{"u1": []byte("held")}}}
	esc := &fakeEscrow{}
	m := newMigrator(src, esc, &fakeLedger{}, defaultCfg())
	if _, err := m.Migrate(context.Background(), Input{TenantID: "acme", Holds: []HeldArtifact{{
		ResourceType: "artifact", ResourceID: "r1", BlobURI: "u1", ArtifactURI: "u1",
	}}}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if src.reads != 1 || len(esc.written) != 1 {
		t.Fatalf("reads=%d writes=%d, want 1 and 1", src.reads, len(esc.written))
	}
}
