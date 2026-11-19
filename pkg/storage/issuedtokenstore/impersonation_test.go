// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// spec: 13.3 (Credential Flow: Platform-admin impersonation, rule 3) —
// every NotActiveError matches ErrNotActive and names its state.
func TestNotActiveErrorMatchesSentinel_spec_13_3(t *testing.T) {
	t.Parallel()
	for _, state := range []string{StateRevoked, StateExpired} {
		var err error = &NotActiveError{State: state}
		if !errors.Is(err, ErrNotActive) {
			t.Errorf("errors.Is(%v, ErrNotActive) = false", err)
		}
		if !strings.Contains(err.Error(), state) {
			t.Errorf("Error() = %q, want state %q", err.Error(), state)
		}
	}
	if errors.Is(ErrNotFound, ErrNotActive) {
		t.Error("ErrNotFound matches ErrNotActive")
	}
}

// spec: 13.3 (Credential Flow: Platform-admin impersonation, rule 1 (c)) —
// the record refuses, before touching the pool, a call with no caller
// tenant or a row that is not an impersonation row. A nil pool would
// panic on any transaction, so returning an error proves nothing ran.
func TestRecordImpersonationRejectsMalformedInputWithoutWriting_spec_13_3(t *testing.T) {
	t.Parallel()
	s := New(nil)
	cases := []struct {
		name   string
		tok    IssuedToken
		caller string
	}{
		{name: "empty caller tenant", tok: IssuedToken{JTI: "j", TenantID: "acme", Subject: "bob", ActSubject: "alice"}},
		{name: "no act_sub", tok: IssuedToken{JTI: "j", TenantID: "acme", Subject: "bob"}, caller: "platform"},
		{name: "has parent_jti", tok: IssuedToken{JTI: "j", TenantID: "acme", Subject: "bob", ActSubject: "alice", ParentJTI: "p"}, caller: "platform"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.RecordImpersonation(context.Background(), tc.tok, tc.caller); err == nil {
				t.Fatal("RecordImpersonation returned nil, want an error")
			}
		})
	}
}

// userRow answers the fence's users read with a fixed result.
type userRow struct {
	invalidated bool
	err         error
}

func (r userRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*bool) = r.invalidated
	return nil
}

// revokedAtRow answers the end's re-read with a fixed revoked_at.
type revokedAtRow struct {
	revokedAt *time.Time
	err       error
}

func (r revokedAtRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(**time.Time) = r.revokedAt
	return nil
}

// queryRowTx answers QueryRow with row and records the SQL and arguments.
// Every other pgx.Tx method panics through the nil embedded interface.
type queryRowTx struct {
	pgx.Tx
	row     pgx.Row
	gotSQL  string
	gotArgs []any
}

func (tx *queryRowTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	tx.gotSQL, tx.gotArgs = sql, args
	return tx.row
}

// spec: 13.3 (Credential Flow: Platform-admin impersonation, rule 1 (c)),
// 11.4 (User Invalidation), 12.8 (Lifecycle fence and drain)
// The fence refuses a user row that records a hard disable or full revoke,
// passes a missing row, fails closed on a read error, and locks the row.
func TestCheckUserNotInvalidated_spec_13_3(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	cases := []struct {
		name     string
		row      userRow
		wantInv  bool
		wantErr  error
		wantPass bool
	}{
		{name: "live user passes", row: userRow{}, wantPass: true},
		{name: "missing user passes", row: userRow{err: pgx.ErrNoRows}, wantPass: true},
		{name: "invalidated user is refused", row: userRow{invalidated: true}, wantInv: true},
		{name: "read error fails closed", row: userRow{err: boom}, wantErr: boom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &queryRowTx{row: tc.row}
			err := checkUserNotInvalidated(context.Background(), tx, "acme", "bob")
			if !strings.Contains(tx.gotSQL, "FOR SHARE") {
				t.Errorf("users read %q takes no FOR SHARE lock", tx.gotSQL)
			}
			if len(tx.gotArgs) != 2 || tx.gotArgs[0] != "acme" || tx.gotArgs[1] != "bob" {
				t.Errorf("users read args = %v, want [acme bob]", tx.gotArgs)
			}
			var inv *UserInvalidatedError
			switch {
			case tc.wantPass:
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			case tc.wantInv:
				if !errors.As(err, &inv) || inv.TenantID != "acme" || inv.Subject != "bob" {
					t.Fatalf("err = %v, want UserInvalidatedError for acme/bob", err)
				}
			default:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			}
		})
	}
}

// spec: 13.3 (Credential Flow: Platform-admin impersonation, rules 2 and 3)
// When the end's conditional update matches no row, the re-read reports
// ErrNotFound for no impersonation row, revoked for a revoked row, expired
// otherwise, and the read error itself on failure. The re-read matches the
// impersonation predicate so another token's row reads as not found.
func TestInactiveImpersonationErrorClassifiesMiss_spec_13_3(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	boom := errors.New("boom")
	cases := []struct {
		name      string
		row       revokedAtRow
		wantErr   error
		wantState string
	}{
		{name: "no row", row: revokedAtRow{err: pgx.ErrNoRows}, wantErr: ErrNotFound},
		{name: "revoked", row: revokedAtRow{revokedAt: &at}, wantState: StateRevoked},
		{name: "expired", row: revokedAtRow{}, wantState: StateExpired},
		{name: "read error", row: revokedAtRow{err: boom}, wantErr: boom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &queryRowTx{row: tc.row}
			err := inactiveImpersonationError(context.Background(), tx, "imp-1")
			if !strings.Contains(tx.gotSQL, impersonationPredicate) {
				t.Errorf("re-read %q does not match the impersonation predicate", tx.gotSQL)
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			var na *NotActiveError
			if !errors.As(err, &na) || na.State != tc.wantState {
				t.Fatalf("err = %v, want NotActiveError %s", err, tc.wantState)
			}
		})
	}
}
