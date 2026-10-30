// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// resumeCredFixture is a resume driver over a service-mode pool, whose
// restore claims no pod, with a configured tenant credentialPolicy and
// fault-injecting tenant, runtime, and session stores. A restore resolves
// the session's credentials before it runs the driver's entry hook, so a
// row that enters resuming passed the §4.9 resolution.
type resumeCredFixture struct {
	srv      *Server
	inner    sessionstore.Store
	tenants  *faultTenants
	runtimes *faultRuntimes
	sessions *faultSessions
	mu       sync.Mutex
	attempts []string
}

func newResumeCredFixture(t *testing.T) *resumeCredFixture {
	t.Helper()
	ctx := context.Background()
	const ns = "lenny-agents"
	c := fake.NewClientBuilder().WithScheme(claimAtCreateScheme(t)).WithObjects(
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-pool", Namespace: ns},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "svc-tmpl", MinWarm: 1, MaxWarm: 5},
		},
		&lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tmpl", Namespace: ns},
			Spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef:       "svc-runtime",
				IsolationProfile: string(isolation.ProfileSandboxed),
				ExecutionMode:    string(runtimestore.ExecutionModeService),
			},
		},
	).Build()
	memTenants := tenantstore.NewMemory()
	if err := memTenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{"anthropic_direct": {DefaultPool: "claude-prod"}},
	}}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	memRuntimes := runtimestore.NewMemory()
	for _, name := range []string{"svc-runtime", lookupOriginRT} {
		if err := memRuntimes.Create(ctx, runtimestore.Runtime{Name: name, SupportedProviders: []string{"anthropic_direct"}}); err != nil {
			t.Fatalf("create runtime: %v", err)
		}
	}
	credPools := credentialpoolstore.NewMemory()
	if err := credPools.Create(ctx, poolFixture("claude-prod", "anthropic_direct", credentialpoolstore.CredentialActive)); err != nil {
		t.Fatalf("create credential pool: %v", err)
	}
	inner := memstore.New()
	if err := inner.Create(ctx, sessionstore.Session{
		ID: lookupOriginID, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: lookupOriginRT, State: session.StateRunning,
	}); err != nil {
		t.Fatalf("create origin: %v", err)
	}
	f := &resumeCredFixture{
		inner:    inner,
		tenants:  &faultTenants{Store: memTenants},
		runtimes: &faultRuntimes{Store: memRuntimes},
		sessions: &faultSessions{Store: inner},
	}
	f.srv = New(f.sessions, Options{
		DevTenantHeader:  true,
		PodBinder:        &podsession.Binder{Client: c, Namespace: ns},
		AgentNamespace:   ns,
		Tenants:          f.tenants,
		Runtimes:         f.runtimes,
		CredentialPools:  credPools,
		CredentialRouter: credrouter.NewDefault(),
		IncSessionResumeAttempt: func(pool, outcome string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.attempts = append(f.attempts, pool+":"+outcome)
		},
	})
	t.Cleanup(f.srv.Close)
	return f
}

// seed persists a resume_pending row at coordination generation 4.
func (f *resumeCredFixture) seed(t *testing.T, id string, inherit bool) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "svc-runtime",
		IsolationProfile: isolation.ProfileSandboxed, State: session.StateResumePending,
		CoordinationGeneration: 4, RetryCount: 2, Pool: "svc-pool", PoolRef: "svc-pool",
	}
	if inherit {
		row.CredentialOriginSessionID = lookupOriginID
	}
	if err := f.inner.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	got, err := f.inner.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return got
}

func (f *resumeCredFixture) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.attempts...)
}

// TestResumeDriverRetriesCredentialLookupFailure_spec_7_3 drives the resume
// driver over a session whose credential resolution fails transiently, for
// each read the resolution makes. Each attempt is a pre-claim failure: it
// writes nothing, so the row stays resume_pending at its coordination
// generation with its retryCount unchanged, the entry hook never runs, and no
// resume attempt is counted. Once the store recovers, the next attempt
// resolves the credential, enters resuming at G, and commits to running.
//
// spec: §7.3 (Resume driver item 1; Credential leases on the replacement
// pod; Retry accounting), §4.9 (Pre-Claim Credential Availability Check),
// §16.1 (lenny_session_resume_attempts_total)
func TestResumeDriverRetriesCredentialLookupFailure_spec_7_3(t *testing.T) {
	for _, tc := range []struct {
		name    string
		inherit bool
		arm     func(f *resumeCredFixture)
		disarm  func(f *resumeCredFixture)
	}{
		{
			name:   "tenant policy read",
			arm:    func(f *resumeCredFixture) { f.tenants.arm(errInjectedLookup) },
			disarm: func(f *resumeCredFixture) { f.tenants.arm(nil) },
		},
		{
			name:   "tenant policy read canceled",
			arm:    func(f *resumeCredFixture) { f.tenants.arm(context.Canceled) },
			disarm: func(f *resumeCredFixture) { f.tenants.arm(nil) },
		},
		{
			name:   "runtime read",
			arm:    func(f *resumeCredFixture) { f.runtimes.arm("svc-runtime", errInjectedLookup) },
			disarm: func(f *resumeCredFixture) { f.runtimes.arm("svc-runtime", nil) },
		},
		{
			name: "origin session read", inherit: true,
			arm:    func(f *resumeCredFixture) { f.sessions.arm(lookupOriginID, errInjectedLookup) },
			disarm: func(f *resumeCredFixture) { f.sessions.arm(lookupOriginID, nil) },
		},
		{
			name: "origin runtime read", inherit: true,
			arm:    func(f *resumeCredFixture) { f.runtimes.arm(lookupOriginRT, errInjectedLookup) },
			disarm: func(f *resumeCredFixture) { f.runtimes.arm(lookupOriginRT, nil) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResumeCredFixture(t)
			row := f.seed(t, "s-resume", tc.inherit)
			tc.arm(f)
			for i := 0; i < 3; i++ {
				f.srv.DriveResumeAndWait(context.Background(), row)
				got, err := f.inner.Get(context.Background(), "acme", row.ID)
				if err != nil {
					t.Fatalf("read row: %v", err)
				}
				if got.State != session.StateResumePending || got.CoordinationGeneration != 4 || got.RetryCount != 2 {
					t.Fatalf("attempt %d left the row %s at gen %d retry %d, want resume_pending at gen 4 retry 2 (a pre-claim failure writes nothing)",
						i, got.State, got.CoordinationGeneration, got.RetryCount)
				}
			}
			if got := f.recorded(); len(got) != 0 {
				t.Errorf("resume attempts recorded %v for pre-claim failures, want none", got)
			}
			tc.disarm(f)
			f.srv.DriveResumeAndWait(context.Background(), row)
			got, err := f.inner.Get(context.Background(), "acme", row.ID)
			if err != nil {
				t.Fatalf("read row: %v", err)
			}
			if got.State != session.StateRunning || got.CoordinationGeneration != 5 || got.RetryCount != 2 {
				t.Errorf("after recovery the row is %s at gen %d retry %d, want running at gen 5 retry 2", got.State, got.CoordinationGeneration, got.RetryCount)
			}
			if got := f.recorded(); len(got) != 1 || got[0] != "svc-pool:success" {
				t.Errorf("resume attempts recorded %v, want [svc-pool:success]", got)
			}
		})
	}
}

// flappingTenants fails each tenant read with probability p, drawn from a
// seeded source, and records the decision under the call identifier the
// caller's context carries.
type flappingTenants struct {
	tenantstore.Store
	mu        sync.Mutex
	rng       *rand.Rand
	p         float64
	decisions map[string]bool
	reads     int
	faults    int
}

type callIDKey struct{}

func (f *flappingTenants) Get(ctx context.Context, id string) (tenantstore.Tenant, error) {
	f.mu.Lock()
	fail := f.rng.Float64() < f.p
	f.reads++
	if fail {
		f.faults++
	}
	if call, ok := ctx.Value(callIDKey{}).(string); ok {
		f.decisions[call] = fail
	}
	f.mu.Unlock()
	if fail {
		return tenantstore.Tenant{}, errInjectedLookup
	}
	return f.Store.Get(ctx, id)
}

func (f *flappingTenants) decision(call string) (fail, seen bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fail, seen = f.decisions[call]
	return fail, seen
}

// TestCredentialLookupFlappingStoreRandomizedInterleavings_spec_4_9 runs
// concurrent resume attempts for several sessions and concurrent
// delegation-time availability checks against a tenant store that fails a
// random share of its reads, under a logged seed. It checks invariants after
// every round rather than one end state:
//
//   - a resume attempt either commits the row to running at its next
//     generation or leaves it exactly as seeded (resume_pending, generation
//     4, retryCount 2), so a failed credential resolution never writes the
//     row, enters resuming, or advances the retry count;
//   - the counted resume attempts equal the rows committed to running, so no
//     attempt that failed its resolution was counted;
//   - each availability check fails with the retryable lookup error exactly
//     when its own tenant read failed, and admits otherwise, so no read
//     failure is ever answered as an admission.
//
// Every session reaches running once enough rounds pass.
//
// spec: §4.9 (Pre-Claim Credential Availability Check), §7.3 (Resume driver;
// Retry accounting), §8.3 (Credential availability pre-check at delegation
// time)
func TestCredentialLookupFlappingStoreRandomizedInterleavings_spec_4_9(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	f := newResumeCredFixture(t)
	flap := &flappingTenants{Store: f.tenants.Store, rng: rand.New(rand.NewSource(seed)), p: 0.6, decisions: map[string]bool{}}
	f.srv.tenants = flap

	const sessions = 8
	rows := make([]sessionstore.Session, sessions)
	for i := range rows {
		rows[i] = f.seed(t, fmt.Sprintf("s-flap-%d", i), i%2 == 1)
	}

	var checks sync.WaitGroup
	var checkErrs sync.Map
	call := 0
	for round := 0; round < 200; round++ {
		var wg sync.WaitGroup
		for _, row := range rows {
			wg.Add(1)
			go func(row sessionstore.Session) {
				defer wg.Done()
				f.srv.DriveResumeAndWait(context.Background(), row)
			}(row)
		}
		for j := 0; j < 4; j++ {
			call++
			id := fmt.Sprintf("check-%d", call)
			checks.Add(1)
			go func(id string, inherit bool) {
				defer checks.Done()
				q := DelegationCredentialQuery{TenantID: "acme", UserID: "alice@acme.com", ChildRuntimeRef: "svc-runtime"}
				if inherit {
					q.CredentialOriginSessionID = lookupOriginID
				}
				err := f.srv.CheckDelegationCredentialAvailability(context.WithValue(context.Background(), callIDKey{}, id), q)
				checkErrs.Store(id, err)
			}(id, j%2 == 1)
		}
		wg.Wait()

		running := 0
		for _, row := range rows {
			got, err := f.inner.Get(context.Background(), "acme", row.ID)
			if err != nil {
				t.Fatalf("read %s: %v", row.ID, err)
			}
			switch {
			case got.State == session.StateRunning && got.CoordinationGeneration == 5 && got.RetryCount == 2:
				running++
			case got.State == session.StateResumePending && got.CoordinationGeneration == 4 && got.RetryCount == 2:
			default:
				t.Fatalf("round %d: %s is %s at gen %d retry %d; a failed resolution wrote the row",
					round, row.ID, got.State, got.CoordinationGeneration, got.RetryCount)
			}
		}
		if n := len(f.recorded()); n != running {
			t.Fatalf("round %d: %d resume attempts counted for %d committed rows", round, n, running)
		}
		if running == sessions {
			break
		}
	}
	checks.Wait()

	for _, row := range rows {
		got, _ := f.inner.Get(context.Background(), "acme", row.ID)
		if got.State != session.StateRunning {
			t.Errorf("%s never reached running (seed %d)", row.ID, seed)
		}
	}
	checkErrs.Range(func(k, v any) bool {
		id := k.(string)
		err, _ := v.(error)
		fail, seen := flap.decision(id)
		if !seen {
			t.Errorf("%s: the check made no tenant read", id)
			return true
		}
		var lookup *CredentialLookupError
		switch {
		case fail && !errors.As(err, &lookup):
			t.Errorf("%s: tenant read failed but the check answered %v, want *CredentialLookupError", id, err)
		case !fail && err != nil:
			t.Errorf("%s: tenant read succeeded but the check answered %v, want admission", id, err)
		}
		return true
	})
	flap.mu.Lock()
	t.Logf("tenant reads %d, injected faults %d", flap.reads, flap.faults)
	if flap.faults == 0 {
		t.Error("no fault was injected; the run proves nothing")
	}
	flap.mu.Unlock()
}
