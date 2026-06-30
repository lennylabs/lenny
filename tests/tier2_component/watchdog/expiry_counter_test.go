//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §16.1 lenny_session_expiry_total{pool, reason}
// idle-termination counter. The §11.3 watchdog drives the platform expiry
// clocks against the Postgres-backed SessionStore, the gatewaymetrics terminal
// hook implements SessionExpiryNotifier, and the resulting counter is scraped
// off the real Prometheus /metrics endpoint. This pins the emitter end to end:
// the §6.2 maxClientIdleSeconds idle-clock expiry stamps reason=max_idle_time
// and the §11.3 maxSessionAge age-cap expiry stamps reason=max_session_age,
// both labelled by the session's §5.2 pool.
package watchdog_component_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
)

// expiryTerminalHook is the minimal TerminalHook that satisfies the watchdog's
// SessionExpiryNotifier optional interface and forwards the resolved reason to
// the gatewaymetrics counter, mirroring the gateway's sessionserver wiring
// without dragging the full Server into the component test.
type expiryTerminalHook struct {
	inc func(pool, reason string)
}

func (h expiryTerminalHook) OnSessionTerminal(context.Context, session.State, sessionstore.Session) {}

func (h expiryTerminalHook) OnSessionExpired(_ context.Context, sess sessionstore.Session, reason string) {
	h.inc(sess.PoolRef, reason)
}

// spec: 16.1 (lenny_session_expiry_total{reason}), 16.1.1 (reason
// vocabulary), 6.2 (maxClientIdleSeconds clock), 11.3 line 199 (max client
// idle row)
//
// diagnosis: the §16.1 session-expiry counter did not flow from a
// watchdog-driven expiry through the terminal hook onto the real Prometheus
// /metrics endpoint with the spec reason vocabulary. A failure means an
// operator scraping the gateway cannot distinguish idle reclamation
// (max_idle_time) from age-cap expiry (max_session_age) when the watchdog runs
// against real Postgres, so the burn-rate alerts and dashboards lose the
// idle-termination signal the maxClientIdleSeconds bound exists to surface.
func TestWatchdogSessionExpiryCounter_spec_16_1(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}

	tick := time.Now().UTC().Truncate(time.Microsecond)

	// idleBorn places the running idle row past the 600s idle cap but under the
	// 3600s age cap, so the idle sweep wins; ageBorn places the suspended row
	// past the 3600s age cap. The two birth times must straddle the age cap
	// because Tick runs sweepMaxAge before sweepIdle: a running row born past
	// both caps would age-expire first and never reach the idle sweep. F-11.3.7.
	idleBorn := tick.Add(-700 * time.Second)
	ageBorn := tick.Add(-2 * time.Hour)

	// idleRow runs the idle clock (running) with a tight idle cap; ageRow is
	// suspended (idle clock paused) so only the maxSessionAge cap can expire
	// it. Each carries a distinct pool so the counter's pool label is checked.
	idleID := newUUID(t)
	ageID := newUUID(t)
	if err := store.Create(ctx, sessionstore.Session{
		ID: idleID, TenantID: tenant, State: session.StateRunning,
		RuntimeRef: "echo", PoolRef: "pool-idle", CreatedAt: idleBorn, UpdatedAt: idleBorn,
	}); err != nil {
		t.Fatalf("create idle row: %v", err)
	}
	if err := store.Create(ctx, sessionstore.Session{
		ID: ageID, TenantID: tenant, State: session.StateSuspended,
		RuntimeRef: "echo", PoolRef: "pool-age", CreatedAt: ageBorn, UpdatedAt: ageBorn,
	}); err != nil {
		t.Fatalf("create age row: %v", err)
	}

	// A 600s idle cap and a 3600s age cap, with the other recovery/awaiting
	// sweeps disabled so the two rows take distinct expiry edges: the running
	// row idle-expires (max_idle_time), the suspended row age-expires
	// (max_session_age) once it crosses the 3600s cap.
	huge := watchdog.DefaultMaxSessionAgeSeconds * 100
	w := watchdog.New(store, watchdog.StaticTenants{tenant}, watchdog.Config{
		MaxIdleSeconds:                 600,
		MaxSessionAgeSeconds:           3600,
		MaxAwaitingClientActionSeconds: huge,
		MaxSuspendedPodHoldSeconds:     huge,
		MaxResumePendingSeconds:        huge,
		MaxResumingSeconds:             huge,
		MaxFinalizingSeconds:           huge,
	}, nil).WithTerminalHook(expiryTerminalHook{inc: m.IncSessionExpiry})

	// At the tick: the running row is past the 600s idle cap (born tick-700s)
	// yet under the 3600s age cap, so the idle sweep claims it; the suspended
	// row is past the 3600s age cap (born tick-2h), so the age sweep claims it.
	res, err := w.Tick(ctx, tick)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Fatalf("IdleExpirations: got %d, want 1", res.IdleExpirations)
	}
	if res.Expirations < 2 {
		t.Fatalf("Expirations: got %d, want >= 2 (idle + age)", res.Expirations)
	}

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_session_expiry_total{pool="pool-idle",reason="max_idle_time"} 1`,
		`lenny_session_expiry_total{pool="pool-age",reason="max_session_age"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in /metrics\n---\n%s", want, body)
		}
	}
}
