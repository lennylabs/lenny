// SPDX-License-Identifier: MIT

package tier0_static

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// TestCoreDeploymentInventory is the §17.2 line 84 core-Deployment
// inventory suite: it renders the chart with stock values and fail-closes
// when any item of the §17.8.5 mandatory lenny-ops inventory is absent.
// The spec names it tests/integration/core_deployment_inventory_test.go;
// this repo files pure chart-render checks under tier0_static (no live
// cluster, only the helm CLI), parallel to the gated-webhook inventory
// the spec calls out as the precedent that caught chart-author omissions
// in iter3/iter4.
//
// lenny-ops is mandatory in every install from Phase 3.5 onward (§17.8.5),
// so the suite needs no feature-flag parameterisation: every item below
// must render unconditionally. A chart-author omission of any of these
// fails this test rather than shipping silently.
//
// spec: §17.2 line 84 (core_deployment_inventory_test.go); §17.8.5
// (mandatory lenny-ops deployment inventory); §17.1 rows 15-21.
func TestCoreDeploymentInventory(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	manifests := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		// coredns.clusterIP is required when agentNamespaces is non-empty
		// (the stock default); it is unrelated to the inventory under test.
		Set: []string{"coredns.clusterIP=10.96.0.10"},
	})

	// The §17.8.5 mandatory core inventory. The lenny-ops-leader Lease
	// itself is created at runtime by the first lenny-ops pod (§17.1 row
	// 19), so the chart-rendered proxy for it is the leader-election Role
	// that grants the Lease lifecycle.
	required := []struct {
		kind string
		name string
	}{
		{"Deployment", "lenny-ops"},
		{"Service", "lenny-gateway-pods"},
		{"ServiceAccount", "lenny-backup-sa"},
		{"Role", "lenny-ops-leader-election"},
		{"PodDisruptionBudget", "lenny-ops"},
		{"NetworkPolicy", "lenny-ops-deny-all-ingress"},
		{"NetworkPolicy", "lenny-ops-allow-ingress-from-ingress-controller"},
		{"NetworkPolicy", "lenny-ops-egress"},
		{"NetworkPolicy", "lenny-backup-job"},
	}
	for _, r := range required {
		if _, ok := manifests.Find(r.kind, r.name); !ok {
			t.Errorf("§17.8.5 core inventory: %s/%s is not rendered by the chart with stock values; "+
				"a mandatory lenny-ops resource must not be omitted (re-add its template under charts/lenny/templates/)",
				r.kind, r.name)
		}
	}

	// The headless gateway-pods Service must stay headless (clusterIP None)
	// so every gateway replica is individually addressable for the §10.1
	// CheckpointBarrier fan-out.
	if svc, ok := manifests.Find("Service", "lenny-gateway-pods"); ok {
		spec, _ := svc.Raw["spec"].(map[string]any)
		if spec == nil || spec["clusterIP"] != "None" {
			t.Errorf("§17.8.5: Service/lenny-gateway-pods must be headless (clusterIP: None), got spec=%v", spec)
		}
	}
}
