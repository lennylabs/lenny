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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
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

// Claim finds an idle Sandbox in the request's pool, flips it to the
// claimed phase under an optimistic-locking guard, and creates the
// binding SandboxClaim. A status-update conflict means a competing
// gateway replica won that pod, so Claim moves to the next idle pod.
// ErrNoIdlePod is returned when no idle pod can be claimed.
func (c *Claimer) Claim(ctx context.Context, req ClaimRequest) (*lennyv1.SandboxClaim, error) {
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
		sb.Status.Phase = string(state.Claimed)
		if err := c.Client.Status().Update(ctx, sb); err != nil {
			if apierrors.IsConflict(err) {
				// A competing replica claimed this pod first.
				continue
			}
			return nil, fmt.Errorf("claim sandbox %s: %w", sb.Name, err)
		}

		claim := &lennyv1.SandboxClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      claimName(req.SessionID),
				Namespace: c.Namespace,
			},
			Spec: lennyv1.SandboxClaimSpec{
				SandboxRef: sb.Name,
				SessionID:  req.SessionID,
				TenantID:   req.TenantID,
			},
		}
		if err := c.Client.Create(ctx, claim); err != nil {
			// The pod is claimed but unbound; the §4.6.1 orphan-claim
			// garbage collection reclaims it.
			return nil, fmt.Errorf("create sandbox claim for %s: %w", sb.Name, err)
		}
		return claim, nil
	}
	return nil, ErrNoIdlePod
}

// claimName is the deterministic SandboxClaim name for a session, so a
// repeated claim for the same session collides rather than duplicating.
func claimName(sessionID string) string {
	return "claim-" + sessionID
}
