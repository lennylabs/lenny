// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.9 Token Service unavailability guard end
// to end across both credential delivery modes. When the Token Service
// circuit breaker is open and a still-valid lease reaches its renewBefore
// deadline, the proactive renewal worker MUST NOT drop the lease into the
// Fallback Flow (checkpoint-and-restart, which re-mints against the down
// Token Service and loops). Instead it extends the enforced lease deadline
// through the delivery mode's enforcement point and reschedules, calling the
// Token Service on no path.
//
// This suite wires the production components the gateway wires: the real
// adapter.Server over a real gRPC connection (the direct-mode enforcement
// point), the real credleasestore.Store the LLM Proxy reads and the real
// llmproxy.Handler that reads it (the proxy-mode enforcement point), the real
// credrenewal.Worker driving the sweep, and a real credassign.Client whose
// §4.3 per-subsystem breaker is forced open so its renewal attempt short-
// circuits to ErrTokenServiceUnavailable without touching the Token Service
// stub. The OnExtend dispatcher mirrors cmd/lenny-gateway's credRenewalWiring.
//
// spec: §4.9 line 1470 (Token Service unavailability guard).
package tier4_integration_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

const (
	guardProvider = "anthropic_direct"
	// guardDirectPayload is a direct-mode lease payload: a direct-mode
	// lease with a positive expiry arms the adapter expiry timer the guard
	// re-arms. spec: §4.9 line 1149.
	guardDirectPayload = `{"deliveryMode":"direct","materializedConfig":{"apiKey":"sk-ant-x"}}`
)

// guardTokenServiceStub counts AssignCredentials invocations so a test can
// prove the guard's extension path made no Token Service round-trip. It
// answers every RPC Unimplemented; with the breaker forced open it is never
// reached at all.
type guardTokenServiceStub struct {
	assignCalls atomic.Int32
}

func (s *guardTokenServiceStub) AssignCredentials(context.Context, *tokensv1.AssignCredentialsRequest, ...grpc.CallOption) (*tokensv1.AssignCredentialsResponse, error) {
	s.assignCalls.Add(1)
	return nil, errors.New("token service unavailable")
}

func (s *guardTokenServiceStub) RotateCredentials(context.Context, *tokensv1.RotateCredentialsRequest, ...grpc.CallOption) (*tokensv1.RotateCredentialsResponse, error) {
	return nil, errors.New("unimplemented")
}

func (s *guardTokenServiceStub) RevokeCredentials(context.Context, *tokensv1.RevokeCredentialsRequest, ...grpc.CallOption) (*tokensv1.RevokeCredentialsResponse, error) {
	return nil, errors.New("unimplemented")
}

func (s *guardTokenServiceStub) ProbeSecretAccess(context.Context, *tokensv1.ProbeSecretAccessRequest, ...grpc.CallOption) (*tokensv1.ProbeSecretAccessResponse, error) {
	return nil, errors.New("unimplemented")
}

// forcedOpenRenewer is the credrenewal.Renewer the guard exercises: it drives
// a real credassign.Client whose §4.3 per-subsystem breaker is forced open, so
// Assign short-circuits to ErrTokenServiceUnavailable and the client never
// dials the Token Service. It maps the breaker-open sentinel to
// credrenewal.ErrRenewInfraUnavailable exactly as credRenewalWiring.Renew
// does, so the worker recognizes it and holds the lease.
type forcedOpenRenewer struct {
	client *credassign.Client
	pool   string
}

func (r *forcedOpenRenewer) Renew(_ context.Context, lease credrenewal.Lease) (credrenewal.Lease, error) {
	_, err := r.client.Assign(r.pool, lease.SessionID, "", "acme")
	if errors.Is(err, credassign.ErrTokenServiceUnavailable) {
		return credrenewal.Lease{}, fmt.Errorf("%w: %w", credrenewal.ErrRenewInfraUnavailable, err)
	}
	if err != nil {
		return credrenewal.Lease{}, err
	}
	return credrenewal.Lease{}, errors.New("unexpected renewal success while the breaker is forced open")
}

// newForcedOpenRenewer builds a credassign.Client with the breaker tripped
// open (cooldown far in the future so it stays open) and a recording stub, and
// returns the renewer plus the stub so a test can assert zero Token Service
// calls.
func newForcedOpenRenewer(t *testing.T) (*forcedOpenRenewer, *guardTokenServiceStub) {
	t.Helper()
	stub := &guardTokenServiceStub{}
	breaker := &subsystem.Breaker{FailureThreshold: 1, Cooldown: time.Hour}
	breaker.RecordFailure() // trips closed -> open
	if breaker.State() != subsystem.StateOpen {
		t.Fatalf("breaker state = %v, want open", breaker.State())
	}
	client := credassign.NewClient(credassign.ClientOptions{
		Stub:      stub,
		Leases:    credleasestore.New(),
		Creds:     credcache.New(),
		TenantID:  "acme",
		Subsystem: &subsystem.Subsystem{Breaker: breaker},
	})
	return &forcedOpenRenewer{client: client, pool: "claude-prod"}, stub
}

// guardOnExtend mirrors cmd/lenny-gateway's credRenewalWiring.onExtend: it
// resolves the lease's delivery mode from the store, and either re-arms the
// adapter expiry timer over ExtendCredentialLease (direct) or advances the
// stored record's ExpiresAt/RenewBefore the LLM Proxy reads (proxy).
func guardOnExtend(leases *credleasestore.Store, adapterCli *adapterclient.Client) func(credrenewal.Lease, time.Time) error {
	return func(lease credrenewal.Lease, newExpiresAt time.Time) error {
		rec, ok := leases.GetByID(lease.LeaseID)
		if !ok {
			return fmt.Errorf("guard extend: lease %s not in store", lease.LeaseID)
		}
		switch rec.DeliveryMode {
		case credential.DeliveryProxy:
			rec.ExpiresAt = newExpiresAt
			rec.RenewBefore = lease.ExpiresAt
			return leases.Put(rec)
		default:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return adapterCli.ExtendCredentialLease(ctx, lease.SessionID, guardProvider, lease.LeaseID, newExpiresAt)
		}
	}
}

// serveGuardAdapter serves a real adapter.Server over an in-memory gRPC
// connection and returns a gateway adapter client wired to it plus the
// credentials directory the direct-mode file is written under.
func serveGuardAdapter(t *testing.T) (*adapterclient.Client, string) {
	t.Helper()
	base := t.TempDir()
	credsDir := filepath.Join(base, "run", "lenny")
	if err := os.MkdirAll(credsDir, 0o755); err != nil {
		t.Fatalf("make credentials dir: %v", err)
	}
	s := adapter.New("guard-integration")
	s.CredentialsDir = credsDir
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial guard adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl, credsDir
}

// credentialFileHasProvider reports whether the adapter credential file under
// dir still carries an entry for provider. A missing file (the entry was
// deleted) yields false.
func credentialFileHasProvider(t *testing.T, dir, provider string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	return strings.Contains(string(data), provider)
}

// guardDirectStoreRecord is a valid direct-mode lease record for the lease
// store the OnExtend dispatcher reads to resolve the delivery mode.
func guardDirectStoreRecord(leaseID, sessionID string, issuedAt, expiresAt time.Time) credential.Lease {
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

// TestGuardDirectModeReArmsAdapterTimerNoTokenServiceCall proves the guard's
// direct-mode path re-arms the adapter expiry timer past the original
// expiresAt (the credential file survives) and issues no Token Service call. A
// direct lease is assigned with a short real expiry, so its real adapter timer
// would fire and delete the credential file within the test window; a single
// breaker-open worker sweep extends the enforced deadline far into the future,
// so the file survives past the original deadline.
//
// spec: §4.9 (line 1470, direct-mode ExtendCredentialLease enforcement point)
//
// diagnosis: a transient Token Service outage terminated a still-valid direct-
// mode session's credential at its original deadline instead of extending it,
// or the extension path made a Token Service call. The worker did not
// recognize the breaker-open sentinel, or OnExtend did not reach the adapter.
func TestGuardDirectModeReArmsAdapterTimerNoTokenServiceCall_spec_4_9(t *testing.T) {
	adapterCli, credsDir := serveGuardAdapter(t)
	ctx := context.Background()

	start := time.Now()
	originalExpiry := start.Add(1500 * time.Millisecond)
	if err := adapterCli.AssignCredentials(ctx, "run-direct", map[string]*adapterv1.CredentialLease{
		guardProvider: {
			LeaseId:         "cl-direct",
			Provider:        guardProvider,
			Payload:         []byte(guardDirectPayload),
			ExpiresAtUnixMs: originalExpiry.UnixMilli(),
		},
	}); err != nil {
		t.Fatalf("assign direct lease to adapter: %v", err)
	}
	if !credentialFileHasProvider(t, credsDir, guardProvider) {
		t.Fatal("credential file missing provider entry right after assignment")
	}

	leases := credleasestore.New()
	if err := leases.Put(guardDirectStoreRecord("cl-direct", "run-direct", start.Add(-30*time.Minute), originalExpiry)); err != nil {
		t.Fatalf("seed direct store record: %v", err)
	}

	renewer, stub := newForcedOpenRenewer(t)
	var exhausted, capped int32
	w := credrenewal.New(renewer, credrenewal.Options{
		OnExtend:              guardOnExtend(leases, adapterCli),
		OnExhausted:           func(credrenewal.Lease) { atomic.AddInt32(&exhausted, 1) },
		OnExtensionCapReached: func(credrenewal.Lease) { atomic.AddInt32(&capped, 1) },
	})
	// buffer = ExpiresAt - RenewBefore is large, so the re-armed adapter timer
	// fires far past the test window; the file must survive the original 1.5s
	// deadline.
	w.Track(credrenewal.Lease{
		LeaseID:     "cl-direct",
		SessionID:   "run-direct",
		ExpiresAt:   originalExpiry,
		RenewBefore: start.Add(-2 * time.Second),
		LeaseTTL:    time.Hour,
	})

	if renewed := w.Tick(ctx, start); renewed != 0 {
		t.Fatalf("worker reported %d renewals under a forced-open breaker, want 0", renewed)
	}
	if atomic.LoadInt32(&exhausted) != 0 {
		t.Fatal("the guard exhausted a still-valid lease into the Fallback Flow (the restart loop §4.9 forbids)")
	}
	if atomic.LoadInt32(&capped) != 0 {
		t.Fatal("the guard reached the cumulative-extension cap on the first sweep")
	}
	if w.Tracked() != 1 {
		t.Fatalf("worker tracks %d leases after one breaker-open sweep, want 1 (held, not dropped)", w.Tracked())
	}

	// Wait past the original 1.5s deadline. The extension re-armed the adapter
	// timer far into the future, so the credential file must still carry the
	// provider entry.
	deadline := originalExpiry.Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !credentialFileHasProvider(t, credsDir, guardProvider) {
		t.Fatal("credential file lost the provider entry past the original expiry: the adapter timer was not extended")
	}

	if n := stub.assignCalls.Load(); n != 0 {
		t.Fatalf("the guard's direct-mode extension made %d Token Service AssignCredentials calls, want 0", n)
	}
}

// noopUsage satisfies the LLM Proxy's usage sink; it never signals budget
// exhaustion so a request that passes the expiry check forwards upstream.
type noopUsage struct{}

func (noopUsage) RecordUsage(context.Context, credential.Lease, llmproxy.Usage) (bool, llmproxy.Outcome) {
	return false, llmproxy.OutcomeGranted
}

// TestGuardProxyModeAdvancesLeaseStoreNoTokenServiceCall proves the guard's
// proxy-mode path advances the credleasestore record the LLM Proxy reads, so
// the proxy keeps honoring requests past the original expiresAt, and issues no
// Token Service call. A proxy lease is minted through the production
// assignment service; the handler's clock is fixed just past the original
// expiry, so an un-extended request is rejected LEASE_EXPIRED while an extended
// one forwards upstream.
//
// spec: §4.9 (line 1470, proxy-mode lease-store enforcement point)
//
// diagnosis: a still-valid proxy session was rejected LEASE_EXPIRED under a
// transient Token Service outage instead of being extended, or the extension
// made a Token Service call. OnExtend advanced a store the proxy does not read,
// or the breaker-open sentinel was not recognized.
func TestGuardProxyModeAdvancesLeaseStoreNoTokenServiceCall_spec_4_9(t *testing.T) {
	ctx := context.Background()
	upstream := llmprovider.New(t)

	leases := credleasestore.New()
	creds := credcache.New()
	assign := credassign.New(leases, creds)
	assign.RegisterPool(credassign.Pool{
		Name:               "claude-prod",
		Provider:           credential.ProviderAnthropicDirect,
		DeliveryMode:       credential.DeliveryProxy,
		Strategy:           credential.StrategyLeastLoaded,
		ProxyURL:           "https://lenny-llm-proxy.internal/llm-proxy",
		ProxyDialect:       string(credential.ProxyDialectAnthropic),
		LeaseTTLSeconds:    300,
		RenewBeforeSeconds: 60,
		Credentials:        []credassign.PoolCredential{{ID: "cred-1", APIKey: "sk-upstream-secret", Healthy: true}},
	})
	lease, err := assign.Assign("claude-prod", "run-proxy", "", "acme")
	if err != nil {
		t.Fatalf("mint proxy lease: %v", err)
	}
	if lease.Proxy == nil || lease.Proxy.LeaseToken == "" {
		t.Fatalf("minted lease carries no proxy token: %+v", lease)
	}

	// The handler's clock is fixed just past the original expiry so the un-
	// extended lease reads as expired and the extended one reads as valid.
	checkTime := lease.ExpiresAt.Add(30 * time.Second)
	handler := &llmproxy.Handler{
		Leases:      leases,
		Translators: llmproxy.NewTranslatorRegistry(&llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL(), DefaultAnthropicVersion: "2023-06-01"}),
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: creds,
		Usage:       noopUsage{},
		Now:         func() time.Time { return checkTime },
	}

	// Before the extension: a request at checkTime is past the original
	// expiry, so the proxy rejects it LEASE_EXPIRED. This proves the harness
	// enforces expiry, so the post-extension success is meaningful.
	if status := proxyRequestStatus(t, handler, lease.Proxy.LeaseToken); status != http.StatusForbidden {
		t.Fatalf("un-extended proxy request past expiry returned %d, want 403 LEASE_EXPIRED", status)
	}

	renewer, stub := newForcedOpenRenewer(t)
	var exhausted int32
	w := credrenewal.New(renewer, credrenewal.Options{
		OnExtend:              guardOnExtend(leases, nil), // proxy mode never touches the adapter client
		OnExhausted:           func(credrenewal.Lease) { atomic.AddInt32(&exhausted, 1) },
		OnExtensionCapReached: func(credrenewal.Lease) {},
	})
	w.Track(credrenewal.Lease{
		LeaseID:     lease.LeaseID,
		SessionID:   "run-proxy",
		ExpiresAt:   lease.ExpiresAt,
		RenewBefore: lease.RenewBefore,
		LeaseTTL:    300 * time.Second,
	})

	if renewed := w.Tick(ctx, lease.RenewBefore); renewed != 0 {
		t.Fatalf("worker reported %d renewals under a forced-open breaker, want 0", renewed)
	}
	if atomic.LoadInt32(&exhausted) != 0 {
		t.Fatal("the guard exhausted a still-valid proxy lease into the Fallback Flow")
	}

	// After the extension: the same request at checkTime now falls before the
	// advanced deadline, so the proxy forwards it upstream (200).
	if status := proxyRequestStatus(t, handler, lease.Proxy.LeaseToken); status != http.StatusOK {
		t.Fatalf("extended proxy request past the original expiry returned %d, want 200; the guard did not advance the store the proxy reads", status)
	}

	if n := stub.assignCalls.Load(); n != 0 {
		t.Fatalf("the guard's proxy-mode extension made %d Token Service AssignCredentials calls, want 0", n)
	}
}

// proxyRequestStatus issues one Anthropic Messages request through the handler
// with the lease token as the x-api-key and returns the HTTP status.
func proxyRequestStatus(t *testing.T, handler *llmproxy.Handler, token string) int {
	t.Helper()
	body := `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"ping"}]}`
	req, err := http.NewRequest(http.MethodPost, "http://proxy/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build proxy request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}
