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

// scriptedRow answers one QueryRow call: it scans value into the first
// destination, or returns err.
type scriptedRow struct {
	value any
	err   error
}

func (r scriptedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	switch d := dest[0].(type) {
	case *string:
		*d = r.value.(string)
	case **time.Time:
		*d = r.value.(*time.Time)
	}
	return nil
}

// scriptedTx answers QueryRow by statement prefix. Every other pgx.Tx
// method panics through the nil embedded interface.
type scriptedTx struct {
	pgx.Tx
	update scriptedRow
	reread scriptedRow
	calls  []string
}

func (tx *scriptedTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	stmt := strings.Fields(sql)[0]
	tx.calls = append(tx.calls, stmt)
	if stmt == "UPDATE" {
		return tx.update
	}
	return tx.reread
}

// spec: 13.3 (Credential Flow: Write-before-issue ordering, Token rotation
// and revocation)
// diagnosis: a failure means the rotation's revoke UPDATE matching no live
// row was read as "the subject has no row", so a revocation that
// committed without the §11.7 lock between the step (1a) re-read and the
// UPDATE let the replacement commit. Check revokePrevious and
// previousRevokedError in issuedtokenstore.go.
func TestRevokePreviousDistinguishesRevokedFromMissing_spec_13_3(t *testing.T) {
	t.Parallel()
	revokedAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		update      scriptedRow
		reread      scriptedRow
		wantSub     string
		wantRevoked bool
		wantErr     func(error) bool
	}{
		{
			name:        "live row is revoked and returns its subject",
			update:      scriptedRow{value: "alice@acme.com"},
			wantSub:     "alice@acme.com",
			wantRevoked: true,
			wantErr:     func(err error) bool { return err == nil },
		},
		{
			name:   "row revoked by a concurrent lock-free writer refuses the rotation",
			update: scriptedRow{err: pgx.ErrNoRows},
			reread: scriptedRow{value: &revokedAt},
			wantErr: func(err error) bool {
				var re *SourceRevokedError
				return errors.As(err, &re) && re.Role == SourceSubjectToken &&
					re.JTI == "prev" && re.RevokedAt.Equal(revokedAt) &&
					re.Reason() == "subject_token_revoked"
			},
		},
		{
			name:    "subject with no row commits without a revoke",
			update:  scriptedRow{err: pgx.ErrNoRows},
			reread:  scriptedRow{err: pgx.ErrNoRows},
			wantErr: func(err error) bool { return err == nil },
		},
		{
			name:    "live row the UPDATE did not match fails closed",
			update:  scriptedRow{err: pgx.ErrNoRows},
			reread:  scriptedRow{value: (*time.Time)(nil)},
			wantErr: func(err error) bool { return err != nil && !errors.As(err, new(*SourceRevokedError)) },
		},
		{
			name:    "re-read failure fails closed",
			update:  scriptedRow{err: pgx.ErrNoRows},
			reread:  scriptedRow{err: errors.New("connection reset")},
			wantErr: func(err error) bool { return err != nil && strings.Contains(err.Error(), "connection reset") },
		},
		{
			name:    "UPDATE failure is returned",
			update:  scriptedRow{err: errors.New("deadlock detected")},
			wantErr: func(err error) bool { return err != nil && strings.Contains(err.Error(), "deadlock detected") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tx := &scriptedTx{update: tc.update, reread: tc.reread}
			sub, revoked, err := revokePrevious(context.Background(), tx, "acme", "prev", "rotation_replaced", revokedAt)
			if !tc.wantErr(err) {
				t.Fatalf("revokePrevious err = %v", err)
			}
			if sub != tc.wantSub || revoked != tc.wantRevoked {
				t.Errorf("revokePrevious = (%q, %v), want (%q, %v)", sub, revoked, tc.wantSub, tc.wantRevoked)
			}
		})
	}
}
