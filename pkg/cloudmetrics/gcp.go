// SPDX-License-Identifier: MIT

package cloudmetrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GCPMetricClient is the subset of *monitoring.MetricClient the
// poller uses. Exported so tests can inject a mock.
type GCPMetricClient interface {
	ListTimeSeries(ctx context.Context, req *monitoringpb.ListTimeSeriesRequest) GCPTimeSeriesIterator
	Close() error
}

// GCPTimeSeriesIterator abstracts the iterator returned by
// ListTimeSeries.
type GCPTimeSeriesIterator interface {
	Next() (*monitoringpb.TimeSeries, error)
}

// GCPPoller polls Cloud Monitoring for tier-12 metrics. Mirrors the
// AWS poller shape: database CPU/connections, cache CPU/evictions,
// load-balancer request count + latency, node CPU.
type GCPPoller struct {
	client      GCPMetricClient
	projectID   string
	region      string
	sqlInstance string
	cacheName   string
	lbName      string
	migName     string
}

// NewGCPPoller constructs a poller backed by a real Cloud Monitoring
// client.
func NewGCPPoller(ctx context.Context, projectID, region, sqlInstance, cacheName, lbName, migName string) (*GCPPoller, error) {
	raw, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloudmetrics.gcp: NewMetricClient: %w", err)
	}
	return &GCPPoller{
		client:      &gcpClientAdapter{raw: raw},
		projectID:   projectID,
		region:      region,
		sqlInstance: sqlInstance,
		cacheName:   cacheName,
		lbName:      lbName,
		migName:     migName,
	}, nil
}

// NewGCPPollerWithClient lets tests inject a mock client.
func NewGCPPollerWithClient(client GCPMetricClient, projectID, region, sqlInstance, cacheName, lbName, migName string) *GCPPoller {
	return &GCPPoller{
		client: client, projectID: projectID, region: region,
		sqlInstance: sqlInstance, cacheName: cacheName, lbName: lbName, migName: migName,
	}
}

func (p *GCPPoller) Provider() string { return "gcp" }

// Tick polls each configured metric and returns the latest sample.
func (p *GCPPoller) Tick(ctx context.Context, now time.Time) ([]Sample, error) {
	type query struct {
		filter string
		name   string
		help   string
		labels map[string]string
	}
	queries := []query{}
	if p.sqlInstance != "" {
		queries = append(queries,
			query{
				filter: fmt.Sprintf(`metric.type="cloudsql.googleapis.com/database/cpu/utilization" resource.labels.database_id="%s:%s"`, p.projectID, p.sqlInstance),
				name:   "lenny_cloud_cloudsql_cpu_ratio",
				help:   "Cloud SQL CPU utilization (0..1).",
				labels: map[string]string{"instance": p.sqlInstance, "project": p.projectID},
			},
			query{
				filter: fmt.Sprintf(`metric.type="cloudsql.googleapis.com/database/postgresql/num_backends" resource.labels.database_id="%s:%s"`, p.projectID, p.sqlInstance),
				name:   "lenny_cloud_cloudsql_connections",
				help:   "Cloud SQL active backends.",
				labels: map[string]string{"instance": p.sqlInstance, "project": p.projectID},
			},
		)
	}
	if p.cacheName != "" {
		queries = append(queries,
			query{
				filter: fmt.Sprintf(`metric.type="redis.googleapis.com/stats/cpu_utilization" resource.labels.instance_id="%s"`, p.cacheName),
				name:   "lenny_cloud_memorystore_cpu_ratio",
				help:   "Memorystore Redis CPU utilization (0..1).",
				labels: map[string]string{"cluster": p.cacheName, "project": p.projectID},
			},
			query{
				filter: fmt.Sprintf(`metric.type="redis.googleapis.com/stats/evicted_keys" resource.labels.instance_id="%s"`, p.cacheName),
				name:   "lenny_cloud_memorystore_evictions_total",
				help:   "Memorystore Redis evicted keys in window.",
				labels: map[string]string{"cluster": p.cacheName, "project": p.projectID},
			},
		)
	}
	if p.lbName != "" {
		queries = append(queries,
			query{
				filter: fmt.Sprintf(`metric.type="loadbalancing.googleapis.com/https/request_count" resource.labels.url_map_name="%s"`, p.lbName),
				name:   "lenny_cloud_lb_request_count",
				help:   "Cloud Load Balancer request count in window.",
				labels: map[string]string{"lb": p.lbName, "project": p.projectID},
			},
			query{
				filter: fmt.Sprintf(`metric.type="loadbalancing.googleapis.com/https/total_latencies" resource.labels.url_map_name="%s"`, p.lbName),
				name:   "lenny_cloud_lb_target_latency_seconds",
				help:   "Cloud Load Balancer total latency.",
				labels: map[string]string{"lb": p.lbName, "project": p.projectID},
			},
		)
	}
	if p.migName != "" {
		queries = append(queries, query{
			filter: fmt.Sprintf(`metric.type="compute.googleapis.com/instance/cpu/utilization" metadata.user_labels."mig"="%s"`, p.migName),
			name:   "lenny_cloud_node_cpu_ratio",
			help:   "GCE node CPU utilization (0..1).",
			labels: map[string]string{"mig": p.migName, "project": p.projectID},
		})
	}

	samples := []Sample{}
	for _, q := range queries {
		it := p.client.ListTimeSeries(ctx, &monitoringpb.ListTimeSeriesRequest{
			Name:   "projects/" + p.projectID,
			Filter: q.filter,
			Interval: &monitoringpb.TimeInterval{
				StartTime: timestamppb.New(now.Add(-5 * time.Minute)),
				EndTime:   timestamppb.New(now),
			},
			View: monitoringpb.ListTimeSeriesRequest_FULL,
		})
		ts, err := it.Next()
		if err == iterator.Done || ts == nil {
			continue
		}
		if err != nil {
			continue
		}
		if len(ts.Points) == 0 {
			continue
		}
		v := pointValue(ts.Points[0])
		samples = append(samples, Sample{Name: q.name, Help: q.help, Labels: q.labels, Value: v})
	}
	return samples, nil
}

// Close releases the metric client.
func (p *GCPPoller) Close() error {
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

// pointValue extracts a numeric value from a Cloud Monitoring Point
// regardless of its Value union type.
func pointValue(pt *monitoringpb.Point) float64 {
	if pt == nil || pt.Value == nil {
		return 0
	}
	switch v := pt.Value.Value.(type) {
	case *monitoringpb.TypedValue_DoubleValue:
		return v.DoubleValue
	case *monitoringpb.TypedValue_Int64Value:
		return float64(v.Int64Value)
	case *monitoringpb.TypedValue_DistributionValue:
		if v.DistributionValue != nil && v.DistributionValue.Count > 0 {
			return v.DistributionValue.Mean
		}
	}
	return 0
}

// --- adapter around the real client --------------------------------

type gcpClientAdapter struct{ raw *monitoring.MetricClient }

func (a *gcpClientAdapter) ListTimeSeries(ctx context.Context, req *monitoringpb.ListTimeSeriesRequest) GCPTimeSeriesIterator {
	return &gcpIteratorAdapter{it: a.raw.ListTimeSeries(ctx, req)}
}
func (a *gcpClientAdapter) Close() error { return a.raw.Close() }

type gcpIteratorAdapter struct{ it *monitoring.TimeSeriesIterator }

func (a *gcpIteratorAdapter) Next() (*monitoringpb.TimeSeries, error) { return a.it.Next() }

// silence the unused alias linter
var _ = strings.Contains
