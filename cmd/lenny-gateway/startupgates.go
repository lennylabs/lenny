// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/delegation/recovery"
	"github.com/lennylabs/lenny/pkg/gateway/deployment/devmode"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/pki/tlsprobe"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
	"github.com/lennylabs/lenny/pkg/observability/slo"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// buildStartupGates is the §4.1 composition-root build step (R1) for the
// gateway's startup gates and process-wide providers, which run before any
// subsystem is constructed. It seeds the §17.2 elicitation content-integrity
// floor provider, enforces the §17.4 dev-mode TLS startup assertion and logs
// the §5.3 dev-mode isolation warning, installs the §16.3 OpenTelemetry tracer
// provider, surfaces the §16.5 provisional-SLO warning, reads the §12.8
// clock-injection offset, resolves the §10.6 noEnvironmentPolicy, runs the
// §10.3 platform-config validation and TLS startup probe, validates the §4.2
// pooler mode, logs the §11.3/§8.10 delegation recovery configuration, and
// warns on a high §4.6.1 reserved-hold TTL. It records the elicitation floor
// provider, the trace-provider shutdown function, and the resolved
// noEnvironmentPolicy on the accumulator so the build steps below read them
// back. Any failed gate is fatal (log.Fatalf), as in the original composition
// root, because a gateway that cannot pass a startup gate must not become
// ready.
//
// spec: §4.1 gateway subsystem seams; §10.3 config validation / TLS probe;
// §17.4 dev-mode gate; §16.3 tracing; §16.5 SLO warning.
func (w *gatewayWiring) buildStartupGates() {
	f := w.f
	elicitationFloor := f.elicitationFloor
	devMode := f.devMode
	tlsTerminatedUpstream := f.tlsTerminatedUpstream
	sloValidated := f.sloValidated
	noEnvPolicy := f.noEnvPolicy
	oidcIssuerURL := f.oidcIssuerURL
	oidcClientID := f.oidcClientID
	maxSessionAgeSeconds := f.maxSessionAgeSeconds
	startupProbeRedisAddr := f.startupProbeRedisAddr
	startupProbePgBouncerAddr := f.startupProbePgBouncerAddr
	startupProbeCA := f.startupProbeCA
	startupProbeCert := f.startupProbeCert
	startupProbeKey := f.startupProbeKey
	poolerMode := f.poolerMode
	delegationMaxLevelRecoverySeconds := f.delegationMaxLevelRecoverySeconds
	delegationMaxTreeRecoverySeconds := f.delegationMaxTreeRecoverySeconds
	delegationUsageQuiescenceTimeoutSeconds := f.delegationUsageQuiescenceTimeoutSeconds
	delegationCascadeTimeoutSeconds := f.delegationCascadeTimeoutSeconds
	delegationMaxOrphanTasksPerTenant := f.delegationMaxOrphanTasksPerTenant
	claimHoldTTLSeconds := f.claimHoldTTLSeconds

	// spec: §17.2 line 86 / §9.2 line 64 — the platform-wide elicitation
	// content-integrity floor is seeded from the
	// --elicitation-content-integrity-floor flag and then kept live by the
	// phase-stamp ConfigMap reconcile started below (when a cluster client
	// exists). Every floor read (the per-request effective-mode resolver,
	// the admin below-floor guard, and the §16.5 weakened-mode gauge) goes
	// through this provider so a `helm upgrade` floor change takes effect
	// without a gateway restart. F-17.2.9.
	w.elicitationFloorProvider = elicitationfloor.NewProvider(*elicitationFloor)

	// spec: §17.4 line 268 — dev-mode hard startup assertion. The
	// gateway's own listener is plain HTTP; production terminates TLS at
	// the ingress (the §17 line 7 Deployment+Service+Ingress topology)
	// and acknowledges that posture with --tls-terminated-upstream. With
	// neither dev mode nor that acknowledgment the gateway refuses to
	// start so a misconfigured staging or production deployment cannot
	// silently run without encryption. F-17.4.5.
	if err := devmode.ResolveStartupGate(*devMode, *tlsTerminatedUpstream); err != nil {
		log.Fatalf("lenny-gateway: LENNY_TLS_REQUIRED: %v", err)
	}

	// spec: §5.3 line 677 — in dev mode the default isolation profile
	// falls back to runc. Log the mandated warning once at startup so an
	// accidental production dev-mode install is visible in the logs.
	if *devMode {
		log.Printf("lenny-gateway: %s", isolation.DevModeIsolationWarning)
		// spec: §17.4 line 269 — when dev mode relaxes TLS, log the
		// warning at startup and re-broadcast it every minute while the
		// process runs. F-17.4.6.
		devmode.StartWarnTicker(context.Background(), devmode.WarnInterval, func(msg string) {
			log.Printf("lenny-gateway: %s", msg)
		})
	}

	// spec: §16.3 line 359 — install the process-wide OpenTelemetry
	// TracerProvider and W3C trace-context propagator so the §16.3 span
	// catalog has a real exporter behind it instead of the global no-op
	// provider. The gateway emits 100% (head) and an OTLP/HTTP exporter
	// ships spans to OTEL_EXPORTER_OTLP_ENDPOINT; with no endpoint (or in
	// dev mode) a stdout exporter covers `make run`. F-16.3.2 / F-16.3.8.
	traceShutdown, err := tracing.InitProvider(context.Background(), tracing.ProviderConfig{
		ServiceName:  "lenny-gateway",
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		DevMode:      *devMode,
	})
	if err != nil {
		log.Fatalf("lenny-gateway: tracing init: %v", err)
	}
	w.traceShutdown = traceShutdown

	// spec: §16.5 lines 609, 623 — surface the provisional-SLO startup
	// warning unless the Phase 14.5 benchmark gate has set slo.validated,
	// so a deployment running the unvalidated defaults cannot silently
	// treat them as customer SLA commitments.
	if msg, emit := slo.StartupWarning(*sloValidated); emit {
		log.Printf("lenny-gateway: %s", msg)
	}

	// §12.8 clock-injection harness. Read LENNY_CLOCK_OFFSET_SECONDS
	// once at startup so the gateway and every clock-using subsystem
	// pick up a chaos-test offset. A non-integer value fails loudly
	// here rather than silently behaving as production. Production
	// installs run with the variable unset; the §17.6 preflight Job
	// asserts that separately via AssertProductionDefault.
	if err := clockinject.FromEnv(); err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}

	// §10.6: the platform-wide noEnvironmentPolicy must be set
	// explicitly. Dev mode derives allow-all for local convenience;
	// outside dev mode an unset value is a fatal misconfiguration so a
	// chart with the default stripped fails closed at startup. See
	// resolveNoEnvironmentPolicy for the pure-function form the
	// §11.1 TestGatewayConfigValidation regression test exercises.
	resolvedNoEnvPolicy, err := resolveNoEnvironmentPolicy(*noEnvPolicy, *devMode)
	if err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}
	w.resolvedNoEnvPolicy = resolvedNoEnvPolicy

	// spec: §10.3 lines 361-371 — startup configuration validation. Each
	// missing required platform key emits a structured LENNY_CONFIG_MISSING
	// log entry (config_key/scope/remediation fields) and the gateway
	// refuses to become ready by exiting non-zero so Kubernetes surfaces
	// CrashLoopBackOff rather than the replica silently serving with
	// undefined semantics. noEnvironmentPolicy is gated just above;
	// playground.devTenantId by playground.Config.Validate; this covers the
	// OIDC and session-duration keys. The OIDC keys are exempt in dev mode
	// (§10.3 line 373 / §17.4). F-10.3.14.
	if missing := validatePlatformConfig(*devMode, *oidcIssuerURL, *oidcClientID, *maxSessionAgeSeconds); len(missing) > 0 {
		for _, m := range missing {
			slog.Error("LENNY_CONFIG_MISSING", "config_key", m.configKey, "scope", m.scope, "remediation", m.remediation)
		}
		log.Fatalf("lenny-gateway: %d required platform configuration key(s) missing or invalid (§10.3); see the LENNY_CONFIG_MISSING entries above", len(missing))
	}

	// spec: §10.3 line 359 — gateway startup TLS probe. Before the replica
	// is marked ready, verify a TLS handshake to Redis and PgBouncer
	// succeeds and that a plaintext connection is refused, so a
	// misconfigured backend (wrong port, missing cert) fails startup
	// rather than degrading silently at runtime. Dev mode is exempt (the
	// line 373 dev-mode symmetry / §17.4) and an unset endpoint is skipped.
	// F-10.3.15.
	if !*devMode && (*startupProbeRedisAddr != "" || *startupProbePgBouncerAddr != "") {
		probeTLS, perr := buildStartupProbeTLSConfig(*startupProbeCA, *startupProbeCert, *startupProbeKey)
		if perr != nil {
			log.Fatalf("lenny-gateway: §10.3 startup TLS probe configuration: %v", perr)
		}
		if err := tlsprobe.Probe(
			context.Background(), tlsprobe.Config{TLSConfig: probeTLS},
			tlsprobe.Target{Backend: tlsprobe.BackendRedis, Addr: *startupProbeRedisAddr},
			tlsprobe.Target{Backend: tlsprobe.BackendPgBouncer, Addr: *startupProbePgBouncerAddr},
		); err != nil {
			log.Fatalf("lenny-gateway: §10.3 startup TLS probe failed: %v", err)
		}
		log.Printf("lenny-gateway: §10.3 startup TLS probe passed (redis=%q pgbouncer=%q)", *startupProbeRedisAddr, *startupProbePgBouncerAddr)
	}

	// spec: §4.2 line 165 — LENNY_POOLER_MODE must be one of the two
	// documented values. The trigger-level guard runs regardless of
	// this check; failing at startup keeps a misconfigured deploy
	// from running silently with the wrong assumed posture.
	switch *poolerMode {
	case "transactional", "external":
		log.Printf("lenny-gateway: pooler-mode=%s (§4.2 line 165)", *poolerMode)
	default:
		log.Fatalf("lenny-gateway: --pooler-mode must be `transactional` or `external`, got %q (§4.2 line 165)", *poolerMode)
	}

	// spec: §11.3 line 224 — surface the effective
	// `delegation.usageQuiescenceTimeoutSeconds` at startup so operators
	// see the value the §8.10 tree-recovery orchestrator will consume.
	// The recovery package exposes the Config field; the gateway-side
	// orchestrator threads `delegationQuiescenceCfg` to keep one source
	// of truth. F-11.3.19.
	//
	// spec: §8.10 lines 1022-1023 / 1042 — extend the same Config to
	// carry `maxLevelRecoverySeconds` / `maxTreeRecoverySeconds` so the
	// recovery package consumes the live operator overrides. F-8.10.6.
	delegationQuiescenceCfg := recovery.Config{
		LevelTimeout:           time.Duration(*delegationMaxLevelRecoverySeconds) * time.Second,
		TreeTimeout:            time.Duration(*delegationMaxTreeRecoverySeconds) * time.Second,
		UsageQuiescenceTimeout: time.Duration(*delegationUsageQuiescenceTimeoutSeconds) * time.Second,
	}
	log.Printf("lenny-gateway: delegation.usageQuiescenceTimeoutSeconds=%ds (§11.3 line 224)",
		int(delegationQuiescenceCfg.QuiescenceDeadline(time.Time{}).Sub(time.Time{}).Seconds()))
	log.Printf("lenny-gateway: delegation.maxLevelRecoverySeconds=%ds delegation.maxTreeRecoverySeconds=%ds (§8.10 lines 1022-1023)",
		*delegationMaxLevelRecoverySeconds, *delegationMaxTreeRecoverySeconds)
	log.Printf("lenny-gateway: delegation.cascadeTimeoutSeconds=%ds delegation.maxOrphanTasksPerTenant=%d (§8.10 lines 1078, 1103)",
		*delegationCascadeTimeoutSeconds, *delegationMaxOrphanTasksPerTenant)
	_ = delegationQuiescenceCfg

	// §4.6.1: a high reserved-hold TTL holds a recycled pod out of its pinned
	// tenant's claimable idle inventory (a reserved pod is counted as occupied
	// for inventory and scaling, §4.6.2) and delays retirement-limit
	// evaluation at the next disposition. Warn at startup when the operator
	// sets it above the advisory ceiling so the inventory effect is visible in
	// the logs; the value is honored regardless.
	if *claimHoldTTLSeconds > recycle.HighClaimHoldTTLWarnSeconds {
		log.Printf("lenny-gateway: WARNING: claimHoldTTLSeconds=%ds exceeds the advisory ceiling of %ds (§4.6.1); a long reserved hold depresses apparent idle inventory and delays retirement-limit evaluation",
			*claimHoldTTLSeconds, recycle.HighClaimHoldTTLWarnSeconds)
	}
}
