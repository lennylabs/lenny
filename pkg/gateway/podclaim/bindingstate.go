// SPDX-License-Identifier: MIT

package podclaim

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// writeBoundStatus stamps the first binding state (`bound`) on a freshly
// created per-pod SandboxClaim. The claim is CREATEd with spec only,
// because a Kubernetes status subresource is not writable by the resource
// Create call (§4.6.1 "created with spec only, and the gateway writes the
// first binding state (`bound`) with a subsequent status patch"); this
// helper writes that first status. It also stamps the binding-state
// transition time, which the WarmPoolController orphan GC keys its
// live-binding-state reclaim predicate on (§4.6.1 / §4.6.3).
//
// The write is a status-subresource PATCH via Server-Side Apply under the
// gateway field manager (§4.6.3 grants the gateway `get`/`patch` on the
// `sandboxclaims/status` subresource and no `update` verb, so the write
// must be a PATCH; controller-runtime's Status().Update issues an HTTP PUT
// the API server authorizes against `update`). The gateway is the sole
// writer of SandboxClaim.status (the WarmPoolController consumes the
// binding state as projection input but never writes it, §4.6.3), so the
// apply is naturally idempotent and conflict-free without a retry loop.
// The supplied now() clock makes the transition stamp testable.
//
// WriteBoundStatus writes the first `bound` binding-state status patch on a
// freshly created per-pod SandboxClaim. It is exported for the §4.6.1
// Postgres-backed fallback claim path, which CREATEs the per-pod claim
// outside the in-cluster claim path and must write the same first binding
// state. spec: §4.6.1 (fallback claim path; first `bound` status patch).
func WriteBoundStatus(ctx context.Context, cl client.Client, namespace, claimName string) error {
	return writeBoundStatus(ctx, cl, namespace, claimName, time.Now)
}

// spec: §4.6.1 (pod claim mechanism; first `bound` status patch);
// §4.6.3 (gateway-owned SandboxClaim.status binding state via the
// `patch`-only sandboxclaims/status grant).
func writeBoundStatus(ctx context.Context, cl client.Client, namespace, claimName string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	// Idempotent: a retry after a partial success finds the claim already
	// bound and returns without re-stamping the transition time. A NotFound
	// surfaces as an error so the caller can undo the counter reservation.
	var cur lennyv1.SandboxClaim
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: claimName}, &cur); err != nil {
		return fmt.Errorf("podclaim: get claim %s for bound status: %w", claimName, err)
	}
	if cur.Status.Phase == string(claimstate.Bound) {
		return nil
	}
	// §4.6.3 — write only status.phase and status.bindingStateTransitionTime
	// via an SSA apply (PATCH verb) under the gateway field manager. The
	// claim is CREATEd with spec only, so this apply establishes the first
	// binding state. ForceOwnership is not needed: the gateway is the sole
	// writer of SandboxClaim.status.
	patch := &lennyv1.SandboxClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: lennyv1.GroupVersion.String(),
			Kind:       "SandboxClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: namespace,
		},
	}
	patch.Status.Phase = string(claimstate.Bound)
	patch.Status.BindingStateTransitionTime = &metav1.Time{Time: now().UTC()}
	if err := cl.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.Gateway))); err != nil {
		return fmt.Errorf("podclaim: write bound status on claim %s: %w", claimName, err)
	}
	return nil
}
