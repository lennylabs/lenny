// SPDX-License-Identifier: MIT

package main

import "testing"

// TestResolveTokenServiceTenant_spec_4_3 covers every combination of an
// explicit --token-service-tenant and the --tenancy-mode values. An
// explicit tenant always wins. Without one, single-tenant mode (and any
// mode other than multi, matching the flag's "single" default) resolves
// to the §4.2 built-in `default` tenant, and multi-tenant mode resolves
// to no fallback because each lease carries its own tenant.
//
// spec: §4.2 (Session Manager, tenant_id claim extraction), §4.3 (Token
// Service), §7.1 (Normal Flow, step 23).
func TestResolveTokenServiceTenant_spec_4_3(t *testing.T) {
	cases := []struct {
		flag, mode, want string
	}{
		{flag: "", mode: "single", want: "default"},
		{flag: "", mode: "", want: "default"},
		{flag: "", mode: "multi", want: ""},
		{flag: "acme", mode: "single", want: "acme"},
		{flag: "acme", mode: "multi", want: "acme"},
		{flag: "acme", mode: "", want: "acme"},
	}
	for _, tc := range cases {
		if got := resolveTokenServiceTenant(tc.flag, tc.mode); got != tc.want {
			t.Errorf("resolveTokenServiceTenant(%q, %q) = %q, want %q", tc.flag, tc.mode, got, tc.want)
		}
	}
}
