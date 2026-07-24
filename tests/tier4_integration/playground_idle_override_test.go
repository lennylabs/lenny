// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §27.6 playground idle-timeout
// override and hard duration cap. It wires the real create path
// (sessionserver.Server with SetPlaygroundCaps, the same call
// cmd/lenny-gateway makes) to the real §11.3 watchdog reaper (via the real
// sessionidle.Resolver and sessionage.Resolver) and asserts the session the
// create path stamped is reclaimed at exactly the playground-tightened
// boundary, not the runtime's looser one.
//
// The component pieces are each pinned in isolation elsewhere: the §27.6
// cap-stamping arithmetic in pkg/gateway/mcpfabric/playground and its
// landing on the row in pkg/gateway/sessionserver, the resolver
// most-restrictive-wins arithmetic in pkg/gateway/session/sessionidle and
// pkg/gateway/session/sessionage, and the watchdog sweep semantics against
// a fake resolver in pkg/gateway/runtime/watchdog. Nothing below this test
// drives a session shaped exactly as the create path produces it through
// the real resolvers and a real watchdog Tick, advancing a clock across the
// reclaim boundary.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionage"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/clockstep"
)

// idlePlaygroundCapDisabled is a budget so large a watchdog sweep never
// reaches it inside a test window, isolating the sweep under test —
// mirrors the tier-1 watchdog suite's idleCapDisabled convention.
const idlePlaygroundCapDisabled = 1 << 30

// newPlaygroundServer wires a real sessionserver.Server against an
// in-memory store and an allow-all "acme" tenant, with rt as the runtime
// registry, then calls SetPlaygroundCaps(caps, nil) — the same call
// cmd/lenny-gateway's httpsurface.go makes after constructing the session
// server — so the create path applies the real §27.6 cap-stamping logic.
func newPlaygroundServer(t *testing.T, rt runtimestore.Store, caps playground.Config) (*sessionserver.Server, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Now().UTC() }
	ts := tenantstore.NewMemory()
	if err := ts.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
	}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                      clock,
		UploadTokenIssuer:          uploadtoken.NewIssuer(ring, clock),
		Tenants:                    ts,
		DefaultNoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
		Runtimes:                   rt,
	})
	srv.SetPlaygroundCaps(caps, nil)
	return srv, store
}

// createPlaygroundSession drives a real POST /v1/sessions as an
// origin=playground principal — the same claim §27.3 stamps on every
// playground-minted bearer — and returns the row the real create path
// persisted, caps and origin label included.
func createPlaygroundSession(t *testing.T, srv *sessionserver.Server, store *memstore.Store, runtimeRef string) sessionstore.Session {
	t.Helper()
	body, err := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: runtimeRef})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "alice@acme.com", TenantID: "acme", Origin: "playground",
	}))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	row, err := store.Get(context.Background(), "acme", env.ID)
	if err != nil {
		t.Fatalf("get created row: %v", err)
	}
	return row
}

// spec: §27.6 — "Playground-initiated sessions MUST NOT remain idle for
// longer than playground.maxIdleTimeSeconds (default: 300 / 5 min). The
// gateway enforces this value as a hard override of the pool's effective
// sessionPolicy.maxClientIdleSeconds ... The effective idle cap is
// min(maxClientIdleSeconds, playground.maxIdleTimeSeconds) ... the override
// never relaxes a stricter platform bound and can only tighten a looser
// one."
//
// diagnosis: a failure means a playground-initiated session, created
// through the real create path and reaped through the real watchdog sweep
// and resolver (not a fake resolver or a hand-built row), is not reclaimed
// at its stamped playground idle cap — so a client that walks away from the
// playground would hold a pod and credential lease past the spec's 5-minute
// bound, or the runtime's looser bound would leak through unenforced.
func TestPlaygroundIdleOverrideReclaimsSessionAtEffectiveCap(t *testing.T) {
	rt := runtimestore.NewMemory()
	if err := rt.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-code",
		Type: runtimestore.TypeAgent,
		// The runtime declares a looser 600s idle bound. The playground
		// override (default 300s) must tighten it, per the spec sentence
		// above, not relax it.
		SessionPolicy: &runtimestore.SessionPolicy{MaxClientIdleSeconds: 600},
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	srv, store := newPlaygroundServer(t, rt, playground.Config{
		MaxIdleTimeSeconds: 300,
		MaxSessionMinutes:  1000, // far larger than the idle window under test
	})

	row := createPlaygroundSession(t, srv, store, "claude-code")
	if row.Origin != "playground" {
		t.Fatalf("origin = %q, want playground", row.Origin)
	}
	if row.Timeouts == nil || row.Timeouts.MaxIdleSeconds != 300 {
		t.Fatalf("stamped MaxIdleSeconds = %+v, want 300 (min(600, 300))", row.Timeouts)
	}

	// The create path always starts a session at `created`; the §6.2 idle
	// clock runs only in `running`, `input_required`, and
	// `awaiting_client_action`. Move it to `running` to let the clock run,
	// and read back the store-stamped UpdatedAt (the idle anchor, since no
	// qualifying activity has been recorded) rather than assume a value.
	running, err := store.Update(context.Background(), "acme", row.ID, func(r *sessionstore.Session) error {
		r.State = session.StateRunning
		return nil
	})
	if err != nil {
		t.Fatalf("transition to running: %v", err)
	}

	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		MaxCreatedSeconds:              idlePlaygroundCapDisabled,
		MaxSessionAgeSeconds:           idlePlaygroundCapDisabled,
		MaxAwaitingClientActionSeconds: idlePlaygroundCapDisabled,
		MaxSuspendedPodHoldSeconds:     idlePlaygroundCapDisabled,
		MaxResumePendingSeconds:        idlePlaygroundCapDisabled,
	}, nil).WithIdleResolver(sessionidle.NewResolver(rt, nil))

	clk := clockstep.New(running.UpdatedAt)

	// Under the 300s effective cap: the session survives.
	clk.Advance(299 * time.Second)
	res, err := w.Tick(context.Background(), clk.Now())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.IdleExpirations != 0 {
		t.Fatalf("IdleExpirations at 299s = %d, want 0 (under the 300s effective cap)", res.IdleExpirations)
	}

	// Past the 300s effective cap. The runtime's looser 600s bound never
	// applies: the session is reclaimed.
	clk.Advance(2 * time.Second)
	res, err = w.Tick(context.Background(), clk.Now())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Fatalf("IdleExpirations at 301s = %d, want 1", res.IdleExpirations)
	}
	got, err := store.Get(context.Background(), "acme", row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != session.StateExpired {
		t.Errorf("state = %q, want expired", got.State)
	}
	if got.FailureReason != string(session.FailureExpiredIdle) {
		t.Errorf("FailureReason = %q, want %q", got.FailureReason, session.FailureExpiredIdle)
	}
}

// spec: §27.6 — "Hard duration cap.
// min(sandboxTemplate.spec.maxSessionMinutes, playground.maxSessionMinutes).
// Enforcement binds whenever the session-capability JWT carries the
// origin: "playground" claim ..., so the cap applies uniformly to oidc,
// apiKey, and dev playground sessions."
//
// diagnosis: a failure means a playground-initiated session, created
// through the real create path and reaped through the real watchdog
// maxSessionAge sweep and resolver, is not terminated at its stamped hard
// duration cap — so a playground session could run past the tighter of the
// runtime's and the deployer's playground.maxSessionMinutes bound.
func TestPlaygroundHardDurationCapTerminatesSessionAtEffectiveCap(t *testing.T) {
	rt := runtimestore.NewMemory()
	if err := rt.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-code",
		Type: runtimestore.TypeAgent,
		// The runtime (standing in for the sandboxTemplate) declares a
		// looser 60-minute session-age cap. The playground override (1
		// minute) must tighten it.
		Limits: &runtimestore.Limits{MaxSessionAgeSeconds: 3600},
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	srv, store := newPlaygroundServer(t, rt, playground.Config{
		MaxIdleTimeSeconds: 100000, // far larger than the duration window under test
		MaxSessionMinutes:  1,
	})

	row := createPlaygroundSession(t, srv, store, "claude-code")
	if row.Timeouts == nil || row.Timeouts.MaxSessionAgeSeconds != 60 {
		t.Fatalf("stamped MaxSessionAgeSeconds = %+v, want 60 (min(3600, 60))", row.Timeouts)
	}

	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		MaxCreatedSeconds:              idlePlaygroundCapDisabled,
		MaxIdleSeconds:                 idlePlaygroundCapDisabled,
		MaxAwaitingClientActionSeconds: idlePlaygroundCapDisabled,
		MaxSuspendedPodHoldSeconds:     idlePlaygroundCapDisabled,
		MaxResumePendingSeconds:        idlePlaygroundCapDisabled,
	}, nil).WithSessionAgeResolver(sessionage.New(rt, nil))

	clk := clockstep.New(row.CreatedAt)

	// Under the 60s effective cap: the session survives.
	clk.Advance(59 * time.Second)
	res, err := w.Tick(context.Background(), clk.Now())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Expirations != 0 {
		t.Fatalf("Expirations at 59s = %d, want 0 (under the 60s effective cap)", res.Expirations)
	}

	// Past the 60s effective cap. The runtime's looser 3600s bound never
	// applies: the session terminates.
	clk.Advance(2 * time.Second)
	res, err = w.Tick(context.Background(), clk.Now())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Fatalf("Expirations at 61s = %d, want 1", res.Expirations)
	}
	got, err := store.Get(context.Background(), "acme", row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != session.StateExpired {
		t.Errorf("state = %q, want expired", got.State)
	}
	if got.FailureReason != string(session.FailureExpiredDeadline) {
		t.Errorf("FailureReason = %q, want %q", got.FailureReason, session.FailureExpiredDeadline)
	}
}
