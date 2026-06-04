// SPDX-License-Identifier: MIT

package v1alpha1_test

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

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

func TestSandboxClaimDeepCopyIsolatesTheCopy(t *testing.T) {
	orig := &lennyv1.SandboxClaim{
		Spec:   lennyv1.SandboxClaimSpec{SandboxRef: "sbx-1", SessionID: "sess-1"},
		Status: lennyv1.SandboxClaimStatus{Phase: "bound"},
	}
	cp := orig.DeepCopy()
	cp.Spec.SandboxRef = "sbx-2"
	cp.Status.Phase = "released"
	if orig.Spec.SandboxRef != "sbx-1" || orig.Status.Phase != "bound" {
		t.Errorf("DeepCopy did not isolate the copy; the original was mutated: %+v", orig)
	}
}

// TestSandboxTemplateDeepCopyIsolatesNestedPolicy exercises the
// pointer-valued TaskPolicy so a regression in the generated DeepCopy
// (a shallow pointer copy) is caught.
func TestSandboxTemplateDeepCopyIsolatesNestedPolicy(t *testing.T) {
	orig := &lennyv1.SandboxTemplate{
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:    "claude-code",
			ExecutionMode: "task",
			TaskPolicy: &lennyv1.TaskPolicy{
				AcknowledgeBestEffortScrub: true,
				MaxTasksPerPod:             50,
				CleanupCommands:            []string{"rm -rf /tmp/sandbox-*"},
			},
		},
	}
	cp := orig.DeepCopy()
	cp.Spec.RuntimeRef = "mutated"
	cp.Spec.TaskPolicy.MaxTasksPerPod = 1
	cp.Spec.TaskPolicy.CleanupCommands[0] = "mutated"
	if orig.Spec.RuntimeRef != "claude-code" {
		t.Errorf("DeepCopy did not isolate spec scalar: %+v", orig.Spec)
	}
	if orig.Spec.TaskPolicy.MaxTasksPerPod != 50 {
		t.Errorf("DeepCopy shared the TaskPolicy pointer; original was mutated: %+v", orig.Spec.TaskPolicy)
	}
	if orig.Spec.TaskPolicy.CleanupCommands[0] != "rm -rf /tmp/sandbox-*" {
		t.Errorf("DeepCopy shared the CleanupCommands slice; original was mutated: %+v", orig.Spec.TaskPolicy)
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
