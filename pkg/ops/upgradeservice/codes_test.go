// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

// spec: §25.8 Error Codes table (line 3629) — all nine canonical codes
// must be declared with their spec-table wire values.
func TestSection258ErrorCodesMatchSpecTable(t *testing.T) {
	want := map[string]bool{
		"UPGRADE_ALREADY_IN_PROGRESS":  false,
		"UPGRADE_PREFLIGHT_FAILED":     false,
		"UPGRADE_IMAGE_NOT_PULLABLE":   false,
		"UPGRADE_ROLLBACK_UNAVAILABLE": false,
		"UPGRADE_ROLLBACK_MANUAL_CRD":  false,
		"UPGRADE_NOT_IN_PROGRESS":      false,
		"UPGRADE_CHANNEL_UNREACHABLE":  false,
		"CONFIG_VALIDATION_FAILED":     false,
		"CONFIG_RESTART_REQUIRED":      false,
	}
	got := upgradeservice.Section258ErrorCodes()
	if len(got) != len(want) {
		t.Fatalf("Section258ErrorCodes len = %d, want %d: %v", len(got), len(want), got)
	}
	for _, code := range got {
		if _, ok := want[code]; !ok {
			t.Errorf("unexpected code %q not in the §25.8 table", code)
			continue
		}
		want[code] = true
	}
	for code, seen := range want {
		if !seen {
			t.Errorf("§25.8 code %q missing from Section258ErrorCodes", code)
		}
	}
}

// spec: §25.8 — the orchestrator's named constants alias the canonical
// spec-table values so a single source of truth feeds both.
func TestOrchestratorCodesAliasSpecTable(t *testing.T) {
	if upgradeservice.CodeUpgradeInProgress != "UPGRADE_ALREADY_IN_PROGRESS" {
		t.Errorf("CodeUpgradeInProgress = %q, want UPGRADE_ALREADY_IN_PROGRESS", upgradeservice.CodeUpgradeInProgress)
	}
	if upgradeservice.CodeNoUpgrade != "UPGRADE_NOT_IN_PROGRESS" {
		t.Errorf("CodeNoUpgrade = %q, want UPGRADE_NOT_IN_PROGRESS", upgradeservice.CodeNoUpgrade)
	}
	if upgradeservice.CodeNotRollbackable != "UPGRADE_ROLLBACK_UNAVAILABLE" {
		t.Errorf("CodeNotRollbackable = %q, want UPGRADE_ROLLBACK_UNAVAILABLE", upgradeservice.CodeNotRollbackable)
	}
	if upgradeservice.CodeChannelUnreachable != "UPGRADE_CHANNEL_UNREACHABLE" {
		t.Errorf("CodeChannelUnreachable = %q, want UPGRADE_CHANNEL_UNREACHABLE", upgradeservice.CodeChannelUnreachable)
	}
}
