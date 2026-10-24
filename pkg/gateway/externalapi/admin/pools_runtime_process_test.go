// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// The messages each admission branch of recycle.runtimeProcess returns.
const (
	msgRestartable   = "requires a restartable runtime"
	msgInterim       = `recycle.runtimeProcess \"restart\" is refused until the intra-pod MCP servers rebind per runtime generation (proposal 0084); use \"keep\"`
	msgRequired      = "recycle.runtimeProcess (keep or restart) is required"
	msgUnknown       = "not a recognised §5.2 lifetime"
	msgRejectedHere  = "recycle.runtimeProcess is permitted only"
	msgAcknowledging = "acknowledgeProcessLevelIsolation=true"
)

// lifetimeCase is one pool body of the recycle.runtimeProcess admission
// rule, with the substring of the 400 message it must receive ("" admits).
type lifetimeCase struct {
	name    string
	runtime string
	policy  *runtimestore.SessionPolicy
	wantSub string
}

// lifetimePolicy builds a recycling sessionPolicy with the given
// concurrency, session bound, lifetime, and acknowledgment.
func lifetimePolicy(concurrent, maxSessions int, lifetime runtimestore.RuntimeProcessLifetime, ack bool) *runtimestore.SessionPolicy {
	return &runtimestore.SessionPolicy{
		MaxConcurrentSessions:            concurrent,
		AcknowledgeProcessLevelIsolation: ack,
		Recycle: &runtimestore.RecyclePolicy{
			Enabled: true, AcknowledgeBestEffortScrub: true,
			MaxSessionsPerPod: maxSessions, RuntimeProcess: lifetime,
		},
	}
}

// lifetimeCases enumerates each admission branch on a sequential and a
// concurrent reusing recycling pool. "fixed" is restartable and "legacy" is
// not. The restartable refusal runs before the interim refusal, so a
// `restart` pool on a non-restartable runtime receives the end-state error.
func lifetimeCases() []lifetimeCase {
	keep, restart := runtimestore.RuntimeProcessKeep, runtimestore.RuntimeProcessRestart
	var cases []lifetimeCase
	for _, c := range []struct {
		label      string
		concurrent int
	}{{"sequential", 0}, {"concurrent", 2}} {
		cases = append(
			cases,
			lifetimeCase{c.label + " keep admitted", "legacy", lifetimePolicy(c.concurrent, 5, keep, true), ""},
			lifetimeCase{c.label + " restart on non-restartable runtime", "legacy", lifetimePolicy(c.concurrent, 5, restart, true), msgRestartable},
			lifetimeCase{c.label + " restart on unresolved runtime", "", lifetimePolicy(c.concurrent, 5, restart, true), msgRestartable},
			lifetimeCase{c.label + " restart on restartable runtime (interim)", "fixed", lifetimePolicy(c.concurrent, 5, restart, true), msgInterim},
			lifetimeCase{c.label + " lifetime missing", "fixed", lifetimePolicy(c.concurrent, 5, "", true), msgRequired},
			lifetimeCase{c.label + " lifetime unknown", "fixed", lifetimePolicy(c.concurrent, 5, "forever", true), msgUnknown},
			lifetimeCase{c.label + " lifetime on one-session pool", "fixed", lifetimePolicy(c.concurrent, 1, keep, true), msgRejectedHere},
		)
	}
	return append(cases, lifetimeCase{"sequential keep unacknowledged", "fixed", lifetimePolicy(0, 5, keep, false), msgAcknowledging})
}

// seedLifetimeRuntimes registers a restartable and a non-restartable runtime.
func seedLifetimeRuntimes(t *testing.T, runtimes *runtimestore.Memory) {
	t.Helper()
	for _, rt := range []runtimestore.Runtime{{Name: "fixed", Restartable: true}, {Name: "legacy"}} {
		if err := runtimes.Create(context.Background(), rt); err != nil {
			t.Fatalf("seed runtime %s: %v", rt.Name, err)
		}
	}
}

// assertLifetimeOutcome checks one admin response against a case.
func assertLifetimeOutcome(t *testing.T, where string, tc lifetimeCase, code int, body []byte, okCode int) {
	t.Helper()
	if tc.wantSub == "" {
		if code != okCode {
			t.Errorf("%s %s: status %d, want %d; body=%s", where, tc.name, code, okCode, body)
		}
		return
	}
	if code != http.StatusBadRequest {
		t.Errorf("%s %s: status %d, want 400; body=%s", where, tc.name, code, body)
		return
	}
	if !bytes.Contains(body, []byte(`"VALIDATION_ERROR"`)) || !bytes.Contains(body, []byte(tc.wantSub)) {
		t.Errorf("%s %s: body %s, want VALIDATION_ERROR containing %q", where, tc.name, body, tc.wantSub)
	}
}

// spec: §5.2 (Runtime-process lifetime), §15.1 (dryRun)
// diagnosis: the create handler or its dry run admits a pool that the
// recycle.runtimeProcess rule refuses, refuses the admitted `keep` pool,
// returns another status or code, orders the interim refusal ahead of the
// restartable refusal, or stores a refused pool.
func TestCreatePoolRuntimeProcessAdmission_spec_5_2(t *testing.T) {
	for _, path := range []string{"/v1/admin/pools", "/v1/admin/pools?dryRun=true"} {
		for _, tc := range lifetimeCases() {
			router, store, runtimes, _ := newPoolAdmin(t)
			seedLifetimeRuntimes(t, runtimes)
			rr := poolReq(t, router.Handler(), http.MethodPost, path, admin.PoolPayload{
				Name: "lt", RuntimeRef: tc.runtime, ExecutionMode: "session", SessionPolicy: tc.policy,
			})
			assertLifetimeOutcome(t, "POST "+path, tc, rr.Code, rr.Body.Bytes(), http.StatusCreated)
			_, err := store.Get(context.Background(), "lt")
			stored := err == nil
			wantStored := tc.wantSub == "" && path == "/v1/admin/pools"
			if stored != wantStored || (!stored && !errors.Is(err, poolstore.ErrNotFound)) {
				t.Errorf("POST %s %s: stored=%v (err=%v), want %v", path, tc.name, stored, err, wantStored)
			}
		}
	}
}

// spec: §5.2 (Runtime-process lifetime), §15.1 (dryRun)
// diagnosis: a PUT or its dry run admits a sessionPolicy or a runtimeRef
// swap that the recycle.runtimeProcess rule refuses, or changes the stored
// row on a refusal.
func TestUpdatePoolRuntimeProcessAdmission_spec_5_2(t *testing.T) {
	for _, query := range []string{"", "?dryRun=true"} {
		for _, tc := range lifetimeCases() {
			router, store, runtimes, _ := newPoolAdmin(t)
			seedLifetimeRuntimes(t, runtimes)
			if err := store.Create(context.Background(), poolstore.Pool{
				Name: "lt", RuntimeRef: tc.runtime, ExecutionMode: runtimestore.ExecutionModeSession,
			}); err != nil {
				t.Fatalf("seed pool: %v", err)
			}
			before, _ := store.Get(context.Background(), "lt")
			rr := putPoolQuery(t, router.Handler(), "lt", query, admin.UpdatePoolRequest{SessionPolicy: tc.policy})
			assertLifetimeOutcome(t, "PUT"+query, tc, rr.Code, rr.Body.Bytes(), http.StatusOK)
			after, _ := store.Get(context.Background(), "lt")
			changed := after.Generation != before.Generation
			if changed != (tc.wantSub == "" && query == "") {
				t.Errorf("PUT%s %s: generation %d → %d", query, tc.name, before.Generation, after.Generation)
			}
		}
	}
	// A PUT that swaps the runtimeRef from a restartable runtime to one that
	// is not while selecting `restart` is refused with the end-state error,
	// which reads the merged effective runtimeRef rather than the stored one.
	router, store, runtimes, _ := newPoolAdmin(t)
	seedLifetimeRuntimes(t, runtimes)
	restartPool := poolstore.Pool{Name: "swap", RuntimeRef: "fixed", ExecutionMode: runtimestore.ExecutionModeSession}
	if err := store.Create(context.Background(), restartPool); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	legacy := "legacy"
	rr := putPoolQuery(t, router.Handler(), "swap", "", admin.UpdatePoolRequest{
		RuntimeRef: &legacy, SessionPolicy: lifetimePolicy(0, 5, runtimestore.RuntimeProcessRestart, false),
	})
	assertLifetimeOutcome(t, "PUT swap", lifetimeCase{name: "runtimeRef swap", wantSub: msgRestartable},
		rr.Code, rr.Body.Bytes(), http.StatusOK)
}

// spec: §5.2 (Runtime-process lifetime), §17.6 (bootstrap)
// diagnosis: the bootstrap seed admits a pool the recycle.runtimeProcess
// rule refuses, records the restartable refusal under a code other than
// SEED_VALIDATION or a store-side branch under one other than
// SEED_STORE_ERROR, or stores a refused pool.
func TestBootstrapPoolRuntimeProcessAdmission_spec_5_2(t *testing.T) {
	for _, dry := range []string{"", "?dryRun=true"} {
		for _, tc := range lifetimeCases() {
			s := newFullBootstrapRouter(t, false)
			seedLifetimeRuntimes(t, s.runtimes)
			_, resp := postBootstrap(t, s.router, admin.BootstrapRequest{
				Pools: []admin.PoolPayload{{Name: "lt", RuntimeRef: tc.runtime, ExecutionMode: "session", SessionPolicy: tc.policy}},
			}, dry)
			_, err := s.pools.Get(context.Background(), "lt")
			if tc.wantSub == "" {
				if len(resp.Pools.Errors) != 0 {
					t.Errorf("seed dry=%q %s: errors %+v, want admitted", dry, tc.name, resp.Pools.Errors)
				}
				if (err == nil) != (dry == "") {
					t.Errorf("seed dry=%q %s: stored=%v", dry, tc.name, err == nil)
				}
				continue
			}
			wantCode := "SEED_STORE_ERROR"
			if tc.wantSub == msgRestartable {
				wantCode = "SEED_VALIDATION"
			}
			if dry != "" && wantCode == "SEED_STORE_ERROR" {
				// A dry run does not reach the store, so the store-side
				// branches are not evaluated on the seed's dry run.
				continue
			}
			if len(resp.Pools.Errors) != 1 || resp.Pools.Errors[0].Code != wantCode ||
				!bytes.Contains([]byte(resp.Pools.Errors[0].Message), []byte(unescape(tc.wantSub))) {
				t.Errorf("seed dry=%q %s: errors %+v, want one %s containing %q", dry, tc.name, resp.Pools.Errors, wantCode, tc.wantSub)
			}
			if err == nil {
				t.Errorf("seed dry=%q %s: a refused pool was stored", dry, tc.name)
			}
		}
	}
}

// unescape turns the JSON-escaped quotes of a message substring into the
// plain quotes a decoded error message carries.
func unescape(s string) string {
	return string(bytes.ReplaceAll([]byte(s), []byte(`\"`), []byte(`"`)))
}
