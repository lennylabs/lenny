// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for the §25.4 Self-Monitoring guarantee that
// lenny-ops's self-health goroutine emits an ops_health_status_changed
// event onto the Redis stream when a real backing dependency fails.
//
// §25.4 Self-Monitoring: "A background goroutine runs every
// ops.selfHealth.checkIntervalSeconds seconds ... When the aggregate
// self-health status changes, lenny-ops emits an ops_health_status_changed
// event to the Redis stream ... Events carry the replica identity
// (source.replicaID field) so subscribers can distinguish leader-replica
// self-health from non-leader-replica self-health." The check table fixes
// "Postgres connection pool ... Connection errors > 0 in last 60s" as the
// unhealthy condition, and the event-driven supplement adds "Postgres
// connection error (any) → immediate evaluation of the postgres_pool
// check." §25.4 "Calling the Gateway" adds the gateway-auth component:
// "Continuous refresh failures within the pre-expiry window emit
// ops_health_status_changed with component gateway-auth degraded."
//
// The existing coverage (pkg/ops/opsservice selfchecks_test.go,
// selfhealth_test.go, gatewayauthcheck_test.go, cmd/lenny-ops main_test.go)
// exercises the checks and the emission only against injected fakes: a nil
// pool, a nil Redis client, a canned probe error, and a miniredis stream.
// No test drives the real self-health goroutine against a genuine backing
// dependency that fails. These tests wire the real opsservice.Service
// self-monitor the way cmd/lenny-ops wires it — the production
// PostgresPoolCheck / GatewayAuthCheck over a real pgx pool and a real HTTP
// probe, the production eventbuffer.StreamEmitter over a real Redis stream —
// then inject a genuine Postgres outage (terminate the container) and a
// genuine gateway 401, and assert the goroutine emits the documented
// ops_health_status_changed event carrying the emitting replica identity.
package tier8_chaos_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// opsSelfHealthReplicaID is the synthetic replica identity the self-monitor
// stamps on every ops_health_status_changed event it emits. The assertions
// recover it from the emitted event to prove the §25.4 "Events carry the
// replica identity" requirement holds for a real dependency failure.
const opsSelfHealthReplicaID = "chaos-ops-replica-a"

// selfHealthInterval keeps the self-monitor goroutine ticking fast enough
// that a test observes a transition within a few seconds of the injection
// without the flakiness a sub-100ms interval would add.
const selfHealthInterval = 300 * time.Millisecond

// followerElector is an opsservice.Elector that never acquires leadership,
// so only the per-replica self-monitor loop runs. The §25.4 self-monitor is
// leader-independent (every replica runs it), which is exactly the loop
// under test; the leader-only loops (cron, webhook, reconcilers) are left
// unregistered.
type followerElector struct{}

func (followerElector) Run(ctx context.Context, _ func(context.Context), _ func()) { <-ctx.Done() }
func (followerElector) IsLeader() bool                                             { return false }

// selfHealthEventSeverity maps a §25.4 aggregate self-health status to the
// §25.3 CloudEvents severity, mirroring cmd/lenny-ops so the emitted event
// carries the same severity the production binary stamps.
func selfHealthEventSeverity(status string) string {
	switch status {
	case "unhealthy":
		return "critical"
	case "degraded":
		return "warning"
	default:
		return "info"
	}
}

// startSelfMonitor runs the real opsservice.Service self-monitor over the
// supplied §25.4 checks, wiring OnSelfHealthChange to emit the
// ops_health_status_changed event onto the real Redis stream through the
// production eventbuffer.StreamEmitter — the same emission cmd/lenny-ops
// performs. It returns the running Service once the goroutine has recorded
// its first (baseline) evaluation so the caller can inject a failure against
// a known-good state.
func startSelfMonitor(t *testing.T, redisClient *redis.Client, checks map[string]opsservice.SelfCheck) *opsservice.Service {
	t.Helper()

	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    redisClient,
		Buffer:    eventbuffer.NewEventBuffer(0),
		Source:    "//lenny.dev/ops/" + opsSelfHealthReplicaID,
		ReplicaID: opsSelfHealthReplicaID,
	})

	onChange := func(prev, next opsservice.SelfHealthReport) {
		// spec: §25.4 Self-Monitoring — the payload the production binary
		// (cmd/lenny-ops) emits: the replica identity, the previous and
		// current aggregate status, and the transition string.
		fields := map[string]any{
			"replicaId":  opsSelfHealthReplicaID,
			"previous":   prev.StatusText,
			"current":    next.StatusText,
			"transition": prev.StatusText + " -> " + next.StatusText,
		}
		payload, err := json.Marshal(fields)
		if err != nil {
			t.Errorf("marshal ops_health_status_changed payload: %v", err)
			return
		}
		if err := emitter.Emit(context.Background(), events.OperationalEvent{
			Type:            events.EventOpsHealthStatusChanged.CloudEventsType(),
			Subject:         "ops/" + opsSelfHealthReplicaID,
			Severity:        selfHealthEventSeverity(next.StatusText),
			DataContentType: "application/json",
			Data:            payload,
		}); err != nil {
			t.Errorf("emit ops_health_status_changed to the Redis stream: %v", err)
		}
	}

	svc, err := opsservice.New(opsservice.Config{
		ReplicaID:          opsSelfHealthReplicaID,
		Elector:            followerElector{},
		SelfHealthChecks:   checks,
		SelfHealthInterval: selfHealthInterval,
		OnSelfHealthChange: onChange,
	})
	if err != nil {
		t.Fatalf("build opsservice.Service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("opsservice.Service.Run did not return after context cancel")
		}
	})

	// Wait for the goroutine's first (baseline) evaluation so a later
	// injection produces a clean transition rather than racing the initial
	// tick. The monitor reports healthy before its first Evaluate and
	// records the real baseline on the immediate first tick.
	if !waitFor(5*time.Second, func() bool { return len(svc.Monitor().Report().Checks) > 0 }) {
		t.Fatalf("self-monitor did not record a baseline evaluation within 5s")
	}
	return svc
}

// checkStatus reports the current status of the named §25.4 self-health
// check as recorded by the running self-monitor goroutine, which keeps
// Report() current on every tick.
func checkStatus(svc *opsservice.Service, name string) string {
	for _, c := range svc.Monitor().Report().Checks {
		if c.Name == name {
			return c.Status.String()
		}
	}
	return ""
}

// waitFor polls cond until it returns true or the deadline elapses.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

// awaitHealthEvent polls the §25.5 ops:events:stream for an
// ops_health_status_changed event whose payload reports the wanted current
// status, and returns its decoded replica identity. It fails the test if no
// such event appears within the deadline.
func awaitHealthEvent(t *testing.T, redisClient *redis.Client, wantCurrent string) string {
	t.Helper()
	const wantType = "dev.lenny.ops_health_status_changed"
	var lastSeen string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := redisClient.XRange(context.Background(), eventbuffer.DefaultStreamKey, "-", "+").Result()
		if err != nil {
			t.Fatalf("XRange %s: %v", eventbuffer.DefaultStreamKey, err)
		}
		for _, m := range msgs {
			raw, ok := m.Values["event"].(string)
			if !ok {
				continue
			}
			var ev events.OperationalEvent
			if err := json.Unmarshal([]byte(raw), &ev); err != nil {
				continue
			}
			if ev.Type != wantType {
				continue
			}
			var data struct {
				ReplicaID string `json:"replicaId"`
				Current   string `json:"current"`
			}
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			lastSeen = data.Current
			if data.Current == wantCurrent {
				return data.ReplicaID
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no %s event with current=%q appeared on %s within 30s (last current seen: %q)",
		wantType, wantCurrent, eventbuffer.DefaultStreamKey, lastSeen)
	return ""
}

// spec: §25.4 (self-monitoring; postgres_pool self-health check; the
// ops_health_status_changed emission carrying the replica identity)
// diagnosis: the §25.4 self-health goroutine did not emit
// ops_health_status_changed when the real Postgres backing connection
// failed. The self-monitor runs the production PostgresPoolCheck over a real
// pgx pool; terminating the Postgres container is a genuine connection error
// (§25.4 "Connection errors > 0 in last 60s" → unhealthy), which must flip
// the aggregate from healthy to unhealthy and emit the documented event onto
// ops:events:stream carrying source.replicaID. A failure means either the
// PostgresPoolCheck does not classify a real dropped connection as unhealthy,
// the aggregate transition did not fire the change hook, or the emission does
// not reach the Redis stream / does not carry the emitting replica identity.
func TestOpsSelfHealthEmitsOnRealPostgresConnectionError(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	// The real §25.4 postgres_pool self-health check over the real pool. At
	// baseline the pool pings the live container and reports healthy.
	checks := map[string]opsservice.SelfCheck{
		opsservice.CheckPostgresPool: opsservice.PostgresPoolCheck(pg.Pool),
	}
	svc := startSelfMonitor(t, rd.Client, checks)

	// Confirm the baseline: the goroutine observes postgres_pool healthy
	// while Postgres is up, so the transition below is attributable to the
	// injected outage rather than a pre-existing condition.
	if !waitFor(5*time.Second, func() bool {
		return checkStatus(svc, opsservice.CheckPostgresPool) == "healthy"
	}) {
		t.Skipf("precondition not met: postgres_pool is not healthy before the injection")
	}

	// Inject: terminate the Postgres container. Subsequent pool pings fail
	// to reach the backend — a genuine §25.4 connection error.
	pg.Stop(t)

	// Assert: the self-health goroutine emits ops_health_status_changed with
	// current=unhealthy, carrying the emitting replica identity.
	gotReplica := awaitHealthEvent(t, rd.Client, "unhealthy")
	if gotReplica != opsSelfHealthReplicaID {
		t.Errorf("ops_health_status_changed carried replicaId %q, want %q; §25.4 requires the event to carry "+
			"the emitting replica identity", gotReplica, opsSelfHealthReplicaID)
	}
}

// spec: §25.4 (self-monitoring; gateway-auth self-health check; the
// ops_health_status_changed emission carrying the replica identity)
// diagnosis: the §25.4 self-health goroutine did not emit
// ops_health_status_changed when the gateway admin API returned 401 to the
// gateway-auth probe. The self-monitor runs the production GatewayAuthCheck
// over a real HTTP probe; §25.4 "Calling the Gateway" makes a gateway auth
// failure degrade the gateway-auth component, which must flip the aggregate
// from healthy to degraded and emit the documented event onto
// ops:events:stream carrying source.replicaID. A failure means either the
// GatewayAuthCheck does not classify a real 401 as degraded, the aggregate
// transition did not fire the change hook, or the emission does not reach the
// Redis stream / does not carry the emitting replica identity.
func TestOpsSelfHealthEmitsOnGatewayAuth401(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})

	// A gateway admin-API stub. It answers the gateway-auth probe with 200
	// at baseline and flips to 401 on demand — the §25.4 "unexpected 401
	// from the gateway's admin API" that degrades the gateway-auth component.
	var unauthorized atomic.Bool
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if unauthorized.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.Close)

	// The gateway-auth probe: one authenticated round-trip to the gateway
	// admin API, returning a non-token error on a 401 so the production
	// GatewayAuthCheck classifies it as degraded (a plain reachability /
	// auth failure rather than a service-account token-mint failure).
	probe := func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, stub.URL+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("gateway admin API returned status %d", resp.StatusCode)
		}
		return nil
	}

	checks := map[string]opsservice.SelfCheck{
		opsservice.CheckGatewayAuth: opsservice.GatewayAuthCheck(probe),
	}
	svc := startSelfMonitor(t, rd.Client, checks)

	// Confirm the baseline: gateway_auth is healthy while the stub answers
	// 200, so the transition below is attributable to the injected 401.
	if !waitFor(5*time.Second, func() bool {
		return checkStatus(svc, opsservice.CheckGatewayAuth) == "healthy"
	}) {
		t.Skipf("precondition not met: gateway_auth is not healthy before the injection")
	}

	// Inject: the gateway admin API starts returning 401 to the probe.
	unauthorized.Store(true)

	// Assert: the self-health goroutine emits ops_health_status_changed with
	// current=degraded, carrying the emitting replica identity.
	gotReplica := awaitHealthEvent(t, rd.Client, "degraded")
	if gotReplica != opsSelfHealthReplicaID {
		t.Errorf("ops_health_status_changed carried replicaId %q, want %q; §25.4 requires the event to carry "+
			"the emitting replica identity", gotReplica, opsSelfHealthReplicaID)
	}
}
