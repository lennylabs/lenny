// SPDX-License-Identifier: MIT

package replication_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore/replication"
)

// lagRecorder collects the §25.11 replication lag/failure signals.
type lagRecorder struct {
	lag    map[string]float64
	failed map[string]float64
}

func newLagRecorder() *lagRecorder {
	return &lagRecorder{lag: map[string]float64{}, failed: map[string]float64{}}
}

func (l *lagRecorder) ReplicationLag(region string, seconds float64) { l.lag[region] = seconds }
func (l *lagRecorder) ReplicationFailures(region string, total float64) {
	l.failed[region] = total
}

func newLagController(t *testing.T, cfg replication.Config, driver *replication.FakeDriver, lag replication.LagObserver) *replication.Controller {
	t.Helper()
	c, err := replication.NewController(replication.ControllerConfig{
		Config: cfg,
		Driver: driver,
		Lag:    lag,
		Now:    func() time.Time { return replNow },
	})
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

// TestMeasureAllReportsLagAndFailures covers the §17.3 line 130 / §25.11
// line 4085 producer of lenny_minio_replication_lag_seconds and
// lenny_minio_replication_failed_total: MeasureAll samples each region's
// declared target and reports the measurement onto the LagObserver.
func TestMeasureAllReportsLagAndFailures_spec_25_11_4085(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetMeasurement("eu-west-1", replication.Measurement{LagSeconds: 42, FailedTotal: 7})
	rec := newLagRecorder()
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c := newLagController(t, cfg, driver, rec)

	if err := c.MeasureAll(context.Background()); err != nil {
		t.Fatalf("MeasureAll: %v", err)
	}
	if rec.lag["eu-west-1"] != 42 {
		t.Errorf("lag = %v, want 42", rec.lag["eu-west-1"])
	}
	if rec.failed["eu-west-1"] != 7 {
		t.Errorf("failed = %v, want 7", rec.failed["eu-west-1"])
	}
}

// TestMeasureAllSkipsRegionsWithoutTarget verifies a region with no
// declared target endpoint (a dev region) is skipped rather than measured,
// matching Configure / PreflightAll. spec: §25.11.
func TestMeasureAllSkipsRegionsWithoutTarget(t *testing.T) {
	driver := replication.NewFakeDriver()
	rec := newLagRecorder()
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{{
		Region:       "dev",
		SourceBucket: "lenny-artifacts",
		// no Target endpoint
	}}}
	c := newLagController(t, cfg, driver, rec)

	if err := c.MeasureAll(context.Background()); err != nil {
		t.Fatalf("MeasureAll: %v", err)
	}
	if _, ok := rec.lag["dev"]; ok {
		t.Error("a region with no target was measured")
	}
}

// TestMeasureAllRecordsFirstErrorAndContinues verifies a measurement error
// for one region is returned as the first error but does not stop the
// sweep over the remaining regions. spec: §25.11.
func TestMeasureAllRecordsFirstErrorAndContinues(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetMeasureError("eu-west-1", errors.New("boom"))
	driver.SetMeasurement("us-east-1", replication.Measurement{LagSeconds: 5, FailedTotal: 1})
	rec := newLagRecorder()
	usRegion := euRegion()
	usRegion.Region = "us-east-1"
	usRegion.DataResidencyRegion = ""
	usRegion.SourceBucket = "lenny-artifacts-us"
	usRegion.Target.Bucket = "lenny-artifacts-us-backup"
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion(), usRegion}}
	c := newLagController(t, cfg, driver, rec)

	if err := c.MeasureAll(context.Background()); err == nil {
		t.Fatal("MeasureAll returned nil despite a region error")
	}
	if rec.lag["us-east-1"] != 5 {
		t.Errorf("us-east-1 lag = %v, want 5 (sweep should continue past the eu error)", rec.lag["us-east-1"])
	}
	if _, ok := rec.lag["eu-west-1"]; ok {
		t.Error("eu-west-1 was reported despite its measurement error")
	}
}

// TestMeasureAllDisabledIsNoOp verifies a disabled config and a nil
// observer both make MeasureAll a no-op. spec: §25.11.
func TestMeasureAllDisabledIsNoOp(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetMeasurement("eu-west-1", replication.Measurement{LagSeconds: 9})

	// Disabled config.
	rec := newLagRecorder()
	disabled := replication.Config{Enabled: false, Regions: []replication.RegionConfig{euRegion()}}
	c := newLagController(t, disabled, driver, rec)
	if err := c.MeasureAll(context.Background()); err != nil {
		t.Fatalf("MeasureAll(disabled): %v", err)
	}
	if len(rec.lag) != 0 {
		t.Errorf("disabled config reported lag: %v", rec.lag)
	}

	// Nil observer: enabled but no Lag wired.
	enabled := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c2 := newLagController(t, enabled, driver, nil)
	if err := c2.MeasureAll(context.Background()); err != nil {
		t.Fatalf("MeasureAll(nil observer): %v", err)
	}
}
