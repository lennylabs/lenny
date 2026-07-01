// SPDX-License-Identifier: MIT

// Package podclaim holds the gateway-side pod-claim path (§4.6.1,
// ADR-007). To run a session the gateway acquires an idle Sandbox pod by
// creating a per-pod SandboxClaim with the deterministic name
// `claim-<podName>`. Exactly one gateway replica's CREATE wins; the
// others receive an AlreadyExists conflict (or a Forbidden from the
// lenny-sandboxclaim-guard webhook) and move to the next idle pod. The
// gateway does not write Sandbox.status: the WarmPoolController projects
// the pod's occupancy phase from the claim's binding state (§4.6.1
// occupancy projection). The session-to-pod binding is recorded on the
// Postgres session row's pod_assignment column by the session server, so
// the claim carries no session identifier.
package podclaim

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// ErrNoIdlePod reports that the pool has no claimable idle Sandbox. The
// caller retries against a refreshed pool view or surfaces warm-pool
// exhaustion to the client.
var ErrNoIdlePod = errors.New("podclaim: pool has no idle pod")

// Claimer binds a session to an idle Sandbox.
type Claimer struct {
	// Client is the controller-runtime client addressing the cluster.
	Client client.Client
	// Namespace is the agent namespace the pool's Sandboxes live in.
	Namespace string
	// Now supplies the wall clock for the §3.2 reserved-hold-window check on
	// the rebind branch. Nil uses time.Now.
	Now func() time.Time
	// OnRebind is invoked with the rebound pod's identifier after a successful
	// §3.2 acquisition-path rebind, so the gateway can cancel the holding
	// replica's local hold-TTL timer. The rebind patch already changed the
	// claim resourceVersion, so a missed cancellation only costs a no-op
	// aborted expiry DELETE; the callback is an optimization. Nil is a no-op.
	OnRebind func(podID string)
}

// now resolves the injected clock, defaulting to wall time.
func (c *Claimer) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// ClaimRequest identifies the session a pod is claimed for.
type ClaimRequest struct {
	// Pool is the SandboxWarmPool to claim a pod from.
	Pool string
	// SessionID is the §15.1 session the claim serves. It is recorded on
	// the Postgres session row's pod_assignment column by the session
	// server; the per-pod SandboxClaim carries no session identifier.
	SessionID string
	// TenantID is the tenant that owns the session.
	TenantID string
}

// Claim acquires an idle Sandbox pod in the request's pool by creating the
// per-pod occupancy SandboxClaim (§4.6.1, the authoritative single-claim
// guard backed by the lenny-sandboxclaim-guard ValidatingAdmissionWebhook)
// and then writing the claim's first `bound` binding-state status patch.
//
// The race protection is the SandboxClaim CREATE under the deterministic
// name claim-<podName>: a second CREATE for the same pod collides on
// AlreadyExists, and the webhook rejects a CREATE that races a different
// claim for the same pod. On a CREATE rejection or an AlreadyExists
// collision, Claim moves to the next idle pod. The gateway does not write
// Sandbox.status; the WarmPoolController projects the pod's occupancy
// phase from the claim binding state (§4.6.1 occupancy projection).
//
// ErrNoIdlePod is returned when no idle pod can be claimed. spec:
// §4.6.1 (pod claim mechanism, occupancy projection), §4.6.3 (ownership
// decomposition), §5.2 (slot assignment atomicity).
func (c *Claimer) Claim(ctx context.Context, req ClaimRequest) (retClaim *lennyv1.SandboxClaim, retErr error) {
	// spec: §16.3 line 337 — the session.claim_pod span. The spec marks
	// the span "Controller"; in this implementation the idle-pod claim is
	// performed gateway-side (this package), so the span is emitted here.
	// The deferred RecordError captures every claim-failure return path.
	ctx, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanSessionClaimPod)
	span.SetAttributes(attribute.String("pool", req.Pool))
	defer func() {
		tracing.RecordError(span, retErr)
		span.End()
	}()

	var list lennyv1.SandboxList
	if err := c.Client.List(ctx, &list,
		client.InNamespace(c.Namespace),
		client.MatchingLabels{warmpool.LabelPool: req.Pool}); err != nil {
		return nil, fmt.Errorf("list sandboxes for pool %s: %w", req.Pool, err)
	}

	// §3.2 acquisition-path rebind branch: before acquiring a fresh idle pod,
	// dispatch onto a pod the same tenant already holds in `reserved` within
	// its hold window. A reserved pod is scrubbed (and, on preConnect pools,
	// SDK-warm), so a same-tenant session rebinds it (`reserved → bound`) with
	// no acquisition round trip. Any gateway replica may rebind; the rebinding
	// replica re-reads the claim after its patch before dispatching. spec:
	// §3.2, §4.6.1 (within-hold rebind).
	if claim, rebound, err := c.rebindReserved(ctx, &list, req); err != nil {
		return nil, err
	} else if rebound {
		return claim, nil
	}

	for i := range list.Items {
		sb := &list.Items[i]
		if sb.Status.Phase != string(state.Idle) {
			continue
		}
		// §4.6.1 CREATE-first: the per-pod SandboxClaim is the
		// authoritative single-claim guard. AlreadyExists (the deterministic
		// claim-<podName> name) and Forbidden (the lenny-sandboxclaim-guard
		// webhook flagging an existing claim for this pod) are the two race
		// outcomes that mean "another claim already binds this pod"; either
		// way a skip to the next idle pod leaves no cleanup behind. Every
		// other CREATE error (validation, network, internal) propagates.
		claim, err := CreateClaim(ctx, c.Client, c.Namespace, sb.Name, req)
		if err != nil {
			if apierrors.IsAlreadyExists(err) || apierrors.IsForbidden(err) {
				continue
			}
			return nil, err
		}
		// §4.6.1: the claim is created with spec only; write the first
		// `bound` binding state with a subsequent status patch (the status
		// subresource is not writable by the Create call). A failure here
		// leaves the claim with empty status, which the §4.6.1 orphan GC
		// reclaims by its CREATE-before-status creation-timestamp predicate.
		if err := writeBoundStatus(ctx, c.Client, c.Namespace, claim.Name, time.Now); err != nil {
			_ = c.Client.Delete(ctx, claim)
			return nil, err
		}
		// §5.2 / §17.2 item 5: a warm-pool pod is labeled with its tenant
		// on first assignment by the gateway so the pod-scoped
		// lenny-tenant-label-immutability webhook backstops the pin at the
		// Kubernetes layer (the §13.2 NET-003 NetworkPolicies select pods,
		// not Sandboxes). A missing pod is tolerated by the helper.
		if err := stampPodTenant(ctx, c.Client, c.Namespace, sb.Name, req.TenantID); err != nil {
			return nil, fmt.Errorf("label pod %s with tenant: %w", sb.Name, err)
		}
		return claim, nil
	}
	return nil, ErrNoIdlePod
}

// rebindReserved implements the §3.2 acquisition-path rebind: it finds a pod
// the request's tenant holds in `reserved` within its hold window and
// dispatches the session onto it by patching the claim `reserved → bound`,
// returning the rebound claim. rebound is false when no eligible reserved pod
// exists, in which case the caller falls through to normal idle acquisition.
//
// Eligibility: the per-pod claim is in the `reserved` binding state, pinned
// to the request tenant (a reserved pod is pinned for its pinned tenant
// alone), and its holdExpiresAt is still in the future. A claim whose hold
// has already expired is left to the holder's expiry DELETE or the orphan GC;
// rebinding an expired hold would race the DELETE for no benefit.
//
// The rebind patch (WriteRebindStatus) changes the claim resourceVersion, so
// the holder's precondition-guarded hold-expiry DELETE fenced on the
// reserved-patch version fails and aborts (§3.2 rebind-vs-hold-expiry race).
// A WriteRebindStatus that loses to a concurrent expiry DELETE (the claim
// vanished) is tolerated as not-rebound so the caller falls through to normal
// acquisition. After a successful patch the claim is re-read so the returned
// object reflects the post-rebind state the caller dispatches against, per
// §3.2 ("the rebinding replica re-reads the claim after its patch before
// dispatching").
//
// spec: §3.2 (within-hold rebind, any replica may rebind, re-read after
// patch), §4.6.1 (reserved hold, holdExpiresAt), §4.6.3 (reserved → bound).
func (c *Claimer) rebindReserved(ctx context.Context, list *lennyv1.SandboxList, req ClaimRequest) (*lennyv1.SandboxClaim, bool, error) {
	now := c.now()
	for i := range list.Items {
		sb := &list.Items[i]
		if sb.Status.Phase != string(state.Reserved) {
			continue
		}
		claim, found, err := c.reservedClaimForTenant(ctx, sb.Name, req.TenantID, now)
		if err != nil {
			return nil, false, err
		}
		if !found {
			continue
		}
		if err := WriteRebindStatus(ctx, c.Client, c.Namespace, claim.Name, c.now); err != nil {
			if apierrors.IsNotFound(err) {
				// A concurrent hold-expiry DELETE reclaimed the claim before the
				// rebind patch landed; the pod is returning to idle. Fall through
				// to normal acquisition rather than treating it as an error.
				continue
			}
			return nil, false, fmt.Errorf("podclaim: rebind reserved claim %s: %w", claim.Name, err)
		}
		// §3.2: re-read the claim after the rebind patch so the dispatched
		// object reflects the post-rebind `bound` state and resourceVersion.
		rebound, reErr := c.claimByName(ctx, claim.Name)
		if reErr != nil {
			if apierrors.IsNotFound(reErr) {
				// The claim vanished between the rebind patch and the re-read
				// (a racing DELETE that lost its precondition would not delete,
				// so this is the orphan GC or a manual delete); fall through.
				continue
			}
			return nil, false, fmt.Errorf("podclaim: re-read rebound claim %s: %w", claim.Name, reErr)
		}
		// Cancel the holding replica's local hold-TTL timer (a no-op on a
		// peer-held claim). The precondition guard already aborts a stale
		// expiry DELETE, so this only avoids a wasted API call. spec: §3.2.
		if c.OnRebind != nil {
			c.OnRebind(sb.Name)
		}
		return rebound, true, nil
	}
	return nil, false, nil
}

// reservedClaimForTenant reads the per-pod claim for sandboxName and returns
// it when it is a `reserved` claim pinned to tenantID with a hold window that
// has not yet expired. found is false otherwise: no claim, a non-reserved
// binding state (a racing rebind or expiry already moved it), a different
// tenant pin, or an already-expired hold. The Sandbox phase having read
// `reserved` is only a hint; the claim binding state is authoritative, so the
// claim is re-checked here rather than trusted from the projection.
func (c *Claimer) reservedClaimForTenant(ctx context.Context, sandboxName, tenantID string, now time.Time) (*lennyv1.SandboxClaim, bool, error) {
	var claim lennyv1.SandboxClaim
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: ClaimName(sandboxName)}, &claim); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("podclaim: get reserved claim for sandbox %s: %w", sandboxName, err)
	}
	if claim.Status.Phase != string(claimstate.Reserved) {
		return nil, false, nil
	}
	if claim.Spec.TenantID != tenantID {
		// A reserved pod is held for its pinned tenant alone; never rebind it
		// to a different tenant. spec: §3.2, §5.2 (tenant pinning).
		return nil, false, nil
	}
	if claim.Status.HoldExpiresAt == nil || !claim.Status.HoldExpiresAt.Time.After(now) {
		// The hold has expired (or carries no deadline); leave it to the
		// holder's expiry DELETE or the orphan GC. spec: §3.2.
		return nil, false, nil
	}
	return &claim, true, nil
}

// claimByName re-reads a SandboxClaim by its claim name. It backs the §3.2
// post-rebind re-read so the caller dispatches against the current object.
func (c *Claimer) claimByName(ctx context.Context, claimName string) (*lennyv1.SandboxClaim, error) {
	var claim lennyv1.SandboxClaim
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: claimName}, &claim); err != nil {
		return nil, err
	}
	return &claim, nil
}

// CreateClaim creates the per-pod occupancy SandboxClaim (§4.6.1) that
// records the gateway's acquisition of the named Sandbox pod. The §4.6.1
// normal claim path and the Postgres-backed fallback claim path share this
// helper so both create an identical SandboxClaim: the CRD a fallback claim
// creates is a real object, so the lenny-sandboxclaim-guard admission
// webhook's CREATE-time per-pod-uniqueness check guards the fallback
// exactly as it guards the normal path. The claim name is deterministic
// per pod (claim-<podName>), so a second CREATE for the same pod collides
// at CREATE rather than duplicating the binding. The claim is created with
// spec only; the gateway writes the first binding state with a subsequent
// status patch (see writeBoundStatus).
func CreateClaim(ctx context.Context, cl client.Client, namespace, sandboxName string, req ClaimRequest) (*lennyv1.SandboxClaim, error) {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName(sandboxName),
			Namespace: namespace,
		},
		Spec: lennyv1.SandboxClaimSpec{
			// The per-pod occupancy claim (§4.6.3) carries only sandboxRef
			// and tenantId; the session-to-pod binding lives on the Postgres
			// session row's pod_assignment column.
			SandboxRef: sandboxName,
			TenantID:   req.TenantID,
		},
	}
	if err := cl.Create(ctx, claim); err != nil {
		// The pod is acquired but its binding state is unset; the §4.6.1
		// orphan-claim garbage collection reclaims it on the
		// CREATE-before-status predicate.
		return nil, fmt.Errorf("create sandbox claim for %s: %w", sandboxName, err)
	}
	return claim, nil
}

// DeleteClaim deletes the per-pod occupancy SandboxClaim for podName. It is
// the gateway's only action when a session-mode occupancy episode ends: the
// gateway never writes Sandbox.status (§4.6.3 ownership decomposition), so it
// releases the pod by deleting the claim and the WarmPoolController projects
// the resulting occupancy phase (a claim deleted on a `recycle.enabled:
// false` pod projects `draining` then `terminated`; on a recycling pod under
// its limits it projects `idle`, §4.6.1 occupancy projection). The delete is
// idempotent: a missing claim is a no-op so a double release or a claim the
// orphan GC already collected does not error.
//
// spec: §4.6.1 (occupancy projection on claim DELETE); §4.6.3 (gateway is
// not a writer of Sandbox.status; releases via the claim lifecycle).
func DeleteClaim(ctx context.Context, cl client.Client, namespace, podName string) error {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName(podName),
			Namespace: namespace,
		},
	}
	if err := cl.Delete(ctx, claim); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("podclaim: delete per-pod claim for sandbox %s: %w", podName, err)
	}
	return nil
}

// claimName is the deterministic SandboxClaim name for a pod, so a second
// claim for the same pod collides at CREATE rather than duplicating. The
// per-pod name (claim-<podName>) replaces the former per-session name now
// that the SandboxClaim is a per-pod occupancy claim (§4.6.1).
func claimName(podName string) string {
	return ClaimName(podName)
}

// ClaimName is the exported §4.6.1 deterministic SandboxClaim name for a
// pod (`claim-<podName>`). The §4.7 recycle disposition driver resolves a
// pod's claim by this name to patch its binding state, so the mapping lives
// in one place rather than being re-derived by every caller.
func ClaimName(podName string) string {
	return "claim-" + podName
}
