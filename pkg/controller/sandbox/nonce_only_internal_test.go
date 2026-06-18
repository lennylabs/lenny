// SPDX-License-Identifier: MIT

package sandbox

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/lifecycle"
)

// TestNonceOnlyConditions_DerivesFromCarrier_spec_4_7 verifies the §4.7
// pod-level condition is derived from the Sandbox.spec.requireSoPeercred
// carrier: an explicit false yields SOPeercredDisabled=True, while nil or
// true yields no entry (so the WPC owns no condition and the apply removes a
// stale one on revert).
//
// spec: §4.4, §4.7.
func TestNonceOnlyConditions_DerivesFromCarrier_spec_4_7(t *testing.T) {
	cases := []struct {
		name    string
		carrier *bool
		wantLen int
	}{
		{"explicit false renders the condition", ptr.To(false), 1},
		{"nil carrier renders no condition", nil, 0},
		{"explicit true renders no condition", ptr.To(true), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := &lennyv1.Sandbox{Spec: lennyv1.SandboxSpec{RequireSoPeercred: tc.carrier}}
			got := nonceOnlyConditions(sb)
			if len(got) != tc.wantLen {
				t.Fatalf("nonceOnlyConditions len = %d, want %d", len(got), tc.wantLen)
			}
			if tc.wantLen == 1 {
				c := got[0]
				if c.Type != lennyv1.SandboxConditionSOPeercredDisabled || c.Status != metav1.ConditionTrue {
					t.Errorf("condition = {%s,%s}, want {%s,True}", c.Type, c.Status, lennyv1.SandboxConditionSOPeercredDisabled)
				}
			}
		})
	}
}

// TestEqualSOPeercredCondition_IgnoresTransitionTime_spec_4_4 verifies the
// status-write gate compares the SOPeercredDisabled condition by value
// (status, reason, message) and ignores LastTransitionTime so an unchanged
// condition does not force a status write on every reconcile, while a
// present-vs-absent difference is detected.
//
// spec: §4.4.
func TestEqualSOPeercredCondition_IgnoresTransitionTime_spec_4_4(t *testing.T) {
	disabled := nonceOnlyConditions(&lennyv1.Sandbox{
		Spec: lennyv1.SandboxSpec{RequireSoPeercred: ptr.To(false)},
	})
	// Same value, different transition time: equal.
	other := append([]metav1.Condition(nil), disabled...)
	other[0].LastTransitionTime = metav1.Time{}
	if !equalSOPeercredCondition(disabled, other) {
		t.Error("conditions differing only in LastTransitionTime should compare equal")
	}
	// Present vs absent: not equal (drives the revert write).
	if equalSOPeercredCondition(disabled, nil) {
		t.Error("present condition must not compare equal to an absent one")
	}
	if equalSOPeercredCondition(nil, disabled) {
		t.Error("absent condition must not compare equal to a present one")
	}
	// Both absent: equal (no write needed).
	if !equalSOPeercredCondition(nil, nil) {
		t.Error("two absent conditions should compare equal")
	}
}

// TestBuildSandboxStatusPatch_RevertsDropsCondition_spec_4_7 verifies the
// §4.5 revert path: a Sandbox that carries SOPeercredDisabled=True in its
// status but whose carrier has reverted to the SO_PEERCRED-enforcing default
// (nil) produces a status patch that drops the condition (sends an empty
// condition list under the WPC field manager).
//
// spec: §4.5, §4.7.
func TestBuildSandboxStatusPatch_RevertsDropsCondition_spec_4_7(t *testing.T) {
	live := &lennyv1.Sandbox{
		Status: lennyv1.SandboxStatus{
			Phase: "idle",
			Conditions: []metav1.Condition{{
				Type:   lennyv1.SandboxConditionSOPeercredDisabled,
				Status: metav1.ConditionTrue,
				Reason: "NonceOnlyMode",
			}},
		},
	}
	// Carrier is nil (reverted), so no condition should be re-applied.
	patch := buildSandboxStatusPatch(live, lifecycle.Decision{}, nil, lifecycle.PodReady)
	if patch == nil {
		t.Fatal("expected a status patch dropping the stale condition, got nil")
	}
	if len(patch.Status.Conditions) != 0 {
		t.Errorf("revert patch carries %d conditions, want 0 (condition dropped)", len(patch.Status.Conditions))
	}
}

// TestResolveNonceOnly_FailsClosed_spec_4_7 verifies the §4.7 render gate's
// fail-closed branches: an embedded runtime, an unset or explicitly-true
// runtime field, and an unresolvable or unacknowledged pool all leave the
// flag off (nil), so the adapter default of true governs. Only a sidecar
// runtime carrying false in a fully-resolved acknowledged pool activates
// nonce-only mode.
//
// spec: §4.7, §5.3.
func TestResolveNonceOnly_FailsClosed_spec_4_7(t *testing.T) {
	scheme := sdkWarmScheme(t)
	ackTmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "tmpl-ack", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxTemplateSpec{AcknowledgeNonceOnlyAuth: true},
	}
	noAckTmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "tmpl-noack", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxTemplateSpec{AcknowledgeNonceOnlyAuth: false},
	}
	ackPool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "pool-ack", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "tmpl-ack"},
	}
	noAckPool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "pool-noack", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "tmpl-noack"},
	}
	noTmplPool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "pool-notmpl", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxWarmPoolSpec{},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(ackTmpl, noAckTmpl, ackPool, noAckPool, noTmplPool).Build()
	r := &Reconciler{Client: cl, Scheme: scheme}

	sb := func(poolRef string) *lennyv1.Sandbox {
		return &lennyv1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: "sb", Namespace: "lenny-agents"},
			Spec:       lennyv1.SandboxSpec{PoolRef: poolRef},
		}
	}
	sidecarFalse := &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{DeploymentModel: "sidecar", RequireSoPeercred: ptr.To(false)}}

	cases := []struct {
		name string
		rt   *lennyv1.Runtime
		pool string
		want bool // true => nonce-only activated (non-nil false)
	}{
		{"embedded runtime never activates", &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{DeploymentModel: "embedded", RequireSoPeercred: ptr.To(false)}}, "pool-ack", false},
		{"nil field never activates", &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{DeploymentModel: "sidecar"}}, "pool-ack", false},
		{"explicit true never activates", &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{DeploymentModel: "sidecar", RequireSoPeercred: ptr.To(true)}}, "pool-ack", false},
		{"empty poolRef fails closed", sidecarFalse, "", false},
		{"missing pool fails closed", sidecarFalse, "pool-absent", false},
		{"pool without templateRef fails closed", sidecarFalse, "pool-notmpl", false},
		{"unacknowledged pool fails closed", sidecarFalse, "pool-noack", false},
		{"acknowledged sidecar false activates", sidecarFalse, "pool-ack", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.resolveNonceOnly(context.Background(), sb(tc.pool), tc.rt)
			activated := got != nil && !*got
			if activated != tc.want {
				t.Errorf("resolveNonceOnly = %v, want activated=%v", got, tc.want)
			}
		})
	}
}

// TestBuildSandboxStatusPatch_NoopWhenConditionStable_spec_4_7 verifies that
// a Sandbox already carrying SOPeercredDisabled=True whose carrier still
// records nonce-only mode produces no status patch when nothing else
// changed, so the reconcile does not churn the status every pass.
//
// spec: §4.4, §4.7.
func TestBuildSandboxStatusPatch_NoopWhenConditionStable_spec_4_7(t *testing.T) {
	stable := nonceOnlyConditions(&lennyv1.Sandbox{
		Spec: lennyv1.SandboxSpec{RequireSoPeercred: ptr.To(false)},
	})
	live := &lennyv1.Sandbox{
		Spec:   lennyv1.SandboxSpec{RequireSoPeercred: ptr.To(false)},
		Status: lennyv1.SandboxStatus{Phase: "idle", Conditions: stable},
	}
	if patch := buildSandboxStatusPatch(live, lifecycle.Decision{}, nil, lifecycle.PodReady); patch != nil {
		t.Errorf("expected no status patch when the condition is stable, got %+v", patch.Status)
	}
}
