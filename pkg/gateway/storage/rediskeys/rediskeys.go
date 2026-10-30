// SPDX-License-Identifier: MIT

// Package rediskeys is the §12.4 Redis wrapper layer that enforces the
// tenant-key isolation convention at the point every command leaves the
// gateway. The §12.4 key table mandates that all Redis keys lead with the
// `t:{tenant_id}:` prefix. The braces are written literally (TenantPrefix
// builds the prefix), so they form a Redis Cluster hash tag and all of a
// tenant's keys share one hash slot; the wrapper admits only that braced
// form. §12.4 groups the remaining keys into two
// exception categories: keys that carry no tenant component by design, and
// keys that are tenant-scoped but not tenant-leading. The wrapper enforces
// the leading prefix only for the exception prefixes it recognizes:
//
//   - `lenny:pod:{pod_id}:*` slot-counter keys (pod-scoped; pod IDs are
//     cluster-globally unique).
//   - `cb:{name}` circuit-breaker keys (platform-scoped operational
//     controls).
//   - `{root_session_id}:dlg:*` delegation-budget keys (tree-scoped hash
//     tag); the wrapper validates the calling tenant owns the
//     `root_session_id` before permitting the operation.
//
// spec §12.4 (enforcement clause): the tenant-leading convention is enforced
// in the Redis wrapper layer. A command that attaches a Scope via WithScope
// is validated, and its keys must lead with the scope's `t:{tenant_id}:`
// prefix or with one of the recognized exception prefixes (`lenny:pod:`,
// `cb:`, or `{root_session_id}:dlg:`, where the wrapper additionally
// validates the calling tenant owns the `root_session_id`). Keys that carry
// no leading prefix the wrapper recognizes (`rl:`, `sq:`, `pg:sess-tenant:`,
// `conn:oauth:state:`, `derive_lock:`, `lenny:events:`, and
// `lenny:session:terminate`) are issued on unscoped or platform-scoped
// command paths the wrapper passes through unvalidated; their isolation is
// structural rather than leading-prefix enforcement.
//
// Enforcement is a go-redis Hook (Guard) installed on the shared client.
// The hook validates the key arguments of every command against the Scope
// carried in the command's context. A command issued without a Scope is
// passed through unvalidated so unmigrated call sites and genuinely
// platform-scoped operations (PlatformRedis) are not broken; call paths
// that attach a Scope via WithScope receive full cross-tenant enforcement.
package rediskeys

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn"
)

var (
	// ErrCrossTenant reports that a key carries a `t:{tenant_id}:` prefix
	// for a tenant other than the one in the calling scope.
	ErrCrossTenant = errors.New("rediskeys: key belongs to a different tenant")
	// ErrNoPrefix reports that a key leads with neither the tenant prefix
	// nor any of the recognized §12.4 exception prefixes (`lenny:pod:`,
	// `cb:`, or `{root_session_id}:dlg:`).
	ErrNoPrefix = errors.New("rediskeys: key has no tenant or exception prefix")
	// ErrDelegationOwnership reports that a `{root_session_id}:dlg:` key
	// names a root session the calling tenant does not own.
	ErrDelegationOwnership = errors.New("rediskeys: delegation budget key root_session_id not owned by calling scope")
)

// Scope is the per-request tenant context the wrapper validates keys
// against. The zero Scope (empty tenant) is the platform/unscoped scope:
// it permits any in-prefix key but never authorizes a delegation-budget
// key (which always requires explicit root-session ownership).
type Scope struct {
	tenantID   string
	ownedRoots map[string]struct{}
}

// TenantScope returns a Scope bound to tenantID. Keys under
// `t:{tenantID}:` and the platform-wide `cb:` / `lenny:pod:` exceptions
// are permitted; cross-tenant keys are rejected.
func TenantScope(tenantID string) Scope {
	return Scope{tenantID: tenantID}
}

// DelegationScope returns a Scope bound to tenantID that additionally
// authorizes `{root}:dlg:*` keys for each root session id in
// ownedRootSessionIDs. The gateway resolves ownership via SessionStore
// under RLS before constructing the scope (spec §12.4 enforcement clause;
// §8.3 R-04).
func DelegationScope(tenantID string, ownedRootSessionIDs ...string) Scope {
	owned := make(map[string]struct{}, len(ownedRootSessionIDs))
	for _, id := range ownedRootSessionIDs {
		owned[id] = struct{}{}
	}
	return Scope{tenantID: tenantID, ownedRoots: owned}
}

// TenantID returns the tenant the scope is bound to ("" for platform).
func (s Scope) TenantID() string { return s.tenantID }

func (s Scope) ownsRoot(root string) bool {
	_, ok := s.ownedRoots[root]
	return ok
}

type scopeCtxKey struct{}

// WithScope returns a context carrying scope. Redis commands issued with
// the returned context are validated against scope by the Guard hook.
func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeCtxKey{}, scope)
}

// ScopeFromContext returns the Scope attached by WithScope, if any.
func ScopeFromContext(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(scopeCtxKey{}).(Scope)
	return s, ok
}

// ValidateKey checks key against scope per the §12.4 convention. A key is
// admitted when it leads with the scope's `t:{tenant_id}:` prefix, with a
// `cb:` or `lenny:pod:` exception prefix, or with a `{root}:dlg:` prefix
// for a root session the scope owns. An empty key is admitted (keyless
// command argument).
func ValidateKey(scope Scope, key string) error {
	if key == "" {
		return nil
	}
	switch {
	case strings.HasPrefix(key, "cb:"):
		return nil
	case strings.HasPrefix(key, "lenny:pod:"):
		return nil
	case strings.HasPrefix(key, "t:"):
		t, ok := tenantOf(key)
		if !ok {
			return fmt.Errorf("%w: %q", ErrNoPrefix, key)
		}
		if scope.tenantID != "" && t != scope.tenantID {
			return fmt.Errorf("%w: key tenant %q, scope tenant %q", ErrCrossTenant, t, scope.tenantID)
		}
		return nil
	}
	if root, ok := delegationRoot(key); ok {
		if scope.ownsRoot(root) {
			return nil
		}
		return fmt.Errorf("%w: root_session_id %q", ErrDelegationOwnership, root)
	}
	return fmt.Errorf("%w: %q", ErrNoPrefix, key)
}

// tenantKeyOpen and tenantKeyClose bracket the tenant id in every
// tenant-scoped key: `t:{` + tenant + `}:`.
const (
	tenantKeyOpen  = "t:{"
	tenantKeyClose = "}:"
)

// TenantPrefix returns the §12.4 `t:{tenant_id}:` key prefix for tenantID,
// with the braces written literally. The braces are a Redis Cluster hash
// tag, so every key a tenant owns hashes to the slot of tenantID and a
// script, transaction, or multi-key command over one tenant's keys stays in
// one slot. Every store builds its tenant keys, tenant SCAN patterns, and
// tenant pub/sub channel names from this prefix.
//
// spec: §12.4 Redis HA and Failure Modes ("Tenant key isolation"; the
// Redis Cluster pre-plan: the `t:{tenant_id}:` prefix hashes all keys for
// a tenant to the same slot); §17.8.2.
func TenantPrefix(tenantID string) string {
	return tenantKeyOpen + tenantID + tenantKeyClose
}

// TenantOf returns the tenant id a `t:{tenant_id}:...` key or channel
// carries. It accepts only the braced form TenantPrefix writes: a key
// without the braces, or with an empty tenant segment, returns false.
func TenantOf(key string) (string, bool) {
	return tenantOf(key)
}

// tenantOf extracts the tenant id from a `t:{tenant_id}:...` key. It
// requires the literal `t:{` opener, a non-empty tenant segment, and the
// `}:` closer, so an unbraced `t:<tenant>:` key is rejected.
func tenantOf(key string) (string, bool) {
	if !strings.HasPrefix(key, tenantKeyOpen) {
		return "", false
	}
	rest := key[len(tenantKeyOpen):]
	idx := strings.Index(rest, tenantKeyClose)
	if idx <= 0 {
		return "", false
	}
	return rest[:idx], true
}

// delegationRoot extracts the `{root_session_id}` (or bare
// `root_session_id`) that precedes a `:dlg:` segment, stripping the
// optional Redis Cluster hash-tag braces.
func delegationRoot(key string) (string, bool) {
	idx := strings.Index(key, ":dlg:")
	if idx <= 0 {
		return "", false
	}
	root := key[:idx]
	root = strings.TrimPrefix(root, "{")
	root = strings.TrimSuffix(root, "}")
	if root == "" {
		return "", false
	}
	return root, true
}

// Guard is the go-redis Hook that enforces ValidateKey on every command
// whose context carries a Scope.
type Guard struct{}

// NewGuard returns a Guard ready to install via (redis.UniversalClient).AddHook.
func NewGuard() *Guard { return &Guard{} }

var _ redis.Hook = (*Guard)(nil)

// DialHook passes through; the Guard inspects commands, not dials.
func (g *Guard) DialHook(next redis.DialHook) redis.DialHook { return next }

// ProcessHook validates a single command before it is issued.
func (g *Guard) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := g.check(ctx, cmd); err != nil {
			return err
		}
		return next(ctx, cmd)
	}
}

// ProcessPipelineHook validates every command in a pipeline before any is
// issued; one cross-tenant command rejects the whole pipeline.
func (g *Guard) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if err := g.check(ctx, cmd); err != nil {
				return err
			}
		}
		return next(ctx, cmds)
	}
}

func (g *Guard) check(ctx context.Context, cmd redis.Cmder) error {
	scope, ok := ScopeFromContext(ctx)
	if !ok {
		return nil
	}
	for _, key := range redisconn.CommandKeys(cmd) {
		if err := ValidateKey(scope, key); err != nil {
			return fmt.Errorf("rediskeys: command %q rejected: %w", cmd.Name(), err)
		}
	}
	return nil
}
