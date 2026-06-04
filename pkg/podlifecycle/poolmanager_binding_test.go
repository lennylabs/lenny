// SPDX-License-Identifier: MIT

package podlifecycle_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/podlifecycle"
)

// TestAgentSandboxIsTheDefaultPoolManager_spec_17_1 pins the §17.1 row 9
// contract: the Warm Pool Controller manages pod lifecycle via the
// PoolManager interface, whose named default implementation is the
// kubernetes-sigs/agent-sandbox CRD backend. The three AgentSandbox*
// types satisfy the §4.6.1 forward-compatibility interfaces, so an
// alternative backend (custom kubebuilder controllers, a multi-cluster
// router) swaps in behind these interfaces with every consumer
// untouched. The assignments fail to compile if a default type drifts
// from its interface. spec: spec/04_system-components.md lines 333-363;
// §17.1 row 9. F-17.1.11.
func TestAgentSandboxIsTheDefaultPoolManager_spec_17_1(t *testing.T) {
	var (
		reader  podlifecycle.PoolReader          = &podlifecycle.AgentSandboxPoolReader{}
		lifeMgr podlifecycle.PodLifecycleManager = &podlifecycle.AgentSandboxPodLifecycleManager{}
		poolMgr podlifecycle.PoolManager         = &podlifecycle.AgentSandboxPoolManager{}
	)
	if reader == nil || lifeMgr == nil || poolMgr == nil {
		t.Fatal("agent-sandbox default implementations must be non-nil")
	}

	// The controller-facing PoolManager embeds the read-only PoolReader
	// per §4.6.1, so the default also satisfies PoolReader transitively —
	// the controller never needs a second handle for pool reads.
	if _, ok := poolMgr.(podlifecycle.PoolReader); !ok {
		t.Fatal("AgentSandboxPoolManager must also satisfy PoolReader (the §4.6.1 embedding)")
	}
	// Likewise the gateway-facing PodLifecycleManager embeds PoolReader.
	if _, ok := lifeMgr.(podlifecycle.PoolReader); !ok {
		t.Fatal("AgentSandboxPodLifecycleManager must also satisfy PoolReader (the §4.6.1 embedding)")
	}
}
