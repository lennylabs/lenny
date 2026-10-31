// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 credential-delivery boundary for a §4.9 pre-claim credential
// resolution that cannot read its inputs. The resolution reads the tenant
// credentialPolicy and the session runtime's supportedProviders. When either
// read fails with a transient error, the gateway does not know which
// credential the session requires, so it must refuse the start rather than
// run the session with no credential. Before the fix the resolution treated
// such a read as "no policy" and the session ran with no credential lease.
//
// The suite drives the two external entry points a client or a parent agent
// reaches, over an envtest warm pool, an in-process adapter, and a
// lease-counting credential assigner: a combined POST /v1/sessions/start
// during a runtime-store outage, and lenny/delegate_task during a
// tenant-store outage. Each outage refusal must deliver no lease, claim no
// pod, and leave no running session; each recovery control must start the
// same request with exactly one lease, so the refusal assertions are not
// vacuous.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

const (
	lookupOutageParentRuntime = "outage-parent"
	lookupOutageAgentRuntime  = "outage-agent"
	lookupOutageToolRuntime   = "outage-tool"
	lookupOutageAgentPool     = "outage-agent-pool"
	lookupOutageToolPool      = "outage-tool-pool"
	lookupOutageAgentSandbox  = "sbx-outage-agent"
	lookupOutageToolSandbox   = "sbx-outage-tool"
	lookupOutageCredPool      = "outage-claude-prod"
)

var errLookupOutage = errors.New("injected store outage")

// lookupOutageTenants fails Get while down is set.
type lookupOutageTenants struct {
	tenantstore.Store
	mu   sync.Mutex
	down bool
}

func (o *lookupOutageTenants) set(down bool) { o.mu.Lock(); o.down = down; o.mu.Unlock() }

func (o *lookupOutageTenants) Get(ctx context.Context, id string) (tenantstore.Tenant, error) {
	o.mu.Lock()
	down := o.down
	o.mu.Unlock()
	if down {
		return tenantstore.Tenant{}, errLookupOutage
	}
	return o.Store.Get(ctx, id)
}

// lookupOutageRuntimes fails Get while down is set.
type lookupOutageRuntimes struct {
	runtimestore.Store
	mu   sync.Mutex
	down bool
}

func (o *lookupOutageRuntimes) set(down bool) { o.mu.Lock(); o.down = down; o.mu.Unlock() }

func (o *lookupOutageRuntimes) Get(ctx context.Context, name string) (runtimestore.Runtime, error) {
	o.mu.Lock()
	down := o.down
	o.mu.Unlock()
	if down {
		return runtimestore.Runtime{}, errLookupOutage
	}
	return o.Store.Get(ctx, name)
}

// lookupOutageLedger is a Binder.Credentials that records every lease it
// delivers, by session.
type lookupOutageLedger struct {
	mu        sync.Mutex
	delivered map[string]int
	total     int
}

func (l *lookupOutageLedger) AssignProto(pool, sessionID, _, _ string, _ int64) (*adapterv1.CredentialLease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.delivered[sessionID]++
	l.total++
	return &adapterv1.CredentialLease{
		LeaseId: "cl-" + pool + "-" + sessionID, Provider: pool,
		Payload: []byte(`{"deliveryMode":"proxy","materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt"}}`),
	}, nil
}
func (*lookupOutageLedger) ReleaseSession(string) error        { return nil }
func (*lookupOutageLedger) ReleaseThrough(string, int64) error { return nil }
func (*lookupOutageLedger) Release(string) error               { return nil }

func (l *lookupOutageLedger) count() int { l.mu.Lock(); defer l.mu.Unlock(); return l.total }
func (l *lookupOutageLedger) forSession(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.delivered[id]
}

// spec: §4.9 (Pre-Claim Credential Availability Check), §7.1 (Atomicity of
// session creation), §8.2 (delegated-child materialization), §13
// (credential delivery), §14.1 (WorkspacePlan Schema Versioning, Runtime
// workspace defaults rule 4), §15.1 (REST API, Error code catalog)
// diagnosis: a session whose tenant credentialPolicy or runtime cannot be
// read must not start: the top-level start answers the retryable
// SERVICE_UNAVAILABLE with Retry-After, because the runtime is first read
// when the request binds its workspace plan and §14.1 rule 4 fails a
// transient runtime-store error there with that code, and the delegated
// child fails its delegate_task
// call, each with no lease delivered, no pod claimed, and no running row.
// A refusal that delivered a lease or claimed a pod, or a session that ran
// during the outage, means the pre-claim resolution failed open and a
// session ran without the credential its tenant policy requires. A recovery
// control that delivers no lease means the assigner is not live and the
// refusal assertions prove nothing.
func TestCredentialLookupOutageFailsClosed(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	ctx := context.Background()
	cluster := lookupOutageCluster(t)

	adapterSrv := adapter.New("lookup-outage-adapter")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = noopRuntime{}
	ledger := &lookupOutageLedger{delivered: map[string]int{}}
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        deliveryGateNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      deliveryGateDialer(t, adapterSrv),
		Credentials:      ledger,
	}

	memTenants := tenantstore.NewMemory()
	if err := memTenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools:   map[string]credential.ProviderPool{"anthropic_direct": {DefaultPool: lookupOutageCredPool}},
	}}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	tenants := &lookupOutageTenants{Store: memTenants}
	memRuntimes := runtimestore.NewMemory()
	for _, name := range []string{lookupOutageParentRuntime, lookupOutageAgentRuntime, lookupOutageToolRuntime} {
		mustCreateRuntimeDeny(t, memRuntimes, runtimestore.Runtime{
			Name: name, Type: runtimestore.TypeAgent, SupportedProviders: []string{"anthropic_direct"},
		})
	}
	runtimes := &lookupOutageRuntimes{Store: memRuntimes}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: lookupOutageCredPool, Provider: "anthropic_direct", MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: lookupOutageCredPool + "-cred", SecretRef: "secret-" + lookupOutageCredPool, Status: credentialpoolstore.CredentialActive},
		},
	}); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  func() string { return "sess-outage-top" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          deliveryGateNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
	})
	t.Cleanup(srv.Close)

	t.Run("top-level start during a runtime-store outage", func(t *testing.T) {
		body, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: lookupOutageAgentRuntime})
		post := func() *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			rr := httptest.NewRecorder()
			ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice).ServeHTTP(rr, req)
			return rr
		}
		before := ledger.count()
		runtimes.set(true)
		rr := post()
		runtimes.set(false)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("start during the outage: status %d, want 503; body=%s", rr.Code, rr.Body.String())
		}
		var env struct {
			Error struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &env)
		// §14.1 Runtime workspace defaults rule 4: the start binds its plan,
		// the plan binding reads the runtime first, and a transient
		// runtime-store error there fails with SERVICE_UNAVAILABLE (§15.1).
		if env.Error.Code != "SERVICE_UNAVAILABLE" || !env.Error.Retryable {
			t.Errorf("answer = %s retryable=%v, want retryable SERVICE_UNAVAILABLE", env.Error.Code, env.Error.Retryable)
		}
		if rr.Header().Get("Retry-After") == "" {
			t.Errorf("the refused start carries no Retry-After header; §15.1 sets it on SERVICE_UNAVAILABLE")
		}
		if got := ledger.count() - before; got != 0 {
			t.Fatalf("the refused start delivered %d leases, want 0", got)
		}
		if _, err := store.Get(ctx, "acme", "sess-outage-top"); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("the refused start persisted a row (err=%v)", err)
		}
		assertLookupOutageUnclaimed(t, cluster, lookupOutageAgentSandbox)

		rr = post()
		if rr.Code != http.StatusCreated && rr.Code != http.StatusOK {
			t.Fatalf("start after recovery: status %d; body=%s", rr.Code, rr.Body.String())
		}
		row, err := store.Get(ctx, "acme", "sess-outage-top")
		if err != nil || row.State != session.StateRunning {
			t.Fatalf("row after recovery = %+v err=%v, want running", row.State, err)
		}
		if got := ledger.forSession("sess-outage-top"); got != 1 {
			t.Errorf("leases delivered to the recovered session = %d, want 1", got)
		}
	})

	t.Run("delegated child during a tenant-store outage", func(t *testing.T) {
		mcpSrv := mcp.NewServer()
		mcptools.Register(mcpSrv, mcptools.Deps{
			SessionService:    ownerprincipal.SessionAccess{},
			DevTenantFallback: true,
			Store:             store,
			Executor:          executor.NewEchoExecutor(),
			Runtimes:          memRuntimes,
			ChildMaterializer: srv,
			Delegation: delegation.NewService(store, delegation.Options{
				IDFunc:   denyLeakageIDCounter("sess-outage-child-"),
				Runtimes: memRuntimes,
			}),
			Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
			IDFunc:   func() string { return "sess_mcp" },
			TenantID: "acme",
		})
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := store.Create(ctx, sessionstore.Session{
			ID: "sess_parent_outage", TenantID: "acme", UserID: "alice@acme.com",
			State: session.StateRunning, RuntimeRef: lookupOutageParentRuntime,
			IsolationProfile: isolation.ProfileSandboxed, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create parent: %v", err)
		}

		before := ledger.count()
		tenants.set(true)
		resp := denyLeakageDelegate(t, mcpSrv, "sess_parent_outage", lookupOutageToolRuntime, "independent")
		tenants.set(false)
		result, _ := resp["result"].(map[string]any)
		if result == nil || result["isError"] != true {
			if resp["error"] == nil {
				t.Fatalf("delegate_task during the outage must fail: %+v", resp)
			}
		}
		if got := ledger.count() - before; got != 0 {
			t.Fatalf("the refused delegation delivered %d leases, want 0", got)
		}
		rows, err := store.List(ctx, "acme", sessionstore.ListFilter{})
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}
		for _, r := range rows {
			if r.ParentSessionID == "sess_parent_outage" && (r.State == session.StateRunning || r.PodAssignment != "") {
				t.Errorf("child %s is %s on %q during the outage; it ran without its credential", r.ID, r.State, r.PodAssignment)
			}
		}
		assertLookupOutageUnclaimed(t, cluster, lookupOutageToolSandbox)

		resp = denyLeakageDelegate(t, mcpSrv, "sess_parent_outage", lookupOutageToolRuntime, "independent")
		result, _ = resp["result"].(map[string]any)
		if result == nil || result["isError"] == true {
			t.Fatalf("delegate_task after recovery must succeed: %+v", resp)
		}
		childID := denyLeakageChildID(t, result)
		if got := ledger.forSession(childID); got != 1 {
			t.Errorf("leases delivered to the recovered child = %d, want 1", got)
		}
	})
}

func assertLookupOutageUnclaimed(t *testing.T, c client.Client, sandbox string) {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := c.Get(context.Background(), client.ObjectKey{Namespace: deliveryGateNS, Name: "claim-" + sandbox}, &claim)
	switch {
	case err == nil:
		t.Errorf("%s was claimed by a request refused before its claim", sandbox)
	case !apierrors.IsNotFound(err):
		t.Fatalf("get claim of %s: %v", sandbox, err)
	}
}

// lookupOutageCluster returns an envtest cluster with one warm pool and one
// idle sandbox for the top-level runtime and one for the delegated-child
// runtime.
func lookupOutageCluster(t *testing.T) client.Client {
	t.Helper()
	env := envtest.Start(t)
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme lenny: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	if err := c.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: deliveryGateNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	seedMaterializeCredPool(t, c, lookupOutageAgentPool, lookupOutageAgentPool+"-tmpl", lookupOutageAgentRuntime)
	seedMaterializeCredSandbox(t, c, lookupOutageAgentSandbox, lookupOutageAgentPool, "10.244.6.21")
	seedMaterializeCredPool(t, c, lookupOutageToolPool, lookupOutageToolPool+"-tmpl", lookupOutageToolRuntime)
	seedMaterializeCredSandbox(t, c, lookupOutageToolSandbox, lookupOutageToolPool, "10.244.6.22")
	return c
}
