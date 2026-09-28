// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// runStartupChainContinuityCheck implements the §12.3 startup
// chain-continuity check: it re-verifies the most recent lastN audit
// rows of every tenant's hash chain, increments
// lenny_audit_chain_integrity_total per tenant by §11.7 state, and logs
// the spec WARN message for each broken chain. Under the §11.7 nextval
// allocator a benign transaction-rollback gap keeps its prev_hash link
// intact and verifyChainWindow reports it detected-but-not-broken, so
// only a non-linking prev_hash across the gap (a committed audit row
// tampered with or removed) reaches the boundary-populated WARN branch.
// A broken chain fires the §16.5 AuditChainGap alert through the metric;
// the gateway does not refuse to start. spec: §12.3, §11.7.
// F-12.3.9. F-11.2.10.
func runStartupChainContinuityCheck(ctx context.Context, db, ctrlDB integrity.Querier, lastN int, m *gatewaymetrics.Metrics) {
	// db is the ledger instance holding audit_log (the separate §12.3
	// billing/audit Postgres when configured, otherwise the primary);
	// ctrlDB is the control-plane pool where the tenants.state deletion
	// skip-set is authoritative, so the retained gdpr.*-only remnant of a
	// tenant in state='deleting' or state='deleted' does not raise a false
	// §16.5 AuditChainGap alert. In the co-located topology the call site
	// passes the same pool for both. spec: §12.3, §12.8.
	results, err := integrity.CheckChainContinuityRecent(ctx, db, ctrlDB, lastN)
	if err != nil {
		log.Printf("lenny-gateway: WARNING: §12.3 startup audit chain-continuity check could not run: %v", err)
		return
	}
	for _, r := range results {
		m.IncAuditChainIntegrity(string(r.Result.Integrity))
		if !r.Broken() {
			continue
		}
		if r.GapHighSeq() > 0 {
			// After the §11.7 nextval switch the audit sequence_number is
			// allocated by nextval, so a benign transaction-rollback gap has
			// an intact prev_hash link and verifyChainWindow classifies it
			// detected-but-not-broken (no boundaries populated). The only
			// boundary-populated ChainBroken (GapHighSeq() > 0) is a
			// non-linking prev_hash across the gap — a committed audit row
			// tampered with or removed — never buffered-T2 loss, which is the
			// accepted unsignaled tradeoff of the opt-in audit.batchingEnabled
			// path and carries no chain-level signal. The message matches the
			// §12.3 WARN string verbatim; the surrounding §12.3 prose
			// directs the operator to reconcile against the independent SIEM
			// copy and document the break in their compliance records.
			// spec: §12.3, §11.7. F-11.2.10.
			log.Printf("Audit chain broken for tenant %s: prev_hash does not link across sequence %d to %d (~%s to %s). This indicates a committed audit row was tampered with or removed. T3/T4 events are synchronous and will not appear in chain gaps.",
				r.TenantID, r.GapLowSeq(), r.GapHighSeq(),
				r.GapStart().Format(time.RFC3339), r.GapEnd().Format(time.RFC3339))
			continue
		}
		log.Printf("lenny-gateway: WARNING: §12.3 audit chain broken for tenant %s at sequence %d: %s",
			r.TenantID, r.Result.BreakSeq, r.Result.Detail)
	}
}

// ocsfMetricsAdapter bridges the §11.7 OCSF translator's metric surface
// onto the gateway's Prometheus registry: a per-row translation failure
// advances lenny_audit_ocsf_translation_failed_total labeled by event
// type and ocsf.ErrorClass. Success and dead-letter counts stay on the
// translator's in-memory CountingMetrics (no dedicated Prometheus series
// exists for them in the §16.1 catalog). F-11.7.1 / F-11.7.15.
type ocsfMetricsAdapter struct{ metrics *gatewaymetrics.Metrics }

func (a ocsfMetricsAdapter) TranslationFailed(eventType string, class ocsf.ErrorClass) {
	a.metrics.IncAuditOCSFTranslationFailed(eventType, string(class))
}

func (a ocsfMetricsAdapter) TranslationSucceeded(string) {}

func (a ocsfMetricsAdapter) DeadLettered(string) {}

// auditRetentionMetrics adapts the gateway metrics object to the
// auditretention.MetricsSink. Only the §16.1-cataloged
// lenny_audit_partition_drop_blocked gauge is exported through
// Prometheus; the per-sweep rows-pruned and run-outcome counts are not
// §16.1 series and are surfaced through the pruner's onTick log line, so
// those two sink methods are deliberate no-ops. spec: §16.4.
type auditRetentionMetrics struct{ m *gatewaymetrics.Metrics }

func (auditRetentionMetrics) AddAuditRowsPruned(int)      {}
func (auditRetentionMetrics) IncAuditRetentionRun(string) {}

func (a auditRetentionMetrics) SetAuditPartitionDropBlocked(partition string, blocked bool) {
	a.m.SetAuditPartitionDropBlocked(partition, blocked)
}

// auditPruneTenants enumerates the audit chains the §16.4 retention
// sweep covers: every registered tenant plus the "platform"
// pseudo-tenant, which carries platform-admin audit rows (e.g.
// compliance.profile_decommissioned) that are not keyed to a registered
// tenant row but still age past the retention window. F-11.7.17.
type auditPruneTenants struct {
	store tenantstore.Store
}

func (a auditPruneTenants) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := a.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows)+1)
	out = append(out, "platform")
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

// auditBatchingNoSIEM reports the §12.3 AuditBatchingNoSIEM
// condition: production mode has T2 audit batching enabled but no SIEM
// endpoint, so buffered T2 audit events would be lost on a crash with
// no external durable copy to recover from. F-12.3.15.
func auditBatchingNoSIEM(env string, batchingEnabled, siemConfigured bool) bool {
	return env == "production" && batchingEnabled && !siemConfigured
}
