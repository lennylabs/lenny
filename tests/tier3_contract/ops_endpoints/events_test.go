// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.5 Operational Event Stream delivery
// surface served by lenny-ops: the SSE stream, the monotonic-cursor
// polling endpoint with gap detection, the webhook subscription
// registration response, and the HMAC-signed webhook delivery path.
//
// These tests exercise the assembled pkg/ops/opsserver HTTP handler (the
// full mux plus the §25.4 correlation/access-log middleware) as a wire
// contract, rather than the pkg/ops/events service in isolation, so the
// SSE framing, the polling envelope, and the subscription-registration
// body are pinned at the surface an agent actually calls. The webhook
// delivery test drives the pkg/ops/opsservice delivery worker against a
// fake receiver that recomputes the HMAC-SHA256 signature and dedups by
// the CloudEvents id, closing the §4.0 gap that the event-stream
// delivery surface had no contract test above the unit tier.
package ops_endpoints_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// eventStreamServer builds a §25 lenny-ops Server with the §25.5
// EventStream service (seeded with the supplied events) and a
// MemoryStore-backed EventSubscriptions service wired, so the contract
// suite drives the assembled event-stream and subscription surfaces
// through the same mux the other §25 endpoints use.
func eventStreamServer(t *testing.T, capacity int, seed ...gwevents.OperationalEvent) *opsserver.Server {
	t.Helper()
	stream := opsstream.New(opsstream.Options{Capacity: capacity})
	for _, e := range seed {
		if _, err := stream.Publish(context.Background(), e); err != nil {
			t.Fatalf("seed event %q: %v", e.Type, err)
		}
	}
	subs := eventsubscription.NewService(eventsubscription.NewMemoryStore())
	// The §25.5 SSRF validator resolves the callback host on every call; a
	// contract test must not depend on live DNS, so inject a resolver that
	// maps every host to a public, globally-routable address. The scheme,
	// IP-literal, and metadata-host checks still run.
	subs.SSRF = eventsubscription.NewSSRFValidator(eventsubscription.SSRFConfig{
		Resolver: stubResolver{addr: netip.MustParseAddr("93.184.216.34")},
	})
	return opsserver.New(opsserver.Options{
		EventStream:        stream,
		EventSubscriptions: subs,
	})
}

// stubResolver is a §25.5 SSRF DNS seam that maps every host to a fixed
// public address so the callback-URL validation runs in a contract test
// without real DNS.
type stubResolver struct{ addr netip.Addr }

func (s stubResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{s.addr}, nil
}

// sseFrame is one parsed SSE record from GET /v1/admin/events/stream.
type sseFrame struct {
	id, event, data string
}

// serveSSE issues an SSE request against the assembled server with a
// pre-cancelled context so the handler writes the buffered backlog and
// returns without blocking on live delivery, then parses the frames.
// The §25.4 statusRecorder middleware forwards Flush to the underlying
// httptest recorder, which satisfies http.Flusher, so the handler does
// not abort with "streaming unsupported".
func serveSSE(t *testing.T, srv *opsserver.Server, target string, headers map[string]string) []sseFrame {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req.WithContext(ctx))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("SSE Content-Type = %q, want text/event-stream", ct)
	}
	var frames []sseFrame
	var cur sseFrame
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		switch {
		case line == "" && cur != (sseFrame{}):
			frames = append(frames, cur)
			cur = sseFrame{}
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
	if cur != (sseFrame{}) {
		frames = append(frames, cur)
	}
	return frames
}

// TestEventStreamSSEFramingContract pins the §25.5 SSE frame wire
// contract: each buffered event is emitted as one SSE record whose id:
// line carries the CloudEvents id (the resume key), whose event: line
// carries the CloudEvents type, and whose data: line is the full
// CloudEvents JSON envelope with the same id and type.
//
// spec: 25.5 (SSE Delivery — "Each SSE frame is a CloudEvents JSON
// record — the full envelope in the frame's data: line ... The SSE id:
// line carries the CloudEvents id attribute so clients reconnecting with
// Last-Event-ID resume from the correct position"), 16.6 (CloudEvents
// type is dev.lenny.<short_name>)
// diagnosis: A failure means the assembled lenny-ops SSE endpoint no
// longer frames operational events per §25.5. Either the id: line is
// missing (a reconnecting client cannot resume via Last-Event-ID), the
// event: line dropped the CloudEvents type, or the data: line is not the
// CloudEvents envelope. Any of these breaks a watchdog consuming the
// stream.
func TestEventStreamSSEFramingContract(t *testing.T) {
	srv := eventStreamServer(
		t, 0,
		gwevents.OperationalEvent{Type: "dev.lenny.alert_fired", Severity: "critical"},
		gwevents.OperationalEvent{Type: "dev.lenny.pool_state_changed", Severity: "warning"},
	)
	frames := serveSSE(t, srv, "/v1/admin/events/stream", nil)
	if len(frames) != 2 {
		t.Fatalf("SSE frames = %d, want 2 backlog frames", len(frames))
	}
	wantType := []string{"dev.lenny.alert_fired", "dev.lenny.pool_state_changed"}
	for i, f := range frames {
		if f.id == "" {
			t.Errorf("frame %d has no id: line; a client cannot resume via Last-Event-ID", i)
		}
		if f.event != wantType[i] {
			t.Errorf("frame %d event: = %q, want %q", i, f.event, wantType[i])
		}
		var env gwevents.OperationalEvent
		if err := json.Unmarshal([]byte(f.data), &env); err != nil {
			t.Fatalf("frame %d data: is not a CloudEvents JSON record: %v", i, err)
		}
		if env.Type != wantType[i] {
			t.Errorf("frame %d envelope type = %q, want %q", i, env.Type, wantType[i])
		}
		// The id: line is the CloudEvents id / eventKey; it must match the
		// envelope so a Last-Event-ID resume lands on the right event.
		if env.ID != f.id {
			t.Errorf("frame %d id: line %q != envelope id %q", i, f.id, env.ID)
		}
		if env.SpecVersion == "" {
			t.Errorf("frame %d envelope missing specversion", i)
		}
	}
}

// TestEventStreamSSEResumeContract pins the §25.5 Last-Event-ID resume:
// a reconnecting client that presents the id: line of an event it
// already saw receives only the events after it, not the whole backlog.
//
// spec: 25.5 (SSE Delivery — "clients reconnecting with Last-Event-ID
// resume from the correct position via XRANGE")
// diagnosis: A failure means the SSE endpoint ignores Last-Event-ID and
// replays already-delivered events, so a reconnecting watchdog double-
// processes events it already handled.
func TestEventStreamSSEResumeContract(t *testing.T) {
	srv := eventStreamServer(
		t, 0,
		gwevents.OperationalEvent{Type: "dev.lenny.alert_fired"},
		gwevents.OperationalEvent{Type: "dev.lenny.pool_state_changed"},
		gwevents.OperationalEvent{Type: "dev.lenny.drift_detected"},
	)
	all := serveSSE(t, srv, "/v1/admin/events/stream", nil)
	if len(all) != 3 {
		t.Fatalf("initial SSE frames = %d, want 3", len(all))
	}
	// Reconnect presenting the first event's id: everything after it, and
	// only that, must be replayed.
	resumed := serveSSE(t, srv, "/v1/admin/events/stream", map[string]string{
		"Last-Event-ID": all[0].id,
	})
	if len(resumed) != 2 {
		t.Fatalf("resumed SSE frames = %d, want 2 (events after the resume point)", len(resumed))
	}
	if resumed[0].id != all[1].id {
		t.Errorf("resume delivered %q first, want %q (the event after the resume key)", resumed[0].id, all[1].id)
	}
}

// pollBody drives GET /v1/admin/events and returns the decoded envelope
// plus its pagination sub-object.
func pollBody(t *testing.T, srv *opsserver.Server, target string) (map[string]any, map[string]any) {
	t.Helper()
	rec, body := request(t, srv, http.MethodGet, target, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("poll %s status = %d, want 200; body=%v", target, rec.Code, body)
	}
	assertJSONContentType(t, rec)
	pag, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("poll response missing §25.2 pagination envelope: %v", body)
	}
	return body, pag
}

// TestEventStreamPollingCursorContract pins the §25.5 polling contract:
// GET /v1/admin/events returns the §25.2 canonical pagination envelope
// with an opaque source-kind cursor, and paging forward with that cursor
// advances monotonically past the events already returned.
//
// spec: 25.5 (Polling Delivery — "?cursor= opaque string from a prior
// response ... Response: items, pagination.cursor, pagination.hasMore,
// pagination.cursorKind"; "Ordering: chronological (oldest-first within
// a page) by default")
// diagnosis: A failure means the polling endpoint no longer honors the
// opaque cursor: either it does not report cursorKind/hasMore, or a
// follow-up poll with the returned cursor re-serves events already read,
// so a polling watchdog cannot make forward progress without duplicates.
func TestEventStreamPollingCursorContract(t *testing.T) {
	srv := eventStreamServer(
		t, 0,
		gwevents.OperationalEvent{Type: "dev.lenny.alert_fired"},
		gwevents.OperationalEvent{Type: "dev.lenny.pool_state_changed"},
		gwevents.OperationalEvent{Type: "dev.lenny.drift_detected"},
	)
	// First page, oldest-first, one item at a time.
	body, pag := pollBody(t, srv, "/v1/admin/events?limit=1")
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("first page items = %d, want 1", len(items))
	}
	if pag["cursorKind"] != opsstream.SourceKindBuffer {
		t.Errorf("cursorKind = %v, want %q", pag["cursorKind"], opsstream.SourceKindBuffer)
	}
	if pag["hasMore"] != true {
		t.Errorf("hasMore = %v, want true after a one-item page of three events", pag["hasMore"])
	}
	firstItem := items[0].(map[string]any)
	if _, ok := firstItem["id"]; !ok {
		t.Errorf("poll item dropped its top-level wrapper id; the /v1/admin/events envelope item shape is {\"id\":N,\"event\":{...}}: %v", firstItem)
	}
	cursor, _ := pag["cursor"].(string)
	if cursor == "" {
		t.Fatal("first page returned no cursor")
	}
	// Second page with the cursor advances past the first event. The item's
	// top-level wrapper id is the per-source buffer position and must advance
	// across the page boundary.
	body2, _ := pollBody(t, srv, "/v1/admin/events?limit=1&cursor="+cursor)
	items2, _ := body2["items"].([]any)
	if len(items2) != 1 {
		t.Fatalf("second page items = %d, want 1", len(items2))
	}
	secondItem := items2[0].(map[string]any)
	if secondItem["id"] == firstItem["id"] {
		t.Errorf("cursor did not advance: second page re-served item id %v", firstItem["id"])
	}
}

// TestEventStreamPollingGapDetectionContract pins the §25.5 eviction/gap
// contract: when the cursor's event has been evicted from the capped
// buffer, the poll response reports pagination.gapDetected and an
// oldestAvailableCursor so the agent knows to re-read platform state
// rather than assume continuity.
//
// spec: 25.5 (Eviction and gap behavior — "When events are evicted
// before a slow subscriber reads them, the subscriber's next request
// returns pagination.gapDetected: true"; Polling Delivery — "the
// response returns pagination.gapDetected: true and
// pagination.oldestAvailableCursor")
// diagnosis: A failure means the polling endpoint silently serves past
// an eviction without flagging the gap, so a slow watchdog loses events
// without knowing it must resync. Either gapDetected is not raised when
// the cursor's eventKey is gone, or oldestAvailableCursor is missing.
func TestEventStreamPollingGapDetectionContract(t *testing.T) {
	// A three-event ring buffer, controlled directly so eviction is
	// precise: three events fill it, and a capture-poll records a cursor
	// pointing at the oldest retained event.
	stream := opsstream.New(opsstream.Options{Capacity: 3})
	for _, typ := range []string{"e1", "e2", "e3"} {
		if _, err := stream.Publish(context.Background(), gwevents.OperationalEvent{Type: "dev.lenny." + typ}); err != nil {
			t.Fatalf("publish %s: %v", typ, err)
		}
	}
	srv := opsserver.New(opsserver.Options{EventStream: stream})
	// Capture a cursor pointing at the oldest retained event (e1).
	_, pag0 := pollBody(t, srv, "/v1/admin/events?limit=1")
	staleCursor, _ := pag0["cursor"].(string)
	if staleCursor == "" {
		t.Fatal("pre-eviction poll returned no cursor")
	}
	// Evict e1..e3 by publishing three more events into the size-3 buffer.
	for _, typ := range []string{"e4", "e5", "e6"} {
		if _, err := stream.Publish(context.Background(), gwevents.OperationalEvent{Type: "dev.lenny." + typ}); err != nil {
			t.Fatalf("publish %s: %v", typ, err)
		}
	}
	// Poll with the stale cursor: the event it names has been evicted.
	body, pag := pollBody(t, srv, "/v1/admin/events?limit=10&cursor="+staleCursor)
	if pag["gapDetected"] != true {
		t.Errorf("gapDetected = %v, want true when the cursor's event was evicted; body=%v", pag["gapDetected"], body)
	}
	if oldest, _ := pag["oldestAvailableCursor"].(string); oldest == "" {
		t.Errorf("gap response missing oldestAvailableCursor; body=%v", body)
	}
}

// TestEventSubscriptionRegistrationContract pins the §25.5 webhook
// subscription registration response: POST /v1/admin/event-subscriptions
// returns the plaintext secret exactly once with the single-use warning,
// and the read endpoints redact the secret while surfacing only its
// fingerprint.
//
// spec: 25.5 (Webhook Secret Lifecycle — "generates a secret
// server-side ... and returns the plaintext secret once in the response
// body alongside a clear single-use notice"; "never returned on read
// endpoints (GET /v1/admin/event-subscriptions,
// GET /v1/admin/event-subscriptions/{id} omit the secret field
// entirely)")
// diagnosis: A failure means the subscription-registration wire contract
// drifted: either Create no longer returns the one-time secret plus its
// rotation warning (a caller can never obtain the HMAC secret), or a read
// endpoint leaks the secret (it must expose only the fingerprint).
func TestEventSubscriptionRegistrationContract(t *testing.T) {
	srv := eventStreamServer(t, 0)
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", nil,
		map[string]any{
			"callbackUrl": "https://hooks.acme.com/lenny",
			"types":       []string{"dev.lenny.alert_fired"},
			"description": "acme incident bridge",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatal("create response missing subscription id")
	}
	secret, _ := body["secret"].(string)
	if secret == "" {
		t.Error("create response omitted the one-time plaintext secret")
	}
	if warn, _ := body["secretRotationWarning"].(string); !strings.Contains(warn, "only time the secret will be returned") {
		t.Errorf("secretRotationWarning = %q, want the §25.5 single-use notice", warn)
	}
	if _, ok := body["secretFingerprint"]; !ok {
		t.Error("create response missing secretFingerprint")
	}

	// GET {id} must redact the secret.
	recGet, get := request(t, srv, http.MethodGet, "/v1/admin/event-subscriptions/"+id, nil, nil)
	if recGet.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%v", recGet.Code, get)
	}
	if _, leaked := get["secret"]; leaked {
		t.Error("GET /v1/admin/event-subscriptions/{id} leaked the plaintext secret")
	}
	if get["secretFingerprint"] == nil || get["secretFingerprint"] == "" {
		t.Error("read view missing secretFingerprint")
	}

	// List must also redact.
	_, list := request(t, srv, http.MethodGet, "/v1/admin/event-subscriptions", nil, nil)
	subs, _ := list["subscriptions"].([]any)
	if len(subs) != 1 {
		t.Fatalf("list returned %d subscriptions, want 1", len(subs))
	}
	if _, leaked := subs[0].(map[string]any)["secret"]; leaked {
		t.Error("GET /v1/admin/event-subscriptions leaked the plaintext secret")
	}
}

// TestEventStreamAndDeliveryRoutesReportTenantAdminInOpenAPIMetadata pins
// the §25.5 tenant-admin admissibility of the poll/stream endpoints and the
// per-subscription deliveries/rotate-secret endpoints in the OpenAPI role
// metadata the F-COV-3 merge serves. §25.4 requires platform-admin or
// tenant-admin on every lenny-ops endpoint, and §25.5's Tenant Isolation
// section documents SSE/polling as tenant-scoped and the deliveries/
// rotate-secret endpoints as reachable for a subscription's owning tenant
// (TestEventSubscriptionTenantOwnershipIsolation in event_subscriptions_-
// test.go pins the same-tenant success path), so the x-lenny-required-role
// these routes carry must admit tenant-admin rather than report
// platform-admin as the sole role.
//
// spec: 25.4 ("Requires platform-admin or tenant-admin role on all
// endpoints"), 25.5 ("SSE and polling endpoints apply the same filter:
// tenant-scoped callers only see events matching their tenant or carrying
// no tenant label")
// diagnosis: opsserver.RouteSchemas() reports one of these routes as
// x-lenny-required-role: platform-admin, under-reporting the tenant-admin
// admissibility §25.4/§25.5 grant. A caller deriving its callable surface
// from the served openapi.json (the §25.4 /me.links.openApi discovery hop)
// would wrongly conclude a tenant-admin cannot poll or stream operational
// events, or list/rotate the secret of its own subscription's deliveries.
func TestEventStreamAndDeliveryRoutesReportTenantAdminInOpenAPIMetadata(t *testing.T) {
	want := map[string]bool{
		"GET /v1/admin/events":                                  true,
		"GET /v1/admin/events/stream":                           true,
		"GET /v1/admin/event-subscriptions/{id}/deliveries":     true,
		"POST /v1/admin/event-subscriptions/{id}/rotate-secret": true,
	}
	found := map[string]bool{}
	for _, r := range opsserver.RouteSchemas() {
		key := r.Method + " " + r.Path
		if !want[key] {
			continue
		}
		found[key] = true
		if r.RequiredRole != mcp.RoleTenantAdmin {
			t.Errorf("%s x-lenny-required-role = %q, want %q (admits platform-admin and tenant-admin)",
				key, r.RequiredRole, mcp.RoleTenantAdmin)
		}
	}
	for key := range want {
		if !found[key] {
			t.Errorf("RouteSchemas has no entry for %q", key)
		}
	}
}

// fixedEventSource is a §25.5 EventSource that yields a fixed event slice
// on the first poll and nothing thereafter, so a worker Tick delivers a
// known event exactly once per configured poll.
type fixedEventSource struct {
	events []opsservice.WebhookEvent
	polled bool
}

func (f *fixedEventSource) Poll(context.Context) ([]opsservice.WebhookEvent, error) {
	if f.polled {
		return nil, nil
	}
	f.polled = true
	return f.events, nil
}

// fixedSubscriptions is a §25.5 SubscriptionSource returning one active
// subscription.
type fixedSubscriptions struct {
	sub opsservice.WebhookSubscription
}

func (f fixedSubscriptions) Subscriptions() []opsservice.WebhookSubscription {
	return []opsservice.WebhookSubscription{f.sub}
}

// TestWebhookDeliveryHMACContract pins the §25.5 webhook delivery wire
// contract: the delivery worker POSTs the CloudEvents JSON body to the
// callback URL with an X-Lenny-Signature that is the HMAC-SHA256 of the
// body keyed by the subscription secret, a stable X-Lenny-Event-Id equal
// to the CloudEvents id, and a per-attempt X-Lenny-Delivery-Id, so a
// receiver can validate authenticity and deduplicate replays by the
// CloudEvents id.
//
// spec: 25.5 (Webhook Delivery — "X-Lenny-Signature — HMAC-SHA256 of the
// request body using the subscription's secret"; "X-Lenny-Event-Id —
// the CloudEvents id attribute"; "X-Lenny-Delivery-Id — a UUID unique to
// this delivery attempt (enables idempotency on the receiver)"), 14
// (receivers MUST deduplicate by CloudEvents id)
// diagnosis: A failure means the webhook delivery path no longer signs
// deliveries per §25.5, or the event-id/delivery-id headers drifted, so a
// receiver either cannot authenticate the delivery (invalid HMAC) or
// cannot deduplicate a redelivered event (unstable X-Lenny-Event-Id).
func TestWebhookDeliveryHMACContract(t *testing.T) {
	secret := []byte("whsec_contract_test_secret")
	body := []byte(`{"specversion":"1.0","id":"ops:1700000000000000000:1","type":"dev.lenny.alert_fired","source":"//lenny.dev/ops/0"}`)
	const eventID = "ops:1700000000000000000:1"

	type received struct {
		sigValid    bool
		eventID     string
		deliveryIDs map[string]bool
		replay      bool
	}
	got := received{deliveryIDs: map[string]bool{}}
	seenEventIDs := map[string]bool{}

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		// Recompute the HMAC-SHA256 of the body and compare in constant time.
		mac := hmac.New(sha256.New, secret)
		mac.Write(raw)
		want := hex.EncodeToString(mac.Sum(nil))
		gotSig := r.Header.Get(webhookdelivery.HeaderSignature)
		got.sigValid = hmac.Equal([]byte(want), []byte(gotSig))
		got.eventID = r.Header.Get(webhookdelivery.HeaderEventID)
		if did := r.Header.Get(webhookdelivery.HeaderDeliveryID); did != "" {
			got.deliveryIDs[did] = true
		}
		// Receiver-side idempotency: dedup by the CloudEvents id so a
		// redelivered event is recognized as a replay.
		if seenEventIDs[got.eventID] {
			got.replay = true
		}
		seenEventIDs[got.eventID] = true
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	worker := opsservice.NewWebhookWorker(opsservice.WebhookWorkerConfig{
		Events: &fixedEventSource{events: []opsservice.WebhookEvent{
			{ID: eventID, Type: "dev.lenny.alert_fired", Body: body},
		}},
		Subscriptions: fixedSubscriptions{sub: opsservice.WebhookSubscription{
			ID:           "sub-1",
			CallbackURL:  receiver.URL,
			Secret:       secret,
			TenantFilter: eventsubscription.TenantFilterAll,
		}},
	})

	if err := worker.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	if !got.sigValid {
		t.Error("delivered X-Lenny-Signature is not a valid HMAC-SHA256 of the body")
	}
	if got.eventID != eventID {
		t.Errorf("X-Lenny-Event-Id = %q, want the CloudEvents id %q", got.eventID, eventID)
	}
	if len(got.deliveryIDs) != 1 {
		t.Errorf("delivery ids seen = %d, want 1 per attempt", len(got.deliveryIDs))
	}
	if got.replay {
		t.Error("first delivery was flagged a replay")
	}

	// Redeliver the same event. Its CloudEvents id is stable, so the
	// receiver deduping by X-Lenny-Event-Id recognizes it as a replay.
	worker2 := opsservice.NewWebhookWorker(opsservice.WebhookWorkerConfig{
		Events: &fixedEventSource{events: []opsservice.WebhookEvent{
			{ID: eventID, Type: "dev.lenny.alert_fired", Body: body},
		}},
		Subscriptions: fixedSubscriptions{sub: opsservice.WebhookSubscription{
			ID:           "sub-1",
			CallbackURL:  receiver.URL,
			Secret:       secret,
			TenantFilter: eventsubscription.TenantFilterAll,
		}},
	})
	if err := worker2.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if !got.replay {
		t.Error("redelivery of the same CloudEvents id was not recognized as a replay by the receiver")
	}
	if len(got.deliveryIDs) != 2 {
		t.Errorf("delivery ids after two deliveries = %d, want 2 (each attempt gets a unique X-Lenny-Delivery-Id)", len(got.deliveryIDs))
	}
}

// serveSSERaw issues an SSE request against the assembled server with a
// pre-cancelled context and returns the raw response body, so a test can pin
// the comment lines (":degradation", ":gap") the frame parser discards.
func serveSSERaw(t *testing.T, srv *opsserver.Server, target string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req.WithContext(ctx))
	return rec.Body.String()
}

// TestEventStreamNoRedisWiredEmitsNoDegradationEnvelopeContract pins the §25.5
// degradation matrix at the wire: it keys on the two source-health signals and
// enumerates exactly three read-side states, of which Redis-reachable is the
// healthy, envelope-free one. A lenny-ops replica with no Redis client wired is
// in that healthy state, so GET /v1/admin/events serializes no degradation
// object and an SSE connection emits no :degradation comment.
//
// spec: 25.5 (Degradation — the read surface attaches the EVENT_STREAM_DEGRADED
// envelope when serving from the gateway-buffer fall-back during a Redis
// outage, and the lenny-ops-local-buffer envelope when both sources are
// unreachable)
// diagnosis: A failure means the read surface reports a fourth degradation
// state that the §25.5 matrix does not define. Every poll response from a
// Redis-less deployment carries a degradation object on an otherwise healthy
// 200, and every SSE connection opens with a :degradation comment, so a
// consumer that treats the envelope as an outage signal reads a permanent
// outage that is not happening.
func TestEventStreamNoRedisWiredEmitsNoDegradationEnvelopeContract(t *testing.T) {
	srv := eventStreamServer(
		t, 0,
		gwevents.OperationalEvent{Type: "dev.lenny.alert_fired", Severity: "critical"},
	)

	body, _ := pollBody(t, srv, "/v1/admin/events")
	if deg, ok := body["degradation"]; ok {
		t.Errorf("poll response carried a degradation object (%v) from a replica with no Redis client wired; §25.5 classifies that state healthy", deg)
	}

	raw := serveSSERaw(t, srv, "/v1/admin/events/stream")
	if strings.Contains(raw, ":degradation") {
		t.Errorf("SSE connection emitted a :degradation comment from a replica with no Redis client wired: %q", raw)
	}
}
