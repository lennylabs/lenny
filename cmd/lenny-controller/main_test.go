// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// TestBuildSchemeRegistersControllerTypes confirms the manager scheme
// resolves every lenny.dev/v1 CRD the controllers reconcile and the
// core types the shared cache lists, so a missing AddToScheme call is
// caught before the binary starts.
func TestBuildSchemeRegistersControllerTypes(t *testing.T) {
	s := buildScheme()

	for _, obj := range []runtime.Object{
		&lennyv1.Runtime{},
		&lennyv1.Sandbox{},
		&lennyv1.SandboxClaim{},
		&lennyv1.SandboxTemplate{},
		&lennyv1.SandboxWarmPool{},
	} {
		if _, _, err := s.ObjectKinds(obj); err != nil {
			t.Errorf("%T is not registered with the manager scheme: %v", obj, err)
		}
	}

	if _, _, err := s.ObjectKinds(&corev1.Pod{}); err != nil {
		t.Errorf("core/v1 Pod is not registered with the manager scheme: %v", err)
	}
}
