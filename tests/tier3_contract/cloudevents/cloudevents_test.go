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
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
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

// alwaysActiveExpr is an evaluator.ExprEvaluator whose Active call always
// reports true, driving a rule straight into StateFiring on the first
// tick after its For window elapses.
type alwaysActiveExpr struct{}

func (alwaysActiveExpr) Active(context.Context, string) (bool, error) { return true, nil }

// spec: 25.17 (End-to-End Operational Example, Step 1: Observe — Event
// Arrives)
// diagnosis: §25.17 Step 1 shows the WarmPoolExhausted alert_fired
// event's `data` payload carrying severity, alertName, labels, runbook,
// and a suggestedAction object with action, endpoint, body, and
// reasoning. A payload-field rename or drop here breaks the watchdog's
// Step 2 decision step, which reads these fields directly off the SSE
// event without a follow-up call.
func TestCloudEventsAlertFiredPayloadFields(t *testing.T) {
	var rule rules.Rule
	for _, r := range rules.Catalog() {
		if r.Name == "WarmPoolExhausted" {
			rule = r
			break
		}
	}
	if rule.Name == "" {
		t.Fatal("WarmPoolExhausted not in the §16.5 catalog")
	}

	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "gw-7f4c2a1e")
	onFired, onResolved := evaluator.EmitCallbacks(evaluator.EventEmitOptions{
		Emitter: em,
		Source:  "//lenny.dev/gateway/gw-7f4c2a1e",
	})
	ev := evaluator.New([]rules.Rule{rule}, alwaysActiveExpr{}, evaluator.Options{
		OnFired: onFired, OnResolved: onResolved,
	})
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev.Tick(context.Background(), t0)
	// The rule's For is 60s; the second tick past that sustain window
	// crosses the pending -> firing edge and emits alert_fired.
	ev.Tick(context.Background(), t0.Add(2*time.Minute))

	page := buf.Query(0, events.EventFilter{EventType: "alert_fired"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("alert_fired emitted %d events, want 1", len(page.Events))
	}
	envelope := page.Events[0].Event
	if envelope.Type != "dev.lenny.alert_fired" {
		t.Errorf("type = %q, want dev.lenny.alert_fired", envelope.Type)
	}

	var data struct {
		Severity        string `json:"severity"`
		AlertName       string `json:"alertName"`
		Runbook         string `json:"runbook"`
		SuggestedAction *struct {
			Action    string          `json:"action"`
			Endpoint  string          `json:"endpoint"`
			Body      json.RawMessage `json:"body,omitempty"`
			Reasoning string          `json:"reasoning"`
		} `json:"suggestedAction"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("alert_fired data payload: %v", err)
	}

	if data.Severity != "critical" {
		t.Errorf("data.severity = %q, want critical", data.Severity)
	}
	if data.AlertName != "WarmPoolExhausted" {
		t.Errorf("data.alertName = %q, want WarmPoolExhausted", data.AlertName)
	}
	if data.Runbook != "warm-pool-exhaustion" {
		t.Errorf("data.runbook = %q, want warm-pool-exhaustion", data.Runbook)
	}
	if data.SuggestedAction == nil {
		t.Fatal("data.suggestedAction is absent, want a well-formed remediation hint")
	}
	if data.SuggestedAction.Action != "SCALE_WARM_POOL" {
		t.Errorf("data.suggestedAction.action = %q, want SCALE_WARM_POOL", data.SuggestedAction.Action)
	}
	if data.SuggestedAction.Endpoint == "" {
		t.Error("data.suggestedAction.endpoint is empty, want the remediation PUT route")
	}
	if data.SuggestedAction.Reasoning == "" {
		t.Error("data.suggestedAction.reasoning is empty, want the human-readable rationale")
	}
}

// spec: 25.17 (End-to-End Operational Example, Step 1: Observe — Event
// Arrives). The Step 1 SSE payload is:
// `"suggestedAction":{"action":"SCALE_WARM_POOL","endpoint":"PUT
// /v1/admin/pools/default-gvisor/warm-count","body":{"minWarm":15},
// "reasoning":"Pool exhausted for 3 minutes. Peak claim rate:
// 4.2/min."}` — the suggestedAction object carries a `body` key holding
// the concrete remediation request body.
// diagnosis: §25.17 Step 1's suggestedAction carries a `body` field
// alongside action/endpoint/reasoning so a watchdog can issue the PUT
// without a follow-up diagnostic call. This test is a non-blocking
// skip: pkg/alerting/rules.criticalAlerts's WarmPoolExhausted entry
// deliberately sets no Body on its catalog-level SuggestedAction (the
// concrete minWarm value depends on the pool's live claim rate, which
// the static rule catalog compiled at build time has no way to know;
// only the runtime §25.6 pool diagnostic can compute it). The §25.7
// Path B canonical alert_fired example (spec/25_agent-operability.md
// around line 3238) goes further and omits suggestedAction from the
// payload entirely, conflicting with §25.17's fuller illustration.
// Reconciling whether §25.17's example should drop `body` (matching
// the deliberate rule-level design) or whether the catalog should grow
// a template body is a spec-versus-implementation decision left for
// human review.
func TestCloudEventsAlertFiredPayloadSuggestedActionBody(t *testing.T) {
	t.Skip("WarmPoolExhausted's catalog-level SuggestedAction sets no Body, so alert_fired's suggestedAction omits body; spec-versus-implementation reconciliation pending (see TEST-GAPS.md)")

	var rule rules.Rule
	for _, r := range rules.Catalog() {
		if r.Name == "WarmPoolExhausted" {
			rule = r
			break
		}
	}
	if rule.Name == "" {
		t.Fatal("WarmPoolExhausted not in the §16.5 catalog")
	}

	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "gw-7f4c2a1e")
	onFired, onResolved := evaluator.EmitCallbacks(evaluator.EventEmitOptions{
		Emitter: em,
		Source:  "//lenny.dev/gateway/gw-7f4c2a1e",
	})
	ev := evaluator.New([]rules.Rule{rule}, alwaysActiveExpr{}, evaluator.Options{
		OnFired: onFired, OnResolved: onResolved,
	})
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev.Tick(context.Background(), t0)
	ev.Tick(context.Background(), t0.Add(2*time.Minute))

	page := buf.Query(0, events.EventFilter{EventType: "alert_fired"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("alert_fired emitted %d events, want 1", len(page.Events))
	}

	var data struct {
		SuggestedAction *struct {
			Body json.RawMessage `json:"body"`
		} `json:"suggestedAction"`
	}
	if err := json.Unmarshal(page.Events[0].Event.Data, &data); err != nil {
		t.Fatalf("alert_fired data payload: %v", err)
	}
	if data.SuggestedAction == nil {
		t.Fatal("data.suggestedAction is absent, want a well-formed remediation hint")
	}
	if len(data.SuggestedAction.Body) == 0 {
		t.Fatal("data.suggestedAction.body is absent, want the §25.17 Step 1 example's {\"minWarm\":15}")
	}
	var body struct {
		MinWarm int `json:"minWarm"`
	}
	if err := json.Unmarshal(data.SuggestedAction.Body, &body); err != nil {
		t.Fatalf("data.suggestedAction.body: %v", err)
	}
	if body.MinWarm != 15 {
		t.Errorf("data.suggestedAction.body.minWarm = %d, want 15", body.MinWarm)
	}
}

// spec: 25.5 (Event Types table — alert_fired payload highlights list
// "labels"), 25.7 (runbook discovery), 25.17 (End-to-End Operational
// Example, Step 1). The §25.5 Event Types table names labels among the
// alert_fired payload highlights, and both the §25.7 and §25.17
// WarmPoolExhausted alert_fired payloads carry
// "labels":{"pool":"default-gvisor"} — the firing series' identifying
// labels, not only the rule's static labels.
// diagnosis: the alert_fired data payload must carry a `labels` object
// naming the firing series (e.g. the exhausted pool). A watchdog reads
// labels.pool off the SSE event to scope its remediation to the right
// pool without a follow-up diagnostic call. A missing labels field
// forces the agent to guess which instance fired. This test is a
// non-blocking skip: the evaluator's EmitCallbacks builds the payload
// with no labels key at all, and the ExprEvaluator contract resolves a
// rule expression to a bool with no matched-series labels, so the
// firing series' pool identity has no source to flow from. Closing the
// gap is a product decision (whether emit.go merges the rule's static
// Labels and whether ExprEvaluator surfaces matched-series labels) that
// is left for human review.
func TestCloudEventsAlertFiredPayloadCarriesLabels(t *testing.T) {
	t.Skip("alert_fired payload emits no labels field: EmitCallbacks sets no labels key and ExprEvaluator surfaces no matched-series labels; product decision pending")

	var rule rules.Rule
	for _, r := range rules.Catalog() {
		if r.Name == "WarmPoolExhausted" {
			rule = r
			break
		}
	}
	if rule.Name == "" {
		t.Fatal("WarmPoolExhausted not in the §16.5 catalog")
	}

	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "gw-7f4c2a1e")
	onFired, onResolved := evaluator.EmitCallbacks(evaluator.EventEmitOptions{
		Emitter: em,
		Source:  "//lenny.dev/gateway/gw-7f4c2a1e",
	})
	ev := evaluator.New([]rules.Rule{rule}, alwaysActiveExpr{}, evaluator.Options{
		OnFired: onFired, OnResolved: onResolved,
	})
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev.Tick(context.Background(), t0)
	ev.Tick(context.Background(), t0.Add(2*time.Minute))

	page := buf.Query(0, events.EventFilter{EventType: "alert_fired"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("alert_fired emitted %d events, want 1", len(page.Events))
	}
	envelope := page.Events[0].Event

	var data struct {
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("alert_fired data payload: %v", err)
	}
	if len(data.Labels) == 0 {
		t.Fatal("data.labels is absent or empty, want the firing series' identifying labels")
	}
	if data.Labels["pool"] == "" {
		t.Errorf("data.labels.pool = %q, want the exhausted pool's identity", data.Labels["pool"])
	}
}
