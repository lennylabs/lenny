// SPDX-License-Identifier: MIT

package warmpool

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// nonceOnlyMember builds a member Sandbox carrying the §4.5 nonce-only
// signals the pool-level trigger reads back: the WPC-resolved
// Sandbox.spec.requireSoPeercred carrier and/or the SOPeercredDisabled
// status condition.
func nonceOnlyMember(carrier *bool, condition bool) lennyv1.Sandbox {
	sb := lennyv1.Sandbox{Spec: lennyv1.SandboxSpec{RequireSoPeercred: carrier}}
	if condition {
		sb.Status.Conditions = []metav1.Condition{{
			Type:   lennyv1.SandboxConditionSOPeercredDisabled,
			Status: metav1.ConditionTrue,
			Reason: "RenderedNonceOnly",
		}}
	}
	return sb
}

// TestPoolNonceOnly_spec_4_5 exercises the §4.5 pool-level trigger across
// the carrier signal, the condition signal, the revert latch, and the
// clean baseline. The trigger is True when any member carries either the
// requireSoPeercred: false carrier or the SOPeercredDisabled=True
// condition, so the §4.7 latch holds across a revert until the last
// nonce-only pod is replaced.
//
// spec: §4.5 (trigger from member Sandboxes); §4.7 (revert latch).
func TestPoolNonceOnly_spec_4_5(t *testing.T) {
	cases := []struct {
		name      string
		sandboxes []lennyv1.Sandbox
		want      bool
	}{
		{
			name: "carrier false trips the trigger",
			sandboxes: []lennyv1.Sandbox{
				nonceOnlyMember(ptr.To(false), false),
			},
			want: true,
		},
		{
			name: "condition alone trips the trigger",
			sandboxes: []lennyv1.Sandbox{
				nonceOnlyMember(nil, true),
			},
			want: true,
		},
		{
			name: "one nonce-only member among enforcing members trips it",
			sandboxes: []lennyv1.Sandbox{
				nonceOnlyMember(nil, false),
				nonceOnlyMember(ptr.To(false), false),
			},
			want: true,
		},
		{
			name: "latch holds while a pre-revert pod survives",
			// After a revert, freshly created pods carry no signal, but a
			// pre-revert pod still carries both until it is replaced.
			sandboxes: []lennyv1.Sandbox{
				nonceOnlyMember(nil, false),
				nonceOnlyMember(ptr.To(false), true),
			},
			want: true,
		},
		{
			name: "clean once the last nonce-only pod is gone",
			sandboxes: []lennyv1.Sandbox{
				nonceOnlyMember(nil, false),
				nonceOnlyMember(ptr.To(true), false),
			},
			want: false,
		},
		{
			name:      "empty pool is not degraded",
			sandboxes: nil,
			want:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := poolNonceOnly(tc.sandboxes); got != tc.want {
				t.Errorf("poolNonceOnly = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSecurityDegradedCondition_spec_4_7 verifies the condition builder
// yields True/NonceOnlyMode while degraded, no condition (nil) for a clean
// pool that was never degraded, and an explicit False/SOPeercredEnforced on
// recovery of a pool that already carried the condition. The nil case mirrors
// evaluateRuntimeClass leaving the condition list untouched when the check
// does not apply; the explicit False is reserved for the recovery transition.
//
// spec: §4.7; §4.5 (no condition for non-nonce-only pools; explicit False
// only on full recovery of a previously-degraded pool).
func TestSecurityDegradedCondition_spec_4_7(t *testing.T) {
	// Degraded: True regardless of whether the condition is already present.
	deg := securityDegradedCondition(true, false)
	if deg == nil {
		t.Fatal("degraded condition is nil, want a True condition")
	}
	if deg.Type != conditionSecurityDegradedMode || deg.Status != metav1.ConditionTrue {
		t.Errorf("degraded condition = {%s,%s}, want {%s,True}", deg.Type, deg.Status, conditionSecurityDegradedMode)
	}
	if deg.Reason == "" {
		t.Error("degraded condition carries no reason")
	}

	// Clean and never degraded: no condition write at all.
	if cond := securityDegradedCondition(false, false); cond != nil {
		t.Errorf("clean never-degraded condition = %+v, want nil (no write)", cond)
	}

	// Clean but previously degraded: the explicit False recovery transition.
	recovery := securityDegradedCondition(false, true)
	if recovery == nil {
		t.Fatal("recovery condition is nil, want an explicit False")
	}
	if recovery.Type != conditionSecurityDegradedMode || recovery.Status != metav1.ConditionFalse {
		t.Errorf("recovery condition = {%s,%s}, want {%s,False}", recovery.Type, recovery.Status, conditionSecurityDegradedMode)
	}
	if recovery.Reason == "" {
		t.Error("recovery condition carries no reason")
	}
}
