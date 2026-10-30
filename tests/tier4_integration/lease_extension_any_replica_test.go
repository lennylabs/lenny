//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration coverage for the §8.6 store-backed extension state
// across two gateway replicas. Each replica runs its own leasecontrol
// Service over its own StoreBudgetSource, and both read one Postgres: the
// production session pgstore for the requesting, root, and parent rows, and
// the production denialpg store for the delegation_tree_budget row's denial
// and cumulative grants. No replica registers a session, so a session
// created on one replica is extended on the other, a denial written by one
// binds the other, and concurrent grants share one pool bounded by the
// effective max.
package tier4_integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol/denialpg"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// extensionReplica is one gateway replica's §8.6 extension surface.
type extensionReplica struct {
	src *leasecontrol.StoreBudgetSource
	svc *leasecontrol.Service
}

// newExtensionReplica builds a replica over the shared Postgres: auto
// mode, a 500k deployment token ceiling.
func newExtensionReplica(t *testing.T, pg *containers.Postgres) extensionReplica {
	t.Helper()
	src, err := leasecontrol.NewStoreBudgetSource(sessionpg.New(pg.Pool), denialpg.New(pg.Pool), leasecontrol.ExtensionConfig{
		DeploymentBudget:    500_000,
		DeploymentMaxBudget: 2_000_000,
		ApprovalMode:        leasecontrol.ApprovalModeAuto,
	})
	if err != nil {
		t.Fatalf("NewStoreBudgetSource: %v", err)
	}
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: src})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return extensionReplica{src: src, svc: svc}
}

// seedExtensionRoot creates a running root row with a 100k token lease.
func seedExtensionRoot(t *testing.T, sessions sessionstore.Store, tenant string) string {
	t.Helper()
	id := uuid.NewString()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: tenant, State: session.StateRunning, CreatedAt: time.Unix(1, 0).UTC(),
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 100_000},
	}); err != nil {
		t.Fatalf("seed root row: %v", err)
	}
	return id
}

func tokensReq(tenant, sessionID string, tokens int64) leasecontrol.ExtendRequest {
	return leasecontrol.ExtendRequest{TenantID: tenant, SessionID: sessionID, Requested: leasecontrol.Dimensions{Tokens: tokens}}
}

// spec: §8.6 (Extension state), §8.6 (Durability), §10.1.1
//
// diagnosis: a failure means the §8.6 extension state is still per replica.
// Either a session created on replica A had no extension state on replica
// B, a denial written on A did not bind B's first request, concurrent
// grants on A and B raised the tree's cumulative grant past the effective
// max less the base, a terminal session received a grant, or a derived
// session inherited its derive source's lease as a ceiling.
func TestLeaseExtensionAnyReplica_spec_8_6(t *testing.T) {
	t.Parallel()
	pg := sharedDatabase(t)
	tenant := "acme-ext-" + uuid.NewString()[:8]
	seedTenant(t, pg, tenant)
	sessions := sessionpg.New(pg.Pool)
	denials := denialpg.New(pg.Pool)
	a, b := newExtensionReplica(t, pg), newExtensionReplica(t, pg)
	ctx := context.Background()

	t.Run("created on A, extended on B", func(t *testing.T) {
		root := seedExtensionRoot(t, sessions, tenant)
		resp, err := b.svc.ExtendLease(ctx, tokensReq(tenant, root, 50_000))
		if err != nil {
			t.Fatalf("ExtendLease on B: %v", err)
		}
		if resp.Status != leasecontrol.StatusGranted || resp.Granted.Tokens != 50_000 {
			t.Fatalf("B response = %v granted %d, want GRANTED 50000", resp.Status, resp.Granted.Tokens)
		}
		got, err := denials.Grants(ctx, tenant, root)
		if err != nil || got.Tokens != 50_000 {
			t.Fatalf("ext_tokens = %+v, %v, want 50000", got, err)
		}
		tb, err := a.src.TreeBudget(ctx, tenant, root)
		if err != nil || tb.Current.Tokens != 150_000 {
			t.Fatalf("A reads current = %d, %v, want 150000", tb.Current.Tokens, err)
		}
	})

	t.Run("denial on A binds B's first request", func(t *testing.T) {
		root := seedExtensionRoot(t, sessions, tenant)
		if err := a.src.Deny(ctx, tenant, root, root); err != nil {
			t.Fatalf("Deny on A: %v", err)
		}
		resp, err := b.svc.ExtendLease(ctx, tokensReq(tenant, root, 10_000))
		if err != nil {
			t.Fatalf("ExtendLease on B: %v", err)
		}
		if resp.Status != leasecontrol.StatusRejected {
			t.Fatalf("B response = %v, want REJECTED", resp.Status)
		}
	})

	t.Run("concurrent grants share one pool", func(t *testing.T) {
		root := seedExtensionRoot(t, sessions, tenant)
		// Headroom is 400k; each of 2x4 requests asks for 150k.
		var wg sync.WaitGroup
		var mu sync.Mutex
		var granted int64
		var partial int
		for i := 0; i < 8; i++ {
			r := a
			if i%2 == 1 {
				r = b
			}
			wg.Add(1)
			go func(r extensionReplica) {
				defer wg.Done()
				resp, err := r.svc.ExtendLease(ctx, tokensReq(tenant, root, 150_000))
				if err != nil {
					t.Errorf("ExtendLease: %v", err)
					return
				}
				mu.Lock()
				granted += resp.Granted.Tokens
				if resp.Status == leasecontrol.StatusPartiallyGranted {
					partial++
				}
				mu.Unlock()
			}(r)
		}
		wg.Wait()
		got, err := denials.Grants(ctx, tenant, root)
		if err != nil {
			t.Fatalf("Grants: %v", err)
		}
		if got.Tokens != 400_000 || granted != 400_000 {
			t.Fatalf("ext_tokens = %d, granted sum = %d, want both 400000 (effective max less base)", got.Tokens, granted)
		}
		if partial != 1 {
			t.Errorf("partially granted responses = %d, want 1 (the grant that took the remaining headroom)", partial)
		}
	})

	t.Run("terminal session gets ErrSessionTerminal", func(t *testing.T) {
		root := seedExtensionRoot(t, sessions, tenant)
		if _, err := sessions.Update(ctx, tenant, root, func(s *sessionstore.Session) error {
			s.State = session.StateCompleted
			return nil
		}); err != nil {
			t.Fatalf("terminal write: %v", err)
		}
		if _, err := b.src.TreeBudget(ctx, tenant, root); !errors.Is(err, leasecontrol.ErrSessionTerminal) {
			t.Fatalf("TreeBudget err = %v, want ErrSessionTerminal", err)
		}
		if _, err := b.src.ApplyGrant(ctx, tenant, root, root, leasecontrol.Dimensions{Tokens: 1}); !errors.Is(err, leasecontrol.ErrSessionTerminal) {
			t.Fatalf("ApplyGrant err = %v, want ErrSessionTerminal", err)
		}
		if got, _ := denials.Grants(ctx, tenant, root); got.Tokens != 0 {
			t.Fatalf("terminal session received a grant: %d", got.Tokens)
		}
	})

	t.Run("derived session ignores its source lease", func(t *testing.T) {
		source := seedExtensionRoot(t, sessions, tenant)
		derived := uuid.NewString()
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: derived, TenantID: tenant, State: session.StateRunning, CreatedAt: time.Unix(2, 0).UTC(),
			ParentSessionID: source, RootSessionID: derived,
		}); err != nil {
			t.Fatalf("seed derived row: %v", err)
		}
		tb, err := b.src.TreeBudget(ctx, tenant, derived)
		if err != nil {
			t.Fatalf("TreeBudget(derived): %v", err)
		}
		if tb.RootSessionID != derived || tb.ParentCeiling != (leasecontrol.Dimensions{}) {
			t.Fatalf("derived view root %q ceiling %+v, want its own root and no parent ceiling", tb.RootSessionID, tb.ParentCeiling)
		}
	})
}
