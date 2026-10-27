// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos: §12.3 session writes whose commit acknowledgement is lost.
//
// The Postgres session store runs on a pool whose connection is cut as the
// transaction's COMMIT goes on the wire, with the server either committing
// or rolling back, and the primary endpoint then refused, slow, or
// restored when the store reads the row back. The store must report a write
// applied exactly when the primary holds that write, except for the
// accepted residual §12.3 names: a committed write whose read-back cannot
// complete before its timeout is reported not applied.

package tier8_chaos_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/commitfault"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/randctl"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const commitAckTenant = "chaos-commit-ack"

// commitAckChaos is one Postgres with a plain store and a store whose
// commits the injector cuts.
type commitAckChaos struct {
	plain  *sessionpg.Store
	faulty *sessionpg.Store
	inj    *commitfault.Injector
}

func newCommitAckChaos(t *testing.T) commitAckChaos {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedChaosTenant(t, pg, commitAckTenant)
	pool, inj := commitfault.NewPool(t, pg.DSN)
	return commitAckChaos{plain: sessionpg.New(pg.Pool), faulty: sessionpg.New(pool), inj: inj}
}

// seedRunning creates one running session and returns its id.
func (c commitAckChaos) seedRunning(t *testing.T) string {
	t.Helper()
	id := uuid.NewString()
	if err := c.plain.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: commitAckTenant, State: session.StateRunning, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return id
}

// primaryReason reads the row's FailureReason from the primary.
func (c commitAckChaos) primaryReason(t *testing.T, id string) string {
	t.Helper()
	got, err := c.plain.GetPrimary(context.Background(), commitAckTenant, id)
	if err != nil {
		t.Fatalf("GetPrimary: %v", err)
	}
	return got.FailureReason
}

func writeReason(reason string) func(*sessionstore.Session) error {
	return func(s *sessionstore.Session) error {
		s.FailureReason = reason
		return nil
	}
}

// spec: 12.3 (Postgres HA Requirements)
// diagnosis: a session write committed while the primary endpoint dropped
// at COMMIT and came back within the read-back bound, and the store
// reported it not applied, or a terminal write whose endpoint stayed away
// past the bound was reported applied. A writer that sees the first error
// skips the post-commit steps of a committed write, so a committed terminal
// write runs no terminal pipeline. Check Store.Update's read-back in
// pkg/gateway/session/sessionstore/pgstore.
func TestSessionWriteCommitAckLostAcrossPrimaryOutage_spec_12_3(t *testing.T) {
	c := newCommitAckChaos(t)
	ctx := context.Background()

	t.Run("endpoint restored inside the read-back bound", func(t *testing.T) {
		id := c.seedRunning(t)
		outage := time.Second
		c.inj.OnRedial(func(ctx context.Context) error {
			select {
			case <-time.After(outage):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		c.inj.Arm(commitfault.CommitThenCut)
		got, err := c.faulty.Update(ctx, commitAckTenant, id, func(s *sessionstore.Session) error {
			s.State = session.StateCompleted
			s.FailureReason = "restored"
			return nil
		})
		if err != nil {
			t.Fatalf("terminal Update across a %v outage at COMMIT = %v, want the written row", outage, err)
		}
		if got.State != session.StateCompleted {
			t.Errorf("returned state = %q, want completed", got.State)
		}
		if r := c.primaryReason(t, id); r != "restored" {
			t.Errorf("primary FailureReason = %q, want the committed write", r)
		}
	})

	t.Run("endpoint gone past the read-back bound", func(t *testing.T) {
		id := c.seedRunning(t)
		c.inj.OnRedial(func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		c.inj.Arm(commitfault.CommitThenCut)
		if _, err := c.faulty.Update(ctx, commitAckTenant, id, writeReason("unconfirmed")); err == nil {
			t.Fatal("Update whose read-back outlasted its bound = nil, want the commit error")
		}
		// The accepted residual: the write committed and is reported not
		// applied.
		if r := c.primaryReason(t, id); r != "unconfirmed" {
			t.Errorf("primary FailureReason = %q, want the committed write", r)
		}
	})
}

// spec: 12.3 (Postgres HA Requirements)
// diagnosis: across randomized commit faults and read-back conditions, the
// session store reported a write applied that the primary does not hold,
// or reported a write not applied that committed while its read-back could
// complete. Check Store.Update's write identifier and writeApplied in
// pkg/gateway/session/sessionstore/pgstore.
func TestSessionWriteCommitAckRandomizedFaults_spec_12_3(t *testing.T) {
	c := newCommitAckChaos(t)
	r := randctl.New(t)
	ctx := context.Background()
	modes := []commitfault.Mode{commitfault.Pass, commitfault.CommitThenCut, commitfault.CutBeforeCommit}
	refused := errors.New("primary refused")
	const rounds = 40
	for i := 0; i < rounds; i++ {
		id := c.seedRunning(t)
		mode := modes[r.IntN(len(modes))]
		refuse := r.IntN(4) == 0
		delay := time.Duration(r.IntN(200)) * time.Millisecond
		c.inj.OnRedial(func(context.Context) error {
			if refuse {
				return refused
			}
			time.Sleep(delay)
			return nil
		})
		c.inj.Arm(mode)
		reason := fmt.Sprintf("round-%d", i)
		_, err := c.faulty.Update(ctx, commitAckTenant, id, writeReason(reason))
		held := c.primaryReason(t, id) == reason
		desc := fmt.Sprintf("round %d mode=%d refuse=%v delay=%v", i, mode, refuse, delay)
		switch {
		case err == nil && !held:
			t.Errorf("%s: Update reported the write applied but the primary does not hold it", desc)
		case mode == commitfault.Pass && err != nil:
			t.Errorf("%s: Update with no fault = %v, want nil", desc, err)
		case mode == commitfault.CommitThenCut && !refuse && err != nil:
			t.Errorf("%s: committed write with a reachable primary = %v, want nil", desc, err)
		case mode == commitfault.CutBeforeCommit && err == nil:
			t.Errorf("%s: rolled-back write reported applied", desc)
		}
	}
}
