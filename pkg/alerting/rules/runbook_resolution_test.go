// SPDX-License-Identifier: MIT

package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runbookURLPrefix is the §17.7 rendered-site host pattern runbook()
// composes; the slug after it is the on-disk filename minus ".md".
// The platform-wide alert→file resolution invariant is covered by
// tests/tier11_docs TestAlertCatalogRunbookSlugsResolveToDocs (with
// its §17.7 procedural allowlist); this file pins the §17.3 / §25.11
// disaster-recovery subset that F-17.3.11 added.
const runbookURLPrefix = "https://docs.lenny.dev/runbooks/"

// TestBackupAlertsCarryRunbooks pins the §25.11 / §16.5 disaster-
// recovery alerts to their runbooks so an agent responding to a backup
// or replication alert always has a remediation page. spec: §17.3;
// §25.11 Alerting Rules. F-17.3.11.
func TestBackupAlertsCarryRunbooks(t *testing.T) {
	want := map[string]string{
		"BackupOverdue":                       "backup-overdue",
		"BackupFailed":                        "backup-failed",
		"BackupStorageHigh":                   "backup-storage-high",
		"BackupReconcileBlocked":              "backup-reconcile-blocked",
		"MinIOArtifactReplicationLagHigh":     "minio-replication-lag",
		"MinIOArtifactReplicationLagCritical": "minio-replication-lag",
		"MinIOArtifactReplicationFailed":      "minio-replication-lag",
	}
	got := map[string]string{}
	for _, r := range Catalog() {
		if _, ok := want[r.Name]; ok {
			got[r.Name] = strings.TrimPrefix(r.RunbookURL, runbookURLPrefix)
		}
	}
	// docs/runbooks is three directories up from pkg/alerting/rules.
	runbookDir := filepath.Join("..", "..", "..", "docs", "runbooks")
	for name, slug := range want {
		if got[name] != slug {
			t.Errorf("alert %q runbook slug = %q, want %q", name, got[name], slug)
			continue
		}
		// The slug must resolve to an on-disk runbook so the alert's
		// runbook_url does not 404 when an agent follows it.
		path := filepath.Join(runbookDir, slug+".md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("alert %q runbook %q resolves to missing file %s", name, slug, path)
		}
	}
}
