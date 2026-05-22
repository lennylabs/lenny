// SPDX-License-Identifier: MIT

// lenny-loadrunner is the long-running agent that runs on each
// off-cluster runner instance in the tier-12 load-runner pool. It
// registers with the tier-12 control plane, pulls jobs from a
// per-cloud work queue, executes k6 in a subprocess, streams the
// k6 output metrics back to the control plane in real time, and
// uploads the full k6 JSON report to object storage on completion.
//
// Wave 5 cut: the binary parses the documented flag surface,
// resolves the dispatcher, and runs the main receive→execute→ack
// loop against the in-memory dispatcher. The k6 subprocess
// invocation and the cloud streaming wiring land in Wave 6 alongside
// the cloud SDKs.
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
		controlPlane   = flag.String("control-plane-url", "", "lenny-loadctl base URL for heartbeats and metric streaming")
		runnerID       = flag.String("runner-id", "", "runner identity (defaults to hostname)")
		k6Binary       = flag.String("k6", "k6", "path to the k6 binary")
		_              = controlPlane // wired in Wave 6
		_              = runnerID     // wired in Wave 6
		_              = k6Binary     // wired in Wave 6
	)
	flag.Parse()

	d, err := newDispatcher(*dispatcherKind, *queueURL, *region, *visibility)
	if err != nil {
		return fmt.Errorf("dispatcher: %w", err)
	}
	defer d.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Install signal handler so SIGTERM / SIGINT cleanly cancel an
	// in-flight job rather than killing it abruptly.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sigs
		log.Printf("lenny-loadrunner: received %s; shutting down", s)
		cancel()
	}()

	return loop(ctx, d)
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
func loop(ctx context.Context, d dispatch.Dispatcher) error {
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

		execErr := execute(ctx, job)
		if execErr != nil {
			log.Printf("execute %s: %v", job.RunID, execErr)
			if err := d.Nack(ctx, job, execErr.Error()); err != nil {
				log.Printf("Nack: %v", err)
			}
			continue
		}
		if err := d.Ack(ctx, job); err != nil {
			log.Printf("Ack: %v", err)
		}
	}
}

// execute runs a single Job. Wave 5 cut: logs the receipt and
// returns nil. Wave 6 wires the k6 subprocess, the metrics streaming
// channel, and the object-storage upload of the report.
func execute(ctx context.Context, j *dispatch.Job) error {
	log.Printf("(stub) would invoke k6 --vus=%d --duration=%s against %s", j.VUs, j.Duration, j.TargetURL)
	return nil
}
