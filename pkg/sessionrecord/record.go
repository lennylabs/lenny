// SPDX-License-Identifier: MIT

// Package sessionrecord models the §8.8 TaskRecord and TaskResult
// envelopes: the durable, protocol-bridging contract for a delegated
// task. The types here are the canonical Go representation of the §8.8
// JSON schemas (TaskRecord, TaskResult) and the §15.4.1 MessagePart
// content envelope. The gateway projects a TaskRecord on read (from the
// session row plus its transcript) and writes a TaskResult into the
// §8.10 tree archive when a child settles; both routes go through these
// types so the wire envelope is identical across surfaces. The package
// name carries Lenny's own session vocabulary; the exported type names
// (Record, Result) and the JSON wire tags (taskId, etc.) keep the
// external-protocol Task vocabulary the §8.8 schemas define.
//
// The package depends on nothing outside the standard library so the
// domain model stays free of gateway-layer dependencies. Projection
// (row + transcript → Record) and error classification live in the
// gateway, where the stores and the §15.2.1 classifier are available.
//
// spec: §8.8 lines 804-940 (TaskRecord / TaskResult); §15.4.1 lines
// 1479-1540 (MessagePart).
package sessionrecord

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SchemaVersion is the §8.8 TaskRecord / TaskResult envelope schema
// version. It is the single producer constant every writer stamps, so
// the envelope version has one source of truth rather than a literal
// `1` scattered across producers. Per §8.8 (and §15.5 item 7) the value
// is immutable once a record is created and envelope schema changes are
// additive-only; ReconcileSchemaVersion enforces the immutability rule
// on any read-modify-write path.
// spec: §8.8 lines 806, 825-827; §15.5 item 7.
const SchemaVersion = 1

// Message-entry roles on a §8.8 TaskRecord. The messages array uses
// caller/agent, distinct from the §7.2 transcript's user/assistant/
// system roles; the gateway maps transcript roles onto these at
// projection time.
// spec: §8.8 lines 810-817.
const (
	RoleCaller = "caller"
	RoleAgent  = "agent"
)

// MessagePart is the §15.4.1 internal content envelope. It carries its
// own per-type SchemaVersion, independent of the enclosing TaskRecord
// envelope version: the two version axes evolve separately per §8.8
// line 825. v1 producers emit `text` parts; the full canonical type
// registry (code, image, diff, file, execution_result, error, ...) is
// an open string per §15.4.1.
// spec: §15.4.1 lines 1483-1540.
type MessagePart struct {
	SchemaVersion int            `json:"schemaVersion"`
	ID            string         `json:"id,omitempty"`
	Type          string         `json:"type"`
	MimeType      string         `json:"mimeType,omitempty"`
	Inline        string         `json:"inline,omitempty"`
	Ref           string         `json:"ref,omitempty"`
	Annotations   map[string]any `json:"annotations,omitempty"`
	Parts         []MessagePart  `json:"parts,omitempty"`
	Status        string         `json:"status,omitempty"`
}

// TextPart builds a §15.4.1 `text` MessagePart carrying inline content.
// It is the projection used when the source is a plain-text transcript
// entry; a runtime that emits richer parts (image, file ref) populates
// MessagePart directly.
// spec: §15.4.1 lines 1530-1531 — `text` guarantees type, inline,
// mimeType (text/plain).
func TextPart(content string) MessagePart {
	return MessagePart{
		SchemaVersion: SchemaVersion,
		Type:          "text",
		MimeType:      "text/plain",
		Inline:        content,
		Status:        "complete",
	}
}

// MessageContent is the §15.4 `MessageEnvelope.input` union: a message's
// inbound content is either a bare string or a §15.4.1 `MessagePart[]`
// array. The two forms are one contract — §15.4 binds `MessageEnvelope`
// identically across the stdin binary protocol, the platform MCP server
// tools, and every external API, so the REST `/messages` endpoint and the
// MCP `lenny/send_message` tool accept the same union. A bare string is
// sugar for a single `text` part: it unmarshals to one MessagePart via
// TextPart, so a structured consumer always reads `Parts` and never has to
// branch on which wire form arrived.
//
// The zero value is empty (no parts). UnmarshalJSON accepts the bare
// string (`"hi"`), the part array (`[{"type":"text","inline":"hi"}]`), or
// JSON null (empty); any other JSON shape is a validation error.
// MarshalJSON round-trips the form that was unmarshalled (bare string when
// the input was a bare string, array otherwise) so a buffered message
// re-delivered from the inbox carries the original wire form.
//
// spec: §15.4 (MessageEnvelope.input oneOf(string, MessagePart[])),
// §15.2.1 (REST/MCP parity).
type MessageContent struct {
	// parts is the canonical §15.4.1 MessagePart array. A bare-string
	// input is normalized to a single text part here at unmarshal time.
	parts []MessagePart
	// wasString records that the wire input arrived as a bare string, so
	// MarshalJSON re-emits the bare-string sugar rather than the array.
	wasString bool
}

// MessageContentJSONSchema is the §15.4 `MessageEnvelope.input` union
// expressed as a JSON Schema fragment: `oneOf(string, MessagePart[])`. It
// is defined once here so the REST OpenAPI schema and the MCP
// `lenny/send_message` `inputSchema` express the identical union and the
// two surfaces cannot drift. The MessagePart branch lists the §15.4.1
// part fields; `type` is the only required field per the §15.4.1 part
// contract. A `oneOf` is valid in an MCP tool input schema, so the union
// is MCP-compliant. spec: §15.4 (MessageEnvelope.input), §15.2.1 (REST/MCP
// parity).
const MessageContentJSONSchema = `{"oneOf":[{"type":"string","description":"§15.4 bare-string shorthand: sugar for a single text MessagePart."},{"type":"array","description":"§15.4.1 MessagePart[] structured content.","items":{"type":"object","required":["type"],"properties":{"type":{"type":"string"},"mimeType":{"type":"string"},"inline":{"type":"string"},"ref":{"type":"string"},"schemaVersion":{"type":"integer"},"annotations":{"type":"object"}}}}]}`

// MessageContentFromText builds a MessageContent carrying a single text
// part from a plain string. It is the constructor the REST and MCP send
// paths use when the only content is text, and the form a buffered message
// re-delivers as a bare string.
// spec: §15.4 (bare string is a single text MessagePart).
func MessageContentFromText(s string) MessageContent {
	return MessageContent{parts: []MessagePart{TextPart(s)}, wasString: true}
}

// MessageContentFromParts builds a MessageContent from an explicit
// §15.4.1 MessagePart array.
// spec: §15.4 (MessageEnvelope.input MessagePart[]).
func MessageContentFromParts(parts []MessagePart) MessageContent {
	return MessageContent{parts: parts}
}

// Parts returns the canonical §15.4.1 MessagePart array. A bare-string
// input is already normalized to a single text part, so a consumer reads
// Parts uniformly regardless of which wire form arrived.
func (m MessageContent) Parts() []MessagePart { return m.parts }

// Text projects the content to its plain-text form: the concatenation of
// every `text` part's inline content. It is the projection the gateway's
// text-only delivery, transcript, and interceptor paths consume until they
// carry the full multipart envelope. A non-text part contributes no text.
// spec: §15.4.1 (text part inline content).
func (m MessageContent) Text() string {
	if len(m.parts) == 1 && m.parts[0].Type == "text" {
		return m.parts[0].Inline
	}
	var b strings.Builder
	for _, p := range m.parts {
		if p.Type == "text" {
			b.WriteString(p.Inline)
		}
	}
	return b.String()
}

// IsEmpty reports whether the content carries no parts.
func (m MessageContent) IsEmpty() bool { return len(m.parts) == 0 }

// UnmarshalJSON decodes the §15.4 oneOf(string, MessagePart[]) union: a
// bare JSON string becomes a single text part, a JSON array decodes to the
// MessagePart slice, and JSON null is the empty value. Any other JSON shape
// (object, number, bool) is a validation error so a malformed body is
// rejected rather than silently coerced. spec: §15.4 (MessageEnvelope.input).
func (m *MessageContent) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	switch {
	case trimmed == "" || trimmed == "null":
		m.parts = nil
		m.wasString = false
		return nil
	case trimmed[0] == '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("message content string: %w", err)
		}
		m.parts = []MessagePart{TextPart(s)}
		m.wasString = true
		return nil
	case trimmed[0] == '[':
		var parts []MessagePart
		if err := json.Unmarshal(data, &parts); err != nil {
			return fmt.Errorf("message content parts: %w", err)
		}
		m.parts = parts
		m.wasString = false
		return nil
	default:
		return fmt.Errorf("message content must be a string or a MessagePart array, got %.16s", trimmed)
	}
}

// MarshalJSON re-emits the union form that was unmarshalled: a bare-string
// input (or a MessageContentFromText value) marshals back to a JSON string,
// and a part-array input marshals to the MessagePart array. Round-tripping
// the original form keeps a buffered message's re-delivered wire body
// byte-stable on the bare-string path. spec: §15.4 (MessageEnvelope.input).
func (m MessageContent) MarshalJSON() ([]byte, error) {
	if m.wasString && len(m.parts) == 1 && m.parts[0].Type == "text" {
		return json.Marshal(m.parts[0].Inline)
	}
	if m.parts == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(m.parts)
}

// Message is one entry in a §8.8 TaskRecord messages array. State is
// set only on agent entries that have reached a terminal task state.
// spec: §8.8 lines 810-817.
type Message struct {
	Role  string        `json:"role"`
	Parts []MessagePart `json:"parts"`
	State string        `json:"state,omitempty"`
}

// Usage is the §8.8 per-task usage rollup.
// spec: §8.8 lines 897-903.
type Usage struct {
	InputTokens            int64   `json:"inputTokens"`
	OutputTokens           int64   `json:"outputTokens"`
	WallClockSeconds       float64 `json:"wallClockSeconds"`
	PodMinutes             float64 `json:"podMinutes"`
	CredentialLeaseMinutes float64 `json:"credentialLeaseMinutes"`
}

// TreeUsage is the §8.8 task-tree usage rollup: this task's usage plus
// every descendant's. It is populated by the gateway only after all
// descendants settle; the producer surfaces a nil *TreeUsage (JSON
// null) for an in-progress task or one with unsettled descendants.
// spec: §8.8 lines 904-917.
type TreeUsage struct {
	Usage
	TotalTasks int64 `json:"totalTasks"`
}

// Record is the §8.8 TaskRecord envelope: the durable, protocol-
// bridging record for a task. Usage and TreeUsage are pointers so an
// unpopulated rollup serializes as absent / null rather than a zeroed
// object.
// spec: §8.8 lines 806-823.
type Record struct {
	SchemaVersion int        `json:"schemaVersion"`
	TaskID        string     `json:"taskId"`
	SessionID     string     `json:"sessionId,omitempty"`
	State         string     `json:"state"`
	Messages      []Message  `json:"messages"`
	Usage         *Usage     `json:"usage,omitempty"`
	TreeUsage     *TreeUsage `json:"treeUsage,omitempty"`
}

// Result is the §8.8 TaskResult lenny/await_children returns and the
// §8.10 tree archive persists for a settled child. Output is present on
// a completed task and nil (JSON-absent) on a failed/cancelled/expired
// one; Error is the inverse.
// spec: §8.8 lines 885-940.
type Result struct {
	SchemaVersion int        `json:"schemaVersion"`
	TaskID        string     `json:"taskId"`
	State         string     `json:"state"`
	Output        *Output    `json:"output,omitempty"`
	Usage         *Usage     `json:"usage,omitempty"`
	TreeUsage     *TreeUsage `json:"treeUsage,omitempty"`
	Error         *Error     `json:"error,omitempty"`
}

// Output is the §8.8 TaskResult.output block: the child's emitted parts
// plus any lenny-blob:// artifact references. Both arrays are always
// present (possibly empty) when Output itself is set.
// spec: §8.8 lines 888-891.
type Output struct {
	Parts        []MessagePart `json:"parts"`
	ArtifactRefs []string      `json:"artifactRefs"`
}

// Error is the §8.8 TaskResult.error block. Category is the §15.2.1 /
// §16.3 error taxonomy value the gateway classifier assigns to Code;
// RetriesExhausted reports whether the gateway exhausted its automatic
// recovery budget before declaring the terminal failure.
// spec: §8.8 lines 922-940 (failure example: code, category, message,
// retriesExhausted).
type Error struct {
	Code             string `json:"code"`
	Category         string `json:"category,omitempty"`
	Message          string `json:"message"`
	RetriesExhausted bool   `json:"retriesExhausted,omitempty"`
}

// Error implements the error interface so a synthesized failure block
// (e.g. RuntimeCrash) can be returned through a Go error value and
// recovered with errors.As. The string form is the code and message,
// which is what a log line or a client-facing wrapper renders.
func (e *Error) Error() string {
	if e == nil {
		return "<nil task error>"
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// maxCrashStderrBytes bounds the stderr tail folded into a RUNTIME_CRASH
// message so a runtime that died mid-stack-dump does not produce a
// multi-megabyte error block. The tail is the most diagnostic part of a
// crash, so the head is dropped when the capture exceeds the cap.
const maxCrashStderrBytes = 4096

// RuntimeCrash synthesizes the §15.4.1 RUNTIME_CRASH error block from a
// non-zero runtime exit code and the runtime's captured stderr. The §8.8
// failure taxonomy classifies a runtime crash as TRANSIENT: the gateway
// retries on a fresh pod, and only marks retriesExhausted after the
// pod-crash retry budget is spent (a property the gateway sets later, not
// at synthesis time). The stderr tail is trimmed of trailing whitespace
// and capped to the last maxCrashStderrBytes so the message stays bounded.
// spec: §15.4.1 line 1889 — "When the process exits non-zero without
// emitting a `response`, the adapter synthesizes a `RUNTIME_CRASH` error
// from the exit code and stderr."; §8.8 lines 936-938.
func RuntimeCrash(exitCode int, stderr string) *Error {
	trimmed := strings.TrimRight(stderr, " \t\r\n")
	if len(trimmed) > maxCrashStderrBytes {
		trimmed = trimmed[len(trimmed)-maxCrashStderrBytes:]
	}
	msg := fmt.Sprintf("runtime exited with code %d without emitting a response", exitCode)
	if trimmed != "" {
		msg += ": " + trimmed
	}
	return &Error{
		Code:     "RUNTIME_CRASH",
		Category: "TRANSIENT",
		Message:  msg,
	}
}

// ReconcileSchemaVersion returns the envelope schema version a writer
// must persist given the version already on the record (existing, 0
// when the record is new) and the version this writer would produce.
// An already-written version wins: §8.8 makes the envelope schemaVersion
// immutable once the first writer sets it, so a read-modify-write path —
// including a re-archive of an already-settled node — preserves the
// original version rather than re-deriving it. This is how a rolling
// gateway upgrade where replica B knows schema 2 does not silently
// mutate a schema-1 record replica A created.
// spec: §8.8 lines 825-827; §15.5 item 7.
func ReconcileSchemaVersion(existing, producer int) int {
	if existing > 0 {
		return existing
	}
	return producer
}

// RetriesExhausted reports whether a terminal task exhausted its
// automatic recovery budget, the value the §8.8 TaskResult.error block
// carries as retriesExhausted. When the caller knows the effective
// budget (maxRetries > 0) the report is the precise "consumed the whole
// budget" comparison; when the budget is not available at the call site
// (maxRetries == 0) it falls back to "ran at least one automatic
// recovery attempt", which is the row-only witness that the gateway
// drove the session through the retry path before it terminated.
// spec: §8.8 lines 936-938 (RUNTIME_CRASH → retriesExhausted: true after
// the pod-crash retries are exhausted); §7.3 lines 408-411.
func RetriesExhausted(retryCount int64, maxRetries int) bool {
	if maxRetries > 0 {
		return retryCount >= int64(maxRetries)
	}
	return retryCount > 0
}
