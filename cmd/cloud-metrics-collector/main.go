// SPDX-License-Identifier: MIT

// cloud-metrics-collector polls the active cloud provider's metrics
// API and exposes the results as Prometheus-format metrics that the
// tier-12 load-run Prometheus scrapes.
//
// TESTING.md §12.12 and §24.1.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/cloudmetrics"
)

func main() {
	var (
		listen   = flag.String("listen", ":9100", "HTTP listen address for /metrics")
		provider = flag.String("provider", "aws", "cloud provider: aws|gcp|azure")
		region   = flag.String("region", "us-west-2", "cloud region")
		interval = flag.Duration("interval", 30*time.Second, "polling interval")

		// AWS resources
		awsRDSInstance    = flag.String("aws-rds-instance", "", "RDS instance identifier")
		awsCacheCluster   = flag.String("aws-elasticache-cluster", "", "ElastiCache cluster id")
		awsLoadBalancer   = flag.String("aws-alb", "", "ALB name")
		awsAutoScalingASG = flag.String("aws-asg", "", "Node ASG name")

		// GCP resources
		gcpProjectID   = flag.String("gcp-project", "", "GCP project ID")
		gcpSQLInstance = flag.String("gcp-sql-instance", "", "Cloud SQL instance name")
		gcpCacheName   = flag.String("gcp-memorystore-instance", "", "Memorystore instance id")
		gcpLBName      = flag.String("gcp-loadbalancer", "", "Cloud Load Balancer URL map name")
		gcpMIGName     = flag.String("gcp-mig", "", "GCE managed instance group name")

		// Azure resource IDs (full ARM IDs)
		azPGServerID = flag.String("azure-pg-server-id", "", "Flexible Server resource ID")
		azCacheID    = flag.String("azure-cache-id", "", "Azure Cache for Redis resource ID")
		azLBID       = flag.String("azure-lb-id", "", "Azure Load Balancer resource ID")
		azVMSSID     = flag.String("azure-vmss-id", "", "VMSS resource ID")
	)
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigs; cancel() }()

	collector, err := buildCollector(ctx, *provider, *region, *interval, builderArgs{
		awsRDSInstance: *awsRDSInstance, awsCacheCluster: *awsCacheCluster,
		awsLoadBalancer: *awsLoadBalancer, awsAutoScalingASG: *awsAutoScalingASG,
		gcpProjectID: *gcpProjectID, gcpSQLInstance: *gcpSQLInstance,
		gcpCacheName: *gcpCacheName, gcpLBName: *gcpLBName, gcpMIGName: *gcpMIGName,
		azPGServerID: *azPGServerID, azCacheID: *azCacheID,
		azLBID: *azLBID, azVMSSID: *azVMSSID,
	})
	if err != nil {
		log.Fatalf("cloud-metrics-collector: %v", err)
	}

	go collector.Run(ctx, log.Printf)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprint(w, collector.Render())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("cloud-metrics-collector: provider=%s region=%s interval=%s listen=%s", *provider, *region, *interval, *listen)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("cloud-metrics-collector: %v", err)
	}
}

type builderArgs struct {
	awsRDSInstance, awsCacheCluster, awsLoadBalancer, awsAutoScalingASG string
	gcpProjectID, gcpSQLInstance, gcpCacheName, gcpLBName, gcpMIGName   string
	azPGServerID, azCacheID, azLBID, azVMSSID                           string
}

func buildCollector(ctx context.Context, provider, region string, interval time.Duration, a builderArgs) (*cloudmetrics.Collector, error) {
	switch provider {
	case "aws":
		poller, err := cloudmetrics.NewAWSPoller(ctx, region, a.awsRDSInstance, a.awsCacheCluster, a.awsLoadBalancer, a.awsAutoScalingASG)
		if err != nil {
			return nil, err
		}
		return cloudmetrics.NewCollector(interval, poller), nil
	case "gcp":
		if a.gcpProjectID == "" {
			return nil, fmt.Errorf("gcp: -gcp-project is required")
		}
		poller, err := cloudmetrics.NewGCPPoller(ctx, a.gcpProjectID, region, a.gcpSQLInstance, a.gcpCacheName, a.gcpLBName, a.gcpMIGName)
		if err != nil {
			return nil, err
		}
		return cloudmetrics.NewCollector(interval, poller), nil
	case "azure":
		poller, err := cloudmetrics.NewAzurePoller(ctx, region, a.azPGServerID, a.azCacheID, a.azLBID, a.azVMSSID)
		if err != nil {
			return nil, err
		}
		return cloudmetrics.NewCollector(interval, poller), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want aws|gcp|azure)", provider)
	}
}
