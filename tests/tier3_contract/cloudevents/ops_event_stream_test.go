// SPDX-License-Identifier: MIT

//go:build contract

// Wire-conformance contract test for the §25.5 Operational Event Stream
// envelope. §25.5 states that every event the stream service emits and
// delivers — over SSE, polling, and webhook — is a CloudEvents v1.0.2
// JSON record. This file drives all three delivery surfaces of the
// pkg/ops/events.Service, extracts the CloudEvents record each one puts
// on the wire, and pins it against the required CloudEvents context
// attributes and the application/ocsf+json discriminator for an
// audit-bearing event.
//
// The sibling files in this package pin the distinct §12.3.7
// eventbus.Event envelope; they do not exercise the §25.5
// OperationalEvent type or its three delivery transports, so a
// regression in the operational-event wire form (a wrong specversion, a
// dropped extension, a double-wrapped OCSF payload) would pass them
// while breaking every CloudEvents-conformant operations agent.
package cloudevents_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	opsevents "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// opsSourcePattern is the §25.5 `source` context-attribute form for an
// operational event: `//lenny.dev/gateway/{replicaId}` or
// `//lenny.dev/ops/{replicaId}`. spec: §25.5 (Event envelope).
var opsSourcePattern = regexp.MustCompile(`^//lenny\.dev/(gateway|ops)/[^/]+$`)

// newRepresentativeEvents returns a non-audit and an audit-bearing
// operational event with their §25.5 CloudEvents context attributes and
// Lenny extensions set, but with the emitter-stamped attributes (id,
// time, specversion) left zero so the Service stamps them exactly as it
// does in production.
func newRepresentativeEvents() (nonAudit, audit gwevents.OperationalEvent) {
	nonAudit = gwevents.OperationalEvent{
		Source:          "//lenny.dev/ops/ops-1",
		Type:            gwevents.EventType("operation_progressed").CloudEventsType(),
		Subject:         "operation/op-1",
		Severity:        "info",
		DataContentType: gwevents.ContentTypeJSON,
		Data:            json.RawMessage(`{"operationId":"op-1","kind":"platform_upgrade","newStatus":"running","progress":0.5}`),
		Extensions:      map[string]string{"lennytenantid": "acme", "lennyoperationid": "op-1"},
	}
	// An audit-bearing event carries the §11.7 OCSF v1.1.0 record inline
	// in `data` with datacontenttype application/ocsf+json (single-envelope
	// model). spec: §25.5 (Audit-bearing events).
	audit = gwevents.OperationalEvent{
		Source:          "//lenny.dev/gateway/gw-7f4c2",
		Type:            gwevents.EventType("audit_session_terminated").CloudEventsType(),
		Subject:         "session/sess-1",
		Severity:        "warning",
		DataContentType: gwevents.ContentTypeOCSF,
		Data:            json.RawMessage(`{"class_uid":3002,"metadata":{"version":"1.1.0"},"activity_id":1}`),
		Extensions:      map[string]string{"lennytenantid": "acme", "lennyrootsessionid": "root-9"},
	}
	return nonAudit, audit
}

// assertOperationalEnvelope pins one CloudEvents record extracted from a
// §25.5 delivery surface against the required context attributes. surface
// names the transport for failure messages; wantAudit selects the
// datacontenttype and inline-OCSF assertions.
//
// spec: §25.5 (Event envelope; Audit-bearing events)
func assertOperationalEnvelope(t *testing.T, sch interface{ Validate(any) error }, surface string, raw []byte, wantAudit bool) {
	t.Helper()

	// The record must validate against the externally published
	// CloudEvents v1.0.2 JSON Schema, not merely against Lenny's own view.
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("%s: decode CloudEvents record: %v; raw=%s", surface, err, raw)
	}
	if err := sch.Validate(instance); err != nil {
		t.Errorf("%s: record does not validate against the published CloudEvents v1.0.2 schema: %v", surface, err)
	}

	var flat map[string]json.RawMessage
	if err := json.Unmarshal(raw, &flat); err != nil {
		t.Fatalf("%s: decode record as flat object: %v", surface, err)
	}
	str := func(key string) string {
		var s string
		_ = json.Unmarshal(flat[key], &s)
		return s
	}

	// §25.5: the record is a CloudEvents v1.0.2 record. Per the CloudEvents
	// v1.0.2 spec and the §12.6 envelope-contract table, the specversion
	// attribute value for this revision is "1.0" (the "1.0.2" is the spec
	// document revision, not the attribute value); the §25.5 worked SSE
	// frame carries "specversion":"1.0".
	if got := str("specversion"); got != "1.0" {
		t.Errorf("%s: specversion = %q, want \"1.0\"", surface, got)
	}
	// §25.5: the CloudEvents id is the canonical eventKey, always present.
	if str("id") == "" {
		t.Errorf("%s: CloudEvents id is empty; the envelope carries no eventKey", surface)
	}
	// §25.5: source identifies the emitting component as
	// //lenny.dev/gateway/{replicaId} or //lenny.dev/ops/{replicaId}.
	if src := str("source"); !opsSourcePattern.MatchString(src) {
		t.Errorf("%s: source = %q, want //lenny.dev/{gateway|ops}/{replicaId}", surface, src)
	}
	// §25.5: the type field is dev.lenny.<short_name>.
	if typ := str("type"); !strings.HasPrefix(typ, "dev.lenny.") {
		t.Errorf("%s: type = %q, want a dev.lenny. prefix", surface, typ)
	}
	// §25.5: the Lenny extensions flatten to top-level keys on the wire.
	// lennytenantid is always present; a null (unset) value is not enough.
	if _, ok := flat["lennytenantid"]; !ok {
		t.Errorf("%s: extension lennytenantid did not flatten into the record: %s", surface, raw)
	}

	// §25.5: an audit-bearing event sets datacontenttype
	// application/ocsf+json and carries the OCSF record inline in `data`
	// (a JSON object, not a double-wrapped string). A non-audit event uses
	// application/json.
	if wantAudit {
		if got := str("datacontenttype"); got != gwevents.ContentTypeOCSF {
			t.Errorf("%s: audit-bearing datacontenttype = %q, want %q", surface, got, gwevents.ContentTypeOCSF)
		}
		var asObject map[string]any
		if err := json.Unmarshal(flat["data"], &asObject); err != nil {
			t.Errorf("%s: audit-bearing `data` is not an inline JSON object (double-wrapped OCSF record): %v; data=%s",
				surface, err, flat["data"])
		} else if asObject["class_uid"] == nil {
			t.Errorf("%s: inline OCSF record lost its class_uid field: %s", surface, flat["data"])
		}
	} else if got := str("datacontenttype"); got != gwevents.ContentTypeJSON {
		t.Errorf("%s: non-audit datacontenttype = %q, want %q", surface, got, gwevents.ContentTypeJSON)
	}
}

// spec: 25.5
// diagnosis: a failure here means the §25.5 Operational Event Stream puts
// a non-conformant CloudEvents record on one of its three delivery
// surfaces (SSE, polling, or webhook): a wrong specversion, a missing id
// or source, a type without the dev.lenny. prefix, a dropped Lenny
// extension, or a double-wrapped OCSF payload. Any of these breaks an
// off-the-shelf CloudEvents consumer even though the record round-trips
// through Lenny's own types. The same OperationalEvent is delivered on
// all three surfaces, so a per-surface failure localizes the defect to
// that transport's framing; a shared failure localizes it to the
// envelope marshaler.
func TestOperationalEventStreamCloudEventsConformance(t *testing.T) {
	sch := schematest.Compile(t, "tests/testdata/cloudevents/v1.0.2-cloudevents.schema.json")
	fixedNow := func() time.Time { return time.Date(2026, 4, 17, 14, 32, 8, 0, time.UTC) }

	nonAudit, audit := newRepresentativeEvents()

	// The webhook fan-out receives the same stamped events the SSE and
	// polling surfaces serve, so the webhook body is byte-for-byte the
	// record the other surfaces deliver.
	var fannedOut []gwevents.OperationalEvent
	svc := opsevents.New(opsevents.Options{
		ReplicaID: "ops-1",
		Now:       fixedNow,
		Webhook: func(_ context.Context, e gwevents.OperationalEvent) {
			fannedOut = append(fannedOut, e)
		},
	})

	ctx := context.Background()
	if _, err := svc.Publish(ctx, nonAudit); err != nil {
		t.Fatalf("Publish non-audit: %v", err)
	}
	if _, err := svc.Publish(ctx, audit); err != nil {
		t.Fatalf("Publish audit: %v", err)
	}

	// Both events, in publish order: index 0 is non-audit, index 1 audit.
	wantAudit := []bool{false, true}

	t.Run("SSE frames", func(t *testing.T) {
		records := sseRecords(t, svc)
		if len(records) != 2 {
			t.Fatalf("SSE stream delivered %d records, want 2", len(records))
		}
		for i, raw := range records {
			assertOperationalEnvelope(t, sch, "SSE", raw, wantAudit[i])
		}
	})

	t.Run("polling items", func(t *testing.T) {
		records := pollRecords(t, svc)
		if len(records) != 2 {
			t.Fatalf("poll response returned %d items, want 2", len(records))
		}
		for i, raw := range records {
			assertOperationalEnvelope(t, sch, "polling", raw, wantAudit[i])
		}
	})

	t.Run("webhook bodies", func(t *testing.T) {
		if len(fannedOut) != 2 {
			t.Fatalf("webhook fan-out saw %d events, want 2", len(fannedOut))
		}
		for i, e := range fannedOut {
			ct, body := deliverWebhook(t, e)
			// §25.5 Webhook Delivery: Content-Type is application/cloudevents+json.
			if ct != webhookdelivery.ContentType {
				t.Errorf("webhook Content-Type = %q, want %q", ct, webhookdelivery.ContentType)
			}
			assertOperationalEnvelope(t, sch, "webhook", body, wantAudit[i])
		}
	})
}

// sseRecords drives the SSE handler and returns the CloudEvents record
// carried in each frame's `data:` line, in delivery order. The request
// context is pre-cancelled so the handler flushes the buffered backlog
// and then returns from its live-stream select loop.
func sseRecords(t *testing.T, svc *opsevents.Service) [][]byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	cctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(cctx)
	rec := httptest.NewRecorder()

	svc.HandleStream(rec, platformAdminReq(req))

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("SSE Content-Type = %q, want text/event-stream", ct)
	}
	var records [][]byte
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			records = append(records, []byte(data))
		}
	}
	return records
}

// pollRecords drives the polling handler and returns the CloudEvents
// record under each item's `event` key, in chronological order.
func pollRecords(t *testing.T, svc *opsevents.Service) [][]byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)
	rec := httptest.NewRecorder()

	svc.HandlePoll(rec, platformAdminReq(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("poll status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []struct {
			Event json.RawMessage `json:"event"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode poll page: %v; body=%s", err, rec.Body.String())
	}
	records := make([][]byte, len(page.Items))
	for i, it := range page.Items {
		records[i] = it.Event
	}
	return records
}

// deliverWebhook POSTs the marshalled CloudEvents record for e over the
// §25.5 webhook transport to a capture server and returns the Content-Type
// header and body the receiver observed.
func deliverWebhook(t *testing.T, e gwevents.OperationalEvent) (contentType string, body []byte) {
	t.Helper()
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal webhook body: %v", err)
	}
	var gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	out := webhookdelivery.NewTransport(5*time.Second).Deliver(context.Background(), webhookdelivery.Delivery{
		CallbackURL: srv.URL,
		Body:        body,
		Secret:      []byte("test-secret"),
		EventType:   e.Type,
		EventID:     e.ID,
		DeliveryID:  "delivery-1",
		Attempt:     1,
	})
	if !out.Delivered() {
		t.Fatalf("webhook delivery failed: status=%d err=%v", out.StatusCode, out.Err)
	}
	return gotCT, gotBody
}
