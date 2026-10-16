// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// RecycleBoundaryArmer arms the §5.2 missing-report timeout for a pod at the
// bound → recycling patch. *recycle.RecycleBoundaryCoordinator satisfies it
// through OnRecycling; the interface is defined at this consumer so podsession
// does not import the recycle package. spec: §4.7 (gateway-side missing-report
// timeout armed at session termination).
type RecycleBoundaryArmer interface {
	OnRecycling(podID string)
}

// dispositionFailed is the disposition string Release treats as the §6.2
// "ends in failure or a crash" terminal that always retires the pod
// regardless of recycle settings. It is the first of the two retire
// conditions; the second is a binding marked crashed (BindResult.Crashed),
// whose attach stream ended in a reported stream failure before the release
// unbound it. Every other terminal (completed, cancelled, expired) recycles
// on a recycling pool. spec: §6.2 (a session that ends in failure or a crash
// always retires its pod); §28.5.1 (Gateway-to-pod).
const dispositionFailed = "failed"

// Release tears down a session that Bind placed on a pod: it returns the
// binding's §4.9 credential leases (releasePodCredentials: the leases the
// binding's attempt minted and the session's leases at or below the binding's
// stamp), then applies the §5.2 disposition by either
// recycling the pod (patch the claim bound → recycling, then signal the
// whole-pod scrub) or draining it (signal the adapter shutdown, then delete the
// claim). disposition is the session-terminal outcome the session reached
// (completed, failed, cancelled, or expired); the fine session-terminal states
// and the Terminated session-condition fact are recorded on the Postgres
// session model (§7.2 / §8.8), so Release writes no Sandbox.status field — the
// WarmPoolController is the sole writer of Sandbox.status (§4.6.3).
//
// On a recycling pool (BindResult.Recycle, maxConcurrentSessions: 1) a clean
// terminal (anything but "failed") on a binding not marked crashed brings
// occupancy to zero. The §4.7 recycle
// disposition orders the two steps patch-then-scrub: Release first patches the
// per-pod claim bound → recycling (podclaim.WriteRecyclingStatus), then signals
// the adapter so the whole-pod scrub runs while the pod projects `claimed`. The
// claim must be in `recycling` before any §4.7 ReportPodScrub can arrive,
// because the claim state machine admits only recycling → reserved/released/failed,
// not bound → reserved (§4.6.3); the ReportPodScrub report then drives the
// recycle-vs-retire disposition off the `recycling` binding state. A
// non-recycling pool, a failed session, or a binding marked crashed takes the
// drain path, whatever disposition the release passes: the
// adapter is signaled to tear the session down and the per-pod claim is deleted,
// which the WarmPoolController projects as draining → terminated (§4.6.1). The
// recycling patch is durable and ordered first; the adapter Shutdown is
// best-effort — a coordinating-gateway crash after the patch leaves the claim in
// `recycling` and the §4.6.1 orphan GC drains the stuck pod — so on the recycle
// path Release returns only an error from the recycling patch. spec: §4.6.1,
// §4.7 (recycle on occupancy-zero, patch-then-scrub ordering); §4.6.1; §4.6.3;
// §7.2 / §8.8.
func (b *Binder) Release(ctx context.Context, result *BindResult, disposition string) error {
	// spec: §7.3 (Credential leases on the replacement pod) — return the
	// binding's leases before the disposition, so the credential's
	// active-session counter is decremented on the way out. The release is
	// scoped to the binding's stamp, so a lease a later restore of the
	// session minted survives a stale replica's release.
	b.releasePodCredentials(result)

	// spec: §4.7 / §6.2 — a recycling pool recycles
	// the pod across whole sessions of the same tenant when occupancy reaches
	// zero after a clean session termination; a failed/crashed session always
	// retires the pod. On the recycle path Release patches the claim
	// bound → recycling FIRST, then signals the adapter scrub: the claim must be
	// in `recycling` before any ReportPodScrub arrives, because the claim state
	// machine admits recycling → reserved/released/failed but not bound →
	// reserved (§4.6.3). On the retire path it tears the session down and deletes
	// the claim so the WarmPoolController drains the pod.
	// spec: §28.5.1 (Gateway-to-pod) — a binding whose attach stream ended in
	// a reported stream failure retires the pod whatever the disposition.
	if result.Recycle && disposition != dispositionFailed && !result.Crashed() {
		if err := podclaim.WriteRecyclingStatus(ctx, b.Client, b.Namespace, podclaim.ClaimName(result.SandboxName), nil); err != nil {
			return fmt.Errorf("podsession: patch claim recycling for sandbox %s: %w", result.SandboxName, err)
		}
		// §4.7: arm the gateway-side missing-report timeout now that the claim
		// is `recycling`. The adapter's ReportPodScrub cancels it; if the report
		// never arrives within cleanupTimeoutSeconds plus a grace, the
		// coordinator retires the pod so a hung adapter does not leave it stuck
		// in `recycling` until the much longer orphan-GC window. Armed before
		// the best-effort Shutdown so a Shutdown that blocks does not delay the
		// timer.
		if b.RecycleBoundary != nil {
			b.RecycleBoundary.OnRecycling(result.SandboxName)
		}
		// The claim now projects `recycling`. This Shutdown carries the §4.7
		// recycle disposition plus the pool's whole-pod scrub parameters: it is
		// the occupancy-zero signal that runs the §5.2 whole-pod scrub the
		// adapter reports via ReportPodScrub, which drives the disposition off
		// the `recycling` binding state. spec: §4.7, §5.2 (whole-pod scrub
		// on the occupancy-zero recycle edge).
		b.shutdownAdapter(ctx, result, true)
		return nil
	}

	// Retire path: a failed session, a binding marked crashed (§6.2), or a
	// non-recycling pool.
	// Send a plain Shutdown (recycle: false) so a failed session on a recycling
	// pool retires its pod rather than triggering the scrub-and-reuse path, then
	// tear the session's processes down and drain the pod.
	b.shutdownAdapter(ctx, result, false)
	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: result.SandboxName}, &sb); err != nil {
		return fmt.Errorf("podsession: get sandbox %s: %w", result.SandboxName, err)
	}
	return b.drain(ctx, &sb)
}

// shutdownAdapter shuts the pod's runtime down through the adapter and closes
// the connection. recycle carries the effective per-release recycle decision
// (Release's `result.Recycle && disposition != dispositionFailed &&
// !result.Crashed()`), passed
// true only from the recycle branch and false from the retire path; it is not
// re-derived from result.Recycle here, because result.Recycle is the
// pool-level recycle.enabled flag and a failed/crashed session on a recycling
// pool, and a crashed binding on one, take the retire path with that flag still
// set. Keying the wire call on
// result.Recycle would send the recycle disposition on that retire path,
// telling the adapter to keep the pod alive and run an async whole-pod scrub
// whose report would race a claim the gateway never patched to recycling, a
// fail-open regression on the crash path.
//
// On the recycle path it sends the §4.7 recycle disposition (ShutdownRecycle)
// carrying the pod identity (SandboxName) and the pool's whole-pod scrub
// parameters, which is the occupancy-zero signal that triggers the §5.2 scrub
// the adapter reports via ReportPodScrub. On the retire path it sends a plain
// Shutdown that tears the session's processes down before the pod drains. The
// Shutdown call is best-effort. A nil Adapter (a BindSlot result or a
// re-resolved release) is a no-op.
//
// spec: §5.2 (whole-pod scrub trigger); §4.7 (Shutdown recycle disposition);
// §6.2 (failed/crashed session always retires).
func (b *Binder) shutdownAdapter(ctx context.Context, result *BindResult, recycle bool) {
	if result.Adapter == nil {
		return
	}
	if recycle {
		_, _ = result.Adapter.ShutdownRecycle(ctx, result.SessionID, adapterclient.RecycleScrub{
			PodID:                 result.SandboxName,
			CleanupCommands:       result.CleanupCommands,
			CleanupTimeoutSeconds: int32(result.CleanupTimeoutSeconds),
		})
	} else {
		_, _ = result.Adapter.Shutdown(ctx, result.SessionID, "", 0)
	}
	result.Adapter.Close()
}
