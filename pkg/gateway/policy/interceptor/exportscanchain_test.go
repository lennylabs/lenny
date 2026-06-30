// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
)

// spec: §4.8 lines 1036-1050 — the PreExportMaterialization phase is not
// independently registerable; it invokes the same interceptor named by
// contentPolicy.interceptorRef, registered at PreDelegation.
func TestExportScanChainForRunsNamedPreDelegationInterceptor_spec_4_8_1038(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	scanner := &fakeInterceptor{name: "acme-scanner", builtin: true, calls: &calls}
	mustRegister(t, c, interceptor.PhasePreDelegation, scanner)

	sub, ok := c.ExportScanChainFor("acme-scanner")
	if !ok {
		t.Fatal("ExportScanChainFor: want ok for a registered PreDelegation interceptor")
	}
	// The named interceptor runs at the export phase, not at PreDelegation.
	if got := sub.Len(interceptor.PhasePreExportMaterialization); got != 1 {
		t.Fatalf("sub PreExportMaterialization len = %d, want 1", got)
	}
	if got := sub.Len(interceptor.PhasePreDelegation); got != 0 {
		t.Fatalf("sub PreDelegation len = %d, want 0", got)
	}
	res := sub.Run(context.Background(), interceptor.Request{
		Phase:     interceptor.PhasePreExportMaterialization,
		TenantID:  "acme",
		SessionID: "sess-1",
		Content:   []byte(`{}`),
	})
	if res.Action != interceptor.ActionAllow {
		t.Fatalf("sub Run action = %q, want allow", res.Action)
	}
	if !equal(calls, []string{"acme-scanner"}) {
		t.Fatalf("calls = %v, want [acme-scanner]", calls)
	}
}

// spec: §8.3 rule 1 — a scanExportedFiles policy whose interceptorRef
// resolves to nothing must fail closed; ExportScanChainFor reports ok=false
// so the caller surfaces EXPORT_FILE_SCAN_UNAVAILABLE.
func TestExportScanChainForUnresolvable_spec_8_3_rule1(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreDelegation,
		&fakeInterceptor{name: "registered", builtin: true})

	cases := []struct {
		name string
		ref  string
	}{
		{"empty ref", ""},
		{"unknown ref", "missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if sub, ok := c.ExportScanChainFor(tc.ref); ok || sub != nil {
				t.Fatalf("ExportScanChainFor(%q) = (%v, %v), want (nil, false)", tc.ref, sub, ok)
			}
		})
	}
}

// spec: §4.8 line 1050 — only a contentPolicy.interceptorRef interceptor
// (PreDelegation) is invoked at PreExportMaterialization; an interceptor
// registered at a different phase must not be resolvable by ref.
func TestExportScanChainForIgnoresOtherPhases_spec_4_8_1050(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePostRoute,
		&fakeInterceptor{name: "post-route-only", builtin: true})

	if sub, ok := c.ExportScanChainFor("post-route-only"); ok || sub != nil {
		t.Fatal("ExportScanChainFor resolved an interceptor registered outside PreDelegation")
	}
}

// recordingFailObserver captures fail-open escalation events so a test can
// assert the sub-chain inherited the source chain's escalation config.
type recordingFailObserver struct {
	escalated int
}

func (r *recordingFailObserver) FailOpenEscalated(context.Context, interceptor.FailOpenEvent) {
	r.escalated++
}
func (r *recordingFailObserver) FailOpenRestored(context.Context, interceptor.FailOpenEvent) {}

// spec: §8.3 rule 5 — the export-scan posture follows the interceptor's
// failPolicy, so the sub-chain must inherit the source chain's fail-open
// escalation config (a fail-open interceptor that keeps erroring escalates
// to fail-closed on the export path just as on the delegation path).
func TestExportScanChainForInheritsFailOpenEscalation_spec_8_3_rule5(t *testing.T) {
	c := interceptor.NewChain()
	rec := &recordingFailObserver{}
	clk := func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	c.SetFailOpenEscalation(1, time.Minute, rec, clk)
	flaky := &fakeInterceptor{
		name:       "flaky",
		builtin:    true,
		failPolicy: interceptor.FailOpen,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, errors.New("scanner down")
		},
	}
	mustRegister(t, c, interceptor.PhasePreDelegation, flaky)

	sub, ok := c.ExportScanChainFor("flaky")
	if !ok {
		t.Fatal("ExportScanChainFor: want ok")
	}
	req := interceptor.Request{
		Phase:    interceptor.PhasePreExportMaterialization,
		TenantID: "acme",
		Content:  []byte(`{}`),
	}
	// First error is within the ceiling: skipped (admitted) under fail-open.
	if res := sub.Run(context.Background(), req); res.Action != interceptor.ActionAllow {
		t.Fatalf("first run action = %q, want allow (fail-open skip)", res.Action)
	}
	// Second error crosses maxConsecutive=1: escalates to fail-closed → reject.
	if res := sub.Run(context.Background(), req); res.Action != interceptor.ActionReject {
		t.Fatalf("second run action = %q, want reject (escalated)", res.Action)
	}
	if rec.escalated != 1 {
		t.Fatalf("escalated events = %d, want 1 (config not inherited)", rec.escalated)
	}
}
