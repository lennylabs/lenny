// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

type stubHealth struct {
	healthy bool
	detail  string
}

func (h stubHealth) Healthy(context.Context) (bool, string, error) { return h.healthy, h.detail, nil }

type stubImages struct {
	unpullable map[string]bool
}

func (i stubImages) Pullable(_ context.Context, ref string) (bool, string, error) {
	if i.unpullable[ref] {
		return false, "manifest not found", nil
	}
	return true, "", nil
}

type stubConns struct{ ok bool }

func (c stubConns) HasFreeConnections(context.Context) (bool, string, error) {
	return c.ok, "", nil
}

func plan() map[string]string {
	return map[string]string{
		"gateway": "ghcr.io/lennylabs/lenny-gateway:1.6.0",
		"ops":     "ghcr.io/lennylabs/lenny-ops:1.6.0",
	}
}

// spec: §25.8 Phase 1 — every gate passes (unconfigured gates skip) and the
// plan is returned as the preview.
func TestPreflight_AllPass_spec_25_8(t *testing.T) {
	pf := upgradeservice.NewPreflighter(upgradeservice.PreflighterOptions{
		Store:  upgradeservice.NewMemoryStore(),
		Health: stubHealth{healthy: true},
		Images: stubImages{},
		Conns:  stubConns{ok: true},
	})
	res, err := pf.Preflight(context.Background(), upgradeservice.PreflightRequest{
		TargetVersion: "1.6.0", CurrentVersion: "1.5.0", MinUpgradeFrom: "1.3.0", ImagePlan: plan(),
	})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !res.Passed || len(res.Failures) != 0 {
		t.Fatalf("res = %+v", res)
	}
	if res.Plan["ops"] != "ghcr.io/lennylabs/lenny-ops:1.6.0" {
		t.Errorf("plan preview = %+v", res.Plan)
	}
}

// spec: §25.8 line 3499 — a current version below minUpgradeFrom fails the
// version gate.
func TestPreflight_VersionPrerequisiteFails_spec_25_8(t *testing.T) {
	pf := upgradeservice.NewPreflighter(upgradeservice.PreflighterOptions{Store: upgradeservice.NewMemoryStore()})
	res, _ := pf.Preflight(context.Background(), upgradeservice.PreflightRequest{
		TargetVersion: "1.6.0", CurrentVersion: "1.2.0", MinUpgradeFrom: "1.3.0", ImagePlan: plan(),
	})
	if res.Passed || !contains(res.Failures, upgradeservice.CheckVersionPrerequisite) {
		t.Fatalf("res = %+v, want version_prerequisite failure", res)
	}
}

// spec: §25.8 line 3500 — an unpullable image fails the image gate and
// OnlyImageGateFailed routes the handler to UPGRADE_IMAGE_NOT_PULLABLE.
func TestPreflight_ImageNotPullable_spec_25_8(t *testing.T) {
	pf := upgradeservice.NewPreflighter(upgradeservice.PreflighterOptions{
		Store:  upgradeservice.NewMemoryStore(),
		Images: stubImages{unpullable: map[string]bool{"ghcr.io/lennylabs/lenny-ops:1.6.0": true}},
	})
	res, _ := pf.Preflight(context.Background(), upgradeservice.PreflightRequest{
		TargetVersion: "1.6.0", CurrentVersion: "1.5.0", ImagePlan: plan(),
	})
	if res.Passed || !res.OnlyImageGateFailed() {
		t.Fatalf("res = %+v, want only image gate failed", res)
	}
	if len(res.UnpullableImages) != 1 || res.UnpullableImages[0] != "ghcr.io/lennylabs/lenny-ops:1.6.0" {
		t.Fatalf("unpullable = %v", res.UnpullableImages)
	}
}

// spec: §25.8 line 3498 — an upgrade already in progress fails the gate.
func TestPreflight_UpgradeInProgress_spec_25_8(t *testing.T) {
	store := upgradeservice.NewMemoryStore()
	svc := upgradeservice.New(upgradeservice.Options{Store: store})
	if _, err := svc.Start(context.Background(), upgradeservice.StartRequest{TargetVersion: "1.6.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pf := upgradeservice.NewPreflighter(upgradeservice.PreflighterOptions{Store: store})
	res, _ := pf.Preflight(context.Background(), upgradeservice.PreflightRequest{TargetVersion: "1.6.0", CurrentVersion: "1.5.0"})
	if res.Passed || !contains(res.Failures, upgradeservice.CheckNoUpgradeInProgress) {
		t.Fatalf("res = %+v, want no_upgrade_in_progress failure", res)
	}
}

// spec: §25.8 lines 3497, 3501 — health and connection gates fail when
// their seams report unhealthy / no free connections.
func TestPreflight_HealthAndConnsFail_spec_25_8(t *testing.T) {
	pf := upgradeservice.NewPreflighter(upgradeservice.PreflighterOptions{
		Store:  upgradeservice.NewMemoryStore(),
		Health: stubHealth{healthy: false, detail: "gateway degraded"},
		Conns:  stubConns{ok: false},
	})
	res, _ := pf.Preflight(context.Background(), upgradeservice.PreflightRequest{
		TargetVersion: "1.6.0", CurrentVersion: "1.5.0", ImagePlan: plan(),
	})
	if res.Passed {
		t.Fatalf("res should fail: %+v", res)
	}
	if !contains(res.Failures, upgradeservice.CheckPlatformHealthy) || !contains(res.Failures, upgradeservice.CheckPostgresConnections) {
		t.Fatalf("failures = %v", res.Failures)
	}
	// A multi-gate failure is not an image-only failure.
	if res.OnlyImageGateFailed() {
		t.Errorf("multi-gate failure misclassified as image-only")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if strings.EqualFold(v, x) {
			return true
		}
	}
	return false
}
