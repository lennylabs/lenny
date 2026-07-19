//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.4 "Event Emission" degraded-mode
// guarantee: the escalation_created operational event is emitted through
// the event emitter (Redis stream + in-memory buffer) independently of
// which storage tier accepted the escalation record, so a webhook
// subscriber is still notified when Postgres is down.
//
// The unit suite (pkg/ops/opsservice/webhookloop_test.go,
// deliveryrecorder_test.go) covers the webhook worker against an
// in-memory event source, and the tier-3 contract suite pins the
// escalation create shape. Neither composes the flow that the §25.4
// Event Emission and §25.17 failure-path payoff describe: an escalation
// created while Postgres is down whose escalation_created CloudEvent
// still reaches a signed webhook receiver by way of the live Redis
// event stream the delivery worker fans out from. This test adds that
// composed proof.
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
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// receivedWebhook captures one delivery a stub webhook receiver observed.
type receivedWebhook struct {
	signature string
	eventType string
	body      []byte
}

// TestEscalationWebhookReachesReceiverWhilePostgresDown boots the real
// cmd/lenny-ops binary against Postgres and Redis, takes Postgres down,
// and creates an escalation. With Postgres unavailable the record lands
// in the Redis Tier-2 store (201, durable-redis), but the crux is the
// event path: the escalation_created CloudEvent is emitted to the
// platform Redis stream through the same event emitter the binary wires,
// regardless of the storage tier. The §25.5 webhook delivery worker
// (opsservice.WebhookWorker) consuming that stream through the production
// opsservice.RedisEventSource delivers the event, HMAC-signed, to a stub
// receiver — the "webhook subscriber still paged when Postgres is down"
// guarantee, composed end to end against real backends.
//
// The subscription-CRUD SSRF/allowlist gate (§25.5 lines 2735-2745) is
// exercised elsewhere (cmd/lenny-ops/webhook_ssrf_test.go); it blocks a
// loopback callback URL and is not part of the guarantee under test, so
// the worker here is driven with a static subscription pointed at an
// in-process receiver. The event source, the worker, the HMAC signing,
// and the escalation-create-and-emit path are all the real product code.
//
// spec: §25.4 (Event Emission) — "The escalation_created event is emitted
// through the gateway's event emitter (Redis stream + in-memory buffer),
// independently of which storage tier accepted the escalation record.
// This means a webhook subscriber (e.g., PagerDuty integration) receives
// the escalation notification even when Postgres is down — the event
// stream carries the event, and lenny-ops delivers it to cached webhook
// subscriptions."; §25.15 (Failure Mode Analysis, degraded mode); §25.17
// (Failure Path: Escalation) — "The escalation_created event is emitted
// to the event stream. A webhook subscriber routes it to PagerDuty."
//
// diagnosis: a failure means the §25.4 degraded-mode escalation-
// notification guarantee is broken as a composed flow. Either the
// cmd/lenny-ops composition root did not emit escalation_created to the
// Redis stream when Postgres was down (the storage-tier fall-through
// swallowed or skipped emission), the event never reached the webhook
// delivery worker through the production Redis event source, or the
// delivered payload was not the HMAC-signed escalation_created CloudEvent
// naming the created escalation — any of which leaves an operator unpaged
// during exactly the storage outage most likely to produce an escalation.
func TestEscalationWebhookReachesReceiverWhilePostgresDown(t *testing.T) {
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
	client := &http.Client{Timeout: 30 * time.Second}
	ctx := context.Background()

	// do issues an admin request under the dev platform-admin headers the
	// unauthenticated ops surface honours and returns the status, the
	// response headers, and the decoded top-level JSON object.
	do := func(method, path string, body any) (int, http.Header, map[string]any) {
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
		return resp.StatusCode, resp.Header, out
	}

	// ---- webhook receiver stub ----
	// A single-delivery capture receiver. It returns 200 so the §25.5
	// delivery worker treats the attempt as delivered (no retry backoff),
	// and forwards the signature, the CloudEvents type header, and the
	// raw body for signature and payload assertions.
	received := make(chan receivedWebhook, 8)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		select {
		case received <- receivedWebhook{
			signature: r.Header.Get(webhookdelivery.HeaderSignature),
			eventType: r.Header.Get(webhookdelivery.HeaderEventType),
			body:      raw,
		}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	escalationType := events.EventEscalationCreated.CloudEventsType()
	secret := []byte("degraded-mode-webhook-hmac-secret")

	// ---- precondition: with Postgres up the durable tier serves ----
	// This makes the post-outage behavior attributable to the injected
	// failure rather than to a subsystem that never reached a durable
	// store. The emit for this escalation is already on the Redis stream
	// by the time the POST returns; priming the event source after this
	// point snaps the read cursor past it so the worker only delivers the
	// degraded-mode escalation created below.
	code, hdr, esc := do(http.MethodPost, "/v1/admin/escalations", map[string]any{
		"severity": "critical",
		"summary":  "precondition: escalation lands durably while Postgres is up",
	})
	if code != http.StatusCreated {
		t.Fatalf("precondition POST /v1/admin/escalations: status %d, want 201; body %v", code, esc)
	}
	if got := hdr.Get("X-Lenny-Persistence"); got != "durable-postgres" {
		t.Fatalf("precondition escalation X-Lenny-Persistence = %q, want durable-postgres", got)
	}

	// ---- wire the production webhook delivery worker to the live stream ----
	// The event source and the worker are the real §25.5 product code; the
	// worker fans the escalation_created event out from the same
	// ops:events:stream the binary emits to. Priming the source now snaps
	// its per-process cursor to the current tail, so only events appended
	// after this point (the degraded-mode escalation) are delivered.
	source := opsservice.NewRedisEventSource(rd.Client, eventbuffer.DefaultStreamKey)
	if _, err := source.Poll(ctx); err != nil {
		t.Fatalf("prime Redis event source: %v", err)
	}
	worker := opsservice.NewWebhookWorker(opsservice.WebhookWorkerConfig{
		Events: source,
		Subscriptions: staticSubscriptions{{
			ID:           "sub-degraded-page",
			CallbackURL:  receiver.URL,
			Secret:       secret,
			Types:        []string{escalationType},
			TenantFilter: "*",
		}},
	})

	// ---- inject: stop Postgres, leave Redis up ----
	// The event stream (Redis) still carries operational events; only the
	// Tier-1 escalation store is gone.
	pg.Stop(t)

	// parse extracts the CloudEvents type, subject, and inner escalation id
	// from a delivered webhook body.
	parse := func(body []byte) (ceType, subject, dataID string) {
		var ce struct {
			Type    string `json:"type"`
			Subject string `json:"subject"`
			Data    struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(body, &ce)
		return ce.Type, ce.Subject, ce.Data.ID
	}

	// ---- degraded: escalation lands in the Redis tier, event still emitted ----
	// §25.4 create path: with Postgres down and Redis up the record lands
	// in the Tier-2 Redis store (201, durable-redis), and emission to the
	// stream is independent of the accepting tier. The create is retried
	// across the Postgres shutdown transition: the pool may briefly surface
	// a server-terminated-connection error before it settles into the
	// connection-refused steady state the degraded-mode guarantee targets.
	var escID string
	createDeadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(createDeadline) {
		code, hdr, esc = do(http.MethodPost, "/v1/admin/escalations", map[string]any{
			"severity":    "critical",
			"alertName":   "WarmPoolExhausted",
			"runbookName": "warm-pool-exhaustion",
			"summary":     "Postgres down: operator must still be paged for this escalation",
		})
		if code == http.StatusCreated {
			if got := hdr.Get("X-Lenny-Persistence"); got != "durable-redis" {
				t.Errorf("degraded escalation X-Lenny-Persistence = %q, want durable-redis (Redis Tier-2 with Postgres down)", got)
			}
			escID, _ = esc["id"].(string)
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if escID == "" {
		t.Fatalf("degraded escalation create never fell through to the Redis tier while Postgres was down (last status %d: %v)", code, esc)
	}

	// ---- delivery: the escalation_created CloudEvent reaches the receiver ----
	// Drive the §25.5 worker until the stub receiver observes the delivery
	// of the escalation created above, or the window elapses. The worker
	// polls the stream each tick; each delivered escalation_created is
	// matched by its inner escalation id, so a delivery from a superseded
	// create attempt during the shutdown transition is skipped.
	var got *receivedWebhook
	deadline := time.Now().Add(20 * time.Second)
poll:
	for time.Now().Before(deadline) {
		if err := worker.Tick(ctx); err != nil {
			t.Fatalf("webhook worker tick: %v", err)
		}
		for drained := true; drained; {
			select {
			case r := <-received:
				if _, _, dataID := parse(r.body); dataID == escID {
					got = &r
					break poll
				}
			default:
				drained = false
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if got == nil {
		t.Fatalf("escalation_created webhook for %s not delivered within the window while Postgres was down", escID)
	}

	// The delivery carries the escalation_created type both in the
	// X-Lenny-Event-Type header and in the CloudEvents envelope.
	if got.eventType != escalationType {
		t.Errorf("delivered X-Lenny-Event-Type = %q, want %q", got.eventType, escalationType)
	}
	// §25.5 HMAC: X-Lenny-Signature is the hex HMAC-SHA256 of the exact
	// body under the subscription secret; a receiver validates it before
	// trusting the notification.
	if want := webhookdelivery.Sign(secret, got.body); got.signature != want {
		t.Errorf("delivered X-Lenny-Signature = %q, want %q (HMAC over the delivered body)", got.signature, want)
	}
	// The delivered body is the escalation_created CloudEvent naming the
	// escalation created while Postgres was down.
	ceType, subject, dataID := parse(got.body)
	if ceType != escalationType {
		t.Errorf("delivered CloudEvent type = %q, want %q", ceType, escalationType)
	}
	if subject != "escalation/"+escID {
		t.Errorf("delivered CloudEvent subject = %q, want escalation/%s", subject, escID)
	}
	if dataID != escID {
		t.Errorf("delivered escalation_created names escalation %q, want %q", dataID, escID)
	}
}

// staticSubscriptions is a fixed opsservice.SubscriptionSource for
// driving the §25.5 webhook worker in-process against a known callback.
type staticSubscriptions []opsservice.WebhookSubscription

// Subscriptions implements opsservice.SubscriptionSource.
func (s staticSubscriptions) Subscriptions() []opsservice.WebhookSubscription {
	return []opsservice.WebhookSubscription(s)
}
