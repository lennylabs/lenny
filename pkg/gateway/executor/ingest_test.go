// SPDX-License-Identifier: MIT

package executor

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/degradation"
)

// spec: §15.4.1 lines 1503, 1522 — an unregistered unprefixed type is
// collapsed to `text`, the original is preserved in
// `annotations.originalType`, and an `unregistered_platform_type` warning
// rides on the part (MED-018).
func TestIngestUnregisteredPlatformType_spec_15_4_1_1522(t *testing.T) {
	env := responseEnvelope{
		Type:   "response",
		Output: []wireMessagePart{{Type: "heatmap", Inline: "<<grid>>"}},
	}
	parts, envAnn := ingestResponse(env)
	if envAnn != nil {
		t.Errorf("an unregistered type is a part-level warning, not an envelope annotation: %v", envAnn)
	}
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(parts))
	}
	p := parts[0]
	if p.Type != "text" {
		t.Errorf("type = %q, want collapse to text", p.Type)
	}
	if p.Text != "<<grid>>" {
		t.Errorf("inline payload not carried as text: %q", p.Text)
	}
	if p.Annotations["originalType"] != "heatmap" {
		t.Errorf("originalType = %v, want heatmap", p.Annotations["originalType"])
	}
	if !degradation.Has(p.Annotations, degradation.AnnotationUnregisteredPlatformType) {
		t.Error("missing unregistered_platform_type warning annotation")
	}
}

// spec: §15.4.1 line 1522 — a vendor-namespaced custom type still falls
// back to text with originalType, but does NOT earn the warning: it is a
// deliberate extension, not an unrecognized platform type.
func TestIngestVendorNamespacedType_spec_15_4_1_1522(t *testing.T) {
	env := responseEnvelope{
		Type:   "response",
		Output: []wireMessagePart{{Type: "x-acme/heatmap", Inline: "v"}},
	}
	parts, _ := ingestResponse(env)
	p := parts[0]
	if p.Type != "text" {
		t.Errorf("vendor type should collapse to text, got %q", p.Type)
	}
	if p.Annotations["originalType"] != "x-acme/heatmap" {
		t.Errorf("originalType = %v", p.Annotations["originalType"])
	}
	if degradation.Has(p.Annotations, degradation.AnnotationUnregisteredPlatformType) {
		t.Error("vendor-namespaced type must not warn unregistered_platform_type")
	}
}

// spec: §15.4.1 — a canonical type passes through untouched with no
// annotations.
func TestIngestCanonicalTypeUntouched_spec_15_4_1(t *testing.T) {
	env := responseEnvelope{
		Type:   "response",
		Output: []wireMessagePart{{Type: "code", Inline: "print(1)"}},
	}
	parts, envAnn := ingestResponse(env)
	if envAnn != nil {
		t.Errorf("canonical part raised an envelope annotation: %v", envAnn)
	}
	if parts[0].Type != "code" {
		t.Errorf("canonical type mutated to %q", parts[0].Type)
	}
	if parts[0].Annotations != nil {
		t.Errorf("canonical part gained annotations: %v", parts[0].Annotations)
	}
	if parts[0].SchemaVersion != 1 {
		t.Errorf("missing schemaVersion not defaulted to 1: %d", parts[0].SchemaVersion)
	}
}

// spec: §15.4.1 lines 1499-1501 — a part stamped a schemaVersion ahead of
// the gateway's known max raises a `schema_version_ahead` annotation on
// the enclosing envelope, carrying knownVersion + encounteredVersion. The
// part itself is still forward-read (MED-017).
func TestIngestSchemaVersionAhead_spec_15_4_1_1501(t *testing.T) {
	env := responseEnvelope{
		Type: "response",
		Output: []wireMessagePart{
			{Type: "text", Inline: "a", SchemaVersion: 1},
			{Type: "text", Inline: "b", SchemaVersion: 4},
		},
	}
	parts, envAnn := ingestResponse(env)
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 (both forward-read)", len(parts))
	}
	body, ok := envAnn[degradation.AnnotationSchemaVersionAhead].(map[string]any)
	if !ok {
		t.Fatalf("missing schema_version_ahead envelope annotation: %v", envAnn)
	}
	if body["knownVersion"] != 1 {
		t.Errorf("knownVersion = %v, want 1", body["knownVersion"])
	}
	if body["encounteredVersion"] != 4 {
		t.Errorf("encounteredVersion = %v, want the highest ahead version 4", body["encounteredVersion"])
	}
}

// spec: §15.4.1 — the Basic-level `{type:"response", text:"..."}`
// shorthand ingests as a single v1 text part with no degradation.
func TestIngestResponseTextShorthand_spec_15_4_1(t *testing.T) {
	parts, envAnn := ingestResponse(responseEnvelope{Type: "response", Text: "hi"})
	if envAnn != nil || len(parts) != 1 || parts[0].Type != "text" || parts[0].Text != "hi" {
		t.Errorf("shorthand ingest = %+v / %v", parts, envAnn)
	}
	if parts[0].SchemaVersion != 1 {
		t.Errorf("shorthand schemaVersion = %d, want 1", parts[0].SchemaVersion)
	}
}
