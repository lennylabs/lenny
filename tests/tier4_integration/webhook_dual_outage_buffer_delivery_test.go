//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.15 "Postgres + Redis both down"
// degraded-mode guarantee that webhook delivery continues from the
// gateway event buffer when lenny-ops was running with a populated
// subscription cache before the outage.
//
// The §25.5 cold-start suite (event_subscription_cold_start_test.go)
// covers the Postgres-down empty-cache case, and
// escalation_webhook_under_outage_test.go covers Postgres-down with
// Redis still up (the escalation_created event travels the live Redis
// ops:events:stream the webhook worker fans out from). Neither composes
// the §25.15 dual-outage case the failure-mode table names: with both
// Postgres and Redis unreachable and a subscription cached before the
// outage, a gateway-originated operational event must still reach the
// subscriber, delivered from the gateway in-memory event buffer rather
// than the (down) Redis stream. This test adds that composed proof.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	eventsubpgstore "github.com/lennylabs/lenny/pkg/ops/eventsubscription/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestWebhookDeliveredFromGatewayBufferDuringDualOutage boots the real
// cmd/lenny-ops binary against Postgres and Redis with a webhook
// subscription persisted before boot (so the §25.5 subscription cache
// loads it — the spec's "populated cache before the outage"
// precondition), then takes both Postgres and Redis down and emits a
// gateway-originated operational event (an escalation_created, produced
// by creating an escalation against the ops surface). With the Redis
// ops:events:stream gone, the §25.5 webhook worker must source the event
// from the gateway in-memory event buffer and still deliver it,
// HMAC-signed, to the receiver stub the cached subscription names.
//
// spec: §25.15 (Failure Mode Analysis) — "Postgres + Redis both down:
// Core operational loop still functions in degraded mode: event stream
// via gateway buffer ... webhook delivery from buffer with cached
// subscriptions provided lenny-ops was running with a populated cache
// before the outage (a lenny-ops cold start during the outage produces
// an empty subscription cache and no webhook deliveries until Postgres
// recovers)."
//
// diagnosis: a failure means the §25.15 dual-outage webhook-delivery
// guarantee is broken as a composed flow. With both stores down the
// webhook worker's only wired event source (the Redis ops:events:stream)
// is unreachable and no gateway-buffer fall-back source delivers the
// event, so a subscriber that was cached before the outage receives no
// notification during exactly the storage outage the spec promises to
// keep delivering through.
func TestWebhookDeliveredFromGatewayBufferDuringDualOutage(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	// Non-blocking skip tied to an open TEST-GAPS finding: lenny-ops
	// wires the §25.5 webhook worker's only event source to the Redis
	// ops:events:stream (cmd/lenny-ops/events_wiring.go — RedisEventSource
	// when Redis was up at start, otherwise emptyEventSource). No
	// gateway-buffer fall-back source exists, so during a Redis outage the
	// worker's Poll returns an error and delivers nothing. Whether and how
	// lenny-ops should source gateway-buffered events for webhook delivery
	// during a Redis outage (the §25.15 promise) is a product-behavior
	// change whose mechanism the spec under-specifies; it is left for
	// human review through the change-proposal pipeline. Un-skip once the
	// gateway-buffer delivery path is implemented.
	t.Skip("§25.15 dual-outage webhook delivery from the gateway event buffer is not wired in lenny-ops (the webhook worker's only event source is the Redis stream); pending a human decision on the delivery mechanism")

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	// ---- receiver stub the cached subscription points at ----
	received := make(chan receivedWebhook, 8)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		select {
		case received <- receivedWebhook{
			signature: r.Header.Get("X-Lenny-Signature"),
			eventType: r.Header.Get("X-Lenny-Event-Type"),
			body:      raw,
		}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	escalationType := events.EventEscalationCreated.CloudEventsType()

	// ---- seed the subscription before boot so the cache loads it ----
	// Persisted directly through the store so it is present when lenny-ops
	// starts and its §25.5 subscription cache performs the initial load —
	// the "populated cache before the outage" precondition the spec names.
	now := time.Now().UTC()
	if err := eventsubpgstore.New(pg.Pool).Create(ctx, eventsubscription.Record{
		ID:                "sub-dual-outage",
		CallbackURL:       receiver.URL,
		Types:             []string{escalationType},
		SecretHash:        "",
		SecretFingerprint: "",
		CreatedBy:         "alice@acme.com",
		TenantFilter:      eventsubscription.TenantFilterAll,
		Generation:        1,
		CreatedAt:         now,
		UpdatedAt:         now,
		Active:            true,
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	// ---- boot lenny-ops with the cache populated from Postgres ----
	// --webhook-allow-http admits the plaintext receiver stub; the §25.5
	// delivery-time SSRF policy is exercised elsewhere and is not the
	// behavior under test here.
	ops := opsprocess.StartWith(
		t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
		"--webhook-allow-http",
	)
	base := ops.BaseURL()
	client := &http.Client{Timeout: 30 * time.Second}

	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequestWithContext(ctx, method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out
	}

	// ---- inject the dual outage: stop Postgres and Redis ----
	// The Redis ops:events:stream the webhook worker consumes is now gone;
	// only the gateway in-memory buffer carries operational events.
	pg.Stop(t)
	rd.Stop(t)

	// ---- emit a gateway-originated event during the outage ----
	// Creating an escalation with both stores down lands the record
	// in-memory (202 Accepted) and emits escalation_created; the spec
	// promises the cached subscription is still notified from the buffer.
	var escID string
	createDeadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(createDeadline) {
		code, esc := do(http.MethodPost, "/v1/admin/escalations", map[string]any{
			"severity": "critical",
			"summary":  "dual outage: subscriber cached before the outage must still be notified",
		})
		if code == http.StatusCreated || code == http.StatusAccepted {
			escID, _ = esc["id"].(string)
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if escID == "" {
		t.Fatal("escalation create never succeeded in degraded mode during the dual outage")
	}

	// ---- delivery: the escalation_created webhook reaches the receiver ----
	deadline := time.Now().Add(30 * time.Second)
	var got *receivedWebhook
	for time.Now().Before(deadline) && got == nil {
		select {
		case r := <-received:
			got = &r
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
	if got == nil {
		t.Fatalf("escalation_created webhook for %s was not delivered from the gateway buffer during the dual outage", escID)
	}
	if got.eventType != escalationType {
		t.Errorf("delivered X-Lenny-Event-Type = %q, want %q", got.eventType, escalationType)
	}
}
