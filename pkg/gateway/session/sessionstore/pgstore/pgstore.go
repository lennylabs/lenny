// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §4.2 SessionStore. It
// satisfies the same sessionstore.Store contract as the in-memory
// memstore, so the gateway swaps backends without changing callers.
//
// Every operation runs inside a transaction that sets
// app.current_tenant: the §12.3 lenny_tenant_guard trigger rejects
// writes without it, and the row-level security policies filter reads
// by it. Reads additionally carry an explicit tenant_id predicate so
// the store behaves identically whether the connection role is the
// non-superuser lenny_app (RLS-filtered) or a superuser (RLS bypassed).
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Store is the Postgres-backed SessionStore. Construct with New.
type Store struct {
	pool *pgxpool.Pool
	// read is the §12.3 line 146 read-replica pool for the read-heavy
	// query classes the spec names (session status, task tree). It is set
	// to pool unless WithReadPool wires a separate reader endpoint, so a
	// single primary serves reads and writes unchanged. Writes always use
	// pool. spec: §12.3 line 146.
	read *pgxpool.Pool
}

// Option configures a Store at construction time.
type Option func(*Store)

// WithReadPool routes the read-heavy session-status and task-tree
// queries (§12.3 line 146) to a separate read-replica pool. A nil pool
// keeps reads on the primary. spec: §12.3 line 146.
func WithReadPool(read *pgxpool.Pool) Option {
	return func(s *Store) {
		if read != nil {
			s.read = read
		}
	}
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied. Without WithReadPool
// the read-heavy query paths share pool.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	s := &Store{pool: pool, read: pool}
	for _, o := range opts {
		o(s)
	}
	return s
}

var _ sessionstore.Store = (*Store)(nil)

// selectList is the column projection for reads. UUID columns are
// rendered as text so they scan into the Session struct's string
// fields; parent_session_id collapses NULL to the empty string.
//
// The trailing five columns from migration 0050 are
// (cwd, pod_assignment, recovery_generation, coordination_generation,
// schema_version) — the §4.2 line 156 session-record fields. The
// next three columns from migration 0055 are
// (retry_count, policy_enforcement_state, resume_eligible_until) —
// the §4.2 line 158-159 retry counter, policy enforcement state,
// and resume window. The final column from migration 0061 is
// `last_successful_checkpoint_at` — the §4.4 line 258 freshness
// timestamp the gauge / `lenny_checkpoint_stale_sessions` reaper keys
// off.
// The column (workspace_snapshot_hash) is the §4.5 line 311
// content-addressed snapshot hash added in migration 0068. The
// execution_mode and scrub_policy columns are the §7.1
// line 75 sessionIsolationLevel halves added in migration 0084.
// The conversation_continuity column is the §7.1 line 74
// sessionIsolationLevel envelope half, and the
// terminated_at/terminated_reason and suspended_at/suspended_reason
// columns are the §7.2 / §8.8 session-condition facts relocated off
// Sandbox.status.conditions, all added in migration 0168. The nullable
// condition timestamps COALESCE to NULL: a zero timestamp means the
// condition has not fired. spec: §6.49; §7.1 line 74; §7.2 line 230;
// §8.8.
// The metadata column is the §7.1 line 6 client-supplied
// CreateSession(..., metadata) payload added in migration 0086
// (F-7.3.20). The next two columns are the §7.3
// retry_policy + last_checkpoint_workspace_bytes pair added in
// migration 0087 (F-7.3.1 / F-7.3.21). The trailing two columns are
// the §8.2 line 52 delegation tracing_context and the §8.3 line 266 /
// §8.10 cascade_on_failure lease policy added in migration 0090
// (F-8.2.14 / F-8.2.15).
const selectList = `id::text, tenant_id, user_id, state, runtime_ref, pool_ref,
	isolation_profile, COALESCE(parent_session_id::text, ''), failure_class,
	failure_reason, workspace_snapshot_ref, workspace_snapshot_source,
	workspace_snapshot_at, parent_workspace_ref, retention_expires_at,
	upload_token_digest, upload_token_expiry, created_at, updated_at,
	workspace_plan, legal_hold,
	experiment_id, experiment_variant_id, experiment_inherited, environment,
	cwd, pod_assignment, recovery_generation, coordination_generation,
	schema_version,
	retry_count, policy_enforcement_state, resume_eligible_until,
	last_successful_checkpoint_at,
	COALESCE(workspace_snapshot_hash, ''),
	execution_mode, scrub_policy,
	conversation_continuity,
	terminated_at, terminated_reason,
	suspended_at, suspended_reason,
	metadata,
	retry_policy, last_checkpoint_workspace_bytes,
	last_seq,
	workspace_root,
	tracing_context, cascade_on_failure,
	COALESCE(root_session_id::text, ''),
	tree_visibility,
	delegation_depth,
	delegation_lease,
	env, request_envelope,
	legal_hold_set_by, legal_hold_set_at, legal_hold_note,
	last_agent_activity_at,
	COALESCE(credential_origin_session_id::text, '')`

// Create persists a fresh session row. root_session_id defaults to the
// session's own id when the caller did not stamp one (a standalone
// session is the root of its own tree). The §8.2 delegation path stamps
// the parent's RootSessionID onto every child so all rows in one tree
// share the same root, which §8.9 and §12.5 read via the
// idx_sessions_root index. F-8.9.8.
func (s *Store) Create(ctx context.Context, sess sessionstore.Session) error {
	now := time.Now().UTC()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = sess.CreatedAt
	}
	ref, src, at, hash := snapshotCols(sess.WorkspaceSnapshot)

	// The trailing five binds ($26-$30) cover the §4.2 session-record
	// fields added in migration 0050: cwd, pod_assignment,
	// recovery_generation, coordination_generation, schema_version.
	// The next three binds ($31-$33) cover the §4.2 line 158-159
	// retry counter, policy enforcement state, and resume window
	// added in migration 0055. Bind $34 is the §4.4 freshness
	// timestamp added in migration 0061. Bind $35 is the §4.5
	// line 311 content-addressed snapshot hash added in
	// migration 0068. Binds $36 and $37 are the §7.1 line 75
	// execution_mode and scrub_policy halves of sessionIsolationLevel
	// added in migration 0084. Bind $38 is the §7.1 line 6
	// client-supplied metadata payload added in migration 0086
	// (F-7.3.20). Binds $39 and $40 are the §7.3 retry_policy +
	// last_checkpoint_workspace_bytes pair added in migration 0087
	// (F-7.3.1 / F-7.3.21). Binds $43 and $44 are the §8.2 line 52
	// delegation tracing_context and the §8.3 line 266 / §8.10
	// cascade_on_failure lease policy added in migration 0090
	// (F-8.2.14 / F-8.2.15). Bind $45 is the §8.9 root_session_id —
	// children inherit the parent's root so the column identifies the
	// delegation tree by a single value; empty falls back to the
	// session's own id so a standalone row stays its own root. F-8.9.8.
	const insertSQL = `INSERT INTO sessions (
		id, tenant_id, user_id, state, runtime_ref, pool_ref, isolation_profile,
		parent_session_id, root_session_id, failure_class, failure_reason,
		workspace_snapshot_ref, workspace_snapshot_source, workspace_snapshot_at,
		parent_workspace_ref, retention_expires_at, upload_token_digest,
		upload_token_expiry, created_at, updated_at, workspace_plan, legal_hold,
		experiment_id, experiment_variant_id, experiment_inherited, environment,
		cwd, pod_assignment, recovery_generation, coordination_generation,
		schema_version,
		retry_count, policy_enforcement_state, resume_eligible_until,
		last_successful_checkpoint_at,
		workspace_snapshot_hash,
		execution_mode, scrub_policy,
		conversation_continuity,
		terminated_at, terminated_reason,
		suspended_at, suspended_reason,
		metadata,
		retry_policy, last_checkpoint_workspace_bytes,
		last_seq,
		workspace_root,
		tracing_context, cascade_on_failure,
		tree_visibility,
		delegation_depth,
		delegation_lease,
		env, request_envelope,
		last_agent_activity_at,
		credential_origin_session_id
	) VALUES (
		$1::uuid, $2, $3, $4, $5, $6, $7,
		NULLIF($8, '')::uuid,
		-- §8.9 root_session_id: caller-provided ($45) wins; otherwise
		-- inherit the parent's root via subquery so derived/delegated
		-- children share the parent's tree; otherwise default to own
		-- id so a standalone session is its own root. F-8.9.8.
		COALESCE(
			NULLIF($45, '')::uuid,
			(SELECT root_session_id FROM sessions
				WHERE id = NULLIF($8, '')::uuid AND tenant_id = $2),
			$1::uuid
		),
		$9, $10,
		$11, $12, $13, $14, $15, $16, $17, $18, $19, $20::jsonb, $21,
		$22, $23, $24, $25,
		$26, $27, $28, $29, $30,
		$31, $32::jsonb, $33,
		$34,
		NULLIF($35, ''),
		$36, $37,
		$52,
		$53, $54,
		$55, $56,
		$38::jsonb,
		$39::jsonb, $40,
		$41,
		$42,
		$43::jsonb, $44,
		$46,
		$47,
		$48::jsonb,
		$49::jsonb, $50::jsonb,
		$51,
		-- §8.3 line 472 credential_origin_session_id: empty string
		-- collapses to SQL NULL (the read path COALESCEs it back to the
		-- row's own id), matching the parent_session_id NULLIF convention.
		NULLIF($57, '')::uuid
	)`

	expID, expVariant, expInherited := experimentCols(sess.ExperimentContext)
	schemaVersion := sess.SchemaVersion
	if schemaVersion == 0 {
		// spec: §4.2 line 156 — v1 sessions are written at schema_version=1.
		schemaVersion = 1
	}
	err := pgtenant.InTx(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insertSQL,
			sess.ID, sess.TenantID, sess.UserID, string(sess.State), sess.RuntimeRef,
			sess.PoolRef, string(sess.IsolationProfile), sess.ParentSessionID,
			string(sess.FailureClass), sess.FailureReason,
			ref, src, pgtenant.NullTime(at), sess.ParentWorkspaceRef,
			pgtenant.NullTime(sess.RetentionExpiresAt), sess.UploadTokenDigest,
			pgtenant.NullTime(sess.UploadTokenExpiry), sess.CreatedAt, sess.UpdatedAt,
			jsonbArg(sess.WorkspacePlan), sess.LegalHold,
			expID, expVariant, expInherited, sess.Environment,
			sess.Cwd, sess.PodAssignment, sess.RecoveryGeneration,
			sess.CoordinationGeneration, schemaVersion,
			sess.RetryCount, policyEnforcementArg(sess.PolicyEnforcementState),
			pgtenant.NullTime(sess.ResumeEligibleUntil),
			pgtenant.NullTime(sess.LastSuccessfulCheckpointAt),
			hash,
			sess.ExecutionMode, sess.ScrubPolicy,
			metadataArg(sess.Metadata),
			retryPolicyArg(sess.RetryPolicy),
			workspaceBytesArg(sess.WorkspaceSnapshot),
			sess.LastSeq,
			sess.WorkspaceRoot,
			tracingContextArg(sess.TracingContext),
			string(sess.CascadeOnFailure),
			// $45 — §8.9 RootSessionID; empty falls back to the
			// session's own id via SQL COALESCE in insertSQL. F-8.9.8.
			sess.RootSessionID,
			// $46 — §8.5 tree_visibility persisted raw; empty stays the
			// in-Go "resolve to default full" convention (the read path
			// normalises via TreeVisibility.OrDefault). The §8.2
			// delegation Service stamps an explicit resolved value onto
			// every child it admits. F-8.5.2 / F-8.9.2.
			string(sess.TreeVisibility),
			// $47 — §10.7 delegation_depth; 0 for a root session, stamped
			// to parent.depth+1 by the §8.2 delegation Service. Invariant
			// after creation, so it is absent from updateSQL. F-10.7.5.
			int(sess.DelegationDepth),
			// $48 — §8.2 delegation_lease; the granted lease_slice the
			// §8.2 delegation Service stamps on every child it admits, so
			// a descendant's own lease_slice can be validated against the
			// ancestor ceiling. NULL for a root/standalone session and
			// any child whose lease declared no slice. Invariant after
			// creation, so it is absent from updateSQL. F-8.2.2.
			delegationLeaseArg(sess.DelegationLease),
			// $49 — §14 client-supplied env map; every key passed the
			// deployer blocklist at admission. NULL when the client
			// supplied none. F-14.1.12.
			envArg(sess.Env),
			// $50 — §14.1 request envelope bundle (pool, timeouts,
			// credentialPolicy, delegationLease, runtimeOptions). NULL when
			// the client supplied none of the bundled fields. F-14.1.14.
			requestEnvelopeArg(sess),
			// $51 — §6.2 lines 273-300 idle-timer anchor; NULL until the
			// first qualifying agent activity is recorded, in which case
			// the watchdog falls back to updated_at. F-11.3.7.
			pgtenant.NullTime(sess.LastAgentActivityAt),
			// $52 — §7.1 line 74 conversation_continuity envelope half;
			// "platform" for session mode, "none" for service mode, or ""
			// until the next read path resolves it from the pool. spec:
			// §7.1 line 74.
			sess.ConversationContinuity,
			// $53-$54 — §7.2 / §8.8 Terminated session-condition fact
			// relocated off Sandbox.status.conditions. terminated_at passes
			// through NullTime so a zero time persists as SQL NULL per the
			// "condition has not fired" sentinel. spec: §6.49; §7.2 line
			// 230; §8.8.
			pgtenant.NullTime(sess.TerminatedAt), sess.TerminatedReason,
			// $55-$56 — §7.2 interrupt-suspension Suspended condition fact.
			// suspended_at is NULL while the session is not suspended. spec:
			// §6.49; §7.2 line 230; §8.8.
			pgtenant.NullTime(sess.SuspendedAt), sess.SuspendedReason,
			// $57 — §8.3 line 472 / 488 credential_origin_session_id; the
			// resolved origin pool the delegation Service stamps at
			// child-row creation so contiguous `inherit` hops share one
			// origin. Empty falls back to the row's own id via the
			// NULLIF/COALESCE convention. Invariant after creation, so it is
			// absent from updateSQL.
			sess.CredentialOriginSessionID)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sessionstore.ErrAlreadyExists
	}
	return err
}

// Get returns the session row keyed by (tenantID, id). A cross-tenant
// miss is indistinguishable from a missing row (§4.2 isolation).
func (s *Store) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	var out sessionstore.Session
	// spec: §12.3 line 146 — session-status read routes to the read replica.
	err := pgtenant.InTx(ctx, s.read, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM sessions WHERE id = $1::uuid AND tenant_id = $2`,
			id, tenantID)
		sess, err := scanSession(row)
		if err != nil {
			return err
		}
		out = sess
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, normalizeMiss(err)
	}
	return out, nil
}

// GetByID resolves a session by its globally-unique id across every
// tenant, backing the §24.11 platform-admin session-investigation
// surface where the operator supplies only the session id. It runs
// under the §4.2 platform-admin cross-tenant context
// (`app.current_tenant = '__all__'`) so the lenny_tenant_isolation RLS
// policy does not filter the lookup by tenant. The caller MUST gate
// this on a platform-admin role. spec: §24.11 lines 135-136; §4.2
// line 163.
func (s *Store) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	var out sessionstore.Session
	// spec: §12.3 line 146 — session-status read routes to the read replica.
	err := pgtenant.InAllTenants(ctx, s.read, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM sessions WHERE id = $1::uuid`, id)
		sess, err := scanSession(row)
		if err != nil {
			return err
		}
		out = sess
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, normalizeMiss(err)
	}
	return out, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE, then
// writes it back. UpdatedAt strictly advances on every successful
// Update (clamped to the prior value + 1µs, the Postgres timestamptz
// resolution) so callers can rely on monotonicity.
func (s *Store) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	// The trailing five SET clauses cover the §4.2 session-record
	// fields added in migration 0050; the next three cover the §4.2
	// line 158-159 retry counter, policy enforcement state, and
	// resume window added in migration 0055. Clause $32 is the §4.4
	// line 258 freshness timestamp added in migration 0061. Clause
	// $33 is the §4.5 line 311 content-addressed snapshot hash
	// added in migration 0068. Clause $36 is the §7.1 line 6
	// client-supplied metadata payload added in migration 0086
	// (F-7.3.20). Clauses $37 and $38 are the §7.3 retry_policy +
	// last_checkpoint_workspace_bytes pair added in migration 0087
	// (F-7.3.1 / F-7.3.21). Clauses $41 and $42 are the §8.2 line 52
	// delegation tracing_context and the §8.3 line 266 / §8.10
	// cascade_on_failure lease policy added in migration 0090
	// (F-8.2.14 / F-8.2.15).
	// last_seq is GREATEST-floored so a late writer from a sibling
	// replica cannot rewind a freshly published Seq; the DB CHECK
	// constraint catches the impossible negative. F-7.3.3.
	const updateSQL = `UPDATE sessions SET
		user_id = $3, state = $4, runtime_ref = $5, pool_ref = $6,
		isolation_profile = $7, parent_session_id = NULLIF($8, '')::uuid,
		failure_class = $9, failure_reason = $10, workspace_snapshot_ref = $11,
		workspace_snapshot_source = $12, workspace_snapshot_at = $13,
		parent_workspace_ref = $14, retention_expires_at = $15,
		upload_token_digest = $16, upload_token_expiry = $17, updated_at = $18,
		legal_hold = $19, experiment_id = $20, experiment_variant_id = $21,
		experiment_inherited = $22, environment = $23,
		cwd = $24, pod_assignment = $25, recovery_generation = $26,
		coordination_generation = $27, schema_version = $28,
		retry_count = $29, policy_enforcement_state = $30::jsonb,
		resume_eligible_until = $31,
		last_successful_checkpoint_at = $32,
		workspace_snapshot_hash = NULLIF($33, ''),
		execution_mode = $34, scrub_policy = $35,
		conversation_continuity = $50,
		terminated_at = $51, terminated_reason = $52,
		suspended_at = $53, suspended_reason = $54,
		metadata = $36::jsonb,
		retry_policy = $37::jsonb,
		last_checkpoint_workspace_bytes = $38,
		last_seq = GREATEST(last_seq, $39),
		workspace_root = CASE
			WHEN $40 = '' THEN workspace_root
			ELSE $40
		END,
		tracing_context = $41::jsonb,
		cascade_on_failure = $42,
		tree_visibility = $43,
		env = $44::jsonb,
		request_envelope = $45::jsonb,
		legal_hold_set_by = $46,
		legal_hold_set_at = $47,
		legal_hold_note = $48,
		last_agent_activity_at = $49
	WHERE id = $1::uuid AND tenant_id = $2`

	var out sessionstore.Session
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM sessions WHERE id = $1::uuid AND tenant_id = $2 FOR UPDATE`,
			id, tenantID)
		sess, err := scanSession(row)
		if err != nil {
			return err
		}
		prevUpdated := sess.UpdatedAt
		prevRecoveryGen := sess.RecoveryGeneration
		prevCoordGen := sess.CoordinationGeneration
		prevRetryCount := sess.RetryCount
		prevLastSeq := sess.LastSeq
		if err := mutate(&sess); err != nil {
			return err
		}
		// spec: §4.2 line 156 / line 158 — recovery_generation,
		// coordination_generation, and retry_count are monotonically
		// non-decreasing. Enforce the floor here so an accidental
		// rollback in the mutate callback cannot violate the
		// invariant; the DB CHECK constraint catches the impossible
		// negative.
		if sess.RecoveryGeneration < prevRecoveryGen {
			sess.RecoveryGeneration = prevRecoveryGen
		}
		if sess.CoordinationGeneration < prevCoordGen {
			sess.CoordinationGeneration = prevCoordGen
		}
		if sess.RetryCount < prevRetryCount {
			sess.RetryCount = prevRetryCount
		}
		// spec: §7.3 line 397 — sessions.last_seq is monotonic. The
		// GREATEST in updateSQL is the cross-replica floor; this
		// callback-level guard keeps the in-memory copy consistent so
		// the returned Session never reports a rewound LastSeq even if
		// the mutate callback explicitly zeroes it. F-7.3.3.
		if sess.LastSeq < prevLastSeq {
			sess.LastSeq = prevLastSeq
		}
		sess.UpdatedAt = pgtenant.MonotonicNext(prevUpdated, time.Now())
		ref, src, at, hash := snapshotCols(sess.WorkspaceSnapshot)
		expID, expVariant, expInherited := experimentCols(sess.ExperimentContext)
		schemaVersion := sess.SchemaVersion
		if schemaVersion == 0 {
			schemaVersion = 1
		}
		if _, err := tx.Exec(
			ctx, updateSQL,
			id, tenantID, sess.UserID, string(sess.State), sess.RuntimeRef,
			sess.PoolRef, string(sess.IsolationProfile), sess.ParentSessionID,
			string(sess.FailureClass), sess.FailureReason, ref, src, pgtenant.NullTime(at),
			sess.ParentWorkspaceRef, pgtenant.NullTime(sess.RetentionExpiresAt),
			sess.UploadTokenDigest, pgtenant.NullTime(sess.UploadTokenExpiry), sess.UpdatedAt,
			sess.LegalHold, expID, expVariant, expInherited, sess.Environment,
			sess.Cwd, sess.PodAssignment, sess.RecoveryGeneration,
			sess.CoordinationGeneration, schemaVersion,
			sess.RetryCount, policyEnforcementArg(sess.PolicyEnforcementState),
			pgtenant.NullTime(sess.ResumeEligibleUntil),
			pgtenant.NullTime(sess.LastSuccessfulCheckpointAt),
			hash,
			sess.ExecutionMode, sess.ScrubPolicy,
			metadataArg(sess.Metadata),
			retryPolicyArg(sess.RetryPolicy),
			workspaceBytesArg(sess.WorkspaceSnapshot),
			sess.LastSeq,
			sess.WorkspaceRoot,
			tracingContextArg(sess.TracingContext),
			string(sess.CascadeOnFailure),
			// $43 — §8.5 tree_visibility; see Create. F-8.5.2 / F-8.9.2.
			string(sess.TreeVisibility),
			// $44 — §14 client-supplied env map; see Create. F-14.1.12.
			envArg(sess.Env),
			// $45 — §14.1 request envelope bundle; see Create. F-14.1.14.
			requestEnvelopeArg(sess),
			// $46-$48 — §15.1 line 865 legal-hold provenance (setBy, setAt,
			// note) reported by GET /v1/admin/legal-holds.
			sess.LegalHoldSetBy, pgtenant.NullTime(sess.LegalHoldSetAt), sess.LegalHoldNote,
			// $49 — §6.2 lines 273-300 idle-timer anchor; the activity
			// stamper advances it ≤1/s on qualifying agent events. NULL
			// until the first event. F-11.3.7.
			pgtenant.NullTime(sess.LastAgentActivityAt),
			// $50 — §7.1 line 74 conversation_continuity envelope half; see
			// Create. spec: §7.1 line 74.
			sess.ConversationContinuity,
			// $51-$52 — §7.2 / §8.8 Terminated session-condition fact; the
			// terminal-disposition writer (S27/S30) stamps terminated_at and
			// terminated_reason when the session reaches a terminal state.
			// terminated_at passes through NullTime so a non-terminal row
			// keeps SQL NULL. spec: §6.49; §7.2 line 230; §8.8.
			pgtenant.NullTime(sess.TerminatedAt), sess.TerminatedReason,
			// $53-$54 — §7.2 interrupt-suspension Suspended condition fact;
			// stamped when the session enters `suspended`, NULL otherwise.
			// spec: §6.49; §7.2 line 230; §8.8.
			pgtenant.NullTime(sess.SuspendedAt), sess.SuspendedReason,
		); err != nil {
			return err
		}
		out = sess
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, normalizeMiss(err)
	}
	return out, nil
}

// List returns the tenant's sessions newest-first, narrowed by filter.
func (s *Store) List(ctx context.Context, tenantID string, filter sessionstore.ListFilter) ([]sessionstore.Session, error) {
	q := `SELECT ` + selectList + ` FROM sessions WHERE tenant_id = $1`
	args := []any{tenantID}
	if filter.State != "" {
		args = append(args, string(filter.State))
		q += fmt.Sprintf(" AND state = $%d", len(args))
	}
	if filter.RuntimeRef != "" {
		args = append(args, filter.RuntimeRef)
		q += fmt.Sprintf(" AND runtime_ref = $%d", len(args))
	}
	if filter.FailureClass != "" {
		args = append(args, string(filter.FailureClass))
		q += fmt.Sprintf(" AND failure_class = $%d", len(args))
	}
	// spec: §11.4 line 256 — full_revoke step 1 narrows to the
	// invalidation subject. Pushes the filter to SQL so a tenant with
	// many sessions does not scan tenant-wide.
	if filter.UserID != "" {
		args = append(args, filter.UserID)
		q += fmt.Sprintf(" AND user_id = $%d", len(args))
	}
	// spec: §15.1 line 598 — labels filter is AND-containment over the
	// §14 Labels map, which rides the request_envelope JSONB bundle under
	// the `labels` key. The `@>` containment matches a row whose stored
	// labels include every requested pair. F-15.1.15.
	if len(filter.Labels) > 0 {
		want, err := json.Marshal(filter.Labels)
		if err == nil {
			args = append(args, string(want))
			q += fmt.Sprintf(" AND request_envelope -> 'labels' @> $%d::jsonb", len(args))
		}
	}
	// spec: §15.1 lines 652, 661 — `?includeDeriveFailures=false` drops
	// the audit-only derive_failure rows. F-15.1.14.
	if filter.ExcludeDeriveFailures {
		args = append(args, string(session.FailureClassDeriveFailure))
		q += fmt.Sprintf(" AND failure_class IS DISTINCT FROM $%d", len(args))
	}
	q += ` ORDER BY created_at DESC, id`

	var out []sessionstore.Session
	// spec: §12.3 line 146 — session-status listing routes to the read replica.
	err := pgtenant.InTx(ctx, s.read, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			sess, err := scanSession(rows)
			if err != nil {
				return err
			}
			out = append(out, sess)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListByRoot implements Store — every row whose root_session_id equals
// rootSessionID within tenantID, ordered by created_at ascending so a
// caller can rebuild the §8.9 tree by walking ParentSessionID. Uses the
// `idx_sessions_root` index (§12.5 line 101) for an O(tree size)
// projection instead of the O(tenant size) List path. An empty
// rootSessionID returns no rows. spec: §8.9 line 1010; §12.5 line 101.
// F-8.9.7.
func (s *Store) ListByRoot(ctx context.Context, tenantID, rootSessionID string) ([]sessionstore.Session, error) {
	if rootSessionID == "" {
		return nil, nil
	}
	q := `SELECT ` + selectList + ` FROM sessions WHERE tenant_id = $1
		AND root_session_id = $2::uuid
		ORDER BY created_at ASC, id`
	var out []sessionstore.Session
	// spec: §12.3 line 146 — task-tree projection routes to the read replica.
	err := pgtenant.InTx(ctx, s.read, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, tenantID, rootSessionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			sess, err := scanSession(rows)
			if err != nil {
				return err
			}
			out = append(out, sess)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the session row. session_messages rows cascade.
func (s *Store) Delete(ctx context.Context, tenantID, id string) error {
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM sessions WHERE id = $1::uuid AND tenant_id = $2`, id, tenantID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return sessionstore.ErrNotFound
		}
		return nil
	})
	return normalizeMiss(err)
}

// DeleteByUser implements Store — the §12.8 GDPR-erasure adapter. It
// removes every session owned by userID within tenantID and returns
// the count deleted; a user with no sessions yields (0, nil).
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	deleted := 0
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM sessions WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		return nil
	})
	return deleted, err
}

// DeleteByTenant implements Store — the §12.1 / §12.8 Phase 4
// tenant-deletion erasure adapter. Removes every session row
// belonging to tenantID and returns the count deleted; a tenant with
// no sessions yields (0, nil).
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("sessionstore: DeleteByTenant requires a concrete tenant_id")
	}
	deleted := 0
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM sessions WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		return nil
	})
	return deleted, err
}

// GetActiveSlotsByPod implements Store — the §5.2 rehydration seed
// source. It counts live (non-terminal) sessions bound to podID. The
// read runs cross-tenant (InAllTenants) because the rehydration path
// holds only the pod identity; §5.2 tenant pinning guarantees every
// counted row belongs to one tenant. The partial index from migration
// 0080 keeps the count under the 5ms target the spec assumes during a
// post-restart rehydration burst. The non-terminal predicate matches
// the index predicate and session.TerminalStates() verbatim.
// spec: §5.2 line 521 ("GetActiveSlotsByPod ... indexed ... returns at
// most maxConcurrent rows").
func (s *Store) GetActiveSlotsByPod(ctx context.Context, podID string) (int, error) {
	if podID == "" {
		return 0, nil
	}
	var count int
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sessions
			   WHERE pod_assignment = $1
			     AND state NOT IN ('completed', 'failed', 'cancelled', 'expired')`,
			podID).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("pgstore: count active slots for pod %s: %w", podID, err)
	}
	return count, nil
}

// ReserveSlotUnderLock implements Store — the §12.4 line 208 Redis-outage
// capacity gate. The count-and-decide runs inside one transaction holding a
// per-pod `pg_advisory_xact_lock`, so two concurrent admissions for the same
// pod during a Redis outage serialize: the second waits for the first to
// commit (releasing the xact lock) before reading the count, and cannot
// observe the same free slot. The lock key is a 64-bit hash of podID; the
// xact-scoped form releases automatically at commit or rollback, so a
// crashed gateway never strands the lock. The count predicate matches
// GetActiveSlotsByPod (and session.TerminalStates()) verbatim so the gate
// reads the same source the blocking-rehydration protocol reads. The
// returned post-admission count is the observed count plus one; the caller
// (slotcounter) does not write the Postgres row — the slot occupancy is
// recorded when the session row's pod_assignment is persisted on bind.
// spec: §12.4 line 208 (Postgres fallback under a per-pod advisory lock);
// §5.2 line 541 (intra-pod capacity gate during a Redis outage).
func (s *Store) ReserveSlotUnderLock(ctx context.Context, podID string, maxConcurrent int32) (int32, bool, error) {
	if podID == "" {
		return 0, false, nil
	}
	if maxConcurrent < 1 {
		return 0, false, fmt.Errorf("pgstore: maxConcurrent must be >= 1, got %d", maxConcurrent)
	}
	var count int32
	var admitted bool
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		// Per-pod advisory lock scoped to this transaction. The lock key is a
		// stable 64-bit hash of the pod identifier so distinct pods never
		// contend; hashtext returns a 32-bit value, widened to the bigint the
		// single-argument advisory-lock form expects.
		if _, err := tx.Exec(ctx,
			"SELECT pg_advisory_xact_lock(hashtext($1)::bigint)", podID); err != nil {
			return fmt.Errorf("pgstore: acquire per-pod advisory lock for %s: %w", podID, err)
		}
		var current int32
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM sessions
			   WHERE pod_assignment = $1
			     AND state NOT IN ('completed', 'failed', 'cancelled', 'expired')`,
			podID).Scan(&current); err != nil {
			return fmt.Errorf("pgstore: count active slots under lock for pod %s: %w", podID, err)
		}
		if current >= maxConcurrent {
			count = current
			admitted = false
			return nil
		}
		count = current + 1
		admitted = true
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return count, admitted, nil
}

// PoolDrainStats implements Store — the §15.1 line 797 pool-drain
// accounting. It counts live (non-terminal) sessions bound to poolRef
// across every tenant and reports the oldest created_at among them so
// the drain handler can derive the activeSessions count and the
// Retry-After estimate. The query is pool-scoped rather than
// tenant-scoped because drain is a platform-global pool operation; it
// runs InAllTenants like GetActiveSlotsByPod. The non-terminal predicate
// matches session.TerminalStates() verbatim. A pool with no live
// sessions returns (0, time.Time{}, nil).
// spec: §15.1 line 797.
func (s *Store) PoolDrainStats(ctx context.Context, poolRef string) (int, time.Time, error) {
	if poolRef == "" {
		return 0, time.Time{}, nil
	}
	var count int
	var oldest *time.Time
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*), min(created_at) FROM sessions
			   WHERE pool_ref = $1
			     AND state NOT IN ('completed', 'failed', 'cancelled', 'expired')`,
			poolRef).Scan(&count, &oldest)
	})
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("pgstore: pool drain stats for %s: %w", poolRef, err)
	}
	if oldest == nil {
		return count, time.Time{}, nil
	}
	return count, *oldest, nil
}

// CountActiveSessions implements Store — the §11.2 per-tenant
// concurrent-session quota count. It counts the tenant's live
// (non-terminal) sessions with a COUNT query so the gateway's quota
// check does not materialize every historical row. The partial index
// from migration 0102 (sessions(tenant_id) WHERE state NOT IN terminal)
// keeps the count cheap on a tenant with a large session history. The
// non-terminal predicate matches the index predicate and
// session.TerminalStates() verbatim.
// spec: §11.2 (per-tenant concurrent-session quota with hard rejection).
func (s *Store) CountActiveSessions(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	var count int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sessions
			   WHERE tenant_id = $1
			     AND state NOT IN ('completed', 'failed', 'cancelled', 'expired')`,
			tenantID).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("pgstore: count active sessions for tenant %s: %w", tenantID, err)
	}
	return count, nil
}

// CountActiveSessionsByUser implements Store — the §11.1 per-user
// concurrent-session admission count. It counts the user's live
// (non-terminal) sessions with a COUNT query, scoped to the tenant via
// RLS. The non-terminal predicate matches session.TerminalStates()
// verbatim. spec: §11.1 line 8 (Concurrency limits — per-user).
func (s *Store) CountActiveSessionsByUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, nil
	}
	var count int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sessions
			   WHERE tenant_id = $1
			     AND user_id = $2
			     AND state NOT IN ('completed', 'failed', 'cancelled', 'expired')`,
			tenantID, userID).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("pgstore: count active sessions for user %s/%s: %w", tenantID, userID, err)
	}
	return count, nil
}

// CountActiveSessionsByRuntime implements Store — the §11.1 per-runtime
// concurrent-session admission count. It counts live (non-terminal)
// sessions targeting runtimeRef within the tenant. The non-terminal
// predicate matches session.TerminalStates() verbatim.
// spec: §11.1 line 8 (Concurrency limits — per-runtime).
func (s *Store) CountActiveSessionsByRuntime(ctx context.Context, tenantID, runtimeRef string) (int, error) {
	if tenantID == "" || runtimeRef == "" {
		return 0, nil
	}
	var count int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sessions
			   WHERE tenant_id = $1
			     AND runtime_ref = $2
			     AND state NOT IN ('completed', 'failed', 'cancelled', 'expired')`,
			tenantID, runtimeRef).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("pgstore: count active sessions for runtime %s/%s: %w", tenantID, runtimeRef, err)
	}
	return count, nil
}

// CountActiveSessionsGlobal implements Store — the §11.1 global
// concurrent-session admission count. It counts every live
// (non-terminal) session across all tenants (InAllTenants) so the
// gateway-wide ceiling bounds total live sessions. The non-terminal
// predicate matches session.TerminalStates() verbatim.
// spec: §11.1 line 8 (Concurrency limits — global).
func (s *Store) CountActiveSessionsGlobal(ctx context.Context) (int, error) {
	var count int
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sessions
			   WHERE state NOT IN ('completed', 'failed', 'cancelled', 'expired')`).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("pgstore: count active sessions global: %w", err)
	}
	return count, nil
}

// CountActiveSessionsInRecoveryGlobal implements Store — the §16.5
// Session availability SLI numerator. It counts live sessions across
// every tenant in a retry/recovery state; the recovery predicate matches
// session.RecoveryStates() verbatim.
// spec: §16.5 line 616 (Session availability SLO). F-16.5.3.
func (s *Store) CountActiveSessionsInRecoveryGlobal(ctx context.Context) (int, error) {
	var count int
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sessions
			   WHERE state IN ('resume_pending', 'resuming', 'awaiting_client_action')`).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("pgstore: count active sessions in recovery global: %w", err)
	}
	return count, nil
}

// CountActiveDelegatedChildrenByUser implements Store — the §11.1
// per-user active-delegated-children admission count. It counts live
// (non-terminal) sessions owned by userID within the tenant that carry
// a non-empty parent_session_id (delegated children). The non-terminal
// predicate matches session.TerminalStates() verbatim.
// spec: §11.1 line 9 (Active delegated children — per-user).
func (s *Store) CountActiveDelegatedChildrenByUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, nil
	}
	var count int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sessions
			   WHERE tenant_id = $1
			     AND user_id = $2
			     AND parent_session_id IS NOT NULL
			     AND state NOT IN ('completed', 'failed', 'cancelled', 'expired')`,
			tenantID, userID).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("pgstore: count active delegated children for user %s/%s: %w", tenantID, userID, err)
	}
	return count, nil
}

// scanSession reads one row in selectList order into a Session.
func scanSession(row pgx.Row) (sessionstore.Session, error) {
	var (
		s                       sessionstore.Session
		state, isoProf, failCls string
		wsRef, wsSrc            string
		wsAt, retAt, upExp      *time.Time
		planJSON                []byte
		expID, expVariant       string
		expInherited            bool
		// §4.2 line 158-159 retry / policy / resume fields from
		// migration 0055.
		policyJSON  []byte
		resumeUntil *time.Time
		// §4.4 line 258 freshness timestamp from migration 0061.
		lastCheckpointAt *time.Time
		// §4.5 line 311 content-addressed snapshot hash from
		// migration 0068.
		wsHash string
		// §7.2 / §8.8 session-condition timestamps from migration 0168.
		// Nullable: NULL means the condition has not fired (the session is
		// neither terminal nor suspended). spec: §6.49; §7.2 line 230; §8.8.
		terminatedAt *time.Time
		suspendedAt  *time.Time
		// §7.1 line 6 client-supplied metadata payload from migration
		// 0086 (F-7.3.20).
		metadataJSON []byte
		// §7.3 client-supplied retry policy and
		// last_checkpoint_workspace_bytes columns from migration 0087
		// (F-7.3.1 / F-7.3.21).
		retryPolicyJSON     []byte
		lastCheckpointBytes *int64
		// §8.2 line 52 delegation tracing_context and §8.3 line 266 /
		// §8.10 cascade_on_failure lease policy from migration 0090
		// (F-8.2.14 / F-8.2.15).
		tracingContextJSON []byte
		cascadeOnFailure   string
		// §8.5 tree_visibility visibility boundary from migration 0094
		// (F-8.5.2 / F-8.9.2).
		treeVisibility string
		// §10.7 delegation_depth from migration 0095 (F-10.7.5).
		delegationDepth int
		// §8.2 delegation_lease granted lease_slice from migration 0099
		// (F-8.2.2).
		delegationLeaseJSON []byte
		// §14 client-supplied env map and the §14.1 request envelope
		// bundle from migration 0109 (F-14.1.12 / F-14.1.14).
		envJSON             []byte
		requestEnvelopeJSON []byte
		// §15.1 line 865 legal-hold provenance from migration 0145.
		// legal_hold_set_at is nullable (no hold → SQL NULL).
		legalHoldSetAt *time.Time
		// §6.2 lines 273-300 idle-timer anchor from migration 0159.
		// Nullable (NULL until the first qualifying agent event). F-11.3.7.
		lastAgentActivityAt *time.Time
	)
	if err := row.Scan(
		&s.ID, &s.TenantID, &s.UserID, &state, &s.RuntimeRef, &s.PoolRef,
		&isoProf, &s.ParentSessionID, &failCls, &s.FailureReason,
		&wsRef, &wsSrc, &wsAt, &s.ParentWorkspaceRef, &retAt,
		&s.UploadTokenDigest, &upExp, &s.CreatedAt, &s.UpdatedAt, &planJSON,
		&s.LegalHold, &expID, &expVariant, &expInherited, &s.Environment,
		// §4.2 session-record fields from migration 0050.
		// spec: §4.2 line 156.
		&s.Cwd, &s.PodAssignment, &s.RecoveryGeneration,
		&s.CoordinationGeneration, &s.SchemaVersion,
		// §4.2 line 158-159 retry / policy / resume fields from
		// migration 0055.
		&s.RetryCount, &policyJSON, &resumeUntil,
		// §4.4 freshness timestamp from migration 0061.
		// spec: §4.4 line 258.
		&lastCheckpointAt,
		// §4.5 line 311 content-addressed snapshot hash from
		// migration 0068.
		&wsHash,
		// §7.1 line 75 sessionIsolationLevel halves from migration 0084.
		&s.ExecutionMode, &s.ScrubPolicy,
		// §7.1 line 74 conversation_continuity envelope half and the
		// §7.2 / §8.8 Terminated/Suspended session-condition facts from
		// migration 0168. The nullable timestamps scan into pointers and
		// map back to the zero time when NULL. spec: §6.49; §7.1 line 74;
		// §7.2 line 230; §8.8.
		&s.ConversationContinuity,
		&terminatedAt, &s.TerminatedReason,
		&suspendedAt, &s.SuspendedReason,
		// §7.1 line 6 client metadata from migration 0086 (F-7.3.20).
		&metadataJSON,
		// §7.3 retry_policy + last_checkpoint_workspace_bytes from
		// migration 0087 (F-7.3.1 / F-7.3.21).
		&retryPolicyJSON, &lastCheckpointBytes,
		// §7.3 line 397 last_seq durable counter from migration 0088
		// (F-7.3.3).
		&s.LastSeq,
		// §7.3 line 408 workspace_root recorded at first bind from
		// migration 0089 (F-7.3.15).
		&s.WorkspaceRoot,
		// §8.2 line 52 / §8.10 — tracing_context + cascade_on_failure
		// from migration 0090 (F-8.2.14 / F-8.2.15).
		&tracingContextJSON, &cascadeOnFailure,
		// §8.9 line 1010 — root_session_id surfaces the delegation-tree
		// apex on every row in the tree so a single-shard query can
		// rebuild the §8.9 tree without walking ParentSessionID. F-8.9.8.
		&s.RootSessionID,
		// §8.5 line 540 — tree_visibility scopes get_task_tree for this
		// session; from migration 0094 (F-8.5.2 / F-8.9.2).
		&treeVisibility,
		// §10.7 line 905 — delegation_depth feeds the eval_results copy
		// the Results API filters on; from migration 0095 (F-10.7.5).
		&delegationDepth,
		// §8.2 lines 38-48 — delegation_lease carries the granted
		// lease_slice so a descendant's slice validates against the
		// ancestor ceiling; from migration 0099 (F-8.2.2).
		&delegationLeaseJSON,
		// §14 / §14.1 — env map + request envelope bundle from
		// migration 0109 (F-14.1.12 / F-14.1.14).
		&envJSON, &requestEnvelopeJSON,
		// §15.1 line 865 — legal-hold provenance from migration 0145.
		&s.LegalHoldSetBy, &legalHoldSetAt, &s.LegalHoldNote,
		// §6.2 lines 273-300 — idle-timer anchor from migration 0159
		// (F-11.3.7). NULL → the watchdog falls back to updated_at.
		&lastAgentActivityAt,
		// §8.3 line 472 / 488 — credential_origin_session_id from
		// migration 0176. COALESCEd to '' when NULL so a self-origin row
		// scans as empty, matching the memstore convention. The finalize
		// path treats an empty or self value as "not inherited".
		&s.CredentialOriginSessionID,
	); err != nil {
		return sessionstore.Session{}, err
	}
	if legalHoldSetAt != nil {
		s.LegalHoldSetAt = *legalHoldSetAt
	}
	if lastAgentActivityAt != nil {
		s.LastAgentActivityAt = *lastAgentActivityAt
	}
	// spec: §6.49 / §7.2 line 230 / §8.8 — a NULL terminated_at /
	// suspended_at means the session-condition has not fired, mapped to
	// the zero time so the in-memory Session matches the memstore "not
	// fired" sentinel. The reason strings scan directly into the struct.
	if terminatedAt != nil {
		s.TerminatedAt = *terminatedAt
	}
	if suspendedAt != nil {
		s.SuspendedAt = *suspendedAt
	}
	// spec: §14 lines 47-50 — decode the client-supplied env map. A
	// nil/empty payload leaves Env nil so the read envelope omits it.
	// F-14.1.12.
	if len(envJSON) > 0 {
		if env, err := decodeMetadata(envJSON); err == nil && len(env) > 0 {
			s.Env = env
		}
	}
	// spec: §14.1 — decode the request envelope bundle back into the
	// distinct Session fields. A nil/empty payload leaves all of them
	// unset. F-14.1.14 / F-14.1.15.
	if len(requestEnvelopeJSON) > 0 {
		applyStoredEnvelope(&s, requestEnvelopeJSON)
	}
	s.DelegationDepth = uint32(delegationDepth)
	// spec: §8.2 lines 38-48 — decode the granted lease_slice. A
	// nil/empty payload leaves DelegationLease nil, matching the
	// "no explicit budget binding" case (root/standalone or a child
	// whose lease declared no slice). F-8.2.2.
	if len(delegationLeaseJSON) > 0 {
		var dl sessionstore.DelegationLease
		if err := json.Unmarshal(delegationLeaseJSON, &dl); err == nil && !dl.IsZero() {
			s.DelegationLease = &dl
		}
	}
	if len(policyJSON) > 0 {
		s.PolicyEnforcementState = policyJSON
	}
	if len(metadataJSON) > 0 {
		if md, err := decodeMetadata(metadataJSON); err == nil && len(md) > 0 {
			s.Metadata = md
		}
	}
	if len(retryPolicyJSON) > 0 {
		if rp, err := decodeRetryPolicy(retryPolicyJSON); err == nil && rp != nil {
			s.RetryPolicy = rp
		}
	}
	// spec: §8.2 line 52 / §8.3 line 286 — decode the delegation
	// tracing_context back into the in-memory string→string map so a
	// Postgres-backed reload returns the same context the parent
	// registered. A nil/empty payload yields a nil map, matching the
	// "no context registered" case the in-memory store models. F-8.2.14.
	if len(tracingContextJSON) > 0 {
		if tc, err := decodeTracingContext(tracingContextJSON); err == nil && len(tc) > 0 {
			s.TracingContext = tc
		}
	}
	// spec: §8.3 line 266 / §8.10 — cascade_on_failure persisted as TEXT;
	// the empty string preserves the in-Go convention "empty resolves to
	// the §8.10 default (cancel_all)". F-8.2.15.
	if cascadeOnFailure != "" {
		s.CascadeOnFailure = session.CascadePolicy(cascadeOnFailure)
	}
	// spec: §8.5 line 540 — tree_visibility persisted raw; the empty
	// string preserves the in-Go "resolve to default full" convention
	// (TreeVisibility.OrDefault). F-8.5.2 / F-8.9.2.
	s.TreeVisibility = session.TreeVisibility(treeVisibility)
	if resumeUntil != nil {
		s.ResumeEligibleUntil = *resumeUntil
	}
	if lastCheckpointAt != nil {
		s.LastSuccessfulCheckpointAt = *lastCheckpointAt
	}
	if expID != "" {
		s.ExperimentContext = &sessionstore.ExperimentContext{
			ExperimentID: expID, VariantID: expVariant, Inherited: expInherited,
		}
	}
	if len(planJSON) > 0 {
		s.WorkspacePlan = planJSON
	}
	s.State = session.State(state)
	s.IsolationProfile = isolation.Profile(isoProf)
	s.FailureClass = session.FailureClass(failCls)
	if retAt != nil {
		s.RetentionExpiresAt = *retAt
	}
	if upExp != nil {
		s.UploadTokenExpiry = *upExp
	}
	if wsRef != "" || wsSrc != "" || wsAt != nil || wsHash != "" || (lastCheckpointBytes != nil && *lastCheckpointBytes > 0) {
		ws := &sessionstore.WorkspaceSnapshot{
			Ref:         wsRef,
			Source:      sessionstore.WorkspaceSnapshotSource(wsSrc),
			ContentHash: wsHash,
		}
		if wsAt != nil {
			ws.Timestamp = *wsAt
		}
		if lastCheckpointBytes != nil {
			ws.Bytes = *lastCheckpointBytes
		}
		s.WorkspaceSnapshot = ws
	}
	return s, nil
}

// snapshotCols flattens an optional WorkspaceSnapshot into its four
// column values: ref, source, timestamp, and the §4.5 line 311
// content-addressed hash.
func snapshotCols(ws *sessionstore.WorkspaceSnapshot) (ref, src string, at time.Time, hash string) {
	if ws == nil {
		return "", "", time.Time{}, ""
	}
	return ws.Ref, string(ws.Source), ws.Timestamp, ws.ContentHash
}

// experimentCols flattens an optional §10.7 ExperimentContext into its
// three column values. A nil context stores empty strings, which the
// scan path reads back as a nil context.
func experimentCols(ec *sessionstore.ExperimentContext) (id, variantID string, inherited bool) {
	if ec == nil {
		return "", "", false
	}
	return ec.ExperimentID, ec.VariantID, ec.Inherited
}

// jsonbArg renders a raw §14 WorkspacePlan as a jsonb query argument.
// An empty document becomes a SQL NULL so the column stays null for a
// session created without a workspace plan.
func jsonbArg(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// delegationLeaseArg renders the §8.2 granted lease_slice as a jsonb
// query argument. A nil or all-zero lease imposes no budget binding and
// is stored as SQL NULL so the read path returns DelegationLease nil.
// F-8.2.2.
func delegationLeaseArg(l *sessionstore.DelegationLease) any {
	if l.IsZero() {
		return nil
	}
	b, err := json.Marshal(l)
	if err != nil {
		return nil
	}
	return string(b)
}

// policyEnforcementArg renders the §4.2 line 158 policy enforcement
// state as a jsonb argument. The column carries a NOT NULL DEFAULT
// of '{}'::jsonb, so an empty payload falls through to the spec
// default rather than the SQL NULL the workspace-plan column uses.
// spec: §4.2 line 158 — "Retry counters and policy enforcement".
func policyEnforcementArg(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

// metadataArg renders the §7.1 line 6 client-supplied metadata map as
// a jsonb argument. A nil or empty map stores SQL NULL so the read
// path can distinguish "client supplied nothing" from "client supplied
// {}" — the §15.1 GET envelope omits the field in both cases. F-7.3.20.
func metadataArg(md map[string]string) any {
	if len(md) == 0 {
		return nil
	}
	b, err := json.Marshal(md)
	if err != nil {
		return nil
	}
	return string(b)
}

// envArg renders the §14 client-supplied env map as a jsonb argument.
// A nil or empty map stores SQL NULL so the read path distinguishes
// "client supplied no env" from "client supplied {}" — the §15.1 GET
// envelope omits the field in both cases. F-14.1.12.
func envArg(env map[string]string) any {
	return metadataArg(env)
}

// storedEnvelope is the on-disk shape of the §14.1 request-envelope
// bundle: the outer-envelope fields that do not need their own column or
// index (pool, timeouts, credentialPolicy, delegationLease,
// runtimeOptions). Bundling them into one JSONB column keeps the §14.1
// surface to a single migration column. spec: §14.1 line 311. F-14.1.14.
type storedEnvelope struct {
	Pool             string                                 `json:"pool,omitempty"`
	Timeouts         *sessionstore.SessionTimeouts          `json:"timeouts,omitempty"`
	CredentialPolicy *sessionstore.CredentialPolicyOverride `json:"credentialPolicy,omitempty"`
	DelegationLease  *sessionstore.DelegationLeaseRequest   `json:"delegationLease,omitempty"`
	RuntimeOptions   json.RawMessage                        `json:"runtimeOptions,omitempty"`
	// Origin carries the §27.3 origin=playground session label. It rides
	// the envelope bundle rather than a dedicated column because v1 has no
	// §25.9 column-filtered audit query yet (F-25.9.2); persisting it here
	// keeps the label durable across replicas without a migration.
	// F-27.6.8.
	Origin string `json:"origin,omitempty"`
	// Labels carries the §14 line 311 client-supplied session labels. They
	// ride the envelope bundle so the §15.1 list label filter
	// (`request_envelope->'labels' @> $n`) has a durable, indexable target
	// without a dedicated column. spec: §14 line 311; §15.1 line 598.
	// F-15.1.15.
	Labels map[string]string `json:"labels,omitempty"`
	// Callback carries the §14 session-terminal webhook fields: the
	// validated callbackUrl, its DNS-pinned IP, the KMS-envelope-sealed
	// callbackSecret (opaque ciphertext), and any undelivered events. They
	// ride the bundle so the §14 callback survives a coordinator handoff
	// without a dedicated migration column; the sealed secret stays opaque
	// to the lenny_app role exactly as a dedicated ciphertext column would.
	// spec: §14 lines 108-150. F-14.1.11.
	CallbackURL      string                            `json:"callbackUrl,omitempty"`
	CallbackPinnedIP string                            `json:"callbackPinnedIp,omitempty"`
	CallbackSecret   []byte                            `json:"callbackSecret,omitempty"`
	WebhookEvents    []sessionstore.WebhookEventRecord `json:"webhookEvents,omitempty"`
}

// requestEnvelopeArg renders the §14.1 request-envelope bundle for a
// session as a jsonb argument. When the session carries none of the
// bundled fields the column stays SQL NULL so the read path leaves every
// bundled field unset. F-14.1.14.
func requestEnvelopeArg(sess sessionstore.Session) any {
	env := storedEnvelope{
		Pool:             sess.Pool,
		Timeouts:         sess.Timeouts,
		CredentialPolicy: sess.CredentialPolicyOverride,
		DelegationLease:  sess.DelegationLeaseRequest,
		RuntimeOptions:   sess.RuntimeOptions,
		Origin:           sess.Origin,
		Labels:           sess.Labels,
		CallbackURL:      sess.CallbackURL,
		CallbackPinnedIP: sess.CallbackPinnedIP,
		CallbackSecret:   sess.CallbackSecret,
		WebhookEvents:    sess.WebhookEvents,
	}
	if env.Pool == "" && env.Timeouts == nil && env.CredentialPolicy == nil &&
		env.DelegationLease == nil && len(env.RuntimeOptions) == 0 && env.Origin == "" &&
		len(env.Labels) == 0 && env.CallbackURL == "" && env.CallbackPinnedIP == "" &&
		len(env.CallbackSecret) == 0 && len(env.WebhookEvents) == 0 {
		return nil
	}
	b, err := json.Marshal(env)
	if err != nil {
		return nil
	}
	return string(b)
}

// applyStoredEnvelope decodes the §14.1 request-envelope bundle and
// distributes it back onto the Session's distinct fields. A malformed
// payload is ignored (the gateway validates the bundle at admission, so
// a stored value is by-construction well-formed). F-14.1.14.
func applyStoredEnvelope(s *sessionstore.Session, raw []byte) {
	var env storedEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return
	}
	s.Pool = env.Pool
	s.Timeouts = env.Timeouts
	s.CredentialPolicyOverride = env.CredentialPolicy
	s.DelegationLeaseRequest = env.DelegationLease
	if len(env.RuntimeOptions) > 0 {
		s.RuntimeOptions = env.RuntimeOptions
	}
	s.Origin = env.Origin
	if len(env.Labels) > 0 {
		s.Labels = env.Labels
	}
	s.CallbackURL = env.CallbackURL
	s.CallbackPinnedIP = env.CallbackPinnedIP
	s.CallbackSecret = env.CallbackSecret
	s.WebhookEvents = env.WebhookEvents
}

// decodeMetadata parses the JSONB payload back into a string→string
// map. A malformed payload returns an error so the caller can surface
// the corruption; in practice the gateway's decode boundary rejects
// non-string values before they reach Postgres. F-7.3.20.
func decodeMetadata(raw []byte) (map[string]string, error) {
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// retryPolicyArg renders the §7.3 RetryPolicy as a JSONB argument. A
// nil pointer stores SQL NULL so the read path can distinguish "client
// supplied no override" from "client supplied an explicit object". An
// unmarshalable payload (the in-memory struct is by-construction
// well-formed, but defensive) stores NULL rather than fail the write.
// spec: §7.3 lines 377-393.
func retryPolicyArg(p *session.RetryPolicy) any {
	if p == nil {
		return nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return string(b)
}

// decodeRetryPolicy parses the JSONB payload back into a RetryPolicy.
// A malformed payload returns an error so the caller can surface the
// corruption; in practice the gateway writes only ClampRetryPolicy
// output, so the on-row payload is well-formed by construction.
// spec: §7.3 lines 377-393.
func decodeRetryPolicy(raw []byte) (*session.RetryPolicy, error) {
	var out session.RetryPolicy
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// workspaceBytesArg surfaces the §7.3 line 397
// last_checkpoint_workspace_bytes column value from the snapshot. A nil
// snapshot or a zero size stores SQL NULL so the read path treats
// "never checkpointed" the same as "no size reported", matching the
// §10.1 preStop tiered-cap fallback rule. spec: §7.3 line 397; §10.1.
func workspaceBytesArg(ws *sessionstore.WorkspaceSnapshot) any {
	if ws == nil || ws.Bytes <= 0 {
		return nil
	}
	return ws.Bytes
}

// tracingContextArg renders the §8.2 line 52 / §8.3 line 286 delegation
// tracingContext map as a JSONB argument. A nil or empty map stores SQL
// NULL so the read path returns nil (the "no context registered" case)
// rather than the empty map. F-8.2.14.
// spec: §8.2 line 52, §8.3 line 286.
func tracingContextArg(tc map[string]string) any {
	if len(tc) == 0 {
		return nil
	}
	b, err := json.Marshal(tc)
	if err != nil {
		return nil
	}
	return string(b)
}

// decodeTracingContext parses the JSONB payload back into the in-memory
// string→string map. The gateway writes only well-formed values (the map
// originates as a Go map[string]string via lenny/set_tracing_context), so
// a malformed payload returns an error the caller can surface. F-8.2.14.
// spec: §8.2 line 52.
func decodeTracingContext(raw []byte) (map[string]string, error) {
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// normalizeMiss maps pgx.ErrNoRows and the invalid-UUID-text error to
// the store's ErrNotFound: a malformed or absent id is, from the
// caller's view, simply "no such session".
func normalizeMiss(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionstore.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return sessionstore.ErrNotFound
	}
	return err
}
