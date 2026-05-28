// SPDX-License-Identifier: MIT

// Package degradation exports the §15.5 degradation-annotation catalog
// and the helpers that stamp annotations onto MessageEnvelope and
// OutputPart wire frames.
//
// The §15.5 line 2461 catalog defines three annotations that observability
// dashboards, SLO alerts, and forward-compat rollout tracking MUST treat
// as separate kinds:
//
//   - schema_version_ahead          — live consumer forward-read of a
//     record whose schemaVersion exceeds the consumer's understanding.
//   - durable_schema_version_ahead  — durable consumer cannot pass
//     unknown fields through to a schema-strict sink; the record is
//     queued for manual review.
//   - mcp_protocol_version_retired  — an active MCP session whose
//     negotiated protocolVersion has been removed is forced to
//     terminate.
//
// Each helper returns a `map[string]any` ready to drop into the
// `annotations` field on a MessageEnvelope or OutputPart. The keys
// (`knownVersion`, `encounteredVersion`, `recordType`, `retiredVersion`,
// `currentVersions`) mirror the spec table verbatim so durable
// consumers reading a persisted envelope see the same key shape the
// gateway emits on the live stream.
//
// spec: §15.5 lines 2461-2469. F-15.5.5.
package degradation

import "sort"

// Annotation key names — these MUST match the catalog in §15.5 line
// 2461 verbatim so dashboards and SLO alerts route events correctly.
const (
	// AnnotationSchemaVersionAhead — live consumer forward-read.
	// Direction: new writer → old reader.
	AnnotationSchemaVersionAhead = "schema_version_ahead"

	// AnnotationDurableSchemaVersionAhead — durable consumer queue.
	// Direction: new writer → old reader (durable).
	AnnotationDurableSchemaVersionAhead = "durable_schema_version_ahead"

	// AnnotationMcpProtocolVersionRetired — MCP retirement defect.
	// Direction: old writer → new reader.
	AnnotationMcpProtocolVersionRetired = "mcp_protocol_version_retired"
)

// SchemaVersionAhead builds the §15.5 `schema_version_ahead` annotation
// body for a live consumer that encountered a record whose
// `schemaVersion` exceeds its known schema. `known` and `encountered`
// are the integer schema revisions documented in §15.5 item 7.
//
// The returned map is suitable for `Annotations[AnnotationSchemaVersionAhead]`.
func SchemaVersionAhead(known, encountered int) map[string]any {
	return map[string]any{
		"knownVersion":       known,
		"encounteredVersion": encountered,
	}
}

// DurableSchemaVersionAhead builds the §15.5
// `durable_schema_version_ahead` annotation body for a durable consumer
// that cannot pass unknown fields through to its sink. `recordType` is
// the canonical record name (`TaskRecord`, `WorkspacePlan`,
// `MessageEnvelope`, `session_messages`, `billing_event`, `audit_event`,
// `checkpoint_metadata`, `session_record`) — durable consumer alerts
// route on this key.
func DurableSchemaVersionAhead(known, encountered int, recordType string) map[string]any {
	return map[string]any{
		"knownVersion":       known,
		"encounteredVersion": encountered,
		"recordType":         recordType,
	}
}

// McpProtocolVersionRetired builds the §15.5
// `mcp_protocol_version_retired` annotation body for an active MCP
// session whose negotiated protocolVersion has been removed from the
// gateway. `retired` is the now-retired protocol version; `current` is
// the list of versions the gateway currently serves (sorted for
// deterministic emission).
func McpProtocolVersionRetired(retired string, current []string) map[string]any {
	sorted := append([]string(nil), current...)
	sort.Strings(sorted)
	return map[string]any{
		"retiredVersion":  retired,
		"currentVersions": sorted,
	}
}

// Stamp merges the supplied annotation under `key` into target. The
// caller passes the envelope/part's `Annotations` map; Stamp returns
// the (possibly newly allocated) map so the caller can re-assign it.
// Stamp panics if `body` is nil to prevent accidental erasure of an
// annotation by a buggy producer.
func Stamp(target map[string]any, key string, body map[string]any) map[string]any {
	if body == nil {
		panic("degradation.Stamp: nil annotation body")
	}
	if target == nil {
		target = map[string]any{}
	}
	target[key] = body
	return target
}

// Has reports whether the annotations map carries `key`. Returns false
// for a nil map.
func Has(annotations map[string]any, key string) bool {
	if annotations == nil {
		return false
	}
	_, ok := annotations[key]
	return ok
}
