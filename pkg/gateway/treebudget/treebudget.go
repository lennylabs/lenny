// SPDX-License-Identifier: MIT

// Package treebudget is the Redis-backed §8.2 per-tree delegation
// budget counter. It atomically reserves the structural budget axes a
// `lenny/delegate_task` admission consumes — tree node count, tree
// in-memory footprint, per-parent concurrent children, per-parent
// total descendants, and the tree token pool — against the caps the
// resolved delegation lease declares.
//
// The counters live under the §12.4 tree-scoped keys
// `{root_session_id}:dlg:*`. The `{root_session_id}` hash tag (literal
// braces) co-locates every key for one tree on a single Redis Cluster
// slot so the multi-key reserve/return scripts execute atomically.
// Tenant isolation is enforced one layer up: the caller validates that
// root_session_id belongs to the calling tenant before invoking either
// script (§12.4 line 193).
//
// Failure posture (§12.4 line 213): a Redis error fails closed for new
// delegations. Reserve returns ErrBudgetUnavailable, which the §8.5
// handler surfaces as the retryable DELEGATION_BUDGET_UNAVAILABLE. A
// cap breach returns *BudgetExhaustedError, surfaced as BUDGET_EXHAUSTED.
//
// spec: §8.2 lines 57, 114-130; §12.4 lines 193, 213.
package treebudget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
)

// DefaultTTL is the GC safety expiry refreshed on every reserve. The
// counters are reconstructed from the Postgres checkpoint on Redis
// recovery (§12.4 line 218), so the TTL only reclaims keys for trees
// that vanished without a terminal offload. A long window keeps a
// long-lived tree's counters alive between delegation hops.
const DefaultTTL = 24 * time.Hour

// DefaultMaxTreeMemoryBytes is the §8.2 default cap on a delegation
// tree's aggregate gateway memory footprint (2 MB). Every lease carries
// it by default so the tree's footprint is bounded even when no
// explicit maxTreeMemoryBytes is declared.
//
// spec: §8.2 line 127 ("default: 2097152 / 2 MB").
const DefaultMaxTreeMemoryBytes int64 = 2097152

// PerNodeMemoryBytes is the §8.2 per-node in-memory footprint estimate
// (~12 KB: virtual child interface, capped event buffer, elicitation
// state, task metadata). Each admitted delegation reserves this much
// of the tree's maxTreeMemoryBytes.
//
// spec: §8.2 line 124 ("Total per node ~12 KB").
const PerNodeMemoryBytes int64 = 12 * 1024

// Reservation is the budget a single delegation admission consumes.
// Each axis carries the cap from the resolved lease (0 means no limit
// at that axis) and the amount this hop adds. Tree-wide axes
// (TreeSize, TreeMemory, Tokens) share one key across the tree;
// per-parent axes (ParallelChildren, ChildrenTotal) are scoped to the
// delegating parent so each node enforces its own fan-out.
//
// spec: §8.2 line 44-48; §12.4 line 193.
type Reservation struct {
	RootSessionID   string
	ParentSessionID string

	TreeSizeCap   int64
	TreeSizeDelta int64

	TreeMemoryCap   int64
	TreeMemoryDelta int64

	ParallelChildrenCap   int64
	ParallelChildrenDelta int64

	ChildrenTotalCap   int64
	ChildrenTotalDelta int64

	TokenCap   int64
	TokenDelta int64
}

// Totals is the post-reservation counter state returned on admission.
type Totals struct {
	TreeSize         int64
	TreeMemory       int64
	ParallelChildren int64
	ChildrenTotal    int64
	Tokens           int64
}

// axisName maps the §8.2 axis order used by the Lua scripts to the
// human-facing field name carried in BudgetExhaustedError.Axis and the
// per-axis violation string.
var axisName = []string{"maxTreeSize", "maxTreeMemoryBytes", "maxParallelChildren", "maxChildrenTotal", "maxTokenBudget"}

// BudgetExhaustedError is returned by Reserve when the reservation
// would push an axis over its cap. No counter is mutated. The §8.5
// handler maps this to BUDGET_EXHAUSTED.
//
// spec: §8.2 line 127.
type BudgetExhaustedError struct {
	// Axis is the canonical lease field name that overflowed.
	Axis string
	// Current is the counter value before the rejected reservation.
	Current int64
	// Cap is the axis ceiling the reservation would have breached.
	Cap int64
	// Delta is the amount the rejected reservation tried to add.
	Delta int64
}

func (e *BudgetExhaustedError) Error() string {
	return fmt.Sprintf("delegation tree budget exhausted: %s %d + %d exceeds cap %d", e.Axis, e.Current, e.Delta, e.Cap)
}

// ErrBudgetUnavailable is returned when the Redis-backed counters
// cannot be consulted (outage, script error). Per §12.4 line 213 the
// admission path fails closed: new delegations are rejected with the
// retryable DELEGATION_BUDGET_UNAVAILABLE rather than admitted
// unbudgeted.
var ErrBudgetUnavailable = errors.New("delegation tree budget unavailable")

// Reserver runs the atomic budget reserve/return scripts.
type Reserver struct {
	client redis.UniversalClient
	ttl    time.Duration
}

// New returns a Reserver backed by client. A zero ttl selects
// DefaultTTL.
func New(client redis.UniversalClient, ttl time.Duration) *Reserver {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Reserver{client: client, ttl: ttl}
}

// reserveScript checks every capped axis against its cap before
// mutating any counter, then applies all increments atomically and
// refreshes the TTL on freshly-created keys. KEYS are the five §12.4
// counters in axis order; ARGV carries the five (cap, delta) pairs
// followed by the TTL seconds.
//
// Return on rejection: {0, axisIndex, current, cap}. Return on
// admission: {1, treeSize, treeMemory, parallelChildren, childrenTotal,
// tokens}.
var reserveScript = redis.NewScript(`
local cur = {}
for i = 1, 5 do
  cur[i] = tonumber(redis.call('GET', KEYS[i]) or '0')
end
-- §8.6 line 643: the cumulative per-tree token-cap extension grant raises
-- the tokens-axis ceiling so a granted lease extension admits subsequent
-- delegations. KEYS[7] holds the granted token delta. A zero base token
-- cap means "unlimited" and the grant never narrows it to a finite cap.
local token_grant = tonumber(redis.call('GET', KEYS[7]) or '0')
for i = 1, 5 do
  local cap = tonumber(ARGV[(i-1)*2 + 1])
  if i == 5 and cap > 0 then cap = cap + token_grant end
  local delta = tonumber(ARGV[(i-1)*2 + 2])
  if cap > 0 and delta > 0 and (cur[i] + delta) > cap then
    return {0, i, cur[i], cap}
  end
end
local ttl = tonumber(ARGV[11])
local out = {1}
for i = 1, 5 do
  local delta = tonumber(ARGV[(i-1)*2 + 2])
  local v = cur[i]
  if delta ~= 0 then
    v = redis.call('INCRBY', KEYS[i], delta)
    if redis.call('TTL', KEYS[i]) == -1 then
      redis.call('EXPIRE', KEYS[i], ttl)
    end
  end
  out[i+1] = v
end
-- §8.3 line 379: track the per-tree parallel-children high-watermark so
-- the gateway can observe the maximum simultaneous in-flight children
-- onto lenny_delegation_parallel_children_high_watermark at tree
-- completion. out[4] is the parallel_children counter post-increment for
-- the delegating parent; KEYS[6] holds the running tree-wide max.
local pc = out[4]
local hwm = tonumber(redis.call('GET', KEYS[6]) or '0')
if pc > hwm then
  redis.call('SET', KEYS[6], pc)
  redis.call('EXPIRE', KEYS[6], ttl)
end
return out
`)

// returnScript decrements each counter by its delta, clamping at zero
// so a double-return or an over-return can never drive a counter
// negative and inflate the available budget. KEYS are the five
// counters; ARGV carries the five deltas.
var returnScript = redis.NewScript(`
for i = 1, 5 do
  local delta = tonumber(ARGV[i])
  if delta ~= 0 then
    local v = redis.call('DECRBY', KEYS[i], delta)
    if v < 0 then
      redis.call('SET', KEYS[i], 0)
    end
  end
end
return 1
`)

// keys returns the five §12.4 delegation budget keys for r in the axis
// order the scripts expect. The literal braces around root_session_id
// are the Redis Cluster hash tag.
func (r Reservation) keys() []string {
	root := "{" + r.RootSessionID + "}:dlg:"
	return []string{
		root + "tree_size",
		root + "tree_memory",
		root + "parallel_children:" + r.ParentSessionID,
		root + "children_total:" + r.ParentSessionID,
		root + "tokens",
	}
}

// hwmKey returns the §8.3 line 379 tree-wide parallel-children
// high-watermark key for r's root. It shares the `{root_session_id}`
// hash tag so it co-locates on the same Redis Cluster slot as the
// per-tree counters and can be updated atomically inside the reserve
// script.
func hwmKey(rootSessionID string) string {
	return "{" + rootSessionID + "}:dlg:parallel_children_hwm"
}

// tokenGrantKey returns the §8.6 line 643 cumulative token-cap extension
// grant key for the tree. A lease extension that raises the token budget
// increments it; the reserve script (KEYS[7]) adds it to the tokens-axis
// ceiling so post-grant admissions observe the expanded pool. It shares
// the `{root_session_id}` hash tag so it co-locates on the same Redis
// Cluster slot as the counters.
func tokenGrantKey(rootSessionID string) string {
	return "{" + rootSessionID + "}:dlg:token_grant"
}

// reserveKeys returns the five counter keys followed by the
// high-watermark key (KEYS[6]) and the token-cap grant key (KEYS[7]) the
// reserve script reads. Return does not touch either, so returnScript
// keeps the five-key form.
func (r Reservation) reserveKeys() []string {
	return append(r.keys(), hwmKey(r.RootSessionID), tokenGrantKey(r.RootSessionID))
}

// Reserve atomically admits one delegation against the tree budget. On
// success it returns the post-reservation Totals. It returns
// *BudgetExhaustedError when an axis cap would be breached (no counter
// mutated) and ErrBudgetUnavailable when Redis cannot be consulted
// (fail closed).
//
// spec: §8.2 lines 57, 127; §12.4 line 213.
func (s *Reserver) Reserve(ctx context.Context, r Reservation) (retTotals Totals, retErr error) {
	if r.RootSessionID == "" {
		return Totals{}, fmt.Errorf("treebudget: empty root session id")
	}
	// spec: §16.3 line 347 — the Redis-Lua budget reserve runs under a
	// `delegation.budget_reserve` span carrying the mandated outcome,
	// tenant_id, root_session_id, and lua_queue_wait_ms attributes. The
	// tracer resolves the process-global provider tracing.InitProvider
	// installs. tenant_id is projected from the correlation context by
	// Start when present; the Reservation carries no tenant field, so
	// root_session_id is the explicit per-tree key set here. outcome is
	// stamped from the actual result by the deferred closure below.
	ctx, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanDelegationBudgetReserve)
	span.SetAttributes(attribute.String(tracing.AttrRootSessionID, r.RootSessionID))
	outcome := "rejected"
	defer func() {
		span.SetAttributes(attribute.String(tracing.AttrOutcome, outcome))
		tracing.RecordError(span, retErr)
		span.End()
	}()
	argv := []any{
		r.TreeSizeCap, r.TreeSizeDelta,
		r.TreeMemoryCap, r.TreeMemoryDelta,
		r.ParallelChildrenCap, r.ParallelChildrenDelta,
		r.ChildrenTotalCap, r.ChildrenTotalDelta,
		r.TokenCap, r.TokenDelta,
		int64(s.ttl.Seconds()),
	}
	// The Lua script executes atomically under the per-root serialization
	// the `{root_session_id}` hash tag enforces, so the round-trip latency
	// is the closest cheaply-available measurement of the §16.3 line 347
	// lua_queue_wait_ms (the time the reserve spent in the Lua path,
	// including any per-slot serialization). It is emitted as a real
	// measured value rather than an invented queue-wait estimate.
	luaStart := time.Now()
	res, err := reserveScript.Run(ctx, s.client, r.reserveKeys(), argv...).Int64Slice()
	span.SetAttributes(attribute.Int64(tracing.AttrLuaQueueWaitMs, time.Since(luaStart).Milliseconds()))
	if err != nil {
		// Fail closed: a Redis outage or script error must not admit an
		// unbudgeted delegation (§12.4 line 213).
		return Totals{}, fmt.Errorf("%w: %v", ErrBudgetUnavailable, err)
	}
	if len(res) == 0 {
		return Totals{}, fmt.Errorf("%w: empty script result", ErrBudgetUnavailable)
	}
	if res[0] == 0 {
		if len(res) < 4 {
			return Totals{}, fmt.Errorf("%w: malformed rejection result", ErrBudgetUnavailable)
		}
		idx := int(res[1])
		name := "unknown"
		if idx >= 1 && idx <= len(axisName) {
			name = axisName[idx-1]
		}
		return Totals{}, &BudgetExhaustedError{
			Axis:    name,
			Current: res[2],
			Cap:     res[3],
			Delta:   axisDelta(r, idx),
		}
	}
	if len(res) < 6 {
		return Totals{}, fmt.Errorf("%w: malformed admission result", ErrBudgetUnavailable)
	}
	outcome = "reserved"
	return Totals{
		TreeSize:         res[1],
		TreeMemory:       res[2],
		ParallelChildren: res[3],
		ChildrenTotal:    res[4],
		Tokens:           res[5],
	}, nil
}

// Return releases a previously reserved delegation's budget. It is
// called when a child reaches a terminal state (decrement
// parallel_children) or on completed-subtree offload (decrement
// tree_memory). A Redis error is returned for the caller to log; a
// failed return leaks budget conservatively rather than over-admitting,
// so it is not fatal to correctness.
//
// spec: §8.2 line 130 (decrement on offload).
func (s *Reserver) Return(ctx context.Context, r Reservation) (retErr error) {
	if r.RootSessionID == "" {
		return fmt.Errorf("treebudget: empty root session id")
	}
	// spec: §16.3 line 348 — the Redis-Lua budget return runs under a
	// `delegation.budget_return` span carrying the mandated outcome,
	// tenant_id, root_session_id, and lua_queue_wait_ms attributes,
	// mirroring Reserve. tenant_id is projected from the correlation
	// context by Start when present; root_session_id is set explicitly
	// from the Reservation. outcome is "returned" on a successful
	// release and is left unset (the deferred RecordError stamps the
	// error) on a Redis failure.
	ctx, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanDelegationBudgetReturn)
	span.SetAttributes(attribute.String(tracing.AttrRootSessionID, r.RootSessionID))
	defer func() {
		tracing.RecordError(span, retErr)
		span.End()
	}()
	argv := []any{
		r.TreeSizeDelta,
		r.TreeMemoryDelta,
		r.ParallelChildrenDelta,
		r.ChildrenTotalDelta,
		r.TokenDelta,
	}
	// The return Lua script executes atomically per the per-root slot
	// serialization, so its round-trip latency is the §16.3 line 348
	// lua_queue_wait_ms measurement (emitted as a real measured value).
	luaStart := time.Now()
	err := returnScript.Run(ctx, s.client, r.keys(), argv...).Err()
	span.SetAttributes(attribute.Int64(tracing.AttrLuaQueueWaitMs, time.Since(luaStart).Milliseconds()))
	if err != nil {
		return fmt.Errorf("treebudget: return: %w", err)
	}
	span.SetAttributes(attribute.String(tracing.AttrOutcome, "returned"))
	return nil
}

// axisDelta returns the reservation delta for the 1-based axis index
// the reserve script reports, so BudgetExhaustedError carries the
// amount the rejected hop attempted to add.
func axisDelta(r Reservation, idx int) int64 {
	switch idx {
	case 1:
		return r.TreeSizeDelta
	case 2:
		return r.TreeMemoryDelta
	case 3:
		return r.ParallelChildrenDelta
	case 4:
		return r.ChildrenTotalDelta
	case 5:
		return r.TokenDelta
	default:
		return 0
	}
}

// ObserveHighWatermark reads and clears the §8.3 line 379 per-tree
// parallel-children high-watermark for rootSessionID, returning the
// maximum simultaneous in-flight children the reserve script recorded
// over the tree's lifetime. The gateway calls it once when the tree
// root reaches a terminal state and feeds the value to the
// lenny_delegation_parallel_children_high_watermark histogram. The key
// is deleted in the same round trip (GETDEL) so a re-settle of the same
// root cannot double-count and the slot is reclaimed immediately rather
// than waiting for the TTL. A tree that admitted no delegation has no
// key and returns 0 with found=false, which the caller skips.
//
// spec: §8.3 line 379; §16.1.
func (s *Reserver) ObserveHighWatermark(ctx context.Context, rootSessionID string) (value int64, found bool, err error) {
	if rootSessionID == "" {
		return 0, false, fmt.Errorf("treebudget: empty root session id")
	}
	v, err := s.client.GetDel(ctx, hwmKey(rootSessionID)).Int64()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("treebudget: read high-watermark: %w", err)
	}
	return v, true, nil
}

// GrantTokenBudget raises the tree's tokens-axis ceiling by delta,
// recording a §8.6 line 643 lease-extension grant so every subsequent
// `lenny/delegate_task` admission in the tree is gated against the
// expanded token pool rather than the parent lease's pre-extension
// MaxTokenBudget. Without this bridge an ExtendLease GRANTED response
// raises only the in-memory leasecontrol view and the next admission
// still sees the old cap (F-8.6.3). The grant is cumulative (INCRBY) and
// shares the tree's `{root_session_id}` hash tag and GC TTL with the
// counters. A non-positive delta is a no-op. A zero base token cap
// (unlimited) is unaffected: the reserve script only folds the grant into
// a finite cap.
//
// spec: §8.6 line 643; §8.2 line 57.
func (s *Reserver) GrantTokenBudget(ctx context.Context, rootSessionID string, delta int64) error {
	if rootSessionID == "" {
		return fmt.Errorf("treebudget: empty root session id")
	}
	if delta <= 0 {
		return nil
	}
	key := tokenGrantKey(rootSessionID)
	if err := s.client.IncrBy(ctx, key, delta).Err(); err != nil {
		return fmt.Errorf("treebudget: grant token budget: %w", err)
	}
	// Refresh the GC TTL so the grant key lives as long as the counters
	// it raises the ceiling for.
	if err := s.client.Expire(ctx, key, s.ttl).Err(); err != nil {
		return fmt.Errorf("treebudget: refresh token-grant ttl: %w", err)
	}
	return nil
}

// PurgeRoot deletes every delegation tree-budget key for rootSessionID:
// the tree-wide counters (`tokens`, `tree_size`, `tree_memory`), the
// per-parent reservation keys (`parallel_children:*`,
// `children_total:*`), and the parallel-children high-watermark. It is
// the §12.8 step-16 erasure primitive: the GDPR erasure orchestrator
// calls it for each root session the erased user owns, before
// SessionStore deletion makes the root session ids irrecoverable. All
// keys share the `{root_session_id}` hash tag, so a single slot-local
// SCAN covers the whole tree. Returns the number of keys removed; a
// non-root session id matches no keys and returns 0.
//
// spec: §12.8 line 831 (step 16 — "delete tree-wide keys ... and scan
// for per-parent keys ... using slot-local SCAN").
func (s *Reserver) PurgeRoot(ctx context.Context, rootSessionID string) (int, error) {
	if rootSessionID == "" {
		return 0, fmt.Errorf("treebudget: empty root session id")
	}
	pattern := "{" + rootSessionID + "}:dlg:*"
	const delBatch = 256
	var (
		cursor  uint64
		deleted int
	)
	for {
		keys, next, err := s.client.Scan(ctx, cursor, pattern, delBatch).Result()
		if err != nil {
			return deleted, fmt.Errorf("treebudget: purge scan: %w", err)
		}
		if len(keys) > 0 {
			n, err := s.client.Del(ctx, keys...).Result()
			if err != nil {
				return deleted, fmt.Errorf("treebudget: purge del: %w", err)
			}
			deleted += int(n)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return deleted, nil
}
