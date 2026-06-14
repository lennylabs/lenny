// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// durationBuckets are the §25.11 lenny_backup_duration_seconds and
// lenny_restore_duration_seconds histogram upper bounds in seconds. A
// backup or restore spans seconds in a single-shard dev deployment to
// hours in a multi-shard production restore, so the bounds run from 1s
// to 2h.
var durationBuckets = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600, 7200}

// MetricsCollector is the §25.11 Metrics-table Prometheus collector for
// the backup and restore outcome series:
//
//	lenny_backup_duration_seconds  (histogram, type)
//	lenny_backup_size_bytes        (gauge, type, backup_id)
//	lenny_backup_total             (counter, type, status)
//	lenny_restore_duration_seconds (histogram)
//	lenny_restore_total            (counter, status)
//
// It derives every series from the durable ops_backups /
// ops_restore_state rows at scrape time via const metrics, so the
// counters and histograms survive a lenny-ops restart without an
// in-process completion hook: the database is the source of truth and a
// scrape reconstructs the cumulative distribution from the rows present.
// A load error emits nothing for the affected family rather than
// reporting a zero that would read as "no failures" or a stalled
// distribution. The lenny_backup_last_successful_timestamp gauge is
// published separately by the leader-gated backup-metrics cron, which
// must skip non-leader replicas; these outcome series are
// leader-independent (any replica that can read the store reports the
// same cumulative totals), so they ride the collector instead.
//
// spec: §25.11 Metrics table (lines 4304-4311).
type MetricsCollector struct {
	svc *Service

	backupDuration  *prometheus.Desc
	backupSize      *prometheus.Desc
	backupTotal     *prometheus.Desc
	restoreDuration *prometheus.Desc
	restoreTotal    *prometheus.Desc
}

// NewMetricsCollector returns a collector over svc. Register it on the
// process registry (lenny-ops uses the default registry so the §16.9
// /metrics exposition scrapes it).
func NewMetricsCollector(svc *Service) *MetricsCollector {
	return &MetricsCollector{
		svc: svc,
		backupDuration: prometheus.NewDesc(
			"lenny_backup_duration_seconds",
			"§25.11 backup duration in seconds by type.",
			[]string{"type"}, nil,
		),
		backupSize: prometheus.NewDesc(
			"lenny_backup_size_bytes",
			"§25.11 backup archive size in bytes by type and backup id.",
			[]string{"type", "backup_id"}, nil,
		),
		backupTotal: prometheus.NewDesc(
			"lenny_backup_total",
			"§25.11 backup outcomes by type and terminal status.",
			[]string{"type", "status"}, nil,
		),
		restoreDuration: prometheus.NewDesc(
			"lenny_restore_duration_seconds",
			"§25.11 restore duration in seconds.",
			nil, nil,
		),
		restoreTotal: prometheus.NewDesc(
			"lenny_restore_total",
			"§25.11 restore outcomes by terminal status.",
			[]string{"status"}, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.backupDuration
	ch <- c.backupSize
	ch <- c.backupTotal
	ch <- c.restoreDuration
	ch <- c.restoreTotal
}

// Collect implements prometheus.Collector.
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	if c == nil || c.svc == nil {
		return
	}
	ctx := context.Background()
	c.collectBackups(ctx, ch)
	c.collectRestores(ctx, ch)
}

// histAccumulator accumulates a const histogram from observed durations.
// bkt holds cumulative counts keyed by upper bound (observations <= bound).
type histAccumulator struct {
	count uint64
	sum   float64
	bkt   map[float64]uint64
}

func newHistAccumulator() *histAccumulator {
	bkt := make(map[float64]uint64, len(durationBuckets))
	for _, b := range durationBuckets {
		bkt[b] = 0
	}
	return &histAccumulator{bkt: bkt}
}

func (h *histAccumulator) observe(d float64) {
	h.count++
	h.sum += d
	for _, b := range durationBuckets {
		if d <= b {
			h.bkt[b]++
		}
	}
}

// elapsed is the wall-clock seconds between start and a completion time,
// clamped at zero so a clock skew never produces a negative observation.
func elapsed(start, end time.Time) float64 {
	d := end.Sub(start).Seconds()
	if d < 0 {
		return 0
	}
	return d
}

// terminalBackupStatuses are the §25.11 backup outcomes counted by
// lenny_backup_total. Pending/running/verifying rows are in-flight and
// not yet an outcome.
func isTerminalBackupStatus(status string) bool {
	switch status {
	case StatusCompleted, StatusVerified, StatusFailed, StatusVerificationFailed, StatusExpired:
		return true
	default:
		return false
	}
}

func (c *MetricsCollector) collectBackups(ctx context.Context, ch chan<- prometheus.Metric) {
	backups, err := c.svc.store.ListBackups(ctx, BackupFilter{})
	if err != nil {
		return
	}
	type key struct{ typ, status string }
	totals := make(map[key]uint64)
	durByType := make(map[string]*histAccumulator)
	for _, b := range backups {
		if isTerminalBackupStatus(b.Status) {
			totals[key{b.Type, b.Status}]++
		}
		// A successful backup with a recorded completion contributes its
		// duration to the per-type histogram and its size to the per-backup
		// gauge. A failed or in-flight backup has no meaningful duration or
		// archive size.
		if (b.Status == StatusCompleted || b.Status == StatusVerified) && b.CompletedAt != nil {
			h := durByType[b.Type]
			if h == nil {
				h = newHistAccumulator()
				durByType[b.Type] = h
			}
			h.observe(elapsed(b.StartedAt, *b.CompletedAt))
			if b.SizeBytes > 0 {
				ch <- prometheus.MustNewConstMetric(
					c.backupSize, prometheus.GaugeValue, float64(b.SizeBytes), b.Type, b.ID,
				)
			}
		}
	}
	for k, n := range totals {
		ch <- prometheus.MustNewConstMetric(
			c.backupTotal, prometheus.CounterValue, float64(n), k.typ, k.status,
		)
	}
	for typ, h := range durByType {
		ch <- prometheus.MustNewConstHistogram(
			c.backupDuration, h.count, h.sum, h.bkt, typ,
		)
	}
}

// isTerminalRestoreStatus reports whether a restore row is a completed
// outcome. Running/paused restores are in-flight.
func isTerminalRestoreStatus(status string) bool {
	switch status {
	case RestoreStatusCompleted, RestoreStatusFailed:
		return true
	default:
		return false
	}
}

func (c *MetricsCollector) collectRestores(ctx context.Context, ch chan<- prometheus.Metric) {
	restores, err := c.svc.store.ListRestores(ctx, RestoreFilter{})
	if err != nil {
		return
	}
	totals := make(map[string]uint64)
	dur := newHistAccumulator()
	for _, r := range restores {
		if isTerminalRestoreStatus(r.Status) {
			totals[r.Status]++
		}
		if r.Status == RestoreStatusCompleted && r.CompletedAt != nil {
			dur.observe(elapsed(r.StartedAt, *r.CompletedAt))
		}
	}
	for status, n := range totals {
		ch <- prometheus.MustNewConstMetric(
			c.restoreTotal, prometheus.CounterValue, float64(n), status,
		)
	}
	// Emit the restore duration histogram only when at least one restore
	// has completed; an empty histogram would read as a stalled
	// distribution before the first restore.
	if dur.count > 0 {
		ch <- prometheus.MustNewConstHistogram(
			c.restoreDuration, dur.count, dur.sum, dur.bkt,
		)
	}
}
