// SPDX-License-Identifier: MIT

// lenny-loadrunner is the long-running agent that runs on each
// off-cluster runner instance in the tier-12 load-runner pool. It
// registers with the tier-12 control plane, pulls jobs from a
// per-cloud work queue, executes k6 against the supplied target,
// posts the per-scenario ack back to loadctl, and uploads the k6
// JSON to object storage.
//
// TESTING.md §12.12 (Wave 5 lenny-loadrunner binary).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/loadrunner/dispatch"
	"github.com/lennylabs/lenny/pkg/loadrunner/exec"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("lenny-loadrunner: %v", err)
	}
}

func run() error {
	var (
		dispatcherKind = flag.String("dispatcher", "inmem", "dispatcher: inmem|aws|gcp|azure")
		queueURL       = flag.String("queue-url", "", "queue identifier (SQS URL / Pub/Sub subscription / Service Bus path)")
		region         = flag.String("region", "", "cloud region (for non-inmem dispatchers)")
		visibility     = flag.Duration("visibility-timeout", 5*time.Minute, "visibility timeout for in-flight jobs (inmem only)")
		loadctlURL     = flag.String("loadctl-url", "", "lenny-loadctl base URL for the ack callback")
		k6Binary       = flag.String("k6", "k6", "path to the k6 binary; falls back to a noop runner when missing")
	)
	flag.Parse()

	d, err := newDispatcher(*dispatcherKind, *queueURL, *region, *visibility)
	if err != nil {
		return fmt.Errorf("dispatcher: %w", err)
	}
	defer d.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sigs
		log.Printf("lenny-loadrunner: received %s; shutting down", s)
		cancel()
	}()

	cfg := exec.Config{
		Runner:     exec.K6Runner{Binary: *k6Binary},
		LoadctlURL: *loadctlURL,
		HeartbeatFn: func(ctx context.Context, j *dispatch.Job) error {
			return d.Heartbeat(ctx, j)
		},
		HeartbeatInt: 30 * time.Second,
	}
	return loop(ctx, d, cfg)
}

func newDispatcher(kind, queueURL, region string, vis time.Duration) (dispatch.Dispatcher, error) {
	switch kind {
	case "inmem":
		return dispatch.NewInMem(vis), nil
	case "aws", "gcp", "azure":
		return dispatch.New(context.Background(), dispatch.CloudConfig{
			Provider: kind,
			QueueURL: queueURL,
			Region:   region,
		})
	default:
		return nil, fmt.Errorf("unknown dispatcher %q (want inmem|aws|gcp|azure)", kind)
	}
}

// loop is the runner main loop: receive → execute → ack.
func loop(ctx context.Context, d dispatch.Dispatcher, cfg exec.Config) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		recvCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		job, err := d.Receive(recvCtx)
		cancel()
		if err != nil {
			if errors.Is(err, dispatch.ErrNoJob) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			if errors.Is(err, context.Canceled) {
				return nil
			}
			log.Printf("Receive: %v", err)
			time.Sleep(time.Second)
			continue
		}
		log.Printf("received job: run=%s scenario=%s vus=%d duration=%s", job.RunID, job.Scenario, job.VUs, job.Duration)

		summary, execErr := exec.Execute(ctx, cfg, job)
		if execErr != nil {
			log.Printf("execute %s: %v", job.RunID, execErr)
			if err := d.Nack(ctx, job, execErr.Error()); err != nil {
				log.Printf("Nack: %v", err)
			}
			continue
		}
		_ = summary
		if err := d.Ack(ctx, job); err != nil {
			log.Printf("Ack: %v", err)
		}
	}
}
