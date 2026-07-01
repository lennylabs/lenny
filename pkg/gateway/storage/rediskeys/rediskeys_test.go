// SPDX-License-Identifier: MIT

package rediskeys_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
)

// spec §12.4 line 177-195: every key leads with `t:{tenant_id}:` or one of
// the three documented exception prefixes.
func TestValidateKey_spec_12_4(t *testing.T) {
	acme := rediskeys.TenantScope("acme")
	dlgScope := rediskeys.DelegationScope("acme", "root-1")

	cases := []struct {
		name  string
		scope rediskeys.Scope
		key   string
		want  error
	}{
		{"own tenant lease", acme, "t:acme:lease:session:s1", nil},
		{"own tenant inbox", acme, "t:acme:session:s1:inbox", nil},
		{"cross tenant rejected", acme, "t:globex:session:s1:dlq", rediskeys.ErrCrossTenant},
		{"cb exception", acme, "cb:pool:default", nil},
		{"pod exception", acme, "lenny:pod:pod-7:active_slots", nil},
		{"no prefix rejected", acme, "raw:key", rediskeys.ErrNoPrefix},
		{"empty tenant segment rejected", acme, "t::oops", rediskeys.ErrNoPrefix},
		{"empty key admitted", acme, "", nil},
		{"platform scope admits any tenant", rediskeys.TenantScope(""), "t:globex:billing:stream", nil},
		{"dlg owned root", dlgScope, "{root-1}:dlg:tokens", nil},
		{"dlg owned root no braces", dlgScope, "root-1:dlg:tree_size", nil},
		{"dlg unowned root rejected", dlgScope, "{root-9}:dlg:tokens", rediskeys.ErrDelegationOwnership},
		{"dlg rejected without ownership", acme, "{root-1}:dlg:tokens", rediskeys.ErrDelegationOwnership},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := rediskeys.ValidateKey(tc.scope, tc.key)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("ValidateKey(%q) = %v, want nil", tc.key, err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateKey(%q) = %v, want %v", tc.key, err, tc.want)
			}
		})
	}
}

// guardedClient wires the Guard hook onto a miniredis-backed client, the
// same installation NewSingleShardRouter performs in production.
func guardedClient(t *testing.T) redis.UniversalClient {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cl.AddHook(rediskeys.NewGuard())
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// spec §12.4 line 195: no raw Redis command may be issued without the
// tenant prefix. The Guard rejects a cross-tenant SET before it reaches
// Redis when the calling context is scoped to a different tenant.
func TestGuard_CrossTenantWriteRejected_spec_12_4(t *testing.T) {
	cl := guardedClient(t)
	ctx := rediskeys.WithScope(context.Background(), rediskeys.TenantScope("acme"))

	if err := cl.Set(ctx, "t:globex:scache:t:x", "v", 0).Err(); !errors.Is(err, rediskeys.ErrCrossTenant) {
		t.Fatalf("cross-tenant SET err = %v, want ErrCrossTenant", err)
	}
	if err := cl.Set(ctx, "t:acme:scache:t:x", "v", 0).Err(); err != nil {
		t.Fatalf("same-tenant SET err = %v, want nil", err)
	}
}

// An unscoped context (no WithScope) is passed through unvalidated so
// unmigrated call paths and platform-scoped operations are not broken.
func TestGuard_UnscopedContextPassesThrough_spec_12_4(t *testing.T) {
	cl := guardedClient(t)
	if err := cl.Set(context.Background(), "rl:u:acme:alice:42", "1", 0).Err(); err != nil {
		t.Fatalf("unscoped SET err = %v, want nil", err)
	}
}

// The Guard validates pipelines: one cross-tenant command rejects the
// whole pipeline before any command is issued.
func TestGuard_PipelineCrossTenantRejected_spec_12_4(t *testing.T) {
	cl := guardedClient(t)
	ctx := rediskeys.WithScope(context.Background(), rediskeys.TenantScope("acme"))
	pipe := cl.Pipeline()
	pipe.Set(ctx, "t:acme:scache:t:a", "1", 0)
	pipe.Set(ctx, "t:globex:scache:t:b", "2", 0)
	if _, err := pipe.Exec(ctx); !errors.Is(err, rediskeys.ErrCrossTenant) {
		t.Fatalf("pipeline err = %v, want ErrCrossTenant", err)
	}
}

// EVAL key extraction honors numkeys: the Guard validates the declared
// keys, not the trailing script arguments.
func TestGuard_EvalKeyExtraction_spec_12_4(t *testing.T) {
	cl := guardedClient(t)
	ctx := rediskeys.WithScope(context.Background(), rediskeys.TenantScope("acme"))
	// numkeys=1, key=t:globex:..., arg=ignored. Must reject on the key.
	script := redis.NewScript("return 1")
	if err := script.Eval(ctx, cl, []string{"t:globex:session:s:inbox"}, "arg").Err(); !errors.Is(err, rediskeys.ErrCrossTenant) {
		t.Fatalf("EVAL cross-tenant err = %v, want ErrCrossTenant", err)
	}
}
