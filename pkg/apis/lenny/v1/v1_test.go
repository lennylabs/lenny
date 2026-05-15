// SPDX-License-Identifier: MIT

package v1_test

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// spec: §4.6 — the lenny.dev/v1 CRD API types.

func TestAddToSchemeRegistersAllKinds(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	for _, obj := range []runtime.Object{
		&lennyv1.Runtime{}, &lennyv1.RuntimeList{},
		&lennyv1.Sandbox{}, &lennyv1.SandboxList{},
		&lennyv1.SandboxClaim{}, &lennyv1.SandboxClaimList{},
	} {
		gvks, _, err := scheme.ObjectKinds(obj)
		if err != nil {
			t.Errorf("%T is not registered with the scheme: %v", obj, err)
			continue
		}
		if len(gvks) == 0 || gvks[0].Group != "lenny.dev" || gvks[0].Version != "v1" {
			t.Errorf("%T: registered as %v, want group lenny.dev version v1", obj, gvks)
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
