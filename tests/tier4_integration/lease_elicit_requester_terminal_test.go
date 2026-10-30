//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration coverage for the §8.6 lease-extension elicitation
// when its requesting session ends while the elicitation is pending. The
// production Elicitor and the leasecontrol Service run over the
// production Postgres session store, interaction store, and denialpg
// store. The requesting session reaches a terminal state while the
// elicitor polls, and the elicitor dismisses the stored interaction with
// LEASE_EXTENSION_REQUESTER_TERMINAL, so the client can no longer answer it.
package tier4_integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol/denialpg"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol/leaseelicit"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/session/interactionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
)

// spec: §8.6 (Elicitation mode flow), §8.6 (Extension state), §9.2 (Elicitation chain)
//
// diagnosis: a failure means a lease-extension elicitation outlives its
// requesting session on the production stores. Either the elicitor does
// not re-read the requester from the session store while it polls, the
// dismissal is not written to the interaction store with the
// LEASE_EXTENSION_REQUESTER_TERMINAL reason, the extension request is not
// failed with ErrRequesterEnded, or a client can still answer the prompt.
func TestLeaseElicitRequesterTerminalDismisses_spec_8_6(t *testing.T) {
	t.Parallel()
	pg := sharedDatabase(t)
	tenant := "acme-elicit-" + uuid.NewString()[:8]
	seedTenant(t, pg, tenant)
	ctx := context.Background()
	sessions := sessionpg.New(pg.Pool)
	interactions := interactionpg.New(pg.Pool)

	root := uuid.NewString()
	const user = "alice@acme.com"
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: root, TenantID: tenant, UserID: user, State: session.StateRunning, CreatedAt: time.Now().UTC(),
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 100_000},
	}); err != nil {
		t.Fatalf("seed root row: %v", err)
	}

	src, err := leasecontrol.NewStoreBudgetSource(sessions, denialpg.New(pg.Pool), leasecontrol.ExtensionConfig{
		DeploymentBudget:    500_000,
		DeploymentMaxBudget: 2_000_000,
		ApprovalMode:        leasecontrol.ApprovalModeElicitation,
	})
	if err != nil {
		t.Fatalf("NewStoreBudgetSource: %v", err)
	}
	elicitationID := "lease-elicit-" + uuid.NewString()
	elicitor := leaseelicit.New(leaseelicit.Config{
		Sessions:     sessions,
		Interactions: interactions,
		Clock:        func() time.Time { return time.Now().UTC() },
		IDGen:        func() string { return elicitationID },
		Timeout:      30 * time.Second,
		Poll:         20 * time.Millisecond,
	})
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: src, Elicitor: elicitor})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := svc.ExtendLease(ctx, leasecontrol.ExtendRequest{
			TenantID: tenant, SessionID: root, Requested: leasecontrol.Dimensions{Tokens: 50_000},
		})
		done <- err
	}()

	waitInteractionPending(t, interactions, tenant, root)
	if _, err := sessions.Update(ctx, tenant, root, func(s *sessionstore.Session) error {
		s.State = session.StateCancelled
		return nil
	}); err != nil {
		t.Fatalf("terminal write: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, leasecontrol.ErrRequesterEnded) {
			t.Fatalf("ExtendLease err = %v, want ErrRequesterEnded", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ExtendLease did not return after the requester ended")
	}

	stored, err := interactions.Get(ctx, tenant, root, user, elicitationID)
	if err != nil {
		t.Fatalf("Get interaction: %v", err)
	}
	if stored.Phase != interactionstore.PhaseDismissed || stored.Reason != leaseelicit.ReasonRequesterTerminal {
		t.Fatalf("stored interaction = %s/%q, want %s/%s",
			stored.Phase, stored.Reason, interactionstore.PhaseDismissed, leaseelicit.ReasonRequesterTerminal)
	}

	// The client's answer to the dismissed prompt is refused.
	_, err = mcptools.ResolveElicitation(ctx, interactions, tenant, root, user, elicitationID,
		interactionstore.PhaseResponded, map[string]any{"action": "accept"}, "")
	if !errors.Is(err, interactionstore.ErrAlreadyResolved) {
		t.Fatalf("client respond err = %v, want ErrAlreadyResolved", err)
	}
	if got, err := denialpg.New(pg.Pool).Grants(ctx, tenant, root); err != nil || got.Tokens != 0 {
		t.Fatalf("ext_tokens = %d, %v after a dismissed elicitation, want 0", got.Tokens, err)
	}
}

// waitInteractionPending waits for the requesting session's elicitation
// to be stored pending.
func waitInteractionPending(t *testing.T, store interactionstore.Store, tenant, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := store.ListPending(context.Background(), tenant, sessionID)
		if err == nil && len(pending) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("lease elicitation was never stored pending")
}
