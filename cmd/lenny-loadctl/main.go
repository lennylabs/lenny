// SPDX-License-Identifier: MIT

// lenny-loadctl is the tier-12 control plane. It exposes the HTTP API
// that AI agents, CI pipelines, and operators drive load runs
// through; persists run state; dispatches scenarios to the
// lenny-loadrunner pool; ingests metrics; renders the per-run HTML
// report; and serves the small operator UI.
//
// TESTING.md §12.12 and §24.1 (Wave 6 control plane).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/loadctl"
	"github.com/lennylabs/lenny/pkg/loadrunner/dispatch"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("lenny-loadctl: %v", err)
	}
}

func run() error {
	var (
		listenAddr     = flag.String("listen", ":8080", "HTTP listen address")
		storageURL     = flag.String("storage-url", "s3://lenny-load-reports", "object storage URL for reports")
		databaseURL    = flag.String("database-url", "memory://", "persistence backing (memory:// or postgres://...)")
		progressDir    = flag.String("progress-dir", "", "persistent progress sink: empty disables, file://path or absolute path writes one JSONL per run")
		runDuration    = flag.Duration("run-duration", 0, "per-scenario duration stamped onto every Job (zero selects the 60s default)")
		dispatcherKind = flag.String("dispatcher", "scaffold", "dispatcher kind: scaffold|aws|gcp|azure (scaffold runs the simulated-state-machine dev path with no real runner)")
		queueURL       = flag.String("queue-url", "", "queue identifier for non-scaffold dispatchers (SQS URL / Pub/Sub topic / Service Bus queue path)")
		region         = flag.String("region", "", "cloud region (required when dispatcher is aws|gcp|azure)")
		rlRunsPerMin   = flag.Int("ratelimit-runs-per-min", 0, "POST /api/v1/runs per-source rate cap (0 disables)")
		rlProgressPerS = flag.Int("ratelimit-progress-per-sec", 0, "POST /api/v1/progress per-source rate cap (0 disables)")
		rlAckPerS      = flag.Int("ratelimit-ack-per-sec", 0, "POST /api/v1/ack per-source rate cap (0 disables)")
	)
	flag.Parse()

	operatorTokens := splitTokens(os.Getenv("LENNY_LOADCTL_OPERATOR_TOKENS"))
	runnerTokens := splitTokens(os.Getenv("LENNY_LOADCTL_RUNNER_TOKENS"))

	submitter, err := newSubmitter(*dispatcherKind, *queueURL, *region)
	if err != nil {
		return fmt.Errorf("submitter: %w", err)
	}

	server, err := loadctl.NewServer(loadctl.Config{
		StorageURL:  *storageURL,
		DatabaseURL: *databaseURL,
		ProgressDir: *progressDir,
		RunDuration: *runDuration,
		Submitter:   submitter,
		Auth: loadctl.AuthConfig{
			OperatorTokens: operatorTokens,
			RunnerTokens:   runnerTokens,
		},
		RateLimit: loadctl.RateLimitConfig{
			RunCreatePerMinute: *rlRunsPerMin,
			ProgressPerSecond:  *rlProgressPerS,
			AckPerSecond:       *rlAckPerS,
		},
	})
	if err != nil {
		if submitter != nil {
			_ = submitter.Close()
		}
		return err
	}
	defer server.Close()

	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigs
		log.Printf("lenny-loadctl: shutting down")
		shutdownCtx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		// Stop the HTTP listener first (existing requests drain), then
		// signal the Server's background goroutines and wait for them.
		_ = srv.Shutdown(shutdownCtx)
		_ = server.Shutdown(shutdownCtx)
		cancel()
	}()

	log.Printf("lenny-loadctl: listening on %s (dispatcher=%s)", *listenAddr, *dispatcherKind)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	<-ctx.Done()
	return nil
}

// splitTokens parses a comma-separated env-var value into a slice of
// non-empty tokens. Whitespace around tokens is trimmed.
func splitTokens(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// newSubmitter constructs the dispatch.Submitter the loadctl Server
// publishes jobs through. The "scaffold" kind returns nil, which the
// Server interprets as the simulated-state-machine path (see
// loadctl.Config.Submitter).
func newSubmitter(kind, queueURL, region string) (dispatch.Submitter, error) {
	switch kind {
	case "scaffold":
		return nil, nil
	case "aws", "gcp", "azure":
		if queueURL == "" {
			return nil, fmt.Errorf("-queue-url is required for dispatcher=%s", kind)
		}
		return dispatch.NewSubmitter(context.Background(), dispatch.CloudConfig{
			Provider: kind,
			QueueURL: queueURL,
			Region:   region,
		})
	default:
		return nil, fmt.Errorf("unknown dispatcher %q (want scaffold|aws|gcp|azure)", kind)
	}
}
