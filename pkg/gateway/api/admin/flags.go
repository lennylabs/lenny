// SPDX-License-Identifier: MIT

package admin

import (
	"flag"
	"os"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the admin router and the connector OAuth flow
// read: the capacity recommendations, the external adapter harness, the
// current mTLS CA, the ops service URL, the billing dual control, the legal
// hold escrow, and the connector OAuth callback and CA bundle.
type Flags struct {
	// RecommendationsDisabledRules is the --recommendations-disabled-rules flag.
	RecommendationsDisabledRules *string
	// RecommendationsWindowOverrides is the --recommendations-window-overrides flag.
	RecommendationsWindowOverrides *string
	// RecommendationsDisableOnOutage is the --recommendations-disable-on-prometheus-outage flag.
	RecommendationsDisableOnOutage *bool
	// ExternalAdapterHarnessPath is the --external-adapter-harness-path flag.
	ExternalAdapterHarnessPath *string
	// MTLSCurrentCAID is the --mtls-current-ca-id flag.
	MTLSCurrentCAID *string
	// OpsServiceURL is the --ops-service-url flag.
	OpsServiceURL *string
	// BillingDualControlThreshold is the --billing-dual-control-threshold flag.
	BillingDualControlThreshold *float64
	// BillingApproverWebhookURL is the --billing-approver-webhook-url flag.
	BillingApproverWebhookURL *string
	// LegalHoldEscrowBucket is the --legal-hold-escrow-bucket flag.
	LegalHoldEscrowBucket *string
	// LegalHoldEscrowEndpoint is the --legal-hold-escrow-endpoint flag.
	LegalHoldEscrowEndpoint *string
	// ConnectorOAuthCallbackURL is the --connector-oauth-callback-url flag.
	ConnectorOAuthCallbackURL *string
	// ConnectorOAuthCA is the --connector-oauth-ca flag.
	ConnectorOAuthCA *string
}

// RegisterFlags registers these flags on fs and returns their values, which
// fs fills when it parses. Each flag's default is read from its environment
// variable, as the composition root read it before the flags moved here.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	// §25.3 — operator knobs for the capacity
	// recommendations service. disabled-rules skips noisy rules across
	// every replica; window-overrides shrinks a rule's sliding window to
	// cut ring-buffer memory; disable-on-prometheus-outage fails closed
	// with RECOMMENDATIONS_UNAVAILABLE instead of computing from a
	// fallback reader. F-25.3.12.
	f.RecommendationsDisabledRules = fs.String("recommendations-disabled-rules",
		os.Getenv("LENNY_RECOMMENDATIONS_DISABLED_RULES"),
		"§25.3 — comma-separated recommendation rule IDs to disable across all replicas. Override via LENNY_RECOMMENDATIONS_DISABLED_RULES. F-25.3.12.")
	f.RecommendationsWindowOverrides = fs.String("recommendations-window-overrides",
		os.Getenv("LENNY_RECOMMENDATIONS_WINDOW_OVERRIDES"),
		"§25.3 — comma-separated per-category sliding-window overrides as category=duration (e.g. warm_pool_sizing=12h,credential_pool_sizing=72h). Override via LENNY_RECOMMENDATIONS_WINDOW_OVERRIDES. F-25.3.12.")
	f.RecommendationsDisableOnOutage = fs.Bool("recommendations-disable-on-prometheus-outage",
		envdefault.Flag("LENNY_RECOMMENDATIONS_DISABLE_ON_PROMETHEUS_OUTAGE"),
		"§25.3 — return 503 RECOMMENDATIONS_UNAVAILABLE instead of computing from a fallback reader when the metric source is unreachable. Override via LENNY_RECOMMENDATIONS_DISABLE_ON_PROMETHEUS_OUTAGE. F-25.3.12.")
	f.ExternalAdapterHarnessPath = fs.String("external-adapter-harness-path",
		os.Getenv("LENNY_EXTERNAL_ADAPTER_HARNESS_PATH"),
		"§24.8 / §15 — path to the lenny-compliance harness binary the external-adapter validate gate drives. When empty the gateway resolves `lenny-compliance` on $PATH; when the harness is absent, POST /v1/admin/external-adapters/{name}/validate returns 503 ADAPTER_VALIDATION_UNAVAILABLE and the adapter stays pending_validation. Override via LENNY_EXTERNAL_ADAPTER_HARNESS_PATH. F-24.8.2.")
	f.MTLSCurrentCAID = fs.String("mtls-current-ca-id", os.Getenv("LENNY_MTLS_CURRENT_CA_ID"),
		"§10.3 id of the cluster-internal CA that currently signs control-plane leaves (the chart's mtls.currentCAID, default lenny-mtls-ca when mtls.enabled). When set, the gateway serves the durable /v1/admin/ca-rotation procedure (begin/promote/retire) so an operator rotation is audited, overlap-window enforced, and resumable across restarts. Empty disables the CA-rotation admin surface (mTLS PKI disabled).")
	f.OpsServiceURL = fs.String("ops-service-url", os.Getenv("LENNY_OPS_SERVICE_URL"),
		"§25.14 public URL of the lenny-ops service (the ops.ingress.host Helm value). Advertised in GET /v1/admin/platform/version so lenny-ctl auto-discovers the ops endpoint. Override via LENNY_OPS_SERVICE_URL.")
	f.BillingDualControlThreshold = fs.Float64("billing-dual-control-threshold", envdefault.Float64("LENNY_BILLING_DUAL_CONTROL_THRESHOLD", 0),
		"§11.2.1 billing.dualControlThreshold: an operator-initiated billing correction whose absolute adjustment value exceeds this requires a second platform-admin's approval. The default of 0 makes every correction dual-control. Override via LENNY_BILLING_DUAL_CONTROL_THRESHOLD.")
	f.BillingApproverWebhookURL = fs.String("billing-approver-webhook-url", os.Getenv("LENNY_BILLING_APPROVER_WEBHOOK_URL"),
		"§11.2.1 billing.approverNotificationWebhook: when set, a billing correction entering the dual-control pending state notifies eligible approvers by POSTing a signed notification to this URL. Empty leaves the channel unconfigured. Override via LENNY_BILLING_APPROVER_WEBHOOK_URL. F-11.2.14.")
	// §12.8 — the single-region legal-hold escrow bucket the
	// Phase 3.5 force-delete override migrates held evidence into. When
	// `--legal-hold-escrow-bucket` is unset the deployment configures no
	// escrow, so a force-delete with acknowledgeHoldOverride fails closed
	// with LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE rather than destroying
	// held evidence with nowhere to segregate it (§12.8).
	f.LegalHoldEscrowBucket = fs.String("legal-hold-escrow-bucket", os.Getenv("LENNY_LEGAL_HOLD_ESCROW_BUCKET"),
		"§12.8 single-region legal-hold escrow bucket name. Empty disables the force-delete override (it fails closed with LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE).")
	f.LegalHoldEscrowEndpoint = fs.String("legal-hold-escrow-endpoint", os.Getenv("LENNY_LEGAL_HOLD_ESCROW_ENDPOINT"),
		"§12.8 legal-hold escrow bucket endpoint (S3-compatible).")
	f.ConnectorOAuthCallbackURL = fs.String("connector-oauth-callback-url", os.Getenv("LENNY_CONNECTOR_OAUTH_CALLBACK_URL"),
		"§9.3 absolute URL the connector OAuth provider redirects back to (the gateway's GET /v1/admin/connectors/oauth/callback). Wiring the connector OAuth 2.1 flow requires it. Override via LENNY_CONNECTOR_OAUTH_CALLBACK_URL.")
	f.ConnectorOAuthCA = fs.String("connector-oauth-ca", os.Getenv("LENNY_CONNECTOR_OAUTH_CA"),
		"path to a CA bundle that verifies the §9.3 connector OAuth provider's token-endpoint TLS certificate. Empty uses the system trust store. Set this for a provider behind a private CA. Override via LENNY_CONNECTOR_OAUTH_CA.")
	return f
}
