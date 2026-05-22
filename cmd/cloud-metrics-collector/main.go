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
		listen          = flag.String("listen", ":9100", "HTTP listen address for /metrics")
		provider        = flag.String("provider", "aws", "cloud provider: aws|gcp|azure")
		region          = flag.String("region", "us-west-2", "cloud region")
		interval        = flag.Duration("interval", 30*time.Second, "polling interval")
		rdsInstance     = flag.String("aws-rds-instance", "", "RDS instance identifier")
		cacheCluster    = flag.String("aws-elasticache-cluster", "", "ElastiCache cluster id")
		loadBalancer    = flag.String("aws-alb", "", "ALB name")
		autoScalingASG  = flag.String("aws-asg", "", "Node ASG name")
	)
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigs; cancel() }()

	collector, err := buildCollector(ctx, *provider, *region, *interval, *rdsInstance, *cacheCluster, *loadBalancer, *autoScalingASG)
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

func buildCollector(ctx context.Context, provider, region string, interval time.Duration, rdsID, cacheID, alb, asg string) (*cloudmetrics.Collector, error) {
	switch provider {
	case "aws":
		poller, err := cloudmetrics.NewAWSPoller(ctx, region, rdsID, cacheID, alb, asg)
		if err != nil {
			return nil, err
		}
		return cloudmetrics.NewCollector(interval, poller), nil
	case "gcp", "azure":
		// Wave 6 cut: AWS is the canonical poller. GCP (Cloud
		// Monitoring) and Azure (Azure Monitor) pollers mirror the
		// AWS structure; they land behind the cloud-specific build
		// tag in the same wave that wires the dispatcher SDKs.
		return cloudmetrics.NewCollector(interval), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want aws|gcp|azure)", provider)
	}
}
