// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

type capturingAudit struct{ events []AuditEvent }

func (c *capturingAudit) EmitAdminEvent(_ context.Context, e AuditEvent) {
	c.events = append(c.events, e)
}

// spec: §8.3 lines 349-352 / §11.2.1 — registering a weaker pool that a
// more-restrictive parent could delegate to under an active
// DelegationPolicy rule emits pool.isolation_warning to both the §11.7
// audit chain and the §11.2.1 billing stream (under the affected
// tenant). F-11.2.1.
func TestEmitPoolIsolationWarnings_spec_8_3_350(t *testing.T) {
	rts := runtimestore.NewMemory()
	if err := rts.Create(context.Background(), runtimestore.Runtime{Name: "coder", Type: runtimestore.TypeAgent}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: "kata-pool", RuntimeRef: "coder", IsolationProfile: isolation.ProfileMicrovm,
		ExecutionMode: runtimestore.ExecutionModeSession,
	}); err != nil {
		t.Fatalf("seed kata-pool: %v", err)
	}
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: "runc-pool", RuntimeRef: "coder", IsolationProfile: isolation.ProfileStandard,
		AllowStandardIsolation: true, ExecutionMode: runtimestore.ExecutionModeSession,
	}); err != nil {
		t.Fatalf("seed runc-pool: %v", err)
	}
	pols := delegationpolicystore.NewMemory()
	if err := pols.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		Name: "team-agents", TenantID: "acme",
		Rules: []delegationpolicystore.Rule{{Target: delegationpolicystore.Target{Types: []string{"agent"}}, Allow: true}},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	billing := billingstore.NewMemory()
	cap := &capturingAudit{}
	r := NewRouter(tenantstore.NewMemory(), Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: cap,
	}).WithPools(pools).WithRuntimes(rts).WithDelegationPolicies(pols).
		WithBillingCorrections(billing, correctionstore.NewMemory(), 0)

	p := authmw.Principal{Subject: "admin@acme.com", TenantID: "platform", Roles: []auth.Role{auth.RolePlatformAdmin}}

	// Registering the weak runc-pool surfaces the kata-pool→runc-pool warning.
	r.emitPoolIsolationWarnings(context.Background(), p, "runc-pool")

	// Audit chain received the warning.
	var auditWarn *AuditEvent
	for i := range cap.events {
		if cap.events[i].Type == string(obsaudit.EventPoolIsolationWarning) {
			auditWarn = &cap.events[i]
		}
	}
	if auditWarn == nil {
		t.Fatalf("no pool.isolation_warning audit event in %+v", cap.events)
	}
	if auditWarn.Detail["conflicting_pool_name"] != "kata-pool" || auditWarn.Detail["pool_isolation"] != "standard" {
		t.Errorf("audit detail = %+v", auditWarn.Detail)
	}

	// Billing stream received the warning under the affected tenant (acme).
	evs, err := billing.Since(context.Background(), "acme", 0, 10)
	if err != nil {
		t.Fatalf("billing Since: %v", err)
	}
	if len(evs) != 1 || evs[0].EventType != billingstore.EventPoolIsolationWarning {
		t.Fatalf("billing = %+v", evs)
	}
	c := evs[0].Conditional
	if c == nil || c.PoolName != "runc-pool" || c.ConflictingPoolName != "kata-pool" ||
		c.PoolIsolation != "standard" || c.ConflictingIsolation != "microvm" {
		t.Fatalf("billing conditional = %+v", c)
	}

	// Registering the strongest pool surfaces nothing.
	billing2 := billingstore.NewMemory()
	r2 := NewRouter(tenantstore.NewMemory(), Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: &capturingAudit{},
	}).WithPools(pools).WithRuntimes(rts).WithDelegationPolicies(pols).
		WithBillingCorrections(billing2, correctionstore.NewMemory(), 0)
	r2.emitPoolIsolationWarnings(context.Background(), p, "kata-pool")
	if evs, _ := billing2.Since(context.Background(), "acme", 0, 10); len(evs) != 0 {
		t.Errorf("kata-pool (strongest) must produce no billing warnings, got %+v", evs)
	}
}
