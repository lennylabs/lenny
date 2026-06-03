// SPDX-License-Identifier: MIT

package backup_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// fakeShardRegions is a static §12.8 shard→region resolver for tests.
type fakeShardRegions struct {
	shards []backup.ShardRegion
	err    error
}

func (f fakeShardRegions) ShardRegions(context.Context) ([]backup.ShardRegion, error) {
	return f.shards, f.err
}

// fakeResidencyMetrics records the §12.8 lenny_data_residency_violation_total
// increments the fail-closed abort raises.
type fakeResidencyMetrics struct {
	mu  sync.Mutex
	ops []string
}

func (m *fakeResidencyMetrics) DataResidencyViolation(operation string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = append(m.ops, operation)
}

func (m *fakeResidencyMetrics) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.ops)
}

// newRegionService builds a §12.8 per-region Service over in-memory
// dependencies wired with the supplied region map, shard resolver, audit
// sink, and residency metric.
func newRegionService(t *testing.T, regions map[string]backup.RegionBackupConfig, shards []backup.ShardRegion, sink backup.AuditSink, metrics backup.ResidencyMetrics) (*backup.Service, *backup.MemStore, *backup.FakeLauncher) {
	t.Helper()
	store := backup.NewMemStore()
	launcher := backup.NewFakeLauncher()
	seq := 0
	svc, err := backup.NewService(backup.Config{
		Store:           store,
		Launcher:        launcher,
		Locker:          backup.NewMemLocker(),
		PlatformVersion: "1.5.0",
		SchemaVersion:   42,
		Audit:           sink,
		Regions:         regions,
		ShardRegions:    fakeShardRegions{shards: shards},
		Residency:       metrics,
		Now:             func() time.Time { return fixedNow },
		NewID: func(prefix string) string {
			seq++
			return prefix + "-" + string(rune('a'+seq-1))
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store, launcher
}

func twoRegions() map[string]backup.RegionBackupConfig {
	return map[string]backup.RegionBackupConfig{
		"eu-west-1": {MinioEndpoint: "https://minio.eu:9000", KMSKeyID: "kms-eu", AccessCredentialSecret: "lenny-backup-minio-eu", Bucket: "backups-eu"},
		"us-east-1": {MinioEndpoint: "https://minio.us:9000", KMSKeyID: "kms-us", AccessCredentialSecret: "lenny-backup-minio-us", Bucket: "backups-us"},
	}
}

// TestPerRegionDispatchLaunchesOneJobPerRegion_spec_12_8_935 covers the
// §12.8 line 935 requirement that a multi-region backup runs one pg_dump
// Job per region, each scoped to that region's MinIO endpoint, KMS key,
// bucket, and access-credential Secret, with one component per region.
func TestPerRegionDispatchLaunchesOneJobPerRegion_spec_12_8_935(t *testing.T) {
	rec := &recordingSink{}
	shards := []backup.ShardRegion{
		{ShardID: "shard-eu", Region: "eu-west-1"},
		{ShardID: "shard-us", Region: "us-east-1"},
	}
	svc, store, launcher := newRegionService(t, twoRegions(), shards, rec.sink(), &fakeResidencyMetrics{})

	b, err := svc.CreateBackup(context.Background(), backup.BackupRequest{Type: "full", StartedBy: "alice"})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if b.Status != backup.StatusRunning {
		t.Errorf("status = %q, want running", b.Status)
	}

	specs := launcher.LaunchedSpecs()
	if len(specs) != 2 {
		t.Fatalf("launched %d Jobs, want one per region", len(specs))
	}
	byRegion := map[string]backup.JobSpec{}
	for _, s := range specs {
		if s.Region == "" {
			t.Errorf("per-region Job carries no region: %+v", s)
		}
		if s.RegionConfig.MinioEndpoint == "" || s.RegionConfig.AccessCredentialSecret == "" {
			t.Errorf("region %s Job not scoped to its endpoint/secret: %+v", s.Region, s.RegionConfig)
		}
		byRegion[s.Region] = s
	}
	if eu, ok := byRegion["eu-west-1"]; !ok || eu.RegionConfig.MinioEndpoint != "https://minio.eu:9000" {
		t.Errorf("eu Job = %+v, want eu endpoint", byRegion["eu-west-1"])
	}
	if eu := byRegion["eu-west-1"]; len(eu.Shards) != 1 || eu.Shards[0] != "shard-eu" {
		t.Errorf("eu Job shards = %v, want [shard-eu]", eu.Shards)
	}

	// One component per region, each carrying the region's Job id.
	stored, _ := store.GetBackup(context.Background(), b.ID)
	if len(stored.Components) != 2 {
		t.Fatalf("components = %+v, want one per region", stored.Components)
	}
	for _, c := range stored.Components {
		if c.Region == "" || c.JobID == "" {
			t.Errorf("component missing region/jobId: %+v", c)
		}
	}

	// The created event is audited and carries the regions covered.
	created := rec.byType(string(audit.EventBackupCreated))
	if len(created) != 1 {
		t.Fatalf("backup.created events = %d, want 1", len(created))
	}
	if _, ok := created[0].Fields["regions"]; !ok {
		t.Errorf("backup.created fields = %+v, want regions list", created[0].Fields)
	}
}

// TestPerRegionUnresolvableFailsClosed_spec_25_11_4336 covers the §25.11
// line 4336 / §12.8 line 936 fail-closed control: a shard whose resolved
// region has no backups.regions entry aborts with
// BACKUP_REGION_UNRESOLVABLE, marks the row failed, emits a
// DataResidencyViolationAttempt audit event, increments the residency
// counter, and launches no Job.
func TestPerRegionUnresolvableFailsClosed_spec_25_11_4336(t *testing.T) {
	rec := &recordingSink{}
	metrics := &fakeResidencyMetrics{}
	// us-east-1 is not in the region map.
	regions := map[string]backup.RegionBackupConfig{
		"eu-west-1": {MinioEndpoint: "https://minio.eu:9000", AccessCredentialSecret: "lenny-backup-minio-eu"},
	}
	shards := []backup.ShardRegion{
		{ShardID: "shard-eu", Region: "eu-west-1"},
		{ShardID: "shard-us", Region: "us-east-1"},
	}
	svc, store, launcher := newRegionService(t, regions, shards, rec.sink(), metrics)

	b, err := svc.CreateBackup(context.Background(), backup.BackupRequest{Type: "full", StartedBy: "alice"})
	if err == nil {
		t.Fatalf("CreateBackup succeeded, want BACKUP_REGION_UNRESOLVABLE")
	}
	if code := backup.CodeOf(err); code != backup.ErrCodeBackupRegionUnresolvable {
		t.Fatalf("error code = %q, want %q", code, backup.ErrCodeBackupRegionUnresolvable)
	}
	if b != nil {
		t.Errorf("CreateBackup returned a backup on abort: %+v", b)
	}

	// No Job was launched.
	if specs := launcher.LaunchedSpecs(); len(specs) != 0 {
		t.Errorf("launched %d Jobs on a fail-closed abort, want 0", len(specs))
	}

	// The row is recorded failed with the spec error string.
	all, _ := store.ListBackups(context.Background(), backup.BackupFilter{})
	if len(all) != 1 {
		t.Fatalf("stored %d backups, want the failed row", len(all))
	}
	if all[0].Status != backup.StatusFailed {
		t.Errorf("row status = %q, want failed", all[0].Status)
	}
	if !strings.Contains(all[0].Error, backup.ErrCodeBackupRegionUnresolvable) || !strings.Contains(all[0].Error, "us-east-1") {
		t.Errorf("row error = %q, want BACKUP_REGION_UNRESOLVABLE for us-east-1", all[0].Error)
	}

	// The §12.8 line 936 DataResidencyViolationAttempt is audited with the
	// backup operation, requested region, and shard id.
	viol := rec.byType(string(audit.EventDataResidencyViolationAttempt))
	if len(viol) != 1 {
		t.Fatalf("DataResidencyViolationAttempt events = %d, want 1", len(viol))
	}
	if viol[0].Fields["operation"] != "backup" {
		t.Errorf("operation = %v, want backup", viol[0].Fields["operation"])
	}
	if viol[0].Fields["requested_region"] != "us-east-1" {
		t.Errorf("requested_region = %v, want us-east-1", viol[0].Fields["requested_region"])
	}
	if viol[0].Fields["shard_id"] != "shard-us" {
		t.Errorf("shard_id = %v, want shard-us", viol[0].Fields["shard_id"])
	}

	// The residency counter incremented exactly once.
	if metrics.count() != 1 {
		t.Errorf("residency violations = %d, want 1", metrics.count())
	}
}

// TestPerRegionIncompleteEntryFailsClosed_spec_25_11_4336 covers the
// §25.11 line 4336 "or the region's MinIO endpoint / KMS key is
// unreachable" branch: a region present in the map but missing its
// endpoint or credential Secret is unresolvable, the same as an absent
// entry.
func TestPerRegionIncompleteEntryFailsClosed_spec_25_11_4336(t *testing.T) {
	rec := &recordingSink{}
	// eu-west-1 is present but missing its access-credential Secret.
	regions := map[string]backup.RegionBackupConfig{
		"eu-west-1": {MinioEndpoint: "https://minio.eu:9000"},
	}
	shards := []backup.ShardRegion{{ShardID: "shard-eu", Region: "eu-west-1"}}
	svc, _, launcher := newRegionService(t, regions, shards, rec.sink(), &fakeResidencyMetrics{})

	_, err := svc.CreateBackup(context.Background(), backup.BackupRequest{Type: "postgres", StartedBy: "alice"})
	if backup.CodeOf(err) != backup.ErrCodeBackupRegionUnresolvable {
		t.Fatalf("error = %v, want BACKUP_REGION_UNRESOLVABLE", err)
	}
	if specs := launcher.LaunchedSpecs(); len(specs) != 0 {
		t.Errorf("launched %d Jobs, want 0", len(specs))
	}
	if !rec.has(string(audit.EventDataResidencyViolationAttempt)) {
		t.Error("no DataResidencyViolationAttempt event on incomplete-region abort")
	}
}

// TestConfigBackupNotRegionRouted_spec_12_8_935 covers that a config-only
// backup, which dumps no Postgres shards, is not subject to per-region
// dispatch even when regions are configured: it takes the single global
// Job path.
func TestConfigBackupNotRegionRouted_spec_12_8_935(t *testing.T) {
	rec := &recordingSink{}
	shards := []backup.ShardRegion{{ShardID: "shard-us", Region: "us-east-1"}} // unresolvable region
	svc, _, launcher := newRegionService(t, twoRegions(), shards, rec.sink(), &fakeResidencyMetrics{})

	b, err := svc.CreateBackup(context.Background(), backup.BackupRequest{Type: "config", StartedBy: "alice"})
	if err != nil {
		t.Fatalf("config backup: %v", err)
	}
	specs := launcher.LaunchedSpecs()
	if len(specs) != 1 || specs[0].Region != "" {
		t.Errorf("config backup launched %+v, want one non-region Job", specs)
	}
	if b.JobID == "" {
		t.Error("config backup has no job id")
	}
}

// TestNewServiceRejectsRegionsWithoutResolver covers that a Regions map
// with no ShardRegions resolver is rejected at construction, so a
// misconfiguration cannot silently bypass the §12.8 residency control.
func TestNewServiceRejectsRegionsWithoutResolver(t *testing.T) {
	_, err := backup.NewService(backup.Config{
		Store:    backup.NewMemStore(),
		Launcher: backup.NewFakeLauncher(),
		Regions:  twoRegions(),
	})
	if err == nil {
		t.Fatal("NewService accepted Regions without a ShardRegions resolver")
	}
}

// TestPerRegionResolverErrorFailsRow covers that a shard-resolution error
// fails the inserted row terminally rather than leaving it pending for
// the reconciler to time out.
func TestPerRegionResolverErrorFailsRow(t *testing.T) {
	store := backup.NewMemStore()
	launcher := backup.NewFakeLauncher()
	svc, err := backup.NewService(backup.Config{
		Store:        store,
		Launcher:     launcher,
		Locker:       backup.NewMemLocker(),
		Regions:      twoRegions(),
		ShardRegions: fakeShardRegions{err: errors.New("postgres down")},
		Now:          func() time.Time { return fixedNow },
		NewID:        func(prefix string) string { return prefix + "-a" },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.CreateBackup(context.Background(), backup.BackupRequest{Type: "full"}); err == nil {
		t.Fatal("CreateBackup succeeded with a failing resolver")
	}
	all, _ := store.ListBackups(context.Background(), backup.BackupFilter{})
	if len(all) != 1 || all[0].Status != backup.StatusFailed {
		t.Errorf("stored rows = %+v, want one failed row", all)
	}
	if specs := launcher.LaunchedSpecs(); len(specs) != 0 {
		t.Errorf("launched %d Jobs, want 0", len(specs))
	}
}
