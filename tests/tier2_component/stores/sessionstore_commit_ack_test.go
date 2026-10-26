//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §12.3 lost commit acknowledgement on a session
// write. The Postgres session store under test runs on a pool whose
// connection is cut at the moment the transaction's COMMIT goes on the
// wire, so the server commits or rolls back while the store sees the
// commit fail. The store must then classify the write by the identifier it
// stored, read back from the primary.
package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/commitfault"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// writeIDReadBack is the statement text of the store's primary read-back,
// which the injector's write log is searched for.
const writeIDReadBack = "SELECT last_write_id = $3::uuid"

// commitAckFixture is one session row behind a store whose commits the
// injector can cut, plus a store on the plain pool for setup and checks.
type commitAckFixture struct {
	pg     *containers.Postgres
	faulty *pgstore.Store
	plain  *pgstore.Store
	inj    *commitfault.Injector
	tenant string
	id     string
}

// newCommitAckFixture starts Postgres, seeds one running session, and
// builds the faulty store over a single-connection injector pool.
func newCommitAckFixture(t *testing.T) commitAckFixture {
	t.Helper()
	plain, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	id := newUUID(t)
	if err := plain.Create(ctx, sessionstore.Session{
		ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	pool, inj := commitfault.NewPool(t, pg.DSN)
	return commitAckFixture{pg: pg, faulty: pgstore.New(pool), plain: plain, inj: inj, tenant: tenant, id: id}
}

// setReason is a mutate that writes reason into FailureReason.
func setReason(reason string) func(*sessionstore.Session) error {
	return func(s *sessionstore.Session) error {
		s.FailureReason = reason
		return nil
	}
}

// primaryReason reads the row's FailureReason through the plain store.
func (f commitAckFixture) primaryReason(t *testing.T) string {
	t.Helper()
	got, err := f.plain.GetPrimary(context.Background(), f.tenant, f.id)
	if err != nil {
		t.Fatalf("GetPrimary: %v", err)
	}
	return got.FailureReason
}

// spec: 12.3 (Postgres HA Requirements)
// diagnosis: the session store misclassified a session write whose commit
// acknowledgement was lost. Either a committed write returned an error
// (its writer then skips its post-commit steps, so a committed terminal
// write runs no terminal pipeline), an aborted write returned success, the
// read-back ran on the caller's cancelled context, or a failed or slow
// read-back reported the write applied. Check Store.Update's write
// identifier and writeApplied in pkg/gateway/session/sessionstore/pgstore.
func TestSessionStoreLostCommitAckClassifiedByWriteID_spec_12_3(t *testing.T) {
	t.Parallel()

	t.Run("committed write returns the written row", func(t *testing.T) {
		t.Parallel()
		f := newCommitAckFixture(t)
		f.inj.Arm(commitfault.CommitThenCut)
		got, err := f.faulty.Update(context.Background(), f.tenant, f.id, setReason("committed"))
		if err != nil {
			t.Fatalf("Update after a committed write with a lost acknowledgement = %v, want nil", err)
		}
		if !f.inj.Fired() {
			t.Fatal("the commit fault never fired, so the case did not exercise a lost acknowledgement")
		}
		if got.FailureReason != "committed" {
			t.Errorf("returned FailureReason = %q, want the written row", got.FailureReason)
		}
		if r := f.primaryReason(t); r != "committed" {
			t.Errorf("primary FailureReason = %q, want the committed write", r)
		}
	})

	t.Run("committed write survives a cancelled caller context", func(t *testing.T) {
		t.Parallel()
		f := newCommitAckFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// The caller's context ends as the read-back dials, which is when
		// a request cancelled during the commit reaches the check.
		f.inj.OnRedial(func(context.Context) error {
			cancel()
			return nil
		})
		f.inj.Arm(commitfault.CommitThenCut)
		if _, err := f.faulty.Update(ctx, f.tenant, f.id, setReason("detached")); err != nil {
			t.Fatalf("Update = %v, want nil: the read-back must not run on the caller's context", err)
		}
	})

	t.Run("aborted write returns the commit error", func(t *testing.T) {
		t.Parallel()
		f := newCommitAckFixture(t)
		f.inj.Arm(commitfault.CutBeforeCommit)
		_, err := f.faulty.Update(context.Background(), f.tenant, f.id, setReason("aborted"))
		if err == nil {
			t.Fatal("Update after an aborted commit = nil, want the commit error")
		}
		if errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("Update error = %v, want the commit error rather than ErrNotFound", err)
		}
		if !f.inj.Saw(writeIDReadBack) {
			t.Error("the store did not read the write identifier back after the commit error")
		}
		if r := f.primaryReason(t); r != "" {
			t.Errorf("primary FailureReason = %q, want the aborted write absent", r)
		}
	})

	t.Run("read-back failure returns the commit error", func(t *testing.T) {
		t.Parallel()
		f := newCommitAckFixture(t)
		f.inj.OnRedial(func(context.Context) error { return errors.New("primary unreachable") })
		f.inj.Arm(commitfault.CommitThenCut)
		if _, err := f.faulty.Update(context.Background(), f.tenant, f.id, setReason("unread")); err == nil {
			t.Fatal("Update whose read-back failed = nil, want the commit error")
		}
		// The write committed; §12.3 accepts treating it as not applied.
		if r := f.primaryReason(t); r != "unread" {
			t.Errorf("primary FailureReason = %q, want the committed write", r)
		}
	})

	t.Run("read-back timeout returns the commit error", func(t *testing.T) {
		t.Parallel()
		f := newCommitAckFixture(t)
		f.inj.OnRedial(func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		f.inj.Arm(commitfault.CommitThenCut)
		start := time.Now()
		_, err := f.faulty.Update(context.Background(), f.tenant, f.id, setReason("slow"))
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("Update whose read-back timed out = nil, want the commit error")
		}
		if elapsed < sessionstore.DefaultPrimaryReadTimeout {
			t.Errorf("Update returned after %v, before the %v read-back bound", elapsed, sessionstore.DefaultPrimaryReadTimeout)
		}
		if elapsed > 3*sessionstore.DefaultPrimaryReadTimeout {
			t.Errorf("Update returned after %v, want the read-back cut at %v", elapsed, sessionstore.DefaultPrimaryReadTimeout)
		}
	})

	t.Run("exec error makes no read-back", func(t *testing.T) {
		t.Parallel()
		f := newCommitAckFixture(t)
		f.inj.Arm(commitfault.CommitThenCut)
		_, err := f.faulty.Update(context.Background(), f.tenant, f.id, func(s *sessionstore.Session) error {
			s.TreeVisibility = session.TreeVisibility("not-a-visibility")
			return nil
		})
		if err == nil {
			t.Fatal("Update violating the tree_visibility CHECK = nil, want the Exec error")
		}
		if f.inj.Fired() {
			t.Error("a COMMIT was sent after the Exec error")
		}
		if f.inj.Saw(writeIDReadBack) {
			t.Error("the store read the write identifier back after an Exec error")
		}
	})

	t.Run("mutate error makes no read-back", func(t *testing.T) {
		t.Parallel()
		f := newCommitAckFixture(t)
		boom := errors.New("mutate refused")
		_, err := f.faulty.Update(context.Background(), f.tenant, f.id, func(*sessionstore.Session) error { return boom })
		if !errors.Is(err, boom) {
			t.Fatalf("Update = %v, want the mutate error unchanged", err)
		}
		if f.inj.Saw(writeIDReadBack) {
			t.Error("the store read the write identifier back after a mutate error")
		}
	})

	t.Run("competing writer with the same updated_at is not this write", func(t *testing.T) {
		t.Parallel()
		f := newCommitAckFixture(t)
		ctx := context.Background()
		// Both writers' clocks lag the stored updated_at, so each stamps
		// the stored value plus 1µs.
		prev := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
		if _, err := f.pg.Pool.Exec(ctx, `UPDATE sessions SET updated_at = $1 WHERE id = $2::uuid`, prev, f.id); err != nil {
			t.Fatalf("seed updated_at: %v", err)
		}
		var competing sessionstore.Session
		var competingErr error
		f.inj.OnRedial(func(context.Context) error {
			competing, competingErr = f.plain.Update(ctx, f.tenant, f.id, setReason("competitor"))
			return competingErr
		})
		f.inj.Arm(commitfault.CutBeforeCommit)
		_, err := f.faulty.Update(ctx, f.tenant, f.id, setReason("rolled-back"))
		if competingErr != nil {
			t.Fatalf("competing Update: %v", competingErr)
		}
		if want := prev.Add(time.Microsecond); !competing.UpdatedAt.Equal(want) {
			t.Fatalf("competing UpdatedAt = %v, want %v so both writes stamp the same instant", competing.UpdatedAt, want)
		}
		if err == nil {
			t.Fatal("Update rolled back behind a competing writer with the same updated_at = nil, want the commit error")
		}
		if r := f.primaryReason(t); r != "competitor" {
			t.Errorf("primary FailureReason = %q, want the competing write", r)
		}
	})
}
