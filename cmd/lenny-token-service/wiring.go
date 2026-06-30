// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	tokencache "github.com/lennylabs/lenny/pkg/tokenservice/cache"
	"github.com/lennylabs/lenny/pkg/tokenservice/promemit"
)

// runTokenService wires and starts every Token Service subsystem from the
// parsed flags, then serves the §13.3 HTTP token-exchange surface, the §4.3
// gRPC TokenService surface, and the §16.1 metrics surface until shutdown.
// It is the Token Service composition root: a flat ordered sequence of
// per-subsystem build-step calls (each defined in a subsystem-named sibling
// file and documented there), terminating in the signal-driven graceful
// shutdown. No subsystem is constructed inline here; every build step records
// its outputs on the tokenServiceWiring accumulator.
//
// This is the ordered call sequence proposal 0020 §4 Part A R11 specifies, the
// same archetype as R1 (cmd/lenny-gateway) and R4 (cmd/lenny-ops) at smaller
// scale. The deferred Postgres-pool and Redis-client closes the former
// monolithic main ran on return stay deferred here so they run at process
// shutdown in the same order.
//
// spec: §4.1 — the gateway and its sibling binaries are each one component
// whose composition root constructs each subsystem in dependency order;
// §13.3 — the Token Service serves the HTTP exchange surface plus the §4.3
// gRPC credential-assignment surface.
func runTokenService(f *tokenServiceFlags) {
	w := &tokenServiceWiring{f: f}

	// §4 / §17.5 KMS provider and the §4-KMS-envelope-backed, circuit-broken
	// JWT signer.
	w.buildKMSAndSigner()

	// §16.1 metric vectors and the §13.3 durable write-before-issue
	// IssuedTokenStore + Auditor (Postgres-backed or the dev in-memory chain).
	w.buildMetrics()
	w.buildStores()
	if w.pgPool != nil {
		defer w.pgPool.Close()
	}

	// §13.3 NTP drift self-monitor, then the §13.3 token-exchange Server.
	w.buildDriftMonitor()
	w.buildServer()

	// §4.0 EventEmitter (local buffer plus optional §25.5 Redis stream) and
	// the §4.3 Redis-backed encrypted access-token cache.
	w.buildEventEmitterAndCache()
	if w.redisCleanup != nil {
		defer w.redisCleanup()
	}

	// §13.3 HTTP, §16.1 metrics, and §4.3 / §12.2.4 gRPC listeners.
	w.buildHTTPSurface()
	w.buildMetricsSurface()
	w.buildGRPCSurface()

	// Serve every surface until SIGTERM/SIGINT, then drain.
	w.runServer()
}

// tokenServiceWiring is the §13.3 Token Service composition-root accumulator.
// It carries the parsed flags plus the subsystem components each build step
// constructs, so the per-subsystem build steps hand their outputs to the steps
// that wire them. runTokenService is the composition root: it parses its
// inputs once (via parseFlags, in main) and then runs an ordered sequence of
// per-subsystem build-step calls. The build steps live in subsystem-named
// sibling files under cmd/lenny-token-service/ (proposal 0020 §4 Part A R11,
// the same archetype as R1 and R4 at smaller scale):
//
//   - kms.go: the §4 / §17.5 KMS provider and the §4-KMS-envelope-backed,
//     circuit-broken JWT signer — buildKMSAndSigner.
//   - stores.go: the §16.1 metrics emitter and the §13.3 write-before-issue
//     IssuedTokenStore + Auditor (Postgres-backed or the dev in-memory chain)
//     — buildMetrics and buildStores.
//   - server.go: the §13.3 NTP drift self-monitor, the §13.3 token-exchange
//     Server, and the §4.0 EventEmitter plus the §4.3 access-token cache —
//     buildDriftMonitor, buildServer, and buildEventEmitterAndCache.
//   - surfaces.go: the §13.3 HTTP, §16.1 metrics, and §4.3 / §12.2.4 gRPC
//     listeners, and the signal-driven graceful shutdown — buildHTTPSurface,
//     buildMetricsSurface, buildGRPCSurface, and runServer.
//
// Decomposition mechanism (mirrors the gatewayWiring and opsWiring accumulator
// decision in cmd/lenny-gateway/wiring.go and cmd/lenny-ops/wiring.go): the
// per-subsystem build steps use this shared accumulator rather than a
// (component, error) return per subsystem, because the composition root threads
// many cross-step locals between subsystems (the KMS provider feeds the signer
// and the access cache, the metrics emitter feeds the server, the gRPC surface,
// and the drift monitor, and the auditor feeds the server and the gRPC
// surface), so a constructor-per-subsystem would carry a large return surface
// the caller re-threads by hand.
//
// Error handling stays as the inline log.Fatalf the original composition root
// used: a Token Service replica that cannot construct a subsystem at startup
// must abort the process rather than return an error to a caller with no
// recovery path. The deferred Postgres-pool close and Redis-client close stay
// deferred in runTokenService so they run at process shutdown rather than when
// the build step returns.
//
// spec: §4.1 — the gateway and its sibling binaries are each one component
// whose composition root threads its inputs and constructed subsystems through
// the per-subsystem builders in dependency order; §13.3 — the Token Service
// serves the HTTP exchange surface plus the §4.3 gRPC credential-assignment
// surface.
type tokenServiceWiring struct {
	f *tokenServiceFlags

	// §4 / §17.5 KMS provider and the §4-envelope-backed signer.
	kmsProvider kms.Provider
	signer      *jwt.BreakerSigner

	// §16.1 metrics and the §13.3 write-before-issue state.
	metricsEmitter *promemit.Emitter
	pgPool         *pgxpool.Pool
	issuedTokens   tokenservice.IssuedTokenStore
	auditor        tokenservice.Auditor

	// §13.3 drift monitor and the token-exchange Server.
	driftMonitor *driftmonitor.Monitor
	srv          *tokenservice.Server

	// §4.0 EventEmitter, §25.5 stream buffer, and the §4.3 access-token cache.
	replicaID    string
	opsEmitter   events.EventEmitter
	accessCache  *tokencache.Cache
	redisCleanup func()

	// §13.3 HTTP, §16.1 metrics, and §4.3 gRPC listeners.
	httpSrv    *http.Server
	metricsSrv *http.Server
	grpcSrv    *grpc.Server
}

// ctx is the process root context the build steps share. The Token Service has
// no signal-bounded root context (it serves until SIGTERM/SIGINT via the
// dedicated stop channel in runServer), so the build steps use a plain
// background context for the KMS and drift-monitor startup paths, matching the
// former inline main.
var ctx = context.Background()

// fatalf aborts the process with the lenny-token-service prefix the former
// inline composition root used, keeping the startup failure log lines
// byte-for-byte identical after the decomposition.
func fatalf(format string, args ...any) {
	log.Fatalf("lenny-token-service: "+format, args...)
}
