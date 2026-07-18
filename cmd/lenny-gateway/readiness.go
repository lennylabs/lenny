// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
)

// readinessResult is the HTTP status code and plain-text body the
// /readyz handler writes. An empty Body means "no body" (the 200 path).
type readinessResult struct {
	Code int
	Body string
}

// hardDepProbe reports the worst health Status across the gateway's
// hard backend dependencies. The /readyz handler wires it to
// health.Aggregator.HardDependencyStatus over the configured backend
// names; a deployment with no hard backend wired passes a probe that
// always returns StatusHealthy.
type hardDepProbe func(ctx context.Context) health.Status

// readinessVerdict computes the §10.4 readiness probe result. It is
// split out of the inline /readyz handler so the precedence rules are
// unit-testable without standing up the whole gateway.
//
// Precedence (first match wins):
//
//  1. draining — the §10.1 preStop staged drain flipped readiness so
//     the Endpoints controller removes this pod before the
//     eviction-checkpoint drain runs.
//  2. clockDrift — this replica's NTP drift exceeds the §13.3 5s
//     ceiling, so `exp` validation cannot be trusted and the replica
//     must leave traffic even if its stores are reachable. F-13.3.5.
//  3. rebuildPending — the §4.9 credential deny-list startup rebuild has
//     not committed its authoritative Reset yet, so this replica's deny
//     list is incomplete and it could resolve a retained revoked lease
//     without the shadowing deny-list entry. Readiness reports 503 so the
//     replica serves no proxy traffic until the deny list is complete;
//     /healthz stays live throughout so a boot-time store outage that
//     outlasts a liveness probe does not crashloop the pod.
//  4. dualStoreDown — both Postgres and Redis are unreachable. The
//     §10.1 dual-store degraded mode keeps the replica READY so it can
//     answer session.create with 503 PLATFORM_DEGRADED + Retry-After
//     instead of vanishing from the Service (a removed pod cannot
//     deliver the clean degraded response). The dualstore.Monitor owns
//     the per-replica countdown and the SSE broadcast. F-10.1.3.
//  5. hard-dependency unhealthy — the externalized session-truth store
//     (Postgres) is unreachable from this replica, so it cannot serve.
//     Readiness reports 503 so traffic routes to a replica that can.
//     Redis-only loss does not gate readiness because §12.4 provides a
//     Postgres advisory-lock lease fallback that keeps a Redis-down
//     replica functional in degraded mode.
//
// spec: §10.4 line 386; §4.9 startup deny-list rebuild; §10.1 dual-store
// unavailability; §13.3 line 595; §12.4 advisory-lock lease fallback.
// F-10.4.6.
func readinessVerdict(ctx context.Context, draining, clockDrift, rebuildPending, dualStoreDown bool, probe hardDepProbe) readinessResult {
	if draining {
		return readinessResult{Code: 503, Body: "draining\n"}
	}
	if clockDrift {
		return readinessResult{Code: 503, Body: "clock_drift_exceeded\n"}
	}
	if rebuildPending {
		return readinessResult{Code: 503, Body: "credential_deny_list_rebuild_pending\n"}
	}
	if dualStoreDown {
		return readinessResult{Code: 200}
	}
	if probe != nil && probe(ctx) == health.StatusUnhealthy {
		return readinessResult{Code: 503, Body: "backend_unavailable\n"}
	}
	return readinessResult{Code: 200}
}
