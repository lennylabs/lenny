// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
)

// EventTypeDelegationExportFileScanRejected and ...FailedOpen are the
// §11.7 lines 69-70 audit event types for the §8.7 file-export scan
// path.
const (
	EventTypeDelegationExportFileScanRejected = "delegation.export_file_scan_rejected"
	EventTypeDelegationExportScanFailedOpen   = "delegation.export_scan_failed_open"
)

// ExportScanMetrics is the §16.1 lines 80-81 metric surface the
// ExportScanObserver records to. *gatewaymetrics.Metrics implements it.
// A nil recorder disables metric emission. The interface lives here so
// the policy layer does not import the gateway metrics package.
type ExportScanMetrics interface {
	IncExportFileScan(pool, tenantID, policyName, interceptorRef, outcome string)
	ObserveExportFileScanDuration(pool, tenantID, interceptorRef string, seconds float64)
}

// ExportScanObserver writes the §11.7 file-export scan audit events to
// the per-tenant §11.7 hash chain and records the §16.1 export-scan
// metrics. It satisfies interceptor.ExportScanObserver so the gateway
// hands it to RunPreExportMaterialization via an ExportScanContext. Both
// emissions are best-effort: the scan decision has already been made by
// the time the observer runs, so an audit-append or metric failure is
// dropped rather than failing the export. The §11.7 hash-chain integrity
// check surfaces a persistent audit-backend fault.
type ExportScanObserver struct {
	appender AuditAppender
	metrics  ExportScanMetrics
	clock    func() time.Time
	// billing tees the §11.2.1 delegation.export_file_scan_rejected /
	// export_scan_failed_open events into the per-tenant billing stream
	// alongside the §11.7 audit append, so cost-attribution / compliance
	// consumers see them in the ordered billing record. Nil disables the
	// tee. spec: §11.2.1. F-11.2.1.
	billing *billingfanout.Emitter
}

// NewExportScanObserver returns an observer backed by appender and
// metrics. Either may be nil to disable that emission. clock overrides
// time.Now; pass nil in production.
func NewExportScanObserver(appender AuditAppender, metrics ExportScanMetrics, clock func() time.Time) *ExportScanObserver {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &ExportScanObserver{appender: appender, metrics: metrics, clock: clock}
}

// WithBilling wires the §11.2.1 billing tee for the export-scan events.
// Nil-safe: passing a nil emitter leaves the tee disabled.
func (o *ExportScanObserver) WithBilling(billing *billingfanout.Emitter) *ExportScanObserver {
	o.billing = billing
	return o
}

// ExportFileScanned records the §16.1 metrics for every scanned file and
// emits the §11.7 audit event for the rejected and failed-open outcomes.
// The admitted, modified, and failed_closed outcomes record the metric
// only: §11.7 defines no dedicated audit event for them (a fail-closed
// rejection's caller-facing signal is the §15.1 EXPORT_FILE_SCAN_UNAVAILABLE
// 503). spec: §11.7 lines 69-70; §16.1 lines 80-81; F-8.7.9; F-8.7.10.
func (o *ExportScanObserver) ExportFileScanned(ctx context.Context, ev interceptor.ExportScanEvent) {
	if o.metrics != nil {
		o.metrics.IncExportFileScan(ev.Pool, ev.TenantID, ev.PolicyName, ev.InterceptorRef, string(ev.Outcome))
		o.metrics.ObserveExportFileScanDuration(ev.Pool, ev.TenantID, ev.InterceptorRef, ev.Duration.Seconds())
	}

	var eventType string
	var billingType billingstore.EventType
	switch ev.Outcome {
	case interceptor.OutcomeRejected:
		eventType = EventTypeDelegationExportFileScanRejected
		billingType = billingstore.EventDelegationExportFileScanRejected
	case interceptor.OutcomeFailedOpen:
		eventType = EventTypeDelegationExportScanFailedOpen
		billingType = billingstore.EventDelegationExportScanFailedOpen
	default:
		return
	}
	// spec: §11.2.1 — tee the export-scan outcome into the per-tenant
	// billing stream alongside the §11.7 audit append.
	o.billing.Emit(ctx, billingfanout.ExportFileScan(billingType, ev.TenantID,
		ev.PolicyName, ev.InterceptorRef, ev.FilePath, ev.FileSize, ev.Reason))
	if o.appender == nil {
		return
	}
	// spec: §11.7 lines 119-122 — payload fields are policy_name,
	// interceptor_ref, file_path, file_size, and reason.
	payload, _ := json.Marshal(map[string]any{
		"policy_name":     ev.PolicyName,
		"interceptor_ref": ev.InterceptorRef,
		"file_path":       ev.FilePath,
		"file_size":       ev.FileSize,
		"reason":          ev.Reason,
	})
	_, _ = o.appender.Append(ctx, ev.TenantID, eventType, payload, o.clock())
}

// Ensure ExportScanObserver satisfies the interceptor observer at
// compile time.
var _ interceptor.ExportScanObserver = (*ExportScanObserver)(nil)
