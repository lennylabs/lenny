// SPDX-License-Identifier: MIT

package preflight

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/direct_mode_isolation"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// CredentialDeliveryPool is the credential-delivery projection of one
// SandboxTemplate pool definition. The scan pairs each pool's
// deliveryMode with the isolationProfile, spiffeBinding, and
// egressProfile authored on the same SandboxTemplate spec so the
// canonical Decide rule can evaluate a forbidden combination.
//
// spec: §4.9 — the SandboxTemplate pool definition is the resource that
// carries deliveryMode + spiffeBinding + isolationProfile.
type CredentialDeliveryPool struct {
	// Name is the SandboxTemplate (pool) name.
	Name string
	// DeliveryMode is the pool's §4.9 deliveryMode ("proxy" or "direct").
	DeliveryMode string
	// IsolationProfile is the pool's §5.3 isolationProfile.
	IsolationProfile string
	// SpiffeBinding is the pool's §4.9 spiffeBinding ("enabled" or
	// "disabled").
	SpiffeBinding string
	// EgressProfile is the pool's §13.2 egressProfile.
	EgressProfile string
}

// CheckCredentialDelivery scans every SandboxTemplate pool definition at
// install and upgrade time and fails the install fail-closed when any
// pool in a multi-tenant deployment carries a forbidden
// credential-delivery combination. It reuses the canonical
// direct_mode_isolation.Decide as the single decision rule the
// registration validation and the admission webhook already share, so a
// combination the preflight rejects is exactly the combination the
// webhook would reject on CRD apply. The scan enforces only in
// multi-tenant mode; a single-tenant or development deployment permits
// the combinations and the check passes cleanly.
//
// spec: §4.9 — "The lenny-preflight Job additionally scans all
// SandboxTemplate pool definitions at install and upgrade time and fails
// when any pool in a multi-tenant deployment carries deliveryMode: proxy
// + spiffeBinding: disabled." §13.1 — matching the shareProcessNamespace
// preflight pattern (fail-closed at install time).
func CheckCredentialDelivery(templates []CredentialDeliveryPool, tenancyMode string, devMode bool) Decision {
	if tenancyMode != direct_mode_isolation.TenancyMulti {
		return Decision{Passed: true}
	}
	for _, t := range templates {
		dec := direct_mode_isolation.Decide(direct_mode_isolation.Request{
			TenancyMode:      tenancyMode,
			DevMode:          devMode,
			Kind:             "SandboxTemplate " + t.Name,
			DeliveryMode:     t.DeliveryMode,
			IsolationProfile: t.IsolationProfile,
			SpiffeBinding:    t.SpiffeBinding,
			EgressProfile:    t.EgressProfile,
		})
		if !dec.Allowed {
			return Decision{Passed: false, Reason: dec.Reason}
		}
	}
	return Decision{Passed: true}
}

// gatherCredentialDeliveryPools lists the cluster's SandboxTemplate pools
// and projects each onto its credential-delivery fields. On a fresh
// install the SandboxTemplate CRD may not be applied yet; a missing kind
// or absent CRD is treated as "no pools to scan" so the scan never blocks
// a first install. Any other list error is returned so the caller fails
// the install fail-closed, inverting the advisory node-drain-timeout
// sibling that treats a read failure as a pass.
func gatherCredentialDeliveryPools(ctx context.Context, reader client.Reader) ([]CredentialDeliveryPool, error) {
	var list lennyv1.SandboxTemplateList
	if err := reader.List(ctx, &list); err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]CredentialDeliveryPool, 0, len(list.Items))
	for i := range list.Items {
		t := &list.Items[i]
		out = append(out, CredentialDeliveryPool{
			Name:             t.Name,
			DeliveryMode:     t.Spec.DeliveryMode,
			IsolationProfile: t.Spec.IsolationProfile,
			SpiffeBinding:    t.Spec.SpiffeBinding,
			EgressProfile:    t.Spec.EgressProfile,
		})
	}
	return out, nil
}
