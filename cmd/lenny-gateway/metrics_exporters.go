// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// exportStorageQuotaMetrics refreshes the §16.1 per-tenant
// storage-quota gauges from the tenant registry and the storage
// counter. Only tenants with a configured quota are exported so the
// §16.5 StorageQuotaHigh alert does not divide by a zero limit.
func exportStorageQuotaMetrics(ctx context.Context, tenants tenantstore.Store, counter storagequota.Counter, m *gatewaymetrics.Metrics) {
	rows, err := tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		log.Printf("lenny-gateway: storage-quota metrics: listing tenants failed: %v", err)
		return
	}
	for _, t := range rows {
		if t.StorageQuotaBytes <= 0 {
			continue
		}
		used, err := counter.Used(ctx, t.ID)
		if err != nil {
			continue
		}
		m.SetStorageQuota(t.ID, used, t.StorageQuotaBytes)
	}
}

// exportElicitationIntegrityWeakened refreshes the §16.5
// ElicitationContentIntegrityWeakened standing-alert gauge: the count
// of active tenants whose §9.2 effective elicitation content-integrity
// mode (max(platformFloor, tenantStored)) is weaker than enforce. The
// gauge keeps the standing warning alert firing while any tenant runs a
// reduced-integrity posture and resolves it to zero once every active
// tenant resolves to enforce. List with the zero filter already drops
// soft-deleted rows, so the count reflects active tenants only. Errors
// are logged but never bubble — the exporter is a best-effort signal
// and must not interrupt the gauge-refresh loop.
//
// spec: §16.5
// spec: §9.2
func exportElicitationIntegrityWeakened(ctx context.Context, tenants tenantstore.Store, floor string, m *gatewaymetrics.Metrics) {
	rows, err := tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("lenny-gateway: elicitation-integrity weakened gauge: listing tenants failed: %v", err)
		}
		return
	}
	var weakened int
	for _, t := range rows {
		eff := elicitation.ResolveEffectiveWithDefaults(floor, t.ElicitationContentIntegrity)
		if !eff.AtLeast(elicitation.ModeEnforce) {
			weakened++
		}
	}
	m.SetElicitationIntegrityWeakened(weakened)
}

// tenantListerForHPA is the narrow interface exportHPAGauges
// requires. Both tenantsLister (production) and the test fake
// staticTenantLister satisfy it.
type tenantListerForHPA interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// exportHPAGauges refreshes the §4.1 / §16.1 horizontal-scaling
// gauges: the primary scale-out trigger (request queue depth, the
// in-flight HTTP request count on this replica), the secondary HPA
// metric (active streaming connections), and the capacity-ceiling
// numerator (non-terminal sessions tracked across tenants). Each
// gauge is set unconditionally on every poll so a transient store
// failure does not strand the gauge at a stale value — the next poll
// retries. Errors are logged but never bubble: the exporter is a
// best-effort signal and must not interrupt the watchdog loop.
//
// spec: §4.1 SCL-026 (HPA metric roles)
// spec: §16.1 (gauge metric definitions)
// spec: §16.5 GatewaySessionBudgetNearExhaustion (denominator gauge)
func exportHPAGauges(ctx context.Context, sessions sessionstore.Store, lister tenantListerForHPA, bus *sessionevents.Bus, m *gatewaymetrics.Metrics) {
	// Request queue depth — the §4.1 SCL-026 primary HPA scale-out
	// trigger. The metric is the count of HTTP requests the metrics
	// Middleware is currently servicing on this replica.
	m.SetRequestQueueDepth(m.InflightRequests())

	// Active streams — the §4.1 SCL-026 secondary HPA metric. Counts
	// in-flight SSE subscribers on this replica's sessionevents bus.
	if bus != nil {
		m.SetActiveStreams(bus.ActiveSubscribers())
		// spec: §10.4 / §16 catalog — sample the worst
		// per-session SSE replay buffer utilization so the
		// lenny_event_bus_replay_buffer_utilization gauge tracks the
		// pressure on the §10.4 reconnect-window assumption. F-10.4.11.
		m.SetReplayBufferUtilization(bus.MaxReplayBufferUtilization())
	}

	// Active sessions — the §16.5 GatewaySessionBudgetNearExhaustion
	// alert numerator. Walks every tenant and counts non-terminal
	// sessions. Production scale will replace the per-tenant list
	// with a SessionStore.Count primitive; the per-tenant walk is
	// adequate for current tier sizes.
	tenants, err := lister.ListTenants(ctx)
	if err != nil {
		log.Printf("lenny-gateway: HPA gauge export: listing tenants failed: %v", err)
		return
	}
	var active int
	for _, tenant := range tenants {
		rows, err := sessions.List(ctx, tenant, sessionstore.ListFilter{})
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("lenny-gateway: HPA gauge export: tenant %q list failed: %v", tenant, err)
			}
			continue
		}
		for _, row := range rows {
			if !session.IsTerminal(row.State) {
				active++
			}
		}
	}
	m.SetActiveSessions(active)
}

// exportSessionAvailabilityRatio refreshes the §16.5 Session availability
// SLI: lenny_session_unavailability_ratio is the fraction of active
// sessions currently in a retry/recovery state (resume_pending, resuming,
// awaiting_client_action), the inverse of "uptime of sessions not in
// retry/recovery state". The SessionAvailabilityBurnRate alert reads it.
// The ratio is 0 when there are no active sessions (an idle gateway is
// fully available). F-16.5.3.
func exportSessionAvailabilityRatio(ctx context.Context, sessions sessionstore.Store, m *gatewaymetrics.Metrics) {
	active, err := sessions.CountActiveSessionsGlobal(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("lenny-gateway: session-availability gauge export: active count failed: %v", err)
		}
		return
	}
	if active == 0 {
		m.SetSessionUnavailabilityRatio(0)
		return
	}
	recovery, err := sessions.CountActiveSessionsInRecoveryGlobal(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("lenny-gateway: session-availability gauge export: recovery count failed: %v", err)
		}
		return
	}
	m.SetSessionUnavailabilityRatio(float64(recovery) / float64(active))
}

// exportCircuitBreakerMetrics refreshes the §16.1 circuit-breaker
// gauges: the per-breaker open state and the cache freshness. In
// in-memory mode there is no cache, so it reports the registry as
// always-current and initialized.
func exportCircuitBreakerMetrics(ctx context.Context, breakers breakerRegistry, cache *cachingstore.Store, m *gatewaymetrics.Metrics) {
	if rows, err := breakers.List(ctx); err == nil {
		for _, b := range rows {
			m.SetCircuitBreakerOpen(b.Name, b.State == circuitbreaker.StateOpen)
		}
	}
	if cache == nil {
		m.SetCircuitBreakerCache(0, true)
		return
	}
	last := cache.LastRefresh()
	if last.IsZero() {
		m.SetCircuitBreakerCache(0, false)
		return
	}
	m.SetCircuitBreakerCache(time.Since(last).Seconds(), true)
}
