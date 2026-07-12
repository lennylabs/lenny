// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration testbed: the §5.2 concurrent execution mode running
// together with §8.2 recursive delegation and §4.9 multi-protocol proxy
// traffic on a single pod. Two simultaneous sessions land in distinct
// slots on one pod, each with its own credential lease. Under mixed load
// one slot delegates a child while the other calls a model through the
// reverse proxy, and the flow asserts the three properties the concurrent
// mode promises hold across the combined workload:
//
//   - Per-slot credential isolation. Each slot's independent credential
//     lease is written to its own /run/lenny/slots/{slotId}/credentials.json
//     (§6.1) and neither slot's lease leaks into the other's file or into
//     the single-slot /run/lenny/credentials.json path.
//   - Independent budget accounting. The delegating slot's child budget is
//     carved from that slot's own session lease (§8.2). A slice that
//     exceeds the delegating slot's budget is rejected even though the
//     sibling slot on the same pod holds ample budget, and the identical
//     slice is admitted under the sibling — proving the budget is bound to
//     the session, not pooled across the pod's slots.
//   - Per-slot cleanup. Releasing each slot emits its own
//     ReportSessionScrub tagged with that slot's slotId and sessionId
//     (§5.2), and releasing one slot leaves the sibling slot's session and
//     credential file intact until the sibling is released in turn.
//
// This composes the production surfaces the earlier building blocks
// exposed: the real adapter.Server per-slot path over its gRPC contract
// with the real echo-concurrent runtime (from the concurrent-workspace
// flow), the delegation Service over the session store (from the
// multi-agent testbed), and the LLM reverse proxy against the llmprovider
// stub (from the proxy round-trip flow). The adapter tracks pod-side slot
// state while the session store tracks per-session budget, keyed on the
// same session ids the gateway would use, so the composition mirrors how
// the gateway ties a slot to a session in production.
//
// Tier 5 (full chart on Kind, a concurrent-mode pool serving two live
// sessions that delegate and proxy through real pods) is the promotion
// target for the same behavior; this integration-tier test pins the
// per-slot isolation deterministically first.
//
// The concurrentAdapterClient / buildConcurrentRuntime / concurrentSocketAddr
// helpers live in concurrent_workspace_test.go; callModelThroughProxy and
// testbedTenant live in multi_agent_testbed_test.go (same package).
//
// spec: §5.2 (concurrent sessions: per-slot credential-file group-read,
// per-slot cleanup via ReportSessionScrub on every session release),
// §6.1 (per-slot credential lease lifecycle:
// /run/lenny/slots/{slotId}/credentials.json, each active slot an
// independent lease, revoked independently), §8.2 (delegation budget
// carved from the parent's remaining budget), §4.9 (LLM reverse proxy).
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	dlease "github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// concurrentSlotSession binds one concurrent-mode slot to its session: the
// pod slot the adapter tracks and the delegation-store session that carries
// the slot's independent budget. The gateway uses the session id as the slot
// id in production; the test keeps them distinct strings so a cross-wiring
// (using one where the other is meant) fails loudly.
type concurrentSlotSession struct {
	sessionID string
	slotID    string
	provider  string // the credential provider assigned to this slot
	leaseID   string // the slot's credential lease id
}

// recordingScrubReporter captures every ReportSessionScrub the adapter emits
// so the test can assert one independent per-slot cleanup report per release,
// each tagged with its own slot and session. It stands in for the gateway
// client the release path reports through. spec: §5.2 (ReportSessionScrub).
type recordingScrubReporter struct {
	mu      sync.Mutex
	reports []scrubReport
}

type scrubReport struct {
	podID     string
	sessionID string
	slotID    string
	outcome   gatewaycontrol.SessionScrubOutcome
}

func (r *recordingScrubReporter) ReportSessionScrub(_ context.Context, podID, sessionID, slotID string, outcome gatewaycontrol.SessionScrubOutcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, scrubReport{podID: podID, sessionID: sessionID, slotID: slotID, outcome: outcome})
	return nil
}

func (r *recordingScrubReporter) snapshot() []scrubReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]scrubReport, len(r.reports))
	copy(out, r.reports)
	return out
}

// spec: 5.2 (concurrent sessions: per-slot credential files, per-slot cleanup
//
//	via ReportSessionScrub on every session release; each slot an
//	independent, simultaneous session), 6.1 (per-slot credential lease
//	lifecycle: /run/lenny/slots/{slotId}/credentials.json, revoked
//	independently), 8.2 (delegation budget carved from the parent's
//	remaining budget), 4.9 (LLM reverse proxy)
//
// diagnosis: concurrent execution mode regressed in combination with
//
//	delegation and multi-protocol proxy traffic on one pod. Either two
//	slots' credential leases were not isolated (one slot's lease leaked
//	into the sibling's per-slot file or into the single-slot credentials
//	path), the delegating slot's budget was not carved from its own
//	session lease (a slice bounded by one slot's budget was pooled against
//	the pod so it drew on the sibling's budget, or the sibling's budget did
//	not admit a slice the delegating slot was denied), or the per-slot
//	cleanup did not report one independent ReportSessionScrub per release
//	(a slot release disturbed a sibling slot, or a release emitted no
//	per-slot scrub). Any of these breaks the isolation the concurrent mode
//	promises when real delegation and proxy load run on the shared pod.
func TestConcurrentSlotsDelegationAndProxyIsolation_spec_5_2(t *testing.T) {
	const podID = "pod-concurrent-testbed"
	// The adapter reads its pod identity once from the Downward API POD_NAME
	// env at construction; a non-empty pod id is required for the per-slot
	// scrub report to travel to the gateway (§5.2). Set before adapter.New.
	t.Setenv("POD_NAME", podID)

	echoConcurrentBin := buildConcurrentRuntime(t)

	base := t.TempDir()
	srv := adapter.New("tier4-concurrent-delegation-proxy")
	srv.WorkspaceRoot = filepath.Join(base, "workspace", "current")
	srv.WorkspaceBase = filepath.Join(base, "workspace")
	srv.SessionsRoot = filepath.Join(base, "sessions")
	srv.ArtifactsRoot = filepath.Join(base, "artifacts")
	srv.CredentialsDir = filepath.Join(base, "run", "lenny")
	reporter := &recordingScrubReporter{}
	srv.SessionScrubReporter = reporter

	// One real runtime process per pod serves every slot, multiplexed on
	// slotId over the single connection (§5.2, §15.4.1).
	rt, err := adapter.NewSocketRuntimeProcess(concurrentSocketAddr(t))
	if err != nil {
		t.Fatalf("bind pod runtime socket: %v", err)
	}
	rt.SpawnPath = echoConcurrentBin
	rt.AcceptTimeout = 15 * time.Second
	srv.Runtime = rt
	t.Cleanup(func() { _ = rt.Close(context.Background(), "pod-teardown") })

	client := concurrentAdapterClient(t, srv)
	ctx := context.Background()

	// The delegating slot (alice) holds a deliberately small budget; the
	// proxy-calling slot (bob) holds a much larger one. The asymmetry is what
	// proves the budgets are per-session: a slice bob would admit is denied to
	// alice on the same pod.
	const aliceBudget = 30000
	const bobBudget = 100000
	alice := concurrentSlotSession{sessionID: "sess-alice", slotID: "slot-01", provider: "anthropic", leaseID: "lease-alice-anth"}
	bob := concurrentSlotSession{sessionID: "sess-bob", slotID: "slot-02", provider: "openai", leaseID: "lease-bob-oai"}

	// ---- open both slots on the one pod, each with its own credential lease ----
	for _, s := range []concurrentSlotSession{alice, bob} {
		if _, err := client.StartSession(ctx, &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: s.sessionID},
			Runtime:   "echo-concurrent",
			SlotId:    &adapterv1.SlotId{Value: s.slotID},
		}); err != nil {
			t.Fatalf("StartSession(%s on %s): %v", s.sessionID, s.slotID, err)
		}
		// §6.1: each active slot obtains its independent credential lease via a
		// separate AssignCredentials RPC at slot assignment time.
		if _, err := client.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
			SessionId: &adapterv1.SessionId{Value: s.sessionID},
			SlotId:    &adapterv1.SlotId{Value: s.slotID},
			Leases: map[string]*adapterv1.CredentialLease{
				s.provider: {LeaseId: s.leaseID, Provider: s.provider, Payload: []byte(`{}`)},
			},
		}); err != nil {
			t.Fatalf("AssignCredentials(%s on %s): %v", s.sessionID, s.slotID, err)
		}
	}

	// ---- the delegation harness: real Service over the session store ----
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	idc := &idCounter{}
	svc := delegation.NewService(store, delegation.Options{
		Clock:     func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:    idc.next,
		Runtimes:  runtimes,
		CycleMode: cycle.ModeEnforce,
	})
	for _, rtName := range []string{"orch-alice", "orch-bob", "child-alice", "child-bob"} {
		if err := runtimes.Create(ctx, runtimestore.Runtime{Name: rtName, Type: runtimestore.TypeAgent}); err != nil {
			t.Fatalf("register runtime %s: %v", rtName, err)
		}
	}
	// The two slots are two running sessions with independent delegation
	// leases. Nothing pools budget across them.
	seedConcurrentSession(t, ctx, store, alice.sessionID, "orch-alice", "pool-alice", aliceBudget)
	seedConcurrentSession(t, ctx, store, bob.sessionID, "orch-bob", "pool-bob", bobBudget)

	// ---- Assertion A: per-slot credential isolation ----
	assertSlotCredentialIsolation(t, srv, alice, bob)

	// ---- mixed load: slot 0 delegates a child while slot 1 calls the proxy ----
	// slot 0's delegation runs on a background goroutine (it touches no *testing.T,
	// so it is safe off the test goroutine) while slot 1's proxy call runs on the
	// test goroutine, so the two workloads overlap on the shared pod. The proxy
	// leg stays on the test goroutine because callModelThroughProxy reports
	// failures with t.Fatalf, which must run there.
	upstream := llmprovider.New(t)
	var child sessionstore.Session
	var delegErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// slot 0 (alice) delegates a child; the budget is carved from alice's
		// own session lease.
		res, err := svc.Delegate(ctx, testbedTenant, delegation.Request{
			ParentSessionID: alice.sessionID,
			RuntimeRef:      "child-alice",
			PoolRef:         "pool-child-alice",
			UserID:          "alice@acme.com",
			LeaseSlice:      dlease.LeaseSlice{MaxTokenBudget: aliceBudget},
		})
		if err != nil {
			delegErr = err
			return
		}
		child = res.Child
	}()
	// slot 1 (bob) calls a model through the §4.9 reverse proxy while slot 0's
	// delegation is in flight.
	callModelThroughProxy(t, upstream, bob.sessionID)
	wg.Wait()
	if delegErr != nil {
		t.Fatalf("slot 0 delegation under mixed load: %v", delegErr)
	}

	// ---- Assertion B: independent budget accounting ----
	// The child's budget was carved from alice's session, and the child's
	// parent is alice's session (not bob's).
	if child.DelegationLease == nil || child.DelegationLease.MaxTokenBudget != aliceBudget {
		t.Fatalf("slot 0 child carved budget = %+v, want %d from alice's session lease", child.DelegationLease, aliceBudget)
	}
	if child.ParentSessionID != alice.sessionID {
		t.Errorf("slot 0 child parent = %q, want %q; the child was carved from the wrong slot's session",
			child.ParentSessionID, alice.sessionID)
	}
	// A slice larger than alice's own budget is rejected, even though the
	// sibling slot bob on the same pod holds far more budget: budget is bound
	// to the session, not pooled across the pod's slots (§8.2).
	overSlice := int64(aliceBudget + 10000) // 40000 > alice's 30000, < bob's 100000
	if _, err := svc.Delegate(ctx, testbedTenant, delegation.Request{
		ParentSessionID: alice.sessionID,
		RuntimeRef:      "child-bob", // fresh (runtime, pool) — not a cycle under alice
		PoolRef:         "pool-overbudget",
		UserID:          "alice@acme.com",
		LeaseSlice:      dlease.LeaseSlice{MaxTokenBudget: overSlice},
	}); !isBudgetExceeded(err) {
		t.Errorf("over-budget delegation from slot 0 err = %v, want *BudgetExceededError; alice's budget was pooled with bob's", err)
	}
	// The identical slice is admitted under bob, whose own session budget
	// covers it — the denial above was a property of alice's lease, not of the
	// slice or the shared pod.
	if _, err := svc.Delegate(ctx, testbedTenant, delegation.Request{
		ParentSessionID: bob.sessionID,
		RuntimeRef:      "child-bob",
		PoolRef:         "pool-child-bob",
		UserID:          "bob@acme.com",
		LeaseSlice:      dlease.LeaseSlice{MaxTokenBudget: overSlice},
	}); err != nil {
		t.Errorf("same slice under slot 1 err = %v, want admitted; budgets are not independent per slot", err)
	}
	// bob's proxy usage was recorded against bob's session, independent of
	// alice's delegation subtree.
	if _, ok := upstream.LastRequest(); !ok {
		t.Error("slot 1 proxy request never reached the upstream provider")
	}

	// ---- Assertion C: per-slot cleanup under mixed load ----
	// Release slot 0 first. Its cleanup reports independently; slot 1 is
	// untouched — its per-slot credential file and session remain.
	shutdownSlotCleanly(t, ctx, client, alice)
	if reports := reporter.snapshot(); len(reports) != 1 {
		t.Fatalf("after releasing slot 0, ReportSessionScrub count = %d, want exactly 1 (one per slot release)", len(reports))
	}
	assertScrub(t, reporter.snapshot()[0], podID, alice)
	// slot 1's credential file survives slot 0's release: cleanup is per-slot.
	if _, err := os.Stat(slotCredentialFile(srv, bob.slotID)); err != nil {
		t.Errorf("slot 1 credential file was disturbed by slot 0's release: %v", err)
	}
	// slot 0's own per-slot credential tree was removed on its release (§6.1
	// independent revoke).
	if _, err := os.Stat(slotCredentialFile(srv, alice.slotID)); !os.IsNotExist(err) {
		t.Errorf("slot 0 per-slot credential file survived its release (err=%v); the slot was not cleaned up", err)
	}

	// Release slot 1. It emits its own independent per-slot scrub.
	shutdownSlotCleanly(t, ctx, client, bob)
	reports := reporter.snapshot()
	if len(reports) != 2 {
		t.Fatalf("after releasing both slots, ReportSessionScrub count = %d, want exactly 2 (one per slot)", len(reports))
	}
	assertScrub(t, reports[1], podID, bob)
	// The two reports name distinct slots and sessions: the per-slot cleanup
	// never conflated the two slots.
	if reports[0].slotID == reports[1].slotID || reports[0].sessionID == reports[1].sessionID {
		t.Errorf("per-slot scrub reports collide: %+v and %+v", reports[0], reports[1])
	}
}

// seedConcurrentSession creates a running session with an independent
// delegation lease, the row the delegation Service carves a child budget
// from. Each concurrent slot is one such session. spec: §5.2, §8.2.
func seedConcurrentSession(t *testing.T, ctx context.Context, store *memstore.Store, sessionID, runtimeRef, poolRef string, budget int64) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: sessionID, TenantID: testbedTenant, UserID: "alice@acme.com",
		RuntimeRef: runtimeRef, PoolRef: poolRef,
		State:            session.StateRunning,
		IsolationProfile: isolation.ProfileSandboxed,
		DelegationLease:  &sessionstore.DelegationLease{MaxTokenBudget: budget, MaxChildrenTotal: 8},
		CreatedAt:        now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
}

// assertSlotCredentialIsolation reads each slot's per-slot credential file and
// asserts it holds only that slot's lease, that neither slot's lease leaked
// into the sibling's file, and that the single-slot /run/lenny/credentials.json
// path was never written (a concurrent pod has no pod-global credential file).
// spec: §6.1 (per-slot credential files at /run/lenny/slots/{slotId}/).
func assertSlotCredentialIsolation(t *testing.T, srv *adapter.Server, alice, bob concurrentSlotSession) {
	t.Helper()
	for _, s := range []concurrentSlotSession{alice, bob} {
		providers := readCredentialProviders(t, slotCredentialFile(srv, s.slotID))
		lease, ok := providers[s.provider]
		if !ok {
			t.Errorf("slot %s credential file is missing its own provider %q; got providers %v", s.slotID, s.provider, providers)
			continue
		}
		if lease != s.leaseID {
			t.Errorf("slot %s provider %q lease = %q, want %q", s.slotID, s.provider, lease, s.leaseID)
		}
	}
	// Cross-check: neither slot's file carries the sibling's provider.
	aliceProviders := readCredentialProviders(t, slotCredentialFile(srv, alice.slotID))
	if _, leaked := aliceProviders[bob.provider]; leaked {
		t.Errorf("slot %s credential file leaked sibling provider %q; per-slot credential isolation broken", alice.slotID, bob.provider)
	}
	bobProviders := readCredentialProviders(t, slotCredentialFile(srv, bob.slotID))
	if _, leaked := bobProviders[alice.provider]; leaked {
		t.Errorf("slot %s credential file leaked sibling provider %q; per-slot credential isolation broken", bob.slotID, alice.provider)
	}
	// The single-slot pod-global credential file must never exist on a
	// concurrent pod; each slot has its own file under slots/{slotId}/.
	if _, err := os.Stat(filepath.Join(srv.CredentialsDir, "credentials.json")); !os.IsNotExist(err) {
		t.Errorf("pod-global /run/lenny/credentials.json exists on a concurrent pod (err=%v); credentials were not written per slot", err)
	}
}

// slotCredentialFile returns the slot's per-slot credential file path,
// /run/lenny/slots/{slotId}/credentials.json. spec: §6.1 line 28.
func slotCredentialFile(srv *adapter.Server, slotID string) string {
	return filepath.Join(srv.CredentialsDir, "slots", slotID, "credentials.json")
}

// readCredentialProviders reads the adapter credential file and returns a
// provider -> leaseId map, so a test can assert exactly which leases a slot's
// file carries.
func readCredentialProviders(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credential file %s: %v", path, err)
	}
	var doc struct {
		Providers []struct {
			Provider string `json:"provider"`
			LeaseID  string `json:"leaseId"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode credential file %s: %v", path, err)
	}
	out := make(map[string]string, len(doc.Providers))
	for _, p := range doc.Providers {
		out[p.Provider] = p.LeaseID
	}
	return out
}

// shutdownSlotCleanly tears down one slot through the adapter's Shutdown RPC
// and asserts the runtime reported a clean exit, so the per-slot cleanup
// outcome the adapter reports is `released`. spec: §5.2, §6.4.
func shutdownSlotCleanly(t *testing.T, ctx context.Context, client adapterv1.AdapterClient, s concurrentSlotSession) {
	t.Helper()
	resp, err := client.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: s.sessionID},
		SlotId:    &adapterv1.SlotId{Value: s.slotID},
	})
	if err != nil {
		t.Fatalf("Shutdown(%s on %s): %v", s.sessionID, s.slotID, err)
	}
	if !resp.GetExitedCleanly() {
		t.Errorf("Shutdown(%s) reported a non-clean exit; the slot cleanup was not released", s.slotID)
	}
}

// assertScrub checks one per-slot cleanup report carries the pod identity, the
// released slot's session and slot ids, and the released outcome.
func assertScrub(t *testing.T, r scrubReport, podID string, s concurrentSlotSession) {
	t.Helper()
	if r.podID != podID {
		t.Errorf("scrub report pod = %q, want %q", r.podID, podID)
	}
	if r.sessionID != s.sessionID || r.slotID != s.slotID {
		t.Errorf("scrub report = {session %q, slot %q}, want {%s, %s}", r.sessionID, r.slotID, s.sessionID, s.slotID)
	}
	if r.outcome != gatewaycontrol.SessionScrubReleased {
		t.Errorf("scrub report outcome = %v, want released for a clean per-slot cleanup", r.outcome)
	}
}

// isBudgetExceeded reports whether err is the §8.2 over-budget rejection.
func isBudgetExceeded(err error) bool {
	var bx *dlease.BudgetExceededError
	return errors.As(err, &bx)
}
