// SPDX-License-Identifier: MIT

package credassign_test

import (
	"sort"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
)

// raceListStore is a lease store that records one more lease for the listed
// session right after the first LeasesBySession call returns, as a mint that
// runs concurrently with a release does.
type raceListStore struct {
	credleasestore.LeaseStore
	once  sync.Once
	extra credential.Lease
}

func (r *raceListStore) LeasesBySession(ids []string) []credential.Lease {
	out := r.LeaseStore.LeasesBySession(ids)
	r.once.Do(func() { _ = r.Put(r.extra) })
	return out
}

func leaseIDs(ls []credential.Lease) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.LeaseID)
	}
	sort.Strings(out)
	return out
}

func multiCredPool() credassign.Pool {
	return proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-1"), healthyCred("key-2", "sk-2"), healthyCred("key-3", "sk-3"))
}

// TestAssignRecordsTheCoordinationGeneration_spec_7_3 asserts that a lease
// minted through Assign and AssignProto records the generation the caller
// passed, so a generation-scoped release can tell incarnations apart.
//
// spec: §7.3 (Credential leases on the replacement pod), §4.9 (Proactive Lease
// Renewal)
func TestAssignRecordsTheCoordinationGeneration_spec_7_3(t *testing.T) {
	svc, leases, _ := newService(t)
	svc.RegisterPool(multiCredPool())
	l, err := svc.Assign("claude-prod", "s_1", "", "acme", 4)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if got, _ := leases.GetByID(l.LeaseID); got.CoordinationGeneration != 4 {
		t.Errorf("stored generation = %d, want 4", got.CoordinationGeneration)
	}
	wire, err := svc.AssignProto("claude-prod", "s_1", "", "acme", 9)
	if err != nil {
		t.Fatalf("AssignProto: %v", err)
	}
	if got, _ := leases.GetByID(wire.GetLeaseId()); got.CoordinationGeneration != 9 {
		t.Errorf("stored generation = %d, want 9", got.CoordinationGeneration)
	}
}

// TestServiceReleaseThroughKeepsLaterLeases_spec_7_3 asserts that
// ReleaseThrough releases the session's leases at or below the generation,
// including one recorded while the release runs, and keeps every later lease
// and every other session's lease, returning each released lease's pool slot.
//
// spec: §7.3 (Credential leases on the replacement pod)
func TestServiceReleaseThroughKeepsLaterLeases_spec_7_3(t *testing.T) {
	inner := credleasestore.New()
	race := &raceListStore{LeaseStore: inner}
	svc := credassign.New(race, credcache.New())
	svc.RegisterPool(multiCredPool())
	early, _ := svc.Assign("claude-prod", "s_1", "", "acme", 2)
	at, _ := svc.Assign("claude-prod", "s_1", "", "acme", 3)
	late, _ := svc.Assign("claude-prod", "s_1", "", "acme", 4)
	other, _ := svc.Assign("claude-prod", "s_2", "", "acme", 1)
	race.extra = early
	race.extra.LeaseID = "cl-recorded-during-release"
	race.extra.CoordinationGeneration = 3
	race.extra.Proxy = &credential.ProxyConfig{LeaseToken: "lt-during"}

	if err := svc.ReleaseThrough("s_1", 3); err != nil {
		t.Fatalf("ReleaseThrough: %v", err)
	}
	if got := leaseIDs(inner.LeasesBySession([]string{"s_1"})); len(got) != 1 || got[0] != late.LeaseID {
		t.Errorf("s_1 leases = %v, want only the later %s", got, late.LeaseID)
	}
	if _, ok := inner.GetByID(other.LeaseID); !ok {
		t.Error("another session's lease was released")
	}
	for _, id := range []string{early.LeaseID, at.LeaseID} {
		if _, ok := inner.GetByID(id); ok {
			t.Errorf("lease %s at or below the generation survived", id)
		}
	}
	// The released slots are free again: three more mints fit on the
	// three-credential pool beside the two kept leases only if the
	// released leases' slots were returned (least-loaded keeps assigning).
	if _, err := svc.Assign("claude-prod", "s_3", "", "acme", 0); err != nil {
		t.Errorf("mint after the release: %v", err)
	}
}

// TestServiceReleaseSessionCatchesALeaseRecordedDuringTheRelease_spec_4_9
// asserts that ReleaseSession lists the session's leases twice, so a lease
// recorded while the release runs is released too.
//
// spec: §4.9 (Proactive Lease Renewal)
func TestServiceReleaseSessionCatchesALeaseRecordedDuringTheRelease_spec_4_9(t *testing.T) {
	inner := credleasestore.New()
	race := &raceListStore{LeaseStore: inner}
	svc := credassign.New(race, credcache.New())
	svc.RegisterPool(multiCredPool())
	first, _ := svc.Assign("claude-prod", "s_1", "", "acme", 1)
	race.extra = first
	race.extra.LeaseID = "cl-recorded-during-release"
	race.extra.Proxy = &credential.ProxyConfig{LeaseToken: "lt-during"}

	if err := svc.ReleaseSession("s_1"); err != nil {
		t.Fatalf("ReleaseSession: %v", err)
	}
	if got := inner.LeasesBySession([]string{"s_1"}); len(got) != 0 {
		t.Errorf("leases left = %v, want none", leaseIDs(got))
	}
}

// TestClientReleaseThroughAndSessionRevokeEveryMatch_spec_7_3 asserts that
// the gateway-side Client's ReleaseThrough keeps later leases and revokes the
// earlier ones on the Token Service, and that ReleaseSession revokes a lease
// recorded while the release runs.
//
// spec: §7.3 (Credential leases on the replacement pod), §4.9 (Proactive Lease
// Renewal)
func TestClientReleaseThroughAndSessionRevokeEveryMatch_spec_7_3(t *testing.T) {
	h := newClientHarness(t, multiCredPool())
	defer h.closer()
	early, err := h.client.Assign("claude-prod", "s_1", "", "", 2)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	late, err := h.client.Assign("claude-prod", "s_1", "", "", 5)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if got, _ := h.clientLeases.GetByID(late.LeaseID); got.CoordinationGeneration != 5 {
		t.Errorf("client-side generation = %d, want 5", got.CoordinationGeneration)
	}
	if err := h.client.ReleaseThrough("s_1", 3); err != nil {
		t.Fatalf("ReleaseThrough: %v", err)
	}
	if _, ok := h.serverLeases.GetByID(early.LeaseID); ok {
		t.Error("the earlier lease is still recorded on the Token Service")
	}
	if _, ok := h.serverLeases.GetByID(late.LeaseID); !ok {
		t.Error("the later lease was revoked on the Token Service")
	}
	if _, ok := h.clientLeases.GetByID(late.LeaseID); !ok {
		t.Error("the later lease was dropped from the gateway's store")
	}

	if err := h.client.ReleaseSession("s_1"); err != nil {
		t.Fatalf("ReleaseSession: %v", err)
	}
	if _, ok := h.serverLeases.GetByID(late.LeaseID); ok {
		t.Error("ReleaseSession left the later lease recorded on the Token Service")
	}
}
