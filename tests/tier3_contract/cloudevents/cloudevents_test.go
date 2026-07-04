//go:build contract

// SPDX-License-Identifier: MIT

// Contract test for the §12.3.7 CloudEvents envelope. The EventBus
// publishes CloudEvents v1.0.2 messages; this suite asserts every
// published event carries the required context attributes, the
// lenny-prefixed extension attributes (lennytenantid,
// lennyrootsessionid, lennyoperationid), the dev.lenny. type prefix,
// and that audit-bearing events set datacontenttype
// application/ocsf+json. It also asserts the §12.4 tenant-prefixed
// channel-name convention.
//
// This file converts the TestCloudEventsEnvelopeShape,
// TestCloudEventsLennyExtensions, and TestCloudEventsTenantPrefixedChannels
// scaffolds (formerly skipped in scaffolds_test.go) into real tests.
package cloudevents_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
)

// spec: 12.3.7
// diagnosis: §12.3.7 mandates that every published Event sets the
// CloudEvents v1.0.2 context attributes — specversion "1.0", a unique
// id, a //lenny.dev/ source, a dev.lenny.<short_name> type, an RFC 3339
// time, a datacontenttype, and an opaque subject. A produced envelope
// that omits or mis-shapes any of them is a wire-contract regression.
func TestCloudEventsEnvelopeShape(t *testing.T) {
	for _, short := range []string{"delegation_tree_completed", "session_state_changed", "operation_progressed"} {
		ev, err := eventbus.NewEvent(eventbus.NewEventInput{
			TenantID:    "acme",
			PublisherID: "gw-7f4c2",
			Component:   "gateway",
			ShortName:   short,
			Subject:     "session/sess-1",
			Data:        json.RawMessage(`{"k":"v"}`),
		})
		if err != nil {
			t.Fatalf("NewEvent %s: %v", short, err)
		}
		// Validate enforces the §12.3.7 envelope contract.
		if err := ev.Validate(); err != nil {
			t.Errorf("%s: envelope fails the §12.3.7 contract: %v", short, err)
		}
		if ev.SpecVersion != "1.0" {
			t.Errorf("%s: specversion = %q, want 1.0", short, ev.SpecVersion)
		}
		if ev.Type != "dev.lenny."+short {
			t.Errorf("%s: type = %q, want dev.lenny.%s", short, ev.Type, short)
		}
		if !strings.HasPrefix(ev.Source, "//lenny.dev/gateway/") {
			t.Errorf("%s: source = %q, want //lenny.dev/gateway/ prefix", short, ev.Source)
		}
		if _, err := time.Parse(time.RFC3339, ev.Time); err != nil {
			t.Errorf("%s: time %q is not RFC 3339: %v", short, ev.Time, err)
		}
		// Round-trip through the structured-content JSON: every
		// attribute, including extensions, survives.
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", short, err)
		}
		var back eventbus.Event
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("%s: Unmarshal: %v", short, err)
		}
		if back.ID != ev.ID || back.Type != ev.Type || back.Subject != ev.Subject {
			t.Errorf("%s: envelope did not round-trip cleanly", short)
		}
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 says lennytenantid is always present and mirrors
// the tenantID; lennyrootsessionid is present for delegation-tree
// events; lennyoperationid is present where applicable. A publisher
// that omits a mandated extension breaks cross-cutting SIEM filters.
func TestCloudEventsLennyExtensions(t *testing.T) {
	t.Run("lennytenantid is mandatory on every event", func(t *testing.T) {
		ev, err := eventbus.NewEvent(eventbus.NewEventInput{
			TenantID: "acme", PublisherID: "gw-1", ShortName: "session_state_changed", Subject: "session/s",
		})
		if err != nil {
			t.Fatalf("NewEvent: %v", err)
		}
		if ev.Extensions[eventbus.ExtTenantID] != "acme" {
			t.Errorf("lennytenantid = %q, want acme", ev.Extensions[eventbus.ExtTenantID])
		}
		// An envelope stripped of lennytenantid fails Validate.
		ev.Extensions = map[string]string{}
		if err := ev.Validate(); err == nil {
			t.Error("Validate accepted an envelope with no lennytenantid")
		}
	})

	t.Run("delegation-tree events carry lennyrootsessionid and lennyoperationid", func(t *testing.T) {
		ev, err := eventbus.NewEvent(eventbus.NewEventInput{
			TenantID: "acme", PublisherID: "gw-1", ShortName: "delegation_tree_completed",
			Subject: "tree/root-9", RootSessionID: "root-9", OperationID: "op-7",
		})
		if err != nil {
			t.Fatalf("NewEvent: %v", err)
		}
		if ev.Extensions[eventbus.ExtRootSessionID] != "root-9" {
			t.Errorf("lennyrootsessionid = %q, want root-9", ev.Extensions[eventbus.ExtRootSessionID])
		}
		if ev.Extensions[eventbus.ExtOperationID] != "op-7" {
			t.Errorf("lennyoperationid = %q, want op-7", ev.Extensions[eventbus.ExtOperationID])
		}
		// The extensions flatten to top-level keys in the wire JSON.
		b, _ := json.Marshal(ev)
		var flat map[string]any
		if err := json.Unmarshal(b, &flat); err != nil {
			t.Fatalf("Unmarshal flat: %v", err)
		}
		for _, k := range []string{"lennytenantid", "lennyrootsessionid", "lennyoperationid"} {
			if _, ok := flat[k]; !ok {
				t.Errorf("extension %q did not flatten into the wire JSON", k)
			}
		}
	})
}

// spec: 12.3.7
// diagnosis: §12.3.7 says EventBus events are published on
// tenant-prefixed channels (t:{tenant_id}:evt:{topic}) and a
// subscriber on tenant A never receives tenant B events. The channel
// name must be derived solely from the tenant id and topic so two
// tenants can never collide.
func TestCloudEventsTenantPrefixedChannels(t *testing.T) {
	for _, topic := range eventbus.AllTopics() {
		a := eventbus.ChannelName("acme", topic)
		b := eventbus.ChannelName("globex", topic)
		if a == b {
			t.Errorf("topic %q: tenants acme and globex share channel %q", topic, a)
		}
		if !strings.HasPrefix(a, "t:acme:evt:") {
			t.Errorf("topic %q: channel %q lacks the t:{tenant}:evt: prefix", topic, a)
		}
	}
	// The two §12.3.7 topics produce distinct channels for one tenant.
	if eventbus.ChannelName("acme", eventbus.TopicDelegationTree) ==
		eventbus.ChannelName("acme", eventbus.TopicSessionLifecycle) {
		t.Error("the two §12.3.7 topics must map to distinct channels")
	}
}

// spec: 12.3.7
// diagnosis: §12.3.7 says an audit-bearing event carrying an OCSF
// record sets datacontenttype application/ocsf+json; a plain control
// event uses application/json. The single-envelope model — nothing is
// double-wrapped — depends on the content-type discriminator.
func TestCloudEventsAuditBearingContentType(t *testing.T) {
	plain, err := eventbus.NewEvent(eventbus.NewEventInput{
		TenantID: "acme", PublisherID: "gw-1", ShortName: "session_state_changed", Subject: "session/s",
	})
	if err != nil {
		t.Fatalf("NewEvent plain: %v", err)
	}
	if plain.DataContentType != eventbus.ContentTypeJSON {
		t.Errorf("plain event datacontenttype = %q, want application/json", plain.DataContentType)
	}
	if plain.IsAuditBearing() {
		t.Error("a plain control event must not report audit-bearing")
	}

	ocsfEv, err := eventbus.NewEvent(eventbus.NewEventInput{
		TenantID: "acme", PublisherID: "gw-1", ShortName: "session_state_changed", Subject: "session/s",
		Data: json.RawMessage(`{"class_uid":3002,"metadata":{"version":"1.1.0"}}`), OCSF: true,
	})
	if err != nil {
		t.Fatalf("NewEvent ocsf: %v", err)
	}
	if ocsfEv.DataContentType != eventbus.ContentTypeOCSF {
		t.Errorf("audit-bearing datacontenttype = %q, want application/ocsf+json", ocsfEv.DataContentType)
	}
	if !ocsfEv.IsAuditBearing() {
		t.Error("an OCSF event must report audit-bearing")
	}
}

// spec: 12.6 (single-envelope inline model)
// diagnosis: §12.6's single-envelope model requires the OCSF record to
// sit inline under the top-level `data` key as a JSON object; nothing is
// double-wrapped. A failure here means the envelope double-wraps the OCSF
// record — the SDK-alias serialization (application/ocsf+json data written
// as an escaped JSON string) the native struct exists to avoid — so `data`
// surfaces on the wire as a quoted string rather than an object.
func TestCloudEventsInlineOCSFWireForm(t *testing.T) {
	ev, err := eventbus.NewEvent(eventbus.NewEventInput{
		TenantID: "acme", PublisherID: "gw-1", ShortName: "session_state_changed", Subject: "session/s",
		Data: json.RawMessage(`{"class_uid":3002,"metadata":{"version":"1.1.0"}}`), OCSF: true,
	})
	if err != nil {
		t.Fatalf("NewEvent ocsf: %v", err)
	}
	if ev.DataContentType != eventbus.ContentTypeOCSF {
		t.Fatalf("test precondition: datacontenttype = %q, want application/ocsf+json", ev.DataContentType)
	}

	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// The wire form is a single flat CloudEvents object; `data` must
	// carry the OCSF record inline. Parsing into json.RawMessage keeps
	// the byte form of `data` intact so the object-versus-string
	// discriminator below is exact.
	var flat map[string]json.RawMessage
	if err := json.Unmarshal(b, &flat); err != nil {
		t.Fatalf("Unmarshal flat: %v", err)
	}
	raw, ok := flat["data"]
	if !ok {
		t.Fatalf("wire JSON has no top-level `data` key: %s", b)
	}

	// A double-wrapped payload surfaces `data` as a quoted JSON string;
	// the single-envelope inline model requires a JSON object. Decoding
	// into map[string]any succeeds only when `data` is an object, so it
	// fails against a string-wrapped regression.
	var asObject map[string]any
	if err := json.Unmarshal(raw, &asObject); err != nil {
		t.Fatalf("top-level `data` is not a JSON object (double-wrapped OCSF record): %v; data=%s", err, raw)
	}
	if got := asObject["class_uid"]; got == nil {
		t.Errorf("inline OCSF record lost its class_uid field on the wire: %s", raw)
	}
}
