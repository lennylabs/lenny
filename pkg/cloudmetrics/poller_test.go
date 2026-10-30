// SPDX-License-Identifier: MIT

package cloudmetrics

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

type fakeCW struct {
	calls []*cloudwatch.GetMetricDataInput
}

func (f *fakeCW) GetMetricData(ctx context.Context, in *cloudwatch.GetMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	f.calls = append(f.calls, in)
	results := []types.MetricDataResult{}
	for _, q := range in.MetricDataQueries {
		results = append(results, types.MetricDataResult{
			Id:     q.Id,
			Values: []float64{42.0},
		})
	}
	return &cloudwatch.GetMetricDataOutput{MetricDataResults: results}, nil
}

func TestAWSPollerTickEmitsSamples(t *testing.T) {
	f := &fakeCW{}
	p := NewAWSPollerWithClient(f, "us-west-2", "rds-1", "ec-1", "alb-1", "asg-1")
	samples, err := p.Tick(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// 2 RDS + 2 ElastiCache + 2 ALB + 1 ASG = 7 metrics.
	if len(samples) != 7 {
		t.Errorf("samples=%d want 7", len(samples))
	}
}

func TestAWSPollerSkipsWhenAllEmpty(t *testing.T) {
	f := &fakeCW{}
	p := NewAWSPollerWithClient(f, "us-west-2", "", "", "", "")
	samples, err := p.Tick(context.Background(), time.Now())
	if err != nil {
		t.Errorf("Tick: %v", err)
	}
	if len(samples) != 0 {
		t.Errorf("expected zero samples when no resources are configured; got %d", len(samples))
	}
	if len(f.calls) != 0 {
		t.Errorf("expected zero GetMetricData calls; got %d", len(f.calls))
	}
}

func TestCollectorRenders(t *testing.T) {
	c := NewCollector(time.Minute, fakePoller{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.poll(ctx, nil)
	body := c.Render()
	if !strings.Contains(body, "lenny_cloud_test_metric") {
		t.Errorf("rendered output missing metric: %s", body)
	}
	if !strings.Contains(body, `region="us-west-2"`) {
		t.Errorf("rendered labels missing: %s", body)
	}
}

type fakePoller struct{}

func (fakePoller) Provider() string { return "test" }
func (fakePoller) Tick(_ context.Context, _ time.Time) ([]Sample, error) {
	return []Sample{{
		Name:   "lenny_cloud_test_metric",
		Help:   "test metric",
		Labels: map[string]string{"region": "us-west-2"},
		Value:  1.23,
	}}, nil
}

// silence unused warning
var _ = aws.String
