//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §16.1 lenny_session_expiry_total{pool, reason}
// idle-termination counter. The §11.3 watchdog drives the platform expiry
// clocks against the Postgres-backed SessionStore, the gatewaymetrics terminal
// hook implements SessionExpiryNotifier, and the resulting counter is scraped
// off the real Prometheus /metrics endpoint. This pins the emitter end to end:
// an idle running session that can be idle-suspended goes to the idle
// suspender and is neither written nor counted, the §6.2 maxClientIdleSeconds
// idle expiry of a session that cannot be idle-suspended stamps
// reason=max_idle_time, and the §11.3 maxSessionAge age-cap expiry stamps
// reason=max_session_age, each labelled by the session's §5.2 pool.
package watchdog_component_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// expiryTerminalHook is the minimal TerminalHook that satisfies the watchdog's
// SessionExpiryNotifier optional interface and forwards the resolved reason to
// the gatewaymetrics counter, mirroring the gateway's sessionserver wiring
// without dragging the full Server into the component test.
type expiryTerminalHook struct {
	inc func(pool, reason string)
}

// BeginTerminal opens no claim; the fake records only the terminal hook.
func (expiryTerminalHook) BeginTerminal(string) func() { return func() {} }

func (h expiryTerminalHook) OnSessionTerminal(context.Context, session.State, sessionstore.Session) {}

func (h expiryTerminalHook) OnSessionExpired(_ context.Context, sess sessionstore.Session, reason string) {
	h.inc(sess.PoolRef, reason)
}

// idleSuspendability answers the watchdog's suspendability check from a fixed
// set of suspendable session IDs, with the platform idle cap.
type idleSuspendability struct{ suspendable map[string]bool }

func (idleSuspendability) EffectiveMaxIdleSeconds(context.Context, sessionstore.Session) int {
	return 0
}

func (r idleSuspendability) IdleSuspendable(_ context.Context, sess sessionstore.Session) (bool, error) {
	return r.suspendable[sess.ID], nil
}

// recordingSuspender records each session the watchdog hands to the idle
// suspender and writes nothing, so the row stays as the sweep found it.
type recordingSuspender struct {
	mu  sync.Mutex
	ids []string
}

func (r *recordingSuspender) SuspendIdle(_ context.Context, row sessionstore.Session, _ int) {
	r.mu.Lock()
	r.ids = append(r.ids, row.ID)
	r.mu.Unlock()
}

func (r *recordingSuspender) suspended() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ids...)
}

// spec: 16.1 (lenny_session_expiry_total{reason}), 16.1.1 (Attribute
// Naming), 6.2 (maxClientIdleSeconds clock behavior across states), 11.3
// (Timeouts and Cancellation)
//
// diagnosis: the §16.1 session-expiry counter did not flow from a
// watchdog-driven expiry through the terminal hook onto the real Prometheus
// /metrics endpoint with the spec reason vocabulary, or an idle session that
// can be idle-suspended was expired and counted instead of being handed to
// the idle suspender. A failure means an operator scraping the gateway cannot
// distinguish idle reclamation (max_idle_time) from age-cap expiry
// (max_session_age) when the watchdog runs against real Postgres, or an idle
// session the platform must suspend and keep resumable is terminated.
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
	// 3600s age cap, so the idle sweep wins. The two rows must straddle the age
	// cap because Tick runs sweepMaxAge before sweepIdle: a running row whose
	// active age is past both caps would age-expire first and never reach the
	// idle sweep. F-11.3.7.
	idleBorn := tick.Add(-700 * time.Second)
	ageBorn := tick.Add(-2 * time.Hour)

	// idleRow runs the idle clock (running) with a tight idle cap. ageRow is
	// resume_pending, where the idle clock and the maxSessionAge timer are both
	// paused, and carries an accumulated active age past the 3600s cap, so only
	// the maxSessionAge cap can expire it. maxSessionAge counts active time, so
	// a paused row expires only on the age it accrued before it paused (§6.2
	// maxSessionAge timer behavior across states). Each row carries a distinct
	// pool so the counter's pool label is checked.
	idleID := newUUID(t)
	ageID := newUUID(t)
	suspendID := newUUID(t)
	if err := store.Create(ctx, sessionstore.Session{
		ID: suspendID, TenantID: tenant, State: session.StateRunning,
		RuntimeRef: "echo", PoolRef: "pool-suspend", CreatedAt: idleBorn, UpdatedAt: idleBorn,
	}); err != nil {
		t.Fatalf("create suspendable row: %v", err)
	}
	if err := store.Create(ctx, sessionstore.Session{
		ID: idleID, TenantID: tenant, State: session.StateRunning,
		RuntimeRef: "echo", PoolRef: "pool-idle", CreatedAt: idleBorn, UpdatedAt: idleBorn,
	}); err != nil {
		t.Fatalf("create idle row: %v", err)
	}
	if err := store.Create(ctx, sessionstore.Session{
		ID: ageID, TenantID: tenant, State: session.StateResumePending,
		RuntimeRef: "echo", PoolRef: "pool-age", CreatedAt: ageBorn, UpdatedAt: tick.Add(-time.Minute),
		AccumulatedSessionAgeSeconds: (90 * time.Minute).Seconds(),
	}); err != nil {
		t.Fatalf("create age row: %v", err)
	}

	// A 600s idle cap and a 3600s age cap, with the other recovery/awaiting
	// sweeps disabled so the two rows take distinct expiry edges: the running
	// row idle-expires (max_idle_time), the resume_pending row age-expires
	// (max_session_age) on its accrued active age.
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
	suspender := &recordingSuspender{}
	w = w.WithIdleResolver(idleSuspendability{suspendable: map[string]bool{suspendID: true}}).
		WithIdleSuspender(suspender)

	// At the tick: the running row is past the 600s idle cap (born tick-700s)
	// yet under the 3600s age cap, so the idle sweep claims it; the
	// resume_pending row's accrued active age is past the 3600s cap, so the age
	// sweep claims it.
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
	if res.IdleSuspensions != 1 {
		t.Fatalf("IdleSuspensions: got %d, want 1", res.IdleSuspensions)
	}
	if got := suspender.suspended(); len(got) != 1 || got[0] != suspendID {
		t.Fatalf("idle suspender received %v, want only the suspendable row %s", got, suspendID)
	}
	row, err := store.Get(ctx, tenant, suspendID)
	if err != nil {
		t.Fatalf("get suspendable row: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("suspendable row state = %s, want running: the sweep wrote a row it handed to the suspender", row.State)
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
	if strings.Contains(body, `pool="pool-suspend"`) {
		t.Errorf("the idle-suspended session was counted as an expiry\n---\n%s", body)
	}
}
