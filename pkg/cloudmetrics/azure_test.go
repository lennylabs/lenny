// SPDX-License-Identifier: MIT

package cloudmetrics

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
)

type fakeAzureClient struct {
	calls []string
}

func (f *fakeAzureClient) QueryResource(ctx context.Context, resourceURI string, options *azquery.MetricsClientQueryResourceOptions) (azquery.MetricsClientQueryResourceResponse, error) {
	f.calls = append(f.calls, resourceURI)
	return azquery.MetricsClientQueryResourceResponse{
		Response: azquery.Response{
			Value: []*azquery.Metric{{
				TimeSeries: []*azquery.TimeSeriesElement{{
					Data: []*azquery.MetricValue{{Average: to.Ptr(50.0)}},
				}},
			}},
		},
	}, nil
}

func TestAzurePollerEmitsConfiguredSamples(t *testing.T) {
	c := &fakeAzureClient{}
	p := NewAzurePollerWithClient(c, "eastus", "/sub/x/pg", "/sub/x/cache", "/sub/x/lb", "/sub/x/vmss")
	samples, err := p.Tick(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// 2 Flexible Server + 2 Cache + 2 LB + 1 VMSS = 7
	if len(samples) != 7 {
		t.Errorf("samples=%d want 7", len(samples))
	}
	if samples[0].Value != 50.0 {
		t.Errorf("sample value=%v want 50.0", samples[0].Value)
	}
}

func TestAzurePollerSkipsWhenAllEmpty(t *testing.T) {
	c := &fakeAzureClient{}
	p := NewAzurePollerWithClient(c, "eastus", "", "", "", "")
	samples, _ := p.Tick(context.Background(), time.Now())
	if len(samples) != 0 {
		t.Errorf("samples=%d want 0", len(samples))
	}
	if len(c.calls) != 0 {
		t.Errorf("calls=%d want 0", len(c.calls))
	}
}
