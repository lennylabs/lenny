// SPDX-License-Identifier: MIT

package podclaim

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// statusFieldOwner is the gateway field manager every SandboxClaim.status
// write carries. spec: §4.6.3 grants the gateway `patch` (not `update`) on
// the `sandboxclaims/status` subresource, so every binding-state write is a
// Server-Side Apply PATCH under this manager. The gateway is the sole writer
// of SandboxClaim.status (the WarmPoolController consumes the binding state
// as projection input but never writes it), so the apply is naturally
// idempotent and conflict-free without a retry loop.
const statusFieldOwner = string(ownership.Gateway)

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
// the API server authorizes against `update`). The supplied now() clock
// makes the transition stamp testable.
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
	patch := statusApplyPatch(namespace, claimName)
	patch.Status.Phase = string(claimstate.Bound)
	patch.Status.BindingStateTransitionTime = &metav1.Time{Time: now().UTC()}
	if err := applyStatus(ctx, cl, patch); err != nil {
		return fmt.Errorf("podclaim: write bound status on claim %s: %w", claimName, err)
	}
	return nil
}

// WriteRecyclingStatus patches a bound claim to `recycling` when occupancy
// reaches zero on a recycling pool, so the gateway can coordinate the
// whole-pod scrub (and, on preConnect pools, the SDK re-warm that follows
// it) while the WarmPoolController projects the pod as `claimed`. The
// transition stamp it records is the orphan GC's reclaim key for the
// `recycling` window (§4.6.1 live-binding-state predicate), which covers a
// coordinating-gateway crash during the scrub wait. The patch is idempotent:
// a claim already `recycling` is a no-op so a retry does not churn the
// transition stamp.
//
// spec: §4.6.1 (recycling window, orphan GC), §4.6.3 (binding-state
// enumeration), §6.14 (projection: a `recycling` claim projects `claimed`).
func WriteRecyclingStatus(ctx context.Context, cl client.Client, namespace, claimName string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	cur, err := getClaim(ctx, cl, namespace, claimName, "recycling status")
	if err != nil {
		return err
	}
	if cur.Status.Phase == string(claimstate.Recycling) {
		return nil
	}
	patch := statusApplyPatch(namespace, claimName)
	patch.Status.Phase = string(claimstate.Recycling)
	patch.Status.BindingStateTransitionTime = &metav1.Time{Time: now().UTC()}
	if err := applyStatus(ctx, cl, patch); err != nil {
		return fmt.Errorf("podclaim: write recycling status on claim %s: %w", claimName, err)
	}
	return nil
}

// WriteRewarmStartedStatus stamps `rewarmStartedAt` on a `recycling` claim
// when a successful (or warn-policy) `ReportPodScrub` arrives on a preConnect
// pool and the recycle disposition begins the SDK re-warm. The phase stays
// `recycling`; the stamp anchors the `sdkConnectTimeoutSeconds` re-warm
// watchdog so only the re-warm leg is measured, and the projection enters
// `sdk_connecting` from it (§6.14). The stamp does not re-stamp the
// binding-state transition time, because the binding state does not change.
// It is idempotent: a claim that already carries the stamp is a no-op.
//
// spec: §4.6.1 (rewarmStartedAt stamp on a preConnect successful
// ReportPodScrub), §4.6.3 (rewarmStartedAt anchor), §6.14 (sdk_connecting
// projection from rewarmStartedAt).
func WriteRewarmStartedStatus(ctx context.Context, cl client.Client, namespace, claimName string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	cur, err := getClaim(ctx, cl, namespace, claimName, "rewarm-started stamp")
	if err != nil {
		return err
	}
	// The stamp is only meaningful while the claim is recycling; a write at
	// any other binding state is a caller bug. Fail closed rather than stamp
	// an unrelated state.
	if cur.Status.Phase != string(claimstate.Recycling) {
		return fmt.Errorf("podclaim: rewarm-started stamp requires a recycling claim, claim %s is %q", claimName, cur.Status.Phase)
	}
	if cur.Status.RewarmStartedAt != nil {
		return nil
	}
	patch := statusApplyPatch(namespace, claimName)
	// Re-assert the phase so the SSA apply does not drop the gateway-owned
	// `recycling` value while adding the stamp.
	patch.Status.Phase = string(claimstate.Recycling)
	patch.Status.RewarmStartedAt = &metav1.Time{Time: now().UTC()}
	patch.Status.BindingStateTransitionTime = cur.Status.BindingStateTransitionTime
	if err := applyStatus(ctx, cl, patch); err != nil {
		return fmt.Errorf("podclaim: write rewarm-started stamp on claim %s: %w", claimName, err)
	}
	return nil
}

// ReservedHold is the optimistic-concurrency token a reserved-hold DELETE is
// fenced against. WriteReservedStatus returns the claim UID and the
// `resourceVersion` observed at the `reserved` patch; a same-tenant rebind
// (`reserved → bound`) lands a further status patch that changes the
// `resourceVersion`, so a hold-expiry DELETE carrying these preconditions
// fails and the expiry aborts. spec: §4.6.1 (precondition-guarded DELETE),
// §3.2 (rebind-vs-hold-expiry race).
type ReservedHold struct {
	// UID is the claim's metadata.uid at the reserved patch.
	UID types.UID
	// ResourceVersion is the claim's resourceVersion observed at the
	// reserved patch — the version a rebind invalidates.
	ResourceVersion string
	// HoldExpiresAt is the reservation deadline stamped on the claim.
	HoldExpiresAt time.Time
}

// WriteReservedStatus patches a `recycling` claim to `reserved` once the pod
// is scrubbed (and, on preConnect pools, SDK-warm) and stamps `holdExpiresAt`
// at the reservation time plus the deployment-level hold TTL
// (`gateway.claimHoldTTLSeconds`). The pod is then held for its pinned tenant
// and excluded from idle inventory until the hold expires. The apply drops
// `rewarmStartedAt` once the re-warm completes (the re-warm watchdog no
// longer applies). It returns the optimistic-concurrency token a hold-expiry
// DELETE must carry: the claim UID and the `resourceVersion` observed at this
// patch, so a cross-replica rebind that lands first wins the race.
//
// spec: §4.6.1 (reserved hold, holdExpiresAt stamp, precondition token),
// §4.6.3 (binding-state enumeration, holdExpiresAt), §6.14 (reserved
// projection).
func WriteReservedStatus(ctx context.Context, cl client.Client, namespace, claimName string, holdTTL time.Duration, now func() time.Time) (ReservedHold, error) {
	if now == nil {
		now = time.Now
	}
	reservedAt := now().UTC()
	holdExpiresAt := reservedAt.Add(holdTTL)
	patch := statusApplyPatch(namespace, claimName)
	patch.Status.Phase = string(claimstate.Reserved)
	patch.Status.HoldExpiresAt = &metav1.Time{Time: holdExpiresAt}
	patch.Status.BindingStateTransitionTime = &metav1.Time{Time: reservedAt}
	if err := applyStatus(ctx, cl, patch); err != nil {
		return ReservedHold{}, fmt.Errorf("podclaim: write reserved status on claim %s: %w", claimName, err)
	}
	// The SSA apply returns the object with its post-patch UID and
	// resourceVersion; these fence the hold-expiry DELETE against a rebind.
	return ReservedHold{
		UID:             patch.UID,
		ResourceVersion: patch.ResourceVersion,
		HoldExpiresAt:   holdExpiresAt,
	}, nil
}

// WriteRebindStatus patches a `reserved` claim back to `bound` when a
// same-tenant session arrives within the hold window, dispatching onto the
// pod with no acquisition round trip. The apply drops `holdExpiresAt` (the
// pod is no longer held) and re-stamps the binding-state transition time.
// Any gateway replica may rebind; the patch changes the `resourceVersion`,
// so a hold-expiry DELETE fenced on the reserved-patch version fails its
// precondition and the expiry aborts (§3.2 rebind-vs-hold-expiry race). The
// rebinding caller re-reads the claim after this patch before dispatching.
//
// spec: §4.6.1 (reserved → bound rebind, precondition race), §4.6.3
// (binding-state enumeration), §3.2 (rebind-vs-hold-expiry race).
func WriteRebindStatus(ctx context.Context, cl client.Client, namespace, claimName string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	patch := statusApplyPatch(namespace, claimName)
	patch.Status.Phase = string(claimstate.Bound)
	patch.Status.BindingStateTransitionTime = &metav1.Time{Time: now().UTC()}
	if err := applyStatus(ctx, cl, patch); err != nil {
		return fmt.Errorf("podclaim: write rebind status on claim %s: %w", claimName, err)
	}
	return nil
}

// WriteDispositionStatus records a terminal retirement disposition
// (`released` on a limit-reached retirement, `failed` on a scrub-failure or
// crashed-session retirement) on the claim. The WarmPoolController projects
// the pod to `draining` then `terminated` from a terminal disposition,
// retiring it rather than returning it to the pool (§6.14). The patch records
// the binding-state transition time and is rejected when disposition is not a
// terminal state, so the gateway fails closed rather than writing an
// out-of-enum value the API server would reject.
//
// spec: §4.6.1 (terminal disposition), §4.6.3 (released/failed terminal
// dispositions), §6.14 (draining/terminated projection).
func WriteDispositionStatus(ctx context.Context, cl client.Client, namespace, claimName string, disposition claimstate.State, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	if !claimstate.IsTerminal(disposition) {
		return fmt.Errorf("podclaim: disposition %q is not a terminal binding state", disposition)
	}
	patch := statusApplyPatch(namespace, claimName)
	patch.Status.Phase = string(disposition)
	patch.Status.BindingStateTransitionTime = &metav1.Time{Time: now().UTC()}
	if err := applyStatus(ctx, cl, patch); err != nil {
		return fmt.Errorf("podclaim: write %s disposition on claim %s: %w", disposition, claimName, err)
	}
	return nil
}

// DeleteOnHoldExpiry deletes a reserved claim when its hold TTL expires,
// returning the pod to `idle`. The DELETE carries the Kubernetes
// preconditions captured at the `reserved` patch (the claim UID and the
// `resourceVersion`), so a cross-replica rebind that landed first (changing
// the `resourceVersion`) makes the DELETE fail its precondition with a
// Conflict; the expiry then aborts and the rebound claim is left intact. A
// missing claim is a no-op (already deleted or never existed), so a double
// expiry or a claim the orphan GC already reclaimed does not error.
//
// aborted is true when the DELETE failed its precondition, signaling the
// caller that a rebind won the race and the pod is still claimed; err is nil
// in that case so the caller distinguishes a lost race from a real failure.
//
// spec: §4.6.1 (precondition-guarded hold-expiry DELETE), §3.2
// (rebind-vs-hold-expiry race: a rebind that lands first aborts the expiry).
func DeleteOnHoldExpiry(ctx context.Context, cl client.Client, namespace, claimName string, hold ReservedHold) (aborted bool, err error) {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: namespace,
		},
	}
	preconditions := client.Preconditions(metav1.Preconditions{
		UID:             &hold.UID,
		ResourceVersion: &hold.ResourceVersion,
	})
	err = cl.Delete(ctx, claim, preconditions)
	switch {
	case err == nil:
		return false, nil
	case apierrors.IsNotFound(err):
		// Already deleted (double expiry, or the orphan GC reclaimed it).
		return false, nil
	case apierrors.IsConflict(err):
		// A rebind (or any other writer) changed the resourceVersion since
		// the reserved patch: the precondition failed, so the expiry aborts
		// and the rebound claim is left intact. spec: §3.2.
		return true, nil
	default:
		return false, fmt.Errorf("podclaim: hold-expiry delete of claim %s: %w", claimName, err)
	}
}

// statusApplyPatch builds the SSA apply skeleton for a SandboxClaim status
// patch: TypeMeta plus the object key, with an empty status the caller fills.
// SSA needs the apiVersion/kind populated.
func statusApplyPatch(namespace, claimName string) *lennyv1.SandboxClaim {
	return &lennyv1.SandboxClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: lennyv1.GroupVersion.String(),
			Kind:       "SandboxClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: namespace,
		},
	}
}

// applyStatus writes a SandboxClaim status via Server-Side Apply under the
// gateway field manager (the §4.6.3 `patch`-only sandboxclaims/status grant).
// The apply updates patch in place with the server's response, so callers
// that need the post-patch UID or resourceVersion read them off patch.
func applyStatus(ctx context.Context, cl client.Client, patch *lennyv1.SandboxClaim) error {
	return cl.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(statusFieldOwner))
}

// getClaim reads the named claim for a binding-state write, wrapping a
// NotFound with the write's purpose so the caller can branch.
func getClaim(ctx context.Context, cl client.Client, namespace, claimName, purpose string) (*lennyv1.SandboxClaim, error) {
	var cur lennyv1.SandboxClaim
	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: claimName}, &cur); err != nil {
		return nil, fmt.Errorf("podclaim: get claim %s for %s: %w", claimName, purpose, err)
	}
	return &cur, nil
}
