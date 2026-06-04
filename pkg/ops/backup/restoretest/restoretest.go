// SPDX-License-Identifier: MIT

// Package restoretest holds the §25.11 Test Restore result record and
// the store that persists it. The lenny-restore-test CronJob (the
// lenny-backup binary in restore-test mode) records one Result per run;
// the leader lenny-ops replica reads the latest Result on each scrape
// and re-exposes it as the §25.11 / §16.1 restore-test Prometheus
// series. A short-lived Job pod cannot hold a scrapeable Prometheus
// registry across its lifetime, so the outcome is durable in Postgres
// and lenny-ops publishes it, mirroring the
// lenny_backup_last_successful_timestamp sampling path.
//
// spec: §25.11 lines 4098, 4128-4133, 4254-4256; §16.1 restore-test gate.
package restoretest

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Result is the outcome of one §25.11 Test Restore run.
type Result struct {
	// ID is the result row id (the §25.11 Job id).
	ID string
	// BackupID is the backup the run restored. Empty when no backup
	// matched the restore-test backup selector.
	BackupID string
	// BackupType is the backup type the run restored ("full",
	// "postgres", ...).
	BackupType string
	// StartedAt and CompletedAt bound the run.
	StartedAt   time.Time
	CompletedAt time.Time
	// Success is the §16.1 restore-test gate: true only when the archive
	// downloaded, its checksum matched, the Postgres dump was readable
	// (and restored, when a scratch DSN was configured), and the sampled
	// ArtifactStore success rate met the §25.11 99% floor.
	Success bool
	// ArtifactChecked reports whether the §25.11 sampled-HEAD
	// ArtifactStore check ran (it is skipped when no sample size or no
	// replication target is configured).
	ArtifactChecked bool
	// ArtifactSampled is the number of object keys HEAD-checked.
	ArtifactSampled int
	// ArtifactPresent is the number of sampled keys present at the
	// replication target.
	ArtifactPresent int
	// ArtifactMissing is the number of sampled keys absent at the
	// replication target (ArtifactSampled - ArtifactPresent), the
	// lenny_restore_test_artifact_missing_total source.
	ArtifactMissing int
	// ArtifactSuccessRate is ArtifactPresent / ArtifactSampled, the
	// lenny_restore_test_artifact_success_rate source. It is 1.0 when no
	// keys were sampled.
	ArtifactSuccessRate float64
	// Error carries the §25.11 failure reason when Success is false.
	Error string
}

// DurationSeconds returns the run's elapsed wall-clock seconds, the
// lenny_restore_test_duration_seconds source. It clamps a non-positive
// span to zero so a clock skew does not publish a negative duration.
func (r Result) DurationSeconds() float64 {
	d := r.CompletedAt.Sub(r.StartedAt).Seconds()
	if d < 0 {
		return 0
	}
	return d
}

// Store persists §25.11 Test Restore results. The lenny-backup binary
// uses Record; the lenny-ops metric sampler uses Latest and
// TotalArtifactMissing.
type Store interface {
	// Record inserts one Test Restore result.
	Record(ctx context.Context, r Result) error
	// Latest returns the most recently completed result. The boolean is
	// false when no result has been recorded yet, so the sampler can
	// leave the gauges unset rather than publishing a zero that reads as
	// a failed restore before the first run.
	Latest(ctx context.Context) (Result, bool, error)
	// TotalArtifactMissing returns the cumulative count of sampled
	// objects absent across every recorded run, the monotonic source for
	// the lenny_restore_test_artifact_missing_total counter.
	TotalArtifactMissing(ctx context.Context) (int64, error)
}

// Memory is an in-memory Store for tests and a Kubernetes-less local
// deployment.
type Memory struct {
	mu      sync.Mutex
	results []Result
}

var _ Store = (*Memory)(nil)

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{} }

// Record implements Store.
func (m *Memory) Record(_ context.Context, r Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = append(m.results, r)
	return nil
}

// Latest implements Store.
func (m *Memory) Latest(_ context.Context) (Result, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.results) == 0 {
		return Result{}, false, nil
	}
	sorted := append([]Result(nil), m.results...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CompletedAt.Before(sorted[j].CompletedAt)
	})
	return sorted[len(sorted)-1], true, nil
}

// TotalArtifactMissing implements Store.
func (m *Memory) TotalArtifactMissing(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, r := range m.results {
		total += int64(r.ArtifactMissing)
	}
	return total, nil
}
