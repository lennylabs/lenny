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
