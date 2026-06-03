// SPDX-License-Identifier: MIT

package replication

import "context"

// Measurement is one §25.11 ArtifactStore replication health sample for a
// region, read from the source MinIO cluster's bucket-replication
// metrics. The Controller reports it onto the platform's
// lenny_minio_replication_lag_seconds gauge and
// lenny_minio_replication_failed_total counter.
type Measurement struct {
	// LagSeconds is the off-cluster replication target's lag behind the
	// source bucket, the §17.3 / §25.11 RPO signal. It is derived from the
	// source bucket's replication queue depth and observed throughput; see
	// deriveLagSeconds.
	LagSeconds float64
	// FailedTotal is the cumulative object-level replication failure count
	// the source MinIO cluster reports for the bucket (permission,
	// network, destination-full). The reporter tracks deltas, so a
	// cumulative value maps onto the monotonic
	// lenny_minio_replication_failed_total counter.
	FailedTotal float64
}

// LagObserver receives the §25.11 replication-lag and replication-failure
// signals the Controller samples on each measurement tick. The gateway's
// implementation drives the lenny_minio_replication_lag_seconds gauge and
// the lenny_minio_replication_failed_total counter. A nil LagObserver
// drops the signals. This is a separate seam from Metrics (which carries
// the residency-violation signal) so the residency state machine and the
// lag-scrape path stay independent.
type LagObserver interface {
	// ReplicationLag sets the region's replication-lag gauge in seconds.
	ReplicationLag(region string, seconds float64)
	// ReplicationFailures reports the region's cumulative object-level
	// replication failure count. The implementation converts cumulative
	// totals into counter increments.
	ReplicationFailures(region string, totalFailed float64)
}

// deriveLagSeconds estimates the §25.11 replication lag from the source
// bucket's replication queue. Lag is the time to drain the currently
// queued bytes at the observed replication throughput, which is how
// `mc admin replicate status` estimates the backlog. An empty queue is
// zero lag. A non-empty queue with no observed throughput is a stalled
// replication path: the estimate floors throughput at 1 KiB/s so the lag
// surfaces as a large positive value that trips MinIOArtifactReplicationLag*
// rather than reading as zero. spec: §17.3 line 130; §25.11 line 4085.
func deriveLagSeconds(queuedBytes, bandwidthBytesPerSec float64) float64 {
	if queuedBytes <= 0 {
		return 0
	}
	if bandwidthBytesPerSec <= 0 {
		bandwidthBytesPerSec = 1024
	}
	return queuedBytes / bandwidthBytesPerSec
}

// MeasureAll samples replication health for every enabled region with a
// declared target and reports it onto the LagObserver. It is the §25.11 /
// §17.3 producer of lenny_minio_replication_lag_seconds and
// lenny_minio_replication_failed_total: the gauges the spec names but
// which the residency preflight does not itself measure. A measurement
// error for one region is recorded as the first returned error and does
// not stop the sweep over the remaining regions. With no LagObserver the
// method is a no-op.
func (c *Controller) MeasureAll(ctx context.Context) error {
	if !c.cfg.Enabled || c.lag == nil {
		return nil
	}
	var firstErr error
	for _, rc := range c.cfg.Regions {
		if rc.Target.Endpoint == "" {
			continue
		}
		m, err := c.driver.MeasureReplication(ctx, rc)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		c.lag.ReplicationLag(rc.Region, m.LagSeconds)
		c.lag.ReplicationFailures(rc.Region, m.FailedTotal)
	}
	return firstErr
}
