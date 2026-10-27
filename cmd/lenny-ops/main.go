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
	f := parseFlags()
	if err := f.kmsFinalize(); err != nil {
		log.Fatalf("lenny-ops: %v", err)
	}
	// spec: §16.1.1 (Attribute Naming) deployment_tier; §25.4 (The
	// lenny-ops Service) Idempotency-Key. Fail closed on an off-set tier.
	if err := validatePlatformTier(*f.platformTier); err != nil {
		log.Fatalf("lenny-ops: %v", err)
	}
	runOps(f)
}

// configureStructuredLogging installs the §25.4 JSON logger as the
// process-wide slog.Default. The pkg/observability/logging handler
// auto-attaches the §16.4 correlation fields (component, operation_id,
// agent_name, trace_id, …) from any context that carries a
// correlation.Fields value. The stdlib log package is redirected so
// existing log.Printf call sites also surface as structured records and
// no log line escapes the §25.4 format.
//
// spec: §25.4; §16.4. Delegates to the shared
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
// policy. spec: §25.5. F-25.4.9.
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

// authInputs carries what buildAuthConfig needs: the §10.2 key set, the
// revocation set, the §10.2 platform-managed role resolver, and the
// authentication flags.
type authInputs struct {
	keySet      *jwt.KeySet
	revocations *opsRevocation
	// platformRoles is the §10.2 platform-managed role resolver. Nil
	// leaves the token's roles claim authoritative, which is the posture
	// of a process with no Postgres.
	platformRoles authmw.PlatformRoleResolver
	hmacKeyFile   string
	issuer        string
	multiTenant   bool
	devMode       bool
	production    bool
	rps           float64
	burst         int
}

// errDevTrustInProduction refuses the dev embedded-OIDC trust hook in
// production mode (--environment=prod).
var errDevTrustInProduction = errors.New("§17.4: --bearer-trust-hmac-key-file is the dev embedded-OIDC trust hook and is refused in production mode (--environment=prod)")

// errDevTrustWithoutDevMode refuses the dev embedded-OIDC trust hook when
// --dev-mode (LENNY_DEV_MODE) is off, as the gateway does.
var errDevTrustWithoutDevMode = errors.New("§17.4: --bearer-trust-hmac-key-file is the dev embedded-OIDC trust hook and requires --dev-mode (LENNY_DEV_MODE=true)")

// devRelaxed reports whether lenny-ops admits its dev-mode security
// relaxations: the X-Lenny-* identity headers, the dev roles, and the
// unauthenticated surface when no trust hook is configured. §17.4 "Dev
// mode guard rails" item 3 makes LENNY_DEV_MODE the single gate for every
// dev-mode relaxation, so --dev-mode is required. Production mode
// (--environment=prod) never admits a relaxation: a production process authenticates every request
// through the §10.2 key set whether or not --dev-mode is set.
//
// spec: §17.4 "Dev mode guard rails"; §25.4 (The lenny-ops Service:
// Authentication).
func devRelaxed(devMode, production bool) bool {
	return devMode && !production
}

// checkDevTrustPosture refuses --bearer-trust-hmac-key-file in production
// mode (--environment=prod) and without --dev-mode. runOps calls it before the key
// set loads, so the refusal does not wait on a KMS round trip, and
// buildAuthConfig repeats it. The production refusal takes precedence so
// that a production process names the production rule.
//
// spec: §17.4 "Dev mode guard rails"; §25.4 (The lenny-ops Service:
// Authentication).
func checkDevTrustPosture(devMode, production bool, hmacKeyFile string) error {
	if hmacKeyFile == "" {
		return nil
	}
	if production {
		return errDevTrustInProduction
	}
	if !devMode {
		return errDevTrustWithoutDevMode
	}
	return nil
}

// buildAuthConfig assembles the §25.4 authentication and role gate for
// the operability surface.
//
// It returns a config in every posture except dev mode without the trust
// hook. The verifier is the §10.2 key set, composed with the dev trust
// member through a MultiVerifier when the key file is set;
// --oidc-issuer-url wraps only the dev trust member, because
// Lenny-issued tokens are accepted whatever their issuer and audience.
// The revocation set applies to tokens the key set verifies and never to
// dev trust-hook tokens (§25.4 rule 6). The platform-managed role
// resolver applies to every authenticated principal, whichever member
// verified its token, so a stored role assignment replaces the roles
// claim as it does on the gateway (§10.2 "Role assignment"). The
// per-service-account rate limiter is always attached.
//
// The X-Lenny-* identity headers and the dev roles are honored only
// under devRelaxed, and only devRelaxed without the key file serves the
// surface unauthenticated. Outside dev mode, in production mode or not,
// every request authenticates through the key set. The key file in
// production mode (--environment=prod) or without --dev-mode is an error
// the caller treats as fatal.
//
// spec: §25.4 (The lenny-ops Service: Authentication); §10.2
// (Authentication) "Verifier key set and rotation"; §17.4 "Dev mode guard
// rails".
func buildAuthConfig(in authInputs) (*opsserver.AuthConfig, error) {
	if err := checkDevTrustPosture(in.devMode, in.production, in.hmacKeyFile); err != nil {
		return nil, err
	}
	relaxed := devRelaxed(in.devMode, in.production)
	if relaxed && in.hmacKeyFile == "" {
		log.Printf("lenny-ops: §25.4 WARNING — dev mode and no bearer trust key; the operability surface is UNAUTHENTICATED (dev only)")
		return nil, nil
	}
	opts := authmw.Options{
		Verifier:    in.keySet,
		MultiTenant: in.multiTenant,
		// In dev mode the dev headers (X-Lenny-Tenant-ID /
		// X-Lenny-User-ID / X-Lenny-Roles) remain a convenience
		// transport; outside it every claim is anchored to the bearer JWT.
		AllowDevHeaders: relaxed,
		AllowDevRoles:   relaxed,
		PlatformRoles:   in.platformRoles,
	}
	if in.hmacKeyFile != "" {
		dev, err := devTrustVerifier(in.hmacKeyFile, in.issuer)
		if err != nil {
			return nil, err
		}
		opts.Verifier = jwt.NewMultiVerifier(in.keySet, dev)
		opts.RevocationScope = keySetScope(in.keySet)
	}
	in.revocations.apply(&opts)
	return &opsserver.AuthConfig{
		Options:     opts,
		RateLimiter: opsserver.NewRateLimiter(in.rps, in.burst),
	}, nil
}

// devTrustVerifier loads the §17.4 embedded OIDC provider's HMAC key and,
// when issuer is set, pins the iss claim of tokens signed with it.
func devTrustVerifier(hmacKeyFile, issuer string) (jwt.Verifier, error) {
	signer, err := jwt.LoadHMACKeyFile(hmacKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load bearer trust key %s: %w", hmacKeyFile, err)
	}
	if issuer == "" {
		return signer, nil
	}
	return jwt.NewClaimChecker(signer, jwt.ExpectedClaims{Issuer: issuer}), nil
}

// keySetScope reports whether a key in the key set signed a token, which
// is how lenny-ops tells a Lenny-issued token from a dev trust-hook token
// when both members are trusted. It reads the header and the signature
// and never the exp or nbf claim, so a key-set token whose exp passes
// between authmw's verification and this call stays in scope and still
// meets the first-read gate and the revocation set.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set
// rules 3, 4, and 6); §10.2 (Authentication) "Verifier key set and
// rotation".
func keySetScope(keySet *jwt.KeySet) func(string) bool {
	return keySet.Signed
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

// envDevMode reads LENNY_DEV_MODE with the gateway's parsing
// (cmd/lenny-gateway envFlag): 1, true, yes, or on in any case enables dev
// mode, and every other value or an unset variable leaves it off, so one
// LENNY_DEV_MODE value means the same thing to both binaries.
//
// spec: §17.4 "Dev mode guard rails".
func envDevMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LENNY_DEV_MODE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
