// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// errCredLookupOutage is the transient store fault these cases inject.
var errCredLookupOutage = errors.New("injected credential-policy store outage")

// outageTenants fails Get while down is set.
type outageTenants struct {
	tenantstore.Store
	mu   sync.Mutex
	down bool
}

func (o *outageTenants) set(down bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.down = down
}

func (o *outageTenants) Get(ctx context.Context, id string) (tenantstore.Tenant, error) {
	o.mu.Lock()
	down := o.down
	o.mu.Unlock()
	if down {
		return tenantstore.Tenant{}, errCredLookupOutage
	}
	return o.Store.Get(ctx, id)
}

// outageRuntimes fails Get while down is set.
type outageRuntimes struct {
	runtimestore.Store
	mu   sync.Mutex
	down bool
}

func (o *outageRuntimes) set(down bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.down = down
}

func (o *outageRuntimes) Get(ctx context.Context, name string) (runtimestore.Runtime, error) {
	o.mu.Lock()
	down := o.down
	o.mu.Unlock()
	if down {
		return runtimestore.Runtime{}, errCredLookupOutage
	}
	return o.Store.Get(ctx, name)
}

// leaseLedger is a Binder.Credentials that mints a proxy-mode lease and
// records, per session, every lease minted and every release.
type leaseLedger struct {
	mu       sync.Mutex
	minted   map[string]int
	released map[string]int
}

func newLeaseLedger() *leaseLedger {
	return &leaseLedger{minted: map[string]int{}, released: map[string]int{}}
}

func (l *leaseLedger) AssignProto(pool, sessionID, _, _ string, _ int64) (*adapterv1.CredentialLease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.minted[sessionID]++
	return &adapterv1.CredentialLease{
		LeaseId:  "cl-" + pool + "-" + sessionID,
		Provider: pool,
		Payload:  []byte(`{"deliveryMode":"proxy","materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt"}}`),
	}, nil
}

func (l *leaseLedger) ReleaseSession(sessionID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.released[sessionID]++
	return nil
}

func (l *leaseLedger) ReleaseThrough(sessionID string, _ int64) error {
	return l.ReleaseSession(sessionID)
}
func (*leaseLedger) Release(string) error { return nil }

func (l *leaseLedger) mintedFor(sessionID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.minted[sessionID]
}

// credOutageFixture is an envtest warm pool with one idle sandbox, an
// in-process adapter, a tenant credentialPolicy that requires an
// anthropic_direct credential, and tenant and runtime stores that can be
// taken down.
type credOutageFixture struct {
	srv      *sessionserver.Server
	store    *memstore.Store
	registry *podsession.Registry
	cluster  client.Client
	tenants  *outageTenants
	runtimes *outageRuntimes
	ledger   *leaseLedger
}

func newCredOutageFixture(t *testing.T, id string) *credOutageFixture {
	t.Helper()
	ctx := context.Background()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))
	ledger := newLeaseLedger()
	binder.Credentials = ledger

	memTenants := tenantstore.NewMemory()
	if err := memTenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools:   map[string]credential.ProviderPool{"anthropic_direct": {DefaultPool: "claude-prod"}},
	}}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	memRuntimes := runtimestore.NewMemory()
	if err := memRuntimes.Create(ctx, runtimestore.Runtime{Name: "echo", SupportedProviders: []string{"anthropic_direct"}}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "claude-prod", Provider: "anthropic_direct", MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: "claude-prod-cred-a", SecretRef: "secret-claude-prod", Status: credentialpoolstore.CredentialActive},
		},
	}); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}
	f := &credOutageFixture{
		store:    memstore.New(),
		registry: podsession.NewRegistry(),
		cluster:  cluster,
		tenants:  &outageTenants{Store: memTenants},
		runtimes: &outageRuntimes{Store: memRuntimes},
		ledger:   ledger,
	}
	f.srv = sessionserver.New(f.store, sessionserver.Options{
		IDFunc:                  func() string { return id },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             f.registry,
		AgentNamespace:          podTestNS,
		Tenants:                 f.tenants,
		Runtimes:                f.runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
	})
	t.Cleanup(f.srv.Close)
	return f
}

// sandboxClaimed reports whether sbx-1 carries a per-pod claim.
func (f *credOutageFixture) sandboxClaimed(t *testing.T) bool {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := f.cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim)
	return err == nil
}

// spec: §4.9 (Pre-Claim Credential Availability Check), §7.1 (Atomicity of
// session creation), §15.1 (SESSION_CREATION_FAILED)
// diagnosis: a combined create-and-start whose runtime read fails
// transiently must fail closed before the claim: 503 SESSION_CREATION_FAILED
// with Retry-After and reason credential_lookup_unavailable, no session row,
// no claimed pod, and no credential lease. Once the store recovers the same
// request starts the session with its credential lease. A failure means a
// session was started without the credential its tenant policy requires, a
// pod or lease leaked on the refusal, or the refusal was not retryable.
func TestCreateAndStartFailsClosedOnCredentialLookupOutage_spec_4_9(t *testing.T) {
	t.Parallel()
	f := newCredOutageFixture(t, "sess-cred-outage")
	h := f.srv.Handler()
	body, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})

	f.runtimes.set(true)
	rr := postSessionStep(t, h, "/v1/sessions/start", body)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("create-and-start during the outage: status %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code      string         `json:"code"`
			Retryable bool           `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "SESSION_CREATION_FAILED" || !env.Error.Retryable || rr.Header().Get("Retry-After") == "" {
		t.Errorf("answer = %s retryable=%v Retry-After=%q, want retryable SESSION_CREATION_FAILED with Retry-After",
			env.Error.Code, env.Error.Retryable, rr.Header().Get("Retry-After"))
	}
	if env.Error.Details["reason"] != "credential_lookup_unavailable" {
		t.Errorf("details.reason = %v, want credential_lookup_unavailable", env.Error.Details["reason"])
	}
	if _, err := f.store.Get(context.Background(), "acme", "sess-cred-outage"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("the refused create persisted a row (err=%v)", err)
	}
	if f.sandboxClaimed(t) {
		t.Error("sbx-1 was claimed for a session refused before the claim")
	}
	if n := f.ledger.mintedFor("sess-cred-outage"); n != 0 {
		t.Errorf("credential leases minted = %d, want 0", n)
	}

	f.runtimes.set(false)
	rr = postSessionStep(t, h, "/v1/sessions/start", body)
	if rr.Code != http.StatusCreated && rr.Code != http.StatusOK {
		t.Fatalf("create-and-start after recovery: status %d; body=%s", rr.Code, rr.Body.String())
	}
	row, err := f.store.Get(context.Background(), "acme", "sess-cred-outage")
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if row.State != session.StateRunning || row.PodAssignment != "sbx-1" {
		t.Errorf("row = %s on %q, want running on sbx-1", row.State, row.PodAssignment)
	}
	if n := f.ledger.mintedFor("sess-cred-outage"); n != 1 {
		t.Errorf("credential leases minted after recovery = %d, want 1", n)
	}
}

// spec: §8.2 (delegated-child materialization), §4.9 (Pre-Claim Credential
// Availability Check), §8.3 (Credential availability pre-check at
// delegation time)
// diagnosis: a delegated child whose tenant credentialPolicy read fails
// transiently must stay in created with no pod and no lease, and the
// materialization must return the retryable lookup error. Once the store
// recovers, the same child materializes to running with its lease. A failure
// means a child ran without the credential its policy requires or leaked a
// warm pod on the refusal.
func TestMaterializeDelegatedChildFailsClosedOnCredentialLookupOutage_spec_8_2(t *testing.T) {
	t.Parallel()
	f := newCredOutageFixture(t, "unused")
	seedDelegatedChild(t, f.store, "child-outage", "")

	f.tenants.set(true)
	_, err := f.srv.MaterializeDelegatedChild(context.Background(), "acme", "child-outage")
	var lookup *sessionserver.CredentialLookupError
	if !errors.As(err, &lookup) || !errors.Is(err, errCredLookupOutage) {
		t.Fatalf("materialize during the outage = %v, want *CredentialLookupError wrapping the outage", err)
	}
	row, gerr := f.store.Get(context.Background(), "acme", "child-outage")
	if gerr != nil {
		t.Fatalf("read child: %v", gerr)
	}
	if row.State != session.StateCreated || row.PodAssignment != "" {
		t.Errorf("child = %s on %q, want created with no pod", row.State, row.PodAssignment)
	}
	if f.sandboxClaimed(t) || f.registry.Len() != 0 {
		t.Errorf("claimed=%v bindings=%d, want no claim and no binding", f.sandboxClaimed(t), f.registry.Len())
	}
	if n := f.ledger.mintedFor("child-outage"); n != 0 {
		t.Errorf("credential leases minted = %d, want 0", n)
	}

	f.tenants.set(false)
	st, err := f.srv.MaterializeDelegatedChild(context.Background(), "acme", "child-outage")
	if err != nil {
		t.Fatalf("materialize after recovery: %v", err)
	}
	if st != session.StateRunning {
		t.Errorf("state = %q, want running", st)
	}
	if n := f.ledger.mintedFor("child-outage"); n != 1 {
		t.Errorf("credential leases minted after recovery = %d, want 1", n)
	}
}

// spec: §7.3 (Resume driver item 1; Credential leases on the replacement
// pod; Retry accounting), §4.9 (Pre-Claim Credential Availability Check)
// diagnosis: the resume driver resolves a checkpointed session's
// credentials before it claims the replacement pod. A transient tenant
// policy read failure is a pre-claim failure: the row stays resume_pending at
// its generation and retry count, no pod is claimed, and no lease is minted.
// When the store recovers, the next attempt restores the session onto the
// pod with its lease. A failure means a session resumed without the
// credential its policy requires, or a failed attempt claimed a pod or
// advanced the retry count.
func TestResumeFailsClosedOnCredentialLookupOutage_spec_7_3(t *testing.T) {
	t.Parallel()
	f := newCredOutageFixture(t, "sess-resume-outage")
	seedAwaitingSession(t, f.store, sessionstore.Session{
		ID: "sess-resume-outage", UserID: "alice@acme.com", WorkspaceSnapshot: checkpointed(),
	})
	rr := postSessionStep(t, f.srv.Handler(), "/v1/sessions/sess-resume-outage/resume", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d; body=%s", rr.Code, rr.Body.String())
	}
	pending, err := f.store.Get(context.Background(), "acme", "sess-resume-outage")
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if pending.State != session.StateResumePending {
		t.Fatalf("state after POST /resume = %q, want resume_pending", pending.State)
	}

	f.tenants.set(true)
	for i := 0; i < 2; i++ {
		f.srv.DriveResumeAndWait(context.Background(), pending)
		got, _ := f.store.Get(context.Background(), "acme", "sess-resume-outage")
		if got.State != session.StateResumePending || got.CoordinationGeneration != pending.CoordinationGeneration ||
			got.RetryCount != pending.RetryCount {
			t.Fatalf("attempt %d: row = %s gen %d retry %d, want resume_pending gen %d retry %d",
				i, got.State, got.CoordinationGeneration, got.RetryCount, pending.CoordinationGeneration, pending.RetryCount)
		}
	}
	if f.sandboxClaimed(t) {
		t.Error("sbx-1 was claimed by an attempt that failed before its claim")
	}
	if n := f.ledger.mintedFor("sess-resume-outage"); n != 0 {
		t.Errorf("credential leases minted = %d, want 0", n)
	}

	f.tenants.set(false)
	f.srv.DriveResumeAndWait(context.Background(), pending)
	got, _ := f.store.Get(context.Background(), "acme", "sess-resume-outage")
	if got.State != session.StateRunning || got.PodAssignment != "sbx-1" {
		t.Errorf("after recovery row = %s on %q, want running on sbx-1", got.State, got.PodAssignment)
	}
	if n := f.ledger.mintedFor("sess-resume-outage"); n != 1 {
		t.Errorf("credential leases minted after recovery = %d, want 1", n)
	}
}
