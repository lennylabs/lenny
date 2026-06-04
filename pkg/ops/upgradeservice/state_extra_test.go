// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// spec: §25.8 line 3422 — an air-gapped skip-channel start carries an
// explicit per-component image set, recorded as target_images.
func TestStart_AirGapExplicitImages_spec_25_8(t *testing.T) {
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()})
	images := map[string]string{
		"gateway": "mirror.internal/lenny-gateway@sha256:aaa",
		"ops":     "mirror.internal/lenny-ops@sha256:bbb",
	}
	st, err := svc.Start(context.Background(), upgradeservice.StartRequest{
		TargetVersion:  "1.6.0",
		Images:         images,
		PreviousImages: map[string]string{"ops": "mirror.internal/lenny-ops@sha256:old"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.TargetImages["gateway"] != images["gateway"] {
		t.Fatalf("target images = %+v", st.TargetImages)
	}
	if st.PreviousImages["ops"] != "mirror.internal/lenny-ops@sha256:old" {
		t.Fatalf("previous images = %+v", st.PreviousImages)
	}
	// The recorded images survive a reload.
	got, _, _ := svc.Status(context.Background())
	if got.TargetImages["ops"] != images["ops"] {
		t.Fatalf("reloaded target images = %+v", got.TargetImages)
	}
}

// spec: §25.8 line 3549 — RollbackOnTimeout refuses a post-migration phase
// (past the point of no return).
func TestRollbackOnTimeout_RejectsPostMigration_spec_25_8(t *testing.T) {
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()})
	advanceTo(t, svc, upgrade.GatewayRoll)
	if _, err := svc.RollbackOnTimeout(context.Background(), upgradeservice.CodeOpsRollTimeout, "x"); err != upgradeservice.ErrNotRollbackable {
		t.Fatalf("RollbackOnTimeout post-migration err = %v, want ErrNotRollbackable", err)
	}
}

// spec: §25.8 line 3509 — RollbackOnTimeout transitions a rollbackable
// phase to RolledBack and stamps the failure code.
func TestRollbackOnTimeout_StampsErrorCode_spec_25_8(t *testing.T) {
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()})
	advanceTo(t, svc, upgrade.OpsRoll)
	st, err := svc.RollbackOnTimeout(context.Background(), upgradeservice.CodeOpsRollTimeout, "ops pod never came up")
	if err != nil {
		t.Fatalf("RollbackOnTimeout: %v", err)
	}
	if st.Phase != upgrade.RolledBack || st.Error != upgradeservice.CodeOpsRollTimeout {
		t.Fatalf("state phase=%s error=%q", st.Phase, st.Error)
	}
}
