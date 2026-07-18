// SPDX-License-Identifier: MIT

package admin

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// TestNewRouter_ThreadsTenancyMode pins that the platform tenancy.mode
// passed through admin.Options reaches the Router. The warm-pool
// pool-registration layer-1 check reads exactly this field to gate the
// §4.9 cross-tenant credential-delivery rejections on multi-tenant mode,
// so a NewRouter that ignored Options.TenancyMode would silently leave
// every deployment reading the empty (non-multi) mode and never enforce.
// The value is sourced from the gateway --tenancy-mode flag, the same
// tenancy signal the layer-2 webhook binary reads.
//
// spec: §4.9.
func TestNewRouter_ThreadsTenancyMode(t *testing.T) {
	r := NewRouter(tenantstore.NewMemory(), Options{TenancyMode: "multi"})
	if r.tenancyMode != "multi" {
		t.Errorf("tenancyMode = %q, want %q (Options.TenancyMode not threaded)", r.tenancyMode, "multi")
	}
}

// TestNewRouter_TenancyModeDefaultsEmpty confirms a Router built without
// an explicit TenancyMode leaves the field the empty string, which the
// enforced() predicate treats as non-multi (the fail-open-safe default a
// single-tenant or unset deployment carries). A Router that defaulted the
// field to "multi" would reject the §4.9 combinations on a single-tenant
// deployment where they are permitted.
//
// spec: §4.9.
func TestNewRouter_TenancyModeDefaultsEmpty(t *testing.T) {
	r := NewRouter(tenantstore.NewMemory(), Options{})
	if r.tenancyMode != "" {
		t.Errorf("tenancyMode = %q, want empty with no Options", r.tenancyMode)
	}
}
