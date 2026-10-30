// SPDX-License-Identifier: MIT

package backup_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// spec: §25.11 Pre-Restore Backup Lifecycle uses type:"pre-restore" —
// ValidType MUST accept it so a forced pre-restore snapshot via
// POST /v1/admin/backups is not rejected at the public endpoint.
func TestValidType(t *testing.T) {
	for _, ok := range []string{"full", "postgres", "config", "pre-restore"} {
		if !backup.ValidType(ok) {
			t.Errorf("ValidType(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "FULL", "snapshot", "incremental"} {
		if backup.ValidType(bad) {
			t.Errorf("ValidType(%q) = true, want false", bad)
		}
	}
}

func TestRequiresConfirm(t *testing.T) {
	if !backup.RequiresConfirm(backup.TypeFull, true) {
		t.Error("a full backup in production should require confirm")
	}
	if backup.RequiresConfirm(backup.TypeFull, false) {
		t.Error("a full backup outside production should not require confirm")
	}
	if backup.RequiresConfirm(backup.TypePostgres, true) {
		t.Error("a postgres backup should not require confirm")
	}
}

func TestValidateSchedule(t *testing.T) {
	if err := backup.ValidateSchedule(backup.Schedule{
		Full:     "0 2 * * *",
		Postgres: "0 */6 * * *",
		Enabled:  true,
	}); err != nil {
		t.Errorf("valid schedule rejected: %v", err)
	}
	// An empty expression is allowed (that backup type is unscheduled).
	if err := backup.ValidateSchedule(backup.Schedule{Postgres: "0 3 * * *"}); err != nil {
		t.Errorf("schedule with only postgres set rejected: %v", err)
	}
	if err := backup.ValidateSchedule(backup.Schedule{Full: "not a cron"}); err == nil {
		t.Error("ValidateSchedule accepted an unparseable full expression")
	}
}

func TestValidateRetentionPolicy(t *testing.T) {
	if err := backup.ValidateRetentionPolicy(backup.RetentionPolicy{
		RetainDays: 30, RetainCount: 10, RetainMinFull: 3,
	}); err != nil {
		t.Errorf("valid policy rejected: %v", err)
	}
	if err := backup.ValidateRetentionPolicy(backup.RetentionPolicy{RetainDays: -1}); err == nil {
		t.Error("ValidateRetentionPolicy accepted a negative bound")
	}
	if err := backup.ValidateRetentionPolicy(backup.RetentionPolicy{}); err == nil {
		t.Error("ValidateRetentionPolicy accepted a policy that retains nothing")
	}
}
