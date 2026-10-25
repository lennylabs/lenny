// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"testing"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// TestApplyCRDFields_DerivesRestartable_spec_5_1 verifies that the
// reconciler mirrors the §5.1 derived restartable flag, computed by the
// RuntimeSpec helper the pod builder also reads, for each conjunct of the
// derivation.
//
// spec: §5.1 (command).
func TestApplyCRDFields_DerivesRestartable_spec_5_1(t *testing.T) {
	cmd := []string{"/usr/local/bin/agent"}
	for _, c := range []struct {
		name string
		spec lennyv1.RuntimeSpec
		want bool
	}{
		{"sidecar agent with command", lennyv1.RuntimeSpec{Type: "agent", Command: cmd, ExecutionMode: "session"}, true},
		{"service mode", lennyv1.RuntimeSpec{Type: "agent", Command: cmd, ExecutionMode: "service"}, false},
		{"preConnect", lennyv1.RuntimeSpec{Type: "agent", Command: cmd, Capabilities: &lennyv1.RuntimeCapabilitiesCRD{PreConnect: true}}, false},
		{"embedded without command", lennyv1.RuntimeSpec{Type: "agent", DeploymentModel: "embedded"}, false},
		{"mcp without command", lennyv1.RuntimeSpec{Type: "mcp"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt := &lennyv1.Runtime{Spec: c.spec}
			rt.Spec.Image = "img@sha256:" + hex64
			rt.Name = "rt"
			var dst runtimestore.Runtime
			applyCRDFields(&dst, rt)
			if dst.Restartable != c.want {
				t.Errorf("Restartable = %v, want %v", dst.Restartable, c.want)
			}
			if dst.Restartable != rt.Spec.Restartable() {
				t.Errorf("mirrored Restartable %v disagrees with RuntimeSpec.Restartable() %v", dst.Restartable, rt.Spec.Restartable())
			}
		})
	}
}

// TestMirror_RestartableFollowsCRDEdits_spec_5_1 drives mirror through the
// create and update paths and asserts that the registry's restartable flag
// tracks the CRD: a sidecar session runtime with command mirrors true, an
// edit that declares capabilities.preConnect clears it, and a re-edit back
// restores it. A stale true after the preConnect edit would let pool
// admission accept recycle.runtimeProcess: restart on a runtime the pod
// builder then refuses.
//
// spec: §5.1 (command), §5.2 (recycle.runtimeProcess).
func TestMirror_RestartableFollowsCRDEdits_spec_5_1(t *testing.T) {
	ctx := context.Background()
	store := runtimestore.NewMemory()
	r := &Reconciler{Store: store}
	rt := &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{
		Type: "agent", Image: "img@sha256:" + hex64, IntegrationLevel: "basic",
		Command: []string{"/usr/local/bin/agent"},
		Env:     []lennyv1.RuntimeEnvVar{{Name: "MODE", Value: "x"}},
	}}
	rt.Name = "restartable-agent"

	step := func(label string, want bool) {
		t.Helper()
		if err := r.mirror(ctx, rt); err != nil {
			t.Fatalf("%s: mirror = %v", label, err)
		}
		got, err := store.Get(ctx, rt.Name)
		if err != nil {
			t.Fatalf("%s: store.Get: %v", label, err)
		}
		if got.Restartable != want {
			t.Errorf("%s: Restartable = %v, want %v", label, got.Restartable, want)
		}
	}

	step("create", true)
	rt.Spec.Capabilities = &lennyv1.RuntimeCapabilitiesCRD{PreConnect: true}
	step("update to preConnect", false)
	rt.Spec.Capabilities = nil
	step("update back to pod-warm", true)
	rt.Spec.ExecutionMode = "service"
	step("update to service mode", false)
}
