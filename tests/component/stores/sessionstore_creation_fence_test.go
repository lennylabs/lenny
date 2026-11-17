//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// fenceEnv is a migrated Postgres with the session, tenant, and user stores
// over one pool, as cmd/lenny-gateway builds them, one tenant, and the
// users alice and bob.
type fenceEnv struct {
	pg       *containers.Postgres
	sessions *pgstore.Store
	tenants  *tenantpg.Store
	users    *userpg.Store
	tenant   string
}

func newFenceEnv(t *testing.T, opts ...pgstore.Option) *fenceEnv {
	t.Helper()
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	e := &fenceEnv{pg: pg, sessions: pgstore.New(pg.Pool, opts...), tenants: tenantpg.New(pg.Pool), users: userpg.New(pg.Pool)}
	e.tenant = freshTenant(t, ctx, pg)
	e.addUsers(t, "alice", "bob")
	return e
}

// addTenant adds another tenant with alice and bob, so one container serves
// several independent cases.
func (e *fenceEnv) addTenant(t *testing.T) string {
	t.Helper()
	e.tenant = freshTenant(t, context.Background(), e.pg)
	e.addUsers(t, "alice", "bob")
	return e.tenant
}

func (e *fenceEnv) addUsers(t *testing.T, subjects ...string) {
	t.Helper()
	for _, sub := range subjects {
		if err := e.users.Create(context.Background(), userstore.User{TenantID: e.tenant, Subject: sub}); err != nil {
			t.Fatalf("seed user %s: %v", sub, err)
		}
	}
}

func (e *fenceEnv) row(owner string) sessionstore.Session {
	return sessionstore.Session{ID: uuid.NewString(), TenantID: e.tenant, UserID: owner, RuntimeRef: "echo", State: session.StateCreated}
}

func (e *fenceEnv) setUser(t *testing.T, subject string, mut func(*userstore.User)) {
	t.Helper()
	if _, err := e.users.Update(context.Background(), e.tenant, subject, func(u *userstore.User) error {
		mut(u)
		return nil
	}); err != nil {
		t.Fatalf("set user marker: %v", err)
	}
}

func (e *fenceEnv) setTenant(t *testing.T, mut func(*tenantstore.Tenant)) {
	t.Helper()
	if _, err := e.tenants.Update(context.Background(), e.tenant, func(tn *tenantstore.Tenant) error {
		mut(tn)
		return nil
	}); err != nil {
		t.Fatalf("set tenant marker: %v", err)
	}
}

func (e *fenceEnv) rowCount(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := e.pg.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE id = $1::uuid`, id).Scan(&n); err != nil {
		t.Fatalf("count session %s: %v", id, err)
	}
	return n
}

func (e *fenceEnv) reservationCount(t *testing.T, owner string) int {
	t.Helper()
	var n int
	if err := e.pg.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM session_reservations WHERE tenant_id = $1 AND user_id = $2`, e.tenant, owner).Scan(&n); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	return n
}

// pgMarkerCase sets one lifecycle marker through the production store
// method and names the code the fence answers.
type pgMarkerCase struct {
	name string
	set  func(t *testing.T, e *fenceEnv)
	code string
}

func pgMarkerCases() []pgMarkerCase {
	return []pgMarkerCase{
		{"soft-disabled owner", func(t *testing.T, e *fenceEnv) {
			e.setUser(t, "alice", func(u *userstore.User) { u.Disabled = true })
		}, sessionstore.RefusalUserInvalidated},
		{"hard-disabled owner", func(t *testing.T, e *fenceEnv) {
			if err := e.users.SoftDelete(context.Background(), e.tenant, "alice", time.Now()); err != nil {
				t.Fatalf("SoftDelete user: %v", err)
			}
		}, sessionstore.RefusalUserInvalidated},
		{"restricted owner", func(t *testing.T, e *fenceEnv) {
			e.setUser(t, "alice", func(u *userstore.User) { u.ProcessingRestricted, u.ErasureJobID = true, "job-1" })
		}, sessionstore.RefusalErasureInProgress},
		{"suspended tenant", func(t *testing.T, e *fenceEnv) {
			e.setTenant(t, func(tn *tenantstore.Tenant) { tn.Suspended = true })
		}, sessionstore.RefusalTenantSuspended},
		{"disabling tenant", func(t *testing.T, e *fenceEnv) {
			e.setTenant(t, func(tn *tenantstore.Tenant) { tn.State = tenantstore.TenantStateDisabling })
		}, sessionstore.RefusalTenantNotActive},
		{"deleted tenant", func(t *testing.T, e *fenceEnv) {
			if err := e.tenants.SoftDelete(context.Background(), e.tenant, time.Now()); err != nil {
				t.Fatalf("SoftDelete tenant: %v", err)
			}
		}, sessionstore.RefusalTenantNotActive},
	}
}

// requirePGRefusal fails the test unless err is a *CreationRefusal with code.
func requirePGRefusal(t *testing.T, err error, code string) {
	t.Helper()
	_ = pgRefusalOf(t, err, code)
}

// pgRefusalOf is requirePGRefusal returning the refusal, for cases that check its details.
func pgRefusalOf(t *testing.T, err error, code string) *sessionstore.CreationRefusal {
	t.Helper()
	var ref *sessionstore.CreationRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("err = %v, want *CreationRefusal %s", err, code)
	}
	if ref.Code != code {
		t.Fatalf("refusal code = %s, want %s", ref.Code, code)
	}
	return ref
}

// awaitLockWaiter polls pg_stat_activity until another backend of this
// test's database waits on a lock, failing when done fires first (the
// concurrent call did not wait) or the deadline passes.
func awaitLockWaiter[T any](t *testing.T, ctx context.Context, pg *containers.Postgres, done <-chan T) {
	t.Helper()
	const q = `SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid() AND wait_event_type = 'Lock'`
	deadline := time.Now().Add(rowLockWaitDeadline)
	for {
		var waiting int
		if err := pg.Pool.QueryRow(ctx, q).Scan(&waiting); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			return
		}
		select {
		case r := <-done:
			t.Fatalf("the concurrent call returned without waiting on a lock: %+v", r)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no backend waited on a lock within %s", rowLockWaitDeadline)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSessionStoreCreationFenceRefusesInTransaction_spec_12_8 pins the
// creation fence inside pgstore.Create against real Postgres: a refusal for
// each marker set through the production marker writers with nothing
// inserted, a fail-closed insert when the user read errors, a refused
// derive-failure row created terminal, and an admitted delegated child of a
// soft-disabled owner.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 11.4 (User Invalidation)
// diagnosis: a failure means pgstore.Create does not read the tenant row and
// the owner's user row inside its insert transaction, applies the refusal
// rules in the wrong order or with the wrong delegated-child classification,
// or admits the insert when the marker read fails, so a lifecycle operation's
// fenced scope can gain new sessions.
func TestSessionStoreCreationFenceRefusesInTransaction_spec_12_8(t *testing.T) {
	ctx := context.Background()
	e := newFenceEnv(t)

	for _, mc := range pgMarkerCases() {
		t.Run(mc.name, func(t *testing.T) {
			e.addTenant(t)
			mc.set(t, e)
			row := e.row("alice")
			requirePGRefusal(t, e.sessions.Create(ctx, row), mc.code)
			if n := e.rowCount(t, row.ID); n != 0 {
				t.Fatalf("refused insert left %d rows", n)
			}
		})
	}

	t.Run("restricted owner details name the user and the job", func(t *testing.T) {
		e.addTenant(t)
		e.setUser(t, "alice", func(u *userstore.User) { u.ProcessingRestricted, u.ErasureJobID = true, "job-7" })
		ref := pgRefusalOf(t, e.sessions.Create(ctx, e.row("alice")), sessionstore.RefusalErasureInProgress)
		if ref.Details["userId"] != "alice" || ref.Details["jobId"] != "job-7" {
			t.Fatalf("details = %v", ref.Details)
		}
	})

	t.Run("derive-failure row created terminal is refused", func(t *testing.T) {
		e.addTenant(t)
		e.setUser(t, "alice", func(u *userstore.User) { u.ProcessingRestricted = true })
		row := e.row("alice")
		row.State, row.FailureClass, row.FailureReason = session.StateFailed, session.FailureClassDeriveFailure, "DERIVE_SNAPSHOT_UNAVAILABLE"
		requirePGRefusal(t, e.sessions.Create(ctx, row), sessionstore.RefusalErasureInProgress)
		if n := e.rowCount(t, row.ID); n != 0 {
			t.Fatalf("refused derive-failure row left %d rows", n)
		}
	})

	t.Run("delegated child of a soft-disabled owner is admitted and a derive is not", func(t *testing.T) {
		e.addTenant(t)
		parent := e.row("alice")
		if err := e.sessions.Create(ctx, parent); err != nil {
			t.Fatalf("seed parent: %v", err)
		}
		e.setUser(t, "alice", func(u *userstore.User) { u.Disabled = true })
		derived := e.row("alice")
		derived.ParentSessionID = parent.ID
		requirePGRefusal(t, e.sessions.Create(ctx, derived), sessionstore.RefusalUserInvalidated)
		child := e.row("alice")
		child.ParentSessionID, child.DelegationDepth = parent.ID, 1
		if err := e.sessions.Create(ctx, child); err != nil {
			t.Fatalf("delegated child: %v, want admit", err)
		}
	})

	t.Run("owner with no registry row and ownerless row are admitted", func(t *testing.T) {
		e.addTenant(t)
		for _, owner := range []string{"", "carol"} {
			if err := e.sessions.Create(ctx, e.row(owner)); err != nil {
				t.Fatalf("Create owner %q: %v", owner, err)
			}
		}
	})

	t.Run("user read error fails the insert closed", func(t *testing.T) {
		e.addTenant(t)
		// A role with every grant the insert needs except users makes the
		// fence's user read fail with a permission error.
		role := "fence_no_users_" + newUUID(t)[:8]
		for _, stmt := range []string{
			"CREATE ROLE " + role + " NOLOGIN",
			"GRANT SELECT, INSERT, UPDATE, DELETE ON tenants, sessions, session_reservations TO " + role,
		} {
			if _, err := e.pg.Pool.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		cfg, err := pgxpool.ParseConfig(e.pg.DSN)
		if err != nil {
			t.Fatalf("parse DSN: %v", err)
		}
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET ROLE "+role)
			return err
		}
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("restricted pool: %v", err)
		}
		t.Cleanup(pool.Close)
		restricted := pgstore.New(pool)

		row := e.row("alice")
		err = restricted.Create(ctx, row)
		var ref *sessionstore.CreationRefusal
		if err == nil || errors.As(err, &ref) {
			t.Fatalf("Create with an unreadable user row = %v, want a read error", err)
		}
		if n := e.rowCount(t, row.ID); n != 0 {
			t.Fatalf("insert after a failed marker read left %d rows", n)
		}
		if err := restricted.Create(ctx, e.row("")); err != nil {
			t.Fatalf("ownerless Create on the same role: %v, want admit (only the user read fails)", err)
		}
	})
}

// TestSessionStoreCreationFenceSerializesWithMarkerWriters_spec_12_8 pins
// the lock conflict of the fence's FOR SHARE reads. A tenant Update or a
// user Update holding FOR UPDATE blocks Create until it commits, and the
// create then observes the new marker and refuses. A Create holding its
// FOR SHARE locks blocks userstore.SoftDelete and tenantstore.SoftDelete,
// whose plain UPDATE takes FOR NO KEY UPDATE; these two cases distinguish
// FOR SHARE from FOR KEY SHARE, under which SoftDelete would not wait and
// the insert could commit after the marker.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
// diagnosis: a failure means the fence's marker reads do not conflict with a
// marker writer: either they run outside the insert transaction, without a
// row lock, or under FOR KEY SHARE, so a create can commit into a scope whose
// marker committed first and escape the drain's listing.
func TestSessionStoreCreationFenceSerializesWithMarkerWriters_spec_12_8(t *testing.T) {
	ctx := context.Background()
	var gate *heldCopier
	e := newFenceEnv(t, pgstore.WithTranscriptCopier(func(context.Context, pgx.Tx, string, string, string) error {
		return gate.wait()
	}))

	for _, w := range []struct {
		name  string
		write func(e *fenceEnv, entered chan<- struct{}, hold <-chan struct{}) error
		code  string
	}{
		{"tenant Update", func(e *fenceEnv, entered chan<- struct{}, hold <-chan struct{}) error {
			_, err := e.tenants.Update(ctx, e.tenant, func(tn *tenantstore.Tenant) error {
				close(entered)
				<-hold
				tn.Suspended = true
				return nil
			})
			return err
		}, sessionstore.RefusalTenantSuspended},
		{"user Update", func(e *fenceEnv, entered chan<- struct{}, hold <-chan struct{}) error {
			_, err := e.users.Update(ctx, e.tenant, "alice", func(u *userstore.User) error {
				close(entered)
				<-hold
				u.ProcessingRestricted = true
				return nil
			})
			return err
		}, sessionstore.RefusalErasureInProgress},
	} {
		t.Run(w.name+" blocks Create, which then refuses", func(t *testing.T) {
			e.addTenant(t)
			hold, entered := make(chan struct{}), make(chan struct{})
			writeDone := make(chan error, 1)
			go func() { writeDone <- w.write(e, entered, hold) }()
			<-entered
			row := e.row("alice")
			createDone := make(chan error, 1)
			go func() { createDone <- e.sessions.Create(ctx, row) }()
			awaitLockWaiter(t, ctx, e.pg, createDone)
			close(hold)
			if err := awaitUpdateResult(t, w.name, writeDone); err != nil {
				t.Fatalf("marker write: %v", err)
			}
			requirePGRefusal(t, awaitUpdateResult(t, "Create", createDone), w.code)
			if n := e.rowCount(t, row.ID); n != 0 {
				t.Fatalf("refused insert left %d rows", n)
			}
		})
	}

	for _, sd := range []struct {
		name   string
		delete func(e *fenceEnv) error
		code   string
	}{
		{"userstore.SoftDelete", func(e *fenceEnv) error {
			return e.users.SoftDelete(ctx, e.tenant, "alice", time.Now())
		}, sessionstore.RefusalUserInvalidated},
		{"tenantstore.SoftDelete", func(e *fenceEnv) error {
			return e.tenants.SoftDelete(ctx, e.tenant, time.Now())
		}, sessionstore.RefusalTenantNotActive},
	} {
		t.Run("an in-flight Create blocks "+sd.name, func(t *testing.T) {
			e.addTenant(t)
			src := e.row("alice")
			if err := e.sessions.Create(ctx, src); err != nil {
				t.Fatalf("seed source: %v", err)
			}
			gate = newHeldCopier()
			// A failure before the release must not leave the insert
			// transaction blocked, or the pool's cleanup waits forever.
			t.Cleanup(gate.release)
			row := e.row("alice")
			// CreateWithTranscript consumes a reservation, so the ID is
			// reserved first, as the replay handler reserves it.
			if err := e.sessions.Reserve(ctx, e.tenant, row.UserID, row.ID, false); err != nil {
				t.Fatalf("reserve replay ID: %v", err)
			}
			createDone := make(chan error, 1)
			go func() { createDone <- e.sessions.CreateWithTranscript(ctx, row, src.ID) }()
			<-gate.entered // the fence holds its FOR SHARE locks in the open insert transaction
			deleteDone := make(chan error, 1)
			go func() { deleteDone <- sd.delete(e) }()
			awaitLockWaiter(t, ctx, e.pg, deleteDone)
			gate.release()
			if err := awaitUpdateResult(t, "Create", createDone); err != nil {
				t.Fatalf("in-flight Create: %v", err)
			}
			if err := awaitUpdateResult(t, sd.name, deleteDone); err != nil {
				t.Fatalf("%s: %v", sd.name, err)
			}
			if n := e.rowCount(t, row.ID); n != 1 {
				t.Fatalf("the create that committed first left %d rows, want 1", n)
			}
			requirePGRefusal(t, e.sessions.Create(ctx, e.row("alice")), sd.code)
		})
	}
}

// heldCopier is a transcript copier that blocks inside the insert
// transaction until released, holding the fence's row locks open.
type heldCopier struct {
	entered chan struct{}
	hold    chan struct{}
	once    sync.Once
}

func newHeldCopier() *heldCopier {
	return &heldCopier{entered: make(chan struct{}), hold: make(chan struct{})}
}

func (h *heldCopier) wait() error {
	if h == nil {
		return fmt.Errorf("no held copier configured")
	}
	close(h.entered)
	<-h.hold
	return nil
}

func (h *heldCopier) release() { h.once.Do(func() { close(h.hold) }) }
