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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
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
		loadctlURL     = flag.String("loadctl-url", "", "lenny-loadctl base URL for ack and registration callbacks")
		runnerID       = flag.String("runner-id", "", "stable runner identifier; defaults to host:pid when empty")
		capacity       = flag.Int("capacity", 1, "advertised concurrent-job capacity for the runner roster")
		cloudLabel     = flag.String("cloud-label", "", "cloud descriptor exposed on the runner roster (aws|gcp|azure|local)")
		registerHB     = flag.Duration("register-heartbeat", 30*time.Second, "how often to heartbeat to /api/v1/runners/{id}/heartbeat")
		k6Binary       = flag.String("k6", "k6", "path to the k6 binary; falls back to a noop runner when missing")
	)
	flag.Parse()
	runnerToken := os.Getenv("LENNY_LOADRUNNER_TOKEN")

	id := *runnerID
	if id == "" {
		host, _ := os.Hostname()
		id = fmt.Sprintf("%s-%d", host, os.Getpid())
	}

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

	client := authedClient(runnerToken)

	if *loadctlURL != "" {
		if err := registerRunner(ctx, client, *loadctlURL, id, *cloudLabel, *capacity); err != nil {
			log.Printf("register: %v (continuing; will retry on next heartbeat)", err)
		}
		go heartbeatLoop(ctx, client, *loadctlURL, id, *registerHB)
	}

	cfg := exec.Config{
		Runner:     &exec.K6Runner{Binary: *k6Binary},
		LoadctlURL: *loadctlURL,
		HTTPClient: client,
		HeartbeatFn: func(ctx context.Context, j *dispatch.Job) error {
			return d.Heartbeat(ctx, j)
		},
		HeartbeatInt: 30 * time.Second,
		ProgressFn:   makeProgressFn(client, *loadctlURL),
		ProgressInt:  time.Second,
	}
	return loop(ctx, d, cfg)
}

// authedClient wraps http.DefaultClient with a Transport that
// injects the configured Bearer token on every request. When the
// token is empty the function returns http.DefaultClient unchanged,
// preserving the dev / unauthenticated flow.
func authedClient(token string) *http.Client {
	if token == "" {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &bearerTransport{token: token, base: http.DefaultTransport},
	}
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid mutating the caller's headers.
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req)
}

// registerRunner posts the runner's identity to loadctl. Idempotent
// on (id); the server upserts the roster entry.
func registerRunner(ctx context.Context, client *http.Client, loadctlURL, id, cloud string, capacity int) error {
	body, err := json.Marshal(map[string]any{
		"id":       id,
		"cloud":    cloud,
		"capacity": capacity,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", loadctlURL+"/api/v1/runners/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("register: status=%d", resp.StatusCode)
	}
	log.Printf("registered runner %s at %s", id, loadctlURL)
	return nil
}

// heartbeatLoop posts a heartbeat every interval until ctx cancels.
// A 404 from the heartbeat (loadctl restarted and lost the roster)
// triggers a re-registration on the next tick.
func heartbeatLoop(ctx context.Context, client *http.Client, loadctlURL, id string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		req, _ := http.NewRequestWithContext(ctx, "POST", loadctlURL+"/api/v1/runners/"+id+"/heartbeat", nil)
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("heartbeat: %v", err)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			_ = registerRunner(ctx, client, loadctlURL, id, "", 0)
		} else if resp.StatusCode >= 400 {
			log.Printf("heartbeat: status=%d", resp.StatusCode)
		}
	}
}

// makeProgressFn returns a ProgressFn that POSTs Progress to the
// loadctl `/api/v1/progress` endpoint. Returns nil when loadctlURL
// is empty so the unit-test path (no callback wired) keeps working.
func makeProgressFn(client *http.Client, loadctlURL string) exec.ProgressFn {
	if loadctlURL == "" {
		return nil
	}
	return func(ctx context.Context, j *dispatch.Job, p exec.Progress) error {
		body, err := json.Marshal(map[string]any{
			"run_id":          j.RunID,
			"scenario":        j.Scenario,
			"elapsed_seconds": p.ElapsedSeconds,
			"iterations":      p.Iterations,
			"metrics":         p.Metrics,
		})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "POST", loadctlURL+"/api/v1/progress", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("progress: status=%d", resp.StatusCode)
		}
		return nil
	}
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
