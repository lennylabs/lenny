// SPDX-License-Identifier: MIT

package erasure

import (
	"context"
	"testing"
)

// spec: §12.8 lines 792-836 — the DeleteByUser dependency order is a
// runtime contract; ValidateOrder pins it so a future reorder of the
// store wiring cannot erase a foreign-key parent before its children.

func noopUser(context.Context, string, string) (int, error)    { return 0, nil }
func noopSession(context.Context, string, string) (int, error) { return 0, nil }

// The canonical wiring the gateway uses passes validation.
func TestValidateOrder_canonicalConfigPasses(t *testing.T) {
	cfg := Config{
		SessionScoped: []SessionEraser{
			{Name: "transcripts", DeleteBySession: noopSession},
			{Name: "artifacts", DeleteBySession: noopSession},
			{Name: "eval_results", DeleteBySession: noopSession},
		},
		UserScoped: []Eraser{
			{Name: "memory", DeleteByUser: noopUser},
			{Name: "interactions", DeleteByUser: noopUser},
			{Name: "sessions", DeleteByUser: noopUser},
		},
	}
	if err := ValidateOrder(cfg); err != nil {
		t.Fatalf("canonical config must validate: %v", err)
	}
}

// The full §12.8 production wiring (every store the gateway erases once
// the §12.8.13 / §12.8.15 coverage lands) validates: the FK children
// precede SessionStore and the post-session stores (tokens,
// credential_pool) follow it. This pins the gateway's orchestrator
// wiring against the dependency contract.
//
// spec: §12.8 lines 792-836 (the 20-step DeleteByUser sequence).
func TestValidateOrder_fullProductionWiringPasses_spec_12_8_792(t *testing.T) {
	cfg := Config{
		SessionScoped: []SessionEraser{
			{Name: "transcripts", DeleteBySession: noopSession},
			{Name: "artifacts", DeleteBySession: noopSession},
			{Name: "eval_results", DeleteBySession: noopSession},
			{Name: "session_tree_archive", DeleteBySession: noopSession},
			{Name: "delegation_budget", DeleteBySession: noopSession},
		},
		UserScoped: []Eraser{
			{Name: "leases", DeleteByUser: noopUser},
			{Name: "experiment_sticky", DeleteByUser: noopUser},
			{Name: "billing_buffer", DeleteByUser: noopUser},
			{Name: "quota", DeleteByUser: noopUser},
			{Name: "memory", DeleteByUser: noopUser},
			{Name: "interactions", DeleteByUser: noopUser},
			{Name: "sessions", DeleteByUser: noopUser},
			{Name: "tokens", DeleteByUser: noopUser},
			{Name: "credential_pool", DeleteByUser: noopUser},
		},
	}
	if err := ValidateOrder(cfg); err != nil {
		t.Fatalf("full production wiring must validate: %v", err)
	}
}

// session_tree_archive erased after SessionStore is an FK violation
// (migration 0100 root_session_id → sessions(id), no ON DELETE action).
func TestValidateOrder_treeArchiveAfterSessionsRejected_spec_12_8_826(t *testing.T) {
	cfg := Config{
		UserScoped: []Eraser{
			{Name: "sessions", DeleteByUser: noopUser},
			{Name: "session_tree_archive", DeleteByUser: noopUser},
		},
	}
	if err := ValidateOrder(cfg); err == nil {
		t.Fatal("session_tree_archive after sessions must be rejected (FK)")
	}
}

// A user-scoped wiring that erases SessionStore before its FK child
// (interactions) is rejected.
func TestValidateOrder_sessionsBeforeChildRejected(t *testing.T) {
	cfg := Config{
		UserScoped: []Eraser{
			{Name: "sessions", DeleteByUser: noopUser},
			{Name: "interactions", DeleteByUser: noopUser},
		},
	}
	if err := ValidateOrder(cfg); err == nil {
		t.Fatal("erasing sessions before interactions must be rejected as an FK order violation")
	}
}

// The eval_results → sessions edge the spec names explicitly is enforced
// even across the session/user-scoped bucket boundary.
func TestValidateOrder_evalAfterSessionsRejected(t *testing.T) {
	// eval_results wired as user-scoped after sessions (a misconfiguration).
	cfg := Config{
		UserScoped: []Eraser{
			{Name: "sessions", DeleteByUser: noopUser},
			{Name: "eval_results", DeleteByUser: noopUser},
		},
	}
	if err := ValidateOrder(cfg); err == nil {
		t.Fatal("eval_results after sessions must be rejected (§12.8 line 808 FK)")
	}
}

// An unranked store name is rejected so a newly added store must be
// assigned a §12.8 dependency rank.
func TestValidateOrder_unknownStoreRejected(t *testing.T) {
	cfg := Config{
		UserScoped: []Eraser{{Name: "mystery_store", DeleteByUser: noopUser}},
	}
	if err := ValidateOrder(cfg); err == nil {
		t.Fatal("a store with no canonical rank must be rejected")
	}
}

// A store wired into two slots is rejected.
func TestValidateOrder_duplicateStoreRejected(t *testing.T) {
	cfg := Config{
		SessionScoped: []SessionEraser{{Name: "artifacts", DeleteBySession: noopSession}},
		UserScoped:    []Eraser{{Name: "artifacts", DeleteByUser: noopUser}},
	}
	if err := ValidateOrder(cfg); err == nil {
		t.Fatal("a store wired into two slots must be rejected")
	}
}

// CanonicalOrder is sorted by rank and places sessions after its FK
// children.
func TestCanonicalOrder_sessionsAfterChildren(t *testing.T) {
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
	// tokens and credential_pool come after sessions per §12.8 steps 18-19.
	for _, after := range []string{"tokens", "credential_pool"} {
		if idx[after] <= idx["sessions"] {
			t.Errorf("%q must follow sessions in the canonical order", after)
		}
	}
}
