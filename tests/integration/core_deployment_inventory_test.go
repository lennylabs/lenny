// SPDX-License-Identifier: MIT

//go:build integration

package integration_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// TestCoreDeploymentInventory is the §17.2 core_deployment_inventory_test.go
// suite (line 84). It renders the chart with stock values and fail-closes
// when any item of the §17.8.5 mandatory lenny-ops inventory is absent, so
// a chart-author omission of a mandatory core deployment cannot ship
// silently the way iter3/iter4 caught for gated webhooks. lenny-ops is
// mandatory in every install from Phase 3.5 onward, so this suite needs no
// feature-flag parameterisation.
//
// spec: §17.2 line 84 (core_deployment_inventory_test.go); §17.8.5
// (mandatory lenny-ops deployment inventory). F-17.2.15.
func TestCoreDeploymentInventory(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	manifests := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       []string{"coredns.clusterIP=10.96.0.10"},
	})

	// The §17.8.5 mandatory core inventory. The lenny-ops-leader Lease is
	// created at runtime by the first lenny-ops pod, so the chart-rendered
	// proxy for it is the leader-election Role that grants the Lease
	// lifecycle (§17.1 row 19).
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

	// PodDisruptionBudget minAvailable: 1 preserves lenny-ops availability
	// during voluntary disruptions (§17.8.5 / §17.1 row 15).
	if pdb, ok := manifests.Find("PodDisruptionBudget", "lenny-ops"); ok {
		spec, _ := pdb.Raw["spec"].(map[string]any)
		if spec == nil || !minAvailableIsOne(spec["minAvailable"]) {
			t.Errorf("§17.8.5: PodDisruptionBudget/lenny-ops must set minAvailable: 1, got spec=%v", spec)
		}
	}
}

// minAvailableIsOne accepts the value 1 whether helm emitted it as an int
// or a string (intstr fields parse either way).
func minAvailableIsOne(v any) bool {
	switch x := v.(type) {
	case int:
		return x == 1
	case int64:
		return x == 1
	case float64:
		return x == 1
	case string:
		return x == "1"
	}
	return false
}
