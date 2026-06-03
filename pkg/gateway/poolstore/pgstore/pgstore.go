// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed poolstore.Store, persisting
// the §5.2 SandboxWarmPool registry to the sandbox_warm_pools table.
// It is a drop-in alternative to poolstore.Memory.
//
// sandbox_warm_pools is platform-global (§5.1): pools are
// platform-wide records keyed by name, so operations run as plain
// queries without an app.current_tenant context. Create and Update
// run the same §5.2 / §5.3 validation as poolstore.Memory so the
// warm-count, session-age, and standard-isolation guards hold
// regardless of backend.
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

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/egress"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Store is the Postgres-backed pool registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ poolstore.Store = (*Store)(nil)

const selectList = `name, runtime_ref, isolation_profile, execution_mode,
	resource_class, warm_count, max_session_age_seconds,
	allow_standard_isolation, concurrency_style, max_concurrent,
	acknowledge_process_level_isolation, cleanup_timeout_seconds,
	allow_cross_tenant_reuse, egress_profile, created_at, updated_at, deleted_at,
	pool_config_generation, task_policy, elicitation_policy, draining_since`

// validatePool runs the §5.2 / §5.3 invariants poolstore.Memory
// enforces on Create and after Update's mutate. The error strings
// match poolstore.Memory so callers see identical behavior.
func validatePool(p poolstore.Pool) error {
	if p.WarmCount < 0 {
		return errors.New("poolstore: warmCount must be >= 0")
	}
	if p.MaxSessionAgeSeconds < 0 {
		return errors.New("poolstore: maxSessionAgeSeconds must be >= 0")
	}
	if p.IsolationProfile == isolation.ProfileStandard && !p.AllowStandardIsolation {
		return errors.New("poolstore: isolationProfile=standard requires allowStandardIsolation=true (§5.3)")
	}
	if err := poolstore.ValidateEgressIsolation(p); err != nil {
		return err
	}
	if err := poolstore.ValidateConcurrentConfig(p); err != nil {
		return err
	}
	if err := poolstore.ValidateTaskPolicy(p); err != nil {
		return err
	}
	return poolstore.ValidateElicitationPolicy(p)
}

// elicitationPolicyJSON is the JSONB wire shape for the §9.2 per-pool
// elicitation policy. The keys match the §9.2 spec yaml so a database
// operator inspecting the row sees the field names the deployer wrote.
type elicitationPolicyJSON struct {
	DepthPolicy     string   `json:"elicitationDepthPolicy,omitempty"`
	SuppressAtDepth int      `json:"elicitationSuppressAtDepth,omitempty"`
	URLModeEnabled  bool     `json:"urlModeElicitationEnabled,omitempty"`
	URLModeDomains  []string `json:"urlModeElicitationDomainAllowlist,omitempty"`
}

// encodeElicitationPolicy returns the JSONB blob to persist or a nil
// byte slice (rendered as SQL NULL) when the pool carries no explicit
// §9.2 elicitation policy. spec: §9.2 lines 86, 90-98.
func encodeElicitationPolicy(p poolstore.Pool) ([]byte, error) {
	if p.ElicitationDepthPolicy == "" && p.ElicitationSuppressAtDepth == 0 &&
		!p.URLModeElicitation.Enabled && len(p.URLModeElicitation.DomainAllowlist) == 0 {
		return nil, nil
	}
	wire := elicitationPolicyJSON{
		DepthPolicy:     string(p.ElicitationDepthPolicy),
		SuppressAtDepth: p.ElicitationSuppressAtDepth,
		URLModeEnabled:  p.URLModeElicitation.Enabled,
		URLModeDomains:  append([]string(nil), p.URLModeElicitation.DomainAllowlist...),
	}
	return json.Marshal(wire)
}

// decodeElicitationPolicy is the inverse of encodeElicitationPolicy: a
// NULL row leaves the §9.2 fields at their zero value, which the
// dispatcher resolves to the §9.2 platform defaults.
func decodeElicitationPolicy(raw []byte, p *poolstore.Pool) error {
	if len(raw) == 0 {
		return nil
	}
	var wire elicitationPolicyJSON
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("poolstore: decode elicitation_policy: %w", err)
	}
	p.ElicitationDepthPolicy = elicitation.DepthPolicy(wire.DepthPolicy)
	p.ElicitationSuppressAtDepth = wire.SuppressAtDepth
	p.URLModeElicitation = elicitation.URLModeAllowlist{
		Enabled:         wire.URLModeEnabled,
		DomainAllowlist: append([]string(nil), wire.URLModeDomains...),
	}
	return nil
}

// taskPolicyJSON is the JSONB wire shape for a Pool.TaskPolicy. The
// keys match the §5.2 spec yaml so a database operator inspecting the
// row sees the same field names the deployer wrote.
type taskPolicyJSON struct {
	AcknowledgeBestEffortScrub      bool     `json:"acknowledgeBestEffortScrub,omitempty"`
	MicrovmScrubMode                string   `json:"microvmScrubMode,omitempty"`
	AcknowledgeMicrovmResidualState bool     `json:"acknowledgeMicrovmResidualState,omitempty"`
	CleanupCommands                 []string `json:"cleanupCommands,omitempty"`
	CleanupTimeoutSeconds           int      `json:"cleanupTimeoutSeconds,omitempty"`
	OnCleanupFailure                string   `json:"onCleanupFailure,omitempty"`
	MaxScrubFailures                int      `json:"maxScrubFailures,omitempty"`
	MaxTasksPerPod                  int      `json:"maxTasksPerPod,omitempty"`
	MaxPodUptimeSeconds             int      `json:"maxPodUptimeSeconds,omitempty"`
	MaxTaskRetries                  *int     `json:"maxTaskRetries,omitempty"`
}

// encodeTaskPolicy returns the JSONB blob to persist or a nil byte
// slice (rendered as SQL NULL) when no policy is set. spec: §5.2.
func encodeTaskPolicy(tp *poolstore.TaskPolicy) ([]byte, error) {
	if tp == nil {
		return nil, nil
	}
	wire := taskPolicyJSON{
		AcknowledgeBestEffortScrub:      tp.AcknowledgeBestEffortScrub,
		MicrovmScrubMode:                string(tp.MicrovmScrubMode),
		AcknowledgeMicrovmResidualState: tp.AcknowledgeMicrovmResidualState,
		CleanupCommands:                 append([]string(nil), tp.CleanupCommands...),
		CleanupTimeoutSeconds:           tp.CleanupTimeoutSeconds,
		OnCleanupFailure:                string(tp.OnCleanupFailure),
		MaxScrubFailures:                tp.MaxScrubFailures,
		MaxTasksPerPod:                  tp.MaxTasksPerPod,
		MaxPodUptimeSeconds:             tp.MaxPodUptimeSeconds,
	}
	if tp.MaxTaskRetries != nil {
		n := *tp.MaxTaskRetries
		wire.MaxTaskRetries = &n
	}
	return json.Marshal(wire)
}

// decodeTaskPolicy is the inverse of encodeTaskPolicy: a NULL row reads
// as nil, an empty JSON object reads as a zero-value TaskPolicy.
func decodeTaskPolicy(raw []byte) (*poolstore.TaskPolicy, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var wire taskPolicyJSON
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("poolstore: decode task_policy: %w", err)
	}
	out := &poolstore.TaskPolicy{
		AcknowledgeBestEffortScrub:      wire.AcknowledgeBestEffortScrub,
		MicrovmScrubMode:                runtimestore.MicrovmScrubMode(wire.MicrovmScrubMode),
		AcknowledgeMicrovmResidualState: wire.AcknowledgeMicrovmResidualState,
		CleanupCommands:                 append([]string(nil), wire.CleanupCommands...),
		CleanupTimeoutSeconds:           wire.CleanupTimeoutSeconds,
		OnCleanupFailure:                runtimestore.CleanupFailureDisposition(wire.OnCleanupFailure),
		MaxScrubFailures:                wire.MaxScrubFailures,
		MaxTasksPerPod:                  wire.MaxTasksPerPod,
		MaxPodUptimeSeconds:             wire.MaxPodUptimeSeconds,
	}
	if wire.MaxTaskRetries != nil {
		n := *wire.MaxTaskRetries
		out.MaxTaskRetries = &n
	}
	return out, nil
}

// Create inserts a new pool row after running the §5.2 name
// validation and the §5.2 / §5.3 invariants. Returns ErrAlreadyExists
// when the name is taken.
func (s *Store) Create(ctx context.Context, p poolstore.Pool) error {
	if err := poolstore.ValidateName(p.Name); err != nil {
		return err
	}
	if err := validatePool(p); err != nil {
		return err
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	if p.Generation == 0 {
		p.Generation = 1
	}
	tpJSON, err := encodeTaskPolicy(p.TaskPolicy)
	if err != nil {
		return err
	}
	epJSON, err := encodeElicitationPolicy(p)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO sandbox_warm_pools (
		name, runtime_ref, isolation_profile, execution_mode,
		resource_class, warm_count, max_session_age_seconds,
		allow_standard_isolation, concurrency_style, max_concurrent,
		acknowledge_process_level_isolation, cleanup_timeout_seconds,
		allow_cross_tenant_reuse, egress_profile, created_at, updated_at, deleted_at,
		pool_config_generation, task_policy, elicitation_policy, draining_since
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
		p.Name, p.RuntimeRef, string(p.IsolationProfile), string(p.ExecutionMode),
		p.ResourceClass, p.WarmCount, p.MaxSessionAgeSeconds,
		p.AllowStandardIsolation, string(p.ConcurrencyStyle), p.MaxConcurrent,
		p.AcknowledgeProcessLevelIsolation, p.CleanupTimeoutSeconds,
		p.AllowCrossTenantReuse, string(p.EgressProfile), p.CreatedAt, p.UpdatedAt, pgtenant.NullTime(p.DeletedAt),
		p.Generation, tpJSON, epJSON, pgtenant.NullTime(p.DrainingSince))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return poolstore.ErrAlreadyExists
	}
	return err
}

// Get returns the pool row keyed by name. Soft-deleted rows are
// returned (callers consult Pool.IsActive()); a missing row is
// ErrNotFound.
func (s *Store) Get(ctx context.Context, name string) (poolstore.Pool, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+selectList+` FROM sandbox_warm_pools WHERE name = $1`, name)
	p, err := scanPool(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return poolstore.Pool{}, poolstore.ErrNotFound
	}
	if err != nil {
		return poolstore.Pool{}, err
	}
	return p, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-runs the §5.2 / §5.3 invariants, and advances UpdatedAt.
func (s *Store) Update(ctx context.Context, name string, mutate func(*poolstore.Pool) error) (poolstore.Pool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return poolstore.Pool{}, fmt.Errorf("poolstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx,
		`SELECT `+selectList+` FROM sandbox_warm_pools WHERE name = $1 FOR UPDATE`, name)
	p, err := scanPool(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return poolstore.Pool{}, poolstore.ErrNotFound
	}
	if err != nil {
		return poolstore.Pool{}, err
	}
	prev := p.UpdatedAt
	if err := mutate(&p); err != nil {
		return poolstore.Pool{}, err
	}
	if err := validatePool(p); err != nil {
		return poolstore.Pool{}, err
	}
	p.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
	// spec: §4.6.2 line 558 — pool_config_generation is bumped on
	// every admin-API write so the gateway-side drift check can
	// compare it to the CRD annotation.
	p.Generation++
	tpJSON, err := encodeTaskPolicy(p.TaskPolicy)
	if err != nil {
		return poolstore.Pool{}, err
	}
	epJSON, err := encodeElicitationPolicy(p)
	if err != nil {
		return poolstore.Pool{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE sandbox_warm_pools SET
		runtime_ref = $2, isolation_profile = $3, execution_mode = $4,
		resource_class = $5, warm_count = $6, max_session_age_seconds = $7,
		allow_standard_isolation = $8, concurrency_style = $9, max_concurrent = $10,
		acknowledge_process_level_isolation = $11, cleanup_timeout_seconds = $12,
		allow_cross_tenant_reuse = $13, egress_profile = $14, updated_at = $15, deleted_at = $16,
		pool_config_generation = $17, task_policy = $18, elicitation_policy = $19, draining_since = $20
	WHERE name = $1`,
		name, p.RuntimeRef, string(p.IsolationProfile), string(p.ExecutionMode),
		p.ResourceClass, p.WarmCount, p.MaxSessionAgeSeconds,
		p.AllowStandardIsolation, string(p.ConcurrencyStyle), p.MaxConcurrent,
		p.AcknowledgeProcessLevelIsolation, p.CleanupTimeoutSeconds,
		p.AllowCrossTenantReuse, string(p.EgressProfile), p.UpdatedAt, pgtenant.NullTime(p.DeletedAt),
		p.Generation, tpJSON, epJSON, pgtenant.NullTime(p.DrainingSince)); err != nil {
		return poolstore.Pool{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return poolstore.Pool{}, fmt.Errorf("poolstore: commit: %w", err)
	}
	return p, nil
}

// List returns the pool rows name-ascending. Soft-deleted rows are
// dropped unless filter.IncludeDeleted is set, and a non-empty
// filter.RuntimeRef restricts the result to that runtime.
func (s *Store) List(ctx context.Context, filter poolstore.ListFilter) ([]poolstore.Pool, error) {
	q := `SELECT ` + selectList + ` FROM sandbox_warm_pools`
	var args []any
	var conds []string
	if !filter.IncludeDeleted {
		conds = append(conds, `deleted_at IS NULL`)
	}
	if filter.RuntimeRef != "" {
		args = append(args, filter.RuntimeRef)
		conds = append(conds, fmt.Sprintf(`runtime_ref = $%d`, len(args)))
	}
	for i, cond := range conds {
		if i == 0 {
			q += ` WHERE `
		} else {
			q += ` AND `
		}
		q += cond
	}
	q += ` ORDER BY name`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []poolstore.Pool
	for rows.Next() {
		p, err := scanPool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SoftDelete sets deleted_at on the row. It is idempotent:
// soft-deleting an already-deleted pool is a no-op success.
func (s *Store) SoftDelete(ctx context.Context, name string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sandbox_warm_pools SET deleted_at = $2, updated_at = $2,
			pool_config_generation = pool_config_generation + 1
		 WHERE name = $1 AND deleted_at IS NULL`, name, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM sandbox_warm_pools WHERE name = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return poolstore.ErrNotFound
	}
	return nil
}

// scanPool reads one row in selectList order into a Pool.
func scanPool(row pgx.Row) (poolstore.Pool, error) {
	var (
		p                                                                poolstore.Pool
		isolationProfile, executionMode, concurrencyStyle, egressProfile string
		deletedAt                                                        *time.Time
		drainingSince                                                    *time.Time
		taskPolicy                                                       []byte
		elicitationPolicy                                                []byte
	)
	if err := row.Scan(
		&p.Name, &p.RuntimeRef, &isolationProfile, &executionMode,
		&p.ResourceClass, &p.WarmCount, &p.MaxSessionAgeSeconds,
		&p.AllowStandardIsolation, &concurrencyStyle, &p.MaxConcurrent,
		&p.AcknowledgeProcessLevelIsolation, &p.CleanupTimeoutSeconds,
		&p.AllowCrossTenantReuse, &egressProfile, &p.CreatedAt, &p.UpdatedAt, &deletedAt,
		&p.Generation, &taskPolicy, &elicitationPolicy, &drainingSince,
	); err != nil {
		return poolstore.Pool{}, err
	}
	p.IsolationProfile = isolation.Profile(isolationProfile)
	p.ExecutionMode = runtimestore.ExecutionMode(executionMode)
	p.ConcurrencyStyle = poolstore.ConcurrencyStyle(concurrencyStyle)
	p.EgressProfile = egress.Profile(egressProfile)
	if deletedAt != nil {
		p.DeletedAt = *deletedAt
	}
	if drainingSince != nil {
		p.DrainingSince = *drainingSince
	}
	tp, err := decodeTaskPolicy(taskPolicy)
	if err != nil {
		return poolstore.Pool{}, err
	}
	p.TaskPolicy = tp
	if err := decodeElicitationPolicy(elicitationPolicy, &p); err != nil {
		return poolstore.Pool{}, err
	}
	return p, nil
}
