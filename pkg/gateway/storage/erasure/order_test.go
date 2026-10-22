// SPDX-License-Identifier: MIT

package erasure

import (
	"context"
	"sort"
	"strings"
	"testing"
)

func noopUser(context.Context, string, string) (int, error) { return 0, nil }

// withoutUserEntry returns the Store table user wiring with name removed.
func withoutUserEntry(name string) Config {
	cfg := StoreTableUserConfig()
	var ss []SessionEraser
	for _, se := range cfg.SessionScoped {
		if se.Name != name {
			ss = append(ss, se)
		}
	}
	var us []Eraser
	for _, e := range cfg.UserScoped {
		if e.Name != name {
			us = append(us, e)
		}
	}
	cfg.SessionScoped, cfg.UserScoped = ss, us
	return cfg
}

// withoutName returns names with name removed.
func withoutName(names []string, name string) []string {
	var out []string
	for _, n := range names {
		if n != name {
			out = append(out, n)
		}
	}
	return out
}

// TestValidatorsCompletenessAndOrder_spec_12_8 runs both validators over the
// Store table wiring and over each kind of defective wiring: a ranked store
// missing from the path, a store both wired and exempt, an exemption with an
// empty reason, an unranked store, a duplicate, and a rank inversion on the
// tenant path. Each defect is rejected with the store and the path named.
//
// spec: 12.8 (Compliance Interfaces: Storage backends in erasure scope),
// 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion
// sequence), 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestValidatorsCompletenessAndOrder_spec_12_8(t *testing.T) {
	type tc struct {
		name    string
		run     func() error
		wantErr []string
	}
	withExempt := func(m map[string]string, name, reason string, fn func() error) func() error {
		return func() error {
			prev, had := m[name]
			m[name] = reason
			defer func() {
				if had {
					m[name] = prev
				} else {
					delete(m, name)
				}
			}()
			return fn()
		}
	}
	tenant := StoreTableTenantNames()
	swapped := StoreTableTenantNames()
	swapped[0], swapped[1] = swapped[1], swapped[0]
	cases := []tc{
		{name: "user store table wiring", run: func() error { return ValidateOrder(StoreTableUserConfig()) }},
		{name: "tenant store table wiring", run: func() error { return ValidateTenantOrder(tenant) }},
		{
			name:    "user missing ranked store",
			run:     func() error { return ValidateOrder(withoutUserEntry("checkpoint_meta")) },
			wantErr: []string{"user erasure", `"checkpoint_meta"`, "neither wired nor exempt"},
		},
		{
			name:    "tenant missing ranked store",
			run:     func() error { return ValidateTenantOrder(withoutName(tenant, "tenant_config")) },
			wantErr: []string{"tenant deletion Phase 4", `"tenant_config"`, "neither wired nor exempt"},
		},
		{
			name:    "user store both wired and exempt",
			run:     withExempt(userExempt, "audit", "a reason", func() error { return ValidateOrder(StoreTableUserConfig()) }),
			wantErr: []string{"user erasure", `"audit"`, "both wired and exempt"},
		},
		{
			name:    "tenant store both wired and exempt",
			run:     withExempt(tenantExempt, "tokens", "a reason", func() error { return ValidateTenantOrder(tenant) }),
			wantErr: []string{"tenant deletion Phase 4", `"tokens"`, "both wired and exempt"},
		},
		{
			name:    "user exempt with empty reason",
			run:     withExempt(userExempt, "billing", "", func() error { return ValidateOrder(StoreTableUserConfig()) }),
			wantErr: []string{"user erasure", `"billing"`, "empty reason"},
		},
		{
			name:    "tenant exempt with empty reason",
			run:     withExempt(tenantExempt, "redis_caches", "", func() error { return ValidateTenantOrder(tenant) }),
			wantErr: []string{"tenant deletion Phase 4", `"redis_caches"`, "empty reason"},
		},
		{
			name: "user unknown store",
			run: func() error {
				cfg := StoreTableUserConfig()
				cfg.UserScoped = append(cfg.UserScoped, Eraser{Name: "mystery_store"})
				return ValidateOrder(cfg)
			},
			wantErr: []string{"user erasure", `"mystery_store"`, "no §12.8 dependency rank"},
		},
		{
			name:    "tenant unknown store",
			run:     func() error { return ValidateTenantOrder(append(StoreTableTenantNames(), "mystery_store")) },
			wantErr: []string{"tenant deletion Phase 4", `"mystery_store"`, "no §12.8 dependency rank"},
		},
		{
			name: "user duplicate across slots",
			run: func() error {
				cfg := StoreTableUserConfig()
				cfg.UserScoped = append(cfg.UserScoped, Eraser{Name: "artifacts"})
				return ValidateOrder(cfg)
			},
			wantErr: []string{"user erasure", `"artifacts"`, "more than one slot"},
		},
		{
			name:    "tenant duplicate",
			run:     func() error { return ValidateTenantOrder(append([]string{"leases"}, tenant...)) },
			wantErr: []string{"tenant deletion Phase 4", `"leases"`, "more than one slot"},
		},
		{
			name:    "tenant rank inversion",
			run:     func() error { return ValidateTenantOrder(swapped) },
			wantErr: []string{"order violation", `"leases"`, `"coordination_mirror"`},
		},
		{
			name:    "tenant exempt store unranked",
			run:     withExempt(tenantExempt, "mystery_store", "a reason", func() error { return ValidateTenantOrder(tenant) }),
			wantErr: []string{"tenant deletion Phase 4", `"mystery_store"`, "no §12.8 dependency rank"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			if len(c.wantErr) == 0 {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("got nil, want an error naming %v", c.wantErr)
			}
			for _, w := range c.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

// TestCanonicalRankNameSet_spec_12_8 pins the set of ranked store names. A
// store added to or dropped from the §12.8 scope must change this list in
// a reviewed edit, together with the Store table wiring on both paths.
//
// spec: 12.8 (Compliance Interfaces: Storage backends in erasure scope)
func TestCanonicalRankNameSet_spec_12_8(t *testing.T) {
	// spec: 12.8
	want := []string{
		"artifacts", "audit", "billing", "billing_buffer", "checkpoint_manifests",
		"checkpoint_meta", "checkpoint_retention", "coordination_mirror", "credential_pool",
		"custom_roles", "delegation_budget", "eval_results", "eviction_state",
		"experiment_sticky", "interactions", "issued_tokens", "leases", "memory",
		"pool_tenant_access", "quota", "redis_caches", "runtime_tenant_access",
		"semantic_cache", "session_dlq_archive", "session_message_queues",
		"session_tree_archive", "sessions", "tenant_config", "tenant_definitions", "tokens",
		"transcripts", "user_roles",
	}
	got := make([]string, 0, len(canonicalRank))
	for n := range canonicalRank {
		got = append(got, n)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("canonicalRank names = %v, want %v", got, want)
	}
}

// TestTenantRankOrderFollowsPhase4_spec_12_8 pins the rank order of the
// §12.8 Phase 4 text: EvalResultStore after the billing EventStore and
// before the delegation budget keys, and the tail in the order the text
// lists it.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestTenantRankOrderFollowsPhase4_spec_12_8(t *testing.T) {
	if got := strings.Join(CanonicalOrder(), ","); got != strings.Join(withExemptions(storeTableTenant), ",") {
		t.Fatalf("CanonicalOrder = %s", got)
	}
}

// withExemptions merges the tenant exemptions into names at their ranks.
func withExemptions(names []string) []string {
	all := append([]string(nil), names...)
	for n := range tenantExempt {
		all = append(all, n)
	}
	sort.Slice(all, func(i, j int) bool { return canonicalRank[all[i]] < canonicalRank[all[j]] })
	return all
}

// session_tree_archive erased after SessionStore is an FK violation
// (migration 0100 root_session_id → sessions(id), no ON DELETE action).
//
// spec: 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence)
func TestValidateOrderTreeArchiveAfterSessionsRejected_spec_12_8(t *testing.T) {
	cfg := withoutUserEntry("session_tree_archive")
	cfg.UserScoped = append(cfg.UserScoped, Eraser{Name: "session_tree_archive", DeleteByUser: noopUser})
	if err := ValidateOrder(cfg); err == nil || !strings.Contains(err.Error(), "FK order violation") {
		t.Fatalf("session_tree_archive after sessions: got %v, want an FK order violation", err)
	}
}

// The eval_results → sessions edge the spec names explicitly is enforced
// even across the session/user-scoped bucket boundary.
//
// spec: 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence)
func TestValidateOrderEvalAfterSessionsRejected_spec_12_8(t *testing.T) {
	cfg := withoutUserEntry("eval_results")
	cfg.UserScoped = append(cfg.UserScoped, Eraser{Name: "eval_results", DeleteByUser: noopUser})
	if err := ValidateOrder(cfg); err == nil || !strings.Contains(err.Error(), "FK order violation") {
		t.Fatalf("eval_results after sessions: got %v, want an FK order violation", err)
	}
}

// The user path is held to the FK edges only, so swapping two user-scoped
// entries out of rank order, which the tenant path would reject, passes.
//
// spec: 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence)
func TestValidateOrderAllowsNonRankOrderWithinFKEdges_spec_12_8(t *testing.T) {
	cfg := StoreTableUserConfig()
	cfg.UserScoped[0], cfg.UserScoped[1] = cfg.UserScoped[1], cfg.UserScoped[0]
	if err := ValidateOrder(cfg); err != nil {
		t.Fatalf("a non-rank user order that honors every FK edge was rejected: %v", err)
	}
}

// CanonicalOrder is sorted by rank and places sessions after its FK
// children and the post-session stores after it.
//
// spec: 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence)
func TestCanonicalOrderSessionsAfterChildren_spec_12_8(t *testing.T) {
	order := CanonicalOrder()
	idx := map[string]int{}
	for i, n := range order {
		idx[n] = i
	}
	for _, child := range []string{"eval_results", "session_tree_archive", "artifacts", "interactions", "memory"} {
		if idx[child] >= idx["sessions"] {
			t.Errorf("%q must precede sessions in the canonical order", child)
		}
	}
	for _, after := range []string{"tokens", "issued_tokens", "credential_pool", "tenant_config"} {
		if idx[after] <= idx["sessions"] {
			t.Errorf("%q must follow sessions in the canonical order", after)
		}
	}
}
