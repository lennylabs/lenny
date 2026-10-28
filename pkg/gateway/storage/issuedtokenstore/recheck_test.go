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

// spec: 13.3 (Credential Flow) — the typed
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
	jti           string
	revokedAt     *time.Time
	impersonation bool
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
	*dest[2].(*bool) = row.impersonation
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
// freshness under concurrent rotation; Platform-admin impersonation, rule
// 5), 11.7 (Audit Logging)
// diagnosis: a failure means the issuance transaction's lock-then-re-read
// step admits a revoked source or a live impersonation bearer, checks
// impersonation before revocation across the sources, re-reads before the §11.7 lock is held,
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
		wantImp     SourceRole
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
		{
			name:        "live impersonation subject is refused",
			tx:          &recheckTx{rows: &recheckRows{rows: []recheckRow{{jti: "subject", impersonation: true}, {jti: "actor"}}}},
			wantImp:     SourceSubjectToken,
			wantQueried: true,
		},
		{
			name:        "live impersonation actor is refused",
			tx:          &recheckTx{rows: &recheckRows{rows: []recheckRow{{jti: "subject"}, {jti: "actor", impersonation: true}}}},
			wantImp:     SourceActorToken,
			wantQueried: true,
		},
		{
			name:        "a revoked actor outranks a live impersonation subject",
			tx:          &recheckTx{rows: &recheckRows{rows: []recheckRow{{jti: "subject", impersonation: true}, {jti: "actor", revokedAt: &at}}}},
			wantRole:    SourceActorToken,
			wantQueried: true,
		},
		{
			name:        "a revoked impersonation subject is refused as revoked",
			tx:          &recheckTx{rows: &recheckRows{rows: []recheckRow{{jti: "subject", revokedAt: &at, impersonation: true}}}},
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
			case tc.wantImp != "":
				var ie *SourceImpersonationError
				if !errors.As(err, &ie) || ie.Role != tc.wantImp {
					t.Fatalf("err = %v, want a SourceImpersonationError naming %s", err, tc.wantImp)
				}
			default:
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			}
		})
	}
}

// spec: 13.3 (Credential Flow: Write-before-issue ordering, step (1a);
// Platform-admin impersonation, rule 2) — the impersonation refusal names
// the role and maps it to the invalid_grant reason.
func TestSourceImpersonationErrorNamesRole_spec_13_3(t *testing.T) {
	t.Parallel()
	for role, reason := range map[SourceRole]string{
		SourceSubjectToken: "subject_token_impersonation",
		SourceActorToken:   "actor_token_impersonation",
	} {
		e := &SourceImpersonationError{Role: role, JTI: "jti-1"}
		if e.Reason() != reason {
			t.Errorf("Reason() = %q, want %q", e.Reason(), reason)
		}
		if msg := e.Error(); !strings.Contains(msg, string(role)) || !strings.Contains(msg, "jti-1") {
			t.Errorf("Error() = %q, want role and jti", msg)
		}
	}
}

// spec: 13.3 (Credential Flow: Write-before-issue ordering, step (1a);
// Platform-admin impersonation, rule 2) — SourceRefusal evaluates a single
// row the way the in-transaction re-read evaluates each source.
func TestIssuedTokenSourceRefusal_spec_13_3(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		tok     IssuedToken
		wantRev bool
		wantImp bool
	}{
		{name: "ordinary live row passes", tok: IssuedToken{JTI: "j"}},
		{name: "ordinary revoked row is revoked", tok: IssuedToken{JTI: "j", RevokedAt: at}, wantRev: true},
		{name: "live impersonation row is impersonation", tok: IssuedToken{JTI: "j", ActSubject: "alice"}, wantImp: true},
		{name: "revoked impersonation row is revoked", tok: IssuedToken{JTI: "j", ActSubject: "alice", RevokedAt: at}, wantRev: true},
		{name: "delegation child passes", tok: IssuedToken{JTI: "j", ActSubject: "alice", ParentJTI: "p"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tok.SourceRefusal(SourceActorToken)
			var re *SourceRevokedError
			var ie *SourceImpersonationError
			switch {
			case tc.wantRev:
				if !errors.As(err, &re) || re.Role != SourceActorToken || re.JTI != "j" || !re.RevokedAt.Equal(at) {
					t.Fatalf("err = %v, want SourceRevokedError for actor_token j", err)
				}
			case tc.wantImp:
				if !errors.As(err, &ie) || ie.Role != SourceActorToken || ie.JTI != "j" {
					t.Fatalf("err = %v, want SourceImpersonationError for actor_token j", err)
				}
			default:
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			}
		})
	}
}
