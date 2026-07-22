// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 credential-delivery boundary for §8.2 delegated-child materialization.
// The functional tier-4 test observes the taskHandle state and the assigned
// pool; this suite pins the delivery boundary the materialization must hold:
// a delegated child is minted and delivered exactly its own credential
// lease(s) into its own pod at delegate_task time (before any finalize),
// scoped to the child's tenant with no cross-tenant or parent leakage, and a
// post-pod-claim credential-assignment race delivers no lease into the
// half-materialized pod and fails closed.
//
// Unlike the sibling finalize-mint boundary (delegation_credential_deny_leakage_
// test.go), materialization runs synchronously inside delegate_task: the real
// *sessionserver.Server is wired as the mcptools ChildMaterializer, so the
// lease-counting Binder.Credentials advances during the delegate_task call
// itself rather than at a later /finalize. The suite drives one lease-counting
// credential assigner (a podsession Binder.Credentials whose AssignProto
// increments a mutex-guarded counter and records the session and tenant each
// lease is delivered to) across the real delegate_task -> delegation.Service ->
// sessionserver materialization path over an envtest warm pool and the real
// credential-pool minting path. The control child drives the counter above
// zero, so the fail-closed assertion is not vacuous: the assigner is live and
// would mint for a child whose assignment does not race.
package tier9_security_test

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
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
)

// The runtimes, warm pools, and sandboxes the materialization boundary spans.
// The control child runs on a runtime that supports anthropic_direct (its
// credential pool assigns), and the race child runs on a runtime that supports
// openai_direct (its credential pool is the assigner's failPool, so the
// post-pod-claim lease mint races and loses). Each child claims from its own
// warm pool with its own idle sandbox, so a claim or a rollback in one sub-test
// does not perturb the pod-claim state the other asserts against.
const (
	matCredParentRuntime  = "planner"
	matCredControlRuntime = "control-tool"
	matCredRaceRuntime    = "race-tool"

	matCredControlPool    = "control-pool"
	matCredRacePool       = "race-pool"
	matCredControlTmpl    = "control-tmpl"
	matCredRaceTmpl       = "race-tmpl"
	matCredControlSandbox = "sbx-mat-control"
	matCredRaceSandbox    = "sbx-mat-race"
)

// materializeLeaseRecord captures one AssignProto delivery: the credential pool
// the lease was minted from and the session and tenant it was delivered to. It
// backs the delivery-boundary assertions that a lease reaches the child's own
// pod, scoped to the child's tenant, with no parent or cross-tenant leakage.
type materializeLeaseRecord struct {
	pool      string
	sessionID string
	tenantID  string
}

// materializeLeaseCounter is a podsession Binder.Credentials that records every
// AssignProto under a mutex. When the resolved credential pool equals failPool
// it models the §8.3 line 470 post-pod-claim assignment race: the pre-claim
// resolution found the pool yet the lease mint loses the race, so it returns
// credential.ErrPoolExhausted and delivers no lease token into the claimed pod.
// Every other pool mints and delivers a lease, driving the counter so the
// fail-closed assertion is not vacuous.
type materializeLeaseCounter struct {
	mu       sync.Mutex
	records  []materializeLeaseRecord
	failPool string
}

func (a *materializeLeaseCounter) AssignProto(pool, sessionID, _, tenantID string) (*adapterv1.CredentialLease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if pool == a.failPool {
		return nil, credential.ErrPoolExhausted
	}
	a.records = append(a.records, materializeLeaseRecord{pool: pool, sessionID: sessionID, tenantID: tenantID})
	return &adapterv1.CredentialLease{
		LeaseId: "cl-" + pool, Provider: pool,
		Payload: []byte(`{"deliveryMode":"proxy","materializedConfig":{"proxyUrl":"https://p/v1","leaseToken":"lt"}}`),
	}, nil
}

func (a *materializeLeaseCounter) ReleaseSession(string) {}

func (a *materializeLeaseCounter) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.records)
}

// snapshot returns a copy of the recorded deliveries so a caller can inspect
// them without holding the lock.
func (a *materializeLeaseCounter) snapshot() []materializeLeaseRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]materializeLeaseRecord, len(a.records))
	copy(out, a.records)
	return out
}

// spec: 8.2 (steps 5-7 lease assignment/delivery), 8.3 (line 470 post-claim
// assignment race), 13 (credential-delivery/isolation boundary)
// diagnosis: a delegated child must be minted and delivered exactly its own
// credential lease(s) into its own pod during delegate_task (before any
// finalize), scoped to the child's tenant. A control child that mints no lease,
// or delivers a lease keyed to the parent session or a foreign tenant, means
// the materialization crossed the credential-delivery isolation boundary §13
// forbids. A post-pod-claim assignment race that delivers any lease into the
// half-materialized pod, or leaves the loser's warm pod claimed, means the
// materialization did not fail closed and leaked a credential the loser must
// never receive (spec/08 line 470).
func TestDelegatedChildMaterializationCredentialDelivery(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	ctx := context.Background()

	cluster := materializeCredCluster(t)

	adapterSrv := adapter.New("mat-cred-adapter")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.StagingDir = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = noopRuntime{}

	// The race child's credential pool is the assigner's failPool, so its
	// post-pod-claim lease mint races and loses; every other pool assigns.
	assigner := &materializeLeaseCounter{failPool: denyLeakagePoolOpen}
	blobs := blobstore.NewMemoryStore(nil)
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        deliveryGateNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      deliveryGateDialer(t, adapterSrv),
		Blobs:            blobs,
		Credentials:      assigner,
	}

	store := memstore.New()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "acme",
		CredentialPolicy: credential.CredentialPolicy{
			PreferredSource: credential.PreferredSourcePool,
			ProviderPools: map[string]credential.ProviderPool{
				denyLeakageAnthropic: {DefaultPool: denyLeakagePoolAnth},
				denyLeakageOpenAI:    {DefaultPool: denyLeakagePoolOpen},
			},
		},
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	runtimes := runtimestore.NewMemory()
	mustCreateRuntimeDeny(t, runtimes, runtimestore.Runtime{
		Name: matCredParentRuntime, Type: runtimestore.TypeAgent,
		SupportedProviders: []string{denyLeakageAnthropic},
	})
	mustCreateRuntimeDeny(t, runtimes, runtimestore.Runtime{
		Name: matCredControlRuntime, Type: runtimestore.TypeAgent,
		SupportedProviders: []string{denyLeakageAnthropic},
	})
	mustCreateRuntimeDeny(t, runtimes, runtimestore.Runtime{
		Name: matCredRaceRuntime, Type: runtimestore.TypeAgent,
		SupportedProviders: []string{denyLeakageOpenAI},
	})

	credPools := credentialpoolstore.NewMemory()
	for _, p := range []struct{ name, provider string }{
		{denyLeakagePoolAnth, denyLeakageAnthropic},
		{denyLeakagePoolOpen, denyLeakageOpenAI},
	} {
		if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: "acme", Name: p.name, Provider: p.provider, MaxConcurrentSessions: 10,
			Credentials: []credentialpoolstore.Credential{
				{ID: p.name + "-cred", SecretRef: "secret-" + p.name, Status: credentialpoolstore.CredentialActive},
			},
		}); err != nil {
			t.Fatalf("create pool %s: %v", p.name, err)
		}
	}

	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "unused" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          deliveryGateNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   blobs,
	})

	// The same *sessionserver.Server is wired as the mcptools ChildMaterializer,
	// so lenny/delegate_task materializes the admitted StateCreated child
	// synchronously (§8.2 steps 5-9) and the credential lease is minted and
	// delivered during the delegate_task call. The §8.3 delegation-time
	// availability pre-check is left unwired so materialization is reached for
	// every admitted child.
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:             store,
		Executor:          executor.NewEchoExecutor(),
		Runtimes:          runtimes,
		ChildMaterializer: srv,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc:   denyLeakageIDCounter("sess-mat-"),
			Runtimes: runtimes,
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})

	// Two running parents so each sub-test filters its own child rows without
	// the sibling sub-test's committed child perturbing the count.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, pid := range []string{"sess_parent_ctl", "sess_parent_race"} {
		if err := store.Create(ctx, sessionstore.Session{
			ID: pid, TenantID: "acme", UserID: "alice@acme.com",
			State: session.StateRunning, RuntimeRef: matCredParentRuntime,
			IsolationProfile: isolation.ProfileSandboxed, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create parent session %s: %v", pid, err)
		}
	}

	// Control child: an independent delegation on a runtime whose provider pool
	// assigns. The lease is minted and delivered during the delegate_task call,
	// so the AssignProto delta occurs before any finalize (none is issued here).
	// The delivery must be a single lease, keyed to the child's own session and
	// the child's tenant, with no delivery keyed to the parent session.
	t.Run("control_child_receives_lease_at_delegate_time", func(t *testing.T) {
		before := assigner.count()
		resp := denyLeakageDelegate(t, mcpSrv, "sess_parent_ctl", matCredControlRuntime, "independent")
		result, _ := resp["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatalf("control materialization must succeed: %+v", resp)
		}
		childID := denyLeakageChildID(t, result)

		child, err := store.Get(ctx, "acme", childID)
		if err != nil {
			t.Fatalf("get materialized child: %v", err)
		}
		if child.State != session.StateRunning {
			t.Errorf("child state = %q, want running (the child materialized synchronously)", child.State)
		}
		if child.PodAssignment == "" {
			t.Error("child has no PodAssignment; materialization claimed no warm pod")
		}

		// The lease was delivered during delegate_task, before any finalize.
		delivered := assigner.count() - before
		if delivered != 1 {
			t.Fatalf("control child delivered %d lease tokens at delegate time, want exactly 1 (the child draws its single provider pool at materialization, before any finalize)", delivered)
		}
		// The delivered lease is keyed to the child's own pod and tenant, with
		// no lease delivered to the parent session (no parent leakage) and none
		// to a foreign tenant (no cross-tenant leakage).
		var got materializeLeaseRecord
		for _, rec := range assigner.snapshot() {
			if rec.sessionID == "sess_parent_ctl" {
				t.Errorf("a lease was delivered to the parent session %q; the child's lease must reach the child pod only", rec.sessionID)
			}
			if rec.sessionID == childID {
				got = rec
			}
		}
		if got.sessionID != childID {
			t.Fatalf("no lease delivered to the child session %q; the delivery boundary was not reached", childID)
		}
		if got.tenantID != "acme" {
			t.Errorf("lease delivered under tenant %q, want acme (a delegated child's lease is scoped to the child's tenant)", got.tenantID)
		}
		if got.pool != denyLeakagePoolAnth {
			t.Errorf("lease minted from pool %q, want %q (the child draws its own runtime's provider pool)", got.pool, denyLeakagePoolAnth)
		}
	})

	// Assignment-race child: the post-pod-claim lease mint loses the race, so
	// the handler must fail closed — return CREDENTIAL_POOL_EXHAUSTED, deliver
	// no lease into the half-materialized pod, release the claimed warm pod, and
	// leave the child non-running.
	t.Run("assignment_race_delivers_no_lease_and_fails_closed", func(t *testing.T) {
		before := assigner.count()
		resp := denyLeakageDelegate(t, mcpSrv, "sess_parent_race", matCredRaceRuntime, "independent")
		result, _ := resp["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("an assignment-race materialization must be a tool error: %+v", resp)
		}
		env := denyLeakageErrorEnvelope(t, result)
		if env["code"] != "CREDENTIAL_POOL_EXHAUSTED" {
			t.Errorf("race code = %v, want CREDENTIAL_POOL_EXHAUSTED", env["code"])
		}

		// No lease token was delivered into the half-materialized pod: the mint
		// failed before delivery, so the net live-pod credential delivery is
		// zero and no credential leaked.
		if delivered := assigner.count() - before; delivered != 0 {
			t.Fatalf("assignment-race child delivered %d lease tokens, want 0 (a losing assignment must deliver no credential into the half-materialized pod)", delivered)
		}

		// The claimed warm pod was released (rollbackClaim): its per-pod
		// SandboxClaim is deleted, so no warm pod leaks past the losing
		// assignment.
		var claim lennyv1.SandboxClaim
		if err := cluster.Get(ctx, client.ObjectKey{Namespace: deliveryGateNS, Name: "claim-" + matCredRaceSandbox}, &claim); err == nil {
			t.Error("per-pod claim still present after the assignment race; the claimed warm pod leaked")
		} else if !apierrors.IsNotFound(err) {
			t.Fatalf("get race claim: %v", err)
		}

		// The child was committed by admission but the failed materialization
		// left it non-running with no persisted pod assignment.
		rows, err := store.List(ctx, "acme", sessionstore.ListFilter{})
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}
		found := false
		for _, r := range rows {
			if r.ParentSessionID != "sess_parent_race" {
				continue
			}
			found = true
			if r.State == session.StateRunning {
				t.Errorf("race child %s reached running on a failed assignment; the materialization did not fail closed", r.ID)
			}
			if r.PodAssignment != "" {
				t.Errorf("race child %s persisted PodAssignment %q; the claimed pod must be released, not persisted", r.ID, r.PodAssignment)
			}
		}
		if !found {
			t.Error("no child committed under the race parent; admission must still commit the child before materialization fails")
		}
	})
}

// materializeCredCluster returns an envtest-backed cluster seeded with the
// control and race warm pools, their templates (SPIFFE binding not disabled, so
// the §4.9 credential-delivery isolation gate does not reject the control
// child), and one idle Sandbox per pool for the delegated child to claim.
func materializeCredCluster(t *testing.T) client.Client {
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
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: deliveryGateNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	seedMaterializeCredPool(t, c, matCredControlPool, matCredControlTmpl, matCredControlRuntime)
	seedMaterializeCredSandbox(t, c, matCredControlSandbox, matCredControlPool, "10.244.5.21")
	seedMaterializeCredPool(t, c, matCredRacePool, matCredRaceTmpl, matCredRaceRuntime)
	seedMaterializeCredSandbox(t, c, matCredRaceSandbox, matCredRacePool, "10.244.5.22")
	return c
}

// seedMaterializeCredPool creates a SandboxWarmPool and its SandboxTemplate for
// the given runtime under the sandboxed §5.3 profile.
func seedMaterializeCredPool(t *testing.T, c client.Client, pool, tmpl, runtimeRef string) {
	t.Helper()
	ctx := context.Background()
	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: pool, Namespace: deliveryGateNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: tmpl, MinWarm: 1, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool %s: %v", pool, err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: tmpl, Namespace: deliveryGateNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: runtimeRef, IsolationProfile: string(isolation.ProfileSandboxed)},
	}); err != nil {
		t.Fatalf("create template %s: %v", tmpl, err)
	}
}

// seedMaterializeCredSandbox creates an idle Sandbox in the pool and stamps its
// status under the WarmPoolController field owner, matching the production
// Apply path the create/claim path reads.
func seedMaterializeCredSandbox(t *testing.T, c client.Client, name, pool, podIP string) {
	t.Helper()
	ctx := context.Background()
	if err := c.Create(ctx, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: deliveryGateNS,
			Labels: map[string]string{warmpool.LabelPool: pool},
		},
	}); err != nil {
		t.Fatalf("create sandbox %s: %v", name, err)
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(name)
	u.SetNamespace(deliveryGateNS)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": podIP}, "status")
	if err := c.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed idle sandbox status %s: %v", name, err)
	}
}
