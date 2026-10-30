// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// This file holds the gateway-side adapters the §12.8 user-erasure runner
// is wired with: the session lister the orchestrator's session-scoped
// erasers read, the completion-receipt sink, the cross_tenant_read
// recorder, and the startup check of the runner's concurrency limit. The
// leader-gated sweep that executes the jobs is addErasureSweep
// (leader_sweeps.go).

// erasureSessionLister is the erasure orchestrator's Sessions lister: the
// ids of the user's session rows listed from the Postgres primary, plus
// every id reserved for the user, so the session-scoped erasers reach the
// data a creation path wrote under a reserved id before its insert. A
// listing or reservation error is returned, so the orchestrator runs no
// eraser and the job fails store_delete with the restriction left set.
//
// spec: §12.8 (Lifecycle fence and drain), §12.8 (DeleteByUser
// dependency-ordered deletion sequence).
type erasureSessionLister struct {
	sessions erasureSessionSource
}

// erasureSessionSource is the part of sessionstore.Store the erasure
// session lister reads.
type erasureSessionSource interface {
	List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error)
	ReservedSessionIDs(ctx context.Context, tenantID, userID string) ([]string, error)
}

// List returns the session ids the erasure of (tenantID, userID) covers.
func (l erasureSessionLister) List(ctx context.Context, tenantID, userID string) ([]string, error) {
	rows, err := l.sessions.List(ctx, tenantID, sessionstore.ListFilter{UserID: userID, Primary: true})
	if err != nil {
		return nil, fmt.Errorf("erasure session lister: list sessions: %w", err)
	}
	reserved, err := l.sessions.ReservedSessionIDs(ctx, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("erasure session lister: reserved session ids: %w", err)
	}
	ids := make([]string, 0, len(rows)+len(reserved))
	for _, s := range rows {
		ids = append(ids, s.ID)
	}
	return append(ids, reserved...), nil
}

// erasureReceiptSink writes the §12.8 gdpr.erasure_completed receipt on
// the job's tenant chain through the audit appender and returns the append
// error, so the runner completes the job and lifts the restriction only
// after the receipt is durable. It writes the payload layout
// admin.ChainAuditSink.EmitAdminEvent writes, attributing the event to the
// principal persisted on the job rather than to a request context. It does
// not go through the admin router's AuditSink, which drops the error.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)).
type erasureReceiptSink struct {
	appender policy.AuditAppender
}

// WriteReceipt appends the receipt of j, which carries its ReceiptAt.
func (s erasureReceiptSink) WriteReceipt(ctx context.Context, j erasurejob.Job) error {
	payload, err := json.Marshal(map[string]any{
		"actor_subject":   j.InitiatedBy,
		"actor_tenant_id": j.TenantID,
		"target_resource": j.UserID,
		"detail":          admin.ErasureCompletionReceipt(j),
	})
	if err != nil {
		return fmt.Errorf("encode erasure receipt: %w", err)
	}
	if _, err := s.appender.Append(ctx, j.TenantID, "gdpr.erasure_completed", payload, j.ReceiptAt); err != nil {
		return fmt.Errorf("append erasure receipt: %w", err)
	}
	return nil
}

// erasureCrossTenantRead returns the recorder the erasure runner, the
// erasure sampler, and the erasure-job handlers call once per read they
// make under the all-tenants sentinel, writing a cross_tenant_read event
// with category erasure_job through the Postgres audit store.
//
// spec: §12.3 (Postgres HA Requirements).
func erasureCrossTenantRead(store *auditstore.Store) erasurejob.CrossTenantReadFunc {
	return func(ctx context.Context, endpoint, callerSubject string, rowCount int) error {
		return store.EmitCrossTenantRead(ctx, auditstore.CrossTenantRead{
			Category:      "erasure_job",
			RowCount:      rowCount,
			Endpoint:      endpoint,
			CallerSubject: callerSubject,
		})
	}
}

// validateErasureMaxConcurrentJobs refuses a runner limit below 1, which
// would leave every erasure job unexecuted.
func validateErasureMaxConcurrentJobs(n int) error {
	if n < 1 {
		return fmt.Errorf("--erasure-max-concurrent-jobs must be at least 1, got %d (§12.8)", n)
	}
	return nil
}
