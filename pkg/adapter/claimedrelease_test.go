// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// replaceWithSuccessor claims alice's slot for an abandoned attempt, removes
// the entry the way a compensating Shutdown does, and creates the successor
// attempt's entry and tree under the same identifier. It returns the
// abandoned claim and the successor's entry.
func replaceWithSuccessor(t *testing.T, s *Server) (abandoned slotClaim, successor *slotState) {
	t.Helper()
	seedEntry(t, s, "alice", tokenA, false)
	claim, err := s.claimSessionSlot("alice", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim alice for the abandoned attempt: %v", err)
	}
	unconditionalShutdown(t, s, context.Background(), "alice")
	if _, err := s.ensureSlotPaths("alice", slotResolve{bindAttempt: tokenB, allowCreate: true}); err != nil {
		t.Fatalf("create the successor's entry: %v", err)
	}
	return claim, slotStateForTest(s, "alice")
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)
//
// A start handler's failure rollback releases the slot only while the
// registry still holds the entry its claim was admitted against. After a
// compensating Shutdown removed that entry and a successor attempt created
// its own under the same identifier, the late rollback removes nothing: the
// successor's entry, token and tree survive and no reclaim hold is opened
// on the identifier. The guarded arm acquires a free guard; the unguarded
// arm finds the guard held by the successor's request on an expired
// context.
func TestAnAbandonedStartsRollbackLeavesTheSuccessorsEntry_spec_4_7_1(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		guarded bool
	}{{"guarded", true}, {"unguarded", false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := bindServer(t)
			claim, successor := replaceWithSuccessor(t, s)
			ctx := context.Background()
			releaseHolder := func() {}
			if !tc.guarded {
				releaseHolder = holdGuard(t, s, "alice")
				ctx = cancelledContext()
			}
			s.releaseClaimedSlot(ctx, "alice", claim)
			releaseHolder()

			if got := slotStateForTest(s, "alice"); got != successor {
				t.Fatalf("registry entry after the abandoned rollback = %p, want the successor's %p", got, successor)
			}
			if successor.bindAttempt != tokenB {
				t.Errorf("successor token = %q, want %q", successor.bindAttempt, tokenB)
			}
			if !slotDirExists(s, "alice") {
				t.Error("the abandoned rollback removed the successor's slot tree")
			}
			if reclaimHoldOpen(s, "alice") {
				t.Error("the abandoned rollback opened a reclaim hold on the successor's identifier")
			}
			if _, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenB}); err != nil {
				t.Errorf("the successor's next request = %v, want admitted with no reclaim hold", err)
			}
		})
	}
}

// reclaimHoldOpen reports whether the §5.2 reclaim hold is open on slotID.
func reclaimHoldOpen(s *Server, slotID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, held := s.reclaiming[slotID]
	return held
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// The ownership comparison is pointer identity: an untokened successor entry
// carries the same empty token as the untokened entry the abandoned claim
// took, and the rollback still leaves it in place.
func TestAnAbandonedStartsRollbackComparesTheEntryNotTheEmptyToken_spec_4_7_1(t *testing.T) {
	t.Parallel()
	s, _ := bindServer(t)
	claim, err := s.claimSessionSlot("alice", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	unconditionalShutdown(t, s, context.Background(), "alice")
	if _, err := s.ensureSlotPaths("alice", slotResolve{allowCreate: true}); err != nil {
		t.Fatalf("create the untokened successor: %v", err)
	}
	successor := slotStateForTest(s, "alice")
	s.releaseClaimedSlot(context.Background(), "alice", claim)
	if got := slotStateForTest(s, "alice"); got != successor || !slotDirExists(s, "alice") {
		t.Errorf("the rollback removed an untokened successor entry: entry %p, want %p", got, successor)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes)
//
// A rollback whose entry is still in place releases it as before: the entry
// and its tree are removed and the completed cleanup ends the hold.
func TestAStartsRollbackReleasesTheEntryItsClaimOwns_spec_5_2(t *testing.T) {
	t.Parallel()
	s, _ := bindServer(t)
	seedEntry(t, s, "alice", tokenA, false)
	if _, err := s.ensureSlotPaths("alice", slotResolve{bindAttempt: tokenA}); err != nil {
		t.Fatalf("materialize alice: %v", err)
	}
	claim, err := s.claimSessionSlot("alice", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	s.releaseClaimedSlot(context.Background(), "alice", claim)
	if hasEntry(s, "alice") || slotDirExists(s, "alice") {
		t.Error("the owning rollback left the entry or its tree")
	}
	if reclaimHoldOpen(s, "alice") {
		t.Error("the guarded owning rollback completed its cleanup and left the reclaim hold open")
	}
	if _, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenB, allowCreate: true}); err != nil {
		t.Errorf("a bind after the completed rollback = %v, want admitted", err)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier reclaim hold)
//
// An owning rollback whose guard acquisition outlived its context removes
// the entry and its tree unguarded, as releaseSessionSlot does, and keeps
// the reclaim hold: an unordered cleanup does not count as completed, so
// the identifier stays held for the life of the pod.
func TestAnUnguardedOwningRollbackKeepsTheReclaimHold_spec_5_2(t *testing.T) {
	t.Parallel()
	s, _ := bindServer(t)
	seedEntry(t, s, "alice", tokenA, false)
	if _, err := s.ensureSlotPaths("alice", slotResolve{bindAttempt: tokenA}); err != nil {
		t.Fatalf("materialize alice: %v", err)
	}
	claim, err := s.claimSessionSlot("alice", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	releaseHolder := holdGuard(t, s, "alice")
	s.releaseClaimedSlot(cancelledContext(), "alice", claim)
	releaseHolder()
	if hasEntry(s, "alice") || slotDirExists(s, "alice") {
		t.Error("the unguarded owning rollback left the entry or its tree")
	}
	if !reclaimHoldOpen(s, "alice") {
		t.Error("the unguarded owning rollback ended the reclaim hold; an unordered cleanup must keep it")
	}
	if _, err := resolveLocked(s, "alice", slotResolve{bindAttempt: tokenB, allowCreate: true}); !isSlotReclaimInProgress(err) {
		t.Errorf("a bind after the unguarded rollback = %v, want the reclaim-hold refusal", err)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes); §6.1; §7.1 (normal flow)
//
// The SDK-warm ConfigureWorkspace's fresh-claim failure arm takes the same
// fenced rollback: its runtime call is parked, a compensating Shutdown
// reclaims the entry, a successor attempt stages its own entry, and the
// runtime call then fails. The successor's entry and tree survive.
func TestAnAbandonedSDKWarmStartsLateFailureLeavesTheSuccessorsEntry_spec_4_7_1(t *testing.T) {
	t.Parallel()
	s, rt := bindServer(t)
	seedEntry(t, s, "alice", tokenA, false)
	parked, unpark := make(chan struct{}), make(chan struct{})
	rt.mu.Lock()
	rt.onConfigure = func(string) error {
		close(parked)
		<-unpark
		return errors.New("configure failed on a cancelled context")
	}
	rt.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := s.ConfigureWorkspace(context.Background(), &adapterv1.ConfigureWorkspaceRequest{
			SessionId: &adapterv1.SessionId{Value: "alice"}, Cwd: "/workspace/slots/alice/current",
		})
		done <- err
	}()
	<-parked
	unconditionalShutdown(t, s, context.Background(), "alice")
	if _, err := s.ensureSlotPaths("alice", slotResolve{bindAttempt: tokenB, allowCreate: true}); err != nil {
		t.Fatalf("create the successor's entry: %v", err)
	}
	successor := slotStateForTest(s, "alice")
	close(unpark)
	if err := <-done; status.Code(err) != codes.Internal {
		t.Fatalf("abandoned ConfigureWorkspace = %v, want the configure failure", err)
	}
	if got := slotStateForTest(s, "alice"); got != successor || !slotDirExists(s, "alice") {
		t.Errorf("the abandoned SDK-warm rollback removed the successor: entry %p, want %p", got, successor)
	}
	if reclaimHoldOpen(s, "alice") {
		t.Error("the abandoned SDK-warm rollback opened a reclaim hold on the successor's identifier")
	}
}
