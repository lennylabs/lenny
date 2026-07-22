// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// seedDelegatedChild commits a StateCreated delegated-child row (a
// ParentSessionID set, no PodAssignment) the way the delegation service does
// before the §8.2 materialization step runs, so MaterializeDelegatedChild sees
// exactly the row it must claim-and-start.
func seedDelegatedChild(t *testing.T, store sessionstore.Store, id, plan string) {
	t.Helper()
	row := sessionstore.Session{
		ID:               id,
		TenantID:         "acme",
		UserID:           "alice@acme.com",
		RuntimeRef:       "echo",
		IsolationProfile: isolation.ProfileSandboxed,
		State:            session.StateCreated,
		ParentSessionID:  "parent-1",
	}
	if plan != "" {
		row.WorkspacePlan = json.RawMessage(plan)
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed delegated child %s: %v", id, err)
	}
}

// materializeCluster builds the envtest warm-pool cluster, the bufconn adapter
// dialer, and the adapter's workspace root the §8.2 materialization bind path
// drives an idle Sandbox through. It reuses the start_pod_test warm-pool
// fixtures (one idle "sbx-1" in "echo-pool").
func materializeCluster(t *testing.T) (client.Client, func(string) (*adapterclient.Client, error), string) {
	t.Helper()
	wsRoot := t.TempDir()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = wsRoot
	adapterSrv.Runtime = &podBindRuntime{}
	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	return cluster, podBindAdapterDialer(t, adapterSrv), wsRoot
}

// materializeCredStores wires a single-provider (anthropic_direct) tenant
// policy, runtime, and credential pool so resolveCredentialPools resolves a
// non-empty provider→pool map and the bind path reaches AssignProto. credStatus
// selects whether the pool's only credential is assignable.
func materializeCredStores(t *testing.T, credStatus credentialpoolstore.CredentialStatus) (tenantstore.Store, runtimestore.Store, credentialpoolstore.Store) {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "echo", SupportedProviders: []string{"anthropic_direct"}}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID:              "acme",
		Name:                  "claude-prod",
		Provider:              "anthropic_direct",
		MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: "claude-prod-cred-a", SecretRef: "secret-claude-prod", Status: credStatus},
		},
	}); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}
	return tenants, runtimes, credPools
}

// materializeFakeScheme builds the scheme the fake kube client needs to hold
// the §5 warm-pool CRDs claimAtCreate resolves a pool from, for the boundary
// cases that never reach a real claim.
func materializeFakeScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

// failingAssigner fails every AssignProto, simulating the §8.3 line 470
// post-pod-claim credential-assignment race: the pre-claim check passed (the
// pool resolves) yet the lease mint fails in the assignment window.
type failingAssigner struct{}

func (failingAssigner) AssignProto(_, _, _, _ string) (*adapterv1.CredentialLease, error) {
	return nil, credential.ErrPoolExhausted
}
func (failingAssigner) ReleaseSession(string) {}

// recordingAssigner assigns a real lease and records every ReleaseSession so a
// test can assert the §7.1 step-23 lease revoke ran during a post-launch
// rollback.
type recordingAssigner struct{ released []string }

func (a *recordingAssigner) AssignProto(_, _, _, _ string) (*adapterv1.CredentialLease, error) {
	return &adapterv1.CredentialLease{}, nil
}

func (a *recordingAssigner) ReleaseSession(sessionID string) {
	a.released = append(a.released, sessionID)
}

// updateFaultStore fails store.Update while fail is set, leaving Get/Create
// intact, so a test can inject the terminal transition-persist failure
// MaterializeDelegatedChild rolls back through rollbackBinding.
type updateFaultStore struct {
	sessionstore.Store
	fail bool
}

func (s *updateFaultStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if s.fail {
		return sessionstore.Session{}, errors.New("injected transition-persist fault")
	}
	return s.Store.Update(ctx, tenantID, id, mut)
}

// spec: 8.2 (steps 5-7), 8.3 (line 470 post-claim assignment race).
// diagnosis: MaterializeDelegatedChild must transition the existing
// StateCreated delegated-child row to running via store.Update (no second
// store.Create) and publish the startOnPod BindResult into the shared
// podRegistry so PodExecutor.streamFor resolves the child and Executor.Send no
// longer rejects it as unbound. A failure here means a delegated child never
// claimed a pod, never launched, or launched but stayed unregistered and
// unreachable, so §8.2 steps 5-9 did not execute.
func TestMaterializeDelegatedChildTransitionsToRunning_spec_8_2(t *testing.T) {
	store := memstore.New()
	seedDelegatedChild(t, store, "child-1", `{
		"schemaVersion": 1,
		"sources": [{"type":"inlineFile","path":"CHILD.md","content":"# delegated","mode":"0644"}]
	}`)
	cluster, dial, wsRoot := materializeCluster(t)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, dial)
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "unused" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	st, err := srv.MaterializeDelegatedChild(context.Background(), "acme", "child-1")
	if err != nil {
		t.Fatalf("MaterializeDelegatedChild: %v", err)
	}
	if st != session.StateRunning {
		t.Errorf("returned state = %q, want running", st)
	}

	row, err := store.Get(context.Background(), "acme", "child-1")
	if err != nil {
		t.Fatalf("get child row: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("persisted child state = %q, want running", row.State)
	}
	if row.PodAssignment != "sbx-1" {
		t.Errorf("child PodAssignment = %q, want sbx-1 (a warm pod was claimed)", row.PodAssignment)
	}
	// registerBinding published the bind so the executor can resolve the child.
	binding, ok := registry.Get("child-1")
	if !ok {
		t.Fatal("registry holds no binding for the materialized child; Executor.Send would reject it as unbound")
	}
	if binding.SandboxName != "sbx-1" {
		t.Errorf("registry binding SandboxName = %q, want sbx-1", binding.SandboxName)
	}
	// The stamped WorkspacePlan streamed through the §6.3 binder onto the pod.
	got, err := os.ReadFile(filepath.Join(wsRoot, "CHILD.md"))
	if err != nil {
		t.Fatalf("child workspace plan was not materialized: %v", err)
	}
	if string(got) != "# delegated" {
		t.Errorf("materialized file = %q, want %q", got, "# delegated")
	}
}

// spec: 8.2 (steps 5-9 delegated-child materialization).
// diagnosis: materialization claims a pod and assigns a lease exactly once
// against a freshly committed StateCreated child. Calling it on a row that is
// not StateCreated (a double-materialization) must fail closed with the typed
// guard error and claim no pod, so a caller bug cannot silently re-claim a pod
// for an already-running child.
func TestMaterializeDelegatedChildNonCreatedGuard_spec_8_2(t *testing.T) {
	store := memstore.New()
	// Seed the child already in running: a second materialization must be refused.
	row := sessionstore.Session{
		ID: "child-run", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
		IsolationProfile: isolation.ProfileSandboxed, State: session.StateRunning, ParentSessionID: "parent-1",
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed running child: %v", err)
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-pool", Namespace: podTestNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "echo-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: podTestNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "echo", IsolationProfile: string(isolation.ProfileSandboxed)},
	}
	c := fake.NewClientBuilder().WithScheme(materializeFakeScheme(t)).WithObjects(pool, tmpl).Build()
	registry := podsession.NewRegistry()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "unused" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               &podsession.Binder{Client: c, Namespace: podTestNS},
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	_, err := srv.MaterializeDelegatedChild(context.Background(), "acme", "child-run")
	var guard *sessionserver.DelegatedChildNotCreatedError
	if !errors.As(err, &guard) {
		t.Fatalf("MaterializeDelegatedChild = %v, want *DelegatedChildNotCreatedError", err)
	}
	if guard.State != session.StateRunning {
		t.Errorf("guard error state = %q, want running", guard.State)
	}
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 (the guard claimed no pod)", registry.Len())
	}
}

// spec: 8.2 (steps 5-7).
// diagnosis: the §4.9 pre-claim credential availability check gates the pod
// claim. A delegated child whose provider intersection is exhausted must be
// rejected with ErrNoCredentialAvailable BEFORE any pod is claimed, so a child
// that cannot be supplied a credential never occupies a warm pod. A failure
// here means the claim ran ahead of (or without) the pre-check.
func TestMaterializeDelegatedChildPreClaimExhaustion_spec_8_2(t *testing.T) {
	store := memstore.New()
	seedDelegatedChild(t, store, "child-exhausted", "")
	tenants, runtimes, credPools := materializeCredStores(t, credentialpoolstore.CredentialRevoked)
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-pool", Namespace: podTestNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "echo-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: podTestNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "echo", IsolationProfile: string(isolation.ProfileSandboxed)},
	}
	c := fake.NewClientBuilder().WithScheme(materializeFakeScheme(t)).WithObjects(pool, tmpl).Build()
	registry := podsession.NewRegistry()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "unused" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               &podsession.Binder{Client: c, Namespace: podTestNS},
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
	})

	_, err := srv.MaterializeDelegatedChild(context.Background(), "acme", "child-exhausted")
	if !errors.Is(err, credrouter.ErrNoCredentialAvailable) {
		t.Fatalf("MaterializeDelegatedChild = %v, want ErrNoCredentialAvailable (pre-check gates the claim)", err)
	}
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 (no pod claimed on pre-claim exhaustion)", registry.Len())
	}
	row, err := store.Get(context.Background(), "acme", "child-exhausted")
	if err != nil {
		t.Fatalf("get child row: %v", err)
	}
	if row.State != session.StateCreated {
		t.Errorf("child state = %q, want created (unchanged after pre-claim rejection)", row.State)
	}
}

// spec: 8.3 (line 470 post-claim assignment race).
// diagnosis: a credential-assignment failure after the pod is claimed (the
// pre-check passed but the lease mint raced and failed) must fail closed: the
// engine returns the CredentialAssignmentError sentinel, releases the claimed
// pod via rollbackClaim (no warm pod leaked), and leaves the row non-running. A
// failure here means a losing delegation leaked its warm pod, which §8.3
// forbids, or the child was left partially materialized.
func TestMaterializeDelegatedChildAssignmentRaceFailsClosed_spec_8_3(t *testing.T) {
	store := memstore.New()
	seedDelegatedChild(t, store, "child-race", "")
	tenants, runtimes, credPools := materializeCredStores(t, credentialpoolstore.CredentialActive)
	cluster, dial, _ := materializeCluster(t)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, dial)
	// The pre-check resolves an assignable pool, but the lease mint fails in the
	// assignment window: the §8.3 line 470 post-pod-claim race.
	binder.Credentials = failingAssigner{}
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "unused" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
	})

	_, err := srv.MaterializeDelegatedChild(context.Background(), "acme", "child-race")
	var credAssign *podsession.CredentialAssignmentError
	if !errors.As(err, &credAssign) {
		t.Fatalf("MaterializeDelegatedChild = %v, want *CredentialAssignmentError", err)
	}
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 (the raced pod must not be registered)", registry.Len())
	}
	row, err := store.Get(context.Background(), "acme", "child-race")
	if err != nil {
		t.Fatalf("get child row: %v", err)
	}
	if row.State != session.StateCreated {
		t.Errorf("child state = %q, want created (non-running after the assignment race)", row.State)
	}
	// The pod claimed before the assignment failure was released: its per-pod
	// SandboxClaim is deleted, so no warm pod leaks.
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err == nil {
		t.Errorf("per-pod claim still present after the assignment race; the claimed pod leaked")
	}
}

// spec: 8.2 (steps 5-7), 8.3 (line 470 post-claim assignment race).
// diagnosis: a store.Update-to-running failure AFTER a successful launch must
// release the bound pod and its assigned credential lease via rollbackBinding,
// mirroring the top-level create path's post-launch persist rollback, and leave
// the row non-running with no registry entry. A failure here means a launched
// child pod and its lease leaked past a failed terminal persist, or a registry
// entry survived the failed write.
func TestMaterializeDelegatedChildPersistFailureReleasesBinding_spec_8_2(t *testing.T) {
	base := memstore.New()
	store := &updateFaultStore{Store: base}
	seedDelegatedChild(t, store, "child-persist", "")
	cluster, dial, _ := materializeCluster(t)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, dial)
	// A recording assigner is wired so the terminal rollback's §7.1 step-23
	// lease revoke is observable. No credential pool is named, so the launch
	// (StartSession) succeeds without an AssignCredentials RPC; the rollback
	// still drives the lease-release primitive that a real lease would ride.
	assigner := &recordingAssigner{}
	binder.Credentials = assigner
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "unused" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	// Fail the terminal transition-persist Update: claim and launch succeed, the
	// row transition fails, and the bound pod + lease must be rolled back.
	store.fail = true
	_, err := srv.MaterializeDelegatedChild(context.Background(), "acme", "child-persist")
	if err == nil {
		t.Fatal("MaterializeDelegatedChild = nil, want a transition-persist failure")
	}
	// The lease assigned during the launch was released on rollback.
	if len(assigner.released) != 1 || assigner.released[0] != "child-persist" {
		t.Errorf("ReleaseSession calls = %v, want [child-persist] (rollbackBinding revokes the lease)", assigner.released)
	}
	// No registry entry leaked: rollbackBinding runs before registerBinding.
	if registry.Len() != 0 {
		t.Errorf("registry holds %d bindings, want 0 (rollbackBinding precedes registerBinding)", registry.Len())
	}
	// The launched pod was released: its per-pod SandboxClaim is deleted.
	store.fail = false
	var claim lennyv1.SandboxClaim
	if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err == nil {
		t.Errorf("per-pod claim still present after the failed persist; the bound pod leaked")
	}
	row, err := store.Get(context.Background(), "acme", "child-persist")
	if err != nil {
		t.Fatalf("get child row: %v", err)
	}
	if row.State != session.StateCreated {
		t.Errorf("child state = %q, want created (non-running after the failed transition)", row.State)
	}
}
