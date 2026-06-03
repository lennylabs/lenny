// SPDX-License-Identifier: MIT

// Package opsserver implements the HTTP surface of the §25 lenny-ops
// operability service. lenny-ops is a separate Deployment from the
// gateway and hosts the operability endpoints that read durable state
// (Postgres, Redis, the Kubernetes API, Prometheus) rather than
// in-process gateway state.
//
// This package carries the service's routing, its own liveness and
// readiness probes, and the §25.6 connectivity diagnostic. The §25.6
// session and pool diagnostics and the §25.4 and later operability
// endpoints register here as they are built.
package opsserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lennylabs/lenny/pkg/ops/auditrate"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	opsevents "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
	"github.com/lennylabs/lenny/pkg/ops/opsidem"
	"github.com/lennylabs/lenny/pkg/ops/probe"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// probeTimeout bounds each §25 dependency probe (§25.6: 2s timeouts).
const probeTimeout = 2 * time.Second

// SelfHealthReporter yields the §25.4 lenny-ops self-health report the
// GET /v1/admin/ops/health endpoint serves. pkg/ops/opsservice's
// SelfHealthMonitor satisfies it.
type SelfHealthReporter interface {
	// SelfHealth returns the current self-health report as a JSON object.
	SelfHealth() map[string]any
}

// LeaderReporter reports this replica's §25.4 leader-election state and
// the background loops it owns. pkg/ops/opsservice's Service satisfies
// it. The readiness endpoint surfaces this so an operator querying any
// replica can see which one is the leader.
type LeaderReporter interface {
	// IsLeader reports whether this replica holds the lenny-ops-leader
	// Lease.
	IsLeader() bool
	// LoopNames returns the names of the background loops.
	LoopNames() []string
	// LeaderLoopsRunning reports whether the leader-only loops are
	// active on this replica.
	LeaderLoopsRunning() bool
}

// Server is the lenny-ops HTTP handler. It routes the operability
// endpoints and the Kubernetes liveness and readiness probes.
type Server struct {
	mux                *http.ServeMux
	handler            http.Handler // mux wrapped in the §25.4 correlation/access-log middleware
	probes             map[string]probe.Func
	runbooks           RunbookSource
	selfHealth         SelfHealthReporter
	leader             LeaderReporter
	backups            backup.BackupService
	diagnostics        diagnostics.DiagnosticService
	drift              *driftservice.Service
	locks              coordination.RemediationLockService
	lockCoordination   *coordination.CoordinationGate
	escalations        *escalation.Service
	eventStream        *opsevents.Service
	eventSubscriptions *eventsubscription.Service
	mcp                *mcp.Server
	releaseChannel     *releasechannel.Publisher
	upgrade            *upgradeservice.Service
	upgradeChecker     *upgradeservice.Checker
	versionAggregator  *upgradeservice.VersionAggregator
	podLogs            PodLogReader
	production         bool

	// idem applies the §25.4 idempotency middleware to mutating routes
	// when an Idempotency store is configured; nil leaves the surface
	// without idempotency tracking.
	idem *opsidem.Middleware

	// diagAudit applies the §25.9 diagnostics-audit rate limiting +
	// coalescing when DiagnosticsAudit is configured; nil disables it.
	diagAudit    *auditrate.Limiter
	diagAuditCfg *DiagnosticsAuditConfig
}

// Options configures a lenny-ops Server.
type Options struct {
	// Probes are the §25 dependency checks (Postgres, Redis, MinIO, the
	// Kubernetes API, the gateway) the readiness and connectivity
	// endpoints run. A nil map leaves the dependency report empty.
	Probes map[string]probe.Func
	// Runbooks is the §25.7 runbook index source. A nil source leaves
	// the runbook endpoint reporting the index unavailable.
	Runbooks RunbookSource
	// SelfHealth is the §25.4 self-health report source for GET
	// /v1/admin/ops/health. A nil source reports the self-health report
	// unavailable.
	SelfHealth SelfHealthReporter
	// Leader is the §25.4 leader-election state source. A nil source
	// omits the leader-election fields from the readiness report.
	Leader LeaderReporter
	// Backups is the §25.11 BackupService. A nil service reports the
	// backup-and-restore endpoints as unavailable.
	Backups backup.BackupService
	// Diagnostics is the §25.6 DiagnosticService for the session, pool,
	// and credential-pool diagnostic endpoints. A nil service reports
	// those endpoints as unavailable.
	Diagnostics diagnostics.DiagnosticService
	// Drift is the §25.10 configuration-drift service. A nil service
	// reports the drift endpoints as unavailable.
	Drift *driftservice.Service
	// Locks is the §25.4 remediation-lock service. A nil service reports
	// the remediation-lock endpoints as unavailable.
	Locks coordination.RemediationLockService
	// LockCoordination, when non-nil, applies the §25.4 ops.locks.memoryTier
	// policy to in-memory (Tier-3) lock acquisitions: a multi-replica
	// deployment under the single-replica-only default rejects an
	// uncoordinated acquire with 503 REMEDIATION_LOCK_NO_COORDINATION, and
	// the "always" mode attaches a replica-local degradation warning. A nil
	// gate leaves acquisitions ungated (the v1 single-process dev path).
	LockCoordination *coordination.CoordinationGate
	// Escalations is the §25.4 escalation service. A nil service reports
	// the escalation endpoints as unavailable.
	Escalations *escalation.Service
	// EventStream is the §25.5 operational-event stream service. When
	// non-nil the Server registers GET /v1/admin/events/stream (SSE) and
	// GET /v1/admin/events (polling). A nil service leaves the routes
	// unmapped (404), useful for deployments that disable the stream.
	EventStream *opsevents.Service
	// EventSubscriptions is the §25.5 webhook-subscription service.
	// When non-nil the Server registers the
	// /v1/admin/event-subscriptions CRUD routes; when nil the routes
	// are unmapped (404), so a developer-mode deployment that runs
	// without webhook delivery does not advertise a surface it cannot
	// serve.
	EventSubscriptions *eventsubscription.Service
	// ReleaseChannel is the §25.8 release-channel manifest publisher.
	// When non-nil the Server registers GET /v1/latest backed by the
	// publisher; when nil the path is unmapped (404). A nil publisher
	// is the cold-start signal that the operator has not yet
	// configured the §25.8 Ed25519 signing key, which §25.8 keeps an
	// explicit operator action (the lenny-ops binary refuses to start
	// the publisher when no key is configured rather than serving
	// unsigned responses).
	ReleaseChannel *releasechannel.Publisher
	// Upgrade is the §25.8 platform-upgrade orchestrator. When non-nil
	// the Server registers the upgrade lifecycle routes
	// (start/proceed/pause/rollback/verify/status); when nil the routes
	// are unmapped (404), the cold-start posture for a deployment whose
	// upgrade orchestrator is not yet wired.
	Upgrade *upgradeservice.Service
	// UpgradeChecker is the §25.8 release-channel upgrade-check client.
	// When non-nil the Server registers GET
	// /v1/admin/platform/upgrade-check; when nil the route is unmapped.
	UpgradeChecker *upgradeservice.Checker
	// VersionAggregator backs the §25.8 GET
	// /v1/admin/platform/version/full report. When non-nil the Server
	// registers the route; when nil the route is unmapped (404). The
	// aggregator queries the per-component version sources lenny-ops can
	// reach and flags drift from the running build.
	VersionAggregator *upgradeservice.VersionAggregator
	// PodLogs, when non-nil, backs the §25.4 GET
	// /v1/admin/logs/pods/{namespace}/{name} log-proxy endpoint with the
	// Kubernetes pod-log API. A nil reader (no cluster connection) leaves
	// the endpoint reporting the log proxy unavailable.
	PodLogs PodLogReader
	// Production reports whether this deployment is production, which
	// gates the §25.11 confirm requirement for a full backup.
	Production bool
	// DiagnosticsAudit, when non-nil, enables §25.9 diagnostics-audit
	// emission with per-resource coalescing and a per-service-account
	// rate cap on the §25.6 diagnostic endpoints. A nil value leaves the
	// diagnostic endpoints emitting no audit event (they still serve).
	DiagnosticsAudit *DiagnosticsAuditConfig
	// Auth, when non-nil, wraps every operability route (all paths except
	// the Kubernetes probes /healthz and /readyz) in the §25.4 lines
	// 1562-1564 OIDC authentication + platform-admin/tenant-admin role
	// gate, and applies the §25.4 line 2001 per-service-account rate limit
	// when a RateLimiter is set. A nil value leaves the surface
	// unauthenticated (dev / embedded single-node only); the production
	// binary always supplies it.
	Auth *AuthConfig

	// Idempotency, when non-nil, wraps every mutating (POST/PUT)
	// operability route in the §25.4 idempotency middleware backed by this
	// store. Records are keyed by (Idempotency-Key, caller_id); the
	// required-key endpoints (upgrade start, restore execute, full backup)
	// reject a missing key (400) and fail closed (503) on a store outage
	// at Tier 2/3. A nil store leaves the surface without idempotency
	// tracking. The middleware sits inside the Auth wrapper so caller_id
	// resolves from the verified principal.
	Idempotency opsidem.Store

	// Metrics, when non-nil, is served at GET /metrics as the §16.8 / §16.9
	// Prometheus scrape surface. lenny-ops registers its instruments (the
	// §16.8 self-health, backup, drift, rate-limit, and diagnostics series)
	// on the process default registry; the binary passes a promhttp handler
	// over it. The endpoint is exempt from the §25.4 OIDC gate because a
	// Prometheus scrape carries no bearer token — the §13.2 NET-045
	// metrics-scrape NetworkPolicy is the access control, matching the
	// gateway. A nil handler defaults to promhttp.Handler() over the
	// process default registry so a dev or embedded build still exposes
	// whatever instruments are registered.
	Metrics http.Handler
}

// New returns a Server with the liveness probe, readiness probe, and
// the §25 operability endpoints registered: the §25.6 diagnostics, the
// §25.7 runbook index, the §25.4 self-health, remediation-lock, and
// escalation endpoints, the §25.10 drift endpoints, the §25.11 backup
// and restore endpoints, and the §25.12 MCP management server.
func New(opts Options) *Server {
	s := &Server{
		mux:                http.NewServeMux(),
		probes:             opts.Probes,
		runbooks:           opts.Runbooks,
		selfHealth:         opts.SelfHealth,
		leader:             opts.Leader,
		backups:            opts.Backups,
		diagnostics:        opts.Diagnostics,
		drift:              opts.Drift,
		locks:              opts.Locks,
		lockCoordination:   opts.LockCoordination,
		escalations:        opts.Escalations,
		eventStream:        opts.EventStream,
		eventSubscriptions: opts.EventSubscriptions,
		releaseChannel:     opts.ReleaseChannel,
		upgrade:            opts.Upgrade,
		upgradeChecker:     opts.UpgradeChecker,
		versionAggregator:  opts.VersionAggregator,
		podLogs:            opts.PodLogs,
		production:         opts.Production,
		diagAuditCfg:       opts.DiagnosticsAudit,
	}
	// §25.4 idempotency: caller_id is the verified principal's OIDC sub
	// (callerIdentity), and Production reports the Tier 2/3 required-key
	// posture so required endpoints reject a missing key and fail closed
	// on a store outage.
	if opts.Idempotency != nil {
		s.idem = opsidem.New(opts.Idempotency, opsidem.Config{
			CallerID:   callerIdentity,
			Production: opts.Production,
		})
	}
	// §25.9 diagnostics-audit rate limiting: when configured, the §25.6
	// diagnostic endpoints record each access through the coalescing
	// limiter, emitting one audit event per resource per 60s window.
	if opts.DiagnosticsAudit != nil {
		s.diagAudit = auditrate.New(opts.DiagnosticsAudit.RatePerMinute).WithFlush(opts.DiagnosticsAudit.Emit)
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	// spec: §16.8 / §16.9 line 720 — the Prometheus scrape surface. §16.9
	// names lenny-ops (TCP 9090) a mandatory scrape target in every tier;
	// the §16.8 self-monitoring, backup, drift, and rate-limit series are
	// registered on the process default registry and exposed here. The
	// route is exempt from the §25.4 OIDC gate (a scrape carries no bearer;
	// the §13.2 NET-045 NetworkPolicy is the access control).
	metricsHandler := opts.Metrics
	if metricsHandler == nil {
		metricsHandler = promhttp.Handler()
	}
	s.mux.Handle("GET /metrics", metricsHandler)
	s.mux.HandleFunc("GET /v1/admin/diagnostics/connectivity", s.handleConnectivity)
	s.mux.HandleFunc("GET /v1/admin/runbooks", s.handleListRunbooks)
	s.mux.HandleFunc("GET /v1/admin/runbooks/{name}/steps", s.handleRunbookSteps)
	// spec: §25.7 lines 3055-3057, §17.7 line 741 — the full markdown
	// hop for an agent that already holds the runbook name.
	s.mux.HandleFunc("GET /v1/admin/runbooks/{name}", s.handleRunbookMarkdown)
	s.mux.HandleFunc("GET /v1/admin/ops/health", s.handleOpsHealth)
	s.registerLogRoutes()
	s.registerBackupRoutes()
	s.registerDiagnosticsRoutes()
	s.registerDriftRoutes()
	s.registerLockRoutes()
	s.registerEscalationRoutes()
	s.registerEventStreamRoutes()
	s.registerEventSubscriptionRoutes()
	s.registerReleaseChannelRoutes()
	s.registerPlatformUpgradeRoutes()
	// §25.12: the MCP management server exposes the §25 operability
	// surface as MCP tools. It is built last so it can route to the
	// services registered above.
	s.mcp = mcp.NewServer(s.mcpInvoker())
	s.mux.Handle("/mcp/management", s.mcp)
	s.mux.Handle("/mcp/management/", s.mcp)
	// §25.4 idempotency sits closest to the mux, inside the auth wrapper,
	// so it reads caller_id from the principal the auth middleware
	// attaches. Probe paths are GET, so the middleware passes them through
	// untouched even on the dev (Auth nil) path.
	inner := http.Handler(s.mux)
	if s.idem != nil {
		inner = s.idem.Wrap(inner)
	}
	// §25.4 lines 1562-1564: when an AuthConfig is supplied, every
	// operability route runs through the OIDC authentication + role gate
	// (the Kubernetes probes are exempt). The auth wrapper sits inside the
	// correlation/access-log middleware so a rejected request is still
	// logged with its correlation context.
	routes := inner
	if opts.Auth != nil {
		routes = s.withOpsAuth(inner, opts.Auth)
	}
	// §25.4 lines 2499-2526: every request runs through the correlation
	// middleware (stamps operation_id / agent_name / trace_id from inbound
	// headers onto the request context) and the access-log middleware
	// (one structured log line per request, projected through the §25.4
	// JSON handler configured in main).
	s.handler = withCorrelation(withAccessLog(routes))
	return s
}

// ServeHTTP routes a request to the registered operability handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// strictHealthDeps are the dependencies the §25.4 strict readiness
// gate requires reachable before a replica accepts traffic: the
// Postgres connection and the Kubernetes API (spec: §25.4 line 1055 —
// "200 only when both Postgres connection AND K8s API are reachable").
var strictHealthDeps = []string{ProbePostgresName, ProbeK8sAPIName}

// Probe names the strict readiness gate keys on. They mirror the
// opsservice.Probe* constants the binary registers; redeclared here so
// the opsserver package does not import the wiring package.
const (
	ProbePostgresName = "postgres"
	ProbeK8sAPIName   = "kubernetes"
)

// handleHealthz serves both §25.4 probe modes on the same path (spec:
// §25.4 lines 1033-1057):
//
//   - permissive (the default, used by liveness and startup): it reports
//     that the process is running. It does not fail on a downstream
//     dependency outage, so a transient Postgres or Kubernetes API blip
//     does not cause Kubernetes to restart an otherwise-healthy pod.
//   - strict (?strict=true, used by readiness): 200 only when both the
//     Postgres connection and the Kubernetes API are reachable; otherwise
//     503 with a body naming the degraded dependencies so traffic is not
//     routed to a replica that cannot serve the full §25 API.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("strict") != "true" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	// spec: §25.4 line 1055 — strict gate. A required probe that is
	// absent from the map (e.g. no Kubernetes client wired) cannot be
	// confirmed reachable, so it counts as degraded.
	results := probe.Run(r.Context(), s.probes, probeTimeout)
	var degraded []string
	for _, dep := range strictHealthDeps {
		if res, ok := results[dep]; !ok || !res.OK {
			degraded = append(degraded, dep)
		}
	}
	if len(degraded) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":       "degraded",
			"degraded":     degraded,
			"dependencies": dependencyReport(results),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"dependencies": dependencyReport(results),
	})
}

// handleReadyz is the readiness probe. §25 has lenny-ops degrade
// gracefully when a downstream dependency is transiently unavailable,
// so the process reports ready (HTTP 200) as long as it is serving;
// the per-dependency probe results are reported in the body so an
// operator or agent reading the endpoint sees which dependency is
// down.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	results := probe.Run(r.Context(), s.probes, probeTimeout)
	body := map[string]any{
		"status":       "ready",
		"dependencies": dependencyReport(results),
	}
	// §25.4: an operator querying any replica sees which one holds the
	// lenny-ops-leader Lease and runs the singleton background loops.
	if s.leader != nil {
		body["leader"] = s.leader.IsLeader()
		body["leaderLoopsRunning"] = s.leader.LeaderLoopsRunning()
		body["loops"] = s.leader.LoopNames()
	}
	writeJSON(w, http.StatusOK, body)
}

// handleOpsHealth serves the §25.4 GET /v1/admin/ops/health endpoint:
// the structured lenny-ops self-health report (Postgres pool, Redis
// consumer lag, webhook backlog, K8s API connectivity, memory
// pressure) the watchdog polls as a complement to the event stream.
func (s *Server) handleOpsHealth(w http.ResponseWriter, _ *http.Request) {
	if s.selfHealth == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "unknown",
			"detail": "self-health monitoring is not configured",
		})
		return
	}
	writeJSON(w, http.StatusOK, s.selfHealth.SelfHealth())
}

// handleConnectivity serves the §25.6 GET /v1/admin/diagnostics/-
// connectivity endpoint: the dependency connectivity checks. When a
// §25.6 DiagnosticService is configured the connectivity check runs
// through it (it probes Postgres, Redis, MinIO, the Kubernetes API, the
// gateway, and registered connectors from outside the cluster). When no
// DiagnosticService is configured — a deployment without a gateway or
// Postgres connection — the endpoint falls back to the readiness
// dependency probes so connectivity diagnosis still works.
func (s *Server) handleConnectivity(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { diagnostics.ObserveRequestDuration("connectivity", time.Since(start)) }()
	if s.diagnostics != nil {
		report, err := s.diagnostics.CheckConnectivity(r.Context())
		if err != nil {
			writeDiagnosticsError(w, err)
			return
		}
		// spec: §25.9 line 3697 — connectivity is a platform-wide check,
		// so its coalescing window keys on a fixed resource.
		s.recordDiagnosticAudit(r, eventConnectivityChecked, "connectivity", "platform")
		writeJSON(w, http.StatusOK, report)
		return
	}
	results := probe.Run(r.Context(), s.probes, probeTimeout)
	s.recordDiagnosticAudit(r, eventConnectivityChecked, "connectivity", "platform")
	writeJSON(w, http.StatusOK, map[string]any{
		"dependencies": dependencyReport(results),
		"healthy":      probe.AllOK(results),
	})
}

// dependencyReport projects probe results into the JSON body shape the
// readiness and connectivity endpoints share.
func dependencyReport(results map[string]probe.Result) map[string]map[string]any {
	deps := make(map[string]map[string]any, len(results))
	for name, res := range results {
		entry := map[string]any{
			"ok":         res.OK,
			"durationMs": res.Duration.Milliseconds(),
		}
		if res.Detail != "" {
			entry["detail"] = res.Detail
		}
		deps[name] = entry
	}
	return deps
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONValue encodes v to an already-headered response writer. It
// is used by handlers that set the status code and Content-Type
// themselves before writing the body.
func writeJSONValue(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}
