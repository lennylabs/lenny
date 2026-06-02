// SPDX-License-Identifier: MIT

package backup_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// fakeReplication is a §25.11 ReplicationLagSource for tests. A non-nil
// err makes both methods fail so the preview's unavailable-warning path
// is exercised.
type fakeReplication struct {
	lag        int
	orphans    int
	err        error
	gotTakenAt time.Time
}

func (f *fakeReplication) ReplicationLagSeconds(context.Context) (int, error) {
	return f.lag, f.err
}

func (f *fakeReplication) EstimatedOrphanArtifactRows(_ context.Context, takenAt time.Time) (int, error) {
	f.gotTakenAt = takenAt
	return f.orphans, f.err
}

// fakeDataLoss is a §25.11 DataLossEstimator for tests.
type fakeDataLoss struct {
	est        backup.DataLossEstimate
	err        error
	gotTakenAt time.Time
	gotNow     time.Time
}

func (f *fakeDataLoss) EstimateDataLoss(_ context.Context, takenAt, now time.Time) (backup.DataLossEstimate, error) {
	f.gotTakenAt = takenAt
	f.gotNow = now
	return f.est, f.err
}

// newServiceWith builds a §25.11 Service over in-memory dependencies
// with the supplied estimate sources wired.
func newServiceWith(t *testing.T, repl backup.ReplicationLagSource, dl backup.DataLossEstimator) (*backup.Service, *backup.MemStore) {
	t.Helper()
	store := backup.NewMemStore()
	seq := 0
	svc, err := backup.NewService(backup.Config{
		Store:           store,
		Launcher:        backup.NewFakeLauncher(),
		Locker:          backup.NewMemLocker(),
		PlatformVersion: "1.5.0",
		SchemaVersion:   42,
		ReplicationLag:  repl,
		DataLoss:        dl,
		Now:             func() time.Time { return fixedNow },
		NewID: func(prefix string) string {
			seq++
			return prefix + "-" + string(rune('a'+seq-1))
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store
}

// spec: §25.11 line 4094 — the preview populates
// artifactReplicationLagSeconds and estimatedOrphanArtifactRows from the
// replication-lag source. F-17.3.12 / F-25.11.14.
func TestPreviewRestorePopulatesReplicationLag(t *testing.T) {
	repl := &fakeReplication{lag: 900, orphans: 17}
	svc, store := newServiceWith(t, repl, nil)
	completed := fixedNow.Add(-time.Hour)
	b := completedBackup(t, svc, store, "full", completed)

	preview, err := svc.PreviewRestore(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if preview.ArtifactReplicationLagSeconds != 900 {
		t.Errorf("artifactReplicationLagSeconds = %d, want 900", preview.ArtifactReplicationLagSeconds)
	}
	if preview.EstimatedOrphanArtifactRows != 17 {
		t.Errorf("estimatedOrphanArtifactRows = %d, want 17", preview.EstimatedOrphanArtifactRows)
	}
	// The orphan estimate is computed for the backup's taken-at time.
	if !repl.gotTakenAt.Equal(completed) {
		t.Errorf("orphan estimate taken-at = %v, want %v", repl.gotTakenAt, completed)
	}
}

// spec: §25.11 line 4094 — a source error leaves the fields zero and
// flags the uncertainty rather than reporting a bare 0. F-17.3.12.
func TestPreviewRestoreReplicationSourceErrorWarns(t *testing.T) {
	repl := &fakeReplication{err: errors.New("prometheus unreachable")}
	svc, store := newServiceWith(t, repl, nil)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))

	preview, err := svc.PreviewRestore(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if preview.ArtifactReplicationLagSeconds != 0 || preview.EstimatedOrphanArtifactRows != 0 {
		t.Errorf("fields = (%d, %d), want zero on source error",
			preview.ArtifactReplicationLagSeconds, preview.EstimatedOrphanArtifactRows)
	}
	if !hasWarning(preview.Warnings, "replication lag is unavailable") {
		t.Errorf("warnings = %v, want an unavailable-replication warning", preview.Warnings)
	}
}

// spec: §25.11 line 4094 — with no replication source the preview
// reports zero lag and no warning (degraded in-memory deployment).
func TestPreviewRestoreNoReplicationSource(t *testing.T) {
	svc, store := newServiceWith(t, nil, nil)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))

	preview, err := svc.PreviewRestore(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if preview.ArtifactReplicationLagSeconds != 0 || preview.EstimatedOrphanArtifactRows != 0 {
		t.Error("expected zero replication fields without a source")
	}
	for _, w := range preview.Warnings {
		if hasWarning([]string{w}, "replication lag is unavailable") {
			t.Errorf("unexpected unavailable warning without a source: %q", w)
		}
	}
}

// spec: §25.11 line 3957 — the preview's downtime is computed, not the
// former "PT15M" constant. F-17.3.16.
func TestPreviewRestoreComputesDowntime(t *testing.T) {
	svc, store := newServiceWith(t, nil, nil)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))
	// Give the backup a recorded size so the estimate is not the base.
	b.SizeBytes = 100 << 20
	if err := store.UpdateBackup(context.Background(), *b); err != nil {
		t.Fatalf("UpdateBackup: %v", err)
	}
	preview, err := svc.PreviewRestore(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	// base 2m + 3 components×1m + 100MiB/50MiBps(2s) = PT5M2S.
	if preview.EstimatedDowntime != "PT5M2S" {
		t.Errorf("estimatedDowntime = %q, want PT5M2S", preview.EstimatedDowntime)
	}
}

// spec: §25.11 line 4225 — the safety check populates the data-loss
// estimate from the estimator. F-17.3.15.
func TestSafetyCheckPopulatesDataLoss(t *testing.T) {
	dl := &fakeDataLoss{est: backup.DataLossEstimate{
		MutationsSinceBackup: 15234,
		SessionsAffected:     124,
		AuditEventsLost:      8921,
		TablesWithDivergence: []string{"sessions", "audit_log"},
	}}
	svc, store := newServiceWith(t, nil, dl)
	completed := fixedNow.Add(-time.Hour)
	b := completedBackup(t, svc, store, "full", completed)

	check, err := svc.SafetyCheckRestore(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("SafetyCheckRestore: %v", err)
	}
	if check.DataLossEstimate.MutationsSinceBackup != 15234 {
		t.Errorf("mutationsSinceBackup = %d, want 15234", check.DataLossEstimate.MutationsSinceBackup)
	}
	if check.DataLossEstimate.SessionsAffected != 124 || check.DataLossEstimate.AuditEventsLost != 8921 {
		t.Errorf("data-loss estimate = %+v, want the seeded values", check.DataLossEstimate)
	}
	if !dl.gotTakenAt.Equal(completed) || !dl.gotNow.Equal(fixedNow) {
		t.Errorf("estimator args = (%v, %v), want (%v, %v)", dl.gotTakenAt, dl.gotNow, completed, fixedNow)
	}
	// An hour-old backup with mutations is not safe.
	if check.Safe {
		t.Error("a backup with mutations since must not be reported safe")
	}
}

// spec: §25.11 line 4227 — a backup with zero mutations since means the
// platform has been idle, so the restore is safe regardless of age.
// F-17.3.15.
func TestSafetyCheckIdlePlatformIsSafe(t *testing.T) {
	dl := &fakeDataLoss{est: backup.DataLossEstimate{MutationsSinceBackup: 0}}
	svc, store := newServiceWith(t, nil, dl)
	// An hour-old backup would normally be unsafe by the 5-minute rule.
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))

	check, err := svc.SafetyCheckRestore(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("SafetyCheckRestore: %v", err)
	}
	if !check.Safe {
		t.Error("an idle platform (zero mutations) restore must be reported safe")
	}
	if check.RecommendedAction != "restore is safe; execute" {
		t.Errorf("recommendedAction = %q, want the safe action", check.RecommendedAction)
	}
}

// spec: §25.11 line 4225 — an estimator error flags the uncertainty in
// the compatibility warnings rather than reporting a zero estimate as
// fact. F-17.3.15.
func TestSafetyCheckDataLossErrorWarns(t *testing.T) {
	dl := &fakeDataLoss{err: errors.New("pg_stat unreachable")}
	svc, store := newServiceWith(t, nil, dl)
	b := completedBackup(t, svc, store, "full", fixedNow.Add(-time.Hour))

	check, err := svc.SafetyCheckRestore(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("SafetyCheckRestore: %v", err)
	}
	if check.DataLossEstimate.MutationsSinceBackup != 0 {
		t.Error("expected a zero estimate on estimator error")
	}
	if !hasWarning(check.Compatibility.Warnings, "data-loss estimate is unavailable") {
		t.Errorf("warnings = %v, want a data-loss-unavailable warning", check.Compatibility.Warnings)
	}
	// Without a usable estimate the restore stays unsafe (5-minute rule).
	if check.Safe {
		t.Error("an unavailable estimate must not flip an old backup to safe")
	}
}

// hasWarning reports whether any warning contains substr.
func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
