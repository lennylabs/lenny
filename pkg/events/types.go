// SPDX-License-Identifier: MIT

// Package events is the neutral operational-event vocabulary every
// subsystem that emits or consumes §16.6 operational events depends on.
// It carries the §25.3 / §12.6 CloudEvents value types (OperationalEvent,
// BufferedEvent, EventFilter, Pagination, BufferedEventPage), the
// EventEmitter sink interface, the §16.6 event-type catalogue, the §25.3
// stable-eventKey keyer and its nonce checkpoint, and the §25.3 emission
// and buffer metric families.
//
// The package is gateway-neutral: it imports only the standard library
// and pkg/observability/metrics, never pkg/gateway. The concrete §25.3
// in-process ring buffer and the local and Redis-stream emitters that
// produce these events live in pkg/gateway/eventbuffer, which imports
// this package; the dependency edge is one-way (eventbuffer -> events).
// spec: §25.3 (operational events), §16.6 (catalogue), §11 (the
// EventEmitter policy sink).
package events

import (
	"encoding/json"
	"strings"
	"time"
)

// EventType is a §16.6 operational-event short name. The CloudEvents
// `type` attribute of an emitted event is "dev.lenny." + EventType.
type EventType string

// CloudEventsType returns the full CloudEvents `type` for the short
// name — the value the OperationalEvent.Type field carries.
func (t EventType) CloudEventsType() string { return cloudEventsPrefix + string(t) }

// OperationalEvent is the §25.3 / §12.6 operational event: a
// CloudEvents v1.0.2 record. The spec illustrates the Go type as
// `type OperationalEvent = cloudevents.Event`; the CloudEvents go-sdk
// is deliberately not vendored (see pkg/gateway/eventbus/cloudevents.go
// for the same decision on the §12.3.7 audit envelope), so the envelope
// is modeled natively here with the exact CloudEvents context-attribute
// contract and marshals to the CloudEvents structured-content JSON wire
// format. spec: §25.3 lines 654-659 / §12.6.
type OperationalEvent struct {
	// ID is the CloudEvents id — the canonical eventKey.
	ID string `json:"id"`

	// Source identifies the emitting context.
	Source string `json:"source,omitempty"`

	// SpecVersion is the CloudEvents spec version.
	SpecVersion string `json:"specversion"`

	// Type is the CloudEvents type, of the form dev.lenny.<short_name>.
	Type string `json:"type"`

	// Subject is the CloudEvents v1.0.2 subject context attribute. It
	// names the resource the event is about — for the §16.6 catalogue
	// this is the canonical resource identifier (e.g. "pool/default-
	// gvisor", "session/abc123", "credential_pool/openai-prod"). Agents
	// filter related events without parsing the payload by matching
	// CloudEvents subject; the buffer's matchFilter honours the §25.5
	// ?resourceType / ?resourceId filters against this field. spec:
	// §16.6 catalogue / CloudEvents v1.0.2 subject context attribute.
	Subject string `json:"subject,omitempty"`

	// Time is the event timestamp.
	Time time.Time `json:"time"`

	// Severity is the Lenny extension attribute the §25.3 event-buffer
	// query filters on.
	Severity string `json:"severity,omitempty"`

	// DataContentType is the media type of Data.
	DataContentType string `json:"datacontenttype,omitempty"`

	// Data is the event-specific payload.
	Data json.RawMessage `json:"data,omitempty"`

	// Extensions are CloudEvents extension attributes beyond the
	// structured context fields above (e.g. the §12.3.7 lenny-prefixed
	// lennytenantid / lennyrootsessionid). They flatten into the
	// top-level CloudEvents JSON object on the wire, mirroring
	// pkg/gateway/eventbus.Event. CloudEvents extension attribute names
	// are lowercase alphanumeric. The map is excluded from the default
	// struct marshaling; MarshalJSON / UnmarshalJSON move it to and from
	// the top-level object. spec: §25.3 line 650 / §12.6 CloudEvents
	// envelope.
	Extensions map[string]string `json:"-"`
}

// MarshalJSON flattens the CloudEvents extension attributes into the
// top-level structured-content object, as the CloudEvents JSON format
// requires. The structured context fields marshal through the struct
// tags (so omitempty and the subject/severity/data rules are preserved);
// an extension never clobbers a structured attribute of the same name.
func (e OperationalEvent) MarshalJSON() ([]byte, error) {
	type known OperationalEvent
	base, err := json.Marshal(known(e))
	if err != nil {
		return nil, err
	}
	if len(e.Extensions) == 0 {
		return base, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range e.Extensions {
		if _, taken := m[k]; taken {
			continue
		}
		ev, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		m[k] = ev
	}
	return json.Marshal(m)
}

// UnmarshalJSON parses a structured-content CloudEvents object, pulling
// any string-valued attribute that is not a known context field back
// into Extensions so the envelope round-trips.
func (e *OperationalEvent) UnmarshalJSON(b []byte) error {
	type known OperationalEvent
	var k known
	if err := json.Unmarshal(b, &k); err != nil {
		return err
	}
	*e = OperationalEvent(k)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for key, rawVal := range raw {
		if knownEventAttributes[key] {
			continue
		}
		var s string
		if json.Unmarshal(rawVal, &s) == nil {
			if e.Extensions == nil {
				e.Extensions = map[string]string{}
			}
			e.Extensions[key] = s
		}
	}
	return nil
}

// knownEventAttributes is the set of structured CloudEvents context
// attributes OperationalEvent models as first-class fields; every other
// top-level string attribute is treated as an extension.
var knownEventAttributes = map[string]bool{
	"id": true, "source": true, "specversion": true, "type": true,
	"subject": true, "time": true, "severity": true,
	"datacontenttype": true, "data": true,
}

// BufferedEvent is one event in the buffer, tagged with its monotonic
// buffer id — the cursor a poller advances through.
type BufferedEvent struct {
	ID    uint64           `json:"id"`
	Event OperationalEvent `json:"event"`
}

// EventFilter narrows a §25.3 / §25.5 buffer query. An empty field does
// not filter on that dimension. The CSV fields accept the §25.2 CSV form
// (e.g. EventType "alert_fired,session_failed", Severity
// "critical,warning"); a query matches when the event matches any one
// of the comma-separated tokens. spec: §25.2 lines 210-211, 334-345.
type EventFilter struct {
	// EventType matches the CloudEvents type, either in full
	// (dev.lenny.alert_fired) or by short-name suffix (alert_fired).
	// A CSV value matches the union of its tokens.
	EventType string

	// Severity matches the event's severity extension attribute. A CSV
	// value matches the union of its tokens.
	Severity string

	// ResourceType matches the type segment of the event's Subject (the
	// part before the first "/", e.g. "pool" in "pool/default-gvisor").
	// A CSV value matches the union of its tokens. spec: §25.2 line 340
	// (?resourceType=pool).
	ResourceType string

	// ResourceID matches the id segment of the event's Subject (the part
	// after the first "/", e.g. "default-gvisor" in "pool/default-
	// gvisor"). A CSV value matches the union of its tokens. spec: §25.2
	// line 340 (?resourceId=default-gvisor).
	ResourceID string

	// Since constrains the query to events at or after this instant. A
	// zero value imposes no lower time bound. spec: §25.2 line 344
	// (?since=, RFC 3339).
	Since time.Time

	// Until constrains the query to events at or before this instant. A
	// zero value imposes no upper time bound. spec: §25.2 line 344
	// (?until=, RFC 3339).
	Until time.Time
}

// Matches reports whether e satisfies the filter. An empty filter field
// does not constrain that dimension; a CSV field matches when any token
// matches (a §25.2 union). spec: §25.2 lines 210-211, 334-345.
func (f EventFilter) Matches(e OperationalEvent) bool {
	if !matchToken(f.Severity, func(tok string) bool { return e.Severity == tok }) {
		return false
	}
	if !matchToken(f.EventType, func(tok string) bool {
		return e.Type == tok || strings.HasSuffix(e.Type, "."+tok)
	}) {
		return false
	}
	resType, resID := splitSubject(e.Subject)
	if !matchToken(f.ResourceType, func(tok string) bool { return resType == tok }) {
		return false
	}
	if !matchToken(f.ResourceID, func(tok string) bool { return resID == tok }) {
		return false
	}
	if !f.Since.IsZero() && e.Time.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && e.Time.After(f.Until) {
		return false
	}
	return true
}

// splitSubject splits a CloudEvents subject of the canonical
// "type/id" form into its resource type and id segments. A subject
// with no "/" is treated as the resource type with an empty id. spec:
// §16.6 catalogue subject convention ("pool/default-gvisor").
func splitSubject(subject string) (resType, resID string) {
	if i := strings.Index(subject, "/"); i >= 0 {
		return subject[:i], subject[i+1:]
	}
	return subject, ""
}

// matchToken reports whether csv is empty (no constraint) or any of its
// comma-separated, space-trimmed tokens satisfies match. An all-empty
// CSV ("," or " ") imposes no constraint.
func matchToken(csv string, match func(string) bool) bool {
	if csv == "" {
		return true
	}
	any := false
	for _, tok := range strings.Split(csv, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		any = true
		if match(tok) {
			return true
		}
	}
	// A CSV of only empty tokens imposes no constraint.
	return !any
}

// Pagination is the §25.2 canonical pagination envelope for the §25.3
// buffer query. The buffer's cursor is a per-replica monotonic uint64
// sequence, so cursorKind is always "buffer-seq" (one of the §25.2
// listed kinds). spec: §25.2 lines 245-275 / §25.3 line 750.
type Pagination struct {
	// Cursor is the id of the last event on the page — the value a
	// poller passes back as ?since=.
	Cursor uint64 `json:"cursor"`
	// HasMore reports that more events remain past Cursor.
	HasMore bool `json:"hasMore"`
	// Limit is the effective page size applied to this query.
	Limit int `json:"limit,omitempty"`
	// CursorKind disambiguates the cursor flavour for the agent; the
	// buffer is always "buffer-seq".
	CursorKind string `json:"cursorKind,omitempty"`
	// HeadCursor is the id of the newest event in the buffer, so a
	// poller can tell whether it has reached the live head.
	HeadCursor uint64 `json:"headCursor,omitempty"`
	// GapDetected reports that the requested cursor predated the oldest
	// retained event — the buffer wrapped past it (§25.3).
	GapDetected bool `json:"gapDetected,omitempty"`
	// GapReason explains a detected gap in human-readable form.
	GapReason string `json:"gapReason,omitempty"`
	// OldestAvailableCursor is the oldest id still in the buffer, set
	// alongside GapDetected so a poller can recover.
	OldestAvailableCursor uint64 `json:"oldestAvailableCursor,omitempty"`
	// SuggestedAction is the §25.2 recovery hint ("resync") emitted on a
	// gap so the agent re-reads platform state before assuming
	// continuity.
	SuggestedAction string `json:"suggestedAction,omitempty"`
}

// BufferedEventPage is the §25.3 GET /v1/admin/events/buffer response.
// Pagination fields ride under the §25.2 canonical pagination envelope.
// spec: §25.3 line 750.
type BufferedEventPage struct {
	Events     []BufferedEvent `json:"events"`
	Pagination Pagination      `json:"pagination"`

	// BufferAge is the duration since the oldest retained event.
	BufferAge string `json:"bufferAge,omitempty"`
}
