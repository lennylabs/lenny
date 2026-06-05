// SPDX-License-Identifier: MIT

// Package task models the §8.8 TaskRecord and TaskResult envelopes —
// the durable, protocol-bridging contract for a delegated task. The
// types here are the canonical Go representation of the §8.8 JSON
// schemas (TaskRecord, TaskResult) and the §15.4.1 OutputPart content
// envelope. The gateway projects a TaskRecord on read (from the session
// row plus its transcript) and writes a TaskResult into the §8.10 tree
// archive when a child settles; both routes go through these types so
// the wire envelope is identical across surfaces.
//
// The package depends on nothing outside the standard library so the
// domain model stays free of gateway-layer dependencies. Projection
// (row + transcript → Record) and error classification live in the
// gateway, where the stores and the §15.2.1 classifier are available.
//
// spec: §8.8 lines 804-940 (TaskRecord / TaskResult); §15.4.1 lines
// 1479-1540 (OutputPart).
package task

import (
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

// OutputPart is the §15.4.1 internal content envelope. It carries its
// own per-type SchemaVersion, independent of the enclosing TaskRecord
// envelope version: the two version axes evolve separately per §8.8
// line 825. v1 producers emit `text` parts; the full canonical type
// registry (code, image, diff, file, execution_result, error, ...) is
// an open string per §15.4.1.
// spec: §15.4.1 lines 1483-1540.
type OutputPart struct {
	SchemaVersion int            `json:"schemaVersion"`
	ID            string         `json:"id,omitempty"`
	Type          string         `json:"type"`
	MimeType      string         `json:"mimeType,omitempty"`
	Inline        string         `json:"inline,omitempty"`
	Ref           string         `json:"ref,omitempty"`
	Annotations   map[string]any `json:"annotations,omitempty"`
	Parts         []OutputPart   `json:"parts,omitempty"`
	Status        string         `json:"status,omitempty"`
}

// TextPart builds a §15.4.1 `text` OutputPart carrying inline content.
// It is the projection used when the source is a plain-text transcript
// entry; a runtime that emits richer parts (image, file ref) populates
// OutputPart directly.
// spec: §15.4.1 lines 1530-1531 — `text` guarantees type, inline,
// mimeType (text/plain).
func TextPart(content string) OutputPart {
	return OutputPart{
		SchemaVersion: SchemaVersion,
		Type:          "text",
		MimeType:      "text/plain",
		Inline:        content,
		Status:        "complete",
	}
}

// Message is one entry in a §8.8 TaskRecord messages array. State is
// set only on agent entries that have reached a terminal task state.
// spec: §8.8 lines 810-817.
type Message struct {
	Role  string       `json:"role"`
	Parts []OutputPart `json:"parts"`
	State string       `json:"state,omitempty"`
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
	Parts        []OutputPart `json:"parts"`
	ArtifactRefs []string     `json:"artifactRefs"`
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
