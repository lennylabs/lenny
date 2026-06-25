// SPDX-License-Identifier: MIT

// Package degradation exports the §15.5 degradation-annotation catalog
// and the helpers that stamp annotations onto MessageEnvelope and
// MessagePart wire frames.
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
// `annotations` field on a MessageEnvelope or MessagePart. The keys
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

	// AnnotationBlobRefUnresolvable — a consumer could not dereference an
	// MessagePart `ref` (blob expired, storage unavailable, network
	// partition). It is a §15.4.1 MessageEnvelope annotation distinct from
	// the §15.5 schemaVersion family. spec: §15.4.1 lines 1575-1579.
	AnnotationBlobRefUnresolvable = "blob_ref_unresolvable"

	// AnnotationUnregisteredPlatformType — an unprefixed MessagePart `type`
	// not in the v1 registry was passed through with the custom-type
	// fallback. It is a §15.4.1 ingress warning carried on the part.
	// spec: §15.4.1 lines 1503, 1522.
	AnnotationUnregisteredPlatformType = "unregistered_platform_type"
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

// BlobRefUnresolvable builds the §15.4.1 `blob_ref_unresolvable`
// annotation body for a consumer that encountered a MessagePart `ref` it
// could not dereference. `partID` is the unresolvable part's id, `ref`
// the `lenny-blob://` reference, and `reason` the failure detail (blob
// expired, storage unavailable, network partition). The consumer also
// substitutes a placeholder `error` part and never silently drops the
// part.
//
// The returned map is suitable for `Annotations[AnnotationBlobRefUnresolvable]`.
// spec: §15.4.1 lines 1575-1579.
func BlobRefUnresolvable(partID, ref, reason string) map[string]any {
	return map[string]any{
		"partId": partID,
		"ref":    ref,
		"reason": reason,
	}
}

// UnregisteredPlatformType builds the §15.4.1 `unregistered_platform_type`
// warning body for an unprefixed MessagePart `type` the gateway did not
// find in the v1 registry. `typ` is the unrecognized type string the
// runtime emitted; the gateway records it before applying the custom-type
// fallback (collapse to `text` with `annotations.originalType`).
//
// The returned map is suitable for `Annotations[AnnotationUnregisteredPlatformType]`.
// spec: §15.4.1 lines 1503, 1522.
func UnregisteredPlatformType(typ string) map[string]any {
	return map[string]any{
		"type": typ,
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
