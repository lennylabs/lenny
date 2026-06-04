// SPDX-License-Identifier: MIT

// Package podclaim holds the gateway-side pod-claim path (§4.6.1,
// ADR-007). To run a session the gateway claims an idle Sandbox: it
// flips the Sandbox phase idle → claimed with an optimistic-locking
// guard and creates the binding SandboxClaim. When two gateway
// replicas race for the same pod the API server rejects the loser's
// status update with a conflict, and the loser moves to the next idle
// pod. The lenny-sandboxclaim-guard admission webhook backstops the
// same single-claim invariant.
package podclaim

import (
	"context"
	"errors"
	"fmt"

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
	// SessionID is the §15.1 session the claim serves.
	SessionID string
	// TenantID is the tenant that owns the session.
	TenantID string
}

// Claim finds an idle Sandbox in the request's pool, creates the binding
// SandboxClaim (the §4.6.1 authoritative single-claim guard, backed by
// the lenny-sandboxclaim-guard ValidatingAdmissionWebhook), and then
// flips the Sandbox phase idle → claimed via SSA Apply under the
// §4.6.3 gateway field manager + ForceOwnership.
//
// The race protection is the SandboxClaim CREATE: the deterministic
// claim name (claim-<sessionID>) collides when the same session retries,
// and the webhook rejects a CREATE that races a different session for
// the same pod (a non-terminal claim already binds the Sandbox). On a
// CREATE rejection or an AlreadyExists collision, Claim moves to the
// next idle pod. The SSA phase write that follows is observability + a
// §6.2 state-machine signal: it transfers ownership of status.phase from
// the WPC to the gateway for the duration of the session lifecycle, per
// §4.6.3's gateway carve-out.
//
// ErrNoIdlePod is returned when no idle pod can be claimed. spec:
// §4.6.1 ADR-007 (single-claim invariant), §4.6.3 ownership table,
// §5.2 / §6.2 lines 83-94 gateway-driven phase set.
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
		// §4.6.1 CREATE-first: the SandboxClaim CRD is the authoritative
		// single-claim guard. AlreadyExists (the deterministic claim-<sessionID>
		// name) and Forbidden (the lenny-sandboxclaim-guard webhook flagging a
		// non-terminal existing claim) are the two race outcomes that mean
		// "another claim already binds this pod"; either way the gateway has
		// not touched Sandbox.status, so a skip to the next idle pod leaves
		// no cleanup behind. Every other CREATE error (validation, network,
		// internal) is a real failure that propagates to the caller.
		claim, err := CreateClaim(ctx, c.Client, c.Namespace, sb.Name, req)
		if err != nil {
			if apierrors.IsAlreadyExists(err) || apierrors.IsForbidden(err) {
				continue
			}
			return nil, err
		}
		// SSA + ForceOwnership for the §4.6.3 status.phase claim. A failure
		// here means the gateway holds the binding SandboxClaim but failed
		// to advance the phase observability; the §4.6.1 orphan-claim
		// garbage collector (F-4.6.3) reclaims the dangling claim.
		if err := ApplyGatewayPhase(ctx, c.Client, sb, state.Claimed); err != nil {
			_ = c.Client.Delete(ctx, claim)
			return nil, err
		}
		// §5.2 line 392 / §17.2 item 5: a warm-pool pod is labeled with
		// its tenant on first assignment by the gateway so the pod-scoped
		// lenny-tenant-label-immutability webhook backstops the pin at the
		// Kubernetes layer (the §13.2 NET-003 NetworkPolicies select pods,
		// not Sandboxes). A missing pod is tolerated by the helper. F-17.2.3.
		if err := stampPodTenant(ctx, c.Client, c.Namespace, sb.Name, req.TenantID); err != nil {
			return nil, fmt.Errorf("label pod %s with tenant: %w", sb.Name, err)
		}
		return claim, nil
	}
	return nil, ErrNoIdlePod
}

// CreateClaim creates the binding SandboxClaim CRD that links a session
// to the named Sandbox. The §4.6.1 normal claim path and the
// Postgres-backed fallback claim path share this helper so both create
// an identical SandboxClaim: the CRD a fallback claim creates is a real
// object, so the lenny-sandboxclaim-guard admission webhook's
// CREATE-time single-claim check guards the fallback exactly as it
// guards the normal path. The SandboxClaim name is deterministic per
// session, so a repeated claim for the same session collides at CREATE
// rather than duplicating the binding.
func CreateClaim(ctx context.Context, cl client.Client, namespace, sandboxName string, req ClaimRequest) (*lennyv1.SandboxClaim, error) {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName(req.SessionID),
			Namespace: namespace,
		},
		Spec: lennyv1.SandboxClaimSpec{
			SandboxRef: sandboxName,
			SessionID:  req.SessionID,
			TenantID:   req.TenantID,
		},
	}
	if err := cl.Create(ctx, claim); err != nil {
		// The pod is claimed but unbound; the §4.6.1 orphan-claim
		// garbage collection reclaims it.
		return nil, fmt.Errorf("create sandbox claim for %s: %w", sandboxName, err)
	}
	return claim, nil
}

// claimName is the deterministic SandboxClaim name for a session, so a
// repeated claim for the same session collides rather than duplicating.
func claimName(sessionID string) string {
	return "claim-" + sessionID
}
