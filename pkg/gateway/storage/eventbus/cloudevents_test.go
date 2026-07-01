// SPDX-License-Identifier: MIT

package eventbus

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// spec: 12.3.7
// diagnosis: §12.3.7 says every published Event sets the CloudEvents
// v1.0.2 context attributes — specversion "1.0", an id, a source, a
// dev.lenny.* type, an RFC 3339 time, a datacontenttype, and a subject.
// NewEvent must populate all of them and Validate must accept the result.
func TestNewEventSetsRequiredAttributes(t *testing.T) {
	ev, err := NewEvent(NewEventInput{
		TenantID:    "acme",
		PublisherID: "gw-7f4c2",
		Component:   "gateway",
		ShortName:   "session_state_changed",
		Subject:     "session/sess-1",
		Data:        json.RawMessage(`{"k":"v"}`),
		Now:         func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if ev.SpecVersion != "1.0" {
		t.Errorf("specversion = %q, want 1.0", ev.SpecVersion)
	}
	if ev.Type != "dev.lenny.session_state_changed" {
		t.Errorf("type = %q, want dev.lenny.session_state_changed", ev.Type)
	}
	if ev.Source != "//lenny.dev/gateway/gw-7f4c2" {
		t.Errorf("source = %q, want //lenny.dev/gateway/gw-7f4c2", ev.Source)
	}
	if !strings.HasPrefix(ev.ID, "acme:gw-7f4c2:") {
		t.Errorf("id = %q, want {tenant}:{publisher}: prefix", ev.ID)
	}
	if ev.DataContentType != ContentTypeJSON {
		t.Errorf("datacontenttype = %q, want application/json", ev.DataContentType)
	}
	if _, err := time.Parse(time.RFC3339, ev.Time); err != nil {
		t.Errorf("time %q is not RFC 3339: %v", ev.Time, err)
	}
	if err := ev.Validate(); err != nil {
		t.Errorf("a freshly built event must validate: %v", err)
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 mandates the lenny-prefixed extension attributes:
// lennytenantid is always present and mirrors the tenantID; the
// delegation-tree and operation extensions appear when supplied.
func TestNewEventSetsLennyExtensions(t *testing.T) {
	ev, err := NewEvent(NewEventInput{
		TenantID:      "acme",
		PublisherID:   "gw-1",
		ShortName:     "delegation_tree_completed",
		Subject:       "tree/root-1",
		RootSessionID: "root-1",
		OperationID:   "op-42",
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if ev.Extensions[ExtTenantID] != "acme" {
		t.Errorf("lennytenantid = %q, want acme", ev.Extensions[ExtTenantID])
	}
	if ev.Extensions[ExtRootSessionID] != "root-1" {
		t.Errorf("lennyrootsessionid = %q, want root-1", ev.Extensions[ExtRootSessionID])
	}
	if ev.Extensions[ExtOperationID] != "op-42" {
		t.Errorf("lennyoperationid = %q, want op-42", ev.Extensions[ExtOperationID])
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 says the id is
// {tenantId}:{publisherId}:{nanoTimestamp}:{nonce} with a 64-bit
// crypto/rand nonce — never a sequence counter. Two events built in
// the same nanosecond must still have distinct ids.
func TestNewEventIDIsUniquePerEvent(t *testing.T) {
	fixed := func() time.Time { return time.Unix(1_700_000_000, 12345).UTC() }
	a, err := NewEvent(NewEventInput{TenantID: "acme", PublisherID: "gw-1", ShortName: "x", Subject: "s/1", Now: fixed})
	if err != nil {
		t.Fatalf("NewEvent a: %v", err)
	}
	b, err := NewEvent(NewEventInput{TenantID: "acme", PublisherID: "gw-1", ShortName: "x", Subject: "s/1", Now: fixed})
	if err != nil {
		t.Fatalf("NewEvent b: %v", err)
	}
	if a.ID == b.ID {
		t.Errorf("two events share id %q — the crypto nonce is not doing its job", a.ID)
	}
	// The id has four colon-separated segments.
	if got := strings.Count(a.ID, ":"); got != 3 {
		t.Errorf("id %q has %d colons, want 3 (tenant:publisher:nanos:nonce)", a.ID, got)
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 says an audit-bearing event carrying an OCSF
// record sets datacontenttype application/ocsf+json. The OCSF flag
// must select that content type and IsAuditBearing must report it.
func TestNewEventAuditBearingContentType(t *testing.T) {
	ev, err := NewEvent(NewEventInput{
		TenantID: "acme", PublisherID: "gw-1", ShortName: "audit", Subject: "s/1",
		Data: json.RawMessage(`{"class_uid":3002}`), OCSF: true,
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if ev.DataContentType != ContentTypeOCSF {
		t.Errorf("datacontenttype = %q, want application/ocsf+json", ev.DataContentType)
	}
	if !ev.IsAuditBearing() {
		t.Error("IsAuditBearing must report true for an OCSF event")
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 requires the CloudEvents envelope to round-trip
// through structured-content JSON — the lenny-prefixed extensions are
// flattened to top-level attributes and parsed back.
func TestEventJSONRoundTrip(t *testing.T) {
	ev, err := NewEvent(NewEventInput{
		TenantID: "acme", PublisherID: "gw-1", ShortName: "session_state_changed",
		Subject: "session/s-1", RootSessionID: "root-1",
		Data: json.RawMessage(`{"state":"running"}`),
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// The extensions are flattened to top-level keys.
	var flat map[string]any
	if err := json.Unmarshal(b, &flat); err != nil {
		t.Fatalf("Unmarshal flat: %v", err)
	}
	if flat["lennytenantid"] != "acme" {
		t.Errorf("flattened lennytenantid = %v, want acme", flat["lennytenantid"])
	}
	var back Event
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.ID != ev.ID || back.Type != ev.Type || back.Subject != ev.Subject {
		t.Errorf("round trip lost a field: %+v vs %+v", back, ev)
	}
	if back.Extensions[ExtTenantID] != "acme" || back.Extensions[ExtRootSessionID] != "root-1" {
		t.Errorf("round trip lost extensions: %v", back.Extensions)
	}
	if string(back.Data) != `{"state":"running"}` {
		t.Errorf("round trip lost data: %s", back.Data)
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 / §12.4 say the EventBus channel is
// t:{tenant_id}:evt:{topic}. ChannelName must build exactly that, so a
// subscriber on one tenant cannot receive another tenant's events.
func TestChannelNameIsTenantPrefixed(t *testing.T) {
	if got := ChannelName("acme", TopicDelegationTree); got != "t:acme:evt:delegation_tree" {
		t.Errorf("ChannelName = %q, want t:acme:evt:delegation_tree", got)
	}
	if ChannelName("acme", TopicSessionLifecycle) == ChannelName("globex", TopicSessionLifecycle) {
		t.Error("two tenants must not share a channel name")
	}
}

// spec: 12.3.7
// diagnosis: Validate is the §12.3.7 envelope-contract guard. It must
// reject an envelope missing the mandatory lennytenantid extension, a
// wrong spec version, and a type without the dev.lenny. prefix.
func TestValidateRejectsMalformedEnvelopes(t *testing.T) {
	good, _ := NewEvent(NewEventInput{TenantID: "acme", PublisherID: "gw-1", ShortName: "x", Subject: "s/1"})

	bad := good
	bad.Extensions = map[string]string{} // lennytenantid removed
	if err := bad.Validate(); err == nil {
		t.Error("Validate accepted an envelope with no lennytenantid")
	}

	bad = good
	bad.SpecVersion = "0.3"
	if err := bad.Validate(); err == nil {
		t.Error("Validate accepted a non-1.0 spec version")
	}

	bad = good
	bad.Type = "com.example.event"
	if err := bad.Validate(); err == nil {
		t.Error("Validate accepted a type without the dev.lenny. prefix")
	}
}
