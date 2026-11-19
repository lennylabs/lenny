// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"testing"
	"time"

	apisession "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
)

// unavailableStores is a DualStoreGate that reports both coordination
// stores unreachable while down is set.
type unavailableStores struct{ down bool }

func (u unavailableStores) Unavailable() bool { return u.down }

// drainFixture wires f's server with one pool per route-test runtime, named
// "<runtime>-pool", and drains the pool of drained when it is not empty. A
// live session aged ageSeconds in the drained pool sets the drain estimate.
func drainFixture(t *testing.T, f *wdFixture, drained string, ageSeconds int) {
	t.Helper()
	ctx := context.Background()
	pools := poolstore.NewMemory()
	for _, rt := range []string{rcRequested, rcExperiment, rcFinal} {
		if err := pools.Create(ctx, poolstore.Pool{Name: rt + "-pool", RuntimeRef: rt, MaxSessionAgeSeconds: 3600}); err != nil {
			t.Fatalf("create pool: %v", err)
		}
	}
	if drained != "" {
		if _, err := pools.Update(ctx, drained+"-pool", func(p *poolstore.Pool) error {
			p.DrainingSince = f.srv.clock().Add(-time.Minute)
			return nil
		}); err != nil {
			t.Fatalf("drain pool: %v", err)
		}
		if ageSeconds > 0 {
			if err := f.store.Create(ctx, sessionstore.Session{
				ID: "sess_live", TenantID: wdTenant, UserID: testOwner, PoolRef: drained + "-pool",
				State:     apisession.StateRunning,
				CreatedAt: f.srv.clock().Add(-time.Duration(ageSeconds) * time.Second),
			}); err != nil {
				t.Fatalf("seed live session: %v", err)
			}
		}
	}
	f.srv.pools = pools
	f.srv.poolNameResolver = func(_ context.Context, runtime string, _ isolation.Profile, _ string) (string, bool) {
		return runtime + "-pool", true
	}
}

// TestCreateAndStartRefusesWhileThePoolDrains pins that POST
// /v1/sessions/start refuses a create that would select a draining pool with
// the 503 POOL_DRAINING, Retry-After, and details POST /v1/sessions returns,
// on the requested runtime and on a runtime the PreRoute segments or the
// ExperimentRouter selected, before any pod claim and with no row written. A
// request routing moves off the draining pool, and a request when no pool
// drains, is admitted on both routes.
//
// spec: §15.1 (REST API) POOL_DRAINING, §4.8 (Gateway Policy Engine), §10.7 (Experiment Primitives)
func TestCreateAndStartRefusesWhileThePoolDrains(t *testing.T) {
	cases := []struct {
		name       string
		chain      map[interceptor.Phase][]routeStep
		experiment bool
		draining   string
		refused    bool
	}{
		{"requested-runtime-draining", nil, false, rcRequested, true},
		{"below-hint-to-draining", map[interceptor.Phase][]routeStep{
			interceptor.PhasePreRoute: {{name: "h", priority: 150, run: rewriteRuntime(rcFinal)}},
		}, false, rcFinal, true},
		{"below-hint-off-draining", map[interceptor.Phase][]routeStep{
			interceptor.PhasePreRoute: {{name: "h", priority: 150, run: rewriteRuntime(rcFinal)}},
		}, false, rcRequested, false},
		{"above-hint-to-draining", map[interceptor.Phase][]routeStep{
			interceptor.PhasePreRoute: {{name: "h", priority: 350, run: rewriteRuntime(rcFinal)}},
		}, false, rcFinal, true},
		{"experiment-to-draining", nil, true, rcExperiment, true},
		{"nothing-draining", nil, false, "", false},
	}
	for _, route := range rcRoutes {
		for _, tc := range cases {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				// A refused case wires the pod binder so the test can count
				// claims; an admitted case runs without one, so the
				// create-and-start route persists its running row without a
				// pod and returns 201.
				opts := wdOpts{claims: tc.refused}
				if tc.chain != nil {
					opts.chain = rcChain(t, tc.chain)
				}
				if tc.experiment {
					opts.experiments = rcExperiments(t, rcRequested)
				}
				f := newWDFixture(t, rcRuntimes(), opts)
				drainFixture(t, f, tc.draining, 120)

				rr := f.post(t, route, "sess_drain", rcRequested, "")
				if !tc.refused {
					if rr.Code != http.StatusCreated {
						t.Fatalf("status %d, want 201; body=%s", rr.Code, rr.Body.String())
					}
					return
				}
				if rr.Code != http.StatusServiceUnavailable {
					t.Fatalf("status %d, want 503; body=%s", rr.Code, rr.Body.String())
				}
				code, details := wdErrorCode(t, rr)
				if code != "POOL_DRAINING" {
					t.Errorf("code = %q, want POOL_DRAINING", code)
				}
				if details["pool"] != tc.draining+"-pool" {
					t.Errorf("details.pool = %v, want %s-pool", details["pool"], tc.draining)
				}
				if got := rr.Header().Get("Retry-After"); got != "120" {
					t.Errorf("Retry-After = %q, want 120", got)
				}
				if f.exists("sess_drain") {
					t.Error("a refused create left a session row")
				}
				if got := f.claimCount(t); got != 0 {
					t.Errorf("claims = %d, want 0", got)
				}
			})
		}
	}
}

// TestCreateAndStartServiceRefusesWhileThePoolDrains pins the drain refusal
// on the in-process create-and-start entry point the single-shot path and
// the MCP create-and-start tool reach: a ServiceError with HTTP status 503,
// code POOL_DRAINING, the pool in its details, and the Retry-After seconds
// carried in RetryAfterSeconds. A pool that drains with no live session
// estimates zero seconds.
//
// spec: §15.1 (REST API) POOL_DRAINING, §15 (Built-in adapter single-shot compute model)
func TestCreateAndStartServiceRefusesWhileThePoolDrains(t *testing.T) {
	for _, tc := range []struct {
		name      string
		age       int
		wantRetry int
	}{
		{"live-session-sets-estimate", 120, 120},
		{"no-live-session", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWDFixture(t, rcRuntimes(), wdOpts{})
			drainFixture(t, f, rcRequested, tc.age)
			f.nextID = "sess_drain_svc"

			_, svcErr := f.srv.CreateAndStartService(ownerCtx(context.Background()), wdTenant, CreateSessionRequest{RuntimeRef: rcRequested})
			if svcErr == nil {
				t.Fatal("CreateAndStartService admitted a create against a draining pool")
			}
			if svcErr.HTTPStatus != http.StatusServiceUnavailable || svcErr.Code != "POOL_DRAINING" {
				t.Fatalf("error = %d %s, want 503 POOL_DRAINING", svcErr.HTTPStatus, svcErr.Code)
			}
			if svcErr.Details["pool"] != rcRequested+"-pool" {
				t.Errorf("details.pool = %v, want %s-pool", svcErr.Details["pool"], rcRequested)
			}
			if svcErr.RetryAfterSeconds != tc.wantRetry {
				t.Errorf("RetryAfterSeconds = %d, want %d", svcErr.RetryAfterSeconds, tc.wantRetry)
			}
			if f.exists("sess_drain_svc") {
				t.Error("a refused create left a session row")
			}
		})
	}
}

// TestCreateRoutesRefuseWhileBothStoresAreUnavailable pins that both
// creation routes refuse a create while Postgres and Redis are both
// unreachable with the same 503 PLATFORM_DEGRADED envelope, Retry-After of
// ten seconds, and dual_store_unavailable reason, before the claim and with
// no row written, and admit a create once the stores are available.
//
// spec: §10.1.3 (Dual-Store Unavailability) item 2
func TestCreateRoutesRefuseWhileBothStoresAreUnavailable(t *testing.T) {
	for _, route := range rcRoutes {
		t.Run(route+"/unavailable", func(t *testing.T) {
			f := newWDFixture(t, rcRuntimes(), wdOpts{claims: true})
			f.srv.dualStore = unavailableStores{down: true}

			rr := f.post(t, route, "sess_degraded", rcRequested, "")
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("status %d, want 503; body=%s", rr.Code, rr.Body.String())
			}
			code, details := wdErrorCode(t, rr)
			if code != "PLATFORM_DEGRADED" {
				t.Errorf("code = %q, want PLATFORM_DEGRADED", code)
			}
			if details["reason"] != "dual_store_unavailable" {
				t.Errorf("details.reason = %v, want dual_store_unavailable", details["reason"])
			}
			if got := rr.Header().Get("Retry-After"); got != "10" {
				t.Errorf("Retry-After = %q, want 10", got)
			}
			if f.exists("sess_degraded") {
				t.Error("a refused create left a session row")
			}
			if got := f.claimCount(t); got != 0 {
				t.Errorf("claims = %d, want 0", got)
			}
		})
		t.Run(route+"/available", func(t *testing.T) {
			f := newWDFixture(t, rcRuntimes(), wdOpts{})
			f.srv.dualStore = unavailableStores{down: false}
			if rr := f.post(t, route, "sess_healthy", rcRequested, ""); rr.Code != http.StatusCreated {
				t.Fatalf("status %d, want 201; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestCreateAndStartServiceRefusesWhileBothStoresAreUnavailable pins the
// dual-store refusal on the in-process create-and-start entry point: a
// retryable ServiceError with HTTP status 503, code PLATFORM_DEGRADED, and
// ten Retry-After seconds, with no row written.
//
// spec: §10.1.3 (Dual-Store Unavailability) item 2, §15 (Built-in adapter single-shot compute model)
func TestCreateAndStartServiceRefusesWhileBothStoresAreUnavailable(t *testing.T) {
	f := newWDFixture(t, rcRuntimes(), wdOpts{})
	f.srv.dualStore = unavailableStores{down: true}
	f.nextID = "sess_degraded_svc"

	_, svcErr := f.srv.CreateAndStartService(ownerCtx(context.Background()), wdTenant, CreateSessionRequest{RuntimeRef: rcRequested})
	if svcErr == nil {
		t.Fatal("CreateAndStartService admitted a create while both stores are unavailable")
	}
	if svcErr.HTTPStatus != http.StatusServiceUnavailable || svcErr.Code != "PLATFORM_DEGRADED" {
		t.Fatalf("error = %d %s, want 503 PLATFORM_DEGRADED", svcErr.HTTPStatus, svcErr.Code)
	}
	if !svcErr.Retryable {
		t.Error("the dual-store refusal must be retryable")
	}
	if svcErr.RetryAfterSeconds != 10 {
		t.Errorf("RetryAfterSeconds = %d, want 10", svcErr.RetryAfterSeconds)
	}
	if f.exists("sess_degraded_svc") {
		t.Error("a refused create left a session row")
	}
}
