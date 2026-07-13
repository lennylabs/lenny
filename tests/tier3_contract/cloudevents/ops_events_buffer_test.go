// SPDX-License-Identifier: MIT

//go:build contract

// Wire-conformance contract test for the §25.3 Gateway Event Buffer read
// surface. §25.3 states that every emitted operational event is a
// CloudEvents v1.0.2 JSON record, that an audit-bearing event carries the
// §11.7 OCSF v1.1.0 record directly in the CloudEvents `data` field with
// datacontenttype application/ocsf+json (single-envelope model, no
// double-wrapping), and that a non-audit event uses application/json with
// an event-specific JSON payload in `data`. This file drives the
// production emit path (pkg/gateway/eventbuffer.Emitter records into the
// in-process buffer) and reads the records back out through the assembled
// GET /v1/admin/events/buffer admin endpoint, then validates each record
// the endpoint serves against the externally published CloudEvents
// v1.0.2 JSON Schema and pins the datacontenttype discriminator.
//
// The sibling ops_event_stream_test.go pins the §25.5 OperationalEvent
// stream delivery surfaces (SSE, polling, webhook); it does not read back
// through the §25.3 buffer query endpoint. The admin package's own
// eventbuffer_test.go reads the endpoint but validates only against
// Lenny's own BufferedEventPage type, not the published CloudEvents
// schema. A regression that made the buffer serve a record well-formed
// per Lenny's types but non-conformant to CloudEvents (a dropped source,
// a double-wrapped OCSF payload, a mangled datacontenttype) would pass
// both and still break every off-the-shelf CloudEvents consumer polling
// the buffer.
package cloudevents_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 25.3
// diagnosis: a failure here means the §25.3 Gateway Event Buffer serves a
// record over GET /v1/admin/events/buffer that is not a conformant
// CloudEvents v1.0.2 record, or that violates the §25.3 single-envelope
// model: a missing id/source, a specversion other than "1.0", a type
// without the dev.lenny. prefix, an audit-bearing event whose OCSF record
// is double-wrapped or whose datacontenttype is not application/ocsf+json,
// or a non-audit event whose datacontenttype is not application/json. Any
// of these breaks an operations agent that polls the buffer with an
// off-the-shelf CloudEvents consumer, even though the record round-trips
// through Lenny's own BufferedEventPage type. The emit path and the read
// path are exercised end to end, so a failure localizes to either the
// Emitter's envelope stamping or the admin endpoint's serialization.
func TestGatewayEventBufferCloudEventsConformance(t *testing.T) {
	sch := schematest.Compile(t, "tests/testdata/cloudevents/v1.0.2-cloudevents.schema.json")
	fixedNow := func() time.Time { return time.Date(2026, 4, 17, 14, 32, 8, 0, time.UTC) }

	buf := eventbuffer.NewEventBuffer(0)
	emitter := eventbuffer.NewEmitter(buf, "gw-test", eventbuffer.WithClock(fixedNow))
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: fixedNow,
	}).WithEventBuffer(buf).WithEventEmitter(emitter)

	ctx := context.Background()

	// Emit every §16.6 gateway-emitted operational-event type as a
	// non-audit event (datacontenttype application/json, an event-specific
	// JSON object in data). §25.3 line 649: "Non-audit operational events
	// use datacontenttype application/json and carry an event-specific
	// JSON payload in data."
	for _, et := range events.GatewayEventTypes() {
		ev := events.OperationalEvent{
			Source:          "//lenny.dev/gateway/gw-test",
			Type:            et.CloudEventsType(),
			Subject:         "pool/default-gvisor",
			Severity:        "info",
			DataContentType: events.ContentTypeJSON,
			Data:            json.RawMessage(`{"pool":"default-gvisor","oldState":"warming","newState":"ready"}`),
			Extensions:      map[string]string{"lennytenantid": "acme"},
		}
		if err := emitter.Emit(ctx, ev); err != nil {
			t.Fatalf("emit %s: %v", et, err)
		}
	}

	// Emit an audit-bearing event: the §11.7 OCSF v1.1.0 record is carried
	// directly in data with datacontenttype application/ocsf+json.
	// §25.3 line 649: "Audit-bearing events carry the OCSF record directly
	// in the CloudEvents data field with datacontenttype
	// application/ocsf+json ... there is no intermediate container between
	// them."
	auditEv := events.OperationalEvent{
		Source:          "//lenny.dev/gateway/gw-test",
		Type:            events.EventSessionTerminated.CloudEventsType(),
		Subject:         "session/sess-1",
		Severity:        "warning",
		DataContentType: events.ContentTypeOCSF,
		Data:            json.RawMessage(`{"class_uid":3002,"metadata":{"version":"1.1.0"},"activity_id":1}`),
		Extensions:      map[string]string{"lennytenantid": "acme"},
	}
	if err := emitter.Emit(ctx, auditEv); err != nil {
		t.Fatalf("emit audit event: %v", err)
	}

	// Read the records back out through the assembled admin endpoint. The
	// ?limit=500 covers the full ring so every emitted event is on one page.
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/buffer?limit=500", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/admin/events/buffer: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// Decode the response keeping each event as raw wire JSON so the
	// CloudEvents record is validated as the endpoint served it, not as it
	// re-round-trips through Lenny's OperationalEvent type.
	var page struct {
		Events []struct {
			ID    uint64          `json:"id"`
			Event json.RawMessage `json:"event"`
		} `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode buffer page: %v; body=%s", err, rr.Body.String())
	}

	wantJSON := len(events.GatewayEventTypes())
	wantTotal := wantJSON + 1
	if len(page.Events) != wantTotal {
		t.Fatalf("buffer returned %d events, want %d", len(page.Events), wantTotal)
	}

	// The Emitter records each event by value and stamps the CloudEvents id
	// on its own copy, so the emitted distribution — not a caller-held id —
	// is what the read side must preserve. Classify each served record by
	// its wire datacontenttype and confirm the buffer returned exactly the
	// mix that was emitted: one audit-bearing OCSF record and one non-audit
	// JSON record per §16.6 gateway event type. A buffer that dropped,
	// duplicated, or flipped a datacontenttype fails this tally. spec: §25.3
	// line 649 (single-envelope model; audit-bearing application/ocsf+json,
	// non-audit application/json).
	var gotOCSF, gotJSON int
	for _, be := range page.Events {
		switch assertBufferedRecordConformant(t, sch, be.Event) {
		case events.ContentTypeOCSF:
			gotOCSF++
		case events.ContentTypeJSON:
			gotJSON++
		}
	}
	if gotOCSF != 1 {
		t.Errorf("buffer served %d audit-bearing (application/ocsf+json) records, want 1", gotOCSF)
	}
	if gotJSON != wantJSON {
		t.Errorf("buffer served %d non-audit (application/json) records, want %d", gotJSON, wantJSON)
	}
}

// assertBufferedRecordConformant validates one CloudEvents record the
// §25.3 buffer endpoint served against the published CloudEvents v1.0.2
// schema and pins the §25.3 single-envelope discriminator by the record's
// own wire datacontenttype: an application/ocsf+json record carries the
// OCSF record inline in data as a JSON object with class_uid (no
// double-wrapping); an application/json record carries an event-specific
// JSON object. It returns the record's datacontenttype so the caller can
// confirm the buffer preserved the emitted OCSF/JSON distribution.
//
// spec: 25.3
func assertBufferedRecordConformant(t *testing.T, sch interface{ Validate(any) error }, raw []byte) string {
	t.Helper()

	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("decode buffered CloudEvents record: %v; raw=%s", err, raw)
	}
	if err := sch.Validate(instance); err != nil {
		t.Errorf("buffered record does not validate against the published CloudEvents v1.0.2 schema: %v; raw=%s", err, raw)
		return ""
	}

	var flat map[string]json.RawMessage
	if err := json.Unmarshal(raw, &flat); err != nil {
		t.Fatalf("decode buffered record as flat object: %v", err)
	}
	str := func(key string) string {
		var s string
		_ = json.Unmarshal(flat[key], &s)
		return s
	}

	// §25.3 line 652 / §12.6: the record is a CloudEvents v1.0.2 record.
	// The specversion attribute value for this revision is "1.0".
	if got := str("specversion"); got != "1.0" {
		t.Errorf("specversion = %q, want \"1.0\"; raw=%s", got, raw)
	}
	// §25.3 line 646: the CloudEvents id is the canonical eventKey, always
	// present.
	if str("id") == "" {
		t.Errorf("buffered record carries no CloudEvents id (eventKey); raw=%s", raw)
	}
	// §25.3 line 647: the CloudEvents type follows dev.lenny.<short_name>.
	if typ := str("type"); len(typ) < len("dev.lenny.") || typ[:len("dev.lenny.")] != "dev.lenny." {
		t.Errorf("type = %q, want a dev.lenny. prefix", typ)
	}
	// §25.3 line 649: the Lenny extensions flatten onto the top-level
	// object; lennytenantid is present.
	if _, ok := flat["lennytenantid"]; !ok {
		t.Errorf("extension lennytenantid did not flatten into the buffered record: %s", raw)
	}

	// §25.3 line 649: single-envelope model. An audit-bearing record sets
	// datacontenttype application/ocsf+json and carries the OCSF record
	// inline in data as a JSON object (not a double-wrapped string). A
	// non-audit record uses application/json and carries an event-specific
	// JSON object. The datacontenttype must be one of these two values.
	dct := str("datacontenttype")
	switch dct {
	case events.ContentTypeOCSF:
		var obj map[string]any
		if err := json.Unmarshal(flat["data"], &obj); err != nil {
			t.Errorf("audit-bearing data is not an inline JSON object (double-wrapped OCSF record): %v; data=%s", err, flat["data"])
		} else if obj["class_uid"] == nil {
			t.Errorf("inline OCSF record lost its class_uid field: %s", flat["data"])
		}
	case events.ContentTypeJSON:
		var obj map[string]any
		if err := json.Unmarshal(flat["data"], &obj); err != nil {
			t.Errorf("non-audit data is not an event-specific JSON object: %v; data=%s", err, flat["data"])
		}
	default:
		t.Errorf("datacontenttype = %q, want %q or %q", dct, events.ContentTypeOCSF, events.ContentTypeJSON)
	}
	return dct
}
