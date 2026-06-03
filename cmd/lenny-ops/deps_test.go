// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// spec: §25.11 schedule.enabled — when an operator turns off scheduled
// backups via PUT /v1/admin/backups/schedule, subsequent cron fires
// MUST become no-ops without waiting for a lenny-ops restart.
func TestScheduledBackupsRespectScheduleEnabled_spec_25_11(t *testing.T) {
	svc, jobs := buildBackupService(false)
	if len(jobs) < 2 {
		t.Fatalf("buildBackupService returned %d jobs; want backup-full + backup-postgres + retention", len(jobs))
	}

	ctx := context.Background()

	// Default schedule has enabled:true — both backup jobs create a backup.
	for _, j := range jobs {
		if !strings.HasPrefix(j.Name, "backup-") || j.Name == "backup-retention" {
			continue
		}
		if err := j.Run(ctx); err != nil {
			t.Fatalf("default-enabled run %s: %v", j.Name, err)
		}
	}
	page, err := svc.ListBackups(ctx, backup.BackupFilter{}, "", 0)
	if err != nil {
		t.Fatalf("ListBackups (after default-enabled): %v", err)
	}
	if got, want := len(page.Backups), 2; got != want {
		t.Fatalf("after default-enabled cron fires: %d backups, want %d", got, want)
	}

	// Flip schedule.enabled to false via the public API and re-run.
	if _, err := svc.UpdateSchedule(ctx, backup.BackupSchedule{
		Full: "0 2 * * *", Postgres: "0 */6 * * *", Enabled: false,
	}); err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	for _, j := range jobs {
		if !strings.HasPrefix(j.Name, "backup-") || j.Name == "backup-retention" {
			continue
		}
		if err := j.Run(ctx); err != nil {
			t.Fatalf("disabled run %s: %v", j.Name, err)
		}
	}
	page2, err := svc.ListBackups(ctx, backup.BackupFilter{}, "", 0)
	if err != nil {
		t.Fatalf("ListBackups (after disabled): %v", err)
	}
	if got, want := len(page2.Backups), 2; got != want {
		t.Errorf("after enabled=false cron fires: %d backups, want %d (no new ones)", got, want)
	}

	// Retention still runs unconditionally even when scheduling is disabled.
	for _, j := range jobs {
		if j.Name != "backup-retention" {
			continue
		}
		if err := j.Run(ctx); err != nil {
			t.Errorf("retention run while schedule disabled: %v", err)
		}
	}
}

// spec: §25.11 line 4106 — "The schedule is stored in Postgres and
// modifiable at runtime via PUT /v1/admin/backups/schedule." The cron
// jobs expose ExpressionFunc so the evaluator fires on the stored cron,
// reflecting an edit without a restart. F-25.11.5.
func TestScheduledBackupCronReflectsRuntimeEdit_spec_25_11(t *testing.T) {
	svc, jobs := buildBackupService(false)

	byName := map[string]func() string{}
	for _, j := range jobs {
		if j.ExpressionFunc != nil {
			byName[j.Name] = j.ExpressionFunc
		}
	}
	for _, name := range []string{"backup-full", "backup-postgres"} {
		if byName[name] == nil {
			t.Fatalf("%s has no ExpressionFunc; the runtime schedule cannot take effect", name)
		}
	}

	// The default-store schedule matches the compiled-in cron.
	if got := byName["backup-full"](); got != "0 2 * * *" {
		t.Errorf("default backup-full expression = %q, want 0 2 * * *", got)
	}
	if got := byName["backup-postgres"](); got != "0 */6 * * *" {
		t.Errorf("default backup-postgres expression = %q, want 0 */6 * * *", got)
	}

	// An operator edit is reflected by the next ExpressionFunc call.
	if _, err := svc.UpdateSchedule(context.Background(), backup.BackupSchedule{
		Full: "0 5 * * *", Postgres: "30 */4 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	if got := byName["backup-full"](); got != "0 5 * * *" {
		t.Errorf("post-edit backup-full expression = %q, want 0 5 * * *", got)
	}
	if got := byName["backup-postgres"](); got != "30 */4 * * *" {
		t.Errorf("post-edit backup-postgres expression = %q, want 30 */4 * * *", got)
	}
}
