// SPDX-License-Identifier: MIT

// Package label_immutability implements the pure-decision logic of the
// lenny-label-immutability ValidatingAdmissionWebhook per spec §17.2
// item 5 and §5.2 NET-003. The webhook is deployed with
// failurePolicy: Fail.
//
// The webhook enforces two distinct rules on agent-pod labels:
//
//  1. Three labels are strictly immutable on existing agent pods:
//     - lenny.dev/managed
//     - lenny.dev/delivery-mode
//     - lenny.dev/egress-profile
//     Any mutation (including unset → value or value → value) is
//     rejected with HTTP 403.
//
//  2. lenny.dev/tenant-id has constrained transitions:
//     - unset → {tenant_id} : allowed only for the gateway SA on
//     initial first-assignment of a pod to a tenant.
//     - {tenant_id} → "unassigned" : allowed only for the
//     WarmPoolController SA when a pod is returned to the pool.
//     - {tenant_id} → {different_tenant_id} : always rejected.
//     - {tenant_id} → unset, "unassigned" → {tenant_id} etc are
//     outside the documented permitted transitions and are rejected.
//
// The webhook binary (a later phase) wraps this package with a
// k8s.io/api/admission/v1 handler that extracts old and new pod label
// maps and the AdmissionRequest's UserInfo, then calls Decide.
package label_immutability

import (
	"errors"
	"fmt"
)

// Canonical label keys watched by the webhook.
const (
	LabelManaged       = "lenny.dev/managed"
	LabelDeliveryMode  = "lenny.dev/delivery-mode"
	LabelEgressProfile = "lenny.dev/egress-profile"
	LabelDNSPolicy     = "lenny.dev/dns-policy"
	LabelTenantID      = "lenny.dev/tenant-id"
)

// UnassignedTenantID is the sentinel tenant-id value used when a pod
// has been returned to the warm pool. The transition
// {tenant_id} → unassigned is the only legal teardown edge.
const UnassignedTenantID = "unassigned"

// ServiceAccount usernames whose admission UserInfo grants the
// privileged transitions. The strings match §10.3 / §17.2 mentions.
const (
	GatewayServiceAccount = "system:serviceaccount:lenny-system:lenny-gateway"
	WarmPoolControllerSA  = "system:serviceaccount:lenny-system:lenny-controller"
	// PoolScalingControllerSA is included for symmetry; the
	// label-immutability webhook does not authorize any tenant-id
	// transition for it (only field-ownership matters), but tests
	// reference the constant.
	PoolScalingControllerSA = "system:serviceaccount:lenny-system:lenny-pool-scaling-controller"
)

// Request is the input to Decide. The webhook builds it from the
// AdmissionReview before calling Decide; the package itself stays
// transport-agnostic.
type Request struct {
	// OldLabels is the label map of the existing pod object. nil for
	// CREATE requests; the immutability rules permit any initial value
	// on CREATE.
	OldLabels map[string]string

	// NewLabels is the label map of the inbound write.
	NewLabels map[string]string

	// UserInfoUsername is the AdmissionRequest's userInfo.username,
	// used to authorize the tenant-id transitions. The API server
	// populates this from the authenticated principal; it cannot be
	// spoofed by the caller.
	UserInfoUsername string
}

// Decision is the admission outcome.
type Decision struct {
	Allowed bool
	Reason  string
	Code    int
}

// ErrMissingNewLabels is returned by Decide when NewLabels is nil. A
// pod write should always include labels (even an empty map).
var ErrMissingNewLabels = errors.New("label_immutability: NewLabels is required")

// Decide applies the §17.2 / §5.2 NET-003 rules and returns the
// admission outcome.
//
// Behaviour:
//   - For CREATE (OldLabels == nil), only the tenant-id authorization
//     rule applies: setting tenant-id to a non-empty value on initial
//     create is allowed only for the gateway SA (the same
//     `unset → {tenant_id}` transition NET-003 enforces; CREATE is
//     the canonical instance of that transition).
//   - For UPDATE (OldLabels != nil), the three strictly-immutable
//     labels must not change, and the tenant-id transition rules apply.
//
// Decide is the composition of DecideImmutableLabels (the §17.2 item 5
// rule set) and DecideTenantTransition (the §5.2 line 392 / NET-003
// rule set). It is preserved as the single-call entry point for
// callers that want both rule sets evaluated against one Request; the
// production webhooks each call one of the two split deciders directly
// so the two ValidatingWebhookConfigurations the spec names
// (lenny-label-immutability and lenny-tenant-label-immutability) hold
// independent decision paths.
func Decide(r Request) (Decision, error) {
	if d, err := DecideImmutableLabels(r); err != nil || !d.Allowed {
		return d, err
	}
	return DecideTenantTransition(r)
}

// DecideImmutableLabels enforces the §17.2 item 5 rule that the
// canonical egress-governing labels (lenny.dev/managed,
// lenny.dev/delivery-mode, lenny.dev/egress-profile, lenny.dev/dns-policy)
// are immutable on existing agent pods. spec: §13.2 line 180 — mutating
// lenny.dev/egress-profile from `restricted` to `internet`, or adding
// lenny.dev/dns-policy to a non-opted-out pod, would broaden egress or
// bypass the dedicated CoreDNS instance without re-admission through the
// pool controller. The gateway-tenant-id transition rules belong to
// DecideTenantTransition.
//
// spec: §17.2 item 5; §13.2 line 180.
func DecideImmutableLabels(r Request) (Decision, error) {
	if r.NewLabels == nil {
		return Decision{}, ErrMissingNewLabels
	}
	for _, key := range []string{LabelManaged, LabelDeliveryMode, LabelEgressProfile, LabelDNSPolicy} {
		if r.OldLabels == nil {
			continue
		}
		oldV := r.OldLabels[key]
		newV := r.NewLabels[key]
		if oldV != newV {
			return Decision{
				Allowed: false,
				Reason:  fmt.Sprintf("label %s is immutable; %q → %q is forbidden", key, oldV, newV),
				Code:    403,
			}, nil
		}
	}
	return Decision{Allowed: true, Code: 200}, nil
}

// DecideTenantTransition enforces the §5.2 line 392 / NET-003 rule set
// on lenny.dev/tenant-id transitions. The webhook config that calls
// this decider is the spec-named lenny-tenant-label-immutability.
//
// spec: §5.2 line 392; §13.2 line 498.
func DecideTenantTransition(r Request) (Decision, error) {
	if r.NewLabels == nil {
		return Decision{}, ErrMissingNewLabels
	}
	var oldTenant string
	if r.OldLabels != nil {
		oldTenant = r.OldLabels[LabelTenantID]
	}
	newTenant := r.NewLabels[LabelTenantID]

	if oldTenant == newTenant {
		return Decision{Allowed: true, Code: 200}, nil
	}

	switch {
	case oldTenant == "" && newTenant != "":
		// Initial assignment: only the gateway SA may set tenant-id.
		// `unassigned` is not a real tenant id; this transition is
		// allowed but rare — controllers may use it during pool
		// initialization.
		if r.UserInfoUsername != GatewayServiceAccount {
			return Decision{
				Allowed: false,
				Reason:  fmt.Sprintf("tenant_label_immutable: only %s may set %s from unset to %q (got user %q)", GatewayServiceAccount, LabelTenantID, newTenant, r.UserInfoUsername),
				Code:    403,
			}, nil
		}
		return Decision{Allowed: true, Code: 200}, nil

	case oldTenant != "" && newTenant == UnassignedTenantID:
		// Pool return: only the WarmPoolController SA may reset
		// tenant-id to "unassigned".
		if r.UserInfoUsername != WarmPoolControllerSA {
			return Decision{
				Allowed: false,
				Reason:  fmt.Sprintf("tenant_label_immutable: only %s may reset %s from %q to %q (got user %q)", WarmPoolControllerSA, LabelTenantID, oldTenant, UnassignedTenantID, r.UserInfoUsername),
				Code:    403,
			}, nil
		}
		return Decision{Allowed: true, Code: 200}, nil

	default:
		// Every other transition — including {tenant_id} → different
		// {tenant_id}, {tenant_id} → unset, and unassigned → {tenant_id}
		// — is forbidden.
		return Decision{
			Allowed: false,
			Reason:  fmt.Sprintf("tenant_label_immutable: %s transition %q → %q is not a permitted edge", LabelTenantID, oldTenant, newTenant),
			Code:    403,
		}, nil
	}
}
