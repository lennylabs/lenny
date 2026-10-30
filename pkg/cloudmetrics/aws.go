// SPDX-License-Identifier: MIT

package cloudmetrics

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// CloudWatchAPI is the subset of the CloudWatch client we use.
// Exported so tests can plug a mock.
type CloudWatchAPI interface {
	GetMetricData(ctx context.Context, params *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
}

// AWSPoller polls CloudWatch for tier-12 metrics: RDS CPU and
// connections, ElastiCache CPU and evictions, ALB request counts,
// EKS node CPU.
type AWSPoller struct {
	client         CloudWatchAPI
	region         string
	rdsInstanceID  string
	cacheClusterID string
	loadBalancer   string
	nodeAutoScaler string
}

// NewAWSPoller constructs an AWSPoller. The required identifiers
// (RDS instance, ElastiCache cluster, ALB name, ASG name) are
// passed through from up-loadgen.sh / up-loadctl.sh terraform
// outputs.
func NewAWSPoller(ctx context.Context, region, rdsID, cacheID, alb, asg string) (*AWSPoller, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("cloudmetrics.aws: load config: %w", err)
	}
	return &AWSPoller{
		client:         cloudwatch.NewFromConfig(cfg),
		region:         region,
		rdsInstanceID:  rdsID,
		cacheClusterID: cacheID,
		loadBalancer:   alb,
		nodeAutoScaler: asg,
	}, nil
}

// NewAWSPollerWithClient lets tests inject a CloudWatch mock.
func NewAWSPollerWithClient(client CloudWatchAPI, region, rdsID, cacheID, alb, asg string) *AWSPoller {
	return &AWSPoller{
		client:         client,
		region:         region,
		rdsInstanceID:  rdsID,
		cacheClusterID: cacheID,
		loadBalancer:   alb,
		nodeAutoScaler: asg,
	}
}

func (p *AWSPoller) Provider() string { return "aws" }

// Tick polls each metric and returns the latest sample per metric.
func (p *AWSPoller) Tick(ctx context.Context, now time.Time) ([]Sample, error) {
	end := now
	start := now.Add(-5 * time.Minute)
	queries := []types.MetricDataQuery{}
	type binding struct {
		id     string
		name   string
		help   string
		labels map[string]string
	}
	bindings := []binding{}

	add := func(id, namespace, metric, stat string, dims []types.Dimension, outName, help string, labels map[string]string) {
		queries = append(queries, types.MetricDataQuery{
			Id: aws.String(id),
			MetricStat: &types.MetricStat{
				Metric: &types.Metric{
					Namespace:  aws.String(namespace),
					MetricName: aws.String(metric),
					Dimensions: dims,
				},
				Period: aws.Int32(60),
				Stat:   aws.String(stat),
			},
			ReturnData: aws.Bool(true),
		})
		bindings = append(bindings, binding{id: id, name: outName, help: help, labels: labels})
	}

	if p.rdsInstanceID != "" {
		dims := []types.Dimension{{Name: aws.String("DBInstanceIdentifier"), Value: aws.String(p.rdsInstanceID)}}
		add("rds_cpu", "AWS/RDS", "CPUUtilization", "Average", dims,
			"lenny_cloud_rds_cpu_percent", "RDS CPU utilization (percent).",
			map[string]string{"instance": p.rdsInstanceID, "region": p.region})
		add("rds_conn", "AWS/RDS", "DatabaseConnections", "Average", dims,
			"lenny_cloud_rds_connections", "RDS active connections.",
			map[string]string{"instance": p.rdsInstanceID, "region": p.region})
	}
	if p.cacheClusterID != "" {
		dims := []types.Dimension{{Name: aws.String("CacheClusterId"), Value: aws.String(p.cacheClusterID)}}
		add("ec_cpu", "AWS/ElastiCache", "EngineCPUUtilization", "Average", dims,
			"lenny_cloud_elasticache_cpu_percent", "ElastiCache engine CPU.",
			map[string]string{"cluster": p.cacheClusterID, "region": p.region})
		add("ec_evict", "AWS/ElastiCache", "Evictions", "Sum", dims,
			"lenny_cloud_elasticache_evictions_total", "ElastiCache evictions in window.",
			map[string]string{"cluster": p.cacheClusterID, "region": p.region})
	}
	if p.loadBalancer != "" {
		dims := []types.Dimension{{Name: aws.String("LoadBalancer"), Value: aws.String(p.loadBalancer)}}
		add("alb_req", "AWS/ApplicationELB", "RequestCount", "Sum", dims,
			"lenny_cloud_alb_request_count", "ALB request count in window.",
			map[string]string{"alb": p.loadBalancer, "region": p.region})
		add("alb_lat", "AWS/ApplicationELB", "TargetResponseTime", "Average", dims,
			"lenny_cloud_alb_target_latency_seconds", "ALB target response time.",
			map[string]string{"alb": p.loadBalancer, "region": p.region})
	}
	if p.nodeAutoScaler != "" {
		dims := []types.Dimension{{Name: aws.String("AutoScalingGroupName"), Value: aws.String(p.nodeAutoScaler)}}
		add("asg_cpu", "AWS/EC2", "CPUUtilization", "Average", dims,
			"lenny_cloud_node_cpu_percent", "Node CPU utilization (percent).",
			map[string]string{"asg": p.nodeAutoScaler, "region": p.region})
	}
	if len(queries) == 0 {
		return nil, nil
	}
	out, err := p.client.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		StartTime:         aws.Time(start),
		EndTime:           aws.Time(end),
		MetricDataQueries: queries,
	})
	if err != nil {
		return nil, err
	}
	byID := map[string]float64{}
	for _, r := range out.MetricDataResults {
		if r.Id == nil || len(r.Values) == 0 {
			continue
		}
		byID[*r.Id] = r.Values[0]
	}
	samples := []Sample{}
	for _, b := range bindings {
		v, ok := byID[b.id]
		if !ok {
			continue
		}
		samples = append(samples, Sample{Name: b.name, Help: b.help, Labels: b.labels, Value: v})
	}
	return samples, nil
}
