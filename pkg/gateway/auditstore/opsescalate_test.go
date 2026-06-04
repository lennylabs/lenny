// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/events"
)

// captureEmitter records the events it is asked to emit and can be told
// to fail, so the escalation path's best-effort contract is observable.
type captureEmitter struct {
	got  []events.OperationalEvent
	fail error
}

func (c *captureEmitter) Emit(_ context.Context, e events.OperationalEvent) error {
	c.got = append(c.got, e)
	return c.fail
}

func sampleRow(eventType string, payload string) audit.Row {
	return audit.Row{
		ID:                 "row-uuid",
		Seq:                7,
		TenantID:           "acme",
		EventType:          eventType,
		EventSchemaVersion: "v1",
		Payload:            json.RawMessage(payload),
		Timestamp:          time.Unix(1700000000, 0).UTC(),
		PrevHash:           "deadbeef",
	}
}

// spec: §25.5 line 2556 / §16.7 line 661 — an escalating audit row is
// emitted onto the operational event stream as an audit-bearing
// CloudEvent: datacontenttype application/ocsf+json, the OCSF record in
// data, type dev.lenny.<event_type>, and the tenant correlation
// extension.
func TestEscalateToOpsStreamEmitsAuditBearing_spec_25_5(t *testing.T) {
	em := &captureEmitter{}
	s := New(nil)
	s.SetOpsStreamEmitter(em, "gw-abcde")

	row := sampleRow("delegation.self_recursion_allowed",
		`{"session_id":"sess-1","operation_id":"op-9","root_session_id":"root-2"}`)
	s.escalateToOpsStream(context.Background(), row)

	if len(em.got) != 1 {
		t.Fatalf("got %d emitted events, want 1", len(em.got))
	}
	ev := em.got[0]
	if !ev.IsAuditBearing() {
		t.Fatalf("emitted event is not audit-bearing: datacontenttype=%q", ev.DataContentType)
	}
	if ev.Type != "dev.lenny.delegation.self_recursion_allowed" {
		t.Errorf("type=%q, want dev.lenny.delegation.self_recursion_allowed", ev.Type)
	}
	if ev.Source != "//lenny.dev/gateway/gw-abcde" {
		t.Errorf("source=%q", ev.Source)
	}
	if ev.Subject != "session/sess-1" {
		t.Errorf("subject=%q, want session/sess-1", ev.Subject)
	}
	if ev.Extensions["lennytenantid"] != "acme" {
		t.Errorf("lennytenantid=%q, want acme", ev.Extensions["lennytenantid"])
	}
	if ev.Extensions["lennyoperationid"] != "op-9" {
		t.Errorf("lennyoperationid=%q, want op-9", ev.Extensions["lennyoperationid"])
	}
	if ev.Extensions["lennyrootsessionid"] != "root-2" {
		t.Errorf("lennyrootsessionid=%q, want root-2", ev.Extensions["lennyrootsessionid"])
	}
	// The data field must parse as the OCSF record (a JSON object with
	// the §11.7 metadata.version marker), not be double-wrapped.
	var ocsfObj map[string]any
	if err := json.Unmarshal(ev.Data, &ocsfObj); err != nil {
		t.Fatalf("data is not a JSON object: %v", err)
	}
	if _, ok := ocsfObj["metadata"]; !ok {
		t.Errorf("OCSF record missing metadata block: %s", ev.Data)
	}
}

// spec: §16.7 line 661 — only the escalating subset reaches the stream;
// an ordinary audit event is never double-emitted.
func TestEscalateToOpsStreamSkipsNonEscalating_spec_16_7(t *testing.T) {
	em := &captureEmitter{}
	s := New(nil)
	s.SetOpsStreamEmitter(em, "gw-abcde")

	s.escalateToOpsStream(context.Background(),
		sampleRow("token.exchanged", `{"caller_sub":"alice"}`))

	if len(em.got) != 0 {
		t.Fatalf("non-escalating event was emitted: %+v", em.got)
	}
}

// A Store with no ops emitter wired leaves the escalation path inert.
func TestEscalateToOpsStreamNilEmitter(t *testing.T) {
	s := New(nil)
	// No panic, no emission — escalation is a no-op without an emitter.
	s.escalateToOpsStream(context.Background(),
		sampleRow("delegation.self_recursion_allowed", `{}`))
}

// spec: §25.5 line 2556 — escalation is best-effort; an emit failure is
// swallowed because the audit row is already durable.
func TestEscalateToOpsStreamEmitErrorIsSwallowed_spec_25_5(t *testing.T) {
	em := &captureEmitter{fail: errors.New("redis down")}
	s := New(nil)
	s.SetOpsStreamEmitter(em, "gw-abcde")

	// Does not panic and does not surface the error.
	s.escalateToOpsStream(context.Background(),
		sampleRow("eventbus.republish_requested", `{}`))

	if len(em.got) != 1 {
		t.Fatalf("expected one emit attempt even on failure, got %d", len(em.got))
	}
}

// A tenant-scoped escalating event with no session_id falls back to the
// tenant subject and omits the optional correlation extensions.
func TestAuditBearingEventTenantSubjectFallback(t *testing.T) {
	ev, err := auditBearingEvent(sampleRow("audit.partition_drop_forced", `{"partition":"p1"}`), "gw-1")
	if err != nil {
		t.Fatalf("auditBearingEvent: %v", err)
	}
	if ev.Subject != "tenant/acme" {
		t.Errorf("subject=%q, want tenant/acme", ev.Subject)
	}
	if _, ok := ev.Extensions["lennyoperationid"]; ok {
		t.Errorf("lennyoperationid should be absent when payload has no operation_id")
	}
}
