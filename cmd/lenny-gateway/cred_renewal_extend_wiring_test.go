// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/migrations"
	sessionapi "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	credleasepg "github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/kms"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// These tests pin the §4.9 Token Service unavailability guard wiring in
// cmd/lenny-gateway: credRenewalWiring.Renew maps the breaker-open
// credassign.ErrTokenServiceUnavailable to credrenewal.ErrRenewInfraUnavailable
// so the worker holds and reschedules a still-valid lease; credRenewalWiring.onExtend
// dispatches the extension by delivery mode (the adapter ExtendCredentialLease RPC
// for direct mode, a GetByID+Put advance of the gateway lease store the LLM Proxy
// reads for proxy mode); the two tracking sites stamp Lease.LeaseTTL so the
// cumulative-extension cap stays TTL-derived across a normal renewal; and
// onExtensionCapReached tears the capped session down to the §8.8 expired state
// with the expired:lease reason rather than entering the Fallback Flow.
//
// spec: §4.9 line 1470 (Token Service unavailability guard); §8.8 line 869
// (expired:lease surfacing).

// extendRecorder is a minimal Adapter gRPC server that captures the
// ExtendCredentialLease request the direct-mode guard sends.
type extendRecorder struct {
	adapterv1.UnimplementedAdapterServer
	mu  sync.Mutex
	got *adapterv1.ExtendCredentialLeaseRequest
}

func (r *extendRecorder) ExtendCredentialLease(_ context.Context, req *adapterv1.ExtendCredentialLeaseRequest) (*adapterv1.ExtendCredentialLeaseResponse, error) {
	r.mu.Lock()
	r.got = req
	r.mu.Unlock()
	return &adapterv1.ExtendCredentialLeaseResponse{}, nil
}

func (r *extendRecorder) last() *adapterv1.ExtendCredentialLeaseRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got
}

// dialExtendRecorder serves rec over an in-memory connection and returns
// an adapter client wired to it.
func dialExtendRecorder(t *testing.T, rec *extendRecorder) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, rec)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial extend recorder: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// recordingReasonTerminator captures the (sessionID, reason) pair the
// §4.9 cumulative-extension-cap teardown passes to the session terminator,
// so a test can assert the reason is the §8.8 expired:lease FailureReason.
type recordingReasonTerminator struct {
	mu       sync.Mutex
	sessions []string
	reasons  []string
}

func (r *recordingReasonTerminator) TerminateSession(sessionID, reason string) {
	r.mu.Lock()
	r.sessions = append(r.sessions, sessionID)
	r.reasons = append(r.reasons, reason)
	r.mu.Unlock()
}

// fakeExtendAssigner is a credassign.Assigner stand-in whose Assign
// returns a scripted lease or error, so the wiring's breaker-open sentinel
// mapping and LeaseTTL stamping can be exercised without a real pool.
type fakeExtendAssigner struct {
	assignErr error
	lease     credential.Lease
}

func (f *fakeExtendAssigner) Assign(_, _, _, _ string) (credential.Lease, error) {
	if f.assignErr != nil {
		return credential.Lease{}, f.assignErr
	}
	return f.lease, nil
}

func (f *fakeExtendAssigner) AssignProto(_, _, _, _ string) (*adapterv1.CredentialLease, error) {
	return nil, nil
}

func (f *fakeExtendAssigner) ProtoLeaseByID(string) (*adapterv1.CredentialLease, error) {
	return nil, nil
}
func (f *fakeExtendAssigner) Release(string)                              {}
func (f *fakeExtendAssigner) ReleaseSession(string)                       {}
func (f *fakeExtendAssigner) OnAssigned(func(credassign.LeaseAssignment)) {}

// directLease builds a valid direct-mode pool-backed lease record for the
// lease store.
func extendDirectLease(leaseID, sessionID string, issuedAt, expiresAt time.Time) credential.Lease {
	return credential.Lease{
		LeaseID:      leaseID,
		SessionID:    sessionID,
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryDirect,
		IssuedAt:     issuedAt,
		ExpiresAt:    expiresAt,
		RenewBefore:  expiresAt.Add(-5 * time.Minute),
	}
}

// proxyLease builds a valid proxy-mode pool-backed lease record for the
// lease store.
func extendProxyLease(leaseID, sessionID string, issuedAt, expiresAt time.Time) credential.Lease {
	return credential.Lease{
		LeaseID:      leaseID,
		SessionID:    sessionID,
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryProxy,
		IssuedAt:     issuedAt,
		ExpiresAt:    expiresAt,
		RenewBefore:  expiresAt.Add(-5 * time.Minute),
		Proxy: &credential.ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			LeaseToken:   "lt-" + leaseID,
		},
	}
}

// TestOnExtendDirectModeDispatchesToAdapter proves a direct-mode lease
// under the §4.9 Token Service unavailability guard re-arms the adapter
// expiry timer over ExtendCredentialLease with the new deadline, and does
// not advance the lease-store record (the enforcement point for a
// direct-mode lease is the adapter timer, not the store). A regression
// that routed a direct-mode extension to the lease store would leave the
// adapter timer firing at the old deadline and delete the credential file.
func TestOnExtendDirectModeDispatchesToAdapter_spec_4_9(t *testing.T) {
	issuedAt := time.Now().Add(-30 * time.Minute)
	expiresAt := time.Now().Add(5 * time.Minute)
	rec := extendDirectLease("cl-direct", "run_a", issuedAt, expiresAt)

	store := credleasestore.New()
	if err := store.Put(rec); err != nil {
		t.Fatalf("seed direct lease: %v", err)
	}

	adapter := &extendRecorder{}
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "run_a", Adapter: dialExtendRecorder(t, adapter)})

	wiring := newCredRenewalWiring(&fakeExtendAssigner{}, registry, nil, store, nil)
	wiring.pools["cl-direct"] = renewalProvider{pool: "claude-prod", provider: "anthropic_direct"}

	newExpiresAt := expiresAt.Add(5 * time.Minute)
	if err := wiring.onExtend(credrenewal.Lease{
		LeaseID:   "cl-direct",
		SessionID: "run_a",
		ExpiresAt: expiresAt,
	}, newExpiresAt); err != nil {
		t.Fatalf("onExtend direct: %v", err)
	}

	got := adapter.last()
	if got == nil {
		t.Fatal("direct-mode extension sent no ExtendCredentialLease RPC to the adapter")
	}
	if got.GetSessionId().GetValue() != "run_a" {
		t.Errorf("ExtendCredentialLease session = %q, want run_a", got.GetSessionId().GetValue())
	}
	if got.GetProvider() != "anthropic_direct" {
		t.Errorf("ExtendCredentialLease provider = %q, want anthropic_direct", got.GetProvider())
	}
	if got.GetLeaseId() != "cl-direct" {
		t.Errorf("ExtendCredentialLease leaseId = %q, want cl-direct", got.GetLeaseId())
	}
	if got.GetExpiresAtUnixMs() != newExpiresAt.UnixMilli() {
		t.Errorf("ExtendCredentialLease expiresAt = %d, want %d", got.GetExpiresAtUnixMs(), newExpiresAt.UnixMilli())
	}

	// The lease store must be untouched: a direct-mode extension does not
	// advance the stored record's deadline.
	after, ok := store.GetByID("cl-direct")
	if !ok {
		t.Fatal("direct lease vanished from the store")
	}
	if !after.ExpiresAt.Equal(expiresAt) {
		t.Errorf("direct-mode extension advanced the lease-store ExpiresAt to %v, want it untouched at %v", after.ExpiresAt, expiresAt)
	}
}

// TestOnExtendProxyModeAdvancesLeaseStore proves a proxy-mode lease under
// the guard advances its ExpiresAt/RenewBefore in the gateway lease store
// the LLM Proxy reads (with no adapter RPC), so the proxy's server-side
// expiry check honors the extension. The new RenewBefore is the
// pre-extension ExpiresAt, matching the worker's own arithmetic.
func TestOnExtendProxyModeAdvancesLeaseStore_spec_4_9(t *testing.T) {
	issuedAt := time.Now().Add(-30 * time.Minute)
	expiresAt := time.Now().Add(5 * time.Minute)
	rec := extendProxyLease("cl-proxy", "run_b", issuedAt, expiresAt)

	store := credleasestore.New()
	if err := store.Put(rec); err != nil {
		t.Fatalf("seed proxy lease: %v", err)
	}

	// An empty registry: a proxy-mode extension must not consult the adapter
	// registry at all, so a missing pod binding is irrelevant.
	wiring := newCredRenewalWiring(&fakeExtendAssigner{}, podsession.NewRegistry(), nil, store, nil)

	newExpiresAt := expiresAt.Add(5 * time.Minute)
	if err := wiring.onExtend(credrenewal.Lease{
		LeaseID:   "cl-proxy",
		SessionID: "run_b",
		ExpiresAt: expiresAt,
	}, newExpiresAt); err != nil {
		t.Fatalf("onExtend proxy: %v", err)
	}

	after, ok := store.GetByID("cl-proxy")
	if !ok {
		t.Fatal("proxy lease vanished from the store")
	}
	if !after.ExpiresAt.Equal(newExpiresAt) {
		t.Errorf("proxy-mode extension ExpiresAt = %v, want %v", after.ExpiresAt, newExpiresAt)
	}
	if !after.RenewBefore.Equal(expiresAt) {
		t.Errorf("proxy-mode extension RenewBefore = %v, want the pre-extension ExpiresAt %v", after.RenewBefore, expiresAt)
	}
}

// TestOnExtendProxyModePgStore proves the proxy-mode advance lands on the
// Postgres-backed pgstore.Store the LLM Proxy reads under the durable
// topology (w.llmLeases is swapped to pgstore when Postgres is configured),
// so the guard cannot regress by writing to a store the proxy does not
// consult. It boots embedded Postgres, so it is skipped under -short.
func TestOnExtendProxyModePgStore_spec_4_9(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// The lenny roles (0001, 0002), the credential_leases table (0038), and
	// its §12.9 envelope-encryption reshape (0129).
	for _, name := range []string{
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0038_credential_leases.up.sql",
		"0129_credential_leases_envelope.up.sql",
	} {
		sql, readErr := migrations.FS.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if _, execErr := pool.Exec(ctx, string(sql)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}

	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	var durable credleasestore.LeaseStore
	durable, err = credleasepg.New(pool, provider)
	if err != nil {
		t.Fatalf("pgstore.New: %v", err)
	}

	issuedAt := time.Now().Add(-30 * time.Minute)
	expiresAt := time.Now().Add(5 * time.Minute)
	if err := durable.Put(extendProxyLease("cl-pg", "run_c", issuedAt, expiresAt)); err != nil {
		t.Fatalf("seed proxy lease into pgstore: %v", err)
	}

	wiring := newCredRenewalWiring(&fakeExtendAssigner{}, podsession.NewRegistry(), nil, durable, nil)
	newExpiresAt := expiresAt.Add(5 * time.Minute)
	if err := wiring.onExtend(credrenewal.Lease{
		LeaseID:   "cl-pg",
		SessionID: "run_c",
		ExpiresAt: expiresAt,
	}, newExpiresAt); err != nil {
		t.Fatalf("onExtend proxy pgstore: %v", err)
	}

	after, ok := durable.GetByID("cl-pg")
	if !ok {
		t.Fatal("proxy lease vanished from the durable store")
	}
	if !after.ExpiresAt.Equal(newExpiresAt) {
		t.Errorf("durable proxy-mode extension ExpiresAt = %v, want %v", after.ExpiresAt, newExpiresAt)
	}
	if !after.RenewBefore.Equal(expiresAt) {
		t.Errorf("durable proxy-mode extension RenewBefore = %v, want the pre-extension ExpiresAt %v", after.RenewBefore, expiresAt)
	}
}

// TestRenewMapsBreakerOpenToSentinel proves credRenewalWiring.Renew maps
// the breaker-open credassign.ErrTokenServiceUnavailable to
// credrenewal.ErrRenewInfraUnavailable at the package boundary (so the
// worker recognizes it with errors.Is and holds the lease), while
// preserving the underlying cause. A regression that returned the assign
// error raw would exhaust a still-valid lease into the Fallback Flow, the
// restart loop §4.9 forbids.
func TestRenewMapsBreakerOpenToSentinel_spec_4_9(t *testing.T) {
	wiring := newCredRenewalWiring(&fakeExtendAssigner{assignErr: credassign.ErrTokenServiceUnavailable}, podsession.NewRegistry(), nil, nil, nil)
	wiring.pools["cl-x"] = renewalProvider{pool: "claude-prod"}

	_, err := wiring.Renew(context.Background(), credrenewal.Lease{LeaseID: "cl-x", SessionID: "run_d"})
	if err == nil {
		t.Fatal("Renew under a breaker-open assign returned nil error")
	}
	if !errors.Is(err, credrenewal.ErrRenewInfraUnavailable) {
		t.Errorf("Renew error %v is not credrenewal.ErrRenewInfraUnavailable; the worker will not hold the lease", err)
	}
	if !errors.Is(err, credassign.ErrTokenServiceUnavailable) {
		t.Errorf("Renew error %v dropped the underlying credassign.ErrTokenServiceUnavailable cause", err)
	}
}

// TestStampsLeaseTTLOnTrackAndRenew proves both worker-tracking sites stamp
// Lease.LeaseTTL from the lease's own minted lifetime (ExpiresAt-IssuedAt),
// so the §4.9 cumulative-extension cap stays TTL-derived across a normal
// renewal instead of silently degrading to DefaultMaxExtensions. The worker
// unit test cannot observe this because its fake Renewer populates LeaseTTL
// itself.
func TestStampsLeaseTTLOnTrackAndRenew_spec_4_9(t *testing.T) {
	issuedAt := time.Now().Add(-40 * time.Minute)
	expiresAt := time.Now().Add(20 * time.Minute)
	wantTTL := expiresAt.Sub(issuedAt)

	// Track path: renewalLease stamps LeaseTTL from the assigned lease.
	tracked := renewalLease(credential.Lease{
		LeaseID:   "cl-track",
		SessionID: "run_e",
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	})
	if tracked.LeaseTTL != wantTTL {
		t.Errorf("track-path LeaseTTL = %v, want %v (ExpiresAt-IssuedAt)", tracked.LeaseTTL, wantTTL)
	}

	// Renew return: the replacement lease carries its own TTL so the reset
	// after a normal renewal grants the fresh lease's TTL-derived budget.
	nextIssued := time.Now().Add(-1 * time.Minute)
	nextExpires := time.Now().Add(59 * time.Minute)
	wantNextTTL := nextExpires.Sub(nextIssued)
	wiring := newCredRenewalWiring(&fakeExtendAssigner{lease: credential.Lease{
		LeaseID:      "cl-next",
		SessionID:    "run_e",
		CredentialID: "key-1",
		IssuedAt:     nextIssued,
		ExpiresAt:    nextExpires,
		RenewBefore:  nextExpires.Add(-5 * time.Minute),
	}}, podsession.NewRegistry(), nil, nil, nil)
	wiring.pools["cl-old"] = renewalProvider{pool: "claude-prod"}

	next, err := wiring.Renew(context.Background(), credrenewal.Lease{LeaseID: "cl-old", SessionID: "run_e"})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if next.LeaseTTL != wantNextTTL {
		t.Errorf("Renew-return LeaseTTL = %v, want %v (next.ExpiresAt-next.IssuedAt)", next.LeaseTTL, wantNextTTL)
	}
}

// TestOnExtensionCapReachedTerminatesSession proves the §4.9
// cumulative-extension-cap teardown drives the capped lease's session to
// the §8.8 expired state with the existing expired:lease FailureReason,
// which the §8.8 MCP adapter surfaces to clients as a failed task carrying
// the expired:lease error code. It does not re-enter the Fallback Flow. A
// regression that stamped a non-expired:* reason would surface a
// non-conformant client-facing task error code (§8.8 line 869).
func TestOnExtensionCapReachedTerminatesSession_spec_4_9_8_8(t *testing.T) {
	term := &recordingReasonTerminator{}
	wiring := newCredRenewalWiring(&fakeExtendAssigner{}, podsession.NewRegistry(), nil, nil, term)
	wiring.pools["cl-cap"] = renewalProvider{pool: "claude-prod", provider: "anthropic_direct"}

	wiring.onExtensionCapReached(credrenewal.Lease{LeaseID: "cl-cap", SessionID: "run_f"})

	term.mu.Lock()
	defer term.mu.Unlock()
	if len(term.sessions) != 1 || term.sessions[0] != "run_f" {
		t.Fatalf("cap teardown terminated sessions=%v, want exactly [run_f]", term.sessions)
	}
	if term.reasons[0] != string(sessionapi.FailureExpiredLease) {
		t.Errorf("cap teardown reason = %q, want %q (expired:lease)", term.reasons[0], sessionapi.FailureExpiredLease)
	}
	if term.reasons[0] != "expired:lease" {
		t.Errorf("cap teardown reason = %q, want the §8.8 expired:* prefixed reason expired:lease", term.reasons[0])
	}

	// The capped lease's pool binding is dropped so the wiring does not leak
	// it after teardown.
	wiring.mu.Lock()
	_, stillTracked := wiring.pools["cl-cap"]
	wiring.mu.Unlock()
	if stillTracked {
		t.Error("cap teardown left the capped lease's pool binding in place")
	}
}
