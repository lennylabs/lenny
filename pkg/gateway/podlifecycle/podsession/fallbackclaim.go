// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// §4.6.1 lenny_pod_claim_fallback_skipped_total reason labels: the two
// fallback preconditions whose failure skips the Postgres-backed claim.
const (
	// FallbackSkipReasonMirrorStale is recorded when the agent_pod_state
	// mirror lag exceeds the freshness precondition (precondition 1).
	FallbackSkipReasonMirrorStale = "mirror_stale"
	// FallbackSkipReasonAPIServerUnreachable is recorded when the API
	// server readiness probe fails (precondition 2).
	FallbackSkipReasonAPIServerUnreachable = "apiserver_unreachable"
)

// DefaultFallbackMaxMirrorLagSeconds is the §4.6.1
// podClaimFallbackMaxMirrorLagSeconds default: the fallback claim runs
// only when the mirror is no more than this many seconds stale.
const DefaultFallbackMaxMirrorLagSeconds = 10

// fallbackClaim runs the §4.6.1 Postgres-backed fallback claim after
// the Kubernetes-API claim returned podclaim.ErrNoIdlePod. It returns
// the claimed Sandbox name, or podclaim.ErrNoIdlePod when the fallback
// is disabled, the mirror is too stale to trust, or the mirror also
// has no idle pod (the warm pool is genuinely exhausted, which the
// caller surfaces as WARM_POOL_EXHAUSTED).
//
// The fallback claims an agent_pod_state row, then reproduces the
// authoritative side of a claim: it creates the binding SandboxClaim
// CRD (so the lenny-sandboxclaim-guard webhook's CREATE-time check
// still guards against a double-claim), re-reads the Sandbox to confirm
// it is still idle (the WPC may have drained the pod between the mirror
// snapshot and the CRD lookup), and flips the Sandbox CRD phase idle →
// claimed via SSA Apply under the §4.6.3 gateway field manager +
// ForceOwnership. If the live Sandbox is past idle the just-created
// SandboxClaim is deleted and ErrNoIdlePod is returned so the caller
// surfaces warm-pool exhaustion rather than binding a session to a
// draining pod.
func (b *Binder) fallbackClaim(ctx context.Context, req podclaim.ClaimRequest) (string, error) {
	if b.Fallback == nil {
		// No Postgres mirror is configured; the no-idle-pod result stands.
		// This is the absence of a fallback path, not a skip of a
		// configured one, so it is not counted.
		return "", podclaim.ErrNoIdlePod
	}

	// Freshness precondition: above podClaimFallbackMaxMirrorLagSeconds
	// the mirror may still show pods already claimed in etcd but not yet
	// mirrored, so a fallback claim would race the Kubernetes-API claim.
	maxLag := b.FallbackMaxMirrorLagSeconds
	if maxLag == 0 {
		maxLag = DefaultFallbackMaxMirrorLagSeconds
	}
	lag, err := b.Fallback.MirrorLagSeconds(ctx, req.Pool)
	if err != nil {
		return "", fmt.Errorf("podsession: read mirror lag for pool %s: %w", req.Pool, err)
	}
	if lag > maxLag {
		// Precondition 1 (mirror freshness): the mirror is too stale to
		// trust; defer to the no-idle-pod result rather than risk claiming
		// an already-claimed pod.
		b.recordFallbackSkip(FallbackSkipReasonMirrorStale)
		return "", podclaim.ErrNoIdlePod
	}

	// Precondition 2 (admission reachability): the fallback's
	// lenny-sandboxclaim-guard CREATE check traverses the API server, so
	// probe reachability before locking a mirror row. A failed probe
	// means full API-server unavailability (distinct from watch-stream
	// degradation, which the fallback is designed for), so skip rather
	// than waste a SELECT ... FOR UPDATE SKIP LOCKED that would fail on
	// the subsequent CRD CREATE anyway.
	if b.APIServerReachable != nil {
		if err := b.APIServerReachable(ctx); err != nil {
			b.recordFallbackSkip(FallbackSkipReasonAPIServerUnreachable)
			return "", podclaim.ErrNoIdlePod
		}
	}

	pod, claimed, err := b.Fallback.ClaimIdle(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return "", fmt.Errorf("podsession: fallback claim from pool %s: %w", req.Pool, err)
	}
	if !claimed {
		// The mirror also has no idle pod: the warm pool is exhausted.
		return "", podclaim.ErrNoIdlePod
	}

	// Reproduce the authoritative side of a claim. Create the binding
	// SandboxClaim first: the lenny-sandboxclaim-guard webhook rejects
	// the CREATE if the pod is already claimed, which backstops the
	// §4.6.1 single-claim invariant for the fallback path.
	claim, err := podclaim.CreateClaim(ctx, b.Client, b.Namespace, pod.PodID, req)
	if err != nil {
		return "", err
	}

	// Re-read the Sandbox to detect a mid-fallback drain: the mirror
	// snapshot may pre-date a §6.2 idle → draining transition the WPC
	// applied while we were locking the Postgres row. If the live phase
	// has moved past idle, delete the orphan claim and surface the
	// no-idle-pod result rather than binding a session to a doomed pod.
	// spec: §4.6.1 fallback claim consistency; §6.2.
	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: pod.PodID}, &sb); err != nil {
		_ = b.Client.Delete(ctx, claim)
		return "", fmt.Errorf("podsession: get sandbox %s for fallback claim: %w", pod.PodID, err)
	}
	if sb.Status.Phase != string(state.Idle) {
		_ = b.Client.Delete(ctx, claim)
		return "", podclaim.ErrNoIdlePod
	}
	// §4.6.1: the per-pod claim is CREATEd with spec only; write its first
	// `bound` binding state with a subsequent status patch, the same first
	// status the in-cluster Claimer writes. The gateway does not write
	// Sandbox.status; the WarmPoolController projects the pod's occupancy
	// phase from the claim binding state.
	if err := podclaim.WriteBoundStatus(ctx, b.Client, b.Namespace, claim.Name); err != nil {
		_ = b.Client.Delete(ctx, claim)
		return "", err
	}
	return pod.PodID, nil
}

// recordFallbackSkip increments the §4.6.1
// lenny_pod_claim_fallback_skipped_total counter for reason when a
// counter hook is wired. A nil hook is a no-op.
func (b *Binder) recordFallbackSkip(reason string) {
	if b.FallbackSkipped != nil {
		b.FallbackSkipped(reason)
	}
}
