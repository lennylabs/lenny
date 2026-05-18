// SPDX-License-Identifier: MIT

package opsservice

import (
	"sort"
	"sync"
)

// HealthStatus is the §25.4 aggregate self-health status of a lenny-ops
// replica. The values are ordered: a worse status compares greater, so
// the aggregate is the maximum across the individual checks.
type HealthStatus int

const (
	// StatusHealthy means every self-health check is nominal.
	StatusHealthy HealthStatus = iota
	// StatusDegraded means at least one check is degraded and none is
	// unhealthy.
	StatusDegraded
	// StatusUnhealthy means at least one check is unhealthy.
	StatusUnhealthy
)

// String renders the §25.4 self-health status name used in event
// payloads and the GET /v1/admin/ops/health report.
func (s HealthStatus) String() string {
	switch s {
	case StatusHealthy:
		return "healthy"
	case StatusDegraded:
		return "degraded"
	case StatusUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}

// CheckResult is the outcome of one §25.4 self-health check.
type CheckResult struct {
	// Name identifies the check (postgres_pool, redis_consumer_lag,
	// webhook_backlog, k8s_api, memory_pressure).
	Name string `json:"name"`
	// Status is the check's contribution to the aggregate.
	Status HealthStatus `json:"-"`
	// StatusText is the JSON form of Status.
	StatusText string `json:"status"`
	// Detail describes why the check is not healthy; empty when healthy.
	Detail string `json:"detail,omitempty"`
}

// SelfCheck evaluates one §25.4 self-health dimension. The leader and
// the followers both run the same checks; the implementation is
// supplied by the caller so the monitor has one tested loop
// independent of the Postgres, Redis, and Kubernetes clients.
type SelfCheck func() CheckResult

// SelfHealthReport is the §25.4 structured self-health report served by
// GET /v1/admin/ops/health and carried in ops_health_status_changed
// event payloads.
type SelfHealthReport struct {
	// Status is the aggregate: the worst of the individual checks.
	Status HealthStatus `json:"-"`
	// StatusText is the JSON form of Status.
	StatusText string `json:"status"`
	// Checks are the individual check results, ordered by name.
	Checks []CheckResult `json:"checks"`
	// ReplicaID identifies the reporting replica (§25.4 multi-replica
	// scope: each replica reports its own self-health).
	ReplicaID string `json:"replicaId,omitempty"`
}

// evaluateSelfHealth runs every check and folds the results into an
// aggregate report. The aggregate status is the worst individual
// status, matching §25.4: any unhealthy check makes the replica
// unhealthy, any degraded check makes it degraded.
func evaluateSelfHealth(replicaID string, checks map[string]SelfCheck) SelfHealthReport {
	report := SelfHealthReport{Status: StatusHealthy, ReplicaID: replicaID}
	for name, check := range checks {
		res := check()
		if res.Name == "" {
			res.Name = name
		}
		res.StatusText = res.Status.String()
		report.Checks = append(report.Checks, res)
		if res.Status > report.Status {
			report.Status = res.Status
		}
	}
	sort.Slice(report.Checks, func(i, j int) bool { return report.Checks[i].Name < report.Checks[j].Name })
	report.StatusText = report.Status.String()
	return report
}

// SelfHealthMonitor runs the §25.4 self-health checks on a ticker and
// holds the latest report so the GET /v1/admin/ops/health endpoint can
// serve it without re-running the checks. When the aggregate status
// changes between evaluations it invokes the change hook so the
// service can emit an ops_health_status_changed event.
type SelfHealthMonitor struct {
	replicaID string
	checks    map[string]SelfCheck
	onChange  func(prev, next SelfHealthReport)

	mu     sync.RWMutex
	latest SelfHealthReport
}

// NewSelfHealthMonitor returns a monitor over the given §25.4 checks.
// onChange, when non-nil, is invoked with the previous and the new
// report each time the aggregate status transitions; the service wires
// it to ops_health_status_changed emission.
func NewSelfHealthMonitor(replicaID string, checks map[string]SelfCheck, onChange func(prev, next SelfHealthReport)) *SelfHealthMonitor {
	m := &SelfHealthMonitor{replicaID: replicaID, checks: checks, onChange: onChange}
	m.latest = SelfHealthReport{Status: StatusHealthy, StatusText: StatusHealthy.String(), ReplicaID: replicaID}
	return m
}

// Evaluate runs every check once, stores the result as the latest
// report, fires the change hook on a status transition, and returns
// the new report. It is safe to call concurrently with Report.
func (m *SelfHealthMonitor) Evaluate() SelfHealthReport {
	next := evaluateSelfHealth(m.replicaID, m.checks)
	m.mu.Lock()
	prev := m.latest
	m.latest = next
	m.mu.Unlock()
	if m.onChange != nil && prev.Status != next.Status {
		m.onChange(prev, next)
	}
	return next
}

// Report returns the most recent self-health report. Before the first
// Evaluate it reports healthy with no checks.
func (m *SelfHealthMonitor) Report() SelfHealthReport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latest
}

// SelfHealth projects the latest report into the JSON object the
// §25.4 GET /v1/admin/ops/health endpoint serves. It satisfies
// opsserver.SelfHealthReporter so the HTTP surface can serve the
// monitor's state without re-running the checks.
func (m *SelfHealthMonitor) SelfHealth() map[string]any {
	r := m.Report()
	checks := make([]map[string]any, 0, len(r.Checks))
	for _, c := range r.Checks {
		entry := map[string]any{"name": c.Name, "status": c.Status.String()}
		if c.Detail != "" {
			entry["detail"] = c.Detail
		}
		checks = append(checks, entry)
	}
	body := map[string]any{"status": r.Status.String(), "checks": checks}
	if r.ReplicaID != "" {
		body["replicaId"] = r.ReplicaID
	}
	return body
}
