// SPDX-License-Identifier: MIT

package backup_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

func ptr(t time.Time) *time.Time { return &t }

// seriesValue returns the value of the named counter/gauge series whose
// labels match want, or (0, false) when absent.
func seriesValue(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) (float64, bool) {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if !labelsMatch(m, want) {
				continue
			}
			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				return m.GetCounter().GetValue(), true
			case dto.MetricType_GAUGE:
				return m.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

// histogramSeries returns the histogram with the given name whose labels
// match want, or (nil, false) when absent.
func histogramSeries(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) (*dto.Histogram, bool) {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name || mf.GetType() != dto.MetricType_HISTOGRAM {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsMatch(m, want) {
				return m.GetHistogram(), true
			}
		}
	}
	return nil, false
}

func labelsMatch(m *dto.Metric, want map[string]string) bool {
	got := map[string]string{}
	for _, lp := range m.GetLabel() {
		got[lp.GetName()] = lp.GetValue()
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return len(got) == len(want)
}

// bucketCount returns the cumulative count for the histogram bucket whose
// upper bound is le.
func bucketCount(h *dto.Histogram, le float64) uint64 {
	for _, b := range h.GetBucket() {
		if b.GetUpperBound() == le {
			return b.GetCumulativeCount()
		}
	}
	return 0
}

// seedBackupRow inserts a full backup row directly into the store.
func seedBackupRow(t *testing.T, store *backup.MemStore, b backup.Backup) {
	t.Helper()
	if err := store.InsertBackup(context.Background(), b); err != nil {
		t.Fatalf("InsertBackup: %v", err)
	}
}

func seedRestore(t *testing.T, store *backup.MemStore, r backup.RestoreState) {
	t.Helper()
	if err := store.InsertRestore(context.Background(), r); err != nil {
		t.Fatalf("InsertRestore: %v", err)
	}
}

// spec: §25.11 Metrics table (lines 4306-4308) — lenny_backup_total counts
// terminal outcomes by type and status, lenny_backup_size_bytes is a
// per-backup gauge, and lenny_backup_duration_seconds is a per-type
// histogram derived from the ops_backups rows at scrape time.
func TestMetricsCollectorBackupOutcomes_spec_25_11(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	start := fixedNow
	seedBackupRow(t, store, backup.Backup{ID: "bkp-a", Type: "full", Status: backup.StatusCompleted,
		StartedAt: start, CompletedAt: ptr(start.Add(30 * time.Second)), SizeBytes: 1000})
	seedBackupRow(t, store, backup.Backup{ID: "bkp-b", Type: "full", Status: backup.StatusCompleted,
		StartedAt: start, CompletedAt: ptr(start.Add(90 * time.Second)), SizeBytes: 2000})
	seedBackupRow(t, store, backup.Backup{ID: "bkp-c", Type: "full", Status: backup.StatusFailed,
		StartedAt: start, CompletedAt: ptr(start.Add(3 * time.Second))})
	seedBackupRow(t, store, backup.Backup{ID: "bkp-d", Type: "incremental", Status: backup.StatusCompleted,
		StartedAt: start, CompletedAt: ptr(start.Add(5 * time.Second)), SizeBytes: 500})
	// An in-flight backup is not an outcome and must not be counted.
	seedBackupRow(t, store, backup.Backup{ID: "bkp-e", Type: "full", Status: backup.StatusRunning, StartedAt: start})

	reg := prometheus.NewRegistry()
	reg.MustRegister(backup.NewMetricsCollector(svc))

	if v, ok := seriesValue(t, reg, "lenny_backup_total", map[string]string{"type": "full", "status": "completed"}); !ok || v != 2 {
		t.Errorf("backup_total{full,completed} = %v (present=%v), want 2", v, ok)
	}
	if v, ok := seriesValue(t, reg, "lenny_backup_total", map[string]string{"type": "full", "status": "failed"}); !ok || v != 1 {
		t.Errorf("backup_total{full,failed} = %v (present=%v), want 1", v, ok)
	}
	if v, ok := seriesValue(t, reg, "lenny_backup_total", map[string]string{"type": "incremental", "status": "completed"}); !ok || v != 1 {
		t.Errorf("backup_total{incremental,completed} = %v (present=%v), want 1", v, ok)
	}
	// The running backup contributes no outcome series.
	if _, ok := seriesValue(t, reg, "lenny_backup_total", map[string]string{"type": "full", "status": "running"}); ok {
		t.Error("backup_total{full,running} present, want absent")
	}

	// Per-backup size gauge — only completed backups.
	if v, ok := seriesValue(t, reg, "lenny_backup_size_bytes", map[string]string{"type": "full", "backup_id": "bkp-a"}); !ok || v != 1000 {
		t.Errorf("backup_size_bytes{a} = %v (present=%v), want 1000", v, ok)
	}
	if v, ok := seriesValue(t, reg, "lenny_backup_size_bytes", map[string]string{"type": "incremental", "backup_id": "bkp-d"}); !ok || v != 500 {
		t.Errorf("backup_size_bytes{d} = %v (present=%v), want 500", v, ok)
	}
	if _, ok := seriesValue(t, reg, "lenny_backup_size_bytes", map[string]string{"type": "full", "backup_id": "bkp-c"}); ok {
		t.Error("backup_size_bytes for a failed backup present, want absent")
	}

	// Per-type duration histogram — full has two observations (30s, 90s).
	h, ok := histogramSeries(t, reg, "lenny_backup_duration_seconds", map[string]string{"type": "full"})
	if !ok {
		t.Fatal("backup_duration_seconds{full} histogram absent")
	}
	if h.GetSampleCount() != 2 {
		t.Errorf("full duration count = %d, want 2", h.GetSampleCount())
	}
	if h.GetSampleSum() != 120 {
		t.Errorf("full duration sum = %v, want 120", h.GetSampleSum())
	}
	if c := bucketCount(h, 30); c != 1 {
		t.Errorf("full duration le=30 cumulative = %d, want 1", c)
	}
	if c := bucketCount(h, 120); c != 2 {
		t.Errorf("full duration le=120 cumulative = %d, want 2", c)
	}
}

// spec: §25.11 Metrics table (lines 4309-4310) — lenny_restore_total counts
// terminal restores by status and lenny_restore_duration_seconds is the
// completed-restore duration histogram.
func TestMetricsCollectorRestoreOutcomes_spec_25_11(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	start := fixedNow
	seedRestore(t, store, backup.RestoreState{ID: "rst-a", Status: backup.RestoreStatusCompleted,
		StartedAt: start, CompletedAt: ptr(start.Add(60 * time.Second))})
	seedRestore(t, store, backup.RestoreState{ID: "rst-b", Status: backup.RestoreStatusFailed, StartedAt: start})
	// A running restore is not a terminal outcome.
	seedRestore(t, store, backup.RestoreState{ID: "rst-c", Status: backup.RestoreStatusRunning, StartedAt: start})

	reg := prometheus.NewRegistry()
	reg.MustRegister(backup.NewMetricsCollector(svc))

	if v, ok := seriesValue(t, reg, "lenny_restore_total", map[string]string{"status": "completed"}); !ok || v != 1 {
		t.Errorf("restore_total{completed} = %v (present=%v), want 1", v, ok)
	}
	if v, ok := seriesValue(t, reg, "lenny_restore_total", map[string]string{"status": "failed"}); !ok || v != 1 {
		t.Errorf("restore_total{failed} = %v (present=%v), want 1", v, ok)
	}
	if _, ok := seriesValue(t, reg, "lenny_restore_total", map[string]string{"status": "running"}); ok {
		t.Error("restore_total{running} present, want absent")
	}

	h, ok := histogramSeries(t, reg, "lenny_restore_duration_seconds", map[string]string{})
	if !ok {
		t.Fatal("restore_duration_seconds histogram absent")
	}
	if h.GetSampleCount() != 1 || h.GetSampleSum() != 60 {
		t.Errorf("restore duration count/sum = %d/%v, want 1/60", h.GetSampleCount(), h.GetSampleSum())
	}
}

// spec: §25.11 — an empty store reports no outcome series and no restore
// histogram, so the alerts read "no series" rather than a misleading zero.
func TestMetricsCollectorEmptyStore_spec_25_11(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	reg := prometheus.NewRegistry()
	reg.MustRegister(backup.NewMetricsCollector(svc))

	if _, ok := seriesValue(t, reg, "lenny_backup_total", map[string]string{"type": "full", "status": "completed"}); ok {
		t.Error("backup_total present on empty store, want absent")
	}
	if _, ok := histogramSeries(t, reg, "lenny_restore_duration_seconds", map[string]string{}); ok {
		t.Error("restore_duration_seconds present on empty store, want absent")
	}
}
