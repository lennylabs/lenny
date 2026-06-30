// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/events"
	obaudit "github.com/lennylabs/lenny/pkg/observability/audit"
)

// opsStreamEmitter is the seam the §25.5 operational-event sink
// satisfies. It is the events.EventEmitter contract narrowed to the one
// method this package calls, kept local so the auditstore Store does not
// take a hard dependency on a concrete emitter and tests can substitute
// a fake. spec: §25.3 lines 660-663.
type opsStreamEmitter interface {
	Emit(ctx context.Context, event events.OperationalEvent) error
}

// WithOpsStreamEmitter wires the §16.7 / §25.5 operational-event
// escalation path: after a successful Append whose event type is one
// §16.7 routes onto the operational event stream, the Store translates
// the sealed row to OCSF and emits an audit-bearing CloudEvent
// (datacontenttype application/ocsf+json) through emitter. publisherID
// is the gateway-replica id stamped onto the CloudEvents source. A nil
// emitter leaves the escalation path disabled. F-25.5.18.
func WithOpsStreamEmitter(emitter opsStreamEmitter, publisherID string) Option {
	return func(s *Store) {
		s.opsEmitter = emitter
		s.opsPublisherID = publisherID
	}
}

// SetOpsStreamEmitter wires the §25.5 operational-event escalation
// emitter after construction. The gateway builds the Store before the
// shared operational-event emitter exists, so this setter completes the
// wiring once the emitter is available. F-25.5.18.
func (s *Store) SetOpsStreamEmitter(emitter opsStreamEmitter, publisherID string) {
	s.opsEmitter = emitter
	s.opsPublisherID = publisherID
}

// escalateToOpsStream emits an audit-bearing operational event for a
// committed audit row when the row's event type is one §16.7 routes
// onto the §25.5 operational event stream. The emit is best-effort: the
// audit row is already durable, so a translation or publish failure is
// logged and does not affect the Append result. spec: §16.7 line 661;
// §25.5 line 2556.
func (s *Store) escalateToOpsStream(ctx context.Context, row audit.Row) {
	if s.opsEmitter == nil {
		return
	}
	if !obaudit.EscalatesToOperationalStream(obaudit.EventType(row.EventType)) {
		return
	}
	ev, err := auditBearingEvent(row, s.opsPublisherID)
	if err != nil {
		// Translation failure on the escalation copy never blocks the
		// audit write; the row is durable and the §11.7 SIEM forwarder
		// drives the canonical OCSF translation independently.
		log.Printf("auditstore: ops-stream escalation translate (%s tenant=%s seq=%d): %v",
			row.EventType, row.TenantID, row.Seq, err)
		return
	}
	if err := s.opsEmitter.Emit(ctx, ev); err != nil {
		log.Printf("auditstore: ops-stream escalation emit (%s tenant=%s seq=%d): %v",
			row.EventType, row.TenantID, row.Seq, err)
	}
}

// auditBearingEvent translates a committed audit row to its §11.7 OCSF
// v1.1.0 record and wraps it in a §25.5 audit-bearing operational event.
// The CloudEvents data field carries the OCSF record directly
// (datacontenttype application/ocsf+json, the single-envelope model);
// the type is dev.lenny.<event_type>; lennytenantid (and, when present,
// lennyoperationid / lennyrootsessionid) ride as CloudEvents extension
// attributes. The Time and ID are left zero so the emitter stamps the
// §25.3 envelope. spec: §25.5 line 2556; §16.7 line 661.
func auditBearingEvent(row audit.Row, publisherID string) (events.OperationalEvent, error) {
	in := ocsf.Input{
		ID:                 row.TenantID + ":" + fmt.Sprint(row.Seq),
		Sequence:           row.Seq,
		TenantID:           row.TenantID,
		EventType:          row.EventType,
		EventSchemaVersion: row.EventSchemaVersion,
		CreatedAtUnixMs:    row.Timestamp.UTC().UnixMilli(),
		Payload:            row.Payload,
		PrevHash:           row.PrevHash,
		ChainIntegrity:     audit.ChainUnchecked,
	}
	rec, terr := ocsf.Translate(in)
	if terr != nil {
		return events.OperationalEvent{}, terr
	}
	body, err := ocsf.MarshalRecord(rec)
	if err != nil {
		return events.OperationalEvent{}, err
	}
	ext := map[string]string{"lennytenantid": row.TenantID}
	subject := "tenant/" + row.TenantID
	if p := payloadFields(row.Payload); p != nil {
		if sid, ok := p["session_id"].(string); ok && sid != "" {
			subject = "session/" + sid
		}
		if oid, ok := p["operation_id"].(string); ok && oid != "" {
			ext["lennyoperationid"] = oid
		}
		if rid, ok := p["root_session_id"].(string); ok && rid != "" {
			ext["lennyrootsessionid"] = rid
		}
	}
	return events.OperationalEvent{
		Source:  "//lenny.dev/gateway/" + publisherID,
		Type:    "dev.lenny." + row.EventType,
		Subject: subject,
		// Every escalated event is a security-salient §16.7 row operators
		// asked to see in real time; tag it warning so the §25.5
		// ?severity= filter surfaces the escalation set.
		Severity:        "warning",
		DataContentType: events.ContentTypeOCSF,
		Data:            body,
		Extensions:      ext,
	}, nil
}

// payloadFields decodes a row payload into a generic map for the few
// correlation fields auditBearingEvent reads. A nil or non-object
// payload yields nil so the caller falls back to the tenant subject.
func payloadFields(payload json.RawMessage) map[string]any {
	if len(payload) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil
	}
	return m
}
