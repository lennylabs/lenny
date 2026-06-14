// SPDX-License-Identifier: MIT

package podclaim

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
// The write is a status-subresource Update with bounded conflict retry: a
// fresh CREATE has no competing status writer, but a concurrent
// WarmPoolController projection read or orphan GC may bump the
// resourceVersion, so a conflict re-reads and re-applies the bound state.
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
// §4.6.3 (gateway-owned SandboxClaim.status binding state).
func writeBoundStatus(ctx context.Context, cl client.Client, namespace, claimName string, now func() time.Time) error {
	const maxConflictRetries = 3
	if now == nil {
		now = time.Now
	}
	for attempt := 0; ; attempt++ {
		var cur lennyv1.SandboxClaim
		if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: claimName}, &cur); err != nil {
			return fmt.Errorf("podclaim: get claim %s for bound status: %w", claimName, err)
		}
		if cur.Status.Phase == string(claimstate.Bound) {
			// Idempotent: a retry after a partial success finds the claim
			// already bound and returns without a redundant write.
			return nil
		}
		cur.Status.Phase = string(claimstate.Bound)
		cur.Status.BindingStateTransitionTime = &metav1.Time{Time: now().UTC()}
		err := cl.Status().Update(ctx, &cur)
		if err == nil {
			return nil
		}
		if apierrors.IsConflict(err) && attempt < maxConflictRetries {
			continue
		}
		return fmt.Errorf("podclaim: write bound status on claim %s: %w", claimName, err)
	}
}
