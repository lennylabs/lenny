// SPDX-License-Identifier: MIT

package cloudmetrics

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
)

// AzureMetricsClient is the subset of azquery.MetricsClient used.
type AzureMetricsClient interface {
	QueryResource(ctx context.Context, resourceURI string, options *azquery.MetricsClientQueryResourceOptions) (azquery.MetricsClientQueryResourceResponse, error)
}

// AzurePoller polls Azure Monitor for tier-12 metrics. Mirrors the
// AWS poller shape: Flexible Server CPU/connections, Cache CPU/
// evictions, Azure Load Balancer request count + latency, VMSS node
// CPU.
type AzurePoller struct {
	client AzureMetricsClient
	region string

	pgServerResourceID   string
	cacheResourceID      string
	lbResourceID         string
	vmssResourceID       string
}

// NewAzurePoller constructs a poller against the default Azure
// credential and the Monitor metrics endpoint.
func NewAzurePoller(ctx context.Context, region, pgServerID, cacheID, lbID, vmssID string) (*AzurePoller, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("cloudmetrics.azure: NewDefaultAzureCredential: %w", err)
	}
	client, err := azquery.NewMetricsClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("cloudmetrics.azure: NewMetricsClient: %w", err)
	}
	return &AzurePoller{
		client: client, region: region,
		pgServerResourceID: pgServerID,
		cacheResourceID:    cacheID,
		lbResourceID:       lbID,
		vmssResourceID:     vmssID,
	}, nil
}

// NewAzurePollerWithClient lets tests inject a mock client.
func NewAzurePollerWithClient(client AzureMetricsClient, region, pgServerID, cacheID, lbID, vmssID string) *AzurePoller {
	return &AzurePoller{
		client: client, region: region,
		pgServerResourceID: pgServerID,
		cacheResourceID:    cacheID,
		lbResourceID:       lbID,
		vmssResourceID:     vmssID,
	}
}

func (p *AzurePoller) Provider() string { return "azure" }

// Tick polls each configured resource and emits one sample per
// metric.
func (p *AzurePoller) Tick(ctx context.Context, now time.Time) ([]Sample, error) {
	type query struct {
		resource string
		metric   string
		name     string
		help     string
		labels   map[string]string
	}
	queries := []query{}
	if p.pgServerResourceID != "" {
		queries = append(queries,
			query{p.pgServerResourceID, "cpu_percent", "lenny_cloud_flexible_server_cpu_percent",
				"Flexible Server CPU utilization (percent).", map[string]string{"resource": p.pgServerResourceID}},
			query{p.pgServerResourceID, "active_connections", "lenny_cloud_flexible_server_connections",
				"Flexible Server active connections.", map[string]string{"resource": p.pgServerResourceID}},
		)
	}
	if p.cacheResourceID != "" {
		queries = append(queries,
			query{p.cacheResourceID, "percentProcessorTime", "lenny_cloud_cache_cpu_percent",
				"Azure Cache for Redis CPU (percent).", map[string]string{"resource": p.cacheResourceID}},
			query{p.cacheResourceID, "evictedkeys", "lenny_cloud_cache_evictions_total",
				"Azure Cache for Redis evicted keys in window.", map[string]string{"resource": p.cacheResourceID}},
		)
	}
	if p.lbResourceID != "" {
		queries = append(queries,
			query{p.lbResourceID, "PacketCount", "lenny_cloud_lb_request_count",
				"Azure Load Balancer packet count in window.", map[string]string{"resource": p.lbResourceID}},
			query{p.lbResourceID, "ALBHealthState", "lenny_cloud_lb_health",
				"Azure Load Balancer health state.", map[string]string{"resource": p.lbResourceID}},
		)
	}
	if p.vmssResourceID != "" {
		queries = append(queries, query{
			p.vmssResourceID, "Percentage CPU", "lenny_cloud_node_cpu_percent",
			"VMSS node CPU utilization (percent).", map[string]string{"resource": p.vmssResourceID},
		})
	}

	samples := []Sample{}
	for _, q := range queries {
		resp, err := p.client.QueryResource(ctx, q.resource, &azquery.MetricsClientQueryResourceOptions{
			MetricNames: to.Ptr(q.metric),
			Interval:    to.Ptr("PT1M"),
			Timespan:    to.Ptr(azquery.NewTimeInterval(now.Add(-5*time.Minute), now)),
		})
		if err != nil {
			continue
		}
		v, ok := latestAzureValue(resp)
		if !ok {
			continue
		}
		samples = append(samples, Sample{Name: q.name, Help: q.help, Labels: q.labels, Value: v})
	}
	return samples, nil
}

func latestAzureValue(resp azquery.MetricsClientQueryResourceResponse) (float64, bool) {
	for _, m := range resp.Value {
		if m == nil {
			continue
		}
		for _, ts := range m.TimeSeries {
			if ts == nil {
				continue
			}
			for i := len(ts.Data) - 1; i >= 0; i-- {
				d := ts.Data[i]
				if d == nil {
					continue
				}
				if d.Average != nil {
					return *d.Average, true
				}
				if d.Total != nil {
					return *d.Total, true
				}
				if d.Maximum != nil {
					return *d.Maximum, true
				}
				if d.Count != nil {
					return *d.Count, true
				}
			}
		}
	}
	return 0, false
}
