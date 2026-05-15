// SPDX-License-Identifier: MIT

// Command lenny-gateway is the minimal Lenny gateway binary. It
// serves:
//
//   - §15.1 REST session endpoints (POST/GET/list/derive/upload/...).
//   - §15.1 admin endpoints (tenant + runtime CRUD) gated on
//     platform-admin.
//   - §15.1 GET /v1/blobs/{ref} blob dereference.
//
// The handler stack wraps every request with:
//
//   - §10.2 auth middleware — Bearer JWT or dev-mode header
//     fallback, configurable via LENNY_DEV_MODE.
//   - §11.6 circuit-breaker admission middleware.
//   - §11.5 idempotency replay cache middleware.
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
	"crypto/rand"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

func main() {
	addr := flag.String("addr", ":8080", "address to bind (host:port)")
	multiTenant := flag.Bool("multi-tenant", false, "enable §10.2 multi-tenant claim extraction")
	devMode := flag.Bool("dev-mode", envFlag("LENNY_DEV_MODE"),
		"enable dev-mode auth shortcuts (X-Lenny-Roles dev-header). Override via LENNY_DEV_MODE.")
	shutdownTimeout := flag.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")
	flag.Parse()

	// ----- Stores -----
	sessions := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()

	// ----- §7.1 uploadToken KeyRing -----
	// One ephemeral signing key per process. Production deployers
	// rotate this key on the §7.1 24-hour schedule via the KMS-backed
	// implementation; the minimal gateway uses a random key per boot.
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		log.Fatalf("lenny-gateway: rand: %v", err)
	}
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "boot", Secret: seed[:]})
	uploadIssuer := uploadtoken.NewIssuer(ring, nil)
	uploadTracker := uploadtoken.NewMemoryTracker()
	uploadVerifier := uploadtoken.NewVerifier(ring, uploadTracker, nil)

	// ----- Session API -----
	sessionSrv := sessionserver.New(sessions, sessionserver.Options{
		UploadTokenIssuer:   uploadIssuer,
		UploadTokenVerifier: uploadVerifier,
		Blobs:               blobs,
	})

	// ----- Admin API -----
	adminRouter := admin.NewRouter(tenants, admin.Options{}).
		WithRuntimes(runtimes)

	// ----- Compose the mux -----
	mux := http.NewServeMux()
	mux.Handle("/v1/sessions", sessionSrv.Handler())
	mux.Handle("/v1/sessions/", sessionSrv.Handler())
	mux.Handle("/v1/blobs/", sessionSrv.Handler())
	mux.Handle("/v1/admin/", adminRouter.Handler())

	// ----- Healthz (unauthenticated) -----
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// ----- Middleware stack -----
	var handler http.Handler = mux

	// Idempotency innermost (after auth + circuit; needs the
	// authenticated tenant on the request to scope keys correctly).
	idemStore := idemmw.NewMemoryStore()
	handler = idemmw.Wrap(handler, idemStore, idemmw.Options{})

	// Circuit breaker next: rejects requests when any open breaker
	// matches. Empty registry = no breakers configured = pass-through.
	cbRegistry := cbmw.NewMemoryRegistry()
	handler = cbmw.Wrap(handler, cbRegistry, cbmw.Options{})

	// Auth outermost. AllowDevRoles is only honoured when the dev
	// flag is set (LENNY_DEV_MODE=true or --dev-mode); production
	// deployments leave it off so X-Lenny-Roles cannot self-grant
	// platform-admin.
	authOpts := authmw.Options{
		MultiTenant:     *multiTenant,
		AllowDevHeaders: true,
		AllowDevRoles:   *devMode,
	}
	if !*multiTenant {
		// Even in single-tenant mode, dev-header callers carry the
		// tenant header. Flip to multi-tenant with a permissive
		// registry so the header round-trips.
		authOpts.MultiTenant = true
	}
	authOpts.Registry = permissiveRegistry{}
	handler = authmw.Wrap(handler, authOpts)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-gateway: listening on %s (dev_mode=%v multi_tenant=%v)",
			*addr, *devMode, *multiTenant)
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

// permissiveRegistry accepts every tenant. The minimal gateway uses
// this so dev-header transports can name an arbitrary tenant during
// integration tests without operator pre-provisioning. Production
// swaps in a Postgres-backed Registry (e.g., the in-memory
// tenantstore.Memory which also satisfies auth.TenantRegistry).
type permissiveRegistry struct{}

func (permissiveRegistry) IsRegistered(string) (bool, error) { return true, nil }

// envFlag returns true when the env var name is set to a truthy
// value (1, true, yes — case-insensitive). Used to default the
// --dev-mode flag from LENNY_DEV_MODE.
func envFlag(name string) bool {
	v := os.Getenv(name)
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
