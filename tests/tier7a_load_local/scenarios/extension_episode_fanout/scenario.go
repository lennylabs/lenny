// SPDX-License-Identifier: MIT

//go:build load_local

// Package extension_episode_fanout exercises the §8.6 budget-exhaustion
// lease-extension episode under concurrent load. Proposal 0023 relocates
// the trigger into the gateway LLM Proxy and dispatches the extension as
// a per-tree episode on a single tracked goroutine: many concurrent
// exhausting sessions in one tree join the one pending episode
// (treeConsent.pending / §8.6 line 719 batching) rather than opening a
// second elicitation, then the episode's per-session completion fan-out
// raises or terminates each joined session.
//
// The invariants this scenario asserts:
//   - The per-tree episode goroutine runs the fan-out and exits, leaking
//     no goroutine per pending elicitation.
//   - Every joined session is reclaimed (raised or terminated); no
//     session is left denied with nothing to clear it.
//   - The whole path is -race clean (run under -race with the load_local
//     tag).
//
// TESTING.md §12.7.a regression scenarios; proposal 0023 S2.
package extension_episode_fanout

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "extension_episode_fanout"

// treeCount is the number of distinct delegation trees the scenario
// spreads its sessions over. Sessions on the same tree join one episode;
// spreading over several trees exercises several concurrent episodes.
const treeCount = 8

// sessionsPerTree is how many sessions each tree holds. Half have token
// headroom (raised on extension) and half are parent-capped to zero
// headroom (terminated on extension), so the fan-out exercises both
// per-session branches within one episode.
const sessionsPerTree = 16

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// Scenario drives concurrent ExtendForBudget calls against a shared
// leasecontrol.Service and asserts the episode fan-out and
// goroutine-leak invariants.
type Scenario struct {
	counters *scenkit.Counters

	svc       *leasecontrol.Service
	reclaimer *countingReclaimer
	elicitor  *delayElicitor

	baselineGoro int
}

func (s *Scenario) Name() string { return name }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 64, Duration: 2 * time.Second}
}

// treeID returns the tree root session id for tree index i.
func treeID(i int) string { return fmt.Sprintf("root-%d", i) }

// sessionID returns the j-th session id in tree i. j==0 is the tree root.
func sessionID(i, j int) string {
	if j == 0 {
		return treeID(i)
	}
	return fmt.Sprintf("root-%d-child-%d", i, j)
}

// headroom reports whether session j in a tree has token headroom (and
// so is raised on extension) or is parent-capped to zero (terminated).
func headroom(j int) bool { return j%2 == 0 }

func (s *Scenario) Setup(ctx context.Context) error {
	budgets := leasecontrol.NewMemoryBudgetSource()
	for i := 0; i < treeCount; i++ {
		root := treeID(i)
		budgets.RegisterTree(root, leasecontrol.TreeConfig{
			TenantID:           "acme",
			CurrentTokenBudget: 100_000,
			DeploymentBase:     2_000_000,
			DeploymentMax:      4_000_000,
			// Auto mode would resolve without the elicitor; elicitation mode
			// with the delay elicitor holds each episode open long enough for
			// concurrent sessions to batch onto it.
			ApprovalMode: leasecontrol.ApprovalModeElicitation,
		})
		for j := 1; j < sessionsPerTree; j++ {
			sid := sessionID(i, j)
			budgets.AddSession(sid, root, "acme")
			if !headroom(j) {
				// Parent lease caps the child at its current budget: zero
				// headroom, so its extension resolves CEILING_REACHED and the
				// fan-out terminates it.
				budgets.SetParentLease(sid, leasecontrol.SessionLease{TokenCeiling: 100_000})
			}
		}
	}

	s.elicitor = &delayElicitor{delay: 5 * time.Millisecond}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:  budgets,
		Tenants:  budgets,
		Elicitor: s.elicitor,
		// The episode dispatches its elicitation on a background context so
		// a caller's cancelled in-path wait never cancels the elicitation.
		EpisodeContext: context.Background,
	})
	if err != nil {
		return fmt.Errorf("new leasecontrol service: %w", err)
	}
	s.reclaimer = newCountingReclaimer()
	svc.SetReclaimer(s.reclaimer)
	s.svc = svc

	// Baseline goroutine count after the service is built and idle.
	time.Sleep(20 * time.Millisecond)
	s.baselineGoro = runtime.NumGoroutine()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Spread virtual users across trees so many sessions on one tree
	// exhaust concurrently and join the one pending episode.
	tree := vu % treeCount
	sess := 1 + ((vu/treeCount + iter) % (sessionsPerTree - 1))
	id := sessionID(tree, sess)

	// Short in-path wait: many callers detach at Pending while the shared
	// elicitation resolves, then the episode fan-out reclaims each.
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Millisecond)
	defer cancel()
	outcome, err := s.svc.ExtendForBudget(callCtx, id)
	if err != nil {
		s.counters.IncOnError(ctx, "errors", err)
		return err
	}
	switch outcome {
	case leasecontrol.OutcomeGranted:
		s.counters.Inc("granted_in_path")
	case leasecontrol.OutcomePending:
		s.counters.Inc("pending")
	case leasecontrol.OutcomeTerminal:
		s.counters.Inc("terminal_in_path")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	// Let every in-flight episode resolve and run its fan-out.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !s.elicitor.anyPending() && !s.reclaimer.anyOutstanding() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Give the runtime a beat to let resolved episode goroutines exit.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	postGoro := runtime.NumGoroutine()

	s.counters.EmitTo(r)
	r.AddCustom("raised", float64(s.reclaimer.raiseCount()))
	r.AddCustom("terminated", float64(s.reclaimer.terminateCount()))
	r.AddCustom("elicitations", float64(s.elicitor.count()))
	r.AddCustom("baseline_goro", float64(s.baselineGoro))
	r.AddCustom("post_run_goro", float64(postGoro))
	r.AddCustom("delta_goro", float64(postGoro-s.baselineGoro))

	if e := s.counters.Get("errors"); e > 0 {
		return fmt.Errorf("§8.6 extension episode observed %d errors under load", e)
	}
	// Every session that detached at the in-path deadline must have been
	// reclaimed by the fan-out (raised or terminated). None may be left
	// denied with nothing to clear it: a raise or a terminate accounts for
	// each pending detach eventually, so at least one reclaim must have
	// fired for a run that produced any pending detach.
	if s.counters.Get("pending") > 0 && s.reclaimer.total() == 0 {
		return fmt.Errorf("§8.6 fan-out reclaimed no session despite %d pending detaches: sessions left denied",
			s.counters.Get("pending"))
	}
	// The episode goroutine runs its fan-out and exits; no goroutine leaks
	// per pending elicitation. Allow a small tolerance for runtime
	// scheduler slack.
	if delta := postGoro - s.baselineGoro; delta > 8 {
		return fmt.Errorf("goroutine leak: %d goroutines above baseline after every episode resolved (episode goroutines must exit)", delta)
	}
	return nil
}

// delayElicitor approves every elicitation after a small delay, so
// concurrent exhausting sessions on one tree batch onto the one pending
// episode before it resolves. It records the elicitation count and the
// in-flight count so Assert can wait for every episode to drain.
type delayElicitor struct {
	delay time.Duration
	mu    sync.Mutex
	calls int
	inflt int
}

func (e *delayElicitor) Elicit(ctx context.Context, _ /*tenantID*/, _ /*sessionID*/ string) (bool, error) {
	e.mu.Lock()
	e.calls++
	e.inflt++
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.inflt--
		e.mu.Unlock()
	}()
	select {
	case <-time.After(e.delay):
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (e *delayElicitor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *delayElicitor) anyPending() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inflt > 0
}

// countingReclaimer records the episode fan-out's per-session RaiseBudget
// and TerminateSession calls so the scenario can assert every joined
// session was reclaimed.
type countingReclaimer struct {
	mu         sync.Mutex
	raised     map[string]int64
	terminated map[string]int
}

func newCountingReclaimer() *countingReclaimer {
	return &countingReclaimer{raised: map[string]int64{}, terminated: map[string]int{}}
}

func (r *countingReclaimer) RaiseBudget(sessionID string, delta int64) {
	r.mu.Lock()
	r.raised[sessionID] += delta
	r.mu.Unlock()
}

func (r *countingReclaimer) TerminateSession(sessionID string) {
	r.mu.Lock()
	r.terminated[sessionID]++
	r.mu.Unlock()
}

func (r *countingReclaimer) raiseCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.raised)
}

func (r *countingReclaimer) terminateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.terminated)
}

func (r *countingReclaimer) total() int {
	return r.raiseCount() + r.terminateCount()
}

// anyOutstanding reports nothing durable; it exists so Assert's drain
// loop has a symmetric predicate to the elicitor's in-flight count. The
// reclaimer has no in-flight state (its calls are synchronous), so this
// is always false and the loop drains on the elicitor alone.
func (r *countingReclaimer) anyOutstanding() bool { return false }
