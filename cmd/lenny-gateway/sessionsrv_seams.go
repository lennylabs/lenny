// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/derivelock"
)

// experimentRejectionReporter bridges a §10.7 ExperimentRouter
// fail-closed rejection to the §11.7 audit chain, the §16.1 metrics
// registry, and the §25.3 operational-event buffer: it records the
// `experiment.isolation_mismatch` event on all three and increments
// `lenny_experiment_isolation_rejections_total`.
type experimentRejectionReporter struct {
	audit   admin.AuditSink
	metrics *gatewaymetrics.Metrics
	emitter events.EventEmitter
}

func (e experimentRejectionReporter) ReportExperimentIsolationRejection(ctx context.Context, ev sessionserver.ExperimentIsolationRejection) {
	if e.metrics != nil {
		e.metrics.RecordExperimentIsolationRejection(ev.TenantID, ev.ExperimentID, ev.VariantID)
	}
	detail := map[string]any{
		"tenant_id":            ev.TenantID,
		"user_id":              ev.UserID,
		"experiment_id":        ev.ExperimentID,
		"variant_id":           ev.VariantID,
		"sessionMinIsolation":  ev.SessionMinIsolation,
		"variantPoolIsolation": ev.VariantPoolIsolation,
	}
	if e.audit != nil {
		e.audit.EmitAdminEvent(ctx, admin.AuditEvent{
			Type:           "experiment.isolation_mismatch",
			ActorTenantID:  ev.TenantID,
			TargetResource: ev.ExperimentID,
			Detail:         detail,
		})
	}
	// §16.6: the rejection is also an operational event — surface it on
	// the §25.3 event buffer so ops agents observe it without log scraping.
	if e.emitter != nil {
		data, _ := json.Marshal(detail)
		_ = e.emitter.Emit(ctx, events.OperationalEvent{
			Source:          "/v1/sessions",
			Type:            events.EventExperimentIsolationMismatch.CloudEventsType(),
			Severity:        "warning",
			DataContentType: "application/json",
			Data:            data,
		})
	}
}

// ObserveTargetingDuration records the §16.1
// lenny_experiment_targeting_duration_seconds histogram.
func (e experimentRejectionReporter) ObserveTargetingDuration(_ context.Context, provider string, seconds float64) {
	if e.metrics != nil {
		e.metrics.ObserveExperimentTargetingDuration(provider, seconds)
	}
}

// RecordTargetingError increments the §16.1
// lenny_experiment_targeting_error_total counter.
func (e experimentRejectionReporter) RecordTargetingError(_ context.Context, provider, errorType string) {
	if e.metrics != nil {
		e.metrics.RecordExperimentTargetingError(provider, errorType)
	}
}

// deriveDowngradeBillingAuditor implements
// sessionserver.DeriveAuditSink. The §7.1 derive rule 5
// derive.isolation_downgrade event is enumerated in the §11.2.1 billing
// event set but not in the §16.7 audit catalog, so it is emitted to the
// per-tenant billing stream (an append-only record matching the audit
// log integrity model) rather than the §11.7 hash chain — the same
// closed-catalog discipline as F-9.2.11. spec: §11.2.1; §7.1 rule 5.
// F-11.2.1.
type deriveDowngradeBillingAuditor struct {
	billing *billingfanout.Emitter
}

func (a deriveDowngradeBillingAuditor) EmitDeriveIsolationDowngrade(ctx context.Context, ev sessionserver.DeriveIsolationDowngradeEvent) {
	a.billing.Emit(ctx, billingfanout.DeriveIsolationDowngrade(
		ev.TenantID, ev.SourceSessionID, string(ev.SourceIsolationProfile),
		ev.TargetPool, string(ev.TargetIsolationProfile), ev.AuthorizingUserSubject, ev.TicketID,
	))
}

// sessionLifecycleAuditor adapts the gateway audit appender to the
// sessionserver.LifecycleAuditSink interface. It writes the §7.1 /
// §16.6 session lifecycle events (session.created and the terminal
// session.{completed,failed,cancelled,expired}) to the §11.7
// hash-chained audit log under the session's own tenant partition. The
// tenant is taken from the session-derived event, satisfying the §11.7 write-time tenant-validation rule. The OCSF mapping maps
// these event types to API Activity (6003).
type sessionLifecycleAuditor struct {
	appender policy.AuditAppender
}

func (a sessionLifecycleAuditor) EmitSessionLifecycle(ctx context.Context, ev sessionserver.SessionLifecycleEvent) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id": ev.SessionID,
		"user_sub":   ev.UserID,
		"runtime":    ev.RuntimeRef,
		"state":      ev.State,
	}
	if ev.FailureClass != "" {
		payload["failure_class"] = ev.FailureClass
	}
	if ev.Detail != "" {
		// spec: §7.1 — workspaceSealFailed records the last MinIO
		// export error in the detail field.
		payload["detail"] = ev.Detail
	}
	if ev.Outcome != "" {
		// spec: §13.4; §11.7 — the §16.6 session.upload boundary records
		// accepted/rejected so the SIEM stream carries the upload-rejection
		// class; the rejected row pairs the outcome with a sub-code reason.
		// F-13.4.8.
		payload["outcome"] = ev.Outcome
	}
	if ev.Reason != "" {
		payload["reason"] = ev.Reason
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	at := ev.At
	if at.IsZero() {
		at = clockinject.Now().UTC()
	}
	_, _ = a.appender.Append(ctx, ev.TenantID, ev.EventType, json.RawMessage(data), at)
}

// interactionResolutionAuditor adapts the gateway audit appender to
// the sessionserver.InteractionAuditSink interface. It writes the
// §7.2 / §11.7 / §16.7 tool-use approve/deny and elicitation
// respond/dismiss events to the hash-chained audit log under the
// session's own tenant partition. The tenant is taken from the
// session-derived event, satisfying the §11.7 write-time
// tenant-validation rule. The OCSF mapping maps these event types to
// API Activity (6003). spec: §7.2. F-7.2.8.
type interactionResolutionAuditor struct {
	appender policy.AuditAppender
}

func (a interactionResolutionAuditor) EmitInteractionResolution(ctx context.Context, ev sessionserver.InteractionResolutionEvent) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id":     ev.SessionID,
		"user_sub":       ev.UserID,
		"interaction_id": ev.InteractionID,
		"phase":          ev.Phase,
	}
	if ev.Reason != "" {
		// §15.1 deny body — the optional dismissal reason recorded so
		// the post-incident reconstruction can show why a tool call was
		// denied.
		payload["reason"] = ev.Reason
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	at := ev.At
	if at.IsZero() {
		at = clockinject.Now().UTC()
	}
	_, _ = a.appender.Append(ctx, ev.TenantID, ev.EventType, json.RawMessage(data), at)
}

// defaultDeriveLock picks the §7.1 derive-lock implementation.
// Redis-backed serialization is mandatory across replicas; the in-
// process Memory fallback is correct for the minimal-gateway and
// single-replica deployments (the in-memory store mutex inside
// derive.go is the only other serialization path in v1, and it
// serializes by accident — not by spec). F-7.1.12.
func defaultDeriveLock(client redis.UniversalClient) derivelock.Lock {
	if client != nil {
		return derivelock.NewRedis(client)
	}
	return derivelock.NewMemory(derivelock.DefaultWait)
}
