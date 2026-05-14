// SPDX-License-Identifier: MIT

// Command lenny-token-service is the minimal §13.3 Token Service. It
// serves POST /v1/oauth/token (RFC 8693) against an in-memory issued-
// tokens store using the dev HMAC signer from pkg/auth/jwt. Provides
// a concrete target for the tier-3 contract tests until the
// Postgres-backed Token Service with KMS signing lands.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

func main() {
	addr := flag.String("addr", ":8081", "address to bind (host:port)")
	issuer := flag.String("issuer", "https://lenny.dev.local/token", "iss claim stamped on issued tokens")
	flag.Parse()

	// Random dev-mode secret per process. Production wires KMS.
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		log.Fatalf("lenny-token-service: rand: %v", err)
	}
	signer := jwt.NewHMACSigner("dev-1", secret[:])

	srv := tokenservice.NewServer(tokenservice.Options{
		Signer: signer,
		Issuer: *issuer,
		PerDialectCap: map[string]time.Duration{
			"lenny-gateway": 24 * time.Hour,
			"lenny-ops":     1 * time.Hour,
			"llm-proxy":     1 * time.Hour,
		},
	})

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-token-service: listening on %s", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
