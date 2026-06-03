// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/blobstore/replication"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
)

// parseReplicationConfig decodes the §25.11 minio.artifactBackup
// configuration the operator supplies via --artifact-replication-config
// (LENNY_ARTIFACT_REPLICATION_CONFIG) as JSON, then runs the §25.11
// startup CONFIG_INVALID validation. The JSON field names match the
// replication.Config Go fields case-insensitively (e.g. "enabled",
// "regions", "sourceBucket", "dataResidencyRegion", "target",
// "accessCredentialSecret", "allowedDestinationCidrs"). An empty raw
// string yields a disabled config so the controller is never built.
func parseReplicationConfig(raw string) (replication.Config, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return replication.Config{}, nil
	}
	var cfg replication.Config
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return replication.Config{}, fmt.Errorf("CONFIG_INVALID: artifact-replication-config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return replication.Config{}, err
	}
	return cfg, nil
}

// replicationAuditSink routes the §25.11 ArtifactStore-replication audit
// events the Controller emits into the gateway's §16.7 audit pipeline.
// Replication events are platform-scoped (no tenant), so they land on
// the "platform" chain, matching the §16.7 platform-scoped audit
// convention (e.g. cross_tenant_read). The two cataloged event types are
// artifact.cross_region_replication_verified and
// artifact_replication.resumed; the residency-violation event
// (DataResidencyViolationAttempt) rides the same chain so the §25.11
// "the DataResidencyViolationAttempt audit event never fires" gap is
// closed. F-12.5.20 / F-16.7.2.
type replicationAuditSink struct {
	appender policy.AuditAppender
}

// emit implements replication.AuditSink. A nil appender drops the event;
// an append error is swallowed because the residency state machine has
// already recorded the authoritative outcome and the audit row is the
// observability counterpart, not a gate.
func (s replicationAuditSink) emit(e replication.AuditEvent) {
	if s.appender == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"region":                       e.Region,
		"source_data_residency_region": e.SourceDataResidencyRegion,
		"destination_endpoint":         e.DestinationEndpoint,
		"destination_bucket":           e.DestinationBucket,
		"destination_jurisdiction_tag": e.DestinationJurisdictionTag,
		"operation":                    e.Operation,
		"detail":                       e.Detail,
		"timestamp":                    e.At.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	at := e.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, _ = s.appender.Append(context.Background(), "platform", e.Type, payload, at)
}

// replicationMetricsAdapter bridges the §25.11 replication Controller's
// metric signals onto the gateway's Prometheus counters. F-12.5.20 /
// F-16.7.2.
type replicationMetricsAdapter struct {
	m *gatewaymetrics.Metrics
}

// ResidencyViolation implements replication.Metrics: it advances the
// region-scoped lenny_minio_replication_residency_violation_total and the
// shared lenny_data_residency_violation_total.
func (a replicationMetricsAdapter) ResidencyViolation(region string) {
	a.m.IncMinioReplicationResidencyViolation(region)
}

// ReplicationVerified implements replication.Metrics. The §16.1 catalog
// carries no positive-case replication-verified counter (the positive
// signal is the sampled artifact.cross_region_replication_verified audit
// event), so this is intentionally a no-op.
func (a replicationMetricsAdapter) ReplicationVerified(string) {}

// replicationLagAdapter bridges the §17.3 / §25.11 replication Controller's
// MeasureAll signals onto the gateway's lenny_minio_replication_lag_seconds
// gauge and lenny_minio_replication_failed_total counter. The source MinIO
// cluster reports a cumulative replication-failure total, so the adapter
// tracks the last-observed total per region and advances the counter by the
// non-negative delta; a counter reset (the source process restarting) re-bases
// from the new lower total. F-17.3.7.
type replicationLagAdapter struct {
	m  *gatewaymetrics.Metrics
	mu sync.Mutex
	// lastFailed records the last cumulative failure total observed per
	// region, so ReplicationFailures emits monotonic counter increments.
	lastFailed map[string]float64
}

func newReplicationLagAdapter(m *gatewaymetrics.Metrics) *replicationLagAdapter {
	return &replicationLagAdapter{m: m, lastFailed: map[string]float64{}}
}

// ReplicationLag implements replication.LagObserver.
func (a *replicationLagAdapter) ReplicationLag(region string, seconds float64) {
	a.m.SetMinioReplicationLag(region, seconds)
}

// ReplicationFailures implements replication.LagObserver: it converts the
// source cluster's cumulative failure total into a counter increment.
func (a *replicationLagAdapter) ReplicationFailures(region string, totalFailed float64) {
	a.mu.Lock()
	last, ok := a.lastFailed[region]
	a.lastFailed[region] = totalFailed
	a.mu.Unlock()
	delta := totalFailed
	if ok && totalFailed >= last {
		delta = totalFailed - last
	} else if ok {
		// The source counter dropped (a restart re-based it); re-base
		// without emitting a spurious large increment.
		delta = 0
	}
	a.m.AddMinioReplicationFailed(region, delta)
}

// newReplicationDriver builds the §25.11 production MinIODriver. The
// source client is the gateway's own MinIO endpoint (the cluster holding
// the ArtifactStore bucket). destClientFor resolves each destination's
// AccessCredentialSecret from the named Kubernetes Secret in namespace,
// the §25.11 way the off-cluster replication target credentials are
// provisioned. F-12.5.20.
func newReplicationDriver(src replicationSource, k8s client.Client, namespace, roleARN string) (*replication.MinIODriver, error) {
	if k8s == nil {
		return nil, fmt.Errorf("replication: destination secret resolution requires a Kubernetes client (set --agent-namespace)")
	}
	srcClient, err := minio.New(src.endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(src.accessKey, src.secretKey, ""),
		Secure: src.useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("replication: build source MinIO client: %w", err)
	}
	destClientFor := func(t replication.Target) (*minio.Client, error) {
		var sec corev1.Secret
		key := client.ObjectKey{Namespace: namespace, Name: t.AccessCredentialSecret}
		if err := k8s.Get(context.Background(), key, &sec); err != nil {
			return nil, fmt.Errorf("replication: read destination secret %s/%s: %w", namespace, t.AccessCredentialSecret, err)
		}
		access := string(sec.Data["accessKey"])
		secret := string(sec.Data["secretKey"])
		if access == "" || secret == "" {
			return nil, fmt.Errorf("replication: destination secret %s/%s missing accessKey/secretKey", namespace, t.AccessCredentialSecret)
		}
		endpoint, secure := splitMinioEndpoint(t.Endpoint)
		return minio.New(endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(access, secret, ""),
			Secure: secure,
		})
	}
	return replication.NewMinIODriver(replication.MinIODriverConfig{
		Source:             srcClient,
		DestClientFor:      destClientFor,
		ReplicationRoleARN: roleARN,
	})
}

// replicationSource is the gateway's source MinIO connection parameters
// the replication driver dials.
type replicationSource struct {
	endpoint  string
	accessKey string
	secretKey string
	useSSL    bool
}

// splitMinioEndpoint normalizes a §25.11 target endpoint — a bare
// host:port or a full URL — into the (host:port, secure) pair minio-go's
// constructor expects. An https:// scheme selects TLS and http:// (or a
// bare host:port) selects plaintext, so the operator controls the
// destination transport explicitly via the endpoint scheme.
func splitMinioEndpoint(endpoint string) (host string, secure bool) {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
		return u.Host, u.Scheme == "https"
	}
	return endpoint, false
}

// runReplicationController drives the §25.11 residency preflight loop:
// it configures replication on every enabled region at startup, then
// re-runs the residency preflight on the configured tick. The loop runs
// under the gateway's background context, co-located with the §12.5
// leader-elected MinIO maintenance sweeps. A preflight error is logged
// and does not stop the loop — a suspended region is re-checked each
// tick so a freshly-drifted region is caught (resume stays operator-
// driven). F-12.5.20.
func runReplicationController(ctx context.Context, ctrl *replication.Controller, logf func(string, ...any)) {
	if err := ctrl.Configure(ctx); err != nil {
		logf("lenny-gateway: §25.11 artifact replication initial configure: %v", err)
	}
	// §17.3 / §25.11: sample the replication-lag and failure gauges on the
	// same cadence as the residency tick so the lag/failed metrics the
	// residency preflight does not itself produce are emitted. F-17.3.7.
	if err := ctrl.MeasureAll(ctx); err != nil {
		logf("lenny-gateway: §25.11 artifact replication initial lag measure: %v", err)
	}
	tick := ctrl.ResidencyTickInterval()
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := ctrl.PreflightAll(ctx); err != nil {
				logf("lenny-gateway: §25.11 artifact replication residency preflight: %v", err)
			}
			if err := ctrl.MeasureAll(ctx); err != nil {
				logf("lenny-gateway: §25.11 artifact replication lag measure: %v", err)
			}
		}
	}
}
