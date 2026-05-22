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
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/loadctl"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("lenny-loadctl: %v", err)
	}
}

func run() error {
	var (
		listenAddr  = flag.String("listen", ":8080", "HTTP listen address")
		storageURL  = flag.String("storage-url", "s3://lenny-load-reports", "object storage URL for reports")
		databaseURL = flag.String("database-url", "memory://", "persistence backing (memory:// or postgres://...)")
	)
	flag.Parse()

	server, err := loadctl.NewServer(loadctl.Config{
		StorageURL:  *storageURL,
		DatabaseURL: *databaseURL,
	})
	if err != nil {
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
		shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
		cancel()
	}()

	log.Printf("lenny-loadctl: listening on %s", *listenAddr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	<-ctx.Done()
	return nil
}
