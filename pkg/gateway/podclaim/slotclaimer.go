// SPDX-License-Identifier: MIT

package podclaim

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// LabelTenant is the §5.2 tenant-pinning label. The gateway stamps it
// on a concurrent-mode (or task-mode) pod at first slot assignment; the
// lenny-tenant-label-immutability admission webhook rejects any mutation
// of it to a different non-empty value, so a pod pinned to one tenant
// can never be re-pinned to another. It is the Kubernetes-layer half of
// the two-layer tenant-pinning enforcement; SlotClaimer is the
// application-layer half.
const LabelTenant = "lenny.dev/tenant-id"

// ConcurrencyStyle is the §5.2 concurrent-mode sub-variant.
type ConcurrencyStyle string

const (
	// StyleWorkspace is `concurrencyStyle: workspace` — workspace-
	// concurrent. Each slot gets its own per-slot workspace tree under
	// /workspace/slots/{slotId}/ and the pod's /workspace/shared/ is
	// shared read-only across the pod's slots (§6.4).
	StyleWorkspace ConcurrencyStyle = "workspace"

	// StyleStateless is `concurrencyStyle: stateless` — stateless-
	// concurrent. No workspace is materialized for a slot; the pod holds
	// no Lenny-managed per-slot workspace or session state (§5.2).
	StyleStateless ConcurrencyStyle = "stateless"
)

// IsValid reports whether s is a known concurrency style.
func (s ConcurrencyStyle) IsValid() bool {
	return s == StyleWorkspace || s == StyleStateless
}

// Concurrent-mode claim sentinels.
var (
	// ErrNoConcurrentSlot reports that the pool can host no further slot:
	// every existing pod is at its maxConcurrent bound and no idle warm
	// pod is left to open a fresh slot on. The §5.2 slot-assignment rule
	// maps this to WARM_POOL_EXHAUSTED with details.reason
	// "concurrent_slots_exhausted".
	ErrNoConcurrentSlot = errors.New("podclaim: pool has no free concurrent slot")

	// ErrTenantMismatch reports that the only pods with free slot
	// capacity are pinned to a different tenant. §5.2 tenant pinning
	// forbids placing a slot for one tenant on a pod first assigned to
	// another, so the slot cannot be assigned there; the claim falls
	// through to a fresh idle pod, and ErrTenantMismatch is surfaced only
	// when no fresh pod is available either.
	ErrTenantMismatch = errors.New("podclaim: candidate pods are pinned to a different tenant")
)

// SlotRequest identifies the session a concurrent-mode slot is claimed
// for, the pool to claim from, and the concurrent-mode parameters of
// that pool.
type SlotRequest struct {
	// Pool is the SandboxWarmPool to claim a slot from.
	Pool string
	// SessionID is the §15.1 session the slot serves. It is also used as
	// the slot's SlotID: one session occupies exactly one slot, the
	// SandboxClaim name is deterministic per session, and the §6.4
	// per-slot workspace path /workspace/slots/{slotId}/ is therefore
	// stable and collision-free for the slot's lifetime.
	SessionID string
	// TenantID is the tenant that owns the session. §5.2 tenant pinning
	// binds a concurrent-mode pod to the first tenant it serves; a slot
	// for a different tenant is never placed on that pod.
	TenantID string
	// Style is the §5.2 concurrency style of the pool.
	Style ConcurrencyStyle
	// MaxConcurrent is the §5.2 per-pod slot bound. A pod hosts at most
	// this many slots simultaneously; the (MaxConcurrent+1)-th slot
	// request claims a fresh warm pod instead.
	MaxConcurrent int32
}

// SlotResult reports the slot a concurrent-mode session was placed on.
type SlotResult struct {
	// SandboxName is the pod the slot was opened on.
	SandboxName string
	// SlotID identifies the slot within that pod.
	SlotID string
	// Claim is the SandboxClaim binding the slot.
	Claim *lennyv1.SandboxClaim
	// FreshPod is true when the slot opened a previously-idle pod (its
	// first slot), false when the slot landed on a pod that was already
	// hosting other slots.
	FreshPod bool
	// ActiveSlots is the pod's slot count after this slot was reserved,
	// including this slot. It never exceeds MaxConcurrent.
	ActiveSlots int32
}

// SlotClaimer places concurrent-mode (§5.2) sessions onto warm pods. In
// `executionMode: concurrent` a pod hosts up to maxConcurrent
// simultaneous slots rather than being claimed exclusively for one
// session, so SlotClaimer first tries to land the slot on a pod that is
// already hosting slots (slot_active phase) and only claims a fresh
// idle pod when every partially-occupied pod is at its bound.
type SlotClaimer struct {
	// Client is the controller-runtime client addressing the cluster.
	Client client.Client
	// Namespace is the agent namespace the pool's Sandboxes live in.
	Namespace string
}

// ClaimSlot reserves a concurrent-mode slot for the request's session.
//
// Placement (§5.2 concurrent-workspace slot assignment):
//
//  1. A pod already hosting slots (phase slot_active) with
//     activeSlots < maxConcurrent and pinned to the request's tenant is
//     preferred — the slot joins that pod, sharing its process namespace
//     and, in workspace mode, its /workspace/shared/ tree.
//  2. When every slot_active pod for the tenant is at the maxConcurrent
//     bound, an idle warm pod is claimed and the slot opens it: phase
//     idle → slot_active, activeSlots 0 → 1, and the pod is pinned to
//     the tenant.
//  3. When no idle pod is left either, ErrNoConcurrentSlot is returned;
//     §5.2 maps this to WARM_POOL_EXHAUSTED / "concurrent_slots_exhausted".
//
// Slot reservation is atomic against competing gateway replicas. The
// activeSlots increment and the phase write go through an
// optimistic-locking status update: the cap is re-checked against the
// freshly-listed Sandbox, and a status-update conflict (a competing
// replica reserved a slot on the same pod first) makes ClaimSlot
// re-evaluate the pod or move to the next one rather than overrun the
// bound. This reproduces the §5.2 atomic check-and-increment guarantee
// without a separate Redis counter: the Sandbox status is the counter,
// and the API server's compare-and-swap is the atomicity primitive.
//
// Tenant pinning (§5.2) is enforced at the application layer here: a
// slot is never placed on a pod whose status.tenantId is a different
// non-empty value, and the pod is pinned (status.tenantId and the
// lenny.dev/tenant-id label) on its first slot. The
// lenny-tenant-label-immutability admission webhook is the
// Kubernetes-layer backstop.
func (c *SlotClaimer) ClaimSlot(ctx context.Context, req SlotRequest) (*SlotResult, error) {
	if !req.Style.IsValid() {
		return nil, fmt.Errorf("podclaim: invalid concurrency style %q", req.Style)
	}
	if req.MaxConcurrent < 1 {
		return nil, fmt.Errorf("podclaim: maxConcurrent must be >= 1, got %d", req.MaxConcurrent)
	}

	var list lennyv1.SandboxList
	if err := c.Client.List(ctx, &list,
		client.InNamespace(c.Namespace),
		client.MatchingLabels{warmpool.LabelPool: req.Pool}); err != nil {
		return nil, fmt.Errorf("list sandboxes for pool %s: %w", req.Pool, err)
	}

	// Pass 1: land the slot on a pod already hosting slots, pinned to
	// this tenant and below the maxConcurrent bound. Sharing an existing
	// pod is the point of concurrent mode — it is preferred over burning
	// a fresh warm pod.
	sawTenantMismatch := false
	for i := range list.Items {
		sb := &list.Items[i]
		if sb.Status.Phase != string(state.SlotActive) {
			continue
		}
		if sb.Status.ActiveSlots >= req.MaxConcurrent {
			// Pod is at its slot bound; the §5.2 atomic cap check would
			// reject a reservation here. Try the next pod.
			continue
		}
		if sb.Status.TenantID != "" && sb.Status.TenantID != req.TenantID {
			// §5.2 tenant pinning: this pod is pinned to another tenant.
			sawTenantMismatch = true
			continue
		}
		res, conflict, err := c.reserveSlot(ctx, sb, req)
		if err != nil {
			return nil, err
		}
		if conflict {
			// A competing replica reserved a slot on this pod first; the
			// cap may now be reached. Skip it and try the next pod.
			continue
		}
		return res, nil
	}

	// Pass 2: every slot_active pod is full (or pinned elsewhere). Claim
	// a fresh idle warm pod and open its first slot.
	for i := range list.Items {
		sb := &list.Items[i]
		if sb.Status.Phase != string(state.Idle) {
			continue
		}
		res, conflict, err := c.reserveSlot(ctx, sb, req)
		if err != nil {
			return nil, err
		}
		if conflict {
			// A competing replica claimed this idle pod first; try the
			// next idle pod.
			continue
		}
		return res, nil
	}

	// No slot_active pod has free capacity for this tenant and no idle
	// pod is left. When the only blocker was tenant pinning, surface that
	// distinction; otherwise the pool is genuinely slot-exhausted.
	if sawTenantMismatch {
		return nil, ErrTenantMismatch
	}
	return nil, ErrNoConcurrentSlot
}

// reserveSlot atomically reserves one slot on sb for the request.
//
// Ordering matters for two independent invariants:
//
//   - The SandboxClaim is created first. Its name is deterministic per
//     session, so a repeated slot claim for a live session collides at
//     CREATE before the pod's slot count is touched. This prevents a
//     duplicate-session claim from leaking a slot increment, and it
//     lets the lenny-sandboxclaim-guard webhook see the claim.
//   - The slot count is then reserved with an optimistic-locking status
//     update: the maxConcurrent cap and the tenant pin are re-checked
//     against the freshly-listed Sandbox, status.activeSlots is
//     incremented, the phase advances (idle → slot_active for the first
//     slot, slot_active → slot_active for a further slot), and
//     status.tenantId is pinned on the first slot.
//
// A status-update conflict (a competing gateway replica reserved a slot
// on the same pod first), or a cap re-check that now fails, is reported
// via the conflict return so the caller can re-evaluate; it is not an
// error. When that happens the just-created SandboxClaim is deleted so
// the next placement attempt for the session sees a clean slate. On a
// successful reservation reserveSlot stamps the lenny.dev/tenant-id
// label (best-effort).
func (c *SlotClaimer) reserveSlot(ctx context.Context, sb *lennyv1.Sandbox, req SlotRequest) (res *SlotResult, conflict bool, err error) {
	// Re-check the cap and the tenant pin against the freshly-listed
	// object before doing any writes.
	if sb.Status.ActiveSlots >= req.MaxConcurrent {
		return nil, true, nil
	}
	if sb.Status.TenantID != "" && sb.Status.TenantID != req.TenantID {
		return nil, true, nil
	}

	// Create the binding SandboxClaim first — the deterministic-name
	// CREATE is the duplicate-session guard and must precede any slot
	// count mutation.
	claim, err := createSlotClaim(ctx, c.Client, c.Namespace, sb.Name, req)
	if err != nil {
		return nil, false, err
	}

	freshPod := sb.Status.ActiveSlots == 0
	sb.Status.Phase = string(state.SlotActive)
	sb.Status.ActiveSlots++
	if sb.Status.TenantID == "" {
		// First slot on this pod pins it to the tenant for its lifetime.
		sb.Status.TenantID = req.TenantID
	}
	if err := c.Client.Status().Update(ctx, sb); err != nil {
		if apierrors.IsConflict(err) {
			// A competing replica reserved a slot on this pod first. Undo
			// the SandboxClaim so the caller can retry cleanly on another
			// pod, and report the conflict.
			_ = c.Client.Delete(ctx, claim)
			return nil, true, nil
		}
		_ = c.Client.Delete(ctx, claim)
		return nil, false, fmt.Errorf("reserve slot on sandbox %s: %w", sb.Name, err)
	}

	// Stamp the §5.2 tenant-pinning label so the
	// lenny-tenant-label-immutability webhook backstops the pin at the
	// Kubernetes layer. Best-effort: the status write above already
	// pinned the pod at the application layer, and a label conflict means
	// a competing writer set the same value.
	if sb.Labels == nil || sb.Labels[LabelTenant] != req.TenantID {
		if sb.Labels == nil {
			sb.Labels = map[string]string{}
		}
		sb.Labels[LabelTenant] = req.TenantID
		if err := c.Client.Update(ctx, sb); err != nil && !apierrors.IsConflict(err) {
			return nil, false, fmt.Errorf("label sandbox %s with tenant: %w", sb.Name, err)
		}
	}

	return &SlotResult{
		SandboxName: sb.Name,
		SlotID:      req.SessionID,
		Claim:       claim,
		FreshPod:    freshPod,
		ActiveSlots: sb.Status.ActiveSlots,
	}, false, nil
}

// ReleaseSlot releases a concurrent-mode slot when its session ends or
// fails. It deletes the slot's SandboxClaim and decrements the pod's
// status.activeSlots; per §6.2 the pod returns to the idle phase when
// its last slot drains (activeSlots reaches 0) and otherwise stays
// slot_active. The pod's status.tenantId pin is left in place: §5.2
// tenant pinning binds a concurrent-mode pod to its tenant for the pod's
// lifetime, not just while a slot is occupied, so an idle-again
// concurrent-mode pod keeps its pin and accepts further slots only for
// the same tenant.
//
// A status-update conflict is retried against a refreshed Sandbox so a
// concurrent release of a sibling slot on the same pod does not lose a
// decrement. The decrement is clamped at 0 so a double release cannot
// drive the count negative.
func (c *SlotClaimer) ReleaseSlot(ctx context.Context, sandboxName, sessionID string) error {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName(sessionID),
			Namespace: c.Namespace,
		},
	}
	if err := c.Client.Delete(ctx, claim); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("podclaim: delete slot claim for session %s: %w", sessionID, err)
	}

	// Decrement the pod's slot count under optimistic locking, retrying
	// on conflict so a sibling slot's concurrent release does not clobber
	// this decrement.
	for {
		var sb lennyv1.Sandbox
		if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: sandboxName}, &sb); err != nil {
			if apierrors.IsNotFound(err) {
				// The pod is already gone (terminated/replaced); nothing to
				// decrement.
				return nil
			}
			return fmt.Errorf("podclaim: get sandbox %s for slot release: %w", sandboxName, err)
		}
		if sb.Status.ActiveSlots <= 0 {
			// Already at zero — a double release, or the pod was reset.
			return nil
		}
		sb.Status.ActiveSlots--
		if sb.Status.ActiveSlots == 0 {
			// §6.2: the last slot drained, the pod returns to idle.
			sb.Status.Phase = string(state.Idle)
		}
		if err := c.Client.Status().Update(ctx, &sb); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return fmt.Errorf("podclaim: release slot on sandbox %s: %w", sandboxName, err)
		}
		return nil
	}
}

// createSlotClaim creates the SandboxClaim binding a concurrent-mode
// slot to its pod. The claim name is deterministic per session, so a
// repeated slot claim for the same session collides at CREATE rather
// than opening a duplicate slot, and the SlotID field carries the §6.4
// slot identifier.
func createSlotClaim(ctx context.Context, cl client.Client, namespace, sandboxName string, req SlotRequest) (*lennyv1.SandboxClaim, error) {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName(req.SessionID),
			Namespace: namespace,
		},
		Spec: lennyv1.SandboxClaimSpec{
			SandboxRef: sandboxName,
			SessionID:  req.SessionID,
			TenantID:   req.TenantID,
			SlotID:     req.SessionID,
		},
	}
	if err := cl.Create(ctx, claim); err != nil {
		return nil, fmt.Errorf("create slot claim for %s: %w", sandboxName, err)
	}
	return claim, nil
}
