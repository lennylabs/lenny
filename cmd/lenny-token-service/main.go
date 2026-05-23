// SPDX-License-Identifier: MIT

// Command lenny-token-service is the §13.3 Token Service. It serves
// two surfaces:
//
//   - HTTP `POST /v1/oauth/token` (RFC 8693 token exchange) for the
//     external dialect-issuing path.
//   - gRPC `lenny.tokenservice.v1.TokenService` for the §4.3 / §12.2.4
//     credential-assignment trust boundary the gateway calls over mTLS
//     to materialize, rotate, and revoke credential leases.
//
// Both surfaces sign with the §4 KMS-envelope-backed signer: the HMAC-
// SHA256 signing key is sealed under a KMS key-encryption-key rather
// than being a plaintext per-process secret. The in-process kms.Local
// provider is the no-cloud development KEK; a cloud deployment swaps
// in an AWS/GCP/Azure provider behind the same kms.Provider interface.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/kms"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

func main() {
	addr := flag.String("addr", ":8081", "address to bind for the HTTP token-exchange surface (host:port)")
	grpcAddr := flag.String("grpc-addr", "",
		"address to bind for the §4.3 gRPC TokenService surface (host:port). Empty disables the gRPC listener.")
	issuer := flag.String("issuer", "https://lenny.dev.local/token", "iss claim stamped on issued tokens")
	redisURL := flag.String("redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis connection URL for the §25.5 operational event stream. When set, the Token "+
			"Service's EventEmitter writes to ops:events:stream alongside the gateway and the "+
			"controllers; when empty, events stay in the local in-memory buffer. Override via "+
			"LENNY_REDIS_URL.")
	redisPassword := flag.String("redis-password", os.Getenv("LENNY_REDIS_PASSWORD"),
		"Redis AUTH password. Override via LENNY_REDIS_PASSWORD.")
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

	// §4.0 EventEmitter wiring. The §4.0 spec requires every process
	// hosting subsystems that may emit §16.6 operational events to
	// construct an EventEmitter so a future emit site does not have to
	// re-thread the dependency through the binary. The Token Service
	// signs and mints credential tokens (§4.3) and rotates leases for
	// §4.9; once the rotation events are wired they will land on this
	// emitter without further main.go changes. With --redis-url the
	// emitter streams to the §25.5 platform-scoped Redis stream alongside
	// the gateway and the controllers; without Redis the emitter writes
	// only to the process-local in-memory buffer (the §25.5 per-replica
	// fall-back). The buffer is constructed unconditionally so a Redis
	// outage degrades to local-only delivery rather than dropping events.
	replicaID := os.Getenv("HOSTNAME")
	if replicaID == "" {
		replicaID = "token-service"
	}
	opsEventBuffer := events.NewEventBuffer(0)
	var opsEmitter events.EventEmitter = events.NewEmitter(opsEventBuffer, replicaID)
	if *redisURL != "" {
		redisClient, err := redisconn.NewClient(redisconn.Config{URL: *redisURL, Password: *redisPassword})
		if err != nil {
			log.Fatalf("lenny-token-service: redis client: %v", err)
		}
		defer func() { _ = redisClient.Close() }()
		opsEmitter = events.NewStreamEmitter(events.StreamEmitterOptions{
			Client:    redisClient,
			Buffer:    opsEventBuffer,
			Source:    "//lenny.dev/token-service/" + replicaID,
			ReplicaID: replicaID,
		})
		log.Printf("lenny-token-service: §25.5 operational events streaming to Redis %s", events.DefaultStreamKey)
	}
	// Keep opsEmitter live so the linker retains the wiring even before a
	// subsystem in this binary takes it as a constructor dependency. A
	// future credential-rotation event emit will replace this no-op log.
	log.Printf("lenny-token-service: §4.0 EventEmitter ready (replica=%s, redis=%t)",
		replicaID, *redisURL != "")
	_ = opsEmitter

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// §4.3 / §12.2.4 gRPC TokenService surface. The credential-
	// assignment service is the same in-process credassign.Service
	// pool-selection + lease-minting logic the gateway runs today;
	// the binary makes it reachable over gRPC so the gateway can
	// switch its call site from the in-process MintLease to a gRPC
	// client without re-implementing the lease semantics. Pool
	// registration runs through the same RegisterPool entry point;
	// no pools are registered at startup so AssignCredentials fails
	// fast until an operator configures pools. The in-memory lease
	// store and credential cache are appropriate for the development
	// path; a production deployment swaps in Postgres-backed
	// `credleasestore/pgstore` and a shared credential cache.
	leases := credleasestore.New()
	cache := credcache.New()
	assignSvc := credassign.New(leases, cache)
	tsGRPC := tokenservice.NewGRPCServer(assignSvc, leases)

	var grpcSrv *grpc.Server
	if *grpcAddr != "" {
		grpcSrv = grpc.NewServer()
		tokensv1.RegisterTokenServiceServer(grpcSrv, tsGRPC)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-token-service: HTTP token-exchange listening on %s", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen http: %v", err)
		}
	}()
	if grpcSrv != nil {
		lis, err := net.Listen("tcp", *grpcAddr)
		if err != nil {
			log.Fatalf("listen grpc %s: %v", *grpcAddr, err)
		}
		go func() {
			log.Printf("lenny-token-service: gRPC TokenService listening on %s", *grpcAddr)
			if err := grpcSrv.Serve(lis); err != nil {
				log.Fatalf("serve grpc: %v", err)
			}
		}()
	}
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	if grpcSrv != nil {
		grpcSrv.GracefulStop()
	}
}
