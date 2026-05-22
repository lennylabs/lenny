// SPDX-License-Identifier: MIT

package cloudmetrics

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
)

type fakeGCPClient struct {
	queries []*monitoringpb.ListTimeSeriesRequest
}

type fakeGCPIterator struct {
	served bool
}

func (f *fakeGCPClient) ListTimeSeries(_ context.Context, req *monitoringpb.ListTimeSeriesRequest) GCPTimeSeriesIterator {
	f.queries = append(f.queries, req)
	return &fakeGCPIterator{}
}
func (f *fakeGCPClient) Close() error { return nil }

func (it *fakeGCPIterator) Next() (*monitoringpb.TimeSeries, error) {
	if it.served {
		return nil, iterator.Done
	}
	it.served = true
	return &monitoringpb.TimeSeries{
		Points: []*monitoringpb.Point{{
			Value: &monitoringpb.TypedValue{
				Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.42},
			},
		}},
	}, nil
}

func TestGCPPollerEmitsConfiguredSamples(t *testing.T) {
	c := &fakeGCPClient{}
	p := NewGCPPollerWithClient(c, "acme", "us-central1", "sql-1", "redis-1", "lb-1", "mig-1")
	samples, err := p.Tick(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// 2 CloudSQL + 2 Memorystore + 2 LB + 1 MIG = 7
	if len(samples) != 7 {
		t.Errorf("samples=%d want 7", len(samples))
	}
	if samples[0].Value != 0.42 {
		t.Errorf("sample value=%v want 0.42", samples[0].Value)
	}
}

func TestGCPPollerSkipsWhenAllEmpty(t *testing.T) {
	c := &fakeGCPClient{}
	p := NewGCPPollerWithClient(c, "acme", "us-central1", "", "", "", "")
	samples, _ := p.Tick(context.Background(), time.Now())
	if len(samples) != 0 {
		t.Errorf("samples=%d want 0", len(samples))
	}
	if len(c.queries) != 0 {
		t.Errorf("queries=%d want 0", len(c.queries))
	}
}
