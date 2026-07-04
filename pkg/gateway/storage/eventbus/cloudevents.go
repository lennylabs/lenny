// SPDX-License-Identifier: MIT

// Package eventbus is the §12.3.7 EventBus — the audit and event
// publication layer on top of the Wave-1 pkg/gateway/pubsub Redis
// substrate. It carries control-plane events (delegation-tree signals,
// session-lifecycle notifications) and, when an event is audit-bearing,
// the §11.7 OCSF record, each inside a CloudEvents v1.0.2 envelope.
//
// The CloudEvents envelope makes the event transport-independent: the
// same envelope flows over Redis pub/sub, SSE, and §25 webhooks. The
// envelope is the transport; an OCSF record, when present, is the
// payload. Nothing is double-wrapped.
//
// EventBus provides at-most-once delivery in v1 (Redis pub/sub drops
// messages when no subscriber is connected). Callers must tolerate
// missed events; a caller that requires guaranteed delivery polls
// durable Postgres state as a fallback. The §12.3.7 publish-state
// machine and the retranscribe worker close the gap for audit-bearing
// events.
package eventbus

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"time"
)

// SpecVersion is the §12.3.7 CloudEvents envelope version. The
// envelope's specversion attribute carries "1.0"; the implemented
// revision is v1.0.2.
const SpecVersion = "1.0"

// content types the §12.3.7 datacontenttype attribute uses.
const (
	// ContentTypeJSON is the default datacontenttype.
	ContentTypeJSON = "application/json"

	// ContentTypeOCSF is the datacontenttype an audit-bearing event
	// sets — its data field carries an OCSF v1.1.0 record (§11.7).
	ContentTypeOCSF = "application/ocsf+json"
)

// typePrefix is the §12.3.7 CloudEvents `type` prefix. The full type
// is "dev.lenny." + the §16.6 / §25 catalog short name.
const typePrefix = "dev.lenny."

// EventTopic identifies a control-plane event channel. §12.3.7 pins
// this list; adding a topic requires updating it, the CloudEvents type
// registry, and the §12.4 key table.
type EventTopic string

const (
	// TopicDelegationTree carries budget signals, child completion,
	// and tree cancellation.
	TopicDelegationTree EventTopic = "delegation_tree"

	// TopicSessionLifecycle carries state transitions and coordinator
	// handoff notifications.
	TopicSessionLifecycle EventTopic = "session_lifecycle"
)

// AllTopics returns the closed §12.3.7 topic list.
func AllTopics() []EventTopic {
	return []EventTopic{TopicDelegationTree, TopicSessionLifecycle}
}

// IsValid reports whether t is one of the §12.3.7 topics.
func (t EventTopic) IsValid() bool {
	return t == TopicDelegationTree || t == TopicSessionLifecycle
}

// Event is a §12.3.7 CloudEvents v1.0.2 envelope. §12.6 codifies this
// native structured-content struct rather than an alias of the go-sdk
// cloudevents.Event type, because the released go-sdk serializes
// application/ocsf+json data as an escaped JSON string and would
// double-wrap the audit record. The struct implements the exact
// §12.3.7 context-attribute contract and emits `data` inline.
type Event struct {
	// SpecVersion is the CloudEvents spec version — always "1.0".
	SpecVersion string `json:"specversion"`

	// ID is the unique identifier
	// {tenantId}:{publisherId}:{nanoTimestamp}:{nonce}. It is the
	// primary de-duplication key under a future at-least-once backend.
	ID string `json:"id"`

	// Source is the emitter URI //lenny.dev/{component}/{replicaId}.
	Source string `json:"source"`

	// Type is dev.lenny.<short_name> from the §16.6 / §25 catalogs.
	Type string `json:"type"`

	// Time is an RFC 3339 UTC timestamp.
	Time string `json:"time"`

	// DataContentType is application/json by default;
	// application/ocsf+json for an audit-bearing event.
	DataContentType string `json:"datacontenttype"`

	// Subject is an opaque domain subject: session/{id}, tree/{id},
	// or pool/{name}.
	Subject string `json:"subject"`

	// Data is the event-specific payload, JSON-encoded. For an
	// audit-bearing event it is the OCSF v1.1.0 record.
	Data json.RawMessage `json:"data,omitempty"`

	// Extensions are the §12.3.7 lenny-prefixed extension attributes:
	// lennytenantid (always), lennyrootsessionid (delegation-tree
	// events), lennyoperationid (where applicable). CloudEvents
	// extension attribute names are lowercase alphanumeric.
	Extensions map[string]string `json:"-"`
}

// extension attribute names §12.3.7 pins.
const (
	ExtTenantID      = "lennytenantid"
	ExtRootSessionID = "lennyrootsessionid"
	ExtOperationID   = "lennyoperationid"
)

// MarshalJSON flattens the extension attributes into the top-level
// CloudEvents JSON object, as the structured content mode requires.
func (e Event) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"specversion":     e.SpecVersion,
		"id":              e.ID,
		"source":          e.Source,
		"type":            e.Type,
		"time":            e.Time,
		"datacontenttype": e.DataContentType,
		"subject":         e.Subject,
	}
	if len(e.Data) > 0 {
		m["data"] = e.Data
	}
	for k, v := range e.Extensions {
		m[k] = v
	}
	return json.Marshal(m)
}

// UnmarshalJSON parses a structured-content CloudEvents object,
// pulling the lenny-prefixed extension attributes back into Extensions.
func (e *Event) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	get := func(key string) string {
		raw, ok := m[key]
		if !ok {
			return ""
		}
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	e.SpecVersion = get("specversion")
	e.ID = get("id")
	e.Source = get("source")
	e.Type = get("type")
	e.Time = get("time")
	e.DataContentType = get("datacontenttype")
	e.Subject = get("subject")
	if raw, ok := m["data"]; ok {
		e.Data = raw
	}
	known := map[string]bool{
		"specversion": true, "id": true, "source": true, "type": true,
		"time": true, "datacontenttype": true, "subject": true, "data": true,
	}
	for k, raw := range m {
		if known[k] {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if e.Extensions == nil {
				e.Extensions = map[string]string{}
			}
			e.Extensions[k] = s
		}
	}
	return nil
}

// Validate reports an error when the envelope violates the §12.3.7
// CloudEvents contract: the required context attributes must be set,
// the spec version must be "1.0", the type must carry the dev.lenny.
// prefix, and lennytenantid must always be present.
func (e Event) Validate() error {
	if e.SpecVersion != SpecVersion {
		return fmt.Errorf("eventbus: specversion %q, want %q", e.SpecVersion, SpecVersion)
	}
	for name, val := range map[string]string{
		"id": e.ID, "source": e.Source, "type": e.Type,
		"time": e.Time, "datacontenttype": e.DataContentType, "subject": e.Subject,
	} {
		if val == "" {
			return fmt.Errorf("eventbus: CloudEvents attribute %q is empty", name)
		}
	}
	if len(e.Type) <= len(typePrefix) || e.Type[:len(typePrefix)] != typePrefix {
		return fmt.Errorf("eventbus: type %q lacks the %q prefix", e.Type, typePrefix)
	}
	if _, err := time.Parse(time.RFC3339, e.Time); err != nil {
		return fmt.Errorf("eventbus: time %q is not RFC 3339: %w", e.Time, err)
	}
	if e.DataContentType != ContentTypeJSON && e.DataContentType != ContentTypeOCSF {
		return fmt.Errorf("eventbus: datacontenttype %q is not a §12.3.7 value", e.DataContentType)
	}
	if e.Extensions[ExtTenantID] == "" {
		return fmt.Errorf("eventbus: %s extension is mandatory on every event", ExtTenantID)
	}
	return nil
}

// IsAuditBearing reports whether the envelope carries an OCSF audit
// record (datacontenttype application/ocsf+json).
func (e Event) IsAuditBearing() bool {
	return e.DataContentType == ContentTypeOCSF
}

// NewEventInput carries the caller-supplied fields NewEvent needs.
type NewEventInput struct {
	// TenantID populates the lennytenantid extension and the id prefix.
	TenantID string

	// PublisherID is the gateway replica id (gw-<5-hex>) — the id
	// segment and the source path's replica id.
	PublisherID string

	// Component is the source URI component segment (e.g. "gateway").
	Component string

	// ShortName is the §16.6 / §25 catalog short name; the type
	// becomes dev.lenny.<ShortName>.
	ShortName string

	// Subject is the opaque domain subject.
	Subject string

	// RootSessionID, when set, populates the lennyrootsessionid
	// extension (delegation-tree events).
	RootSessionID string

	// OperationID, when set, populates the lennyoperationid extension.
	OperationID string

	// Data is the JSON-encoded payload.
	Data json.RawMessage

	// OCSF marks the event audit-bearing — Data is an OCSF record and
	// datacontenttype is set to application/ocsf+json.
	OCSF bool

	// Now is the event time source; defaults to time.Now.
	Now func() time.Time
}

// NewEvent builds a §12.3.7 CloudEvents v1.0.2 envelope with the
// mandatory context attributes and the lenny-prefixed extensions. The
// id is {tenantId}:{publisherId}:{nanoTimestamp}:{nonce} with a 64-bit
// crypto/rand nonce — never math/rand and never a sequence counter,
// because the id is the de-duplication key under a future
// at-least-once backend.
func NewEvent(in NewEventInput) (Event, error) {
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	if in.PublisherID == "" {
		return Event{}, fmt.Errorf("eventbus: NewEvent requires a publisherId")
	}
	if in.Component == "" {
		in.Component = "gateway"
	}
	t := now().UTC()
	nonce, err := cryptoNonce()
	if err != nil {
		return Event{}, fmt.Errorf("eventbus: generate id nonce: %w", err)
	}
	ct := ContentTypeJSON
	if in.OCSF {
		ct = ContentTypeOCSF
	}
	ext := map[string]string{ExtTenantID: in.TenantID}
	if in.RootSessionID != "" {
		ext[ExtRootSessionID] = in.RootSessionID
	}
	if in.OperationID != "" {
		ext[ExtOperationID] = in.OperationID
	}
	return Event{
		SpecVersion:     SpecVersion,
		ID:              fmt.Sprintf("%s:%s:%d:%s", in.TenantID, in.PublisherID, t.UnixNano(), nonce),
		Source:          fmt.Sprintf("//lenny.dev/%s/%s", in.Component, in.PublisherID),
		Type:            typePrefix + in.ShortName,
		Time:            t.Format(time.RFC3339),
		DataContentType: ct,
		Subject:         in.Subject,
		Data:            in.Data,
		Extensions:      ext,
	}, nil
}

// cryptoNonce returns a lowercase-hex 64-bit value from crypto/rand —
// the §12.3.7 id nonce construction. math/rand and sequence counters
// are explicitly forbidden because the id is the de-duplication key.
func cryptoNonce() (string, error) {
	max := new(big.Int).Lsh(big.NewInt(1), 64)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", n), nil
}

// ChannelName returns the §12.4 tenant-prefixed Redis channel for a
// (tenant, topic) pair: t:{tenant_id}:evt:{topic}. Callers never build
// raw channel names — tenant isolation is enforced at this boundary.
func ChannelName(tenantID TenantID, topic EventTopic) string {
	return fmt.Sprintf("t:%s:evt:%s", tenantID, topic)
}

// SortedExtensionNames returns the extension attribute names of e
// sorted, for deterministic diagnostics.
func SortedExtensionNames(e Event) []string {
	out := make([]string, 0, len(e.Extensions))
	for k := range e.Extensions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
