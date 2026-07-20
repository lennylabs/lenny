// SPDX-License-Identifier: MIT

// Command lenny-ops runs the §25 operability service: a Deployment
// separate from the gateway that hosts the operability endpoints
// reading durable state (Postgres, Redis, the Kubernetes API,
// Prometheus). §25 makes lenny-ops mandatory in every Lenny
// installation; it is reachable only from outside the cluster via an
// Ingress, never from internal cluster workloads.
//
// lenny-ops runs as a Deployment with one or more replicas. The §25.4
// service body has two parts: the HTTP surface (pkg/ops/opsserver),
// which every replica serves, and the leader-elected background loops
// (pkg/ops/opsservice) — the cron evaluator, the webhook delivery
// worker, the scheduled-backup runner, and the reconciliation
// goroutines — which only the replica holding the lenny-ops-leader
// Lease runs. Every replica also runs its own §25.4 self-monitor.
//
// Usage:
//
//	lenny-ops --addr :8090 --leader-election-namespace lenny-system \
//	  --postgres-dsn $LENNY_POSTGRES_DSN --redis-url $LENNY_REDIS_URL
//
// The cluster connection is resolved from the in-cluster service
// account when running as a pod, or from KUBECONFIG otherwise. When no
// cluster connection is available the binary still serves the HTTP
// surface in degraded mode and skips leader election.
package main

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	opsLogging "github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// buildVersion is the compiled-in lenny-ops binary version, overridden
// at build time via "-X main.buildVersion=...". The §25.8 upgrade-check
// compares it against the release channel's advertised version to decide
// whether a newer release is available (§25.8 "lenny-ops binary metadata
// — local, compiled-in via ldflags").
var buildVersion = "dev"

// main is the §25.4 lenny-ops entry point. It parses the command-line flags
// once into the opsFlags value, then hands off to runOps, which wires and
// starts every subsystem and blocks on the §25.4 run-and-shutdown loop. No
// subsystem is constructed here; the composition root in wiring.go runs the
// ordered per-subsystem build sequence (proposal 0020 §4 Part A R4).
//
// spec: §4.1 — the composition root parses its inputs once and threads them
// to each subsystem builder; §25.4 — the lenny-ops service body.
func main() {
	runOps(parseFlags())
}

// configureStructuredLogging installs the §25.4 JSON logger as the
// process-wide slog.Default. The pkg/observability/logging handler
// auto-attaches the §16.4 correlation fields (component, operation_id,
// agent_name, trace_id, …) from any context that carries a
// correlation.Fields value. The stdlib log package is redirected so
// existing log.Printf call sites also surface as structured records and
// no log line escapes the §25.4 format.
//
// spec: §25.4 lines 2499-2526; §16.4 lines 370-372. Delegates to the shared
// logging.Setup so the gateway, lenny-ops, and every other binary install
// the identical §16.4 handler and stdlib-log bridge (component, ts in UTC,
// and any context-borne correlation fields). lenny-ops logs to stderr.
func configureStructuredLogging() {
	opsLogging.Setup(os.Stderr, "lenny-ops")
}

// envOr returns the environment variable name when set, else fallback.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// buildWebhookSSRF assembles the §25.5 callback-URL SSRF validator from
// the ops.webhooks.{allowHTTP,blockedCIDRs,domainAllowlist} Helm values.
// blockedCIDRs and domainAllowlist are comma-separated; a malformed CIDR
// entry is logged and skipped so one typo does not disable the whole
// policy. spec: §25.5 lines 2735-2745. F-25.4.9.
func buildWebhookSSRF(allowHTTP bool, blockedCIDRs, domainAllowlist string) *eventsubscription.SSRFValidator {
	cfg := eventsubscription.SSRFConfig{
		AllowHTTP:       allowHTTP,
		DomainAllowlist: splitCSV(domainAllowlist),
	}
	for _, raw := range splitCSV(blockedCIDRs) {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			log.Printf("lenny-ops: ignoring malformed ops.webhooks.blockedCIDRs entry %q: %v", raw, err)
			continue
		}
		cfg.BlockedCIDRs = append(cfg.BlockedCIDRs, p)
	}
	return eventsubscription.NewSSRFValidator(cfg)
}

// splitCSV splits a comma-separated flag value into trimmed, non-empty
// tokens. An empty input yields nil so the caller's zero-policy default
// (no allowlist, no extra CIDRs) holds.
func splitCSV(s string) []string {
	var out []string
	for _, tok := range strings.Split(s, ",") {
		if t := strings.TrimSpace(tok); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// envInt64 parses the named environment variable as an int64, falling
// back when it is unset or malformed.
func envInt64(name string, fallback int64) int64 {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

// envInt parses the named environment variable as an int, falling back
// when it is unset or malformed.
func envInt(name string, fallback int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// durationOrDefault converts a seconds count to a Duration, returning def
// when seconds is non-positive. The §25.4 ops.security.oidc values arrive
// from the chart as seconds; this keeps the flag defaults expressed as a
// single source of truth.
func durationOrDefault(seconds int, def time.Duration) time.Duration {
	if seconds <= 0 {
		return def
	}
	return time.Duration(seconds) * time.Second
}

// buildAuthConfig assembles the §25.4 lines 1562-1564 OIDC
// authentication + role gate for the operability surface.
//
// The v1 verify key is the shared HMAC signing key at hmacKeyFile (the
// gateway Token Service / §17.4 embedded OIDC key). When an issuer is
// supplied the verifier additionally asserts the iss claim. The per-
// service-account rate limiter (§25.4 line 2001) is always attached when
// auth is enabled.
//
// When no key file is configured the surface is unauthenticated: that is
// admitted only outside production. In production it is a fatal
// misconfiguration (serving the platform-admin remediation-lock / backup
// / drift surface anonymously is the §25.4 security regression the gate
// exists to close), reported as an error so the caller can refuse to
// start.
func buildAuthConfig(hmacKeyFile, issuer string, multiTenant, production bool, rps float64, burst int) (*opsserver.AuthConfig, error) {
	if hmacKeyFile == "" {
		if production {
			return nil, errors.New("§25.4 line 1562 requires authentication in production: set --bearer-trust-hmac-key-file (LENNY_BEARER_TRUST_HMAC_KEY_FILE)")
		}
		log.Printf("lenny-ops: §25.4 WARNING — no bearer verify key configured; the operability surface is UNAUTHENTICATED (dev only)")
		return nil, nil
	}
	signer, err := jwt.LoadHMACKeyFile(hmacKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load bearer trust key %s: %w", hmacKeyFile, err)
	}
	var verifier jwt.Verifier = signer
	if issuer != "" {
		verifier = jwt.NewClaimChecker(verifier, jwt.ExpectedClaims{Issuer: issuer})
	}
	return &opsserver.AuthConfig{
		Options: authmw.Options{
			Verifier:    verifier,
			MultiTenant: multiTenant,
			// Outside production the dev headers (X-Lenny-Tenant-ID /
			// X-Lenny-User-ID / X-Lenny-Roles) remain a convenience
			// transport; production anchors every claim to the bearer JWT.
			AllowDevHeaders: !production,
			AllowDevRoles:   !production,
		},
		RateLimiter: opsserver.NewRateLimiter(rps, burst),
	}, nil
}

// envFloat parses the named environment variable as a float64, falling
// back when it is unset or malformed.
func envFloat(name string, fallback float64) float64 {
	if v := os.Getenv(name); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// envBool parses the named environment variable as a bool, falling back
// when it is unset or malformed.
func envBool(name string, fallback bool) bool {
	if v := os.Getenv(name); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

// splitAndTrim splits a comma-separated string and drops empty entries
// after trimming whitespace. Used to parse --redis-sentinel-addrs.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
