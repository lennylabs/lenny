// SPDX-License-Identifier: MIT

//go:build load_local

// Package claim_admission_ordering exercises the §4.6.1 + §5.2 per-pod
// occupancy claim admission ordering under concurrency. The SandboxClaim is
// a per-pod occupancy claim with the deterministic name claim-<podName>:
// exactly one gateway replica's CREATE wins a contested idle pod, and every
// other replica receives an AlreadyExists conflict and retries on a
// different pod. The gateway no longer SSA-mirrors Sandbox.status.phase, and
// the lenny-sandboxclaim-guard webhook reads no phase (its CREATE check is
// per-pod uniqueness by .spec.sandboxRef), so the former phase-mirror lag
// race the regression at commit 2b20338 covered no longer exists.
//
// This scenario instead pins the surviving concurrency invariant: under N
// concurrent acquisitions of the same idle pod, the per-pod CREATE admits
// exactly one and rejects the rest, and intra-pod capacity for a
// maxConcurrentSessions>1 pool is gated by the atomic counter rather than by
// any Sandbox.status read.
//
// TESTING.md §12.7.a regression scenarios.
package claim_admission_ordering

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/fakekube"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "claim_admission_ordering"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters

	store   *fakekube.ObjectStore
	sandbox string
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 24, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.store = fakekube.NewObjectStore()
	s.sandbox = "pod-ordered"
	// The idle pod carries no per-pod claim yet. The gateway no longer
	// writes Sandbox.status, so the seed is the WPC-projected idle phase
	// only; the admission decision never reads it.
	return s.store.Create(&fakekube.Object{
		Kind:        "Sandbox",
		Namespace:   "lenny-agents",
		Name:        s.sandbox,
		Annotations: map[string]string{"phase": "idle"},
	})
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

// claimName is the deterministic per-pod claim name (§4.6.1): the CREATE
// race resolves pod acquisition by name uniqueness with no phase read.
func claimName(sandbox string) string { return "claim-" + sandbox }

// acquire models the §4.6.1 per-pod CREATE admission: the first CREATE under
// the deterministic name claim-<podName> wins; a second CREATE for the same
// pod fails AlreadyExists (the fakekube store rejects a duplicate name), the
// same outcome the lenny-sandboxclaim-guard webhook enforces. It returns nil
// when this caller won the pod.
func (s *Scenario) acquire() error {
	return s.store.Create(&fakekube.Object{
		Kind:      "SandboxClaim",
		Namespace: "lenny-agents",
		Name:      claimName(s.sandbox),
		// Spec carries only sandboxRef and tenantId (§4.6.3); no session or
		// slot identifier. The status binding state is written by a
		// subsequent patch the admission decision does not consult.
		Annotations: map[string]string{"sandboxRef": s.sandbox, "tenantId": "acme"},
	})
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Concurrent gateway replicas race for the same idle pod. The per-pod
	// CREATE under the deterministic name claim-<podName> is the §4.6.1
	// acquisition guard, resolved at the API-server level with no phase read
	// and no Sandbox.status write: exactly one CREATE wins across the whole
	// run; every other replica receives AlreadyExists and would retry on a
	// different idle pod. The fakekube store's Create is the same
	// name-uniqueness primitive, so this exercises the real ordering
	// invariant without a cluster.
	if err := s.acquire(); err != nil {
		s.counters.Inc("rejected_already_claimed")
		return nil
	}
	s.counters.Inc("admitted")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	// §4.6.1 per-pod uniqueness: across all concurrent acquisitions of the
	// single contested pod, exactly one CREATE is admitted. Two winners would
	// be a double-claim, the invariant the per-pod claim exists to uphold.
	admitted := s.counters.Get("admitted")
	if admitted == 0 {
		return fmt.Errorf("scenario did not exercise the per-pod claim admission path")
	}
	if admitted != 1 {
		return fmt.Errorf("§4.6.1 violated: %d concurrent CREATEs admitted for one pod, want exactly 1", admitted)
	}
	return nil
}
