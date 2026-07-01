// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// tokenServiceFlags carries every lenny-token-service command-line flag as the
// pointer the flag package populates at parse time, plus the §4/§17.5 KMS
// provider options and the finalize hook providerflags.Bind returns. The §4.1
// composition root parses its inputs once into this value and threads them to
// each subsystem builder, so a build step reads a flag through the embedded
// *tokenServiceFlags rather than re-deriving it. The fields keep the names the
// former inline composition root used, so the moved construct-and-wire blocks
// read unchanged.
//
// spec: §4.1 — the gateway and its sibling binaries are each one component
// whose composition root parses inputs once and threads them to every
// subsystem builder; §13.3 names the Token Service flag surface.
type tokenServiceFlags struct {
	addr            *string
	grpcAddr        *string
	metricsAddr     *string
	issuer          *string
	postgresDSN     *string
	redisURL        *string
	redisPassword   *string
	rlCallerPerSec  *int
	rlCallerPerMin  *int
	rlTenantPerSec  *int
	rlSampleWindow  *time.Duration
	tlsCert         *string
	tlsKey          *string
	tlsCA           *string
	secretNamespace *string

	// §4/§17.5 KMS provider selector. kmsOpts is the resolved option set and
	// kmsFinalize is the post-parse validation hook providerflags.Bind returns;
	// runTokenService calls it before any wiring.
	kmsOpts     *providerflags.Options
	kmsFinalize func() error
}

// parseFlags defines every Token Service flag on flag.CommandLine, parses the
// command line, and returns the populated tokenServiceFlags. It performs no
// wiring; the caller hands the value to runTokenService. The flag definitions
// are grouped into per-domain register helpers so each stays a navigable block
// rather than one flat list, mirroring the gateway and lenny-ops composition
// roots (proposal 0020 §4 Part A R11).
//
// spec: §13.3 — the Token Service flag surface; §4.1 — the composition root
// parses its inputs once and threads them to each subsystem builder.
func parseFlags() *tokenServiceFlags {
	f := &tokenServiceFlags{}
	f.registerListenerFlags()
	f.registerIssuerAndStateFlags()
	f.registerRateLimitFlags()
	f.registerTLSFlags()
	f.registerKMSFlags()
	flag.Parse()
	return f
}

// registerListenerFlags registers the §13.3 HTTP, §4.3 gRPC, and §16.1 metrics
// listener address flags. spec: §13.3, §4.3, §16.1.
func (f *tokenServiceFlags) registerListenerFlags() {
	f.addr = flag.String("addr", ":8081", "address to bind for the HTTP token-exchange surface (host:port)")
	f.grpcAddr = flag.String("grpc-addr", "",
		"address to bind for the §4.3 gRPC TokenService surface (host:port). Empty disables the gRPC listener.")
	f.metricsAddr = flag.String("metrics-addr", "",
		"address to bind for the §16.1 Prometheus metrics surface (host:port). Empty disables the metrics listener. The chart renders :9090 to match tokenService.metricsPort.")
}

// registerIssuerAndStateFlags registers the issuer claim, the §13.3
// write-before-issue Postgres DSN, the §25.5 Redis event-stream URL, and the
// §4.9 RBAC live-probe secret namespace. spec: §13.3, §25.5, §4.9.
func (f *tokenServiceFlags) registerIssuerAndStateFlags() {
	f.issuer = flag.String("issuer", "https://lenny.dev.local/token",
		"iss claim stamped on issued tokens. Override via LENNY_TOKEN_ISSUER.")
	if envIssuer := os.Getenv("LENNY_TOKEN_ISSUER"); envIssuer != "" {
		*f.issuer = envIssuer
	}
	f.postgresDSN = flag.String("postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, the §13.3 write-before-issue path persists issued-token metadata to the issued_tokens table and writes `token.exchanged` audit rows to audit_log under the §11.7 per-tenant advisory lock. When empty, the Token Service runs without durable issued-token or audit state (dev mode).")
	f.redisURL = flag.String("redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis connection URL for the §25.5 operational event stream. When set, the Token "+
			"Service's EventEmitter writes to ops:events:stream alongside the gateway and the "+
			"controllers; when empty, events stay in the local in-memory buffer. Override via "+
			"LENNY_REDIS_URL.")
	f.redisPassword = flag.String("redis-password", os.Getenv("LENNY_REDIS_PASSWORD"),
		"Redis AUTH password. Override via LENNY_REDIS_PASSWORD.")
	// §4.9 line 1212 admin-time RBAC live-probe namespace. The probe's
	// SelfSubjectAccessReview and Secret get target this namespace, where
	// credentialPool secretRef Secrets are mounted. Defaults to the
	// Token Service's own namespace (POD_NAMESPACE, set by the downward
	// API), falling back to lenny-system.
	f.secretNamespace = flag.String("secret-namespace", envOr("POD_NAMESPACE", "lenny-system"),
		"namespace the §4.9 RBAC live-probe checks for credentialPool secretRef Secrets.")
}

// registerRateLimitFlags registers the §13.3 line 607 /v1/oauth/token rate
// limits. The defaults transcribe the §13.3 line 607 normative limits.
// spec: §13.3.
func (f *tokenServiceFlags) registerRateLimitFlags() {
	f.rlCallerPerSec = flag.Int("oauth-rate-limit-caller-per-second", envInt("LENNY_OAUTH_RL_CALLER_PER_SECOND", 10),
		"§13.3 line 607 per-(tenant_id, sub) per-second cap on /v1/oauth/token. Zero disables the tier.")
	f.rlCallerPerMin = flag.Int("oauth-rate-limit-caller-per-minute", envInt("LENNY_OAUTH_RL_CALLER_PER_MINUTE", 300),
		"§13.3 line 607 per-(tenant_id, sub) per-minute cap on /v1/oauth/token. Zero disables the tier.")
	f.rlTenantPerSec = flag.Int("oauth-rate-limit-tenant-per-second", envInt("LENNY_OAUTH_RL_TENANT_PER_SECOND", 100),
		"§13.3 line 607 per-tenant per-second cap on /v1/oauth/token. Zero disables the tier.")
	f.rlSampleWindow = flag.Duration("oauth-rate-limit-sample-window", envDuration("LENNY_OAUTH_RL_SAMPLE_WINDOW", 10*time.Second),
		"§13.3 line 611 rolling-window for sampled audit emission on rate-limited requests (one audit row per (tenant_id, sub, limit_tier) per window per replica).")
}

// registerTLSFlags registers the §4.3 / §10.3 mTLS material for the gRPC
// listener. The Token Service requires the gateway to present a client
// certificate signed by the same CA the chart's mtls-pki.yaml mints.
// spec: §4.3 line 195, §13.2 line 217.
func (f *tokenServiceFlags) registerTLSFlags() {
	f.tlsCert = flag.String("tls-cert", os.Getenv("LENNY_TOKEN_SERVICE_TLS_CERT"),
		"path to the Token Service's server certificate for the §4.3 / §10.3 mTLS gRPC listener. Empty serves the gRPC surface in plaintext (dev mode only).")
	f.tlsKey = flag.String("tls-key", os.Getenv("LENNY_TOKEN_SERVICE_TLS_KEY"),
		"path to the private key for --tls-cert.")
	f.tlsCA = flag.String("tls-ca", os.Getenv("LENNY_TOKEN_SERVICE_CA"),
		"path to the CA bundle that verifies gateway client certificates on the §4.3 / §10.3 mTLS gRPC link. Required when --tls-cert is set; the Token Service uses tls.RequireAndVerifyClientCert.")
}

// registerKMSFlags binds the §4 / §17.5 KMS provider selector flags. The cloud
// adapters (pkg/kms/{aws,gcp,azure}) reach the binary through these flags; the
// chart renders tokenService.kms.* into them. providerflags.Bind returns the
// option set and a post-parse finalize hook runTokenService calls before
// wiring. spec: §4, §17.5.
func (f *tokenServiceFlags) registerKMSFlags() {
	f.kmsOpts, f.kmsFinalize = providerflags.Bind(flag.CommandLine, os.Getenv, providerflags.Options{
		Provider: providerflags.ProviderLocal,
	})
}
