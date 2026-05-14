// SPDX-License-Identifier: MIT

// Command lenny-gateway is the minimal Lenny gateway binary. It
// serves the §15.1 REST session endpoints from
// pkg/gateway/sessionserver wrapped in the gateway middleware stack:
//
//   - auth        — §10.2 Bearer JWT + dev-header fallback
//   - idempotency — §11.5 Idempotency-Key replay cache
//   - circuit     — §11.6 admission gate
//
// Backed by in-memory stores. The tier-3 contract suites and the
// tier-4 integration tests drive the same binary; production swaps
// the in-memory backends for Postgres / Redis / Kubernetes wiring
// behind the same interfaces.
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
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// permissiveRegistry accepts every tenant. The minimal gateway uses
// this so dev-header transports can name an arbitrary tenant during
// integration tests without operator pre-provisioning. Production
// swaps in a Postgres-backed Registry.
type permissiveRegistry struct{}

func (permissiveRegistry) IsRegistered(string) (bool, error) { return true, nil }

var _ auth.TenantRegistry = permissiveRegistry{}

func main() {
	addr := flag.String("addr", ":8080", "address to bind (host:port)")
	multiTenant := flag.Bool("multi-tenant", false, "enable §10.2 multi-tenant claim extraction")
	shutdownTimeout := flag.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")
	flag.Parse()

	store := memstore.New()
	server := sessionserver.New(store, sessionserver.Options{})

	// Wrap in reverse order so the outermost middleware (auth) runs
	// first on every request.
	var handler http.Handler = server.Handler()

	// Idempotency innermost (after auth + circuit; needs the
	// authenticated tenant on the request to scope keys correctly).
	idemStore := idemmw.NewMemoryStore()
	handler = idemmw.Wrap(handler, idemStore, idemmw.Options{})

	// Circuit breaker next: rejects requests when any open breaker
	// matches. Empty registry = no breakers configured = pass-through.
	cbRegistry := cbmw.NewMemoryRegistry()
	handler = cbmw.Wrap(handler, cbRegistry, cbmw.Options{})

	// Auth outermost: validates Bearer or dev headers, attaches
	// Principal to context, sets X-Lenny-Tenant-ID for downstream
	// handlers. The minimal gateway defaults to multi-tenant mode
	// with a permissive Registry so dev-header callers can name an
	// arbitrary tenant during contract / integration tests without
	// pre-registering it. Production swaps in a real Registry.
	authOpts := authmw.Options{
		MultiTenant:     *multiTenant,
		AllowDevHeaders: true,
	}
	if *multiTenant {
		authOpts.Registry = permissiveRegistry{}
	} else {
		// Even in single-tenant mode, AllowDevHeaders should not
		// silently force every request to "default" — flip to
		// multi-tenant with a permissive registry so the
		// X-Lenny-Tenant-ID header round-trips.
		authOpts.MultiTenant = true
		authOpts.Registry = permissiveRegistry{}
	}
	handler = authmw.Wrap(handler, authOpts)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-gateway: listening on %s", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("lenny-gateway: listen: %v", err)
		}
	}()

	<-stopCh
	log.Printf("lenny-gateway: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
