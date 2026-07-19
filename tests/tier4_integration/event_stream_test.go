//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration suite for the §25.5 Operational Event Stream. The
// real cmd/lenny-ops binary runs against a Postgres and a Redis
// container, and the tests drive the operability surface the TESTING.md
// §15.3 integration-suite contract names: SSE connection and event
// delivery, event-type and severity filtering, the Redis stream as the
// primary event source, webhook subscriptions gated by SSRF-validated
// callback URLs, and the opaque cursor model with its per-replica
// monotonic ULID-like eventKey.
//
// Coverage split: the read surface (SSE, polling), the cursor model, and
// the Redis-stream source are exercised here against the live binary.
// The Redis-down / gateway-buffer fall-back and the gap-on-eviction path
// (which needs the in-memory ring buffer to overflow its fixed 500-event
// capacity) are degradation behaviours pinned at unit tier in
// pkg/ops/events (degradation_test.go, gapmetric_test.go,
// read_surface_test.go); reproducing an eviction or a source outage
// against the live binary is not something a Tier-4 flow can do cheaply
// or deterministically, so this suite asserts the observable contract of
// those paths (the opaque cursorKind the surface reports, the ULID-like
// eventKey the cursor round-trips) rather than forcing the outage.
package tier4_integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// adminRequest issues an admin request against the unauthenticated
// lenny-ops surface under the dev platform-admin headers it honours and
// returns the status, response headers, and decoded top-level JSON
// object. It mirrors the do closure used across the other §25.4/§25.5
// tier-4 tests so the event-stream suite reads the same way.
func adminRequest(t *testing.T, base, method, path string, body any) (int, http.Header, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, resp.Header, out
}

// createEscalation POSTs a §25.4 escalation and returns its id. Creating
// an escalation synchronously emits the escalation_created operational
// event through the shared lenny-ops event emitter, so it is the driver
// this suite uses to put a known CloudEvent onto both the in-memory read
// surface and the Redis stream.
func createEscalation(t *testing.T, base, severity, summary string) string {
	t.Helper()
	code, _, esc := adminRequest(t, base, http.MethodPost, "/v1/admin/escalations", map[string]any{
		"severity": severity,
		"summary":  summary,
	})
	if code != http.StatusCreated {
		t.Fatalf("POST /v1/admin/escalations (%s): status %d, want 201 (%v)", severity, code, esc)
	}
	id, _ := esc["id"].(string)
	if id == "" {
		t.Fatalf("POST /v1/admin/escalations (%s): no id in response %v", severity, esc)
	}
	return id
}

// opsSSEFrame is one parsed Server-Sent-Events record from the §25.5 stream.
type opsSSEFrame struct {
	id        string
	eventType string
	data      []byte
}

// readSSEFrames reads SSE records from r until it has collected want
// frames whose event: line matches eventType, or ctx expires. Comment
// lines (":degradation", ":gap") and non-matching frames are skipped.
// spec: §25.5 SSE Delivery — each frame carries an id: line (the
// CloudEvents id / eventKey), an event: line (the type), and a data:
// line (the full CloudEvents JSON record).
func readSSEFrames(ctx context.Context, r *bufio.Reader, eventType string, want int) ([]opsSSEFrame, error) {
	var out []opsSSEFrame
	var cur opsSSEFrame
	for {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		line, err := r.ReadString('\n')
		if err != nil {
			return out, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			// End of a frame. Keep it when it matches the wanted type.
			if cur.eventType == eventType && cur.data != nil {
				out = append(out, cur)
				if len(out) >= want {
					return out, nil
				}
			}
			cur = opsSSEFrame{}
		case strings.HasPrefix(line, ":"):
			// Comment line (degradation / gap control signal); ignore.
		case strings.HasPrefix(line, "id:"):
			cur.id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			cur.eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			cur.data = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}

// parseEventKey splits a §25.5 canonical eventKey
// ({replicaID}:{emittedAt}:{nonce}) into its trailing emittedAt-nanos and
// monotonic nonce. The replicaID prefix is returned verbatim; only the
// last two colon-separated segments are the ULID-like ordering fields, so
// a replicaID that itself contains a colon still parses.
func parseEventKey(t *testing.T, key string) (replicaID string, nanos int64, nonce uint64) {
	t.Helper()
	parts := strings.Split(key, ":")
	if len(parts) < 3 {
		t.Fatalf("eventKey %q: want {replicaID}:{emittedAt}:{nonce}, got %d segments", key, len(parts))
	}
	nonce, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
	if err != nil {
		t.Fatalf("eventKey %q: nonce segment %q not a uint64: %v", key, parts[len(parts)-1], err)
	}
	nanos, err = strconv.ParseInt(parts[len(parts)-2], 10, 64)
	if err != nil {
		t.Fatalf("eventKey %q: emittedAt segment %q not an int64: %v", key, parts[len(parts)-2], err)
	}
	replicaID = strings.Join(parts[:len(parts)-2], ":")
	if replicaID == "" {
		t.Fatalf("eventKey %q: empty replicaID prefix", key)
	}
	return replicaID, nanos, nonce
}

// TestOpsEventStreamReadSurfaceE2E boots cmd/lenny-ops against real
// Postgres and Redis and drives the §25.5 read surface end to end: an SSE
// consumer receives the escalation_created CloudEvent the binary emits,
// event-type and severity filters narrow the polling endpoint, an
// unrecognized filter token is rejected with INVALID_EVENT_FILTER, the
// opaque cursor advances so a follow-up poll returns only events created
// after it, and each event's eventKey is the ULID-like
// {replicaID}:{emittedAt}:{nonce} whose per-replica nonce increases
// monotonically. The emitted event is also asserted onto the Redis
// ops:events:stream, the §25.5 primary source both the SSE/webhook worker
// and peer replicas consume.
//
// spec: §25.5 (Operational Event Stream) — "SSE Delivery ... Each SSE
// frame is a CloudEvents JSON record ... The SSE id: line carries the
// CloudEvents id attribute"; "Polling Delivery ... Response: items,
// pagination.cursor, pagination.hasMore, pagination.cursorKind (one of
// redis, buffer, mixed), pagination.headCursor"; "Cursor Model ... The
// canonical ordering key is eventKey (a ULID-like
// {replicaID}:{emittedAt}:{nonce})"; "Storage — Redis capped stream. Key:
// ops:events:stream (platform-scoped)"; "Error Codes — INVALID_EVENT_FILTER
// | PERMANENT | 400 | Unrecognized event type or severity in filter". The
// eventKey nonce is "a per-replica monotonic counter" (§25.5 Cursor Model).
//
// diagnosis: a failure means the cmd/lenny-ops composition root did not
// wire the §25.5 read surface to the live event emitter. Either the SSE
// handler did not deliver an emitted escalation_created CloudEvent (the
// eventStream service is not the emitter subsystems publish through, or
// the SSE id:/event:/data: framing drifted), the polling filters or the
// opaque cursor diverged from §25.5, the INVALID_EVENT_FILTER rejection is
// missing, the eventKey lost its ULID-like {replicaID}:{ts}:{nonce}
// structure or its monotonic nonce, or the emitter no longer fans the
// event out to the platform Redis stream ops:events:stream.
func TestOpsEventStreamReadSurfaceE2E(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	ops := opsprocess.StartWith(
		t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
	)
	base := ops.BaseURL()
	ctx := context.Background()

	escType := events.EventEscalationCreated.CloudEventsType() // dev.lenny.escalation_created

	// ---- bullet 1 + 5: SSE delivery and the monotonic eventKey ----
	// Create one escalation, then open an SSE stream filtered to the
	// escalation_created type. With no Last-Event-ID the handler replays
	// the buffered backlog (the just-created event) before going live, so
	// the first frame is deterministic. A second escalation created while
	// the connection is open must arrive live with a strictly greater
	// nonce.
	idA := createEscalation(t, base, "critical", "warm pool exhausted; scale-up failed")

	sseCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, http.MethodGet,
		base+"/v1/admin/events/stream?eventType=escalation_created", nil)
	sseReq.Header.Set("X-Lenny-Tenant-ID", "acme")
	sseReq.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	sseReq.Header.Set("X-Lenny-Roles", "platform-admin")
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("SSE stream status %d, want 200", sseResp.StatusCode)
	}
	if ct := sseResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("SSE Content-Type = %q, want text/event-stream", ct)
	}
	sseReader := bufio.NewReader(sseResp.Body)

	// First frame: the replayed escalation A. Reading it proves the
	// subscription is installed, so the escalation B created next is
	// delivered live rather than raced.
	frames, err := readSSEFrames(sseCtx, sseReader, escType, 1)
	if err != nil || len(frames) != 1 {
		t.Fatalf("read backlog SSE frame: got %d frames, err %v", len(frames), err)
	}
	frameA := frames[0]
	assertEscalationFrame(t, frameA, escType, idA)
	_, _, nonceA := parseEventKey(t, frameA.id)

	idB := createEscalation(t, base, "warning", "connector token nearing expiry")
	frames, err = readSSEFrames(sseCtx, sseReader, escType, 1)
	if err != nil || len(frames) != 1 {
		t.Fatalf("read live SSE frame: got %d frames, err %v", len(frames), err)
	}
	frameB := frames[0]
	assertEscalationFrame(t, frameB, escType, idB)
	replicaA, _, nonceB := parseEventKey(t, frameB.id)
	if nonceB <= nonceA {
		t.Fatalf("eventKey nonce not monotonic: A=%d, B=%d (want B>A)", nonceA, nonceB)
	}
	_ = replicaA
	cancel() // release the SSE connection before the polling assertions.

	// ---- bullet 2: event-type and severity filtering (polling) ----
	// A third escalation at info severity gives three severities in the
	// buffer. The type filter returns every escalation_created event; the
	// severity filter returns only the critical one.
	idC := createEscalation(t, base, "info", "routine capacity review requested")

	page := pollEvents(t, base, "eventType=escalation_created")
	items := page.items
	if len(items) < 3 {
		t.Fatalf("poll eventType=escalation_created: got %d items, want >=3 (A,B,C)", len(items))
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.Event.Type != escType {
			t.Fatalf("poll item type = %q, want %q (type filter leaked)", it.Event.Type, escType)
		}
		_, id := splitSubject(it.Event.Subject)
		seen[id] = true
	}
	for _, want := range []string{idA, idB, idC} {
		if !seen[want] {
			t.Fatalf("poll eventType filter missing escalation %s (saw %v)", want, seen)
		}
	}
	// cursorKind is the opaque source kind (§25.5 buffer/redis/mixed). This
	// binary runs with --redis-url wired and Redis reachable, so the healthy
	// read surface serves the merged cross-replica view from the Redis
	// ops:events:stream and reports the "redis" source kind.
	if page.cursorKind != "redis" {
		t.Fatalf("poll pagination.cursorKind = %q, want redis", page.cursorKind)
	}
	if page.headCursor == "" {
		t.Fatalf("poll pagination.headCursor empty, want an opaque cursor")
	}
	if page.cursor == "" {
		t.Fatalf("poll pagination.cursor empty, want an opaque cursor")
	}

	sev := pollEvents(t, base, "severity=critical")
	if len(sev.items) == 0 {
		t.Fatalf("poll severity=critical: no items, want the critical escalation")
	}
	sawCritical := false
	for _, it := range sev.items {
		if it.Event.Severity != "critical" {
			t.Fatalf("poll severity=critical returned severity %q (filter leaked)", it.Event.Severity)
		}
		if _, id := splitSubject(it.Event.Subject); id == idA {
			sawCritical = true
		}
	}
	if !sawCritical {
		t.Fatalf("poll severity=critical did not return the critical escalation %s", idA)
	}

	// ---- bullet 2: unrecognized filter token is a permanent 400 ----
	code, _, body := adminRequest(t, base, http.MethodGet,
		"/v1/admin/events?eventType=not_a_real_event_type", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("poll bad eventType: status %d, want 400 (%v)", code, body)
	}
	if gotCode := opsErrorCode(body); gotCode != "INVALID_EVENT_FILTER" {
		t.Fatalf("poll bad eventType: error code %q, want INVALID_EVENT_FILTER (%v)", gotCode, body)
	}

	// ---- bullet 5: the opaque cursor advances ----
	// Poll to the head, capture the cursor, create one more escalation,
	// then poll from that cursor. The follow-up page must contain only the
	// newly created event, proving the opaque cursor round-trips to the
	// correct position rather than replaying the whole buffer.
	head := pollEvents(t, base, "eventType=escalation_created")
	idD := createEscalation(t, base, "warning", "post-cursor escalation")
	after := pollEventsCursor(t, base, "eventType=escalation_created", head.cursor)
	if len(after.items) != 1 {
		t.Fatalf("poll after cursor: got %d items, want exactly 1 (the post-cursor escalation)", len(after.items))
	}
	if _, id := splitSubject(after.items[0].Event.Subject); id != idD {
		t.Fatalf("poll after cursor returned escalation %s, want the post-cursor %s", id, idD)
	}

	// ---- bullet 3: the Redis stream is the primary event source ----
	// Every emitted event is fanned out to the platform-scoped
	// ops:events:stream the §25.5 webhook worker and peer replicas consume.
	entries, err := rd.Client.XRange(ctx, eventbuffer.DefaultStreamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRANGE %s: %v", eventbuffer.DefaultStreamKey, err)
	}
	if len(entries) == 0 {
		t.Fatalf("Redis stream %s empty; the binary did not fan events out to the primary source", eventbuffer.DefaultStreamKey)
	}
	streamHasEscalation := false
	for _, e := range entries {
		for _, v := range e.Values {
			if s, ok := v.(string); ok && strings.Contains(s, "escalation_created") {
				streamHasEscalation = true
			}
		}
	}
	if !streamHasEscalation {
		t.Fatalf("Redis stream %s carries no escalation_created event; primary-source fan-out broken", eventbuffer.DefaultStreamKey)
	}
}

// assertEscalationFrame checks that an SSE frame carries a well-formed
// escalation_created CloudEvent whose id matches the frame's id: line and
// whose subject names the given escalation.
func assertEscalationFrame(t *testing.T, f opsSSEFrame, wantType, wantEscalationID string) {
	t.Helper()
	if f.eventType != wantType {
		t.Fatalf("SSE frame event: %q, want %q", f.eventType, wantType)
	}
	if f.id == "" {
		t.Fatalf("SSE frame missing id: line (the CloudEvents eventKey)")
	}
	var ce events.OperationalEvent
	if err := json.Unmarshal(f.data, &ce); err != nil {
		t.Fatalf("SSE frame data is not a CloudEvents record: %v (%s)", err, f.data)
	}
	if ce.SpecVersion != events.CloudEventsSpecVersion {
		t.Fatalf("SSE CloudEvent specversion = %q, want %q", ce.SpecVersion, events.CloudEventsSpecVersion)
	}
	if ce.Type != wantType {
		t.Fatalf("SSE CloudEvent type = %q, want %q", ce.Type, wantType)
	}
	if ce.ID != f.id {
		t.Fatalf("SSE CloudEvent id %q != frame id: line %q", ce.ID, f.id)
	}
	if _, id := splitSubject(ce.Subject); id != wantEscalationID {
		t.Fatalf("SSE CloudEvent subject %q does not name escalation %s", ce.Subject, wantEscalationID)
	}
}

// polledPage is the decoded §25.5 polling response fields the assertions
// read.
type polledPage struct {
	items      []events.BufferedEvent
	cursor     string
	cursorKind string
	headCursor string
}

func pollEvents(t *testing.T, base, query string) polledPage {
	t.Helper()
	return pollEventsCursor(t, base, query, "")
}

func pollEventsCursor(t *testing.T, base, query, cursor string) polledPage {
	t.Helper()
	path := "/v1/admin/events?" + query
	if cursor != "" {
		path += "&cursor=" + cursor
	}
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status %d (%s)", path, resp.StatusCode, raw)
	}
	var decoded struct {
		Items      []events.BufferedEvent `json:"items"`
		Pagination struct {
			Cursor     string `json:"cursor"`
			CursorKind string `json:"cursorKind"`
			HeadCursor string `json:"headCursor"`
		} `json:"pagination"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode poll response: %v", err)
	}
	return polledPage{
		items:      decoded.Items,
		cursor:     decoded.Pagination.Cursor,
		cursorKind: decoded.Pagination.CursorKind,
		headCursor: decoded.Pagination.HeadCursor,
	}
}

// splitSubject splits a CloudEvents subject "type/id" into its segments,
// matching the §16.6 subject convention the emitter uses
// (escalation/<id>).
func splitSubject(subject string) (resType, resID string) {
	if i := strings.Index(subject, "/"); i >= 0 {
		return subject[:i], subject[i+1:]
	}
	return subject, ""
}

// opsErrorCode reads the canonical error envelope's error.code field.
func opsErrorCode(body map[string]any) string {
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		return ""
	}
	code, _ := errObj["code"].(string)
	return code
}

// TestOpsEventSubscriptionWebhookSSRFE2E boots cmd/lenny-ops against real
// Postgres and Redis and drives the §25.5 webhook-subscription surface:
// callback URLs that fail the SSRF/DNS-rebinding validation are rejected
// with WEBHOOK_VALIDATION_FAILED at creation time, a subscription with a
// conformant HTTPS callback is created with the high-entropy secret
// returned exactly once alongside the single-use notice, the read
// endpoints omit the secret and expose only its fingerprint, the row is
// persisted to ops_event_subscriptions, and a DELETE removes it.
//
// spec: §25.5 (SSRF and DNS Rebinding Protections) — "Scheme: HTTPS only
// (HTTP allowed only when ops.webhooks.allowHTTP: true, off by default).
// Host: must be a registered domain (not a raw IPv4 or IPv6 literal) ...
// link-local/private addresses are rejected"; (Error Codes)
// "WEBHOOK_VALIDATION_FAILED | PERMANENT | 422 | Callback URL failed SSRF
// validation"; (Webhook Secret Lifecycle) "generates a secret
// server-side ... and returns the plaintext secret once in the response
// body alongside a clear single-use notice ... never returned on read
// endpoints (GET ... omit the secret field entirely)".
//
// diagnosis: a failure means the cmd/lenny-ops composition root did not
// gate subscription creation with the §25.5 SSRF validator, or the secret
// lifecycle diverged. Either a private/loopback/non-HTTPS callback URL
// was accepted (an SSRF hole), the create response did not return the
// secret once with the rotation warning, a read endpoint leaked the
// secret, or the subscription did not persist to ops_event_subscriptions.
func TestOpsEventSubscriptionWebhookSSRFE2E(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	ops := opsprocess.StartWith(
		t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
	)
	base := ops.BaseURL()
	ctx := context.Background()

	// ---- bullet 4: SSRF-validated callback URLs are rejected ----
	// Each of these fails a distinct §25.5 mitigation: non-HTTPS scheme,
	// loopback host, a raw private-IP literal, and the AWS metadata
	// endpoint. All must be rejected at creation with the permanent 422.
	rejected := []struct {
		name string
		url  string
	}{
		{"non-https scheme", "http://hooks.example.com/lenny"},
		{"loopback host", "https://localhost/lenny"},
		{"private ip literal", "https://10.0.0.5/lenny"},
		{"metadata host", "https://metadata.google.internal/lenny"},
	}
	for _, rc := range rejected {
		code, _, body := adminRequest(t, base, http.MethodPost, "/v1/admin/event-subscriptions", map[string]any{
			"callbackUrl": rc.url,
		})
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("create subscription (%s): status %d, want 422 (%v)", rc.name, code, body)
		}
		if gotCode := opsErrorCode(body); gotCode != "WEBHOOK_VALIDATION_FAILED" {
			t.Fatalf("create subscription (%s): error code %q, want WEBHOOK_VALIDATION_FAILED (%v)", rc.name, gotCode, body)
		}
	}

	// ---- bullet 4: a conformant HTTPS callback is created once ----
	// example.com resolves to a public IP, so it passes the resolved-IP
	// SSRF check. The response returns the plaintext secret exactly once
	// with the single-use rotation notice.
	const callback = "https://example.com/lenny-webhook"
	code, _, created := adminRequest(t, base, http.MethodPost, "/v1/admin/event-subscriptions", map[string]any{
		"callbackUrl": callback,
		"types":       []string{"escalation_created"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create valid subscription: status %d, want 201 (%v)", code, created)
	}
	subID, _ := created["id"].(string)
	if subID == "" {
		t.Fatalf("create valid subscription: no id in response %v", created)
	}
	secret, _ := created["secret"].(string)
	if secret == "" {
		t.Fatalf("create valid subscription: secret not returned once (%v)", created)
	}
	if warn, _ := created["secretRotationWarning"].(string); warn == "" {
		t.Fatalf("create valid subscription: missing single-use secretRotationWarning (%v)", created)
	}
	if cb, _ := created["callbackUrl"].(string); cb != callback {
		t.Fatalf("create valid subscription: callbackUrl = %q, want %q", cb, callback)
	}

	// ---- persistence: the row lands in ops_event_subscriptions ----
	// The stored secret is a hash and a fingerprint, never the plaintext.
	var storedURL, secretHash, fingerprint string
	err := pg.Pool.QueryRow(ctx,
		`SELECT callback_url, secret_hash, secret_fingerprint FROM ops_event_subscriptions WHERE id = $1`, subID).
		Scan(&storedURL, &secretHash, &fingerprint)
	if err != nil {
		t.Fatalf("read ops_event_subscriptions row %s: %v", subID, err)
	}
	if storedURL != callback {
		t.Fatalf("persisted callback_url = %q, want %q", storedURL, callback)
	}
	if secretHash == "" || secretHash == secret {
		t.Fatalf("persisted secret_hash must be a hash, not empty and not the plaintext secret")
	}
	if fingerprint == "" {
		t.Fatalf("persisted secret_fingerprint empty; audit cannot correlate the secret")
	}

	// ---- read endpoints omit the secret ----
	code, _, got := adminRequest(t, base, http.MethodGet, "/v1/admin/event-subscriptions/"+subID, nil)
	if code != http.StatusOK {
		t.Fatalf("get subscription: status %d, want 200 (%v)", code, got)
	}
	if _, present := got["secret"]; present {
		t.Fatalf("get subscription leaked the secret field (%v)", got)
	}
	if fp, _ := got["secretFingerprint"].(string); fp != fingerprint {
		t.Fatalf("get subscription fingerprint = %q, want %q", fp, fingerprint)
	}

	code, _, list := adminRequest(t, base, http.MethodGet, "/v1/admin/event-subscriptions", nil)
	if code != http.StatusOK {
		t.Fatalf("list subscriptions: status %d, want 200 (%v)", code, list)
	}
	subs, _ := list["subscriptions"].([]any)
	found := false
	for _, s := range subs {
		m, _ := s.(map[string]any)
		if id, _ := m["id"].(string); id == subID {
			found = true
			if _, present := m["secret"]; present {
				t.Fatalf("list subscriptions leaked the secret field (%v)", m)
			}
		}
	}
	if !found {
		t.Fatalf("list subscriptions did not include the created subscription %s", subID)
	}

	// ---- delete removes the row ----
	code, _, _ = adminRequest(t, base, http.MethodDelete, "/v1/admin/event-subscriptions/"+subID, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete subscription: status %d, want 204", code)
	}
	var remaining int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM ops_event_subscriptions WHERE id = $1`, subID).Scan(&remaining); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("subscription row still present after delete (%d rows)", remaining)
	}

	_ = rd // Redis is required for the binary's §25.5 wiring even though
	// this test exercises the CRUD/SSRF surface rather than the stream.
}
