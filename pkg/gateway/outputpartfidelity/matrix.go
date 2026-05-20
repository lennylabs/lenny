// SPDX-License-Identifier: MIT

// Package outputpartfidelity encodes the §15.4.1 Translation Fidelity
// Matrix and ships per-OutputPart translators for the OpenAI Chat
// Completions and Open Responses wire forms. The package is the data
// model behind the §12.10 fidelity-matrix conformance test: it lets a
// table-driven test assert that the documented per-field fidelity
// classification matches the actual translator behavior.
//
// The matrix covers two external-protocol adapters in v1:
//
//   - OpenAI Chat Completions (`POST /v1/chat/completions`).
//   - Open Responses (`POST /v1/responses`), which is the dialect
//     the OpenAI Responses API also serves.
//
// MCP, REST, and A2A fidelity rows live in §15.4.1 but are out of
// scope for the v1 conformance test surface: REST is `[exact]` on every
// field by construction; A2A is post-V1; MCP is exercised by separate
// tier-3 contract suites against the MCP fabric.
package outputpartfidelity

// FidelityTag is the §15.4.1 classification of how a single
// `OutputPart` field survives translation to an external wire format.
type FidelityTag string

const (
	// TagExact reports that the field round-trips with no loss.
	TagExact FidelityTag = "exact"
	// TagLossy reports that the field is representable in the target
	// protocol but some information is lost or transformed; the
	// original value cannot be fully reconstructed from the wire form
	// alone.
	TagLossy FidelityTag = "lossy"
	// TagDropped reports that the field has no representation in the
	// target protocol and is not present on the wire. A round-trip
	// ingests the field back with the field's zero value.
	TagDropped FidelityTag = "dropped"
	// TagUnsupported reports that the field semantics are
	// fundamentally incompatible with the target protocol. No mapping
	// attempt is made.
	TagUnsupported FidelityTag = "unsupported"
	// TagExtended reports that the field is carried in a protocol
	// extension (annotation, metadata, sidecar) that conformant
	// clients MAY ignore; the value is recoverable on ingest by a
	// Lenny-aware consumer.
	TagExtended FidelityTag = "extended"
)

// Adapter names an external-protocol adapter row in the matrix.
type Adapter string

const (
	// AdapterOpenAICompletions is the §15.1 POST /v1/chat/completions
	// translator (OpenAI Chat Completions dialect).
	AdapterOpenAICompletions Adapter = "openai_completions"
	// AdapterOpenResponses is the §15.1 POST /v1/responses translator
	// (Open Responses Specification, also served as the OpenAI
	// Responses API by §15.1 superset note).
	AdapterOpenResponses Adapter = "open_responses"
)

// Field names an OutputPart field row in the matrix.
type Field string

const (
	FieldSchemaVersion Field = "schemaVersion"
	FieldID            Field = "id"
	FieldType          Field = "type"
	FieldMimeType      Field = "mimeType"
	FieldInline        Field = "inline"
	FieldRef           Field = "ref"
	FieldAnnotations   Field = "annotations"
	FieldParts         Field = "parts"
	FieldStatus        Field = "status"
	FieldProtocolHints Field = "protocolHints"
)

// Matrix returns the §15.4.1 fidelity tag for the (adapter, field)
// pair. The map is hand-transcribed from the spec table and is the
// single source of truth for the §12.10 conformance assertion.
//
// A missing (adapter, field) returns ("", false). Callers MUST treat
// the boolean as the existence check.
func Matrix(a Adapter, f Field) (FidelityTag, bool) {
	row, ok := matrixData[a]
	if !ok {
		return "", false
	}
	tag, ok := row[f]
	return tag, ok
}

// Adapters returns every adapter row covered by the matrix.
func Adapters() []Adapter {
	return []Adapter{AdapterOpenAICompletions, AdapterOpenResponses}
}

// Fields returns every OutputPart field row covered by the matrix in
// the same order the spec lists them.
func Fields() []Field {
	return []Field{
		FieldSchemaVersion,
		FieldID,
		FieldType,
		FieldMimeType,
		FieldInline,
		FieldRef,
		FieldAnnotations,
		FieldParts,
		FieldStatus,
		FieldProtocolHints,
	}
}

// matrixData is the per-(adapter, field) fidelity table from §15.4.1
// "Translation Fidelity Matrix".
var matrixData = map[Adapter]map[Field]FidelityTag{
	AdapterOpenAICompletions: {
		// Chat Completions has no version field. Round-trip
		// asymmetric: version information permanently lost.
		FieldSchemaVersion: TagDropped,
		// No per-content-block ID in Chat Completions.
		FieldID: TagDropped,
		// Everything becomes `text` or `image_url`; custom types
		// and reasoning_trace collapsed to `text` with no type
		// recovery.
		FieldType: TagLossy,
		// Only image/* MIME types preserved via image_url; all
		// other MIME types dropped.
		FieldMimeType: TagLossy,
		// Carried as content string or base64 for images.
		FieldInline: TagExact,
		// No URI reference in Chat Completions; adapter resolves
		// `ref` to inline before sending.
		FieldRef: TagDropped,
		// No annotation mechanism in Chat Completions.
		FieldAnnotations: TagDropped,
		// Flattened to sequential content entries; nesting
		// structure not recoverable on round-trip.
		FieldParts: TagDropped,
		// Chat Completions has `finish_reason` only at message
		// level; per-part status not representable.
		FieldStatus: TagDropped,
		// Consumed by the adapter before serialization;
		// intentionally not sent on wire.
		FieldProtocolHints: TagDropped,
	},
	AdapterOpenResponses: {
		// Responses API output items carry no schema version
		// field; re-added with default on ingest.
		FieldSchemaVersion: TagDropped,
		// Mapped to Responses API `output[].id`; preserved on
		// outbound and recoverable on inbound for top-level
		// output items.
		FieldID: TagExtended,
		// Text, image, and file output types map natively;
		// reasoning_trace mapped to output_text with a reasoning
		// role annotation. Custom types not in the registry
		// collapse to output_text with no type recovery on
		// inbound.
		FieldType: TagLossy,
		// image/* and well-known file MIME types preserved via
		// output_image and file output items; uncommon MIME types
		// collapsed to generic file output with no MIME recovery.
		FieldMimeType: TagLossy,
		// Carried as text or base64-encoded image content.
		FieldInline: TagExact,
		// No `lenny-blob://` URI in Responses API; adapter
		// resolves `ref` to inline before sending.
		FieldRef: TagDropped,
		// No per-output annotation mechanism in the Open Responses
		// Specification.
		FieldAnnotations: TagDropped,
		// Responses API output items are flat; nesting structure
		// not representable.
		FieldParts: TagDropped,
		// `failed` status maps to an output item with
		// status:"failed"; streaming and complete distinctions
		// partially recoverable via SSE streaming events.
		FieldStatus: TagLossy,
		// Consumed by the adapter before serialization.
		FieldProtocolHints: TagDropped,
	},
}
