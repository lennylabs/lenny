// SPDX-License-Identifier: MIT

package podclaim

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// LabelTenant is the §5.2 tenant-pinning label. The gateway stamps it
// on a concurrent-session pod at first slot assignment; the
// lenny-tenant-label-immutability admission webhook rejects any mutation
// of it to a different non-empty value, so a pod pinned to one tenant
// can never be re-pinned to another. It is the Kubernetes-layer half of
// the two-layer tenant-pinning enforcement; SlotClaimer is the
// application-layer half.
//
// spec: §6.4 lines 416-419 / §5.1 — the gateway-stamps-on-claim pattern
// established here is the working precedent for the missing
// `lenny.dev/workspace-tier: t4` writer (tracked under F-6.4.4 / F-6.4.9).
// A future T4 writer (in the pool controller or pod-spec builder) should
// follow the same SSA-Apply + `lenny-gateway` field-manager handoff used
// in (*SlotClaimer).Claim below so the lenny-t4-node-isolation webhook
// has a stable predicate to match on.
const LabelTenant = "lenny.dev/tenant-id"

// stampPodTenant lands the §5.2 line 392 tenant pin on the agent *pod*
// (not only the Sandbox CR) at first assignment, so the pod-scoped
// lenny-tenant-label-immutability ValidatingAdmissionWebhook (§17.2 item
// 5) actually sees the `unset → {tenant_id}` transition it is meant to
// guard. The webhook authorizes that transition only for the gateway
// ServiceAccount, which is the identity this gateway-side write carries,
// so the Kubernetes-layer half of the two-layer tenant pin binds where
// the spec places it: on the pod whose labels the §13.2 NET-003
// NetworkPolicies select. A pod that is absent at claim time
// (terminating, or not yet materialized by the Sandbox reconciler) is
// tolerated — the label is moot without a pod and the pin still stands
// on Sandbox.status; the next assignment re-stamps it. A conflict is the
// spec-intended idempotency (a competing writer set the same value).
//
// A JSON-merge patch is used rather than an SSA Apply so a missing pod
// returns NotFound instead of being created label-only (which would fail
// pod validation). The patch is keyed by the Sandbox name because the
// §4.6.1 reconciler names the backing pod identically to its Sandbox.
//
// spec: §5.2 line 392 (Kubernetes-layer tenant pin) / §17.2 line 46
// (immutable labels enforced on agent pods) / §13.2 NET-003. F-17.2.3.
func stampPodTenant(ctx context.Context, cl client.Client, namespace, name, tenantID string) error {
	if tenantID == "" {
		return nil
	}
	body := fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, LabelTenant, tenantID)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	err := cl.Patch(ctx, pod, client.RawPatch(types.MergePatchType, []byte(body)),
		client.FieldOwner(string(ownership.Gateway)))
	if apierrors.IsNotFound(err) || apierrors.IsConflict(err) {
		return nil
	}
	return err
}

// StampDrainRequest stamps the §4.6.3 lenny.dev/drain-request annotation on
// the agent Pod named podName (the §4.6.1 reconciler names the pod
// identically to its Sandbox). The gateway stamps this annotation when a
// pod crosses the §5.2 unhealthy-slot threshold; the WarmPoolController
// consumes it and writes the draining transition on Sandbox.status. The
// gateway never writes Sandbox.status itself (§4.6.3 ownership
// decomposition), so the unhealthy-threshold drain is routed through this
// annotation rather than a gateway phase write. The annotation value is the
// RFC3339Nano request instant so a re-stamp is idempotent in effect and the
// WarmPoolController can age the request.
//
// A JSON-merge patch is used (matching stampPodTenant) so a missing pod
// returns NotFound rather than being created annotation-only; an absent or
// terminating pod is tolerated because a pod with no slots needs no drain.
//
// spec: §4.6.3 (gateway stamps drain-request; WarmPoolController writes the
// drain); §5.2 (unhealthy-slot whole-pod replacement trigger).
func StampDrainRequest(ctx context.Context, cl client.Client, namespace, podName string, requestedAt time.Time) error {
	body := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`,
		lennyv1.AnnotationDrainRequest, requestedAt.UTC().Format(time.RFC3339Nano))
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace}}
	err := cl.Patch(ctx, pod, client.RawPatch(types.MergePatchType, []byte(body)),
		client.FieldOwner(string(ownership.Gateway)))
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("podclaim: stamp drain-request on pod %s: %w", podName, err)
	}
	return nil
}

// StampScrubWarning stamps the §5.2 lenny.dev/scrub-warning annotation on the
// recycling agent Pod named podName. The gateway stamps it when a whole-pod
// scrub fails under the onScrubFailure: warn policy and the recycle
// disposition reuses the pod (reserve, preConnect re-warm, or the §6.39
// cordon-drain-under-warn). The marker records that the pod re-enters the pool
// carrying residual-state risk; it persists through the §6.2 preConnect
// re-warm because SDK readiness is orthogonal to residual-state risk. The
// annotation value is the RFC3339Nano stamp instant so a re-stamp is
// idempotent in effect and a consumer can age the marker.
//
// A JSON-merge patch is used (matching StampDrainRequest) so a missing pod
// returns NotFound rather than being created annotation-only; an absent or
// terminating pod is tolerated because a pod that is already gone needs no
// marker. The gateway's `get`/`patch` on agent Pods grant covers this write;
// the gateway never writes Sandbox.status itself (§4.6.3).
//
// spec: §5.2 (warn policy returns the pod with a scrub_warning annotation);
// §6.2 (preConnect re-warm on scrub_warning persists the annotation); §4.6.3
// (gateway patches agent Pod annotations, never Sandbox.status).
func StampScrubWarning(ctx context.Context, cl client.Client, namespace, podName string, stampedAt time.Time) error {
	body := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`,
		lennyv1.AnnotationScrubWarning, stampedAt.UTC().Format(time.RFC3339Nano))
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace}}
	err := cl.Patch(ctx, pod, client.RawPatch(types.MergePatchType, []byte(body)),
		client.FieldOwner(string(ownership.Gateway)))
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("podclaim: stamp scrub-warning on pod %s: %w", podName, err)
	}
	return nil
}

// StampMaxPodUptime stamps the §4.6.1 lenny.dev/max-pod-uptime-seconds
// annotation on the agent Pod named podName (the §4.6.1 reconciler names the
// pod identically to its Sandbox). The gateway holds the pool's
// sessionPolicy.recycle.maxPodUptimeSeconds cap in its poolstore, but that cap
// is absent from every CRD the WarmPoolController reconciles, so the gateway
// delivers it on the pod as this annotation and the controller's
// reconcileUptime arm reads it to level-trigger the CreationTimestamp-derived
// uptime drain. The annotation carries the cap as a decimal integer number of
// seconds. A non-positive cap (a pool that sets no maxPodUptimeSeconds) stamps
// nothing: the annotation's absence disables the controller's uptime check,
// matching the field's optional status and mirroring the expiredByUptime and
// podscrub.Decide `maxPodUptimeSeconds > 0` guards.
//
// A JSON-merge patch is used (matching stampPodTenant and StampDrainRequest) so
// a missing pod returns NotFound rather than being created annotation-only; an
// absent or terminating pod is tolerated because a pod that is already gone
// needs no cap. The gateway's `get`/`patch` on agent Pods grant covers this
// write alongside the lenny.dev/drain-request stamp and the lenny.dev/tenant-id
// pin; the gateway never writes Sandbox.status itself (§4.6.3 ownership
// decomposition).
//
// spec: §4.6.1 (uptime drains derive from the pod CreationTimestamp against
// recycle.maxPodUptimeSeconds and are WarmPoolController-written); §4.6.3 (the
// gateway delivers the cap the controller reads).
func StampMaxPodUptime(ctx context.Context, cl client.Client, namespace, podName string, maxPodUptimeSeconds int64) error {
	if maxPodUptimeSeconds <= 0 {
		return nil
	}
	body := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`,
		lennyv1.AnnotationMaxPodUptimeSeconds, strconv.FormatInt(maxPodUptimeSeconds, 10))
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace}}
	err := cl.Patch(ctx, pod, client.RawPatch(types.MergePatchType, []byte(body)),
		client.FieldOwner(string(ownership.Gateway)))
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("podclaim: stamp max-pod-uptime on pod %s: %w", podName, err)
	}
	return nil
}

// Concurrent-session claim sentinels.
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

// SlotRequest identifies the session a concurrent-session slot is claimed
// for, the pool to claim from, and the concurrent-session parameters of
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
	// binds a concurrent-session pod to the first tenant it serves; a slot
	// for a different tenant is never placed on that pod.
	TenantID string
	// MaxConcurrentSessions is the §5.2 sessionPolicy.maxConcurrentSessions
	// per-pod slot bound. A pod hosts at most this many slots
	// simultaneously; the (MaxConcurrentSessions+1)-th slot request claims
	// a fresh warm pod instead.
	MaxConcurrentSessions int32
	// MaxPodUptimeSeconds is the §6.2 lines 166-167 concurrent-session
	// pod-uptime retirement cap. Before a slot is placed on a candidate
	// pod, ClaimSlot compares the pod's wall-clock uptime (now minus the
	// Sandbox's creation timestamp) against this value and skips a pod over
	// the cap so no new slot lands on it. The skip is a read-only placement
	// filter: the gateway does not write Sandbox.status. The
	// WarmPoolController owns the actual draining transition, which it
	// derives from the pod CreationTimestamp against
	// recycle.maxPodUptimeSeconds (§4.6.1). Zero leaves uptime retirement
	// off. spec: §4.6.1 (uptime drains are WarmPoolController-written).
	MaxPodUptimeSeconds int64
}

// SlotResult reports the slot a concurrent-session session was placed on.
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
	// including this slot. It never exceeds MaxConcurrentSessions.
	ActiveSlots int32
}

// SlotClaimer places concurrent-session (§5.2) sessions onto warm pods.
// When sessionPolicy.maxConcurrentSessions > 1 a pod hosts up to that many
// simultaneous slots multiplexed onto a single per-pod SandboxClaim. The
// claim is the pod-acquisition guard; the atomic Redis slot counter is the
// intra-pod capacity gate, with a §12.4 Postgres fallback during a Redis
// outage. SlotClaimer first lands a slot on a pod already hosting slots for
// the tenant (its per-pod claim exists and is non-terminal) and only
// acquires a fresh idle pod when every partially-occupied pod is at its
// bound.
type SlotClaimer struct {
	// Client is the controller-runtime client addressing the cluster.
	Client client.Client
	// Namespace is the agent namespace the pool's Sandboxes live in.
	Namespace string
	// Counter is the §5.2 atomic slot counter and §12.4 Redis-outage
	// Postgres-fallback gate. Required: the intra-pod capacity gate has no
	// in-cluster substitute now that the gateway no longer mirrors the slot
	// count onto Sandbox.status. A nil Counter is a configuration error and
	// makes ClaimSlot return an error rather than silently overrunning the
	// per-pod bound.
	Counter *slotcounter.Counter
	// OnSlotConflict records a §5.2 line 519 atomic-reservation failure
	// due to slot contention: a candidate pod was at its maxConcurrent
	// bound when the reservation was attempted. It backs the
	// lenny_slot_assignment_conflict_total counter (labeled by pool) so
	// operators can detect pool under-sizing. Nil is a no-op.
	OnSlotConflict func(pool string)
	// OnRehydrate records a §5.2 line 521 post-recovery rehydration
	// event: the slot counter for a pod was seeded from Postgres after a
	// Redis restart. It backs the lenny_slot_rehydration_total counter
	// (labeled by pod and pool) and fires exactly once per pod per Redis
	// restart (the replica that won the rehydration). Nil is a no-op.
	OnRehydrate func(podID, pool string)
	// Now supplies the wall clock for the §6.2 lines 166-167 pod-uptime
	// retirement check. Nil defaults to time.Now.
	Now func() time.Time
	// RecycleBoundary arms the §3.4 gateway-side missing-report timeout when
	// ReleaseSlot patches the per-pod claim bound → recycling on the
	// occupancy-zero edge of a recycling concurrent-session pool. The adapter's
	// ReportPodScrub cancels it; if no report arrives within
	// cleanupTimeoutSeconds plus a grace, the coordinator retires the pod rather
	// than leaving it stuck in `recycling` until the much longer §4.6.1
	// orphan-GC window. Nil is a no-op (a deployment with no in-process recycle
	// coordinator); the orphan GC remains the crash backstop. spec: §3.4
	// (missing-report timeout).
	RecycleBoundary RecycleBoundaryArmer
}

// RecycleBoundaryArmer arms the §3.4 missing-report timeout for a pod at the
// bound → recycling patch. *recycle.RecycleBoundaryCoordinator satisfies it
// through OnRecycling; the interface is defined at this consumer so podclaim
// does not import the recycle package (which imports podclaim). spec: §3.4
// (gateway-side missing-report timeout armed at session termination).
type RecycleBoundaryArmer interface {
	OnRecycling(podID string)
}

// now returns the claimer's clock, defaulting to time.Now. spec: §6.2
// lines 166-167 — the pod-uptime retirement check needs a wall clock.
func (c *SlotClaimer) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// recordSlotConflict reports a §5.2 line 519 slot-contention reservation
// failure for pool. Nil-safe.
func (c *SlotClaimer) recordSlotConflict(pool string) {
	if c.OnSlotConflict != nil {
		c.OnSlotConflict(pool)
	}
}

// recordRehydration reports a §5.2 line 521 slot-counter rehydration
// event for podID in pool. Nil-safe.
func (c *SlotClaimer) recordRehydration(podID, pool string) {
	if c.OnRehydrate != nil {
		c.OnRehydrate(podID, pool)
	}
}

// expiredByUptime reports whether sb has exceeded the pool's
// maxConcurrentSessions pool maxPodUptimeSeconds and is therefore not a
// valid slot-placement candidate (§6.2 lines 166-167). The pod's
// wall-clock uptime is measured from the Sandbox's creation timestamp,
// which the WarmPoolController stamps when it provisions the warm pod; a
// zero cap disables the check. It is a read-only predicate: ClaimSlot uses
// it to skip the pod, and the WarmPoolController owns the resulting
// draining transition, derived from the same CreationTimestamp (§4.6.1).
//
// spec: spec/06_warm-pod-model.md §6.2 lines 166-167; spec/04 §4.6.1
// (uptime drains are WarmPoolController-written).
func (c *SlotClaimer) expiredByUptime(sb *lennyv1.Sandbox, req SlotRequest) bool {
	if req.MaxPodUptimeSeconds <= 0 {
		return false
	}
	created := sb.CreationTimestamp.Time
	if created.IsZero() {
		return false
	}
	limit := time.Duration(req.MaxPodUptimeSeconds) * time.Second
	return c.now().Sub(created) > limit
}

// ClaimSlot reserves a concurrent-session slot for the request's session
// on a per-pod occupancy claim (§4.6.1, §5.2). The session-to-pod binding
// is recorded on the Postgres session row's pod_assignment column by the
// session server; SlotResult.SlotID carries the session identifier so the
// per-slot workspace path /workspace/slots/{slotId}/ stays stable.
//
// Placement (§5.2 concurrent-session slot assignment):
//
//  1. A pod already hosting slots for the request's tenant (its per-pod
//     SandboxClaim exists, is non-terminal, and is pinned to the tenant)
//     with free Redis-counter capacity is preferred — the slot joins that
//     pod, sharing its process namespace and /workspace/shared/ tree. No
//     new claim is created; the slot multiplexes onto the existing per-pod
//     claim and the Redis counter is the intra-pod capacity gate.
//  2. When every claimed pod for the tenant is at the maxConcurrentSessions
//     bound, an idle warm pod is acquired: the gateway CREATEs the per-pod
//     claim (claim-<podName>), writes its first `bound` status, reserves
//     the first slot, and pins the pod to the tenant.
//  3. When no idle pod is left either, ErrNoConcurrentSlot is returned;
//     §5.2 maps this to WARM_POOL_EXHAUSTED / "concurrent_slots_exhausted".
//
// Slot reservation is atomic against competing gateway replicas through the
// §5.2 Redis Lua GET-compare-INCR counter (with the §12.4 Postgres-fallback
// gate during a Redis outage); pod acquisition is atomic through the
// deterministic per-pod claim CREATE. The gateway does not write
// Sandbox.status; the WarmPoolController projects the pod's occupancy phase
// from the claim binding state (§4.6.1).
//
// Tenant pinning (§5.2) is enforced at the application layer here: a slot is
// never placed on a pod whose claim is pinned to a different tenant, and the
// pod is pinned (the lenny.dev/tenant-id label) on its first slot. The
// lenny-tenant-label-immutability admission webhook is the Kubernetes-layer
// backstop.
func (c *SlotClaimer) ClaimSlot(ctx context.Context, req SlotRequest) (*SlotResult, error) {
	if req.MaxConcurrentSessions < 1 {
		return nil, fmt.Errorf("podclaim: maxConcurrentSessions must be >= 1, got %d", req.MaxConcurrentSessions)
	}
	if c.Counter == nil {
		// The Redis counter (with its §12.4 Postgres fallback) is the only
		// intra-pod capacity gate now that the gateway does not mirror the
		// slot count onto Sandbox.status; without it a fresh slot could
		// overrun the per-pod bound. Fail closed.
		return nil, errors.New("podclaim: slot counter is required for concurrent-session slot assignment")
	}

	var list lennyv1.SandboxList
	if err := c.Client.List(ctx, &list,
		client.InNamespace(c.Namespace),
		client.MatchingLabels{warmpool.LabelPool: req.Pool}); err != nil {
		return nil, fmt.Errorf("list sandboxes for pool %s: %w", req.Pool, err)
	}

	// Pass 1: land the slot on a pod that already hosts slots for this
	// tenant — its per-pod claim exists, is non-terminal, and is pinned to
	// the tenant — and has free Redis-counter capacity. Sharing an existing
	// pod is the point of concurrent mode, preferred over acquiring a fresh
	// warm pod.
	sawTenantMismatch := false
	for i := range list.Items {
		sb := &list.Items[i]
		claim, found, err := c.podClaim(ctx, sb.Name)
		if err != nil {
			return nil, err
		}
		if !found || claimstate.IsTerminal(claimstate.State(claim.Status.Phase)) {
			// No live per-pod claim: this pod is not yet hosting slots for
			// anyone. It is a Pass-2 candidate (a fresh acquisition).
			continue
		}
		if claim.Spec.TenantID != "" && claim.Spec.TenantID != req.TenantID {
			// §5.2 tenant pinning: this pod is pinned to another tenant.
			sawTenantMismatch = true
			continue
		}
		if c.expiredByUptime(sb, req) {
			// §6.2 line 166 / §4.6.1: maxPodUptimeSeconds exceeded — the pod
			// accepts no new slots and its existing slots drain. This is a
			// read-only placement filter; the gateway skips the pod without
			// writing Sandbox.status. The WarmPoolController owns the
			// claimed → draining uptime transition, derived from the pod
			// CreationTimestamp.
			continue
		}
		// §3.2 within-hold rebind: a same-tenant claim held in `reserved`
		// within its hold window is dispatched onto with no acquisition round
		// trip. The slot path consumes the hold by patching the claim
		// `reserved → bound` before reserving the first slot; the rebind
		// changes the resourceVersion so the holder's expiry DELETE aborts.
		// A rebind that loses to a concurrent expiry DELETE (the claim
		// vanished, or moved off `reserved`) re-reads as not-rebound and the
		// pod is skipped for normal acquisition. spec: §3.2, §4.6.1.
		if claim.Status.Phase == string(claimstate.Reserved) {
			rebound, ok, err := c.rebindReservedSlot(ctx, claim)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			claim = rebound
		}
		res, conflict, err := c.reserveSlot(ctx, sb, claim, req, false)
		if err != nil {
			return nil, err
		}
		if conflict {
			// The Redis counter reported the pod at its bound; try the next.
			continue
		}
		return res, nil
	}

	// Pass 2: every claimed pod is full (or pinned elsewhere). Acquire a
	// fresh idle warm pod — one with no live per-pod claim — and open its
	// first slot. The §4.6.1 occupancy projection still leaves a not-yet-
	// acquired warm pod at the WPC-written idle phase, so the idle-pod scan
	// matches on phase here; the per-pod claim CREATE inside reserveSlot is
	// the acquisition guard against a concurrent replica.
	for i := range list.Items {
		sb := &list.Items[i]
		if sb.Status.Phase != string(state.Idle) {
			continue
		}
		_, found, err := c.podClaim(ctx, sb.Name)
		if err != nil {
			return nil, err
		}
		if found {
			// A claim exists (a Pass-1 candidate handled above, or a reserved
			// hold); never double-acquire.
			continue
		}
		if c.expiredByUptime(sb, req) {
			// §6.2 line 167 / §4.6.1: maxPodUptimeSeconds exceeded while idle,
			// checked before next assignment. Read-only placement filter: the
			// gateway skips the pod without writing Sandbox.status. The
			// WarmPoolController owns the idle → draining uptime transition,
			// derived from the pod CreationTimestamp.
			continue
		}
		res, conflict, err := c.reserveSlot(ctx, sb, nil, req, true)
		if err != nil {
			return nil, err
		}
		if conflict {
			// A competing replica acquired this idle pod first; try the next.
			continue
		}
		return res, nil
	}

	// No claimed pod has free capacity for this tenant and no idle pod is
	// left. §5.2 line 519 distinguishes the cause via details.reason: when
	// the pool holds no pods at all the reason is "no_idle_pods"
	// (ErrNoIdlePod); when pods exist but every slot is full the reason is
	// "concurrent_slots_exhausted" (ErrNoConcurrentSlot). Tenant pinning is
	// surfaced distinctly so the binder can fall through.
	if len(list.Items) == 0 {
		return nil, ErrNoIdlePod
	}
	if sawTenantMismatch {
		return nil, ErrTenantMismatch
	}
	return nil, ErrNoConcurrentSlot
}

// rebindReservedSlot consumes a §3.2 reserved hold on a concurrent-session
// pod: when the same tenant holds the pod in `reserved` within its hold
// window, it patches the claim `reserved → bound` and re-reads it so the slot
// reservation lands on a `bound` claim. ok is false when the hold has expired
// or a concurrent expiry DELETE / rebind moved the claim off `reserved`, in
// which case the caller skips the pod (a slot can never be reserved on a
// `reserved` claim because a reserved pod has zero active slots and must be
// rebound first). The rebind changes the resourceVersion, so the holder's
// precondition-guarded expiry DELETE aborts. spec: §3.2 (within-hold rebind),
// §4.6.1 (reserved hold, holdExpiresAt), §4.6.3 (reserved → bound).
func (c *SlotClaimer) rebindReservedSlot(ctx context.Context, claim *lennyv1.SandboxClaim) (*lennyv1.SandboxClaim, bool, error) {
	if claim.Status.HoldExpiresAt == nil || !claim.Status.HoldExpiresAt.Time.After(c.now()) {
		// The hold has expired (or carries no deadline); leave the claim to the
		// holder's expiry DELETE or the orphan GC rather than racing it.
		return nil, false, nil
	}
	if err := WriteRebindStatus(ctx, c.Client, c.Namespace, claim.Name, c.now); err != nil {
		if apierrors.IsNotFound(err) {
			// A concurrent expiry DELETE reclaimed the claim first; skip the pod.
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("podclaim: rebind reserved claim %s for slot: %w", claim.Name, err)
	}
	// §3.2: re-read the claim after the rebind patch so the slot reservation
	// lands on the post-rebind `bound` object. The claim name is the
	// authoritative key; req.SandboxRef on the claim spec names its pod.
	rebound, found, err := c.podClaim(ctx, claim.Spec.SandboxRef)
	if err != nil {
		return nil, false, err
	}
	if !found || rebound.Status.Phase != string(claimstate.Bound) {
		// The claim vanished or was moved off `bound` between the patch and the
		// re-read; skip rather than reserve a slot on an unstable claim.
		return nil, false, nil
	}
	return rebound, true, nil
}

// podClaim reads the per-pod occupancy SandboxClaim (claim-<podName>) for
// sandboxName. found is false when no claim exists (a fresh idle pod). The
// claim's binding state and tenant pin drive ClaimSlot's placement.
func (c *SlotClaimer) podClaim(ctx context.Context, sandboxName string) (*lennyv1.SandboxClaim, bool, error) {
	var claim lennyv1.SandboxClaim
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: claimName(sandboxName)}, &claim); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("podclaim: get per-pod claim for sandbox %s: %w", sandboxName, err)
	}
	return &claim, true, nil
}

// reserveSlot reserves one Redis-counter slot on sb for the request and, on
// a fresh acquisition, CREATEs the per-pod claim and writes its first
// `bound` status. existing is the live per-pod claim on a Pass-1 placement
// and nil on a fresh acquisition; freshPod selects which path runs.
//
// Ordering:
//
//   - The §5.2 Redis counter reservation runs first (with the §12.4
//     Postgres-fallback gate during a Redis outage). A pod at its
//     maxConcurrentSessions bound returns the conflict signal so the caller
//     skips it; a Redis-outage fail-closed surfaces as an error.
//   - On a fresh acquisition the per-pod claim is then CREATEd (the §4.6.1
//     acquisition guard) and its first `bound` status is written. An
//     AlreadyExists collision (a competing replica acquired the pod first)
//     undoes the counter reservation and returns the conflict signal.
//
// The gateway does not write Sandbox.status; the WarmPoolController projects
// the pod's occupancy phase from the claim binding state (§4.6.1). On a
// successful reservation reserveSlot stamps the lenny.dev/tenant-id label on
// the pod (best-effort) for the §13.2 NET-003 NetworkPolicy selector.
func (c *SlotClaimer) reserveSlot(ctx context.Context, sb *lennyv1.Sandbox, existing *lennyv1.SandboxClaim, req SlotRequest, freshPod bool) (res *SlotResult, conflict bool, err error) {
	// §5.2 atomic slot reservation. The Redis Lua GET-compare-INCR
	// enforces the maxConcurrentSessions cap atomically across gateway
	// replicas; during a Redis outage the §12.4 Postgres-fallback gate
	// serializes the count-and-decide under a per-pod advisory lock and
	// fails closed after a bounded outage window.
	newCount, rehydrated, err := c.Counter.Reserve(ctx, sb.Name, req.MaxConcurrentSessions)
	if rehydrated {
		// §5.2 line 521: this reservation seeded the pod's slot counter from
		// Postgres after a Redis restart. Emit the rehydration event
		// regardless of the reservation outcome.
		c.recordRehydration(sb.Name, req.Pool)
	}
	if errors.Is(err, slotcounter.ErrSlotsExhausted) {
		// §5.2 line 519: the pod is at its maxConcurrentSessions bound.
		c.recordSlotConflict(req.Pool)
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reserve slot via counter on sandbox %s: %w", sb.Name, err)
	}

	claim := existing
	if freshPod {
		// §4.6.1 fresh acquisition: CREATE the per-pod claim (the acquisition
		// guard) and write its first `bound` status. An AlreadyExists or
		// Forbidden means a competing replica acquired the pod first — undo
		// the counter reservation and report the conflict so the caller
		// retries on another pod.
		created, err := CreateClaim(ctx, c.Client, c.Namespace, sb.Name, ClaimRequest{
			Pool: req.Pool, SessionID: req.SessionID, TenantID: req.TenantID,
		})
		if err != nil {
			_, _ = c.Counter.Release(ctx, sb.Name)
			if apierrors.IsAlreadyExists(err) || apierrors.IsForbidden(err) {
				return nil, true, nil
			}
			return nil, false, err
		}
		if err := writeBoundStatus(ctx, c.Client, c.Namespace, created.Name, c.now); err != nil {
			_, _ = c.Counter.Release(ctx, sb.Name)
			_ = c.Client.Delete(ctx, created)
			return nil, false, err
		}
		claim = created
	}

	tenantID := req.TenantID
	if claim != nil && claim.Spec.TenantID != "" {
		tenantID = claim.Spec.TenantID
	}

	// §17.2 item 5 / §5.2 line 392: stamp the tenant pin on the agent pod so
	// the pod-scoped lenny-tenant-label-immutability webhook binds where the
	// §13.2 NET-003 NetworkPolicies select. Best-effort: a missing pod is
	// tolerated and the next assignment re-stamps it.
	if err := stampPodTenant(ctx, c.Client, sb.Namespace, sb.Name, tenantID); err != nil {
		if freshPod {
			_, _ = c.Counter.Release(ctx, sb.Name)
			_ = c.Client.Delete(ctx, claim)
		}
		return nil, false, fmt.Errorf("label pod %s with tenant: %w", sb.Name, err)
	}

	// §4.6.1/§4.6.3: deliver the pool's recycle.maxPodUptimeSeconds cap onto the
	// agent pod so the WarmPoolController's reconcileUptime arm can level-trigger
	// the CreationTimestamp-derived uptime drain. The cap lives only in the
	// gateway poolstore (absent from every CRD the controller reconciles), so the
	// gateway delivers it as the AnnotationMaxPodUptimeSeconds annotation the way
	// it delivers the tenant pin, alongside the same slot claim. A non-positive
	// cap (a pool that sets no maxPodUptimeSeconds) stamps nothing, matching the
	// field's optional status. Best-effort: a missing pod is tolerated and the
	// next assignment re-stamps it. Without this write the annotation is never
	// present in production, so the controller's uptime check stays disabled and
	// the §5.2 maxPodUptimeSeconds drain never fires.
	if err := StampMaxPodUptime(ctx, c.Client, sb.Namespace, sb.Name, req.MaxPodUptimeSeconds); err != nil {
		if freshPod {
			_, _ = c.Counter.Release(ctx, sb.Name)
			_ = c.Client.Delete(ctx, claim)
		}
		return nil, false, fmt.Errorf("stamp max-pod-uptime on pod %s: %w", sb.Name, err)
	}

	return &SlotResult{
		SandboxName: sb.Name,
		SlotID:      req.SessionID,
		Claim:       claim,
		FreshPod:    freshPod,
		ActiveSlots: newCount,
	}, false, nil
}

// ReleaseSlot releases a concurrent-session slot when its session ends or
// fails. It decrements the pod's §5.2 Redis slot counter and, when the last
// slot drains (the counter reaches zero), disposes of the per-pod SandboxClaim
// per the §3.4 recycle disposition. While other slots remain the claim is
// left in place: the per-pod claim spans the whole occupancy episode, and
// the session-to-pod binding the released session held is cleared on its
// Postgres session row by the session server. The gateway does not write
// Sandbox.status; the WarmPoolController projects the pod's occupancy phase
// from claim existence and binding state (§4.6.1, §6.41).
//
// recycle selects the occupancy-zero disposition for a recycling pool (the
// §3.1 "Concurrent" preset, maxConcurrentSessions > 1 with recycle.enabled:
// true) on a clean terminal: the last-slot-drain edge patches the per-pod
// claim bound → recycling (WriteRecyclingStatus) rather than deleting it, so
// the adapter runs the whole-pod scrub when occupancy reaches zero and its
// §4.7 ReportPodScrub drives the recycle-vs-retire disposition. This is the
// concurrent-session counterpart of the session-mode Binder.Release recycle
// branch and closes the §3.1 concurrent-workspace residue gap (shared /tmp,
// /dev/shm, surviving processes across cohorts). When recycle is false (a
// non-recycling "Bounded cohort" pool, or a failed/crashed concurrent session
// the caller maps to the drain path) the claim is deleted directly so the
// §4.6.1 occupancy projection moves the pod claimed → draining → terminated.
//
// The counter decrement is clamped at zero so a double release cannot drive
// the count negative; a release that reaches zero disposes of the claim
// idempotently (a NotFound from either the recycling patch or the DELETE is a
// no-op).
//
// leaked reports that the slot's adapter cleanup did not complete cleanly (§6.2
// leaked-slot semantics). A leaked slot's resources are not reclaimed until pod
// termination, so its occupancy must remain counted: ReleaseSlot skips the
// Redis-counter decrement and the occupancy-zero disposition entirely, leaving
// the counter at its current value so the gateway does not over-assign a new
// slot into the leaked slot's unreleased resources. A pod carrying a leaked
// slot is by definition not at occupancy zero, so it takes neither the recycle
// patch nor the claim DELETE; it is retired by the §5.2/§6.2 liveness paths
// (the ceil-threshold drain, the per-release maxSessionsPerPod drain, or the
// WarmPoolController uptime drain) rather than at this release.
//
// recycled reports whether this release crossed the occupancy-zero edge of a
// recycling pool: the last slot released cleanly, the Redis counter reached
// zero, and the per-pod claim was patched bound → recycling. On that edge the
// caller (Binder.ReleaseSlot) sends the adapter the whole-pod recycle Shutdown
// that triggers the §5.2 scrub, so the signal is threaded back rather than kept
// internal. It is false on every other release (a sibling slot remains, the
// slot leaked, the pool does not recycle, or the claim had already vanished).
//
// spec: §3.1, §3.4 (occupancy-zero recycle disposition); §6.2 (leaked slot
// remains counted); §6.30/§6.41; §5.2 (whole-pod scrub trigger, threaded to the
// binder via recycled).
func (c *SlotClaimer) ReleaseSlot(ctx context.Context, sandboxName, sessionID string, recycle, leaked bool) (recycled bool, err error) {
	if c.Counter == nil {
		// The Redis counter (with its §12.4 Postgres fallback) is the only
		// intra-pod occupancy record now that the gateway does not mirror the
		// slot count onto Sandbox.status. Without it, release cannot tell
		// whether sibling slots remain, so deleting the per-pod claim would
		// over-release a pod that still hosts live slots. Fail closed, the same
		// posture ClaimSlot takes on a nil counter. spec: §12.4 (every
		// Redis-backed role has a durable fallback; the gate fails closed when
		// it cannot decide).
		return false, errors.New("podclaim: slot counter is required for concurrent-session slot release")
	}
	if leaked {
		// §6.2: a leaked slot remains counted in the pod's Redis slot-counter
		// occupancy until pod termination, so the gateway does not over-assign
		// a new slot into its unreleased resources. Skip the decrement and the
		// occupancy-zero disposition: the slot's occupancy stays, and the pod is
		// not at occupancy zero while the leak persists.
		return false, nil
	}
	// §5.2 atomic slot release. The Redis counter is the source of truth —
	// DECR clamped at zero.
	remaining, err := c.Counter.Release(ctx, sandboxName)
	if err != nil {
		return false, fmt.Errorf("podclaim: release slot via counter on sandbox %s: %w", sandboxName, err)
	}

	if remaining > 0 {
		// Sibling slots remain on the pod; the per-pod claim stays.
		return false, nil
	}

	if recycle {
		// §3.4 recycle disposition on the occupancy-zero edge of a recycling
		// concurrent pool: patch the per-pod claim bound → recycling so the
		// claim is in `recycling` before any §4.7 ReportPodScrub arrives (the
		// claim state machine admits recycling → reserved/released/failed but
		// not bound → reserved, §3.2). The adapter runs the whole-pod scrub on
		// occupancy zero and the ReportPodScrub disposition drives recycle vs.
		// retire off the `recycling` binding state. A claim that vanished (a
		// concurrent retirement, the §4.6.1 orphan GC reclaimed it) is a no-op:
		// there is nothing left to recycle. spec: §3.1, §3.4, §6.41.
		if err := WriteRecyclingStatus(ctx, c.Client, c.Namespace, ClaimName(sandboxName), c.now); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		// §3.4: arm the gateway-side missing-report timeout now that the claim
		// is `recycling`. The adapter's ReportPodScrub cancels it; if no report
		// arrives within cleanupTimeoutSeconds plus a grace the coordinator
		// retires the pod rather than leaving it stuck in `recycling` until the
		// much longer §4.6.1 orphan-GC window.
		if c.RecycleBoundary != nil {
			c.RecycleBoundary.OnRecycling(sandboxName)
		}
		// §5.2: the claim is now `recycling` on a true occupancy-zero edge (every
		// slot released cleanly, so no leaked slot holds occupancy). Signal the
		// binder to send the adapter the whole-pod recycle Shutdown that triggers
		// the scrub whose ReportPodScrub cancels the armed timeout.
		return true, nil
	}

	// Last slot drained on a non-recycling pool, or a failed/crashed session:
	// delete the per-pod occupancy claim so the pod retires. The §4.6.1
	// occupancy projection moves the pod from claimed to draining on the
	// claim DELETE.
	return false, DeleteClaim(ctx, c.Client, c.Namespace, sandboxName)
}
