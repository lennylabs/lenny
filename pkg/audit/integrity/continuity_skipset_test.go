// SPDX-License-Identifier: MIT

package integrity

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// skipsetQuerier is a minimal integrity.Querier that dispatches by SQL
// substring for the two queries auditTenants issues: the audit_log
// tenant enumeration (SELECT DISTINCT tenant_id) and the control-plane
// deletion skip-set (FROM tenants WHERE state). Each is scripted from a
// string slice so a test can enumerate one instance and script the
// deletion state on a different instance, exercising the §12.3 split
// billing/audit-pool routing.
type skipsetQuerier struct {
	auditTenants  []string // rows for SELECT DISTINCT tenant_id FROM audit_log
	deletingIDs   []string // rows for SELECT id FROM tenants WHERE state IN (...)
	auditErr      error    // returned by the audit_log enumeration when set
	deletionErr   error    // returned by the tenants deletion query when set
	sawAuditQuery bool
	sawCtrlQuery  bool
}

func (q *skipsetQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	switch {
	case strings.Contains(sql, "FROM tenants WHERE state"):
		q.sawCtrlQuery = true
		if q.deletionErr != nil {
			return nil, q.deletionErr
		}
		return &stringScanRows{rows: q.deletingIDs}, nil
	case strings.Contains(sql, "DISTINCT tenant_id"):
		q.sawAuditQuery = true
		if q.auditErr != nil {
			return nil, q.auditErr
		}
		return &stringScanRows{rows: q.auditTenants}, nil
	default:
		return &stringScanRows{}, nil
	}
}

func (q *skipsetQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("QueryRow is not used by auditTenants")
}

// stringScanRows replays a single-column string result set.
type stringScanRows struct {
	pgx.Rows
	rows []string
	idx  int
}

func (r *stringScanRows) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *stringScanRows) Scan(dest ...any) error {
	*dest[0].(*string) = r.rows[r.idx-1]
	return nil
}

func (r *stringScanRows) Close()     {}
func (r *stringScanRows) Err() error { return nil }

// spec: 12.8 (post-teardown remnant exempt, §12.8 gdpr.* skip), 11.7 (chain integrity)
// diagnosis: a failure means auditTenants either fails to exclude a
// tenant in state='deleting'/'deleted' — so tenant teardown walks the
// retained gdpr.*-only remnant and fires a false §16.5 AuditChainGap —
// or excludes a live tenant that must still be verified.
func TestAuditTenantsExcludesDeletionSkipSet(t *testing.T) {
	ctx := context.Background()

	t.Run("deleting and deleted tenants are excluded, live tenants walked", func(t *testing.T) {
		q := &skipsetQuerier{
			auditTenants: []string{"globex", "acme", "initech", "platform"},
			// acme is mid-teardown, initech is past the Phase-6 tombstone;
			// both carry a discontinuous gdpr.*-only remnant and MUST be
			// skipped. globex is live; platform has no tenants row.
			deletingIDs: []string{"acme", "initech"},
		}
		got, err := auditTenants(ctx, q, q)
		if err != nil {
			t.Fatalf("auditTenants: %v", err)
		}
		want := []string{"globex", "platform"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("auditTenants = %v, want %v (deleting/deleted tenants excluded, sorted)", got, want)
		}
	})

	t.Run("no tenant in deletion enumerates every tenant, sorted", func(t *testing.T) {
		q := &skipsetQuerier{
			auditTenants: []string{"globex", "acme"},
			deletingIDs:  nil,
		}
		got, err := auditTenants(ctx, q, q)
		if err != nil {
			t.Fatalf("auditTenants: %v", err)
		}
		want := []string{"acme", "globex"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("auditTenants = %v, want %v", got, want)
		}
	})

	// spec: 12.3 line 103 (split billing/audit Postgres)
	// The skip-set MUST resolve from the control-plane pool, not the
	// ledger pool. Under the §12.3 Tier-3 topology the ledger instance's
	// tenants table carries no deletion state, so reading the skip-set
	// from the ledger pool would exclude nothing and fire the false alert
	// exactly where the reconciliation must hold.
	t.Run("skip-set resolves from ctrlDB under the split topology", func(t *testing.T) {
		// auditDB (ledger instance): holds the deleted tenant's remnant,
		// but its tenants table has no deletion state.
		auditDB := &skipsetQuerier{
			auditTenants: []string{"acme", "globex"},
			deletingIDs:  nil,
		}
		// ctrlDB (control-plane instance): tenants.state is authoritative.
		ctrlDB := &skipsetQuerier{
			deletingIDs: []string{"acme"},
		}
		got, err := auditTenants(ctx, auditDB, ctrlDB)
		if err != nil {
			t.Fatalf("auditTenants: %v", err)
		}
		want := []string{"globex"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("auditTenants = %v, want %v (skip-set from ctrlDB excludes acme)", got, want)
		}
		if !auditDB.sawAuditQuery {
			t.Error("audit_log enumeration must run on auditDB")
		}
		if !ctrlDB.sawCtrlQuery {
			t.Error("deletion skip-set must be read from ctrlDB")
		}
		if auditDB.sawCtrlQuery {
			t.Error("deletion skip-set must not be read from the ledger pool")
		}

		// Negative control: reading the skip-set from the ledger pool (the
		// defect a tenants join on the ledger connection would ship) does
		// not exclude the remnant.
		gotWrong, err := auditTenants(ctx, auditDB, auditDB)
		if err != nil {
			t.Fatalf("auditTenants (wrong pool): %v", err)
		}
		if !reflect.DeepEqual(gotWrong, []string{"acme", "globex"}) {
			t.Fatalf("auditTenants with the ledger pool as ctrlDB = %v, want the unfiltered set [acme globex]", gotWrong)
		}
	})

	t.Run("deletion-query error is surfaced before enumerating audit_log", func(t *testing.T) {
		sentinel := errors.New("ctrl pool down")
		q := &skipsetQuerier{
			auditTenants: []string{"acme"},
			deletionErr:  sentinel,
		}
		if _, err := auditTenants(ctx, q, q); !errors.Is(err, sentinel) {
			t.Fatalf("auditTenants error = %v, want wrapping %v", err, sentinel)
		}
	})
}

// spec: 12.8 (post-teardown remnant exempt), 11.7 (chain integrity)
// diagnosis: a failure means tenantsInDeletion reads the wrong state
// set or fails to surface a control-plane read error, so the continuity
// verifier either walks a deleted tenant's remnant or masks a ctrl-pool
// outage.
func TestTenantsInDeletion(t *testing.T) {
	ctx := context.Background()

	t.Run("returns exactly the deleting and deleted tenants", func(t *testing.T) {
		q := &skipsetQuerier{deletingIDs: []string{"acme", "initech"}}
		skip, err := tenantsInDeletion(ctx, q)
		if err != nil {
			t.Fatalf("tenantsInDeletion: %v", err)
		}
		if len(skip) != 2 {
			t.Fatalf("skip-set size = %d, want 2", len(skip))
		}
		for _, id := range []string{"acme", "initech"} {
			if _, ok := skip[id]; !ok {
				t.Errorf("skip-set missing %q", id)
			}
		}
	})

	t.Run("empty control-plane result is an empty skip-set", func(t *testing.T) {
		q := &skipsetQuerier{}
		skip, err := tenantsInDeletion(ctx, q)
		if err != nil {
			t.Fatalf("tenantsInDeletion: %v", err)
		}
		if len(skip) != 0 {
			t.Fatalf("skip-set size = %d, want 0", len(skip))
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		sentinel := errors.New("boom")
		q := &skipsetQuerier{deletionErr: sentinel}
		if _, err := tenantsInDeletion(ctx, q); !errors.Is(err, sentinel) {
			t.Fatalf("tenantsInDeletion error = %v, want wrapping %v", err, sentinel)
		}
	})
}
