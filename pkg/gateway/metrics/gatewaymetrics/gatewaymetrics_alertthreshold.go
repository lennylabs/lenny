// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// alertThresholdMetrics holds the §11.2.1 / §12.6 / §16.5 / §25.13 startup-set alert-threshold scalar gauges.
type alertThresholdMetrics struct {
	// billingCorrectionRateThreshold is the §11.2.1 / §16.5 startup-set
	// gauge that exposes the deployer-configurable percentage (default
	// 5%) the BillingCorrectionRateHigh alert reads via
	// scalar(lenny_billing_correction_rate_threshold). F-11.2.23.
	billingCorrectionRateThreshold prometheus.Gauge
	// eventBusDropAlertThreshold is the §12.6 line 683 / §16.5 startup-set
	// gauge that exposes the deployer-configurable per-minute dropped-
	// publish ceiling (default 10/min) the EventBusPublishDropped alert
	// reads via scalar(lenny_event_bus_drop_alert_threshold). F-12.6.23.
	eventBusDropAlertThreshold prometheus.Gauge
	// gatewayQueueDepthThreshold / gatewayLatencyThresholdSeconds /
	// credentialPoolLowThreshold expose the §25.13 line 4737
	// tier-dependent alert thresholds as startup-set gauges. The
	// §16.5 GatewayQueueDepthHigh, GatewayLatencyHigh, and
	// CredentialPoolLow alerts read each value via `scalar(...)` so
	// the bundled expressions resolve against operator-tunable inputs
	// rather than literal constants. F-25.13.2.
	gatewayQueueDepthThreshold     prometheus.Gauge
	gatewayLatencyThresholdSeconds prometheus.Gauge
	credentialPoolLowThreshold     prometheus.Gauge
	// sloBurnRateFastMultiplier / sloBurnRateSlowMultiplier expose the
	// §16.5 line 640 operator-configurable burn-rate window multipliers
	// (slo.burnRate.fastMultiplier default 14, slowMultiplier default 3).
	// Every burn-rate alert compares its budget-normalised ratio against
	// scalar(lenny_slo_burn_rate_{fast,slow}_multiplier or vector(default))
	// so the multipliers retune when the bundled alerts page without
	// regenerating the PrometheusRule. F-16.5.3.
	sloBurnRateFastMultiplier prometheus.Gauge
	sloBurnRateSlowMultiplier prometheus.Gauge
	// sessionUnavailabilityRatio is the §16.5 SessionAvailabilityBurnRate
	// SLI source: the fraction of active sessions currently in a
	// retry/recovery state (resume_pending, resuming,
	// awaiting_client_action). The gateway export loop refreshes it from
	// the session store. F-16.5.3.
	sessionUnavailabilityRatio prometheus.Gauge
	// auditSIEMConfigured / auditRetentionDays / envProduction are the
	// §16.4 / §16.5 startup-set scalar gauges the AuditSIEMNotConfigured
	// and AuditRetentionLow alerts read. auditSIEMConfigured is 1 when
	// audit.siem.endpoint is set (the SIEM-configured suppression term),
	// auditRetentionDays is the resolved §16.4 audit.retentionDays
	// window, and envProduction is 1 when LENNY_ENV=production. The
	// alerts gate on `lenny_env_production == 1` so they stay inert
	// outside production. F-16.4.9; F-16.4.10.
	auditSIEMConfigured prometheus.Gauge
	auditRetentionDays  prometheus.Gauge
	envProduction       prometheus.Gauge
}

// newAlertThresholdMetrics constructs, registers, and materializes the alertthreshold metric subsystem
// against reg. spec: §16 observability metrics.
func newAlertThresholdMetrics(reg *prometheus.Registry) (alertThresholdMetrics, error) {
	var m alertThresholdMetrics
	// spec: §11.2.1 line 187 — "deployer-configurable percentage (default
	// 5%)". The §16.5 BillingCorrectionRateHigh alert evaluates
	// lenny_billing_correction_rate_24h > scalar(lenny_billing_correction_rate_threshold);
	// the gateway emits this gauge at startup from the
	// billing.correctionRateThreshold Helm value. F-11.2.23.
	billingCorrectionRateThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_billing_correction_rate_threshold",
		Help: "Deployer-configurable BillingCorrectionRateHigh alert threshold as a fraction (§11.2.1 / §16.5; default 0.05).",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §12.6 line 683 — "the EventBusPublishDropped alert fires when
	// dropped-event rate exceeds eventBus.dropAlertThreshold (default
	// 10/min)". The §16.5 alert evaluates
	// rate(lenny_event_bus_publish_dropped_total[5m]) * 60 >
	// scalar(lenny_event_bus_drop_alert_threshold); the gateway emits this
	// gauge at startup from the eventBus.dropAlertThreshold Helm value.
	// F-12.6.23.
	eventBusDropAlertThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_event_bus_drop_alert_threshold",
		Help: "Deployer-configurable EventBusPublishDropped per-minute alert threshold (§12.6 line 683 / §16.5; default 10).",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §25.13 line 4737 — tier-dependent §16.5 thresholds. The
	// chart emits the configured Helm values into these gauges at
	// gateway startup; the bundled alert expressions read them via
	// `scalar(...)` so a tier preset tightening the threshold flows
	// through to the rendered manifest without re-rendering the rule
	// expressions. F-25.13.2.
	gatewayQueueDepthThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_queue_depth_threshold",
		Help: "Configured §16.5 GatewayQueueDepthHigh ceiling (§25.13 line 4737).",
	}, nil)
	if err != nil {
		return m, err
	}
	gatewayLatencyThresholdSeconds, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_latency_threshold_seconds",
		Help: "Configured §16.5 GatewayLatencyHigh p95 ceiling in seconds (§25.13 line 4737).",
	}, nil)
	if err != nil {
		return m, err
	}
	credentialPoolLowThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_credential_pool_low_threshold",
		Help: "Configured §16.5 CredentialPoolLow utilisation fraction (§25.13 line 4737).",
	}, nil)
	if err != nil {
		return m, err
	}
	sloBurnRateFastMultiplier, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_slo_burn_rate_fast_multiplier",
		Help: "Configured §16.5 line 640 fast-window burn-rate multiplier (slo.burnRate.fastMultiplier, default 14).",
	}, nil)
	if err != nil {
		return m, err
	}
	sloBurnRateSlowMultiplier, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_slo_burn_rate_slow_multiplier",
		Help: "Configured §16.5 line 640 slow-window burn-rate multiplier (slo.burnRate.slowMultiplier, default 3).",
	}, nil)
	if err != nil {
		return m, err
	}
	sessionUnavailabilityRatio, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_session_unavailability_ratio",
		Help: "Fraction of active sessions in a retry/recovery state, read by SessionAvailabilityBurnRate (§16.5).",
	}, nil)
	if err != nil {
		return m, err
	}
	// §16.4 / §16.5: the audit SIEM-configured suppression term, the
	// resolved general audit retention window, and the production-mode
	// indicator the AuditSIEMNotConfigured and AuditRetentionLow alerts
	// read. The gateway main sets them at startup from audit.siem.endpoint,
	// the resolved audit.retentionDays / audit.retentionPreset, and
	// LENNY_ENV. F-16.4.9; F-16.4.10.
	auditSIEMConfigured, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_audit_siem_configured",
		Help: "1 when audit.siem.endpoint is configured, 0 otherwise; suppresses AuditSIEMNotConfigured / AuditRetentionLow (§16.4 / §16.5).",
	}, nil)
	if err != nil {
		return m, err
	}
	auditRetentionDays, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_audit_retention_days",
		Help: "Resolved general (non-gdpr) audit-log retention window in days, from audit.retentionPreset or audit.retentionDays (§16.4).",
	}, nil)
	if err != nil {
		return m, err
	}
	envProduction, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_env_production",
		Help: "1 when LENNY_ENV=production, 0 otherwise; gates the production-only audit alerts (§16.5).",
	}, nil)
	if err != nil {
		return m, err
	}
	reg.MustRegister(billingCorrectionRateThreshold,
		eventBusDropAlertThreshold,
		gatewayQueueDepthThreshold,
		gatewayLatencyThresholdSeconds,
		credentialPoolLowThreshold,
		sloBurnRateFastMultiplier,
		sloBurnRateSlowMultiplier,
		sessionUnavailabilityRatio,
		auditSIEMConfigured,
		auditRetentionDays,
		envProduction)
	billingCorrectionRateThresholdChild := billingCorrectionRateThreshold.WithLabelValues()
	billingCorrectionRateThresholdChild.Set(0.05)
	eventBusDropAlertThresholdChild := eventBusDropAlertThreshold.WithLabelValues()
	eventBusDropAlertThresholdChild.Set(10)
	gatewayQueueDepthThresholdChild := gatewayQueueDepthThreshold.WithLabelValues()
	gatewayQueueDepthThresholdChild.Set(20)
	gatewayLatencyThresholdSecondsChild := gatewayLatencyThresholdSeconds.WithLabelValues()
	gatewayLatencyThresholdSecondsChild.Set(3.0)
	credentialPoolLowThresholdChild := credentialPoolLowThreshold.WithLabelValues()
	credentialPoolLowThresholdChild.Set(0.80)
	sloBurnRateFastMultiplierChild := sloBurnRateFastMultiplier.WithLabelValues()
	sloBurnRateFastMultiplierChild.Set(14)
	sloBurnRateSlowMultiplierChild := sloBurnRateSlowMultiplier.WithLabelValues()
	sloBurnRateSlowMultiplierChild.Set(3)
	sessionUnavailabilityRatioChild := sessionUnavailabilityRatio.WithLabelValues()
	auditSIEMConfiguredChild := auditSIEMConfigured.WithLabelValues()
	auditRetentionDaysChild := auditRetentionDays.WithLabelValues()
	auditRetentionDaysChild.Set(365)
	envProductionChild := envProduction.WithLabelValues()
	m.billingCorrectionRateThreshold = billingCorrectionRateThresholdChild
	m.eventBusDropAlertThreshold = eventBusDropAlertThresholdChild
	m.gatewayQueueDepthThreshold = gatewayQueueDepthThresholdChild
	m.gatewayLatencyThresholdSeconds = gatewayLatencyThresholdSecondsChild
	m.credentialPoolLowThreshold = credentialPoolLowThresholdChild
	m.sloBurnRateFastMultiplier = sloBurnRateFastMultiplierChild
	m.sloBurnRateSlowMultiplier = sloBurnRateSlowMultiplierChild
	m.sessionUnavailabilityRatio = sessionUnavailabilityRatioChild
	m.auditSIEMConfigured = auditSIEMConfiguredChild
	m.auditRetentionDays = auditRetentionDaysChild
	m.envProduction = envProductionChild
	return m, nil
}
