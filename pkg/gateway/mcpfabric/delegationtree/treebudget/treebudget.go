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
// script (§12.4).
//
// Failure posture (§12.4): a Redis error fails closed for new
// delegations. Reserve returns ErrBudgetUnavailable, which the §8.5
// handler surfaces as the retryable DELEGATION_BUDGET_UNAVAILABLE. A
// cap breach returns *BudgetExhaustedError, surfaced as BUDGET_EXHAUSTED.
//
// spec: §8.2; §12.4.
package treebudget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/redisconn"
)

// DefaultTTL is the GC safety expiry of the delegation budget keys: 8 days,
// one day longer than the default suspended-session lifetime
// (gateway.maxSuspendedSessionSeconds, 604800 s), so a suspended tree's keys
// outlive the tree. The reserve script arms the TTL only on a key that has
// none, and the session server re-arms it with RearmTree on each transition
// into or out of suspended. The counters are reconstructed from the Postgres
// checkpoint on Redis recovery (§12.4), so the TTL only reclaims keys for
// trees that vanished without a terminal offload. The value is the default
// of the operator-tunable --delegation-budget-key-ttl-seconds.
//
// spec: §8.3 (Delegation Policy and Lease)
const DefaultTTL = 8 * 24 * time.Hour

// DefaultMaxTreeMemoryBytes is the §8.2 default cap on a delegation
// tree's aggregate gateway memory footprint (2 MB). Every lease carries
// it by default so the tree's footprint is bounded even when no
// explicit maxTreeMemoryBytes is declared.
//
// spec: §8.2.
const DefaultMaxTreeMemoryBytes int64 = 2097152

// PerNodeMemoryBytes is the §8.2 per-node in-memory footprint estimate
// (~12 KB: virtual child interface, capped event buffer, elicitation
// state, task metadata). Each admitted delegation reserves this much
// of the tree's maxTreeMemoryBytes.
//
// spec: §8.2.
const PerNodeMemoryBytes int64 = 12 * 1024

// Reservation is the budget a single delegation admission consumes.
// Each axis carries the cap from the resolved lease (0 means no limit
// at that axis) and the amount this hop adds. Tree-wide axes
// (TreeSize, TreeMemory, Tokens) share one key across the tree;
// per-parent axes (ParallelChildren, ChildrenTotal) are scoped to the
// delegating parent so each node enforces its own fan-out.
//
// spec: §8.2; §12.4.
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

	// CreateTree marks the reservation as the tree's first. When the
	// tree-wide sentinel ({root}:dlg:tokens) is absent, a marked Reserve
	// creates the tree-wide keys and proceeds, and an unmarked one returns
	// ErrBudgetKeysExpired without writing any key. The gateway marks a
	// reservation only after the SessionStore shows no delegated session
	// under the root. Return ignores it.
	//
	// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
	CreateTree bool
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
// spec: §8.2.
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
// cannot be consulted (outage, script error). Per §12.4 the
// admission path fails closed: new delegations are rejected with the
// retryable DELEGATION_BUDGET_UNAVAILABLE rather than admitted
// unbudgeted.
var ErrBudgetUnavailable = errors.New("delegation tree budget unavailable")

// ErrBudgetKeysExpired is returned by Reserve and Return when the tree-wide
// sentinel key ({root}:dlg:tokens) does not exist and the run may not create
// it. No key was written. Only a Reserve that returns it is a §8.3
// BUDGET_KEYS_EXPIRED detection; the caller decides whether the reservation
// is the tree's first.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
var ErrBudgetKeysExpired = errors.New("delegation tree budget keys expired")

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

// Script status codes. The reserve script returns statusRejected or
// statusAdmitted as the first element of its result table, and both scripts
// return statusKeysExpired when the tree-wide sentinel is absent and the run
// may not create it.
const (
	statusRejected    = 0
	statusAdmitted    = 1
	statusKeysExpired = 2
)

// reserveScript checks the tree-wide sentinel (KEYS[5], the tokens key)
// before reading any counter. When the sentinel is absent and ARGV[12] does
// not mark the run as the tree's first reservation, it returns {2} and writes
// no key. When the run is marked, it creates each tree-wide key it does not
// find (tree_size, tree_memory, tokens) at zero with the TTL, the sentinel
// included even for a zero token delta, and proceeds. It then checks every
// capped axis against its cap before mutating any counter, applies all
// increments atomically, and arms the TTL on a key it creates. KEYS are the
// five §12.4 counters in axis order followed by the high-watermark key; ARGV
// carries the five (cap, delta) pairs, the TTL seconds, and the
// first-reservation flag.
//
// Return on rejection: {0, axisIndex, current, cap}. Return on admission:
// {1, treeSize, treeMemory, parallelChildren, childrenTotal, tokens}. Return
// on an absent sentinel: {2}.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §8.3
// (Defense-in-depth TTL)
var reserveScript = redis.NewScript(`
local ttl = tonumber(ARGV[11])
if redis.call('EXISTS', KEYS[5]) == 0 then
  if tonumber(ARGV[12]) ~= 1 then
    return {2}
  end
  for _, i in ipairs({1, 2, 5}) do
    redis.call('SET', KEYS[i], 0, 'NX', 'EX', ttl)
  end
end
local cur = {}
for i = 1, 5 do
  cur[i] = tonumber(redis.call('GET', KEYS[i]) or '0')
end
for i = 1, 5 do
  local cap = tonumber(ARGV[(i-1)*2 + 1])
  local delta = tonumber(ARGV[(i-1)*2 + 2])
  if cap > 0 and delta > 0 and (cur[i] + delta) > cap then
    return {0, i, cur[i], cap}
  end
end
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
-- §8.3: track the per-tree parallel-children high-watermark so
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

// returnScript checks the tree-wide sentinel (KEYS[5]) first and returns 2
// with no key written when it is absent. Otherwise it decrements each counter
// that exists by its non-zero delta, skipping a key that does not exist so it
// never recreates one, and clamps a negative result at zero with KEEPTTL so a
// double-return or an over-return can neither drive a counter negative nor
// drop its TTL. KEYS are the five counters; ARGV carries the five deltas.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §8.3
// (Defense-in-depth TTL)
var returnScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[5]) == 0 then
  return 2
end
for i = 1, 5 do
  local delta = tonumber(ARGV[i])
  if delta ~= 0 and redis.call('EXISTS', KEYS[i]) == 1 then
    local v = redis.call('DECRBY', KEYS[i], delta)
    if v < 0 then
      redis.call('SET', KEYS[i], 0, 'KEEPTTL')
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

// hwmKey returns the §8.3 tree-wide parallel-children
// high-watermark key for r's root. It shares the `{root_session_id}`
// hash tag so it co-locates on the same Redis Cluster slot as the
// per-tree counters and can be updated atomically inside the reserve
// script.
func hwmKey(rootSessionID string) string {
	return "{" + rootSessionID + "}:dlg:parallel_children_hwm"
}

// reserveKeys returns the five counter keys followed by the
// high-watermark key (KEYS[6]) the reserve script updates. Return does not
// touch the high-watermark, so returnScript keeps the five-key form.
func (r Reservation) reserveKeys() []string {
	return append(r.keys(), hwmKey(r.RootSessionID))
}

// Reserve atomically admits one delegation against the tree budget. On
// success it returns the post-reservation Totals. It returns
// *BudgetExhaustedError when an axis cap would be breached (no counter
// mutated), ErrBudgetKeysExpired when the tree-wide sentinel is absent and
// the reservation is not marked CreateTree (no key written), and
// ErrBudgetUnavailable when Redis cannot be consulted (fail closed).
//
// spec: §8.2; §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup); §12.4.
func (s *Reserver) Reserve(ctx context.Context, r Reservation) (retTotals Totals, retErr error) {
	if r.RootSessionID == "" {
		return Totals{}, fmt.Errorf("treebudget: empty root session id")
	}
	// spec: §16.3 — the Redis-Lua budget reserve runs under a
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
		boolArg(r.CreateTree),
	}
	// The Lua script executes atomically under the per-root serialization
	// the `{root_session_id}` hash tag enforces, so the round-trip latency
	// is the closest cheaply-available measurement of the §16.3
	// lua_queue_wait_ms (the time the reserve spent in the Lua path,
	// including any per-slot serialization). It is emitted as a real
	// measured value rather than an invented queue-wait estimate.
	luaStart := time.Now()
	res, err := reserveScript.Run(ctx, s.client, r.reserveKeys(), argv...).Int64Slice()
	span.SetAttributes(attribute.Int64(tracing.AttrLuaQueueWaitMs, time.Since(luaStart).Milliseconds()))
	if err != nil {
		// Fail closed: a Redis outage or script error must not admit an
		// unbudgeted delegation (§12.4).
		return Totals{}, fmt.Errorf("%w: %v", ErrBudgetUnavailable, err)
	}
	if len(res) == 0 {
		return Totals{}, fmt.Errorf("%w: empty script result", ErrBudgetUnavailable)
	}
	if res[0] == statusKeysExpired {
		return Totals{}, ErrBudgetKeysExpired
	}
	if res[0] == statusRejected {
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
// so it is not fatal to correctness. Return creates no key and keeps the
// TTL of every key it writes. It returns ErrBudgetKeysExpired, with no key
// written, when the tree-wide sentinel is absent; that result is not a
// BUDGET_KEYS_EXPIRED detection.
//
// spec: §8.2; §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup).
func (s *Reserver) Return(ctx context.Context, r Reservation) (retErr error) {
	if r.RootSessionID == "" {
		return fmt.Errorf("treebudget: empty root session id")
	}
	// spec: §16.3 — the Redis-Lua budget return runs under a
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
	// serialization, so its round-trip latency is the §16.3
	// lua_queue_wait_ms measurement (emitted as a real measured value).
	luaStart := time.Now()
	status, err := returnScript.Run(ctx, s.client, r.keys(), argv...).Int64()
	span.SetAttributes(attribute.Int64(tracing.AttrLuaQueueWaitMs, time.Since(luaStart).Milliseconds()))
	if err != nil {
		return fmt.Errorf("treebudget: return: %w", err)
	}
	if status == statusKeysExpired {
		return ErrBudgetKeysExpired
	}
	span.SetAttributes(attribute.String(tracing.AttrOutcome, "returned"))
	return nil
}

// boolArg encodes a script flag as the integer ARGV value the Lua scripts
// compare against 1.
func boolArg(b bool) int64 {
	if b {
		return 1
	}
	return 0
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

// ObserveHighWatermark reads and clears the §8.3 per-tree
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
// spec: §8.3; §16.1.
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

// RearmTree sets the Reserver's TTL on the delegation budget keys of the
// tree rooted at rootSessionID: the tree-wide counters (tree_size,
// tree_memory, tokens), the high-watermark key, and the
// parallel_children and children_total keys of each parent in parentIDs. It
// issues no SCAN. EXPIRE creates no absent key, and every key carries the
// {rootSessionID} hash tag, so the pipeline runs on one cluster slot. The
// session server calls it on each committed transition into or out of
// suspended, so a suspended session's budget keys outlive its suspended
// lifetime.
//
// spec: §8.3 (Delegation Policy and Lease)
func (s *Reserver) RearmTree(ctx context.Context, rootSessionID string, parentIDs []string) error {
	if rootSessionID == "" {
		return fmt.Errorf("treebudget: empty root session id")
	}
	root := "{" + rootSessionID + "}:dlg:"
	keys := []string{
		root + "tree_size", root + "tree_memory", root + "tokens",
		hwmKey(rootSessionID),
	}
	for _, p := range parentIDs {
		keys = append(keys, root+"parallel_children:"+p, root+"children_total:"+p)
	}
	_, err := s.client.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, k := range keys {
			p.Expire(ctx, k, s.ttl)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("treebudget: re-arm tree %s: %w", rootSessionID, err)
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
// keys share the `{root_session_id}` hash tag, so a SCAN on the node that
// owns that slot covers the whole tree. Returns the number of keys removed; a
// non-root session id matches no keys and returns 0.
//
// spec: §12.8; §8.3 (Delegation Policy and Lease).
func (s *Reserver) PurgeRoot(ctx context.Context, rootSessionID string) (int, error) {
	if rootSessionID == "" {
		return 0, fmt.Errorf("treebudget: empty root session id")
	}
	// The tree's keys share the {rootSessionID} hash tag, so on a Redis
	// Cluster the SCAN runs on the master that owns that slot
	// (redisconn.ScanDelete routes it by the high-watermark key).
	deleted, err := redisconn.ScanDelete(ctx, s.client, hwmKey(rootSessionID), "{"+rootSessionID+"}:dlg:*")
	if err != nil {
		return deleted, fmt.Errorf("treebudget: purge: %w", err)
	}
	return deleted, nil
}
