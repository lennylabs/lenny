// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the race between a respond to a pending elicitation
// and the expiry of the elicitations a session's pod raised, on the Postgres
// interaction store. The expiry runs when the session leaves its pod; a
// respond arriving at the same moment either lands first, and the expiry
// leaves the answered row, or arrives after the delete and finds nothing.
//
// spec: §9.2 (Elicitation Chain)

package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/session/interactionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// elicitationExpiryRaceRuns is the number of independent respond-versus-expiry
// races the test runs, each on its own elicitation.
const elicitationExpiryRaceRuns = 100

// TestElicitationRespondRacingExpiryHasOneOutcome_spec_9_2 races a respond
// (Resolve to responded) against ExpireRaisedBy for the raising session, once
// per run on a fresh pending elicitation, released from one barrier.
//
// spec: 9.2 (Elicitation Chain)
// diagnosis: a failure means the respond and the expiry both took effect (the
// respond succeeded and the expiry counted the row, or the row survives
// pending), or neither did. Either the delete does not exclude a row whose
// phase moved off pending under it, or the respond does not lock the row it
// resolves, so a runtime can receive an answer to an elicitation the platform
// reported expired, or a client's answer is lost without a not-found reply.
func TestElicitationRespondRacingExpiryHasOneOutcome_spec_9_2(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	tenant := "t-elicit-race"
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, tenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	store := interactionpg.New(pg.Pool)

	respondWins, expiryWins := 0, 0
	for i := range elicitationExpiryRaceRuns {
		session := fmt.Sprintf("sess-%d", i)
		id := fmt.Sprintf("elicit-%d", i)
		if err := store.Put(ctx, interactionstore.Interaction{
			ID: id, Kind: interactionstore.KindElicitation, SessionID: session, TenantID: tenant, UserID: "alice",
			Detail: map[string]any{interactionstore.DetailOriginPod: session},
		}); err != nil {
			t.Fatalf("run %d: Put: %v", i, err)
		}

		var (
			start      = make(chan struct{})
			wg         sync.WaitGroup
			resolveErr error
			expired    int
			expireErr  error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, resolveErr = store.Resolve(ctx, tenant, session, "alice", id, func(in *interactionstore.Interaction) error {
				in.Phase = interactionstore.PhaseResponded
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			expired, expireErr = store.ExpireRaisedBy(ctx, tenant, session)
		}()
		close(start)
		wg.Wait()

		if expireErr != nil {
			t.Fatalf("run %d: ExpireRaisedBy: %v", i, expireErr)
		}
		got, getErr := store.Get(ctx, tenant, session, "alice", id)
		switch {
		case resolveErr == nil && expired == 0:
			respondWins++
			if getErr != nil || got.Phase != interactionstore.PhaseResponded {
				t.Fatalf("run %d: the respond won but the row reads (%v, %v), want responded", i, got.Phase, getErr)
			}
		case errors.Is(resolveErr, interactionstore.ErrNotFound) && expired == 1:
			expiryWins++
			if !errors.Is(getErr, interactionstore.ErrNotFound) {
				t.Fatalf("run %d: the expiry won but Get = %v, want ErrNotFound", i, getErr)
			}
		default:
			t.Fatalf("run %d: respond = %v and expiry counted %d; want exactly one of the two to take effect", i, resolveErr, expired)
		}
	}
	t.Logf("respond won %d runs, expiry won %d runs", respondWins, expiryWins)
}
