// SPDX-License-Identifier: MIT

// Command lenny-token-service is the §13.3 Token Service. It serves
// POST /v1/oauth/token (RFC 8693) against an in-memory issued-tokens
// store. It signs with the §4 KMS-envelope-backed signer: the HMAC-
// SHA256 signing key is sealed under a KMS key-encryption-key rather
// than being a plaintext per-process secret. The in-process kms.Local
// provider is the no-cloud development KEK; a cloud deployment swaps
// in an AWS/GCP/Azure provider behind the same kms.Provider interface.
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

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

func main() {
	addr := flag.String("addr", ":8081", "address to bind (host:port)")
	issuer := flag.String("issuer", "https://lenny.dev.local/token", "iss claim stamped on issued tokens")
	flag.Parse()

	// §4 KMS provider. The in-process kms.Local provider is the
	// no-cloud development KEK; the signing key it wraps is sealed
	// under a KMS KEK, so no plaintext signing key is persisted.
	kmsProvider, err := kms.NewLocalRandom()
	if err != nil {
		log.Fatalf("lenny-token-service: kms provider: %v", err)
	}
	signer, err := jwt.NewKMSSigner(context.Background(), kmsProvider, jwt.TokenServiceKEKAlias, "dev-1")
	if err != nil {
		log.Fatalf("lenny-token-service: kms-backed signer: %v", err)
	}

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
