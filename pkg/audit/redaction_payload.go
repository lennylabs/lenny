// SPDX-License-Identifier: MIT

package audit

import (
	"encoding/json"
	"time"
)

// RedactedPayloadMarker is the key the §12.8 step-14 in-place dead-letter
// redaction sets on a scrubbed row's payload. The §11.7 periodic integrity
// check and the chain verifier key on it to tell an authorized GDPR
// redaction (which carries a signature-bearing RedactionReceipt) apart
// from a tamper that merely cleared the payload.
//
// spec: §12.8 line 810.
const RedactedPayloadMarker = "redacted"

// RedactedPayload is the §12.8 line 810 shape the dead-letter redaction
// rewrites a scrubbed audit_log row's payload to. The raw canonical PII
// fields are dropped; the fields the spec preserves for pipeline-failure
// forensics (the OCSF translation error class and the original event type)
// are carried through verbatim. The `redacted` / `user_id_redacted_from_row`
// flags record that the rewrite happened so a chain verifier and the
// §11.7 integrity check can locate the row and demand a RedactionReceipt.
//
// spec: §12.8 line 810 — `{"redacted": true, "redacted_at": "<timestamp>",
// "user_id_redacted_from_row": true, "error_class": "<preserved>",
// "event_type": "<preserved>"}`.
type RedactedPayload struct {
	Redacted              bool   `json:"redacted"`
	RedactedAt            string `json:"redacted_at"`
	UserIDRedactedFromRow bool   `json:"user_id_redacted_from_row"`
	ErrorClass            string `json:"error_class"`
	EventType             string `json:"event_type"`
}

// BuildRedactedPayload returns the §12.8 line 810 redacted payload for a
// dead-lettered row, preserving the original event type and OCSF
// translation error class (the failure forensics the spec keeps) while
// dropping every PII-bearing field. redactedAt is the instant the rewrite
// commits, mirrored onto the gdpr.erasure_deadletter_redacted event.
//
// spec: §12.8 line 810.
func BuildRedactedPayload(originalEventType, originalErrorClass string, redactedAt time.Time) json.RawMessage {
	b, _ := json.Marshal(RedactedPayload{
		Redacted:              true,
		RedactedAt:            redactedAt.UTC().Format(time.RFC3339Nano),
		UserIDRedactedFromRow: true,
		ErrorClass:            originalErrorClass,
		EventType:             originalEventType,
	})
	return b
}

// IsRedactedPayload reports whether payload carries the §12.8 in-place
// redaction marker (`"redacted": true`). The chain verifier and the
// auditstore row scanner use it to decide whether a content-hash break is
// a lawful GDPR redaction (which a RedactionReceipt then authenticates) or
// an unexplained tamper.
//
// spec: §12.8 line 810.
func IsRedactedPayload(payload json.RawMessage) bool {
	if len(payload) == 0 {
		return false
	}
	var probe struct {
		Redacted bool `json:"redacted"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return false
	}
	return probe.Redacted
}
