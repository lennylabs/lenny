// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// spec: 13.3 (Credential Flow) — the step (1a) re-read queries the
// listed source jtis once each and skips an absent actor's empty jti.
func TestSourceJTIsDeduplicatesAndSkipsEmpty_spec_13_3(t *testing.T) {
	t.Parallel()
	got := sourceJTIs([]SourceToken{
		{Role: SourceSubjectToken, JTI: "a"},
		{Role: SourceActorToken, JTI: ""},
		{Role: SourceActorToken, JTI: "a"},
		{Role: SourceActorToken, JTI: "b"},
	})
	if want := []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("sourceJTIs = %v, want %v", got, want)
	}
	if got := sourceJTIs(nil); len(got) != 0 {
		t.Errorf("sourceJTIs(nil) = %v, want empty", got)
	}
}

// spec: 13.3 (Credential Flow) — an empty source list, the gateway's
// in-process admin rotation, makes no query and passes.
func TestRecheckSourcesEmptyListMakesNoQuery_spec_13_3(t *testing.T) {
	t.Parallel()
	// A nil tx would panic on any query, so passing proves no query ran.
	if err := recheckSources(context.Background(), nil, nil); err != nil {
		t.Errorf("recheckSources(nil) = %v, want nil", err)
	}
	if err := recheckSources(context.Background(), nil, []SourceToken{{Role: SourceSubjectToken}}); err != nil {
		t.Errorf("recheckSources(empty jti) = %v, want nil", err)
	}
}

// spec: 13.3 (Credential Flow), 8.2 (Delegation Mechanism) — the typed
// error names the role and maps it to the invalid_grant reason.
func TestSourceRevokedErrorNamesRole_spec_13_3(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for role, reason := range map[SourceRole]string{
		SourceSubjectToken: "subject_token_revoked",
		SourceActorToken:   "actor_token_revoked",
	} {
		e := &SourceRevokedError{Role: role, JTI: "jti-1", RevokedAt: at}
		if e.Reason() != reason {
			t.Errorf("Reason() = %q, want %q", e.Reason(), reason)
		}
		if msg := e.Error(); !strings.Contains(msg, string(role)) || !strings.Contains(msg, "jti-1") {
			t.Errorf("Error() = %q, want role and jti", msg)
		}
	}
}

// recheckRow is one issued_tokens row the step (1a) re-read returns.
type recheckRow struct {
	jti       string
	revokedAt *time.Time
}

// recheckRows serves rows to the re-read, failing Scan with scanErr and
// reporting iterErr from Err after the last row.
type recheckRows struct {
	pgx.Rows
	rows    []recheckRow
	next    int
	scanErr error
	iterErr error
}

func (r *recheckRows) Next() bool { r.next++; return r.next <= len(r.rows) }
func (r *recheckRows) Close()     {}
func (r *recheckRows) Err() error { return r.iterErr }
func (r *recheckRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	row := r.rows[r.next-1]
	*dest[0].(*string) = row.jti
	*dest[1].(**time.Time) = row.revokedAt
	return nil
}

// recheckTx answers the §11.7 lock statements with execErr and the step
// (1a) re-read with rows or queryErr, and records whether the re-read ran.
// Every other pgx.Tx method panics through the nil embedded interface.
type recheckTx struct {
	pgx.Tx
	execErr  error
	queryErr error
	rows     *recheckRows
	queried  bool
}

func (tx *recheckTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, tx.execErr
}

func (tx *recheckTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	tx.queried = true
	if tx.queryErr != nil {
		return nil, tx.queryErr
	}
	return tx.rows, nil
}

// spec: 13.3 (Credential Flow: Write-before-issue ordering, Actor-token
// freshness under concurrent rotation), 11.7 (Audit Logging)
// diagnosis: a failure means the issuance transaction's lock-then-re-read
// step admits a revoked source, re-reads before the §11.7 lock is held,
// or swallows a failed lock, query, scan, or row iteration, so a token is
// issued from a source whose revocation the transaction never observed.
func TestLockAndRecheckRefusesRevokedSourcesAndFailsClosed_spec_13_3(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	sources := []SourceToken{
		{Role: SourceSubjectToken, JTI: "subject"},
		{Role: SourceActorToken, JTI: "actor"},
	}
	boom := errors.New("boom")
	cases := []struct {
		name        string
		tx          *recheckTx
		wantRole    SourceRole
		wantErr     error
		wantQueried bool
	}{
		{
			name:        "unrevoked and unrecorded sources pass",
			tx:          &recheckTx{rows: &recheckRows{rows: []recheckRow{{jti: "subject"}}}},
			wantQueried: true,
		},
		{
			name:        "revoked actor is named",
			tx:          &recheckTx{rows: &recheckRows{rows: []recheckRow{{jti: "subject"}, {jti: "actor", revokedAt: &at}}}},
			wantRole:    SourceActorToken,
			wantQueried: true,
		},
		{
			name:        "subject is named first when both are revoked",
			tx:          &recheckTx{rows: &recheckRows{rows: []recheckRow{{jti: "actor", revokedAt: &at}, {jti: "subject", revokedAt: &at}}}},
			wantRole:    SourceSubjectToken,
			wantQueried: true,
		},
		{name: "a failed lock re-reads nothing", tx: &recheckTx{execErr: boom}, wantErr: boom},
		{name: "a failed re-read query fails", tx: &recheckTx{queryErr: boom}, wantErr: boom, wantQueried: true},
		{
			name:        "a failed scan fails",
			tx:          &recheckTx{rows: &recheckRows{rows: []recheckRow{{jti: "subject"}}, scanErr: boom}},
			wantErr:     boom,
			wantQueried: true,
		},
		{
			name:        "a failed row iteration fails",
			tx:          &recheckTx{rows: &recheckRows{iterErr: boom}},
			wantErr:     boom,
			wantQueried: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := lockAndRecheck(context.Background(), tc.tx, "acme", sources)
			if tc.tx.queried != tc.wantQueried {
				t.Errorf("re-read ran = %v, want %v", tc.tx.queried, tc.wantQueried)
			}
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.wantRole != "":
				var re *SourceRevokedError
				if !errors.As(err, &re) || re.Role != tc.wantRole || !re.RevokedAt.Equal(at) {
					t.Fatalf("err = %v, want a SourceRevokedError naming %s", err, tc.wantRole)
				}
			default:
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			}
		})
	}
}
