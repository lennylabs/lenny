// SPDX-License-Identifier: MIT

package v1alpha1_test

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// spec: §4.6 — the lenny.dev/v1alpha1 CRD API types.

func TestAddToSchemeRegistersAllKinds(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	for _, obj := range []runtime.Object{
		&lennyv1.Runtime{}, &lennyv1.RuntimeList{},
		&lennyv1.Sandbox{}, &lennyv1.SandboxList{},
		&lennyv1.SandboxClaim{}, &lennyv1.SandboxClaimList{},
		&lennyv1.SandboxTemplate{}, &lennyv1.SandboxTemplateList{},
		&lennyv1.SandboxWarmPool{}, &lennyv1.SandboxWarmPoolList{},
	} {
		gvks, _, err := scheme.ObjectKinds(obj)
		if err != nil {
			t.Errorf("%T is not registered with the scheme: %v", obj, err)
			continue
		}
		// spec: §15.5 line 2433 — CRDs ship initially at v1alpha1 and
		// follow the graduation path v1alpha1 → v1beta1 → v1.
		if len(gvks) == 0 || gvks[0].Group != "lenny.dev" || gvks[0].Version != "v1alpha1" {
			t.Errorf("%T: registered as %v, want group lenny.dev version v1alpha1", obj, gvks)
		}
	}
}

func TestSandboxDeepCopyIsolatesTheCopy(t *testing.T) {
	orig := &lennyv1.Sandbox{
		Spec:   lennyv1.SandboxSpec{RuntimeRef: "claude-code", PoolRef: "default"},
		Status: lennyv1.SandboxStatus{Phase: "idle"},
	}
	cp := orig.DeepCopy()
	cp.Spec.RuntimeRef = "mutated"
	cp.Status.Phase = "claimed"
	if orig.Spec.RuntimeRef != "claude-code" || orig.Status.Phase != "idle" {
		t.Errorf("DeepCopy did not isolate the copy; the original was mutated: %+v", orig)
	}
}

// TestRuntimeDeepCopyIsolatesRequireSoPeercred exercises the §4.7
// nonce-only activation pointer on RuntimeSpec so a shallow pointer copy
// in the generated DeepCopy is caught. A pointer distinguishes the
// default (nil, treated as true) from an explicit false that activates
// nonce-only mode.
// spec: §4.7 (nonce-only mode activation field).
func TestRuntimeDeepCopyIsolatesRequireSoPeercred(t *testing.T) {
	orig := &lennyv1.Runtime{
		Spec: lennyv1.RuntimeSpec{
			Type:              "agent",
			DeploymentModel:   "sidecar",
			RequireSoPeercred: ptr.To(false),
		},
	}
	cp := orig.DeepCopy()
	*cp.Spec.RequireSoPeercred = true
	if orig.Spec.RequireSoPeercred == nil || *orig.Spec.RequireSoPeercred {
		t.Errorf("DeepCopy shared the RequireSoPeercred pointer; original was mutated: %+v", orig.Spec.RequireSoPeercred)
	}
}

// TestSandboxDeepCopyIsolatesRequireSoPeercred exercises the §4.7
// resolved-decision carrier on SandboxSpec. The WarmPoolController writes
// it with the resolved render decision; a shallow pointer copy would let
// one Sandbox's decision leak into another.
// spec: §4.7 (resolved nonce-only render decision carrier).
func TestSandboxDeepCopyIsolatesRequireSoPeercred(t *testing.T) {
	orig := &lennyv1.Sandbox{
		Spec: lennyv1.SandboxSpec{
			RuntimeRef:        "claude-code",
			PoolRef:           "default",
			RequireSoPeercred: ptr.To(false),
		},
	}
	cp := orig.DeepCopy()
	*cp.Spec.RequireSoPeercred = true
	if orig.Spec.RequireSoPeercred == nil || *orig.Spec.RequireSoPeercred {
		t.Errorf("DeepCopy shared the RequireSoPeercred pointer; original was mutated: %+v", orig.Spec.RequireSoPeercred)
	}
}

// TestSandboxTemplateAcknowledgeNonceOnlyAuthDeepCopies exercises the
// §5.3 PSC-mirrored acknowledgment scalar on SandboxTemplateSpec that the
// WarmPoolController render gate reads.
// spec: §4.7, §5.3 (nonce-only acknowledgment gate).
func TestSandboxTemplateAcknowledgeNonceOnlyAuthDeepCopies(t *testing.T) {
	orig := &lennyv1.SandboxTemplate{
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:               "claude-code",
			AcknowledgeNonceOnlyAuth: true,
		},
	}
	cp := orig.DeepCopy()
	cp.Spec.AcknowledgeNonceOnlyAuth = false
	if !orig.Spec.AcknowledgeNonceOnlyAuth {
		t.Errorf("DeepCopy did not isolate AcknowledgeNonceOnlyAuth: %+v", orig.Spec)
	}
}

// TestNonceOnlyConditionTypeConstants pins the §7.8 condition-type
// constant values the WarmPoolController writes and the surfacing
// predicate reads back. The string values are the wire-visible condition
// types on the Sandbox and SandboxTemplate status; a rename would silently
// break the §13.1 security posture surfacing.
// spec: §4.7, §7.8 (SOPeercredDisabled / SecurityDegradedMode conditions).
func TestNonceOnlyConditionTypeConstants(t *testing.T) {
	if lennyv1.SandboxConditionSOPeercredDisabled != "SOPeercredDisabled" {
		t.Errorf("SandboxConditionSOPeercredDisabled = %q, want SOPeercredDisabled",
			lennyv1.SandboxConditionSOPeercredDisabled)
	}
	if lennyv1.SandboxTemplateConditionSecurityDegradedMode != "SecurityDegradedMode" {
		t.Errorf("SandboxTemplateConditionSecurityDegradedMode = %q, want SecurityDegradedMode",
			lennyv1.SandboxTemplateConditionSecurityDegradedMode)
	}
}

// TestSandboxClaimDeepCopyIsolatesTheCopy exercises the per-pod-occupancy
// SandboxClaim, including the pointer-valued binding-state timestamps the
// gateway stamps at the reserved hold and the recycle re-warm.
// spec: §4.6.3 (CRD field ownership and binding state), §6.2 (pod state
// machine).
func TestSandboxClaimDeepCopyIsolatesTheCopy(t *testing.T) {
	holdExpires := metav1.NewTime(time.Unix(1700000010, 0))
	rewarmStarted := metav1.NewTime(time.Unix(1700000000, 0))
	transition := metav1.NewTime(time.Unix(1700000005, 0))
	orig := &lennyv1.SandboxClaim{
		Spec: lennyv1.SandboxClaimSpec{SandboxRef: "sbx-1", TenantID: "acme"},
		Status: lennyv1.SandboxClaimStatus{
			Phase:                      "reserved",
			HoldExpiresAt:              &holdExpires,
			RewarmStartedAt:            &rewarmStarted,
			BindingStateTransitionTime: &transition,
		},
	}
	cp := orig.DeepCopy()
	cp.Spec.SandboxRef = "sbx-2"
	cp.Spec.TenantID = "globex"
	cp.Status.Phase = "released"
	cp.Status.HoldExpiresAt.Time = time.Unix(0, 0)
	cp.Status.RewarmStartedAt.Time = time.Unix(0, 0)
	cp.Status.BindingStateTransitionTime.Time = time.Unix(0, 0)
	if orig.Spec.SandboxRef != "sbx-1" || orig.Spec.TenantID != "acme" || orig.Status.Phase != "reserved" {
		t.Errorf("DeepCopy did not isolate the copy; the original was mutated: %+v", orig)
	}
	if !orig.Status.HoldExpiresAt.Equal(&holdExpires) {
		t.Errorf("DeepCopy shared the HoldExpiresAt pointer; original was mutated: %v", orig.Status.HoldExpiresAt)
	}
	if !orig.Status.RewarmStartedAt.Equal(&rewarmStarted) {
		t.Errorf("DeepCopy shared the RewarmStartedAt pointer; original was mutated: %v", orig.Status.RewarmStartedAt)
	}
	if !orig.Status.BindingStateTransitionTime.Equal(&transition) {
		t.Errorf("DeepCopy shared the BindingStateTransitionTime pointer; original was mutated: %v", orig.Status.BindingStateTransitionTime)
	}
}

// TestSandboxTemplateDeepCopyIsolatesNestedPolicy exercises the
// pointer-valued SessionPolicy and its nested RecyclePolicy so a
// regression in the generated DeepCopy (a shallow pointer copy) is caught.
// spec: §5.2 (sessionPolicy and recycle block).
func TestSandboxTemplateDeepCopyIsolatesNestedPolicy(t *testing.T) {
	orig := &lennyv1.SandboxTemplate{
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:    "claude-code",
			ExecutionMode: "session",
			SessionPolicy: &lennyv1.SessionPolicy{
				Recycle: &lennyv1.RecyclePolicy{
					ScrubProfile:                    "in-place",
					AcknowledgeMicrovmResidualState: true,
				},
			},
		},
	}
	cp := orig.DeepCopy()
	cp.Spec.RuntimeRef = "mutated"
	cp.Spec.SessionPolicy.Recycle.ScrubProfile = "standard"
	cp.Spec.SessionPolicy.Recycle.AcknowledgeMicrovmResidualState = false
	if orig.Spec.RuntimeRef != "claude-code" {
		t.Errorf("DeepCopy did not isolate spec scalar: %+v", orig.Spec)
	}
	if orig.Spec.SessionPolicy.Recycle.ScrubProfile != "in-place" {
		t.Errorf("DeepCopy shared the RecyclePolicy pointer; original was mutated: %+v", orig.Spec.SessionPolicy.Recycle)
	}
	if !orig.Spec.SessionPolicy.Recycle.AcknowledgeMicrovmResidualState {
		t.Errorf("DeepCopy shared the RecyclePolicy pointer; original was mutated: %+v", orig.Spec.SessionPolicy.Recycle)
	}
}

// TestSandboxWarmPoolDeepCopyIsolatesCircuitBreaker exercises the
// PoolScalingController-owned status carve-out so a shallow copy of
// the circuit-breaker pointer is caught.
func TestSandboxWarmPoolDeepCopyIsolatesCircuitBreaker(t *testing.T) {
	orig := &lennyv1.SandboxWarmPool{
		Spec: lennyv1.SandboxWarmPoolSpec{
			TemplateRef: "default",
			MinWarm:     2,
			MaxWarm:     10,
		},
		Status: lennyv1.SandboxWarmPoolStatus{
			WarmCount: 2,
			SDKWarmCircuitBreaker: &lennyv1.SDKWarmCircuitBreakerStatus{
				OpenedReason: "demotion_rate_exceeded",
			},
		},
	}
	cp := orig.DeepCopy()
	cp.Spec.MinWarm = 99
	cp.Status.SDKWarmCircuitBreaker.OpenedReason = "operator_manual"
	if orig.Spec.MinWarm != 2 {
		t.Errorf("DeepCopy did not isolate spec scalar: %+v", orig.Spec)
	}
	if orig.Status.SDKWarmCircuitBreaker.OpenedReason != "demotion_rate_exceeded" {
		t.Errorf("DeepCopy shared the SDKWarmCircuitBreaker pointer; original was mutated: %+v", orig.Status.SDKWarmCircuitBreaker)
	}
}
