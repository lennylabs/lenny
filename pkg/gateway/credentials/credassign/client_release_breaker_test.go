// SPDX-License-Identifier: MIT

package credassign_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// scriptedTokenService answers AssignCredentials with a lease or a
// scripted status, and records every RevokeCredentials request while
// answering it with a scripted status. It drives the gateway-side Client
// through every outcome the breaker and the release path classify.
type scriptedTokenService struct {
	tokensv1.UnimplementedTokenServiceServer

	mu         sync.Mutex
	assignCode codes.Code // codes.OK mints a lease
	revokeCode codes.Code // codes.OK revokes cleanly
	revokes    []*tokensv1.RevokeCredentialsRequest
	nextLease  int
}

func (s *scriptedTokenService) AssignCredentials(_ context.Context, req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assignCode != codes.OK {
		return nil, status.Error(s.assignCode, "scripted assign answer")
	}
	s.nextLease++
	id := "lease_" + string(rune('a'+s.nextLease-1))
	return &tokensv1.AssignCredentialsResponse{Leases: map[string]*tokensv1.CredentialLease{
		string(credential.ProviderAnthropicDirect): {
			LeaseId:      id,
			SessionId:    req.GetSessionId(),
			Provider:     string(credential.ProviderAnthropicDirect),
			Source:       string(credential.SourcePool),
			PoolId:       req.GetPoolIds()[0],
			CredentialId: "key-1",
			TenantId:     req.GetTenantId(),
			DeliveryMode: string(credential.DeliveryDirect),
		},
	}}, nil
}

func (s *scriptedTokenService) RevokeCredentials(_ context.Context, req *tokensv1.RevokeCredentialsRequest) (*tokensv1.RevokeCredentialsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokes = append(s.revokes, req)
	if s.revokeCode != codes.OK {
		return nil, status.Error(s.revokeCode, "scripted revoke answer")
	}
	return &tokensv1.RevokeCredentialsResponse{}, nil
}

func (s *scriptedTokenService) setAssign(c codes.Code) {
	s.mu.Lock()
	s.assignCode = c
	s.mu.Unlock()
}

func (s *scriptedTokenService) setRevoke(c codes.Code) {
	s.mu.Lock()
	s.revokeCode = c
	s.mu.Unlock()
}

func (s *scriptedTokenService) revokeRequests() []*tokensv1.RevokeCredentialsRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*tokensv1.RevokeCredentialsRequest(nil), s.revokes...)
}

// scriptedHarness is a Client wired over bufconn to a scriptedTokenService.
type scriptedHarness struct {
	client *credassign.Client
	server *scriptedTokenService
	leases credleasestore.LeaseStore
	sub    *subsystem.Subsystem
}

// newScriptedHarness wires a Client with replica tenant replicaTenant and
// a breaker that opens after threshold consecutive counted failures.
func newScriptedHarness(t *testing.T, replicaTenant string, threshold int) *scriptedHarness {
	t.Helper()
	srv := &scriptedTokenService{}
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	tokensv1.RegisterTokenServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.DialContext(context.Background())
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		gs.Stop()
		_ = lis.Close()
	})
	sub := &subsystem.Subsystem{
		Name:    "token_service",
		Breaker: &subsystem.Breaker{FailureThreshold: threshold, Cooldown: time.Hour},
	}
	leases := credleasestore.New()
	return &scriptedHarness{
		client: credassign.NewClient(credassign.ClientOptions{
			Stub:      tokensv1.NewTokenServiceClient(conn),
			Leases:    leases,
			Creds:     credcache.New(),
			TenantID:  replicaTenant,
			Timeout:   time.Second,
			Subsystem: sub,
		}),
		server: srv,
		leases: leases,
		sub:    sub,
	}
}

// allCodes enumerates every gRPC status code except OK.
func allNonOKCodes() []codes.Code {
	var out []codes.Code
	for c := codes.Canceled; c <= codes.Unauthenticated; c++ {
		out = append(out, c)
	}
	return out
}

// faultCodes is the §4.3 set of Token Service answers that count toward
// opening the breaker.
var faultCodes = map[codes.Code]bool{
	codes.Canceled: true, codes.Unknown: true, codes.DeadlineExceeded: true,
	codes.Aborted: true, codes.Internal: true, codes.Unavailable: true, codes.DataLoss: true,
}

// TestClientAssignBreakerEveryStatusCode_spec_4_3 sends every non-OK
// gRPC status through a real AssignCredentials RPC against a breaker
// with a threshold of one. A service fault opens the breaker; every
// healthy answer, ResourceExhausted among them, leaves it closed.
//
// spec: §4.3 (Token Service, High availability and failure handling).
func TestClientAssignBreakerEveryStatusCode_spec_4_3(t *testing.T) {
	for _, code := range allNonOKCodes() {
		t.Run(code.String(), func(t *testing.T) {
			h := newScriptedHarness(t, "acme", 1)
			h.server.setAssign(code)
			if _, err := h.client.Assign("pool-a", "s_1", "", "acme"); err == nil {
				t.Fatalf("Assign succeeded against a %s answer", code)
			}
			wantOpen := faultCodes[code]
			if got := h.sub.State() == subsystem.StateOpen; got != wantOpen {
				t.Fatalf("after one %s answer: breaker open = %v, want %v", code, got, wantOpen)
			}
		})
	}
}

// TestClientExhaustedPoolKeepsBreakerClosedForHealthyPools_spec_4_3
// reproduces the reported failure: a burst of exhausted-pool answers,
// well past the breaker threshold, must neither open the breaker nor
// stop a later assignment from a healthy pool. Each exhausted answer
// still surfaces as credential.ErrPoolExhausted.
//
// spec: §4.3 (Token Service, High availability and failure handling),
// §4.9 (Credential Leasing Service).
func TestClientExhaustedPoolKeepsBreakerClosedForHealthyPools_spec_4_3(t *testing.T) {
	h := newScriptedHarness(t, "acme", 3)
	h.server.setAssign(codes.ResourceExhausted)
	for i := 0; i < 10; i++ {
		_, err := h.client.Assign("exhausted-pool", "s_1", "", "acme")
		if !errors.Is(err, credential.ErrPoolExhausted) {
			t.Fatalf("call %d: got %v, want credential.ErrPoolExhausted", i, err)
		}
	}
	if st := h.sub.State(); st != subsystem.StateClosed {
		t.Fatalf("after 10 exhausted-pool answers: breaker state = %s, want closed", st)
	}
	h.server.setAssign(codes.OK)
	if _, err := h.client.Assign("healthy-pool", "s_2", "", "acme"); err != nil {
		t.Fatalf("healthy pool after exhausted answers: %v", err)
	}
}

// TestClientAssignLimiterRefusalReturnsError_spec_4_3 covers the path
// where the subsystem refuses the call before the RPC runs: a limiter
// with no free slot and an already-cancelled context. The Client must
// return the refusal rather than read the nil response.
//
// spec: §4.3 (Token Service, High availability and failure handling),
// §4.1 (Edge Gateway Replicas).
func TestClientAssignLimiterRefusalReturnsError_spec_4_3(t *testing.T) {
	h := newScriptedHarness(t, "acme", 3)
	lim := &subsystem.Limiter{MaxConcurrent: 1}
	release, err := lim.Acquire(context.Background())
	if err != nil {
		t.Fatalf("hold the only limiter slot: %v", err)
	}
	defer release()
	h.sub.Limiter = lim
	h.sub.Breaker = nil
	// The Client builds its own timeout context, so a saturated limiter
	// blocks until that timeout and then refuses.
	_, aerr := h.client.Assign("pool-a", "s_1", "", "acme")
	if aerr == nil {
		t.Fatal("Assign succeeded while the limiter refused the call")
	}
}

// assignLease mints one lease for session through the harness.
func assignLease(t *testing.T, h *scriptedHarness, session, tenant string) credential.Lease {
	t.Helper()
	h.server.setAssign(codes.OK)
	lease, err := h.client.Assign("pool-a", session, "", tenant)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	return lease
}

// TestClientReleaseCarriesLeaseTenant_spec_7_1 asserts that the
// RevokeCredentials request carries the tenant the lease was minted
// for, which wins over the replica-wide tenant, and that the replica
// tenant is the fallback only for a lease that recorded none.
//
// spec: §7.1 (Normal Flow, step 23), §4.3 (Token Service), §4.2
// (Session Manager, tenant_id claim extraction).
func TestClientReleaseCarriesLeaseTenant_spec_7_1(t *testing.T) {
	cases := []struct {
		name          string
		replicaTenant string
		leaseTenant   string
		wantTenant    string
	}{
		{name: "lease tenant with no replica tenant", leaseTenant: "acme", wantTenant: "acme"},
		{name: "lease tenant wins over replica tenant", replicaTenant: "default", leaseTenant: "globex", wantTenant: "globex"},
		{name: "replica tenant when the lease has none", replicaTenant: "default", wantTenant: "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newScriptedHarness(t, tc.replicaTenant, 3)
			lease := assignLease(t, h, "s_1", tc.leaseTenant)
			if lease.TenantID != tc.wantTenant {
				t.Fatalf("minted lease tenant = %q, want %q", lease.TenantID, tc.wantTenant)
			}
			if err := h.client.Release(lease.LeaseID); err != nil {
				t.Fatalf("Release: %v", err)
			}
			reqs := h.server.revokeRequests()
			if len(reqs) != 1 {
				t.Fatalf("RevokeCredentials calls = %d, want 1", len(reqs))
			}
			if reqs[0].GetTenantId() != tc.wantTenant || reqs[0].GetLeaseId() != lease.LeaseID {
				t.Fatalf("revoke request = {tenant %q lease %q}, want {tenant %q lease %q}",
					reqs[0].GetTenantId(), reqs[0].GetLeaseId(), tc.wantTenant, lease.LeaseID)
			}
		})
	}
}

// TestClientReleaseWithoutTenantSurfacesError_spec_7_1 asserts that a
// lease with no recorded tenant on a client with no replica tenant is
// not sent with an empty tenant (which the Token Service rejects): the
// release returns ErrReleaseTenantUnknown, sends no RPC, and still drops
// the local lease so the proxy stops resolving the bearer.
//
// spec: §7.1 (Normal Flow, step 23), §4.3 (Token Service).
func TestClientReleaseWithoutTenantSurfacesError_spec_7_1(t *testing.T) {
	h := newScriptedHarness(t, "", 3)
	lease := credential.Lease{
		LeaseID: "orphan", SessionID: "s_1", Provider: credential.ProviderAnthropicDirect,
		Source: credential.SourcePool, PoolID: "pool-a", CredentialID: "key-1", DeliveryMode: credential.DeliveryDirect,
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := h.leases.Put(lease); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	err := h.client.Release("orphan")
	if !errors.Is(err, credassign.ErrReleaseTenantUnknown) {
		t.Fatalf("Release = %v, want ErrReleaseTenantUnknown", err)
	}
	if n := len(h.server.revokeRequests()); n != 0 {
		t.Fatalf("RevokeCredentials calls = %d, want 0", n)
	}
	if _, ok := h.leases.GetByID("orphan"); ok {
		t.Fatal("local lease kept after a failed release")
	}
}

// TestClientReleaseEveryRevokeStatus_spec_7_1 drives every gRPC status
// the Token Service can answer RevokeCredentials with through Release.
// OK and NotFound (the lease is already gone) release cleanly, which is
// the §7.1 idempotent reconcile; every other status is returned to the
// caller, wrapped with the lease and session, rather than dropped. The
// local lease is removed in every case. A fault status also counts
// against the breaker, and a healthy answer does not.
//
// spec: §7.1 (Normal Flow, step 23), §4.3 (Token Service).
func TestClientReleaseEveryRevokeStatus_spec_7_1(t *testing.T) {
	codesUnderTest := append([]codes.Code{codes.OK}, allNonOKCodes()...)
	for _, code := range codesUnderTest {
		t.Run(code.String(), func(t *testing.T) {
			h := newScriptedHarness(t, "acme", 1)
			lease := assignLease(t, h, "s_1", "acme")
			h.server.setRevoke(code)
			err := h.client.Release(lease.LeaseID)
			wantErr := code != codes.OK && code != codes.NotFound
			if wantErr {
				if err == nil {
					t.Fatalf("revoke answered %s: Release returned nil, want the failure surfaced", code)
				}
				if status.Code(errors.Unwrap(err)) != code {
					t.Fatalf("revoke answered %s: Release error %v does not wrap the status", code, err)
				}
			} else if err != nil {
				t.Fatalf("revoke answered %s: Release = %v, want nil", code, err)
			}
			if _, ok := h.leases.GetByID(lease.LeaseID); ok {
				t.Fatalf("revoke answered %s: local lease kept after Release", code)
			}
			if got := h.sub.State() == subsystem.StateOpen; got != faultCodes[code] {
				t.Fatalf("revoke answered %s: breaker open = %v, want %v", code, got, faultCodes[code])
			}
			// A second release of the same lease is a no-op: the lease is
			// gone locally, so no further RPC is sent.
			before := len(h.server.revokeRequests())
			if err := h.client.Release(lease.LeaseID); err != nil {
				t.Fatalf("second Release = %v, want nil", err)
			}
			if after := len(h.server.revokeRequests()); after != before {
				t.Fatalf("second Release sent %d more RPCs, want 0", after-before)
			}
		})
	}
}

// TestClientReleaseBreakerOpenFailsFast_spec_4_3 asserts that a lease
// release honours the §4.3 breaker like every other Token Service call:
// with the breaker open, Release returns ErrTokenServiceUnavailable
// without reaching the server.
//
// spec: §4.3 (Token Service, High availability and failure handling),
// §7.1 (Normal Flow, step 23).
func TestClientReleaseBreakerOpenFailsFast_spec_4_3(t *testing.T) {
	h := newScriptedHarness(t, "acme", 1)
	lease := assignLease(t, h, "s_1", "acme")
	h.server.setAssign(codes.Unavailable)
	if _, err := h.client.Assign("pool-a", "s_2", "", "acme"); err == nil {
		t.Fatal("Assign succeeded against Unavailable")
	}
	if st := h.sub.State(); st != subsystem.StateOpen {
		t.Fatalf("breaker state = %s, want open", st)
	}
	err := h.client.Release(lease.LeaseID)
	if !errors.Is(err, credassign.ErrTokenServiceUnavailable) {
		t.Fatalf("Release with breaker open = %v, want ErrTokenServiceUnavailable", err)
	}
	if n := len(h.server.revokeRequests()); n != 0 {
		t.Fatalf("RevokeCredentials calls = %d, want 0 while the breaker is open", n)
	}
}

// TestClientReleaseSessionAttemptsEveryLease_spec_7_1 asserts that the
// session-teardown release attempts every lease the session holds even
// when the Token Service fails each one, returns every failure joined,
// and leaves other sessions' leases untouched. An empty or unknown
// session is a no-op.
//
// spec: §7.1 (Normal Flow, step 23).
func TestClientReleaseSessionAttemptsEveryLease_spec_7_1(t *testing.T) {
	h := newScriptedHarness(t, "acme", 100)
	a := assignLease(t, h, "s_1", "acme")
	b := assignLease(t, h, "s_1", "acme")
	other := assignLease(t, h, "s_2", "acme")

	if err := h.client.ReleaseSession(""); err != nil {
		t.Fatalf("ReleaseSession(\"\") = %v, want nil", err)
	}
	if err := h.client.ReleaseSession("s_absent"); err != nil {
		t.Fatalf("ReleaseSession(unknown) = %v, want nil", err)
	}

	h.server.setRevoke(codes.Internal)
	err := h.client.ReleaseSession("s_1")
	if err == nil {
		t.Fatal("ReleaseSession returned nil while every revoke failed")
	}
	for _, id := range []string{a.LeaseID, b.LeaseID} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("joined error %q does not name lease %s", err, id)
		}
		if _, ok := h.leases.GetByID(id); ok {
			t.Errorf("lease %s kept locally after ReleaseSession", id)
		}
	}
	if n := len(h.server.revokeRequests()); n != 2 {
		t.Fatalf("RevokeCredentials calls = %d, want 2 (one per lease of s_1)", n)
	}
	if _, ok := h.leases.GetByID(other.LeaseID); !ok {
		t.Fatal("ReleaseSession(s_1) removed s_2's lease")
	}

	h.server.setRevoke(codes.OK)
	if err := h.client.ReleaseSession("s_2"); err != nil {
		t.Fatalf("ReleaseSession(s_2) = %v, want nil", err)
	}
}
