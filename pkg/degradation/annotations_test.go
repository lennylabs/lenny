// SPDX-License-Identifier: MIT

package degradation_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/degradation"
)

// spec: §15.5 line 2465 — `schema_version_ahead` carries
// `knownVersion` and `encounteredVersion`. F-15.5.5.
func TestSchemaVersionAheadFieldShape_spec_15_5_2465(t *testing.T) {
	body := degradation.SchemaVersionAhead(1, 3)
	if body["knownVersion"] != 1 {
		t.Errorf("knownVersion = %v, want 1", body["knownVersion"])
	}
	if body["encounteredVersion"] != 3 {
		t.Errorf("encounteredVersion = %v, want 3", body["encounteredVersion"])
	}
	if got := len(body); got != 2 {
		t.Errorf("body has %d fields, want exactly 2 (knownVersion + encounteredVersion)", got)
	}
}

// spec: §15.5 line 2466 — `durable_schema_version_ahead` carries
// `knownVersion`, `encounteredVersion`, and `recordType`. F-15.5.5.
func TestDurableSchemaVersionAheadFieldShape_spec_15_5_2466(t *testing.T) {
	body := degradation.DurableSchemaVersionAhead(2, 4, "TaskRecord")
	if body["knownVersion"] != 2 {
		t.Errorf("knownVersion = %v", body["knownVersion"])
	}
	if body["encounteredVersion"] != 4 {
		t.Errorf("encounteredVersion = %v", body["encounteredVersion"])
	}
	if body["recordType"] != "TaskRecord" {
		t.Errorf("recordType = %v, want TaskRecord", body["recordType"])
	}
}

// spec: §15.5 line 2467 — `mcp_protocol_version_retired` carries
// `retiredVersion` and `currentVersions`; the list MUST be sorted so
// dashboards see a stable identity for the alert. F-15.5.5.
func TestMcpProtocolVersionRetiredFieldShape_spec_15_5_2467(t *testing.T) {
	body := degradation.McpProtocolVersionRetired("2024-11-05", []string{"2025-06-18", "2025-03-26"})
	if body["retiredVersion"] != "2024-11-05" {
		t.Errorf("retiredVersion = %v", body["retiredVersion"])
	}
	cur, _ := body["currentVersions"].([]string)
	want := []string{"2025-03-26", "2025-06-18"}
	if !reflect.DeepEqual(cur, want) {
		t.Errorf("currentVersions = %v, want %v (sorted)", cur, want)
	}
}

// spec: §15.5 line 2469 — annotations MUST NOT be conflated; the three
// keys are distinct. The catalog constants must therefore stay
// non-equal. F-15.5.5.
func TestAnnotationKeysAreDistinct_spec_15_5_2469(t *testing.T) {
	keys := map[string]struct{}{
		degradation.AnnotationSchemaVersionAhead:        {},
		degradation.AnnotationDurableSchemaVersionAhead: {},
		degradation.AnnotationMcpProtocolVersionRetired: {},
	}
	if len(keys) != 3 {
		t.Errorf("annotation keys collapsed to %d unique values", len(keys))
	}
	if degradation.AnnotationSchemaVersionAhead != "schema_version_ahead" {
		t.Errorf("schema_version_ahead key drifted: %q", degradation.AnnotationSchemaVersionAhead)
	}
	if degradation.AnnotationDurableSchemaVersionAhead != "durable_schema_version_ahead" {
		t.Errorf("durable_schema_version_ahead key drifted: %q", degradation.AnnotationDurableSchemaVersionAhead)
	}
	if degradation.AnnotationMcpProtocolVersionRetired != "mcp_protocol_version_retired" {
		t.Errorf("mcp_protocol_version_retired key drifted: %q", degradation.AnnotationMcpProtocolVersionRetired)
	}
}

// spec: §15.5 line 2461 — Stamp returns the (possibly newly allocated)
// annotations map so producers can drop the result back into the
// MessageEnvelope.Annotations field. F-15.5.5.
func TestStampAllocatesNilAndPreservesExisting(t *testing.T) {
	got := degradation.Stamp(nil, degradation.AnnotationSchemaVersionAhead, degradation.SchemaVersionAhead(1, 2))
	if !degradation.Has(got, degradation.AnnotationSchemaVersionAhead) {
		t.Error("Stamp(nil) did not allocate a map")
	}

	got = degradation.Stamp(got, degradation.AnnotationDurableSchemaVersionAhead,
		degradation.DurableSchemaVersionAhead(1, 2, "session_record"))
	if !degradation.Has(got, degradation.AnnotationSchemaVersionAhead) {
		t.Error("Stamp overwrote pre-existing schema_version_ahead annotation")
	}
	if !degradation.Has(got, degradation.AnnotationDurableSchemaVersionAhead) {
		t.Error("Stamp did not write durable_schema_version_ahead annotation")
	}
}

// spec: §15.5 line 2461 — Stamp panics on nil body to surface a
// producer-side bug rather than silently erasing an annotation.
// F-15.5.5.
func TestStampPanicsOnNilBody(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Stamp(nil body) did not panic")
		}
	}()
	degradation.Stamp(nil, degradation.AnnotationSchemaVersionAhead, nil)
}

func TestHasNilSafe(t *testing.T) {
	if degradation.Has(nil, degradation.AnnotationSchemaVersionAhead) {
		t.Error("Has(nil) returned true")
	}
}

// spec: §15.5 line 2461 — the wire shape of an annotation body must
// JSON-round-trip cleanly so downstream durable consumers can decode
// it without manual parsing. F-15.5.5.
func TestAnnotationJSONRoundTrip(t *testing.T) {
	bodies := []map[string]any{
		degradation.SchemaVersionAhead(1, 9),
		degradation.DurableSchemaVersionAhead(1, 9, "TaskRecord"),
		degradation.McpProtocolVersionRetired("2024-11-05", []string{"2025-03-26"}),
	}
	for _, b := range bodies {
		blob, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var back map[string]any
		if err := json.Unmarshal(blob, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(back) == 0 {
			t.Errorf("round-trip lost fields: %s", blob)
		}
	}
}
