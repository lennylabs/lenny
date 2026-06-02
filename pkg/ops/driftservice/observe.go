// SPDX-License-Identifier: MIT

package driftservice

// §25.10 line 3871 drift audit event types. The HTTP layer and the
// reconcile orchestrator emit these through the AuditSink; the catalog
// in pkg/observability/audit declares the canonical constants and the
// deps wiring maps these strings onto them. F-25.10.2.
const (
	EventReportGenerated        = "drift.report_generated"
	EventReconciliationStarted  = "drift.reconciliation_started"
	EventResourceReconciled     = "drift.resource_reconciled"
	EventReconciliationComplete = "drift.reconciliation_completed"
	EventSnapshotRefreshed      = "drift.snapshot_refreshed"
)

// AuditEvent is one §25.10 drift audit event. Type is one of the
// EventXxx constants; Details carries the §25.10 line 3871 per-event
// payload (e.g. previous_written_at / byteSize for snapshot_refreshed).
type AuditEvent struct {
	Type    string
	Actor   string
	Details map[string]any
}

// AuditSink receives §25.10 drift audit events. The deps wiring emits
// each onto the §16.7 hash-chained audit store (logged until the
// lenny-ops audit-store client lands, matching the backup/escalation
// posture). F-25.10.2.
type AuditSink interface {
	Emit(ev AuditEvent)
}

// noopAuditSink is the default AuditSink: it discards events so a
// service constructed without an audit wiring still runs.
type noopAuditSink struct{}

func (noopAuditSink) Emit(AuditEvent) {}

// Metrics receives the §25.10 line 3858-3859 drift metric increments.
// The deps wiring backs it with the two Prometheus counters; tests use
// a recording double. F-25.10.3.
type Metrics interface {
	// DriftDetected increments lenny_drift_detected_total{resource_type,
	// severity} once per drifted field reported.
	DriftDetected(resourceType, severity string)
	// Reconciled increments lenny_drift_reconciled_total{resource_type,
	// outcome} once per resource reconciliation (outcome "applied" or
	// "failed").
	Reconciled(resourceType, outcome string)
}

// noopMetrics is the default Metrics sink: it discards increments.
type noopMetrics struct{}

func (noopMetrics) DriftDetected(string, string) {}
func (noopMetrics) Reconciled(string, string)    {}
