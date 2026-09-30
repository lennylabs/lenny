// SPDX-License-Identifier: MIT

package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	credleasepg "github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// buildExecutorAndCredentials constructs the §17.4 executor (echo, subprocess,
// or pod), the §4.9 upstream-credential cache and lease store, and the §4.9
// credential-assignment Assigner (the §4.3 Token Service client over mTLS or
// the in-process Service), recording each on the accumulator.
//
// spec: §4.1 gateway subsystem seams; §17.4 executor; §4.9 credentials; §4.3
// Token Service.
func (w *gatewayWiring) buildExecutorAndCredentials() {
	f := w.f
	agentRuntime := f.agentRuntime
	runtimeBin := f.runtimeBin
	tokenServiceAddr := f.tokenServiceAddr
	tokenServiceCA := f.tokenServiceCA
	tokenServiceCert := f.tokenServiceCert
	tokenServiceKey := f.tokenServiceKey
	tokenServiceTenant := f.tokenServiceTenant

	// pgPool and the §4 KMS provider (recorded by buildTokenSigningStores)
	// are read back from the accumulator.
	pgPool := w.pgPool

	// ----- Session API + Executor -----
	// §17.4 local-dev runtime selection: LENNY_AGENT_RUNTIME=echo forces
	// the built-in echo executor (zero-credential mode), --runtime-bin /
	// LENNY_AGENT_BINARY dispatches to a child process speaking the
	// §28.5.3 adapter protocol, and the default is the in-process echo
	// executor. The --agent-namespace branch below replaces this with a
	// PodExecutor when the gateway places sessions on warm pods.
	// F-17.4.15.
	exec, execDesc, err := resolveExecutor(*runtimeBin, *agentRuntime)
	if err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}
	log.Printf("lenny-gateway: %s", execDesc)

	// ----- §4.9 credential-assignment service -----
	// credCache is the §4.9 upstream-credential cache. The §4.7 binder's
	// credential-assignment path populates it through the credassign
	// service below, and the §4.9 LLM reverse proxy reads it on every
	// upstream call. Both reference this one instance, so a lease the
	// binder assigns resolves on the proxy hot path.
	credCache := credcache.New()
	// llmLeases is the §4.9 credential-lease store: the credassign
	// service records each minted lease here, and the §4.9 LLM proxy
	// resolves an inbound lease token against it. Postgres-backed when
	// configured, otherwise the in-memory per-replica working set.
	var llmLeases credleasestore.LeaseStore = credleasestore.New()
	if pgPool != nil {
		// §12.9 classifies a credential lease as T4 — Restricted, so the
		// Postgres-backed store envelope-encrypts the lease body under
		// the platform "platform:credential-leases" KEK.
		pgLeases, lerr := credleasepg.New(pgPool, w.kmsProvider)
		if lerr != nil {
			log.Fatalf("lenny-gateway: construct credential-lease store: %v", lerr)
		}
		llmLeases = pgLeases
		// The Postgres backend carries the plain expires_at projection
		// (migration 0175), so startBillingAndSecurityWorkers runs the
		// one-time §4.9 backfill against it via an expiresAtBackfiller
		// type assertion. This guard keeps that selection wired if the
		// method signature ever drifts.
		var _ expiresAtBackfiller = pgLeases
	}
	// credAssign mints a session's §4.9 credential leases. It is one of
	// two implementations:
	//
	//   - The §4.3-compliant Client when --token-service-grpc-addr is
	//     set: the gateway calls lenny-token-service over mTLS and the
	//     Token Service is the only component with KMS decrypt rights;
	//     the gateway materializes nothing in-process. The Client
	//     mirrors each minted lease into llmLeases and the upstream
	//     credential into credCache so the §4.9 LLM proxy hot path is
	//     unchanged.
	//
	//   - The in-process Service when --token-service-grpc-addr is
	//     empty: dev mode and self-contained tests run without a
	//     separate Token Service process. The Service registers
	//     deployer-configured credential pools and mints leases
	//     locally.
	//
	// In both modes the §4.7 binder pushes the minted leases to a pod
	// via the adapter's AssignCredentials RPC and the §4.9 renewal
	// worker tracks them for proactive rotation.
	var (
		credAssign       credassign.Assigner
		inProcessAssign  *credassign.Service
		tokenServiceConn *grpc.ClientConn
		// §4.9 admin-time RBAC live-probe. Set only when the Token
		// Service link is present; the probe is Token-Service-owned and
		// has no meaning without that link.
		secretProber admin.SecretAccessProber
	)
	// §4.3 per-subsystem circuit breaker for Token Service
	// calls. A degraded Token Service trips this breaker open after
	// consecutive transient failures; the credassign client returns
	// ErrTokenServiceUnavailable so the session-start path can surface
	// the §4.3 retryable error.
	tokenServiceSubsystem := &subsystem.Subsystem{
		Name:    "token_service",
		Breaker: &subsystem.Breaker{},
	}
	if *tokenServiceAddr != "" {
		conn, err := dialTokenService(*tokenServiceAddr, *tokenServiceCert, *tokenServiceKey, *tokenServiceCA)
		if err != nil {
			log.Fatalf("lenny-gateway: dial Token Service %q: %v", *tokenServiceAddr, err)
		}
		tokenServiceConn = conn
		credAssign = credassign.NewClient(credassign.ClientOptions{
			Stub:      tokensv1.NewTokenServiceClient(conn),
			Leases:    llmLeases,
			Creds:     credCache,
			TenantID:  *tokenServiceTenant,
			Subsystem: tokenServiceSubsystem,
		})
		// §4.9: the admin credential-pool handlers probe Token
		// Service Secret-read access over this same mTLS link before
		// persisting a new secretRef.
		secretProber = &tokenServiceSecretProber{stub: tokensv1.NewTokenServiceClient(conn)}
		log.Printf("lenny-gateway: §4.3 credential materialization via lenny-token-service at %s", *tokenServiceAddr)
	} else {
		inProcessAssign = credassign.New(llmLeases, credCache)
		credAssign = inProcessAssign
	}

	// spec: §4.1 — record the §17.4 executor and the §4.9 credential
	// surfaces on the accumulator for the pod-lifecycle, messaging, and
	// later subsystem steps.
	w.exec = exec
	w.credCache = credCache
	w.llmLeases = llmLeases
	w.credAssign = credAssign
	w.inProcessAssign = inProcessAssign
	w.tokenServiceConn = tokenServiceConn
	w.secretProber = secretProber
	w.tokenServiceSubsystem = tokenServiceSubsystem
}

// dialTokenService dials lenny-token-service for the §4.3 credential
// materialization path. mTLS is required in production deployments —
// the gateway has a distinct client identity per replica per §4.3 —
// and certPath / keyPath / caPath name the project's mTLS material.
// With every TLS flag empty the dial falls through to plaintext for
// dev mode, which is the path the gateway-side bufconn tests exercise.
func dialTokenService(addr, certPath, keyPath, caPath string) (*grpc.ClientConn, error) {
	if addr == "" {
		return nil, fmt.Errorf("token service address is empty")
	}
	var transport grpc.DialOption
	switch {
	case certPath == "" && keyPath == "" && caPath == "":
		transport = grpc.WithTransportCredentials(insecure.NewCredentials())
	case certPath == "" || keyPath == "" || caPath == "":
		return nil, fmt.Errorf("token service mTLS requires --token-service-tls-cert, --token-service-tls-key, and --token-service-ca to all be set")
	default:
		// spec: §10.3 — present the gateway leaf via a
		// filesystem-watching GetClientCertificate callback so a
		// cert-manager renewal is picked up on the next dial without a
		// gateway restart.
		reloader, err := certreload.New(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load token-service client cert: %w", err)
		}
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read token-service CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("token-service CA bundle %q parsed no certificates", caPath)
		}
		transport = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			GetClientCertificate: reloader.GetClientCertificate,
			RootCAs:              pool,
			MinVersion:           tls.VersionTLS13,
		}))
	}
	return grpc.NewClient(addr, transport)
}
