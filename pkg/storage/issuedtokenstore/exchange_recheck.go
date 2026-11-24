// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// SourceUserInvalidatedError reports that the step (1a) re-read of an
// exchange found a source token whose user row records a hard disable or a
// full revoke. No issuance statement ran.
type SourceUserInvalidatedError struct {
	// Role is the exchange input whose user is invalidated.
	Role SourceRole
	// Subject is that token's sub.
	Subject string
}

// Error implements error.
func (e *SourceUserInvalidatedError) Error() string {
	return fmt.Sprintf("issuedtokenstore: the user %q of the %s is invalidated", e.Subject, e.Role)
}

// Reason returns the step (1a) reason for the invalidated user:
// subject_user_invalidated, actor_user_invalidated, or
// caller_user_invalidated.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, step (1a))
func (e *SourceUserInvalidatedError) Reason() string {
	return strings.TrimSuffix(string(e.Role), "_token") + "_user_invalidated"
}

// LineageDepthError reports that the parent row of an exchange records a
// depth at or above the lineage depth cap. No issuance statement ran.
type LineageDepthError struct {
	// ParentJTI is the parent token the issued row would record.
	ParentJTI string
	// Depth is the parent row's depth, and Max the cap it reached.
	Depth int
	Max   int
}

// Error implements error.
func (e *LineageDepthError) Error() string {
	return fmt.Sprintf("issuedtokenstore: parent token %q is at lineage depth %d, at or above the cap %d", e.ParentJTI, e.Depth, e.Max)
}

// Reason returns the step (1a) reason for a parent at the cap.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, step (1a))
func (e *LineageDepthError) Reason() string { return "lineage_depth_exceeded" }

// recheckExchange runs step (1a) of an exchange on /v1/oauth/token on tx:
// the §11.7 lock and the re-read of the source rows (lockAndRecheck), then
// the user rows of the sources, then the lineage depth cap on the parent
// the issued row records. It sets tok.Depth from the parent row. A refusal
// returns a *SourceRevokedError, a *SourceImpersonationError, a
// *SourceUserInvalidatedError, or a *LineageDepthError, in that order of
// precedence.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, step (1a))
func (s *Store) recheckExchange(ctx context.Context, tx pgx.Tx, tok *IssuedToken, sources []SourceToken) error {
	if err := lockAndRecheck(ctx, tx, tok.TenantID, sources); err != nil {
		return err
	}
	if err := recheckSourceUsers(ctx, tx, tok.TenantID, sources); err != nil {
		return err
	}
	parentDepth, hasParentRow, err := readParentDepth(ctx, tx, tok.TenantID, tok.ParentJTI)
	if err != nil {
		return err
	}
	if hasParentRow && parentDepth >= s.limits.LineageMaxDepth {
		return &LineageDepthError{ParentJTI: tok.ParentJTI, Depth: parentDepth, Max: s.limits.LineageMaxDepth}
	}
	tok.Depth = childDepth(tok.ParentJTI, parentDepth, hasParentRow)
	return nil
}

// recordDepth sets tok.Depth from the parent row on tx without applying the
// cap. The playground mint records its bearer through it, because the
// lineage depth cap applies to the token endpoint only.
//
// spec: §10.2 (Playground mint invariants, invariant 6), §12.2
func recordDepth(ctx context.Context, tx pgx.Tx, tok *IssuedToken) error {
	parentDepth, hasParentRow, err := readParentDepth(ctx, tx, tok.TenantID, tok.ParentJTI)
	if err != nil {
		return err
	}
	tok.Depth = childDepth(tok.ParentJTI, parentDepth, hasParentRow)
	return nil
}

// childDepth returns the depth of a row whose parent is parentJTI: 0 with
// no parent, the parent row's depth plus one when the parent has a row,
// and 1 when it has none.
//
// spec: §12.2 (TokenIssuanceStore depth column)
func childDepth(parentJTI string, parentDepth int, hasParentRow bool) int {
	switch {
	case parentJTI == "":
		return 0
	case hasParentRow:
		return parentDepth + 1
	default:
		return 1
	}
}

// readParentDepth reads the depth of the parent row in the tenant. An empty
// parent jti, or a parent without a row, reports hasRow false.
func readParentDepth(ctx context.Context, tx pgx.Tx, tenantID, parentJTI string) (depth int, hasRow bool, err error) {
	if parentJTI == "" {
		return 0, false, nil
	}
	err = tx.QueryRow(ctx,
		`SELECT depth FROM issued_tokens WHERE jti = $1 AND tenant_id = $2`,
		parentJTI, tenantID).Scan(&depth)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("issuedtokenstore: read parent depth: %w", err)
	}
	return depth, true, nil
}

// recheckSourceUsers reads, under a share lock, the user row of the sub of
// each source that lists one, in list order, and returns a
// *SourceUserInvalidatedError naming the first source whose user row
// records a hard disable or a full revoke. A sub read once is not read
// again, so a token that shares its sub with an earlier source is reported
// in that source's role. A user without a row passes. The share lock
// conflicts with the update that writes the invalidation level, so the
// exchange either observes a full revoke's level or commits before it.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, step (1a)),
// §11.4
func recheckSourceUsers(ctx context.Context, tx pgx.Tx, tenantID string, sources []SourceToken) error {
	seen := make(map[string]struct{}, len(sources))
	for _, src := range sources {
		if src.Subject == "" {
			continue
		}
		if _, dup := seen[src.Subject]; dup {
			continue
		}
		seen[src.Subject] = struct{}{}
		err := checkUserNotInvalidated(ctx, tx, tenantID, src.Subject)
		var invalidated *UserInvalidatedError
		if errors.As(err, &invalidated) {
			return &SourceUserInvalidatedError{Role: src.Role, Subject: src.Subject}
		}
		if err != nil {
			return err
		}
	}
	return nil
}
