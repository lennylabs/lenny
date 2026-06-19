// SPDX-License-Identifier: MIT

//go:build load_local

// Package create_pool_exhaustion exercises the §7.1 / §15.1 eager-claim
// model under concurrency: every session create claims an idle warm pod
// synchronously (§7.1 step 4), so a finite pool admits exactly as many
// concurrent creates as it has idle pods and fails the rest fast at /create
// with the §7.1-atomicity SESSION_CREATION_FAILED (pool exhaustion surfaced
// before the client uploads) rather than deferring the claim to /start.
//
// The claim primitive under test is the §4.6.1 per-pod occupancy SandboxClaim
// CREATE under the deterministic name claim-<podName>: a finite set of idle
// pods admits one CREATE each; once every pod carries a claim, a fresh create
// finds no idle pod and fails. The fakekube store's Create is the same
// name-uniqueness primitive the lenny-sandboxclaim-guard webhook enforces, so
// this exercises the real fail-fast-at-create ordering without a cluster.
//
// Regression source: proposal 0007 reverses the F-7.1.6 deferred-claim
// decision. Before it, /create claimed no pod and pool exhaustion surfaced
// only at /start; this scenario pins that exhaustion now fails the create.
//
// TESTING.md §12.7.a regression scenarios.
package create_pool_exhaustion

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/fakekube"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
)

const name = "create_pool_exhaustion"

// poolSize is the number of idle warm pods the finite pool starts with. The
// VU count exceeds it so the surplus creates fail fast at claim.
const poolSize = 8

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{} })
}

// Scenario claims a finite pool of idle pods concurrently and asserts the
// claim count never exceeds the pool size (no double-claim) and that the
// surplus creates fail fast rather than blocking or overcommitting.
type Scenario struct {
	store *fakekube.ObjectStore

	// idle is the list of pod names the pool warmed; an iteration walks it in
	// order and claims the first one whose per-pod claim CREATE wins.
	idle []string

	// claimed counts the live per-pod claims so a test can assert the pool
	// never overcommits. Incremented on a winning CREATE; the scenario never
	// releases within the run (a created session holds its pod through the
	// upload window), so the count saturates at poolSize.
	claimed atomic.Int64

	admitted  atomic.Int64
	exhausted atomic.Int64

	// peakClaimed is the high-water mark of live claims, the overcommit vector.
	mu          sync.Mutex
	peakClaimed int64
}

func (s *Scenario) Name() string { return name }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	// More VUs than idle pods so the surplus creates contend for the last
	// pods and then fail fast once the pool is exhausted.
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.store = fakekube.NewObjectStore()
	s.idle = make([]string, 0, poolSize)
	for i := 0; i < poolSize; i++ {
		pod := fmt.Sprintf("pod-%02d", i)
		s.idle = append(s.idle, pod)
		// The idle pod carries no per-pod claim yet. The gateway no longer
		// writes Sandbox.status, so the seed is the WPC-projected idle phase
		// annotation only; the claim admission never reads it.
		if err := s.store.Create(&fakekube.Object{
			Kind:        "Sandbox",
			Namespace:   "lenny-agents",
			Name:        pod,
			Annotations: map[string]string{"phase": "idle"},
		}); err != nil {
			return fmt.Errorf("seed idle pod %s: %w", pod, err)
		}
	}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

// claimName is the deterministic §4.6.1 per-pod claim name.
func claimName(pod string) string { return "claim-" + pod }

// claimOne walks the idle pool and creates the per-pod occupancy claim for the
// first pod whose CREATE wins. It returns the claimed pod name, or "" when
// every pod already carries a claim (pool exhausted, the create-time
// ErrNoIdlePod the gateway maps to SESSION_CREATION_FAILED).
func (s *Scenario) claimOne() string {
	for _, pod := range s.idle {
		err := s.store.Create(&fakekube.Object{
			Kind:      "SandboxClaim",
			Namespace: "lenny-agents",
			Name:      claimName(pod),
			// §4.6.3 per-pod occupancy claim: spec carries only sandboxRef and
			// tenantId; no session or slot identifier.
			Annotations: map[string]string{"sandboxRef": pod, "tenantId": "acme"},
		})
		if err == nil {
			return pod
		}
		// AlreadyExists: another create won this pod; try the next idle pod.
	}
	return ""
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Each iteration is one §7.1 create that claims a pod synchronously at
	// /create. Concurrent creates race for the finite idle pool; exactly
	// poolSize win and the surplus fail fast.
	pod := s.claimOne()
	if pod == "" {
		// Pool exhausted: the create fails fast with SESSION_CREATION_FAILED
		// rather than deferring the claim to /start.
		s.exhausted.Add(1)
		return nil
	}
	s.admitted.Add(1)
	live := s.claimed.Add(1)
	s.mu.Lock()
	if live > s.peakClaimed {
		s.peakClaimed = live
	}
	s.mu.Unlock()
	return nil
}

// Assert validates the §7.1 / §15.1 eager-claim invariants: the finite pool
// admits exactly poolSize creates (one per idle pod, no double-claim) and the
// surplus creates fail fast at claim rather than blocking.
func (s *Scenario) Assert(r *loadgen.Result) error {
	r.AddCustom("creates_admitted", float64(s.admitted.Load()))
	r.AddCustom("creates_exhausted", float64(s.exhausted.Load()))
	r.AddCustom("peak_claimed", float64(s.peakClaimed))

	if s.admitted.Load() != poolSize {
		return fmt.Errorf("§7.1 step 4 violated: %d creates admitted against a %d-pod pool, want exactly %d (one per idle pod)",
			s.admitted.Load(), poolSize, poolSize)
	}
	if s.peakClaimed > poolSize {
		return fmt.Errorf("§4.6.1 violated: peak live claims = %d > pool size %d (double-claim / overcommit)", s.peakClaimed, poolSize)
	}
	if s.exhausted.Load() == 0 {
		return fmt.Errorf("scenario never exercised the fail-fast exhaustion path; VU count did not exceed the pool")
	}
	if r.Iterations < 100 {
		return fmt.Errorf("scenario did not get enough load: %d iterations (want >= 100)", r.Iterations)
	}
	return nil
}
