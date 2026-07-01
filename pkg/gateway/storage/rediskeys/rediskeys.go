// SPDX-License-Identifier: MIT

// Package rediskeys is the §12.4 Redis wrapper layer that enforces the
// tenant-key isolation convention at the point every command leaves the
// gateway. The §12.4 key table mandates that all Redis keys lead with the
// `t:{tenant_id}:` prefix, with three documented exception classes:
//
//   - `lenny:pod:{pod_id}:*` slot-counter keys (pod-scoped; pod IDs are
//     cluster-globally unique).
//   - `cb:{name}` circuit-breaker keys (platform-scoped operational
//     controls).
//   - `{root_session_id}:dlg:*` delegation-budget keys (tree-scoped hash
//     tag); the wrapper validates the calling tenant owns the
//     `root_session_id` before permitting the operation.
//
// spec §12.4 line 195: "This convention is enforced in the Redis wrapper
// layer; no raw Redis command may be issued without the tenant prefix (or
// pod prefix for slot counters, or `cb:` prefix for circuit breakers, or
// `{root_session_id}:dlg:` prefix for delegation budget keys — the wrapper
// validates the calling tenant owns the `root_session_id` before permitting
// the operation)."
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
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

var (
	// ErrCrossTenant reports that a key carries a `t:{tenant_id}:` prefix
	// for a tenant other than the one in the calling scope.
	ErrCrossTenant = errors.New("rediskeys: key belongs to a different tenant")
	// ErrNoPrefix reports that a key leads with neither the tenant prefix
	// nor any of the three documented exception prefixes.
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
// under RLS before constructing the scope (spec §12.4 line 195;
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

// tenantOf extracts the tenant id from a `t:{tenant_id}:...` key. It
// requires a non-empty tenant segment terminated by a second colon.
func tenantOf(key string) (string, bool) {
	rest := key[len("t:"):]
	idx := strings.IndexByte(rest, ':')
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
	for _, key := range commandKeys(cmd) {
		if err := ValidateKey(scope, key); err != nil {
			return fmt.Errorf("rediskeys: command %q rejected: %w", cmd.Name(), err)
		}
	}
	return nil
}

// keylessCommands carry no key in argument position 1 (or carry a cursor /
// subcommand there). The Guard does not validate these; the keys they
// eventually touch are validated on the per-key commands that follow.
var keylessCommands = map[string]struct{}{
	"ping": {}, "echo": {}, "select": {}, "auth": {}, "hello": {},
	"scan": {}, "cluster": {}, "info": {}, "client": {}, "command": {},
	"dbsize": {}, "flushdb": {}, "flushall": {}, "script": {}, "wait": {},
	"multi": {}, "exec": {}, "discard": {}, "unwatch": {}, "swapdb": {},
}

// multiKeyCommands take an unbounded run of keys starting at argument 1.
var multiKeyCommands = map[string]struct{}{
	"del": {}, "unlink": {}, "mget": {}, "watch": {}, "exists": {},
	"sunion": {}, "sinter": {}, "sdiff": {}, "pfcount": {},
}

// commandKeys returns the key arguments the Guard must validate for cmd.
func commandKeys(cmd redis.Cmder) []string {
	args := cmd.Args()
	if len(args) < 2 {
		return nil
	}
	name := cmd.Name()
	if _, ok := keylessCommands[name]; ok {
		return nil
	}
	switch name {
	case "eval", "evalsha", "eval_ro", "evalsha_ro", "fcall", "fcall_ro":
		// [name, script|sha|fn, numkeys, key1, key2, ..., arg1, ...]
		if len(args) < 3 {
			return nil
		}
		n := toInt(args[2])
		keys := make([]string, 0, n)
		for i := 0; i < n && 3+i < len(args); i++ {
			keys = append(keys, toString(args[3+i]))
		}
		return keys
	case "mset", "msetnx":
		// [name, key1, val1, key2, val2, ...]
		keys := make([]string, 0, (len(args)-1)/2)
		for i := 1; i < len(args); i += 2 {
			keys = append(keys, toString(args[i]))
		}
		return keys
	}
	if _, ok := multiKeyCommands[name]; ok {
		keys := make([]string, 0, len(args)-1)
		for i := 1; i < len(args); i++ {
			keys = append(keys, toString(args[i]))
		}
		return keys
	}
	return []string{toString(args[1])}
}

func toString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(v)
	}
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		i, _ := strconv.Atoi(fmt.Sprint(v))
		return i
	}
}
