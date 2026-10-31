// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// This file pins the fail-closed contract of the §4.9 pre-claim credential
// resolution against a lookup that gives no definite answer. A tenant,
// runtime, or credential-origin read that fails with anything other than
// not-found must stop every caller before it claims a pod or mints a lease,
// with a retryable error, rather than start the session with no credential.

// errInjectedLookup is the transient store fault the lookup wrappers inject.
var errInjectedLookup = errors.New("injected transient store fault")

// lookupFault selects which read a fixture's stores fail and how.
type lookupFault int

const (
	faultNoneLookup lookupFault = iota
	faultTenantTransient
	faultTenantCanceled
	faultTenantDeadline
	faultTenantNotFound
	faultRuntimeTransient
	faultRuntimeNotFound
	faultRuntimeBaseTransient
	faultOriginSessionTransient
	faultOriginSessionNotFound
	faultOriginRuntimeTransient
)

func (f lookupFault) String() string {
	return [...]string{
		"no fault", "tenant transient", "tenant context canceled", "tenant deadline exceeded",
		"tenant not found", "runtime transient", "runtime not found", "derived runtime base transient",
		"origin session transient", "origin session not found", "origin runtime transient",
	}[f]
}

// usesOrigin reports whether the fault targets an inherit hop's credential
// origin, so the session row under test must name one.
func (f lookupFault) usesOrigin() bool {
	return f == faultOriginSessionTransient || f == faultOriginSessionNotFound || f == faultOriginRuntimeTransient
}

// faultTenants fails Get with the armed error. Like a database driver it
// honors the caller's context first, so a cancelled or expired context
// surfaces as the context error.
type faultTenants struct {
	tenantstore.Store
	mu   sync.Mutex
	err  error
	gets int
}

func (f *faultTenants) arm(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *faultTenants) Get(ctx context.Context, id string) (tenantstore.Tenant, error) {
	f.mu.Lock()
	f.gets++
	err := f.err
	f.mu.Unlock()
	if cerr := ctx.Err(); cerr != nil {
		return tenantstore.Tenant{}, cerr
	}
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	return f.Store.Get(ctx, id)
}

// faultRuntimes fails Get for the named runtimes with the mapped error.
type faultRuntimes struct {
	runtimestore.Store
	mu    sync.Mutex
	fails map[string]error
}

func (f *faultRuntimes) arm(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails == nil {
		f.fails = map[string]error{}
	}
	f.fails[name] = err
}

func (f *faultRuntimes) Get(ctx context.Context, name string) (runtimestore.Runtime, error) {
	f.mu.Lock()
	err := f.fails[name]
	f.mu.Unlock()
	if err != nil {
		return runtimestore.Runtime{}, err
	}
	return f.Store.Get(ctx, name)
}

// faultSessions fails Get for the named session ids with the mapped error.
type faultSessions struct {
	sessionstore.Store
	mu    sync.Mutex
	fails map[string]error
}

func (f *faultSessions) arm(id string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails == nil {
		f.fails = map[string]error{}
	}
	f.fails[id] = err
}

func (f *faultSessions) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	f.mu.Lock()
	err := f.fails[id]
	f.mu.Unlock()
	if err != nil {
		return sessionstore.Session{}, err
	}
	return f.Store.Get(ctx, tenantID, id)
}

// countingAssigner is a Binder.Credentials that counts every lease it mints,
// so a test asserts that no lease is minted on a failed resolution.
type countingAssigner struct {
	mu      sync.Mutex
	assigns int
}

func (a *countingAssigner) AssignProto(pool, _, _, _ string, _ int64) (*adapterv1.CredentialLease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.assigns++
	return &adapterv1.CredentialLease{LeaseId: "cl-" + pool, Provider: pool}, nil
}
func (*countingAssigner) ReleaseSession(string) error        { return nil }
func (*countingAssigner) ReleaseThrough(string, int64) error { return nil }
func (*countingAssigner) Release(string) error               { return nil }

func (a *countingAssigner) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.assigns
}

// kubeWrites counts the writes the binder's client issues, by verb.
type kubeWrites struct {
	mu     sync.Mutex
	counts map[string]int
}

func (k *kubeWrites) add(verb string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.counts == nil {
		k.counts = map[string]int{}
	}
	k.counts[verb]++
}

// nonDelete returns the number of writes other than deletes: a claim, a
// slot reservation, or a status patch.
func (k *kubeWrites) nonDelete() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for verb, c := range k.counts {
		if verb != "delete" {
			n += c
		}
	}
	return n
}

const (
	lookupNS          = "lenny-agents"
	lookupExclusiveRT = "claude-code"
	lookupConcRT      = "claude-conc"
	lookupDerivedRT   = "claude-derived"
	lookupOriginRT    = "origin-rt"
	lookupOriginID    = "origin-1"
	lookupSandbox     = "sbx-1"
)

// lookupFixture is a Server wired with fault-injecting tenant, runtime, and
// session stores over a single-provider tenant credentialPolicy, a fake
// cluster holding an exclusive pool and a concurrent-workspace pool, a
// counting lease assigner, and a write-counting client.
type lookupFixture struct {
	srv      *Server
	tenants  *faultTenants
	runtimes *faultRuntimes
	sessions *faultSessions
	assigner *countingAssigner
	writes   *kubeWrites
	cluster  client.Client
}

type lookupFixtureOpts struct {
	// noPolicy seeds the tenant with no credentialPolicy.
	noPolicy bool
	// noProviders registers the session runtimes with no supportedProviders.
	noProviders bool
}

func newLookupFixture(t *testing.T, opts lookupFixtureOpts) *lookupFixture {
	t.Helper()
	ctx := context.Background()
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
		},
	}
	if opts.noPolicy {
		policy = credential.CredentialPolicy{}
	}
	memTenants := tenantstore.NewMemory()
	if err := memTenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	providers := []string{"anthropic_direct"}
	if opts.noProviders {
		providers = nil
	}
	memRuntimes := runtimestore.NewMemory()
	for _, rt := range []runtimestore.Runtime{
		{Name: lookupExclusiveRT, SupportedProviders: providers},
		{Name: lookupConcRT, SupportedProviders: providers},
		{Name: lookupOriginRT, SupportedProviders: []string{"anthropic_direct"}},
		{Name: "claude-base", SupportedProviders: providers},
		{Name: lookupDerivedRT, BaseRuntime: "claude-base"},
	} {
		if err := memRuntimes.Create(ctx, rt); err != nil {
			t.Fatalf("create runtime %s: %v", rt.Name, err)
		}
	}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, poolFixture("claude-prod", "anthropic_direct", credentialpoolstore.CredentialActive)); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}
	sessions := &faultSessions{Store: memstore.New()}
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: lookupOriginID, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: lookupOriginRT,
	}); err != nil {
		t.Fatalf("create origin session: %v", err)
	}

	writes := &kubeWrites{}
	objs := []client.Object{
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: "claude-pool", Namespace: lookupNS},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-tmpl", MinWarm: 1, MaxWarm: 5},
		},
		&lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "claude-tmpl", Namespace: lookupNS},
			Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: lookupExclusiveRT, IsolationProfile: string(isolation.ProfileSandboxed)},
		},
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: "derived-pool", Namespace: lookupNS},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "derived-tmpl", MinWarm: 1, MaxWarm: 5},
		},
		&lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "derived-tmpl", Namespace: lookupNS},
			Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: lookupDerivedRT, IsolationProfile: string(isolation.ProfileSandboxed)},
		},
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: "conc-pool", Namespace: lookupNS},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "conc-tmpl", MinWarm: 1, MaxWarm: 5},
		},
		&lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "conc-tmpl", Namespace: lookupNS},
			Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: lookupConcRT, IsolationProfile: string(isolation.ProfileSandboxed)},
		},
		// The create-time claim a finalize-time row holds on its pod.
		&lennyv1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: "claim-" + lookupSandbox, Namespace: lookupNS}},
	}
	cluster := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				writes.add("create")
				return cl.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				writes.add("update")
				return cl.Update(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				writes.add("patch")
				return cl.Patch(ctx, obj, patch, opts...)
			},
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				writes.add("delete")
				return cl.Delete(ctx, obj, opts...)
			},
			SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				writes.add("status-patch")
				return cl.SubResource(sub).Patch(ctx, obj, patch, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, cl client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				writes.add("status-update")
				return cl.SubResource(sub).Update(ctx, obj, opts...)
			},
		}).Build()

	pools := poolstore.NewMemory()
	if err := pools.Create(ctx, poolstore.Pool{
		Name:          "conc-pool",
		RuntimeRef:    lookupConcRT,
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{MaxConcurrentSessions: 4, AcknowledgeProcessLevelIsolation: true},
	}); err != nil {
		t.Fatalf("create concurrent pool mirror: %v", err)
	}
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	f := &lookupFixture{
		tenants:  &faultTenants{Store: memTenants},
		runtimes: &faultRuntimes{Store: memRuntimes},
		sessions: sessions,
		assigner: &countingAssigner{},
		writes:   writes,
		cluster:  cluster,
	}
	f.srv = New(sessions, Options{
		DevTenantHeader: true,
		PodBinder: &podsession.Binder{
			Client: cluster, Namespace: lookupNS, SlotCounter: slotcounter.New(rc), Credentials: f.assigner,
		},
		AgentNamespace:   lookupNS,
		Pools:            pools,
		Tenants:          f.tenants,
		Runtimes:         f.runtimes,
		CredentialPools:  credPools,
		CredentialRouter: credrouter.NewDefault(),
	})
	t.Cleanup(f.srv.Close)
	return f
}

// apply arms the fault on the fixture's stores and returns the context the
// call under test runs on and the cause a fail-closed error must wrap.
func (f *lookupFixture) apply(t *testing.T, fault lookupFault, runtimeRef string) (context.Context, error) {
	t.Helper()
	ctx := context.Background()
	switch fault {
	case faultTenantTransient:
		f.tenants.arm(errInjectedLookup)
		return ctx, errInjectedLookup
	case faultTenantCanceled:
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		return cctx, context.Canceled
	case faultTenantDeadline:
		dctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		t.Cleanup(cancel)
		return dctx, context.DeadlineExceeded
	case faultTenantNotFound:
		f.tenants.arm(tenantstore.ErrNotFound)
	case faultRuntimeTransient:
		f.runtimes.arm(runtimeRef, errInjectedLookup)
		return ctx, errInjectedLookup
	case faultRuntimeNotFound:
		f.runtimes.arm(runtimeRef, runtimestore.ErrNotFound)
	case faultRuntimeBaseTransient:
		f.runtimes.arm("claude-base", errInjectedLookup)
		return ctx, errInjectedLookup
	case faultOriginSessionTransient:
		f.sessions.arm(lookupOriginID, errInjectedLookup)
		return ctx, errInjectedLookup
	case faultOriginSessionNotFound:
		f.sessions.arm(lookupOriginID, sessionstore.ErrNotFound)
	case faultOriginRuntimeTransient:
		f.runtimes.arm(lookupOriginRT, errInjectedLookup)
		return ctx, errInjectedLookup
	}
	return ctx, nil
}

// claimCount returns the number of SandboxClaims in the fixture's cluster.
func (f *lookupFixture) claimCount(t *testing.T) int {
	t.Helper()
	var claims lennyv1.SandboxClaimList
	if err := f.cluster.List(context.Background(), &claims, client.InNamespace(lookupNS)); err != nil {
		t.Fatalf("list claims: %v", err)
	}
	return len(claims.Items)
}

// lookupOutcome classifies a resolution's result for a caller.
type lookupOutcome int

const (
	// outcomeLookupError is the fail-closed retryable CredentialLookupError.
	outcomeLookupError lookupOutcome = iota
	// outcomeExhausted is the definite §4.9 empty-intersection answer.
	outcomeExhausted
	// outcomeProceeds is a resolution that let the caller move past the
	// credential stage.
	outcomeProceeds
)

func (o lookupOutcome) String() string {
	return [...]string{"credential lookup error", "credential pool exhausted", "proceeds past the credential stage"}[o]
}

func isExhausted(err error) bool {
	var credAssign *podsession.CredentialAssignmentError
	return errors.Is(err, credrouter.ErrNoCredentialAvailable) ||
		errors.Is(err, ErrDelegationCredentialUnavailable) ||
		errors.As(err, &credAssign)
}

// expectedOutcome is the spec outcome of each fault for every caller: a
// read with no definite answer fails closed with a retryable lookup error, a
// definite not-found of the tenant means no policy applies, and a definite
// not-found of the runtime, the origin session, or the origin runtime is the
// §4.9 / §8.3 empty intersection.
func expectedOutcome(f lookupFault) lookupOutcome {
	switch f {
	case faultNoneLookup, faultTenantNotFound:
		return outcomeProceeds
	case faultRuntimeNotFound, faultOriginSessionNotFound:
		return outcomeExhausted
	default:
		return outcomeLookupError
	}
}

var allLookupFaults = []lookupFault{
	faultNoneLookup, faultTenantTransient, faultTenantCanceled, faultTenantDeadline, faultTenantNotFound,
	faultRuntimeTransient, faultRuntimeNotFound, faultRuntimeBaseTransient,
	faultOriginSessionTransient, faultOriginSessionNotFound, faultOriginRuntimeTransient,
}

// TestResolveCredentialPoolsFailsClosedOnLookupError_spec_4_9 sweeps every
// read resolveCredentialPools makes against each failure kind. A transient
// error, a cancelled context, or an expired deadline returns a
// *CredentialLookupError wrapping the cause, never an empty assignment with
// no error. A tenant with no row resolves to no policy; an absent runtime,
// origin session, or origin runtime resolves to CREDENTIAL_POOL_EXHAUSTED.
// The controls pin the success path, the no-policy path (which reads no
// runtime, so a runtime fault does not touch it), a runtime that declares no
// providers, and a deny row.
//
// spec: §4.9 (Pre-Claim Credential Availability Check; Intersection at
// session creation), §8.3 (credential origin), §7.1 (Atomicity of session
// creation)
func TestResolveCredentialPoolsFailsClosedOnLookupError_spec_4_9(t *testing.T) {
	for _, fault := range allLookupFaults {
		for _, rtRef := range []string{lookupExclusiveRT, lookupDerivedRT} {
			if fault == faultRuntimeBaseTransient && rtRef != lookupDerivedRT {
				continue
			}
			t.Run(fault.String()+"/"+rtRef, func(t *testing.T) {
				f := newLookupFixture(t, lookupFixtureOpts{})
				row := sessionstore.Session{ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: rtRef}
				if fault.usesOrigin() {
					row.CredentialOriginSessionID = lookupOriginID
				}
				ctx, cause := f.apply(t, fault, rtRef)
				pools, _, users, err := f.srv.resolveCredentialPools(ctx, row)
				assertLookupOutcome(t, err, cause, expectedOutcome(fault))
				if err != nil && (len(pools) != 0 || len(users) != 0) {
					t.Errorf("failed resolution returned assignments pools=%v users=%v", pools, users)
				}
				if err == nil && pools["anthropic_direct"] != "claude-prod" && fault != faultTenantNotFound {
					t.Errorf("successful resolution under a configured policy assigned %v, want anthropic_direct→claude-prod", pools)
				}
			})
		}
	}

	t.Run("control: no policy reads no runtime", func(t *testing.T) {
		f := newLookupFixture(t, lookupFixtureOpts{noPolicy: true})
		f.runtimes.arm(lookupExclusiveRT, errInjectedLookup)
		pools, _, _, err := f.srv.resolveCredentialPools(context.Background(), sessionstore.Session{
			UserID: testOwner,
			ID:     "s1", TenantID: "acme", RuntimeRef: lookupExclusiveRT,
		})
		if err != nil || len(pools) != 0 {
			t.Errorf("no-policy resolution = (%v, %v), want no assignment and no error", pools, err)
		}
	})
	t.Run("control: runtime declares no providers", func(t *testing.T) {
		f := newLookupFixture(t, lookupFixtureOpts{noProviders: true})
		_, _, _, err := f.srv.resolveCredentialPools(context.Background(), sessionstore.Session{
			UserID: testOwner,
			ID:     "s1", TenantID: "acme", RuntimeRef: lookupExclusiveRT,
		})
		if !errors.Is(err, credrouter.ErrNoCredentialAvailable) {
			t.Errorf("runtime with no providers = %v, want ErrNoCredentialAvailable", err)
		}
	})
	t.Run("control: deny row reads nothing", func(t *testing.T) {
		f := newLookupFixture(t, lookupFixtureOpts{})
		f.tenants.arm(errInjectedLookup)
		pools, _, _, err := f.srv.resolveCredentialPools(context.Background(), sessionstore.Session{
			UserID: testOwner,
			ID:     "s1", TenantID: "acme", RuntimeRef: lookupExclusiveRT, CredentialDeny: true,
		})
		if err != nil || len(pools) != 0 {
			t.Errorf("deny row = (%v, %v), want no assignment and no error (a deny hop holds no credential)", pools, err)
		}
	})
}

func assertLookupOutcome(t *testing.T, err, cause error, want lookupOutcome) {
	t.Helper()
	var lookup *CredentialLookupError
	switch want {
	case outcomeLookupError:
		if !errors.As(err, &lookup) {
			t.Fatalf("err = %v, want *CredentialLookupError (fail closed, retryable)", err)
		}
		if !errors.Is(err, cause) {
			t.Errorf("err = %v, want it to wrap %v", err, cause)
		}
		if isExhausted(err) {
			t.Errorf("err = %v answers a transient lookup as a definite exhaustion", err)
		}
	case outcomeExhausted:
		if !isExhausted(err) {
			t.Errorf("err = %v, want the definite CREDENTIAL_POOL_EXHAUSTED answer", err)
		}
	case outcomeProceeds:
		if errors.As(err, &lookup) || isExhausted(err) {
			t.Errorf("err = %v, want the caller to move past the credential stage", err)
		}
	}
}

// preClaimCaller is one caller of resolveCredentialPools, driven through
// its own entry point on a fixture row.
type preClaimCaller struct {
	name string
	// runtime is the runtime the caller's row runs.
	runtime string
	// row adjusts the base row for the caller.
	row func(*sessionstore.Session)
	// seed persists the row before the call when the caller reads it.
	seed bool
	// call runs the caller and reports how many times a resume driver's
	// post-claim hook ran.
	call func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (hookRuns int, err error)
}

func preClaimCallers() []preClaimCaller {
	countingHook := func(n *int) resumeBind {
		return resumeBind{onClaimed: func(context.Context) error { *n++; return nil }, generation: 1}
	}
	return []preClaimCaller{
		{
			name: "create (POST /v1/sessions claim)", runtime: lookupExclusiveRT,
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				_, err := f.srv.claimAtCreate(ctx, row, workspaceplan.Plan{}, claimRouteCreate)
				return 0, err
			},
		},
		{
			name: "combined create-and-start claim", runtime: lookupExclusiveRT,
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				_, err := f.srv.claimAtCreate(ctx, row, workspaceplan.Plan{}, claimRouteStart)
				return 0, err
			},
		},
		{
			name: "concurrent-pool create slot reservation", runtime: lookupConcRT,
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				_, err := f.srv.claimAtCreate(ctx, row, workspaceplan.Plan{}, claimRouteCreate)
				return 0, err
			},
		},
		{
			name: "finalize lease assignment", runtime: lookupExclusiveRT,
			row: func(r *sessionstore.Session) {
				r.State = session.StateFinalizing
				r.PodAssignment = lookupSandbox
			},
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				_, err := f.srv.prepareAtFinalize(ctx, row, workspaceplan.Plan{})
				return 0, err
			},
		},
		{
			name: "two-step start on a concurrent pool", runtime: lookupConcRT,
			row: func(r *sessionstore.Session) { r.State = session.StateReady },
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				_, err := f.srv.launchOnPod(ctx, row, workspaceplan.Plan{})
				return 0, err
			},
		},
		{
			name: "resume from a checkpoint", runtime: lookupExclusiveRT,
			row: func(r *sessionstore.Session) {
				r.State = session.StateResumePending
				r.WorkspaceSnapshot = &sessionstore.WorkspaceSnapshot{Ref: "ckpt-1", Source: sessionstore.WorkspaceSnapshotCheckpoint}
			},
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				n := 0
				_, err := f.srv.resumeOnPod(ctx, row, countingHook(&n))
				return n, err
			},
		},
		{
			name: "snapshotless resume rebuild", runtime: lookupExclusiveRT,
			row: func(r *sessionstore.Session) { r.State = session.StateResumePending },
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				n := 0
				_, err := f.srv.resumeOnPod(ctx, row, countingHook(&n))
				return n, err
			},
		},
		{
			name: "snapshotless resume rebuild on a concurrent pool", runtime: lookupConcRT,
			row: func(r *sessionstore.Session) { r.State = session.StateResumePending },
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				n := 0
				_, err := f.srv.resumeOnPod(ctx, row, countingHook(&n))
				return n, err
			},
		},
		{
			name: "delegation-time availability check", runtime: lookupExclusiveRT,
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				return 0, f.srv.CheckDelegationCredentialAvailability(ctx, DelegationCredentialQuery{
					TenantID: row.TenantID, UserID: row.UserID, ChildRuntimeRef: row.RuntimeRef,
					CredentialOriginSessionID: row.CredentialOriginSessionID,
				})
			},
		},
		{
			name: "delegated child materialization", runtime: lookupExclusiveRT, seed: true,
			row: func(r *sessionstore.Session) {
				r.State = session.StateCreated
				r.ParentSessionID = "parent-1"
			},
			call: func(ctx context.Context, f *lookupFixture, row sessionstore.Session) (int, error) {
				_, err := f.srv.MaterializeDelegatedChild(ctx, row.TenantID, row.ID)
				return 0, err
			},
		},
	}
}

// TestCredentialPreClaimCallersFailClosed_spec_4_9 drives every caller of the
// §4.9 pre-claim credential resolution against every failure kind. Each
// failed lookup fails the caller closed: it returns the retryable
// *CredentialLookupError, mints no credential lease, issues no claim or slot
// write to the cluster, runs no resume entry hook, and leaves a delegated
// child in created. A finalize-time row leaves its create-time claim to the
// applied failure write's post-terminal hook. The
// definite answers keep their meaning: no tenant row proceeds with no policy,
// an absent runtime or origin is CREDENTIAL_POOL_EXHAUSTED, and the no-fault
// control moves past the credential stage.
//
// spec: §4.9 (Pre-Claim Credential Availability Check), §7.1 (Atomicity of
// session creation), §7.3 (Resume driver; Credential leases on the
// replacement pod), §8.2 (delegated-child materialization), §8.3
// (Credential availability pre-check at delegation time)
func TestCredentialPreClaimCallersFailClosed_spec_4_9(t *testing.T) {
	for _, caller := range preClaimCallers() {
		for _, fault := range allLookupFaults {
			if fault == faultRuntimeBaseTransient {
				continue // covered on the resolution sweep, which runs a derived runtime
			}
			t.Run(caller.name+"/"+fault.String(), func(t *testing.T) {
				f := newLookupFixture(t, lookupFixtureOpts{})
				row := sessionstore.Session{
					ID: "s-" + fmt.Sprint(int(fault)), TenantID: "acme", UserID: "alice@acme.com",
					RuntimeRef: caller.runtime, IsolationProfile: isolation.ProfileSandboxed,
				}
				if caller.row != nil {
					caller.row(&row)
				}
				if fault.usesOrigin() {
					row.CredentialOriginSessionID = lookupOriginID
				}
				if caller.seed {
					if err := f.sessions.Create(context.Background(), row); err != nil {
						t.Fatalf("seed row: %v", err)
					}
				}
				claimsBefore := f.claimCount(t)
				ctx, cause := f.apply(t, fault, caller.runtime)

				hookRuns, err := caller.call(ctx, f, row)

				want := expectedOutcome(fault)
				if caller.name == "delegated child materialization" &&
					(fault == faultTenantCanceled || fault == faultTenantDeadline) {
					// The materialization reads the child row first, on the same
					// context, and refuses there; it never reaches a claim.
					if err == nil {
						t.Fatal("materialization on an ended context succeeded")
					}
				} else {
					assertLookupOutcome(t, err, cause, want)
				}
				if want == outcomeProceeds {
					return
				}
				if n := f.assigner.count(); n != 0 {
					t.Errorf("credential leases minted = %d, want 0 on a failed resolution", n)
				}
				if n := f.writes.nonDelete(); n != 0 {
					t.Errorf("cluster writes = %v, want no claim or slot write on a failed resolution", f.writes.counts)
				}
				if hookRuns != 0 {
					t.Errorf("resume entry hook ran %d times, want 0 (the failure is pre-claim)", hookRuns)
				}
				// A finalize-time row keeps its create-time claim here: the caller's
				// applied failure write releases it through the post-terminal hook.
				if claimsAfter := f.claimCount(t); claimsAfter != claimsBefore {
					t.Errorf("claims %d -> %d, want unchanged", claimsBefore, claimsAfter)
				}
				if caller.seed {
					got, gerr := f.sessions.Store.Get(context.Background(), row.TenantID, row.ID)
					if gerr != nil {
						t.Fatalf("read row: %v", gerr)
					}
					if got.State != session.StateCreated || got.PodAssignment != "" {
						t.Errorf("delegated child = %s on %q, want created with no pod", got.State, got.PodAssignment)
					}
				}
			})
		}
	}
}

// TestWritePodClaimErrorCredentialLookupIsRetryable_spec_7_1 pins the REST
// answer to a failed credential lookup on each endpoint that runs the
// pre-claim resolution: the endpoint's §7.1 atomic-unit code, HTTP 503,
// category TRANSIENT, a Retry-After header, and details.reason
// credential_lookup_unavailable. It is never CREDENTIAL_POOL_EXHAUSTED,
// which would tell the client the pool is out of credentials.
//
// spec: §7.1 (Atomicity of session creation), §15.1 (SESSION_CREATION_FAILED,
// STARTING_FAILED), §4.9
func TestWritePodClaimErrorCredentialLookupIsRetryable_spec_7_1(t *testing.T) {
	for _, tc := range []struct{ endpoint, code string }{
		{"POST /v1/sessions", "SESSION_CREATION_FAILED"},
		{"POST /v1/sessions/start", "SESSION_CREATION_FAILED"},
		{"POST /v1/sessions/{id}/finalize", "SESSION_CREATION_FAILED"},
		{"POST /v1/sessions/{id}/start", "STARTING_FAILED"},
	} {
		for _, cause := range []error{errInjectedLookup, context.Canceled, context.DeadlineExceeded} {
			t.Run(tc.endpoint+"/"+cause.Error(), func(t *testing.T) {
				s := &Server{}
				rr := httptest.NewRecorder()
				s.writePodClaimError(rr, &CredentialLookupError{Lookup: "tenant credentialPolicy", Err: cause}, tc.code, "failed")
				if rr.Code != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, want 503", rr.Code)
				}
				if rr.Header().Get("Retry-After") == "" {
					t.Error("no Retry-After header on a retryable answer")
				}
				var env struct {
					Error struct {
						Code      string         `json:"code"`
						Category  string         `json:"category"`
						Retryable bool           `json:"retryable"`
						Details   map[string]any `json:"details"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if env.Error.Code != tc.code || env.Error.Category != "TRANSIENT" || !env.Error.Retryable {
					t.Errorf("answer = %s/%s retryable=%v, want %s/TRANSIENT retryable", env.Error.Code, env.Error.Category, env.Error.Retryable, tc.code)
				}
				if env.Error.Details["reason"] != "credential_lookup_unavailable" {
					t.Errorf("details.reason = %v, want credential_lookup_unavailable", env.Error.Details["reason"])
				}
			})
		}
	}
}

// TestCreateOverrideRestrictCheckFailsClosed_spec_14 runs the POST
// /v1/sessions request-envelope validation with a per-session
// credentialPolicy override against a tenant policy read that fails. An
// override that would use user credentials cannot be checked against the
// restrict-only rule without the policy, so the create fails closed with the
// retryable SESSION_CREATION_FAILED and copies no override onto the row. A
// narrowing override needs no read and passes through the same fault; a
// tenant with no row has no policy and passes; and a readable policy that
// forbids user credentials still rejects the expansion with VALIDATION_ERROR.
//
// spec: §14 (credentialPolicy restrict-only override), §7.1 (Atomicity of
// session creation), §4.9
func TestCreateOverrideRestrictCheckFailsClosed_spec_14(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override string
		fault    error
		wantOK   bool
		wantCode int
		wantErr  string
	}{
		{"user override, transient read", "user", errInjectedLookup, false, http.StatusServiceUnavailable, "SESSION_CREATION_FAILED"},
		{"user-then-pool override, canceled read", "prefer-user-then-pool", context.Canceled, false, http.StatusServiceUnavailable, "SESSION_CREATION_FAILED"},
		{"pool-then-user override, deadline read", "prefer-pool-then-user", context.DeadlineExceeded, false, http.StatusServiceUnavailable, "SESSION_CREATION_FAILED"},
		{"user override, no tenant row", "user", tenantstore.ErrNotFound, true, 0, ""},
		{"pool override, transient read", "pool", errInjectedLookup, true, 0, ""},
		{"user override, readable policy forbids it", "user", nil, false, http.StatusBadRequest, "VALIDATION_ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			memTenants := tenantstore.NewMemory()
			if err := memTenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", CredentialPolicy: credential.CredentialPolicy{
				PreferredSource: credential.PreferredSourcePool,
				ProviderPools:   map[string]credential.ProviderPool{"anthropic_direct": {DefaultPool: "claude-prod"}},
			}}); err != nil {
				t.Fatalf("create tenant: %v", err)
			}
			tenants := &faultTenants{Store: memTenants}
			srv := New(memstore.New(), Options{DevTenantHeader: true, Tenants: tenants})
			defer srv.Close()
			if tc.fault != nil {
				tenants.arm(tc.fault)
			}
			req := CreateSessionRequest{
				RuntimeRef:       "claude-code",
				CredentialPolicy: &sessionstore.CredentialPolicyOverride{PreferredSource: tc.override},
			}
			body, _ := json.Marshal(req)
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
			rr := httptest.NewRecorder()
			row := sessionstore.Session{UserID: testOwner, ID: "sess-override", TenantID: "acme"}
			_, ok := srv.validateRequestEnvelope(rr, r, req, "acme", &row)
			if ok != tc.wantOK {
				t.Fatalf("envelope ok = %v, want %v; status=%d body=%s", ok, tc.wantOK, rr.Code, rr.Body.String())
			}
			if tc.wantOK {
				if row.CredentialPolicyOverride == nil || row.CredentialPolicyOverride.PreferredSource != tc.override {
					t.Errorf("override on the row = %+v, want %q", row.CredentialPolicyOverride, tc.override)
				}
				return
			}
			if row.CredentialPolicyOverride != nil {
				t.Errorf("a rejected envelope copied the override %+v onto the row", row.CredentialPolicyOverride)
			}
			if rr.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantCode, rr.Body.String())
			}
			var env struct {
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if env.Error.Code != tc.wantErr {
				t.Errorf("code = %q, want %q", env.Error.Code, tc.wantErr)
			}
			if tc.wantCode == http.StatusServiceUnavailable && (!env.Error.Retryable || rr.Header().Get("Retry-After") == "") {
				t.Errorf("fail-closed answer retryable=%v Retry-After=%q, want retryable with Retry-After", env.Error.Retryable, rr.Header().Get("Retry-After"))
			}
		})
	}
}
