// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/backup/restoretest"
)

// spec: §25.11 lines 4128-4256 — the verify and restore-test seams.

type fakeResolver struct {
	target   Target
	ok       bool
	resolveE error
	latestE  error
}

func (f fakeResolver) Resolve(_ context.Context, _ string) (Target, error) {
	return f.target, f.resolveE
}
func (f fakeResolver) ResolveLatest(_ context.Context, _ string) (Target, bool, error) {
	return f.target, f.ok, f.latestE
}

type fakeDownloader struct {
	data []byte
	err  error
}

func (f fakeDownloader) Download(_ context.Context, _ string) ([]byte, error) {
	return f.data, f.err
}

type fakeOpener struct {
	dumps [][]byte
	err   error
}

func (f fakeOpener) ExtractPostgresDumps(_ context.Context, _ []byte) ([][]byte, error) {
	return f.dumps, f.err
}

type fakeInspector struct{ err error }

func (f fakeInspector) ListDump(_ context.Context, _ []byte) error { return f.err }

type fakeRestorer struct {
	called bool
	err    error
}

func (f *fakeRestorer) RestoreAndSmoke(_ context.Context, _ [][]byte) error {
	f.called = true
	return f.err
}

type fakeSampler struct {
	present, sampled int
	err              error
}

func (f fakeSampler) SampleHeads(_ context.Context, _ int) (int, int, error) {
	return f.present, f.sampled, f.err
}

type fakeVerifyReporter struct {
	verified     string
	failedID     string
	failedReason string
}

func (f *fakeVerifyReporter) MarkVerified(_ context.Context, id string) error {
	f.verified = id
	return nil
}
func (f *fakeVerifyReporter) MarkVerificationFailed(_ context.Context, id, reason string) error {
	f.failedID = id
	f.failedReason = reason
	return nil
}

// spec: §25.11 lines 4128-4133 — a verify of a sound archive runs the
// checksum check and pg_restore --list and records status:verified.
func TestRunVerifyHappyPath_spec_25_11_4128(t *testing.T) {
	rep := &fakeVerifyReporter{}
	err := RunVerify(context.Background(), VerifyConfig{
		BackupID:   "bkp-1",
		Resolver:   fakeResolver{target: Target{BackupID: "bkp-1", ObjectPath: "p", Checksum: sha256Hex([]byte("arc"))}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{dumps: [][]byte{[]byte("d0"), []byte("d1")}},
		Inspector:  fakeInspector{},
		Reporter:   rep,
	})
	if err != nil {
		t.Fatalf("RunVerify: %v", err)
	}
	if rep.verified != "bkp-1" {
		t.Errorf("MarkVerified id = %q, want bkp-1", rep.verified)
	}
	if rep.failedID != "" {
		t.Errorf("unexpected MarkVerificationFailed: %s/%s", rep.failedID, rep.failedReason)
	}
}

// spec: §25.11 line 4131 — a checksum mismatch is BACKUP_VERIFICATION_FAILED.
func TestRunVerifyChecksumMismatch_spec_25_11_4131(t *testing.T) {
	rep := &fakeVerifyReporter{}
	err := RunVerify(context.Background(), VerifyConfig{
		BackupID:   "bkp-2",
		Resolver:   fakeResolver{target: Target{BackupID: "bkp-2", ObjectPath: "p", Checksum: "deadbeef"}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{},
		Inspector:  fakeInspector{},
		Reporter:   rep,
	})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	if rep.failedID != "bkp-2" {
		t.Errorf("MarkVerificationFailed id = %q, want bkp-2", rep.failedID)
	}
	if rep.verified != "" {
		t.Errorf("must not MarkVerified on mismatch")
	}
}

// spec: §25.11 line 4132 — an unreadable dump fails pg_restore --list.
func TestRunVerifyUnreadableDump_spec_25_11_4132(t *testing.T) {
	rep := &fakeVerifyReporter{}
	err := RunVerify(context.Background(), VerifyConfig{
		BackupID:   "bkp-3",
		Resolver:   fakeResolver{target: Target{BackupID: "bkp-3", ObjectPath: "p"}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{dumps: [][]byte{[]byte("d0")}},
		Inspector:  fakeInspector{err: errors.New("corrupt header")},
		Reporter:   rep,
	})
	if !errors.Is(err, ErrDumpUnreadable) {
		t.Fatalf("err = %v, want ErrDumpUnreadable", err)
	}
	if rep.failedID != "bkp-3" {
		t.Errorf("MarkVerificationFailed id = %q, want bkp-3", rep.failedID)
	}
}

// A config-only archive carries no Postgres dump; verify runs the
// checksum only and records verified.
func TestRunVerifyConfigOnlyArchive(t *testing.T) {
	rep := &fakeVerifyReporter{}
	err := RunVerify(context.Background(), VerifyConfig{
		BackupID:   "bkp-cfg",
		Resolver:   fakeResolver{target: Target{BackupID: "bkp-cfg", ObjectPath: "p"}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{dumps: nil},
		Inspector:  fakeInspector{err: errors.New("must not be called")},
		Reporter:   rep,
	})
	if err != nil {
		t.Fatalf("RunVerify config-only: %v", err)
	}
	if rep.verified != "bkp-cfg" {
		t.Errorf("MarkVerified id = %q, want bkp-cfg", rep.verified)
	}
}

// A download failure marks verification_failed and returns the error.
func TestRunVerifyDownloadError(t *testing.T) {
	rep := &fakeVerifyReporter{}
	err := RunVerify(context.Background(), VerifyConfig{
		BackupID:   "bkp-4",
		Resolver:   fakeResolver{target: Target{BackupID: "bkp-4", ObjectPath: "p"}},
		Downloader: fakeDownloader{err: errors.New("minio down")},
		Opener:     fakeOpener{},
		Inspector:  fakeInspector{},
		Reporter:   rep,
	})
	if err == nil {
		t.Fatal("expected an error on download failure")
	}
	if rep.failedID != "bkp-4" {
		t.Errorf("MarkVerificationFailed id = %q, want bkp-4", rep.failedID)
	}
}

// A resolve failure returns the error without touching the status.
func TestRunVerifyResolveError(t *testing.T) {
	rep := &fakeVerifyReporter{}
	err := RunVerify(context.Background(), VerifyConfig{
		BackupID:   "bkp-5",
		Resolver:   fakeResolver{resolveE: errors.New("no such backup")},
		Downloader: fakeDownloader{},
		Opener:     fakeOpener{},
		Inspector:  fakeInspector{},
		Reporter:   rep,
	})
	if err == nil {
		t.Fatal("expected an error on resolve failure")
	}
	if rep.failedID != "" || rep.verified != "" {
		t.Errorf("must not touch status when the backup cannot be resolved")
	}
}

// spec: §25.11 lines 4254-4256 — a sound backup with a fully-present
// artifact sample passes the restore test and records success.
func TestRunRestoreTestHappyPath_spec_25_11_4254(t *testing.T) {
	store := restoretest.NewMemory()
	restorer := &fakeRestorer{}
	res, err := RunRestoreTest(context.Background(), RestoreTestConfig{
		JobID:              "job-1",
		Selector:           "full",
		ArtifactSampleSize: 100,
		Resolver:           fakeResolver{ok: true, target: Target{BackupID: "bkp-1", BackupType: "full", Checksum: sha256Hex([]byte("arc"))}},
		Downloader:         fakeDownloader{data: []byte("arc")},
		Opener:             fakeOpener{dumps: [][]byte{[]byte("d0")}},
		Inspector:          fakeInspector{},
		Restorer:           restorer,
		Sampler:            fakeSampler{present: 100, sampled: 100},
		Store:              store,
	})
	if err != nil {
		t.Fatalf("RunRestoreTest: %v", err)
	}
	if !res.Success {
		t.Errorf("Success = false, want true: %s", res.Error)
	}
	if !restorer.called {
		t.Error("the scratch restorer must be invoked")
	}
	if !res.ArtifactChecked || res.ArtifactSuccessRate != 1.0 {
		t.Errorf("artifact check: checked=%v rate=%v", res.ArtifactChecked, res.ArtifactSuccessRate)
	}
	latest, ok, _ := store.Latest(context.Background())
	if !ok || latest.ID != "job-1" || !latest.Success {
		t.Errorf("store latest = %+v ok=%v", latest, ok)
	}
}

// spec: §25.11 line 4098 — an artifact success rate below 99% fails the
// restore test (lenny_restore_test_success = 0).
func TestRunRestoreTestArtifactBelowFloor_spec_25_11_4098(t *testing.T) {
	store := restoretest.NewMemory()
	res, err := RunRestoreTest(context.Background(), RestoreTestConfig{
		JobID:              "job-2",
		ArtifactSampleSize: 100,
		Resolver:           fakeResolver{ok: true, target: Target{BackupID: "bkp-2", BackupType: "full"}},
		Downloader:         fakeDownloader{data: []byte("arc")},
		Opener:             fakeOpener{dumps: [][]byte{[]byte("d0")}},
		Inspector:          fakeInspector{},
		Sampler:            fakeSampler{present: 98, sampled: 100},
		Store:              store,
	})
	if err != nil {
		t.Fatalf("RunRestoreTest: %v", err)
	}
	if res.Success {
		t.Error("Success = true, want false below the 99% floor")
	}
	if res.ArtifactMissing != 2 {
		t.Errorf("ArtifactMissing = %d, want 2", res.ArtifactMissing)
	}
	if res.ArtifactSuccessRate != 0.98 {
		t.Errorf("rate = %v, want 0.98", res.ArtifactSuccessRate)
	}
}

// The 99% floor is inclusive: exactly 0.99 passes.
func TestRunRestoreTestArtifactAtFloor(t *testing.T) {
	store := restoretest.NewMemory()
	res, _ := RunRestoreTest(context.Background(), RestoreTestConfig{
		JobID:              "job-3",
		ArtifactSampleSize: 100,
		Resolver:           fakeResolver{ok: true, target: Target{BackupID: "bkp-3"}},
		Downloader:         fakeDownloader{data: []byte("arc")},
		Opener:             fakeOpener{dumps: [][]byte{[]byte("d0")}},
		Inspector:          fakeInspector{},
		Sampler:            fakeSampler{present: 99, sampled: 100},
		Store:              store,
	})
	if !res.Success {
		t.Errorf("Success = false at exactly 0.99, want true")
	}
}

// No backup matching the selector records a failure rather than a
// silent no-op (the F-17.3.6 defect).
func TestRunRestoreTestNoBackupSelected(t *testing.T) {
	store := restoretest.NewMemory()
	res, err := RunRestoreTest(context.Background(), RestoreTestConfig{
		JobID:      "job-4",
		Selector:   "full",
		Resolver:   fakeResolver{ok: false},
		Downloader: fakeDownloader{},
		Opener:     fakeOpener{},
		Inspector:  fakeInspector{},
		Store:      store,
	})
	if err != nil {
		t.Fatalf("RunRestoreTest: %v", err)
	}
	if res.Success {
		t.Error("Success = true, want false when no backup matched")
	}
	latest, ok, _ := store.Latest(context.Background())
	if !ok || latest.Success {
		t.Errorf("a failed result must be recorded: %+v ok=%v", latest, ok)
	}
}

// A failed scratch restore fails the test.
func TestRunRestoreTestScratchRestoreFails(t *testing.T) {
	store := restoretest.NewMemory()
	res, _ := RunRestoreTest(context.Background(), RestoreTestConfig{
		JobID:      "job-5",
		Resolver:   fakeResolver{ok: true, target: Target{BackupID: "bkp-5"}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{dumps: [][]byte{[]byte("d0")}},
		Inspector:  fakeInspector{},
		Restorer:   &fakeRestorer{err: errors.New("relation already exists")},
		Store:      store,
	})
	if res.Success {
		t.Error("Success = true, want false on a failed scratch restore")
	}
}

// With no Restorer and no Sampler, the run verifies readability only and
// passes; the artifact check is reported as not run.
func TestRunRestoreTestReadabilityOnly(t *testing.T) {
	store := restoretest.NewMemory()
	res, err := RunRestoreTest(context.Background(), RestoreTestConfig{
		JobID:      "job-6",
		Resolver:   fakeResolver{ok: true, target: Target{BackupID: "bkp-6"}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{dumps: [][]byte{[]byte("d0")}},
		Inspector:  fakeInspector{},
		Store:      store,
	})
	if err != nil {
		t.Fatalf("RunRestoreTest: %v", err)
	}
	if !res.Success {
		t.Errorf("Success = false, want true: %s", res.Error)
	}
	if res.ArtifactChecked {
		t.Error("ArtifactChecked = true, want false when no sampler is wired")
	}
}

type errStore struct{}

func (errStore) Record(context.Context, restoretest.Result) error {
	return errors.New("postgres down")
}
func (errStore) Latest(context.Context) (restoretest.Result, bool, error) {
	return restoretest.Result{}, false, nil
}
func (errStore) TotalArtifactMissing(context.Context) (int64, error) { return 0, nil }

// A Store write failure surfaces as an error so the Job fails visibly.
func TestRunRestoreTestStoreError(t *testing.T) {
	_, err := RunRestoreTest(context.Background(), RestoreTestConfig{
		JobID:      "job-7",
		Resolver:   fakeResolver{ok: true, target: Target{BackupID: "bkp-7"}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{dumps: [][]byte{[]byte("d0")}},
		Inspector:  fakeInspector{},
		Store:      errStore{},
	})
	if err == nil {
		t.Fatal("expected an error when the result store write fails")
	}
}
