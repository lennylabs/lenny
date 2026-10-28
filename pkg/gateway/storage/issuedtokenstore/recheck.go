// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
)

// SourceRole names the token-exchange input a rechecked source token came
// from. Its value is the RFC 8693 request parameter name, so the §13.3
// invalid_grant reason for a revoked source is the role followed by
// "_revoked".
type SourceRole string

// Source roles the §13.3 write-before-issue re-read distinguishes.
const (
	// SourceSubjectToken is the exchange's subject_token.
	SourceSubjectToken SourceRole = "subject_token"
	// SourceActorToken is the exchange's actor_token, present on an
	// a2a_delegation child mint (§13.3).
	SourceActorToken SourceRole = "actor_token"
)

// SourceToken is one token the issuance transaction re-reads under the
// §11.7 lock before it writes anything: the jti of an exchange input and
// the role it played in the request.
type SourceToken struct {
	Role SourceRole
	JTI  string
}

// SourceRevokedError reports that the §13.3 write-before-issue re-read
// (step 1a) found a source token whose issued_tokens row has revoked_at
// set. The transaction ran no issuance statement, so no issued_tokens row
// and no token.exchanged row committed. The caller discards the signed
// token and writes the rejection audit row.
type SourceRevokedError struct {
	// Role is the exchange input the revoked token came from.
	Role SourceRole
	// JTI is the revoked source token's identifier.
	JTI string
	// RevokedAt is the revocation instant the re-read observed.
	RevokedAt time.Time
}

// Error implements error.
func (e *SourceRevokedError) Error() string {
	return fmt.Sprintf("issuedtokenstore: %s %q revoked at %s", e.Role, e.JTI, e.RevokedAt.UTC().Format(time.RFC3339Nano))
}

// Reason returns the §13.3 invalid_grant reason for the revoked source:
// subject_token_revoked or actor_token_revoked.
func (e *SourceRevokedError) Reason() string { return string(e.Role) + "_revoked" }

// SourceImpersonationError reports that the §13.3 write-before-issue
// re-read (step 1a) found a source token whose issued_tokens row is a live
// platform-admin impersonation row. A token minted from an impersonation
// bearer would outlive the end of the session, the duration bound, and the
// full-revoke cascade on the admin who started it, so the mint is refused
// whether or not the row is revoked. No issuance statement ran.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, step (1a);
// Platform-admin impersonation, rules 2 and 5).
type SourceImpersonationError struct {
	// Role is the exchange input the impersonation bearer came from.
	Role SourceRole
	// JTI is the impersonation bearer's identifier.
	JTI string
}

// Error implements error.
func (e *SourceImpersonationError) Error() string {
	return fmt.Sprintf("issuedtokenstore: %s %q is an impersonation bearer", e.Role, e.JTI)
}

// Reason returns the §13.3 invalid_grant reason for the impersonation
// source: subject_token_impersonation or actor_token_impersonation.
func (e *SourceImpersonationError) Reason() string { return string(e.Role) + "_impersonation" }

// SourceRefusal evaluates one issued_tokens row as a step (1a) source in
// role. It returns a *SourceRevokedError when the row is revoked, otherwise
// a *SourceImpersonationError when the row is an impersonation row, and
// otherwise nil. A mint that reads a single source row outside the
// issuance transaction evaluates it through this method.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, step (1a);
// Platform-admin impersonation, rule 2).
func (t IssuedToken) SourceRefusal(role SourceRole) error {
	if t.Revoked() {
		return &SourceRevokedError{Role: role, JTI: t.JTI, RevokedAt: t.RevokedAt}
	}
	if t.isImpersonation() {
		return &SourceImpersonationError{Role: role, JTI: t.JTI}
	}
	return nil
}

// isImpersonation reports whether the row matches impersonationPredicate.
func (t IssuedToken) isImpersonation() bool { return t.ActSubject != "" && t.ParentJTI == "" }

// lockAndRecheck takes the §11.7 per-tenant audit advisory lock on tx and
// then runs the §13.3 step (1a) re-read of the listed source tokens. It is
// the first work of every issuance transaction, so a source revoked by a
// concurrent rotation or revocation that committed before this lock was
// granted is observed here, and a concurrent second rotation of one
// subject sees the first rotation's revoked_at stamp and fails.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, Actor-token
// freshness under concurrent rotation), §11.7.
func lockAndRecheck(ctx context.Context, tx pgx.Tx, tenantID string, sources []SourceToken) error {
	if err := auditstore.AcquireLockInTx(ctx, tx, tenantID); err != nil {
		return err
	}
	return recheckSources(ctx, tx, sources)
}

// recheckSources runs the §13.3 step (1a) SELECT over the listed source
// jtis in one statement and returns a *SourceRevokedError naming the first
// listed source whose row has revoked_at set. When no source is revoked it
// returns a *SourceImpersonationError naming the first listed source whose
// row is an impersonation row. Revocation is checked across every source
// before impersonation, so a live impersonation subject with a revoked
// actor reports the revoked actor. Sources are checked in list order, so a
// caller that lists the subject before the actor reports the subject when
// both are revoked or when one token plays both roles. A
// source with no issued_tokens row passes, because a token the platform
// did not record (an upstream IdP token) has no revocation state here. An
// empty list makes no query.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering).
func recheckSources(ctx context.Context, tx pgx.Tx, sources []SourceToken) error {
	jtis := sourceJTIs(sources)
	if len(jtis) == 0 {
		return nil
	}
	states, err := selectSourceStates(ctx, tx, jtis)
	if err != nil {
		return err
	}
	for _, src := range sources {
		if st, ok := states[src.JTI]; ok && st.revokedAt != nil {
			return &SourceRevokedError{Role: src.Role, JTI: src.JTI, RevokedAt: *st.revokedAt}
		}
	}
	for _, src := range sources {
		if st, ok := states[src.JTI]; ok && st.impersonation {
			return &SourceImpersonationError{Role: src.Role, JTI: src.JTI}
		}
	}
	return nil
}

// sourceState is the step (1a) view of one source row: its revocation
// instant, nil when live, and whether it is an impersonation row.
type sourceState struct {
	revokedAt     *time.Time
	impersonation bool
}

// sourceJTIs returns the non-empty jtis of sources, deduplicated.
func sourceJTIs(sources []SourceToken) []string {
	seen := make(map[string]struct{}, len(sources))
	out := make([]string, 0, len(sources))
	for _, src := range sources {
		if src.JTI == "" {
			continue
		}
		if _, dup := seen[src.JTI]; dup {
			continue
		}
		seen[src.JTI] = struct{}{}
		out = append(out, src.JTI)
	}
	return out
}

// selectSourceStates reads revoked_at and the impersonation predicate for
// jtis and returns the state of each listed jti that has a row.
func selectSourceStates(ctx context.Context, tx pgx.Tx, jtis []string) (map[string]sourceState, error) {
	rows, err := tx.Query(ctx,
		`SELECT jti, revoked_at, (`+impersonationPredicate+`) FROM issued_tokens WHERE jti = ANY($1)`, jtis)
	if err != nil {
		return nil, fmt.Errorf("issuedtokenstore: re-read source token state: %w", err)
	}
	defer rows.Close()
	out := make(map[string]sourceState, len(jtis))
	for rows.Next() {
		var (
			jti string
			st  sourceState
		)
		if err := rows.Scan(&jti, &st.revokedAt, &st.impersonation); err != nil {
			return nil, fmt.Errorf("issuedtokenstore: scan source token state: %w", err)
		}
		out[jti] = st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("issuedtokenstore: re-read source token state: %w", err)
	}
	return out, nil
}
