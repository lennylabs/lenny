// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §12.9.5 SSRF and DNS-rebinding test for the §25.5
// /v1/admin/event-subscriptions webhook delivery path. The unit tests in
// pkg/ops/eventsubscription exercise the callback-URL validator in
// isolation with a fixed resolver; they never drive a delivery attempt
// through the production Transport whose guard re-resolves the host, and
// they never exercise the sequence a rebinding attacker relies on: a host
// that resolves to a public address at subscription-create time and to a
// loopback/IMDS/private address at delivery time.
//
// This test wires the production components the lenny-ops delivery worker
// wires (eventsubscription.SSRFValidator as the per-delivery guard on
// webhookdelivery.Transport, driven by opsservice.WebhookWorker) against a
// resolver whose answer flips between create time and delivery time. It
// asserts the create-time validation admits the public host, the delivery
// guard rejects the rebound host with WEBHOOK_VALIDATION_FAILED before any
// HTTP request leaves the process (StatusCode stays 0), and the worker
// marks the delivery failed. It also asserts the userinfo-IP-literal and
// cloud-metadata-hostname smuggling forms are rejected at both create and
// delivery time.

package tier9_security_test

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// publicWebhookIP is a globally routable address a legitimate webhook
// receiver could resolve to at subscription-create time.
const publicWebhookIP = "93.184.216.34"

// rebindResolver is a controllable DNS seam for the §25.5 callback-URL
// guard: it returns whichever address was last set, so a test can resolve
// a host to a public address at create time and flip it to a
// loopback/IMDS/private address before the delivery guard re-resolves.
// This is the adversary's DNS server in the §12.9.5 rebinding scenario.
type rebindResolver struct {
	mu   sync.Mutex
	addr string
}

func (r *rebindResolver) set(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addr = addr
}

func (r *rebindResolver) LookupNetIP(_ context.Context, _, _ string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []netip.Addr{netip.MustParseAddr(r.addr)}, nil
}

// spec: §25.5 (SSRF and DNS Rebinding Protections) — "Callback URL
// validation runs both at subscription creation AND at each delivery
// attempt"; "the HTTP client resolves the host per request and checks the
// resolved IP is not in any of: RFC 1918 ..., RFC 3927 (169.254/16),
// loopback ...; The check happens on every delivery — this closes the DNS
// rebinding gap where a URL that resolves to a legitimate IP at
// subscription time later resolves to 127.0.0.1"; and cloud metadata
// services "are explicitly blocked regardless of other rules."
//
// diagnosis: a failure means a webhook subscription whose callback host is
// public at create time can be rebound to loopback, an RFC1918 address, or
// the cloud instance-metadata endpoint and still have a delivery attempt
// dispatched to it — the §25.5 per-delivery re-resolution defense against
// DNS rebinding is not enforced on the live delivery path, so lenny-ops is
// an SSRF pivot into the cluster or the node's IMDS.
func TestEventWebhookDNSRebindingRejectedAtDelivery_spec_25_5(t *testing.T) {
	ctx := context.Background()

	// Each vector registers a host that resolves public at create time, then
	// rebinds it to a blocked address before the delivery guard runs.
	rebindVectors := []struct {
		name     string
		host     string
		rebindTo string
	}{
		{"rebind-to-loopback", "https://rebind-loopback.acme.test/hook", "127.0.0.1"},
		{"rebind-to-imds", "https://rebind-imds.acme.test/hook", "169.254.169.254"},
		{"rebind-to-rfc1918", "https://rebind-private.acme.test/hook", "10.0.0.7"},
	}
	for _, vec := range rebindVectors {
		t.Run(vec.name, func(t *testing.T) {
			res := &rebindResolver{addr: publicWebhookIP}
			validator := eventsubscription.NewSSRFValidator(eventsubscription.SSRFConfig{Resolver: res})
			// The production Transport guard is the same validator, re-run per
			// delivery attempt (cmd/lenny-ops wires NewTransport(...).WithSSRFGuard(SSRF.Validate)).
			transport := webhookdelivery.NewTransport(2 * time.Second).WithSSRFGuard(validator.Validate)

			// Create-time validation admits the host: it resolves public.
			if err := validator.Validate(ctx, vec.host); err != nil {
				t.Fatalf("create-time validation rejected a public-resolving host %q: %v (the rebinding "+
					"premise requires the subscription to be admitted at create time)", vec.host, err)
			}

			// The attacker's DNS flips the answer before delivery.
			res.set(vec.rebindTo)

			out := transport.Deliver(ctx, webhookdelivery.Delivery{
				CallbackURL: vec.host, Body: []byte(`{}`), Secret: []byte("whsec"),
			})
			if out.Delivered() {
				t.Fatalf("§12.9.5 violation: delivery to %q succeeded after the host rebound to %s; the "+
					"per-delivery SSRF guard did not re-resolve", vec.host, vec.rebindTo)
			}
			if out.Err == nil {
				t.Fatalf("§12.9.5 violation: delivery to a host rebound to %s returned no error", vec.rebindTo)
			}
			// StatusCode is only set from an HTTP response; a zero code proves the
			// guard rejected the attempt before any request left the process.
			if out.StatusCode != 0 {
				t.Errorf("§12.9.5: rebound-host delivery returned HTTP status %d; the receiver was contacted "+
					"despite the SSRF guard", out.StatusCode)
			}
			if code := eventsubscription.CodeOf(out.Err); code != eventsubscription.ErrCodeWebhookValidation {
				t.Errorf("§12.9.5: rebound-host delivery failed with code %q, want %q (err %v)",
					code, eventsubscription.ErrCodeWebhookValidation, out.Err)
			}
		})
	}

	// Host-based smuggling forms are rejected at create time and again at
	// delivery time, independent of the resolver's answer.
	hostVectors := []struct {
		name string
		host string
	}{
		// spec: §25.5 — "rejecting URLs whose path segment contains an IP
		// literal (https://example.com@127.0.0.1/ ...)". url.Parse's Hostname()
		// returns the real host 127.0.0.1, so the userinfo public label does
		// not smuggle the request past the IP-literal check.
		{"userinfo-ip-literal-bypass", "https://public.acme.test@127.0.0.1/hook"},
		// spec: §25.5 — cloud instance metadata hostnames "are explicitly
		// blocked regardless of other rules."
		{"cloud-metadata-hostname", "https://metadata.google.internal/hook"},
	}
	for _, vec := range hostVectors {
		t.Run(vec.name, func(t *testing.T) {
			// A resolver that would answer public if consulted; the host-based
			// checks reject before resolution, so the answer must not matter.
			res := &rebindResolver{addr: publicWebhookIP}
			validator := eventsubscription.NewSSRFValidator(eventsubscription.SSRFConfig{Resolver: res})
			transport := webhookdelivery.NewTransport(2 * time.Second).WithSSRFGuard(validator.Validate)

			if err := validator.Validate(ctx, vec.host); err == nil {
				t.Fatalf("§12.9.5 violation: create-time validation admitted a smuggling URL %q", vec.host)
			}
			out := transport.Deliver(ctx, webhookdelivery.Delivery{
				CallbackURL: vec.host, Body: []byte(`{}`), Secret: []byte("whsec"),
			})
			if out.Delivered() || out.Err == nil {
				t.Fatalf("§12.9.5 violation: delivery to a smuggling URL %q was not rejected (out %+v)",
					vec.host, out)
			}
			if out.StatusCode != 0 {
				t.Errorf("§12.9.5: smuggling-URL delivery returned HTTP status %d; the receiver was contacted",
					out.StatusCode)
			}
			if code := eventsubscription.CodeOf(out.Err); code != eventsubscription.ErrCodeWebhookValidation {
				t.Errorf("§12.9.5: smuggling-URL delivery failed with code %q, want %q (err %v)",
					code, eventsubscription.ErrCodeWebhookValidation, out.Err)
			}
		})
	}
}

// oneShotEvents is an EventSource that yields its events on the first Poll
// and nothing after, so a single worker tick delivers a fixed set.
type oneShotEvents struct {
	events []opsservice.WebhookEvent
	done   bool
}

func (o *oneShotEvents) Poll(context.Context) ([]opsservice.WebhookEvent, error) {
	if o.done {
		return nil, nil
	}
	o.done = true
	return o.events, nil
}

// fixedSubs is a static SubscriptionSource.
type fixedSubs []opsservice.WebhookSubscription

func (f fixedSubs) Subscriptions() []opsservice.WebhookSubscription { return f }

// rebindDeliveryRecorder captures the failed flag of each terminal
// delivery the worker persists.
type rebindDeliveryRecorder struct {
	mu     sync.Mutex
	failed []bool
}

func (r *rebindDeliveryRecorder) RecordDelivery(_ context.Context, _, _ string, _ int, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = append(r.failed, failed)
}

// spec: §25.5 (SSRF and DNS Rebinding Protections) — the per-delivery
// re-resolution "closes the DNS rebinding gap where a URL that resolves to
// a legitimate IP at subscription time later resolves to 127.0.0.1." When
// the guard rejects the rebound host on the live delivery worker path, the
// delivery must be marked failed rather than silently dropped or delivered.
//
// diagnosis: a failure means the §25.5 webhook delivery worker
// (opsservice.WebhookWorker) does not surface a guard-rejected rebinding
// attempt as a failed delivery — either it dispatches the request to the
// rebound (loopback) address or it records the outcome as anything other
// than a failure, so an operator watching delivery health cannot see that
// a rebinding SSRF attempt was blocked.
func TestEventWebhookRebindingDeliveryMarkedFailed_spec_25_5(t *testing.T) {
	ctx := context.Background()

	res := &rebindResolver{addr: publicWebhookIP}
	validator := eventsubscription.NewSSRFValidator(eventsubscription.SSRFConfig{Resolver: res})
	transport := webhookdelivery.NewTransport(2 * time.Second).WithSSRFGuard(validator.Validate)

	const callback = "https://rebind-worker.acme.test/hook"
	// Admitted at create time: the host resolves public.
	if err := validator.Validate(ctx, callback); err != nil {
		t.Fatalf("create-time validation rejected a public-resolving host: %v", err)
	}
	// Rebound to loopback before the worker delivers.
	res.set("127.0.0.1")

	rec := &rebindDeliveryRecorder{}
	var failedSub, failedEvent string
	worker := opsservice.NewWebhookWorker(opsservice.WebhookWorkerConfig{
		Events: &oneShotEvents{events: []opsservice.WebhookEvent{
			{ID: "evt-rebind", Type: "dev.lenny.alert_fired", Body: []byte(`{"id":"evt-rebind"}`)},
		}},
		Subscriptions: fixedSubs{{
			ID: "sub-rebind", CallbackURL: callback, Secret: []byte("whsec"),
			Types: []string{"dev.lenny.alert_fired"}, TenantFilter: "*",
		}},
		Transport:    transport,
		Recorder:     rec,
		TrackingMode: webhookdelivery.TrackingFull,
		EmitFailure: func(subID, eventID string) {
			failedSub, failedEvent = subID, eventID
		},
	})

	if err := worker.Tick(ctx); err != nil {
		t.Fatalf("worker Tick: %v", err)
	}

	rec.mu.Lock()
	records := append([]bool(nil), rec.failed...)
	rec.mu.Unlock()
	if len(records) != 1 {
		t.Fatalf("recorded %d deliveries, want 1", len(records))
	}
	if !records[0] {
		t.Errorf("§12.9.5: the rebound-host delivery was recorded as succeeded, want failed")
	}
	if failedSub != "sub-rebind" || failedEvent != "evt-rebind" {
		t.Errorf("§12.9.5: event_delivery_failed emitted for sub %q event %q, want sub-rebind/evt-rebind",
			failedSub, failedEvent)
	}
}
