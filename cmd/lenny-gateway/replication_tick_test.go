// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore/replication"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// TestRunReplicationControllerTickCatchesDrift drives the live §25.11
// residency-preflight periodic tick through runReplicationController's loop
// with an injected tick channel. Startup configures replication while the
// destination jurisdiction tag matches the source residency region; the tag
// then drifts to a different region (the tag-drift / DNS-rebinding hazard the
// spec calls out). The next periodic tick must re-run the preflight, suspend
// the region fail-closed, emit DataResidencyViolationAttempt, and increment
// lenny_minio_replication_residency_violation_total — without any replication
// batch having been submitted. spec: §25.11 (Runtime residency preflight —
// periodic tick). F-12.5.20 / F-16.7.2.
//
// diagnosis: The residency-preflight periodic tick no longer re-runs the
// preflight (or is disconnected from the ticker), so a post-startup
// jurisdiction-tag drift or DNS rebinding of the replication destination is
// not caught between replication batches and artifacts silently cross a
// jurisdiction boundary.
func TestRunReplicationControllerTickCatchesDrift(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app := &fakeAppender{}
	fd := replication.NewFakeDriver()
	fd.SetJurisdictionTag("dest-bucket", "eu-west-1") // matches source residency

	cfg := replication.Config{
		Enabled: true,
		Regions: []replication.RegionConfig{{
			Region:              "eu-west-1",
			SourceBucket:        "src",
			DataResidencyRegion: "eu-west-1",
			Target: replication.Target{
				Endpoint:               "https://dest:9000",
				Bucket:                 "dest-bucket",
				AccessCredentialSecret: "repl-dest",
			},
		}},
	}
	ctrl, err := replication.NewController(replication.ControllerConfig{
		Config:  cfg,
		Driver:  fd,
		Audit:   replicationAuditSink{appender: app}.emit,
		Metrics: replicationMetricsAdapter{m: m},
	})
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}

	tickCh := make(chan time.Time)
	// processed is signaled after each tick's preflight+measure complete, so
	// the test observes controller state only when the loop is quiescent
	// (no tick in flight).
	processed := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		runReplicationControllerLoop(ctx, ctrl, func(string, ...any) {}, tickCh,
			func() { processed <- struct{}{} })
		close(done)
	}()

	// Fire the first tick while the tag still matches. The send unblocks only
	// after startup Configure completed (proving the region was configured at
	// startup), and this tick's preflight re-verifies residency and leaves the
	// region active.
	tickCh <- time.Now()
	<-processed
	if !fd.IsConfigured("eu-west-1") {
		t.Fatalf("region was not configured at startup")
	}
	if fd.IsSuspended("eu-west-1") {
		t.Fatalf("region suspended while the jurisdiction tag matched")
	}
	if got := violationEvents(app.snapshot()); got != 0 {
		t.Fatalf("unexpected residency violation before drift: %d", got)
	}

	// Post-startup drift: the destination bucket's jurisdiction tag flips to a
	// non-matching region. The startup preflight already passed, so only the
	// periodic tick can catch this.
	fd.SetJurisdictionTag("dest-bucket", "us-east-1")

	// Advance past residencyCheckIntervalSeconds by delivering the next tick.
	tickCh <- time.Now()
	<-processed

	if !fd.IsSuspended("eu-west-1") {
		t.Fatalf("periodic residency tick did not suspend the drifted region")
	}
	if got := violationEvents(app.snapshot()); got != 1 {
		t.Fatalf("want 1 DataResidencyViolationAttempt from the tick, got %d", got)
	}
	if body := metricsBody(t, m); !strings.Contains(body,
		`lenny_minio_replication_residency_violation_total{region="eu-west-1"} 1`) {
		t.Fatalf("residency-violation counter not incremented by the tick:\n%s", body)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("runReplicationControllerLoop did not return after cancel")
	}
}
