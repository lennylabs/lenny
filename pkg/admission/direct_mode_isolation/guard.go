// SPDX-License-Identifier: MIT

// Package direct_mode_isolation implements the pure-decision logic of
// the lenny-direct-mode-isolation ValidatingAdmissionWebhook per spec
// §4.9 and §13.2. The webhook is deployed fail-closed in front of
// SandboxTemplate and CredentialPool resources in agent namespaces and
// is rendered only when the features.llmProxy chart flag is enabled.
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
// The webhook enforces only in multi-tenant mode. In single-tenant mode
// or development mode both configurations are permitted (the warm pool
// controller's pool-registration validation handles the opt-in fields
// there), so the decision is an allow outside multi-tenant mode.
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
)

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

// Decide applies the §4.9 multi-tenant credential-delivery rules. It
// allows every resource outside multi-tenant mode; in multi-tenant mode
// it rejects the direct/standard and proxy/spiffe-disabled
// combinations. The opt-in fields that permit those combinations in
// single-tenant mode do not rescue them here — the webhook enforces
// regardless of any opt-in field value.
func Decide(r Request) Decision {
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
