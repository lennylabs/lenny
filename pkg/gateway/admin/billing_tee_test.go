// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptorstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §11.2.1 — the interceptor failPolicy transition is a billing-stream
// cost-attribution / compliance event. A weakening PUT tees
// interceptor.fail_policy_weakened plus the once-per-window
// interceptor.weakening_cooldown_active into the per-tenant billing
// ledger alongside the §11.7 audit chain. F-11.2.1.
func TestInterceptorWeakeningTeesBilling_spec_11_2_1(t *testing.T) {
	ics := interceptorstore.NewMemory()
	pols := delegationpolicystore.NewMemory()
	billing := billingstore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: &recordingAudit{},
	}).WithInterceptors(ics, 60).WithDelegationPolicies(pols).
		WithBillingCorrections(billing, correctionstore.NewMemory(), 0)
	h := router.Handler()

	doAdminReq(t, h, http.MethodPost, "/v1/admin/interceptors", validInterceptorPayload("scan"), withAdminPrincipal)
	if err := pols.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "p1",
		ContentPolicy: delegationpolicystore.ContentPolicy{InterceptorRef: "scan"},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	body := validInterceptorPayload("scan")
	body.FailPolicy = "fail-open"
	if rr := doAdminReq(t, h, http.MethodPut, "/v1/admin/interceptors/scan", body, withAdminPrincipal); rr.Code != http.StatusOK {
		t.Fatalf("weaken: status %d, body %s", rr.Code, rr.Body.String())
	}

	// The admin principal's tenant is "platform"; the failPolicy events tee
	// under it, mirroring the audit emission.
	events := billingSince(t, billing, "platform")
	weakened := billingOfType(t, events, billingstore.EventInterceptorFailPolicyWeakened)
	if weakened.Conditional == nil || weakened.Conditional.NewFailPolicy != "fail-open" ||
		weakened.Conditional.OldFailPolicy != "fail-closed" {
		t.Fatalf("weakened conditional = %+v", weakened.Conditional)
	}
	if weakened.Conditional.AffectedPolicyCount != 1 || len(weakened.Conditional.AffectedPolicyNames) != 1 {
		t.Fatalf("weakened affected = %+v", weakened.Conditional)
	}
	if weakened.Conditional.CooldownSeconds != 60 || weakened.Conditional.TransitionTS == "" {
		t.Fatalf("weakened window = %+v", weakened.Conditional)
	}
	cooldown := billingOfType(t, events, billingstore.EventInterceptorWeakeningCooldownActive)
	if cooldown.Conditional.OldFailPolicy != "" || cooldown.Conditional.CooldownSeconds != 60 {
		t.Fatalf("cooldown_active conditional = %+v", cooldown.Conditional)
	}

	// The reverse transition tees interceptor.fail_policy_strengthened.
	strengthen := validInterceptorPayload("scan")
	strengthen.FailPolicy = "fail-closed"
	if rr := doAdminReq(t, h, http.MethodPut, "/v1/admin/interceptors/scan", strengthen, withAdminPrincipal); rr.Code != http.StatusOK {
		t.Fatalf("strengthen: status %d", rr.Code)
	}
	events = billingSince(t, billing, "platform")
	strong := billingOfType(t, events, billingstore.EventInterceptorFailPolicyStrengthened)
	if strong.Conditional.CooldownSeconds != 0 || strong.Conditional.TransitionTS != "" {
		t.Fatalf("strengthened must omit cooldown/transition: %+v", strong.Conditional)
	}
}

func billingSince(t *testing.T, store *billingstore.Memory, tenant string) []billingstore.Event {
	t.Helper()
	evs, err := store.Since(context.Background(), tenant, 0, 100)
	if err != nil {
		t.Fatalf("billing Since: %v", err)
	}
	return evs
}

func billingOfType(t *testing.T, events []billingstore.Event, typ billingstore.EventType) billingstore.Event {
	t.Helper()
	for _, e := range events {
		if e.EventType == typ {
			return e
		}
	}
	t.Fatalf("no billing event of type %q in %d events", typ, len(events))
	return billingstore.Event{}
}
