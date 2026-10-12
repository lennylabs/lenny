// SPDX-License-Identifier: MIT

package erasure

import (
	"strings"
	"testing"
)

// TestValidateTenantOrder_spec_12_8 covers the tenant Phase 4 order
// check: the gateway's wiring passes, and an unknown store, a duplicate
// store, and every rank inversion, including the session_tree_archive
// after sessions inversion that left Phase 4 stuck, are rejected.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4).
func TestValidateTenantOrder_spec_12_8(t *testing.T) {
	gateway := []string{
		"leases", "quota", "checkpoint_manifests", "checkpoint_retention", "checkpoint_meta",
		"transcripts", "interactions", "memory", "session_tree_archive", "eval_results",
		"sessions", "tokens", "credential_pool",
	}
	cases := []struct {
		name    string
		names   []string
		wantErr string
	}{
		{name: "gateway wiring", names: gateway},
		{name: "empty sequence", names: nil},
		{name: "minimal no-backend wiring", names: []string{"session_tree_archive", "sessions"}},
		{name: "archive after sessions", names: []string{"sessions", "session_tree_archive"}, wantErr: "order violation"},
		{name: "eval results after sessions", names: []string{"sessions", "eval_results"}, wantErr: "order violation"},
		{name: "tokens before sessions", names: []string{"tokens", "sessions"}, wantErr: "order violation"},
		{name: "unknown store", names: []string{"sessions", "mystery_store"}, wantErr: "no §12.8 dependency rank"},
		{name: "duplicate store", names: []string{"session_tree_archive", "session_tree_archive", "sessions"}, wantErr: "more than one slot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTenantOrder(tc.names)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateTenantOrder(%v) = %v, want nil", tc.names, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateTenantOrder(%v) = %v, want an error containing %q", tc.names, err, tc.wantErr)
			}
		})
	}
}

// TestValidateTenantOrderEveryAdjacentSwap_spec_12_8 swaps every
// adjacent pair of the canonical order and asserts each swap is
// rejected, so no store can be moved ahead of one it must follow.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4).
func TestValidateTenantOrderEveryAdjacentSwap_spec_12_8(t *testing.T) {
	order := CanonicalOrder()
	if err := ValidateTenantOrder(order); err != nil {
		t.Fatalf("canonical order rejected: %v", err)
	}
	for i := 0; i+1 < len(order); i++ {
		swapped := append([]string(nil), order...)
		swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
		if err := ValidateTenantOrder(swapped); err == nil {
			t.Errorf("swap of %q and %q accepted", order[i], order[i+1])
		}
	}
}

// TestFKEdgesAgreeWithRanks_spec_12_8 asserts that every foreign-key
// edge the §12.8 sequence must honor ranks its child before its parent,
// which is what lets ValidateTenantOrder rely on rank order alone.
//
// spec: §12.8 (Compliance Interfaces).
func TestFKEdgesAgreeWithRanks_spec_12_8(t *testing.T) {
	for _, edge := range fkMustPrecede {
		child, parent := edge[0], edge[1]
		cr, okc := canonicalRank[child]
		pr, okp := canonicalRank[parent]
		if !okc || !okp {
			t.Fatalf("edge %v names an unranked store", edge)
		}
		if cr >= pr {
			t.Errorf("edge %s → %s: child rank %d is not below parent rank %d", child, parent, cr, pr)
		}
	}
}
