// SPDX-License-Identifier: MIT

package erasure

import (
	"strings"
	"testing"
)

// TestValidateTenantOrder_spec_12_8 covers the tenant Phase 4 order
// check over the gateway's tenant-path wiring: the wiring passes, and every rank
// inversion that left Phase 4 stuck, including session_tree_archive after
// sessions, is rejected.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4).
func TestValidateTenantOrder_spec_12_8(t *testing.T) {
	move := func(name, after string) []string {
		var out []string
		for _, n := range GatewayTenantPhase4Names() {
			if n == name {
				continue
			}
			out = append(out, n)
			if n == after {
				out = append(out, name)
			}
		}
		return out
	}
	cases := []struct {
		name    string
		names   []string
		wantErr string
	}{
		{name: "gateway wiring", names: GatewayTenantPhase4Names()},
		{name: "empty sequence", names: nil, wantErr: "neither wired nor exempt"},
		{name: "archive after sessions", names: move("session_tree_archive", "sessions"), wantErr: "order violation"},
		{name: "eval results after sessions", names: move("eval_results", "sessions"), wantErr: "order violation"},
		{name: "eval results before billing", names: move("eval_results", "audit"), wantErr: "order violation"},
		{name: "tokens before sessions", names: move("tokens", "delegation_budget"), wantErr: "order violation"},
		{name: "unknown store", names: append(GatewayTenantPhase4Names(), "mystery_store"), wantErr: "no §12.8 dependency rank"},
		{name: "duplicate store", names: append(GatewayTenantPhase4Names(), "tenant_config"), wantErr: "more than one slot"},
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
	order := GatewayTenantPhase4Names()
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
