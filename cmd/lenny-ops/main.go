// SPDX-License-Identifier: MIT

// Command lenny-ops runs the §25 operability service: a Deployment
// separate from the gateway that hosts the operability endpoints
// reading durable state (Postgres, Redis, the Kubernetes API,
// Prometheus). §25 makes lenny-ops mandatory in every Lenny
// installation; it is reachable only from outside the cluster via an
// Ingress, never from internal cluster workloads.
//
// Usage:
//
//	lenny-ops --addr :8080
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

func main() {
	addr := flag.String("addr", ":8080", "address the lenny-ops HTTP server binds to")
	flag.Parse()

	// The §25 dependency probes and the §25.7 runbook index source are
	// registered once their backing clients are wired; until then the
	// readiness report carries no dependency entries and the runbook
	// endpoint reports the index unavailable.
	srv := &http.Server{
		Addr:              *addr,
		Handler:           opsserver.New(opsserver.Options{}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("lenny-ops: serving the operability API on %s", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("lenny-ops: serve: %v", err)
	}
}
