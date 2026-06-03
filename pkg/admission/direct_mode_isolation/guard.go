// SPDX-License-Identifier: MIT

// Package direct_mode_isolation implements the pure-decision logic of
// the lenny-direct-mode-isolation ValidatingAdmissionWebhook per spec
// §4.9 and §13.2. The webhook is deployed fail-closed in front of
// SandboxTemplate resources in agent namespaces and renders
// unconditionally (§13.2 line 440 step 2). The credential-delivery
// fields it inspects (deliveryMode, isolationProfile, spiffeBinding,
// egressProfile) are authored only on the SandboxTemplate spec. A
// SandboxWarmPool references a template by name (templateRef) and
// carries none of these fields, and the per-session Sandbox CR is a
// controller-produced copy-down from an already-validated template, so
// the SandboxTemplate is the only admitted resource that can carry a
// forbidden combination. Scoping the chart rule to sandboxtemplates is
// therefore complete; matching SandboxWarmPool or core Pods would add
// rules with no fields to inspect. spec: §17.2 line 47. F-17.2.14.
//
// In a multi-tenant deployment two credential-delivery configurations
// expose cross-tenant credential risk and are rejected:
//
//	(1) deliveryMode: direct with isolationProfile: standard — a runc
//	    container escape reaches materialized credential material on the
//	    host node, which in a multi-tenant cluster is cross-tenant
//	    credential exposure.
//	(2) deliveryMode: proxy with spiffeBinding: disabled — disabling
//	    SPIFFE-binding removes the defense against cross-pod lease-token
//	    replay.
//
// Those two checks enforce only in multi-tenant mode. In single-tenant
// mode or development mode both configurations are permitted (the warm
// pool controller's pool-registration validation handles the opt-in
// fields there), so the decision is an allow outside multi-tenant mode.
//
// A third combination is rejected in every tenancy mode:
//
//	(3) deliveryMode: proxy with egressProfile: provider-direct — the
//	    §13.2 NET-006 mutual exclusivity. Proxy mode keeps API keys off
//	    the pod by routing LLM traffic through the gateway, but
//	    provider-direct egress opens a CIDR path from the pod straight to
//	    the same provider endpoints, a silent bypass. The two settings
//	    are mutually exclusive regardless of tenancy, so this check runs
//	    before the multi-tenant gate.
//
// The decision logic is split from the webhook HTTP/AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack.
package direct_mode_isolation

import "fmt"

// Rejection codes stamped on a denied admission, per §4.9.
const (
	// RejectDirectModeStandardIsolation rejects deliveryMode: direct
	// combined with isolationProfile: standard in multi-tenant mode.
	RejectDirectModeStandardIsolation = "DirectModeStandardIsolationMultiTenantRejected"
	// RejectProxyModeSpiffeBindingDisabled rejects deliveryMode: proxy
	// combined with spiffeBinding: disabled in multi-tenant mode.
	RejectProxyModeSpiffeBindingDisabled = "ProxyModeSpiffeBindingDisabledMultiTenantRejected"
	// RejectInvalidPoolEgressDeliveryCombo rejects deliveryMode: proxy
	// combined with egressProfile: provider-direct in any tenancy mode,
	// the §13.2 NET-006 mutual exclusivity.
	RejectInvalidPoolEgressDeliveryCombo = "InvalidPoolEgressDeliveryCombo"
)

// EgressProviderDirect is the §13.2 egressProfile value that opens a
// direct CIDR egress path from the pod to LLM provider endpoints. It is
// mutually exclusive with deliveryMode: proxy (NET-006).
const EgressProviderDirect = "provider-direct"

// DeliveryProxy is the §4.9 deliveryMode value that routes LLM traffic
// through the gateway proxy rather than letting the pod hold an API key.
const DeliveryProxy = "proxy"

// TenancyMulti is the platform tenancy.mode value that activates
// webhook enforcement.
const TenancyMulti = "multi"

// Request is the input to Decide: the platform tenancy configuration
// plus the credential-delivery fields of the admitted SandboxTemplate
// or CredentialPool resource.
type Request struct {
	// TenancyMode is the platform tenancy.mode setting. Enforcement is
	// active only when it equals "multi".
	TenancyMode string
	// DevMode is global.devMode. When true the permissive single-tenant
	// rules apply even if TenancyMode is "multi".
	DevMode bool
	// Kind is the admitted resource kind, used in rejection messages.
	Kind string
	// DeliveryMode is the resource's §4.9 deliveryMode ("direct" or
	// "proxy"). Empty when the resource declares none.
	DeliveryMode string
	// IsolationProfile is the resource's §5.3 isolationProfile.
	IsolationProfile string
	// SpiffeBinding is the resource's §4.9 spiffeBinding setting
	// ("enabled" or "disabled"). Empty selects the default, which is
	// enabled for proxy-mode pools.
	SpiffeBinding string
	// EgressProfile is the resource's §13.2 egressProfile
	// ("restricted", "provider-direct", or "internet"). Empty selects
	// the default ("restricted"). Combining "provider-direct" with
	// DeliveryMode "proxy" is rejected under NET-006.
	EgressProfile string
}

// Decision is the admission outcome.
type Decision struct {
	// Allowed is true when the resource's credential-delivery
	// configuration is permitted under the platform tenancy mode.
	Allowed bool
	// Reason is the rejection message relayed to the offending client
	// when Allowed is false; empty when Allowed is true.
	Reason string
	// Code is the HTTP status the webhook surfaces: 200 on allow, 403
	// on rejection.
	Code int
}

// Decide applies the §4.9 / §13.2 credential-delivery rules. The §13.2
// NET-006 proxy/provider-direct mutual exclusivity is rejected in every
// tenancy mode; the §4.9 direct/standard and proxy/spiffe-disabled
// combinations are rejected only in multi-tenant mode. The opt-in fields
// that permit the multi-tenant-gated combinations in single-tenant mode
// do not rescue them here — the webhook enforces regardless of any
// opt-in field value.
func Decide(r Request) Decision {
	// spec: §13.2 line 438 (NET-006) — proxy + provider-direct is an
	// incoherent security posture in any tenancy mode, so this check is
	// not gated on enforced(). The proxy path is designed to keep API
	// keys off the pod; provider-direct egress would hand the pod a
	// bypass route to the same provider CIDRs.
	if r.DeliveryMode == DeliveryProxy && r.EgressProfile == EgressProviderDirect {
		return reject(RejectInvalidPoolEgressDeliveryCombo, r.Kind,
			"deliveryMode: proxy with egressProfile: provider-direct is mutually exclusive "+
				"(NET-006): proxy mode routes LLM traffic through the gateway to keep API keys "+
				"off the pod, but provider-direct egress opens a direct CIDR path to the same "+
				"provider endpoints. Use deliveryMode: proxy with egressProfile: restricted, or "+
				"deliveryMode: direct with egressProfile: provider-direct.")
	}
	if !enforced(r) {
		return Decision{Allowed: true, Code: 200}
	}
	if r.DeliveryMode == "direct" && r.IsolationProfile == "standard" {
		return reject(RejectDirectModeStandardIsolation, r.Kind,
			"deliveryMode: direct with isolationProfile: standard is not permitted "+
				"in multi-tenant mode. Use deliveryMode: proxy, or set isolationProfile: "+
				"sandboxed or microvm.")
	}
	if r.DeliveryMode == "proxy" && r.SpiffeBinding == "disabled" {
		return reject(RejectProxyModeSpiffeBindingDisabled, r.Kind,
			"deliveryMode: proxy with spiffeBinding: disabled is not permitted in "+
				"multi-tenant mode. Remove spiffeBinding: disabled, or set tenancy.mode: "+
				"single / global.devMode: true.")
	}
	return Decision{Allowed: true, Code: 200}
}

// enforced reports whether the webhook rejects unsafe combinations for
// this request. Enforcement is active in multi-tenant mode unless
// development mode is set, which §4.9 lists alongside single-tenant
// mode as a permissive deployment.
func enforced(r Request) bool {
	return r.TenancyMode == TenancyMulti && !r.DevMode
}

// reject builds a denied Decision with the rejection code, the resource
// kind, and the §4.9 remediation message.
func reject(code, kind, msg string) Decision {
	if kind == "" {
		kind = "resource"
	}
	return Decision{
		Allowed: false,
		Code:    403,
		Reason:  fmt.Sprintf("%s: %s %s", code, kind, msg),
	}
}
