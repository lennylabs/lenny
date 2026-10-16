// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §4.9 pre-claim credential resolution
// under a flapping store and concurrent callers on two gateway replicas.
//
// The resolution reads the tenant credentialPolicy, the child runtime, and,
// for an inherit hop, the credential-origin session and its runtime. Two
// sessionserver replicas share those stores, and every read fails at random
// under a logged seed. Many goroutines run the §8.3 delegation-time
// availability check on both replicas at once. Each call carries its own
// identifier, and every store records whether it failed that call's read, so
// the invariant is checked per call rather than on an end state: a call
// whose reads all succeeded admits, and a call with any failed read fails
// with the retryable *sessionserver.CredentialLookupError. No read failure is
// answered as an admission, which on the start, resume, and materialization
// paths would run a session without the credential its policy requires, and
// none is answered as CREDENTIAL_POOL_EXHAUSTED, which would tell the caller
// the pool is empty.
//
// Stress budget:
//
//	lenny-test stress --test TestCredentialLookupFlappingStoresTwoReplicas_spec_4_9 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//
// spec: §4.9 (Pre-Claim Credential Availability Check), §8.3 (Credential
// availability pre-check at delegation time), §10.1 (Horizontal Scaling)
package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

var errFlapOutage = errors.New("injected flapping store fault")

type flapCallKey struct{}

// flapLedger draws one fault decision per read from a seeded source and
// records, per call identifier, whether any of that call's reads failed.
type flapLedger struct {
	mu     sync.Mutex
	rng    *rand.Rand
	p      float64
	failed map[string]bool
	reads  map[string]int
	faults int
}

func (l *flapLedger) decide(ctx context.Context) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	fail := l.rng.Float64() < l.p
	if call, ok := ctx.Value(flapCallKey{}).(string); ok {
		l.reads[call]++
		if fail {
			l.failed[call] = true
		}
	}
	if fail {
		l.faults++
	}
	return fail
}

type flapTenants struct {
	tenantstore.Store
	l *flapLedger
}

func (f flapTenants) Get(ctx context.Context, id string) (tenantstore.Tenant, error) {
	if f.l.decide(ctx) {
		return tenantstore.Tenant{}, errFlapOutage
	}
	return f.Store.Get(ctx, id)
}

type flapRuntimes struct {
	runtimestore.Store
	l *flapLedger
}

func (f flapRuntimes) Get(ctx context.Context, name string) (runtimestore.Runtime, error) {
	if f.l.decide(ctx) {
		return runtimestore.Runtime{}, errFlapOutage
	}
	return f.Store.Get(ctx, name)
}

type flapSessions struct {
	sessionstore.Store
	l *flapLedger
}

func (f flapSessions) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	if f.l.decide(ctx) {
		return sessionstore.Session{}, errFlapOutage
	}
	return f.Store.Get(ctx, tenantID, id)
}

// spec: §4.9 (Pre-Claim Credential Availability Check), §8.3 (Credential
// availability pre-check at delegation time), §10.1 (Horizontal Scaling)
// diagnosis: under concurrent callers on two replicas and a randomly failing
// tenant, runtime, and session store, every availability check must fail
// with the retryable credential lookup error exactly when one of its own
// reads failed, and admit otherwise. A call that admits after a failed read
// means the resolution failed open, so the start, resume, and
// materialization paths that share it would run a session with no
// credential; a call answered CREDENTIAL_POOL_EXHAUSTED after a failed read
// means a transient fault was reported as a definite pool exhaustion.
func TestCredentialLookupFlappingStoresTwoReplicas_spec_4_9(t *testing.T) {
	seed := uint64(time.Now().UnixNano())
	t.Logf("seed %d", seed)
	ctx := context.Background()
	ledger := &flapLedger{
		rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), p: 0.3,
		failed: map[string]bool{}, reads: map[string]int{},
	}

	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{"anthropic_direct": {DefaultPool: "claude-prod"}},
	}}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	for _, name := range []string{"child-rt", "origin-rt"} {
		if err := runtimes.Create(ctx, runtimestore.Runtime{Name: name, SupportedProviders: []string{"anthropic_direct"}}); err != nil {
			t.Fatalf("create runtime: %v", err)
		}
	}
	pools := credentialpoolstore.NewMemory()
	if err := pools.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "claude-prod", Provider: "anthropic_direct", MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{{ID: "c1", SecretRef: "s1", Status: credentialpoolstore.CredentialActive}},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{ID: "origin-1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "origin-rt"}); err != nil {
		t.Fatalf("create origin: %v", err)
	}

	replicas := make([]*sessionserver.Server, 2)
	for i := range replicas {
		replicas[i] = sessionserver.New(flapSessions{Store: sessions, l: ledger}, sessionserver.Options{
			Tenants:          flapTenants{Store: tenants, l: ledger},
			Runtimes:         flapRuntimes{Store: runtimes, l: ledger},
			CredentialPools:  pools,
			CredentialRouter: credrouter.NewDefault(),
		})
		t.Cleanup(replicas[i].Close)
	}

	const workers, perWorker = 16, 60
	var mu sync.Mutex
	results := map[string]error{}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := fmt.Sprintf("call-%d-%d", w, i)
				q := sessionserver.DelegationCredentialQuery{TenantID: "acme", UserID: "alice@acme.com", ChildRuntimeRef: "child-rt"}
				if i%2 == 1 {
					q.CredentialOriginSessionID = "origin-1"
				}
				err := replicas[(w+i)%2].CheckDelegationCredentialAvailability(context.WithValue(ctx, flapCallKey{}, id), q)
				mu.Lock()
				results[id] = err
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	admitted, refused := 0, 0
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	for id, err := range results {
		if ledger.reads[id] == 0 {
			t.Errorf("%s made no store read", id)
			continue
		}
		var lookup *sessionserver.CredentialLookupError
		switch {
		case ledger.failed[id] && !errors.As(err, &lookup):
			t.Errorf("%s: a read failed but the check answered %v, want *CredentialLookupError (seed %d)", id, err, seed)
		case ledger.failed[id] && errors.Is(err, sessionserver.ErrDelegationCredentialUnavailable):
			t.Errorf("%s: a read failure was answered as pool exhaustion (seed %d)", id, seed)
		case !ledger.failed[id] && err != nil:
			t.Errorf("%s: every read succeeded but the check answered %v (seed %d)", id, err, seed)
		case err == nil:
			admitted++
		default:
			refused++
		}
	}
	t.Logf("calls %d, admitted %d, refused %d, injected faults %d", len(results), admitted, refused, ledger.faults)
	if admitted == 0 || refused == 0 {
		t.Errorf("admitted %d refused %d; the run must exercise both outcomes", admitted, refused)
	}
}
