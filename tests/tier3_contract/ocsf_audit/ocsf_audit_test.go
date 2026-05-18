//go:build contract

// SPDX-License-Identifier: MIT

// Contract test for the §11.7 / §12.3.5 OCSF audit-event translation.
// Every audit event Lenny emits has an OCSF v1.1.0 translation; this
// suite generates one of each catalog event type and asserts the
// translated record satisfies the OCSF structural contract the §11.7
// field-mapping table pins — class_uid, category_uid, activity_id,
// type_uid, time, severity_id, metadata.{uid,sequence,tenant_uid,
// version}, and the unmapped.lenny_chain extension. It exercises the
// retranslation path: a failed translation produces a dead-letter
// receipt, and the OCSF wire version is pinned.
//
// The OCSF v1.1.0 JSON schema bundle is not vendored — §11.7 pins the
// field mapping, not an external schema artifact, and the mapping is
// regenerated in CI from Lenny's event-type catalog. This suite
// therefore validates the record against the §11.7 mapping contract
// directly.
//
// This file converts the TestOCSFTranslationCoversEveryEventType,
// TestOCSFRetranslationRetry, and TestOCSFSchemaVersionPin scaffolds
// (formerly skipped in scaffolds_test.go) into real tests.
package ocsf_audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// requireOCSFShape asserts a translated record satisfies the §11.7 /
// OCSF v1.1.0 structural contract: the class identifiers are
// populated, type_uid is class_uid*100+activity_id, the metadata block
// carries the pinned version and the per-record uid, and the
// hash-chain extension is present.
func requireOCSFShape(t *testing.T, rec ocsf.Record, in ocsf.Input) {
	t.Helper()
	if rec.ClassUID == 0 {
		t.Errorf("%s: class_uid is 0 — every OCSF record has a class", in.EventType)
	}
	if rec.CategoryUID == 0 {
		t.Errorf("%s: category_uid is 0", in.EventType)
	}
	if rec.TypeUID != rec.ClassUID*100+rec.ActivityID {
		t.Errorf("%s: type_uid = %d, want class_uid*100+activity_id = %d",
			in.EventType, rec.TypeUID, rec.ClassUID*100+rec.ActivityID)
	}
	if rec.Metadata.Version != ocsf.Version {
		t.Errorf("%s: metadata.version = %q, want %q", in.EventType, rec.Metadata.Version, ocsf.Version)
	}
	if rec.Metadata.UID != in.ID {
		t.Errorf("%s: metadata.uid = %q, want the row id %q", in.EventType, rec.Metadata.UID, in.ID)
	}
	if rec.Metadata.TenantUID != in.TenantID {
		t.Errorf("%s: metadata.tenant_uid = %q, want %q", in.EventType, rec.Metadata.TenantUID, in.TenantID)
	}
	if rec.Metadata.Sequence != in.Sequence {
		t.Errorf("%s: metadata.sequence = %d, want %d", in.EventType, rec.Metadata.Sequence, in.Sequence)
	}
	if rec.SeverityID == 0 {
		t.Errorf("%s: severity_id is 0 — OCSF severity is 1..6", in.EventType)
	}
	if rec.Unmapped.Chain.Integrity == "" {
		t.Errorf("%s: unmapped.lenny_chain.integrity is empty", in.EventType)
	}
	// The record must marshal to a JSON object carrying the OCSF keys.
	b, err := ocsf.MarshalRecord(rec)
	if err != nil {
		t.Fatalf("%s: MarshalRecord: %v", in.EventType, err)
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("%s: translated record is not a JSON object: %v", in.EventType, err)
	}
	for _, key := range []string{"class_uid", "category_uid", "activity_id", "type_uid", "time", "severity_id", "metadata", "unmapped"} {
		if _, ok := obj[key]; !ok {
			t.Errorf("%s: OCSF record is missing the %q key", in.EventType, key)
		}
	}
}

// spec: 11.7
// diagnosis: §11.7 says every audit event leaving the hot tier is an
// OCSF v1.1.0 record, and the §11.7 event-type → class/activity table
// covers Lenny's full event-type catalog. Generating one of each
// catalog event type and translating it must yield a record that
// satisfies the OCSF structural contract.
func TestOCSFTranslationCoversEveryEventType(t *testing.T) {
	catalog := ocsf.CatalogEventTypes()
	if len(catalog) == 0 {
		t.Fatal("the OCSF event-type catalog is empty")
	}
	for i, et := range catalog {
		in := ocsf.Input{
			ID:                 "id-" + et,
			Sequence:           uint64(i + 1),
			TenantID:           "acme",
			EventType:          et,
			EventSchemaVersion: "v1",
			CreatedAtUnixMs:    1_700_000_000_000,
			Payload:            json.RawMessage(`{"user_id":"alice@acme.com","caller_kind":"service"}`),
			PrevHash:           "abcd",
			ChainIntegrity:     audit.ChainVerified,
		}
		rec, err := ocsf.Translate(in)
		if err != nil {
			t.Errorf("catalog event %q failed translation: %v", et, err)
			continue
		}
		requireOCSFShape(t, rec, in)
	}
}

// spec: 11.7
// diagnosis: §11.7 says payload fields with no explicit OCSF mapping
// are routed verbatim into unmapped.lenny.*, and the chain fields into
// unmapped.lenny_chain.*, so external tools can verify the hash chain
// without being OCSF-aware. The mapping must be reversible.
func TestOCSFUnmappedExtensionPreservesPayload(t *testing.T) {
	in := ocsf.Input{
		ID: "id-1", Sequence: 1, TenantID: "acme", EventType: "session.created",
		EventSchemaVersion: "v2",
		Payload:            json.RawMessage(`{"lenny_specific":"value-9","nested":{"a":1}}`),
		PrevHash:           "ff00", ChainIntegrity: audit.ChainVerified,
	}
	rec, err := ocsf.Translate(in)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if rec.Unmapped.Lenny["lenny_specific"] != "value-9" {
		t.Errorf("unmapped.lenny.lenny_specific = %v, want value-9", rec.Unmapped.Lenny["lenny_specific"])
	}
	if rec.Unmapped.Lenny["event_schema_version"] != "v2" {
		t.Errorf("unmapped.lenny.event_schema_version = %v, want v2 (the §11.7 echo)",
			rec.Unmapped.Lenny["event_schema_version"])
	}
	if rec.Unmapped.Chain.PrevHash != "ff00" {
		t.Errorf("unmapped.lenny_chain.prev_hash = %q, want ff00", rec.Unmapped.Chain.PrevHash)
	}
}

// spec: 11.7
// diagnosis: §11.7 says a failed translation does not roll the
// canonical row back; the row transitions retry_pending → dead_lettered
// and a translation-failure receipt (OCSF class 2004) is emitted in
// place of the untranslatable event so the SIEM pointer advances. The
// retranscribe state machine must walk that path.
func TestOCSFRetranslationRetry(t *testing.T) {
	store := &stateStore{rows: map[string]*rowState{}}
	// An event type with no §11.7 class mapping fails every attempt.
	store.add("acme", 1, "permanently.unmapped")
	cfg := ocsf.DefaultTranslationConfig()
	cfg.MaxAttempts = 3
	sink := &capturingSink{}
	tr := ocsf.NewTranslator(store, sink, cfg, nil)
	ctx := context.Background()

	// Attempts before the last transition the row to retry_pending.
	for attempt := 1; attempt < cfg.MaxAttempts; attempt++ {
		if _, err := tr.RunCycle(ctx); err != nil {
			t.Fatalf("RunCycle %d: %v", attempt, err)
		}
		if got := store.stateOf("acme", 1); got != audit.OCSFRetryPending {
			t.Errorf("after attempt %d state = %q, want retry_pending", attempt, got)
		}
	}
	// The final attempt dead-letters the row.
	if _, err := tr.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle final: %v", err)
	}
	if got := store.stateOf("acme", 1); got != audit.OCSFDeadLettered {
		t.Errorf("final state = %q, want dead_lettered", got)
	}
	// The translation-failure receipt is an OCSF class 2004 finding.
	if len(sink.records) != 1 {
		t.Fatalf("sink received %d records, want 1 dead-letter receipt", len(sink.records))
	}
	if sink.records[0].ClassUID != ocsf.ClassAppSecurityFinding {
		t.Errorf("dead-letter receipt class = %d, want 2004 (Application Security Finding)", sink.records[0].ClassUID)
	}
	if sink.records[0].Finding == nil {
		t.Error("dead-letter receipt must carry a finding block")
	}

	// A row that translates cleanly transitions straight to succeeded.
	store.add("acme", 2, "session.created")
	if _, err := tr.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle for the good row: %v", err)
	}
	if got := store.stateOf("acme", 2); got != audit.OCSFSucceeded {
		t.Errorf("good row state = %q, want succeeded", got)
	}
}

// spec: 11.7
// diagnosis: §11.7 pins the OCSF wire version at v1.1.0 and advertises
// it in every record via metadata.version. The version constant is a
// deployer-observable contract; a silent bump breaks every SIEM
// consumer, so the test pins it.
func TestOCSFSchemaVersionPin(t *testing.T) {
	if ocsf.Version != "1.1.0" {
		t.Fatalf("OCSF wire version = %q, want 1.1.0 (§11.7 pin)", ocsf.Version)
	}
	rec, err := ocsf.Translate(ocsf.Input{
		ID: "id-v", Sequence: 1, TenantID: "acme", EventType: "session.created",
		Payload: json.RawMessage(`{}`), ChainIntegrity: audit.ChainVerified,
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if rec.Metadata.Version != "1.1.0" {
		t.Errorf("metadata.version = %q, want 1.1.0 advertised on every record", rec.Metadata.Version)
	}
}

// stateStore is an in-memory ocsf.TranslationStore for the retry test.
type stateStore struct {
	rows map[string]*rowState
}

type rowState struct {
	row ocsf.TranslatableRow
}

func (s *stateStore) add(tenant string, seq uint64, eventType string) {
	key := tenant + ":" + string(rune('0'+seq))
	s.rows[key] = &rowState{row: ocsf.TranslatableRow{
		Input: ocsf.Input{
			ID: "id-" + key, Sequence: seq, TenantID: tenant,
			EventType: eventType, Payload: json.RawMessage(`{}`),
		},
		Topic: "session_lifecycle",
		State: audit.OCSFPending,
	}}
}

func (s *stateStore) key(tenant string, seq uint64) string {
	return tenant + ":" + string(rune('0'+seq))
}

func (s *stateStore) stateOf(tenant string, seq uint64) audit.OCSFTranslationState {
	if r, ok := s.rows[s.key(tenant, seq)]; ok {
		return r.row.State
	}
	return ""
}

func (s *stateStore) PendingTranslation(_ context.Context, limit int) ([]ocsf.TranslatableRow, error) {
	var out []ocsf.TranslatableRow
	for _, r := range s.rows {
		if r.row.State == audit.OCSFPending || r.row.State == audit.OCSFRetryPending {
			out = append(out, r.row)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *stateStore) SetTranslationState(_ context.Context, tenantID string, seq uint64,
	state audit.OCSFTranslationState, retryCount int,
) error {
	r, ok := s.rows[s.key(tenantID, seq)]
	if !ok {
		return errors.New("stateStore: row not found")
	}
	r.row.State = state
	r.row.RetryCount = retryCount
	return nil
}

// capturingSink records every delivered OCSF record.
type capturingSink struct {
	records []ocsf.Record
}

func (s *capturingSink) Deliver(_ context.Context, _, _ string, rec ocsf.Record) error {
	s.records = append(s.records, rec)
	return nil
}
