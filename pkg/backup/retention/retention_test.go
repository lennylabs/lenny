// SPDX-License-Identifier: MIT

package retention_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/backup/retention"
)

var now = time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

// bk builds a backup created ageDays days before the fixed now.
func bk(id string, kind retention.Kind, ageDays int) retention.Backup {
	return retention.Backup{ID: id, Kind: kind, CreatedAt: now.AddDate(0, 0, -ageDays)}
}

func ids(backups []retention.Backup) map[string]bool {
	set := make(map[string]bool, len(backups))
	for _, b := range backups {
		set[b.ID] = true
	}
	return set
}

// tier2 is the §25 Tier-2 default backup retention policy.
var tier2 = retention.Policy{
	RetainDays: 30, RetainCount: 10, RetainMinFull: 3, PreRestoreRetainDays: 7,
}

func TestPlanKeepsRecentBackups(t *testing.T) {
	backups := []retention.Backup{
		bk("a", retention.KindFull, 1),
		bk("b", retention.KindPostgres, 2),
		bk("c", retention.KindFull, 3),
	}
	keep, prune := retention.Plan(backups, tier2, now)
	if len(keep) != 3 || len(prune) != 0 {
		t.Errorf("recent backups: kept %d pruned %d, want 3 kept and 0 pruned", len(keep), len(prune))
	}
}

func TestPlanPrunesBackupsOlderThanRetainDays(t *testing.T) {
	backups := []retention.Backup{
		bk("recent", retention.KindPostgres, 5),
		bk("stale", retention.KindPostgres, 45),
	}
	keep, prune := retention.Plan(backups, tier2, now)
	if !ids(keep)["recent"] || ids(keep)["stale"] {
		t.Errorf("keep = %v, want only the recent backup", ids(keep))
	}
	if len(prune) != 1 || prune[0].ID != "stale" {
		t.Errorf("prune = %v, want the stale backup", prune)
	}
}

func TestPlanEnforcesRetainCount(t *testing.T) {
	policy := retention.Policy{RetainDays: 30, RetainCount: 2, RetainMinFull: 0, PreRestoreRetainDays: 7}
	backups := []retention.Backup{
		bk("d1", retention.KindPostgres, 1),
		bk("d2", retention.KindPostgres, 2),
		bk("d3", retention.KindPostgres, 3),
		bk("d4", retention.KindPostgres, 4),
	}
	keep, prune := retention.Plan(backups, policy, now)
	if len(keep) != 2 || !ids(keep)["d1"] || !ids(keep)["d2"] {
		t.Errorf("keep = %v, want the 2 newest (d1, d2)", ids(keep))
	}
	if len(prune) != 2 {
		t.Errorf("prune = %d backups, want 2 beyond the retain count", len(prune))
	}
}

func TestPlanRetainMinFullFloor(t *testing.T) {
	// RetainCount keeps 2; RetainMinFull keeps a 3rd full backup that
	// the count rule would otherwise prune, even though it is old.
	policy := retention.Policy{RetainDays: 30, RetainCount: 2, RetainMinFull: 3, PreRestoreRetainDays: 7}
	backups := []retention.Backup{
		bk("f1", retention.KindFull, 1),
		bk("f2", retention.KindFull, 2),
		bk("f3", retention.KindFull, 40),
		bk("f4", retention.KindFull, 50),
	}
	keep, prune := retention.Plan(backups, policy, now)
	if !ids(keep)["f1"] || !ids(keep)["f2"] || !ids(keep)["f3"] {
		t.Errorf("keep = %v, want f1, f2, and f3 (the min-full floor)", ids(keep))
	}
	if len(keep) != 3 || len(prune) != 1 || prune[0].ID != "f4" {
		t.Errorf("kept %d pruned %v, want 3 kept and f4 pruned", len(keep), prune)
	}
}

func TestPlanPreRestoreUsesShorterRetention(t *testing.T) {
	backups := []retention.Backup{
		bk("pr-fresh", retention.KindPreRestore, 3),
		bk("pr-stale", retention.KindPreRestore, 10),
		// A regular backup the same age as the stale pre-restore one is
		// kept — pre-restore retention is shorter.
		bk("reg", retention.KindPostgres, 10),
	}
	keep, prune := retention.Plan(backups, tier2, now)
	if !ids(keep)["pr-fresh"] || !ids(keep)["reg"] {
		t.Errorf("keep = %v, want pr-fresh and reg", ids(keep))
	}
	if !ids(prune)["pr-stale"] || len(prune) != 1 {
		t.Errorf("prune = %v, want only the stale pre-restore backup", prune)
	}
}

func TestPlanPreRestoreNotCountedAgainstRetainCount(t *testing.T) {
	// RetainCount is 2; the two full backups consume it. Three recent
	// pre-restore backups are all kept on their own retention and do
	// not displace the regular backups.
	policy := retention.Policy{RetainDays: 30, RetainCount: 2, RetainMinFull: 0, PreRestoreRetainDays: 7}
	backups := []retention.Backup{
		bk("full-1", retention.KindFull, 1),
		bk("full-2", retention.KindFull, 2),
		bk("pr-1", retention.KindPreRestore, 1),
		bk("pr-2", retention.KindPreRestore, 2),
		bk("pr-3", retention.KindPreRestore, 3),
	}
	keep, prune := retention.Plan(backups, policy, now)
	if len(keep) != 5 || len(prune) != 0 {
		t.Errorf("kept %d pruned %d, want all 5 kept — pre-restore backups do not consume retainCount",
			len(keep), len(prune))
	}
}
