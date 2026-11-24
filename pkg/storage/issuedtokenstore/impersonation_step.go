// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/storage/pgtenant"
)

// ImpersonationStep is the impersonation step of one recursive-revocation
// request: the request's tenant, its caller token, the root it revokes,
// and the reason and instant stamped on the impersonation rows it ends.
type ImpersonationStep struct {
	TenantID string
	// CallerJTI and CallerSubject identify the request's caller token. The
	// step reads the caller's issued_tokens row and user row under a share
	// lock and refuses a revoked caller or an invalidated user, as the
	// cascade does.
	CallerJTI     string
	CallerSubject string
	RootJTI       string
	Reason        string
	At            time.Time
}

// RevokedImpersonation is one impersonation row the impersonation step
// revoked: its jti, the tenant it lives in, and its sub.
type RevokedImpersonation struct {
	JTI      string
	TenantID string
	Subject  string
}

// ImpersonationStepResult lists the impersonation rows the step revoked,
// in every tenant.
type ImpersonationStepResult struct {
	Revoked []RevokedImpersonation
}

// JTIsIn returns the jtis of the revoked rows that live in tenantID. The
// recursive revocation passes those of its own tenant to the cascade as
// LineageRevocation.StepRevokedCallers.
func (r ImpersonationStepResult) JTIsIn(tenantID string) []string {
	var out []string
	for _, imp := range r.Revoked {
		if imp.TenantID == tenantID {
			out = append(out, imp.JTI)
		}
	}
	return out
}

// RevokeImpersonationStep runs the impersonation step of a recursive
// revocation in one transaction in the request's tenant, the outer
// transaction. It reads the caller's row and user row under FOR SHARE and
// returns ErrCallerRevoked or ErrCallerUserInvalidated, revoking nothing,
// when the caller is refused. It reads the jtis of the root's lineage
// subtree and the start records in the request's tenant whose
// impersonator_jti is one of them. It revokes the active impersonation
// rows of the request's tenant whose impersonator link names the request's
// tenant and a jti of the subtree, and, for each other target tenant a
// start record names, sets that tenant's context with SET LOCAL, revokes
// that tenant's matching rows, and sets the request's tenant again. Every
// update runs in the outer transaction, which commits once.
//
// The outer transaction holds the share lock on the caller's row until it
// commits, so two requests whose callers are impersonation bearers started
// from tokens of each other's subtrees wait on each other only through row
// locks, which the deadlock detector sees. A transaction that Postgres
// aborts as a deadlock victim is retried, up to cascadeDeadlockAttempts
// transactions, and the retry reads its caller again. Any failure rolls
// back every update in every tenant. No statement sets the __all__
// sentinel, and a subtree that started no impersonation in another tenant
// runs no statement under another tenant's context.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rules 4 and
// 7; Recursive-revocation request authorization), §4.2
func (s *Store) RevokeImpersonationStep(ctx context.Context, req ImpersonationStep) (ImpersonationStepResult, error) {
	if req.At.IsZero() {
		req.At = time.Now().UTC()
	}
	var (
		out ImpersonationStepResult
		err error
	)
	for attempt := 0; attempt < cascadeDeadlockAttempts; attempt++ {
		out, err = s.impersonationStepOnce(ctx, req)
		if !isDeadlock(err) {
			break
		}
	}
	return out, err
}

// impersonationStepOnce is one outer transaction of
// RevokeImpersonationStep.
func (s *Store) impersonationStepOnce(ctx context.Context, req ImpersonationStep) (ImpersonationStepResult, error) {
	var out ImpersonationStepResult
	err := pgtenant.InTx(ctx, s.pool, req.TenantID, func(tx pgx.Tx) error {
		if err := checkCaller(ctx, tx, req.TenantID, req.CallerJTI, req.CallerSubject, nil); err != nil {
			return err
		}
		subtree, err := readSubtree(ctx, tx, req.TenantID, req.RootJTI)
		if err != nil {
			return err
		}
		targets, err := readStartTargets(ctx, tx, req.TenantID, subtree)
		if err != nil {
			return err
		}
		own, err := revokeLinkedImpersonations(ctx, tx, req.TenantID, req, subtree)
		if err != nil {
			return err
		}
		out.Revoked = append(out.Revoked, own...)
		for _, target := range targets {
			rows, err := revokeInTargetTenant(ctx, tx, target, req, subtree)
			if err != nil {
				return err
			}
			out.Revoked = append(out.Revoked, rows...)
		}
		return nil
	})
	if err != nil {
		return ImpersonationStepResult{}, err
	}
	return out, nil
}

// revokeInTargetTenant sets target's context on tx, revokes target's
// impersonation rows linked to the request's tenant and subtree, and sets
// the request's tenant again before it returns, so no later statement of
// the outer transaction runs under target's context.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rule 7), §4.2
func revokeInTargetTenant(ctx context.Context, tx pgx.Tx, target string, req ImpersonationStep, subtree []string) ([]RevokedImpersonation, error) {
	if err := pgtenant.SetLocal(ctx, tx, target); err != nil {
		return nil, err
	}
	rows, err := revokeLinkedImpersonations(ctx, tx, target, req, subtree)
	if err != nil {
		return nil, err
	}
	if err := pgtenant.SetLocal(ctx, tx, req.TenantID); err != nil {
		return nil, err
	}
	return rows, nil
}

// readSubtree returns the jtis of the root's lineage subtree inside the
// tenant: rootJTI itself, whether or not it has a row, and every row whose
// parent_jti chain reaches it, revoked or not.
func readSubtree(ctx context.Context, tx pgx.Tx, tenantID, rootJTI string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`WITH RECURSIVE subtree AS (
			SELECT $1::text AS jti, $2::text AS tenant_id
			UNION ALL
			-- The walk stays inside the tenant: each child is pinned to
			-- its parent's tenant (R-02 tenant isolation).
			SELECT c.jti, c.tenant_id FROM issued_tokens c
				JOIN subtree p ON c.parent_jti = p.jti AND c.tenant_id = p.tenant_id
				WHERE c.tenant_id = $2
		)
		SELECT jti FROM subtree`,
		rootJTI, tenantID)
	if err != nil {
		return nil, fmt.Errorf("issuedtokenstore: read lineage subtree: %w", err)
	}
	return collectStrings(rows)
}

// readStartTargets returns, sorted, the distinct target tenants other than
// tenantID that the tenant's start records name for an impersonator_jti in
// jtis. The sort fixes the order in which concurrent steps update the
// target tenants.
func readStartTargets(ctx context.Context, tx pgx.Tx, tenantID string, jtis []string) ([]string, error) {
	if len(jtis) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT target_tenant_id FROM impersonation_starts
		 WHERE tenant_id = $1 AND impersonator_jti = ANY($2) AND target_tenant_id <> $1`,
		tenantID, jtis)
	if err != nil {
		return nil, fmt.Errorf("issuedtokenstore: read impersonation start records: %w", err)
	}
	targets, err := collectStrings(rows)
	if err != nil {
		return nil, err
	}
	slices.Sort(targets)
	return targets, nil
}

// revokeLinkedImpersonations revokes, in tenantID under the context tx
// currently holds, the active impersonation rows whose impersonator link
// names the request's tenant and a jti of subtree, and returns them.
func revokeLinkedImpersonations(ctx context.Context, tx pgx.Tx, tenantID string, req ImpersonationStep, subtree []string) ([]RevokedImpersonation, error) {
	if len(subtree) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx,
		`UPDATE issued_tokens
		 SET revoked_at = $4, revoked_reason = $5
		 WHERE tenant_id = $1 AND impersonator_tenant_id = $2 AND impersonator_jti = ANY($3)
		   AND `+impersonationPredicate+`
		   AND revoked_at IS NULL AND exp > $4
		 RETURNING jti, tenant_id, sub`,
		tenantID, req.TenantID, subtree, req.At, pgtenant.NullString(req.Reason))
	if err != nil {
		return nil, fmt.Errorf("issuedtokenstore: revoke impersonations in tenant %q: %w", tenantID, err)
	}
	defer rows.Close()
	var out []RevokedImpersonation
	for rows.Next() {
		var r RevokedImpersonation
		if err := rows.Scan(&r.JTI, &r.TenantID, &r.Subject); err != nil {
			return nil, fmt.Errorf("issuedtokenstore: scan revoked impersonation: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// collectStrings reads every single-column text row of rows and closes it.
func collectStrings(rows pgx.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
