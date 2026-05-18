// SPDX-License-Identifier: MIT

package runner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

// fakeDumper is an in-memory runner.Dumper returning fixed components.
type fakeDumper struct {
	pgErr     error
	configErr error
	crdErr    error
}

func (d fakeDumper) DumpPostgres(context.Context) (runner.Component, error) {
	if d.pgErr != nil {
		return runner.Component{}, d.pgErr
	}
	return runner.Component{Name: "postgres", Bytes: []byte("pg-dump-bytes")}, nil
}

func (d fakeDumper) ExportConfig(context.Context) (runner.Component, error) {
	if d.configErr != nil {
		return runner.Component{}, d.configErr
	}
	return runner.Component{Name: "config", Bytes: []byte(`{"runtimes":[]}`)}, nil
}

func (d fakeDumper) ExportCRDs(context.Context) (runner.Component, error) {
	if d.crdErr != nil {
		return runner.Component{}, d.crdErr
	}
	return runner.Component{Name: "crds", Bytes: []byte("crd-manifests")}, nil
}

// fakeArchiver concatenates the component bytes; it does not encrypt,
// so the test can assert on the archive content.
type fakeArchiver struct {
	packErr error
}

func (a fakeArchiver) Pack(_ context.Context, components []runner.Component) (runner.Archive, error) {
	if a.packErr != nil {
		return runner.Archive{}, a.packErr
	}
	var b []byte
	for _, c := range components {
		b = append(b, c.Bytes...)
	}
	return runner.Archive{Data: b, Encrypted: false}, nil
}

// fakeUploader records the uploaded archives keyed by object path.
type fakeUploader struct {
	uploadErr error
	uploaded  map[string]runner.Archive
	deleted   []string
}

func newFakeUploader() *fakeUploader {
	return &fakeUploader{uploaded: map[string]runner.Archive{}}
}

func (u *fakeUploader) Upload(_ context.Context, objectPath string, archive runner.Archive) (string, error) {
	if u.uploadErr != nil {
		return "", u.uploadErr
	}
	u.uploaded[objectPath] = archive
	return objectPath, nil
}

func (u *fakeUploader) DeleteBackupObject(_ context.Context, objectPath string) error {
	u.deleted = append(u.deleted, objectPath)
	return nil
}

// fakeReporter records the run outcome.
type fakeReporter struct {
	completed *runner.Result
	failedMsg string
}

func (r *fakeReporter) BackupCompleted(_ context.Context, result runner.Result) error {
	r.completed = &result
	return nil
}

func (r *fakeReporter) BackupFailed(_ context.Context, _ string, errMsg string) error {
	r.failedMsg = errMsg
	return nil
}

func TestRunFullBackup(t *testing.T) {
	uploader := newFakeUploader()
	reporter := &fakeReporter{}
	result, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-1",
		Mode:     runner.ModeFull,
		Dumper:   fakeDumper{},
		Archiver: fakeArchiver{},
		Uploader: uploader,
		Reporter: reporter,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A full backup covers postgres, config, and crds.
	if len(result.Components) != 3 {
		t.Errorf("components = %d, want 3 (postgres, config, crds)", len(result.Components))
	}
	if result.SizeBytes == 0 || result.Checksum == "" {
		t.Errorf("result = %+v, want a non-empty size and checksum", result)
	}
	// The §25.11 storage path layout.
	if !strings.HasPrefix(result.StoragePath, "backups/full/bkp-1/") ||
		!strings.HasSuffix(result.StoragePath, ".tar.gz.enc") {
		t.Errorf("storage path = %q, want backups/full/bkp-1/<ts>.tar.gz.enc", result.StoragePath)
	}
	if reporter.completed == nil {
		t.Error("the run did not record completion")
	}
	if len(uploader.uploaded) != 1 {
		t.Errorf("uploaded %d archives, want 1", len(uploader.uploaded))
	}
}

func TestRunPostgresBackupSkipsConfig(t *testing.T) {
	result, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-pg",
		Mode:     runner.ModePostgres,
		Dumper:   fakeDumper{},
		Archiver: fakeArchiver{},
		Uploader: newFakeUploader(),
		Reporter: &fakeReporter{},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A postgres-only backup covers postgres only.
	if len(result.Components) != 1 || result.Components[0].Name != "postgres" {
		t.Errorf("components = %+v, want only postgres", result.Components)
	}
}

func TestRunConfigBackupSkipsPostgres(t *testing.T) {
	result, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-cfg",
		Mode:     runner.ModeConfig,
		Dumper:   fakeDumper{},
		Archiver: fakeArchiver{},
		Uploader: newFakeUploader(),
		Reporter: &fakeReporter{},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A config-only backup covers config and crds.
	names := map[string]bool{}
	for _, c := range result.Components {
		names[c.Name] = true
	}
	if names["postgres"] || !names["config"] || !names["crds"] {
		t.Errorf("components = %+v, want config and crds, no postgres", result.Components)
	}
}

func TestRunRecordsFailureWhenDumpFails(t *testing.T) {
	reporter := &fakeReporter{}
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-fail",
		Mode:     runner.ModePostgres,
		Dumper:   fakeDumper{pgErr: errors.New("shard 0 unreachable")},
		Archiver: fakeArchiver{},
		Uploader: newFakeUploader(),
		Reporter: reporter,
	})
	if err == nil {
		t.Fatal("Run should have failed when the dump failed")
	}
	if reporter.failedMsg == "" {
		t.Error("the run did not record the failure on the ops_backups row")
	}
	if reporter.completed != nil {
		t.Error("the run recorded completion despite the dump failure")
	}
}

func TestRunRecordsFailureWhenUploadFails(t *testing.T) {
	uploader := newFakeUploader()
	uploader.uploadErr = errors.New("MinIO unreachable")
	reporter := &fakeReporter{}
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-up",
		Mode:     runner.ModeFull,
		Dumper:   fakeDumper{},
		Archiver: fakeArchiver{},
		Uploader: uploader,
		Reporter: reporter,
	})
	if err == nil {
		t.Fatal("Run should have failed when the upload failed")
	}
	if reporter.failedMsg == "" {
		t.Error("the run did not record the upload failure")
	}
}

func TestRunRejectsChecksumMismatch(t *testing.T) {
	// An Archiver that reports a checksum not matching its content is a
	// bug the run must catch before upload.
	bad := badChecksumArchiver{}
	reporter := &fakeReporter{}
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-cs",
		Mode:     runner.ModePostgres,
		Dumper:   fakeDumper{},
		Archiver: bad,
		Uploader: newFakeUploader(),
		Reporter: reporter,
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Run error = %v, want a checksum mismatch", err)
	}
}

// badChecksumArchiver reports a checksum that does not match its
// content.
type badChecksumArchiver struct{}

func (badChecksumArchiver) Pack(context.Context, []runner.Component) (runner.Archive, error) {
	return runner.Archive{Data: []byte("real-content"), Checksum: "deadbeef"}, nil
}

func TestRunInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  runner.Config
	}{
		{"no backup id", runner.Config{Mode: runner.ModeFull, Dumper: fakeDumper{}, Archiver: fakeArchiver{}, Uploader: newFakeUploader(), Reporter: &fakeReporter{}}},
		{"bad mode", runner.Config{BackupID: "x", Mode: "snapshot", Dumper: fakeDumper{}, Archiver: fakeArchiver{}, Uploader: newFakeUploader(), Reporter: &fakeReporter{}}},
		{"no dumper", runner.Config{BackupID: "x", Mode: runner.ModeFull, Archiver: fakeArchiver{}, Uploader: newFakeUploader(), Reporter: &fakeReporter{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runner.Run(context.Background(), tc.cfg); err == nil {
				t.Error("Run accepted an invalid config")
			}
		})
	}
}

// retStore is an in-memory runner.RetentionStore.
type retStore struct {
	backups []runner.RetentionBackup
	policy  backup.RetentionPolicy
	expired []string
}

func (s *retStore) RetentionInputs(context.Context) ([]runner.RetentionBackup, backup.RetentionPolicy, error) {
	return s.backups, s.policy, nil
}

func (s *retStore) MarkExpired(_ context.Context, backupID string) error {
	s.expired = append(s.expired, backupID)
	return nil
}

func TestRunEnforcesRetentionAfterBackup(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	store := &retStore{
		policy: backup.RetentionPolicy{RetainDays: 30, RetainCount: 2, RetainMinFull: 0},
		backups: []runner.RetentionBackup{
			{ID: "old-1", Type: "postgres", CreatedAt: now.AddDate(0, 0, -3), ObjectPath: "backups/postgres/old-1/x.enc"},
			{ID: "old-2", Type: "postgres", CreatedAt: now.AddDate(0, 0, -2), ObjectPath: "backups/postgres/old-2/x.enc"},
			{ID: "new-1", Type: "postgres", CreatedAt: now.AddDate(0, 0, -1), ObjectPath: "backups/postgres/new-1/x.enc"},
		},
	}
	uploader := newFakeUploader()
	result, err := runner.Run(context.Background(), runner.Config{
		BackupID:       "bkp-now",
		Mode:           runner.ModePostgres,
		Dumper:         fakeDumper{},
		Archiver:       fakeArchiver{},
		Uploader:       uploader,
		Pruner:         uploader,
		Reporter:       &fakeReporter{},
		RetentionStore: store,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// RetainCount 2: the oldest backup is pruned.
	if len(result.Pruned) != 1 || result.Pruned[0] != "old-1" {
		t.Errorf("pruned = %v, want [old-1]", result.Pruned)
	}
	// The pruned backup is marked expired in the store and removed from
	// MinIO — the §25.11 coordinated sequence.
	if len(store.expired) != 1 || store.expired[0] != "old-1" {
		t.Errorf("expired = %v, want [old-1]", store.expired)
	}
	if len(uploader.deleted) != 1 || uploader.deleted[0] != "backups/postgres/old-1/x.enc" {
		t.Errorf("deleted objects = %v, want the old-1 object", uploader.deleted)
	}
}

func TestStoragePathLayout(t *testing.T) {
	ts := time.Date(2026, 5, 18, 2, 30, 15, 0, time.UTC)
	got := runner.StoragePath("full", "bkp-xyz", ts)
	want := "backups/full/bkp-xyz/20260518T023015Z.tar.gz.enc"
	if got != want {
		t.Errorf("StoragePath = %q, want %q", got, want)
	}
}
