// SPDX-License-Identifier: MIT

// Command lenny-gateway is the minimal Lenny gateway binary. It
// serves the §15.1 REST session endpoints from
// pkg/gateway/sessionserver against the in-memory session store
// from pkg/gateway/sessionstore/memstore. No auth, no Postgres, no
// Kubernetes — the tenant is taken from the dev X-Lenny-Tenant-ID
// header (default "default" per §10.2 single-tenant mode).
//
// The intent is to give the tier-3 / tier-4 test harness something
// concrete to run against, even before the Postgres-backed gateway
// ships. As Phase 4+ implementation lands, the in-memory backend is
// swapped for the real Postgres / Redis / Kubernetes wiring without
// changing the handler surface.
//
// Usage:
//
//	lenny-gateway --addr :8080
//
// The binary exits 0 on graceful SIGTERM, non-zero on bind failure.
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

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

func main() {
	addr := flag.String("addr", ":8080", "address to bind (host:port)")
	shutdownTimeout := flag.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")
	flag.Parse()

	store := memstore.New()
	server := sessionserver.New(store, sessionserver.Options{})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-gateway: listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("lenny-gateway: listen: %v", err)
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()

	<-stopCh
	log.Printf("lenny-gateway: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
