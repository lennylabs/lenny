//go:build component

// SPDX-License-Identifier: MIT

// Tier-4 integration test: the real cmd/lenny-gateway binary wires a
// live subsystem (the circuit-breaker handler) to the operational-event
// stream. Inducing the state change through the admin API must deliver
// the documented dev.lenny.* CloudEvent to a consumer reading the
// gateway-side event buffer at GET /v1/admin/events/buffer. This closes
// the §4.0 gap that the EventEmitter wiring on existing subsystems was
// verified only at unit tier per subsystem, with no component-or-higher
// test driving a real state change to a delivered operational event.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: §4.0 (EventEmitter wiring on existing subsystems) — "The
// subsystems below take EventEmitter as a constructor dependency and
// call Emit(ctx, event) at the documented state-change point ...
// Circuit breaker handler (§11.6) — emits circuit_breaker_opened /
// circuit_breaker_closed on state transition." §25.3 exposes the
// gateway-side buffer at GET /v1/admin/events/buffer, and §16.6 fixes
// the CloudEvents type as dev.lenny.<short_name>.
//
// diagnosis: A failure means the live gateway does not deliver a
// subsystem's operational event to the event-buffer consumer. Either
// the circuit-breaker handler is not constructed with the shared
// EventEmitter, the emitter no longer writes into the buffer the admin
// endpoint reads, or the CloudEvents type drifted from the §16.6
// catalog. The subsystem state change succeeded (the breaker opened),
// so the break is in the event-emission wiring, not the breaker logic.
func TestOpsEventEmissionCircuitBreakerReachesBuffer(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0")
	base := gw.BaseURL()

	// Induce the state change against the live gateway: open a circuit
	// breaker through the §15.1 admin API as a platform-admin.
	const name = "rt-echo-ops-event"
	openBody, _ := json.Marshal(map[string]any{
		"reason":     "runaway runtime (ops-event)",
		"limit_tier": "runtime",
		"scope":      map[string]any{"runtime": "echo"},
	})
	adminPost(t, base+"/v1/admin/circuit-breakers/"+name+"/open", openBody)

	// The circuit-breaker handler must have emitted the documented
	// dev.lenny.circuit_breaker_opened event into the buffer the admin
	// endpoint reads.
	opened := waitForBufferEvent(t, base, "dev.lenny.circuit_breaker_opened")
	if got := gjsonString(t, opened.Event.Data, "name"); got != name {
		t.Errorf("circuit_breaker_opened data.name = %q, want %q", got, name)
	}

	// Close it: the same subsystem must emit circuit_breaker_closed on
	// the reverse transition.
	adminPost(t, base+"/v1/admin/circuit-breakers/"+name+"/close", nil)
	closed := waitForBufferEvent(t, base, "dev.lenny.circuit_breaker_closed")
	if got := gjsonString(t, closed.Event.Data, "name"); got != name {
		t.Errorf("circuit_breaker_closed data.name = %q, want %q", got, name)
	}
}

// spec: §25.3 line 687 ("Health service → health_status_changed") and
// line 706 ("health_status_changed | Aggregate health transitioned |
// Old status, new status, triggering component"). The health service
// emits the operational event when the aggregate health verdict
// transitions between Reports, and the event carries the old and new
// aggregate status. §25.3 line 677 fixes the in-memory ring buffer as
// always written regardless of Redis availability, and §25.3 exposes it
// at GET /v1/admin/events/buffer; §16.6 fixes the CloudEvents type as
// dev.lenny.<short_name>.
//
// diagnosis: A failure means a real aggregate-health transition in the
// live gateway does not deliver dev.lenny.health_status_changed to the
// event-buffer consumer, or the delivered event's old/new status does
// not match the observed transition. The health endpoint itself flipped
// (the test reads the before/after verdict off GET /v1/admin/health), so
// the break is in the OnTransition -> EventEmitter -> buffer wiring, not
// in aggregate-health computation. Note the buffer write is documented
// to succeed even with Redis unreachable, so a Redis outage inducing the
// transition must not suppress the buffered event.
func TestOpsEventEmissionHealthStatusChangedReachesBuffer(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	// A live Redis backend is registered as a health checker on the
	// aggregate, so terminating it forces a genuine aggregate-health
	// transition rather than a synthetic one.
	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0")
	base := gw.BaseURL()

	// Establish the baseline aggregate verdict. The first Report sets the
	// baseline and fires no transition (§25.3), and it caches the healthy
	// Redis probe for the per-replica probe-cache window.
	baseline := getHealthStatus(t, base)
	if baseline == "" {
		t.Fatal("baseline GET /v1/admin/health returned an empty status")
	}

	// Inject a genuine Redis outage. Subsequent Redis probes fail to
	// connect, so the aggregate verdict must transition away from the
	// baseline once the probe cache expires.
	rd.Stop(t)

	// Poll the health endpoint until the aggregate verdict flips. Each GET
	// re-runs the Aggregator's Report; once the probe cache expires the
	// re-probe of the now-dead Redis pushes the aggregate off the
	// baseline and fires the health_status_changed hook.
	var observed string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if s := getHealthStatus(t, base); s != baseline {
			observed = s
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if observed == "" {
		t.Fatalf("aggregate health never transitioned away from %q after the Redis outage", baseline)
	}

	// The transition must have delivered dev.lenny.health_status_changed
	// into the buffer, carrying the old and new aggregate status.
	ev := waitForBufferEvent(t, base, "dev.lenny.health_status_changed")
	if got := gjsonString(t, ev.Event.Data, "oldStatus"); got != baseline {
		t.Errorf("health_status_changed data.oldStatus = %q, want %q (the observed baseline verdict)", got, baseline)
	}
	if got := gjsonString(t, ev.Event.Data, "newStatus"); got != observed {
		t.Errorf("health_status_changed data.newStatus = %q, want %q (the observed post-outage verdict)", got, observed)
	}
}

// getHealthStatus reads the aggregate verdict off the §25.3
// GET /v1/admin/health full report. The health surface never returns
// 5xx (§25.3), so a non-200 is a genuine failure.
func getHealthStatus(t *testing.T, base string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		base+"/v1/admin/health", nil)
	adminHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get health: status %d, body=%s", resp.StatusCode, raw)
	}
	var report struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode health report: %v (body=%s)", err, raw)
	}
	return report.Status
}

// bufferEvent mirrors the fields of events.BufferedEvent this test
// asserts on. It decodes the §25.3 GET /v1/admin/events/buffer page
// without importing the product type, so the test pins the wire
// contract rather than the in-process struct.
type bufferEvent struct {
	ID    uint64 `json:"id"`
	Event struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	} `json:"event"`
}

type bufferPage struct {
	Events []bufferEvent `json:"events"`
}

// waitForBufferEvent polls GET /v1/admin/events/buffer until an event of
// the given CloudEvents type appears, failing the test on timeout. The
// buffer write is synchronous with the emit, so this normally returns on
// the first poll; the retry only absorbs subprocess scheduling jitter.
func waitForBufferEvent(t *testing.T, base, ceType string) bufferEvent {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		page := fetchBuffer(t, base)
		for _, e := range page.Events {
			if e.Event.Type == ceType {
				return e
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("event %q never appeared in GET /v1/admin/events/buffer", ceType)
	return bufferEvent{}
}

func fetchBuffer(t *testing.T, base string) bufferPage {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		base+"/v1/admin/events/buffer", nil)
	adminHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get events buffer: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get events buffer: status %d, body=%s", resp.StatusCode, raw)
	}
	var page bufferPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode events buffer page: %v (body=%s)", err, raw)
	}
	return page
}

func adminPost(t *testing.T, url string, body []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, r)
	req.Header.Set("Content-Type", "application/json")
	adminHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d, body=%s", url, resp.StatusCode, raw)
	}
}

func adminHeaders(req *http.Request) {
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-User-ID", "ops@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
}

// gjsonString pulls a top-level string field out of a CloudEvents data
// payload without a dependency on the product event type.
func gjsonString(t *testing.T, data json.RawMessage, key string) string {
	t.Helper()
	if len(data) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	s, _ := m[key].(string)
	return s
}
